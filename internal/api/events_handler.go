package api

import (
	"crypto/subtle"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"monitor/internal/logger"
	"monitor/internal/storage"
)

// EventsResponse 事件列表响应
type EventsResponse struct {
	Events []EventItem `json:"events"`
	Meta   EventsMeta  `json:"meta"`
}

// EventItem 单个事件
//
// ⚠️ `Channel` 与 `Model` 是**标识键**，不是站点上显示给人看的那两个串：
// `Channel` 是通道标识（`O-web`），展示名在配置的 `channel_name`（`O-Pro`）；
// `Model` 是展示名兼 DB 业务键（cx 线统一收敛成 `GPT`），真实请求的模型在
// `request_model`（`gpt-6-astra`）。`status_events` 表只存前者，故后者由本端
// 读取时按运行时配置 join 出来，作为 `ChannelName`/`RequestModels` 下发——
// 否则通知端只能拿标识键当人话发出去（2026-09-06 修）。
type EventItem struct {
	ID       int64  `json:"id"`
	Provider string `json:"provider"`
	Service  string `json:"service"`
	Channel  string `json:"channel,omitempty"`
	Model    string `json:"model,omitempty"`
	// ChannelName 通道展示名，仅当配置了且与 Channel 不同时非空。
	ChannelName string `json:"channel_name,omitempty"`
	// RequestModels 本事件涉及的模型对应的**实际请求模型 ID**（已去重排序）。
	// 通道级事件涉及多个模型，故是数组；查不到配置的（通道已下架/改名）回退原业务键。
	RequestModels   []string       `json:"request_models,omitempty"`
	Type            string         `json:"type"`
	FromStatus      int            `json:"from_status"`
	ToStatus        int            `json:"to_status"`
	TriggerRecordID int64          `json:"trigger_record_id"`
	ObservedAt      int64          `json:"observed_at"`
	CreatedAt       int64          `json:"created_at"`
	Meta            map[string]any `json:"meta,omitempty"`
}

// EventsMeta 事件列表元数据
type EventsMeta struct {
	NextSinceID int64 `json:"next_since_id"`
	HasMore     bool  `json:"has_more"`
	Count       int   `json:"count"`
}

// LatestEventResponse 最新事件ID响应
type LatestEventResponse struct {
	LatestID  int64 `json:"latest_id"`
	Timestamp int64 `json:"timestamp,omitempty"`
}

// GetEvents 获取事件列表
// GET /api/events?since_id=0&limit=20&provider=xxx&service=xxx&channel=xxx&types=DOWN,UP
// limit 默认 20，最大 100
func (h *Handler) GetEvents(c *gin.Context) {
	// 检查 API Token（如果配置了）
	if !h.checkEventsAPIToken(c) {
		return
	}

	// 解析查询参数（仅在参数缺省时使用默认值；显式传参必须是合法整数）
	sinceID := int64(0)
	if rawSinceID, ok := c.GetQuery("since_id"); ok {
		parsedSinceID, err := strconv.ParseInt(strings.TrimSpace(rawSinceID), 10, 64)
		if err != nil || parsedSinceID < 0 {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "since_id 必须为大于等于 0 的整数")
			return
		}
		sinceID = parsedSinceID
	}

	limit := 20
	if rawLimit, ok := c.GetQuery("limit"); ok {
		parsedLimit, err := strconv.Atoi(strings.TrimSpace(rawLimit))
		if err != nil || parsedLimit <= 0 {
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "limit 必须为 1-100 的整数")
			return
		}
		if parsedLimit > 100 {
			parsedLimit = 100
		}
		limit = parsedLimit
	}

	// 构建过滤器
	var filters *storage.EventFilters
	provider := c.Query("provider")
	service := c.Query("service")
	channel := c.Query("channel")
	typesStr := c.Query("types")

	if provider != "" || service != "" || channel != "" || typesStr != "" {
		filters = &storage.EventFilters{
			Provider: provider,
			Service:  service,
			Channel:  channel,
		}

		if typesStr != "" {
			types := strings.Split(typesStr, ",")
			for _, t := range types {
				t = strings.TrimSpace(t)
				if t == "" {
					continue
				}
				if t != "DOWN" && t != "UP" {
					apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "types 仅支持 DOWN、UP（逗号分隔）")
					return
				}
				filters.Types = append(filters.Types, storage.EventType(t))
			}
		}
	}

	// 查询事件
	events, err := h.storage.GetStatusEvents(sinceID, limit+1, filters)
	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error("GetEvents 失败", "since_id", sinceID, "limit", limit, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询事件失败，请稍后再试")
		return
	}

	// 判断是否还有更多
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}

	// 计算下一个 since_id
	var nextSinceID int64
	if len(events) > 0 {
		nextSinceID = events[len(events)-1].ID
	} else {
		nextSinceID = sinceID
	}

	// 构建响应（附带按运行时配置 join 出的展示名）
	displayIndex := h.buildChannelDisplayIndex()
	items := make([]EventItem, 0, len(events))
	for _, e := range events {
		item := EventItem{
			ID:              e.ID,
			Provider:        e.Provider,
			Service:         e.Service,
			Channel:         e.Channel,
			Model:           e.Model,
			Type:            string(e.EventType),
			FromStatus:      e.FromStatus,
			ToStatus:        e.ToStatus,
			TriggerRecordID: e.TriggerRecordID,
			ObservedAt:      e.ObservedAt,
			CreatedAt:       e.CreatedAt,
			Meta:            e.Meta,
		}
		displayIndex.enrich(&item)
		items = append(items, item)
	}

	c.JSON(http.StatusOK, EventsResponse{
		Events: items,
		Meta: EventsMeta{
			NextSinceID: nextSinceID,
			HasMore:     hasMore,
			Count:       len(items),
		},
	})
}

// channelDisplay 单个 PSC 三元组的展示信息，由运行时配置派生。
type channelDisplay struct {
	name          string            // channel_name，仅当与 channel 标识不同时非空
	requestModels map[string]string // model 业务键 -> 实际请求的模型 ID
}

// channelDisplayIndex 按 (provider, service, channel) 索引的展示信息。
type channelDisplayIndex map[[3]string]*channelDisplay

// buildChannelDisplayIndex 扫描运行时配置构建展示信息索引。
//
// 键刻意**不做大小写归一**：监测项唯一性本身就是四元组原值比较
// （config.validateMonitorUniqueness），归一化会把两个合法的不同监测项折叠成一条；
// 而事件里那四个字段正是探测时从同一份配置原样写下去的，原值必然对得上。
// 只对 model 去首尾空白，且**两侧同步去**（见 eventModelKeys）——loader 不 trim
// `model`，配置里混进空格时若只有一侧 trim，join 会静默落空、悄悄退回业务键。
//
// 同一 PSC 的展示名取**首个非空**，与 /api/status 读路径（status_query_handler）
// 同口径，保证通知与站点显示的是同一个名字。
func (h *Handler) buildChannelDisplayIndex() channelDisplayIndex {
	h.cfgMu.RLock()
	defer h.cfgMu.RUnlock()

	index := make(channelDisplayIndex)
	for _, m := range h.config.Monitors {
		key := [3]string{m.Provider, m.Service, m.Channel}
		entry := index[key]
		if entry == nil {
			entry = &channelDisplay{requestModels: make(map[string]string)}
			index[key] = entry
		}
		if entry.name == "" && m.ChannelName != "" && m.ChannelName != m.Channel {
			entry.name = m.ChannelName
		}
		if model := strings.TrimSpace(m.Model); model != "" {
			entry.requestModels[model] = resolvedRequestModel(m)
		}
	}
	return index
}

// enrich 填充事件的展示字段。配置里查不到该通道/模型时逐项回退到原业务键——
// 通道下架或改名后的历史事件必然走这条路径，是预期行为不是异常。
func (idx channelDisplayIndex) enrich(item *EventItem) {
	entry := idx[[3]string{item.Provider, item.Service, item.Channel}]
	if entry != nil {
		item.ChannelName = entry.name
	}

	keys := eventModelKeys(item)
	if len(keys) == 0 {
		return
	}

	resolved := make([]string, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		name := key
		if entry != nil {
			if rm := entry.requestModels[key]; rm != "" {
				name = rm
			}
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		resolved = append(resolved, name)
	}
	sort.Strings(resolved)
	item.RequestModels = resolved
}

// eventModelKeys 取出事件涉及的全部 model 业务键：通道级事件的 Meta["models"]
// 与触发记录的 Model 取并集，与 notifier 既有的取名口径（extractModels）逐字一致。
// Meta["models"] 由 events.enrichChannelEventMeta 写入（DOWN 取 down_models、
// UP 取 up_models），元素与 Model 同为展示名业务键；经 DB 往返后是 []any。
func eventModelKeys(item *EventItem) []string {
	var keys []string
	seen := make(map[string]struct{}, 4)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, dup := seen[s]; dup {
			return
		}
		seen[s] = struct{}{}
		keys = append(keys, s)
	}

	switch models := item.Meta["models"].(type) {
	case []string:
		for _, m := range models {
			add(m)
		}
	case []any:
		for _, raw := range models {
			if m, ok := raw.(string); ok {
				add(m)
			}
		}
	}
	add(item.Model)

	return keys
}

// GetLatestEventID 获取最新事件ID
// GET /api/events/latest
func (h *Handler) GetLatestEventID(c *gin.Context) {
	// 检查 API Token（如果配置了）
	if !h.checkEventsAPIToken(c) {
		return
	}

	latestID, err := h.storage.GetLatestEventID()
	if err != nil {
		logger.FromContext(c.Request.Context(), "api").Error("GetLatestEventID 失败", "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询最新事件 ID 失败，请稍后再试")
		return
	}

	c.JSON(http.StatusOK, LatestEventResponse{
		LatestID: latestID,
	})
}

// checkEventsAPIToken 检查事件 API Token（强制鉴权）
// 如果未配置 api_token，返回 503 拒绝所有请求
// 返回 true 表示验证通过，false 表示验证失败（已返回错误响应）
func (h *Handler) checkEventsAPIToken(c *gin.Context) bool {
	h.cfgMu.RLock()
	apiToken := h.config.Events.APIToken
	h.cfgMu.RUnlock()

	// 未配置 token 时拒绝所有请求
	if apiToken == "" {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "events API 暂不可用")
		return false
	}

	// 验证 Authorization header
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "缺少 Authorization 请求头")
		return false
	}

	// 支持 "Bearer <token>" 格式
	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authorization 格式错误，应为 Bearer <token>")
		return false
	}

	token := strings.TrimPrefix(authHeader, bearerPrefix)
	// 使用恒定时间比较，防止时序攻击
	if subtle.ConstantTimeCompare([]byte(token), []byte(apiToken)) != 1 {
		apiError(c, http.StatusForbidden, ErrCodeForbidden, "API token 无效")
		return false
	}

	return true
}
