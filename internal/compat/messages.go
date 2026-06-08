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
	if tc := anthropicToolChoiceToChat(req.ToolChoice); tc != nil {
		chat.ToolChoice = tc
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
// tool_result 块会被拆成独立的 tool 角色消息。
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

	var out []chatMessage
	var parts []chatContentPart
	var toolCalls []chatToolCall
	var textBuf strings.Builder

	flushUserAssistant := func() {
		if m.Role == "assistant" {
			if textBuf.Len() == 0 && len(toolCalls) == 0 {
				return
			}
			msg := chatMessage{Role: "assistant"}
			if textBuf.Len() > 0 {
				msg.Content = textBuf.String()
			}
			msg.ToolCalls = toolCalls
			out = append(out, msg)
		} else {
			if len(parts) > 0 {
				out = append(out, chatMessage{Role: "user", Content: parts})
			} else if textBuf.Len() > 0 {
				out = append(out, chatMessage{Role: "user", Content: textBuf.String()})
			}
		}
		textBuf.Reset()
		parts = nil
		toolCalls = nil
	}

	for _, b := range blocks {
		switch b.Type {
		case "text":
			if m.Role == "user" {
				parts = append(parts, chatContentPart{Type: "text", Text: b.Text})
			} else {
				textBuf.WriteString(b.Text)
			}
		case "image":
			if url := anthropicImageToURL(b.Source); url != "" {
				parts = append(parts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
			}
		case "thinking", "redacted_thinking":
			// 思考内容不回传给上游，避免污染对话。
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
			// tool_result 必须单独成为 tool 消息，先把已累积的内容刷出。
			flushUserAssistant()
			out = append(out, chatMessage{
				Role:       "tool",
				ToolCallID: b.ToolUseID,
				Content:    anthropicToolResultText(b.Content),
			})
		}
	}
	flushUserAssistant()
	return out, nil
}

func anthropicImageToURL(src *anthropicSource) string {
	if src == nil {
		return ""
	}
	switch src.Type {
	case "url":
		return src.URL
	case "base64":
		if src.Data == "" {
			return ""
		}
		mt := src.MediaType
		if mt == "" {
			mt = "image/png"
		}
		return fmt.Sprintf("data:%s;base64,%s", mt, src.Data)
	}
	return ""
}

// anthropicToolResultText 把 tool_result 的 content（字符串或块数组）压成纯文本。
func anthropicToolResultText(raw json.RawMessage) string {
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
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

func anthropicToolChoiceToChat(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "none":
		return "none"
	case "tool":
		if tc.Name == "" {
			return nil
		}
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	}
	return nil
}

// ---- 响应方向: Chat Completions -> Anthropic Messages ----

func (MessagesCodec) EncodeResponse(chatBody []byte) ([]byte, error) {
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

func (MessagesCodec) EncodeStream(r io.Reader, model string) io.Reader {
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
	s = strings.TrimSpace(s)
	if s == "" {
		return json.RawMessage("{}")
	}
	if !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}
