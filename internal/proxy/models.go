package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"costrict-router/internal/config"
	"costrict-router/internal/i18n"
	"costrict-router/internal/version"
)

// claudeModelPrefix 是给 Claude Code 的模型别名前缀。Claude Code 的 gatewayDiscovery 只把
// id 以 claude/anthropic 开头的模型加入 /model 选择器，所以对它返回的 /v1/models 会给每个
// 上游模型加上该前缀；入站请求再按需把前缀剥掉还原成真实上游模型名。
const claudeModelPrefix = "claude-"

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

// stripClaudeAlias 把 Claude Code 的 claude- 别名前缀还原成真实上游模型名：
// 仅当去掉前缀后确实是已知上游模型时才剥离，否则原样返回（保留真正的 claude-* 名走兜底）。
func (m *ModelResolver) stripClaudeAlias(requested string) string {
	if !strings.HasPrefix(requested, claudeModelPrefix) {
		return requested
	}
	stripped := strings.TrimPrefix(requested, claudeModelPrefix)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.known[stripped]; ok {
		return stripped
	}
	return requested
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
	req.Header.Set("User-Agent", "costrict-router/"+version.Current)
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

// applyModelSubstitution 规整请求体里的模型名：先把 Claude Code 的 claude- 别名还原成真实
// 上游模型，再对仍未知的模型名替换为兜底模型（用户配置的 preferredFallback 或第一个可用模型），
// 并在 debug 下记录。返回（可能改写后的）请求体；解析失败或无需改写时原样返回。
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
	// 先剥别名前缀（仅当还原后是真实上游模型），再做兜底解析。
	requested := h.Models.stripClaudeAlias(probe.Model)
	resolved, _ := h.Models.Resolve(requested, preferredFallback)
	if resolved == probe.Model {
		return body
	}
	out, err := replaceJSONModel(body, resolved)
	if err != nil {
		return body
	}
	if h.Logger != nil && h.Logger.DebugEnabled() {
		h.Logger.Debugf(i18n.T("resolved model %q -> %q", "模型已规整 %q -> %q"), probe.Model, resolved)
	}
	return out
}

// addClaudeModelAlias 改写上游 /v1/models 响应：给每个模型 id 加 claude- 前缀，并在缺省时
// 用原始 id 补 display_name（Claude Code /model 选择器显示 display_name）。其它字段原样保留。
// 返回改写后的响应体；解析失败时返回 (nil,false) 由调用方退回原始响应。
func addClaudeModelAlias(body []byte) ([]byte, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	dataRaw, ok := payload["data"]
	if !ok {
		return nil, false
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(dataRaw, &items); err != nil {
		return nil, false
	}
	for _, item := range items {
		idRaw, ok := item["id"]
		if !ok {
			continue
		}
		var id string
		if err := json.Unmarshal(idRaw, &id); err != nil || id == "" {
			continue
		}
		if _, has := item["display_name"]; !has {
			if dn, err := json.Marshal(id); err == nil {
				item["display_name"] = dn
			}
		}
		if newID, err := json.Marshal(claudeModelPrefix + id); err == nil {
			item["id"] = newID
		}
	}
	newData, err := json.Marshal(items)
	if err != nil {
		return nil, false
	}
	payload["data"] = newData
	// 关闭 HTML 转义，避免模型名/描述里的 <、>、& 被转义。
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
}

// convertToAnthropicModelsFormat 将 OpenAI 风格的 /v1/models 响应转为 Anthropic 风格，
// 使 Claude Code 的 gateway model discovery（CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1）
// 能正确解析。Anthropic SDK 的 models.list() 要求 Anthropic 格式：
//
//	{"data":[{"id":"...","type":"model","display_name":"...","created_at":"RFC3339","max_input_tokens":N,"max_tokens":N}], "has_more":false, "first_id":"...", "last_id":"..."}
//
// OpenAI 风格（上游返回）用的是 object/created/contextWindow/maxTokens，SDK 无法解析。
// 解析失败时返回 (nil, false)，调用方退回原始响应。
func convertToAnthropicModelsFormat(body []byte) ([]byte, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	dataRaw, ok := payload["data"]
	if !ok {
		return nil, false
	}

	// 上游模型项（OpenAI 风格）
	type upstreamModel struct {
		ID                    string `json:"id"`
		Object                string `json:"object,omitempty"`
		Created               int64  `json:"created,omitempty"`
		OwnedBy               string `json:"owned_by,omitempty"`
		ContextWindow         int    `json:"contextWindow,omitempty"`
		MaxTokens             int    `json:"maxTokens,omitempty"`
		DisplayName           string `json:"display_name,omitempty"`
		Description           string `json:"description,omitempty"`
		MaxTokensKey          string `json:"maxTokensKey,omitempty"`
		SupportsImages        bool   `json:"supportsImages,omitempty"`
		SupportsComputerUse   bool   `json:"supportsComputerUse,omitempty"`
		SupportsPromptCache   bool   `json:"supportsPromptCache,omitempty"`
		SupportsReasoningBudget bool  `json:"supportsReasoningBudget,omitempty"`
		RequiredReasoningBudget bool `json:"requiredReasoningBudget,omitempty"`
		CreditConsumption     int    `json:"creditConsumption,omitempty"`
		CreditDiscount        int    `json:"creditDiscount,omitempty"`
	}

	// Anthropic 风格模型项
	type anthropicModel struct {
		ID              string `json:"id"`
		Type            string `json:"type"`
		DisplayName     string `json:"display_name"`
		CreatedAt       string `json:"created_at"`
		MaxInputTokens  int    `json:"max_input_tokens"`
		MaxTokens       int    `json:"max_tokens"`
	}

	var upstream []upstreamModel
	if err := json.Unmarshal(dataRaw, &upstream); err != nil {
		return nil, false
	}

	items := make([]anthropicModel, 0, len(upstream))
	for _, m := range upstream {
		if m.ID == "" {
			continue
		}
		// unix timestamp → RFC 3339；缺省或零值用当前时间。
		var createdAt string
		if m.Created > 0 {
			createdAt = time.Unix(m.Created, 0).UTC().Format(time.RFC3339)
		} else {
			createdAt = time.Now().UTC().Format(time.RFC3339)
		}
		items = append(items, anthropicModel{
			ID:             m.ID,
			Type:           "model",
			DisplayName:    m.DisplayName,
			CreatedAt:      createdAt,
			MaxInputTokens: m.ContextWindow,
			MaxTokens:      m.MaxTokens,
		})
	}

	dataOut, err := json.Marshal(items)
	if err != nil {
		return nil, false
	}
	payload["data"] = dataOut
	// 移除 OpenAI 风格的顶层 object 字段
	delete(payload, "object")
	// 补齐 Anthropic 风格的顶层分页字段
	if raw, err := json.Marshal(false); err == nil {
		payload["has_more"] = raw
	}
	if len(items) > 0 {
		if raw, err := json.Marshal(items[0].ID); err == nil {
			payload["first_id"] = raw
		}
		if raw, err := json.Marshal(items[len(items)-1].ID); err == nil {
			payload["last_id"] = raw
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, false
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true
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
