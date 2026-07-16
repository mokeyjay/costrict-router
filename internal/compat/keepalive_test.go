package compat

import (
	"io"
	"strings"
	"testing"
	"time"
)

// streamCollector 用单个后台 goroutine 持续读流，readUntil 可多次调用观察累计内容。
type streamCollector struct {
	ch  chan streamChunk
	buf strings.Builder
}

type streamChunk struct {
	data string
	err  error
}

func newStreamCollector(r io.Reader) *streamCollector {
	c := &streamCollector{ch: make(chan streamChunk, 64)}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			c.ch <- streamChunk{string(buf[:n]), err}
			if err != nil {
				return
			}
		}
	}()
	return c
}

// readUntil 等待累计内容出现子串（或超时），返回当前累计内容。
func (c *streamCollector) readUntil(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(c.buf.String(), want) {
			return c.buf.String()
		}
		select {
		case chunk := <-c.ch:
			c.buf.WriteString(chunk.data)
			if chunk.err != nil {
				if strings.Contains(c.buf.String(), want) {
					return c.buf.String()
				}
				t.Fatalf("流已结束仍未读到 %q:\n%s", want, c.buf.String())
			}
		case <-deadline:
			t.Fatalf("超时未读到 %q:\n%s", want, c.buf.String())
		}
	}
}

// 回归：上游 gateway 缓冲窗口可达一分钟以上，编码器必须在收到上游首块前
// 就发出起始事件，并在空窗期间持续保活，否则客户端会按空闲超时断开。
func TestMessagesEncodeStreamEarlyStartAndKeepalive(t *testing.T) {
	old := streamKeepaliveInterval
	streamKeepaliveInterval = 20 * time.Millisecond
	defer func() { streamKeepaliveInterval = old }()

	pr, pw := io.Pipe()
	c := newStreamCollector((MessagesCodec{}).EncodeStream(pr, "m", true))
	// 上游一个字节都没发，客户端应先收到 message_start，随后收到 ping 保活。
	got := c.readUntil(t, "message_start")
	if strings.Contains(got, "message_stop") {
		t.Fatalf("流不应提前结束:\n%s", got)
	}
	c.readUntil(t, `"type":"ping"`)

	go func() {
		_, _ = pw.Write([]byte("data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
		_ = pw.Close()
	}()
	rest := c.readUntil(t, "message_stop")
	if !strings.Contains(rest, `"text":"hi"`) {
		t.Fatalf("正文缺失:\n%s", rest)
	}
}

func TestResponsesEncodeStreamEarlyStartAndKeepalive(t *testing.T) {
	old := streamKeepaliveInterval
	streamKeepaliveInterval = 20 * time.Millisecond
	defer func() { streamKeepaliveInterval = old }()

	pr, pw := io.Pipe()
	c := newStreamCollector((ResponsesCodec{}).EncodeStream(pr, "m", true))
	got := c.readUntil(t, "response.created")
	if strings.Contains(got, "response.completed") {
		t.Fatalf("流不应提前结束:\n%s", got)
	}
	// Responses 协议用 SSE 注释行保活，不占 sequence_number。
	c.readUntil(t, ": keep-alive")

	go func() {
		_, _ = pw.Write([]byte("data: {\"id\":\"c\",\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"))
		_ = pw.Close()
	}()
	rest := c.readUntil(t, "response.completed")
	if !strings.Contains(rest, `"delta":"hi"`) {
		t.Fatalf("正文缺失:\n%s", rest)
	}
}
