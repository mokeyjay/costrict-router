package compat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizeChatToolsMixedKeepsFunctions(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"web_search","web_search":{"enable":true}},{"type":"function","function":{"name":"f"}}],"tool_choice":"auto"}`)
	out, err := SanitizeChatTools(body)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Tools []struct {
			Type string `json:"type"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Type != "function" {
		t.Fatalf("tools = %+v", req.Tools)
	}
}

func TestSanitizeChatToolsAllDroppedErrors(t *testing.T) {
	body := []byte(`{"model":"m","tools":[{"type":"web_search"},{"type":"retrieval"}]}`)
	_, err := SanitizeChatTools(body)
	apiErr := AsAPIError(err)
	if apiErr == nil || apiErr.Status != 400 {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(apiErr.Message, "web_search") || !strings.Contains(apiErr.Message, "retrieval") {
		t.Fatalf("错误信息应列出被剔除的类型: %s", apiErr.Message)
	}
}

func TestSanitizeChatToolsPassthroughWhenClean(t *testing.T) {
	for _, body := range []string{
		`{"model":"m"}`,
		`{"model":"m","tools":[{"type":"function","function":{"name":"f"}}]}`,
		`{"model":"m","tools":"garbage"}`,
	} {
		out, err := SanitizeChatTools([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if string(out) != body {
			t.Fatalf("应原样透传: %s -> %s", body, out)
		}
	}
}
