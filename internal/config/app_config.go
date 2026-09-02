package config

import "time"

// ServerConfig 描述 HTTP 入口如何判定"真实客户端 IP"。
//
// 该判定是所有按 IP 计数的防护（探测限流、收录/变更每日配额、submitter_ip_hash 审计）
// 的唯一信任根：判错则三者同时失效。默认值刻意收紧到"只信本机回环代理"，
// 且不预设任何厂商专有 Header——自托管者不在 CDN 后面时开箱即安全。
type ServerConfig struct {
	// TrustedPlatform 是由可信入口覆写、不可被客户端伪造的客户端 IP Header
	// （如 Cloudflare 的 CF-Connecting-IP）。非空时 gin 直接采信该 Header 并跳过
	// X-Forwarded-For 链解析，因此**仅当应用端口不可被绕过入口直连时**才可设置。
	TrustedPlatform string `yaml:"trusted_platform" json:"trusted_platform"`

	// TrustedProxies 是允许提供 X-Forwarded-For / X-Real-IP 的代理地址（IP 或 CIDR）。
	// 留空则回落到 defaultTrustedProxies（仅回环）。
	TrustedProxies []string `yaml:"trusted_proxies" json:"trusted_proxies"`
}

// AppConfig 应用配置
type AppConfig struct {
	// ===== 探测时间配置 =====

	// 巡检间隔（支持 Go duration 格式，例如 "30s"、"1m", "5m"）
	Interval string `yaml:"interval" json:"interval"`

	// 解析后的巡检间隔（内部使用，不序列化）
	IntervalDuration time.Duration `yaml:"-" json:"-"`

	// 慢请求阈值（超过则从绿降为黄），支持 Go duration 格式，例如 "5s"、"3s"
	SlowLatency string `yaml:"slow_latency" json:"slow_latency"`

	// 解析后的慢请求阈值（内部使用，不序列化）
	SlowLatencyDuration time.Duration `yaml:"-" json:"-"`

	// 请求超时时间（支持 Go duration 格式，例如 "10s"、"30s"，默认 "10s"）
	Timeout string `yaml:"timeout" json:"timeout"`

	// 解析后的超时时间（内部使用，不序列化）
	TimeoutDuration time.Duration `yaml:"-" json:"-"`

	// ===== 重试配置 =====

	// 探测重试次数（默认 0，不重试；表示"额外重试次数"，不含首次尝试）
	// 使用 *int 以区分"未设置(nil)"和"显式设置为 0"
	Retry *int `yaml:"retry,omitempty" json:"retry,omitempty"`

	// 解析后的全局重试次数（内部使用）
	RetryCount int `yaml:"-" json:"-"`

	// 重试退避基准间隔（默认 200ms）
	RetryBaseDelay string `yaml:"retry_base_delay" json:"retry_base_delay"`

	// 解析后的退避基准间隔（内部使用）
	RetryBaseDelayDuration time.Duration `yaml:"-" json:"-"`

	// 重试退避最大间隔（默认 2s）
	RetryMaxDelay string `yaml:"retry_max_delay" json:"retry_max_delay"`

	// 解析后的退避最大间隔（内部使用）
	RetryMaxDelayDuration time.Duration `yaml:"-" json:"-"`

	// 重试抖动比例（0-1，默认 0.2；0 表示无抖动）
	// 使用 *float64 以区分"未设置(nil)"和"显式设置为 0"
	RetryJitter *float64 `yaml:"retry_jitter,omitempty" json:"retry_jitter,omitempty"`

	// 解析后的抖动比例（内部使用）
	RetryJitterValue float64 `yaml:"-" json:"-"`

	// ===== 运行时配置 =====

	// 可用率中黄色状态的权重（0-1，默认 0.7）
	// 绿色=1.0, 黄色=degraded_weight, 红色=0.0
	DegradedWeight float64 `yaml:"degraded_weight" json:"degraded_weight"`

	// 并发探测的最大 goroutine 数（默认 10）
	// - 不配置或 0: 使用默认值 10
	// - -1: 无限制，自动扩容到监测项数量
	// - >0: 硬上限，超过时监测项会排队等待执行
	MaxConcurrency int `yaml:"max_concurrency" json:"max_concurrency"`

	// 是否在单个周期内对探测进行错峰（默认 true）
	// 开启后会将监测项均匀分散在整个巡检周期内，避免流量突发
	StaggerProbes *bool `yaml:"stagger_probes,omitempty" json:"stagger_probes,omitempty"`

	// 是否启用并发查询（API 层优化，默认 false）
	// 开启后 /api/status 接口会使用 goroutine 并发查询多个监测项，显著降低响应时间
	// 注意：需要确保数据库连接池足够大（建议 max_open_conns >= 50）
	EnableConcurrentQuery bool `yaml:"enable_concurrent_query" json:"enable_concurrent_query"`

	// 并发查询时的最大并发度（默认 10，仅当 enable_concurrent_query=true 时生效）
	// 限制同时执行的数据库查询数量，防止连接池耗尽
	ConcurrentQueryLimit int `yaml:"concurrent_query_limit" json:"concurrent_query_limit"`

	// 是否启用批量查询（API 层优化，默认 false）
	// 开启后 /api/status 在 7d/30d 场景会优先使用批量查询，将 N 个监测项的 GetLatest+GetHistory 从 2N 次往返降为 2 次
	EnableBatchQuery bool `yaml:"enable_batch_query" json:"enable_batch_query"`

	// 是否启用 DB 侧时间轴聚合（默认 false）
	// 仅对 PostgreSQL 生效：将 7d/30d 的 timeline bucket 聚合下推到数据库，减少数据传输与应用层计算
	// 需要同时启用 enable_batch_query=true 才能生效
	EnableDBTimelineAgg bool `yaml:"enable_db_timeline_agg" json:"enable_db_timeline_agg"`

	// 批量查询最大 key 数（默认 300）
	// 注意：SQLite 场景下会自动回退到 249（因为参数上限 999，每 key 需要 4 个参数）
	BatchQueryMaxKeys int `yaml:"batch_query_max_keys" json:"batch_query_max_keys"`

	// API 响应缓存 TTL 配置（按 period 区分）
	// 默认值：90m/24h = 10s，7d/30d = 60s
	CacheTTL CacheTTLConfig `yaml:"cache_ttl" json:"cache_ttl"`

	// ===== 存储配置 =====

	// 存储配置
	Storage StorageConfig `yaml:"storage" json:"storage"`

	// HTTP 入口的可信代理边界（决定 c.ClientIP() 取值，进而决定限流与配额的计数主体）
	Server ServerConfig `yaml:"server" json:"server"`

	// 公开访问的基础 URL（用于 SEO、sitemap 等）
	// 默认: https://relaypulse.top
	// 可通过环境变量 MONITOR_PUBLIC_BASE_URL 覆盖
	PublicBaseURL string `yaml:"public_base_url" json:"public_base_url"`

	// 是否允许监测项使用私有网络 IP 作为探测目标（默认 false）
	// false 时在 validateFinal() 中输出告警，不阻断启动或热更新
	AllowPrivateNetworks bool `yaml:"allow_private_networks" json:"allow_private_networks"`

	// ===== Provider 策略配置 =====

	// 批量禁用的服务商列表（彻底停用，不探测、不存储、不展示）
	// 列表中的 provider 会自动继承 disabled=true 状态到对应的 monitors
	DisabledProviders []disabledProviderConfig `yaml:"disabled_providers" json:"disabled_providers"`

	// 批量隐藏的服务商列表
	// 列表中的 provider 会自动继承 hidden=true 状态到对应的 monitors
	// 用于临时下架整个服务商（如商家不配合整改）
	HiddenProviders []hiddenProviderConfig `yaml:"hidden_providers" json:"hidden_providers"`

	// ===== 功能开关 =====

	// 是否在公开列表/卡片中隐藏价格列。默认 false（显示价格列）。
	// 运行时配置：改 yaml 即触发热更新，前端通过 /api/status meta 拿到新值，
	// 不需要重建镜像或重启容器。
	HidePriceColumn bool `yaml:"hide_price_column" json:"hide_price_column"`

	// 是否在公开列表中隐藏「分类」筛选器（公益站/商业站）。默认 false（显示）。
	// 运行时配置：改 yaml 即触发热更新，前端通过 /api/status meta 拿到新值，
	// 不需要重建镜像或重启容器。
	// 注意这里隐藏的只是筛选入口，category 字段本身仍照常下发。
	// 开关打开时，前端把 URL 里残留的 ?category= 视为未选中（不再过滤数据），
	// 但**不删除该参数**——关掉开关后用户原来的选择自动恢复。
	HideCategoryFilter bool `yaml:"hide_category_filter" json:"hide_category_filter"`

	// 是否在公开列表中隐藏「模型厂商」筛选器。默认 false（显示）。
	// 运行时配置：改 yaml 即触发热更新，前端通过 /api/status meta 拿到新值，
	// 不需要重建镜像或重启容器。
	// 与 HideCategoryFilter 同款语义：隐藏的只是筛选入口——model_vendor 字段照常
	// 下发、**厂商列不受影响**（列是观察维度，筛选器是操作轴，绑死了以后想单独
	// 调就难拆）。开关打开时前端把 URL 里残留的 ?vendor= 视为未选中（不再过滤
	// 数据），但**不删除该参数**——关掉开关后用户原来的选择自动恢复。
	HideVendorFilter bool `yaml:"hide_vendor_filter" json:"hide_vendor_filter"`

	// 热板/冷板功能配置（默认禁用，保持向后兼容）
	// 启用后可通过 monitor.board 字段控制监测项归属
	Boards BoardsConfig `yaml:"boards" json:"boards"`

	// 是否对外暴露通道技术细节（probe_url, template_name）
	// 默认 true（保持向后兼容）
	// 设为 false 时，API 响应中将不包含这些字段
	ExposeChannelDetails *bool `yaml:"expose_channel_details,omitempty" json:"expose_channel_details,omitempty"`

	// provider 级通道技术细节暴露覆盖配置
	// 可针对特定 provider 覆盖全局 expose_channel_details 设置
	ChannelDetailsProviders []channelDetailsProviderConfig `yaml:"channel_details_providers,omitempty" json:"channel_details_providers,omitempty"`

	// 赞助商置顶配置
	// 用于在页面初始加载时置顶符合条件的赞助商监测项
	SponsorPin SponsorPinConfig `yaml:"sponsor_pin" json:"sponsor_pin"`

	// 服务商自助收录配置
	Onboarding OnboardingConfig `yaml:"onboarding" json:"onboarding"`

	// 变更请求配置
	ChangeRequests ChangeRequestConfig `yaml:"change_requests" json:"change_requests"`

	// 状态订阅通知（事件）配置
	Events EventsConfig `yaml:"events" json:"events"`

	// 公告通知配置（GitHub Discussions / Announcements 分类）
	Announcements AnnouncementsConfig `yaml:"announcements" json:"announcements"`

	// GitHub 通用配置（token/proxy/timeout）
	GitHub GitHubConfig `yaml:"github" json:"github"`

	// ===== 注解系统 =====

	// 是否返回 annotations[]（默认 false）
	// 仅控制 API 是否输出注解数组，不清空 category/sponsor_level/interval_ms 等事实字段
	EnableAnnotations bool `yaml:"enable_annotations" json:"enable_annotations"`

	// 统一注解规则
	// 规则按配置顺序应用；同 ID 后写覆盖前写
	// 每条规则先 remove 再 add，支持"删旧换新"场景
	AnnotationRules []AnnotationRule `yaml:"annotation_rules" json:"annotation_rules"`

	// ===== 监测项列表 =====

	Monitors []ServiceConfig `yaml:"monitors"`
}
