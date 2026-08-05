package config

import (
	"os"
	"path/filepath"
	"strings"
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

// TestInheritCoreBehavior_BlankChildVendorInherits 子行只写空白也应视为「未声明」而继承父值。
// 与 lifecycle.go 的模板注入判空口径对齐——两处若一严一松，空白值会卡在中间既不被模板填、
// 也不被父行填，最终留下一个既非空又非合法 code 的幽灵值。
func TestInheritCoreBehavior_BlankChildVendorInherits(t *testing.T) {
	parent := &ServiceConfig{ModelVendor: "zhipu"}
	child := &ServiceConfig{ModelVendor: "   "}
	inheritCoreBehavior(child, parent)
	if child.ModelVendor != "zhipu" {
		t.Fatalf("空白 vendor 的子行未继承父值，got %q want %q", child.ModelVendor, "zhipu")
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

// ---------------------------------------------------------------------------
// validateModelVendors（宽松校验，随 validateResolvedModelConstraints 一直生效）
// ---------------------------------------------------------------------------

// TestValidateModelVendors_AllEmptyIsNoError 全空恒过——这是对生产 provable no-op 的核心断言。
// 当前 230 行监测行与 20 个模板一个都没有 vendor，这条一旦变红即说明本轮不是 no-op。
func TestValidateModelVendors_AllEmptyIsNoError(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1"},
		{Provider: "a", Service: "cc", Channel: "x", Model: "M2"},
		{Provider: "b", Service: "cx", Channel: "y", Model: "M3"},
	}}
	if err := cfg.validateModelVendors(); err != nil {
		t.Fatalf("全空 vendor 必须无错，got %v", err)
	}
}

func TestValidateModelVendors_UnknownCodeRejected(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "cohere"},
	}}
	err := cfg.validateModelVendors()
	if err == nil {
		t.Fatal("词表外的 vendor 必须被拒")
	}
	if !strings.Contains(err.Error(), "a/cc/x") {
		t.Fatalf("错误信息应含 PSC 定位，实际 %q", err.Error())
	}
}

// TestValidateModelVendors_SameChannelConflict 同一 PSC 下两行都非空且不同 → 拒。
// 这是「一个通道一个厂商」不变量：聚合平台按厂商拆通道，混在一条里会把稳定性平均掉。
func TestValidateModelVendors_SameChannelConflict(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "zhipu"},
		{Provider: "a", Service: "cc", Channel: "x", Model: "M2", ModelVendor: "moonshot"},
	}}
	err := cfg.validateModelVendors()
	if err == nil {
		t.Fatal("同一通道混填两个厂商必须被拒")
	}
	if !strings.Contains(err.Error(), "zhipu") || !strings.Contains(err.Error(), "moonshot") {
		t.Fatalf("错误信息应同时含两个冲突值，实际 %q", err.Error())
	}
}

// TestValidateModelVendors_PartialFillIsAllowed 同 PSC 一行有值一行空 → 放行。
// Phase 3 要逐个模板/逐行回填 vendor，回填期必然出现半填状态，此时不能拒绝加载。
func TestValidateModelVendors_PartialFillIsAllowed(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "zhipu"},
		{Provider: "a", Service: "cc", Channel: "x", Model: "M2"},
		{Provider: "a", Service: "cc", Channel: "x", Model: "M3", ModelVendor: "zhipu"},
	}}
	if err := cfg.validateModelVendors(); err != nil {
		t.Fatalf("同通道部分填充必须放行，got %v", err)
	}
}

// TestValidateModelVendors_DifferentChannelsIndependent 不同 PSC 各用各的厂商，互不干扰。
func TestValidateModelVendors_DifferentChannelsIndependent(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "zhipu"},
		{Provider: "a", Service: "cc", Channel: "y", Model: "M1", ModelVendor: "moonshot"},
		{Provider: "b", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "deepseek"},
	}}
	if err := cfg.validateModelVendors(); err != nil {
		t.Fatalf("不同通道用不同厂商是合法的，got %v", err)
	}
}

// TestValidateModelVendors_WritesBackCanonicalForm 校验通过后写回规范形式，
// 使下游（wire、跨产品 join）只需处理小写规范值。
func TestValidateModelVendors_WritesBackCanonicalForm(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "  ZHIPU  "},
		{Provider: "a", Service: "cc", Channel: "y", Model: "M1", ModelVendor: "   "},
	}}
	if err := cfg.validateModelVendors(); err != nil {
		t.Fatalf("可规范化的值必须放行，got %v", err)
	}
	if got := cfg.Monitors[0].ModelVendor; got != "zhipu" {
		t.Fatalf("未写回规范形式，got %q want %q", got, "zhipu")
	}
	if got := cfg.Monitors[1].ModelVendor; got != "" {
		t.Fatalf("纯空白应规范为空串，got %q", got)
	}
}

// TestValidateModelVendors_ConflictSurvivesCaseDifference 大小写不同的同一厂商不算冲突。
func TestValidateModelVendors_ConflictSurvivesCaseDifference(t *testing.T) {
	cfg := &AppConfig{Monitors: []ServiceConfig{
		{Provider: "a", Service: "cc", Channel: "x", Model: "M1", ModelVendor: "ZHIPU"},
		{Provider: "a", Service: "cc", Channel: "x", Model: "M2", ModelVendor: "zhipu"},
	}}
	if err := cfg.validateModelVendors(); err != nil {
		t.Fatalf("同一厂商仅大小写不同不应判冲突，got %v", err)
	}
}

// TestLoad_TemplateVendorConflictWithinChannel 端到端：同一通道的两行引用了 vendor 不同的
// 两个模板 → 加载失败。这一条正是把校验挂在模板解析之后（而非 validate() 里）才拦得住的情形。
func TestLoad_TemplateVendorConflictWithinChannel(t *testing.T) {
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
    model: "Kimi"
    template: "cc-vendor-b"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-vendor-a", "GLM", "zhipu")
		writeVendorTemplate(t, tmplDir, "cc-vendor-b", "Kimi", "moonshot")
	})

	_, err := NewLoader().Load(cfgPath)
	if err == nil {
		t.Fatal("同一通道两行经模板拿到不同 vendor，必须加载失败")
	}
	// 只断言「加载失败」不够——四元组重复、父图校验等都会让 Load 失败，
	// 那样这条测试会在 vendor 校验被摘掉后依然假绿。断言到 vendor 专属错误文案。
	if !strings.Contains(err.Error(), "model_vendor 不一致") ||
		!strings.Contains(err.Error(), "zhipu") || !strings.Contains(err.Error(), "moonshot") {
		t.Fatalf("失败原因必须是 vendor 冲突，实际 %v", err)
	}
}

// TestLoad_UnknownTemplateVendorRejected 端到端：模板里写了词表外的 vendor → 加载失败。
// 挂在 validate()（模板解析之前）时这条拦不住，未知 code 会静默流到 wire 与跨产品契约上。
func TestLoad_UnknownTemplateVendorRejected(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-bogus"
    api_key: "sk-xxxxxxxx"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-bogus", "GLM", "cohere")
	})

	_, err := NewLoader().Load(cfgPath)
	if err == nil {
		t.Fatal("模板声明词表外 vendor 必须加载失败")
	}
	if !strings.Contains(err.Error(), "model_vendor 未知") || !strings.Contains(err.Error(), "cohere") {
		t.Fatalf("失败原因必须是 vendor 不在词表内，实际 %v", err)
	}
}

// TestLoad_WritesBackCanonicalVendorEndToEnd 端到端确认规范化写回落到最终运行配置上。
func TestLoad_WritesBackCanonicalVendorEndToEnd(t *testing.T) {
	cfgPath := newVendorFixture(t, `monitors:
  - provider: "acme"
    service: "cc"
    channel: "o-max"
    base_url: "https://api.acme.com"
    template: "cc-vendor-a"
    model_vendor: "  ZhiPu "
    api_key: "sk-xxxxxxxx"
`, func(tmplDir string) {
		writeVendorTemplate(t, tmplDir, "cc-vendor-a", "GLM", "zhipu")
	})

	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.Monitors[0].ModelVendor; got != "zhipu" {
		t.Fatalf("运行配置里的 vendor 未规范化，got %q", got)
	}
}
