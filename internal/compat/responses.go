package compat

import (
	"encoding/json"
	"fmt"
	"strings"
)

func ConvertResponsesRequest(body []byte) ([]byte, bool, error) {
	payload, err := rawMap(body)
	if err != nil {
		return nil, false, err
	}
	if isMissingInputWithPreviousResponse(payload) {
		return nil, false, badRequest("costrict-router 不持久化 Responses 状态；使用 previous_response_id 时必须提供完整 input")
	}

	chat := map[string]any{}
	copyRawSame(chat, payload, "model", "temperature", "top_p", "parallel_tool_calls", "user", "metadata")
	copyRaw(chat, payload, "max_output_tokens", "max_tokens")
	stream := rawBool(payload["stream"])
	if stream {
		chat["stream"] = true
		chat["stream_options"] = map[string]any{"include_usage": true}
	}
	if value := convertResponseTextFormat(payload["text"]); value != nil {
		chat["response_format"] = value
	}
	if value := convertResponsesTools(payload["tools"]); value != nil {
		chat["tools"] = value
	}
	if value := convertResponsesToolChoice(payload["tool_choice"]); value != nil {
		chat["tool_choice"] = value
	}
	if value := convertReasoningEffort(payload["reasoning"]); value != "" {
		chat["reasoning_effort"] = value
	}

	messages, err := responsesInputToMessages(payload["instructions"], payload["input"])
	if err != nil {
		return nil, false, err
	}
	chat["messages"] = messages
	encoded, err := marshalObject(chat)
	return encoded, stream, err
}

func responsesInputToMessages(instructions, input json.RawMessage) ([]any, error) {
	var messages []any
	if system := rawString(instructions); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	if len(input) == 0 || string(input) == "null" {
		return messages, nil
	}
	var text string
	if err := json.Unmarshal(input, &text); err == nil {
		if text != "" {
			messages = append(messages, map[string]any{"role": "user", "content": text})
		}
		return messages, nil
	}
	var items []any
	if err := json.Unmarshal(input, &items); err != nil {
		return nil, badRequest("Responses input 只支持字符串或数组")
	}
	for _, item := range items {
		converted, err := responseInputItemToChatMessages(item)
		if err != nil {
			return nil, err
		}
		messages = append(messages, converted...)
	}
	return messages, nil
}

func responseInputItemToChatMessages(item any) ([]any, error) {
	m := getMap(item)
	if m == nil {
		if text, ok := item.(string); ok {
			return []any{map[string]any{"role": "user", "content": text}}, nil
		}
		return nil, badRequest("Responses input 数组项必须是对象或字符串")
	}
	itemType := getString(m, "type")
	switch itemType {
	case "function_call":
		callID := firstNonEmpty(getString(m, "call_id"), getString(m, "id"))
		return []any{map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{
				"id":   callID,
				"type": "function",
				"function": map[string]any{
					"name":      getString(m, "name"),
					"arguments": stringValue(m["arguments"]),
				},
			}},
		}}, nil
	case "function_call_output":
		return []any{map[string]any{
			"role":         "tool",
			"tool_call_id": firstNonEmpty(getString(m, "call_id"), getString(m, "id")),
			"content":      stringValue(m["output"]),
		}}, nil
	case "message", "":
		role := firstNonEmpty(getString(m, "role"), "user")
		return []any{map[string]any{
			"role":    role,
			"content": responseContentToChat(m["content"]),
		}}, nil
	default:
		content := responseBlockToChatPart(m)
		if content != nil {
			return []any{map[string]any{"role": "user", "content": []any{content}}}, nil
		}
		return nil, badRequest(fmt.Sprintf("不支持的 Responses input 类型: %s", itemType))
	}
}

func responseContentToChat(value any) any {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var parts []any
		for _, item := range content {
			if part := responseBlockToChatPart(getMap(item)); part != nil {
				parts = append(parts, part)
			}
		}
		if len(parts) == 1 {
			if text, ok := parts[0].(map[string]any)["text"].(string); ok && parts[0].(map[string]any)["type"] == "text" {
				return text
			}
		}
		return parts
	default:
		return stringValue(content)
	}
}

func responseBlockToChatPart(m map[string]any) any {
	if m == nil {
		return nil
	}
	switch getString(m, "type") {
	case "input_text", "output_text", "text":
		return map[string]any{"type": "text", "text": stringValue(m["text"])}
	case "input_image", "image_url":
		url := firstNonEmpty(stringValue(m["image_url"]), stringValue(m["url"]))
		if url == "" {
			return nil
		}
		image := map[string]any{"url": url}
		if detail := getString(m, "detail"); detail != "" {
			image["detail"] = detail
		}
		return map[string]any{"type": "image_url", "image_url": image}
	default:
		if text := stringValue(m["text"]); text != "" {
			return map[string]any{"type": "text", "text": text}
		}
		return nil
	}
}

func convertResponseTextFormat(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text map[string]any
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil
	}
	format := getMap(text["format"])
	if format == nil {
		return nil
	}
	switch getString(format, "type") {
	case "json_schema":
		schema := map[string]any{}
		for _, key := range []string{"name", "schema", "strict", "description"} {
			if value, ok := format[key]; ok {
				schema[key] = value
			}
		}
		return map[string]any{"type": "json_schema", "json_schema": schema}
	case "json_object":
		return map[string]any{"type": "json_object"}
	default:
		return nil
	}
}

func convertResponsesTools(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	var converted []any
	for _, tool := range tools {
		toolType := getString(tool, "type")
		if toolType != "function" {
			continue
		}
		if fn := getMap(tool["function"]); fn != nil {
			converted = append(converted, map[string]any{"type": "function", "function": fn})
			continue
		}
		fn := map[string]any{}
		for _, key := range []string{"name", "description", "parameters", "strict"} {
			if value, ok := tool[key]; ok {
				fn[key] = value
			}
		}
		converted = append(converted, map[string]any{"type": "function", "function": fn})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

func convertResponsesToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	if getString(choice, "type") == "function" && getString(choice, "name") != "" {
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": getString(choice, "name")},
		}
	}
	return choice
}

func convertReasoningEffort(raw json.RawMessage) string {
	var reasoning map[string]any
	if err := json.Unmarshal(raw, &reasoning); err != nil {
		return ""
	}
	return getString(reasoning, "effort")
}

func ConvertChatCompletionToResponses(body []byte) ([]byte, error) {
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}
	respID := firstNonEmpty(rawString(chat["id"]), responseID("resp"))
	output := chatChoicesToResponsesOutput(chat["choices"])
	status := "completed"
	if hasIncompleteFinish(chat["choices"]) {
		status = "incomplete"
	}
	result := map[string]any{
		"id":      respID,
		"object":  "response",
		"created": timeFromChatCreated(chat["created"]),
		"model":   rawString(chat["model"]),
		"status":  status,
		"output":  output,
		"usage":   chatUsageToResponses(chat["usage"]),
	}
	if len(output) == 1 {
		if text := responseOutputText(output[0]); text != "" {
			result["output_text"] = text
		}
	}
	return json.Marshal(result)
}

func chatChoicesToResponsesOutput(raw json.RawMessage) []any {
	var choices []map[string]any
	_ = json.Unmarshal(raw, &choices)
	var output []any
	for _, choice := range choices {
		message := getMap(choice["message"])
		if message == nil {
			continue
		}
		if content := stringValue(message["content"]); content != "" {
			output = append(output, map[string]any{
				"id":     responseID("msg"),
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []any{map[string]any{
					"type":        "output_text",
					"text":        content,
					"annotations": []any{},
				}},
			})
		}
		if toolCalls, ok := message["tool_calls"].([]any); ok {
			for _, rawCall := range toolCalls {
				call := getMap(rawCall)
				fn := getMap(call["function"])
				output = append(output, map[string]any{
					"id":        firstNonEmpty(getString(call, "id"), responseID("fc")),
					"type":      "function_call",
					"status":    "completed",
					"call_id":   firstNonEmpty(getString(call, "id"), responseID("call")),
					"name":      getString(fn, "name"),
					"arguments": stringValue(fn["arguments"]),
				})
			}
		}
	}
	return output
}

func responseOutputText(item any) string {
	m := getMap(item)
	content, ok := m["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	block := getMap(content[0])
	return getString(block, "text")
}

func hasIncompleteFinish(raw json.RawMessage) bool {
	var choices []map[string]any
	_ = json.Unmarshal(raw, &choices)
	for _, choice := range choices {
		switch getString(choice, "finish_reason") {
		case "length", "content_filter", "refusal":
			return true
		}
	}
	return false
}

func chatUsageToResponses(raw json.RawMessage) map[string]any {
	usage := usageFromChat(raw)
	input := intFromMap(usage, "prompt_tokens", "input_tokens")
	output := intFromMap(usage, "completion_tokens", "output_tokens")
	total := intFromMap(usage, "total_tokens")
	if total == 0 {
		total = input + output
	}
	return map[string]any{
		"input_tokens":  input,
		"output_tokens": output,
		"total_tokens":  total,
	}
}

func timeFromChatCreated(raw json.RawMessage) int64 {
	var value int64
	if err := json.Unmarshal(raw, &value); err == nil && value > 0 {
		return value
	}
	return 0
}

func stringValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any, map[string]any:
		body, _ := json.Marshal(v)
		return string(body)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
