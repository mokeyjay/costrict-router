package compat

import (
	"encoding/json"
	"strings"
)

// ModelPolicyOverride 允许用户按模型关闭 Router 的某项默认兼容降级。
// 上游修复限制的速度快于本工具的发版节奏，用户复查确认后改配置即可生效，无需等新版本。
type ModelPolicyOverride struct {
	// KeepToolChoice 为 true 时不再把 tool_choice=required/指定函数降级为 auto。
	KeepToolChoice bool `json:"keep_tool_choice,omitempty"`
	// KeepResponseFormat 为 true 时不再在带工具时移除 response_format。
	KeepResponseFormat bool `json:"keep_response_format,omitempty"`
	// KeepTemperature 为 true 时不再在结构化输出时省略显式温度。
	KeepTemperature bool `json:"keep_temperature,omitempty"`
}

// ApplyUpstreamModelPolicy 在模型兜底替换完成后应用 CoStrict 当前各模型的兼容策略。
// 这些差异来自真实上游验证，需与项目根目录 AGENTS.md 的复查清单保持同步。
// overrides 按模型名（不区分大小写）覆盖默认策略，键来自配置文件 model_policy_overrides。
func ApplyUpstreamModelPolicy(chatBody []byte, overrides map[string]ModelPolicyOverride) ([]byte, error) {
	// 使用 RawMessage map 只改必要字段，保持原生 /chat/completions 的未知扩展参数透明透传。
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &fields); err != nil {
		return nil, err
	}
	var requestedModel string
	_ = json.Unmarshal(fields["model"], &requestedModel)
	var tools []json.RawMessage
	_ = json.Unmarshal(fields["tools"], &tools)
	var stream bool
	_ = json.Unmarshal(fields["stream"], &stream)

	model := strings.ToLower(requestedModel)
	hasTools := len(tools) > 0
	responseFormat, hasResponseFormat := fields["response_format"]
	hasResponseFormat = hasResponseFormat && len(responseFormat) > 0 && string(responseFormat) != "null"
	ov := lookupPolicyOverride(overrides, requestedModel)

	switch {
	case strings.Contains(model, "deepseek"):
		// DeepSeek thinking 模式拒绝 required 和指定函数对象。
		if !ov.KeepToolChoice {
			forceCompatibleAutoToolChoice(fields)
		}
		if hasTools && !ov.KeepResponseFormat {
			delete(fields, "response_format")
		}
	case strings.Contains(model, "kimi"):
		// Kimi 非流式可接受 required，但流式 thinking 模式会拒绝；指定函数同样降级。
		if stream && !ov.KeepToolChoice {
			forceCompatibleAutoToolChoice(fields)
		}
		if hasTools && !ov.KeepResponseFormat {
			delete(fields, "response_format")
		}
		// Kimi 的结构化输出通道只接受固定温度；省略后交给上游使用合法默认值。
		if hasResponseFormat && !ov.KeepTemperature {
			var temperature float64
			if raw, ok := fields["temperature"]; ok && json.Unmarshal(raw, &temperature) == nil && temperature != 1 {
				delete(fields, "temperature")
			}
		}
	case model == "auto":
		// Auto 可能路由到 Kimi/DeepSeek，优先保证工具链不会被上游直接拒绝。
		if !ov.KeepToolChoice {
			forceCompatibleAutoToolChoice(fields)
		}
		if hasTools && !ov.KeepResponseFormat {
			delete(fields, "response_format")
		}
		if hasResponseFormat && !ov.KeepTemperature {
			delete(fields, "temperature")
		}
	}

	return jsonMarshal(fields)
}

// lookupPolicyOverride 按模型名不区分大小写地查找用户覆盖。
func lookupPolicyOverride(overrides map[string]ModelPolicyOverride, model string) ModelPolicyOverride {
	for name, ov := range overrides {
		if strings.EqualFold(name, model) {
			return ov
		}
	}
	return ModelPolicyOverride{}
}

// ChatParallelToolCalls 返回请求最终允许的并行工具设置；未显式设置时按 OpenAI 默认值 true。
func ChatParallelToolCalls(chatBody []byte) bool {
	var req struct {
		ParallelToolCalls *bool `json:"parallel_tool_calls"`
	}
	if err := json.Unmarshal(chatBody, &req); err != nil || req.ParallelToolCalls == nil {
		return true
	}
	return *req.ParallelToolCalls
}

func forceCompatibleAutoToolChoice(fields map[string]json.RawMessage) {
	raw, ok := fields["tool_choice"]
	if !ok {
		return
	}
	var choice string
	if err := json.Unmarshal(raw, &choice); err == nil {
		if choice == "required" {
			fields["tool_choice"] = json.RawMessage(`"auto"`)
		}
		return
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil && len(object) > 0 {
		fields["tool_choice"] = json.RawMessage(`"auto"`)
	}
}
