package compat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConvertResponsesRequestToChatCompletion(t *testing.T) {
	body := []byte(`{
		"model":"glm-5",
		"instructions":"be concise",
		"input":"hello",
		"max_output_tokens":128,
		"text":{"format":{"type":"json_schema","name":"answer","schema":{"type":"object"},"strict":true}},
		"tools":[{"type":"function","name":"lookup","description":"search","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"lookup"},
		"stream":true
	}`)
	converted, stream, err := ConvertResponsesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if !stream {
		t.Fatal("stream = false")
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["model"] != "glm-5" || int(chat["max_tokens"].(float64)) != 128 {
		t.Fatalf("unexpected chat request: %s", converted)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" || messages[1].(map[string]any)["content"] != "hello" {
		t.Fatalf("messages = %#v", messages)
	}
	responseFormat := chat["response_format"].(map[string]any)
	if responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", responseFormat)
	}
	tools := chat["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "lookup" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestConvertResponsesRequestRejectsStateOnlyPreviousResponse(t *testing.T) {
	_, _, err := ConvertResponsesRequest([]byte(`{"model":"m","previous_response_id":"resp_1"}`))
	if err == nil || !strings.Contains(err.Error(), "不持久化 Responses 状态") {
		t.Fatalf("err = %v", err)
	}
}

func TestConvertResponsesRequestMergesParallelToolCallsAndSkipsReasoning(t *testing.T) {
	body := []byte(`{"model":"m","input":[
		{"type":"message","role":"user","content":"go"},
		{"type":"reasoning","id":"rs_1","summary":[]},
		{"type":"function_call","call_id":"c1","name":"a","arguments":"{}"},
		{"type":"function_call","call_id":"c2","name":"b","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"r1"},
		{"type":"function_call_output","call_id":"c2","output":"r2"}
	]}`)
	converted, _, err := ConvertResponsesRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	var chat map[string]any
	if err := json.Unmarshal(converted, &chat); err != nil {
		t.Fatal(err)
	}
	messages := chat["messages"].([]any)
	// user, assistant(2 tool_calls), tool(c1), tool(c2) —— reasoning 被跳过，并行 function_call 合并
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	assistant := messages[1].(map[string]any)
	if assistant["role"] != "assistant" || len(assistant["tool_calls"].([]any)) != 2 {
		t.Fatalf("assistant tool_calls not merged: %#v", assistant)
	}
	if messages[2].(map[string]any)["role"] != "tool" || messages[3].(map[string]any)["role"] != "tool" {
		t.Fatalf("tool results = %#v", messages)
	}
}

func TestConvertChatCompletionToResponsesIncludesReasoning(t *testing.T) {
	body := []byte(`{"model":"m","choices":[{"finish_reason":"stop","message":{"role":"assistant","reasoning_content":"thinking","content":"hi"}}]}`)
	converted, err := ConvertChatCompletionToResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(converted, &response); err != nil {
		t.Fatal(err)
	}
	output := response["output"].([]any)
	if output[0].(map[string]any)["type"] != "reasoning" || output[1].(map[string]any)["type"] != "message" {
		t.Fatalf("output = %#v", output)
	}
	if response["output_text"] != "hi" {
		t.Fatalf("output_text = %#v", response["output_text"])
	}
}

func TestConvertChatCompletionToResponses(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_1",
		"model":"glm-5",
		"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],
		"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}
	}`)
	converted, err := ConvertChatCompletionToResponses(body)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(converted, &response); err != nil {
		t.Fatal(err)
	}
	if response["object"] != "response" || response["output_text"] != "hi" {
		t.Fatalf("response = %s", converted)
	}
	usage := response["usage"].(map[string]any)
	if int(usage["input_tokens"].(float64)) != 3 || int(usage["output_tokens"].(float64)) != 2 {
		t.Fatalf("usage = %#v", usage)
	}
}
