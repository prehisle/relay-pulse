package scheduler

import (
	"context"
	"log"
	"sync"
	"time"

	"monitor/internal/config"
	"monitor/internal/monitor"
	"monitor/internal/storage"
)

// Scheduler 调度器
type Scheduler struct {
	prober   *monitor.Prober
	interval time.Duration
	ticker   *time.Ticker
	running  bool
	mu       sync.Mutex

	// 配置引用（支持热更新）
	cfg   *config.AppConfig
	cfgMu sync.RWMutex

	// 防止重复触发
	checkInProgress bool
	checkMu         sync.Mutex

	// 保存context用于TriggerNow
	ctx context.Context
}

// NewScheduler 创建调度器
func NewScheduler(store storage.Storage, interval time.Duration) *Scheduler {
	return &Scheduler{
		prober:   monitor.NewProber(store),
		interval: interval,
	}
}

// Start 启动调度器
func (s *Scheduler) Start(ctx context.Context, cfg *config.AppConfig) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.ticker = time.NewTicker(s.interval)
	s.ctx = ctx // 保存context用于TriggerNow

	// 保存初始配置
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	s.mu.Unlock()

	// 立即执行一次
	go s.runChecks(ctx)

	// 定时执行
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("[Scheduler] 调度器已停止")
				s.mu.Lock()
				s.ticker.Stop()
				s.running = false
				s.mu.Unlock()
				return

			case <-s.ticker.C:
				s.runChecks(ctx)
			}
		}
	}()

	log.Printf("[Scheduler] 调度器已启动，间隔: %v", s.interval)
}

// runChecks 执行所有检查（防重复）
func (s *Scheduler) runChecks(ctx context.Context) {
	// 防止重复执行
	s.checkMu.Lock()
	if s.checkInProgress {
		log.Println("[Scheduler] 上一轮检查尚未完成，跳过本次")
		s.checkMu.Unlock()
		return
	}
	s.checkInProgress = true
	s.checkMu.Unlock()

	defer func() {
		s.checkMu.Lock()
		s.checkInProgress = false
		s.checkMu.Unlock()
	}()

	// 获取当前配置（支持热更新）
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()

	if cfg == nil || len(cfg.Monitors) == 0 {
		return
	}

	log.Printf("[Scheduler] 开始巡检 %d 个监控项", len(cfg.Monitors))

	var wg sync.WaitGroup
	// 限制并发数
	sem := make(chan struct{}, 10)

	for _, task := range cfg.Monitors {
		wg.Add(1)
		go func(t config.ServiceConfig) {
			defer wg.Done()

			// 获取信号量
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			// 执行探测
			result := s.prober.Probe(ctx, &t)

			// 保存结果
			if err := s.prober.SaveResult(result); err != nil {
				log.Printf("[Scheduler] 保存结果失败 %s-%s: %v",
					t.Provider, t.Service, err)
			}
		}(task)
	}

	wg.Wait()
	log.Println("[Scheduler] 巡检完成")
}

// UpdateConfig 更新配置（热更新时调用）
func (s *Scheduler) UpdateConfig(cfg *config.AppConfig) {
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()

	// 如果配置中带有新的巡检间隔，动态调整 ticker
	if cfg.IntervalDuration > 0 {
		s.mu.Lock()
		if s.interval != cfg.IntervalDuration {
			s.interval = cfg.IntervalDuration
			if s.ticker != nil {
				s.ticker.Reset(s.interval)
				log.Printf("[Scheduler] 巡检间隔已更新为: %v", s.interval)
			}
		}
		s.mu.Unlock()
	}

	log.Printf("[Scheduler] 配置已更新，下次巡检将使用新配置")
}

// TriggerNow 立即触发一次巡检（热更新后调用）
func (s *Scheduler) TriggerNow() {
	s.mu.Lock()
	running := s.running
	ctx := s.ctx
	s.mu.Unlock()

	if running && ctx != nil {
		go s.runChecks(ctx)
		log.Printf("[Scheduler] 已触发即时巡检")
	}
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running && s.ticker != nil {
		s.ticker.Stop()
		s.running = false
	}

	s.prober.Close()
}
