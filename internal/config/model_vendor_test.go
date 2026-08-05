package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeVendorTemplate 写一个最小可用模板，声明 model_vendor。
func writeVendorTemplate(t *testing.T, tmplDir, name, model, vendor string) {
	t.Helper()
	body := `{
  "model": "` + model + `",
  "request_model": "` + model + `-latest",
  "model_vendor": "` + vendor + `",
  "url": "{{BASE_URL}}/v1/messages",
  "method": "POST",
  "headers": {"x-api-key": "{{API_KEY}}"},
  "body": {"model": "{{MODEL}}"},
  "response": {"success_contains": "ok"}
}`
	if err := os.WriteFile(filepath.Join(tmplDir, name+".json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

// newVendorFixture 建一个隔离配置目录并返回 config.yaml 路径。
func newVendorFixture(t *testing.T, configYAML string, templates func(tmplDir string)) string {
	t.Helper()
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	templates(tmplDir)
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(configYAML), 0600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// monitorByModel 按 model 取出监测行，找不到即 fatal。
func monitorByModel(t *testing.T, cfg *AppConfig, model string) ServiceConfig {
	t.Helper()
	for _, m := range cfg.Monitors {
		if m.Model == model {
			return m
		}
	}
	t.Fatalf("未找到 model=%q 的监测行", model)
	return ServiceConfig{}
}

// TestLoad_ModelVendorFromTemplate 模板声明 vendor → 未写 vendor 的监测行获得该值。
func TestLoad_ModelVendorFromTemplate(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-vendor-a"
    api_key: "sk-xxxxxxxx"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-vendor-a", "GLM", "zhipu")
	})

	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Monitors[0].ModelVendor; got != "zhipu" {
		t.Fatalf("模板 vendor 未注入监测行，got %q want %q", got, "zhipu")
	}
}

// TestLoad_ModelVendorConfigOverridesTemplate 行级显式 vendor 不被模板覆盖（config > template）。
func TestLoad_ModelVendorConfigOverridesTemplate(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-vendor-a"
    model_vendor: "moonshot"
    api_key: "sk-xxxxxxxx"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-vendor-a", "GLM", "zhipu")
	})

	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Monitors[0].ModelVendor; got != "moonshot" {
		t.Fatalf("行级 vendor 被模板覆盖了，got %q want %q", got, "moonshot")
	}
}

// TestLoad_ModelVendorInheritedByChild 子通道继承父通道 vendor。
//
// 这条同时锁死真实流水线顺序：模板注入（resolveTemplates）早于父子继承
// （normalize → applyParentInheritance），子行自身没有 template 引用，
// 故其 vendor 只可能来自「父行经模板拿到值后再被继承」这条链。
func TestLoad_ModelVendorInheritedByChild(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-vendor-a"
    api_key: "sk-xxxxxxxx"
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    parent: "acme/cc/o-max"
    model: "GLM-Air"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-vendor-a", "GLM", "zhipu")
	})

	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	parent := monitorByModel(t, cfg, "GLM")
	child := monitorByModel(t, cfg, "GLM-Air")
	if parent.ModelVendor != "zhipu" {
		t.Fatalf("父行 vendor = %q，want %q", parent.ModelVendor, "zhipu")
	}
	if child.ModelVendor != "zhipu" {
		t.Fatalf("子行未继承父行 vendor，got %q want %q", child.ModelVendor, "zhipu")
	}
}

// TestInheritCoreBehavior_ChildVendorWins 子行已有 vendor 时继承不得覆盖。
// 直接测继承函数：这一步早于任何一致性校验，两值故意不同才能证明"不覆盖"非真空。
func TestInheritCoreBehavior_ChildVendorWins(t *testing.T) {
	parent := &ServiceConfig{ModelVendor: "zhipu"}
	child := &ServiceConfig{ModelVendor: "moonshot"}
	inheritCoreBehavior(child, parent)
	if child.ModelVendor != "moonshot" {
		t.Fatalf("子行自身 vendor 被父行覆盖，got %q want %q", child.ModelVendor, "moonshot")
	}
}

// TestLoadProbeTemplate_TrimsModelVendor 模板 vendor 与 model/request_model 同款 trim。
func TestLoadProbeTemplate_TrimsModelVendor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.json")
	if err := os.WriteFile(path, []byte(`{
  "model": "GLM",
  "model_vendor": "  zhipu  ",
  "url": "{{BASE_URL}}/v1/messages",
  "method": "POST"
}`), 0600); err != nil {
		t.Fatal(err)
	}
	tmpl, err := LoadProbeTemplate(path)
	if err != nil {
		t.Fatalf("LoadProbeTemplate: %v", err)
	}
	if tmpl.ModelVendor != "zhipu" {
		t.Fatalf("模板 vendor 未 trim，got %q", tmpl.ModelVendor)
	}
}

// TestLoad_NoVendorAnywhereIsNoOp 全空配置照常加载——这是「对现状 provable no-op」的直接断言。
func TestLoad_NoVendorAnywhereIsNoOp(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-novendor"
    api_key: "sk-xxxxxxxx"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-novendor", "GLM", "")
	})

	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("全空 vendor 的配置必须照常加载，got %v", err)
	}
	if got := cfg.Monitors[0].ModelVendor; got != "" {
		t.Fatalf("凭空冒出 vendor: %q", got)
	}
}
