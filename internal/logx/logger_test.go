package logx

import (
	"net/http"
	"testing"
)

func TestRedactHeaderMasksSensitiveValues(t *testing.T) {
	in := http.Header{
		"Authorization":  []string{"Bearer secret-token"},
		"X-Api-Key":      []string{"sk-costrict-secret"},
		"X-User-Id":      []string{"user-123"},
		"Zgsm-Client-Id": []string{"machine-abc"},
		"Zgsm-Task-Id":   []string{"task-uuid"},
		"Accept":         []string{"application/json"},
	}
	out := RedactHeader(in)

	for _, key := range []string{"Authorization", "X-Api-Key", "X-User-Id", "Zgsm-Client-Id"} {
		if got := out.Get(key); got != "***" {
			t.Fatalf("%s = %q, want redacted", key, got)
		}
	}
	if out.Get("Accept") != "application/json" {
		t.Fatalf("Accept should not be redacted: %q", out.Get("Accept"))
	}
	if out.Get("Zgsm-Task-Id") != "task-uuid" {
		t.Fatalf("non-sensitive id should not be redacted: %q", out.Get("Zgsm-Task-Id"))
	}
}
