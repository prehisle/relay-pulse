package api

import (
	"slices"
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
			// native 模板不声明模型、要求监测行填，而公开流程填不了——它一条都不该出现在目录里
			if variant.Native {
				t.Errorf("%s 的条目 %q 指向 native 模板 %q：公开流程没有填模型的入口，"+
					"选中它必然卡在「模板未声明模型」", service, opt.Key, opt.Template)
			}
			// 目录条目必须自己声明得起厂商：厂商标注全靠它，提交方已无从填写
			if opt.Vendor == "" {
				t.Errorf("%s 的条目 %q 未声明 model_vendor——上架后厂商列会空着且无人可补",
					service, opt.Key)
			}
			// 模型由模板唯一决定，故选项键与模板一一对应（前端据此做受控选择）
			if opt.Key != opt.Template {
				t.Errorf("%s 的条目 key=%q 与模板 %q 不一致", service, opt.Key, opt.Template)
			}
		}
	}

	// 隐藏模板不得出现（*-anyrouter 是中转商专用指纹版）
	for _, opt := range catalog["cc"] {
		if strings.HasSuffix(opt.Template, "-anyrouter") {
			t.Errorf("内部模板 %q 不应出现在公开目录里", opt.Template)
		}
	}
}

// TestBuildModelCatalog_FirstPartyVendorsAreSelfServable 常驻守卫：五家第一方厂商的模型
// 必须都能在 cc/cx 的自助表单里选到，且各自走的是**专属**模板。
//
// 2026-08-21 之前它们走的是「native 通用模板 + 提交方自填模型 ID/厂商/请求形态」，因此可以
// 提交出「模型 ID 是豆包、厂商标成智谱」这种自相矛盾的数据，请求形态也能选错（症状是 HTTP 200
// 却恒红 content_mismatch）。改成一模型一模板后，这三项全由我们钉死；本测试锁住「钉死之后
// 这些模型仍然选得到」——漏建一个模板就是悄悄下线一个厂商。
func TestBuildModelCatalog_FirstPartyVendorsAreSelfServable(t *testing.T) {
	snapshotProbeRegistry(t)
	if err := probe.InitTemplates("../../templates"); err != nil {
		t.Fatalf("加载内置模板失败: %v", err)
	}

	// 双向锁死：厂商 → 该厂商在自助表单上必须出现的**规范模型 ID**。
	//
	// 只断言「这家厂商有条目」挡不住误标 vendor（把 kimi 的模板标成 zhipu 照样过）。
	// 模型 ID 同时是 DB 业务键，写错会让上架行的历史与既有 native 通道对不上，故这里锁到串。
	firstParty := map[string]string{
		"zhipu":     "glm-5.2",
		"moonshot":  "kimi-k2.7-code",
		"minimax":   "minimax-m3",
		"deepseek":  "deepseek-v4-pro",
		"bytedance": "doubao-seed-2.1-turbo",
	}
	catalog := buildModelCatalog()

	for _, service := range []string{"cc", "cx"} {
		byVendor := make(map[string][]OnboardingModelOption)
		for _, opt := range catalog[service] {
			byVendor[opt.Vendor] = append(byVendor[opt.Vendor], opt)
		}
		for vendor, wantModel := range firstParty {
			options := byVendor[vendor]
			if len(options) == 0 {
				t.Errorf("%s 里厂商 %q 一个可选模型都没有——该厂商在自助表单上已消失", service, vendor)
				continue
			}
			var models []string
			for _, opt := range options {
				models = append(models, opt.RequestModel)
				// 与提交侧同一条规则：公开流程用空模型过闸，模板必须自己声明得起模型
				if _, _, err := onboarding.ValidateModelSelection(opt.Template, "", ""); err != nil {
					t.Errorf("%s 的 %q 过不了提交侧校验: %v", service, opt.Template, err)
				}
			}
			if !slices.Contains(models, wantModel) {
				t.Errorf("%s 里厂商 %q 的可选模型 = %v，缺少 %q", service, vendor, models, wantModel)
			}
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
