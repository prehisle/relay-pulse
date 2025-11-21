package storage

import "time"

// ProbeRecord 探测记录
type ProbeRecord struct {
	ID        int64
	Provider  string
	Service   string
	Status    int   // 1=绿, 0=红, 2=黄
	Latency   int   // ms
	Timestamp int64 // Unix时间戳
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
	GetLatest(provider, service string) (*ProbeRecord, error)

	// GetHistory 获取历史记录（时间范围）
	GetHistory(provider, service string, since time.Time) ([]*ProbeRecord, error)

	// CleanOldRecords 清理旧记录（保留最近N天）
	CleanOldRecords(days int) error
}
