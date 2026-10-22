package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	LoginPath  = "/oidc-auth/api/v1/plugin/login"
	TokenPath  = "/oidc-auth/api/v1/plugin/login/token"
	StatusPath = "/oidc-auth/api/v1/plugin/login/status"
)

type LoginParams struct {
	BaseURL       string `json:"base_url"`
	MachineCode   string `json:"machine_code"`
	State         string `json:"state"`
	Provider      string `json:"provider"`
	PluginVersion string `json:"plugin_version"`
	VSCodeVersion string `json:"vscode_version"`
	URIScheme     string `json:"uri_scheme"`
}

type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	State        string `json:"state"`
}

type Client struct {
	HTTPClient *http.Client
	Params     LoginParams
}

type tokenEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Code    any    `json:"code"`
	Data    Tokens `json:"data"`
}

type statusEnvelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		State  string `json:"state"`
		Status string `json:"status"`
	} `json:"data"`
}

func NewClient(params LoginParams) *Client {
	return &Client{
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
		Params:     params.WithDefaults(),
	}
}

func (p LoginParams) WithDefaults() LoginParams {
	if p.Provider == "" {
		p.Provider = "casdoor"
	}
	p.PluginVersion = NormalizePluginVersion(p.PluginVersion)
	p.VSCodeVersion = NormalizeVSCodeVersion(p.VSCodeVersion)
	if p.URIScheme == "" {
		p.URIScheme = "vscode"
	}
	return p
}

func ParseLoginURL(raw string) (LoginParams, error) {
	// 从插件登录链接中还原服务地址和 OIDC 轮询所需的关键参数。
	u, err := url.Parse(raw)
	if err != nil {
		return LoginParams{}, err
	}
	if u.Scheme == "" || u.Host == "" {
		return LoginParams{}, errors.New("登录 URL 缺少 scheme 或 host")
	}
	q := u.Query()
	params := LoginParams{
		BaseURL:       (&url.URL{Scheme: u.Scheme, Host: u.Host}).String(),
		MachineCode:   q.Get("machine_code"),
		State:         q.Get("state"),
		Provider:      q.Get("provider"),
		PluginVersion: q.Get("plugin_version"),
		VSCodeVersion: q.Get("vscode_version"),
		URIScheme:     q.Get("uri_scheme"),
	}.WithDefaults()
	if params.MachineCode == "" || params.State == "" {
		return LoginParams{}, errors.New("登录 URL 缺少 machine_code 或 state")
	}
	return params, nil
}

func (c *Client) BuildLoginURL() (string, error) {
	return c.buildURL(LoginPath, true)
}

func (c *Client) PollToken(ctx context.Context, interval time.Duration) (Tokens, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		tokens, err := c.GetLoginToken(ctx)
		if err == nil && tokens.AccessToken != "" && tokens.RefreshToken != "" {
			return tokens, nil
		}
		select {
		case <-ctx.Done():
			return Tokens{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) GetLoginToken(ctx context.Context) (Tokens, error) {
	return c.requestToken(ctx, "", true)
}

func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (Tokens, error) {
	return c.requestToken(ctx, refreshToken, false)
}

func (c *Client) CheckStatus(ctx context.Context, accessToken string) error {
	// 登录完成后再次校验服务端状态，避免只拿到 token 但会话未真正生效。
	rawURL, err := c.buildURL(StatusPath, accessToken == "")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("登录状态校验失败: HTTP %d", resp.StatusCode)
	}
	var out statusEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.Success || out.Data.Status != "logged_in" {
		return fmt.Errorf("登录状态不是 logged_in: %s", out.Message)
	}
	return nil
}

func (c *Client) requestToken(ctx context.Context, bearer string, includeMachine bool) (Tokens, error) {
	// 统一处理登录轮询和刷新 token，两种请求只在 bearer 与 machine_code 上有差异。
	rawURL, err := c.buildURL(TokenPath, includeMachine)
	if err != nil {
		return Tokens{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Tokens{}, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Tokens{}, fmt.Errorf("token 请求失败: HTTP %d", resp.StatusCode)
	}
	var out tokenEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Tokens{}, err
	}
	if !out.Success {
		return Tokens{}, fmt.Errorf("token 请求失败: %s", out.Message)
	}
	if out.Data.State == "" {
		out.Data.State = c.Params.State
	}
	return out.Data, nil
}

func (c *Client) buildURL(path string, includeMachine bool) (string, error) {
	// 按 CoStrict 插件接口约定拼接查询参数，machine_code 仅在需要时携带。
	base, err := url.Parse(strings.TrimRight(c.Params.BaseURL, "/"))
	if err != nil {
		return "", err
	}
	if base.Scheme == "" || base.Host == "" {
		return "", errors.New("base URL 无效")
	}
	base.Path = path
	q := base.Query()
	if includeMachine {
		q.Set("machine_code", c.Params.MachineCode)
	}
	q.Set("state", c.Params.State)
	q.Set("provider", c.Params.Provider)
	q.Set("plugin_version", c.Params.PluginVersion)
	q.Set("vscode_version", c.Params.VSCodeVersion)
	q.Set("uri_scheme", c.Params.URIScheme)
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
