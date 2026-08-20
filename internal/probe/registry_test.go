package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundledSelfServeDefaults 锁死内置模板里「自助流程默认模板」的归属。
//
// 这三个是各 service 最便宜、上游覆盖面最广的算术题模板，被自助收录第二步与变更流程
// 当作首次探测的目标。它们**不能**随 templates/ 目录里新增文件而漂移——2026-08-06 新增
// cc-fable-ping-20260806 后，字典序默认值从 cc-haiku-arith 变成了 claude-fable-5，
// 新申请人一进第二步就在探一个几乎没有中转商提供的模型、必然卡在测试步无法提交。
var bundledSelfServeDefaults = map[string]string{
	"cc": "cc-haiku-arith",
	"cx": "cx-gpt-arith",
	"gm": "gm-flash-arith",
}

// bundledVariantCountFloor 是内置模板数量的下界，防「目录读空 → 断言真空通过」。
// 新增模板无需同步（下界不是等值）。
const bundledVariantCountFloor = 20

// snapshotRegistry 备份并在测试结束后还原全局注册表。
// InitTemplates 整体替换 testTypeRegistry，不还原会污染同包内后续测试。
func snapshotRegistry(t *testing.T) {
	t.Helper()

	registryMu.RLock()
	saved := make(map[string]*TestType, len(testTypeRegistry))
	for id, tt := range testTypeRegistry {
		clone := *tt
		saved[id] = &clone
	}
	registryMu.RUnlock()

	t.Cleanup(func() {
		registryMu.Lock()
		testTypeRegistry = saved
		registryMu.Unlock()
	})
}

// writeTemplates 在临时目录里铺一组模板文件，返回目录路径。
// body 为 JSON 片段（不含首尾花括号），会补上 method 这个必填字段。
func writeTemplates(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, extra := range files {
		content := "{\n\t\"method\": \"POST\"" + extra + "\n}\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("写入模板 %s 失败: %v", name, err)
		}
	}
	return dir
}

func variantIDs(tt *TestType) []string {
	ids := make([]string, 0, len(tt.Variants))
	for _, v := range tt.Variants {
		if v != nil {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

// TestInitTemplates_DefaultVariantSelection 覆盖默认模板的三种来源：显式声明、多份声明、无声明回退。
func TestInitTemplates_DefaultVariantSelection(t *testing.T) {
	const flag = ",\n\t\"self_serve_default\": true"

	tests := []struct {
		name        string
		files       map[string]string
		wantDefault string
		wantOrder   []string
	}{
		{
			name: "显式声明的模板胜过字典序首项",
			files: map[string]string{
				"cc-aaa.json": "",
				"cc-zzz.json": flag,
			},
			wantDefault: "cc-zzz",
			wantOrder:   []string{"cc-aaa", "cc-zzz"},
		},
		{
			name: "多份声明取字典序第一个（确定性）",
			files: map[string]string{
				"cc-aaa.json": "",
				"cc-mmm.json": flag,
				"cc-zzz.json": flag,
			},
			wantDefault: "cc-mmm",
			wantOrder:   []string{"cc-aaa", "cc-mmm", "cc-zzz"},
		},
		{
			name: "无人声明时回退字典序首项（自建部署零影响）",
			files: map[string]string{
				"cc-aaa.json": "",
				"cc-zzz.json": "",
			},
			wantDefault: "cc-aaa",
			wantOrder:   []string{"cc-aaa", "cc-zzz"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			snapshotRegistry(t)

			if err := InitTemplates(writeTemplates(t, tc.files)); err != nil {
				t.Fatalf("InitTemplates 失败: %v", err)
			}

			tt, ok := GetTestType("cc")
			if !ok {
				t.Fatal("测试类型 cc 未注册")
			}
			if tt.DefaultVariant != tc.wantDefault {
				t.Errorf("DefaultVariant = %q，期望 %q", tt.DefaultVariant, tc.wantDefault)
			}
			// Order 仍是纯字典序：本轮只改默认值，不动下拉排序。
			if got := variantIDs(tt); strings.Join(got, ",") != strings.Join(tc.wantOrder, ",") {
				t.Errorf("变体顺序 = %v，期望 %v", got, tc.wantOrder)
			}
		})
	}
}

// TestInitTemplates_UnparsableTemplateStaysSelectable 锁死「读不动的模板只是拿不到声明，不会被踢出下拉」。
//
// 把加载失败的模板从列表里摘掉看似更友好，实则会让一次坏部署静默缩短公开可选项、
// 且与今天的行为不一致（今天它照样出现在下拉里，选中后由内联探测返回可读的解析错误）。
func TestInitTemplates_UnparsableTemplateStaysSelectable(t *testing.T) {
	snapshotRegistry(t)

	dir := writeTemplates(t, map[string]string{
		"cc-good.json": ",\n\t\"self_serve_default\": true",
	})
	if err := os.WriteFile(filepath.Join(dir, "cc-broken.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("写入坏模板失败: %v", err)
	}

	if err := InitTemplates(dir); err != nil {
		t.Fatalf("单个模板解析失败不应让 InitTemplates 整体失败: %v", err)
	}

	tt, ok := GetTestType("cc")
	if !ok {
		t.Fatal("测试类型 cc 未注册")
	}
	if got := variantIDs(tt); strings.Join(got, ",") != "cc-broken,cc-good" {
		t.Errorf("变体列表 = %v，期望坏模板仍在列（cc-broken,cc-good）", got)
	}
	if tt.DefaultVariant != "cc-good" {
		t.Errorf("DefaultVariant = %q，期望 cc-good", tt.DefaultVariant)
	}
}

// TestInitTemplates_UnloadableSoleDeclarationDegradesToAlphabetical 钉死最尖锐的退化分支：
// 唯一那份 self_serve_default 声明所在的模板读不动时，声明**拿不到**，默认值静默退回字典序首项。
//
// 这是本设计的已知代价（模板留在列表里、只丢声明），写成测试是为了让它是「已知且有日志留痕」
// 而不是某天现场才发现：判据是 warn「模板加载失败，跳过其默认声明」+ 刷新日志里的 defaults= 一行。
// 内置模板这条线由 TestInitTemplates_BundledDefaultsAreExplicit 兜底（CI 每次跑）。
func TestInitTemplates_UnloadableSoleDeclarationDegradesToAlphabetical(t *testing.T) {
	snapshotRegistry(t)

	dir := t.TempDir()
	// 合法 JSON、带声明，但缺 method → LoadProbeTemplate 报错，声明随之丢失。
	if err := os.WriteFile(filepath.Join(dir, "cc-zzz.json"),
		[]byte("{\n\t\"self_serve_default\": true\n}\n"), 0o644); err != nil {
		t.Fatalf("写入无 method 模板失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cc-aaa.json"),
		[]byte("{\n\t\"method\": \"POST\"\n}\n"), 0o644); err != nil {
		t.Fatalf("写入模板失败: %v", err)
	}

	if err := InitTemplates(dir); err != nil {
		t.Fatalf("InitTemplates 失败: %v", err)
	}

	tt, ok := GetTestType("cc")
	if !ok {
		t.Fatal("测试类型 cc 未注册")
	}
	if tt.DefaultVariant != "cc-aaa" {
		t.Errorf("DefaultVariant = %q，期望退回字典序首项 cc-aaa", tt.DefaultVariant)
	}
	if got := variantIDs(tt); strings.Join(got, ",") != "cc-aaa,cc-zzz" {
		t.Errorf("变体列表 = %v，期望坏模板仍在列（cc-aaa,cc-zzz）", got)
	}
}

// TestInitTemplates_BundledDefaultsAreExplicit 常驻守卫：内置模板的自助默认值必须是显式声明的那三个，
// 且每个 service 恰好一份声明——防再有人新增一个字典序更靠前的模板就悄悄抢走默认探测目标。
func TestInitTemplates_BundledDefaultsAreExplicit(t *testing.T) {
	snapshotRegistry(t)

	const templatesDir = "../../templates"
	if err := InitTemplates(templatesDir); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	total := 0
	for service, wantDefault := range bundledSelfServeDefaults {
		tt, ok := GetTestType(service)
		if !ok {
			t.Fatalf("测试类型 %s 未注册", service)
		}
		if len(tt.Variants) == 0 {
			t.Fatalf("service %s 没扫到任何模板", service)
		}
		total += len(tt.Variants)
		if tt.DefaultVariant != wantDefault {
			t.Errorf("service %s 的默认模板 = %q，期望 %q", service, tt.DefaultVariant, wantDefault)
		}
	}
	if total < bundledVariantCountFloor {
		t.Fatalf("只扫到 %d 个内置模板（下界 %d），断言可能是真空的", total, bundledVariantCountFloor)
	}

	// 每个 service 恰好一份声明：多份虽有确定性兜底（取字典序第一），但那属于配置事故。
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		t.Fatalf("读取内置模板目录失败: %v", err)
	}
	declared := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		service, _, found := strings.Cut(name, "-")
		if !found || service == "" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(templatesDir, entry.Name()))
		if err != nil {
			t.Fatalf("读取模板 %s 失败: %v", name, err)
		}
		if strings.Contains(string(content), "\"self_serve_default\": true") {
			declared[service] = append(declared[service], name)
		}
	}
	for service, names := range declared {
		if len(names) != 1 {
			t.Errorf("service %s 声明了 %d 个自助默认模板 %v，应恰好 1 个", service, len(names), names)
		}
	}
	for service := range bundledSelfServeDefaults {
		if len(declared[service]) == 0 {
			t.Errorf("service %s 没有任何模板声明 self_serve_default", service)
		}
	}
}
