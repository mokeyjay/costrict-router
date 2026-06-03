package compat

import (
	"io"
	"strings"
	"testing"
)

func TestTransformChatCompletionStreamToResponses(t *testing.T) {
	upstream := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
		"data: [DONE]\n\n")
	body, err := io.ReadAll(TransformChatCompletionStreamToResponses(upstream, "glm-5"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"event: response.created", "event: response.output_item.added", "event: response.output_text.delta", "event: response.completed", `"input_tokens":3`, `"sequence_number"`, `"created_at"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in stream:\n%s", want, text)
		}
	}
}

func TestTransformChatCompletionStreamToAnthropic(t *testing.T) {
	upstream := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n" +
		"data: [DONE]\n\n")
	body, err := io.ReadAll(TransformChatCompletionStreamToAnthropic(upstream, "claude-test"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta", "event: message_delta", "event: message_stop", `"input_tokens":3`, `"model":"claude-test"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in stream:\n%s", want, text)
		}
	}
}

func TestTransformStreamSurfacesReasoning(t *testing.T) {
	upstream := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
		"data: [DONE]\n\n"

	resp, err := io.ReadAll(TransformChatCompletionStreamToResponses(strings.NewReader(upstream), "m"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"event: response.reasoning_summary_text.delta", `"type":"reasoning"`} {
		if !strings.Contains(string(resp), want) {
			t.Fatalf("responses stream missing %q:\n%s", want, resp)
		}
	}

	anth, err := io.ReadAll(TransformChatCompletionStreamToAnthropic(strings.NewReader(upstream), "m"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"type":"thinking"`, `"type":"thinking_delta"`, `"type":"signature_delta"`} {
		if !strings.Contains(string(anth), want) {
			t.Fatalf("anthropic stream missing %q:\n%s", want, anth)
		}
	}
}

func TestTransformStreamKeepsToolCallIndexAcrossChunks(t *testing.T) {
	upstream := strings.NewReader("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"x\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n")
	body, err := io.ReadAll(TransformChatCompletionStreamToResponses(upstream, "glm-5"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Count(text, `"item_id":"call_1"`) != 3 {
		t.Fatalf("tool call id was not preserved across chunks:\n%s", text)
	}
	if strings.Count(text, "event: response.function_call_arguments.delta") != 2 {
		t.Fatalf("unexpected tool argument deltas:\n%s", text)
	}
}
