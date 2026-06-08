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
