package api

import (
	"strings"
	"testing"

	"monitor/internal/onboarding"
	"monitor/internal/probe"
)

// snapshotProbeRegistry 备份 probe 注册表并在测试结束后还原。
//
// InitTemplates 会整体替换包级注册表，不还原会污染同包内其它测试（合成 service 一旦残留，
// 就会经 /api/onboarding/meta 下发给前端）。ListTestTypes 返回的是替换前那批指针，
// 故它天然就是一份快照。
func snapshotProbeRegistry(t *testing.T) {
	t.Helper()

	saved := probe.ListTestTypes()
	savedIDs := make(map[string]bool, len(saved))
	for _, tt := range saved {
		savedIDs[tt.ID] = true
	}

	t.Cleanup(func() {
		for _, tt := range probe.ListTestTypes() {
			if !savedIDs[tt.ID] {
				probe.UnregisterTestType(tt.ID)
			}
		}
		for _, tt := range saved {
			probe.RegisterTestType(tt)
		}
	})
}

// TestBuildModelCatalog_FromBundledTemplates 拿**真实** templates/ 目录跑一遍目录组装。
//
// 合成模板测不出这轮真正要防的东西：目录条目对用户是「有哪些模型可选」，一旦内置模板改了
// 声明（少了标签、被标隐藏、忘了 model_vendor），公开表单就会跟着劣化。
func TestBuildModelCatalog_FromBundledTemplates(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	catalog := buildModelCatalog()
	for _, service := range []string{"cc", "cx", "gm"} {
		if len(catalog[service]) == 0 {
			t.Fatalf("service %s 的可选模型为空——公开表单会没有任何模型可选", service)
		}
	}

	seen := make(map[string]bool)
	for service, options := range catalog {
		for _, opt := range options {
			if opt.Label == "" || opt.Template == "" || opt.RequestModel == "" {
				t.Errorf("%s 的条目字段不全: %+v", service, opt)
			}
			if strings.Contains(opt.Label, opt.Template) && opt.Template != opt.Label {
				t.Errorf("%s 的条目标签里混进了模板名（%q）——表单里不该出现模板", service, opt.Label)
			}
			key := service + "|" + opt.Key
			if seen[key] {
				t.Errorf("%s 出现重复选项 key %q", service, opt.Key)
			}
			seen[key] = true

			variant, ok := probe.LookupVariant(service, opt.Template)
			if !ok || !variant.SelfServeVisible {
				t.Errorf("%s 的条目指向不可用模板 %q（found=%v visible=%v）",
					service, opt.Template, ok, variant.SelfServeVisible)
			}
			// 行级模型只允许出现在 native 条目上，与提交侧 ValidateModelSelection 同一规则
			if (opt.Model != "") != variant.Native {
				t.Errorf("%s 的条目 %q: model=%q 与模板 native=%v 不自洽",
					service, opt.Key, opt.Model, variant.Native)
			}
			if opt.Editable != variant.Native {
				t.Errorf("%s 的条目 %q: 可编辑标记应与 native 一致", service, opt.Key)
			}
		}
	}

	// 隐藏模板不得出现（cc-haiku-arith-anyrouter 是中转商专用指纹版）
	for _, opt := range catalog["cc"] {
		if strings.HasSuffix(opt.Template, "-anyrouter") || opt.Template == "cc-haiku-pro-2184" {
			t.Errorf("内部模板 %q 不应出现在公开目录里", opt.Template)
		}
	}

	// 第一方厂商种子条目在场，且模型 ID 可改（同一模型在不同平台 ID 未必相同）
	seeds := onboarding.FirstPartyModels("cc")
	if len(seeds) == 0 {
		t.Fatal("cc 的第一方厂商种子目录为空，断言可能是真空的")
	}
	for _, seed := range seeds {
		found := false
		for _, opt := range catalog["cc"] {
			if opt.Model == seed.Model && opt.Vendor == seed.Vendor {
				found = true
				if !opt.Editable {
					t.Errorf("第一方厂商条目 %q 的模型 ID 应可编辑", seed.Label)
				}
			}
		}
		if !found {
			t.Errorf("第一方厂商种子 %q 未出现在目录里", seed.Label)
		}
	}
}

// TestBuildModelCatalog_VendorOrder 厂商顺序跟随受控词表：原厂三家在前，国内厂商在后。
func TestBuildModelCatalog_VendorOrder(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	var vendors []string
	for _, opt := range buildModelCatalog()["cc"] {
		if len(vendors) == 0 || vendors[len(vendors)-1] != opt.Vendor {
			vendors = append(vendors, opt.Vendor)
		}
	}
	if len(vendors) < 2 {
		t.Fatalf("cc 目录只有 %d 个厂商分组，断言可能是真空的", len(vendors))
	}
	if vendors[0] != "anthropic" {
		t.Errorf("cc 目录首个厂商 = %q，期望 anthropic（受控词表首位）", vendors[0])
	}
	// 同一厂商的条目必须连续成组，否则前端按厂商分组会出现同名分组多次
	seenVendor := make(map[string]bool)
	for _, v := range vendors {
		if seenVendor[v] {
			t.Errorf("厂商 %q 的条目在目录里不连续", v)
		}
		seenVendor[v] = true
	}
}

// TestBuildRequestShapes 自填模型 ID 的请求形态：cc 三种、cx 两种，gm 没有 native 模板故缺席。
func TestBuildRequestShapes(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	shapes := buildRequestShapes()
	if len(shapes["cc"]) == 0 || len(shapes["cx"]) == 0 {
		t.Fatalf("cc/cx 应有 native 请求形态可选，实际 %d/%d", len(shapes["cc"]), len(shapes["cx"]))
	}
	if _, ok := shapes["gm"]; ok {
		t.Error("gm 没有 native 模板，不该出现自填入口")
	}
	for service, list := range shapes {
		for _, shape := range list {
			if shape.Label == "" || shape.Label == shape.Template {
				t.Errorf("%s 的请求形态 %q 缺人话标签——用户看不懂模板名", service, shape.Template)
			}
			if v, ok := probe.LookupVariant(service, shape.Template); !ok || !v.Native {
				t.Errorf("%s 的请求形态 %q 不是 native 模板", service, shape.Template)
			}
		}
	}
}

// TestBuildRequestShapes_OrderFollowsDeclaration 请求形态按模板声明的次序排，而不是文件名字典序。
//
// 这一项不只是好看：列表第一项同时是「其他（自填模型 ID）」的初始值。按字典序排会把
// cc 的推荐形态（默认开思考且支持关闭，最快最省）排到最后，于是自填的人默认拿到一个
// 对多数第一方厂商模型都不合适的形态。
func TestBuildRequestShapes_OrderFollowsDeclaration(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	shapes := buildRequestShapes()
	if got := shapes["cc"][0].Template; got != "cc-native-arith-nothink" {
		t.Errorf("cc 的首个请求形态 = %q，期望 cc-native-arith-nothink（推荐先试的那个）", got)
	}
	if got := shapes["cx"][0].Template; got != "cx-native-arith" {
		t.Errorf("cx 的首个请求形态 = %q，期望 cx-native-arith", got)
	}

	// 字典序会把 cc-native-arith 排在最前，故本断言在「忘了排序」时必然变红
	if shapes["cc"][0].Template == "cc-native-arith" {
		t.Error("cc 的请求形态仍是字典序")
	}
}

// TestFirstPartySeedCatalogIsSound 常驻守卫：手写的第一方厂商种子目录必须自洽。
//
// 种子是纯数据、编译器管不着，而它每一条都会直接变成公开表单里的一个选项。三类错法都只会在
// 用户填完点测试时才暴露（甚至更晚）：模板改名/被标隐藏 → 条目静默消失；模板被改成非 native
// → 用户拿到一个「带模型的可编辑条目」，提交时才撞上「非 native 不该填模型」；厂商 code 拼错
// 或模型 ID 含非法字符 → 同样拖到提交才拒。这里一次性在 CI 拦下。
func TestFirstPartySeedCatalogIsSound(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	total := 0
	for _, service := range []string{"cc", "cx", "gm"} {
		seen := make(map[string]string) // template|model -> label，查重
		for _, seed := range onboarding.FirstPartyModels(service) {
			total++

			variant, ok := probe.LookupVariant(service, seed.Template)
			switch {
			case !ok:
				t.Errorf("%s 种子 %q 指向不存在的模板 %q", service, seed.Label, seed.Template)
				continue
			case !variant.SelfServeVisible:
				t.Errorf("%s 种子 %q 的模板 %q 被标为自助不可见——该条目会静默消失", service, seed.Label, seed.Template)
			case !variant.Native:
				t.Errorf("%s 种子 %q 的模板 %q 不是 native 族——带行级模型的条目只能挂 native 模板",
					service, seed.Label, seed.Template)
			}

			// 与提交侧同一条规则：种子给出的组合必须本来就能过校验
			if _, _, err := onboarding.ValidateModelSelection(seed.Template, seed.Model, seed.Vendor); err != nil {
				t.Errorf("%s 种子 %q 过不了提交侧校验: %v", service, seed.Label, err)
			}

			if seed.Label == "" {
				t.Errorf("%s 种子（模型 %q）缺展示名", service, seed.Model)
			}
			if strings.Contains(seed.Label, seed.Vendor) {
				t.Errorf("%s 种子 %q 的标签里重复了厂商——下拉已按厂商分组", service, seed.Label)
			}

			key := seed.Template + "|" + seed.Model
			if prev, dup := seen[key]; dup {
				t.Errorf("%s 种子 %q 与 %q 重复（同模板同模型），前端选项 key 会撞车", service, seed.Label, prev)
			}
			seen[key] = seed.Label
		}
	}

	if total == 0 {
		t.Fatal("第一方厂商种子目录为空，断言是真空的")
	}
}
