package compat

import (
	"encoding/json"
	"io"
	"strings"
	"time"

	"costrict-router/internal/ids"
)

// encodeResponsesStream 把上游 Chat Completions 的 SSE 流转换成 OpenAI Responses 的语义事件流。
func encodeResponsesStream(r io.Reader, model string) io.Reader {
	return streamPipe(func(write func([]byte) error) error {
		st := &responsesStreamState{write: write, model: model, status: "completed"}
		err := scanChatStream(r, func(payload []byte) error {
			if msg, ok := chatStreamError(payload); ok {
				st.errMsg = msg
				return nil
			}
			var chunk chatChunk
			if err := json.Unmarshal(payload, &chunk); err != nil {
				return nil
			}
			return st.consume(chunk)
		})
		st.finish()
		return err
	})
}

type responsesStreamState struct {
	write func([]byte) error
	seq   sequencer
	model string

	respID  string
	created int64
	started bool

	outputIndex int
	cur         string // "", "reasoning", "text", "tool"
	itemID      string

	textBuf   strings.Builder
	reasonBuf strings.Builder

	toolCallID        string
	toolName          string
	toolArgsBuf       strings.Builder
	toolUpstreamIndex int

	outputs []any
	usage   *chatUsage
	status  string

	sawReasoningField bool
	think             thinkSplitter
	errMsg            string
}

func (s *responsesStreamState) emit(event string, data map[string]any) {
	data["type"] = event
	data["sequence_number"] = s.seq.next()
	_ = s.write(formatSSE(sseEvent{Event: event, Data: string(mustJSON(data))}))
}

func (s *responsesStreamState) responseObject(status string, output []any, usage any) map[string]any {
	if output == nil {
		output = []any{}
	}
	return map[string]any{
		"id":                  s.respID,
		"object":              "response",
		"created_at":          s.created,
		"status":              status,
		"model":               s.model,
		"output":              output,
		"usage":               usage,
		"error":               nil,
		"metadata":            map[string]any{},
		"parallel_tool_calls": true,
	}
}

func (s *responsesStreamState) ensureStarted(chunk chatChunk) {
	if s.started {
		return
	}
	s.started = true
	s.respID = responsesID(chunk.ID)
	s.created = time.Now().Unix()
	s.emit("response.created", map[string]any{"response": s.responseObject("in_progress", nil, nil)})
	s.emit("response.in_progress", map[string]any{"response": s.responseObject("in_progress", nil, nil)})
}

// closeCurrent 关闭当前打开的输出项并把成品追加到 outputs。
func (s *responsesStreamState) closeCurrent() {
	switch s.cur {
	case "reasoning":
		text := s.reasonBuf.String()
		s.emit("response.reasoning_summary_text.done", map[string]any{
			"item_id": s.itemID, "output_index": s.outputIndex, "summary_index": 0, "text": text,
		})
		s.emit("response.reasoning_summary_part.done", map[string]any{
			"item_id": s.itemID, "output_index": s.outputIndex, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": text},
		})
		item := map[string]any{
			"type": "reasoning", "id": s.itemID,
			"summary": []any{map[string]any{"type": "summary_text", "text": text}},
		}
		s.emit("response.output_item.done", map[string]any{"output_index": s.outputIndex, "item": item})
		s.outputs = append(s.outputs, item)
		s.outputIndex++
		s.reasonBuf.Reset()
	case "text":
		text := s.textBuf.String()
		s.emit("response.output_text.done", map[string]any{
			"item_id": s.itemID, "output_index": s.outputIndex, "content_index": 0, "text": text,
		})
		part := map[string]any{"type": "output_text", "text": text, "annotations": []any{}}
		s.emit("response.content_part.done", map[string]any{
			"item_id": s.itemID, "output_index": s.outputIndex, "content_index": 0, "part": part,
		})
		item := map[string]any{
			"type": "message", "id": s.itemID, "status": "completed", "role": "assistant",
			"content": []any{part},
		}
		s.emit("response.output_item.done", map[string]any{"output_index": s.outputIndex, "item": item})
		s.outputs = append(s.outputs, item)
		s.outputIndex++
		s.textBuf.Reset()
	case "tool":
		args := s.toolArgsBuf.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		s.emit("response.function_call_arguments.done", map[string]any{
			"item_id": s.itemID, "output_index": s.outputIndex, "arguments": args,
		})
		item := map[string]any{
			"type": "function_call", "id": s.itemID, "call_id": s.toolCallID,
			"name": s.toolName, "arguments": args, "status": "completed",
		}
		s.emit("response.output_item.done", map[string]any{"output_index": s.outputIndex, "item": item})
		s.outputs = append(s.outputs, item)
		s.outputIndex++
		s.toolArgsBuf.Reset()
		s.toolCallID = ""
		s.toolName = ""
	}
	s.cur = ""
}

func (s *responsesStreamState) openText() {
	s.itemID = "msg_" + ids.UUID()
	s.cur = "text"
	s.emit("response.output_item.added", map[string]any{
		"output_index": s.outputIndex,
		"item": map[string]any{
			"type": "message", "id": s.itemID, "status": "in_progress",
			"role": "assistant", "content": []any{},
		},
	})
	s.emit("response.content_part.added", map[string]any{
		"item_id": s.itemID, "output_index": s.outputIndex, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (s *responsesStreamState) openReasoning() {
	s.itemID = "rs_" + ids.UUID()
	s.cur = "reasoning"
	s.emit("response.output_item.added", map[string]any{
		"output_index": s.outputIndex,
		"item":         map[string]any{"type": "reasoning", "id": s.itemID, "summary": []any{}},
	})
	s.emit("response.reasoning_summary_part.added", map[string]any{
		"item_id": s.itemID, "output_index": s.outputIndex, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": ""},
	})
}

func (s *responsesStreamState) openTool(callID, name string) {
	s.itemID = "fc_" + ids.UUID()
	s.cur = "tool"
	s.toolCallID = callID
	s.toolName = name
	s.emit("response.output_item.added", map[string]any{
		"output_index": s.outputIndex,
		"item": map[string]any{
			"type": "function_call", "id": s.itemID, "call_id": callID,
			"name": name, "arguments": "", "status": "in_progress",
		},
	})
}

func (s *responsesStreamState) emitReasoning(text string) {
	if s.cur != "reasoning" {
		s.closeCurrent()
		s.openReasoning()
	}
	s.reasonBuf.WriteString(text)
	s.emit("response.reasoning_summary_text.delta", map[string]any{
		"item_id": s.itemID, "output_index": s.outputIndex, "summary_index": 0,
		"delta": text,
	})
}

func (s *responsesStreamState) emitText(text string) {
	if s.cur != "text" {
		s.closeCurrent()
		s.openText()
	}
	s.textBuf.WriteString(text)
	s.emit("response.output_text.delta", map[string]any{
		"item_id": s.itemID, "output_index": s.outputIndex, "content_index": 0,
		"delta": text,
	})
}

func (s *responsesStreamState) consume(chunk chatChunk) error {
	if chunk.Usage != nil {
		s.usage = chunk.Usage
	}
	if len(chunk.Choices) == 0 {
		return nil
	}
	s.ensureStarted(chunk)
	choice := chunk.Choices[0]
	delta := choice.Delta

	if delta.ReasoningContent != nil && *delta.ReasoningContent != "" {
		s.sawReasoningField = true
		s.emitReasoning(*delta.ReasoningContent)
	}

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

	for _, tc := range delta.ToolCalls {
		idx := 0
		if tc.Index != nil {
			idx = *tc.Index
		}
		if s.cur != "tool" || s.toolUpstreamIndex != idx {
			s.closeCurrent()
			s.toolUpstreamIndex = idx
			s.openTool(tc.ID, tc.Function.Name)
		}
		if tc.Function.Arguments != "" {
			s.toolArgsBuf.WriteString(tc.Function.Arguments)
			s.emit("response.function_call_arguments.delta", map[string]any{
				"item_id": s.itemID, "output_index": s.outputIndex,
				"delta": tc.Function.Arguments,
			})
		}
	}

	if choice.FinishReason != nil && *choice.FinishReason == "length" {
		s.status = "incomplete"
	}
	return nil
}

func (s *responsesStreamState) finish() {
	if !s.started {
		s.respID = "resp_" + ids.UUID()
		s.created = time.Now().Unix()
		s.emit("response.created", map[string]any{"response": s.responseObject("in_progress", nil, nil)})
	}
	// 上游在 SSE 中返回了错误：显式上报 response.failed，避免静默返回空响应。
	if s.errMsg != "" {
		s.closeCurrent()
		final := s.responseObject("failed", s.outputs, responsesUsage(s.usage))
		final["error"] = map[string]any{"code": "upstream_error", "message": s.errMsg}
		s.emit("response.failed", map[string]any{"response": final})
		return
	}
	// 刷出内联 <think> 拆分器残留。
	if !s.sawReasoningField {
		if reason, text := s.think.flush(); reason != "" {
			s.emitReasoning(reason)
		} else if text != "" {
			s.emitText(text)
		}
	}
	s.closeCurrent()
	final := s.responseObject(s.status, s.outputs, responsesUsage(s.usage))
	if s.status == "incomplete" {
		final["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
		s.emit("response.incomplete", map[string]any{"response": final})
		return
	}
	s.emit("response.completed", map[string]any{"response": final})
}
