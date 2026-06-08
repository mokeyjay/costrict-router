package compat

import (
	"strings"
	"testing"
)

func TestSplitInlineThink(t *testing.T) {
	cases := []struct {
		in       string
		thinking string
		rest     string
	}{
		{"<think>\n推理过程\n</think>\n\n测试成功", "推理过程", "测试成功"},
		{"没有思考的普通正文", "", "没有思考的普通正文"},
		{"<think>未闭合的思考", "未闭合的思考", ""},
		{"  <think>带前导空白</think>答案", "带前导空白", "答案"},
		{"正文里恰好提到 <think> 标签", "", "正文里恰好提到 <think> 标签"},
	}
	for _, c := range cases {
		thinking, rest := splitInlineThink(c.in)
		if thinking != c.thinking || rest != c.rest {
			t.Errorf("splitInlineThink(%q) = (%q,%q), want (%q,%q)", c.in, thinking, rest, c.thinking, c.rest)
		}
	}
}

// 逐字节喂入，验证标签跨 chunk 仍能正确拆分。
func TestThinkSplitterByteByByte(t *testing.T) {
	input := "<think>思考A</think>\n\n正文B"
	var sp thinkSplitter
	var reason, content strings.Builder
	for _, r := range input {
		rs, cs := sp.push(string(r))
		reason.WriteString(rs)
		content.WriteString(cs)
	}
	rf, cf := sp.flush()
	reason.WriteString(rf)
	content.WriteString(cf)
	if reason.String() != "思考A" {
		t.Errorf("reason = %q", reason.String())
	}
	if content.String() != "正文B" {
		t.Errorf("content = %q", content.String())
	}
}

func TestThinkSplitterNoThink(t *testing.T) {
	var sp thinkSplitter
	r, c := sp.push("普通")
	if r != "" {
		t.Fatalf("unexpected reason %q", r)
	}
	// "普通" 第一个字符不是 '<'，应立即判定为正文透传。
	rest := c
	r2, c2 := sp.push("正文")
	rest += c2
	rf, cf := sp.flush()
	rest += cf
	if r2 != "" || rf != "" {
		t.Fatalf("unexpected reason output")
	}
	if rest != "普通正文" {
		t.Fatalf("content = %q", rest)
	}
}

func TestThinkSplitterUnclosed(t *testing.T) {
	var sp thinkSplitter
	var reason strings.Builder
	for _, chunk := range []string{"<thi", "nk>残", "缺思考"} {
		rs, _ := sp.push(chunk)
		reason.WriteString(rs)
	}
	rf, cf := sp.flush()
	reason.WriteString(rf)
	if cf != "" {
		t.Fatalf("unexpected content %q", cf)
	}
	if reason.String() != "残缺思考" {
		t.Fatalf("reason = %q", reason.String())
	}
}
