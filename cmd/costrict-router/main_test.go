package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"costrict-router/internal/config"
)

func TestEnsureLocalAPIKeyGeneratesAndDoesNotRepeat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()

	var out bytes.Buffer
	apiKey, err := ensureLocalAPIKey(path, &cfg, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(apiKey, "sk-costrict-") {
		t.Fatalf("api key = %q", apiKey)
	}
	if !strings.Contains(out.String(), apiKey) {
		t.Fatalf("api key was not printed: %q", out.String())
	}
	if cfg.LocalAPIKeyHash == "" || strings.Contains(cfg.LocalAPIKeyHash, apiKey) {
		t.Fatalf("hash was not stored safely: %q", cfg.LocalAPIKeyHash)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.VerifyLocalAPIKey(apiKey) {
		t.Fatal("saved config does not verify generated api key")
	}

	out.Reset()
	second, err := ensureLocalAPIKey(path, loaded, &out)
	if err != nil {
		t.Fatal(err)
	}
	if second != "" || out.Len() != 0 {
		t.Fatalf("expected existing key to be left alone, key=%q out=%q", second, out.String())
	}
}

func TestResetLocalAPIKeyReplacesOldKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := config.Default()

	oldKey, err := resetLocalAPIKey(path, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := resetLocalAPIKey(path, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if oldKey == newKey {
		t.Fatal("reset returned the same api key")
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VerifyLocalAPIKey(oldKey) {
		t.Fatal("old api key still verifies after reset")
	}
	if !loaded.VerifyLocalAPIKey(newKey) {
		t.Fatal("new api key does not verify after reset")
	}
}

func TestParseModelIDs(t *testing.T) {
	raw := []byte(`{"data":[{"id":"Auto"},{"id":"Tencent-kimi-k2.6"},{"id":""}]}`)
	ids, err := parseModelIDs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "Auto" || ids[1] != "Tencent-kimi-k2.6" {
		t.Fatalf("应跳过空 id，得到: %v", ids)
	}
}

func TestPromptFallbackSelection(t *testing.T) {
	models := []string{"Auto", "Tencent-glm-5.1", "Tencent-kimi-k2.6"}

	cases := []struct {
		name      string
		input     string
		wantModel string
		wantCh    bool
	}{
		{"按序号", "2\n", "Tencent-glm-5.1", true},
		{"按模型名", "Tencent-kimi-k2.6\n", "Tencent-kimi-k2.6", true},
		{"空行保持不变", "\n", "", false},
		{"EOF 保持不变", "", "", false},
		{"超范围后重试", "9\n1\n", "Auto", true},
		{"无效名后重试", "nope\n3\n", "Tencent-kimi-k2.6", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			got, changed, err := promptFallbackSelection(strings.NewReader(c.input), &out, models)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.wantModel || changed != c.wantCh {
				t.Fatalf("got=(%q,%v) want=(%q,%v)", got, changed, c.wantModel, c.wantCh)
			}
		})
	}
}

func TestContainsModel(t *testing.T) {
	models := []string{"Auto", "Tencent-glm-5.1"}
	if !containsModel(models, "Tencent-glm-5.1") || containsModel(models, "gpt-5.4") {
		t.Fatal("containsModel 判断错误")
	}
}

func TestSetTOMLModelCatalogJSONCreatesFile(t *testing.T) {
	tomlPath := filepath.Join(t.TempDir(), "config.toml")
	changed, err := setTOMLModelCatalogJSON(tomlPath, "/home/u/.codex/cat.json")
	if err != nil || !changed {
		t.Fatalf("应创建并改动, changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(tomlPath)
	if strings.TrimSpace(string(got)) != `model_catalog_json = "/home/u/.codex/cat.json"` {
		t.Fatalf("新建内容不对: %q", got)
	}
}

func TestSetTOMLModelCatalogJSONReplacesTopLevelAndKeepsTable(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	original := "model = \"gpt-5\"\nmodel_catalog_json = \"/old/path.json\"\n\n[model_providers.costrict]\nbase_url = \"http://x\"\n"
	if err := os.WriteFile(tomlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := setTOMLModelCatalogJSON(tomlPath, "/new/cat.json")
	if err != nil || !changed {
		t.Fatalf("应替换并改动, changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(tomlPath)
	s := string(got)
	if !strings.Contains(s, `model_catalog_json = "/new/cat.json"`) {
		t.Fatalf("未替换为新值: %s", s)
	}
	if strings.Contains(s, "/old/path.json") {
		t.Fatalf("旧值未被移除: %s", s)
	}
	// 其余内容（含 table）必须保留。
	if !strings.Contains(s, "[model_providers.costrict]") || !strings.Contains(s, `base_url = "http://x"`) || !strings.Contains(s, `model = "gpt-5"`) {
		t.Fatalf("其它内容丢失: %s", s)
	}
	// 首次修改应保留 .bak 原始备份。
	bak, err := os.ReadFile(tomlPath + ".bak")
	if err != nil || string(bak) != original {
		t.Fatalf(".bak 备份不正确: err=%v content=%q", err, bak)
	}
}

func TestSetTOMLModelCatalogJSONInsertsBeforeFirstTable(t *testing.T) {
	tomlPath := filepath.Join(t.TempDir(), "config.toml")
	original := "model = \"gpt-5\"\n\n[profiles.dev]\nmodel = \"o3\"\n"
	if err := os.WriteFile(tomlPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := setTOMLModelCatalogJSON(tomlPath, "/new/cat.json"); err != nil {
		t.Fatal(err)
	}
	got := string(mustRead(t, tomlPath))
	idxKey := strings.Index(got, "model_catalog_json")
	idxTable := strings.Index(got, "[profiles.dev]")
	if idxKey < 0 || idxTable < 0 || idxKey > idxTable {
		t.Fatalf("新键应插在第一个 table 之前（保持顶层）: %s", got)
	}
}

func TestSetTOMLModelCatalogJSONIdempotent(t *testing.T) {
	tomlPath := filepath.Join(t.TempDir(), "config.toml")
	if _, err := setTOMLModelCatalogJSON(tomlPath, "/cat.json"); err != nil {
		t.Fatal(err)
	}
	changed, err := setTOMLModelCatalogJSON(tomlPath, "/cat.json")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("值未变化时不应再改动")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
