package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/storage"
)

// queryAndSerialize 查询数据库并序列化为 JSON（缓存 miss 时调用）
func (h *Handler) queryAndSerialize(ctx context.Context, period, align string, timeFilter *TimeFilter, qProvider, qService, qBoard string, includeHidden bool) ([]byte, error) {
	// 解析时间范围（支持对齐模式）
	startTime, endTime := h.parseTimeRange(period, align)

	// 获取配置副本（线程安全）
	h.cfgMu.RLock()
	monitors := h.config.Monitors
	degradedWeight := h.config.DegradedWeight
	enableConcurrent := h.config.EnableConcurrentQuery
	concurrentLimit := h.config.ConcurrentQueryLimit
	enableBatchQuery := h.config.EnableBatchQuery
	enableDBTimelineAgg := h.config.EnableDBTimelineAgg
	batchQueryMaxKeys := h.config.BatchQueryMaxKeys
	slowLatencyMs := int(h.config.SlowLatencyDuration / time.Millisecond)
	sponsorPin := h.config.SponsorPin
	enableAnnotations := h.config.EnableAnnotations
	boardsEnabled := h.config.Boards.Enabled
	annotationRules := h.config.AnnotationRules
	globalInterval := h.config.IntervalDuration
	hidePriceColumn := h.config.HidePriceColumn
	hideCategoryFilter := h.config.HideCategoryFilter
	h.cfgMu.RUnlock()

	// 应用自动移板 override（运行时覆盖 board/sponsor_level 字段，不修改配置；
	// board 或 sponsor 任一覆盖会连带重算 annotations，见 automove.ApplyOverrides 注释）
	monitors = h.applyBoardOverrides(monitors, annotationRules, globalInterval)

	// 构建 slug -> provider 映射（slug作为provider的路由别名）
	slugToProvider := make(map[string]string)
	for _, task := range monitors {
		normalizedProvider := strings.ToLower(strings.TrimSpace(task.Provider))
		slugToProvider[task.ProviderSlug] = normalizedProvider
	}

	// 将查询参数（可能是slug或provider）映射回真实的provider
	realProvider := qProvider
	if mappedProvider, exists := slugToProvider[qProvider]; exists {
		realProvider = mappedProvider
	}

	// 将监测项拆分为：
	// - plainCandidates: model 为空（仅进入 data，兼容旧前端）
	// - layeredCandidates: model 非空（仅进入 groups，新前端使用）
	plainCandidates := make([]config.ServiceConfig, 0, len(monitors))
	layeredCandidates := make([]config.ServiceConfig, 0, len(monitors))
	for _, task := range monitors {
		if strings.TrimSpace(task.Model) == "" {
			plainCandidates = append(plainCandidates, task)
			continue
		}
		layeredCandidates = append(layeredCandidates, task)
	}

	// data：过滤并去重（PSC）
	filteredData := h.filterMonitors(plainCandidates, realProvider, qService, qBoard, boardsEnabled, includeHidden)
	// groups：过滤但不去重（保留同一 PSC 下的多 model 层，并保留配置顺序）
	filteredLayered := h.filterMonitorsForGroups(layeredCandidates, realProvider, qService, qBoard, boardsEnabled, includeHidden)

	// 根据配置选择批量/并发/串行查询（支持回退：batch → concurrent → serial）
	var response []MonitorResult
	var err error
	var mode string

	// 批量查询仅针对 7d/30d 的高频大查询场景启用（避免对短周期造成额外复杂度）
	tryBatch := enableBatchQuery && (period == "7d" || period == "30d") && len(filteredData) <= batchQueryMaxKeys
	if tryBatch {
		mode = "batch"
		response, err = h.getStatusBatch(ctx, filteredData, startTime, endTime, period, degradedWeight, timeFilter, enableAnnotations, enableDBTimelineAgg)
		if err != nil {
			logger.Warn("api", "批量查询失败，回退到并发/串行模式", "error", err, "monitors", len(filteredData), "period", period)
		}
	}

	// batch 失败/未启用时回退
	if err != nil || !tryBatch {
		if enableConcurrent {
			mode = "concurrent"
			response, err = h.getStatusConcurrent(ctx, filteredData, startTime, endTime, period, degradedWeight, timeFilter, concurrentLimit, enableAnnotations)
		} else {
			mode = "serial"
			response, err = h.getStatusSerial(ctx, filteredData, startTime, endTime, period, degradedWeight, timeFilter, enableAnnotations)
		}
	}

	if err != nil {
		return nil, err
	}

	// 构建 groups（仅包含有 model 的监测项）
	groups, err := h.buildMonitorGroups(ctx, filteredLayered, startTime, endTime, period, degradedWeight, timeFilter, enableAnnotations, enableDBTimelineAgg, enableConcurrent, concurrentLimit, enableBatchQuery, batchQueryMaxKeys)
	if err != nil {
		return nil, err
	}

	logger.Info("api", "GetStatus 查询完成", "mode", mode, "monitors", len(filteredData), "layered", len(filteredLayered), "period", period, "align", align, "count", len(response), "groups", len(groups))

	// 确定 timeline 模式：90m 返回原始记录，其他返回聚合数据
	timelineMode := "aggregated"
	if period == "90m" {
		timelineMode = "raw"
	}

	// 统计各板块通道数量（基于 override 后的全量配置，排除 disabled/hidden/parent）
	boardCounts := buildBoardCounts(monitors)

	// 构建全量监控项 ID 列表（用于前端清理无效收藏）
	// 排除 disabled 和 hidden，但不受 board 过滤影响
	allMonitorIDs := h.buildAllMonitorIDs(monitors)

	// 序列化为 JSON
	meta := gin.H{
		"period":               period,
		"timeline_mode":        timelineMode,
		"count":                len(response),
		"slow_latency_ms":      slowLatencyMs,
		"enable_annotations":   enableAnnotations,
		"hide_price_column":    hidePriceColumn,
		"hide_category_filter": hideCategoryFilter,
		"rpdiag_enabled":       h.rpdiagEnabled(), // 质量列/diag 入口总开关；私有部署未接 rpdiag 时为 false
		"sponsor_pin": gin.H{
			"enabled":    sponsorPin.IsEnabled(),
			"max_pinned": sponsorPin.MaxPinned,
			"min_uptime": sponsorPin.MinUptime,
			"min_level":  sponsorPin.MinLevel,
		},
		"boards": gin.H{
			"enabled": boardsEnabled,
		},
		"board_counts":    boardCounts,
		"all_monitor_ids": allMonitorIDs,
	}
	// 仅在使用对齐模式时返回额外的时间范围信息
	if align != "" {
		meta["align"] = align
		meta["start_time"] = startTime.UTC().Format(time.RFC3339)
		meta["end_time"] = endTime.UTC().Format(time.RFC3339)
	}
	// 返回时段过滤信息
	if timeFilter != nil {
		meta["time_filter"] = timeFilter.String()
		meta["timezone"] = "UTC"
	}

	result := gin.H{
		"meta":   meta,
		"data":   response,
		"groups": groups,
	}

	return json.Marshal(result)
}

// getStatusBatch 批量查询（GetLatestBatchByModelID + GetHistoryBatchByModelID/GetTimelineAggBatchByModelID）
// 将 N 个监测项的查询从 2N 次 SQL 往返降为 2 次，显著优化 7d/30d 场景性能。
// 按 model_id（稳定键）查询，改展示名不再断历史；结果 map 以 ProbeHistoryKey 为键，
// 组装时用同一构建方式反查（见 probeHistoryKey）。
func (h *Handler) getStatusBatch(ctx context.Context, monitors []config.ServiceConfig, since, endTime time.Time, period string, degradedWeight float64, timeFilter *TimeFilter, enableAnnotations bool, enableDBTimelineAgg bool) ([]MonitorResult, error) {
	store := h.storage.WithContext(ctx)

	// 构建查询 key 列表（model_id 维度）
	keys := make([]storage.ProbeHistoryKey, 0, len(monitors))
	for _, task := range monitors {
		keys = append(keys, probeHistoryKey(task))
	}

	// 批量获取最新记录
	latestMap, err := store.GetLatestBatchByModelID(keys)
	if err != nil {
		return nil, fmt.Errorf("批量查询最新记录失败: %w", err)
	}

	// 可选：将 timeline 聚合下推到 PostgreSQL（仅 7d/30d）
	//
	// 保守策略：
	// - 仅当 enable_db_timeline_agg=true 且存储实现支持 TimelineAggStorage 时启用
	// - 任意错误都回退到 GetHistoryBatchByModelID + buildTimeline 逻辑，确保不影响功能
	useDBAgg := enableDBTimelineAgg && (period == "7d" || period == "30d")
	var aggMap map[storage.ProbeHistoryKey][]storage.AggBucketRow
	if useDBAgg {
		if aggStore, ok := store.(storage.TimelineAggStorage); ok {
			bucketCount, bucketWindow, _ := h.determineBucketStrategy(period)
			if bucketCount > 0 {
				var tf *storage.DailyTimeFilter
				if timeFilter != nil {
					tf = &storage.DailyTimeFilter{
						StartMinutes:  timeFilter.StartHour*60 + timeFilter.StartMinute,
						EndMinutes:    timeFilter.EndHour*60 + timeFilter.EndMinute,
						CrossMidnight: timeFilter.CrossMidnight,
					}
				}
				aggMap, err = aggStore.GetTimelineAggBatchByModelID(keys, since, endTime, bucketCount, bucketWindow, tf)
				if err != nil {
					logger.Warn("api", "DB 时间轴聚合失败，回退到应用层聚合", "error", err, "period", period, "monitors", len(monitors))
					aggMap = nil
				} else {
					logger.Info("api", "使用 DB 时间轴聚合", "period", period, "monitors", len(monitors), "buckets", bucketCount)
				}
			}
		}
	}

	// 回退路径：批量获取历史记录（原有逻辑）
	var historyMap map[storage.ProbeHistoryKey][]*storage.ProbeRecord
	if aggMap == nil {
		historyMap, err = store.GetHistoryBatchByModelID(keys, since)
		if err != nil {
			return nil, fmt.Errorf("批量查询历史记录失败: %w", err)
		}
	}

	// 组装结果（保持原有顺序）
	results := make([]MonitorResult, len(monitors))
	for i, task := range monitors {
		key := probeHistoryKey(task)
		if aggMap != nil {
			// timeline 由 DB 聚合结果生成（与 buildTimeline 输出格式一致）
			res := h.buildMonitorResult(task, latestMap[key], nil, endTime, period, degradedWeight, timeFilter, enableAnnotations)
			res.Timeline = h.buildTimelineFromAgg(aggMap[key], endTime, period, degradedWeight)
			results[i] = res
		} else {
			results[i] = h.buildMonitorResult(task, latestMap[key], historyMap[key], endTime, period, degradedWeight, timeFilter, enableAnnotations)
		}
	}

	return results, nil
}

// getStatusSerial 串行查询（原有逻辑）
func (h *Handler) getStatusSerial(ctx context.Context, monitors []config.ServiceConfig, since, endTime time.Time, period string, degradedWeight float64, timeFilter *TimeFilter, enableAnnotations bool) ([]MonitorResult, error) {
	// 初始化为空切片，确保 JSON 序列化时返回 [] 而不是 null
	response := make([]MonitorResult, 0, len(monitors))
	store := h.storage.WithContext(ctx)

	for _, task := range monitors {
		monitorKey := formatMonitorKey(task.Provider, task.Service, task.Channel, task.Model)

		// 获取最新记录（按 model_id 稳定键查询，改展示名不断历史）
		latest, err := store.GetLatestByModelID(task.ModelID)
		if err != nil {
			return nil, fmt.Errorf("查询失败 %s: %w", monitorKey, err)
		}

		// 获取历史记录
		history, err := store.GetHistoryByModelID(task.ModelID, since)
		if err != nil {
			return nil, fmt.Errorf("查询历史失败 %s: %w", monitorKey, err)
		}

		// 构建响应
		result := h.buildMonitorResult(task, latest, history, endTime, period, degradedWeight, timeFilter, enableAnnotations)
		response = append(response, result)
	}

	return response, nil
}

// getStatusConcurrent 并发查询（使用 errgroup + 并发限制）
func (h *Handler) getStatusConcurrent(ctx context.Context, monitors []config.ServiceConfig, since, endTime time.Time, period string, degradedWeight float64, timeFilter *TimeFilter, limit int, enableAnnotations bool) ([]MonitorResult, error) {
	// 使用请求的 context（支持取消）
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(limit) // 限制最大并发度
	store := h.storage.WithContext(gctx)

	// 预分配结果数组（保持顺序）
	results := make([]MonitorResult, len(monitors))

	for i, task := range monitors {
		i, task := i, task // 捕获循环变量
		g.Go(func() error {
			monitorKey := formatMonitorKey(task.Provider, task.Service, task.Channel, task.Model)

			// 获取最新记录（按 model_id 稳定键查询，改展示名不断历史）
			latest, err := store.GetLatestByModelID(task.ModelID)
			if err != nil {
				return fmt.Errorf("GetLatest %s: %w", monitorKey, err)
			}

			// 获取历史记录
			history, err := store.GetHistoryByModelID(task.ModelID, since)
			if err != nil {
				return fmt.Errorf("GetHistory %s: %w", monitorKey, err)
			}

			// 构建响应（固定位置写入，保持顺序）
			results[i] = h.buildMonitorResult(task, latest, history, endTime, period, degradedWeight, timeFilter, enableAnnotations)
			return nil
		})
	}

	// 等待所有 goroutine 完成
	if err := g.Wait(); err != nil {
		return nil, err
	}

	return results, nil
}

// buildMonitorResult 构建单个监测项的响应结构
// enableAnnotations 仅控制 annotations[] 是否输出，不影响事实字段
func (h *Handler) buildMonitorResult(task config.ServiceConfig, latest *storage.ProbeRecord, history []*storage.ProbeRecord, endTime time.Time, period string, degradedWeight float64, timeFilter *TimeFilter, enableAnnotations bool) MonitorResult {
	// 转换为时间轴数据
	timeline := h.buildTimeline(history, endTime, period, degradedWeight, timeFilter)

	// 转换为API响应格式（不暴露数据库主键）
	var current *CurrentStatus
	if latest != nil {
		current = &CurrentStatus{
			Status:    latest.Status,
			Latency:   latest.Latency,
			Timestamp: latest.Timestamp,
		}
	}

	// 生成 slug：优先使用配置的 provider_slug，回退到 provider 小写
	slug := task.ProviderSlug
	if slug == "" {
		slug = strings.ToLower(strings.TrimSpace(task.Provider))
	}

	// 计算收录天数（从 listed_since 到今天，按 CST 业务日历日计算，见 listed_days.go）
	listedDays := listedDaysSince(task.ListedSince, time.Now().UTC())

	// enable_annotations 仅控制 annotations[] 是否输出
	// 事实字段（category, sponsor_level, interval_ms）始终返回
	annotations := task.Annotations
	if !enableAnnotations {
		annotations = nil
	}

	// 根据配置决定是否暴露通道技术细节（probe_url, template_name）
	var probeURL, templateName string
	h.cfgMu.RLock()
	shouldExpose := h.config.ShouldExposeChannelDetails(task.Provider)
	h.cfgMu.RUnlock()
	if shouldExpose {
		probeURL = sanitizeProbeURL(task.BaseURL)
		templateName = task.Template
	}

	return MonitorResult{
		Provider:          task.Provider,
		ProviderName:      task.ProviderName,
		ProviderSlug:      slug,
		ProviderURL:       task.ProviderURL,
		Service:           task.Service,
		ServiceName:       task.ServiceName,
		Category:          task.Category,
		Sponsor:           task.Sponsor,
		SponsorURL:        task.SponsorURL,
		SponsorLevel:      task.SponsorLevel,
		Annotations:       annotations,
		PriceMin:          task.PriceMin,
		PriceMax:          task.PriceMax,
		ListedDays:        listedDays,
		Channel:           task.Channel,
		ChannelID:         task.ChannelID,
		ChannelName:       task.ChannelName,
		Model:             task.Model,
		RequestModel:      resolvedRequestModel(task),
		ModelVendor:       task.ModelVendor,
		Board:             task.Board,
		ColdReason:        task.ColdReason,
		BoardReason:       task.BoardReason,
		BoardReasonModels: task.BoardReasonModels,
		ProbeURL:          probeURL,
		TemplateName:      templateName,
		IntervalMs:        task.IntervalDuration.Milliseconds(),
		SlowLatencyMs:     task.SlowLatencyDuration.Milliseconds(),
		Current:           current,
		Timeline:          timeline,
	}
}

// sanitizeProbeURL 脱敏探测 URL：移除 userinfo 和 query 参数
// 只保留 scheme://host/path 部分，避免泄露敏感信息
// 解析失败时返回空字符串，不泄露可能包含敏感信息的原始 URL
func sanitizeProbeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "" // 解析失败时返回空字符串，避免泄露敏感信息
	}
	u.User = nil    // 移除 user:password@
	u.RawQuery = "" // 移除 query 参数
	u.Fragment = "" // 移除 fragment
	return u.String()
}

// formatMonitorKey 格式化监测项 key（用于日志输出）
func formatMonitorKey(provider, service, channel, model string) string {
	if strings.TrimSpace(model) == "" {
		return provider + "/" + service + "/" + channel
	}
	return provider + "/" + service + "/" + channel + "/" + model
}

// probeHistoryKey 从运行时监测行构建 model_id 维度的 probe_history 查询键。
//
// ModelID 是批量读方法（GetLatestBatchByModelID 等）的查询与去重键；Provider/Service/
// Channel/Model 仅用于日志与让返回 map 与调用方按身份对齐。构建查询列表与组装结果时
// 必须复用本函数，保证两处 key 完全一致（否则结果 map 反查会静默 miss）。
func probeHistoryKey(task config.ServiceConfig) storage.ProbeHistoryKey {
	return storage.ProbeHistoryKey{
		ModelID:  task.ModelID,
		Provider: task.Provider,
		Service:  task.Service,
		Channel:  task.Channel,
		Model:    task.Model,
	}
}

// parsePeriod 解析时间范围（仅用于验证）
func (h *Handler) parsePeriod(period string) (time.Duration, error) {
	switch period {
	case "90m":
		return 90 * time.Minute, nil
	case "24h", "1d":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("不支持的时间范围")
	}
}

// parseTimeRange 解析时间范围，返回 (startTime, endTime)
// align 参数控制时间对齐模式：空=动态滑动窗口, "hour"=整点对齐
// 注意：90m 固定使用动态窗口，7d/30d 模式自动使用 day 对齐，忽略 align 参数
func (h *Handler) parseTimeRange(period, align string) (startTime, endTime time.Time) {
	now := time.Now()

	// 根据 period 计算时间范围
	// 90m: 固定动态窗口
	// 24h: 用户可选 align 模式
	// 7d/30d: 强制使用 day 对齐（包含今天不完整数据）
	switch period {
	case "90m":
		endTime = now // 动态滑动窗口：不对齐
		startTime = endTime.Add(-90 * time.Minute)
	case "24h", "1d":
		endTime = h.alignTimestamp(now, align)
		startTime = endTime.Add(-24 * time.Hour)
	case "7d":
		endTime = h.alignTimestamp(now, "day") // 自动按天对齐
		startTime = endTime.AddDate(0, 0, -7)
	case "30d":
		endTime = h.alignTimestamp(now, "day") // 自动按天对齐
		startTime = endTime.AddDate(0, 0, -30)
	default:
		endTime = h.alignTimestamp(now, align)
		startTime = endTime.Add(-24 * time.Hour)
	}

	return startTime, endTime
}

// alignTimestamp 根据对齐模式调整时间戳
// - align="hour": 向上取整到下一个 UTC 整点
// - align="day": 向上取整到下一天 00:00 UTC
// - 其他值: 保持原值（动态滑动窗口）
func (h *Handler) alignTimestamp(t time.Time, align string) time.Time {
	switch align {
	case "hour":
		// 向上取整到下一个整点（包含当前正在进行的小时）
		// 例如 17:48 → 18:00，这样最后一个 bucket 是 17:00-18:00
		truncated := t.UTC().Truncate(time.Hour)
		if truncated.Before(t.UTC()) {
			return truncated.Add(time.Hour)
		}
		return truncated
	case "day":
		// 向上取整到下一天 00:00 UTC（包含今天不完整的数据）
		// 例如 2024-01-15 12:30 → 2024-01-16 00:00，这样最后一个 bucket 是今天
		truncated := t.UTC().Truncate(24 * time.Hour)
		if truncated.Before(t.UTC()) {
			return truncated.Add(24 * time.Hour)
		}
		return truncated
	default:
		return t
	}
}
