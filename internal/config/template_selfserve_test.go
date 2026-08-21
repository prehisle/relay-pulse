package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTemplateFile 写一份最小可加载模板（method 必填，其余按需拼接）。
func writeTemplateFile(t *testing.T, extraFields string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cc-x-arith.json")
	body := "{\n\t\"method\": \"POST\"" + extraFields + "\n}"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写模板失败: %v", err)
	}
	return path
}

// TestLoadProbeTemplate_SelfServeVisibleDefaultsToTrue 钉死「没写即可见」。
//
// 反过来（默认隐藏）会让自建部署自带的模板在自助表单里集体消失——他们的模板一律不会写这个
// 新字段。这条语义是本设计成立的前提，故用测试固定而非只靠注释。
func TestLoadProbeTemplate_SelfServeVisibleDefaultsToTrue(t *testing.T) {
	tmpl, err := LoadProbeTemplate(writeTemplateFile(t, ""))
	if err != nil {
		t.Fatalf("加载模板失败: %v", err)
	}
	if !tmpl.SelfServeVisible {
		t.Error("未声明 self_serve_visible 的模板应视为自助可见")
	}
	if tmpl.SelfServeLabel != "" {
		t.Errorf("未声明 self_serve_label 时应为空，实际 %q", tmpl.SelfServeLabel)
	}
}

// TestLoadProbeTemplate_SelfServeVisibleExplicit 覆盖显式 true/false 两态，
// 确认 *bool 解析确实区分「没写」与「写了 false」。
func TestLoadProbeTemplate_SelfServeVisibleExplicit(t *testing.T) {
	cases := []struct {
		name  string
		field string
		want  bool
	}{
		{"显式 false 即隐藏", ",\n\t\"self_serve_visible\": false", false},
		{"显式 true 即可见", ",\n\t\"self_serve_visible\": true", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmpl, err := LoadProbeTemplate(writeTemplateFile(t, tc.field))
			if err != nil {
				t.Fatalf("加载模板失败: %v", err)
			}
			if tmpl.SelfServeVisible != tc.want {
				t.Errorf("self_serve_visible = %v，期望 %v", tmpl.SelfServeVisible, tc.want)
			}
		})
	}
}

// TestLoadProbeTemplate_SelfServeLabelTrimmed 与其余字符串字段同口径剥首尾空白。
func TestLoadProbeTemplate_SelfServeLabelTrimmed(t *testing.T) {
	tmpl, err := LoadProbeTemplate(writeTemplateFile(t, ",\n\t\"self_serve_label\": \"  Claude Haiku 4.5  \""))
	if err != nil {
		t.Fatalf("加载模板失败: %v", err)
	}
	if tmpl.SelfServeLabel != "Claude Haiku 4.5" {
		t.Errorf("self_serve_label = %q，期望剥掉首尾空白", tmpl.SelfServeLabel)
	}
}

// TestBundledTemplates_SelfServeVisibleOnesAreLabelled 常驻守卫：内置模板里凡是自助可见的，
// 都必须有一句人话标签。
//
// 自助表单已经不再出现「模板」二字，用户看到的就是这个标签；缺了它，新加的模板会以
// `cc-opus-ping-20260806` 这种文件名的形态出现在公开表单里，或者退化成与别的模板一模一样的
// 「Opus」，用户根本分不清该选哪个。隐藏模板不作要求（它们不进公开表单）。
func TestBundledTemplates_SelfServeVisibleOnesAreLabelled(t *testing.T) {
	const templatesDir = "../../templates"
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("读取内置模板目录失败: %v", err)
	}

	visible, hidden := 0, 0
	seenLabel := make(map[string]string) // service -> label 去重，避免两个模板同名让用户二选一
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		tmpl, err := LoadProbeTemplate(filepath.Join(templatesDir, entry.Name()))
		if err != nil {
			t.Errorf("模板 %s 加载失败: %v", name, err)
			continue
		}
		if !tmpl.SelfServeVisible {
			hidden++
			continue
		}
		visible++
		if tmpl.SelfServeLabel == "" {
			t.Errorf("自助可见的模板 %s 未声明 self_serve_label——它会以文件名形态出现在公开表单里", name)
			continue
		}
		service, _, _ := strings.Cut(name, "-")
		key := service + "|" + tmpl.SelfServeLabel
		if prev, dup := seenLabel[key]; dup {
			t.Errorf("模板 %s 与 %s 在 service=%s 下自助标签重名（%q），用户无法区分",
				name, prev, service, tmpl.SelfServeLabel)
			continue
		}
		seenLabel[key] = name
	}

	// 双向防真空：目录读空 / 全部被标隐藏，都会让上面的断言一条都不执行。
	if visible < bundledTemplateCount {
		t.Fatalf("自助可见模板仅 %d 个，低于下界 %d——要么目录没读到，要么隐藏标记误伤了一批", visible, bundledTemplateCount)
	}
	if hidden == 0 {
		t.Fatal("没有任何模板被标为 self_serve_visible=false——内部/特化模板（历史冻结版本、逆向线路专用、单通道定制）本应被挡在公开表单外")
	}
}
