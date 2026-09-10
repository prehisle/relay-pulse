package automove

import (
	"time"

	"monitor/internal/storage"
)

const (
	// availabilityWindow 是可用率评估回看的时间窗口（7 天）。
	availabilityWindow = 7 * 24 * time.Hour

	availabilityWindowSeconds = int64(availabilityWindow / time.Second)
)

// CalculateAvailability 根据探测记录计算 7 天加权可用率百分比。
//
// 口径：窗口内所有探测记录进同一个分母，按状态权重累加分子，
// 即 `Σ状态权重 / 探测次数 × 100`——与 WebUI 的通道可用率、
// 与 docs/user/config.md 里 degraded_weight 的公式逐字同款。
//
// ⚠️ 刻意**不**按 24h 分桶再对桶求等权平均：那样会让样本稀疏的桶
// （典型是尚未跑满的当天）与完整一天等权，实测可造成 10pp 以上偏差。
//
// endTime 应使用 alignToNextUTCDay() 对齐到下一天 00:00 UTC，
// 与 api/query.go 中 7d period 的 day 对齐保持一致。
//
// records 允许是同一通道下多个模型行的合并结果——通道可用率是
// 「该通道所有模型的可用探测数 / 总探测数」，调用方负责合并。
//
// 返回值：
//   - availability: 0-100 的百分比，无有效记录时返回 -1
//   - total: 落在窗口内并参与计算的探测记录总数
func CalculateAvailability(records []*storage.ProbeRecord, endTime time.Time, degradedWeight float64) (availability float64, total int) {
	if len(records) == 0 {
		return -1, 0
	}

	endUnix := endTime.UTC().Unix()

	var weighted float64

	for _, r := range records {
		if r == nil {
			continue
		}
		age := endUnix - r.Timestamp
		if age < 0 {
			continue // 未来记录，跳过
		}
		if age >= availabilityWindowSeconds {
			continue // 窗口外记录，跳过
		}
		weighted += statusWeight(r.Status, degradedWeight)
		total++
	}

	if total == 0 {
		return -1, 0
	}

	return (weighted / float64(total)) * 100, total
}

// statusWeight 返回探测状态对应的可用率权重。
func statusWeight(status int, degradedWeight float64) float64 {
	switch status {
	case 1:
		return 1.0
	case 2:
		return degradedWeight
	default:
		return 0.0
	}
}
