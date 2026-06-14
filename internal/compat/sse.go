package compat

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// sseEvent 表示一条 SSE 事件。
type sseEvent struct {
	Event string
	Data  string
}

// formatSSE 把事件序列化成 SSE 文本。带 event 字段时输出 `event: x\ndata: y\n\n`。
func formatSSE(e sseEvent) []byte {
	var b strings.Builder
	if e.Event != "" {
		b.WriteString("event: ")
		b.WriteString(e.Event)
		b.WriteByte('\n')
	}
	// data 可能含换行，按 SSE 规范每行都要加 data: 前缀。
	for _, line := range strings.Split(e.Data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return []byte(b.String())
}

// scanChatStream 读取上游 Chat Completions 的 SSE，逐条回调 data 负载（去掉 `data: ` 前缀）。
// 遇到 [DONE] 会停止。回调返回 error 则中断。读取出错会通过回调最后一次以 err 形式上报。
func scanChatStream(r io.Reader, onData func(payload []byte) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(line[len("data:"):])
		if len(payload) == 0 {
			continue
		}
		if bytes.Equal(payload, []byte("[DONE]")) {
			return nil
		}
		// payload 会被 json.Unmarshal 立即解析，复制一份避免 scanner 复用底层切片。
		buf := make([]byte, len(payload))
		copy(buf, payload)
		if err := onData(buf); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// chatStreamError 检测上游 SSE 负载是否是错误对象（部分上游会以 200 + SSE 形式返回错误）。
func chatStreamError(payload []byte) (string, bool) {
	var probe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &probe); err == nil && probe.Error != nil {
		msg := probe.Error.Message
		if msg == "" {
			msg = "upstream error"
		}
		return msg, true
	}
	return "", false
}

// streamPipe 在 goroutine 中运行 generator 把转换后的 SSE 写入管道，返回可读端。
// generator 内部用 write 回调输出字节，返回 error 会被忽略（流式场景尽力而为）。
func streamPipe(generate func(write func([]byte) error) error) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		err := generate(func(b []byte) error {
			_, werr := pw.Write(b)
			return werr
		})
		_ = pw.CloseWithError(err)
	}()
	return pr
}

// sequencer 生成 OpenAI Responses 流所需的自增 sequence_number。
type sequencer struct {
	n int
}

func (s *sequencer) next() int {
	v := s.n
	s.n++
	return v
}

func jsonString(v string) string {
	b, _ := jsonMarshal(v)
	return string(b)
}

// 包装 json.Marshal，禁用 HTML 转义，保证 SSE 文本中 <、>、& 原样输出。
func jsonMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := newJSONEncoder(&buf)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	return out, nil
}

func mustJSON(v any) []byte {
	b, err := jsonMarshal(v)
	if err != nil {
		return []byte(fmt.Sprintf("%q", err.Error()))
	}
	return b
}
