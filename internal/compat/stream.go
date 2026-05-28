package compat

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"time"
)

func TransformChatCompletionStreamToResponses(upstream io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := writeResponsesStream(pw, upstream)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

func TransformChatCompletionStreamToAnthropic(upstream io.Reader) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := writeAnthropicStream(pw, upstream)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()
	return pr
}

func writeResponsesStream(w io.Writer, upstream io.Reader) error {
	respID := responseID("resp")
	created := time.Now().Unix()
	if err := writeSSE(w, "response.created", map[string]any{"type": "response.created", "response": responsesStreamEnvelope(respID, created, "created", nil)}); err != nil {
		return err
	}
	if err := writeSSE(w, "response.in_progress", map[string]any{"type": "response.in_progress", "response": responsesStreamEnvelope(respID, created, "in_progress", nil)}); err != nil {
		return err
	}

	reader := bufio.NewReader(upstream)
	textStarted := false
	textIndex := 0
	toolStarted := map[string]bool{}
	toolIDsByIndex := map[int]string{}
	toolNamesByIndex := map[int]string{}
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
							item := map[string]any{"id": responseID("msg"), "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
							if err := writeSSE(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": textIndex, "item": item}); err != nil {
								return err
							}
							_ = writeSSE(w, "response.content_part.added", map[string]any{"type": "response.content_part.added", "item_id": item["id"], "output_index": textIndex, "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}})
							textStarted = true
						}
						if err := writeSSE(w, "response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": textIndex, "content_index": 0, "delta": content}); err != nil {
							return err
						}
					}
					if calls, ok := delta["tool_calls"].([]any); ok {
						for _, rawCall := range calls {
							call := getMap(rawCall)
							fn := getMap(call["function"])
							callIndex := intFromMap(call, "index")
							callID := firstNonEmpty(getString(call, "id"), toolIDsByIndex[callIndex], responseID("call"))
							toolIDsByIndex[callIndex] = callID
							if name := getString(fn, "name"); name != "" {
								toolNamesByIndex[callIndex] = name
							}
							if !toolStarted[callID] {
								item := map[string]any{"id": callID, "type": "function_call", "status": "in_progress", "call_id": callID, "name": toolNamesByIndex[callIndex], "arguments": ""}
								if err := writeSSE(w, "response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": len(toolStarted) + 1, "item": item}); err != nil {
									return err
								}
								toolStarted[callID] = true
							}
							if args := stringValue(fn["arguments"]); args != "" {
								if err := writeSSE(w, "response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "item_id": callID, "delta": args}); err != nil {
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
	if textStarted {
		_ = writeSSE(w, "response.output_text.done", map[string]any{"type": "response.output_text.done", "output_index": textIndex, "content_index": 0})
		_ = writeSSE(w, "response.content_part.done", map[string]any{"type": "response.content_part.done", "output_index": textIndex, "content_index": 0})
	}
	for callID := range toolStarted {
		_ = writeSSE(w, "response.function_call_arguments.done", map[string]any{"type": "response.function_call_arguments.done", "item_id": callID})
	}
	return writeSSE(w, "response.completed", map[string]any{"type": "response.completed", "response": responsesStreamEnvelope(respID, created, "completed", usage)})
}

func writeAnthropicStream(w io.Writer, upstream io.Reader) error {
	messageID := responseID("msg")
	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if err := writeSSE(w, "message_start", map[string]any{
		"type":    "message_start",
		"message": map[string]any{"id": messageID, "type": "message", "role": "assistant", "content": []any{}, "usage": usage},
	}); err != nil {
		return err
	}

	reader := bufio.NewReader(upstream)
	blockStarted := false
	blockIndex := 0
	blockKind := ""
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
							if !blockStarted || blockKind != "tool_use" {
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
	_ = writeSSE(w, "message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": usage})
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

func responsesStreamEnvelope(id string, created int64, status string, usage map[string]any) map[string]any {
	if usage == nil {
		usage = map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0}
	}
	return map[string]any{"id": id, "object": "response", "created": created, "status": status, "output": []any{}, "usage": usage}
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
