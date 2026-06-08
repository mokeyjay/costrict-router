package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"costrict-router/internal/config"
	"costrict-router/internal/i18n"
)

// ModelResolver 缓存上游可用模型列表，把未知模型名映射到第一个可用模型。
//
// 动机：codex 的 multi_agent / guardian 审查 / 记忆生成等辅助功能会用各自的内部模型名
// （如 gpt-5.4、gpt-5.4-mini）发请求，claude-code 会用 claude-* 模型名，这些上游都没有。
// 统一把未知模型替换成第一个可用模型，避免这些功能因模型不存在而失败。
type ModelResolver struct {
	mu          sync.RWMutex
	known       map[string]struct{}
	fallback    string // 第一个可用模型（上游 /models 列表的首项）
	fetchedAt   time.Time
	lastAttempt time.Time

	fetchMu sync.Mutex // 串行化刷新，避免并发重复拉取
	ttl     time.Duration
	retry   time.Duration
}

// NewModelResolver 创建一个解析器：成功后缓存 ttl，期间不重复拉取；
// 尚未加载成功时按 retry 间隔重试，避免持续 hammering 上游。
func NewModelResolver() *ModelResolver {
	return &ModelResolver{ttl: 10 * time.Minute, retry: time.Minute}
}

// Resolve 返回应转发给上游的模型名：已知模型原样返回；未知模型返回兜底模型。
// 兜底优先级：用户配置的 preferredFallback（仅当它当前确实在可用列表里）> 第一个可用模型。
// 第二个返回值表示是否发生了替换。缓存为空（未加载或拉取失败）时不替换，原样透传，保证 fail-safe。
func (m *ModelResolver) Resolve(requested, preferredFallback string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if requested == "" || len(m.known) == 0 {
		return requested, false
	}
	if _, ok := m.known[requested]; ok {
		return requested, false
	}
	fallback := m.fallback
	if preferredFallback != "" {
		if _, ok := m.known[preferredFallback]; ok {
			fallback = preferredFallback
		}
		// 配置的兜底模型当前不可用时，退回第一个可用模型，避免替换成无效模型。
	}
	if fallback == "" {
		return requested, false
	}
	return fallback, true
}

// shouldFetch 判断是否需要刷新缓存：未加载时按 retry 间隔重试，已加载时按 ttl 过期刷新。
func (m *ModelResolver) shouldFetch() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	now := time.Now()
	if len(m.known) == 0 {
		return now.Sub(m.lastAttempt) >= m.retry
	}
	return now.Sub(m.fetchedAt) >= m.ttl
}

func (m *ModelResolver) update(ids []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	known := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			known[id] = struct{}{}
		}
	}
	m.known = known
	if len(ids) > 0 {
		m.fallback = ids[0]
	}
	m.fetchedAt = time.Now()
	m.lastAttempt = m.fetchedAt
}

func (m *ModelResolver) markAttempt() {
	m.mu.Lock()
	m.lastAttempt = time.Now()
	m.mu.Unlock()
}

// ensureModels 按需刷新上游模型缓存（带 TTL 与失败退避，刷新经 fetchMu 串行化 + 双重检查）。
func (h *Handler) ensureModels(ctx context.Context, cfg config.Config) {
	if h.Models == nil || !h.Models.shouldFetch() {
		return
	}
	h.Models.fetchMu.Lock()
	defer h.Models.fetchMu.Unlock()
	if !h.Models.shouldFetch() {
		return // 已被其它请求刷新
	}
	h.Models.markAttempt()
	ids, err := h.fetchModelIDs(ctx, cfg)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warnf(i18n.T("failed to refresh model list for substitution: %v", "刷新模型替换列表失败: %v"), err)
		}
		return
	}
	h.Models.update(ids)
}

// fetchModelIDs 拉取上游可用模型 ID 列表（保持原始顺序，首项即 fallback）。
func (h *Handler) fetchModelIDs(ctx context.Context, cfg config.Config) ([]string, error) {
	upstreamURL, err := joinURL(cfg.BaseURL, "/ai-gateway/api/v1/models")
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, upstreamURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AccessToken)
	req.Header.Set("x-user-id", cfg.UserID)
	req.Header.Set("User-Agent", "costrict-router/"+Version)
	resp, err := h.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if model.ID != "" {
			ids = append(ids, model.ID)
		}
	}
	return ids, nil
}

// applyModelSubstitution 若请求体里的模型名未知，则替换为兜底模型（用户配置的 preferredFallback
// 或第一个可用模型），并在 debug 下记录。返回（可能改写后的）请求体；解析失败或无需替换时原样返回。
func (h *Handler) applyModelSubstitution(body []byte, preferredFallback string) []byte {
	if h.Models == nil {
		return body
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
		return body
	}
	resolved, changed := h.Models.Resolve(probe.Model, preferredFallback)
	if !changed {
		return body
	}
	out, err := replaceJSONModel(body, resolved)
	if err != nil {
		return body
	}
	if h.Logger != nil && h.Logger.DebugEnabled() {
		h.Logger.Debugf(i18n.T("substituted unknown model %q -> %q", "未知模型已替换 %q -> %q"), probe.Model, resolved)
	}
	return out
}

// replaceJSONModel 仅替换顶层 model 字段，其它字段以 json.RawMessage 原样保留（不重新转义内容）。
func replaceJSONModel(body []byte, model string) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	fields["model"] = raw
	// 关闭 HTML 转义，避免把其它字段里的 <、>、& 误转义（json.RawMessage 默认会被编码器转义）。
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(fields); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
