package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelResolverResolve(t *testing.T) {
	m := NewModelResolver()
	// 缓存为空：fail-safe，不替换。
	if got, changed := m.Resolve("gpt-5.4", ""); got != "gpt-5.4" || changed {
		t.Fatalf("空缓存应原样透传，got=%q changed=%v", got, changed)
	}
	m.update([]string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"})
	// 已知模型原样返回。
	if got, changed := m.Resolve("Tencent-kimi-k2.6", ""); got != "Tencent-kimi-k2.6" || changed {
		t.Fatalf("已知模型不应替换，got=%q changed=%v", got, changed)
	}
	// 未知模型且无配置兜底：替换为第一个可用模型。
	if got, changed := m.Resolve("gpt-5.4", ""); got != "Auto" || !changed {
		t.Fatalf("未知模型应替换为首项 Auto，got=%q changed=%v", got, changed)
	}
	// 空模型名不替换。
	if got, changed := m.Resolve("", ""); got != "" || changed {
		t.Fatalf("空模型名不应替换，got=%q changed=%v", got, changed)
	}
}

func TestModelResolverPreferredFallback(t *testing.T) {
	m := NewModelResolver()
	m.update([]string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"})
	// 配置的兜底模型在可用列表里：未知模型替换为它（而非第一个）。
	if got, changed := m.Resolve("gpt-5.4", "Tencent-glm-5.1"); got != "Tencent-glm-5.1" || !changed {
		t.Fatalf("应替换为配置的兜底模型 Tencent-glm-5.1，got=%q changed=%v", got, changed)
	}
	// 配置的兜底模型不在列表里：退回第一个可用模型，避免替换成无效模型。
	if got, changed := m.Resolve("gpt-5.4", "no-such-model"); got != "Auto" || !changed {
		t.Fatalf("无效兜底应退回首项 Auto，got=%q changed=%v", got, changed)
	}
	// 已知模型即使配了兜底也不替换。
	if got, changed := m.Resolve("Tencent-kimi-k2.6", "Tencent-glm-5.1"); got != "Tencent-kimi-k2.6" || changed {
		t.Fatalf("已知模型不应替换，got=%q changed=%v", got, changed)
	}
}

func TestApplyModelSubstitutionPreservesContent(t *testing.T) {
	h := &Handler{Models: NewModelResolver()}
	h.Models.update([]string{"Auto", "Tencent-glm-5.1"})

	// 含 < > & 的内容必须原样保留（不被 HTML 转义），且仅 model 字段被替换。
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"a < b && c > d"}],"stream":true}`)
	out := h.applyModelSubstitution(body, "")

	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("替换后不是合法 JSON: %v\n%s", err, out)
	}
	var model string
	_ = json.Unmarshal(got["model"], &model)
	if model != "Auto" {
		t.Fatalf("model 应替换为 Auto, got=%q (%s)", model, out)
	}
	if !strings.Contains(string(out), "a < b && c > d") {
		t.Fatalf("内容里的 <、>、& 被错误转义了: %s", out)
	}
	// stream 等其它字段保留。
	if !strings.Contains(string(out), `"stream":true`) {
		t.Fatalf("其它字段丢失: %s", out)
	}
}

func TestApplyModelSubstitutionUsesConfiguredFallback(t *testing.T) {
	h := &Handler{Models: NewModelResolver()}
	h.Models.update([]string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"})
	body := []byte(`{"model":"claude-opus-4-5","messages":[]}`)
	out := h.applyModelSubstitution(body, "Tencent-kimi-k2.6")
	var got map[string]json.RawMessage
	_ = json.Unmarshal(out, &got)
	var model string
	_ = json.Unmarshal(got["model"], &model)
	if model != "Tencent-kimi-k2.6" {
		t.Fatalf("应替换为配置的兜底模型 Tencent-kimi-k2.6, got=%q (%s)", model, out)
	}
}

func TestApplyModelSubstitutionKnownModelUnchanged(t *testing.T) {
	h := &Handler{Models: NewModelResolver()}
	h.Models.update([]string{"Auto", "Tencent-glm-5.1"})
	body := []byte(`{"model":"Tencent-glm-5.1","messages":[]}`)
	out := h.applyModelSubstitution(body, "")
	if string(out) != string(body) {
		t.Fatalf("已知模型请求体不应被改写\n原: %s\n新: %s", body, out)
	}
}

func TestApplyModelSubstitutionNilResolver(t *testing.T) {
	// Models 为 nil 时（如测试构造的 Handler）原样透传，不 panic。
	h := &Handler{}
	body := []byte(`{"model":"whatever"}`)
	if out := h.applyModelSubstitution(body, ""); string(out) != string(body) {
		t.Fatalf("nil resolver 应原样返回, got %s", out)
	}
}

func TestStripClaudeAlias(t *testing.T) {
	m := NewModelResolver()
	m.update([]string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"})
	// claude- 前缀 + 还原后是已知模型 → 剥离。
	if got := m.stripClaudeAlias("claude-Tencent-kimi-k2.6"); got != "Tencent-kimi-k2.6" {
		t.Fatalf("应剥离为 Tencent-kimi-k2.6, got=%q", got)
	}
	// claude- 前缀但还原后不是已知模型（真正的 claude 名）→ 原样保留，后续走兜底。
	if got := m.stripClaudeAlias("claude-3-5-sonnet"); got != "claude-3-5-sonnet" {
		t.Fatalf("非上游模型的 claude- 名不应剥离, got=%q", got)
	}
	// 无前缀 → 原样。
	if got := m.stripClaudeAlias("Tencent-glm-5.1"); got != "Tencent-glm-5.1" {
		t.Fatalf("无前缀不应改动, got=%q", got)
	}
}

func TestApplyModelSubstitutionStripsClaudeAlias(t *testing.T) {
	h := &Handler{Models: NewModelResolver()}
	h.Models.update([]string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"})
	// Claude Code 选了别名模型：应还原成真实上游模型，且不触发兜底。
	body := []byte(`{"model":"claude-Tencent-kimi-k2.6","messages":[]}`)
	out := h.applyModelSubstitution(body, "Tencent-glm-5.1")
	var got map[string]json.RawMessage
	_ = json.Unmarshal(out, &got)
	var model string
	_ = json.Unmarshal(got["model"], &model)
	if model != "Tencent-kimi-k2.6" {
		t.Fatalf("别名应还原为 Tencent-kimi-k2.6（而非兜底）, got=%q (%s)", model, out)
	}
}

func TestAddClaudeModelAlias(t *testing.T) {
	body := []byte(`{"object":"list","data":[{"id":"Tencent-kimi-k2.6","contextWindow":200000,"supportsImages":true},{"id":"Auto"}]}`)
	out, ok := addClaudeModelAlias(body)
	if !ok {
		t.Fatal("改写应成功")
	}
	var payload struct {
		Object string `json:"object"`
		Data   []struct {
			ID             string `json:"id"`
			DisplayName    string `json:"display_name"`
			ContextWindow  int    `json:"contextWindow"`
			SupportsImages bool   `json:"supportsImages"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("改写后不是合法 JSON: %v\n%s", err, out)
	}
	if payload.Object != "list" {
		t.Fatalf("顶层其它字段应保留, got object=%q", payload.Object)
	}
	if len(payload.Data) != 2 {
		t.Fatalf("模型数应为 2, got=%d", len(payload.Data))
	}
	// id 加前缀，display_name 补成原始 id，其它字段保留。
	if payload.Data[0].ID != "claude-Tencent-kimi-k2.6" || payload.Data[0].DisplayName != "Tencent-kimi-k2.6" {
		t.Fatalf("首项改写不对: %+v", payload.Data[0])
	}
	if payload.Data[0].ContextWindow != 200000 || !payload.Data[0].SupportsImages {
		t.Fatalf("原有字段应保留: %+v", payload.Data[0])
	}
	if payload.Data[1].ID != "claude-Auto" || payload.Data[1].DisplayName != "Auto" {
		t.Fatalf("次项改写不对: %+v", payload.Data[1])
	}
}
