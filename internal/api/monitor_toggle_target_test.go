package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
)

// disabledFlags 按文件内行序摘出 Disabled，断言失败时只打这一个切片——
// 直接打 []ServiceConfig 会刷出三屏字段，淹掉真正的差异。
func disabledFlags(f config.MonitorFile) []bool {
	flags := make([]bool, len(f.Monitors))
	for i, m := range f.Monitors {
		flags[i] = m.Disabled
	}
	return flags
}

// newToggleFixture 建一个「一父两子、两个子行 model 都为空」的通道——这正是生产上
// 模板驱动子行的形态，也是展示名无法区分两行、只能靠 model_id 定位的那个形状。
// 返回路由与创建后的文件（含各行铸好的 model_id）。
func newToggleFixture(t *testing.T) (*gin.Engine, config.MonitorFile) {
	t.Helper()
	r := newAdminMonitorTestHandler(t)

	body := `{"monitors":[` +
		`{"provider":"acme","service":"cc","channel":"vip","model":"Opus","template":"cc-opus","base_url":"https://x.com"},` +
		`{"parent":"acme/cc/vip","template":"cc-sonnet"},` +
		`{"parent":"acme/cc/vip","template":"cc-fable"}]}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors", strings.NewReader(body))
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
	if len(created.Monitor.Monitors) != 3 {
		t.Fatalf("期望 1 父 2 子，实际 %d 行", len(created.Monitor.Monitors))
	}
	for i, m := range created.Monitor.Monitors {
		if !config.IsValidModelID(m.ModelID) {
			t.Fatalf("第 %d 行缺 model_id：%+v", i, m)
		}
	}
	return r, created.Monitor
}

// postToggle 发一次 toggle，返回状态码与解析后的磁盘态（非 200 时第二个返回值为零值）。
func postToggle(t *testing.T, r *gin.Engine, payload string) (int, config.MonitorFile) {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/admin/monitors/acme--cc--vip/toggle", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		return w.Code, config.MonitorFile{}
	}
	var resp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal toggle resp: %v", err)
	}
	return w.Code, resp.Monitor
}

// readBack 重新 GET 磁盘上的文件，确认返回体不是唯一真相。
func readBack(t *testing.T, r *gin.Engine) config.MonitorFile {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/admin/monitors/acme--cc--vip", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("回读失败：status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Monitor config.MonitorFile `json:"monitor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal get resp: %v", err)
	}
	return resp.Monitor
}

// TestAdminToggleMonitor_ModelIDTargetsExactlyOneRow：带 model_id 只改中标那一行，
// 父行与另一个子行不受影响；且所有行的 model_id 原样保留（id 变了就等于断历史）。
func TestAdminToggleMonitor_ModelIDTargetsExactlyOneRow(t *testing.T) {
	r, created := newToggleFixture(t)
	targetID := created.Monitors[2].ModelID // 第二个子行

	code, got := postToggle(t, r, `{"field":"disabled","value":true,"model_id":"`+targetID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}

	for _, file := range []config.MonitorFile{got, readBack(t, r)} {
		if got, want := disabledFlags(file), []bool{false, false, true}; !slices.Equal(got, want) {
			t.Errorf("只应停用中标的那一行：disabled = %v, want %v", got, want)
		}
		for i := range file.Monitors {
			if file.Monitors[i].ModelID != created.Monitors[i].ModelID {
				t.Errorf("第 %d 行 model_id 被改动：%q → %q",
					i, created.Monitors[i].ModelID, file.Monitors[i].ModelID)
			}
		}
	}

	// 反向切回：同一个 model_id 必须能把它放出来（停用不是单向门）。
	code, back := postToggle(t, r, `{"field":"disabled","value":false,"model_id":"`+targetID+`"}`)
	if code != http.StatusOK || back.Monitors[2].Disabled {
		t.Errorf("启用失败：status = %d, disabled = %v", code, disabledFlags(back))
	}
}

// TestAdminToggleMonitor_ModelIDAlsoTargetsHidden：选择器与 field 正交，hidden 分支
// 同样只改中标行。两个 field 共用一个 applyToggle，分叉了就是这里先红。
func TestAdminToggleMonitor_ModelIDAlsoTargetsHidden(t *testing.T) {
	r, created := newToggleFixture(t)

	code, got := postToggle(t, r, `{"field":"hidden","value":true,"model_id":"`+created.Monitors[1].ModelID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	hidden := []bool{got.Monitors[0].Hidden, got.Monitors[1].Hidden, got.Monitors[2].Hidden}
	if !slices.Equal(hidden, []bool{false, true, false}) {
		t.Errorf("只应隐藏中标的那一行：hidden = %v", hidden)
	}
	if got, want := disabledFlags(got), []bool{false, false, false}; !slices.Equal(got, want) {
		t.Errorf("切 hidden 不应动 disabled：%v", got)
	}
}

// TestAdminToggleMonitor_UnknownModelIDNeverFallsBackToRoot 是本次改动最关键的一条
// 守卫：找不到目标必须 404、且一个字节都不写。若退化成「找不到就改父行」，用户点一个
// 子模型的停用会把整条通道停掉，而界面上看不出区别。
func TestAdminToggleMonitor_UnknownModelIDNeverFallsBackToRoot(t *testing.T) {
	r, created := newToggleFixture(t)

	code, _ := postToggle(t, r, `{"field":"disabled","value":true,"model_id":"md_deadbeef-0000-4000-8000-000000000000"}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}

	after := readBack(t, r)
	if after.Metadata.Revision != created.Metadata.Revision {
		t.Errorf("失败的 toggle 不应递增 revision：%d → %d", created.Metadata.Revision, after.Metadata.Revision)
	}
	for i, m := range after.Monitors {
		if m.Disabled || m.Hidden {
			t.Errorf("第 %d 行不应被改动：%+v", i, m)
		}
	}
}

// TestAdminToggleMonitor_BlankModelIDRejected：显式传空/空白/null 的 model_id 都是
// 「前端渲染了一个没有目标的按钮」，必须 400，绝不能按「字段缺省」的父行语义静默执行。
//
// `null` 那条是 codex review 抓出来的真实退化：用 *string 接收时 JSON null 与字段缺省
// 都解成 nil，于是 {"model_id":null} 会 200 并停掉**整条通道**（本地实测复现过）。
func TestAdminToggleMonitor_BlankModelIDRejected(t *testing.T) {
	for _, raw := range []string{`""`, `"   "`, `null`} {
		t.Run(raw, func(t *testing.T) {
			r, created := newToggleFixture(t)

			code, _ := postToggle(t, r, `{"field":"disabled","value":true,"model_id":`+raw+`}`)
			if code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", code)
			}

			after := readBack(t, r)
			if after.Metadata.Revision != created.Metadata.Revision {
				t.Errorf("被拒的请求不应写盘：revision %d → %d", created.Metadata.Revision, after.Metadata.Revision)
			}
			if got, want := disabledFlags(after), []bool{false, false, false}; !slices.Equal(got, want) {
				t.Errorf("被拒的请求不应改动任何行：disabled = %v, want %v", got, want)
			}
		})
	}
}

// TestAdminToggleMonitor_OmittedModelIDStillTargetsRoot 钉住向后兼容：旧 payload
// （只有 field/value）语义不变——改父行，子行一律不动。
func TestAdminToggleMonitor_OmittedModelIDStillTargetsRoot(t *testing.T) {
	r, _ := newToggleFixture(t)

	code, got := postToggle(t, r, `{"field":"hidden","value":true}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if !got.Monitors[0].Hidden {
		t.Errorf("父行应被隐藏：%+v", got.Monitors[0])
	}
	if got.Monitors[1].Hidden || got.Monitors[2].Hidden {
		t.Errorf("子行不应被隐藏：%+v", got.Monitors[1:])
	}
}
