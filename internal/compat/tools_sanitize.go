package compat

import (
	"encoding/json"
	"net/http"
	"strings"
)

// CoStrict 网关只接受 type=function 的工具（2026-07 实测：GLM 式 web_search、
// Kimi 式 builtin_function、retrieval 等一律被网关 400 拒绝，报 "tools[0].type is invalid"）。
// 本文件负责在三个入口统一处理不支持的工具类型：能剔除就剔除；全部被剔除时返回
// 明确错误——既避免上游含混的 channel_error，也阻断“无工具却被暗示搜索”时模型
// 幻觉输出原生工具标记（GLM 尤甚）污染 Agent 上下文。

// errAllToolsDropped 生成“工具全部不可用”的统一错误。
func errAllToolsDropped(droppedTypes []string) error {
	detail := ""
	if len(droppedTypes) > 0 {
		detail = "（" + strings.Join(dedupeStrings(droppedTypes), ", ") + "）"
	}
	return newAPIError(http.StatusBadRequest, "invalid_request_error",
		"上游仅支持自定义 function 工具，不支持服务端内置工具"+detail+
			"；本次请求的工具全部属于不支持的类型，无法执行。CoStrict 上游没有服务端联网搜索/检索能力")
}

// SanitizeChatTools 过滤 /v1/chat/completions 透传请求中上游不支持的工具类型。
// 混合场景剔除后放行；全部被剔除时返回明确错误。tools 形状异常时原样透传，交给上游报错。
func SanitizeChatTools(chatBody []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(chatBody, &fields); err != nil {
		return chatBody, nil
	}
	rawTools, ok := fields["tools"]
	if !ok || len(rawTools) == 0 || string(rawTools) == "null" {
		return chatBody, nil
	}
	var tools []json.RawMessage
	if err := json.Unmarshal(rawTools, &tools); err != nil || len(tools) == 0 {
		return chatBody, nil
	}
	kept := make([]json.RawMessage, 0, len(tools))
	var droppedTypes []string
	for _, tool := range tools {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(tool, &probe)
		// type 缺失时按 function 宽松处理，保持与上游一致的最大兼容。
		if probe.Type == "" || probe.Type == "function" {
			kept = append(kept, tool)
			continue
		}
		droppedTypes = append(droppedTypes, probe.Type)
	}
	if len(droppedTypes) == 0 {
		return chatBody, nil
	}
	if len(kept) == 0 {
		return nil, errAllToolsDropped(droppedTypes)
	}
	filtered, err := jsonMarshal(kept)
	if err != nil {
		return chatBody, nil
	}
	fields["tools"] = filtered
	return jsonMarshal(fields)
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
