package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
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
	r.GET("/api/admin/monitors", h.AdminListMonitors)
	r.POST("/api/admin/monitors", h.AdminCreateMonitor)
	r.GET("/api/admin/monitors/:key", h.AdminGetMonitor)
	r.PUT("/api/admin/monitors/:key", h.AdminUpdateMonitor)
	r.POST("/api/admin/monitors/:key/toggle", h.AdminToggleMonitor)
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

// TestAdminListMonitorsSearch 锁定列表搜索口径：表格「通道」列展示的是 channel_name，
// 搜索必须能按它命中；多词不限顺序且须全部命中；分隔符/大小写不敏感；子通道的模型名
// 与 base_url 也可搜。
func TestAdminListMonitorsSearch(t *testing.T) {
	r := newAdminMonitorTestHandler(t)

	create := func(body string) {
		t.Helper()
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-token")
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
		}
	}
	create(`{"monitors":[
		{"provider":"0-0","service":"cx","channel":"o-api","channel_name":"O-Team/Plus","model":"GPT","template":"cx-tiny","base_url":"https://api.zero.example"},
		{"provider":"0-0","service":"cx","channel":"o-api","parent":"0-0/cx/o-api","model":"gpt-5.6-sol"}]}`)
	create(`{"monitors":[{"provider":"0-0","service":"cc","channel":"o-max-main","channel_name":"O-Max","model":"Opus","template":"cc-tiny","base_url":"https://cc.zero.example"}]}`)
	create(`{"monitors":[{"provider":"acme","service":"cc","channel":"vip","model":"Opus","template":"cc-tiny","base_url":"https://acme.example"}]}`)
	create(`{"monitors":[{"provider":"100x","service":"cx","channel":"o-pro","channel_name":"O-Puls/Pro","model":"GPT","template":"cx-tiny","base_url":"https://api.100x.example"}]}`)

	list := func(q string) []string {
		t.Helper()
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/api/admin/monitors?q="+url.QueryEscape(q), nil)
		req.Header.Set("Authorization", "Bearer test-token")
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
		}
		var resp struct {
			Monitors []config.MonitorSummary `json:"monitors"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal list resp: %v", err)
		}
		keys := make([]string, 0, len(resp.Monitors))
		for _, m := range resp.Monitors {
			keys = append(keys, m.Key)
		}
		sort.Strings(keys)
		return keys
	}

	const (
		zeroCX = "0-0--cx--o-api"
		zeroCC = "0-0--cc--o-max-main"
		acme   = "acme--cc--vip"
		x100   = "100x--cx--o-pro"
	)
	cases := []struct {
		q    string
		want []string
	}{
		{"", []string{zeroCX, x100, zeroCC, acme}},
		{"team", []string{zeroCX}},             // 只在 channel_name 里
		{"o-max 0-0", []string{zeroCC}},        // 多词、顺序与存储相反
		{"omax", []string{zeroCC}},             // 词里没分隔符：忽略字段里的分隔符
		{"O_TEAM/plus", []string{zeroCX}},      // 大小写不敏感，分隔符种类互通
		{"0-0", []string{zeroCX, zeroCC}},      // 词里有分隔符：按位置比对，不命中 100x
		{"00", []string{zeroCX, x100, zeroCC}}, // 没分隔符的 00 照旧宽匹配
		{"5.6", []string{zeroCX}},              // 子通道模型名 gpt-5.6-sol
		{"gpt56", []string{zeroCX}},
		{"gpt5.6", []string{}},           // 分隔符位置对不上（取舍：宁可少配也不串）
		{"acme.example", []string{acme}}, // base_url
		{"opus", []string{zeroCC, acme}},
		{"0-0 acme", []string{}},                        // 多词须全部命中
		{"0cx", []string{}},                             // 不得跨字段边界拼出命中
		{" - / ", []string{zeroCX, x100, zeroCC, acme}}, // 全分隔符的输入等于没输
	}
	for _, tc := range cases {
		got := list(tc.q)
		sort.Strings(tc.want)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("q=%q: got %v, want %v", tc.q, got, tc.want)
		}
	}

	// 搜索语料只在服务端用，不得下发
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/admin/monitors", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	r.ServeHTTP(w, req)
	if strings.Contains(strings.ToLower(w.Body.String()), "search") {
		t.Errorf("list response leaks search fields: %s", w.Body.String())
	}
}
