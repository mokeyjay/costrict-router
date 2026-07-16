package compat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// 本文件服务于“先向客户端提交 SSE、再在后台连接上游”的流式路径：
// 上游 gateway 在发出响应头之前就可能缓冲数秒到一分钟以上，代理必须在
// 上游请求完成前先应答客户端；因此上游的 HTTP 错误与非流式响应都要
// 转换成流式编码器能消费的 SSE 块。

// UpstreamErrorSSE 把上游错误包装成流内错误负载（编码器会转成协议错误事件）。
// status 为 0 表示传输层错误（连接失败等），body 为错误正文或错误消息。
func UpstreamErrorSSE(status int, body []byte) []byte {
	message := extractUpstreamErrorMessage(body)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" && status > 0 {
		message = http.StatusText(status)
	}
	if status > 0 {
		message = fmt.Sprintf("上游错误 (HTTP %d): %s", status, message)
	}
	payload := mustJSON(map[string]any{
		"error": map[string]any{"message": message},
	})
	var b bytes.Buffer
	b.WriteString("data: ")
	b.Write(payload)
	b.WriteString("\n\n")
	return b.Bytes()
}

// ChatJSONToSSE 把非流式 Chat Completions JSON 响应转成等价的 SSE 块序列，
// 兜底“客户端要流式但上游返回了完整 JSON”的情况，让流式编码器统一处理。
func ChatJSONToSSE(body []byte) []byte {
	var resp chatCompletionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return UpstreamErrorSSE(0, body)
	}
	var b bytes.Buffer
	writeData := func(v any) {
		b.WriteString("data: ")
		b.Write(mustJSON(v))
		b.WriteString("\n\n")
	}
	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		delta := chatDelta{Role: "assistant"}
		if choice.Message.Content != "" {
			content := choice.Message.Content
			delta.Content = &content
		}
		if choice.Message.ReasoningContent != "" {
			reasoning := choice.Message.ReasoningContent
			delta.ReasoningContent = &reasoning
		}
		for i, tc := range choice.Message.ToolCalls {
			index := i
			tc.Index = &index
			delta.ToolCalls = append(delta.ToolCalls, tc)
		}
		finish := choice.FinishReason
		if finish == "" {
			finish = "stop"
		}
		writeData(chatChunk{ID: resp.ID, Model: resp.Model, Choices: []chatChunkChoice{{Delta: delta, FinishReason: &finish}}})
	}
	if resp.Usage != nil {
		writeData(chatChunk{ID: resp.ID, Model: resp.Model, Usage: resp.Usage})
	}
	b.WriteString("data: [DONE]\n\n")
	return b.Bytes()
}
