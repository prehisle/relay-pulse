package api

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// Handler API处理器
type Handler struct {
	storage storage.Storage
	config  *config.AppConfig
	cfgMu   sync.RWMutex // 保护config的并发访问
}

// NewHandler 创建处理器
func NewHandler(store storage.Storage, cfg *config.AppConfig) *Handler {
	return &Handler{
		storage: store,
		config:  cfg,
	}
}

// CurrentStatus API返回的当前状态（不暴露数据库主键）
type CurrentStatus struct {
	Status    int   `json:"status"`
	Latency   int   `json:"latency"`
	Timestamp int64 `json:"timestamp"`
}

// MonitorResult API返回结构
type MonitorResult struct {
	Provider    string              `json:"provider"`
	ProviderURL string              `json:"provider_url"` // 服务商官网链接
	Service     string              `json:"service"`
	Category    string              `json:"category"` // 分类：commercial（推广站）或 public（公益站）
	Sponsor     string              `json:"sponsor"`  // 赞助者
	SponsorURL  string              `json:"sponsor_url"` // 赞助者链接
	Channel     string              `json:"channel"`  // 业务通道标识
	Current     *CurrentStatus      `json:"current_status"`
	Timeline    []storage.TimePoint `json:"timeline"`
}

// GetStatus 获取监控状态
func (h *Handler) GetStatus(c *gin.Context) {
	// 参数解析
	period := c.DefaultQuery("period", "24h")
	qProvider := c.DefaultQuery("provider", "all")
	qService := c.DefaultQuery("service", "all")

	// 解析时间范围
	since, err := h.parsePeriod(period)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": fmt.Sprintf("无效的时间范围: %s", period),
		})
		return
	}

	// 获取配置副本（线程安全）
	h.cfgMu.RLock()
	monitors := h.config.Monitors
	degradedWeight := h.config.DegradedWeight
	h.cfgMu.RUnlock()

	var response []MonitorResult

	// 遍历配置中的监控项
	seen := make(map[string]bool)
	for _, task := range monitors {
		// 过滤
		if qProvider != "all" && qProvider != task.Provider {
			continue
		}
		if qService != "all" && qService != task.Service {
			continue
		}

		// 去重（使用 provider + service + channel 组合）
		key := task.Provider + "/" + task.Service + "/" + task.Channel
		if seen[key] {
			continue
		}
		seen[key] = true

		// 获取最新记录
		latest, err := h.storage.GetLatest(task.Provider, task.Service, task.Channel)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("查询失败: %v", err),
			})
			return
		}

		// 获取历史记录
		history, err := h.storage.GetHistory(task.Provider, task.Service, task.Channel, since)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": fmt.Sprintf("查询历史失败: %v", err),
			})
			return
		}

		// 转换为时间轴数据
		timeline := h.buildTimeline(history, period, degradedWeight)

		// 转换为API响应格式（不暴露数据库主键）
		var current *CurrentStatus
		if latest != nil {
			current = &CurrentStatus{
				Status:    latest.Status,
				Latency:   latest.Latency,
				Timestamp: latest.Timestamp,
			}
		}

		response = append(response, MonitorResult{
			Provider:    task.Provider,
			ProviderURL: task.ProviderURL,
			Service:     task.Service,
			Category:    task.Category,
			Sponsor:     task.Sponsor,
			SponsorURL:  task.SponsorURL,
			Channel:     task.Channel,
			Current:     current,
			Timeline:    timeline,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"meta": gin.H{
			"period": period,
			"count":  len(response),
		},
		"data": response,
	})
}

// parsePeriod 解析时间范围
func (h *Handler) parsePeriod(period string) (time.Time, error) {
	now := time.Now()

	switch period {
	case "24h", "1d":
		return now.Add(-24 * time.Hour), nil
	case "7d":
		return now.AddDate(0, 0, -7), nil
	case "30d":
		return now.AddDate(0, 0, -30), nil
	default:
		return time.Time{}, fmt.Errorf("不支持的时间范围")
	}
}

// bucketStats 用于聚合每个 bucket 内的探测数据
type bucketStats struct {
	total           int                  // 总探测次数
	weightedSuccess float64              // 累积成功权重（绿=1.0, 黄=degraded_weight, 红=0.0）
	latencySum      int64                // 延迟总和
	last            *storage.ProbeRecord // 最新一条记录
	statusCounts    storage.StatusCounts // 各状态计数
}

// buildTimeline 构建固定长度的时间轴，计算每个 bucket 的可用率和平均延迟
func (h *Handler) buildTimeline(records []*storage.ProbeRecord, period string, degradedWeight float64) []storage.TimePoint {
	// 根据 period 确定 bucket 策略
	bucketCount, bucketWindow, format := h.determineBucketStrategy(period)

	now := time.Now()

	// 初始化 buckets 和统计数据
	buckets := make([]storage.TimePoint, bucketCount)
	stats := make([]bucketStats, bucketCount)

	for i := 0; i < bucketCount; i++ {
		bucketTime := now.Add(-time.Duration(bucketCount-i) * bucketWindow)
		buckets[i] = storage.TimePoint{
			Time:         bucketTime.Format(format),
			Timestamp:    bucketTime.Unix(),
			Status:       -1,  // 缺失标记
			Latency:      0,
			Availability: -1,  // 缺失标记
		}
	}

	// 聚合每个 bucket 的探测结果
	for _, record := range records {
		t := time.Unix(record.Timestamp, 0)
		timeDiff := now.Sub(t)

		// 计算该记录属于哪个 bucket（从后往前）
		bucketIndex := int(timeDiff / bucketWindow)
		if bucketIndex < 0 {
			bucketIndex = 0
		}
		if bucketIndex >= bucketCount {
			continue // 超出范围，忽略
		}

		// 从前往后的索引
		actualIndex := bucketCount - 1 - bucketIndex
		if actualIndex < 0 || actualIndex >= bucketCount {
			continue
		}

		// 聚合统计
		stat := &stats[actualIndex]
		stat.total++
		stat.weightedSuccess += availabilityWeight(record.Status, degradedWeight)
		stat.latencySum += int64(record.Latency)
		incrementStatusCount(&stat.statusCounts, record.Status, record.SubStatus)

		// 保留最新记录
		if stat.last == nil || record.Timestamp > stat.last.Timestamp {
			stat.last = record
		}
	}

	// 根据聚合结果计算可用率和平均延迟
	for i := 0; i < bucketCount; i++ {
		stat := &stats[i]
		buckets[i].StatusCounts = stat.statusCounts
		if stat.total == 0 {
			continue
		}

		// 计算可用率（使用权重）
		buckets[i].Availability = (stat.weightedSuccess / float64(stat.total)) * 100

		// 计算平均延迟（四舍五入）
		avgLatency := float64(stat.latencySum) / float64(stat.total)
		buckets[i].Latency = int(avgLatency + 0.5)

		// 使用最新记录的状态和时间
		if stat.last != nil {
			buckets[i].Status = stat.last.Status
			buckets[i].Timestamp = stat.last.Timestamp
			buckets[i].Time = time.Unix(stat.last.Timestamp, 0).Format(format)
		}
	}

	return buckets
}

// determineBucketStrategy 根据 period 确定 bucket 数量、窗口大小和时间格式
func (h *Handler) determineBucketStrategy(period string) (count int, window time.Duration, format string) {
	switch period {
	case "24h", "1d":
		return 24, time.Hour, "15:04"
	case "7d":
		return 7, 24 * time.Hour, "2006-01-02"
	case "30d":
		return 30, 24 * time.Hour, "2006-01-02"
	default:
		return 24, time.Hour, "15:04"
	}
}

// UpdateConfig 更新配置（热更新时调用）
func (h *Handler) UpdateConfig(cfg *config.AppConfig) {
	h.cfgMu.Lock()
	h.config = cfg
	h.cfgMu.Unlock()
}

// availabilityWeight 根据状态码返回可用率权重
func availabilityWeight(status int, degradedWeight float64) float64 {
	switch status {
	case 1: // 绿色（正常）
		return 1.0
	case 2: // 黄色（降级：慢响应或429）
		return degradedWeight
	default: // 红色（不可用）或灰色（未配置）
		return 0.0
	}
}

// incrementStatusCount 统计每种状态及细分出现次数
func incrementStatusCount(counts *storage.StatusCounts, status int, subStatus storage.SubStatus) {
	switch status {
	case 1: // 绿色
		counts.Available++
	case 2: // 黄色
		counts.Degraded++
		// 黄色细分
		switch subStatus {
		case storage.SubStatusSlowLatency:
			counts.SlowLatency++
		case storage.SubStatusRateLimit:
			counts.RateLimit++
		}
	case 0: // 红色
		counts.Unavailable++
		// 红色细分
		switch subStatus {
		case storage.SubStatusServerError:
			counts.ServerError++
		case storage.SubStatusClientError:
			counts.ClientError++
		case storage.SubStatusNetworkError:
			counts.NetworkError++
		case storage.SubStatusContentMismatch:
			counts.ContentMismatch++
		}
	default: // 灰色（3）或其他
		counts.Missing++
	}
}
