package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"costrict-router/internal/auth"
	"costrict-router/internal/config"
	"costrict-router/internal/i18n"
	"costrict-router/internal/logx"
	"costrict-router/internal/proxy"
)

type Service struct {
	configPath string
	cfg        config.Config
	mu         sync.RWMutex
	logger     *logx.Logger
}

func New(configPath string, cfg config.Config, logger *logx.Logger) *Service {
	cfg.ApplyDefaults()
	return &Service{
		configPath: configPath,
		cfg:        cfg,
		logger:     logger,
	}
}

func (s *Service) Config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Service) EnsureFreshToken(ctx context.Context) error {
	// 请求进入前统一刷新临近过期的 token，并把新 token 回写到配置文件。
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cfg.LoggedIn() {
		return errors.New(i18n.T("not logged in; run costrict-router login first", "未登录，请先执行 costrict-router login"))
	}
	if !auth.ExpiresWithin(s.cfg.AccessToken, 24*time.Hour) {
		return nil
	}
	if s.logger != nil {
		s.logger.Infof(i18n.T("access token expires soon; refreshing", "access token 即将过期，开始刷新"))
	}
	client := auth.NewClient(s.cfg.AuthParams())
	tokens, err := client.RefreshToken(ctx, s.cfg.RefreshToken)
	if err != nil {
		if auth.IsExpired(s.cfg.AccessToken) {
			return fmt.Errorf(i18n.T("access token expired and refresh failed: %w", "access token 已过期且刷新失败: %w"), err)
		}
		if s.logger != nil {
			s.logger.Warnf(i18n.T("token refresh failed; continuing with unexpired access token: %v", "token 刷新失败，继续使用尚未过期的 access token: %v"), err)
		}
		return nil
	}
	if err := s.cfg.ApplyTokens(tokens); err != nil {
		return err
	}
	if err := s.cfg.Save(s.configPath); err != nil {
		return err
	}
	if s.logger != nil {
		s.logger.Infof(i18n.T("token refreshed; access token expires at: %s", "token 刷新成功，access token 过期时间: %s"), s.cfg.AccessTokenExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func (s *Service) StartRefreshLoop(ctx context.Context) {
	go func() {
		for {
			delay := s.nextRefreshDelay()
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				_ = s.EnsureFreshToken(ctx)
			}
		}
	}()
}

func (s *Service) nextRefreshDelay() time.Duration {
	// 根据 access/refresh token 过期时间选择下一次后台刷新检查的等待时间。
	cfg := s.Config()
	if !cfg.LoggedIn() {
		return 24 * time.Hour
	}
	delay := 24 * time.Hour
	if !cfg.AccessTokenExpiresAt.IsZero() {
		accessDelay := time.Until(cfg.AccessTokenExpiresAt.Add(-24 * time.Hour))
		if accessDelay < delay {
			delay = accessDelay
		}
	}
	if !cfg.RefreshTokenExpiresAt.IsZero() {
		refreshDelay := time.Until(cfg.RefreshTokenExpiresAt.Add(-30 * time.Minute))
		if refreshDelay < delay {
			delay = refreshDelay
		}
	}
	if delay < time.Minute {
		return time.Minute
	}
	return delay
}

// ShutdownTokenHeader 是 /-/shutdown 鉴权头名；stop 命令从 PID 文件读取 token 后用它请求关停。
const ShutdownTokenHeader = "X-Shutdown-Token"

func Run(ctx context.Context, configPath string, cfg config.Config, addr string, logger *logx.Logger, debugFullRequest bool, shutdownToken string) error {
	// 组装本地 HTTP 服务、代理 handler 和优雅关闭入口，是 serve/start 的公共运行核心。
	if addr != "" {
		cfg.ListenAddr = addr
	}
	svc := New(configPath, cfg, logger)
	if err := svc.EnsureFreshToken(ctx); err != nil {
		return err
	}
	svc.StartRefreshLoop(ctx)

	handler := &proxy.Handler{
		Tokens:           svc,
		Client:           &http.Client{},
		Logger:           logger,
		DebugFullRequest: debugFullRequest,
		StatusToken:      shutdownToken,
		Models:           proxy.NewModelResolver(),
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	mux.HandleFunc("/-/shutdown", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// 关停接口需鉴权：必须携带与本进程匹配的 shutdown token（从本地 0600 PID 文件读取）。
		// 未配置 token（如前台 serve）时一律拒绝，避免出现无鉴权的关停入口。
		if !validShutdownToken(shutdownToken, r.Header.Get(ShutdownTokenHeader)) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if logger != nil {
			logger.Infof(i18n.T("shutdown requested; stopping local service", "收到关闭请求，准备停止本地服务"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
		go cancel()
	})
	srv := &http.Server{
		Addr:              svc.Config().ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if logger != nil {
			logger.Infof(i18n.T("local service started: http://%s/v1", "本地服务启动: http://%s/v1"), srv.Addr)
		}
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-runCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// validShutdownToken 用常量时间比较校验 shutdown token；expected 为空（未启用）时一律拒绝。
func validShutdownToken(expected, got string) bool {
	if expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
