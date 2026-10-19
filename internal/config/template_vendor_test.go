package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"monitor/internal/modelvendor"
)

// bundledTemplateCount 是 templates/ 下模板文件数量的下界，用来防「目录读空 → 循环零次 → 守卫真空通过」。
// 新增模板时无需同步这个数字（它是下界不是等值），删模板删到低于它才需要重新评估。
const bundledTemplateCount = 20

// TestBundledTemplatesDeclareModelVendor 常驻守卫：内置探针模板的 model_vendor 覆盖，是我方生产
// 「全站厂商非空」的唯一保障。
//
// 我们**刻意没有**接全局 fail-closed 运行时闸：vendor 无法像 model_id 那样由 loader 自动派生
// （spec 明令禁止从 request_model 前缀反推厂商），接闸会让自己手写内联监测行、不套内置模板的
// 自托管用户升级即 crash-loop——正是 v2.69.2 修过的那类伤害。改由本测试守住：生产 monitors.d/
// 的每一行都引用模板，故「模板全覆盖」等价于「生产全覆盖」，且对自托管用户零影响。
//
// 双向锁死：
//   - 非 native 模板必须声明受控词表内的合法 vendor——漏一个，用它的通道厂商列就空着；
//   - native 模板必须**不**声明 vendor（厂商无关，见 IsNativeProbeTemplate 注释），
//     且必须引用 {{MODEL}}，否则监测行填的 model/request_model 根本不进请求体。
func TestBundledTemplatesDeclareModelVendor(t *testing.T) {
	const templatesDir = "../../templates"
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("读取内置模板目录失败: %v", err)
	}

	scanned := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		scanned++
		name := strings.TrimSuffix(entry.Name(), ".json")

		tmpl, err := LoadProbeTemplate(filepath.Join(templatesDir, entry.Name()))
		if err != nil {
			t.Errorf("模板 %s 加载失败: %v", name, err)
			continue
		}

		if IsNativeProbeTemplate(name) {
			if tmpl.ModelVendor != "" {
				t.Errorf("native 模板 %s 不得声明 model_vendor（当前 %q）——它厂商无关，声明后监测行漏填 vendor 会静默继承成错误厂商",
					name, tmpl.ModelVendor)
			}
			if tmpl.Model != "" || tmpl.RequestModel != "" {
				t.Errorf("native 模板 %s 不得声明 model/request_model（当前 %q/%q）——模型必须由监测行按厂商填写",
					name, tmpl.Model, tmpl.RequestModel)
			}
			if !strings.Contains(string(tmpl.BodyRaw), "{{MODEL}}") {
				t.Errorf("native 模板 %s 的 body 未引用 {{MODEL}}，监测行填的 model/request_model 不会进请求体", name)
			}
			continue
		}

		if tmpl.ModelVendor == "" {
			t.Errorf("模板 %s 缺 model_vendor——用它的监测行厂商列会空着（生产靠模板全覆盖，没有运行时闸兜底）", name)
			continue
		}
		if err := modelvendor.Validate(tmpl.ModelVendor); err != nil {
			t.Errorf("模板 %s 的 model_vendor=%q 不在受控词表内: %v", name, tmpl.ModelVendor, err)
		}
	}

	if scanned < bundledTemplateCount {
		t.Fatalf("只扫到 %d 个内置模板（期望至少 %d）——目录路径或过滤条件有误，本守卫可能真空通过", scanned, bundledTemplateCount)
	}
}

// TestIsNativeProbeTemplate 锁死命名约定：第二段恰为 native 才算第一方厂商通用模板。
func TestIsNativeProbeTemplate(t *testing.T) {
	for _, name := range []string{"cc-native-arith", "cx-native-arith", "gm-native-x", " cc-native-arith "} {
		if !IsNativeProbeTemplate(name) {
			t.Errorf("%q 应判为 native 模板", name)
		}
	}
	for _, name := range []string{
		"", "cc-haiku-arith", "cx-gpt54-arith", "cc-kiro-native-arith", "native-cc-arith", "cc",
		"cc-native",  // 缺后缀：不符合 <service>-native-<suffix> 约定
		"cc-native-", // 后缀为空：同上
		"-native-x",  // 缺 service 段
	} {
		if IsNativeProbeTemplate(name) {
			t.Errorf("%q 不应判为 native 模板", name)
		}
	}
}

// writeNativeTemplateConfig 落一份「真实 native 模板 + 给定 monitors 段」的临时配置，返回 config.yaml 路径。
func writeNativeTemplateConfig(t *testing.T, monitorsYAML string) string {
	t.Helper()
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "templates", "cc-native-arith.json"))
	if err != nil {
		t.Fatalf("读取真实 native 模板失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "cc-native-arith.json"), src, 0600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(monitorsYAML), 0600); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

// nativeVendorWarnings 过滤出 validateFinal 里「native 模板缺 vendor」那一类告警。
func nativeVendorWarnings(warns []error) []error {
	var hits []error
	for _, w := range warns {
		if strings.Contains(w.Error(), "第一方厂商通用模板") {
			hits = append(hits, w)
		}
	}
	return hits
}

// TestValidateFinal_NativeTemplateMissingVendorWarns：用了 native 模板却没填 vendor 时必须有告警。
// native 模板厂商无关、不提供 vendor 兜底，这条告警是漏填的唯一提示。
func TestValidateFinal_NativeTemplateMissingVendorWarns(t *testing.T) {
	cfgPath := writeNativeTemplateConfig(t, `monitors:
  - provider: zhipu
    service: cc
    channel: o-nat-main
    model: GLM-4.7
    request_model: glm-4.7
    base_url: https://open.bigmodel.cn/api/anthropic
    template: cc-native-arith
    api_key: sk-xxxxxxxx
`)
	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("加载失败（本用例只该告警、不该阻断）: %v", err)
	}
	if got := nativeVendorWarnings(cfg.validateFinal()); len(got) != 1 {
		t.Fatalf("期望 1 条 native 缺 vendor 告警，实得 %d 条: %v", len(got), got)
	}
}

// TestValidateFinal_NativeChildMustDeclareOwnVendor native 子行**不**继承父行 vendor，
// 只写在父行时子行保持空、并被告警点名。
//
// 同一通道的各层可属于不同厂商，native 模板又不声明厂商，子行漏填时若继承父值，
// 混厂商通道就会把 GLM 子行显示成父行的厂商——宁可空着告警，也不能给错的。
// 代价是单厂商的 native 多模型通道每个子行都要自己写一遍 vendor。
func TestValidateFinal_NativeChildMustDeclareOwnVendor(t *testing.T) {
	cfgPath := writeNativeTemplateConfig(t, `monitors:
  - provider: zhipu
    service: cc
    channel: o-nat-main
    model: GLM-4.7
    request_model: glm-4.7
    model_vendor: zhipu
    base_url: https://open.bigmodel.cn/api/anthropic
    template: cc-native-arith
    api_key: sk-xxxxxxxx
  - provider: zhipu
    service: cc
    channel: o-nat-main
    model: GLM-4.7-Air
    request_model: glm-4.7-air
    parent: zhipu/cc/o-nat-main
    template: cc-native-arith
`)
	cfg, err := NewLoader().Load(cfgPath)
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if len(cfg.Monitors) != 2 {
		t.Fatalf("期望 2 行监测项，实得 %d", len(cfg.Monitors))
	}
	byModel := map[string]string{}
	for _, m := range cfg.Monitors {
		byModel[m.Model] = m.ModelVendor
	}
	if byModel["GLM-4.7"] != "zhipu" {
		t.Fatalf("父行自己声明的 vendor 应保留，实得 %q", byModel["GLM-4.7"])
	}
	if byModel["GLM-4.7-Air"] != "" {
		t.Fatalf("native 子行不应继承父行 vendor，实得 %q", byModel["GLM-4.7-Air"])
	}
	if got := nativeVendorWarnings(cfg.validateFinal()); len(got) != 1 {
		t.Fatalf("期望恰好 1 条告警（漏填的子行），实得 %d 条: %v", len(got), got)
	}
}
