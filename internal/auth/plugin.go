package auth

import (
	"net/http"
	"strconv"
	"strings"
)

// 以下常量对齐 CoStrict 官方 VS Code 扩展（zgsm-ai/costrict）上报给上游的客户端身份，
// 上游通过登录参数和请求头识别客户端版本，跟进上游发版时需同步更新。
const (
	// DefaultPluginVersion 对应扩展 src/package.json 的 version，用于登录参数和 X-Costrict-Version 头。
	DefaultPluginVersion = "3.0.23"
	// DefaultVSCodeVersion 对应扩展登录参数里的 vscode.version，取 VS Code 当前稳定版。
	DefaultVSCodeVersion = "1.143.0"
	// RooCodeUserAgent 对应扩展 src/api/providers/constants.ts 中 DEFAULT_HEADERS 的 User-Agent。
	RooCodeUserAgent = "RooCode/3.52.1"
	// EditorType 对应扩展 x-costrict-idea 头，本地 VS Code 稳定版取值为 vscode.env.appName。
	EditorType = "Visual Studio Code"
)

// PluginUserAgent 返回扩展对话请求使用的 User-Agent，形如 "RooCode/3.52.1 plugin_vscode/3.0.23"。
func PluginUserAgent(pluginVersion string) string {
	return RooCodeUserAgent + " plugin_vscode/" + NormalizePluginVersion(pluginVersion)
}

// ApplyDefaultHeaders 写入扩展 DEFAULT_HEADERS 中的公共头，模型列表等非对话请求只带这一组。
func ApplyDefaultHeaders(h http.Header, pluginVersion string) {
	h.Set("HTTP-Referer", "https://github.com/RooVetGit/Roo-Cline")
	h.Set("X-Title", "Roo Code")
	h.Set("User-Agent", RooCodeUserAgent)
	h.Set("X-Costrict-Version", NormalizePluginVersion(pluginVersion))
}

// NormalizePluginVersion 在版本为空、无法解析或低于内置版本时返回 DefaultPluginVersion，
// 避免旧配置里持久化的历史版本号一直被带给上游；更高的版本按原样保留。
func NormalizePluginVersion(v string) string {
	return atLeastVersion(v, DefaultPluginVersion)
}

// NormalizeVSCodeVersion 与 NormalizePluginVersion 规则一致，下限为 DefaultVSCodeVersion。
func NormalizeVSCodeVersion(v string) string {
	return atLeastVersion(v, DefaultVSCodeVersion)
}

func atLeastVersion(v, floor string) string {
	v = strings.TrimSpace(v)
	if compareVersion(v, floor) < 0 {
		return floor
	}
	return v
}

// compareVersion 按点分数字逐段比较版本号；无法解析的版本视为最低。
func compareVersion(a, b string) int {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	switch {
	case !okA && !okB:
		return 0
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVersion(v string) ([]int, bool) {
	if v == "" {
		return nil, false
	}
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}
