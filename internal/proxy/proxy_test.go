package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"costrict-router/internal/config"
	"costrict-router/internal/logx"
)

type fakeTokens struct {
	cfg config.Config
}

func (f *fakeTokens) Config() config.Config {
	return f.cfg
}

func (f *fakeTokens) EnsureFreshToken(context.Context) error {
	return nil
}

func TestForwardChatAddsCostrictHeaders(t *testing.T) {
	// 验证聊天转发会改写到真实上游路径，并补齐 CoStrict 必需请求头。
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/chat-rag/api/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("zgsm-client-id") != "machine" || r.Header.Get("x-user-id") != "user" {
			t.Fatalf("headers = %+v", r.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		}, nil
	})}

	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
			MachineCode:     "machine",
			UserID:          "user",
		}},
		Client: client,
		Logger: logx.New(&strings.Builder{}, false),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestForwardRequiresLocalAPIKey(t *testing.T) {
	// 本地 /v1 入口必须先校验本地 API Key，失败时不能触达上游。
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	called := false
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			return nil, nil
		})},
	}

	for _, tc := range []struct {
		name          string
		authorization string
	}{
		{name: "missing"},
		{name: "wrong", authorization: "Bearer " + apiKey + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
			if tc.authorization != "" {
				req.Header.Set("Authorization", tc.authorization)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if called {
				t.Fatal("upstream was called without a valid local api key")
			}
		})
	}
}

func TestModelsRequiresLocalAPIKey(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	called := false
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			if r.URL.Path != "/ai-gateway/api/v1/models" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			}, nil
		})},
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without key = %d body = %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("upstream was called without a local api key")
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with key = %d body = %s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("upstream was not called with a valid local api key")
	}
}

func TestDebugLogsChatMetricsWithoutRequestBody(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	var logs strings.Builder
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "secret prompt") {
				t.Fatalf("upstream body = %s", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)),
			}, nil
		})},
		Logger: logx.New(&logs, true),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"secret prompt"}],"max_tokens":100,"temperature":0.7}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	logText := logs.String()
	if !containsAny(logText, "chat metrics", "对话指标") || !strings.Contains(logText, "gpt-test") || !containsAny(logText, "usage=prompt=10 completion=5 total=15", "token=输入=10 输出=5 总计=15") {
		t.Fatalf("metrics log missing expected fields: %s", logText)
	}
	if strings.Contains(logText, "secret prompt") {
		t.Fatalf("debug log leaked request body: %s", logText)
	}
}

func TestDebugFullRequestLogsRequestBody(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	var logs strings.Builder
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)),
			}, nil
		})},
		Logger:           logx.New(&logs, true),
		DebugFullRequest: true,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[{"role":"user","content":"secret prompt"}]}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	logText := logs.String()
	if !strings.Contains(logText, "forward request") || !strings.Contains(logText, "secret prompt") {
		t.Fatalf("full request log missing request body: %s", logText)
	}
}

func TestDebugLogsSSEUsageMetrics(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	var logs strings.Builder
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:         "https://example.com",
			AccessToken:     "access",
			RefreshToken:    "refresh",
			LocalAPIKeyHash: apiKeyHash,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n" +
				"data: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}, nil
		})},
		Logger: logx.New(&logs, true),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "true") || !containsAny(logs.String(), "usage=prompt=3 completion=2 total=5", "token=输入=3 输出=2 总计=5") {
		t.Fatalf("SSE metrics log missing expected fields: %s", logs.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func TestHealthzRedactsTokens(t *testing.T) {
	// 健康检查响应必须脱敏 token 类字段，避免 status/logs 暴露敏感信息。
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:               "https://example.com",
			ListenAddr:            "127.0.0.1:14567",
			AccessToken:           "abcdefghijklmnopqrstuvwxyz",
			RefreshToken:          "refreshabcdefghijklmnopqrstuvwxyz",
			LocalAPIKeyHash:       "v1:sha256:salt:digest",
			MachineCode:           "machineabcdefghijklmnopqrstuvwxyz",
			AccessTokenExpiresAt:  time.Unix(1893456000, 0),
			RefreshTokenExpiresAt: time.Unix(1893456000, 0),
		}},
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("healthz leaked token-like value: %s", rec.Body.String())
	}
	if payload["local_api_key_configured"] != true {
		t.Fatalf("local_api_key_configured = %v", payload["local_api_key_configured"])
	}
}

func localAPIKeyForTest(t *testing.T) (string, string) {
	t.Helper()
	apiKey, err := config.GenerateLocalAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := config.HashLocalAPIKey(apiKey)
	if err != nil {
		t.Fatal(err)
	}
	return apiKey, hash
}

func containsAny(s string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
