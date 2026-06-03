package logx

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	colorReset  = "\033[0m"
	colorDim    = "\033[2m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorRed    = "\033[31m"
)

type Logger struct {
	debug   bool
	color   bool
	base    *log.Logger
	closers []io.Closer
}

func New(w io.Writer, debug bool) *Logger {
	return &Logger{
		debug: debug,
		color: true,
		base:  log.New(w, "", log.LstdFlags),
	}
}

func NewPlain(w io.Writer, debug bool) *Logger {
	return &Logger{
		debug: debug,
		color: false,
		base:  log.New(w, "", log.LstdFlags),
	}
}

func NewFile(path string, debug bool) (*Logger, error) {
	if err := Rotate(path, 5*1024*1024, 3); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{
		debug:   debug,
		color:   true,
		base:    log.New(file, "", log.LstdFlags),
		closers: []io.Closer{file},
	}, nil
}

func Rotate(path string, maxBytes int64, backups int) error {
	// 日志超过阈值时按固定备份数滚动，避免长期后台运行撑爆缓存目录。
	if maxBytes <= 0 || backups <= 0 {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() < maxBytes {
		return nil
	}
	for i := backups - 1; i >= 1; i-- {
		oldPath := fmt.Sprintf("%s.%d", path, i)
		newPath := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Rename(oldPath, newPath)
	}
	return os.Rename(path, fmt.Sprintf("%s.1", path))
}

func (l *Logger) Close() error {
	var firstErr error
	for _, closer := range l.closers {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (l *Logger) Infof(format string, args ...any) {
	l.logf("INFO", "✨", colorGreen, format, args...)
}

func (l *Logger) Warnf(format string, args ...any) {
	l.logf("WARN", "⚠️", colorYellow, format, args...)
}

func (l *Logger) Debugf(format string, args ...any) {
	if l.debug {
		l.logf("DEBUG", "🔎", colorBlue, format, args...)
	}
}

func (l *Logger) Errorf(format string, args ...any) {
	l.logf("ERROR", "❌", colorRed, format, args...)
}

func (l *Logger) DebugEnabled() bool {
	return l.debug
}

func (l *Logger) logf(level, emoji, color, format string, args ...any) {
	prefix := fmt.Sprintf("%s [%s]", emoji, level)
	if l.color {
		prefix = color + prefix + colorReset
	}
	l.base.Printf(prefix+" "+format, args...)
}

func RedactHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for key, values := range h {
		lower := strings.ToLower(key)
		if lower == "authorization" || lower == "x-api-key" || lower == "api-key" || strings.Contains(lower, "token") {
			out[key] = []string{"***"}
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	return out
}

func StripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if inEsc {
			if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
				inEsc = false
			}
			continue
		}
		if ch == 0x1b {
			inEsc = true
			continue
		}
		b.WriteByte(ch)
	}
	return b.String()
}

func TruncateBody(body []byte, max int) string {
	if len(body) == 0 {
		return ""
	}
	if max <= 0 {
		max = 32 * 1024
	}
	if len(body) <= max {
		return string(body)
	}
	return fmt.Sprintf("%s...<truncated %d bytes>", string(body[:max]), len(body)-max)
}
