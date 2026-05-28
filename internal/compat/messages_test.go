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
