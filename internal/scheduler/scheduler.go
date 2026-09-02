package scheduler

import (
	"container/heap"
	"context"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor/internal/automove"
	"monitor/internal/config"
	"monitor/internal/events"
	"monitor/internal/identity"
	"monitor/internal/logger"
	"monitor/internal/monitor"
	"monitor/internal/storage"
)

// task 表示一个待调度的探测任务
type task struct {
	monitor  config.ServiceConfig // 监测配置
	interval time.Duration        // 该任务的巡检间隔
	nextRun  time.Time            // 下次执行时间
	index    int                  // 在堆中的索引（heap.Interface 需要）
	// generation 是本任务被创建时的调度器代次。dispatchDue 把任务弹出堆后会解锁执行探测，
	// 这段窗口内任务不在堆中；若期间发生 rebuildTasks，堆里已有该监测项的新任务，
	// 旧任务再无条件推回就会让同一监测项出现两份、每周期被探测两次。回填前比对代次即可丢弃旧任务。
	generation uint64
}

// 身份键的两个前缀必须隔开：ModelID 空缺时回退四元组，两套键不能互相碰撞。
const (
	taskIdentityModelIDPrefix = "mid\x00"
	taskIdentityLegacyPrefix  = "pscm\x00"
)

// taskIdentityKey 返回监测行的身份键，用于跨配置重建时认领同一个监测项。
//
// 优先用 ModelID（loader 派生的稳定行级 id，改展示名不变），为空时回退 PSCM 四元组。
// 不能只用 ModelID：config.validateModelIDs 明确允许空值（回填前的既有行），
// 且本包是库、单测构造的监测项本来就不填 ModelID——只有 cmd/server 的
// CheckRuntimeModelIDs 才保证非空。全部空值挤进同一个键会让整组误判为可继承。
func taskIdentityKey(m config.ServiceConfig) string {
	if id := strings.TrimSpace(m.ModelID); id != "" {
		return taskIdentityModelIDPrefix + id
	}
	return taskIdentityLegacyPrefix + m.Provider + "\x00" + m.Service + "\x00" + m.Channel + "\x00" + m.Model
}

// monitorPSCKey 构建 provider/service/channel 组合键。
// 分组与「重建时按组认领旧相位」两处必须用同一个构造，否则键一旦漂了继承会静默失效。
func monitorPSCKey(m config.ServiceConfig) string {
	return fmt.Sprintf("%s/%s/%s", m.Provider, m.Service, m.Channel)
}

// effectiveInterval 返回监测项的有效巡检间隔（未配置时回退调度器默认值）。
func effectiveInterval(m config.ServiceConfig, fallback time.Duration) time.Duration {
	if m.IntervalDuration == 0 {
		return fallback
	}
	return m.IntervalDuration
}

// taskSchedule 是旧任务的调度字段值快照。
//
// 刻意存值而不是 *task：一来 s.tasks[:0] 会复用堆的底层指针数组，二来 dispatchDue
// 在解锁状态下写被弹出任务的 nextRun，持有指针等于把一个可能被并发写的字段留在手里。
type taskSchedule struct {
	nextRun  time.Time
	interval time.Duration
}

// groupSchedule 是一个 PSC 组内全部旧任务的快照。
// duplicate 表示组内出现了重复身份键（正常配置不会发生，但库层不假设调用方已校验），
// 此时无法可靠地一一认领，整组放弃继承、退回错峰重排。
type groupSchedule struct {
	members   map[string]taskSchedule
	duplicate bool
}

// monitorGroup 表示一个多模型监测组
// 同一 provider/service/channel 下的多个 model 属于同一组
// 用于实现组间错峰、组内紧凑的调度策略
type monitorGroup struct {
	psc           string // provider/service/channel 组合键
	monitorIdxs   []int  // 组内监测项在 cfg.Monitors 中的索引（按 layer_order 排序）
	firstCfgIndex int    // 组内首个监测项的配置索引（用于组间排序）
}

// snapshotGroupSchedules 在清空任务堆之前，把现有任务按 PSC 组建索引。
// 必须在 s.tasks = s.tasks[:0] 之前调用。
func snapshotGroupSchedules(tasks taskHeap) map[string]*groupSchedule {
	if len(tasks) == 0 {
		return nil
	}
	index := make(map[string]*groupSchedule, len(tasks))
	for _, t := range tasks {
		if t == nil {
			continue
		}
		psc := monitorPSCKey(t.monitor)
		group, ok := index[psc]
		if !ok {
			group = &groupSchedule{members: make(map[string]taskSchedule, 4)}
			index[psc] = group
		}
		key := taskIdentityKey(t.monitor)
		if _, dup := group.members[key]; dup {
			group.duplicate = true
			continue
		}
		group.members[key] = taskSchedule{nextRun: t.nextRun, interval: t.interval}
	}
	return index
}

// canPreserveGroup 判断某个 PSC 组能否整体沿用旧的 nextRun。
//
// 粒度取「组」而非单个任务是刻意的：组内模型按固定 2s 间隔紧凑排布，多模型热力图靠
// 时间戳对齐渲染，只重排组内一个成员会让那一行错位出空洞。
// 三个条件缺一不可——组存在、成员身份键集合完全相同（按集合比，不依赖切片顺序）、
// 每个成员的有效 interval 未变（interval 变了旧相位就不再代表用户想要的节奏）。
func canPreserveGroup(cfg *config.AppConfig, group monitorGroup, fallback time.Duration, old *groupSchedule) bool {
	if old == nil || old.duplicate {
		return false
	}
	if len(group.monitorIdxs) != len(old.members) {
		return false
	}
	seen := make(map[string]struct{}, len(group.monitorIdxs))
	for _, monitorIdx := range group.monitorIdxs {
		m := cfg.Monitors[monitorIdx]
		key := taskIdentityKey(m)
		if _, dup := seen[key]; dup {
			return false
		}
		seen[key] = struct{}{}

		prev, ok := old.members[key]
		if !ok || prev.interval != effectiveInterval(m, fallback) {
			return false
		}
	}
	return true
}

// taskHeap 按下一次触发时间排序的最小堆
type taskHeap []*task

func (h taskHeap) Len() int           { return len(h) }
func (h taskHeap) Less(i, j int) bool { return h[i].nextRun.Before(h[j].nextRun) }
func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *taskHeap) Push(x any) {
	t := x.(*task)
	t.index = len(*h)
	*h = append(*h, t)
}

func (h *taskHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // 避免内存泄漏
	item.index = -1
	*h = old[:n-1]
	return item
}

// Scheduler 调度器（最小堆调度架构）
// 支持每个监测项独立的巡检间隔
type Scheduler struct {
	prober       *monitor.Prober
	eventService *events.Service   // 事件服务（可选）
	autoMover    *automove.Service // 自动移板服务（可选，用于 runtime cold 跳过）

	mu      sync.Mutex
	running bool
	timer   *time.Timer   // 单一定时器，等待最近任务
	tasks   taskHeap      // 任务最小堆
	sem     chan struct{} // 并发控制信号量
	wakeCh  chan struct{} // 唤醒信号（配置变更时）
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup // 追踪在途探测 goroutine

	// generation 每次重建任务堆（含 Stop）递增，用于识别并丢弃堆外窗口里的旧代任务，见 task.generation
	generation uint64
	// lastStaggerEnabled 上一次重建时组间错峰是否生效；切换时强制全量重排，
	// 否则关掉 stagger_probes 后旧任务会一直留着错峰相位、开关看起来没生效
	lastStaggerEnabled bool

	// 配置引用（支持热更新）
	cfg      *config.AppConfig
	cfgMu    sync.RWMutex
	fallback time.Duration // 默认巡检间隔（创建时传入）
}

// NewScheduler 创建调度器
func NewScheduler(store storage.RecordStorage, interval time.Duration, userIDMgr *identity.UserIDManager) *Scheduler {
	return &Scheduler{
		prober:   monitor.NewProber(store, userIDMgr),
		fallback: interval,
		wakeCh:   make(chan struct{}, 1),
	}
}

// SetEventService 设置事件服务
// 用于探测完成后检测状态变更并产生事件
func (s *Scheduler) SetEventService(svc *events.Service) {
	s.mu.Lock()
	s.eventService = svc
	s.mu.Unlock()
}

// SetAutoMover 设置自动移板服务。
// 调度器会基于 runtime cold override 跳过对应 PSC 的 root/子模型任务。
func (s *Scheduler) SetAutoMover(svc *automove.Service) {
	s.mu.Lock()
	s.autoMover = svc
	s.mu.Unlock()
}

// Start 启动调度器
func (s *Scheduler) Start(ctx context.Context, cfg *config.AppConfig) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.mu.Unlock()

	// 初始化事件服务的活跃模型索引（需在任务堆构建前完成，考虑 runtime cold override）
	s.updateActiveModels(cfg)

	// 保存初始配置并初始化任务堆（启动时错峰）
	s.rebuildTasks(cfg, true)

	// 启动调度循环
	go s.loop()

	logger.Info("scheduler", "调度器已启动", "monitors", len(cfg.Monitors))
}

// UpdateConfig 更新配置（热更新时调用）
func (s *Scheduler) UpdateConfig(cfg *config.AppConfig) {
	s.updateActiveModels(cfg)

	// 再重建任务堆（会唤醒调度循环，新任务可能立即执行）
	s.rebuildTasks(cfg, false)

	logger.Info("scheduler", "配置已更新，调度任务已重建")
}

// updateActiveModels 刷新事件服务的活跃模型索引（考虑 runtime cold override）
func (s *Scheduler) updateActiveModels(cfg *config.AppConfig) {
	if cfg == nil {
		return
	}

	s.mu.Lock()
	eventSvc := s.eventService
	autoMover := s.autoMover
	s.mu.Unlock()

	if eventSvc == nil || !eventSvc.IsEnabled() {
		return
	}

	monitors := cfg.Monitors
	if autoMover != nil {
		if overrides := autoMover.Overrides(); len(overrides) > 0 {
			monitors = automove.ApplyOverrides(monitors, overrides, cfg.AnnotationRules, cfg.IntervalDuration)
		}
	}
	eventSvc.UpdateActiveModels(monitors, cfg.Boards.Enabled)
}

// TriggerNow 立即触发所有任务的巡检
func (s *Scheduler) TriggerNow() {
	s.mu.Lock()
	if !s.running || len(s.tasks) == 0 {
		s.mu.Unlock()
		return
	}

	// 将所有任务的 nextRun 设为当前时间
	now := time.Now()
	for _, t := range s.tasks {
		t.nextRun = now
	}
	heap.Init(&s.tasks)
	s.resetTimerLocked()
	s.notifyWakeLocked()
	s.mu.Unlock()

	logger.Info("scheduler", "已触发即时巡检")
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}

	// 让堆外窗口里的旧代任务失效，避免 Stop 之后 dispatchDue 又把它推回堆里
	s.generation++

	// 停止定时器
	if s.timer != nil {
		if !s.timer.Stop() {
			select {
			case <-s.timer.C:
			default:
			}
		}
		s.timer = nil
	}

	s.running = false
	if s.cancel != nil {
		s.cancel()
	}
	// 唤醒 loop 以便退出
	s.notifyWakeLocked()
	s.mu.Unlock()

	// 等待在途探测 goroutine 完成
	s.wg.Wait()

	s.prober.Close()
	logger.Info("scheduler", "调度器已停止")
}

// isRuntimeCold 检查监测项是否被 runtime cold override 覆盖
func isRuntimeCold(autoMover *automove.Service, cfg *config.AppConfig, m config.ServiceConfig) bool {
	if cfg == nil || !cfg.Boards.Enabled || autoMover == nil {
		return false
	}
	return autoMover.IsCold(storage.MonitorKey{
		Provider: m.Provider,
		Service:  m.Service,
		Channel:  m.Channel,
		Model:    m.Model,
	})
}

// rebuildTasks 根据配置重建调度任务堆
// startup=true 时使用启动模式错峰（固定 2 秒间隔）
func (s *Scheduler) rebuildTasks(cfg *config.AppConfig, startup bool) {
	if cfg == nil {
		return
	}

	// 更新配置引用
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 代次必须在所有 return 路径之前递增：配置被切成空/全禁用时同样要让堆外的旧任务失效
	s.generation++

	// 必须赶在 s.tasks 被清空之前快照旧相位。startup 时堆本来就是空的，跳过省一次遍历
	var previousGroups map[string]*groupSchedule
	if !startup {
		previousGroups = snapshotGroupSchedules(s.tasks)
	}

	autoMover := s.autoMover

	monitorCount := len(cfg.Monitors)
	if monitorCount == 0 {
		s.lastStaggerEnabled = false
		s.tasks = s.tasks[:0]
		s.resetTimerLocked()
		s.notifyWakeLocked() // 唤醒 loop 以便重新检查状态
		return
	}

	// 统计禁用和冷板的监测项数量
	disabledCount := 0
	coldCount := 0
	for _, m := range cfg.Monitors {
		if m.Disabled {
			disabledCount++
			continue
		}
		// 冷板项：启用 boards 后不创建探测任务，仅展示历史数据（含 runtime cold）
		if cfg.Boards.Enabled && (m.Board == "cold" || isRuntimeCold(autoMover, cfg, m)) {
			coldCount++
		}
	}
	activeCount := monitorCount - disabledCount - coldCount

	// 如果所有监测项都被禁用或冷板，清空任务
	if activeCount == 0 {
		s.lastStaggerEnabled = false
		s.tasks = s.tasks[:0]
		s.resetTimerLocked()
		s.notifyWakeLocked()
		logger.Info("scheduler", "所有监测项已禁用/冷板，调度器无任务",
			"total", monitorCount, "disabled", disabledCount, "cold", coldCount)
		return
	}

	// 并发控制：-1 表示与活跃监测数持平；>0 为硬上限
	maxConcurrency := cfg.MaxConcurrency
	if maxConcurrency == -1 {
		maxConcurrency = activeCount
	}
	if maxConcurrency < 1 {
		maxConcurrency = 1
	}
	s.sem = make(chan struct{}, maxConcurrency)
	logger.Info("scheduler", "并发控制已更新",
		"max_concurrency", maxConcurrency, "total", monitorCount,
		"disabled", disabledCount, "active", activeCount)

	// 构建多模型监测组（按 provider/service/channel 分组，跳过 runtime cold）
	groups := buildMonitorGroupsWithColdSkip(cfg, autoMover)

	// 调度策略计算
	// 组间错峰：将各组均匀分布在巡检周期内（需 stagger_probes 开启且多于 1 组）
	// 组内紧凑：同一组的模型在 2s 间隔内顺序探测（始终生效）
	const intraGroupInterval = 2 * time.Second // 组内模型间隔固定 2 秒

	// 组间错峰：仅当配置开启且有多个组时生效
	useInterGroupStagger := cfg.ShouldStaggerProbes() && len(groups) > 1
	var groupBaseDelay, groupJitterRange time.Duration

	if useInterGroupStagger {
		minInterval := findMinIntervalWithAutoMover(cfg, autoMover)
		if startup {
			// 启动模式：优先填满最小巡检周期，必要时抬高以避免最坏抖动下组间重叠
			var maxIntraGroupWidth time.Duration
			groupBaseDelay, groupJitterRange, maxIntraGroupWidth = computeStartupStaggerParams(groups, intraGroupInterval, minInterval)
			totalSpread := groupBaseDelay * time.Duration(len(groups))
			if minInterval > 0 && totalSpread > minInterval {
				logger.Warn("scheduler", "启动模式组间错峰总展开超过最小巡检周期，可能跨周期重叠",
					"group_count", len(groups),
					"interval", minInterval,
					"group_base_delay", groupBaseDelay,
					"total_spread", totalSpread)
			}

			logger.Info("scheduler", "启动模式：探测将按组错峰执行",
				"group_count", len(groups),
				"interval", minInterval,
				"group_base_delay", groupBaseDelay,
				"group_jitter_range", groupJitterRange,
				"max_intra_group_width", maxIntraGroupWidth,
				"total_spread", totalSpread,
				"intra_group_interval", intraGroupInterval)
		} else {
			// 热更新模式：优先填满最小 interval，必要时抬高以避免最坏抖动下组间重叠
			if minInterval > 0 {
				const jitterRatioNum int64 = 1
				const jitterRatioDen int64 = 20 // ±5%

				idealBaseDelay := minInterval / time.Duration(len(groups))
				maxIntraGroupWidth := computeMaxIntraGroupWidth(groups, intraGroupInterval)
				requiredBaseDelay := computeRequiredBaseDelay(maxIntraGroupWidth, jitterRatioNum, jitterRatioDen)

				groupBaseDelay = idealBaseDelay
				if requiredBaseDelay > groupBaseDelay {
					groupBaseDelay = requiredBaseDelay
				}
				groupJitterRange = time.Duration(int64(groupBaseDelay) * jitterRatioNum / jitterRatioDen)

				totalSpread := groupBaseDelay * time.Duration(len(groups))
				if totalSpread > minInterval {
					logger.Warn("scheduler", "热更新模式组间错峰总展开超过最小巡检周期，可能跨周期重叠",
						"group_count", len(groups),
						"interval", minInterval,
						"group_base_delay", groupBaseDelay,
						"required_base_delay", requiredBaseDelay,
						"total_spread", totalSpread)
				}

				logger.Info("scheduler", "探测将按组错峰执行",
					"group_count", len(groups),
					"interval", minInterval,
					"group_base_delay", groupBaseDelay,
					"group_jitter_range", groupJitterRange,
					"max_intra_group_width", maxIntraGroupWidth,
					"total_spread", totalSpread,
					"intra_group_interval", intraGroupInterval)
			} else {
				useInterGroupStagger = false
			}
		}
	}

	// 检查组内展开宽度是否超过组间间隔（仅警告，不影响正确性）
	if useInterGroupStagger {
		for _, g := range groups {
			intraGroupWidth := time.Duration(len(g.monitorIdxs)-1) * intraGroupInterval
			if intraGroupWidth > groupBaseDelay && len(g.monitorIdxs) > 1 {
				logger.Warn("scheduler", "组内展开宽度超过组间间隔，可能导致组间重叠",
					"psc", g.psc, "models", len(g.monitorIdxs),
					"intra_group_width", intraGroupWidth, "group_base_delay", groupBaseDelay)
			}
		}
	}

	// 错峰开关切换时不继承任何旧相位，让开关立刻反映到全部任务上
	staggerModeChanged := !startup && useInterGroupStagger != s.lastStaggerEnabled

	// 构建任务堆
	s.tasks = s.tasks[:0]
	heap.Init(&s.tasks)
	now := time.Now()

	preservedGroups := 0

	// 按组遍历，实现组间错峰、组内紧凑
	for groupIdx, group := range groups {
		// 计算组的起始延迟（组间错峰 + 组级抖动）
		var groupDelay time.Duration
		if useInterGroupStagger {
			groupDelay = computeStaggerDelay(groupBaseDelay, groupJitterRange, groupIdx)
		}

		// 热更新时尽量沿用旧相位：错峰总展开可能远大于最短巡检周期（生产实测 95 组
		// 展开 10m33s、最短周期 2m30s），每次热更新都重排等于把短周期通道的下次探测
		// 推后好几个周期，热力图会因此缺块。组成员与各自 interval 都没变就没有重排的理由。
		var previous *groupSchedule
		preserveGroup := false
		if !startup && !staggerModeChanged {
			previous = previousGroups[group.psc]
			preserveGroup = canPreserveGroup(cfg, group, s.fallback, previous)
			if preserveGroup {
				preservedGroups++
			}
		}

		// 遍历组内监测项（按 layer_order 排序：父层优先）
		for intraIdx, monitorIdx := range group.monitorIdxs {
			m := cfg.Monitors[monitorIdx]
			interval := effectiveInterval(m, s.fallback)

			// 计算首次执行时间
			// 组内紧凑：始终生效，组内模型按 2s 间隔顺序探测
			// 组间错峰：仅当开启时应用组级延迟
			intraDelay := time.Duration(intraIdx) * intraGroupInterval
			nextRun := now.Add(intraDelay)
			if useInterGroupStagger {
				nextRun = now.Add(groupDelay + intraDelay)
			}

			switch {
			case preserveGroup:
				// canPreserveGroup 已核对过成员集合，正常必定命中。
				// 仍然判 ok：查不到时零值 time.Time 会让任务立刻到期，
				// 那是比「多等一轮」严重得多的静默故障，不值得省这一行。
				if prev, ok := previous.members[taskIdentityKey(m)]; ok {
					nextRun = prev.nextRun
				}
			case !startup:
				// 热更新里确实需要重排的组：首次延迟封顶到自己的 interval，
				// 否则缩短 interval 的通道反而要多等一个错峰展开才被探测。
				// 启动路径刻意不封顶——那时全部任务都要铺开填满周期。
				if delay := groupDelay + intraDelay; delay > interval {
					nextRun = now.Add(interval)
				}
			}

			heap.Push(&s.tasks, &task{
				monitor:    m,
				interval:   interval,
				nextRun:    nextRun,
				generation: s.generation,
			})
		}
	}

	if !startup {
		logger.Info("scheduler", "任务堆已重建",
			"groups", len(groups), "preserved_groups", preservedGroups,
			"replanned_groups", len(groups)-preservedGroups,
			"stagger_mode_changed", staggerModeChanged)
	}

	s.lastStaggerEnabled = useInterGroupStagger
	s.resetTimerLocked()
	s.notifyWakeLocked()
}

// findMinInterval 找到所有活跃监测项中最小的 interval（跳过已禁用和冷板的）
func (s *Scheduler) findMinInterval(cfg *config.AppConfig) time.Duration {
	s.mu.Lock()
	autoMover := s.autoMover
	s.mu.Unlock()
	return findMinIntervalWithAutoMover(cfg, autoMover)
}

func findMinIntervalWithAutoMover(cfg *config.AppConfig, autoMover *automove.Service) time.Duration {
	minInterval := cfg.IntervalDuration
	for _, m := range cfg.Monitors {
		if m.Disabled {
			continue
		}
		if cfg.Boards.Enabled && (m.Board == "cold" || isRuntimeCold(autoMover, cfg, m)) {
			continue
		}
		if m.IntervalDuration > 0 && (minInterval == 0 || m.IntervalDuration < minInterval) {
			minInterval = m.IntervalDuration
		}
	}
	return minInterval
}

// buildMonitorGroupsWithColdSkip 按 provider/service/channel 分组监测项，同时跳过 runtime cold
func buildMonitorGroupsWithColdSkip(cfg *config.AppConfig, autoMover *automove.Service) []monitorGroup {
	return buildMonitorGroupsFiltered(cfg, func(m config.ServiceConfig) bool {
		return isRuntimeCold(autoMover, cfg, m)
	})
}

// buildMonitorGroups 按 provider/service/channel 分组监测项
// 返回分组列表，组内按 layer_order 排序（父层优先），组间按首个配置索引排序
// 仅包含活跃监测项（跳过 disabled 和 cold board）
func buildMonitorGroups(cfg *config.AppConfig) []monitorGroup {
	return buildMonitorGroupsFiltered(cfg, nil)
}

// buildMonitorGroupsFiltered 分组监测项的核心实现，extraSkip 为可选的额外跳过判断
func buildMonitorGroupsFiltered(cfg *config.AppConfig, extraSkip func(config.ServiceConfig) bool) []monitorGroup {
	if len(cfg.Monitors) == 0 {
		return nil
	}

	// 临时结构：收集每个 PSC 下的监测项索引
	type pscEntry struct {
		idxs          []int
		firstCfgIndex int
	}
	pscMap := make(map[string]*pscEntry)

	for i, m := range cfg.Monitors {
		// 跳过已禁用的监测项
		if m.Disabled {
			continue
		}
		// 跳过冷板项（启用 boards 功能时）
		if cfg.Boards.Enabled && m.Board == "cold" {
			continue
		}
		// 跳过额外过滤的项（如 runtime cold）
		if extraSkip != nil && extraSkip(m) {
			continue
		}

		// 构建 PSC 键
		psc := monitorPSCKey(m)

		entry, exists := pscMap[psc]
		if !exists {
			entry = &pscEntry{
				idxs:          make([]int, 0, 4), // 预分配，大多数组 ≤4 个模型
				firstCfgIndex: i,
			}
			pscMap[psc] = entry
		}
		entry.idxs = append(entry.idxs, i)
	}

	if len(pscMap) == 0 {
		return nil
	}

	// 构建分组列表
	groups := make([]monitorGroup, 0, len(pscMap))
	for psc, entry := range pscMap {
		// 组内排序：按 layer_order（父层优先），相同时按配置索引
		// 父层 (Parent="") 的 layer_order 隐式为 0
		idxsCopy := make([]int, len(entry.idxs))
		copy(idxsCopy, entry.idxs)

		sort.SliceStable(idxsCopy, func(a, b int) bool {
			ma := &cfg.Monitors[idxsCopy[a]]
			mb := &cfg.Monitors[idxsCopy[b]]

			// 排序优先级：父层(0) < 子层(1)，相同层级按配置索引
			orderA := computeLayerOrder(ma)
			orderB := computeLayerOrder(mb)

			if orderA != orderB {
				return orderA < orderB
			}
			return idxsCopy[a] < idxsCopy[b]
		})

		groups = append(groups, monitorGroup{
			psc:           psc,
			monitorIdxs:   idxsCopy,
			firstCfgIndex: entry.firstCfgIndex,
		})
	}

	// 组间排序：按首个配置索引升序（确保确定性顺序）
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].firstCfgIndex < groups[j].firstCfgIndex
	})

	return groups
}

// computeLayerOrder 计算监测项的父/子层优先级
// 父层（Parent=""）返回 0，子层（有 Parent）返回 1
// 用于组内排序，确保父层优先调度
func computeLayerOrder(m *config.ServiceConfig) int {
	if strings.TrimSpace(m.Parent) == "" {
		return 0 // 父层
	}
	return 1 // 子层
}

// loop 调度主循环
func (s *Scheduler) loop() {
	for {
		s.mu.Lock()
		running := s.running
		timer := s.timer
		ctx := s.ctx
		s.mu.Unlock()

		if !running {
			return
		}

		var timerC <-chan time.Time
		if timer != nil {
			timerC = timer.C
		}

		select {
		case <-ctx.Done():
			s.Stop()
			return

		case <-timerC:
			// 定时器触发，执行到期任务
			s.dispatchDue()

		case <-s.wakeCh:
			// 配置变更唤醒，重新计算等待时间
			// 循环继续，会重新获取 timer
		}
	}
}

// dispatchDue 执行所有已到期的任务
func (s *Scheduler) dispatchDue() {
	for {
		s.mu.Lock()
		if len(s.tasks) == 0 {
			s.resetTimerLocked()
			s.mu.Unlock()
			return
		}

		// 检查堆顶任务是否到期
		next := s.tasks[0]
		now := time.Now()
		if next.nextRun.After(now) {
			// 最近任务未到期，重置定时器等待
			s.resetTimerLocked()
			s.mu.Unlock()
			return
		}

		// 弹出到期任务
		heap.Pop(&s.tasks)
		s.mu.Unlock()

		// 异步执行探测任务
		s.runTask(next)

		// 使用"至少间隔"语义：下次执行时间 = max(计划时间+interval, 当前时间+interval)
		// 避免探测耗时超过 interval 时快速补跑多个周期
		plannedNext := next.nextRun.Add(next.interval)
		minNext := time.Now().Add(next.interval)
		if plannedNext.Before(minNext) {
			next.nextRun = minNext
		} else {
			next.nextRun = plannedNext
		}

		// 重新入队。堆外窗口期间可能发生 rebuildTasks/Stop，此时堆里要么已有该监测项的
		// 新任务、要么整个调度已停；无条件推回会让同一监测项每周期被探测两次（见 task.generation）
		s.mu.Lock()
		if next.generation == s.generation && s.running {
			heap.Push(&s.tasks, next)
		}
		// 丢弃旧任务时同样要按当前堆重置定时器
		s.resetTimerLocked()
		s.mu.Unlock()
	}
}

// runTask 在并发控制下执行单个探测任务
func (s *Scheduler) runTask(t *task) {
	s.mu.Lock()
	ctx := s.ctx
	sem := s.sem
	eventSvc := s.eventService
	s.mu.Unlock()

	if ctx == nil || sem == nil {
		return
	}

	// 获取信号量
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	// 追踪在途 goroutine
	s.wg.Add(1)

	// 异步执行，释放信号量
	go func(m config.ServiceConfig) {
		defer s.wg.Done()
		defer func() { <-sem }()

		result := s.prober.Probe(ctx, &m)
		record, err := s.prober.SaveResult(result)
		if err != nil {
			logger.Error("scheduler", "保存结果失败",
				"provider", m.Provider, "service", m.Service, "channel", m.Channel, "model", m.Model, "error", err)
			return
		}

		// 事件检测（如果启用）
		if eventSvc != nil && eventSvc.IsEnabled() {
			if event, err := eventSvc.ProcessRecord(record); err != nil {
				logger.Error("scheduler", "事件检测失败",
					"provider", m.Provider, "service", m.Service, "channel", m.Channel, "model", m.Model, "error", err)
			} else if event != nil {
				logger.Info("scheduler", "检测到状态变更",
					"provider", m.Provider, "service", m.Service, "channel", m.Channel, "model", m.Model,
					"event_type", event.EventType, "from", event.FromStatus, "to", event.ToStatus)
			}
		}
	}(t.monitor)
}

// resetTimerLocked 重置定时器到下一个任务（需持有 s.mu）
func (s *Scheduler) resetTimerLocked() {
	if len(s.tasks) == 0 {
		// 无任务，停止定时器
		if s.timer != nil {
			if !s.timer.Stop() {
				select {
				case <-s.timer.C:
				default:
				}
			}
			s.timer = nil
		}
		return
	}

	// 计算等待时间
	wait := max(time.Until(s.tasks[0].nextRun), 0)

	if s.timer == nil {
		s.timer = time.NewTimer(wait)
		return
	}

	// 重置现有定时器
	if !s.timer.Stop() {
		select {
		case <-s.timer.C:
		default:
		}
	}
	s.timer.Reset(wait)
}

// notifyWakeLocked 唤醒调度循环（需持有 s.mu）
func (s *Scheduler) notifyWakeLocked() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
		// 已有唤醒信号，无需重复发送
	}
}

// computeMaxIntraGroupWidth 计算所有组中的最大组内展开宽度。
func computeMaxIntraGroupWidth(groups []monitorGroup, intraGroupInterval time.Duration) time.Duration {
	var maxWidth time.Duration
	for _, g := range groups {
		if len(g.monitorIdxs) <= 1 {
			continue
		}
		w := time.Duration(len(g.monitorIdxs)-1) * intraGroupInterval
		if w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

// computeRequiredBaseDelay 计算满足最坏抖动下相邻组不重叠所需的最小组间基准间隔。
// 要求：baseDelay - 2*jitterRange >= maxWidth
// jitterRange = baseDelay * (num/den)
// => baseDelay >= maxWidth * den / (den - 2*num)
func computeRequiredBaseDelay(maxIntraGroupWidth time.Duration, jitterRatioNum, jitterRatioDen int64) time.Duration {
	denom := jitterRatioDen - 2*jitterRatioNum
	if maxIntraGroupWidth <= 0 || denom <= 0 {
		return 0
	}
	numerator := int64(maxIntraGroupWidth) * jitterRatioDen
	return time.Duration((numerator + denom - 1) / denom)
}

// computeStartupStaggerParams 计算启动模式下的组间错峰参数（纯函数，便于测试）
// 目标：
//   - groupBaseDelay 优先填满 interval（interval / numGroups）
//   - 若多模型组内展开导致需要更大间隔，则以 requiredBaseDelay 为准
//   - 考虑最坏抖动时相邻组不重叠：baseDelay - 2*jitterRange >= maxIntraGroupWidth
//     其中 jitterRange = baseDelay * jitterRatio（启动模式固定 ±10%）
func computeStartupStaggerParams(groups []monitorGroup, intraGroupInterval, interval time.Duration) (groupBaseDelay, groupJitterRange, maxIntraGroupWidth time.Duration) {
	const jitterRatioNum int64 = 1
	const jitterRatioDen int64 = 10 // ±10%

	maxIntraGroupWidth = computeMaxIntraGroupWidth(groups, intraGroupInterval)
	requiredBaseDelay := computeRequiredBaseDelay(maxIntraGroupWidth, jitterRatioNum, jitterRatioDen)

	// 优先按 interval / numGroups 填满周期
	if len(groups) > 0 {
		groupBaseDelay = interval / time.Duration(len(groups))
	}
	if requiredBaseDelay > groupBaseDelay {
		groupBaseDelay = requiredBaseDelay
	}

	// ±10% 抖动范围
	groupJitterRange = time.Duration(int64(groupBaseDelay) * jitterRatioNum / jitterRatioDen)
	return groupBaseDelay, groupJitterRange, maxIntraGroupWidth
}

// computeStaggerDelay 计算错峰延迟时间
// 基准延迟 = baseDelay * index
// 抖动范围由调用方指定（通常为启动模式 ±10%，热更新模式 ±5%）
// 注意：使用全局 rand（Go 1.20+ 并发安全）
func computeStaggerDelay(baseDelay, jitterRange time.Duration, index int) time.Duration {
	delay := baseDelay * time.Duration(index)
	if jitterRange <= 0 {
		if delay < 0 {
			return 0
		}
		return delay
	}

	max := int64(jitterRange)
	if max <= 0 {
		if delay < 0 {
			return 0
		}
		return delay
	}

	// 随机抖动：±jitterRange（使用全局 rand，Go 1.20+ 并发安全）
	offset := rand.Int63n(max*2+1) - max
	delay += time.Duration(offset)
	if delay < 0 {
		return 0
	}
	return delay
}
