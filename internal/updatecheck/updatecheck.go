// Package updatecheck 异步检查 GitHub 上的最新 Release，不影响主服务启动。
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"costrict-router/internal/i18n"
	"costrict-router/internal/logx"
)

const (
	latestReleaseAPI = "https://api.github.com/repos/mokeyjay/costrict-router/releases/latest"
	LatestReleaseURL = "https://github.com/mokeyjay/costrict-router/releases/latest"
	checkTimeout     = 8 * time.Second
)

type release struct {
	Name    string `json:"name"`
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

type semanticVersion struct {
	major int
	minor int
	patch int
}

// Start 在后台检查一次最新版本。开发构建没有合法 Release 版本号时直接跳过。
func Start(ctx context.Context, logger *logx.Logger, current string) {
	if logger == nil {
		return
	}
	if _, ok := parseSemanticVersion(current); !ok {
		logger.Debugf(i18n.T("skip update check for development version %q", "开发版本 %q 跳过更新检查"), current)
		return
	}

	go func() {
		checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		defer cancel()

		latest, updateAvailable, err := check(checkCtx, http.DefaultClient, latestReleaseAPI, current)
		if err != nil {
			// 更新检查属于非关键辅助功能，网络失败不能干扰服务启动或制造普通级别噪音。
			logger.Debugf(i18n.T("update check failed: %v", "更新检查失败: %v"), err)
			return
		}
		if !updateAvailable {
			return
		}

		logger.Warnf(i18n.T(
			"a new version is available: %s (current: %s); please update manually: %s",
			"发现新版本 %s（当前版本: %s），请手动下载更新: %s"),
			latest, current, LatestReleaseURL)
	}()
}

// check 获取 Release 标题并与当前版本比较。endpoint 参数用于隔离网络测试。
func check(ctx context.Context, client *http.Client, endpoint, current string) (string, bool, error) {
	currentVersion, ok := parseSemanticVersion(current)
	if !ok {
		return "", false, fmt.Errorf("invalid current version %q", current)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "costrict-router/"+current)

	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var latestRelease release
	if err := json.NewDecoder(resp.Body).Decode(&latestRelease); err != nil {
		return "", false, err
	}
	latest := strings.TrimSpace(latestRelease.Name)
	if latest == "" {
		latest = strings.TrimSpace(latestRelease.TagName)
	}
	latestVersion, ok := parseSemanticVersion(latest)
	if !ok {
		return "", false, fmt.Errorf("invalid latest release version %q", latest)
	}

	return latest, latestVersion.newerThan(currentVersion), nil
}

// parseSemanticVersion 支持 v0.3、0.3、v0.3.1；带连字符后缀的预发布版本不参与比较。
func parseSemanticVersion(raw string) (semanticVersion, bool) {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "-") {
		return semanticVersion{}, false
	}
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "v"))
	parts := strings.Split(raw, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semanticVersion{}, false
	}

	numbers := [3]int{}
	for i, part := range parts {
		if part == "" {
			return semanticVersion{}, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return semanticVersion{}, false
		}
		numbers[i] = value
	}
	return semanticVersion{major: numbers[0], minor: numbers[1], patch: numbers[2]}, true
}

func (v semanticVersion) newerThan(other semanticVersion) bool {
	if v.major != other.major {
		return v.major > other.major
	}
	if v.minor != other.minor {
		return v.minor > other.minor
	}
	return v.patch > other.patch
}
