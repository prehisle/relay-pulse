package api

import (
	"sort"

	"monitor/internal/modelvendor"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
)

// OnboardingModelOption 是自助收录「选模型」下拉里的一个条目。
//
// 表单里已经不出现「模板」二字：用户看到的是厂商与模型名，模板是选中后随行提交的实现细节。
// 一个条目要么来自某个专属模板（Anthropic/OpenAI/Google 的模型，Model 恒空、模型由模板定），
// 要么来自第一方厂商种子目录（native 模板 + 一个可改的模型 ID）。
type OnboardingModelOption struct {
	// Key 选项唯一键，供前端做受控选择（同一模板可因不同种子模型出现多次）。
	Key string `json:"key"`
	// Label 人话模型名。
	Label string `json:"label"`
	// Vendor 模型厂商 code；空表示模板未声明（词表外厂商由用户在表单里另选）。
	Vendor string `json:"vendor"`
	// Template 提交时随行的 template_name。
	Template string `json:"template"`
	// Model 提交时随行的行级模型 ID：仅第一方厂商条目非空，且**前端可改**
	// （同一模型在不同平台的 ID 未必相同，见 onboarding.FirstPartyModel 注释）。
	Model string `json:"model"`
	// RequestModel 该条目实际会请求的模型 ID，仅供展示（模板声明的值；第一方条目与 Model 同源）。
	RequestModel string `json:"request_model"`
	// Editable 该条目的模型 ID 是否可编辑（第一方厂商条目为 true）。
	Editable bool `json:"editable"`
}

// OnboardingRequestShape 是 native 模板对应的一种「请求形态」选项。
//
// 用户看到的是「这个模型默认开不开思考」「接口地址是否已含版本段」这类人话（模板文件里的
// self_serve_label），而不是模板名——形态选错只会探测报错、重选即可，不必让用户理解模板体系。
type OnboardingRequestShape struct {
	Template string `json:"template"`
	Label    string `json:"label"`
}

// buildModelCatalog 组装「service → 可选模型」目录。
//
// 两个来源：
//   - 专属模板派生：注册表里自助可见、非 native 且声明了模型的模板，各出一条；
//   - 第一方厂商种子目录：onboarding.FirstPartyModels，其模板必须仍然存在且可见
//     （模板被删/被标隐藏时该条目自动消失，不留指向空模板的死选项）。
//
// 顺序：先按厂商在受控词表里的位置（Anthropic/OpenAI/Google 在前），同厂商内按标签，
// 使下拉分组稳定、与前端展示厂商的顺序一致。
func buildModelCatalog() map[string][]OnboardingModelOption {
	catalog := make(map[string][]OnboardingModelOption)

	for _, t := range probe.ListTestTypes() {
		var options []OnboardingModelOption

		for _, v := range t.Variants {
			if v == nil || !v.SelfServeVisible || v.Native {
				continue
			}
			// 既没有 request_model 也没有 model 的模板给不出「用户在选什么」，跳过——
			// 它上架后也会被 validateMonitorConfig 的空模型闸拦下。
			requestModel := firstNonEmptyString(v.TemplateRequestModel, v.TemplateModel)
			if requestModel == "" {
				continue
			}
			options = append(options, OnboardingModelOption{
				Key:          v.ID,
				Label:        firstNonEmptyString(v.TemplateLabel, v.TemplateModel, v.ID),
				Vendor:       v.TemplateVendor,
				Template:     v.ID,
				RequestModel: requestModel,
			})
		}

		for _, seed := range onboarding.FirstPartyModels(t.ID) {
			variant, ok := probe.LookupVariant(t.ID, seed.Template)
			if !ok || !variant.SelfServeVisible {
				continue
			}
			options = append(options, OnboardingModelOption{
				Key:          seed.Template + "|" + seed.Model,
				Label:        seed.Label,
				Vendor:       seed.Vendor,
				Template:     seed.Template,
				Model:        seed.Model,
				RequestModel: seed.Model,
				Editable:     true,
			})
		}

		if len(options) == 0 {
			continue
		}
		sortModelOptions(options)
		catalog[t.ID] = options
	}

	return catalog
}

// buildRequestShapes 组装「service → native 模板的请求形态选项」，供「其他（自填模型 ID）」
// 与第一方厂商条目使用。没有 native 模板的 service（如 gm）不出现在结果里，前端据此不渲染
// 自填入口——那种 service 上自填模型也没有能承载它的模板。
func buildRequestShapes() map[string][]OnboardingRequestShape {
	shapes := make(map[string][]OnboardingRequestShape)

	for _, t := range probe.ListTestTypes() {
		var list []OnboardingRequestShape
		for _, v := range t.Variants {
			if v == nil || !v.Native || !v.SelfServeVisible {
				continue
			}
			list = append(list, OnboardingRequestShape{
				Template: v.ID,
				Label:    firstNonEmptyString(v.TemplateLabel, v.ID),
			})
		}
		if len(list) > 0 {
			shapes[t.ID] = list
		}
	}

	return shapes
}

// sortModelOptions 按「厂商词表顺序 → 标签 → key」排序，未收录厂商与未声明厂商排在最后。
func sortModelOptions(options []OnboardingModelOption) {
	rank := vendorRanks()
	sort.SliceStable(options, func(i, j int) bool {
		ri, rj := rank(options[i].Vendor), rank(options[j].Vendor)
		if ri != rj {
			return ri < rj
		}
		if options[i].Label != options[j].Label {
			return options[i].Label < options[j].Label
		}
		return options[i].Key < options[j].Key
	})
}

// vendorRanks 返回「厂商 code → 展示顺序」的查询函数；词表外与空值排最后。
func vendorRanks() func(string) int {
	vendors := modelvendor.Options()
	rank := make(map[string]int, len(vendors))
	for i, v := range vendors {
		rank[v.Code] = i
	}
	return func(code string) int {
		if r, ok := rank[code]; ok {
			return r
		}
		return len(rank)
	}
}

// firstNonEmptyString 返回首个非空串。
func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
