package api

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"

	"monitor/internal/automove"
	"monitor/internal/change"
	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
	"monitor/internal/rpdiag"
	"monitor/internal/storage"
)

// TimeFilter 每日时段过滤器（UTC 时区）
// 用于过滤特定时间段内的探测记录，如工作时间 09:00-17:00
// 支持跨午夜的时间范围，如 22:00-04:00（表示 22:00 到次日 04:00）
type TimeFilter struct {
	StartHour     int  // 开始小时 (0-23)
	StartMinute   int  // 开始分钟 (0 或 30)
	EndHour       int  // 结束小时 (0-24，24:00 表示午夜)
	EndMinute     int  // 结束分钟 (0 或 30)
	CrossMidnight bool // 是否跨午夜（start > end）
}

// timeFilterRegex 时段格式正则：HH:MM-HH:MM
var timeFilterRegex = regexp.MustCompile(`^(\d{2}):(\d{2})-(\d{2}):(\d{2})$`)

// Contains 检查给定 UTC 时间是否在时段范围内（左闭右开区间）
// 支持跨午夜的时间范围，如 22:00-04:00
func (f *TimeFilter) Contains(t time.Time) bool {
	h, m, _ := t.UTC().Clock()
	startMinutes := f.StartHour*60 + f.StartMinute
	endMinutes := f.EndHour*60 + f.EndMinute
	currentMinutes := h*60 + m

	if f.CrossMidnight {
		// 跨午夜：22:00-04:00 表示 [22:00, 24:00) ∪ [00:00, 04:00)
		return currentMinutes >= startMinutes || currentMinutes < endMinutes
	}
	// 正常范围：09:00-17:00 表示 [09:00, 17:00)
	return currentMinutes >= startMinutes && currentMinutes < endMinutes
}

// String 返回时段的字符串表示
func (f *TimeFilter) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", f.StartHour, f.StartMinute, f.EndHour, f.EndMinute)
}

// ParseTimeFilter 解析时段参数
// 返回 nil 表示无过滤（全天）
// 格式：HH:MM-HH:MM，分钟必须为 00 或 30，支持 24:00 表示午夜
// 支持跨午夜的时间范围，如 22:00-04:00（表示 22:00 到次日 04:00）
func ParseTimeFilter(param string) (*TimeFilter, error) {
	if param == "" {
		return nil, nil
	}

	// 正则校验格式
	matches := timeFilterRegex.FindStringSubmatch(param)
	if len(matches) != 5 {
		return nil, fmt.Errorf("无效的时段格式: %s（应为 HH:MM-HH:MM）", param)
	}

	startH, _ := strconv.Atoi(matches[1])
	startM, _ := strconv.Atoi(matches[2])
	endH, _ := strconv.Atoi(matches[3])
	endM, _ := strconv.Atoi(matches[4])

	// 粒度校验：分钟必须为 00 或 30
	if (startM != 0 && startM != 30) || (endM != 0 && endM != 30) {
		return nil, fmt.Errorf("分钟必须为 00 或 30: %s", param)
	}

	// 范围校验：开始 0-23，结束 0-24
	if startH < 0 || startH > 23 {
		return nil, fmt.Errorf("开始小时必须在 0-23 范围内: %s", param)
	}
	if endH < 0 || endH > 24 {
		return nil, fmt.Errorf("结束小时必须在 0-24 范围内: %s", param)
	}
	// 24:00 只允许 24:00，不允许 24:30
	if endH == 24 && endM != 0 {
		return nil, fmt.Errorf("24 点只允许 24:00: %s", param)
	}

	// 判断是否跨午夜
	startTotal := startH*60 + startM
	endTotal := endH*60 + endM
	crossMidnight := startTotal >= endTotal

	// 开始和结束相同时无效（无时段）
	if startTotal == endTotal {
		return nil, fmt.Errorf("开始时间不能等于结束时间: %s", param)
	}

	return &TimeFilter{
		StartHour:     startH,
		StartMinute:   startM,
		EndHour:       endH,
		EndMinute:     endM,
		CrossMidnight: crossMidnight,
	}, nil
}

// statusCache API 响应缓存，防止高频查询打爆数据库
type statusCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
	maxSize int                // 最大缓存条目数，防止内存泄漏
	sf      singleflight.Group // 防止缓存击穿
}

type cacheEntry struct {
	data     []byte
	expireAt time.Time
}

func newStatusCache(ttl time.Duration, maxSize int) *statusCache {
	return &statusCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}
}

// get 获取缓存，过期则删除并返回 miss
func (c *statusCache) get(key string) ([]byte, bool) {
	now := time.Now()
	c.mu.RLock()
	entry := c.entries[key]
	c.mu.RUnlock()

	if entry == nil {
		return nil, false
	}

	if now.After(entry.expireAt) {
		// 懒清理：删除过期 key
		c.mu.Lock()
		if cur := c.entries[key]; cur == entry {
			delete(c.entries, key)
		}
		c.mu.Unlock()
		return nil, false
	}

	return entry.data, true
}

// set 存入缓存（拷贝数据，防止 buffer 复用问题）
func (c *statusCache) set(key string, data []byte) {
	c.setWithTTL(key, data, c.ttl)
}

// setWithTTL 存入缓存（支持自定义 TTL）
func (c *statusCache) setWithTTL(key string, data []byte, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.ttl
	}

	buf := make([]byte, len(data))
	copy(buf, data)

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	// 容量限制：超出时清理过期条目
	if len(c.entries) >= c.maxSize {
		for k, v := range c.entries {
			if now.After(v.expireAt) {
				delete(c.entries, k)
			}
		}
	}

	// 仍然超出则跳过写入（防止 DoS）
	if len(c.entries) >= c.maxSize {
		return
	}

	c.entries[key] = &cacheEntry{
		data:     buf,
		expireAt: now.Add(ttl),
	}
}

// clear 清空所有缓存（配置热更新时调用）
func (c *statusCache) clear() {
	c.mu.Lock()
	c.entries = make(map[string]*cacheEntry)
	c.mu.Unlock()
}

// load 获取缓存，未命中时用 singleflight 合并并发请求
func (c *statusCache) load(key string, loader func() ([]byte, error)) ([]byte, error) {
	return c.loadWithTTL(key, c.ttl, loader)
}

// loadWithTTL 获取缓存（支持自定义 TTL），未命中时用 singleflight 合并并发请求
func (c *statusCache) loadWithTTL(key string, ttl time.Duration, loader func() ([]byte, error)) ([]byte, error) {
	// 先检查缓存
	if data, ok := c.get(key); ok {
		return data, nil
	}

	// singleflight: 同 key 多请求只执行一次 loader
	v, err, _ := c.sf.Do(key, func() (interface{}, error) {
		// double check：可能在等待期间已被其他 goroutine 填充
		if data, ok := c.get(key); ok {
			return data, nil
		}

		fresh, err := loader()
		if err != nil {
			return nil, err // 错误不缓存
		}

		c.setWithTTL(key, fresh, ttl)
		return fresh, nil
	})

	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// Handler API处理器
type Handler struct {
	storage       storage.Storage
	config        *config.AppConfig
	cfgMu         sync.RWMutex         // 保护config的并发访问
	cache         *statusCache         // API 响应缓存
	autoMover     *automove.Service    // 自动移板服务（可选）
	inlineProber  *probe.InlineProber  // 内联探测器
	probeLimiter  *probe.IPLimiter     // 公共探测端点限流
	onboardingMu  sync.RWMutex         // 保护 onboardingSvc 热替换
	onboardingSvc *onboarding.Service  // 自助收录服务（可选）
	changeMu      sync.RWMutex         // 保护 changeSvc 热替换
	changeSvc     *change.Service      // 变更请求服务（可选）
	monitorStore  *config.MonitorStore // monitors.d/ CRUD（可选）
	rpdiagClient  *rpdiag.Client       // rpdiag 质量分客户端（可选，启动时一次性注入）
}

// NewHandler 创建处理器
func NewHandler(store storage.Storage, cfg *config.AppConfig, autoMover *automove.Service) *Handler {
	return &Handler{
		storage:   store,
		config:    cfg,
		cache:     newStatusCache(10*time.Second, 100), // 10 秒缓存，最多 100 条
		autoMover: autoMover,
	}
}

// SetInlineProber 设置内联探测器。
func (h *Handler) SetInlineProber(p *probe.InlineProber) {
	h.inlineProber = p
}

// SetRpdiagClient 注入 rpdiag 质量分客户端。client 为 nil 表示未启用。
// 设计为启动期一次性注入（不支持热替换），故无锁。
func (h *Handler) SetRpdiagClient(client *rpdiag.Client) {
	h.rpdiagClient = client
}

// rpdiagEnabled 报告 rpdiag 质量分功能是否启用（= 是否注入了客户端）。
// 这是「质量列（含前端 rpdiag_enabled flag）/ 旧 /detect 路径 301」共用的单一信号源：
// 私有化部署未设 MONITOR_RPDIAG_ENABLED 时质量入口全部一致地消失。
// rpdiagClient 仅启动期一次性注入、不支持热替换，故读取无需加锁。
func (h *Handler) rpdiagEnabled() bool {
	return h.rpdiagClient != nil
}

// GetRpdiagScores 返回 rpdiag 质量分索引（按 "provider|service|channel" 键）。
// 当客户端未启用（MONITOR_RPDIAG_ENABLED=false / 未设置）时返回空对象，
// 由前端兜底显示 "-"；上游异常时返回 503，前端走同样的空兜底。
func (h *Handler) GetRpdiagScores(c *gin.Context) {
	if !h.rpdiagEnabled() {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	scores, err := h.rpdiagClient.Scores(ctx)
	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error(
			"获取 rpdiag 质量分失败", "error", err,
		)
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "质量分暂不可用")
		return
	}

	c.Header("Cache-Control", "public, max-age=60")
	c.JSON(http.StatusOK, scores)
}

// SetProbeLimiter 设置公共探测端点限流器。
func (h *Handler) SetProbeLimiter(l *probe.IPLimiter) {
	h.probeLimiter = l
}

// SetOnboardingService 设置自助收录服务（并发安全，支持热更新时替换实例）
func (h *Handler) SetOnboardingService(svc *onboarding.Service) {
	h.onboardingMu.Lock()
	h.onboardingSvc = svc
	h.onboardingMu.Unlock()
}

// getOnboardingService 获取当前自助收录服务（并发安全）
func (h *Handler) getOnboardingService() *onboarding.Service {
	h.onboardingMu.RLock()
	defer h.onboardingMu.RUnlock()
	return h.onboardingSvc
}

// SetChangeService 设置变更请求服务（并发安全）
func (h *Handler) SetChangeService(svc *change.Service) {
	h.changeMu.Lock()
	h.changeSvc = svc
	h.changeMu.Unlock()
}

// getChangeService 获取当前变更请求服务（并发安全）
func (h *Handler) getChangeService() *change.Service {
	h.changeMu.RLock()
	defer h.changeMu.RUnlock()
	return h.changeSvc
}

// SetMonitorStore 设置 monitors.d/ 存储（仅初始化时调用一次，无需加锁）
func (h *Handler) SetMonitorStore(store *config.MonitorStore) {
	h.monitorStore = store
}

// getMonitorStore 获取 monitors.d/ 存储
func (h *Handler) getMonitorStore() *config.MonitorStore {
	return h.monitorStore
}

// CurrentStatus API返回的当前状态（不暴露数据库主键）
type CurrentStatus struct {
	Status    int   `json:"status"`
	Latency   int   `json:"latency"`
	Timestamp int64 `json:"timestamp"`
}

// MonitorResult API返回结构
type MonitorResult struct {
	Provider          string              `json:"provider"`
	ProviderName      string              `json:"provider_name,omitempty"` // Provider 显示名称
	ProviderSlug      string              `json:"provider_slug"`           // URL slug（用于生成专属页面链接）
	ProviderURL       string              `json:"provider_url"`            // 服务商官网链接
	Service           string              `json:"service"`
	ServiceName       string              `json:"service_name,omitempty"`        // Service 显示名称
	Category          string              `json:"category"`                      // 分类：commercial（商业站）或 public（公益站）
	Sponsor           string              `json:"sponsor"`                       // 赞助者
	SponsorURL        string              `json:"sponsor_url"`                   // 赞助者链接
	SponsorLevel      config.SponsorLevel `json:"sponsor_level,omitempty"`       // 赞助等级（事实字段，不受 enable_annotations 影响）
	Annotations       []config.Annotation `json:"annotations,omitempty"`         // 统一注解数组（受 enable_annotations 开关控制）
	PriceMin          *float64            `json:"price_min,omitempty"`           // 参考倍率下限
	PriceMax          *float64            `json:"price_max,omitempty"`           // 参考倍率
	ListedDays        *int                `json:"listed_days,omitempty"`         // 收录天数（从 listed_since 计算）
	Channel           string              `json:"channel"`                       // 业务通道标识
	ChannelID         string              `json:"channel_id,omitempty"`          // 通道稳定 id（跨产品 join 锚，供 rpdiag sampler 发现）
	ChannelName       string              `json:"channel_name,omitempty"`        // Channel 显示名称
	Model             string              `json:"model,omitempty"`               // 模型展示名
	RequestModel      string              `json:"request_model,omitempty"`       // 实际请求模型 ID
	ModelVendor       string              `json:"model_vendor,omitempty"`        // 模型厂商受控 code（词表见 internal/modelvendor），与 MonitorLayer 同源同义
	Board             string              `json:"board"`                         // 板块：hot/cold
	ColdReason        string              `json:"cold_reason,omitempty"`         // 冷板原因（仅 cold 有值）
	BoardReason       string              `json:"board_reason,omitempty"`        // 移板机器码（如 "quality_hardfail"），前端本地化
	BoardReasonModels string              `json:"board_reason_models,omitempty"` // 触发质量移板的模型名（逗号连接）
	ProbeURL          string              `json:"probe_url,omitempty"`           // 探测端点 URL（脱敏后）
	TemplateName      string              `json:"template_name,omitempty"`       // 请求体模板名称（如有）
	IntervalMs        int64               `json:"interval_ms"`                   // 监测间隔（毫秒）
	SlowLatencyMs     int64               `json:"slow_latency_ms"`               // 慢请求阈值（毫秒）
	Current           *CurrentStatus      `json:"current_status"`
	Timeline          []storage.TimePoint `json:"timeline"`
}

// GetStatus 获取监测状态
func (h *Handler) GetStatus(c *gin.Context) {
	// 参数解析
	period := c.DefaultQuery("period", "24h")
	align := c.DefaultQuery("align", "")                 // 时间对齐模式：空=动态滑动窗口, "hour"=整点对齐
	timeFilterParam := c.DefaultQuery("time_filter", "") // 每日时段过滤：HH:MM-HH:MM（UTC）
	qProvider := strings.ToLower(strings.TrimSpace(c.DefaultQuery("provider", "all")))
	qService := c.DefaultQuery("service", "all")
	// board 参数：hot/cold/all（默认 hot）
	// 注意：gin 的 DefaultQuery 在参数存在但值为空时返回空字符串，需要额外处理
	qBoard := strings.ToLower(strings.TrimSpace(c.DefaultQuery("board", "hot")))
	if qBoard == "" {
		qBoard = "hot" // 空值归一为默认值
	}
	// include_hidden 参数：用于内部调试，默认不包含隐藏的监测项
	includeHidden := strings.EqualFold(strings.TrimSpace(c.DefaultQuery("include_hidden", "false")), "true")

	// 验证 period 参数
	if _, err := h.parsePeriod(period); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, fmt.Sprintf("无效的时间范围: %s", period))
		return
	}

	// 验证 align 参数
	if align != "" && align != "hour" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, fmt.Sprintf("无效的对齐模式: %s (支持: hour)", align))
		return
	}

	// 验证 board 参数
	if qBoard != "hot" && qBoard != "secondary" && qBoard != "cold" && qBoard != "active" && qBoard != "all" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, fmt.Sprintf("无效的 board 参数: %s (支持: hot/secondary/cold/active/all)", qBoard))
		return
	}

	// 验证 time_filter 参数
	var timeFilter *TimeFilter
	if timeFilterParam != "" {
		// 时段过滤仅支持 7d 和 30d 周期
		if period == "90m" || period == "24h" || period == "1d" {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "时段过滤仅支持 7d 和 30d 周期")
			return
		}

		var err error
		timeFilter, err = ParseTimeFilter(timeFilterParam)
		if err != nil {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
			return
		}
	}

	// 构建缓存 key（使用明确的分隔符避免碰撞）
	cacheKey := fmt.Sprintf("p=%s|align=%s|tf=%s|prov=%s|svc=%s|board=%s|hidden=%t", period, align, timeFilterParam, qProvider, qService, qBoard, includeHidden)

	// 从配置获取缓存 TTL（线程安全）
	h.cfgMu.RLock()
	cacheTTL := h.config.CacheTTL.TTLForPeriod(period)
	h.cfgMu.RUnlock()

	// 使用缓存（singleflight 防止缓存击穿）
	// 注意：使用独立 context，避免单个请求取消影响其他等待的请求
	data, err := h.cache.loadWithTTL(cacheKey, cacheTTL, func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return h.queryAndSerialize(ctx, period, align, timeFilter, qProvider, qService, qBoard, includeHidden)
	})

	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error("GetStatus 失败", "cache_key", cacheKey, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询失败，请稍后再试")
		return
	}

	// CDN 缓存头：Cloudflare 遵守 s-maxage，浏览器遵守 max-age
	ttlSeconds := int(cacheTTL.Seconds())
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d, s-maxage=%d", ttlSeconds, ttlSeconds))
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Writer.Write(data)
}

// applyBoardOverrides 将自动移板 override 应用到监测项列表。
// 覆盖 Board/ColdReason/SponsorLevel 字段，并将父通道 override 传播给同 PSC 的子模型；
// annotationRules/globalInterval 用于 board 或 sponsor 覆盖后重算 annotations。
func (h *Handler) applyBoardOverrides(monitors []config.ServiceConfig, annotationRules []config.AnnotationRule, globalInterval time.Duration) []config.ServiceConfig {
	if h.autoMover == nil {
		return monitors
	}
	return automove.ApplyOverrides(monitors, h.autoMover.Overrides(), annotationRules, globalInterval)
}

// filterMonitors 过滤并去重监测项
// board 参数：hot/secondary/cold/all，boardsEnabled 控制是否启用板块过滤
func (h *Handler) filterMonitors(monitors []config.ServiceConfig, provider, service, board string, boardsEnabled, includeHidden bool) []config.ServiceConfig {
	var filtered []config.ServiceConfig
	seen := make(map[string]bool)

	for _, task := range monitors {
		// 始终过滤已禁用的监测项（不探测、不存储、不展示）
		if task.Disabled {
			continue
		}

		// 过滤隐藏的监测项（除非显式要求包含）
		if !includeHidden && task.Hidden {
			continue
		}

		// 板块过滤（仅当 boards 功能启用时生效）
		if boardsEnabled && board != "all" {
			if board == "active" {
				if task.Board != "hot" && task.Board != "secondary" {
					continue
				}
			} else if board != task.Board {
				continue
			}
		}

		normalizedTaskProvider := strings.ToLower(strings.TrimSpace(task.Provider))

		// 过滤（统一使用 provider 名称匹配）
		if provider != "all" && provider != normalizedTaskProvider {
			continue
		}
		if service != "all" && service != task.Service {
			continue
		}

		// 去重（使用 provider + service + channel 组合）
		key := task.Provider + "/" + task.Service + "/" + task.Channel
		if seen[key] {
			continue
		}
		seen[key] = true

		filtered = append(filtered, task)
	}

	return filtered
}

// buildBoardCounts 统计各板块通道数量（排除 disabled/hidden/有 parent 的监测项）
func buildBoardCounts(monitors []config.ServiceConfig) gin.H {
	var hot, secondary, cold int
	for _, task := range monitors {
		if task.Disabled || task.Hidden {
			continue
		}
		if strings.TrimSpace(task.Parent) != "" {
			continue
		}
		switch task.Board {
		case "hot":
			hot++
		case "secondary":
			secondary++
		case "cold":
			cold++
		}
	}
	return gin.H{
		"hot":       hot,
		"secondary": secondary,
		"cold":      cold,
	}
}

// buildAllMonitorIDs 构建全量监控项 ID 列表（用于前端清理无效收藏）
// 排除 disabled 和 hidden，但不受 board 过滤影响
// ID 格式与前端保持一致：{provider}-{service}-{channel}
func (h *Handler) buildAllMonitorIDs(monitors []config.ServiceConfig) []string {
	seen := make(map[string]bool)
	var ids []string

	for _, task := range monitors {
		// 排除已禁用的监测项
		if task.Disabled {
			continue
		}
		// 排除隐藏的监测项
		if task.Hidden {
			continue
		}

		// 生成 ID（与前端 useMonitorData.ts 保持一致）
		// 前端格式：`${providerKey || item.provider}-${item.service}-${item.channel || 'default'}`
		providerKey := strings.ToLower(strings.TrimSpace(task.Provider))
		channel := task.Channel
		if channel == "" {
			channel = "default"
		}
		id := providerKey + "-" + task.Service + "-" + channel

		// 去重
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}

	return ids
}

// UpdateConfig 更新配置（热更新时调用）
func (h *Handler) UpdateConfig(cfg *config.AppConfig) {
	h.cfgMu.Lock()
	h.config = cfg
	h.cfgMu.Unlock()

	// 配置更新后清空缓存，确保禁用/隐藏状态变更立即生效
	h.cache.clear()
}

// GetSitemap 生成 sitemap.xml
func (h *Handler) GetSitemap(c *gin.Context) {
	// 获取配置副本
	h.cfgMu.RLock()
	monitors := h.config.Monitors
	h.cfgMu.RUnlock()

	// 提取唯一的 provider slugs
	providerSlugs := h.extractUniqueProviderSlugs(monitors)

	// 构建 sitemap XML
	sitemap := h.buildSitemapXML(providerSlugs)

	c.Header("Content-Type", "application/xml; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=3600") // 缓存 1 小时
	c.String(http.StatusOK, sitemap)
}

// extractUniqueProviderSlugs 从监测配置中提取唯一的 provider slugs（排除禁用和隐藏的）
func (h *Handler) extractUniqueProviderSlugs(monitors []config.ServiceConfig) []string {
	slugSet := make(map[string]bool)
	var slugs []string

	for _, task := range monitors {
		// 跳过已禁用的监测项
		if task.Disabled {
			continue
		}
		// 跳过隐藏的监测项
		if task.Hidden {
			continue
		}

		slug := task.ProviderSlug
		if slug == "" {
			slug = strings.ToLower(strings.TrimSpace(task.Provider))
		}

		if !slugSet[slug] {
			slugSet[slug] = true
			slugs = append(slugs, slug)
		}
	}

	return slugs
}

// buildSitemapXML 构建 sitemap.xml 内容
func (h *Handler) buildSitemapXML(providerSlugs []string) string {
	h.cfgMu.RLock()
	baseURL := h.config.PublicBaseURL
	h.cfgMu.RUnlock()
	languages := []struct {
		code string // hreflang 语言码
		path string // URL 路径前缀
	}{
		{"zh-CN", ""}, // 中文默认无前缀
		{"en", "en"},  // 英文
		{"ru", "ru"},  // 俄文
		{"ja", "ja"},  // 日文
	}

	// 首页与服务商页的可用率数据每个巡检周期都在刷新，lastmod 取当日（UTC）是诚实信号；
	// 静态内容页（contact）不随天滚动，故不发 lastmod，避免“假新鲜度”被搜索引擎贬权。
	lastmod := time.Now().UTC().Format("2006-01-02")

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`)
	sb.WriteString("\n")
	sb.WriteString(`        xmlns:xhtml="http://www.w3.org/1999/xhtml">`)
	sb.WriteString("\n")

	// 生成首页 URL（4 个语言版本）
	for _, lang := range languages {
		sb.WriteString("  <url>\n")

		// 生成 loc
		if lang.path == "" {
			sb.WriteString(fmt.Sprintf("    <loc>%s/</loc>\n", baseURL))
		} else {
			sb.WriteString(fmt.Sprintf("    <loc>%s/%s/</loc>\n", baseURL, lang.path))
		}

		// 生成 hreflang 链接（指向所有语言版本）
		for _, altLang := range languages {
			var href string
			if altLang.path == "" {
				href = fmt.Sprintf("%s/", baseURL)
			} else {
				href = fmt.Sprintf("%s/%s/", baseURL, altLang.path)
			}
			sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="%s" href="%s"/>`+"\n", altLang.code, href))
		}

		// x-default 指向中文首页
		sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="x-default" href="%s/"/>`+"\n", baseURL))

		sb.WriteString(fmt.Sprintf("    <lastmod>%s</lastmod>\n", lastmod))
		sb.WriteString("    <priority>1.0</priority>\n")
		sb.WriteString("    <changefreq>daily</changefreq>\n")
		sb.WriteString("  </url>\n")
	}

	// 生成静态内容页 URL（contact 联系页）
	// 仅索引落地页；apply/change 是表单页，前端已设 noindex。
	// 不发 lastmod：这些页的正文不随天滚动，避免“假新鲜度”被搜索引擎贬权。
	// 注：旧 /detect 专题页已下线（301 到 rpdiag 站点），不再收录。
	staticPages := []struct {
		path     string
		priority string
	}{
		{"contact", "0.6"},
	}
	for _, page := range staticPages {
		for _, lang := range languages {
			sb.WriteString("  <url>\n")
			if lang.path == "" {
				sb.WriteString(fmt.Sprintf("    <loc>%s/%s</loc>\n", baseURL, page.path))
			} else {
				sb.WriteString(fmt.Sprintf("    <loc>%s/%s/%s</loc>\n", baseURL, lang.path, page.path))
			}
			for _, altLang := range languages {
				var href string
				if altLang.path == "" {
					href = fmt.Sprintf("%s/%s", baseURL, page.path)
				} else {
					href = fmt.Sprintf("%s/%s/%s", baseURL, altLang.path, page.path)
				}
				sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="%s" href="%s"/>`+"\n", altLang.code, href))
			}
			sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="x-default" href="%s/%s"/>`+"\n", baseURL, page.path))
			sb.WriteString(fmt.Sprintf("    <priority>%s</priority>\n", page.priority))
			sb.WriteString("    <changefreq>weekly</changefreq>\n")
			sb.WriteString("  </url>\n")
		}
	}

	// 生成服务商页面 URL（每个 provider 4 个语言版本）
	for _, slug := range providerSlugs {
		for _, lang := range languages {
			sb.WriteString("  <url>\n")

			// 生成 loc
			if lang.path == "" {
				sb.WriteString(fmt.Sprintf("    <loc>%s/p/%s</loc>\n", baseURL, slug))
			} else {
				sb.WriteString(fmt.Sprintf("    <loc>%s/%s/p/%s</loc>\n", baseURL, lang.path, slug))
			}

			// 生成 hreflang 链接（指向所有语言版本）
			for _, altLang := range languages {
				var href string
				if altLang.path == "" {
					href = fmt.Sprintf("%s/p/%s", baseURL, slug)
				} else {
					href = fmt.Sprintf("%s/%s/p/%s", baseURL, altLang.path, slug)
				}
				sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="%s" href="%s"/>`+"\n", altLang.code, href))
			}

			// x-default 指向中文版本
			sb.WriteString(fmt.Sprintf(`    <xhtml:link rel="alternate" hreflang="x-default" href="%s/p/%s"/>`+"\n", baseURL, slug))

			sb.WriteString(fmt.Sprintf("    <lastmod>%s</lastmod>\n", lastmod))
			sb.WriteString("    <priority>0.8</priority>\n")
			sb.WriteString("    <changefreq>daily</changefreq>\n")
			sb.WriteString("  </url>\n")
		}
	}

	sb.WriteString("</urlset>\n")
	return sb.String()
}

// GetRobots 生成 robots.txt
func (h *Handler) GetRobots(c *gin.Context) {
	h.cfgMu.RLock()
	baseURL := h.config.PublicBaseURL
	h.cfgMu.RUnlock()

	robotsTxt := fmt.Sprintf(`User-agent: *
Allow: /
Disallow: /api/

Sitemap: %s/sitemap.xml
`, baseURL)

	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Header("Cache-Control", "public, max-age=86400") // 缓存 24 小时
	c.String(http.StatusOK, robotsTxt)
}
