package compat

import "strings"

// 部分模型（如 minimax）不使用 reasoning_content 字段，而是把思考过程内联在 content
// 开头的 <think>...</think> 标签里。这里负责把这种内联思考剥离出来，统一转换成独立的
// 思考块（Anthropic thinking / Responses reasoning），与走 reasoning_content 的模型对齐。

const thinkOpenTag = "<think>"
const thinkCloseTag = "</think>"

// splitInlineThink 用于非流式：从 content 开头提取单个 <think>...</think>，
// 返回 (思考文本, 去掉思考后的正文)。未检测到则 thinking 为空、rest 原样返回。
func splitInlineThink(content string) (thinking, rest string) {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, thinkOpenTag) {
		return "", content
	}
	afterOpen := trimmed[len(thinkOpenTag):]
	idx := strings.Index(afterOpen, thinkCloseTag)
	if idx < 0 {
		// 没有闭合标签（通常是被 max_tokens 截断），整段视为思考。
		return strings.TrimSpace(afterOpen), ""
	}
	thinking = strings.TrimSpace(afterOpen[:idx])
	rest = strings.TrimLeft(afterOpen[idx+len(thinkCloseTag):], " \t\r\n")
	return thinking, rest
}

// resolveReasoning 统一两种思考来源：优先用 reasoning_content 字段；该字段为空时，
// 再尝试从 content 开头剥离内联的 <think>...</think>。返回最终的 (思考, 正文)。
func resolveReasoning(reasoningContent, content string) (reasoning, text string) {
	if reasoningContent != "" {
		return reasoningContent, content
	}
	think, rest := splitInlineThink(content)
	return think, rest
}

type thinkState int

const (
	thinkDetecting   thinkState = iota // 还未确定 content 是否以 <think> 开头
	thinkInside                        // 已在 <think> 内部，等待 </think>
	thinkAfterClose                    // 刚遇到 </think>，正在跳过它与正文之间的空白
	thinkPassthrough                   // 已确定后续都是正文，直接透传
)

// thinkSplitter 是流式版本：逐片喂入 content delta，吐出思考增量与正文增量，
// 并能正确处理标签跨多个 chunk 到达的情况。
type thinkSplitter struct {
	state thinkState
	buf   strings.Builder
}

// push 处理一段 content 增量，返回应归入思考块和正文块的增量。
func (t *thinkSplitter) push(s string) (reason, content string) {
	switch t.state {
	case thinkPassthrough:
		return "", s
	case thinkInside:
		return t.feedInside(s)
	case thinkAfterClose:
		return t.feedAfterClose(s)
	default:
		return t.feedDetecting(s)
	}
}

func (t *thinkSplitter) feedAfterClose(s string) (reason, content string) {
	t.buf.WriteString(s)
	trimmed := strings.TrimLeft(t.buf.String(), " \t\r\n")
	if trimmed == "" {
		return "", "" // 仍是分隔空白，继续丢弃
	}
	t.state = thinkPassthrough
	t.buf.Reset()
	return "", trimmed
}

func (t *thinkSplitter) feedDetecting(s string) (reason, content string) {
	t.buf.WriteString(s)
	buf := t.buf.String()
	trimmed := strings.TrimLeft(buf, " \t\r\n")
	if trimmed == "" {
		return "", "" // 目前只有空白，继续等待
	}
	if strings.HasPrefix(trimmed, thinkOpenTag) {
		t.state = thinkInside
		t.buf.Reset()
		return t.feedInside(trimmed[len(thinkOpenTag):])
	}
	if strings.HasPrefix(thinkOpenTag, trimmed) {
		return "", "" // trimmed 仍可能是 <think> 的前缀，继续缓冲
	}
	// 确定不是以 <think> 开头：之前缓冲的（含前导空白）都属于正文。
	t.state = thinkPassthrough
	out := t.buf.String()
	t.buf.Reset()
	return "", out
}

func (t *thinkSplitter) feedInside(s string) (reason, content string) {
	t.buf.WriteString(s)
	buf := t.buf.String()
	if idx := strings.Index(buf, thinkCloseTag); idx >= 0 {
		reason = buf[:idx]
		rest := strings.TrimLeft(buf[idx+len(thinkCloseTag):], " \t\r\n")
		t.buf.Reset()
		if rest == "" {
			// 正文还没到，进入跳空白状态，避免把分隔换行当成正文。
			t.state = thinkAfterClose
			return reason, ""
		}
		t.state = thinkPassthrough
		return reason, rest
	}
	// 未找到完整 </think>，保留末尾可能是其前缀的部分，其余作为思考输出。
	keep := overlapSuffixLen(buf, thinkCloseTag)
	reason = buf[:len(buf)-keep]
	t.buf.Reset()
	t.buf.WriteString(buf[len(buf)-keep:])
	return reason, ""
}

// flush 在流结束时调用，返回缓冲区里残留内容的归属。
func (t *thinkSplitter) flush() (reason, content string) {
	buf := t.buf.String()
	t.buf.Reset()
	switch t.state {
	case thinkInside:
		// <think> 未闭合，残留全部算思考。
		return buf, ""
	case thinkDetecting:
		// 残留是空白或不完整的标签前缀，按正文输出最安全。
		return "", buf
	default:
		return "", ""
	}
}

// overlapSuffixLen 返回 s 的后缀同时是 tag 前缀的最大长度（小于 len(tag)）。
func overlapSuffixLen(s, tag string) int {
	max := len(tag) - 1
	if max > len(s) {
		max = len(s)
	}
	for k := max; k > 0; k-- {
		if s[len(s)-k:] == tag[:k] {
			return k
		}
	}
	return 0
}
