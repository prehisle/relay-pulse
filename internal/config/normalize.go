package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"monitor/internal/logger"
)

// Normalize 规范化配置（填充默认值等）
func (c *AppConfig) normalize() error {
	// 1. 全局时间配置（interval, slow_latency, timeout, retry 系列）
	if err := c.normalizeGlobalTimings(); err != nil {
		return err
	}

	// 2. 全局参数默认值
	if err := c.normalizeGlobalDefaults(); err != nil {
		return err
	}

	// 3. 功能模块配置（sponsor_pin, events, github, announcements）
	if err := c.normalizeFeatureConfigs(); err != nil {
		return err
	}

	// 4. 存储配置
	if err := c.normalizeStorageConfig(); err != nil {
		return err
	}

	// 5. 构建 Provider 映射索引
	ctx := newNormalizeContext()
	if err := c.buildNormalizeIndexes(ctx); err != nil {
		return err
	}

	// 6. 规范化每个监测项（不含注解解析，注解需在继承后处理）
	if err := c.normalizeMonitorsPreInheritance(ctx); err != nil {
		return err
	}

	// 7. 父子继承（必须在 per-monitor 规范化之后，因为继承依赖已规范化的路径/键）
	if err := c.applyParentInheritance(); err != nil {
		return err
	}

	// 8. 继承后处理：注解解析、board/cold_reason 清理
	// 必须在继承之后，确保子通道的注解能正确解析
	if err := c.normalizeMonitorsPostInheritance(ctx); err != nil {
		return err
	}

	return nil
}

// normalizeContext 承载 Normalize() 过程中的中间数据
// 主要用于传递全局构建的映射给 per-monitor 处理
type normalizeContext struct {
	// Provider 可见性映射
	disabledProviderMap map[string]string // provider -> reason
	hiddenProviderMap   map[string]string // provider -> reason
	// Annotation 体系
	annotationRules []AnnotationRule
}

// newNormalizeContext 创建并初始化 normalizeContext
func newNormalizeContext() *normalizeContext {
	return &normalizeContext{
		disabledProviderMap: make(map[string]string),
		hiddenProviderMap:   make(map[string]string),
	}
}

// normalizeGlobalTimings 规范化全局时间配置
// 包括：interval, slow_latency, timeout, retry 系列
func (c *AppConfig) normalizeGlobalTimings() error {
	// 巡检间隔
	if c.Interval == "" {
		c.IntervalDuration = time.Minute
	} else {
		d, err := time.ParseDuration(c.Interval)
		if err != nil {
			return fmt.Errorf("解析 interval 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("interval 必须大于 0")
		}
		c.IntervalDuration = d
	}

	// 慢请求阈值
	if c.SlowLatency == "" {
		c.SlowLatencyDuration = 5 * time.Second
	} else {
		d, err := time.ParseDuration(c.SlowLatency)
		if err != nil {
			return fmt.Errorf("解析 slow_latency 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("slow_latency 必须大于 0")
		}
		c.SlowLatencyDuration = d
	}

	// 请求超时时间（默认 10 秒）
	if c.Timeout == "" {
		c.TimeoutDuration = 10 * time.Second
	} else {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("解析 timeout 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("timeout 必须大于 0")
		}
		c.TimeoutDuration = d
	}

	// ===== 重试配置 =====
	// 重试次数（默认 0，不重试）
	if c.Retry == nil {
		c.RetryCount = 0
	} else {
		c.RetryCount = *c.Retry
	}
	if c.RetryCount < 0 {
		return fmt.Errorf("retry 必须 >= 0")
	}

	// 退避基准间隔（默认 200ms）
	if strings.TrimSpace(c.RetryBaseDelay) == "" {
		c.RetryBaseDelayDuration = 200 * time.Millisecond
	} else {
		d, err := time.ParseDuration(strings.TrimSpace(c.RetryBaseDelay))
		if err != nil {
			return fmt.Errorf("解析 retry_base_delay 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("retry_base_delay 必须 > 0")
		}
		c.RetryBaseDelayDuration = d
	}

	// 退避最大间隔（默认 2s）
	if strings.TrimSpace(c.RetryMaxDelay) == "" {
		c.RetryMaxDelayDuration = 2 * time.Second
	} else {
		d, err := time.ParseDuration(strings.TrimSpace(c.RetryMaxDelay))
		if err != nil {
			return fmt.Errorf("解析 retry_max_delay 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("retry_max_delay 必须 > 0")
		}
		c.RetryMaxDelayDuration = d
	}

	// 全局 max >= base 校验
	if c.RetryMaxDelayDuration < c.RetryBaseDelayDuration {
		return fmt.Errorf("retry_max_delay 必须 >= retry_base_delay")
	}

	// 抖动比例（默认 0.2）
	if c.RetryJitter == nil {
		c.RetryJitterValue = 0.2
	} else {
		c.RetryJitterValue = *c.RetryJitter
	}
	if c.RetryJitterValue < 0 || c.RetryJitterValue > 1 {
		return fmt.Errorf("retry_jitter 必须在 0 到 1 之间，当前值: %.2f", c.RetryJitterValue)
	}

	return nil
}

// normalizeGlobalDefaults 规范化全局参数默认值
func (c *AppConfig) normalizeGlobalDefaults() error {
	// 黄色状态权重（默认 0.7，允许 0.01-1.0）
	// 注意：0 被视为未配置，将使用默认值 0.7
	// 如果需要极低权重，请使用 0.01 或更小的正数
	if c.DegradedWeight == 0 {
		c.DegradedWeight = 0.7 // 未配置时使用默认值
	}
	if c.DegradedWeight < 0 || c.DegradedWeight > 1 {
		return fmt.Errorf("degraded_weight 必须在 0 到 1 之间（0 表示使用默认值 0.7），当前值: %.2f", c.DegradedWeight)
	}

	// 可信代理边界：未配置时只信本机回环（cloudflared / nginx sidecar 的典型部署形态），
	// 绝不回落到 gin 默认的"信任所有代理"——那会让任何人伪造 X-Forwarded-For 顶替客户端 IP。
	if len(c.Server.TrustedProxies) == 0 {
		c.Server.TrustedProxies = append([]string(nil), defaultTrustedProxies...)
	} else {
		// 就地 trim：校验与实际交给 gin 的必须是同一个字符串，否则 " 127.0.0.1 " 会
		// 过了加载期校验、却在运行时解析失败而触发收紧兜底（行为正确但与文档承诺不符）
		for i, entry := range c.Server.TrustedProxies {
			c.Server.TrustedProxies[i] = strings.TrimSpace(entry)
		}
	}

	// 公开访问的基础 URL（默认 https://relaypulse.top）
	if c.PublicBaseURL == "" {
		c.PublicBaseURL = "https://relaypulse.top"
	}

	// 规范化 baseURL：去除尾随斜杠、验证协议
	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	if err := validateBaseURL(c.PublicBaseURL); err != nil {
		return fmt.Errorf("public_base_url 无效: %w", err)
	}

	// 最大并发数（默认 10）
	// - 未配置或 0：使用默认值 10
	// - -1：无限制（自动扩容到监测项数量）
	// - >0：作为硬上限，超过时排队执行
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 10
	}
	if c.MaxConcurrency < -1 {
		return fmt.Errorf("max_concurrency 无效值 %d，有效值：-1(无限制)、0(默认10)、>0(硬上限)", c.MaxConcurrency)
	}

	// 探测错峰（默认开启）
	if c.StaggerProbes == nil {
		defaultValue := true
		c.StaggerProbes = &defaultValue
	}

	// 并发查询限制（默认 10）
	if c.ConcurrentQueryLimit == 0 {
		c.ConcurrentQueryLimit = 10
	}
	if c.ConcurrentQueryLimit < 1 {
		return fmt.Errorf("concurrent_query_limit 必须 >= 1，当前值: %d", c.ConcurrentQueryLimit)
	}

	// 批量查询最大 key 数（默认 300）
	if c.BatchQueryMaxKeys == 0 {
		c.BatchQueryMaxKeys = 300
	}
	if c.BatchQueryMaxKeys < 1 {
		return fmt.Errorf("batch_query_max_keys 必须 >= 1，当前值: %d", c.BatchQueryMaxKeys)
	}

	// 缓存 TTL 配置
	if err := c.CacheTTL.normalize(); err != nil {
		return err
	}

	// 通道技术细节暴露配置（默认 true，保持向后兼容）
	if c.ExposeChannelDetails == nil {
		defaultValue := true
		c.ExposeChannelDetails = &defaultValue
	}

	return nil
}

// normalizeFeatureConfigs 规范化功能模块配置
// 包括：sponsor_pin, events, github, announcements
func (c *AppConfig) normalizeFeatureConfigs() error {
	// 自动移板配置默认值与解析
	if c.Boards.AutoMove.ThresholdCold == 0 {
		c.Boards.AutoMove.ThresholdCold = 10.0
	}
	if c.Boards.AutoMove.ThresholdDown == 0 {
		c.Boards.AutoMove.ThresholdDown = 50.0
	}
	if c.Boards.AutoMove.ThresholdUp == 0 {
		c.Boards.AutoMove.ThresholdUp = 55.0
	}
	if c.Boards.AutoMove.MinProbes == 0 {
		c.Boards.AutoMove.MinProbes = 10
	}
	if strings.TrimSpace(c.Boards.AutoMove.CheckInterval) == "" {
		c.Boards.AutoMove.CheckInterval = "30m"
	}
	{
		d, err := time.ParseDuration(strings.TrimSpace(c.Boards.AutoMove.CheckInterval))
		if err != nil {
			logger.Warn("config", "boards.auto_move.check_interval 无效，已回退默认值",
				"value", c.Boards.AutoMove.CheckInterval, "default", "30m")
			d = 30 * time.Minute
			c.Boards.AutoMove.CheckInterval = "30m"
		} else if d <= 0 {
			logger.Warn("config", "boards.auto_move.check_interval 必须 > 0，已回退默认值",
				"value", c.Boards.AutoMove.CheckInterval, "default", "30m")
			d = 30 * time.Minute
			c.Boards.AutoMove.CheckInterval = "30m"
		}
		c.Boards.AutoMove.CheckIntervalDuration = d
	}
	// 默认值填充后的阈值一致性校验（覆盖用户省略字段导致的组合非法情况）
	if c.Boards.AutoMove.Enabled {
		if c.Boards.AutoMove.ThresholdCold >= c.Boards.AutoMove.ThresholdDown {
			return fmt.Errorf("boards.auto_move.threshold_cold(%.2f) 必须小于 threshold_down(%.2f)，冷板阈值必须低于降级阈值",
				c.Boards.AutoMove.ThresholdCold, c.Boards.AutoMove.ThresholdDown)
		}
	}

	// 赞助通道置顶配置默认值
	if c.SponsorPin.MaxPinned == 0 {
		c.SponsorPin.MaxPinned = 10
	}
	if c.SponsorPin.MinUptime == 0 {
		c.SponsorPin.MinUptime = 95.0
	}
	if c.SponsorPin.MinLevel == "" {
		c.SponsorPin.MinLevel = SponsorLevelBeacon
	}
	// 旧值兼容迁移（持续 1 个版本周期）
	if migrated, ok := c.SponsorPin.MinLevel.deprecatedToNew(); ok {
		logger.Warn("config", "sponsor_pin.min_level 使用已废弃的赞助等级，已自动迁移",
			"old", c.SponsorPin.MinLevel, "new", migrated)
		c.SponsorPin.MinLevel = migrated
	}
	// 验证赞助通道置顶配置
	if c.SponsorPin.MaxPinned < 0 {
		logger.Warn("config", "sponsor_pin.max_pinned 无效，已回退默认值", "value", c.SponsorPin.MaxPinned, "default", 10)
		c.SponsorPin.MaxPinned = 10
	}
	if c.SponsorPin.MinUptime < 0 || c.SponsorPin.MinUptime > 100 {
		logger.Warn("config", "sponsor_pin.min_uptime 超出范围，已回退默认值", "value", c.SponsorPin.MinUptime, "default", 95.0)
		c.SponsorPin.MinUptime = 95.0
	}
	if !c.SponsorPin.MinLevel.isValid() || c.SponsorPin.MinLevel == SponsorLevelNone {
		logger.Warn("config", "sponsor_pin.min_level 无效，已回退默认值", "value", c.SponsorPin.MinLevel, "default", SponsorLevelBeacon)
		c.SponsorPin.MinLevel = SponsorLevelBeacon
	}

	// Events 配置默认值
	if c.Events.Mode == "" {
		c.Events.Mode = "model" // 默认按模型独立触发事件
	}
	if c.Events.Mode != "model" && c.Events.Mode != "channel" {
		return fmt.Errorf("events.mode 必须是 'model' 或 'channel'，当前值: %s", c.Events.Mode)
	}
	if c.Events.DownThreshold == 0 {
		c.Events.DownThreshold = 2 // 默认连续 2 次不可用触发 DOWN
	}
	if c.Events.UpThreshold == 0 {
		c.Events.UpThreshold = 1 // 默认 1 次可用触发 UP
	}
	if c.Events.ChannelDownThreshold == 0 {
		c.Events.ChannelDownThreshold = 1 // 默认 1 个模型 DOWN 触发通道 DOWN
	}
	if c.Events.DownThreshold < 1 {
		return fmt.Errorf("events.down_threshold 必须 >= 1，当前值: %d", c.Events.DownThreshold)
	}
	if c.Events.UpThreshold < 1 {
		return fmt.Errorf("events.up_threshold 必须 >= 1，当前值: %d", c.Events.UpThreshold)
	}
	if c.Events.ChannelDownThreshold < 1 {
		return fmt.Errorf("events.channel_down_threshold 必须 >= 1，当前值: %d", c.Events.ChannelDownThreshold)
	}
	if c.Events.ChannelCountMode == "" {
		c.Events.ChannelCountMode = "recompute" // 默认使用重算模式，更稳定
	}
	if c.Events.ChannelCountMode != "incremental" && c.Events.ChannelCountMode != "recompute" {
		return fmt.Errorf("events.channel_count_mode 必须是 'incremental' 或 'recompute'，当前值: %s", c.Events.ChannelCountMode)
	}

	// GitHub 配置默认值与环境变量覆盖
	if err := c.GitHub.normalize(); err != nil {
		return err
	}

	// 公告配置默认值与解析
	if err := c.Announcements.normalize(); err != nil {
		return err
	}

	// 公告启用但未配置 token：仅警告（可匿名访问，但容易被限流）
	if c.Announcements.IsEnabled() && strings.TrimSpace(c.GitHub.Token) == "" {
		logger.Warn("config", "announcements 已启用但未配置 GITHUB_TOKEN，将使用匿名请求（可能触发限流）")
	}

	// 自助收录配置默认值与解析
	if err := c.normalizeOnboardingConfig(); err != nil {
		return err
	}

	c.normalizeChangeRequestConfig()

	return nil
}

// normalizeChangeRequestConfig 规范化变更请求配置
func (c *AppConfig) normalizeChangeRequestConfig() {
	if !c.ChangeRequests.Enabled {
		return
	}
	if c.ChangeRequests.MaxPerIPPerDay <= 0 {
		c.ChangeRequests.MaxPerIPPerDay = 3
	}
}

// normalizeOnboardingConfig 规范化自助收录配置。
//
// 注意：admin_token / encryption_key / proof_secret / proof_ttl 这组密钥是 onboarding 与
// change_requests **共享**的（见 ChangeRequestConfig 注释；main.go 的变更请求初始化也读
// cfg.Onboarding.* 这几项）。因此只要任一功能启用就必须在此完成默认值填充与必填校验——
// 否则 change_requests 单开（onboarding 关闭）时 ProofTTLDuration 会留零值，proof 签发即刻
// 过期，变更提交全被拒。
func (c *AppConfig) normalizeOnboardingConfig() error {
	if !c.Onboarding.Enabled && !c.ChangeRequests.Enabled {
		return nil
	}

	// proof TTL（默认 5 分钟）
	if strings.TrimSpace(c.Onboarding.ProofTTL) == "" {
		c.Onboarding.ProofTTL = "5m"
	}
	d, err := time.ParseDuration(strings.TrimSpace(c.Onboarding.ProofTTL))
	if err != nil || d <= 0 {
		logger.Warn("config", "onboarding.proof_ttl 无效，已回退默认值",
			"value", c.Onboarding.ProofTTL, "default", "5m")
		d = 5 * time.Minute
		c.Onboarding.ProofTTL = "5m"
	}
	c.Onboarding.ProofTTLDuration = d

	// 每 IP 每天最大提交数（默认 5）
	if c.Onboarding.MaxPerIPPerDay <= 0 {
		c.Onboarding.MaxPerIPPerDay = 5
	}

	// 环境变量覆盖加密密钥
	if envKey := os.Getenv("ONBOARDING_ENCRYPTION_KEY"); envKey != "" {
		c.Onboarding.EncryptionKey = envKey
	}

	// 必要配置校验
	if c.Onboarding.AdminToken == "" {
		return fmt.Errorf("onboarding.admin_token 不能为空（管理后台鉴权必须）")
	}
	if c.Onboarding.EncryptionKey == "" {
		return fmt.Errorf("onboarding.encryption_key 不能为空（API Key 加密必须），可通过环境变量 ONBOARDING_ENCRYPTION_KEY 设置")
	}
	if c.Onboarding.ProofSecret == "" {
		return fmt.Errorf("onboarding.proof_secret 不能为空（test proof 签名必须）")
	}

	return nil
}

// normalizeStorageConfig 规范化存储配置
// 包括：SQLite/PostgreSQL 配置默认值、连接池参数、retention/archive 配置
func (c *AppConfig) normalizeStorageConfig() error {
	// 存储配置默认值
	if c.Storage.Type == "" {
		c.Storage.Type = "sqlite" // 默认使用 SQLite
	}
	if c.Storage.Type == "sqlite" && c.Storage.SQLite.Path == "" {
		c.Storage.SQLite.Path = "monitor.db" // 默认路径
	}
	// SQLite 参数上限保护：默认上限通常为 999，每个 key 需要 4 个参数 (provider, service, channel, model)
	if c.Storage.Type == "sqlite" && c.EnableBatchQuery {
		const sqliteMaxParams = 999
		const keyParams = 4
		maxKeys := sqliteMaxParams / keyParams
		if c.BatchQueryMaxKeys > maxKeys {
			logger.Warn("config", "batch_query_max_keys 超出 SQLite 参数上限，已回退",
				"value", c.BatchQueryMaxKeys, "sqlite_max_params", sqliteMaxParams, "fallback", maxKeys)
			c.BatchQueryMaxKeys = maxKeys
		}
	}

	// DB 侧 timeline 聚合相关验证
	if c.EnableDBTimelineAgg {
		if c.Storage.Type != "postgres" {
			logger.Warn("config", "enable_db_timeline_agg 仅支持 PostgreSQL，将自动回退到应用层聚合", "storage_type", c.Storage.Type)
		}
		if !c.EnableBatchQuery {
			logger.Info("config", "enable_db_timeline_agg 依赖 enable_batch_query=true 才会生效")
		}
	}

	if c.Storage.Type == "postgres" {
		if c.Storage.Postgres.Port == 0 {
			c.Storage.Postgres.Port = 5432
		}
		if c.Storage.Postgres.SSLMode == "" {
			c.Storage.Postgres.SSLMode = "disable"
		}
		// 连接池配置（考虑并发查询场景）
		// - 串行查询：25 个连接足够（默认保守配置）
		// - 并发查询：建议 50+ 连接（支持多个并发请求）
		if c.Storage.Postgres.MaxOpenConns == 0 {
			// 根据是否启用并发查询设置默认值
			if c.EnableConcurrentQuery {
				c.Storage.Postgres.MaxOpenConns = 50 // 并发查询模式
			} else {
				c.Storage.Postgres.MaxOpenConns = 25 // 串行查询模式
			}
		}
		if c.Storage.Postgres.MaxIdleConns == 0 {
			// 空闲连接数建议为最大连接数的 20-30%
			if c.EnableConcurrentQuery {
				c.Storage.Postgres.MaxIdleConns = 10
			} else {
				c.Storage.Postgres.MaxIdleConns = 5
			}
		}
		if c.Storage.Postgres.ConnMaxLifetime == "" {
			c.Storage.Postgres.ConnMaxLifetime = "1h"
		}

		// 并发查询配置校验（仅警告，不强制修改）
		if c.EnableConcurrentQuery {
			if c.Storage.Postgres.MaxOpenConns > 0 && c.Storage.Postgres.MaxOpenConns < c.ConcurrentQueryLimit {
				logger.Warn("config", "max_open_conns 小于 concurrent_query_limit，可能导致连接池等待",
					"max_open_conns", c.Storage.Postgres.MaxOpenConns, "concurrent_query_limit", c.ConcurrentQueryLimit)
			}
		}
	}

	// SQLite 场景下的并发查询警告
	if c.Storage.Type == "sqlite" && c.EnableConcurrentQuery {
		logger.Warn("config", "SQLite 使用单连接，并发查询无性能收益，建议关闭 enable_concurrent_query")
	}

	// 历史数据保留与清理配置
	if err := c.Storage.Retention.normalize(); err != nil {
		return err
	}

	// 历史数据归档配置（仅在启用时校验）
	if c.Storage.Archive.IsEnabled() {
		if err := c.Storage.Archive.normalize(); err != nil {
			return err
		}
		// 校验归档天数应小于保留天数，避免数据在归档前被清理
		// 这是一个严重的配置错误，直接返回错误防止启动
		if c.Storage.Retention.IsEnabled() && c.Storage.Archive.ArchiveDays >= c.Storage.Retention.Days {
			return fmt.Errorf("配置冲突: archive.archive_days(%d) >= retention.days(%d)，数据将在归档前被清理。"+
				"建议: retention.days >= archive_days + backfill_days，如 retention.days=%d",
				c.Storage.Archive.ArchiveDays, c.Storage.Retention.Days,
				c.Storage.Archive.ArchiveDays+c.Storage.Archive.BackfillDays)
		}
		// 校验 backfill 窗口：如果 retention.days < archive_days + backfill_days，停机补齐可能产生空归档
		if c.Storage.Retention.IsEnabled() && c.Storage.Archive.BackfillDays > 1 {
			oldestNeeded := c.Storage.Archive.ArchiveDays + c.Storage.Archive.BackfillDays - 1
			if oldestNeeded >= c.Storage.Retention.Days {
				return fmt.Errorf("配置冲突: archive.archive_days(%d) + backfill_days(%d) - 1 = %d >= retention.days(%d)，"+
					"停机补齐时数据可能已被清理。建议: retention.days >= %d",
					c.Storage.Archive.ArchiveDays, c.Storage.Archive.BackfillDays,
					oldestNeeded, c.Storage.Retention.Days, oldestNeeded+1)
			}
		}
	}

	return nil
}

// buildNormalizeIndexes 构建 Provider 映射索引
// 这些索引用于后续 normalizeMonitors() 中的状态注入
func (c *AppConfig) buildNormalizeIndexes(ctx *normalizeContext) error {
	// 构建禁用的服务商映射（provider -> reason）
	// 注意：provider 统一转小写，与 API 查询逻辑保持一致
	for i, dp := range c.DisabledProviders {
		provider := strings.ToLower(strings.TrimSpace(dp.Provider))
		if provider == "" {
			return fmt.Errorf("disabled_providers[%d]: provider 不能为空", i)
		}
		if _, exists := ctx.disabledProviderMap[provider]; exists {
			return fmt.Errorf("disabled_providers[%d]: provider '%s' 重复配置", i, dp.Provider)
		}
		ctx.disabledProviderMap[provider] = strings.TrimSpace(dp.Reason)
	}

	// 构建隐藏的服务商映射（provider -> reason）
	// 注意：provider 统一转小写，与 API 查询逻辑保持一致
	for i, hp := range c.HiddenProviders {
		provider := strings.ToLower(strings.TrimSpace(hp.Provider))
		if provider == "" {
			return fmt.Errorf("hidden_providers[%d]: provider 不能为空", i)
		}
		if _, exists := ctx.hiddenProviderMap[provider]; exists {
			return fmt.Errorf("hidden_providers[%d]: provider '%s' 重复配置", i, hp.Provider)
		}
		ctx.hiddenProviderMap[provider] = strings.TrimSpace(hp.Reason)
	}

	// 构建 annotation_rules
	ctx.annotationRules = append(ctx.annotationRules, c.AnnotationRules...)

	return nil
}
