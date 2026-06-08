package main

import (
	"bytes"
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
