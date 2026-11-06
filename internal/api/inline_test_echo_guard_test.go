package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/identity"
	"monitor/internal/probe"
)

// TestPublicInlineTestBind_RejectsEchoBypasses 锁住两条公开测试端点（收录 /api/onboarding/test
// 与变更 /api/change/test）在发起探测前就拒掉回显作弊的前置形状：带查询串/fragment 的 base_url，
// 以及过短或首尾带空白的 key（Go 发请求会裁掉请求头值首尾空白，回显回来的 key 就和原样对不上，
// InlineProber 的回显检测因此失效）。两类检查都在 SSRF 守卫的 DNS 解析之前，不需要网络。
func TestPublicInlineTestBind_RejectsEchoBypasses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{inlineProber: probe.NewInlineProber(1, identity.NewUserIDManager())}

	binds := map[string]func(c *gin.Context) bool{
		"onboarding": func(c *gin.Context) bool { _, ok := h.bindOnboardingTestRequest(c); return ok },
		"change":     func(c *gin.Context) bool { _, ok := h.bindInlineTestRequest(c); return ok },
	}
	cases := []struct {
		name, baseURL, apiKey, wantMsg string
	}{
		{"查询串", "https://httpbin.org/anything?RP_ANSWER=20", "sk-long-enough-key", "查询参数"},
		{"空问号", "https://api.example.com/base?", "sk-long-enough-key", "查询参数"},
		{"fragment", "https://api.example.com/base#x", "sk-long-enough-key", "片段"},
		{"key 过短", "https://api.example.com", "short", "过短"},
		{"key 尾部空格", "https://api.example.com", "abcdefghijk ", "首尾"},
		{"key 尾部换行", "https://api.example.com", "abcdefghijk\n", "首尾"},
	}
	for flow, bind := range binds {
		for _, tc := range cases {
			t.Run(flow+"/"+tc.name, func(t *testing.T) {
				payload, _ := json.Marshal(map[string]string{
					"service_type":  "cc",
					"template_name": "cc-haiku-arith",
					"base_url":      tc.baseURL,
					"api_key":       tc.apiKey,
					"target_key":    "acme--cc--main",
				})
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(string(payload)))
				c.Request.Header.Set("Content-Type", "application/json")

				if bind(c) {
					t.Fatal("应在探测前被拒，实际放行")
				}
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.wantMsg) {
					t.Fatalf("期望 400 且含 %q，实际 %d %s", tc.wantMsg, w.Code, w.Body.String())
				}
			})
		}
	}
}
