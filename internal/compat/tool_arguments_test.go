package compat

import (
	"strings"
	"testing"
)

func TestNormalizeToolArguments(t *testing.T) {
	tests := map[string]string{
		`{"cmd":"ok"}`:                `{"cmd":"ok"}`,
		`{"cmd":"ok"}{"cmd":"again"}`: `{"cmd":"ok"}`,
		`{"cmd":`:                     `{}`,
		``:                            `{}`,
	}
	for input, want := range tests {
		if got := normalizeToolArguments(input); got != want {
			t.Fatalf("normalizeToolArguments(%q) = %q, want %q", input, got, want)
		}
	}
}

// 回归：minimax 观测到的双重编码——arguments 是内容为 JSON 对象的字符串，要解包而非丢成 {}。
func TestNormalizeToolArgumentsDoubleEncoded(t *testing.T) {
	got := normalizeToolArguments(`"{\"a\":1}"`)
	if got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
	// 非对象字符串仍退回空对象
	if got := normalizeToolArguments(`"plain text"`); got != "{}" {
		t.Fatalf("got %q", got)
	}
}

func TestJSONObjectGate(t *testing.T) {
	// 第二段 JSON 在同一增量内
	g := &jsonObjectGate{}
	if got := g.filter(`{"a":1}{"b":2}`); got != `{"a":1}` {
		t.Fatalf("got %q", got)
	}
	if got := g.filter(`{"c":3}`); got != "" {
		t.Fatalf("done 后仍放行: %q", got)
	}

	// 跨增量边界 + 字符串里的花括号不干扰配对
	g = &jsonObjectGate{}
	var out strings.Builder
	for _, chunk := range []string{`{"s":"}`, `{\"","n`, `":[1,2`, `]}`, `{"junk":true}`} {
		out.WriteString(g.filter(chunk))
	}
	if out.String() != `{"s":"}{\"","n":[1,2]}` {
		t.Fatalf("got %q", out.String())
	}

	// 非对象内容全量透传（保持与旧行为一致的兜底）
	g = &jsonObjectGate{}
	if got := g.filter("not json at all"); got != "not json at all" {
		t.Fatalf("got %q", got)
	}

	// MiniMax 偶发把参数对象包装成一个 JSON 字符串；流式情况下要等外层字符串
	// 完整后再解包，不能把引号和反斜杠原样发给 Anthropic 客户端。
	g = &jsonObjectGate{}
	var decoded strings.Builder
	for _, chunk := range []string{`  "{\"a\":`, `1,\"b\":\"x\"}"`, `"ignored"`} {
		decoded.WriteString(g.filter(chunk))
	}
	if decoded.String() != `{"a":1,"b":"x"}` {
		t.Fatalf("双重编码流式解包结果 = %q", decoded.String())
	}
}
