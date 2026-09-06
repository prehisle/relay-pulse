package notifier

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"notifier/internal/config"
	"notifier/internal/poller"
	"notifier/internal/qq"
	"notifier/internal/storage"
	"notifier/internal/telegram"
)

// aggregateKey 事件聚合键：按 provider/service/channel/event_type 分组
type aggregateKey struct {
	Provider  string
	Service   string
	Channel   string
	EventType string
}

// eventAggregate 事件聚合缓冲区
type eventAggregate struct {
	firstAt time.Time           // 首个事件到达时间
	timer   *time.Timer         // 聚合窗口定时器
	base    *poller.Event       // 基准事件（用于构建通知）
	models  map[string]struct{} // 收集的所有 model 业务键
	// rendered 收集各事件已解析出的展示模型名（正常情况下是真实请求模型 ID）。
	// 必须与 models 平行收集：只从基准事件取会丢掉同窗口内后续事件的模型。
	rendered map[string]struct{}
}

// Sender 通知发送器（多平台）
type Sender struct {
	cfg      *config.Config
	storage  storage.Storage
	channels map[string]ChannelSender // 平台注册表

	// 平台独立限流器
	tgRateLimiter *time.Ticker
	qqRateLimiter *time.Ticker
	qqJitterMin   time.Duration
	qqJitterMax   time.Duration

	mu       sync.Mutex
	running  bool
	stopChan chan struct{}
	baseCtx  context.Context // 保存启动时的 context，供定时器回调使用

	// 事件聚合：按 provider/service/channel/event_type 分组
	// 同一监测组下多个 model 的事件会在时间窗口内合并为一条通知
	aggWindow time.Duration
	aggMu     sync.Mutex
	aggBuf    map[aggregateKey]*eventAggregate
}

// DefaultAggregateWindow 默认事件聚合窗口时长
// 同一监测组（provider/service/channel）下的多个 model 事件
// 在此时间窗口内会合并为一条通知
//
// 窗口计算依据：
// - 组内探测间隔：2秒/model，6 个 model 需要 10 秒启动完
// - 慢响应时间：可能高达 10 秒
// - poller 轮询间隔：5 秒
// - 最坏情况：10s（启动间隔）+ 10s（慢响应）+ 5s（轮询对齐）= 25s
// - 取 30 秒留有余量
const DefaultAggregateWindow = 30 * time.Second

// NewSender 创建发送器
func NewSender(cfg *config.Config, store storage.Storage) *Sender {
	// 计算限流间隔
	tgRPS := cfg.Limits.TelegramRateLimitPerSecond
	if tgRPS <= 0 {
		tgRPS = 25
	}
	qqRPS := cfg.Limits.QQRateLimitPerSecond
	if qqRPS <= 0 {
		qqRPS = 2
	}

	s := &Sender{
		cfg:           cfg,
		storage:       store,
		channels:      make(map[string]ChannelSender),
		tgRateLimiter: time.NewTicker(time.Second / time.Duration(tgRPS)),
		qqRateLimiter: time.NewTicker(time.Second / time.Duration(qqRPS)),
		qqJitterMin:   cfg.Limits.QQJitterMin,
		qqJitterMax:   cfg.Limits.QQJitterMax,
		stopChan:      make(chan struct{}),
		aggWindow:     DefaultAggregateWindow,
		aggBuf:        make(map[aggregateKey]*eventAggregate),
	}

	// 注册渠道发送器
	if cfg.HasTelegramToken() {
		tg := &telegramChannel{
			client:  telegram.NewClient(cfg.Telegram.BotToken),
			storage: store,
		}
		s.channels[tg.Platform()] = tg
	}
	if cfg.HasQQ() {
		qqCh := &qqChannel{
			client: qq.NewClient(cfg.QQ.OneBotHTTPURL, cfg.QQ.AccessToken),
		}
		s.channels[qqCh.Platform()] = qqCh
	}

	return s
}

// Start 启动发送器
func (s *Sender) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("发送器已在运行")
	}
	s.running = true
	s.stopChan = make(chan struct{})
	s.baseCtx = ctx
	s.mu.Unlock()

	slog.Info("通知发送器启动",
		"telegram_rate_limit", s.cfg.Limits.TelegramRateLimitPerSecond,
		"qq_rate_limit", s.cfg.Limits.QQRateLimitPerSecond,
		"qq_jitter_min", s.qqJitterMin,
		"qq_jitter_max", s.qqJitterMax,
		"telegram_enabled", s.channels[storage.PlatformTelegram] != nil,
		"qq_enabled", s.channels[storage.PlatformQQ] != nil,
		"aggregate_window", s.aggWindow,
	)

	// 启动重试处理
	go s.retryLoop(ctx)

	<-ctx.Done()
	return ctx.Err()
}

// Stop 停止发送器
func (s *Sender) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopChan)
	s.mu.Unlock()

	// 在释放 mu 锁后再 flush，避免死锁
	// 先 flush 再停止 rateLimiter，否则 sendNotification 会阻塞在等待 tick
	s.flushAllAggregates()

	// 最后停止限流器
	if s.tgRateLimiter != nil {
		s.tgRateLimiter.Stop()
	}
	if s.qqRateLimiter != nil {
		s.qqRateLimiter.Stop()
	}
}

// flushAllAggregates 刷新所有聚合缓冲区（用于优雅关闭）
func (s *Sender) flushAllAggregates() {
	s.aggMu.Lock()
	keys := make([]aggregateKey, 0, len(s.aggBuf))
	for k, agg := range s.aggBuf {
		if agg.timer != nil {
			agg.timer.Stop()
		}
		keys = append(keys, k)
	}
	s.aggMu.Unlock()

	// 立即 flush 所有待发送的聚合事件
	for _, key := range keys {
		s.flushAggregate(key)
	}
}

// HandleEvent 处理事件（由 Poller 调用）
// 事件会先进入聚合缓冲区，等待时间窗口结束后合并发送
// 这样同一监测组下多个 model 的 DOWN/UP 事件会合并为一条通知
func (s *Sender) HandleEvent(ctx context.Context, event *poller.Event) error {
	if event == nil {
		return nil
	}

	key := aggregateKey{
		Provider:  event.Provider,
		Service:   event.Service,
		Channel:   event.Channel,
		EventType: event.Type,
	}

	now := time.Now()

	// 在锁外解析展示模型名：纯函数、只读 event，没必要占着 aggMu
	rendered := extractModels(event)

	s.aggMu.Lock()
	agg := s.aggBuf[key]
	if agg == nil {
		// 首个事件：创建聚合缓冲区并启动定时器
		// 注意是浅拷贝，Meta 等引用类型仍与原事件共享，flush 时再各自复制
		baseCopy := *event
		agg = &eventAggregate{
			firstAt:  now,
			base:     &baseCopy,
			models:   make(map[string]struct{}),
			rendered: make(map[string]struct{}),
		}
		agg.timer = time.AfterFunc(s.aggWindow, func() {
			s.flushAggregate(key)
		})
		s.aggBuf[key] = agg

		slog.Debug("事件聚合开始",
			"provider", event.Provider,
			"service", event.Service,
			"channel", event.Channel,
			"type", event.Type,
			"window", s.aggWindow,
		)
	}

	// 收集 model（去重）
	if model := strings.TrimSpace(event.Model); model != "" {
		agg.models[model] = struct{}{}
	}
	for _, model := range rendered {
		agg.rendered[model] = struct{}{}
	}
	s.aggMu.Unlock()

	return nil
}

// mergedEvent 由基准事件与窗口内收集到的模型集合构建待发送事件。
// 抽成方法而非内联在 flushAggregate 里，是为了让「多事件合并成一条通知」
// 这段逻辑能脱离 storage 与发送链路单测。
func (agg *eventAggregate) mergedEvent() poller.Event {
	models := sortedNonEmpty(agg.models)
	rendered := sortedNonEmpty(agg.rendered)

	merged := *agg.base

	// base 是浅拷贝，Meta 仍与原事件共享，写入前先复制一份
	metaCopy := make(map[string]any, len(merged.Meta)+1)
	for k, v := range merged.Meta {
		metaCopy[k] = v
	}
	merged.Meta = metaCopy
	if len(models) > 0 {
		merged.Meta["models"] = models
	}

	// 基准事件只带自己那一个模型，这里换成整个窗口的并集；
	// 窗口内一个都没解析出来时留 nil，让 extractModels 照旧回退到 Meta / Model。
	merged.RequestModels = nil
	if len(rendered) > 0 {
		merged.RequestModels = rendered
	}

	return merged
}

// sortedNonEmpty 返回集合中非空白元素的字典序切片。
func sortedNonEmpty(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for s := range set {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// flushAggregate 刷新聚合缓冲区，发送合并后的通知
func (s *Sender) flushAggregate(key aggregateKey) {
	s.aggMu.Lock()
	agg := s.aggBuf[key]
	if agg == nil {
		s.aggMu.Unlock()
		return
	}
	delete(s.aggBuf, key)
	if agg.timer != nil {
		agg.timer.Stop()
	}
	s.aggMu.Unlock()

	merged := agg.mergedEvent()

	slog.Info("事件聚合完成",
		"provider", key.Provider,
		"service", key.Service,
		"channel", key.Channel,
		"type", key.EventType,
		"models", merged.Meta["models"],
		"rendered_models", merged.RequestModels,
	)

	// 获取发送用的 context
	sendCtx := s.getSendContext()
	if err := s.dispatchEvent(sendCtx, &merged); err != nil {
		slog.Error("聚合事件发送失败",
			"provider", key.Provider,
			"service", key.Service,
			"channel", key.Channel,
			"type", key.EventType,
			"error", err,
		)
	}
}

// getSendContext 获取用于发送通知的 context
// 如果服务已停止或 baseCtx 已取消，返回 Background context 确保 flush 能完成
func (s *Sender) getSendContext() context.Context {
	s.mu.Lock()
	running := s.running
	ctx := s.baseCtx
	s.mu.Unlock()

	// 服务已停止时，使用 Background 确保 flush 能完成发送
	if !running {
		return context.Background()
	}

	if ctx != nil && ctx.Err() == nil {
		return ctx
	}
	return context.Background()
}

// dispatchEvent 分发事件通知给所有订阅者
func (s *Sender) dispatchEvent(ctx context.Context, event *poller.Event) error {
	// 查找订阅者（返回 platform + chatID）
	subscribers, err := s.storage.GetSubscribersByMonitor(ctx, event.Provider, event.Service, event.Channel)
	if err != nil {
		return fmt.Errorf("查询订阅者失败: %w", err)
	}

	if len(subscribers) == 0 {
		return nil
	}

	slog.Info("分发事件通知",
		"event_id", event.ID,
		"provider", event.Provider,
		"service", event.Service,
		"subscribers", len(subscribers),
	)

	// 为每个订阅者创建投递记录并发送
	for _, ref := range subscribers {
		delivery := &storage.Delivery{
			EventID:  event.ID,
			Platform: ref.Platform,
			ChatID:   ref.ChatID,
			Status:   storage.DeliveryStatusPending,
		}

		// 创建投递记录（幂等）
		if err := s.storage.CreateDelivery(ctx, delivery); err != nil {
			slog.Warn("创建投递记录失败",
				"event_id", event.ID,
				"platform", ref.Platform,
				"chat_id", ref.ChatID,
				"error", err,
			)
			continue
		}

		// 异步发送
		go s.sendNotification(ctx, delivery, event)
	}

	return nil
}

// sleepWithContext 带 context 的 sleep，返回 true 表示正常完成，false 表示被取消
func (s *Sender) sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-s.stopChan:
		// 服务正在关闭，跳过等待
		return true
	case <-t.C:
		return true
	}
}

// waitPlatformRateLimit 等待平台限流，返回 true 表示可以发送，false 表示应该放弃
func (s *Sender) waitPlatformRateLimit(ctx context.Context, platform string) bool {
	// 先检查 context 是否已取消
	select {
	case <-ctx.Done():
		return false
	case <-s.stopChan:
		// 服务正在关闭，跳过限流直接发送
		return true
	default:
	}

	// 根据平台选择限流器
	var limiter <-chan time.Time
	switch platform {
	case storage.PlatformTelegram:
		if s.tgRateLimiter == nil {
			return true
		}
		limiter = s.tgRateLimiter.C
	case storage.PlatformQQ:
		if s.qqRateLimiter == nil {
			return true
		}
		limiter = s.qqRateLimiter.C
	default:
		// 未知平台不做限流
		return true
	}

	// 等待限流
	select {
	case <-ctx.Done():
		return false
	case <-s.stopChan:
		// 服务正在关闭，跳过限流直接发送
		return true
	case <-limiter:
	}

	// QQ 额外抖动：进一步错峰，降低风控
	if platform == storage.PlatformQQ && s.qqJitterMax > 0 {
		min := s.qqJitterMin
		max := s.qqJitterMax
		if max < min {
			min, max = max, min
		}
		jitter := min
		if max > min {
			jitter += time.Duration(rand.Int63n(int64(max - min + 1)))
		}
		return s.sleepWithContext(ctx, jitter)
	}

	return true
}

// getChannel 从注册表获取渠道发送器，未注册时返回错误（保持与旧实现一致的错误语义）。
func (s *Sender) getChannel(platform string) (ChannelSender, error) {
	if ch, ok := s.channels[platform]; ok {
		return ch, nil
	}
	switch platform {
	case storage.PlatformTelegram:
		return nil, fmt.Errorf("telegram client not configured")
	case storage.PlatformQQ:
		return nil, fmt.Errorf("qq client not configured")
	default:
		return nil, fmt.Errorf("unknown platform: %s", platform)
	}
}

// sendNotification 发送单条通知（通过注册表路由到具体渠道）
func (s *Sender) sendNotification(ctx context.Context, delivery *storage.Delivery, event *poller.Event) {
	ch, err := s.getChannel(delivery.Platform)
	if err != nil {
		s.handleSendError(ctx, delivery, err)
		return
	}

	// 等待平台限流
	if !s.waitPlatformRateLimit(ctx, delivery.Platform) {
		return
	}

	message := ch.FormatMessage(event)
	messageID, err := ch.Send(ctx, delivery.ChatID, message)
	if err != nil {
		s.handleSendError(ctx, delivery, err)
		return
	}

	// 发送成功
	if err := s.storage.UpdateDeliveryStatus(ctx, delivery.ID, storage.DeliveryStatusSent, messageID, ""); err != nil {
		slog.Error("更新投递状态失败", "error", err)
	}
}

// displayChannel 返回通知里该用的通道名：优先 API 富化出的展示名，
// 缺省时回退到通道标识（历史事件、通道已下架、或对端是旧版 relay-pulse）。
func displayChannel(event *poller.Event) string {
	if event == nil {
		return ""
	}
	if name := strings.TrimSpace(event.ChannelName); name != "" {
		return name
	}
	return event.Channel
}

// extractModels 从事件中提取所有 model 信息
// 优先级：RequestModels > Meta["models"] ∪ event.Model
func extractModels(event *poller.Event) []string {
	if event == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var models []string

	// RequestModels 是 API 端按运行时配置映射出的真实请求模型 ID，服务端已按
	// 「Meta["models"] ∪ Model」同一口径取过并集并去重，故命中即返回、不再并入
	// 下面两个来源——否则 gpt-6-astra 会和它的业务键 GPT 一起出现在同一条通知里。
	if len(event.RequestModels) > 0 {
		for _, m := range event.RequestModels {
			m = strings.TrimSpace(m)
			if m == "" {
				continue
			}
			if _, exists := seen[m]; exists {
				continue
			}
			seen[m] = struct{}{}
			models = append(models, m)
		}
		if len(models) > 0 {
			if len(models) > 1 {
				sort.Strings(models)
			}
			return models
		}
	}

	// 从 Meta["models"] 读取（聚合后的事件会设置这个字段）
	if event.Meta != nil {
		if v, ok := event.Meta["models"]; ok {
			switch t := v.(type) {
			case []string:
				for _, m := range t {
					m = strings.TrimSpace(m)
					if m == "" {
						continue
					}
					if _, exists := seen[m]; exists {
						continue
					}
					seen[m] = struct{}{}
					models = append(models, m)
				}
			case []any:
				// JSON 解码后可能是 []any 类型
				for _, raw := range t {
					m, ok := raw.(string)
					if !ok {
						continue
					}
					m = strings.TrimSpace(m)
					if m == "" {
						continue
					}
					if _, exists := seen[m]; exists {
						continue
					}
					seen[m] = struct{}{}
					models = append(models, m)
				}
			}
		}
	}

	// 回退到单个 event.Model（兼容未聚合的事件）
	if m := strings.TrimSpace(event.Model); m != "" {
		if _, exists := seen[m]; !exists {
			seen[m] = struct{}{}
			models = append(models, m)
		}
	}

	// 保持稳定排序
	if len(models) > 1 {
		sort.Strings(models)
	}

	return models
}

// handleSendError 处理发送错误：先委托渠道做平台特定处理，再走通用重试。
func (s *Sender) handleSendError(ctx context.Context, delivery *storage.Delivery, sendErr error) {
	slog.Warn("发送通知失败",
		"delivery_id", delivery.ID,
		"platform", delivery.Platform,
		"chat_id", delivery.ChatID,
		"error", sendErr,
	)

	// 渠道特定错误处理（如 Telegram blocked 检测）
	if ch, ok := s.channels[delivery.Platform]; ok {
		if ch.HandleSendError(ctx, delivery, sendErr) {
			return // 渠道已处理（终态）
		}
	}

	// 通用重试逻辑
	if err := s.storage.IncrementRetryCount(ctx, delivery.ID); err != nil {
		slog.Error("增加重试计数失败", "error", err)
	}

	if err := s.storage.UpdateDeliveryStatus(ctx, delivery.ID, storage.DeliveryStatusPending, "", sendErr.Error()); err != nil {
		slog.Error("更新投递状态失败", "error", err)
	}
}

// retryLoop 重试失败的投递
func (s *Sender) retryLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.processRetries(ctx)
		}
	}
}

// processRetries 处理重试
func (s *Sender) processRetries(ctx context.Context) {
	deliveries, err := s.storage.GetPendingDeliveries(ctx, 100)
	if err != nil {
		slog.Error("获取待重试投递失败", "error", err)
		return
	}

	for _, delivery := range deliveries {
		// 检查重试次数
		if delivery.RetryCount >= s.cfg.Limits.MaxRetries {
			// 超过最大重试次数，标记为失败
			if err := s.storage.UpdateDeliveryStatus(ctx, delivery.ID, storage.DeliveryStatusFailed, "", "max retries exceeded"); err != nil {
				slog.Error("更新投递状态失败", "error", err)
			}
			continue
		}

		// 重新发送
		go s.retryDelivery(ctx, delivery)
	}
}

// retryDelivery 重试单条投递
func (s *Sender) retryDelivery(ctx context.Context, delivery *storage.Delivery) {
	ch, err := s.getChannel(delivery.Platform)
	if err != nil {
		slog.Warn("重试投递失败",
			"delivery_id", delivery.ID,
			"platform", delivery.Platform,
			"chat_id", delivery.ChatID,
			"retry_count", delivery.RetryCount,
			"error", err,
		)
		if incErr := s.storage.IncrementRetryCount(ctx, delivery.ID); incErr != nil {
			slog.Error("增加重试计数失败", "error", incErr)
		}
		return
	}

	// 等待平台限流
	if !s.waitPlatformRateLimit(ctx, delivery.Platform) {
		return
	}

	msg := fmt.Sprintf("🔔 通知重试 (event_id: %d)\n\n如果您持续收到此消息，请检查订阅设置。", delivery.EventID)
	messageID, err := ch.Send(ctx, delivery.ChatID, msg)
	if err != nil {
		slog.Warn("重试投递失败",
			"delivery_id", delivery.ID,
			"platform", delivery.Platform,
			"chat_id", delivery.ChatID,
			"retry_count", delivery.RetryCount,
			"error", err,
		)

		// 渠道特定错误处理
		if ch.HandleSendError(ctx, delivery, err) {
			return
		}

		if err := s.storage.IncrementRetryCount(ctx, delivery.ID); err != nil {
			slog.Error("增加重试计数失败", "error", err)
		}
		return
	}

	if err := s.storage.UpdateDeliveryStatus(ctx, delivery.ID, storage.DeliveryStatusSent, messageID, ""); err != nil {
		slog.Error("更新投递状态失败", "error", err)
	}
}
