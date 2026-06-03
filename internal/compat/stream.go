package compat

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

func TransformChatCompletionStreamToResponses(upstream io.Reader, model string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := writeResponsesStream(pw, upstream, model)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

func TransformChatCompletionStreamToAnthropic(upstream io.Reader, model string) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := writeAnthropicStream(pw, upstream, model)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

type responsesToolState struct {
	id          string
	name        string
	args        strings.Builder
	outputIndex int
}

func writeResponsesStream(w io.Writer, upstream io.Reader, model string) error {
	respID := responseID("resp")
	created := time.Now().Unix()
	seq := 0
	emit := func(event string, payload map[string]any) error {
		payload["sequence_number"] = seq
		seq++
		return writeSSE(w, event, payload)
	}

	if err := emit("response.created", map[string]any{"type": "response.created", "response": responsesStreamEnvelope(respID, created, model, "in_progress", nil, nil)}); err != nil {
		return err
	}
	if err := emit("response.in_progress", map[string]any{"type": "response.in_progress", "response": responsesStreamEnvelope(respID, created, model, "in_progress", nil, nil)}); err != nil {
		return err
	}

	reader := bufio.NewReader(upstream)
	textItemID := responseID("msg")
	textStarted := false
	textIndex := 0
	var textBuf strings.Builder
	tools := map[int]*responsesToolState{}
	var toolOrder []int
	nextOutputIndex := 0
	var usage map[string]any
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			payload := strings.TrimSpace(line)
			if strings.HasPrefix(payload, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
				if data == "[DONE]" {
					break
				}
				chunk, ok := decodeSSEJSON(data)
				if !ok {
					continue
				}
				if model == "" {
					model = stringValue(chunk["model"])
				}
				if rawUsage, ok := chunk["usage"]; ok && rawUsage != nil {
					if m := getMap(rawUsage); m != nil {
						usage = chatUsageMapToResponses(m)
					}
				}
				for _, choice := range streamChoices(chunk) {
					delta := getMap(choice["delta"])
					if delta == nil {
						continue
					}
					if content := stringValue(delta["content"]); content != "" {
						if !textStarted {
							textIndex = nextOutputIndex
							nextOutputIndex++
							item := map[string]any{"id": textItemID, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
							if err := emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": textIndex, "item": item}); err != nil {
								return err
							}
							_ = emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": textItemID, "output_index": textIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
							textStarted = true
						}
						textBuf.WriteString(content)
						if err := emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": textItemID, "output_index": textIndex, "content_index": 0, "delta": content}); err != nil {
							return err
						}
					}
					if calls, ok := delta["tool_calls"].([]any); ok {
						for _, rawCall := range calls {
							call := getMap(rawCall)
							fn := getMap(call["function"])
							callIndex := intFromMap(call, "index")
							ts := tools[callIndex]
							if ts == nil {
								ts = &responsesToolState{id: firstNonEmpty(getString(call, "id"), responseID("call")), outputIndex: nextOutputIndex}
								nextOutputIndex++
								tools[callIndex] = ts
								toolOrder = append(toolOrder, callIndex)
								if name := getString(fn, "name"); name != "" {
									ts.name = name
								}
								item := map[string]any{"id": ts.id, "type": "function_call", "status": "in_progress", "call_id": ts.id, "name": ts.name, "arguments": ""}
								if err := emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": ts.outputIndex, "item": item}); err != nil {
									return err
								}
							} else if name := getString(fn, "name"); name != "" {
								ts.name = name
							}
							if id := getString(call, "id"); id != "" {
								ts.id = id
							}
							if args := stringValue(fn["arguments"]); args != "" {
								ts.args.WriteString(args)
								if err := emit("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": ts.id, "output_index": ts.outputIndex, "delta": args}); err != nil {
									return err
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	finalOutput := make([]any, nextOutputIndex)
	if textStarted {
		text := textBuf.String()
		_ = emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "item_id": textItemID, "output_index": textIndex, "content_index": 0, "text": text})
		_ = emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "item_id": textItemID, "output_index": textIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}}})
		messageItem := map[string]any{"id": textItemID, "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}}}
		_ = emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": textIndex, "item": messageItem})
		finalOutput[textIndex] = messageItem
	}
	for _, callIndex := range toolOrder {
		ts := tools[callIndex]
		args := ts.args.String()
		_ = emit("response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": ts.id, "output_index": ts.outputIndex, "arguments": args})
		toolItem := map[string]any{"id": ts.id, "type": "function_call", "status": "completed", "call_id": ts.id, "name": ts.name, "arguments": args}
		_ = emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": ts.outputIndex, "item": toolItem})
		finalOutput[ts.outputIndex] = toolItem
	}
	return emit("response.completed", map[string]any{"type": "response.completed", "response": responsesStreamEnvelope(respID, created, model, "completed", finalOutput, usage)})
}

func writeAnthropicStream(w io.Writer, upstream io.Reader, model string) error {
	messageID := responseID("msg")
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if err := writeSSE(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	}); err != nil {
		return err
	}

	reader := bufio.NewReader(upstream)
	blockStarted := false
	blockIndex := 0
	blockKind := ""
	stopReason := "end_turn"
	currentToolIndex := -1
	toolIDsByIndex := map[int]string{}
	toolNamesByIndex := map[int]string{}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			payload := strings.TrimSpace(line)
			if strings.HasPrefix(payload, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(payload, "data:"))
				if data == "[DONE]" {
					break
				}
				chunk, ok := decodeSSEJSON(data)
				if !ok {
					continue
				}
				if rawUsage, ok := chunk["usage"]; ok && rawUsage != nil {
					if m := getMap(rawUsage); m != nil {
						usage = chatUsageMapToAnthropic(m)
					}
				}
				for _, choice := range streamChoices(chunk) {
					if reason := anthropicStopReason(getString(choice, "finish_reason")); reason != "" {
						stopReason = reason
					}
					delta := getMap(choice["delta"])
					if delta == nil {
						continue
					}
					if content := stringValue(delta["content"]); content != "" {
						if !blockStarted || blockKind != "text" {
							if blockStarted {
								_ = writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
								blockIndex++
							}
							if err := writeSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{"type": "text", "text": ""}}); err != nil {
								return err
							}
							blockStarted = true
							blockKind = "text"
						}
						if err := writeSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "text_delta", "text": content}}); err != nil {
							return err
						}
					}
					if calls, ok := delta["tool_calls"].([]any); ok {
						for _, rawCall := range calls {
							call := getMap(rawCall)
							fn := getMap(call["function"])
							callIndex := intFromMap(call, "index")
							callID := firstNonEmpty(getString(call, "id"), toolIDsByIndex[callIndex], responseID("toolu"))
							toolIDsByIndex[callIndex] = callID
							if name := getString(fn, "name"); name != "" {
								toolNamesByIndex[callIndex] = name
							}
							if !blockStarted || blockKind != "tool_use" || currentToolIndex != callIndex {
								if blockStarted {
									_ = writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
									blockIndex++
								}
								block := map[string]any{"type": "tool_use", "id": callID, "name": toolNamesByIndex[callIndex], "input": map[string]any{}}
								if err := writeSSE(w, "content_block_start", map[string]any{"type": "content_block_start", "index": blockIndex, "content_block": block}); err != nil {
									return err
								}
								blockStarted = true
								blockKind = "tool_use"
								currentToolIndex = callIndex
							}
							if args := stringValue(fn["arguments"]); args != "" {
								if err := writeSSE(w, "content_block_delta", map[string]any{"type": "content_block_delta", "index": blockIndex, "delta": map[string]any{"type": "input_json_delta", "partial_json": args}}); err != nil {
									return err
								}
							}
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	if blockStarted {
		_ = writeSSE(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
	}
	_ = writeSSE(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil}, "usage": usage})
	return writeSSE(w, "message_stop", map[string]any{"type": "message_stop"})
}

func writeSSE(w io.Writer, event string, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := io.WriteString(w, "event: "+event+"\n"); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, "data: "+string(body)+"\n\n"); err != nil {
		return err
	}
	return nil
}

func decodeSSEJSON(data string) (map[string]any, bool) {
	var chunk map[string]any
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return nil, false
	}
	return chunk, true
}

func streamChoices(chunk map[string]any) []map[string]any {
	rawChoices, ok := chunk["choices"].([]any)
	if !ok {
		return nil
	}
	choices := make([]map[string]any, 0, len(rawChoices))
	for _, rawChoice := range rawChoices {
		if choice := getMap(rawChoice); choice != nil {
			choices = append(choices, choice)
		}
	}
	return choices
}

func responsesStreamEnvelope(id string, created int64, model, status string, output []any, usage map[string]any) map[string]any {
	if usage == nil {
		usage = map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	if output == nil {
		output = []any{}
	}
	return map[string]any{
		"id":                  id,
		"object":              "response",
		"created_at":          created,
		"model":               model,
		"status":              status,
		"output":              output,
		"usage":               usage,
		"parallel_tool_calls": true,
		"tool_choice":         "auto",
		"tools":               []any{},
		"error":               nil,
		"incomplete_details":  nil,
		"instructions":        nil,
		"metadata":            map[string]any{},
		"temperature":         nil,
		"top_p":               nil,
	}
}

func chatUsageMapToResponses(usage map[string]any) map[string]any {
	input := intFromMap(usage, "prompt_tokens", "input_tokens")
	output := intFromMap(usage, "completion_tokens", "output_tokens")
	total := intFromMap(usage, "total_tokens")
	if total == 0 {
		total = input + output
	}
	return map[string]any{"input_tokens": input, "output_tokens": output, "total_tokens": total}
}

func chatUsageMapToAnthropic(usage map[string]any) map[string]any {
	return map[string]any{
		"input_tokens":  intFromMap(usage, "prompt_tokens", "input_tokens"),
		"output_tokens": intFromMap(usage, "completion_tokens", "output_tokens"),
	}
}
