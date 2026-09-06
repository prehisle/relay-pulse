package poller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"notifier/internal/config"
	"notifier/internal/storage"
)

// Event 状态变更事件（来自 relay-pulse /api/events）
// 字段与 internal/api/events_handler.go EventItem 保持一致
//
// ⚠️ Channel 与 Model 是标识键不是人话名：Channel 是通道标识（O-web）、Model 是
// 展示名兼 DB 业务键（GPT）。站点上显示的那两个串由 API 端 join 运行时配置后
// 经 ChannelName / RequestModels 下发，本服务只负责优先渲染它们、缺省时回退。
type Event struct {
	ID              int64          `json:"id"`
	Provider        string         `json:"provider"`
	Service         string         `json:"service"`
	Channel         string         `json:"channel,omitempty"`
	Model           string         `json:"model,omitempty"`
	ChannelName     string         `json:"channel_name,omitempty"`   // 通道展示名（缺省时回退 Channel）
	RequestModels   []string       `json:"request_models,omitempty"` // 实际请求的模型 ID（缺省时回退 Model）
	Type            string         `json:"type"`                     // DOWN 或 UP
	FromStatus      int            `json:"from_status"`              // 变更前状态
	ToStatus        int            `json:"to_status"`                // 变更后状态
	TriggerRecordID int64          `json:"trigger_record_id"`        // 触发记录ID
	ObservedAt      int64          `json:"observed_at"`              // 事件发生时间（Unix秒）
	CreatedAt       int64          `json:"created_at"`               // 记录创建时间
	Meta            map[string]any `json:"meta,omitempty"`           // 附加信息
}

// EventsResponse /api/events 响应
type EventsResponse struct {
	Events []Event `json:"events"`
	Meta   struct {
		NextSinceID int64 `json:"next_since_id"` // 下一次轮询的游标
		HasMore     bool  `json:"has_more"`      // 是否还有更多事件
		Count       int   `json:"count"`         // 本次返回的事件数
	} `json:"meta"`
}

// EventHandler 事件处理回调
type EventHandler func(ctx context.Context, event *Event) error

// Poller 事件轮询器
type Poller struct {
	cfg        *config.Config
	storage    storage.Storage
	httpClient *http.Client
	handler    EventHandler

	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
}

// NewPoller 创建轮询器
func NewPoller(cfg *config.Config, store storage.Storage, handler EventHandler) *Poller {
	return &Poller{
		cfg:     cfg,
		storage: store,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		handler:  handler,
		stopChan: make(chan struct{}),
	}
}

// Start 启动轮询
func (p *Poller) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return fmt.Errorf("轮询器已在运行")
	}
	p.running = true
	p.stopChan = make(chan struct{})
	p.mu.Unlock()

	slog.Info("事件轮询器启动",
		"events_url", p.cfg.RelayPulse.EventsURL,
		"poll_interval", p.cfg.RelayPulse.PollInterval,
	)

	ticker := time.NewTicker(p.cfg.RelayPulse.PollInterval)
	defer ticker.Stop()

	// 立即执行一次
	p.poll(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("轮询器收到停止信号")
			return ctx.Err()
		case <-p.stopChan:
			slog.Info("轮询器停止")
			return nil
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

// Stop 停止轮询
func (p *Poller) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		close(p.stopChan)
		p.running = false
	}
}

// poll 执行一次轮询
func (p *Poller) poll(ctx context.Context) {
	// 获取游标
	cursor, err := p.storage.GetCursor(ctx)
	if err != nil {
		slog.Error("获取游标失败", "error", err)
		return
	}

	// 获取事件
	events, err := p.fetchEvents(ctx, cursor)
	if err != nil {
		slog.Warn("获取事件失败", "error", err)
		return
	}

	if len(events) == 0 {
		return
	}

	slog.Debug("获取到新事件", "count", len(events), "since_id", cursor)

	// 处理事件
	var maxID int64 = cursor
	for _, event := range events {
		if err := p.handler(ctx, &event); err != nil {
			slog.Error("处理事件失败", "event_id", event.ID, "error", err)
			continue
		}

		if event.ID > maxID {
			maxID = event.ID
		}
	}

	// 更新游标
	if maxID > cursor {
		if err := p.storage.UpdateCursor(ctx, maxID); err != nil {
			slog.Error("更新游标失败", "error", err)
		}
	}
}

// fetchEvents 从 relay-pulse 获取事件
func (p *Poller) fetchEvents(ctx context.Context, sinceID int64) ([]Event, error) {
	url := p.cfg.RelayPulse.EventsURL + "?since_id=" + strconv.FormatInt(sinceID, 10)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}

	// 添加 API Token（如果配置了）
	if p.cfg.RelayPulse.APIToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.RelayPulse.APIToken)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("API Token 无效或缺失")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API 返回错误 [%d]: %s", resp.StatusCode, string(body))
	}

	var eventsResp EventsResponse
	if err := json.NewDecoder(resp.Body).Decode(&eventsResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return eventsResp.Events, nil
}
