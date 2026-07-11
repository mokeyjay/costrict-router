package compat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"costrict-router/internal/ids"
)

// MessagesCodec 实现 Anthropic Messages 协议 (/v1/messages) 与 Chat Completions 的互转。
type MessagesCodec struct{}

func (MessagesCodec) Name() string { return "messages" }

// ---- Anthropic 请求类型 ----

type anthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []anthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system"`
	Temperature   *float64           `json:"temperature"`
	TopP          *float64           `json:"top_p"`
	StopSequences []string           `json:"stop_sequences"`
	Stream        bool               `json:"stream"`
	Tools         []anthropicTool    `json:"tools"`
	ToolChoice    json.RawMessage    `json:"tool_choice"`
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Type        string          `json:"type"`
}

type anthropicBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	// image
	Source *anthropicSource `json:"source"`
	// tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
	// tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	// thinking
	Thinking string `json:"thinking"`
}

type anthropicSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
}

// DecodeRequest 把 Anthropic Messages 请求转换成 Chat Completions 请求。
func (MessagesCodec) DecodeRequest(body []byte) ([]byte, bool, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析 Anthropic 请求失败: "+err.Error())
	}
	if req.Model == "" {
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
	}
	if len(req.Messages) == 0 {
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "缺少 messages 字段")
	}

	chat := chatRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		chat.MaxTokens = &mt
	}
	if len(req.StopSequences) > 0 {
		chat.Stop = req.StopSequences
	}
	if req.Stream {
		chat.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	// system 转成首条 system 消息。
	if sys := anthropicTextFromSystem(req.System); sys != "" {
		chat.Messages = append(chat.Messages, chatMessage{Role: "system", Content: sys})
	}

	for _, m := range req.Messages {
		msgs, err := anthropicMessageToChat(m)
		if err != nil {
			return nil, false, err
		}
		chat.Messages = append(chat.Messages, msgs...)
	}

	// tools
	for _, t := range req.Tools {
		// Anthropic server tool（web_search_20250305 等）由 Anthropic 服务端执行，
		// 没有 input_schema，上游也不支持；转成无参 function 只会诱发无效调用，跳过。
		// 自定义工具的 type 为空或 "custom"。
		if t.Type != "" && t.Type != "custom" {
			continue
		}
		if t.Name == "" {
			continue
		}
		chat.Tools = append(chat.Tools, chatTool{
			Type: "function",
			Function: chatToolFunc{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	tc, parallel := anthropicToolChoiceToChat(req.ToolChoice)
	if tc != nil {
		chat.ToolChoice = tc
	}
	if parallel != nil {
		chat.ParallelToolCalls = parallel
	}

	out, err := jsonMarshal(chat)
	if err != nil {
		return nil, false, err
	}
	return out, req.Stream, nil
}

func anthropicTextFromSystem(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// anthropicMessageToChat 把一条 Anthropic 消息转换成一条或多条 Chat 消息。
// tool_result 块会被拆成独立的 tool 角色消息，且始终排在同一条消息的其他内容之前：
// 上游要求 tool 结果紧跟 assistant 的 tool_calls，user 文本插在中间会报 "tool_call 没有响应"
// （Anthropic 规范也要求 tool_result 位于消息开头，这里对不合规客户端做防御性重排）。
func anthropicMessageToChat(m anthropicMessage) ([]chatMessage, error) {
	// content 是纯字符串的简单情况。
	var asString string
	if err := json.Unmarshal(m.Content, &asString); err == nil {
		return []chatMessage{{Role: m.Role, Content: asString}}, nil
	}

	var blocks []anthropicBlock
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析消息内容失败: "+err.Error())
	}

	var toolMsgs []chatMessage
	var parts []chatContentPart
	var toolCalls []chatToolCall
	var textBuf strings.Builder
	var thinkBuf strings.Builder
	// tool_result 里的图片无法塞进 Chat 的 tool 角色消息（只能是字符串），
	// 暂存到这里，循环结束后统一作为一条 user 消息补发给视觉模型。
	var pendingToolImages []chatContentPart

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if m.Role == "user" {
				parts = append(parts, chatContentPart{Type: "text", Text: b.Text})
			} else {
				textBuf.WriteString(b.Text)
			}
		case "image":
			url, err := anthropicImageToURL(b.Source)
			if err != nil {
				return nil, err
			}
			if url != "" {
				parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
			}
		case "thinking":
			// 思考内容映射到 chat 的 reasoning_content（仅 assistant 回合有意义）：
			// 开启 thinking 的上游要求 assistant tool_call 消息回传它，否则报错。
			if m.Role == "assistant" {
				thinkBuf.WriteString(b.Thinking)
			}
		case "redacted_thinking":
			// 加密思考块无明文，无法回传，跳过。
		case "tool_use":
			args := string(b.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			toolCalls = append(toolCalls, chatToolCall{
				ID:   b.ID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      b.Name,
					Arguments: args,
				},
			})
		case "tool_result":
			text, images, err := anthropicToolResultParts(b.Content)
			if err != nil {
				return nil, err
			}
			if text == "" && len(images) > 0 {
				// tool 消息内容不能为空，且图片要另发，这里放一句占位说明。
				text = "[图片见后续消息]"
			}
			toolMsgs = append(toolMsgs, chatMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    text,
			})
			pendingToolImages = append(pendingToolImages, images...)
		}
	}

	out := toolMsgs
	if m.Role == "assistant" {
		if textBuf.Len() > 0 || len(toolCalls) > 0 {
			msg := chatMessage{Role: "assistant"}
			if textBuf.Len() > 0 {
				msg.Content = textBuf.String()
			}
			// 回传 assistant 的思考内容：开启 thinking 的上游（如 kimi）要求带 tool_calls 的
			// assistant 历史消息必须携带 reasoning_content，缺失会被拒绝（400 channel_error）。
			// 客户端没给思考块时补占位符（与 Responses 侧及官方扩展的常态防御一致）。
			if len(toolCalls) > 0 {
				msg.ReasoningContent = ensureToolReasoning(thinkBuf.String())
			} else if thinkBuf.Len() > 0 {
				msg.ReasoningContent = thinkBuf.String()
			}
			msg.ToolCalls = toolCalls
			out = append(out, msg)
		}
	} else if len(parts) > 0 {
		out = append(out, chatMessage{Role: "user", Content: flattenTextParts(parts)})
	} else if textBuf.Len() > 0 {
		out = append(out, chatMessage{Role: "user", Content: textBuf.String()})
	}
	// 把 tool_result 里的图片补成一条 user 消息，紧跟在 tool 消息之后送给视觉模型。
	if len(pendingToolImages) > 0 {
		parts := []chatContentPart{{Type: "text", Text: "以下是上面工具调用返回的图片："}}
		parts = append(parts, pendingToolImages...)
		out = append(out, chatMessage{Role: "user", Content: parts})
	}
	return out, nil
}

// flattenTextParts 把纯文本分片压平成字符串（官方扩展同样如此，对非视觉上游更兼容）；
// 含图片等非文本分片时保留数组形式。
func flattenTextParts(parts []chatContentPart) any {
	for _, p := range parts {
		if p.Type != "text" {
			return parts
		}
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, "\n")
}

func anthropicImageToURL(src *anthropicSource) (string, error) {
	if src == nil {
		return "", nil
	}
	switch src.Type {
	case "url":
		return validateUpstreamImageURL(src.URL)
	case "base64":
		if src.Data == "" {
			return "", nil
		}
		mt := src.MediaType
		if mt == "" {
			mt = "image/png"
		}
		return fmt.Sprintf("data:%s;base64,%s", mt, src.Data), nil
	}
	return "", nil
}

// anthropicToolResultParts 拆分 tool_result 的 content（字符串或块数组），
// 返回压平后的文本，以及其中携带的图片分片。Chat 的 tool 角色消息只能是字符串，
// 图片需由调用方放到后续 user 消息里送给视觉模型。
func anthropicToolResultParts(raw json.RawMessage) (string, []chatContentPart, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil, nil
	}
	var blocks []anthropicBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		var images []chatContentPart
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "image":
				url, err := anthropicImageToURL(b.Source)
				if err != nil {
					return "", nil, err
				}
				if url != "" {
					images = append(images, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
				}
			}
		}
		return strings.Join(parts, "\n"), images, nil
	}
	return string(raw), nil, nil
}

func anthropicToolChoiceToChat(raw json.RawMessage) (any, *bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var tc struct {
		Type                   string `json:"type"`
		Name                   string `json:"name"`
		DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, nil
	}
	var parallel *bool
	if tc.DisableParallelToolUse != nil {
		value := !*tc.DisableParallelToolUse
		parallel = &value
	}
	switch tc.Type {
	case "auto":
		return "auto", parallel
	case "any":
		return "required", parallel
	case "none":
		return "none", parallel
	case "tool":
		if tc.Name == "" {
			return nil, parallel
		}
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}, parallel
	}
	return nil, parallel
}

// ---- 响应方向: Chat Completions -> Anthropic Messages ----

func (MessagesCodec) EncodeResponse(chatBody []byte, _ bool) ([]byte, error) {
	var resp chatCompletionResponse
	if err := json.Unmarshal(chatBody, &resp); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}

	msg := map[string]any{
		"id":            anthropicMessageID(resp.ID),
		"type":          "message",
		"role":          "assistant",
		"model":         resp.Model,
		"stop_sequence": nil,
	}

	var content []map[string]any
	stopReason := "end_turn"
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		reasoning, text := resolveReasoning(choice.Message.ReasoningContent, choice.Message.Content)
		if reasoning != "" {
			content = append(content, map[string]any{
				"type":     "thinking",
				"thinking": reasoning,
			})
		}
		if text != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": text,
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Function.Name,
				"input": rawJSONOrEmptyObject(tc.Function.Arguments),
			})
		}
		stopReason = anthropicStopReason(choice.FinishReason, len(choice.Message.ToolCalls) > 0)
	}
	if content == nil {
		content = []map[string]any{}
	}
	msg["content"] = content
	msg["stop_reason"] = stopReason

	usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
	if resp.Usage != nil {
		usage["input_tokens"] = resp.Usage.PromptTokens
		usage["output_tokens"] = resp.Usage.CompletionTokens
	}
	msg["usage"] = usage

	return jsonMarshal(msg)
}

func (MessagesCodec) EncodeStream(r io.Reader, model string, _ bool) io.Reader {
	return encodeAnthropicStream(r, model)
}

func (MessagesCodec) EncodeError(status int, body []byte) []byte {
	message := extractUpstreamErrorMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	out, _ := jsonMarshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(status),
			"message": message,
		},
	})
	return out
}

func anthropicStopReason(finish string, hasToolCalls bool) string {
	switch finish {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	case "stop":
		if hasToolCalls {
			return "tool_use"
		}
		return "end_turn"
	case "":
		if hasToolCalls {
			return "tool_use"
		}
		return "end_turn"
	default:
		return "end_turn"
	}
}

func anthropicMessageID(chatID string) string {
	if chatID != "" {
		return "msg_" + strings.TrimPrefix(chatID, "chatcmpl-")
	}
	return "msg_" + ids.UUID()
}

func rawJSONOrEmptyObject(s string) json.RawMessage {
	return json.RawMessage(normalizeToolArguments(s))
}
