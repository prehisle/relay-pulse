package onboarding

import (
	"fmt"
	"regexp"
	"strings"
)

var channelSourcePattern = regexp.MustCompile(`^[a-z0-9]{2,5}$`)

// ChannelSourceOption 是自助收录「通道来源」词表的单一真相源条目。
// Category 既用于前端分组展示，也参与「通道类型↔来源」自洽校验（见 channelTypeAllowedCategories），
// 但不参与 channel code 派生。
type ChannelSourceOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// ChannelSourceCatalog 是后端校验与前端 meta 下发共用的唯一「通道来源」词表，按 service_type 划分。
// 人工新增/调整来源时只改这里，避免 Submit 校验、/meta 下发、前端选项三处漂移。
// 约束：每个 Value 必须满足 channelSourcePattern（2-5 位小写字母/数字）。
//
// cc/cx 的 agg 是**第三方聚合路由**（OpenRouter 一类：同一个模型可能被路由到多个上游）。
// 刻意不为它新开第四种 channel_type：类型轴问的是线路性质，而「聚合」不是第四种性质——
// 上游不确定正是 mixed 的定义，故 Category=mixed 自动落 M，无需改类型映射表。
// 与之相对，云平台转售（火山方舟一类）上游是确定的，走 cloud/nat。
//
// cc/cx 的 nat 是第一方厂商（智谱 GLM / 月之暗面 Kimi / MiniMax / DeepSeek / Qwen 等）
// 用自家模型开放的 Anthropic Messages / OpenAI Responses 兼容端点——协议是那两家的，
// 模型是厂商自己的，两者由 model_vendor 正交轴分别表达。不复用 api：那两条的 Label
// 明确写着 Anthropic Console / OpenAI Platform，指的是那两家自己的官方入口。
// gm 不加：Gemini 的 api（AI Studio）本身就是第一方入口。
// Category=official 使其自动落入 channelTypeAllowedCategories 的 O（官方直连 / 官方转售），
// 那张映射表无需改动。
var ChannelSourceCatalog = map[string][]ChannelSourceOption{
	"cc": {
		{Value: "pro", Label: "Claude Pro 订阅", Category: "subscription"},
		{Value: "max", Label: "Claude Max 订阅", Category: "subscription"},
		{Value: "team", Label: "Claude Team", Category: "subscription"},
		{Value: "ent", Label: "Claude Enterprise", Category: "subscription"},
		{Value: "api", Label: "Anthropic Console API", Category: "official"},
		{Value: "nat", Label: "模型厂商开放平台（自有模型）", Category: "official"},
		{Value: "aws", Label: "AWS Bedrock", Category: "cloud"},
		{Value: "azr", Label: "Azure AI Foundry", Category: "cloud"},
		{Value: "gcp", Label: "Google Vertex AI", Category: "cloud"},
		{Value: "kiro", Label: "Kiro（逆向）", Category: "reverse"},
		{Value: "antg", Label: "Antigravity（逆向）", Category: "reverse"},
		{Value: "agg", Label: "第三方聚合路由（OpenRouter 等）", Category: "mixed"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
	"cx": {
		{Value: "plus", Label: "ChatGPT Plus", Category: "subscription"},
		{Value: "pro", Label: "ChatGPT Pro", Category: "subscription"},
		{Value: "team", Label: "ChatGPT Team", Category: "subscription"},
		{Value: "biz", Label: "ChatGPT Business", Category: "subscription"},
		{Value: "ent", Label: "ChatGPT Enterprise", Category: "subscription"},
		{Value: "api", Label: "OpenAI Platform API", Category: "official"},
		{Value: "nat", Label: "模型厂商开放平台（自有模型）", Category: "official"},
		{Value: "agg", Label: "第三方聚合路由（OpenRouter 等）", Category: "mixed"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
	"gm": {
		{Value: "free", Label: "Google 账号 Free", Category: "subscription"},
		{Value: "adv", Label: "Gemini Advanced", Category: "subscription"},
		{Value: "api", Label: "Gemini API (AI Studio)", Category: "official"},
		{Value: "gcp", Label: "Google Vertex AI", Category: "cloud"},
		{Value: "antg", Label: "Antigravity（逆向）", Category: "reverse"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
}

// ChannelSourceOptionsByService 返回词表的深拷贝，供 API 层下发前端，避免外部修改污染真相源。
func ChannelSourceOptionsByService() map[string][]ChannelSourceOption {
	out := make(map[string][]ChannelSourceOption, len(ChannelSourceCatalog))
	for service, opts := range ChannelSourceCatalog {
		out[service] = append([]ChannelSourceOption(nil), opts...)
	}
	return out
}

// channelTypeAllowedCategories 定义每个通道类型（O/R/M）允许搭配的来源类别（ChannelSourceOption.Category）。
// 单一真相源：同时供 Submit/AdminUpdate 后端校验与 /api/onboarding/meta 下发前端做下拉过滤，
// 避免「官方通道却选逆向来源」一类不自洽提交，且杜绝前后端规则漂移。
// 当前为干净划分——每个 Category 恰属一个类型：官方上游归 O、逆向归 R、混合归 M。
var channelTypeAllowedCategories = map[string][]string{
	"O": {"subscription", "official", "cloud"},
	"R": {"reverse"},
	"M": {"mixed"},
}

// ChannelTypeAllowedCategories 返回映射的深拷贝，供 API 层下发前端，避免外部修改污染真相源。
func ChannelTypeAllowedCategories() map[string][]string {
	out := make(map[string][]string, len(channelTypeAllowedCategories))
	for ct, cats := range channelTypeAllowedCategories {
		out[ct] = append([]string(nil), cats...)
	}
	return out
}

// lookupChannelSource 在对应 service 受控词表中查找 channel_source，返回完整 option。
// 词表成员资格是权威判定；格式正则仅用于在非法输入时给出更清晰的错误信息。
func lookupChannelSource(serviceType, source string) (ChannelSourceOption, error) {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	source = strings.ToLower(strings.TrimSpace(source))
	if !channelSourcePattern.MatchString(source) {
		return ChannelSourceOption{}, fmt.Errorf("channel_source 格式无效（%q），应为 2-5 位小写字母或数字", source)
	}
	options, ok := ChannelSourceCatalog[serviceType]
	if !ok {
		return ChannelSourceOption{}, fmt.Errorf("service_type %q 不支持", serviceType)
	}
	for _, opt := range options {
		if opt.Value == source {
			return opt, nil
		}
	}
	return ChannelSourceOption{}, fmt.Errorf("channel_source %q 不在 service_type=%q 的允许来源中，如需新增请联系运营（QQ:18058344）", source, serviceType)
}

// validateChannelSource 校验 channel_source 是否为对应 service 词表中的合法值，返回小写规范值。
func validateChannelSource(serviceType, source string) (string, error) {
	opt, err := lookupChannelSource(serviceType, source)
	if err != nil {
		return "", err
	}
	return opt.Value, nil
}

// validateChannelTypeSource 在 validateChannelSource 基础上追加「通道类型↔来源类别」自洽校验：
// 来源既要在该 service 词表内，其 Category 还须落在该 channelType 的允许集合中。
// channelType 须已规范为 O/R/M（调用方保证）。返回小写规范 source。
func validateChannelTypeSource(channelType, serviceType, source string) (string, error) {
	opt, err := lookupChannelSource(serviceType, source)
	if err != nil {
		return "", err
	}
	allowed, ok := channelTypeAllowedCategories[strings.ToUpper(strings.TrimSpace(channelType))]
	if !ok {
		return "", fmt.Errorf("channel_type 无效（%q），仅支持 O/R/M", channelType)
	}
	for _, cat := range allowed {
		if opt.Category == cat {
			return opt.Value, nil
		}
	}
	return "", fmt.Errorf("通道来源「%s」与通道类型「%s」不匹配，请选择与该类型相符的来源", opt.Label, channelTypeLabel(channelType))
}

// channelTypeLabel 返回通道类型的中文标签，用于校验错误信息。
func channelTypeLabel(channelType string) string {
	switch strings.ToUpper(strings.TrimSpace(channelType)) {
	case "O":
		return "官方通道"
	case "R":
		return "逆向通道"
	case "M":
		return "混合通道"
	default:
		return channelType
	}
}
