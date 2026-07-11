package compat

import (
	"encoding/json"
	"strings"
)

// normalizeToolArguments 保留合法对象；若模型在对象后重复输出内容，则提取第一个合法对象。
// 完全无法解析时退回空对象，避免 Agent 和上游因畸形历史反复重试。
func normalizeToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return "{}"
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(arguments))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return "{}"
	}
	out, err := jsonMarshal(object)
	if err != nil {
		return "{}"
	}
	return string(out)
}
