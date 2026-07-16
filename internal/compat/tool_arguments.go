package compat

import (
	"encoding/json"
	"strings"
)

// normalizeToolArguments 保留合法对象；若模型在对象后重复输出内容，则提取第一个合法对象。
// 部分模型（官方扩展在 minimax 上观测到）会把整个参数再包一层 JSON 字符串，这里解包内层，
// 完全无法解析时退回空对象，避免 Agent 和上游因畸形历史反复重试。
func normalizeToolArguments(arguments string) string {
	arguments = strings.TrimSpace(arguments)
	if arguments == "" {
		return "{}"
	}
	var object map[string]any
	decoder := json.NewDecoder(strings.NewReader(arguments))
	if err := decoder.Decode(&object); err != nil || object == nil {
		// 双重编码：arguments 本身是一个内容为 JSON 对象的字符串。
		var inner string
		if err := json.NewDecoder(strings.NewReader(arguments)).Decode(&inner); err == nil {
			inner = strings.TrimSpace(inner)
			if strings.HasPrefix(inner, "{") {
				return normalizeToolArguments(inner)
			}
		}
		return "{}"
	}
	out, err := jsonMarshal(object)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// jsonObjectGate 是 normalizeToolArguments 的流式对应物：逐段过滤参数增量，
// 放行第一个完整的顶层 JSON 值，之后的内容（如 kimi 偶发的第二段 JSON）全部丢弃。
// Anthropic 流式协议里 tool_use 的 input 由客户端拼接增量得到，没有最终修正事件，
// 因此垃圾必须在转发前拦下。
type jsonObjectGate struct {
	depth    int
	started  bool
	done     bool
	inString bool
	escaped  bool
	mode     jsonObjectGateMode
	pending  strings.Builder
}

type jsonObjectGateMode uint8

const (
	jsonObjectGateUndecided jsonObjectGateMode = iota
	jsonObjectGatePlain
	jsonObjectGateEncodedString
)

// filter 返回 s 中允许透传给客户端的前缀。
func (g *jsonObjectGate) filter(s string) string {
	if g.done {
		return ""
	}
	// 首段可能只含空白，先缓冲到能够判断顶层类型。以引号开头表示模型把
	// 参数对象再次 JSON.stringify 了；这种情况不能先把外层引号发给 Anthropic
	// 客户端，只能等字符串完整后解包并一次性发出内层对象。
	if g.mode == jsonObjectGateUndecided {
		g.pending.WriteString(s)
		buffered := g.pending.String()
		trimmed := strings.TrimLeft(buffered, " \t\r\n")
		if trimmed == "" {
			return ""
		}
		if trimmed[0] == '"' {
			g.mode = jsonObjectGateEncodedString
			return g.filterEncodedString()
		}
		g.mode = jsonObjectGatePlain
		s = buffered
		g.pending.Reset()
	} else if g.mode == jsonObjectGateEncodedString {
		g.pending.WriteString(s)
		return g.filterEncodedString()
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		if g.inString {
			switch {
			case g.escaped:
				g.escaped = false
			case c == '\\':
				g.escaped = true
			case c == '"':
				g.inString = false
			}
			continue
		}
		switch c {
		case '"':
			g.inString = true
		case '{', '[':
			g.depth++
			g.started = true
		case '}', ']':
			g.depth--
			if g.started && g.depth <= 0 {
				g.done = true
				return s[:i+1]
			}
		}
	}
	return s
}

func (g *jsonObjectGate) filterEncodedString() string {
	var inner string
	decoder := json.NewDecoder(strings.NewReader(g.pending.String()))
	if err := decoder.Decode(&inner); err != nil {
		// JSON 字符串尚未闭合时继续等待后续增量。
		return ""
	}
	g.done = true
	g.pending.Reset()
	return normalizeToolArguments(inner)
}
