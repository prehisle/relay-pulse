package onboarding

import (
	"strings"

	"monitor/internal/config"
)

// BuildServiceConfigFromSubmission 将 Submission（连同已解密的 apiKey）翻译成 ServiceConfig。
//
// 该函数是"用户提交字段" → "运行时监测配置"的官方映射点：
//   - 发布到 monitors.d/ 时（AdminPublish）调用
//   - 管理后台对申请做即时探测（AdminTestSubmission）时调用
//   - 用户提交前自助探测（OnboardingTest）时调用（构造虚拟 Submission）
//
// 字段映射规则：
//   - PSC 默认派生：provider=lower(ProviderName 去空格转-)，service=ServiceType，channel=ChannelCode
//   - 管理员可在审核阶段通过 TargetProvider/TargetService/TargetChannel 覆盖 PSC（用于规范化命名）
//
// 注意：返回的 cfg 字段尚未经过模板填充和 Duration 派生；调用方如需用于内联探测，
// 应再过一次 config.ResolveSingleMonitor。
func BuildServiceConfigFromSubmission(sub *Submission, apiKey string) config.ServiceConfig {
	if sub == nil {
		return config.ServiceConfig{}
	}

	// 派生默认 PSC 标识
	providerSlug := strings.ToLower(strings.ReplaceAll(sub.ProviderName, " ", "-"))
	serviceType := sub.ServiceType
	channelCode := sub.ChannelCode

	// 管理员可覆盖最终发布时的 PSC 标识
	if v := strings.TrimSpace(sub.TargetProvider); v != "" {
		providerSlug = v
	}
	if v := strings.TrimSpace(sub.TargetService); v != "" {
		serviceType = v
	}
	if v := strings.TrimSpace(sub.TargetChannel); v != "" {
		channelCode = v
	}

	cfg := config.ServiceConfig{
		Provider:     providerSlug,
		ProviderName: sub.ProviderName,
		ProviderURL:  sub.WebsiteURL,
		Service:      serviceType,
		Channel:      channelCode,
		ChannelName:  sub.ChannelName,
		Template:     sub.TemplateName,
		// 行级模型只对 native 族非空；非 native 提交这两项恒为空，模板解析时按
		// config > template 回退链自动填上模板声明的值（lifecycle.resolveTemplateForMonitor）。
		Model:        sub.Model,
		ModelVendor:  sub.ModelVendor,
		BaseURL:      sub.BaseURL,
		APIKey:       apiKey,
		Category:     sub.Category,
		ListedSince:  sub.ListedSince,
		ExpiresAt:    sub.ExpiresAt,
		SponsorLevel: config.SponsorLevel(sub.SponsorLevel),
	}
	if sub.PriceMin != 0 {
		v := sub.PriceMin
		cfg.PriceMin = &v
	}
	if sub.PriceMax != 0 {
		v := sub.PriceMax
		cfg.PriceMax = &v
	}
	return cfg
}

// buildServiceConfig 是 BuildServiceConfigFromSubmission 的 method 包装，
// 保留以兼容 Service 内部既有调用方（AdminPublish 等）。
func (s *Service) buildServiceConfig(sub *Submission, apiKey string) config.ServiceConfig {
	return BuildServiceConfigFromSubmission(sub, apiKey)
}
