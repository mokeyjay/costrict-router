package compat

import (
	"encoding/json"
	"net/http"
)

// EstimateMessagesTokens 对 Anthropic Messages 请求做本地 token 估算，
// 供 /v1/messages/count_tokens 使用。上游没有对应接口，而 Claude Code 会用它跟踪
// 上下文用量，只需要一个量级正确的数字。启发式：ASCII 约 4 字符/token，
// 其他字符（以 CJK 为主）约 1 字符/token，图片按固定 1500，工具按 schema JSON 长度折算。
func EstimateMessagesTokens(body []byte) (int, error) {
	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return 0, newAPIError(http.StatusBadRequest, "invalid_request_error", "解析 count_tokens 请求失败: "+err.Error())
	}
	if len(req.Messages) == 0 {
		return 0, newAPIError(http.StatusBadRequest, "invalid_request_error", "缺少 messages 字段")
	}

	tokens := estimateTextTokens(anthropicTextFromSystem(req.System))
	for _, m := range req.Messages {
		tokens += 5 // 每条消息的角色与结构开销
		var asString string
		if err := json.Unmarshal(m.Content, &asString); err == nil {
			tokens += estimateTextTokens(asString)
			continue
		}
		var blocks []anthropicBlock
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				tokens += estimateTextTokens(b.Text)
			case "thinking":
				tokens += estimateTextTokens(b.Thinking)
			case "image":
				tokens += 1500
			case "tool_use":
				tokens += estimateTextTokens(b.Name) + estimateTextTokens(string(b.Input))
			case "tool_result":
				text, images, err := anthropicToolResultParts(b.Content)
				if err == nil {
					tokens += estimateTextTokens(text) + 1500*len(images)
				}
			}
		}
	}
	for _, t := range req.Tools {
		tokens += estimateTextTokens(t.Name) + estimateTextTokens(t.Description) + estimateTextTokens(string(t.InputSchema))
	}
	return tokens, nil
}

// estimateTextTokens 按字符构成估算 token 数：ASCII 约 4 字符/token，其余按 1 字符/token。
func estimateTextTokens(s string) int {
	ascii, other := 0, 0
	for _, r := range s {
		if r < 128 {
			ascii++
		} else {
			other++
		}
	}
	return (ascii+3)/4 + other
}
