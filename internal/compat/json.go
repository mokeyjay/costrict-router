package compat

import (
	"encoding/json"
	"io"
)

// newJSONEncoder 返回一个关闭 HTML 转义的编码器，避免 &、<、> 被转义成 \u 序列。
func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}
