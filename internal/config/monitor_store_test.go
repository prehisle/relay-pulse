package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestMonitorsDir 创建临时 monitors.d/ 结构用于测试。
// 返回 configDir（monitors.d 的父目录）和 cleanup 函数。
func setupTestMonitorsDir(t *testing.T) (string, func()) {
	t.Helper()
	configDir := t.TempDir()
	monitorsDir := filepath.Join(configDir, MonitorsDirName)
	if err := os.MkdirAll(monitorsDir, 0755); err != nil {
		t.Fatal(err)
	}
	return configDir, func() {} // t.TempDir auto-cleans
}

func writeTestMonitorFile(t *testing.T, dir, key string, content string) {
	t.Helper()
	path := filepath.Join(dir, MonitorsDirName, key+".yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func validMonitorYAML(provider, service, channel string, revision int64) string {
	return strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: " + intToStr(revision),
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: " + provider,
		"    service: " + service,
		"    channel: " + channel,
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
	}, "\n")
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	v := n
	if v < 0 {
		s = "-"
		v = -v
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return s + digits
}

func findMonitorByModel(t *testing.T, monitors []ServiceConfig, model string) ServiceConfig {
	t.Helper()
	for _, m := range monitors {
		if m.Model == model {
			return m
		}
	}
	t.Fatalf("monitor with model %q not found", model)
	return ServiceConfig{}
}

// --- SanitizeMonitorKey ---

func TestSanitizeMonitorKey_Valid(t *testing.T) {
	key, err := SanitizeMonitorKey("MyProvider--CC--VIP")
	if err != nil {
		t.Fatal(err)
	}
	if key != "myprovider--cc--vip" {
		t.Errorf("expected myprovider--cc--vip, got %s", key)
	}
}

func TestSanitizeMonitorKey_PathTraversal(t *testing.T) {
	cases := []string{
		"../evil--cc--vip",
		"ok--cc--../../etc/passwd",
		"a/b--cc--vip",
		"a\\b--cc--vip",
		"..--cc--vip",
	}
	for _, c := range cases {
		_, err := SanitizeMonitorKey(c)
		if err == nil {
			t.Errorf("expected error for key %q, got nil", c)
		}
	}
}

func TestSanitizeMonitorKey_InvalidFormat(t *testing.T) {
	cases := []string{
		"",
		"nohyphens",
		"only--two",
		"--empty--parts",
		"a----b",
	}
	for _, c := range cases {
		_, err := SanitizeMonitorKey(c)
		if err == nil {
			t.Errorf("expected error for key %q, got nil", c)
		}
	}
}

// --- MonitorStore.Create ---

func TestCreate_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "testprov", Service: "cc", Channel: "vip", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}

	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}

	// 验证文件已创建
	if file.Key != "testprov--cc--vip" {
		t.Errorf("expected key testprov--cc--vip, got %s", file.Key)
	}
	if file.Metadata.Revision != 1 {
		t.Errorf("expected revision 1, got %d", file.Metadata.Revision)
	}
	if _, err := os.Stat(file.Path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestCreate_DuplicatePSC(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "testprov--cc--vip", validMonitorYAML("testprov", "cc", "vip", 1))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "testprov", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Create(file)
	if err == nil {
		t.Fatal("expected error for duplicate PSC")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Errorf("expected '已存在' error, got: %v", err)
	}
}

func TestCreate_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "../evil", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Create(file)
	if err == nil {
		t.Fatal("expected error for path traversal provider")
	}
}

// --- MonitorStore.Get ---

func TestGet_Exists(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 3))

	file, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if file.Key != "acme--cc--vip" {
		t.Errorf("expected key acme--cc--vip, got %s", file.Key)
	}
	if file.Metadata.Revision != 3 {
		t.Errorf("expected revision 3, got %d", file.Metadata.Revision)
	}
}

func TestGet_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file, err := store.Get("nonexistent--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file != nil {
		t.Errorf("expected nil for nonexistent key, got %+v", file)
	}
}

func TestGet_YmlExtension(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// 写 .yml 文件
	content := validMonitorYAML("acme", "cc", "vip", 5)
	path := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	file, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if file == nil {
		t.Fatal("expected non-nil file for .yml extension")
	}
	if !strings.HasSuffix(file.Path, ".yml") {
		t.Errorf("expected .yml path, got %s", file.Path)
	}
}

func TestGet_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	_, err := store.Get("../evil--cc--vip")
	if err == nil {
		t.Fatal("expected error for path traversal key")
	}
}

// --- MonitorStore.Update ---

func TestUpdate_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.com"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}
	if updated.Metadata.Revision != 2 {
		t.Errorf("expected revision 2, got %d", updated.Metadata.Revision)
	}
}

func TestUpdate_RevisionConflict(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 3))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Update("acme--cc--vip", updated, 1) // 期望 1，实际 3
	if err == nil {
		t.Fatal("expected revision conflict error")
	}
	if !strings.Contains(err.Error(), "revision") {
		t.Errorf("expected 'revision' in error, got: %v", err)
	}
}

func TestUpdate_PSCImmutability(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	// 尝试通过 update 把 channel 改成 free
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "free"},
		},
	}

	err := store.Update("acme--cc--vip", updated, 1)
	if err == nil {
		t.Fatal("expected PSC immutability error")
	}
	if !strings.Contains(err.Error(), "不可变更") {
		t.Errorf("expected '不可变更' in error, got: %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "nonexistent", Service: "cc", Channel: "vip"},
		},
	}

	err := store.Update("nonexistent--cc--vip", updated, 1)
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("expected '不存在' in error, got: %v", err)
	}
}

// --- Update: admin hidden fields preservation ---

func TestUpdate_PreservesRootHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
		"    proxy: socks5://proxy.internal:1080",
		"    env_var_name: ACME_VIP_KEY",
		"    key_type: user",
		"    request_model: claude-3-5-sonnet",
		"    skip_url_validation: true",
		"    url_pattern: \"{{BASE_URL}}/v1/chat/completions\"",
		"    auto_cold_exempt: true",
	}, "\n"))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.example.com", Proxy: "socks5://proxy.internal:1080"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	root := got.Monitors[0]
	// Proxy is now JSON-visible, so it round-trips via the update payload
	if root.Proxy != "socks5://proxy.internal:1080" {
		t.Errorf("Proxy = %q, want round-tripped value", root.Proxy)
	}
	if root.EnvVarName != "ACME_VIP_KEY" {
		t.Errorf("EnvVarName = %q, want preserved", root.EnvVarName)
	}
	// KeyType is now JSON-visible and round-trips explicitly; not in update payload → empty
	if root.KeyType != "" {
		t.Errorf("KeyType = %q, want empty (JSON-visible, not preserved from disk)", root.KeyType)
	}
	if root.RequestModel != "claude-3-5-sonnet" {
		t.Errorf("RequestModel = %q, want preserved", root.RequestModel)
	}
	if !root.SkipURLValidation {
		t.Error("SkipURLValidation = false, want true")
	}
	if root.URLPattern != "{{BASE_URL}}/v1/chat/completions" {
		t.Errorf("URLPattern = %q, want preserved", root.URLPattern)
	}
	// AutoColdExempt is now JSON-visible and round-trips explicitly; not in update payload → false
	if root.AutoColdExempt {
		t.Error("AutoColdExempt = true, want false (JSON-visible, not preserved from disk)")
	}
	// JSON-visible fields should reflect the update, not the old value
	if root.Template != "cc-opus-tiny" {
		t.Errorf("Template = %q, want updated value", root.Template)
	}
}

func TestUpdate_PreservesChildHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    template: cc-haiku-tiny",
		"    base_url: https://api.example.com",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    template: child-template",
		"    proxy: http://child-proxy:8080",
		"    env_var_name: CHILD_ENV",
		"    key_type: user",
		"    request_model: gpt-4o-2024-08-06",
		"    skip_url_validation: true",
		"    url_pattern: \"{{BASE_URL}}/v1/responses\"",
		"    auto_cold_exempt: true",
	}, "\n"))

	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Template: "cc-opus-tiny", BaseURL: "https://new.example.com"},
			{Parent: "acme/cc/vip", Model: "gpt-4o", Template: "child-updated", Proxy: "http://child-proxy:8080"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-4o")
	// Proxy is now JSON-visible, so it round-trips via the update payload
	if child.Proxy != "http://child-proxy:8080" {
		t.Errorf("child.Proxy = %q, want round-tripped value", child.Proxy)
	}
	if child.EnvVarName != "CHILD_ENV" {
		t.Errorf("child.EnvVarName = %q, want preserved", child.EnvVarName)
	}
	if child.RequestModel != "gpt-4o-2024-08-06" {
		t.Errorf("child.RequestModel = %q, want preserved", child.RequestModel)
	}
	if !child.SkipURLValidation {
		t.Error("child.SkipURLValidation = false, want true")
	}
	// AutoColdExempt is now JSON-visible; not in update payload → false
	if child.AutoColdExempt {
		t.Error("child.AutoColdExempt = true, want false (JSON-visible, not preserved from disk)")
	}
}

func TestUpdate_NewChildDoesNotInheritHiddenFields(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
		"    env_var_name: CHILD_ENV",
	}, "\n"))

	// 用 gpt-5 替换 gpt-4o（新增 child 不应继承旧 child 的隐藏字段）
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "gpt-5", Template: "new-child"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-5")
	if child.Proxy != "" {
		t.Errorf("new child.Proxy = %q, want empty", child.Proxy)
	}
	if child.EnvVarName != "" {
		t.Errorf("new child.EnvVarName = %q, want empty", child.EnvVarName)
	}
}

// TestUpdate_MintsModelIDForNewChild 复现并防回归 owlai/cx/O-web 事故：
// admin「编辑→保存」给既有通道新增子通道行时，前端不携带 model_id；v2.53.0 起
// CheckRuntimeModelIDs 是 fail-closed 硬校验，任一行缺 model_id 会跳过整份配置热更新
// （admin 保存仍返回 200，运行态却静默不变）。Update 必须像 Create 一样对真新增行铸
// model_id（既有 id 幂等不动），否则一次普通编辑就会卡死全站热更新。
func TestUpdate_MintsModelIDForNewChild(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// 既有文件：父行已有稳定 id（模拟已回填的父），暂无子通道。
	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  channel_id: ch_11111111-1111-4111-8111-111111111111",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    model: Parent",
		"    model_id: md_22222222-2222-4222-8222-222222222222",
	}, "\n"))

	// admin 保存：父行（带原 id）+ 新增子通道（前端不发 model_id）。
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Parent",
				ModelID: "md_22222222-2222-4222-8222-222222222222"},
			{Parent: "acme/cc/vip", Model: "gpt-5.4"},
		},
	}
	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}

	// 新增子通道必须被铸入合法 model_id，否则 CheckRuntimeModelIDs 会拦下整份热更新。
	child := findMonitorByModel(t, got.Monitors, "gpt-5.4")
	if !IsValidModelID(child.ModelID) {
		t.Errorf("新增子通道未铸 model_id: %q", child.ModelID)
	}
	// 父行稳定 id 不可变。
	parent := findMonitorByModel(t, got.Monitors, "Parent")
	if parent.ModelID != "md_22222222-2222-4222-8222-222222222222" {
		t.Errorf("父行 model_id 应不可变, got %q", parent.ModelID)
	}
	// 整份配置应通过 fail-closed 运行时校验。
	if err := CheckRuntimeModelIDs(got.Monitors); err != nil {
		t.Errorf("CheckRuntimeModelIDs 应通过: %v", err)
	}
}

func TestUpdate_DeletedChildDisappears(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
	}, "\n"))

	// 提交时不包含子通道 → 子通道应该被删除
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 1 {
		t.Errorf("len(Monitors) = %d, want 1 (deleted child should be gone)", len(got.Monitors))
	}
}

func TestUpdate_ChildModelRenameIsRemoveAndAdd(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    proxy: http://proxy-a:8080",
		"    request_model: gpt-4o-2024-08-06",
	}, "\n"))

	// model 重命名 → 视为旧 child 删除 + 新 child 添加，不继承隐藏字段
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "gpt-4.1", Template: "renamed-child"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	child := findMonitorByModel(t, got.Monitors, "gpt-4.1")
	if child.Proxy != "" {
		t.Errorf("renamed child.Proxy = %q, want empty", child.Proxy)
	}
	if child.RequestModel != "" {
		t.Errorf("renamed child.RequestModel = %q, want empty", child.RequestModel)
	}
}

func TestUpdate_JSONVisibleFieldsNotMergedBack(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"    board: hot",
		"    category: commercial",
		"    proxy: socks5://proxy:1080",
		"    env_var_name: ACME_VIP_KEY",
	}, "\n"))

	// board 显式设为空字符串 → 应覆盖旧值，不被回填
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Board: "", Category: "public", Proxy: ""},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	root := got.Monitors[0]
	// JSON-visible fields: should reflect the incoming update, not old values
	if root.Board != "" {
		t.Errorf("Board = %q, want empty (should not be merged back)", root.Board)
	}
	if root.Category != "public" {
		t.Errorf("Category = %q, want 'public' (should reflect update)", root.Category)
	}
	// Proxy is now JSON-visible: empty in update → empty in result
	if root.Proxy != "" {
		t.Errorf("Proxy = %q, want empty (JSON-visible, should not be merged back)", root.Proxy)
	}
	// json:"-" field: should still be preserved
	if root.EnvVarName != "ACME_VIP_KEY" {
		t.Errorf("EnvVarName = %q, want preserved", root.EnvVarName)
	}
}

func TestUpdate_ChildMatchingIgnoresOrder(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  created_at: \"2026-03-14T00:00:00Z\"",
		"  updated_at: \"2026-03-14T00:00:00Z\"",
		"monitors:",
		"  - provider: acme",
		"    service: cc",
		"    channel: vip",
		"  - parent: acme/cc/vip",
		"    model: gpt-4o",
		"    env_var_name: GPT4O_KEY",
		"  - parent: acme/cc/vip",
		"    model: claude-3-7-sonnet",
		"    env_var_name: CLAUDE37_KEY",
	}, "\n"))

	// 子通道顺序反转 → 仍按 model 匹配
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "claude-3-7-sonnet"},
			{Parent: "acme/cc/vip", Model: "gpt-4o"},
		},
	}

	if err := store.Update("acme--cc--vip", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("acme--cc--vip")
	if err != nil {
		t.Fatal(err)
	}
	if c := findMonitorByModel(t, got.Monitors, "gpt-4o"); c.EnvVarName != "GPT4O_KEY" {
		t.Errorf("gpt-4o EnvVarName = %q, want preserved", c.EnvVarName)
	}
	if c := findMonitorByModel(t, got.Monitors, "claude-3-7-sonnet"); c.EnvVarName != "CLAUDE37_KEY" {
		t.Errorf("claude-3-7-sonnet EnvVarName = %q, want preserved", c.EnvVarName)
	}
}

// --- Update: child model-id–based matching (Task 8) ---

// TestPreserveHiddenFieldsAcrossChildModelRename 验证当子通道展示名被重命名时，
// 只要 model_id 相同，admin 隐藏字段（EnvVarName/RequestModel 等）仍被保留。
// 这是 Task 8 所修复的 bug：旧逻辑仅按 parent+model 匹配，改名后找不到匹配导致字段丢失。
func TestPreserveHiddenFieldsAcrossChildModelRename(t *testing.T) {
	existing := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "OldName", ModelID: "md_x", EnvVarName: "FOO", RequestModel: "real-model"},
	}}
	updated := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "NewName", ModelID: "md_x"}, // 展示名已改，model_id 不变，客户端未携带隐藏字段
	}}
	preserveAdminHiddenFields(updated, existing)
	child := &updated.Monitors[1]
	if child.EnvVarName != "FOO" {
		t.Errorf("EnvVarName 应跨改名保留，got %q", child.EnvVarName)
	}
	if child.RequestModel != "real-model" {
		t.Errorf("RequestModel 应跨改名保留，got %q", child.RequestModel)
	}
	if child.ModelID != "md_x" {
		t.Errorf("ModelID 应保留，got %q", child.ModelID)
	}
}

// TestPreserveHiddenFieldsLegacyNoIDStillMatchesByModel 验证无 model_id 的 legacy 子通道
// 仍按展示名（parent+model）匹配，确保零回归。
func TestPreserveHiddenFieldsLegacyNoIDStillMatchesByModel(t *testing.T) {
	existing := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "M1", EnvVarName: "BAR"},
	}}
	updated := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "M1"}, // 展示名不变，无 model_id
	}}
	preserveAdminHiddenFields(updated, existing)
	if updated.Monitors[1].EnvVarName != "BAR" {
		t.Errorf("legacy 按展示名匹配应保留 EnvVarName，got %q", updated.Monitors[1].EnvVarName)
	}
}

func TestDelete_Success(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	if err := store.Delete("acme--cc--vip"); err != nil {
		t.Fatal(err)
	}

	// 原文件应该不存在了
	origPath := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yaml")
	if _, err := os.Stat(origPath); !os.IsNotExist(err) {
		t.Error("expected original file to be removed")
	}

	// .archive/ 目录应该有文件
	archiveDir := filepath.Join(configDir, MonitorsDirName, ".archive")
	entries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 archived file, got %d", len(entries))
	}
	if !strings.HasPrefix(entries[0].Name(), "acme--cc--vip.") {
		t.Errorf("unexpected archive filename: %s", entries[0].Name())
	}
}

func TestDelete_YmlExtension(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// 写 .yml 文件
	content := validMonitorYAML("acme", "cc", "vip", 1)
	path := filepath.Join(configDir, MonitorsDirName, "acme--cc--vip.yml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	if err := store.Delete("acme--cc--vip"); err != nil {
		t.Fatal(err)
	}

	// .archive/ 中的文件应该保持 .yml 后缀
	archiveDir := filepath.Join(configDir, MonitorsDirName, ".archive")
	entries, _ := os.ReadDir(archiveDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 archived file, got %d", len(entries))
	}
	if !strings.HasSuffix(entries[0].Name(), ".yml") {
		t.Errorf("expected .yml extension in archive, got: %s", entries[0].Name())
	}
}

func TestDelete_NotFound(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	err := store.Delete("nonexistent--cc--vip")
	if err == nil {
		t.Fatal("expected not found error")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("expected '不存在' in error, got: %v", err)
	}
}

func TestDelete_PathTraversal(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	err := store.Delete("../evil--cc--vip")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

// --- MonitorStore.List ---

func TestList_Empty(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestList_MultipleFiles(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "alpha--cc--vip", validMonitorYAML("alpha", "cc", "vip", 1))
	writeTestMonitorFile(t, configDir, "beta--cx--free", validMonitorYAML("beta", "cx", "free", 2))

	summaries, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 2 {
		t.Errorf("expected 2 summaries, got %d", len(summaries))
	}
}

// --- 完整 CRUD 流程 ---

func TestCRUD_FullLifecycle(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	// Create
	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "lifecycle", Service: "cc", Channel: "test", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Get
	got, err := store.Get("lifecycle--cc--test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil after Create")
	}
	if got.Metadata.Revision != 1 {
		t.Errorf("expected revision 1 after create, got %d", got.Metadata.Revision)
	}

	// Update
	got.Monitors[0].Template = "cc-opus-tiny"
	if err := store.Update("lifecycle--cc--test", got, 1); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got2, _ := store.Get("lifecycle--cc--test")
	if got2.Metadata.Revision != 2 {
		t.Errorf("expected revision 2 after update, got %d", got2.Metadata.Revision)
	}
	if got2.Monitors[0].Template != "cc-opus-tiny" {
		t.Errorf("expected template cc-opus-tiny, got %s", got2.Monitors[0].Template)
	}

	// List
	summaries, _ := store.List()
	if len(summaries) != 1 {
		t.Errorf("expected 1 summary, got %d", len(summaries))
	}

	// Delete
	if err := store.Delete("lifecycle--cc--test"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got3, _ := store.Get("lifecycle--cc--test")
	if got3 != nil {
		t.Error("expected nil after Delete")
	}
}

// --- 稳定 id：生成 / 暴露 / 不可变 ---

// TestCreateGeneratesIDs 确认 Create 给缺失的 channel_id/model_id 生成合法值并落盘。
func TestCreateGeneratesIDs(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	if !IsValidChannelID(file.Metadata.ChannelID) {
		t.Errorf("channel_id not generated: %q", file.Metadata.ChannelID)
	}
	if !IsValidModelID(file.Monitors[0].ModelID) {
		t.Errorf("model_id not generated: %q", file.Monitors[0].ModelID)
	}
	// 落盘后重新读应保留同一 id
	got, err := store.Get(file.Key)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Metadata.ChannelID != file.Metadata.ChannelID {
		t.Errorf("channel_id not persisted: got %q want %q", got.Metadata.ChannelID, file.Metadata.ChannelID)
	}
	if got.Monitors[0].ModelID != file.Monitors[0].ModelID {
		t.Errorf("model_id not persisted: got %q want %q", got.Monitors[0].ModelID, file.Monitors[0].ModelID)
	}
}

// TestListExposesChannelID 确认 List 摘要带出 channel_id（供 rpdiag sampler 发现）。
func TestListExposesChannelID(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	list, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ChannelID != file.Metadata.ChannelID {
		t.Errorf("List did not expose channel_id: %+v", list)
	}
}

// TestUpdateTreatsIDsAsImmutable 确认 Update 视 channel_id/model_id 为不可变：
// 客户端 PUT 篡改的 id 一律从磁盘 existing 还原。
func TestUpdateTreatsIDsAsImmutable(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	file := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com"},
		},
	}
	if err := store.Create(file); err != nil {
		t.Fatal(err)
	}
	origChannelID := file.Metadata.ChannelID
	origModelID := file.Monitors[0].ModelID

	updated := &MonitorFile{
		Metadata: MonitorFileMetadata{Source: "admin", ChannelID: "ch_tampered"},
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip", Model: "Opus", Template: "cc-haiku-tiny", BaseURL: "https://x.com", ModelID: "md_tampered"},
		},
	}
	if err := store.Update(file.Key, updated, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Get(file.Key)
	if err != nil || got == nil {
		t.Fatalf("get: %v", err)
	}
	if got.Metadata.ChannelID != origChannelID {
		t.Errorf("channel_id must be immutable: got %q want %q", got.Metadata.ChannelID, origChannelID)
	}
	if got.Monitors[0].ModelID != origModelID {
		t.Errorf("model_id must be immutable: got %q want %q", got.Monitors[0].ModelID, origModelID)
	}
}

// --- Update: 子行一对一认领（2026-08-06 生产事故回归） ---

// modelIDsOf 收集监测行的 model_id，供唯一性断言使用。
func modelIDsOf(monitors []ServiceConfig) []string {
	ids := make([]string, 0, len(monitors))
	for _, m := range monitors {
		ids = append(ids, m.ModelID)
	}
	return ids
}

// assertModelIDsUniqueAndNonEmpty 断言所有 model_id 非空且互不相同——
// 这正是 loader 的 fail-closed 闸所要求的不变量，破坏它即导致整份配置热更新被拒。
func assertModelIDsUniqueAndNonEmpty(t *testing.T, monitors []ServiceConfig) {
	t.Helper()
	seen := make(map[string]int, len(monitors))
	for i, m := range monitors {
		if m.ModelID == "" {
			t.Fatalf("monitors[%d] 的 model_id 为空：%v", i, modelIDsOf(monitors))
		}
		if prev, dup := seen[m.ModelID]; dup {
			t.Fatalf("model_id 重复：monitors[%d] 与 monitors[%d] 同为 %q；全量=%v",
				prev, i, m.ModelID, modelIDsOf(monitors))
		}
		seen[m.ModelID] = i
	}
}

// TestUpdate_AddingTemplateChildrenToChannelWithExistingChild 复刻 2026-08-06 生产事故：
// 给一个**已有子行**的通道追加模板驱动子行（展示名来自模板、行里不写 model，故磁盘上
// model 为空串）时，旧的多对一兜底匹配会让每个新行都命中同一个既有子行、复制走它的
// model_id，写出一份 model_id 重复的坏文件——admin 返回 200，热更新却 fail-closed 跳过，
// 且容器重启即加载失败。修复后新行必须各自铸新 id。
func TestUpdate_AddingTemplateChildrenToChannelWithExistingChild(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	const existingChildID = "md_11111111-1111-4111-8111-111111111111"
	writeTestMonitorFile(t, configDir, "saiai--cx--o-web", strings.Join([]string{
		"metadata:",
		"  source: admin",
		"  revision: 1",
		"  channel_id: ch_22222222-2222-4222-8222-222222222222",
		"  created_at: \"2026-08-01T00:00:00Z\"",
		"  updated_at: \"2026-08-01T00:00:00Z\"",
		"monitors:",
		"  - provider: saiai",
		"    service: cx",
		"    channel: o-web",
		"    model_id: md_33333333-3333-4333-8333-333333333333",
		"    base_url: https://api.example.com",
		"  - parent: saiai/cx/o-web",
		"    template: cx-gpt-arith",
		"    model_id: " + existingChildID,
		"    env_var_name: SAIAI_CX_KEY",
	}, "\n"))

	// admin round-trip：既有行带回自己的 model_id，两条新子行只有 template、无 model/无 id。
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "saiai", Service: "cx", Channel: "o-web", BaseURL: "https://api.example.com"},
			{Parent: "saiai/cx/o-web", Template: "cx-gpt-arith", ModelID: existingChildID},
			{Parent: "saiai/cx/o-web", Template: "cx-gpt56terra-arith"},
			{Parent: "saiai/cx/o-web", Template: "cx-gpt56luna-arith"},
		},
	}

	if err := store.Update("saiai--cx--o-web", updated, 1); err != nil {
		t.Fatal(err)
	}

	got, err := store.Get("saiai--cx--o-web")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Monitors) != 4 {
		t.Fatalf("len(Monitors) = %d, want 4", len(got.Monitors))
	}
	assertModelIDsUniqueAndNonEmpty(t, got.Monitors)

	// 既有子行的身份与隐藏字段必须原样保住（一对一认领不能退化成"全都不匹配"）。
	if got.Monitors[1].ModelID != existingChildID {
		t.Errorf("既有子行 model_id = %q, want %q（id 不可变）", got.Monitors[1].ModelID, existingChildID)
	}
	if got.Monitors[1].EnvVarName != "SAIAI_CX_KEY" {
		t.Errorf("既有子行 EnvVarName = %q, want 保留", got.Monitors[1].EnvVarName)
	}
	// 新增子行不继承既有子行的隐藏字段。
	for _, i := range []int{2, 3} {
		if got.Monitors[i].EnvVarName != "" {
			t.Errorf("新增子行 monitors[%d].EnvVarName = %q, want 空", i, got.Monitors[i].EnvVarName)
		}
	}
}

// TestPreserveHiddenFields_ExistingChildClaimedOnlyOnce 是上面事故的函数级最小复现：
// 多个展示名同为空的 updated 子行竞争同一个既有子行时，只有第一个能认领到它。
func TestPreserveHiddenFields_ExistingChildClaimedOnlyOnce(t *testing.T) {
	existing := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cx", Channel: "c"},
		{Parent: "P/cx/c", ModelID: "md_a", EnvVarName: "FIRST"},
	}}
	updated := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cx", Channel: "c"},
		{Parent: "P/cx/c"},
		{Parent: "P/cx/c"},
	}}

	preserveAdminHiddenFields(updated, existing)

	if updated.Monitors[1].ModelID != "md_a" || updated.Monitors[1].EnvVarName != "FIRST" {
		t.Errorf("第一个子行应认领既有行，got id=%q env=%q",
			updated.Monitors[1].ModelID, updated.Monitors[1].EnvVarName)
	}
	if updated.Monitors[2].ModelID != "" || updated.Monitors[2].EnvVarName != "" {
		t.Errorf("第二个子行不应复制既有行，got id=%q env=%q",
			updated.Monitors[2].ModelID, updated.Monitors[2].EnvVarName)
	}
}

// TestPreserveHiddenFields_IDMatchWinsOverEarlierNameMatch 固定「全局 model_id 匹配
// 整体优先于展示名兜底」这一顺序语义：无 id 的行即使排在前面、展示名恰好相同，
// 也不能抢走另一条带 model_id 的行所对应的既有子行。
func TestPreserveHiddenFields_IDMatchWinsOverEarlierNameMatch(t *testing.T) {
	existing := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "B", ModelID: "md_y", EnvVarName: "HB"},
	}}
	updated := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cc", Channel: "c"},
		{Parent: "P/cc/c", Model: "B"},                  // 无 id，展示名与既有行相同
		{Parent: "P/cc/c", Model: "A", ModelID: "md_y"}, // 带 id：既有行其实是被改名到 A
	}}

	preserveAdminHiddenFields(updated, existing)

	if updated.Monitors[2].EnvVarName != "HB" {
		t.Errorf("带 model_id 的行应认领既有行，got %q", updated.Monitors[2].EnvVarName)
	}
	if updated.Monitors[1].EnvVarName != "" || updated.Monitors[1].ModelID != "" {
		t.Errorf("同名无 id 行不应再匹配到已被认领的既有行，got env=%q id=%q",
			updated.Monitors[1].EnvVarName, updated.Monitors[1].ModelID)
	}
}

// TestPreserveHiddenFields_DuplicateExistingChildrenMatchedInFileOrder 覆盖历史坏文件：
// 既有文件里本就有多个同键（同 parent、同空展示名）子行时，按文件顺序一对一分配，
// 多出来的 updated 行不再复制任何既有身份。
func TestPreserveHiddenFields_DuplicateExistingChildrenMatchedInFileOrder(t *testing.T) {
	existing := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cx", Channel: "c"},
		{Parent: "P/cx/c", ModelID: "md_1", EnvVarName: "E1"},
		{Parent: "P/cx/c", ModelID: "md_2", EnvVarName: "E2"},
	}}
	updated := &MonitorFile{Monitors: []ServiceConfig{
		{Provider: "P", Service: "cx", Channel: "c"},
		{Parent: "P/cx/c"},
		{Parent: "P/cx/c"},
		{Parent: "P/cx/c"},
	}}

	preserveAdminHiddenFields(updated, existing)

	if got := updated.Monitors[1].EnvVarName; got != "E1" {
		t.Errorf("monitors[1].EnvVarName = %q, want E1", got)
	}
	if got := updated.Monitors[2].EnvVarName; got != "E2" {
		t.Errorf("monitors[2].EnvVarName = %q, want E2", got)
	}
	if got := updated.Monitors[3].EnvVarName; got != "" {
		t.Errorf("monitors[3].EnvVarName = %q, want 空（无未认领候选）", got)
	}
}

// --- 写盘前 model_id 唯一性 fail-loud ---

// TestUpdate_RejectsDuplicateModelIDInPayload 覆盖一对一认领也堵不住的另一条路径：
// 客户端 payload 自带两条相同的非空 model_id（BackfillFileIDs 只补空值、不会覆盖它们）。
// 这类请求必须在写盘前被拒，磁盘文件与 revision 都不得改动。
func TestUpdate_RejectsDuplicateModelIDInPayload(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	writeTestMonitorFile(t, configDir, "acme--cc--vip", validMonitorYAML("acme", "cc", "vip", 1))

	const dupID = "md_44444444-4444-4444-8444-444444444444"
	updated := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "vip"},
			{Parent: "acme/cc/vip", Model: "m1", ModelID: dupID},
			{Parent: "acme/cc/vip", Model: "m2", ModelID: dupID},
		},
	}

	err := store.Update("acme--cc--vip", updated, 1)
	if err == nil {
		t.Fatal("Update 应因 model_id 重复而失败")
	}
	if !strings.Contains(err.Error(), dupID) {
		t.Errorf("错误信息应含重复的 model_id，got %q", err.Error())
	}
	var dup *DuplicateModelIDError
	if !errors.As(err, &dup) {
		t.Errorf("错误应可被 errors.As 识别为 *DuplicateModelIDError，got %T", err)
	}

	got, getErr := store.Get("acme--cc--vip")
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Metadata.Revision != 1 {
		t.Errorf("revision = %d, want 1（拒绝的写入不得改动磁盘）", got.Metadata.Revision)
	}
	if len(got.Monitors) != 1 {
		t.Errorf("len(Monitors) = %d, want 1（拒绝的写入不得改动磁盘）", len(got.Monitors))
	}
}

// TestCreate_RejectsDuplicateModelIDInPayload 与 Update 对称：创建路径同样 fail-loud。
func TestCreate_RejectsDuplicateModelIDInPayload(t *testing.T) {
	configDir, _ := setupTestMonitorsDir(t)
	store := NewMonitorStore(filepath.Join(configDir, MonitorsDirName))

	const dupID = "md_55555555-5555-4555-8555-555555555555"
	file := &MonitorFile{
		Monitors: []ServiceConfig{
			{Provider: "acme", Service: "cc", Channel: "new", ModelID: dupID},
			{Parent: "acme/cc/new", Model: "m1", ModelID: dupID},
		},
	}

	err := store.Create(file)
	if err == nil {
		t.Fatal("Create 应因 model_id 重复而失败")
	}
	var dup *DuplicateModelIDError
	if !errors.As(err, &dup) {
		t.Errorf("错误应可被 errors.As 识别为 *DuplicateModelIDError，got %T", err)
	}
	if _, statErr := os.Stat(filepath.Join(configDir, MonitorsDirName, "acme--cc--new.yaml")); !os.IsNotExist(statErr) {
		t.Errorf("被拒绝的 Create 不得落盘，stat err = %v", statErr)
	}
}
