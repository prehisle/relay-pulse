package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
)

// newAdminAuthLimitTestRouter 起一个只做 admin 鉴权的路由；限速器每分钟回补 1 个，测试期内视同不回补。
func newAdminAuthLimitTestRouter(t *testing.T, burst int) *gin.Engine {
	t.Helper()
	h := &Handler{
		config:           &config.AppConfig{Onboarding: config.OnboardingConfig{AdminToken: "right-token"}},
		adminAuthLimiter: newAuthFailureLimiter(1, burst),
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/admin/ping", func(c *gin.Context) {
		if h.checkAdminToken(c) {
			c.Status(http.StatusOK)
		}
	})
	return r
}

func adminAuthRequest(r *gin.Engine, ip, authHeader string) int {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/ping", nil)
	req.RemoteAddr = ip + ":12345"
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// 失败次数耗尽后，同一 IP 连正确 token 也要被拒——耗尽检查若放在比对之后，被限速方仍能继续猜。
func TestAdminAuthLimiter_BlocksBeforeCompareOnceExhausted(t *testing.T) {
	r := newAdminAuthLimitTestRouter(t, 3)

	for i := 0; i < 3; i++ {
		if code := adminAuthRequest(r, "203.0.113.7", "Bearer wrong"); code != http.StatusForbidden {
			t.Fatalf("wrong token attempt %d: want 403, got %d", i+1, code)
		}
	}
	if code := adminAuthRequest(r, "203.0.113.7", "Bearer right-token"); code != http.StatusTooManyRequests {
		t.Fatalf("correct token after exhaustion: want 429, got %d", code)
	}
	if code := adminAuthRequest(r, "198.51.100.9", "Bearer right-token"); code != http.StatusOK {
		t.Fatalf("other IP with correct token: want 200, got %d", code)
	}
}

// 成功鉴权与缺头/格式错都不计入失败，站长正常使用后台不会把自己锁住。
func TestAdminAuthLimiter_OnlyTokenMismatchCounts(t *testing.T) {
	r := newAdminAuthLimitTestRouter(t, 2)
	const ip = "203.0.113.8"

	for i := 0; i < 5; i++ {
		if code := adminAuthRequest(r, ip, ""); code != http.StatusUnauthorized {
			t.Fatalf("missing header attempt %d: want 401, got %d", i+1, code)
		}
		if code := adminAuthRequest(r, ip, "Basic abc"); code != http.StatusUnauthorized {
			t.Fatalf("malformed header attempt %d: want 401, got %d", i+1, code)
		}
		if code := adminAuthRequest(r, ip, "Bearer right-token"); code != http.StatusOK {
			t.Fatalf("correct token attempt %d: want 200, got %d", i+1, code)
		}
	}
}
