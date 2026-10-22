package auth

import (
	"net/url"
	"testing"
)

func TestParseLoginURL(t *testing.T) {
	params, err := ParseLoginURL("https://www.abc.com/oidc-auth/api/v1/plugin/login?machine_code=m1&state=s1&provider=casdoor&plugin_version=3.0.23&vscode_version=1.143.0&uri_scheme=vscode")
	if err != nil {
		t.Fatal(err)
	}
	if params.BaseURL != "https://www.abc.com" {
		t.Fatalf("BaseURL = %q", params.BaseURL)
	}
	if params.MachineCode != "m1" || params.State != "s1" {
		t.Fatalf("unexpected params: %+v", params)
	}
}

func TestBuildLoginURL(t *testing.T) {
	// 校验登录 URL 的路径和核心查询参数，防止插件登录协议被改坏。
	client := NewClient(LoginParams{
		BaseURL:     "https://www.abc.com",
		MachineCode: "m1",
		State:       "s1",
	}.WithDefaults())
	raw, err := client.BuildLoginURL()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != LoginPath {
		t.Fatalf("path = %s", u.Path)
	}
	if u.Query().Get("machine_code") != "m1" || u.Query().Get("state") != "s1" {
		t.Fatalf("query = %s", u.RawQuery)
	}
}

func TestBuildLoginURLWithGeneratedValuesMatchesAPIDocShape(t *testing.T) {
	// 生成值必须符合 api.md 抓包形态，避免 machine_code/state 退回 UUID 格式。
	client := NewClient(LoginParams{
		BaseURL:     "https://www.abc.com",
		MachineCode: GenerateMachineCode(),
		State:       GenerateState(),
	}.WithDefaults())
	raw, err := client.BuildLoginURL()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidMachineCode(u.Query().Get("machine_code")) {
		t.Fatalf("machine_code = %q", u.Query().Get("machine_code"))
	}
	if got := u.Query().Get("state"); got == "" || got == "d62571f4-ab1e-41db-9a1b-0ad3be5fb0ef" {
		t.Fatalf("state = %q", got)
	}
}

func TestNormalizePluginVersion(t *testing.T) {
	// 旧配置里的历史版本号需要升级到内置版本，更高版本则按原样保留。
	cases := map[string]string{
		"":         DefaultPluginVersion,
		"2.8.1":    DefaultPluginVersion,
		"3.0.14":   DefaultPluginVersion,
		"invalid":  DefaultPluginVersion,
		" 3.0.23 ": "3.0.23",
		"3.0.100":  "3.0.100",
		"3.1":      "3.1",
		"4.0.0":    "4.0.0",
	}
	for in, want := range cases {
		if got := NormalizePluginVersion(in); got != want {
			t.Errorf("NormalizePluginVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeVSCodeVersion(t *testing.T) {
	if got := NormalizeVSCodeVersion("1.120.0"); got != DefaultVSCodeVersion {
		t.Fatalf("NormalizeVSCodeVersion(1.120.0) = %q", got)
	}
	if got := NormalizeVSCodeVersion("1.200.0"); got != "1.200.0" {
		t.Fatalf("NormalizeVSCodeVersion(1.200.0) = %q", got)
	}
}

func TestPluginUserAgent(t *testing.T) {
	// 对齐扩展 costrict.ts 中对话请求的 User-Agent 格式。
	if got := PluginUserAgent("2.8.1"); got != "RooCode/3.52.1 plugin_vscode/"+DefaultPluginVersion {
		t.Fatalf("PluginUserAgent = %q", got)
	}
}
