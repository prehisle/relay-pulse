package storage

import "time"

// SubStatus 细分状态码（字符串形式，便于扩展和前后端统一）
type SubStatus string

const (
	SubStatusNone            SubStatus = ""                   // 默认值（绿色或灰色无需细分）
	SubStatusSlowLatency     SubStatus = "slow_latency"       // 响应慢
	SubStatusRateLimit       SubStatus = "rate_limit"         // 限流（429）
	SubStatusServerError     SubStatus = "server_error"       // 服务器错误（5xx）
	SubStatusClientError     SubStatus = "client_error"       // 客户端错误（4xx）
	SubStatusAuthError       SubStatus = "auth_error"         // 认证/权限失败（401/403）
	SubStatusInvalidRequest  SubStatus = "invalid_request"    // 请求参数错误（400）
	SubStatusNetworkError    SubStatus = "network_error"      // 网络错误（连接失败）
	SubStatusContentMismatch SubStatus = "content_mismatch"   // 内容校验失败
)

// ProbeRecord 探测记录
type ProbeRecord struct {
	ID        int64
	Provider  string
	Service   string
	Channel   string    // 业务通道标识
	Status    int       // 1=绿, 0=红, 2=黄
	SubStatus SubStatus // 细分状态（黄色/红色原因）
	Latency   int       // ms
	Timestamp int64     // Unix时间戳
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
	RateLimit   int `json:"rate_limit"`   // 黄色-限流次数

	// 细分统计（红色不可用细分）
	ServerError     int `json:"server_error"`     // 红色-服务器错误次数（5xx）
	ClientError     int `json:"client_error"`     // 红色-客户端错误次数（4xx）
	AuthError       int `json:"auth_error"`       // 红色-认证失败次数（401/403）
	InvalidRequest  int `json:"invalid_request"`  // 红色-请求参数错误次数（400）
	NetworkError    int `json:"network_error"`    // 红色-连接失败次数
	ContentMismatch int `json:"content_mismatch"` // 红色-内容校验失败次数
}

// ChannelMigrationMapping 表示 provider/service 对应的目标 channel
type ChannelMigrationMapping struct {
	Provider string
	Service  string
	Channel  string
}

// Storage 存储接口
type Storage interface {
	// Init 初始化存储
	Init() error

	// Close 关闭存储
	Close() error

	// SaveRecord 保存探测记录
	SaveRecord(record *ProbeRecord) error

	// GetLatest 获取最新记录
	GetLatest(provider, service, channel string) (*ProbeRecord, error)

	// GetHistory 获取历史记录（时间范围）
	GetHistory(provider, service, channel string, since time.Time) ([]*ProbeRecord, error)

	// CleanOldRecords 清理旧记录（保留最近N天）
	CleanOldRecords(days int) error

	// MigrateChannelData 将 channel 为空的历史记录迁移到最新配置
	MigrateChannelData(mappings []ChannelMigrationMapping) error
}
