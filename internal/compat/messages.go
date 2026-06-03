package compat

import (
	"encoding/json"
	"fmt"
	"strings"
)

const maxOpenAIToolNameLength = 64

// toolCallReasoningPlaceholder 用于在客户端未回传思考、但上游强制要求 assistant 工具调用消息携带 reasoning_content 时占位。
const toolCallReasoningPlaceholder = " "

func ConvertAnthropicMessagesRequest(body []byte) ([]byte, bool, error) {
	payload, err := rawMap(body)
	if err != nil {
		return nil, false, err
	}
	chat := map[string]any{}
	copyRawSame(chat, payload, "model", "temperature", "top_p", "metadata")
	copyRaw(chat, payload, "max_tokens", "max_tokens")
	if stops := rawAny(payload["stop_sequences"]); stops != nil {
		chat["stop"] = stops
	}
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
	if effort := anthropicThinkingToEffort(payload["thinking"]); effort != "" {
		chat["reasoning_effort"] = effort
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
	var thinkingParts []string
	var toolCalls []any
	for _, rawBlock := range blocks {
		block := getMap(rawBlock)
		switch getString(block, "type") {
		case "text":
			textParts = append(textParts, stringValue(block["text"]))
		case "thinking":
			if t := stringValue(block["thinking"]); t != "" {
				thinkingParts = append(thinkingParts, t)
			}
		case "tool_use":
			toolCalls = append(toolCalls, map[string]any{
				"id":   stringValue(block["id"]),
				"type": "function",
				"function": map[string]any{
					"name":      truncateToolName(stringValue(block["name"])),
					"arguments": stringValue(block["input"]),
				},
			})
		}
	}
	message := map[string]any{"role": "assistant", "content": strings.Join(textParts, "\n")}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	// 回传 assistant 的思考；开了思考的上游（如 kimi）要求工具调用消息携带非空 reasoning_content。
	if reasoning := strings.Join(thinkingParts, "\n"); reasoning != "" {
		message["reasoning_content"] = reasoning
	} else if len(toolCalls) > 0 {
		message["reasoning_content"] = toolCallReasoningPlaceholder
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

func anthropicToolResultContent(value any) any {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var parts []any
		var texts []string
		hasImage := false
		for _, rawBlock := range content {
			block := getMap(rawBlock)
			switch getString(block, "type") {
			case "text":
				text := stringValue(block["text"])
				texts = append(texts, text)
				parts = append(parts, map[string]any{"type": "text", "text": text})
			case "image":
				if part := anthropicImageToChat(block); part != nil {
					parts = append(parts, part)
					hasImage = true
				}
			}
		}
		// 上游 tool 角色消息支持数组内容，含图片时保留为多模态，否则压平成纯文本。
		if hasImage {
			return parts
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
	case "none":
		return "none"
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

func anthropicThinkingToEffort(raw json.RawMessage) string {
	// Anthropic thinking 没有 OpenAI 对应字段，按 budget_tokens 粗略映射到 reasoning_effort。
	var thinking map[string]any
	if err := json.Unmarshal(raw, &thinking); err != nil {
		return ""
	}
	if getString(thinking, "type") != "enabled" {
		return ""
	}
	budget := intFromMap(thinking, "budget_tokens")
	switch {
	case budget <= 0:
		return "medium"
	case budget < 4096:
		return "low"
	case budget < 16384:
		return "medium"
	default:
		return "high"
	}
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
		if reasoning := stringValue(message["reasoning_content"]); reasoning != "" {
			content = append(content, map[string]any{"type": "thinking", "thinking": reasoning, "signature": ""})
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
