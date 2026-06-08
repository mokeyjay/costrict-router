package compat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// APIError 是转换阶段可携带 HTTP 状态与协议错误类型的错误。
type APIError struct {
	Status  int
	Type    string
	Message string
}

func (e *APIError) Error() string { return e.Message }

func newAPIError(status int, typ, message string) *APIError {
	return &APIError{Status: status, Type: typ, Message: message}
}

// AsAPIError 从错误链中提取 *APIError。
func AsAPIError(err error) *APIError {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

// extractUpstreamErrorMessage 从上游错误体里尽量取出可读信息。
func extractUpstreamErrorMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &probe); err == nil {
		if probe.Error.Message != "" {
			return probe.Error.Message
		}
		if probe.Message != "" {
			return probe.Message
		}
	}
	return strings.TrimSpace(string(body))
}

func openAIErrorType(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusNotFound:
		return "invalid_request_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return "api_error"
	}
}

func anthropicErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}
