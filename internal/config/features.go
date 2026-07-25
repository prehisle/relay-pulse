package config

import "time"

// EventsConfig 状态订阅通知（事件）配置
type EventsConfig struct {
	// 是否启用事件功能（默认禁用）
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 事件模式："model"（默认，按模型独立触发）或 "channel"（按通道整体判定）
	// - model: 每个模型独立维护状态机，独立触发 DOWN/UP 事件
	// - channel: 按通道整体判定，任意 N 个模型 DOWN 触发通道 DOWN，所有模型恢复触发通道 UP
	Mode string `yaml:"mode" json:"mode"`

	// 连续 N 次不可用触发 DOWN 事件（默认 2，mode=model 时使用）
	DownThreshold int `yaml:"down_threshold" json:"down_threshold"`

	// 连续 N 次可用触发 UP 事件（默认 1，mode=model 时使用）
	UpThreshold int `yaml:"up_threshold" json:"up_threshold"`

	// 通道级 DOWN 阈值：N 个模型 DOWN 触发通道 DOWN（默认 1，mode=channel 时使用）
	ChannelDownThreshold int `yaml:"channel_down_threshold" json:"channel_down_threshold"`

	// 通道级计数模式（mode=channel 时使用）：
	// - "recompute"（默认）：每次基于活跃模型集合重新计算 down_count/known_count，解决迁移/模型删除等边界问题
	// - "incremental"：增量维护计数，性能最优，适合大规模稳定运行的系统
	ChannelCountMode string `yaml:"channel_count_mode" json:"channel_count_mode"`

	// API 访问令牌（可选，空值表示无鉴权）
	// 配置后需要在请求头中携带 Authorization: Bearer <token>
	APIToken string `yaml:"api_token" json:"-"`
}

// SponsorPinConfig 赞助通道置顶配置
// 用于在页面初始加载时置顶符合条件的赞助通道
type SponsorPinConfig struct {
	// 是否启用置顶功能（默认 true）
	Enabled *bool `yaml:"enabled" json:"enabled"`

	// 最多置顶数量（默认 10；0 或负数回退为默认值，禁用置顶用 enabled: false）
	MaxPinned int `yaml:"max_pinned" json:"max_pinned"`

	// 最低可用率要求（默认 95.0，百分比 0-100）
	MinUptime float64 `yaml:"min_uptime" json:"min_uptime"`

	// 最低赞助级别（默认 "beacon"，可选 public/signal/pulse/beacon/backbone/core）
	MinLevel SponsorLevel `yaml:"min_level" json:"min_level"`
}

// IsEnabled 返回是否启用置顶功能
func (c *SponsorPinConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true // 默认启用
	}
	return *c.Enabled
}

// BoardAutoMoveConfig 基于 7 天可用率自动移板的配置。
// 语义：配置板位（board 字段）是"锚点/天花板"，自动移板只在锚点及以下浮动，绝不向上越板——
// 配 board=hot 的通道可在 hot↔secondary 间双向迟滞、并可冷板；配 board=secondary 的通道
// 永不自动升 hot，只会在可用率过低时冷板。自动冷板为粘性状态，需人工设置 auto_cold_exempt 解除。
type BoardAutoMoveConfig struct {
	// 是否启用自动移板（默认 false）
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 冷板阈值：可用率低于此值 → cold（默认 10.0，百分比 0-100）
	// 自动冷板是 sticky 的，不会自动恢复，需通过 auto_cold_exempt 手动解除
	ThresholdCold float64 `yaml:"threshold_cold" json:"threshold_cold"`

	// 降级阈值：board=hot 的通道可用率低于此值 → secondary（默认 50.0，百分比 0-100）
	ThresholdDown float64 `yaml:"threshold_down" json:"threshold_down"`

	// 恢复阈值：board=hot 被降级到 secondary 后，可用率回到此值 → 升回 hot（默认 55.0，高于 down 以防抖）。
	// 仅作用于配置板位为 hot 的通道的恢复；board=secondary 的通道不受此阈值影响（锚点不向上越板）。
	ThresholdUp float64 `yaml:"threshold_up" json:"threshold_up"`

	// 评估间隔（默认 "30m"）
	CheckInterval string `yaml:"check_interval" json:"check_interval"`

	// 最少探测次数，不足则不判断（新服务商保护，默认 10）
	MinProbes int `yaml:"min_probes" json:"min_probes"`

	// 解析后的运行时字段
	CheckIntervalDuration time.Duration `yaml:"-" json:"-"`
}

// OnboardingConfig 服务商自助收录配置
type OnboardingConfig struct {
	// 是否启用自助收录功能（默认禁用）
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 管理后台认证令牌（Bearer token）
	AdminToken string `yaml:"admin_token" json:"-"`

	// API Key 加密密钥（32 字节 hex 或环境变量 ONBOARDING_ENCRYPTION_KEY）
	EncryptionKey string `yaml:"encryption_key" json:"-"`

	// test proof HMAC 签名密钥
	ProofSecret string `yaml:"proof_secret" json:"-"`

	// test proof 有效期（默认 "5m"）
	ProofTTL string `yaml:"proof_ttl" json:"-"`

	// 解析后的 proof 有效期（内部使用）
	ProofTTLDuration time.Duration `yaml:"-" json:"-"`

	// 每 IP 每天最大提交数（默认 5）
	MaxPerIPPerDay int `yaml:"max_per_ip_per_day" json:"-"`

	// 联系方式（展示给用户，如 "QQ:18058344"）
	ContactInfo string `yaml:"contact_info" json:"contact_info"`
}

// ChangeRequestConfig 变更请求功能配置（独立于 Onboarding，共享 admin_token 和 encryption_key）
type ChangeRequestConfig struct {
	// 是否启用变更请求功能（默认禁用）
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 每 IP 每天最大提交数（默认 3）
	MaxPerIPPerDay int `yaml:"max_per_ip_per_day" json:"-"`

	// 已公开泄露 API Key 的拒绝名单文件名（主配置目录下的直接子文件，空值=功能关闭）。
	// 文件格式与哈希空间见 revoked_keys.go。
	RevokedKeyFile string `yaml:"revoked_key_file" json:"-"`

	// 名单预期条目数。必须与文件去重后的条目数一致，否则整次配置加载失败——
	// 这是唯一能发现「名单被部分写入/截断」的手段（截断后的前缀仍是合法摘要行）。
	RevokedKeyCount int `yaml:"revoked_key_count" json:"-"`

	// 加载后的运行时名单（sha256 hex 集合）。由 loadRevokedKeyFile 填充，不参与 YAML/JSON 序列化。
	// 加载完成后只读，可在热更新的新旧配置间安全共享。
	RevokedKeySHA256 map[string]struct{} `yaml:"-" json:"-"`
}

// BoardsConfig 热板/冷板功能配置
// 用于将监测项分为热板（正常监测）和冷板（停止监测，仅展示历史）
type BoardsConfig struct {
	// 是否启用热板/冷板功能（默认 false，保持向后兼容）
	Enabled bool `yaml:"enabled" json:"enabled"`

	// 自动移板配置（基于 7 天可用率，在配置板位及以下浮动；详见 BoardAutoMoveConfig）
	AutoMove BoardAutoMoveConfig `yaml:"auto_move" json:"auto_move"`
}
