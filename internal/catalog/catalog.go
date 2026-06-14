// Package catalog 生成 codex 的 model_catalog_json 文件内容。
//
// 背景：codex 默认只在 provider 配了 command-auth 或使用 codex 后端时才会拉取 /models；
// 普通 api-key provider 不会拉取，导致第三方模型不出现在 codex `/model` 选择器里。
// codex 提供了 `model_catalog_json` 配置项：指向一个本地 {"models":[ModelInfo,...]} 文件后，
// codex 改用 StaticModelsManager 直接以该文件为权威目录（不发网络请求、不受上述门槛限制），
// 选择器即可显示文件里的模型。本包负责把上游模型 ID 列表转成该文件所需的格式。
package catalog

import (
	_ "embed"
	"encoding/json"
)

// codexBaseInstructions 是 codex 自带的编码 agent 系统提示（models-manager/prompt.md 的快照）。
//
// 为何必须填它：codex 取某模型元数据时按 slug 最长前缀匹配目录项，并把命中项的 base_instructions
// 原样当作请求的系统提示。若留空，选中该模型时主对话的系统提示会被清空。这里复用 codex 自身的
// 提示，使行为与 codex 对未知模型的 model_info_from_slug 回退一致。
//
// 注意：此文件是 codex 某版本的快照，会随 codex 升级漂移；codex 更新提示词时需同步更新。
//
//go:embed codex_base_instructions.md
var codexBaseInstructions string

// defaultContextWindow 是上游未提供上下文窗口时的兜底值（与 codex 的 model_info_from_slug 一致）。
const defaultContextWindow int64 = 272_000

// SourceModel 是构建目录所需的单个上游模型信息（来自 CoStrict /models 接口）。
type SourceModel struct {
	ID             string // 模型 ID
	ContextWindow  int64  // 上下文窗口；<=0 时使用默认值
	SupportsImages bool   // 是否支持图片输入
}

// ModelsResponse 是 codex `model_catalog_json` 文件的顶层结构（对应 codex 的 ModelsResponse）。
type ModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// TruncationPolicy 对应 codex 的 TruncationPolicyConfig。
type TruncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int64  `json:"limit"`
}

// ModelInfo 仅包含 codex ModelInfo 反序列化所需的字段，取值镜像 codex 自身的
// model_info_from_slug 回退（仅把 visibility 设为 list，使模型出现在 codex /model 选择器里）。
// 省略的字段都是 Option 或带 serde(default)，缺省即可。
type ModelInfo struct {
	Slug                          string           `json:"slug"`
	DisplayName                   string           `json:"display_name"`
	SupportedReasoningLevels      []struct{}       `json:"supported_reasoning_levels"`
	ShellType                     string           `json:"shell_type"`
	Visibility                    string           `json:"visibility"`
	SupportedInAPI                bool             `json:"supported_in_api"`
	Priority                      int              `json:"priority"`
	BaseInstructions              string           `json:"base_instructions"`
	SupportsReasoningSummaries    bool             `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary       string           `json:"default_reasoning_summary"`
	SupportVerbosity              bool             `json:"support_verbosity"`
	TruncationPolicy              TruncationPolicy `json:"truncation_policy"`
	SupportsParallelToolCalls     bool             `json:"supports_parallel_tool_calls"`
	ContextWindow                 int64            `json:"context_window"`
	MaxContextWindow              int64            `json:"max_context_window"`
	EffectiveContextWindowPercent int              `json:"effective_context_window_percent"`
	ExperimentalSupportedTools    []string         `json:"experimental_supported_tools"`
	InputModalities               []string         `json:"input_modalities"`
}

// Build 把上游模型列表转成 codex model_catalog_json 期望的目录结构。
// 上下文窗口与图片能力取自上游真实数据；其余字段镜像 codex 的 model_info_from_slug。
// priority 用列表序号以保持选择器顺序。
func Build(models []SourceModel) ModelsResponse {
	out := make([]ModelInfo, 0, len(models))
	for i, m := range models {
		ctx := m.ContextWindow
		if ctx <= 0 {
			ctx = defaultContextWindow
		}
		modalities := []string{"text"}
		if m.SupportsImages {
			modalities = []string{"text", "image"}
		}
		out = append(out, ModelInfo{
			Slug:                          m.ID,
			DisplayName:                   m.ID,
			SupportedReasoningLevels:      []struct{}{},
			ShellType:                     "default",
			Visibility:                    "list",
			SupportedInAPI:                true,
			Priority:                      i,
			BaseInstructions:              codexBaseInstructions,
			SupportsReasoningSummaries:    false,
			DefaultReasoningSummary:       "auto",
			SupportVerbosity:              false,
			TruncationPolicy:              TruncationPolicy{Mode: "bytes", Limit: 10_000},
			SupportsParallelToolCalls:     false,
			ContextWindow:                 ctx,
			MaxContextWindow:              ctx,
			EffectiveContextWindowPercent: 95,
			ExperimentalSupportedTools:    []string{},
			InputModalities:               modalities,
		})
	}
	return ModelsResponse{Models: out}
}

// MarshalJSON 生成可写入文件的缩进 JSON（带末尾换行）。
func (r ModelsResponse) MarshalIndented() ([]byte, error) {
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
