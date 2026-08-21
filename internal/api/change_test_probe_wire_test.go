package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/monitor"
)

// syntheticNativeTemplate 是一份"厂商无关"模板：与 templates/cc-native-arith*.json 同形，
// 刻意不声明 model/request_model/model_vendor，模型必须由监测行提供、经 {{MODEL}} 注入。
const syntheticNativeTemplate = `{
  "url": "{{BASE_URL}}/v1/messages",
  "method": "POST",
  "headers": {"content-type": "application/json"},
  "body": {"model": "{{MODEL}}", "max_tokens": 64},
  "response": {"success_contains": "{{EXPECTED_ANSWER}}"}
}`

// newHandlerWithTemplates 造一个带临时 configDir 的 Handler：configDir 下有 monitors.d/ 与
// templates/，后者按 name→内容写入模板文件。configDir 由 monitorStore.Dir() 的父目录推导。
func newHandlerWithTemplates(t *testing.T, h *Handler, templates map[string]string) *Handler {
	t.Helper()

	configDir := t.TempDir()
	monitorsDir := filepath.Join(configDir, config.MonitorsDirName)
	templatesDir := filepath.Join(configDir, "templates")
	for _, dir := range []string{monitorsDir, templatesDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range templates {
		if err := os.WriteFile(filepath.Join(templatesDir, name+".json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h.monitorStore = config.NewMonitorStore(monitorsDir)
	return h
}

// TestChangeTestConfig_ModelReachesWire 是本轮修复真正要保的性质：
// 变更测试构造的 cfg 经 ResolveSingleMonitor + InjectVariables 之后，
// **wire 上的 body 带的是运行时父行的模型**，而不是空串。
//
// 单看 buildChangeTestConfig 返回值是不够的：模型要穿过模板解析（config > template 回退链）
// 与占位符注入两道，中间任一环把它丢了，光看 pre-resolve 的 cfg 仍会绿。
func TestChangeTestConfig_ModelReachesWire(t *testing.T) {
	registerSyntheticTestTypes(t)
	h := newHandlerWithTemplates(t, newChangeTestHandler(), map[string]string{
		"zz-b": syntheticNativeTemplate,
	})

	cfg, ok, code, body := callBuildChangeTestConfig(h, validChangeTestRequest())
	if !ok {
		t.Fatalf("构造失败，HTTP %d: %s", code, body)
	}

	if err := config.ResolveSingleMonitor(h.snapshotAppConfig(), &cfg, h.configDir()); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	_, wireBody, _, _, _, _ := monitor.InjectVariables(&cfg, identity.NewUserIDManager())

	// 父行的 request_model 优先于 model（与 {{MODEL}} 的回退链一致）
	if !strings.Contains(wireBody, `"model": "glm-4.7-250xxx"`) {
		t.Errorf("wire body 未带上父行模型，实际: %s", wireBody)
	}
	if strings.Contains(wireBody, `"model": ""`) {
		t.Errorf("wire body 出现空 model（正是本轮要根治的缺陷形态）: %s", wireBody)
	}
}

// TestRunResolvedProbe_RejectsEmptyModel 锁死"解析后仍无模型就停下"这道总闸：
// 套 native 模板却没有行级模型时，必须 400，绝不能带着 `"model": ""` 去打上游。
//
// 这条闸放在 runResolvedProbe（两条流程的共同咽喉）而不是变更侧，故收录流程同样受保护。
func TestRunResolvedProbe_RejectsEmptyModel(t *testing.T) {
	h := newHandlerWithTemplates(t, &Handler{config: &config.AppConfig{}}, map[string]string{
		"zz-native": syntheticNativeTemplate,
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// 模板不声明模型 + 监测行也没填 → 解析完仍是空
	result, ok := h.runResolvedProbe(c, config.ServiceConfig{
		Provider: "acme",
		Service:  "zz",
		Channel:  "o-broken",
		Template: "zz-native",
		BaseURL:  "https://example.com",
		APIKey:   "sk-test",
	})

	if ok {
		t.Fatal("空模型不应通过，本该 400 停下")
	}
	if result != nil {
		t.Error("被拒时不应返回探测结果（意味着真发出了请求）")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("HTTP = %d，期望 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "缺少模型") {
		t.Errorf("错误文案未命中「缺少模型」，实际: %s", w.Body.String())
	}
}
