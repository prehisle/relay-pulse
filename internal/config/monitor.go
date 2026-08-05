package config

import (
	"time"
)

// ServiceConfig 单个服务监测配置
type ServiceConfig struct {
	Provider     string       `yaml:"provider" json:"provider"`
	ProviderName string       `yaml:"provider_name" json:"provider_name,omitempty"` // Provider 显示名称（可选，未配置时回退到 provider）
	ProviderSlug string       `yaml:"provider_slug,omitempty" json:"provider_slug"` // URL slug（可选，未配置时使用 provider 小写）
	ProviderURL  string       `yaml:"provider_url,omitempty" json:"provider_url"`   // 服务商官网链接（可选）
	Service      string       `yaml:"service" json:"service"`
	ServiceName  string       `yaml:"service_name" json:"service_name,omitempty"`   // Service 显示名称（可选，未配置时回退到 service）
	Category     string       `yaml:"category" json:"category"`                     // 分类：commercial（商业站）或 public（公益站）
	Sponsor      string       `yaml:"sponsor,omitempty" json:"sponsor"`             // 赞助者：提供 API Key 的个人或组织
	SponsorURL   string       `yaml:"sponsor_url,omitempty" json:"sponsor_url"`     // 赞助者链接（可选）
	SponsorLevel SponsorLevel `yaml:"sponsor_level,omitempty" json:"sponsor_level"` // 赞助等级：public/signal/pulse/beacon/backbone/core（可选，按通道赞助）
	KeyType      string       `yaml:"key_type" json:"key_type,omitempty"`           // API Key 类型：official/user（空值按 official 处理）
	PriceMin     *float64     `yaml:"price_min,omitempty" json:"price_min"`         // 参考倍率下限（可选，如 0.05）
	PriceMax     *float64     `yaml:"price_max,omitempty" json:"price_max"`         // 参考倍率（可选，如 0.2）
	Annotations  []Annotation `yaml:"-" json:"annotations,omitempty"`               // 统一注解（系统派生 + annotation_rules）
	Channel      string       `yaml:"channel" json:"channel"`                       // 业务通道标识（如 "vip-channel"），用于分类和过滤
	Model        string       `yaml:"model" json:"model,omitempty"`                 // 模型系列名（展示/DB 键；可由 template 提供，config 可覆盖）
	ModelID      string       `yaml:"model_id,omitempty" json:"model_id,omitempty"` // 监测行稳定唯一 id（md_<uuidv4>），系统生成、不可变；与 Model 一样不参与父子继承。Phase 1 起作内部存储键
	ChannelID    string       `yaml:"-" json:"-"`                                   // 运行时注入：所属文件 channel_id 下沉到行（loadMonitorsDir），不持久化，经 MonitorResult.channel_id 暴露
	RequestModel string       `yaml:"request_model,omitempty" json:"-"`             // 实际请求模型 ID（注入 {{MODEL}}/{{REQUEST_MODEL}}，为空时回退 Model）
	// ModelVendor 模型厂商受控 code（词表见 internal/modelvendor）。与 Model/RequestModel 一样
	// 可由 template 提供、config 行级覆盖；与 RequestModel 一样参与父子继承——同一通道的各 model
	// 层必属同一厂商（「一个通道一个厂商」不变量，见 validateModelVendors）。
	// 注意它与 Model 的继承语义相反：Model 是父子的区分字段故不继承，vendor 是通道内共享字段。
	ModelVendor string `yaml:"model_vendor,omitempty" json:"model_vendor,omitempty"`
	Parent      string `yaml:"parent" json:"parent,omitempty"`             // 父通道引用，格式 provider/service/channel
	ChannelName string `yaml:"channel_name" json:"channel_name,omitempty"` // Channel 显示名称（可选，未配置时回退到 channel）
	ListedSince string `yaml:"listed_since,omitempty" json:"listed_since"` // 收录日期（可选，格式 "2006-01-02"），用于计算收录天数
	ExpiresAt   string `yaml:"expires_at" json:"expires_at,omitempty"`     // 到期日期（可选，格式 "2006-01-02"），过期后赞助等级降为 pulse（板块由可用率决定，不随到期变动）
	// Template 引用 templates/<name>.json 模板，定义完整的请求方式（url/method/headers/body/response）
	Template string `yaml:"template" json:"template,omitempty"`

	// BaseURL 服务商基础地址（如 "https://api.88code.com"），模板通过 {{BASE_URL}} 引用
	BaseURL string `yaml:"base_url,omitempty" json:"base_url"`

	// SkipURLValidation 跳过该监测项的私网 IP 告警校验（内网/自建探测目标场景）
	SkipURLValidation bool `yaml:"skip_url_validation,omitempty" json:"-"`

	// UserIDRefreshMinutes user_id 刷新间隔（分钟），0 = 使用确定性固定值
	UserIDRefreshMinutes int `yaml:"user_id_refresh_minutes" json:"user_id_refresh_minutes,omitempty"`

	// URLPattern URL 模式（含 {{BASE_URL}} 等占位符）
	// 通常由 ResolveTemplates 从模板填充；也可在 config 中显式设置以覆盖模板
	// 在探测期通过 InjectVariables 替换为最终 URL
	URLPattern string `yaml:"url_pattern,omitempty" json:"-"`

	Method  string            `yaml:"method,omitempty" json:"method"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers"`
	Body    string            `yaml:"body,omitempty" json:"body"`

	// SuccessContains 可选：响应体需包含的关键字，用于判定请求语义是否成功
	SuccessContains string `yaml:"success_contains,omitempty" json:"success_contains"`

	// EnvVarName 可选：自定义环境变量名（用于解决channel名称冲突）
	// 如果指定，则使用此名称覆盖 APIKey，否则使用自动生成的 MONITOR_{PROVIDER}_{SERVICE}_{CHANNEL}_API_KEY
	EnvVarName string `yaml:"env_var_name,omitempty" json:"-"`

	// 自定义巡检间隔（可选，留空则使用全局 interval）
	// 支持 Go duration 格式，例如 "30s"、"1m"、"5m"
	// 商务/赞助等级可按需配置更短间隔
	Interval string `yaml:"interval,omitempty" json:"interval"`

	// 彻底停用配置：不探测、不存储、不展示
	// Disabled 为 true 时，调度器不会创建任务，API 不返回，探测结果不写库
	Disabled       bool   `yaml:"disabled,omitempty" json:"disabled"`
	DisabledReason string `yaml:"disabled_reason,omitempty" json:"disabled_reason"` // 停用原因（可选）

	// 临时下架配置：隐藏但继续探测，用于商家整改期间
	// Hidden 为 true 时，API 不返回该监测项，但调度器继续探测并存储结果
	Hidden       bool   `yaml:"hidden,omitempty" json:"hidden"`
	HiddenReason string `yaml:"hidden_reason,omitempty" json:"hidden_reason"` // 下架原因（可选）

	// 热板/备板/冷板配置：冷板项停止探测，仅展示历史数据（需 boards.enabled=true）
	// Board 可选值：空/"hot"（默认主板）、"secondary"（备板）、"cold"（冷板）。
	// 配置板位是自动移板的"锚点/天花板"：board=secondary 不会被自动升 hot（详见 BoardAutoMoveConfig）。
	Board      string `yaml:"board,omitempty" json:"board"`
	ColdReason string `yaml:"cold_reason" json:"cold_reason,omitempty"` // 冷板原因（可选）

	// 质量移板运行时字段（yaml:"-" 不持久化到 monitors.d，由 automove 在运行时注入）。
	BoardReason       string `yaml:"-" json:"board_reason,omitempty"`        // 移板机器码（如 "quality_hardfail"），前端本地化；运行时注入
	BoardReasonModels string `yaml:"-" json:"board_reason_models,omitempty"` // 触发质量移板的模型名（逗号连接）；运行时注入

	// AutoColdExempt 手动解除自动冷板。
	// 设为 true 时会清除 runtime cold override，并在保持为 true 期间不再自动移入冷板。
	AutoColdExempt bool `yaml:"auto_cold_exempt" json:"auto_cold_exempt,omitempty"`

	// AutoMoveExempt 豁免所有基于可用率的自动移板（hot↔secondary↔cold）。
	// 不影响 expires_at 到期降级逻辑。
	// 设为 true 后，已有的 availability-based override 也会被立即清除。
	AutoMoveExempt bool `yaml:"auto_move_exempt" json:"auto_move_exempt,omitempty"`

	// 通道级慢请求阈值（可选，覆盖模板和全局 slow_latency）
	// 支持 Go duration 格式，例如 "5s"、"15s"
	// 优先级：monitor > template.probe > global
	SlowLatency string `yaml:"slow_latency,omitempty" json:"slow_latency"`

	// 解析后的"慢请求"阈值，用于黄灯判定
	SlowLatencyDuration time.Duration `yaml:"-" json:"-"`

	// 通道级超时时间（可选，覆盖模板和全局 timeout）
	// 支持 Go duration 格式，例如 "10s"、"30s"
	// 优先级：monitor > template.probe > global
	Timeout string `yaml:"timeout,omitempty" json:"timeout"`

	// 解析后的超时时间
	TimeoutDuration time.Duration `yaml:"-" json:"-"`

	// 通道级重试次数（可选，覆盖模板和全局 retry）
	// 0 表示不重试；该字段表示"额外重试次数"，不包含首次尝试
	// 使用 *int 以区分"未设置(nil)"和"显式设置为 0"
	// 优先级：monitor > template.probe > global
	Retry *int `yaml:"retry" json:"retry,omitempty"`

	// 解析后的重试次数（内部使用）
	RetryCount int `yaml:"-" json:"-"`

	// 通道级退避基准间隔（可选，覆盖模板和全局 retry_base_delay）
	// 支持 Go duration 格式，例如 "200ms"、"500ms"
	RetryBaseDelay string `yaml:"retry_base_delay" json:"retry_base_delay,omitempty"`

	// 解析后的退避基准间隔（内部使用）
	RetryBaseDelayDuration time.Duration `yaml:"-" json:"-"`

	// 通道级退避最大间隔（可选，覆盖模板和全局 retry_max_delay）
	// 支持 Go duration 格式，例如 "2s"、"5s"
	RetryMaxDelay string `yaml:"retry_max_delay" json:"retry_max_delay,omitempty"`

	// 解析后的退避最大间隔（内部使用）
	RetryMaxDelayDuration time.Duration `yaml:"-" json:"-"`

	// 通道级抖动比例（可选，覆盖模板和全局 retry_jitter）
	// 取值范围 0-1，0 表示无抖动
	// 使用 *float64 以区分"未设置(nil)"和"显式设置为 0"
	RetryJitter *float64 `yaml:"retry_jitter" json:"retry_jitter,omitempty"`

	// 解析后的抖动比例（内部使用）
	RetryJitterValue float64 `yaml:"-" json:"-"`

	// 解析后的巡检间隔（可选，为空时使用全局 interval）
	IntervalDuration time.Duration `yaml:"-" json:"-"`

	// Proxy 可选：该监测项使用的代理地址
	// 支持格式：
	//   - HTTP/HTTPS 代理：http://host:port, http://user:pass@host:port
	//   - SOCKS5 代理：socks5://host:port, socks5://user:pass@host:port
	//   - socks:// 是 socks5:// 的别名
	// 注意：SOCKS5 代理必须指定端口
	// 不配置时使用系统环境变量代理（HTTP_PROXY/HTTPS_PROXY）
	Proxy string `yaml:"proxy" json:"proxy,omitempty"` // admin monitor CRUD 需要 round-trip；公共 API 不直接序列化 ServiceConfig

	APIKey string `yaml:"api_key" json:"api_key,omitempty"` // admin monitor CRUD 需要 round-trip；公共 API 不直接序列化 ServiceConfig
}

// disabledProviderConfig 批量禁用指定 provider 的配置
// 用于彻底停用某个服务商的所有监测项（不探测、不存储、不展示）
type disabledProviderConfig struct {
	Provider string `yaml:"provider" json:"provider"` // provider 名称，需与 monitors 中的 provider 完全匹配
	Reason   string `yaml:"reason" json:"reason"`     // 停用原因（可选）
}

// hiddenProviderConfig 批量隐藏指定 provider 的配置
// 用于临时下架某个服务商的所有监测项
type hiddenProviderConfig struct {
	Provider string `yaml:"provider" json:"provider"` // provider 名称，需与 monitors 中的 provider 完全匹配
	Reason   string `yaml:"reason" json:"reason"`     // 下架原因（可选）
}

// channelDetailsProviderConfig provider 级通道技术细节暴露配置
// 用于针对特定 provider 覆盖全局 expose_channel_details 设置
type channelDetailsProviderConfig struct {
	Provider string `yaml:"provider" json:"provider"` // provider 名称，匹配时忽略大小写和首尾空格
	Expose   bool   `yaml:"expose" json:"expose"`     // 是否暴露该 provider 的通道技术细节
}
