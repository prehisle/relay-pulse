package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/change"
	"monitor/internal/config"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
)

// clientIPUnder 用给定信任配置跑一次请求，返回 gin 解析出的客户端 IP。
func clientIPUnder(t *testing.T, cfg config.ServerConfig, remoteAddr string, headers map[string]string) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	applyClientIPTrust(router, cfg)
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// TestApplyClientIPTrust 锁定客户端 IP 信任边界。
//
// 生产事故背景：未设可信代理时 gin 默认信任所有代理并取 X-Forwarded-For 链首，
// 攻击者自带 XFF 即可任意伪造来源 IP，令限流/配额/审计三者同时失效
// （实证：5 秒内 7 条提交、7 个不同 submitter_ip_hash）。
func TestApplyClientIPTrust(t *testing.T) {
	loopback := []string{"127.0.0.1", "::1"}

	t.Run("伪造 XFF 不再被采信", func(t *testing.T) {
		got := clientIPUnder(t, config.ServerConfig{TrustedProxies: loopback},
			"203.0.113.9:1234", map[string]string{"X-Forwarded-For": "10.77.1.1"})
		if got != "203.0.113.9" {
			t.Fatalf("ClientIP=%q，非可信来源的 XFF 不应被采信", got)
		}
	})

	t.Run("可信平台 Header 优先于 XFF", func(t *testing.T) {
		got := clientIPUnder(t, config.ServerConfig{
			TrustedPlatform: "CF-Connecting-IP",
			TrustedProxies:  loopback,
		}, "127.0.0.1:1234", map[string]string{
			"CF-Connecting-IP": "198.51.100.7",
			"X-Forwarded-For":  "10.77.1.1",
		})
		if got != "198.51.100.7" {
			t.Fatalf("ClientIP=%q，应取可信平台 Header", got)
		}
	})

	t.Run("回环代理转发的 XFF 仍被采信", func(t *testing.T) {
		got := clientIPUnder(t, config.ServerConfig{TrustedProxies: loopback},
			"127.0.0.1:1234", map[string]string{"X-Forwarded-For": "198.51.100.7"})
		if got != "198.51.100.7" {
			t.Fatalf("ClientIP=%q，可信回环代理的 XFF 应被采信", got)
		}
	})

	t.Run("配置非法时收紧为不信任任何代理", func(t *testing.T) {
		got := clientIPUnder(t, config.ServerConfig{
			TrustedPlatform: "CF-Connecting-IP",
			TrustedProxies:  []string{"not-an-ip"},
		}, "127.0.0.1:1234", map[string]string{
			"CF-Connecting-IP": "198.51.100.7",
			"X-Forwarded-For":  "10.77.1.1",
		})
		if got != "127.0.0.1" {
			t.Fatalf("ClientIP=%q，非法配置必须 fail-closed 退回直连地址", got)
		}
	})
}

// TestSubmitEndpointsAreRateLimited 锁定两个 submit 端点接入 IP 限流。
// 此前只有 /test 与 /auth 接了 limiter，两个 submit 裸奔，秒级突发无闸。
func TestSubmitEndpointsAreRateLimited(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		handler func(h *Handler) gin.HandlerFunc
		wire    func(h *Handler)
	}{
		{
			name:    "onboarding submit",
			handler: func(h *Handler) gin.HandlerFunc { return h.SubmitOnboarding },
			wire:    func(h *Handler) { h.onboardingSvc = &onboarding.Service{} },
		},
		{
			name:    "change submit",
			handler: func(h *Handler) gin.HandlerFunc { return h.SubmitChange },
			wire:    func(h *Handler) { h.changeSvc = &change.Service{} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limiter := probe.NewIPLimiter(1, 1)
			defer limiter.Stop()

			h := &Handler{probeLimiter: limiter}
			tc.wire(h)

			router := gin.New()
			router.POST("/submit", tc.handler(h))

			post := func() int {
				req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader("{"))
				req.Header.Set("Content-Type", "application/json")
				req.RemoteAddr = "192.0.2.10:1234"
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				return w.Code
			}

			// 首个请求过闸后因请求体非法落到 400，证明限流没有误拦
			if got := post(); got != http.StatusBadRequest {
				t.Fatalf("首个请求 status=%d，期望 400", got)
			}
			if got := post(); got != http.StatusTooManyRequests {
				t.Fatalf("第二个请求 status=%d，期望 429", got)
			}
		})
	}
}
