package compat

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"costrict-router/internal/ids"
)

// ResponsesCodec 实现 OpenAI Responses 协议 (/v1/responses) 与 Chat Completions 的互转。
type ResponsesCodec struct{}

func (ResponsesCodec) Name() string { return "responses" }

// ---- Responses 请求类型 ----

type responsesRequest struct {
	Model              string          `json:"model"`
	Input              json.RawMessage `json:"input"`
	Instructions       string          `json:"instructions"`
	MaxOutputTokens    *int            `json:"max_output_tokens"`
	Temperature        *float64        `json:"temperature"`
	TopP               *float64        `json:"top_p"`
	Stream             bool            `json:"stream"`
	Tools              []responsesTool `json:"tools"`
	ToolChoice         json.RawMessage `json:"tool_choice"`
	PreviousResponseID string          `json:"previous_response_id"`
}

type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Output    json.RawMessage `json:"output"`
	Summary   json.RawMessage `json:"summary"`
}

type responsesContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ImageURL json.RawMessage `json:"image_url"`
}

// DecodeRequest 把 Responses 请求转换成 Chat Completions 请求。
func (ResponsesCodec) DecodeRequest(body []byte) ([]byte, bool, error) {
	var req responsesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析 Responses 请求失败: "+err.Error())
	}
	if req.Model == "" {
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
	}
	if len(req.Input) == 0 || string(req.Input) == "null" {
		// 本服务无状态，无法依靠 previous_response_id 还原历史。
		return nil, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "缺少 input 字段（本服务无状态，不支持仅凭 previous_response_id 续聊）")
	}

	chat := chatRequest{
		Model:       req.Model,
		MaxTokens:   req.MaxOutputTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}
	if req.Stream {
		chat.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if req.Instructions != "" {
		chat.Messages = append(chat.Messages, chatMessage{Role: "system", Content: req.Instructions})
	}

	msgs, err := responsesInputToChat(req.Input)
	if err != nil {
		return nil, false, err
	}
	chat.Messages = append(chat.Messages, msgs...)

	for _, t := range req.Tools {
		if t.Type != "" && t.Type != "function" {
			// 仅支持 function 工具，内置工具（web_search 等）上游不支持，忽略。
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
				Parameters:  t.Parameters,
			},
		})
	}
	if tc := responsesToolChoiceToChat(req.ToolChoice); tc != nil {
		chat.ToolChoice = tc
	}

	out, err := jsonMarshal(chat)
	if err != nil {
		return nil, false, err
	}
	return out, req.Stream, nil
}

func responsesInputToChat(raw json.RawMessage) ([]chatMessage, error) {
	// input 为纯字符串时视作一条 user 消息。
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return []chatMessage{{Role: "user", Content: asString}}, nil
	}

	var items []responsesInputItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析 input 失败: "+err.Error())
	}

	var out []chatMessage
	// 记录最近一个 reasoning 项的文本，回填给紧随其后的 assistant 消息的 reasoning_content。
	// 开启 thinking 的模型（如 kimi）要求历史里的 assistant 工具调用消息必须带 reasoning_content，
	// 否则会报 "thinking is enabled but reasoning_content is missing"。
	pendingReasoning := ""
	for _, item := range items {
		typ := item.Type
		if typ == "" {
			typ = "message" // 省略 type 时默认为 message
		}
		switch typ {
		case "message":
			msg, ok, err := responsesMessageItemToChat(item)
			if err != nil {
				return nil, err
			}
			if ok {
				if msg.Role == "assistant" {
					msg.ReasoningContent = pendingReasoning
				}
				out = append(out, msg)
			}
			if msg.Role == "user" {
				pendingReasoning = ""
			}
		case "function_call":
			args := item.Arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			tc := chatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: chatFunctionCall{
					Name:      item.Name,
					Arguments: args,
				},
			}
			// 并行工具调用：codex 会连续发多个 function_call。OpenAI 规范要求它们位于
			// 同一条 assistant 消息的 tool_calls 数组里，且其后紧跟各自的 tool 结果；
			// 拆成多条 assistant 消息会破坏 tool_call 与结果的配对，严格的上游会拒绝。
			if n := len(out); n > 0 && out[n-1].Role == "assistant" && len(out[n-1].ToolCalls) > 0 && out[n-1].Content == nil {
				out[n-1].ToolCalls = append(out[n-1].ToolCalls, tc)
			} else {
				out = append(out, chatMessage{
					Role:             "assistant",
					ReasoningContent: ensureToolReasoning(pendingReasoning),
					ToolCalls:        []chatToolCall{tc},
				})
			}
		case "function_call_output":
			out = append(out, chatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    responsesOutputContent(item.Output),
			})
			pendingReasoning = "" // 工具结果开启新的上下文
		case "reasoning":
			pendingReasoning = responsesReasoningText(item)
		}
	}
	return out, nil
}

// responsesReasoningText 从 reasoning 项里提取可读文本（优先 summary，其次 content）。
func responsesReasoningText(item responsesInputItem) string {
	if text := responsesPartsText(item.Summary); text != "" {
		return text
	}
	return responsesPartsText(item.Content)
}

// responsesPartsText 把字符串或 [{text}] 数组压成纯文本。
func responsesPartsText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// ensureToolReasoning 保证 assistant 工具调用消息的 reasoning_content 非空，
// 以满足开启 thinking 的上游模型的校验（Codex 的 reasoning 多为加密内容，summary 偶尔为空）。
func ensureToolReasoning(reasoning string) string {
	if strings.TrimSpace(reasoning) != "" {
		return reasoning
	}
	return " "
}

func responsesMessageItemToChat(item responsesInputItem) (chatMessage, bool, error) {
	role := normalizeChatRole(item.Role)
	// content 为字符串
	var asString string
	if err := json.Unmarshal(item.Content, &asString); err == nil {
		if asString == "" {
			return chatMessage{}, false, nil
		}
		return chatMessage{Role: role, Content: asString}, true, nil
	}

	var parts []responsesContentPart
	if err := json.Unmarshal(item.Content, &parts); err != nil {
		return chatMessage{}, false, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析消息内容失败: "+err.Error())
	}

	var chatParts []chatContentPart
	var textOnly strings.Builder
	hasNonText := false
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			textOnly.WriteString(p.Text)
			chatParts = append(chatParts, chatContentPart{Type: "text", Text: p.Text})
		case "input_image":
			if url := responsesImageURL(p.ImageURL); url != "" {
				hasNonText = true
				chatParts = append(chatParts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
			}
		}
	}
	if len(chatParts) == 0 {
		return chatMessage{}, false, nil
	}
	// 纯文本时用字符串形式，更兼容上游。
	if !hasNonText {
		return chatMessage{Role: role, Content: textOnly.String()}, true, nil
	}
	return chatMessage{Role: role, Content: chatParts}, true, nil
}

// normalizeChatRole 把 Responses 协议的角色规整为上游 Chat Completions 接受的角色。
// 关键：OpenAI 新规范用 "developer" 取代 "system"，但上游只认 system/user/assistant/tool。
func normalizeChatRole(role string) string {
	switch role {
	case "":
		return "user"
	case "developer":
		return "system"
	default:
		return role
	}
}

func responsesImageURL(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// 也兼容对象形式 {"url":"..."}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj.URL
	}
	return ""
}

// responsesOutputContent 把 function_call_output 的 output 转换成 Chat 工具消息的 content。
// 纯文本时返回字符串；含图片（input_image，如 codex 的 view_image 结果）时返回多模态分片数组，
// 否则图片会被丢弃，导致模型看不到工具返回的图片而产生幻觉。
func responsesOutputContent(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	// 字符串形式：绝大多数工具结果都是纯文本，直接返回。
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	// 数组形式：可能夹带 input_image，逐项转换，含图片时保留为多模态数组。
	var parts []responsesContentPart
	if err := json.Unmarshal(raw, &parts); err == nil {
		var chatParts []chatContentPart
		var textOnly strings.Builder
		hasImage := false
		for _, p := range parts {
			switch p.Type {
			case "input_text", "output_text", "text":
				textOnly.WriteString(p.Text)
				chatParts = append(chatParts, chatContentPart{Type: "text", Text: p.Text})
			case "input_image":
				if url := responsesImageURL(p.ImageURL); url != "" {
					hasImage = true
					chatParts = append(chatParts, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: url}})
				}
			}
		}
		if hasImage {
			return chatParts
		}
		return textOnly.String()
	}
	return string(raw)
}

func responsesToolChoiceToChat(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		// "auto" / "none" / "required" 直接透传
		return s
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err == nil && tc.Type == "function" && tc.Name != "" {
		return map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.Name},
		}
	}
	return nil
}

// ---- 响应方向: Chat Completions -> Responses ----

func (ResponsesCodec) EncodeResponse(chatBody []byte) ([]byte, error) {
	var resp chatCompletionResponse
	if err := json.Unmarshal(chatBody, &resp); err != nil {
		return nil, fmt.Errorf("解析上游响应失败: %w", err)
	}

	created := resp.Created
	if created == 0 {
		created = time.Now().Unix()
	}

	var output []any
	status := "completed"
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		reasoning, text := resolveReasoning(choice.Message.ReasoningContent, choice.Message.Content)
		if reasoning != "" {
			output = append(output, map[string]any{
				"type": "reasoning",
				"id":   "rs_" + ids.UUID(),
				"summary": []any{
					map[string]any{"type": "summary_text", "text": reasoning},
				},
			})
		}
		if text != "" {
			output = append(output, map[string]any{
				"type":   "message",
				"id":     "msg_" + ids.UUID(),
				"status": "completed",
				"role":   "assistant",
				"content": []any{
					map[string]any{
						"type":        "output_text",
						"text":        text,
						"annotations": []any{},
					},
				},
			})
		}
		for _, tc := range choice.Message.ToolCalls {
			args := tc.Function.Arguments
			if strings.TrimSpace(args) == "" {
				args = "{}"
			}
			output = append(output, map[string]any{
				"type":      "function_call",
				"id":        "fc_" + ids.UUID(),
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": args,
				"status":    "completed",
			})
		}
		if choice.FinishReason == "length" {
			status = "incomplete"
		}
	}
	if output == nil {
		output = []any{}
	}

	out := map[string]any{
		"id":                  responsesID(resp.ID),
		"object":              "response",
		"created_at":          created,
		"status":              status,
		"model":               resp.Model,
		"output":              output,
		"usage":               responsesUsage(resp.Usage),
		"error":               nil,
		"metadata":            map[string]any{},
		"parallel_tool_calls": true,
	}
	if status == "incomplete" {
		out["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	return jsonMarshal(out)
}

func (ResponsesCodec) EncodeStream(r io.Reader, model string) io.Reader {
	return encodeResponsesStream(r, model)
}

func (ResponsesCodec) EncodeError(status int, body []byte) []byte {
	message := extractUpstreamErrorMessage(body)
	if message == "" {
		message = http.StatusText(status)
	}
	out, _ := jsonMarshal(map[string]any{
		"error": map[string]any{
			"type":    openAIErrorType(status),
			"message": message,
			"code":    nil,
		},
	})
	return out
}

func responsesID(chatID string) string {
	if chatID != "" {
		return "resp_" + strings.TrimPrefix(chatID, "chatcmpl-")
	}
	return "resp_" + ids.UUID()
}

// responsesUsage 输出 OpenAI Responses 官方完整的 usage 结构。
// 必须带上 input_tokens_details.cached_tokens 与 output_tokens_details.reasoning_tokens，
// 否则 Codex CLI 等严格客户端会因 `missing field reasoning_tokens` 解析失败，
// 把整段流当成中断并重试。
func responsesUsage(u *chatUsage) map[string]any {
	if u == nil {
		return map[string]any{
			"input_tokens":          0,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens":         0,
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
			"total_tokens":          0,
		}
	}
	return map[string]any{
		"input_tokens":          u.PromptTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": u.cachedTokens()},
		"output_tokens":         u.CompletionTokens,
		"output_tokens_details": map[string]any{"reasoning_tokens": u.reasoningTokens()},
		"total_tokens":          u.TotalTokens,
	}
}
