package api

import (
	"sort"

	"monitor/internal/modelvendor"
	"monitor/internal/probe"
)

// OnboardingModelOption 是自助收录「选模型」下拉里的一个条目。
//
// 表单里不出现「模板」二字：用户看到的是厂商与模型名，模板是选中后随行提交的实现细节。
// 条目**全部**由专属模板派生——模板自己声明了 model / request_model / model_vendor 与请求形态，
// 提交方一项都填不了。第一方厂商模型（GLM/Kimi/豆包…）同样各有一个专属模板（cc-glm52-arith 等）。
type OnboardingModelOption struct {
	// Key 选项唯一键，供前端做受控选择。当前恒等于 Template，保留独立字段是因为它是前端的
	// 受控选择键，不该与「提交时发什么」耦合。
	Key string `json:"key"`
	// Label 人话模型名。
	Label string `json:"label"`
	// Vendor 模型厂商 code；空表示模板未声明（自建部署的模板可能如此，站点侧全部声明）。
	Vendor string `json:"vendor"`
	// Template 提交时随行的 template_name。
	Template string `json:"template"`
	// RequestModel 该条目实际会请求的模型 ID，**仅供只读展示**——让用户看清自己选中的是什么，
	// 不作为提交字段（模型由模板在服务端解析，不经 wire）。
	RequestModel string `json:"request_model"`
}

// buildModelCatalog 组装「service → 可选模型」目录。
//
// 唯一来源是注册表里**自助可见、非 native、且声明了模型**的模板，各出一条。
//
// native 族（cc-native-* / cx-native-*）刻意不声明模型、要求监测行填，本就进不了这里；
// 它们同时标了 self_serve_visible:false，只供管理员与既有 native 通道使用。
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

		if len(options) == 0 {
			continue
		}
		sortModelOptions(options)
		catalog[t.ID] = options
	}

	return catalog
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
