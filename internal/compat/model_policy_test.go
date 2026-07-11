package compat

import (
	"encoding/json"
	"testing"
)

func TestApplyUpstreamModelPolicy(t *testing.T) {
	tests := []struct {
		name               string
		body               string
		wantChoice         any
		wantResponseFormat bool
		wantTemperature    bool
	}{
		{
			name:               "DeepSeek 强制工具降级并移除结构约束",
			body:               `{"model":"Tencent-deepseek-v4-pro","messages":[],"tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"required","response_format":{"type":"json_object"}}`,
			wantChoice:         "auto",
			wantResponseFormat: false,
		},
		{
			name:               "Kimi 流式强制工具降级并移除非法温度",
			body:               `{"model":"Tencent-kimi-k2.6","messages":[],"stream":true,"temperature":0,"tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"required","response_format":{"type":"json_object"}}`,
			wantChoice:         "auto",
			wantResponseFormat: false,
			wantTemperature:    false,
		},
		{
			name:               "MiniMax 保留工具与结构约束",
			body:               `{"model":"Tencent-minimax-m2.7","messages":[],"temperature":0,"tools":[{"type":"function","function":{"name":"f"}}],"tool_choice":"required","response_format":{"type":"json_object"}}`,
			wantChoice:         "required",
			wantResponseFormat: true,
			wantTemperature:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, err := ApplyUpstreamModelPolicy([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			var request map[string]any
			if err := json.Unmarshal(out, &request); err != nil {
				t.Fatal(err)
			}
			if request["tool_choice"] != test.wantChoice {
				t.Fatalf("tool_choice = %v", request["tool_choice"])
			}
			_, hasFormat := request["response_format"]
			if hasFormat != test.wantResponseFormat {
				t.Fatalf("response_format exists = %t: %s", hasFormat, out)
			}
			_, hasTemperature := request["temperature"]
			if hasTemperature != test.wantTemperature {
				t.Fatalf("temperature exists = %t: %s", hasTemperature, out)
			}
		})
	}
}

func TestChatParallelToolCallsDefaultAndExplicit(t *testing.T) {
	if !ChatParallelToolCalls([]byte(`{"model":"m"}`)) {
		t.Fatal("未设置时应使用默认值 true")
	}
	if ChatParallelToolCalls([]byte(`{"parallel_tool_calls":false}`)) {
		t.Fatal("显式 false 应被保留")
	}
}

func TestApplyUpstreamModelPolicyPreservesUnknownChatFields(t *testing.T) {
	body := []byte(`{"model":"Tencent-deepseek-v4-pro","messages":[],"tool_choice":"required","seed":42,"frequency_penalty":0.5,"vendor_extension":{"x":1}}`)
	out, err := ApplyUpstreamModelPolicy(body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"seed", "frequency_penalty", "vendor_extension"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("字段 %s 被错误丢弃: %s", name, out)
		}
	}
}
