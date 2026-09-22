package scheduler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"monitor/internal/automove"
	"monitor/internal/config"
	"monitor/internal/storage"
)

func boolPtr(v bool) *bool { return &v }

func newTestStore(t *testing.T) *storage.SQLiteStorage {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "scheduler_test.db")
	store, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mkMonitor(provider, service, channel, model, url string, interval time.Duration) config.ServiceConfig {
	return config.ServiceConfig{
		Provider:               provider,
		Service:                service,
		Channel:                channel,
		Model:                  model,
		BaseURL:                url,
		URLPattern:             "{{BASE_URL}}",
		Method:                 http.MethodGet,
		SlowLatencyDuration:    5 * time.Second,
		TimeoutDuration:        5 * time.Second,
		IntervalDuration:       interval,
		RetryCount:             0,
		RetryBaseDelayDuration: 200 * time.Millisecond,
		RetryMaxDelayDuration:  2 * time.Second,
	}
}

func mkKey(m config.ServiceConfig) storage.MonitorKey {
	return storage.MonitorKey{
		Provider: m.Provider,
		Service:  m.Service,
		Channel:  m.Channel,
		Model:    m.Model,
	}
}

func waitRecords(t *testing.T, store storage.Storage, key storage.MonitorKey, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		recs, err := store.GetHistory(key.Provider, key.Service, key.Channel, key.Model, time.Unix(0, 0))
		if err != nil {
			t.Fatalf("GetHistory: %v", err)
		}
		if len(recs) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %d records, have %d", want, len(recs))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// --- lifecycle ---

func TestStartStop(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	cfg := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		StaggerProbes:    boolPtr(false),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfg)

	s.mu.Lock()
	running := s.running
	schedCtx := s.ctx
	s.mu.Unlock()

	if !running {
		t.Fatal("scheduler not running after Start")
	}
	if schedCtx == nil {
		t.Fatal("scheduler context is nil after Start")
	}

	s.Stop()

	s.mu.Lock()
	running = s.running
	timer := s.timer
	s.mu.Unlock()

	if running {
		t.Error("scheduler still running after Stop")
	}
	if timer != nil {
		t.Error("timer not cleared after Stop")
	}

	select {
	case <-schedCtx.Done():
	case <-time.After(2 * time.Second):
		t.Error("context not cancelled after Stop")
	}
}

func TestStartStop_DoubleStart(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	cfg := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		StaggerProbes:    boolPtr(false),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfg)
	s.Start(ctx, cfg) // second start is no-op
	t.Cleanup(s.Stop)

	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		t.Fatal("scheduler not running after double Start")
	}
}

func TestStartStop_DoubleStop(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	cfg := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		StaggerProbes:    boolPtr(false),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfg)
	s.Stop()
	s.Stop() // second stop is no-op, should not panic
}

// --- TriggerNow ---

func TestTriggerNow(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	mon := mkMonitor("trig", "svc", "ch", "m", srv.URL, 30*time.Second)
	cfg := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		MaxConcurrency:   1,
		StaggerProbes:    boolPtr(false),
		Monitors:         []config.ServiceConfig{mon},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfg)
	t.Cleanup(s.Stop)

	key := mkKey(mon)
	// Wait for initial probe
	waitRecords(t, store, key, 1, 5*time.Second)

	// Trigger immediate re-probe
	s.TriggerNow()
	waitRecords(t, store, key, 2, 5*time.Second)
}

func TestTriggerNow_NotRunning(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)
	// Should not panic when called on a stopped scheduler
	s.TriggerNow()
}

// --- UpdateConfig ---

func TestUpdateConfig(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	cfgA := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		MaxConcurrency:   1,
		StaggerProbes:    boolPtr(false),
		Monitors: []config.ServiceConfig{
			mkMonitor("a", "svc", "ch", "m1", srv.URL, 30*time.Second),
		},
	}

	cfgB := &config.AppConfig{
		IntervalDuration: 45 * time.Second,
		MaxConcurrency:   2,
		StaggerProbes:    boolPtr(false),
		Monitors: []config.ServiceConfig{
			mkMonitor("b", "svc", "ch", "m2", srv.URL, 45*time.Second),
			mkMonitor("c", "svc", "ch2", "m3", srv.URL, 45*time.Second),
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfgA)
	t.Cleanup(s.Stop)

	s.UpdateConfig(cfgB)

	s.mu.Lock()
	taskCount := len(s.tasks)
	semCap := 0
	if s.sem != nil {
		semCap = cap(s.sem)
	}
	s.mu.Unlock()

	if taskCount != len(cfgB.Monitors) {
		t.Errorf("task count: want %d, got %d", len(cfgB.Monitors), taskCount)
	}
	if semCap != cfgB.MaxConcurrency {
		t.Errorf("semaphore cap: want %d, got %d", cfgB.MaxConcurrency, semCap)
	}
}

func TestUpdateConfig_EmptyMonitors(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)

	cfgA := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		MaxConcurrency:   1,
		StaggerProbes:    boolPtr(false),
		Monitors: []config.ServiceConfig{
			mkMonitor("a", "svc", "ch", "m", srv.URL, 30*time.Second),
		},
	}
	cfgEmpty := &config.AppConfig{
		IntervalDuration: 30 * time.Second,
		StaggerProbes:    boolPtr(false),
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfgA)
	t.Cleanup(s.Stop)

	s.UpdateConfig(cfgEmpty)

	s.mu.Lock()
	taskCount := len(s.tasks)
	s.mu.Unlock()

	if taskCount != 0 {
		t.Errorf("task count after empty config: want 0, got %d", taskCount)
	}
}

// --- MaxConcurrency ---

func TestMaxConcurrency(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, time.Minute, nil)

	const maxConc = 2
	const monCount = 4

	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(releaseCh) }) }

	startedCh := make(chan struct{}, monCount)
	var active, maxActive int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		cur := atomic.AddInt32(&active, 1)
		for {
			m := atomic.LoadInt32(&maxActive)
			if cur <= m || atomic.CompareAndSwapInt32(&maxActive, m, cur) {
				break
			}
		}
		startedCh <- struct{}{}
		<-releaseCh
		atomic.AddInt32(&active, -1)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.AppConfig{
		IntervalDuration: time.Minute,
		MaxConcurrency:   maxConc,
		StaggerProbes:    boolPtr(false),
	}
	for i := 0; i < monCount; i++ {
		cfg.Monitors = append(cfg.Monitors, mkMonitor(
			fmt.Sprintf("p%d", i), "svc", fmt.Sprintf("ch%d", i), fmt.Sprintf("m%d", i),
			srv.URL, time.Minute,
		))
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s.Start(ctx, cfg)
	t.Cleanup(s.Stop)
	t.Cleanup(closeRelease)

	// Wait for maxConc probes to start (they will block on releaseCh)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < maxConc; i++ {
		select {
		case <-startedCh:
		case <-timer.C:
			t.Fatalf("timeout waiting for probe %d to start", i)
		}
	}

	// Give a moment for any extra probes to start (they shouldn't)
	time.Sleep(100 * time.Millisecond)

	if peak := atomic.LoadInt32(&maxActive); peak > int32(maxConc) {
		t.Fatalf("peak concurrency %d exceeds limit %d", peak, maxConc)
	}

	// Release blocked probes
	closeRelease()

	// Remaining probes should now execute
	for i := 0; i < monCount-maxConc; i++ {
		select {
		case <-startedCh:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for remaining probe %d", i)
		}
	}
}

func TestRebuildTasks_SkipsRuntimeColdPSC(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, 30*time.Second, nil)

	autoMover := automove.NewService(nil, &config.AppConfig{})
	autoMover.SetOverrides(map[storage.MonitorKey]automove.MonitorOverride{
		{Provider: "cold", Service: "svc", Channel: "vip"}: {
			Board:      "cold",
			ColdReason: "auto cold",
		},
	})
	s.SetAutoMover(autoMover)

	root := mkMonitor("cold", "svc", "vip", "", "https://example.com", 30*time.Second)
	child := mkMonitor("cold", "svc", "vip", "gpt-4o", "https://example.com", 30*time.Second)
	child.Parent = "cold/svc/vip"
	hot := mkMonitor("hot", "svc", "std", "", "https://example.com", 30*time.Second)

	cfg := &config.AppConfig{
		Boards:           config.BoardsConfig{Enabled: true},
		IntervalDuration: 30 * time.Second,
		MaxConcurrency:   1,
		StaggerProbes:    boolPtr(false),
		Monitors:         []config.ServiceConfig{root, child, hot},
	}

	s.rebuildTasks(cfg, false)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) != 1 {
		t.Fatalf("task count = %d, want 1 (only hot monitor)", len(s.tasks))
	}
	got := s.tasks[0].monitor.Provider + "/" + s.tasks[0].monitor.Service + "/" + s.tasks[0].monitor.Channel
	if got != "hot/svc/std" {
		t.Fatalf("remaining task = %s, want hot/svc/std", got)
	}
}

// blockingProbeServer 起一个把每次探测都挂住直到 release 的上游，并记录到达次数。
func blockingProbeServer(t *testing.T) (url string, arrived *atomic.Int32, started <-chan struct{}, release func()) {
	t.Helper()
	releaseCh := make(chan struct{})
	var once sync.Once
	startedCh := make(chan struct{}, 64)
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		count.Add(1)
		startedCh <- struct{}{}
		<-releaseCh
		w.WriteHeader(200)
	}))
	release = func() { once.Do(func() { close(releaseCh) }) }
	t.Cleanup(srv.Close)
	t.Cleanup(release) // 先于 srv.Close 执行（Cleanup 后进先出），否则 Close 等挂住的请求会卡死
	return srv.URL, &count, startedCh, release
}

// 并发槽饱和时派发循环不能被卡住：到期任务都应登记完并按周期推回堆里。
func TestDispatchNotBlockedBySaturatedSemaphore(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, time.Minute, nil)
	url, _, started, _ := blockingProbeServer(t)

	cfg := &config.AppConfig{IntervalDuration: time.Minute, MaxConcurrency: 1, StaggerProbes: boolPtr(false)}
	for i := 0; i < 3; i++ {
		cfg.Monitors = append(cfg.Monitors, mkMonitor(fmt.Sprintf("p%d", i), "svc", "ch", "m", url, time.Minute))
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Start(ctx, cfg)
	t.Cleanup(s.Stop)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first probe")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		queued, inflight := len(s.tasks), len(s.inflight)
		s.mu.Unlock()
		if queued == 3 && inflight == 3 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("dispatch loop stuck: tasks back in heap=%d inflight=%d, want 3/3", queued, inflight)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 同一监测项上一轮还没结束时再次到期（此处用 TriggerNow 模拟），不能再派发一次。
func TestInflightMonitorNotDispatchedTwice(t *testing.T) {
	store := newTestStore(t)
	s := NewScheduler(store, time.Minute, nil)
	url, arrived, started, release := blockingProbeServer(t)

	cfg := &config.AppConfig{
		IntervalDuration: time.Minute,
		MaxConcurrency:   2, // 并发槽富余：旧实现会为同一监测项再起一个探测
		StaggerProbes:    boolPtr(false),
		Monitors:         []config.ServiceConfig{mkMonitor("p", "svc", "ch", "m", url, time.Minute)},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.Start(ctx, cfg)
	t.Cleanup(s.Stop)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first probe")
	}

	for i := 0; i < 3; i++ {
		s.TriggerNow()
		time.Sleep(50 * time.Millisecond)
	}
	if n := arrived.Load(); n != 1 {
		t.Fatalf("probe requests while first still in flight: want 1, got %d", n)
	}

	// 放行后身份释放，下一次到期可以正常派发
	release()
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		busy := len(s.inflight)
		s.mu.Unlock()
		if busy == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inflight identity not released after probe finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.TriggerNow()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("monitor should be dispatched again after previous probe finished")
	}
}
