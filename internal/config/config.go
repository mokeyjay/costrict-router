package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"costrict-router/internal/auth"
	"costrict-router/internal/compat"
)

const EnvConfigPath = "COSTRICT_ROUTER_CONFIG"

type Config struct {
	Version       int    `json:"version"`
	BaseURL       string `json:"base_url"`
	ListenAddr    string `json:"listen_addr"`
	FallbackModel string `json:"fallback_model,omitempty"`
	// ModelPolicyOverrides 按模型名（不区分大小写）覆盖内置的上游兼容降级策略，
	// 上游修复限制后用户改配置即可生效，无需等待新版本。键为模型 id，如 "Tencent-kimi-k2.6"。
	ModelPolicyOverrides  map[string]compat.ModelPolicyOverride `json:"model_policy_overrides,omitempty"`
	MachineCode           string                                `json:"machine_code"`
	State                 string                                `json:"state"`
	Provider              string                                `json:"provider"`
	PluginVersion         string                                `json:"plugin_version"`
	VSCodeVersion         string                                `json:"vscode_version"`
	URIScheme             string                                `json:"uri_scheme"`
	AccessToken           string                                `json:"access_token"`
	RefreshToken          string                                `json:"refresh_token"`
	LocalAPIKeyHash       string                                `json:"local_api_key_hash,omitempty"`
	LocalAPIKeyCreatedAt  time.Time                             `json:"local_api_key_created_at,omitempty"`
	AuthDisabled          bool                                  `json:"auth_disabled,omitempty"`
	UserID                string                                `json:"user_id"`
	AccessTokenExpiresAt  time.Time                             `json:"access_token_expires_at,omitempty"`
	RefreshTokenExpiresAt time.Time                             `json:"refresh_token_expires_at,omitempty"`
	UpdatedAt             time.Time                             `json:"updated_at"`
}

type TokenFile struct {
	AccessToken  string
	RefreshToken string
	State        string
}

func DefaultPath() (string, error) {
	if p := os.Getenv(EnvConfigPath); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "costrict-router", "config.json"), nil
}

func CacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "costrict-router"), nil
}

func DefaultLogPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "costrict-router.log"), nil
}

func DefaultPIDPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "costrict-router.pid"), nil
}

func Load(path string) (*Config, error) {
	// 加载配置时允许文件不存在，首次运行会返回一份带默认值的配置。
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("配置文件 JSON 无效: %w", err)
	}
	cfg.ApplyDefaults()
	return &cfg, nil
}

func Default() Config {
	cfg := Config{
		Version:       1,
		ListenAddr:    "127.0.0.1:14567",
		MachineCode:   auth.GenerateMachineCode(),
		Provider:      "casdoor",
		PluginVersion: "2.8.1",
		VSCodeVersion: "1.120.0",
		URIScheme:     "vscode",
	}
	cfg.ApplyDefaults()
	return cfg
}

func (c *Config) ApplyDefaults() {
	// 只补齐缺失字段，避免覆盖用户已经持久化的登录参数和偏好配置。
	if c.Version == 0 {
		c.Version = 1
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:14567"
	}
	if c.MachineCode == "" {
		c.MachineCode = auth.GenerateMachineCode()
	}
	if c.Provider == "" {
		c.Provider = "casdoor"
	}
	if c.PluginVersion == "" {
		c.PluginVersion = "2.8.1"
	}
	if c.VSCodeVersion == "" {
		c.VSCodeVersion = "1.120.0"
	}
	if c.URIScheme == "" {
		c.URIScheme = "vscode"
	}
}

func (c *Config) Save(path string) error {
	// 保存时先写临时文件再重命名，降低进程中断造成配置损坏的概率。
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	c.ApplyDefaults()
	c.UpdatedAt = time.Now()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmp, 0o600)
	}
	return os.Rename(tmp, path)
}

func (c *Config) AuthParams() auth.LoginParams {
	c.ApplyDefaults()
	return auth.LoginParams{
		BaseURL:       c.BaseURL,
		MachineCode:   c.MachineCode,
		State:         c.State,
		Provider:      c.Provider,
		PluginVersion: c.PluginVersion,
		VSCodeVersion: c.VSCodeVersion,
		URIScheme:     c.URIScheme,
	}
}

func (c *Config) ApplyLoginParams(params auth.LoginParams) {
	params = params.WithDefaults()
	c.BaseURL = strings.TrimRight(params.BaseURL, "/")
	c.MachineCode = params.MachineCode
	c.State = params.State
	c.Provider = params.Provider
	c.PluginVersion = params.PluginVersion
	c.VSCodeVersion = params.VSCodeVersion
	c.URIScheme = params.URIScheme
	c.ApplyDefaults()
}

func (c *Config) ApplyTokens(tokens auth.Tokens) error {
	// 写入 token 后尽量从 JWT claims 中提取过期时间和用户标识。
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return errors.New("token 数据缺少 access_token 或 refresh_token")
	}
	c.AccessToken = tokens.AccessToken
	c.RefreshToken = tokens.RefreshToken
	if tokens.State != "" {
		c.State = tokens.State
	}
	if claims, err := auth.DecodeClaims(tokens.AccessToken); err == nil {
		c.AccessTokenExpiresAt = claims.ExpiresAt
		if c.UserID == "" {
			c.UserID = firstNonEmpty(claims.UniversalID, claims.Subject)
		}
	}
	if claims, err := auth.DecodeClaims(tokens.RefreshToken); err == nil {
		c.RefreshTokenExpiresAt = claims.ExpiresAt
		if c.UserID == "" {
			c.UserID = firstNonEmpty(claims.UniversalID, claims.Subject)
		}
	}
	return nil
}

func (c *Config) LoggedIn() bool {
	return c.BaseURL != "" && c.AccessToken != "" && c.RefreshToken != ""
}

func ReadTokenFile(path string) (TokenFile, error) {
	// 兼容顶层 token 字段和接口 envelope.data 字段，方便测试导入真实响应。
	data, err := os.ReadFile(path)
	if err != nil {
		return TokenFile{}, err
	}
	var raw struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		State        string `json:"state"`
		Data         struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			State        string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return TokenFile{}, err
	}
	out := TokenFile{
		AccessToken:  firstNonEmpty(raw.AccessToken, raw.Data.AccessToken),
		RefreshToken: firstNonEmpty(raw.RefreshToken, raw.Data.RefreshToken),
		State:        firstNonEmpty(raw.State, raw.Data.State),
	}
	if out.AccessToken == "" || out.RefreshToken == "" {
		return TokenFile{}, errors.New("token 文件缺少 access_token 或 refresh_token")
	}
	return out, nil
}

func Redact(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 12 {
		return "***"
	}
	return s[:6] + "***" + s[len(s)-6:]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
