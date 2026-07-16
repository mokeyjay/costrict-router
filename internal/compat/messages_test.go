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

func TestMessagesDecodeDisableParallelToolUse(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[{"role":"user","content":"go"}],
		"tools":[{"name":"f","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto","disable_parallel_tool_use":true}
	}`
	chatBody, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.ParallelToolCalls == nil || *chat.ParallelToolCalls {
		t.Fatalf("parallel_tool_calls = %v: %s", chat.ParallelToolCalls, chatBody)
	}
}

func TestMessagesRejectsRemoteImageURL(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"url","url":"https://example.com/a.png"}}]}]
	}`
	_, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if apiErr := AsAPIError(err); apiErr == nil || apiErr.Status != 400 {
		t.Fatalf("err = %v", err)
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
	out, err := (MessagesCodec{}).EncodeResponse([]byte(chat), true)
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
	out, err := (MessagesCodec{}).EncodeResponse([]byte(chat), true)
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
	r := (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true)
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
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
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

func TestMessagesEncodeStreamInterleavedParallelToolCalls(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f1","arguments":""}},{"index":1,"id":"call_2","type":"function","function":{"name":"f2","arguments":""}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"{\"b\":"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"function":{"arguments":"2}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if count := strings.Count(out, "event: content_block_start"); count != 2 {
		t.Fatalf("content_block_start = %d:\n%s", count, out)
	}
	if count := strings.Count(out, "event: content_block_stop"); count != 2 {
		t.Fatalf("content_block_stop = %d:\n%s", count, out)
	}
	for _, want := range []string{
		`"id":"call_1"`, `"name":"f1"`, `"partial_json":"{\"a\":"`, `"partial_json":"1}"`,
		`"id":"call_2"`, `"name":"f2"`, `"partial_json":"{\"b\":"`, `"partial_json":"2}"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// 回归：Anthropic server tool（web_search 等）没有 input_schema，上游也不支持，
// 不能转成无参 function 工具送给上游。
func TestMessagesDecodeSkipsServerTools(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[{"role":"user","content":"go"}],
		"tools":[
			{"name":"web_search","type":"web_search_20250305","max_uses":5},
			{"name":"f","input_schema":{"type":"object"}},
			{"name":"g","type":"custom","input_schema":{"type":"object"}}
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
	if len(chat.Tools) != 2 || chat.Tools[0].Function.Name != "f" || chat.Tools[1].Function.Name != "g" {
		t.Fatalf("tools = %+v", chat.Tools)
	}
}

// 回归：客户端未开启 thinking 时，assistant 工具调用消息也要带 reasoning_content 占位符，
// 与 Responses 侧 ensureToolReasoning 一致，防御 kimi 等上游的校验回归。
func TestMessagesDecodeToolCallWithoutThinkingGetsPlaceholderReasoning(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[
			{"role":"user","content":"read"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"read","input":{}}]},
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
	var asst *chatMessage
	for i := range chat.Messages {
		if chat.Messages[i].Role == "assistant" {
			asst = &chat.Messages[i]
		}
	}
	if asst == nil || asst.ReasoningContent != " " {
		t.Fatalf("assistant = %+v: %s", asst, chatBody)
	}
}

// 回归：user 消息里 text 排在 tool_result 之前时（不合规客户端），
// tool 消息必须重排到最前，否则 user 文本会插在 tool_calls 与 tool 结果之间被上游拒绝。
func TestMessagesDecodeTextBeforeToolResultReordered(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"f","input":{}}]},
			{"role":"user","content":[
				{"type":"text","text":"env details"},
				{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"}
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
	// assistant(tool_calls) -> tool -> user(text)
	if len(chat.Messages) != 3 || chat.Messages[1].Role != "tool" || chat.Messages[2].Role != "user" {
		t.Fatalf("messages = %s", chatBody)
	}
	if chat.Messages[2].Content != "env details" {
		t.Fatalf("user message = %+v", chat.Messages[2])
	}
}

// 回归：纯文本分片压平为字符串（与官方扩展一致，非视觉上游更兼容）。
func TestMessagesDecodeUserTextPartsFlattened(t *testing.T) {
	body := `{
		"model":"m","max_tokens":16,
		"messages":[{"role":"user","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}]
	}`
	chatBody, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Messages[0].Content != "a\nb" {
		t.Fatalf("content = %#v", chat.Messages[0].Content)
	}
}

// 回归：上游首个增量只有参数、id/name 晚到时，tool_use 块要推迟到 name 就绪再发，
// 之前缓冲的参数随块首增量补发，不能发出空 id/name 的块。
func TestMessagesEncodeStreamToolCallLateIDAndName(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_9","function":{"name":"f","arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if strings.Contains(out, `"name":""`) || strings.Contains(out, `"id":""`) {
		t.Fatalf("发出了空 id/name 的块:\n%s", out)
	}
	for _, want := range []string{
		`"id":"call_9"`, `"name":"f"`, `"partial_json":"{\"a\":"`, `"partial_json":"1}"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// 缓冲的参数必须在块 start 之后才发出
	if start, delta := strings.Index(out, `"type":"tool_use"`), strings.Index(out, "input_json_delta"); start < 0 || delta < start {
		t.Fatalf("增量先于块 start:\n%s", out)
	}
}

// 回归：上游全程没给 tool id 时补一个非空 id（Anthropic 协议要求 tool_use id 非空）。
func TestMessagesEncodeStreamToolCallSynthesizesID(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"f","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if strings.Contains(out, `"id":""`) {
		t.Fatalf("tool_use id 为空:\n%s", out)
	}
	if !strings.Contains(out, `"id":"call_`) {
		t.Fatalf("未补全 id:\n%s", out)
	}
}

// 回归：kimi 偶发在第一个参数对象后继续输出第二段 JSON；Anthropic 流式协议没有
// 最终修正事件，垃圾必须在转发增量时就拦下。
func TestMessagesEncodeStreamToolCallDropsSecondJSONObject(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{\"a\":1}"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if !strings.Contains(out, `"partial_json":"{\"a\":1}"`) {
		t.Fatalf("第一个对象缺失:\n%s", out)
	}
	if strings.Contains(out, `\"b\"`) {
		t.Fatalf("第二段 JSON 未被拦截:\n%s", out)
	}
}

// 回归：MiniMax 偶发把整个参数对象再次编码成 JSON 字符串。Messages 流式响应
// 必须先解包再发 input_json_delta，否则 Anthropic 客户端最终得到的是字符串而非对象。
func TestMessagesEncodeStreamToolCallUnwrapsDoubleEncodedJSONObject(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"\"{\\\"a\\\":"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}\""}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if !strings.Contains(out, `"partial_json":"{\"a\":1}"`) {
		t.Fatalf("未发出解包后的参数对象:\n%s", out)
	}
	if strings.Contains(out, `"partial_json":"\\\"`) {
		t.Fatalf("仍向客户端发出了外层 JSON 字符串:\n%s", out)
	}
}

// 回归：工具块随文本切换关闭后，迟到的参数增量只能丢弃，不能凭空开出空 name 的新块。
func TestMessagesEncodeStreamTextAfterToolCallDropsLateArgs(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{\"a\":"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"content":"text"}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (MessagesCodec{}).EncodeStream(strings.NewReader(upstream), "m", true))
	if strings.Contains(out, `"name":""`) {
		t.Fatalf("出现空 name 的块:\n%s", out)
	}
	// 两个块：tool_use + text
	if count := strings.Count(out, "event: content_block_start"); count != 2 {
		t.Fatalf("content_block_start = %d:\n%s", count, out)
	}
}

// 回归：Claude Code WebSearch 子请求只带 web_search server tool，全剔除必须显式报错，
// 否则模型会在“被暗示搜索却无工具”时幻觉输出原生工具标记，被当成搜索结果回喂。
func TestMessagesDecodeAllServerToolsDroppedErrors(t *testing.T) {
	body := `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}],"tool_choice":{"type":"auto"},"tools":[{"type":"web_search_20250305","name":"web_search"}]}`
	_, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	apiErr := AsAPIError(err)
	if apiErr == nil || apiErr.Status != 400 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(apiErr.Message, "web_search_20250305") {
		t.Fatalf("错误信息应包含被剔除类型: %s", apiErr.Message)
	}
}

func TestMessagesDecodeServerToolMixedIsFiltered(t *testing.T) {
	body := `{"model":"m","max_tokens":10,"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search_20250305","name":"web_search"},{"name":"f","input_schema":{"type":"object"}}]}`
	out, _, err := (MessagesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat struct {
		Tools []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &chat); err != nil {
		t.Fatal(err)
	}
	if len(chat.Tools) != 1 || chat.Tools[0].Function.Name != "f" {
		t.Fatalf("tools = %+v", chat.Tools)
	}
}
