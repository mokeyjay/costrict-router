package proxy

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"costrict-router/internal/compat"
	"costrict-router/internal/config"
	"costrict-router/internal/i18n"
	"costrict-router/internal/ids"
	"costrict-router/internal/logx"
	"costrict-router/internal/version"
)

type TokenProvider interface {
	Config() config.Config
	EnsureFreshToken(context.Context) error
}

type Handler struct {
	Tokens           TokenProvider
	Client           *http.Client
	Logger           *logx.Logger
	DebugFullRequest bool
	// StatusToken 允许后台 CLI 通过 PID 文件中的本机 token 读取脱敏状态，
	// 避免要求用户记住只显示一次的本地 API Key。
	StatusToken string
	// Models 把未知模型名替换为第一个可用模型；为 nil 时不做替换（原样透传）。
	Models *ModelResolver
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		h.handleHealth(w)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/status":
		if !h.authorizeStatus(w, r) {
			return
		}
		h.handleStatus(w)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		if !h.authorizeLocalAPIKey(w, r) {
			return
		}
		h.forward(w, r, "/chat-rag/api/v1/chat/completions", true)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
		if !h.authorizeLocalAPIKey(w, r) {
			return
		}
		h.forwardCompat(w, r, compat.ResponsesCodec{})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		if !h.authorizeLocalAPIKey(w, r) {
			return
		}
		h.forwardCompat(w, r, compat.MessagesCodec{})
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		if !h.authorizeLocalAPIKey(w, r) {
			return
		}
		h.handleModels(w, r)
	default:
		writeOpenAIError(w, http.StatusNotFound, "not_found", i18n.T("local route not found", "未找到本地路由"))
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter) {
	cfg := h.Tokens.Config()
	payload := map[string]any{
		"ok": cfg.LoggedIn(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) handleStatus(w http.ResponseWriter) {
	cfg := h.Tokens.Config()
	payload := map[string]any{
		"ok":                       cfg.LoggedIn(),
		"base_url":                 config.Redact(cfg.BaseURL),
		"listen_addr":              cfg.ListenAddr,
		"machine_code":             config.Redact(cfg.MachineCode),
		"user_id":                  config.Redact(cfg.UserID),
		"access_token":             config.Redact(cfg.AccessToken),
		"refresh_token":            config.Redact(cfg.RefreshToken),
		"access_expires":           cfg.AccessTokenExpiresAt,
		"local_api_key_configured": cfg.LocalAPIKeyHash != "",
		"auth_disabled":            cfg.AuthDisabled,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (h *Handler) authorizeStatus(w http.ResponseWriter, r *http.Request) bool {
	cfg := h.Tokens.Config()
	if cfg.AuthDisabled || validLocalAPIKey(cfg, r) {
		return true
	}
	if validStatusToken(h.StatusToken, r.Header.Get("X-Shutdown-Token")) {
		return true
	}
	if cfg.LocalAPIKeyHash == "" {
		writeOpenAIError(w, http.StatusInternalServerError, "configuration_error", i18n.T("local API key is not configured; restart costrict-router to generate one", "本地 API Key 未配置，请重启 costrict-router 生成"))
		return false
	}
	writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("missing or invalid local API key", "缺少或提供了无效的本地 API Key"))
	return false
}

func (h *Handler) authorizeLocalAPIKey(w http.ResponseWriter, r *http.Request) bool {
	cfg := h.Tokens.Config()
	if cfg.AuthDisabled {
		// 鉴权已被显式关闭：不校验本地 API Key，无 token 或空 token 均放行。
		return true
	}
	if cfg.LocalAPIKeyHash == "" {
		writeOpenAIError(w, http.StatusInternalServerError, "configuration_error", i18n.T("local API key is not configured; restart costrict-router to generate one", "本地 API Key 未配置，请重启 costrict-router 生成"))
		return false
	}
	apiKey := localAPIKeyFromRequest(r)
	if apiKey == "" {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("missing local API key", "缺少本地 API Key"))
		return false
	}
	if !cfg.VerifyLocalAPIKey(apiKey) {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("invalid local API key", "本地 API Key 无效"))
		return false
	}
	return true
}

func validLocalAPIKey(cfg config.Config, r *http.Request) bool {
	return cfg.LocalAPIKeyHash != "" && cfg.VerifyLocalAPIKey(localAPIKeyFromRequest(r))
}

func localAPIKeyFromRequest(r *http.Request) string {
	apiKey, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok || apiKey == "" {
		// Anthropic 风格客户端用 x-api-key 鉴权，OpenAI 风格用 Authorization: Bearer，两者都接受。
		apiKey = strings.TrimSpace(r.Header.Get("x-api-key"))
	}
	return apiKey
}

// forwardCompat 处理需要协议转换的入口（/v1/responses、/v1/messages）：
// 先把客户端请求体翻译成 Chat Completions，转发到上游，再把响应翻译回客户端协议。
func (h *Handler) forwardCompat(w http.ResponseWriter, r *http.Request, codec compat.Codec) {
	start := time.Now()
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	chatBody, stream, err := codec.DecodeRequest(bodyBytes)
	if err != nil {
		if apiErr := compat.AsAPIError(err); apiErr != nil {
			writeOpenAIError(w, apiErr.Status, apiErr.Type, apiErr.Message)
			return
		}
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if err := h.Tokens.EnsureFreshToken(r.Context()); err != nil {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", err.Error())
		return
	}
	cfg := h.Tokens.Config()
	if !cfg.LoggedIn() {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("not logged in; run costrict-router login first", "未登录，请先执行 costrict-router login"))
		return
	}

	// 未知模型替换：codex 辅助功能（multi_agent/guardian/记忆）与 claude-code 会用上游不存在的
	// 模型名，统一替换成兜底模型（用户配置的 fallback_model 或第一个可用模型），避免这些功能失败。
	h.ensureModels(r.Context(), cfg)
	chatBody = h.applyModelSubstitution(chatBody, cfg.FallbackModel)
	chatBody, err = compat.ApplyUpstreamModelPolicy(chatBody)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	parallelToolCalls := compat.ChatParallelToolCalls(chatBody)

	upstreamURL, err := joinURL(cfg.BaseURL, "/chat-rag/api/v1/chat/completions")
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "configuration_error", err.Error())
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, upstreamURL, bytes.NewReader(chatBody))
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	copySelectedHeaders(req.Header, r.Header)
	req.Header.Set("Content-Type", "application/json")
	applyCostrictHeaders(req.Header, cfg, r)

	requestID := req.Header.Get("X-Request-ID")
	chatSummary := summarizeChatRequest(chatBody)
	if h.Logger != nil && h.Logger.DebugEnabled() && h.DebugFullRequest {
		h.Logger.Debugf("forward %s request id=%s method=%s path=%s upstream=%s headers=%v body=%q",
			codec.Name(), requestID, r.Method, r.URL.Path, upstreamURL, logx.RedactHeader(req.Header), logx.TruncateBody(chatBody, 32*1024))
	}

	resp, err := h.httpClient().Do(req)
	headersAt := time.Now()
	if err != nil {
		h.logUpstreamFailure(r, requestID, err)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	status := resp.StatusCode
	outputFormat := responseFormat(resp.Header)
	var responseBody io.Reader = resp.Body
	switch {
	case status >= 200 && status < 300 && stream && outputFormat == "sse":
		responseBody = codec.EncodeStream(resp.Body, modelName(chatBody), parallelToolCalls)
		copyTransformedResponseHeaders(w.Header(), resp.Header, "text/event-stream")
		outputFormat = "sse"
	case status >= 200 && status < 300:
		upstreamBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			writeOpenAIError(w, http.StatusBadGateway, "upstream_error", readErr.Error())
			return
		}
		converted, convertErr := codec.EncodeResponse(upstreamBody, parallelToolCalls)
		if convertErr != nil {
			writeOpenAIError(w, http.StatusBadGateway, "proxy_error", convertErr.Error())
			return
		}
		responseBody = bytes.NewReader(converted)
		copyTransformedResponseHeaders(w.Header(), resp.Header, "application/json")
		outputFormat = "json"
	default:
		// 上游错误体是 OpenAI 形状，转换成目标协议的错误信封，避免客户端 SDK 解析失败。
		upstreamBody, _ := io.ReadAll(resp.Body)
		responseBody = bytes.NewReader(codec.EncodeError(status, upstreamBody))
		copyTransformedResponseHeaders(w.Header(), resp.Header, "application/json")
		outputFormat = "json"
	}

	var collector *responseMetricsCollector
	if h.Logger != nil && h.Logger.DebugEnabled() {
		collector = newResponseMetricsCollector(start, headersAt, outputFormat)
		responseBody = collector.wrap(responseBody)
	}
	w.WriteHeader(status)
	_, copyErr := copyAndFlush(w, responseBody)
	if h.Logger != nil {
		if h.Logger.DebugEnabled() {
			if collector != nil {
				h.logChatMetrics(requestID, r, status, start, chatSummary, collector.finish(), len(chatBody), copyErr)
			}
		} else if status >= 400 {
			h.Logger.Warnf(i18n.T("upstream returned error method=%s path=%s status=%d request_id=%s duration=%s", "上游返回错误 method=%s path=%s status=%d request_id=%s duration=%s"),
				r.Method, r.URL.Path, status, requestID, time.Since(start))
		}
	}
	if copyErr != nil && h.Logger != nil {
		h.Logger.Warnf(i18n.T("failed to copy upstream response request_id=%s err=%v", "复制上游响应失败 request_id=%s err=%v"), requestID, copyErr)
	}
}

func (h *Handler) forward(w http.ResponseWriter, r *http.Request, upstreamPath string, isChatCompletion bool) {
	// 转发前确保 token 可用，再把 OpenAI 兼容路径映射到真实 CoStrict 上游接口。
	start := time.Now()
	if err := h.Tokens.EnsureFreshToken(r.Context()); err != nil {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", err.Error())
		return
	}

	cfg := h.Tokens.Config()
	if !cfg.LoggedIn() {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("not logged in; run costrict-router login first", "未登录，请先执行 costrict-router login"))
		return
	}

	upstreamURL, err := joinURL(cfg.BaseURL, upstreamPath)
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "configuration_error", err.Error())
		return
	}
	if r.URL.RawQuery != "" && upstreamPath != "/ai-gateway/api/v1/models" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	var body io.Reader = r.Body
	var bodyBytes []byte
	var chatSummary chatRequestSummary
	// chat/completions 需读取 body 以替换未知模型；debug 模式也会读 body 用于摘要/日志。
	needBody := r.Body != nil && (isChatCompletion || h.shouldInspectRequest(isChatCompletion))
	if needBody {
		bodyBytes, _ = io.ReadAll(r.Body)
		if isChatCompletion && h.Models != nil {
			h.ensureModels(r.Context(), cfg)
			bodyBytes = h.applyModelSubstitution(bodyBytes, cfg.FallbackModel)
		}
		if isChatCompletion {
			bodyBytes, err = compat.ApplyUpstreamModelPolicy(bodyBytes)
			if err != nil {
				writeOpenAIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
		}
		body = bytes.NewReader(bodyBytes)
		if isChatCompletion {
			chatSummary = summarizeChatRequest(bodyBytes)
		}
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, body)
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	copySelectedHeaders(req.Header, r.Header)
	applyCostrictHeaders(req.Header, cfg, r)

	requestID := req.Header.Get("X-Request-ID")
	if h.Logger != nil && h.Logger.DebugEnabled() && h.DebugFullRequest {
		h.Logger.Debugf("forward request id=%s method=%s path=%s upstream=%s headers=%v body=%q",
			requestID, r.Method, r.URL.Path, upstreamURL, logx.RedactHeader(req.Header), logx.TruncateBody(bodyBytes, 32*1024))
	}

	resp, err := h.httpClient().Do(req)
	headersAt := time.Now()
	if err != nil {
		h.logUpstreamFailure(r, requestID, err)
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	var collector *responseMetricsCollector
	responseBody := io.Reader(resp.Body)
	if h.Logger != nil && h.Logger.DebugEnabled() && isChatCompletion {
		collector = newResponseMetricsCollector(start, headersAt, responseFormat(resp.Header))
		responseBody = collector.wrap(responseBody)
	}
	_, copyErr := copyAndFlush(w, responseBody)
	if h.Logger != nil {
		if h.Logger.DebugEnabled() {
			if isChatCompletion && collector != nil {
				h.logChatMetrics(requestID, r, resp.StatusCode, start, chatSummary, collector.finish(), len(bodyBytes), copyErr)
			} else {
				h.Logger.Debugf(i18n.T("forward response id=%s method=%s path=%s status=%d duration=%s", "转发响应 id=%s method=%s path=%s status=%d 总耗时=%s"),
					requestID, r.Method, r.URL.Path, resp.StatusCode, time.Since(start))
			}
		} else if resp.StatusCode >= 400 {
			h.Logger.Warnf(i18n.T("upstream returned error method=%s path=%s status=%d request_id=%s duration=%s", "上游返回错误 method=%s path=%s status=%d request_id=%s duration=%s"),
				r.Method, r.URL.Path, resp.StatusCode, requestID, time.Since(start))
		}
	}
	if copyErr != nil && h.Logger != nil {
		h.Logger.Warnf(i18n.T("failed to copy upstream response request_id=%s err=%v", "复制上游响应失败 request_id=%s err=%v"), requestID, copyErr)
	}
}

// handleModels 转发 /v1/models 到上游模型列表接口。对 Claude Code 的请求（带 anthropic-version
// 头或 claude-code/ UA）会给每个模型 id 加 claude- 前缀，使其能进入 Claude Code 的 /model 选择器；
// 其它客户端（codex、OpenAI 风格工具）原样返回，互不影响。
func (h *Handler) handleModels(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	if err := h.Tokens.EnsureFreshToken(r.Context()); err != nil {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", err.Error())
		return
	}
	cfg := h.Tokens.Config()
	if !cfg.LoggedIn() {
		writeOpenAIError(w, http.StatusUnauthorized, "authentication_error", i18n.T("not logged in; run costrict-router login first", "未登录，请先执行 costrict-router login"))
		return
	}
	upstreamURL, err := joinURL(cfg.BaseURL, "/ai-gateway/api/v1/models")
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "configuration_error", err.Error())
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, upstreamURL, nil)
	if err != nil {
		writeOpenAIError(w, http.StatusServiceUnavailable, "proxy_error", err.Error())
		return
	}
	copySelectedHeaders(req.Header, r.Header)
	applyCostrictHeaders(req.Header, cfg, r)

	requestID := req.Header.Get("X-Request-ID")
	resp, err := h.httpClient().Do(req)
	if err != nil {
		if h.Logger != nil {
			h.Logger.Warnf(i18n.T("upstream request failed method=%s path=%s request_id=%s err=%v", "上游请求失败 method=%s path=%s request_id=%s err=%v"), r.Method, r.URL.Path, requestID, err)
		}
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		switch {
		case isAnthropicClient(r):
			if rewritten, ok := addClaudeModelAlias(body); ok {
				body = rewritten
			}
			// Anthropic SDK 的 models.list() 要求 Anthropic 格式（type/created_at/max_input_tokens）。
			if converted, ok := convertToAnthropicModelsFormat(body); ok {
				body = converted
			}
		case isCodexClient(r):
			if converted, ok := convertToCodexModelsFormat(body); ok {
				body = converted
			}
		}
	}
	copyResponseHeaders(w.Header(), resp.Header)
	// body 可能被改写，Content-Length 必须按当前长度重设（覆盖上游可能带的旧值）。
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(resp.StatusCode)
	if _, err := w.Write(body); err != nil && h.Logger != nil {
		h.Logger.Warnf(i18n.T("failed to copy upstream response request_id=%s err=%v", "复制上游响应失败 request_id=%s err=%v"), requestID, err)
	}
	if h.Logger != nil && h.Logger.DebugEnabled() {
		h.Logger.Debugf(i18n.T("forward response id=%s method=%s path=%s status=%d duration=%s", "转发响应 id=%s method=%s path=%s status=%d 总耗时=%s"),
			requestID, r.Method, r.URL.Path, resp.StatusCode, time.Since(start))
	}
}

// isAnthropicClient 判断请求是否来自 Anthropic 协议客户端（主要是 Claude Code）：
// 携带 anthropic-version 头，或 User-Agent 以 claude-code/ 开头。codex / OpenAI 风格工具都不满足。
func isAnthropicClient(r *http.Request) bool {
	if r.Header.Get("anthropic-version") != "" {
		return true
	}
	return strings.HasPrefix(r.Header.Get("User-Agent"), "claude-code/")
}

// isCodexClient 兼容 Codex CLI/App 的当前与历史 User-Agent，并接受其 originator 标识。
func isCodexClient(r *http.Request) bool {
	ua := strings.ToLower(r.Header.Get("User-Agent"))
	originator := strings.ToLower(r.Header.Get("originator"))
	return strings.Contains(ua, "codex_cli_rs/") || strings.HasPrefix(ua, "codex/") || strings.Contains(originator, "codex")
}

func (h *Handler) shouldInspectRequest(isChatCompletion bool) bool {
	return h.Logger != nil && h.Logger.DebugEnabled() && (isChatCompletion || h.DebugFullRequest)
}

func (h *Handler) logChatMetrics(requestID string, r *http.Request, status int, start time.Time, req chatRequestSummary, resp responseMetrics, inputBytes int, copyErr error) {
	errorText := ""
	if copyErr != nil {
		errorText = copyErr.Error()
	}
	h.Logger.Debugf(i18n.T(
		"chat metrics id=%s model=%s stream=%t status=%d messages=%d tools=%d max_tokens=%s temperature=%s top_p=%s request_bytes=%d response_bytes=%d headers_latency=%s ttfb=%s duration=%s usage=%s tps=%s copy_error=%s",
		"对话指标 id=%s 模型=%s 流式=%t 状态=%d 消息数=%d 工具数=%d max_tokens=%s temperature=%s top_p=%s 请求字节=%d 响应字节=%d 响应头耗时=%s 首字节耗时=%s 总耗时=%s token=%s 生成速度=%s 复制错误=%s",
	),
		requestID,
		valueOrUnknown(req.Model),
		req.Stream,
		status,
		req.MessagesCount,
		req.ToolsCount,
		valueOrUnknown(req.MaxTokens),
		valueOrUnknown(req.Temperature),
		valueOrUnknown(req.TopP),
		inputBytes,
		resp.Bytes,
		formatDuration(resp.HeadersLatency),
		formatDuration(resp.TTFB),
		formatDuration(resp.Duration),
		resp.Usage.String(),
		resp.TokensPerSecond(),
		valueOrNone(errorText),
	)
}

// logUpstreamFailure 区分“客户端断开导致转发取消”与真正的上游错误：
// 前者是客户端超时/主动取消的正常结果，记为 info，避免被误判为上游故障。
func (h *Handler) logUpstreamFailure(r *http.Request, requestID string, err error) {
	if h.Logger == nil {
		return
	}
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		h.Logger.Infof(i18n.T("client disconnected; upstream forward canceled method=%s path=%s request_id=%s", "客户端已断开，转发随之取消 method=%s path=%s request_id=%s"), r.Method, r.URL.Path, requestID)
		return
	}
	h.Logger.Warnf(i18n.T("upstream request failed method=%s path=%s request_id=%s err=%v", "上游请求失败 method=%s path=%s request_id=%s err=%v"), r.Method, r.URL.Path, requestID, err)
}

func applyCostrictHeaders(h http.Header, cfg config.Config, incoming *http.Request) {
	// 补齐 CoStrict 上游依赖的认证、用户、请求追踪和客户端上下文头。
	requestID := ids.UUID()
	taskID := ids.UUID()
	h.Set("Authorization", "Bearer "+cfg.AccessToken)
	h.Set("Accept-Language", firstHeader(incoming, "Accept-Language", "zh-CN"))
	h.Set("HTTP-Referer", firstHeader(incoming, "HTTP-Referer", "https://github.com/RooVetGit/Roo-Cline"))
	h.Set("X-Title", firstHeader(incoming, "X-Title", "Roo Code"))
	h.Set("User-Agent", fmt.Sprintf("costrict-router/%s (%s/%s)", version.Current, runtime.GOOS, runtime.GOARCH))
	h.Set("X-Costrict-Version", cfg.PluginVersion)
	h.Set("x-quota-identity", firstHeader(incoming, "x-quota-identity", "system"))
	h.Set("X-Request-ID", requestID)
	h.Set("zgsm-request-id", requestID)
	h.Set("zgsm-task-id", taskID)
	h.Set("x-user-id", cfg.UserID)
	h.Set("zgsm-client-id", cfg.MachineCode)
	h.Set("zgsm-provider", "costrict")
	h.Set("x-caller", "chat")
	for _, key := range []string{"zgsm-project-path", "zgsm-prompt-tags", "agent-type"} {
		if value := incoming.Header.Get(key); value != "" {
			h.Set(key, value)
		}
	}
}

func copySelectedHeaders(dst, src http.Header) {
	for _, key := range []string{"Accept", "Content-Type", "Cache-Control"} {
		if value := src.Get(key); value != "" {
			dst.Set(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHop(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyTransformedResponseHeaders(dst, src http.Header, contentType string) {
	for key, values := range src {
		if isHopByHop(key) || strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Content-Type") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	dst.Set("Content-Type", contentType)
}

func modelName(body []byte) string {
	var payload struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &payload)
	return payload.Model
}

func isHopByHop(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func joinURL(base, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("base_url 无效: %s", base)
	}
	u.Path = path
	u.RawQuery = ""
	return u.String(), nil
}

func firstHeader(r *http.Request, key, fallback string) string {
	if value := r.Header.Get(key); value != "" {
		return value
	}
	return fallback
}

func bearerToken(value string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

func validStatusToken(expected, got string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func (h *Handler) httpClient() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

func writeOpenAIError(w http.ResponseWriter, status int, typ, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    typ,
			"message": message,
		},
	})
}
