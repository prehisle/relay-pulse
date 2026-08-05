package config

import (
	"fmt"
	"strings"
	"time"

	"monitor/internal/logger"
)

// applyParentInheritance 实现子通道从父通道继承配置
func (c *AppConfig) applyParentInheritance() error {
	// 构建父通道索引
	rootByPath := make(map[string]*ServiceConfig)
	for i := range c.Monitors {
		if strings.TrimSpace(c.Monitors[i].Parent) != "" {
			continue
		}
		path := fmt.Sprintf("%s/%s/%s", c.Monitors[i].Provider, c.Monitors[i].Service, c.Monitors[i].Channel)
		if existing, exists := rootByPath[path]; exists {
			// 标记为多定义（nil 表示冲突）
			if existing != nil {
				rootByPath[path] = nil
			}
			continue
		}
		rootByPath[path] = &c.Monitors[i]
	}

	// 应用继承
	for i := range c.Monitors {
		child := &c.Monitors[i]
		parentPath := strings.TrimSpace(child.Parent)
		if parentPath == "" {
			continue
		}

		// 注意：provider/service/channel 的继承已在 Validate() 步骤 0 完成
		// 这里直接查找父通道
		parent := rootByPath[parentPath]
		if parent == nil {
			return fmt.Errorf("monitor[%d]: 找不到父通道: %s", i, parentPath)
		}

		// 应用各类配置继承
		inheritCoreBehavior(child, parent)
		inheritedTimings := inheritTimings(child, parent)
		inheritedRetry := inheritRetry(child, parent)
		inheritMeta(child, parent)
		inheritState(child, parent)
		inheritDisplayAndPricing(child, parent)

		// 修复继承后的 Duration 字段
		if err := fixInheritedDurations(i, child, parent, inheritedTimings, inheritedRetry); err != nil {
			return err
		}

		// 注意：以下字段不继承（有特殊约束）：
		// - Model: 父子关系的唯一区分字段，若继承则变成重复项
		// - Provider/Service/Channel: 由父子路径验证强制一致
	}

	return nil
}

// inheritCoreBehavior 继承核心监测行为配置
// 包括：APIKey、Template、BaseURL、Method、Body、SuccessContains、EnvVarName、Proxy、Headers、UserIDRefreshMinutes
func inheritCoreBehavior(child, parent *ServiceConfig) {
	if child.APIKey == "" {
		child.APIKey = parent.APIKey
	}
	if child.Template == "" {
		child.Template = parent.Template
	}
	if child.BaseURL == "" {
		child.BaseURL = parent.BaseURL
	}
	if child.URLPattern == "" {
		child.URLPattern = parent.URLPattern
	}
	if child.Method == "" {
		child.Method = parent.Method
	}
	inheritedBody := false
	if child.Body == "" {
		child.Body = parent.Body
		inheritedBody = true
	}
	if child.RequestModel == "" {
		child.RequestModel = parent.RequestModel
	}
	// ModelVendor 跟随 RequestModel 一起继承：同一通道的各 model 层必属同一厂商。
	// 与 Model 相反（Model 是父子的区分字段故刻意不继承，见本文件末尾说明）。
	if child.ModelVendor == "" {
		child.ModelVendor = parent.ModelVendor
	}
	// SuccessContains 是「当前 Body 应如何判成功」的语义条件，必须与 Body 同源
	// （同一份请求定义「发什么」与「期望什么」）。仅当 Body 也继承自父项时，才继承
	// 父项的 SuccessContains；若子项已有自己的 Body（来自不同模板或手写），其空
	// SuccessContains 表示「只按 HTTP 200 判活、不校验内容」，不能被父项的校验关键字
	// 污染（否则会出现「发 ping、却用父项算术答案校验」的永久 content_mismatch）。
	if inheritedBody && child.SuccessContains == "" {
		child.SuccessContains = parent.SuccessContains
	}
	if child.UserIDRefreshMinutes == 0 {
		child.UserIDRefreshMinutes = parent.UserIDRefreshMinutes
	}
	// 自定义环境变量名（用于 API Key 查找）
	if child.EnvVarName == "" {
		child.EnvVarName = parent.EnvVarName
	}

	// Proxy 继承（子通道可继承父通道的代理配置）
	if strings.TrimSpace(child.Proxy) == "" {
		child.Proxy = parent.Proxy
	}

	// Headers 继承（合并策略：父为基础，子覆盖）
	if len(parent.Headers) > 0 {
		merged := make(map[string]string, len(parent.Headers)+len(child.Headers))
		for k, v := range parent.Headers {
			merged[k] = v
		}
		for k, v := range child.Headers {
			merged[k] = v // 子覆盖父
		}
		child.Headers = merged
	}
}

// inheritedTimingsFlags 记录哪些时间配置字段是从 parent 继承的
type inheritedTimingsFlags struct {
	SlowLatency bool
	Timeout     bool
	Interval    bool
}

// inheritTimings 继承时间/阈值配置
// 返回标记哪些字段是继承的（用于后续重新计算 Duration）
func inheritTimings(child, parent *ServiceConfig) inheritedTimingsFlags {
	flags := inheritedTimingsFlags{}

	// SlowLatency: 字符串形式，空值表示未配置
	if strings.TrimSpace(child.SlowLatency) == "" && strings.TrimSpace(parent.SlowLatency) != "" {
		child.SlowLatency = parent.SlowLatency
		flags.SlowLatency = true
	}
	// Timeout: 字符串形式，空值表示未配置
	if strings.TrimSpace(child.Timeout) == "" && strings.TrimSpace(parent.Timeout) != "" {
		child.Timeout = parent.Timeout
		flags.Timeout = true
	}
	// Interval: 字符串形式，空值表示未配置
	if strings.TrimSpace(child.Interval) == "" && strings.TrimSpace(parent.Interval) != "" {
		child.Interval = parent.Interval
		flags.Interval = true
	}

	return flags
}

// inheritedRetryFlags 记录哪些重试配置字段是从 parent 继承的
type inheritedRetryFlags struct {
	Retry          bool
	RetryBaseDelay bool
	RetryMaxDelay  bool
	RetryJitter    bool
}

// inheritRetry 继承重试配置
// 返回标记哪些字段是继承的（用于后续重新计算 Duration）
func inheritRetry(child, parent *ServiceConfig) inheritedRetryFlags {
	flags := inheritedRetryFlags{}

	// Retry: nil 表示未配置，从 parent 继承
	if child.Retry == nil && parent.Retry != nil {
		v := *parent.Retry
		child.Retry = &v
		child.RetryCount = v
		flags.Retry = true
	}
	if strings.TrimSpace(child.RetryBaseDelay) == "" && strings.TrimSpace(parent.RetryBaseDelay) != "" {
		child.RetryBaseDelay = parent.RetryBaseDelay
		flags.RetryBaseDelay = true
	}
	if strings.TrimSpace(child.RetryMaxDelay) == "" && strings.TrimSpace(parent.RetryMaxDelay) != "" {
		child.RetryMaxDelay = parent.RetryMaxDelay
		flags.RetryMaxDelay = true
	}
	if child.RetryJitter == nil && parent.RetryJitter != nil {
		v := *parent.RetryJitter
		child.RetryJitter = &v
		child.RetryJitterValue = v
		flags.RetryJitter = true
	}

	return flags
}

// inheritMeta 继承元数据配置
// 包括：Category、KeyType、Sponsor、Provider 相关元数据、Board 配置
func inheritMeta(child, parent *ServiceConfig) {
	// Category: 子通道留空优先从父继承（父仍空时由 validate 兜底为 commercial）
	if child.Category == "" {
		child.Category = parent.Category
	}
	// KeyType: 空值表示未显式配置，可从父通道继承
	if child.KeyType == "" {
		child.KeyType = parent.KeyType
	}
	// Sponsor: 继承（通常同一 provider 的赞助者相同）
	if child.Sponsor == "" {
		child.Sponsor = parent.Sponsor
	}
	if child.SponsorURL == "" {
		child.SponsorURL = parent.SponsorURL
	}
	// 注意：SponsorLevel 不继承——按通道赞助语义，必须显式配置，避免隐式放大赞助范围
	// Provider 相关元数据
	if child.ProviderURL == "" {
		child.ProviderURL = parent.ProviderURL
	}
	if child.ProviderSlug == "" {
		child.ProviderSlug = parent.ProviderSlug
	}
	if child.ProviderName == "" {
		child.ProviderName = parent.ProviderName
	}
	if child.ServiceName == "" {
		child.ServiceName = parent.ServiceName
	}

	// --- 板块配置 ---
	// Board: 空值会在 Normalize 中默认为 "hot"，这里只继承显式配置
	if child.Board == "" && parent.Board != "" {
		child.Board = parent.Board
	}
	if child.ColdReason == "" && parent.ColdReason != "" {
		child.ColdReason = parent.ColdReason
	}
}

// inheritState 继承状态配置（级联 OR 逻辑）
// 包括：Disabled、Hidden
func inheritState(child, parent *ServiceConfig) {
	// Disabled: 父禁用则子也禁用
	if parent.Disabled {
		child.Disabled = true
		if child.DisabledReason == "" {
			child.DisabledReason = parent.DisabledReason
		}
	}
	// Hidden: 父隐藏则子也隐藏
	if parent.Hidden {
		child.Hidden = true
		if child.HiddenReason == "" {
			child.HiddenReason = parent.HiddenReason
		}
	}
}

// inheritDisplayAndPricing 继承显示和定价相关配置
// 包括：ChannelName、定价信息、收录日期
// 注意：Annotations 不在此继承，它们在 normalizeMonitorsPostInheritance 中独立解析
func inheritDisplayAndPricing(child, parent *ServiceConfig) {
	// 显示名称继承（子为空时继承）
	if child.ChannelName == "" {
		child.ChannelName = parent.ChannelName
	}

	// 定价信息继承（子为 nil 时继承）
	if child.PriceMin == nil && parent.PriceMin != nil {
		v := *parent.PriceMin
		child.PriceMin = &v
	}
	if child.PriceMax == nil && parent.PriceMax != nil {
		v := *parent.PriceMax
		child.PriceMax = &v
	}

	// 收录日期继承（子为空时继承）
	if child.ListedSince == "" {
		child.ListedSince = parent.ListedSince
	}
}

// fixInheritedDurations 修复继承后的 Duration 字段。
//
// Validate() 中 Duration 解析发生在 applyParentInheritance() 之前，
// 当子通道通过 parent 继承了字符串字段时，需要重新计算对应的 Duration 字段。
//
// 特殊情形：父通道没有显式 interval 字符串（依赖层级回退得到 IntervalDuration），
// 子通道 SponsorLevel 不继承，若子通道自身也无显式 interval，则会在
// normalizeDurationsForMonitorAt 中独立计算出 FREE 层的 5m——即便父通道是付费层 1m。
// 为避免这种"降级误差"，当子通道无自身显式 interval 时，强制同步父通道已解析的
// IntervalDuration，确保父子探测频率一致。
func fixInheritedDurations(
	monitorIdx int,
	child *ServiceConfig,
	parent *ServiceConfig,
	timings inheritedTimingsFlags,
	retry inheritedRetryFlags,
) error {
	// 修复时间配置 Duration
	if timings.Interval {
		// 子通道从父通道继承了显式 interval 字符串，重新解析为 Duration
		trimmed := strings.TrimSpace(child.Interval)
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("monitor[%d]: 解析继承的 interval 失败: %w", monitorIdx, err)
		}
		if d <= 0 {
			return fmt.Errorf("monitor[%d]: 继承的 interval 必须大于 0", monitorIdx)
		}
		child.IntervalDuration = d
	} else if strings.TrimSpace(child.Interval) == "" {
		// 子通道无显式 interval，父通道也无（否则 timings.Interval 会为 true）。
		// SponsorLevel 刻意不继承，若任由子通道独立计算会得到 FREE 层 5m，
		// 而父通道可能因为付费层级已解析为 1m。直接复制父通道的解析结果，
		// 保持父子探测频率一致。
		child.IntervalDuration = parent.IntervalDuration
	}

	if timings.SlowLatency {
		trimmed := strings.TrimSpace(child.SlowLatency)
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 解析继承的 slow_latency 失败: %w",
				monitorIdx, child.Provider, child.Service, child.Channel, err)
		}
		if d <= 0 {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 继承的 slow_latency 必须大于 0",
				monitorIdx, child.Provider, child.Service, child.Channel)
		}
		child.SlowLatencyDuration = d
	}

	if timings.Timeout {
		trimmed := strings.TrimSpace(child.Timeout)
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 解析继承的 timeout 失败: %w",
				monitorIdx, child.Provider, child.Service, child.Channel, err)
		}
		if d <= 0 {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 继承的 timeout 必须大于 0",
				monitorIdx, child.Provider, child.Service, child.Channel)
		}
		child.TimeoutDuration = d
	}

	// 继承后重新检查：slow_latency >= timeout 时黄灯基本不会触发
	if (timings.SlowLatency || timings.Timeout) &&
		child.SlowLatencyDuration >= child.TimeoutDuration {
		logger.Warn("config", "slow_latency >= timeout，慢响应黄灯可能不会触发（继承自 parent）",
			"monitor_index", monitorIdx,
			"provider", child.Provider,
			"service", child.Service,
			"channel", child.Channel,
			"model", child.Model,
			"slow_latency", child.SlowLatencyDuration,
			"timeout", child.TimeoutDuration)
	}

	// 修复重试配置 Duration
	// RetryCount 和 RetryJitterValue 已在继承时直接赋值，无需额外解析
	_ = retry.Retry
	_ = retry.RetryJitter

	if retry.RetryBaseDelay {
		trimmed := strings.TrimSpace(child.RetryBaseDelay)
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 解析继承的 retry_base_delay 失败: %w",
				monitorIdx, child.Provider, child.Service, child.Channel, err)
		}
		if d <= 0 {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 继承的 retry_base_delay 必须 > 0",
				monitorIdx, child.Provider, child.Service, child.Channel)
		}
		child.RetryBaseDelayDuration = d
	}

	if retry.RetryMaxDelay {
		trimmed := strings.TrimSpace(child.RetryMaxDelay)
		d, err := time.ParseDuration(trimmed)
		if err != nil {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 解析继承的 retry_max_delay 失败: %w",
				monitorIdx, child.Provider, child.Service, child.Channel, err)
		}
		if d <= 0 {
			return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 继承的 retry_max_delay 必须 > 0",
				monitorIdx, child.Provider, child.Service, child.Channel)
		}
		child.RetryMaxDelayDuration = d
	}

	// 继承后重新检查：max >= base
	if (retry.RetryBaseDelay || retry.RetryMaxDelay) &&
		child.RetryMaxDelayDuration < child.RetryBaseDelayDuration {
		return fmt.Errorf("monitor[%d] (provider=%s, service=%s, channel=%s): 继承后 retry_max_delay 必须 >= retry_base_delay",
			monitorIdx, child.Provider, child.Service, child.Channel)
	}

	return nil
}
