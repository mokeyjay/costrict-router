package server

import "testing"

func TestValidShutdownToken(t *testing.T) {
	cases := []struct {
		name     string
		expected string
		got      string
		want     bool
	}{
		{"match", "secret-token", "secret-token", true},
		{"mismatch", "secret-token", "wrong", false},
		{"empty got", "secret-token", "", false},
		// expected 为空（前台 serve 未启用关停 token）时必须一律拒绝，避免无鉴权关停。
		{"expected disabled rejects empty", "", "", false},
		{"expected disabled rejects any", "", "anything", false},
	}
	for _, c := range cases {
		if got := validShutdownToken(c.expected, c.got); got != c.want {
			t.Fatalf("%s: validShutdownToken(%q,%q)=%v want=%v", c.name, c.expected, c.got, got, c.want)
		}
	}
}
