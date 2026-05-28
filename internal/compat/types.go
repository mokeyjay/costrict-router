package compat

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type APIError struct {
	Status  int
	Type    string
	Message string
}

func (e *APIError) Error() string {
	return e.Message
}

func badRequest(message string) *APIError {
	return &APIError{Status: 400, Type: "invalid_request_error", Message: message}
}

func rawMap(body []byte) (map[string]json.RawMessage, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, badRequest("请求体不是有效的 JSON")
	}
	return payload, nil
}

func marshalObject(value map[string]any) ([]byte, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("编码转换后的请求失败: %w", err)
	}
	return body, nil
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}

func rawBool(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

func copyRaw(dst map[string]any, src map[string]json.RawMessage, from, to string) {
	if value, ok := src[from]; ok {
		dst[to] = rawAny(value)
	}
}

func copyRawSame(dst map[string]any, src map[string]json.RawMessage, keys ...string) {
	for _, key := range keys {
		copyRaw(dst, src, key, key)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func usageFromChat(raw json.RawMessage) map[string]any {
	var usage map[string]any
	_ = json.Unmarshal(raw, &usage)
	return usage
}

func intFromMap(m map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := m[key].(type) {
		case float64:
			return int(value)
		case int:
			return value
		case json.Number:
			n, _ := value.Int64()
			return int(n)
		}
	}
	return 0
}

func responseID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func getMap(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return nil
}

func getString(m map[string]any, key string) string {
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func getRawMessage(m map[string]json.RawMessage, key string) json.RawMessage {
	if value, ok := m[key]; ok {
		return value
	}
	return nil
}

func isMissingInputWithPreviousResponse(payload map[string]json.RawMessage) bool {
	if rawString(getRawMessage(payload, "previous_response_id")) == "" {
		return false
	}
	input, ok := payload["input"]
	if !ok || len(input) == 0 || string(input) == "null" {
		return true
	}
	if string(input) == `""` || string(input) == "[]" {
		return true
	}
	return false
}

func AsAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}
