package compat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertAnthropicMessagesRequestToChatCompletion(t *testing.T) {
	body := []byte(`{
		"model":"claude-compatible",
		"system":"answer shortly",
		"max_tokens":64,
		"tools":[{"name":"lookup","description":"search","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"lookup"},
		"messages":[
			{"role":"assistant","content":[{"type":"text","text":"need tool"},{"type":"tool_use","id":"toolu_1","name":"lookup","input":{"q":"x"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"result"}]}
		]
	}`)
	converted, stream, err := ConvertAnthropicMessagesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if stream {
		t.Fatal("stream = true")
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].(map[string]any)["role"] != "system" || messages[2].(map[string]any)["role"] != "tool" {
		t.Fatalf("messages = %#v", messages)
	}
	assistant := messages[1].(map[string]any)
	if !strings.Contains(assistant["content"].(string), "need tool") {
		t.Fatalf("assistant = %#v", assistant)
	}
	toolCalls := assistant["tool_calls"].([]any)
	fn := toolCalls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "lookup" || !strings.Contains(fn["arguments"].(string), `"q":"x"`) {
		t.Fatalf("tool calls = %#v", toolCalls)
	}
}

func TestConvertAnthropicMessagesRequestMapsStopSequencesAndThinking(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"stop_sequences":["STOP"],"thinking":{"type":"enabled","budget_tokens":20000},"messages":[{"role":"user","content":"hi"}]}`)
	converted, _, err := ConvertAnthropicMessagesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	stop := chat["stop"].([]any)
	if len(stop) != 1 || stop[0] != "STOP" {
		t.Fatalf("stop = %#v", chat["stop"])
	}
	if chat["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v", chat["reasoning_effort"])
	}
	if _, ok := chat["metadata"]; ok {
		t.Fatalf("metadata should not be polluted: %s", converted)
	}
}

func TestConvertChatCompletionToAnthropicMessageIncludesThinking(t *testing.T) {
	body := []byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","reasoning_content":"let me think","content":"answer"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`)
	converted, err := ConvertChatCompletionToAnthropicMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(converted, &message); err != nil {
		t.Fatal(err)
	}
	content := message["content"].([]any)
	first := content[0].(map[string]any)
	if first["type"] != "thinking" || first["thinking"] != "let me think" {
		t.Fatalf("expected thinking block first: %#v", content)
	}
	if content[1].(map[string]any)["type"] != "text" {
		t.Fatalf("expected text block after thinking: %#v", content)
	}
}

func TestAnthropicToolCallMessageCarriesReasoningContent(t *testing.T) {
	// 无 thinking 块时也要给工具调用消息补占位 reasoning_content（满足强制开思考的上游）。
	body := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"f","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"r"}]}
	]}`)
	converted, _, err := ConvertAnthropicMessagesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	assistant := chat["messages"].([]any)[0].(map[string]any)
	if rc, _ := assistant["reasoning_content"].(string); rc == "" {
		t.Fatalf("assistant tool call missing reasoning_content: %#v", assistant)
	}

	// 有 thinking 块时映射真实思考文本。
	body2 := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"assistant","content":[{"type":"thinking","thinking":"deep"},{"type":"tool_use","id":"t1","name":"f","input":{}}]}
	]}`)
	converted2, _, err := ConvertAnthropicMessagesRequest(body2)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(converted2, &chat)
	assistant2 := chat["messages"].([]any)[0].(map[string]any)
	if assistant2["reasoning_content"] != "deep" {
		t.Fatalf("reasoning_content not mapped from thinking: %#v", assistant2)
	}
}

func TestAnthropicToolResultPreservesImages(t *testing.T) {
	body := []byte(`{"model":"m","max_tokens":64,"messages":[
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[
			{"type":"text","text":"shot"},
			{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}
		]}]}
	]}`)
	converted, _, err := ConvertAnthropicMessagesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	tool := chat["messages"].([]any)[0].(map[string]any)
	parts, ok := tool["content"].([]any)
	if !ok || len(parts) != 2 {
		t.Fatalf("tool content not multimodal array: %#v", tool["content"])
	}
	if parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("image not preserved in tool result: %#v", parts)
	}
}

func TestConvertChatCompletionToAnthropicMessage(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_1",
		"model":"glm-5",
		"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":"use tool","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}],
		"usage":{"prompt_tokens":4,"completion_tokens":6}
	}`)
	converted, err := ConvertChatCompletionToAnthropicMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	var message map[string]any
	if err := json.Unmarshal(converted, &message); err != nil {
		t.Fatal(err)
	}
	if message["type"] != "message" || message["stop_reason"] != "tool_use" {
		t.Fatalf("message = %s", converted)
	}
	content := message["content"].([]any)
	if content[0].(map[string]any)["type"] != "text" || content[1].(map[string]any)["type"] != "tool_use" {
		t.Fatalf("content = %#v", content)
	}
	usage := message["usage"].(map[string]any)
	if int(usage["input_tokens"].(float64)) != 4 || int(usage["output_tokens"].(float64)) != 6 {
		t.Fatalf("usage = %#v", usage)
	}
}
