package compat

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResponsesDecodeStringInput(t *testing.T) {
	chatBody, stream, err := (ResponsesCodec{}).DecodeRequest([]byte(`{"model":"glm-5","input":"hello","instructions":"sys"}`))
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
	if len(chat.Messages) != 2 {
		t.Fatalf("messages = %d: %s", len(chat.Messages), chatBody)
	}
	if chat.Messages[0].Role != "system" || chat.Messages[0].Content != "sys" {
		t.Fatalf("system = %+v", chat.Messages[0])
	}
	if chat.Messages[1].Role != "user" || chat.Messages[1].Content != "hello" {
		t.Fatalf("user = %+v", chat.Messages[1])
	}
}

func TestResponsesDecodeArrayInputWithToolCall(t *testing.T) {
	body := `{"model":"m","max_output_tokens":64,"input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]},
		{"type":"function_call","call_id":"call_1","name":"f","arguments":"{\"x\":1}"},
		{"type":"function_call_output","call_id":"call_1","output":"42"}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.MaxTokens == nil || *chat.MaxTokens != 64 {
		t.Fatalf("max_tokens = %v", chat.MaxTokens)
	}
	if len(chat.Messages) != 3 {
		t.Fatalf("messages = %d: %s", len(chat.Messages), chatBody)
	}
	if chat.Messages[0].Content != "hi" {
		t.Fatalf("user content = %v", chat.Messages[0].Content)
	}
	if len(chat.Messages[1].ToolCalls) != 1 || chat.Messages[1].ToolCalls[0].Function.Name != "f" {
		t.Fatalf("assistant tool call = %+v", chat.Messages[1])
	}
	if chat.Messages[2].Role != "tool" || chat.Messages[2].ToolCallID != "call_1" || chat.Messages[2].Content != "42" {
		t.Fatalf("tool message = %+v", chat.Messages[2])
	}
}

func TestResponsesDecodeMapsDeveloperRoleToSystem(t *testing.T) {
	// Codex 使用 developer 角色，上游只认 system/user/assistant/tool，必须映射。
	body := `{"model":"m","input":[
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev rules"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	if chat.Messages[0].Role != "system" {
		t.Fatalf("developer not mapped to system: %s", chatBody)
	}
	if strings.Contains(string(chatBody), `"developer"`) {
		t.Fatalf("developer role leaked to upstream: %s", chatBody)
	}
}

func TestResponsesStreamSurfacesUpstreamError(t *testing.T) {
	// 上游以 200 + SSE 形式返回错误时，应转成 response.failed 而不是静默空响应。
	upstream := strings.Join([]string{
		`data: {"error":{"code":"channel_error","message":"invalid role 'developer'","type":"invalid_request_error"}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (ResponsesCodec{}).EncodeStream(strings.NewReader(upstream), "m"))
	if !strings.Contains(out, "event: response.failed") {
		t.Fatalf("expected response.failed, got:\n%s", out)
	}
	if !strings.Contains(out, "invalid role 'developer'") {
		t.Fatalf("error message not propagated:\n%s", out)
	}
	if strings.Contains(out, "event: response.completed") {
		t.Fatalf("should not emit completed on error:\n%s", out)
	}
}

func TestResponsesDecodeCarriesReasoningIntoToolCall(t *testing.T) {
	// 开启 thinking 的模型要求历史里的 assistant 工具调用消息带 reasoning_content。
	body := `{"model":"m","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"列目录"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"需要调用 shell"}]},
		{"type":"function_call","call_id":"call_1","name":"shell","arguments":"{\"cmd\":\"ls\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"a.txt"}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	// 找到带 tool_calls 的 assistant 消息
	var toolMsg *chatMessage
	for i := range chat.Messages {
		if len(chat.Messages[i].ToolCalls) > 0 {
			toolMsg = &chat.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("no assistant tool call message: %s", chatBody)
	}
	if toolMsg.ReasoningContent != "需要调用 shell" {
		t.Fatalf("reasoning_content not carried: %q (%s)", toolMsg.ReasoningContent, chatBody)
	}
}

func TestResponsesDecodeToolCallReasoningFallbackNonEmpty(t *testing.T) {
	// 即使没有 reasoning 项，assistant 工具调用消息的 reasoning_content 也必须非空。
	body := `{"model":"m","input":[
		{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	_ = json.Unmarshal(chatBody, &chat)
	if chat.Messages[0].ReasoningContent == "" {
		t.Fatalf("reasoning_content should be non-empty fallback: %s", chatBody)
	}
}

func TestResponsesDecodeMergesParallelToolCalls(t *testing.T) {
	// 并行工具调用：codex 连续发多个 function_call，必须合并到同一条 assistant 消息的
	// tool_calls 数组里，否则会破坏 tool_call 与 tool 结果的配对。
	body := `{"model":"m","input":[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"读 a 和 b"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"并行读取"}]},
		{"type":"function_call","call_id":"call_a","name":"read","arguments":"{\"f\":\"a\"}"},
		{"type":"function_call","call_id":"call_b","name":"read","arguments":"{\"f\":\"b\"}"},
		{"type":"function_call_output","call_id":"call_a","output":"AAA"},
		{"type":"function_call_output","call_id":"call_b","output":"BBB"}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	// 期望：user, assistant(2个tool_calls), tool(a), tool(b) —— 共 4 条。
	if len(chat.Messages) != 4 {
		t.Fatalf("messages = %d, 期望 4 条（并行调用应合并）: %s", len(chat.Messages), chatBody)
	}
	asst := chat.Messages[1]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 2 {
		t.Fatalf("并行 function_call 未合并到一条 assistant 消息: %+v", asst)
	}
	if asst.ToolCalls[0].ID != "call_a" || asst.ToolCalls[1].ID != "call_b" {
		t.Fatalf("合并后的 tool_calls 顺序/ID 不对: %+v", asst.ToolCalls)
	}
	if chat.Messages[2].Role != "tool" || chat.Messages[3].Role != "tool" {
		t.Fatalf("tool 结果应紧跟在 assistant 消息之后: %s", chatBody)
	}
}

func TestResponsesDecodeSequentialToolCallsNotMerged(t *testing.T) {
	// 顺序工具调用（call→result→call）之间隔着 tool 结果，不能被错误合并。
	body := `{"model":"m","input":[
		{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"r1"},
		{"type":"function_call","call_id":"c2","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"c2","output":"r2"}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	_ = json.Unmarshal(chatBody, &chat)
	// 期望：assistant(c1), tool(c1), assistant(c2), tool(c2) —— 两条独立 assistant 消息。
	asstCount := 0
	for _, m := range chat.Messages {
		if m.Role == "assistant" {
			asstCount++
			if len(m.ToolCalls) != 1 {
				t.Fatalf("顺序调用被错误合并: %+v", m)
			}
		}
	}
	if asstCount != 2 {
		t.Fatalf("assistant 消息数 = %d, 期望 2: %s", asstCount, chatBody)
	}
}

func TestResponsesDecodeToolOutputPreservesImage(t *testing.T) {
	// function_call_output 含图片（如 codex 的 view_image 结果）时，必须保留为多模态数组，
	// 否则图片会被丢弃，模型看不到工具返回的图片而产生幻觉。
	body := `{"model":"m","input":[
		{"type":"function_call","call_id":"vi","name":"view_image","arguments":"{\"path\":\"x.png\"}"},
		{"type":"function_call_output","call_id":"vi","output":[
			{"type":"input_image","image_url":"data:image/png;base64,AAAA","detail":"high"}
		]}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	if err := json.Unmarshal(chatBody, &chat); err != nil {
		t.Fatal(err)
	}
	var toolMsg *chatMessage
	for i := range chat.Messages {
		if chat.Messages[i].Role == "tool" {
			toolMsg = &chat.Messages[i]
		}
	}
	if toolMsg == nil {
		t.Fatalf("缺少 tool 消息: %s", chatBody)
	}
	// content 应是分片数组，且含 image_url，而不是被压平成空字符串。
	if !strings.Contains(string(chatBody), `"image_url"`) || !strings.Contains(string(chatBody), "data:image/png;base64,AAAA") {
		t.Fatalf("tool 结果里的图片丢失了: %s", chatBody)
	}
}

func TestResponsesDecodeToolOutputTextStaysString(t *testing.T) {
	// 纯文本工具结果仍应是字符串（绝大多数工具结果），不要无谓地变成数组。
	body := `{"model":"m","input":[
		{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":[{"type":"output_text","text":"hello"}]}
	]}`
	chatBody, _, err := (ResponsesCodec{}).DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	var chat chatRequest
	_ = json.Unmarshal(chatBody, &chat)
	for _, m := range chat.Messages {
		if m.Role == "tool" {
			if s, ok := m.Content.(string); !ok || s != "hello" {
				t.Fatalf("纯文本工具结果应为字符串 \"hello\", 实际: %#v", m.Content)
			}
		}
	}
}

func TestResponsesDecodeRejectsMissingInput(t *testing.T) {
	_, _, err := (ResponsesCodec{}).DecodeRequest([]byte(`{"model":"m","previous_response_id":"resp_1"}`))
	if err == nil {
		t.Fatal("expected error for missing input")
	}
	if apiErr := AsAPIError(err); apiErr == nil || apiErr.Status != 400 {
		t.Fatalf("expected 400 APIError, got %v", err)
	}
}

func TestResponsesEncodeResponse(t *testing.T) {
	chat := `{"id":"chatcmpl-9","model":"glm-5","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`
	out, err := (ResponsesCodec{}).EncodeResponse([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp["object"] != "response" || resp["status"] != "completed" {
		t.Fatalf("resp = %s", out)
	}
	output := resp["output"].([]any)
	msg := output[0].(map[string]any)
	content := msg["content"].([]any)[0].(map[string]any)
	if content["type"] != "output_text" || content["text"] != "hi" {
		t.Fatalf("content = %v", content)
	}
	usage := resp["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 1 || usage["output_tokens"].(float64) != 2 {
		t.Fatalf("usage = %v", usage)
	}
	// Codex CLI 要求 usage 带完整明细，缺 reasoning_tokens 会导致解析失败。
	otd, ok := usage["output_tokens_details"].(map[string]any)
	if !ok {
		t.Fatalf("missing output_tokens_details: %s", out)
	}
	if _, ok := otd["reasoning_tokens"]; !ok {
		t.Fatalf("missing reasoning_tokens: %s", out)
	}
	itd, ok := usage["input_tokens_details"].(map[string]any)
	if !ok {
		t.Fatalf("missing input_tokens_details: %s", out)
	}
	if _, ok := itd["cached_tokens"]; !ok {
		t.Fatalf("missing cached_tokens: %s", out)
	}
}

func TestResponsesUsageCarriesReasoningFromUpstream(t *testing.T) {
	// 上游若提供 completion_tokens_details.reasoning_tokens / prompt_tokens_details.cached_tokens，应透传。
	chat := `{"id":"x","model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens_details":{"reasoning_tokens":7}}}`
	out, err := (ResponsesCodec{}).EncodeResponse([]byte(chat))
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	usage := resp["usage"].(map[string]any)
	if usage["output_tokens_details"].(map[string]any)["reasoning_tokens"].(float64) != 7 {
		t.Fatalf("reasoning_tokens = %v", usage)
	}
	if usage["input_tokens_details"].(map[string]any)["cached_tokens"].(float64) != 4 {
		t.Fatalf("cached_tokens = %v", usage)
	}
}

func TestResponsesEncodeStream(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"He"}}]}`,
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
		`data: {"id":"chatcmpl-1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (ResponsesCodec{}).EncodeStream(strings.NewReader(upstream), "m"))
	for _, want := range []string{
		"event: response.created",
		"event: response.output_item.added",
		"event: response.content_part.added",
		"event: response.output_text.delta",
		`"delta":"He"`,
		`"delta":"llo"`,
		"event: response.output_text.done",
		`"text":"Hello"`,
		"event: response.output_item.done",
		"event: response.completed",
		`"output_tokens":1`,
		`"reasoning_tokens":0`,
		`"cached_tokens":0`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream:\n%s", want, out)
		}
	}
	// sequence_number 必须从 0 递增且唯一。
	if !strings.Contains(out, `"sequence_number":0`) {
		t.Fatalf("missing sequence_number 0:\n%s", out)
	}
}

func TestResponsesEncodeStreamToolCall(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f","arguments":""}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":1}"}}]}}]}`,
		`data: {"id":"c","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	out := readAll(t, (ResponsesCodec{}).EncodeStream(strings.NewReader(upstream), "m"))
	for _, want := range []string{
		`"type":"function_call"`,
		`"call_id":"call_1"`,
		"event: response.function_call_arguments.delta",
		`"delta":"{\"a\":1}"`,
		"event: response.function_call_arguments.done",
		`"arguments":"{\"a\":1}"`,
		"event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in stream:\n%s", want, out)
		}
	}
}
