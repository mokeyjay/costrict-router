package compat

import "testing"

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
