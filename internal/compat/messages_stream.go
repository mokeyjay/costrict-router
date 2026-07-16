package compat

import (
	"encoding/json"
	"io"
	"sort"
	"strings"

	"costrict-router/internal/ids"
)

// anthropicKeepalive 是等待上游期间发给客户端的保活事件（Anthropic 官方协议自带 ping 事件）。
var anthropicKeepalive = formatSSE(sseEvent{Event: "ping", Data: `{"type":"ping"}`})

// encodeAnthropicStream 把上游 Chat Completions 的 SSE 流转换成 Anthropic Messages 的 SSE 事件流。
func encodeAnthropicStream(r io.Reader, model string) io.Reader {
	return streamPipe(anthropicKeepalive, func(write func([]byte) error) error {
		st := &anthropicStreamState{write: write, model: model, tools: make(map[int]*anthropicStreamTool)}
		// 立即发出 message_start：上游 gateway 缓冲窗口可达一分钟以上，
		// 提前起始让客户端马上确认流已建立，等待期间由 ping 保活。
		st.ensureStarted(chatChunk{})
		err := scanChatStream(r, func(payload []byte) error {
			if msg, ok := chatStreamError(payload); ok {
				st.errMsg = msg
				return nil
			}
			var chunk chatChunk
			if err := json.Unmarshal(payload, &chunk); err != nil {
				return nil // 跳过无法解析的块
			}
			return st.consume(chunk)
		})
		st.finish()
		return err
	})
}

type anthropicStreamState struct {
	write func([]byte) error
	model string

	started        bool
	messageID      string
	curKind        string // "", "thinking", "text"
	curBlockIndex  int
	nextBlockIndex int
	blockOpen      bool
	tools          map[int]*anthropicStreamTool
	hasToolCalls   bool
	stopReason     string

	sawReasoningField bool
	think             thinkSplitter
	errMsg            string

	inputTokens  int
	outputTokens int
}

type anthropicStreamTool struct {
	blockIndex int
	// Anthropic 流式协议中 tool_use 的 id/name 只随 content_block_start 发一次，
	// 部分上游首个增量只有参数没有 id/name，因此块的发出要推迟到 name 就绪。
	started bool
	closed  bool
	id      string
	name    string
	pending strings.Builder // content_block_start 之前缓冲的参数增量
	gate    jsonObjectGate  // 丢弃第一个完整 JSON 对象之后的重复输出（如 kimi）
}

func (s *anthropicStreamState) emit(event string, data any) {
	_ = s.write(formatSSE(sseEvent{Event: event, Data: string(mustJSON(data))}))
}

func (s *anthropicStreamState) ensureStarted(chunk chatChunk) {
	if s.started {
		return
	}
	s.started = true
	s.messageID = anthropicMessageID(chunk.ID)
	s.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": s.inputTokens, "output_tokens": 0},
		},
	})
}

func (s *anthropicStreamState) closeBlock() {
	if !s.blockOpen {
		return
	}
	s.emit("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.curBlockIndex,
	})
	s.blockOpen = false
	s.curKind = ""
}

func (s *anthropicStreamState) allocateBlockIndex() int {
	index := s.nextBlockIndex
	s.nextBlockIndex++
	return index
}

func (s *anthropicStreamState) openBlock(kind string, block map[string]any) {
	s.curBlockIndex = s.allocateBlockIndex()
	s.emit("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.curBlockIndex,
		"content_block": block,
	})
	s.blockOpen = true
	s.curKind = kind
}

func (s *anthropicStreamState) emitReasoning(text string) {
	if s.curKind != "thinking" {
		s.closeBlock()
		s.closeTools()
		s.openBlock("thinking", map[string]any{"type": "thinking", "thinking": ""})
	}
	s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.curBlockIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

func (s *anthropicStreamState) emitText(text string) {
	if s.curKind != "text" {
		s.closeBlock()
		s.closeTools()
		s.openBlock("text", map[string]any{"type": "text", "text": ""})
	}
	s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.curBlockIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

func (s *anthropicStreamState) consume(chunk chatChunk) error {
	if chunk.Usage != nil {
		if chunk.Usage.PromptTokens > 0 {
			s.inputTokens = chunk.Usage.PromptTokens
		}
		if chunk.Usage.CompletionTokens > 0 {
			s.outputTokens = chunk.Usage.CompletionTokens
		}
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	s.ensureStarted(chunk)
	choice := chunk.Choices[0]
	delta := choice.Delta

	// 1. 思考内容（reasoning_content 字段）
	if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		s.sawReasoningField = true
		s.emitReasoning(*delta.ReasoningContent)
	}

	// 2. 普通文本（可能内联 <think> 标签，需先剥离）
	if delta.Content != nil && *delta.Content != "" {
		if s.sawReasoningField {
			s.emitText(*delta.Content)
		} else {
			reason, text := s.think.push(*delta.Content)
			if reason != "" {
				s.emitReasoning(reason)
			}
			if text != "" {
				s.emitText(text)
			}
		}
	}

	// 3. 工具调用
	for _, tc := range delta.ToolCalls {
		s.hasToolCalls = true
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		tool := s.tools[idx]
		if tool == nil {
			tool = &anthropicStreamTool{}
			s.tools[idx] = tool
		}
		if tool.closed {
			// 该块已随文本/思考切换关闭，Anthropic 协议无法再补增量，只能丢弃。
			continue
		}
		if tc.ID != "" {
			tool.id = tc.ID
		}
		if tc.Function.Name != "" {
			tool.name = tc.Function.Name
		}
		args := tool.gate.filter(tc.Function.Arguments)
		if !tool.started {
			if tool.name == "" {
				// name 未就绪时先缓冲参数，避免发出空 name 的 tool_use 块。
				tool.pending.WriteString(args)
				continue
			}
			s.startToolBlock(tool)
		}
		if args != "" {
			s.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": tool.blockIndex,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": args},
			})
		}
	}

	if choice.FinishReason != nil && *choice.FinishReason != "" {
		s.stopReason = anthropicStopReason(*choice.FinishReason, s.hasToolCalls)
	}
	return nil
}

// startToolBlock 发出 tool_use 的 content_block_start 并刷出此前缓冲的参数增量。
func (s *anthropicStreamState) startToolBlock(tool *anthropicStreamTool) {
	s.closeBlock()
	if tool.id == "" {
		// Anthropic 协议要求 tool_use id 非空；上游没给时补一个，历史回传时全程一致。
		tool.id = "call_" + ids.UUID()
	}
	tool.blockIndex = s.allocateBlockIndex()
	tool.started = true
	s.emit("content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": tool.blockIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    tool.id,
			"name":  tool.name,
			"input": map[string]any{},
		},
	})
	if tool.pending.Len() > 0 {
		s.emit("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": tool.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": tool.pending.String()},
		})
		tool.pending.Reset()
	}
}

func (s *anthropicStreamState) closeTools() {
	// 先把等待 name 期间未发出的工具块补发出来（按上游 index 保序）。
	indexes := make([]int, 0, len(s.tools))
	for idx := range s.tools {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)
	for _, idx := range indexes {
		tool := s.tools[idx]
		if tool.started || tool.closed {
			continue
		}
		if tool.name == "" {
			// 整个流都没等到 name，无法构成合法 tool_use 块，丢弃。
			tool.closed = true
			continue
		}
		s.startToolBlock(tool)
	}
	tools := make([]*anthropicStreamTool, 0, len(s.tools))
	for _, tool := range s.tools {
		if tool.started && !tool.closed {
			tools = append(tools, tool)
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].blockIndex < tools[j].blockIndex })
	for _, tool := range tools {
		s.emit("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": tool.blockIndex,
		})
		// 标记而非移除：迟到的参数增量据此丢弃，不会再凭空开新块。
		tool.closed = true
	}
}

func (s *anthropicStreamState) finish() {
	if !s.started {
		// 上游没有任何可用内容，至少给出一个最小的合法消息骨架。
		s.messageID = "msg_" + ids.UUID()
		s.emit("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            s.messageID,
				"type":          "message",
				"role":          "assistant",
				"model":         s.model,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
			},
		})
	}
	// 上游在 SSE 中返回了错误：发出 Anthropic error 事件，避免静默结束。
	if s.errMsg != "" {
		s.closeBlock()
		s.closeTools()
		s.emit("error", map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "api_error", "message": s.errMsg},
		})
		return
	}
	// 流结束时把内联 <think> 拆分器里残留的内容刷出。
	if !s.sawReasoningField {
		if reason, text := s.think.flush(); reason != "" || text != "" {
			if reason != "" {
				s.emitReasoning(reason)
			}
			if text != "" {
				s.emitText(text)
			}
		}
	}
	s.closeBlock()
	s.closeTools()
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	s.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": s.stopReason, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": s.inputTokens, "output_tokens": s.outputTokens},
	})
	s.emit("message_stop", map[string]any{"type": "message_stop"})
}
