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
//   - native 模板必须**不**声明 vendor（厂商无关，见 isNativeProbeTemplate 注释），
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

		if isNativeProbeTemplate(name) {
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
