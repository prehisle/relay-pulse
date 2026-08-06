package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
)

// newAdminMonitorTestHandler 起一个最小 admin monitors 路由 + 临时 monitors.d store。
func newAdminMonitorTestHandler(t *testing.T) *gin.Engine {
	t.Helper()
	configDir := t.TempDir()
	monitorsDir := filepath.Join(configDir, config.MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0755); err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		config:       &config.AppConfig{Onboarding: config.OnboardingConfig{AdminToken: "test-token"}},
		monitorStore: config.NewMonitorStore(monitorsDir),
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/admin/monitors", h.AdminCreateMonitor)
	r.GET("/api/admin/monitors/:key", h.AdminGetMonitor)
	r.PUT("/api/admin/monitors/:key", h.AdminUpdateMonitor)
	return r
}

// TestAdminCreateMonitorGeneratesAndExposesIDs 端到端确认：admin 创建通道会生成
// channel_id/model_id（201 响应即带出），AdminGetMonitor 返回的整个 file 也含这两个 id，
// 且响应 wire 真的含 snake_case 的 channel_id 字段（rpdiag sampler 发现契约）。
func TestAdminCreateMonitorGeneratesAndExposesIDs(t *testing.T) {
	r := newAdminMonitorTestHandler(t)

	body := `{"monitors":[{"provider":"acme","service":"cc","channel":"vip","model":"Opus","template":"cc-haiku-tiny","base_url":"https://x.com"}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	var createResp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}
	if !config.IsValidChannelID(createResp.Monitor.Metadata.ChannelID) {
		t.Errorf("create resp missing valid channel_id: %q", createResp.Monitor.Metadata.ChannelID)
	}
	if len(createResp.Monitor.Monitors) != 1 || !config.IsValidModelID(createResp.Monitor.Monitors[0].ModelID) {
		t.Errorf("create resp missing valid model_id: %+v", createResp.Monitor.Monitors)
	}

	// GET 回来，确认整个 file 含同一对 id
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodGet, "/api/admin/monitors/acme--cc--vip", nil)
	req2.Header.Set("Authorization", "Bearer test-token")
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", w2.Code, w2.Body.String())
	}
	var getResp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get resp: %v", err)
	}
	if getResp.Monitor.Metadata.ChannelID != createResp.Monitor.Metadata.ChannelID {
		t.Errorf("get channel_id mismatch: got %q want %q", getResp.Monitor.Metadata.ChannelID, createResp.Monitor.Metadata.ChannelID)
	}
	if len(getResp.Monitor.Monitors) != 1 || getResp.Monitor.Monitors[0].ModelID != createResp.Monitor.Monitors[0].ModelID {
		t.Errorf("get model_id mismatch: %+v", getResp.Monitor.Monitors)
	}
	if !strings.Contains(w2.Body.String(), `"channel_id"`) {
		t.Errorf("get response wire missing channel_id field: %s", w2.Body.String())
	}
}

// TestAdminMonitorDuplicateModelIDMapsTo400 锁死错误映射：payload 自带重复 model_id 是
// 客户端可修正的请求问题，必须回 400 + 可操作错误文本，而不是笼统的 500。
// （映射走 errors.As 认 *config.DuplicateModelIDError，不依赖错误文案子串。）
func TestAdminMonitorDuplicateModelIDMapsTo400(t *testing.T) {
	r := newAdminMonitorTestHandler(t)

	const dupID = "md_66666666-6666-4666-8666-666666666666"

	// 先建一个干净通道，供后续 PUT 使用。
	createBody := `{"monitors":[{"provider":"acme","service":"cc","channel":"vip","model":"Opus","base_url":"https://x.com"}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("准备阶段创建失败：status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create resp: %v", err)
	}

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "create",
			method: http.MethodPost,
			path:   "/api/admin/monitors",
			body: `{"monitors":[` +
				`{"provider":"acme","service":"cc","channel":"dup","model":"A","model_id":"` + dupID + `","base_url":"https://x.com"},` +
				`{"parent":"acme/cc/dup","model":"B","model_id":"` + dupID + `"}]}`,
		},
		{
			name:   "update",
			method: http.MethodPut,
			path:   "/api/admin/monitors/acme--cc--vip",
			// 两条**新增**子行携带同一个 model_id：既有行的 id 会被 copyAdminHiddenFields
			// 强制还原（id 不可变），故只有新行能把 payload 里的重复值带到盘上。
			body: `{"revision":` + strconv.FormatInt(created.Monitor.Metadata.Revision, 10) + `,"monitor":{"monitors":[` +
				`{"provider":"acme","service":"cc","channel":"vip","model":"Opus","base_url":"https://x.com"},` +
				`{"parent":"acme/cc/vip","model":"A","model_id":"` + dupID + `"},` +
				`{"parent":"acme/cc/vip","model":"B","model_id":"` + dupID + `"}]}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), dupID) {
				t.Errorf("响应应含重复的 model_id 以便定位，got %s", w.Body.String())
			}
		})
	}
}
