package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func testHandler(apiKeyHash string, client *http.Client) *Handler {
	return &Handler{
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
}

func TestForwardResponsesConvertsThroughChatCompletions(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/chat-rag/api/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"messages"`) || !strings.Contains(string(body), `hello`) {
			t.Fatalf("upstream body = %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-1","model":"glm-5","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)),
		}, nil
	})}
	handler := testHandler(apiKeyHash, client)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"glm-5","input":"hello"}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) || !strings.Contains(rec.Body.String(), `"text":"hi"`) {
		t.Fatalf("response body = %s", rec.Body.String())
	}
}

func TestForwardAnthropicMessagesConvertsThroughChatCompletions(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/chat-rag/api/v1/chat/completions" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"messages"`) || !strings.Contains(string(body), `hello`) {
			t.Fatalf("upstream body = %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl-1","model":"glm-5","choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`)),
		}, nil
	})}
	handler := testHandler(apiKeyHash, client)

	// 使用 Anthropic 风格的 x-api-key 鉴权。
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"glm-5","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`))
	req.Header.Set("x-api-key", apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"message"`) || !strings.Contains(rec.Body.String(), `"text":"hi"`) {
		t.Fatalf("response body = %s", rec.Body.String())
	}
}

func TestResponsesRejectsMissingInput(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	called := false
	handler := testHandler(apiKeyHash, &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"glm-5","previous_response_id":"resp_1"}`))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("upstream was called for unsupported request")
	}
}

func TestForwardCompatConvertsUpstreamErrorFormat(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"slow down","type":"ai_model_error"}}`)),
		}, nil
	})}
	handler := testHandler(apiKeyHash, client)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", apiKey)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}
	var anthropicErr map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &anthropicErr); err != nil {
		t.Fatal(err)
	}
	if anthropicErr["type"] != "error" {
		t.Fatalf("messages error not anthropic-shaped: %s", rec.Body.String())
	}
	if anthropicErr["error"].(map[string]any)["type"] != "rate_limit_error" {
		t.Fatalf("error type = %v", anthropicErr["error"])
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

func TestAuthDisabledBypassesLocalAPIKey(t *testing.T) {
	called := false
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:      "https://example.com",
			AccessToken:  "access",
			RefreshToken: "refresh",
			// 鉴权已关闭：无 LocalAPIKeyHash、无 Authorization 头也应放行。
			AuthDisabled: true,
		}},
		Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			called = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"data":[]}`)),
			}, nil
		})},
	}

	// 不带任何 token 也能通过。
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("鉴权关闭时无 token 应放行, status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !called {
		t.Fatal("鉴权关闭时请求应转发到上游")
	}

	// 带空 token 也能通过。
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("鉴权关闭时空 token 应放行, status=%d", rec.Code)
	}
}

func TestModelsAliasOnlyForAnthropicClient(t *testing.T) {
	// 上游返回 OpenAI 风格的完整模型列表
	upstreamBody := `{"data":[{"id":"Tencent-glm-5.1","object":"model","created":1781070586,"owned_by":"","contextWindow":198000,"maxTokens":32000,"supportsImages":false}],"object":"list"}`
	newHandler := func() *Handler {
		return &Handler{
			Tokens: &fakeTokens{cfg: config.Config{
				BaseURL:      "https://example.com",
				AccessToken:  "access",
				RefreshToken: "refresh",
				AuthDisabled: true,
			}},
			Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}, nil
			})},
		}
	}

	// Claude Code（带 anthropic-version 头）：
	// id 应加 claude- 前缀，补 display_name，并转为 Anthropic 格式。
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("anthropic-version", "2023-06-01")
	rec := httptest.NewRecorder()
	newHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// Anthropic 格式：id 带 claude- 前缀
	if !strings.Contains(body, `"id":"claude-Tencent-glm-5.1"`) {
		t.Fatalf("Claude Code 应得到加前缀的 id: %s", body)
	}
	// Anthropic 格式：display_name 存在
	if !strings.Contains(body, `"display_name"`) {
		t.Fatalf("Claude Code 应得到 display_name: %s", body)
	}
	// Anthropic 格式：type 而非 object
	if !strings.Contains(body, `"type":"model"`) {
		t.Fatalf("Anthropic 格式应用 type=model: %s", body)
	}
	if strings.Contains(body, `"object":"model"`) {
		t.Fatalf("Anthropic 格式不应包含 object=model: %s", body)
	}
	// Anthropic 格式：created_at 而非 created
	if !strings.Contains(body, `"created_at"`) {
		t.Fatalf("Anthropic 格式应用 created_at: %s", body)
	}
	if strings.Contains(body, `"created":`) {
		t.Fatalf("Anthropic 格式不应包含 created: %s", body)
	}
	// Anthropic 格式：max_input_tokens 而非 contextWindow
	if !strings.Contains(body, `"max_input_tokens"`) {
		t.Fatalf("Anthropic 格式应用 max_input_tokens: %s", body)
	}
	if strings.Contains(body, `"contextWindow"`) {
		t.Fatalf("Anthropic 格式不应包含 contextWindow: %s", body)
	}
	// Anthropic 格式：max_tokens 而非 maxTokens
	if !strings.Contains(body, `"max_tokens"`) {
		t.Fatalf("Anthropic 格式应用 max_tokens: %s", body)
	}
	if strings.Contains(body, `"maxTokens"`) {
		t.Fatalf("Anthropic 格式不应包含 maxTokens: %s", body)
	}
	// Anthropic 格式：顶层有 has_more/first_id/last_id，无 object:"list"
	if !strings.Contains(body, `"has_more":false`) {
		t.Fatalf("Anthropic 格式应有 has_more: %s", body)
	}
	if strings.Contains(body, `"object":"list"`) {
		t.Fatalf("Anthropic 格式不应包含 object=list: %s", body)
	}
	// 不应包含 OpenAI 特有字段
	if strings.Contains(body, `"owned_by"`) {
		t.Fatalf("Anthropic 格式不应包含 owned_by: %s", body)
	}
	// Content-Length 必须与改写后 body 一致。
	if cl := rec.Result().Header.Get("Content-Length"); cl != strconv.Itoa(len(rec.Body.Bytes())) {
		t.Fatalf("Content-Length=%q 与实际 body 长度 %d 不一致", cl, len(rec.Body.Bytes()))
	}

	// Codex 客户端：转换为当前 models manager 要求的 {"models":[...]} 格式，不加 Claude 前缀。
	req2 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req2.Header.Set("User-Agent", "codex_cli_rs/0.135.0")
	rec2 := httptest.NewRecorder()
	newHandler().ServeHTTP(rec2, req2)
	body2 := rec2.Body.String()
	if strings.Contains(body2, "claude-") {
		t.Fatalf("Codex 客户端不应使用 Claude 别名: %s", body2)
	}
	if !strings.Contains(body2, `"slug": "Tencent-glm-5.1"`) {
		t.Fatalf("Codex 模型列表缺少真实模型: %s", body2)
	}
	if !strings.Contains(body2, `"models"`) {
		t.Fatalf("Codex 格式应包含 models: %s", body2)
	}
	if strings.Contains(body2, `"object":"model"`) || strings.Contains(body2, `"data"`) {
		t.Fatalf("Codex 格式不应保留 OpenAI 列表信封: %s", body2)
	}

	// 普通 OpenAI 客户端仍收到标准 data/object 列表，避免 Codex 专用格式污染通用接口。
	req3 := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req3.Header.Set("User-Agent", "openai-go/1.0")
	rec3 := httptest.NewRecorder()
	newHandler().ServeHTTP(rec3, req3)
	body3 := rec3.Body.String()
	if !strings.Contains(body3, `"data"`) || !strings.Contains(body3, `"object":"model"`) {
		t.Fatalf("OpenAI 客户端应保留标准列表格式: %s", body3)
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

func TestHealthzOnlyReturnsHealthState(t *testing.T) {
	// 健康检查无鉴权，只能返回最小健康状态，避免暴露本地配置详情。
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:               "https://example.com",
			ListenAddr:            "127.0.0.1:14567",
			AccessToken:           "abcdefghijklmnopqrstuvwxyz",
			RefreshToken:          "refreshabcdefghijklmnopqrstuvwxyz",
			LocalAPIKeyHash:       "v1:sha256:salt:digest",
			MachineCode:           "machineabcdefghijklmnopqrstuvwxyz",
			UserID:                "useridabcdefghijklmnopqrstuvwxyz",
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
	if payload["ok"] != true || len(payload) != 1 {
		t.Fatalf("healthz 应只返回 ok 字段: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "abcdefghijklmnopqrstuvwxyz") || strings.Contains(rec.Body.String(), "https://example.com") {
		t.Fatalf("healthz leaked detailed config: %s", rec.Body.String())
	}
}

func TestStatusRedactsTokensAndRequiresAuth(t *testing.T) {
	apiKey, apiKeyHash := localAPIKeyForTest(t)
	handler := &Handler{
		Tokens: &fakeTokens{cfg: config.Config{
			BaseURL:               "https://example.com",
			ListenAddr:            "127.0.0.1:14567",
			AccessToken:           "abcdefghijklmnopqrstuvwxyz",
			RefreshToken:          "refreshabcdefghijklmnopqrstuvwxyz",
			LocalAPIKeyHash:       apiKeyHash,
			MachineCode:           "machineabcdefghijklmnopqrstuvwxyz",
			UserID:                "useridabcdefghijklmnopqrstuvwxyz",
			AccessTokenExpiresAt:  time.Unix(1893456000, 0),
			RefreshTokenExpiresAt: time.Unix(1893456000, 0),
		}},
		StatusToken: "shutdown-token",
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status without auth = %d body=%s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatusPayloadRedacted(t, rec)

	req = httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("X-Shutdown-Token", "shutdown-token")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	assertStatusPayloadRedacted(t, rec)
}

func assertStatusPayloadRedacted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("status leaked token-like value: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "https://example.com") {
		t.Fatalf("status leaked full base_url: %s", rec.Body.String())
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
