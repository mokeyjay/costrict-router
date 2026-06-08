package compat

import (
	"encoding/json"
	"io"
)

// 本包负责把 OpenAI Responses (/v1/responses) 与 Anthropic Messages (/v1/messages)
// 两种协议双向翻译到上游唯一支持的 Chat Completions 协议。
//
// 入站方向: 客户端请求体 -> Chat Completions 请求体 (DecodeRequest)
// 出站方向: 上游 Chat Completions 响应 -> 客户端协议响应 (EncodeResponse / EncodeStream)

// Codec 描述一种对外协议与 Chat Completions 之间的双向转换。
type Codec interface {
	// Name 返回协议名，用于日志。
	Name() string
	// DecodeRequest 把客户端请求体转换成 Chat Completions 请求体，并返回是否为流式请求。
	DecodeRequest(body []byte) (chatBody []byte, stream bool, err error)
	// EncodeResponse 把上游 Chat Completions 的 JSON 响应转换成客户端协议响应。
	EncodeResponse(chatBody []byte) ([]byte, error)
	// EncodeStream 把上游 Chat Completions 的 SSE 流转换成客户端协议的 SSE 流。
	EncodeStream(r io.Reader, model string) io.Reader
	// EncodeError 把上游错误体转换成客户端协议的错误信封。
	EncodeError(status int, body []byte) []byte
}

// ---- Chat Completions 请求类型 ----

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	TopP          *float64       `json:"top_p,omitempty"`
	Stop          any            `json:"stop,omitempty"`
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Tools         []chatTool     `json:"tools,omitempty"`
	ToolChoice    any            `json:"tool_choice,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          any            `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Name             string         `json:"name,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Index    *int             `json:"index,omitempty"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatToolFunc `json:"function"`
}

type chatToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// 多模态内容分片，content 既可能是字符串，也可能是分片数组。
type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

// ---- Chat Completions 响应类型 ----

type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
}

type chatChoice struct {
	Index        int           `json:"index"`
	FinishReason string        `json:"finish_reason"`
	Message      chatChoiceMsg `json:"message"`
}

type chatChoiceMsg struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// 不同上游放置缓存/推理 token 明细的位置不一：既有顶层 cached_tokens，
	// 也有标准 OpenAI 的 *_tokens_details 嵌套对象，这里都兼容。
	CachedTokens            int           `json:"cached_tokens"`
	PromptTokensDetails     *tokenDetails `json:"prompt_tokens_details"`
	CompletionTokensDetails *tokenDetails `json:"completion_tokens_details"`
}

type tokenDetails struct {
	CachedTokens    int `json:"cached_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

// cachedTokens 返回输入侧缓存命中的 token 数，按优先级从多个可能位置取值。
func (u *chatUsage) cachedTokens() int {
	if u.PromptTokensDetails != nil && u.PromptTokensDetails.CachedTokens > 0 {
		return u.PromptTokensDetails.CachedTokens
	}
	return u.CachedTokens
}

// reasoningTokens 返回输出侧推理消耗的 token 数（上游通常不提供，缺省为 0）。
func (u *chatUsage) reasoningTokens() int {
	if u.CompletionTokensDetails != nil {
		return u.CompletionTokensDetails.ReasoningTokens
	}
	return 0
}

// ---- Chat Completions 流式 chunk 类型 ----

type chatChunk struct {
	ID      string            `json:"id"`
	Model   string            `json:"model"`
	Choices []chatChunkChoice `json:"choices"`
	Usage   *chatUsage        `json:"usage"`
}

type chatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"`
}

type chatDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          *string        `json:"content,omitempty"`
	ReasoningContent *string        `json:"reasoning_content,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
}
