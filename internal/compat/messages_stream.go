package compat

import (
	"encoding/json"
	"io"

	"costrict-router/internal/ids"
)

// encodeAnthropicStream 把上游 Chat Completions 的 SSE 流转换成 Anthropic Messages 的 SSE 事件流。
func encodeAnthropicStream(r io.Reader, model string) io.Reader {
	return streamPipe(func(write func([]byte) error) error {
		st := &anthropicStreamState{write: write, model: model}
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

	started      bool
	messageID    string
	curKind      string // "", "thinking", "text", "tool"
	curToolIndex int    // 当前打开的 tool_use 对应的上游 tool_calls 下标
	blockOpen    bool
	blockIndex   int // 下一个要分配的 Anthropic content block 序号
	hasToolCalls bool
	stopReason   string

	sawReasoningField bool
	think             thinkSplitter
	errMsg            string

	inputTokens  int
	outputTokens int
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
		"index": s.blockIndex,
	})
	s.blockOpen = false
	s.blockIndex++
	s.curKind = ""
}

func (s *anthropicStreamState) openBlock(kind string, block map[string]any) {
	s.emit("content_block_start", map[string]any{
		"type":          "content_block_start",
		"index":         s.blockIndex,
		"content_block": block,
	})
	s.blockOpen = true
	s.curKind = kind
}

func (s *anthropicStreamState) emitReasoning(text string) {
	if s.curKind != "thinking" {
		s.closeBlock()
		s.openBlock("thinking", map[string]any{"type": "thinking", "thinking": ""})
	}
	s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

func (s *anthropicStreamState) emitText(text string) {
	if s.curKind != "text" {
		s.closeBlock()
		s.openBlock("text", map[string]any{"type": "text", "text": ""})
	}
	s.emit("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
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
		if s.curKind != "tool" || s.curToolIndex != idx {
			s.closeBlock()
			s.curToolIndex = idx
			s.openBlock("tool", map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": map[string]any{},
			})
		}
		if tc.Function.Arguments != "" {
			s.emit("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": s.blockIndex,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
			})
		}
	}

	if choice.FinishReason != nil && *choice.FinishReason != "" {
		s.stopReason = anthropicStopReason(*choice.FinishReason, s.hasToolCalls)
	}
	return nil
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
