package compat

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxOpenAIToolNameLength = 64

func ConvertAnthropicMessagesRequest(body []byte) ([]byte, bool, error) {
	payload, err := rawMap(body)
	if err != nil {
		return nil, false, err
	}
	chat := map[string]any{}
	copyRawSame(chat, payload, "model", "temperature", "top_p", "metadata")
	copyRaw(chat, payload, "max_tokens", "max_tokens")
	stream := rawBool(payload["stream"])
	if stream {
		chat["stream"] = true
		chat["stream_options"] = map[string]any{"include_usage": true}
	}
	if tools := convertAnthropicTools(payload["tools"]); tools != nil {
		chat["tools"] = tools
	}
	if choice := convertAnthropicToolChoice(payload["tool_choice"]); choice != nil {
		chat["tool_choice"] = choice
	}
	if thinking := rawAny(payload["thinking"]); thinking != nil {
		chat["metadata"] = mergeMetadata(chat["metadata"], "anthropic_thinking", thinking)
	}
	if responseFormat := rawAny(payload["output_format"]); responseFormat != nil {
		chat["response_format"] = responseFormat
	}

	messages, err := anthropicMessagesToChat(payload["system"], payload["messages"])
	if err != nil {
		return nil, false, err
	}
	chat["messages"] = messages
	encoded, err := marshalObject(chat)
	return encoded, stream, err
}

func anthropicMessagesToChat(systemRaw, messagesRaw json.RawMessage) ([]any, error) {
	var messages []any
	if system := anthropicSystemToText(systemRaw); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	var anthropic []map[string]any
	if err := json.Unmarshal(messagesRaw, &anthropic); err != nil {
		return nil, badRequest("Anthropic messages 必须是数组")
	}
	for _, message := range anthropic {
		role := getString(message, "role")
		content := message["content"]
		switch role {
		case "assistant":
			messages = append(messages, anthropicAssistantToChat(content))
		case "user":
			messages = append(messages, anthropicUserToChat(content)...)
		default:
			return nil, badRequest(fmt.Sprintf("不支持的 Anthropic role: %s", role))
		}
	}
	return messages, nil
}

func anthropicSystemToText(raw json.RawMessage) string {
	if text := rawString(raw); text != "" {
		return text
	}
	var blocks []map[string]any
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if getString(block, "type") == "text" {
			parts = append(parts, stringValue(block["text"]))
		}
	}
	return strings.Join(parts, "\n")
}

func anthropicUserToChat(content any) []any {
	if text, ok := content.(string); ok {
		return []any{map[string]any{"role": "user", "content": text}}
	}
	blocks, ok := content.([]any)
	if !ok {
		return []any{map[string]any{"role": "user", "content": stringValue(content)}}
	}
	var userParts []any
	var messages []any
	for _, rawBlock := range blocks {
		block := getMap(rawBlock)
		switch getString(block, "type") {
		case "text":
			userParts = append(userParts, map[string]any{"type": "text", "text": stringValue(block["text"])})
		case "image":
			if part := anthropicImageToChat(block); part != nil {
				userParts = append(userParts, part)
			}
		case "tool_result":
			if len(userParts) > 0 {
				messages = append(messages, compactUserContent(userParts))
				userParts = nil
			}
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": stringValue(block["tool_use_id"]),
				"content":      anthropicToolResultContent(block["content"]),
			})
		}
	}
	if len(userParts) > 0 {
		messages = append(messages, compactUserContent(userParts))
	}
	return messages
}

func compactUserContent(parts []any) map[string]any {
	if len(parts) == 1 {
		if part := getMap(parts[0]); getString(part, "type") == "text" {
			return map[string]any{"role": "user", "content": getString(part, "text")}
		}
	}
	return map[string]any{"role": "user", "content": parts}
}

func anthropicAssistantToChat(content any) map[string]any {
	if text, ok := content.(string); ok {
		return map[string]any{"role": "assistant", "content": text}
	}
	blocks, ok := content.([]any)
	if !ok {
		return map[string]any{"role": "assistant", "content": stringValue(content)}
	}
	var textParts []string
	var toolCalls []any
	var thinking []any
	for _, rawBlock := range blocks {
		block := getMap(rawBlock)
		switch getString(block, "type") {
		case "text":
			textParts = append(textParts, stringValue(block["text"]))
		case "tool_use":
			toolCalls = append(toolCalls, map[string]any{
				"id":   stringValue(block["id"]),
				"type": "function",
				"function": map[string]any{
					"name":      truncateToolName(stringValue(block["name"])),
					"arguments": stringValue(block["input"]),
				},
			})
		case "thinking", "redacted_thinking":
			thinking = append(thinking, block)
		}
	}
	message := map[string]any{"role": "assistant", "content": strings.Join(textParts, "\n")}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	if len(thinking) > 0 {
		message["thinking_blocks"] = thinking
	}
	return message
}

func anthropicImageToChat(block map[string]any) any {
	source := getMap(block["source"])
	if source == nil {
		return nil
	}
	var url string
	switch getString(source, "type") {
	case "base64":
		mediaType := firstNonEmpty(getString(source, "media_type"), "image/png")
		url = "data:" + mediaType + ";base64," + stringValue(source["data"])
	case "url":
		url = stringValue(source["url"])
	}
	if url == "" {
		return nil
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

func anthropicToolResultContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var texts []string
		for _, rawBlock := range content {
			block := getMap(rawBlock)
			if getString(block, "type") == "text" {
				texts = append(texts, stringValue(block["text"]))
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	return stringValue(value)
}

func convertAnthropicTools(raw json.RawMessage) any {
	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	var converted []any
	for _, tool := range tools {
		fn := map[string]any{
			"name":       truncateToolName(getString(tool, "name")),
			"parameters": tool["input_schema"],
		}
		if description := getString(tool, "description"); description != "" {
			fn["description"] = description
		}
		converted = append(converted, map[string]any{"type": "function", "function": fn})
	}
	if len(converted) == 0 {
		return nil
	}
	return converted
}

func convertAnthropicToolChoice(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return nil
	}
	switch getString(choice, "type") {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": truncateToolName(getString(choice, "name"))},
		}
	default:
		return nil
	}
}

func mergeMetadata(existing any, key string, value any) map[string]any {
	metadata := map[string]any{}
	if m := getMap(existing); m != nil {
		for k, v := range m {
			metadata[k] = v
		}
	}
	metadata[key] = value
	return metadata
}

func truncateToolName(name string) string {
	if len(name) <= maxOpenAIToolNameLength {
		return name
	}
	return name[:maxOpenAIToolNameLength]
}

func ConvertChatCompletionToAnthropicMessage(body []byte) ([]byte, error) {
	var chat map[string]json.RawMessage
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, err
	}
	choices := decodeChoices(chat["choices"])
	var content []any
	stopReason := "end_turn"
	for _, choice := range choices {
		if reason := anthropicStopReason(getString(choice, "finish_reason")); reason != "" {
			stopReason = reason
		}
		message := getMap(choice["message"])
		if message == nil {
			continue
		}
		if text := stringValue(message["content"]); text != "" {
			content = append(content, map[string]any{"type": "text", "text": text})
		}
		if toolCalls, ok := message["tool_calls"].([]any); ok {
			for _, rawCall := range toolCalls {
				call := getMap(rawCall)
				fn := getMap(call["function"])
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    getString(call, "id"),
					"name":  getString(fn, "name"),
					"input": parseJSONObjectString(stringValue(fn["arguments"])),
				})
			}
		}
	}
	if content == nil {
		content = []any{}
	}
	result := map[string]any{
		"id":          firstNonEmpty(rawString(chat["id"]), responseID("msg")),
		"type":        "message",
		"role":        "assistant",
		"model":       rawString(chat["model"]),
		"content":     content,
		"stop_reason": stopReason,
		"usage":       chatUsageToAnthropic(chat["usage"]),
	}
	return json.Marshal(result)
}

func decodeChoices(raw json.RawMessage) []map[string]any {
	var choices []map[string]any
	_ = json.Unmarshal(raw, &choices)
	return choices
}

func anthropicStopReason(reason string) string {
	switch reason {
	case "":
		return ""
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return reason
	}
}

func chatUsageToAnthropic(raw json.RawMessage) map[string]any {
	usage := usageFromChat(raw)
	return map[string]any{
		"input_tokens":  intFromMap(usage, "prompt_tokens", "input_tokens"),
		"output_tokens": intFromMap(usage, "completion_tokens", "output_tokens"),
	}
}

func parseJSONObjectString(value string) any {
	if strings.TrimSpace(value) == "" {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	return map[string]any{}
}
