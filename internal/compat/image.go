package compat

import (
	"net/http"
	"strings"
)

// validateUpstreamImageURL 限制图片为 data URL。CoStrict 当前视觉通道明确拒绝远程 URL；
// Router 也不代为下载，避免引入 SSRF 与内网资源探测风险。
func validateUpstreamImageURL(url string) (string, error) {
	if url == "" {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(url), "data:") {
		return url, nil
	}
	return "", newAPIError(http.StatusBadRequest, "invalid_request_error", "CoStrict 上游仅支持 base64/data URL 图片，不支持远程图片 URL")
}
