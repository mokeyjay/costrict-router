package compat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessagesDecodeRequestBasic(t *testing.T) {
	body := `{
		"model":"glm-5",
		"max_tokens":128,
		"system":"you are helpful",
		"temperature":0.5,
		"messages":[
			{"role":"user","content":"hello"},
			{"role":"assistant","content":[{"type":"text","text":"hi"}]},
			{"role":"user","content":[{"type":"text","text":"again"}]}
		]
	}`
	chatBody, stream, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if stream {
		t.Fatal("stream should be false")
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Model != "glm-5" || chat.MaxTokens == nil || *chat.MaxTokens != 128 {
		t.Fatalf("chat = %+v", chat)
	}
	if len(chat.Messages) != 4 {
		t.Fatalf("messages = %d: %s", len(chat.Messages), chatBody)
	}
	if chat.Messages[0].Role != "system" || chat.Messages[0].Content != "you are helpful" {
		t.Fatalf("system message = %+v", chat.Messages[0])
	}
}

func TestMessagesDecodeToolResultBecomesToolMessage(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"sh"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"sunny"}]}
		]
	}`
	chatBody, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if len(chat.Messages) != 2 {
		t.Fatalf("messages = %d: %s", len(chat.Messages), chatBody)
	}
	if len(chat.Messages[0].ToolCalls) != 1 || chat.Messages[0].ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("assistant tool call = %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "tool" || chat.Messages[1].ToolCallID != "toolu_1" || chat.Messages[1].Content != "sunny" {
		t.Fatalf("tool message = %+v", chat.Messages[1])
	}
}

// 回归：assistant 历史消息里的 thinking 块要映射成 reasoning_content，
// 否则开启 thinking 的上游（如 kimi）会拒绝带 tool_calls 的消息。
func TestMessagesDecodeThinkingBecomesReasoningContent(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[
			{"role":"user","content":"read the file"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"let me call the tool"},
				{"type":"tool_use","id":"toolu_1","name":"read","input":{"p":"a.txt"}}
			]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}]}
		]
	}`
	chatBody, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	// messages: user, assistant(tool_calls+reasoning), tool
	var asst *chatMessage
	for i := range chat.Messages {
		if chat.Messages[i].Role == "assistant" {
			asst = &chat.Messages[i]
		}
	}
	if asst == nil {
		t.Fatalf("缺少 assistant 消息: %s", chatBody)
	}
	if asst.ReasoningContent != "let me call the tool" {
		t.Fatalf("reasoning_content 未映射: %+v", asst)
	}
	if len(asst.ToolCalls) != 1 || asst.ToolCalls[0].ID != "toolu_1" {
		t.Fatalf("tool_calls 不对: %+v", asst)
	}
}

// 回归：tool_result 里的 image 块要补成紧随其后的一条 user 消息，
// 否则视觉模型收不到图片（Read 工具读图场景）。
func TestMessagesDecodeToolResultImageBecomesFollowupUserMessage(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":[
					{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
				]}
			]}
		]
	}`
	chatBody, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	// 期望: assistant(tool_calls), tool, user(image)
	if len(chat.Messages) != 3 {
		t.Fatalf("messages = %d: %s", len(chat.Messages), chatBody)
	}
	toolMsg := chat.Messages[1]
	if toolMsg.Role != "tool" || toolMsg.ToolCallID != "toolu_1" {
		t.Fatalf("tool 消息不对: %+v", toolMsg)
	}
	if s, _ := toolMsg.Content.(string); s == "" {
		t.Fatalf("tool 消息内容不应为空: %+v", toolMsg)
	}
	userMsg := chat.Messages[2]
	if userMsg.Role != "user" {
		t.Fatalf("应补一条 user 消息承载图片: %+v", userMsg)
	}
	rawParts, err := json.Marshal(userMsg.Content)
	if err != nil {
		t.Fatal(err)
	}
	var contentParts []chatContentPart
	if err := json.Unmarshal(rawParts, &contentParts); err != nil {
		t.Fatalf("user content 应为分片数组: %s", rawParts)
	}
	hasImage := false
	for _, p := range contentParts {
		if p.Type == "image_url" && p.ImageURL != nil && strings.HasPrefix(p.ImageURL.URL, "data:image/png;base64,") {
			hasImage = true
		}
	}
	if !hasImage {
		t.Fatalf("补发的 user 消息缺少图片分片: %s", rawParts)
	}
}

func TestMessagesEncodeResponse(t *testing.T) {
	chat := `{"id":"chatcmpl-7","model":"glm-5","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi there"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	out, err := (MessagesCodec{}).EncodeResponse([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(out, &msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "message" || msg["role"] != "assistant" {
		t.Fatalf("msg = %s", out)
	}
	if msg["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason = %v", msg["stop_reason"])
	}
	content := msg["content"].([]any)
	if len(content) != 1 || content[0].(map[string]any)["text"] != "hi there" {
		t.Fatalf("content = %v", content)
	}
	usage := msg["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 5 || usage["output_tokens"].(float64) != 2 {
		t.Fatalf("usage = %v", usage)
	}
}

func TestMessagesEncodeResponseToolUse(t *testing.T) {
	chat := `{"id":"x","model":"m","choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]}`
	out, err := (MessagesCodec{}).EncodeResponse([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	_ = json.Unmarshal(out, &msg)
	if msg["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %v: %s", msg["stop_reason"], out)
	}
	content := msg["content"].([]any)
	block := content[0].(map[string]any)
	if block["type"] != "tool_use" || block["name"] != "f" || block["id"] != "call_1" {
		t.Fatalf("tool_use block = %v", block)
	}
	input := block["input"].(map[string]any)
	if input["a"].(float64) != 1 {
		t.Fatalf("input = %v", input)
	}
}

func TestMessagesEncodeStream(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"}}]}`,
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	r := (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m")
	out := readAll(t, r)
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"type":"text_delta"`,
		`"text":"He"`,
		`"text":"llo"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream:\n%s", want, out)
		}
	}
}

func TestMessagesEncodeStreamToolCall(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f","arguments":""}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":1}"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m"))
	for _, want := range []string{
		`"type":"tool_use"`,
		`"name":"f"`,
		`"type":"input_json_delta"`,
		`"partial_json":"{\"a\":1}"`,
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream:\n%s", want, out)
		}
	}
}
