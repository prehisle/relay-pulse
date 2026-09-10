package storage

import (
	"context"
	"io"
	"time"
)

// SubStatus 细分状态码（字符串形式，便于扩展和前后端统一）
type SubStatus string

const (
	SubStatusNone            SubStatus = ""                 // 默认值（绿色或灰色无需细分）
	SubStatusSlowLatency     SubStatus = "slow_latency"     // 响应慢
	SubStatusRateLimit       SubStatus = "rate_limit"       // 限流（429）
	SubStatusServerError     SubStatus = "server_error"     // 服务器错误（5xx）
	SubStatusClientError     SubStatus = "client_error"     // 客户端错误（4xx）
	SubStatusAuthError       SubStatus = "auth_error"       // 认证/权限失败（401/403）
	SubStatusInvalidRequest  SubStatus = "invalid_request"  // 请求参数错误（400）
	SubStatusNetworkError    SubStatus = "network_error"    // 网络错误（连接失败）
	SubStatusResponseTimeout SubStatus = "response_timeout" // 响应超时（连接成功但读取响应体超时）
	SubStatusContentMismatch SubStatus = "content_mismatch" // 内容校验失败
)

// ProbeRecord 探测记录
type ProbeRecord struct {
	ID          int64
	Provider    string
	Service     string
	Channel     string    // 业务通道标识
	Model       string    // 模型展示名（可为空，兼容旧数据）
	ModelID     string    // 稳定模型 id（md_<uuidv4>），为空表示尚未回填
	Status      int       // 1=绿, 0=红, 2=黄
	SubStatus   SubStatus // 细分状态（黄色/红色原因）
	HttpCode    int       // HTTP 状态码（0 表示非 HTTP 错误，如网络错误）
	Latency     int       // ms
	Timestamp   int64     // Unix时间戳
	ErrorDetail string    // 失败摘要（仅 status=0 时写入，形态见 monitor.buildFailureSnippet）
}

// TimePoint 时间轴数据点（用于前端展示）
type TimePoint struct {
	Time         string       `json:"time"`          // 格式化时间标签（如 "15:04" 或 "2006-01-02"）
	Timestamp    int64        `json:"timestamp"`     // Unix 时间戳（秒），用于前端精确时间计算
	Status       int          `json:"status"`        // 状态码：1=绿，0=红，2=黄，-1=缺失（bucket内最后一条记录）
	Latency      int          `json:"latency"`       // 平均延迟（毫秒）
	Availability float64      `json:"availability"`  // 可用率百分比（0-100），缺失时为 -1
	StatusCounts StatusCounts `json:"status_counts"` // 各状态计数
}

// StatusCounts 记录一个时间块内各状态出现次数
type StatusCounts struct {
	Available   int `json:"available"`   // 绿色（可用）次数
	Degraded    int `json:"degraded"`    // 黄色（波动/降级）次数
	Unavailable int `json:"unavailable"` // 红色（不可用）次数
	Missing     int `json:"missing"`     // 灰色（无数据/未配置）次数

	// 细分统计（黄色波动细分）
	SlowLatency int `json:"slow_latency"` // 黄色-响应慢次数
	RateLimit   int `json:"rate_limit"`   // 限流次数（HTTP 429，当前视为红色不可用）

	// 细分统计（红色不可用细分）
	ServerError     int `json:"server_error"`     // 红色-服务器错误次数（5xx）
	ClientError     int `json:"client_error"`     // 红色-客户端错误次数（4xx）
	AuthError       int `json:"auth_error"`       // 红色-认证失败次数（401/403）
	InvalidRequest  int `json:"invalid_request"`  // 红色-请求参数错误次数（400）
	NetworkError    int `json:"network_error"`    // 红色-连接失败次数
	ResponseTimeout int `json:"response_timeout"` // 红色-响应超时次数（连接成功但读取响应体超时）
	ContentMismatch int `json:"content_mismatch"` // 红色-内容校验失败次数

	// HTTP 错误码细分统计
	// key: SubStatus 类型（如 "server_error", "client_error"）
	// value: 错误码 -> 出现次数 的映射
	HttpCodeBreakdown map[string]map[int]int `json:"http_code_breakdown,omitempty"`
}

// ChannelMigrationMapping 表示 provider/service 对应的目标 channel
type ChannelMigrationMapping struct {
	Provider string
	Service  string
	Channel  string
}

// ModelIDMigrationMapping 把一个 (provider,service,channel,model) 业务键映射到稳定 model_id，
// 供启动期回填 legacy probe_history 行。
type ModelIDMigrationMapping struct {
	Provider string
	Service  string
	Channel  string
	Model    string
	ModelID  string
}

// MonitorKey 监测项唯一键（provider/service/channel/model）
// 用于批量查询时作为 map 的 key，避免字符串拼接的歧义和冲突
type MonitorKey struct {
	Provider string
	Service  string
	Channel  string
	Model    string
}

// ===== 自动移板 override 持久化相关类型 =====

// MonitorOverrideRecord 自动移板 runtime override 的持久化记录。
type MonitorOverrideRecord struct {
	Key          MonitorKey
	Board        string
	ColdReason   string
	SponsorLevel string // config.SponsorLevel 的字符串表示，避免 storage → config 依赖
	// —— 质量移板机器字段（Task 4）——
	BoardReason           string // 移板机器码，如 quality_hardfail
	QualityLatched        bool   // 质量驱动移板已闩锁
	QualityRecoveryCount  int    // 质量恢复评估计数
	QualityTriggerModels  string // 触发质量移板的模型名（逗号连接）
	QualityLastGeneration uint64 // 上次质量评估代次（存 int64/BIGINT，真实值不超 2^63）
	AvailabilityLatched   bool   // 可用率驱动移板已闩锁
	CreatedAt             int64
	UpdatedAt             int64
}

// ===== 状态订阅通知（事件）相关类型 =====

// EventType 事件类型
type EventType string

const (
	EventTypeDown EventType = "DOWN" // 可用 → 不可用
	EventTypeUp   EventType = "UP"   // 不可用 → 可用
)

// ServiceState 服务状态机持久化状态
// 用于追踪每个监测项的"稳定状态"和抖动计数器
type ServiceState struct {
	Provider string
	Service  string
	Channel  string
	Model    string

	// StableAvailable 稳定态可用性：-1=未初始化, 0=不可用, 1=可用
	StableAvailable int

	// StreakCount 当前连续次数（累计相同可用性方向的次数）
	StreakCount int

	// StreakStatus 连续状态方向：0=不可用, 1=可用
	StreakStatus int

	// LastRecordID 最后处理的探测记录 ID
	LastRecordID int64

	// LastTimestamp 最后更新时间戳（Unix 秒）
	LastTimestamp int64
}

// ChannelState 通道级状态机持久化状态
// 用于 events.mode=channel 时追踪通道整体的可用性状态
type ChannelState struct {
	Provider string
	Service  string
	Channel  string

	// StableAvailable 稳定态可用性：-1=未初始化, 0=不可用, 1=可用
	StableAvailable int

	// DownCount 当前 DOWN 的模型数
	DownCount int

	// KnownCount 已初始化状态的模型数
	KnownCount int

	// LastRecordID 最后处理的探测记录 ID
	LastRecordID int64

	// LastTimestamp 最后更新时间戳（Unix 秒）
	LastTimestamp int64
}

// StatusEvent 状态变更事件
type StatusEvent struct {
	ID       int64
	Provider string
	Service  string
	Channel  string
	Model    string

	// EventType 事件类型（DOWN/UP）
	EventType EventType

	// FromStatus 变更前状态码（0/1/2）
	FromStatus int

	// ToStatus 变更后状态码（0/1/2）
	ToStatus int

	// TriggerRecordID 触发该事件的探测记录 ID
	TriggerRecordID int64

	// ObservedAt 探测时间（来自 ProbeRecord.Timestamp）
	ObservedAt int64

	// CreatedAt 事件创建时间（Unix 秒）
	CreatedAt int64

	// Meta 元数据（JSON 格式，包含 http_code, latency, sub_status 等）
	Meta map[string]any
}

// EventFilters 事件查询过滤器
type EventFilters struct {
	Provider string      // 按 provider 过滤（可选）
	Service  string      // 按 service 过滤（可选）
	Channel  string      // 按 channel 过滤（可选）
	Types    []EventType // 按事件类型过滤（可选，如 ["DOWN", "UP"]）
}

// ProbeHistoryKey 是 probe_history 的稳定身份键（model_id 维度）。
// ModelID 是查询/分桶键；Provider/Service/Channel/Model 仅供日志与返回 map 对齐当前展示名。
type ProbeHistoryKey struct {
	ModelID  string
	Provider string
	Service  string
	Channel  string
	Model    string
}

// ===== 领域子接口 =====

// RecordStorage 探测记录的读写操作
//
// 索引依赖说明：
//   - GetLatest 和 GetHistory 的性能依赖于 idx_probe_history_pscm_ts_cover 覆盖索引
//   - 两个方法都必须包含完整的 (provider, service, channel, model) 等值条件
//   - GetLatestByModelID / GetHistoryByModelID / GetHistoryWithLimitByModelID 依赖
//     idx_probe_history_mid_ts 部分索引（WHERE model_id IS NOT NULL）
//   - ⚠️ 如果新增不带 channel/model 参数的查询方法，需要重新评估索引策略
type RecordStorage interface {
	// SaveRecord 保存探测记录
	SaveRecord(record *ProbeRecord) error

	// GetLatest 获取最新记录
	GetLatest(provider, service, channel, model string) (*ProbeRecord, error)

	// GetHistory 获取历史记录（时间范围）
	GetHistory(provider, service, channel, model string, since time.Time) ([]*ProbeRecord, error)

	// GetHistoryWithLimit 获取指定 PSCM 在 since 之后的最近 limit 条记录，按 timestamp DESC + id DESC 返回。
	// 与 GetHistory 不同，该方法返回值包含 ErrorDetail 字段，用于管理后台日志明细展示。
	// limit <= 0 时回退到 200；上限 clamp 由上层 handler 负责。
	GetHistoryWithLimit(provider, service, channel, model string, since time.Time, limit int) ([]*ProbeRecord, error)

	// GetLatestByModelID 按 model_id 获取最新一条记录，跨展示名（model 字段）历史连续。
	// 返回 nil, nil 表示无记录。
	GetLatestByModelID(modelID string) (*ProbeRecord, error)

	// GetHistoryByModelID 按 model_id 获取 since 之后的历史记录（时间升序），跨展示名历史连续。
	GetHistoryByModelID(modelID string, since time.Time) ([]*ProbeRecord, error)

	// GetHistoryWithLimitByModelID 按 model_id 获取 since 之后最近 limit 条记录（timestamp DESC + id DESC）。
	// 与 GetHistoryByModelID 不同，该方法返回值包含 ErrorDetail 字段，返回倒序（最新在前）。
	// limit <= 0 时回退到 200。
	GetHistoryWithLimitByModelID(modelID string, since time.Time, limit int) ([]*ProbeRecord, error)

	// GetLatestBatch 批量获取每个监测项的最新记录
	GetLatestBatch(keys []MonitorKey) (map[MonitorKey]*ProbeRecord, error)

	// GetHistoryBatch 批量获取多个监测项的历史记录（时间范围）
	GetHistoryBatch(keys []MonitorKey, since time.Time) (map[MonitorKey][]*ProbeRecord, error)

	// GetLatestBatchByModelID 按 model_id 批量获取每个监测项的最新记录，跨展示名历史连续。
	// 返回 map 以入参 ProbeHistoryKey 为键（通过 model_id 反查命中），
	// 绝不用 DB 行的展示名列重建 key——改名后 DB 行 model 是新名，与 caller key 不等会静默丢失。
	GetLatestBatchByModelID(keys []ProbeHistoryKey) (map[ProbeHistoryKey]*ProbeRecord, error)

	// GetHistoryBatchByModelID 按 model_id 批量获取多个监测项的历史记录（时间范围），跨展示名历史连续。
	// 返回 map 同样以入参 ProbeHistoryKey 为键（语义见 GetLatestBatchByModelID）。
	GetHistoryBatchByModelID(keys []ProbeHistoryKey, since time.Time) (map[ProbeHistoryKey][]*ProbeRecord, error)
}

// EventStorage 状态变更检测的持久化操作
type EventStorage interface {
	// GetServiceState 获取服务状态机持久化状态
	// 返回 nil, nil 表示该监测项尚未初始化状态
	GetServiceState(provider, service, channel, model string) (*ServiceState, error)

	// UpsertServiceState 写入或更新服务状态机持久化状态
	UpsertServiceState(state *ServiceState) error

	// GetChannelState 获取通道级状态机持久化状态
	// 返回 nil, nil 表示该通道尚未初始化状态
	GetChannelState(provider, service, channel string) (*ChannelState, error)

	// UpsertChannelState 写入或更新通道级状态机持久化状态
	UpsertChannelState(state *ChannelState) error

	// GetModelStatesForChannel 获取通道下所有模型的状态
	GetModelStatesForChannel(provider, service, channel string) ([]*ServiceState, error)

	// SaveStatusEvent 保存状态变更事件
	// 使用唯一约束确保幂等
	SaveStatusEvent(event *StatusEvent) error

	// GetStatusEvents 查询状态变更事件列表（游标分页）
	GetStatusEvents(sinceID int64, limit int, filters *EventFilters) ([]*StatusEvent, error)

	// GetLatestEventID 获取最新事件 ID（用于客户端初始化游标）
	GetLatestEventID() (int64, error)
}

// RetentionStorage 历史数据清理操作
type RetentionStorage interface {
	// PurgeOldRecords 清理指定时间之前的历史记录
	// 调用方负责循环调用直到无更多数据
	PurgeOldRecords(ctx context.Context, before time.Time, batchSize int) (deleted int64, err error)
}

// MigrationStorage 数据迁移操作（一次性/运维场景）
type MigrationStorage interface {
	// MigrateChannelData 将 channel 为空的历史记录迁移到最新配置
	MigrateChannelData(mappings []ChannelMigrationMapping) error

	// BackfillProbeHistoryModelIDs 按映射把 model_id IS NULL 的历史行回填为对应 model_id（分批、幂等）。
	// 若同一 (provider,service,channel,model) 映射到多个不同 model_id（歧义），在写库前返回错误、不做任何写入。
	BackfillProbeHistoryModelIDs(mappings []ModelIDMigrationMapping) error
}

// OverrideStorage 自动移板 runtime override 的持久化操作。
// 为可选接口：automove.Service 在运行时检测存储实现是否支持，可用时自动启用持久化。
type OverrideStorage interface {
	// ListMonitorOverrides 加载全部 override 快照
	ListMonitorOverrides() ([]MonitorOverrideRecord, error)

	// ReplaceMonitorOverrides 原子替换全部 override 快照（DELETE ALL + INSERT ALL 在事务中）
	ReplaceMonitorOverrides(records []MonitorOverrideRecord) error
}

// Storage 完整存储接口，组合所有领域子接口
//
// 工厂方法返回此接口；消费方应尽量依赖更小的子接口。
// WithContext 返回 Storage 以保持现有调用链兼容。
type Storage interface {
	// Init 初始化存储
	Init() error

	// Close 关闭存储
	Close() error

	// Ping 检查存储连通性（用于就绪探针）
	Ping() error

	// WithContext 返回绑定指定 context 的存储实例
	WithContext(ctx context.Context) Storage

	RecordStorage
	MigrationStorage
	EventStorage
	RetentionStorage
}

// ===== DB 侧时间轴聚合相关类型 =====

// DailyTimeFilter 每日时段过滤器（UTC 时区）
//
// 说明：
// - StartMinutes/EndMinutes 的取值范围为 [0, 1440]（1440 表示 24:00）
// - CrossMidnight 为 true 表示跨午夜：如 22:00-04:00
// - 语义为左闭右开区间：[start, end)
type DailyTimeFilter struct {
	StartMinutes  int
	EndMinutes    int
	CrossMidnight bool
}

// AggBucketRow 表示单个监测项在某个 bucket 内的聚合结果（由数据库返回）
//
// 注意：
//   - BucketIndex 使用与 api.buildTimeline 完全一致的"从前往后"索引：
//     0 表示最旧 bucket，bucketCount-1 表示最新 bucket
//   - Total 为 bucket 内记录总数（已应用 timeFilter 过滤）
//   - LastStatus 为 bucket 内最新一条记录的状态（用于 TimePoint.Status）
//   - LatencySum/LatencyCount 为 status > 0 的延迟聚合（与 buildTimeline 一致）
//   - AllLatencySum/AllLatencyCount 为 latency > 0 的延迟聚合（用于"全不可用时"参考）
type AggBucketRow struct {
	BucketIndex     int
	Total           int
	LastStatus      int
	LatencySum      int64
	LatencyCount    int
	AllLatencySum   int64
	AllLatencyCount int
	StatusCounts    StatusCounts
}

// TimelineAggStorage 为"时间轴聚合下推到数据库"提供的可选能力接口
//
// 仅 PostgreSQL 实现；SQLite 不实现该接口，API 层会自动回退到原有逻辑。
type TimelineAggStorage interface {
	// GetTimelineAggBatch 批量获取多个监测项的时间轴 bucket 聚合结果（时间范围）
	//
	// since/endTime 由 API 的 parseTimeRange 计算得到：
	// - 仅聚合 (since, endTime] 的数据，严格排除 timestamp==since 的边界数据
	// - 聚合窗口由 bucketCount + bucketWindow 决定（与 api.determineBucketStrategy 一致）
	GetTimelineAggBatch(keys []MonitorKey, since, endTime time.Time, bucketCount int, bucketWindow time.Duration, timeFilter *DailyTimeFilter) (map[MonitorKey][]AggBucketRow, error)

	// GetTimelineAggBatchByModelID 按 model_id 批量获取时间轴 bucket 聚合结果（时间范围），跨展示名历史连续。
	// 语义与 GetTimelineAggBatch 完全一致，仅查询/分桶键从四元组改为 model_id；
	// 返回 map 以入参 ProbeHistoryKey 为键（通过 model_id 反查命中）。
	GetTimelineAggBatchByModelID(keys []ProbeHistoryKey, since, endTime time.Time, bucketCount int, bucketWindow time.Duration, timeFilter *DailyTimeFilter) (map[ProbeHistoryKey][]AggBucketRow, error)
}

// ArchiveStorage 为"历史数据归档"提供的可选能力接口
//
// 仅 PostgreSQL 实现（使用 COPY 协议高效导出）；SQLite 可选实现。
type ArchiveStorage interface {
	// ExportDayToWriter 导出指定日期范围的历史记录到 writer
	// dayStart: 日期开始时间戳（Unix 秒，包含）
	// dayEnd: 日期结束时间戳（Unix 秒，不包含）
	// w: 目标 writer（通常是 gzip.Writer）
	// 返回导出的行数和错误
	// 输出格式：CSV（包含表头），字段顺序与 ProbeRecord 一致
	ExportDayToWriter(ctx context.Context, dayStart, dayEnd int64, w io.Writer) (rowCount int64, err error)
}
