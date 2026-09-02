package scheduler

import (
	"context"
	"testing"
	"time"

	"monitor/internal/config"
)

// 本文件覆盖两件事：
//   - 热更新重建任务堆时，未变更的 PSC 组沿用旧的 nextRun（不再每次热更新都把探测往后推）
//   - dispatchDue 的「堆外窗口」期间发生 rebuild/Stop 时，旧代任务不会被推回堆造成重复探测

func mkMonitorWithID(provider, service, channel, model, modelID string, interval time.Duration) config.ServiceConfig {
	m := mkMonitor(provider, service, channel, model, "http://127.0.0.1:1", interval)
	m.ModelID = modelID
	return m
}

// newIdleScheduler 造一个不启动 loop、也不会真的发出探测的调度器，
// 直接驱动 rebuildTasks 观察任务堆。
func newIdleScheduler(t *testing.T, fallback time.Duration) *Scheduler {
	t.Helper()
	return NewScheduler(newTestStore(t), fallback, nil)
}

// snapshotNextRuns 按身份键读出当前堆里每个任务的 nextRun。
func snapshotNextRuns(s *Scheduler) map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]time.Time, len(s.tasks))
	for _, t := range s.tasks {
		out[taskIdentityKey(t.monitor)] = t.nextRun
	}
	return out
}

func cfgWith(monitors ...config.ServiceConfig) *config.AppConfig {
	return &config.AppConfig{
		IntervalDuration: 150 * time.Second,
		MaxConcurrency:   -1,
		StaggerProbes:    boolPtr(true),
		Monitors:         monitors,
	}
}

// 三个组，其中 pa 是双模型组（用于验证组内 2s 紧凑关系）
func baseMonitors() []config.ServiceConfig {
	return []config.ServiceConfig{
		mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 150*time.Second),
		mkMonitorWithID("pa", "cc", "ch", "Sonnet", "md_a2", 150*time.Second),
		mkMonitorWithID("pb", "cc", "ch", "Haiku", "md_b1", 150*time.Second),
		mkMonitorWithID("pc", "cx", "ch", "GPT", "md_c1", 300*time.Second),
	}
}

func TestRebuildTasks_PreservesNextRunWhenNothingSchedulingChanged(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	cfgA := cfgWith(baseMonitors()...)
	s.rebuildTasks(cfgA, true)
	before := snapshotNextRuns(s)
	if len(before) != 4 {
		t.Fatalf("启动后应有 4 个任务，实际 %d", len(before))
	}

	// 只改展示字段，调度语义完全没变
	monitors := baseMonitors()
	monitors[0].ProviderName = "改了个展示名"
	monitors[2].ChannelName = "也只是展示名"
	s.rebuildTasks(cfgWith(monitors...), false)

	after := snapshotNextRuns(s)
	if len(after) != len(before) {
		t.Fatalf("任务数变了：before=%d after=%d", len(before), len(after))
	}
	for key, want := range before {
		got, ok := after[key]
		if !ok {
			t.Fatalf("身份键 %q 在重建后消失", key)
		}
		if !got.Equal(want) {
			t.Errorf("身份键 %q 的 nextRun 被重排：want %v, got %v（差 %v）", key, want, got, got.Sub(want))
		}
	}
}

func TestRebuildTasks_ReplansOnlyTheChangedGroup(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	s.rebuildTasks(cfgWith(baseMonitors()...), true)
	before := snapshotNextRuns(s)

	// 只把 pc 组的 interval 改短
	monitors := baseMonitors()
	monitors[3].IntervalDuration = 30 * time.Second
	s.rebuildTasks(cfgWith(monitors...), false)
	after := snapshotNextRuns(s)

	changed := taskIdentityKey(monitors[3])
	if after[changed].Equal(before[changed]) {
		t.Errorf("interval 变了的组必须重排，nextRun 却原样保留：%v", after[changed])
	}

	for _, m := range monitors[:3] {
		key := taskIdentityKey(m)
		if !after[key].Equal(before[key]) {
			t.Errorf("未受影响的组 %q 不该被重排：before=%v after=%v", key, before[key], after[key])
		}
	}
}

func TestRebuildTasks_ReplansWholeGroupWhenMembershipChanges(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	s.rebuildTasks(cfgWith(baseMonitors()...), true)
	before := snapshotNextRuns(s)

	// 给 pa 组加第三个模型
	monitors := append(baseMonitors(), mkMonitorWithID("pa", "cc", "ch", "Fable", "md_a3", 150*time.Second))
	s.rebuildTasks(cfgWith(monitors...), false)
	after := snapshotNextRuns(s)

	// 整组重排：三个成员都不该留在旧相位，且必须保持组内 2s 紧凑
	var paRuns []time.Time
	for _, id := range []string{"md_a1", "md_a2", "md_a3"} {
		key := taskIdentityModelIDPrefix + id
		run, ok := after[key]
		if !ok {
			t.Fatalf("成员 %s 缺失", id)
		}
		paRuns = append(paRuns, run)
		if old, existed := before[key]; existed && run.Equal(old) {
			t.Errorf("成员 %s 所在组成员变了，应整组重排，却保留了旧相位", id)
		}
	}
	for i := 1; i < len(paRuns); i++ {
		if gap := paRuns[i].Sub(paRuns[i-1]); gap != 2*time.Second {
			t.Errorf("组内第 %d/%d 个成员间隔 %v，应为 2s（多模型热力图靠这个对齐）", i, i+1, gap)
		}
	}

	// 没动过的组仍保留旧相位
	for _, id := range []string{"md_b1", "md_c1"} {
		key := taskIdentityModelIDPrefix + id
		if !after[key].Equal(before[key]) {
			t.Errorf("未受影响的组 %s 被连带重排了", id)
		}
	}
}

// spreadOverflowMonitors 造出「错峰总展开远大于最短巡检周期」的现网形态：
// 一个 4 模型组把 requiredBaseDelay 顶到 6s/0.9 ≈ 6.67s（与现网被顶高的成因相同），
// 再配上足够多的单模型组，展开就会数倍于 interval。
func spreadOverflowMonitors(groupCount int, interval time.Duration) []config.ServiceConfig {
	monitors := make([]config.ServiceConfig, 0, groupCount+4)
	for i, model := range []string{"m0", "m1", "m2", "m3"} {
		monitors = append(monitors, mkMonitorWithID("wide", "cx", "ch", model, "md_wide"+string(rune('a'+i)), interval))
	}
	for i := 1; i < groupCount; i++ {
		id := string(rune('a' + i))
		monitors = append(monitors, mkMonitorWithID("p"+id, "cc", "ch", "M", "md_"+id, interval))
	}
	return monitors
}

// 复刻生产形态：组多到错峰总展开远大于最短巡检周期（现网 95 组展开 10m33s、最短 2m30s）。
// 被重排的组若不封顶首次延迟，缩短 interval 的通道反而要多等一整个展开。
//
// 刻意不依赖「配置里排最后的组拿到最大错峰延迟」这条组序假设——那是
// buildMonitorGroupsFiltered 的内部实现，将来改了会让断言静默失效。
// 这里让全部通道同为 30s，展开约 21×6.67s ≈ 140s，于是相当一部分组无论排第几都会撞到封顶。
func TestRebuildTasks_CapsReplannedFirstDelayAtInterval(t *testing.T) {
	const (
		groupCount = 20
		interval   = 30 * time.Second
		slack      = 2 * time.Second // 覆盖测试取 now 与 rebuildTasks 内部取 now 的执行间隔
	)

	// 空堆上直接走热更新路径：没有旧相位可继承，全部组都会被重排
	s := newIdleScheduler(t, interval)
	now := time.Now()
	s.rebuildTasks(cfgWith(spreadOverflowMonitors(groupCount, interval)...), false)

	atCap := 0
	for key, run := range snapshotNextRuns(s) {
		delay := run.Sub(now)
		if delay < 0 {
			t.Errorf("%s 的首次延迟为负：%v", key, delay)
		}
		if delay > interval+slack {
			t.Errorf("%s 的首次延迟 %v 超过自己的 interval %v——短周期通道要多等一整个错峰展开", key, delay, interval)
		}
		if delay >= interval-slack {
			atCap++
		}
	}
	// 若一个任务都没顶到上限，说明这个场景根本没触发封顶逻辑，断言是真空的
	if atCap < 2 {
		t.Fatalf("只有 %d 个任务达到封顶值，本场景没有真正触发首次延迟封顶，断言无效", atCap)
	}
}

func TestRebuildTasks_ForcesReplanWhenStaggerToggled(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	s.rebuildTasks(cfgWith(baseMonitors()...), true)
	before := snapshotNextRuns(s)

	cfgB := cfgWith(baseMonitors()...)
	cfgB.StaggerProbes = boolPtr(false)
	s.rebuildTasks(cfgB, false)
	after := snapshotNextRuns(s)

	same := 0
	for key, old := range before {
		if after[key].Equal(old) {
			same++
		}
	}
	if same == len(before) {
		t.Error("stagger_probes 被关掉后所有任务仍保留错峰相位，开关等于没生效")
	}
}

// 多模型组里只改一个成员的 interval，整组都要重排——这正是「保留粒度是组」的核心，
// 只重排改动的那一个会让同组成员的 2s 紧凑关系断掉。
func TestRebuildTasks_ReplansWholeGroupWhenOneMemberIntervalChanges(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	s.rebuildTasks(cfgWith(baseMonitors()...), true)
	before := snapshotNextRuns(s)

	monitors := baseMonitors()
	monitors[1].IntervalDuration = 60 * time.Second // pa 组的 Sonnet，Opus 不动

	s.rebuildTasks(cfgWith(monitors...), false)
	after := snapshotNextRuns(s)

	for _, id := range []string{"md_a1", "md_a2"} {
		key := taskIdentityModelIDPrefix + id
		if after[key].Equal(before[key]) {
			t.Errorf("同组成员 %s 的 interval 没变，但同组另一成员变了，整组都该重排", id)
		}
	}
	if gap := after[taskIdentityModelIDPrefix+"md_a2"].Sub(after[taskIdentityModelIDPrefix+"md_a1"]); gap != 2*time.Second {
		t.Errorf("重排后组内间隔 %v，应为 2s", gap)
	}
	for _, id := range []string{"md_b1", "md_c1"} {
		key := taskIdentityModelIDPrefix + id
		if !after[key].Equal(before[key]) {
			t.Errorf("其它组 %s 被连带重排了", id)
		}
	}
}

// interval 的比较必须走「有效值」：没写 interval 的监测项取调度器 fallback，
// 直接比裸 IntervalDuration 会把 0 和 fallback 当成两回事。
func TestRebuildTasks_PreservesWhenEffectiveIntervalMatchesFallback(t *testing.T) {
	const fallback = 150 * time.Second
	s := newIdleScheduler(t, fallback)

	zero := mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 0) // 未配置 interval → 用 fallback
	s.rebuildTasks(cfgWith(zero), true)
	before := snapshotNextRuns(s)

	// 显式写成与 fallback 相同的值：有效 interval 没变，应当保留相位
	explicit := zero
	explicit.IntervalDuration = fallback
	s.rebuildTasks(cfgWith(explicit), false)
	key := taskIdentityModelIDPrefix + "md_a1"
	if !snapshotNextRuns(s)[key].Equal(before[key]) {
		t.Error("显式 interval 与 fallback 相同，有效值没变，不该重排")
	}

	// 换成真的不同的值：必须重排
	changed := zero
	changed.IntervalDuration = 60 * time.Second
	s.rebuildTasks(cfgWith(changed), false)
	if snapshotNextRuns(s)[key].Equal(before[key]) {
		t.Error("有效 interval 变了却仍保留旧相位")
	}
}

// 首次延迟封顶只属于热更新路径。启动时全部任务本来就要铺开填满巡检周期，
// 若误把封顶套到 startup 上，短周期通道会在启动瞬间挤成一堆。
func TestRebuildTasks_StartupDoesNotCapFirstDelay(t *testing.T) {
	const interval = 30 * time.Second

	s := newIdleScheduler(t, interval)
	now := time.Now()
	s.rebuildTasks(cfgWith(spreadOverflowMonitors(20, interval)...), true)

	// 余量必须实质大于 interval：被封顶的任务延迟恰好是「interval + 函数内外取 now 的间隔」，
	// 用 `> interval` 判会把封顶后的任务也算进来，断言就成了永远为真的真空。
	// 本场景不封顶时最大延迟约 20×6.67s ≈ 140s，取 2×interval 作阈值既有余量又能咬住。
	spreadBeyondOneInterval := 0
	for _, run := range snapshotNextRuns(s) {
		if run.Sub(now) > 2*interval {
			spreadBeyondOneInterval++
		}
	}
	if spreadBeyondOneInterval == 0 {
		t.Errorf("启动路径不应封顶首次延迟，却没有任何任务被排到 %v 之外", 2*interval)
	}
}

// 锁死一处有意为之的设计：继承先按 PSC 定位组、再按身份键在组内查，
// 所以两个不同 PSC 即便共用同一个 ModelID 也各查各的，不会串组。
func TestRebuildTasks_SameModelIDInDifferentPSCDoesNotCrossOver(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	monitors := []config.ServiceConfig{
		mkMonitorWithID("pa", "cc", "ch", "M", "md_dup", 150*time.Second),
		mkMonitorWithID("pb", "cc", "ch", "M", "md_dup", 150*time.Second),
	}
	s.rebuildTasks(cfgWith(monitors...), true)

	s.mu.Lock()
	runs := make(map[string]time.Time, len(s.tasks))
	for _, task := range s.tasks {
		runs[task.monitor.Provider] = task.nextRun
	}
	s.mu.Unlock()
	if len(runs) != 2 {
		t.Fatalf("两个 PSC 应各有一个任务，实际 %d", len(runs))
	}

	s.rebuildTasks(cfgWith(monitors...), false)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tasks) != 2 {
		t.Fatalf("重建后应仍有 2 个任务，实际 %d", len(s.tasks))
	}
	for _, task := range s.tasks {
		if want := runs[task.monitor.Provider]; !task.nextRun.Equal(want) {
			t.Errorf("PSC %s 的相位被另一个 PSC 串掉了：want %v, got %v",
				task.monitor.Provider, want, task.nextRun)
		}
	}
}

func TestRebuildTasks_LegacyKeyFallbackWhenModelIDEmpty(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	// 不填 ModelID：库层不能假设调用方跑过 CheckRuntimeModelIDs
	monitors := []config.ServiceConfig{
		mkMonitor("pa", "cc", "ch", "Opus", "http://127.0.0.1:1", 150*time.Second),
		mkMonitor("pa", "cc", "ch", "Sonnet", "http://127.0.0.1:1", 150*time.Second),
	}
	s.rebuildTasks(cfgWith(monitors...), true)
	before := snapshotNextRuns(s)
	if len(before) != 2 {
		t.Fatalf("空 ModelID 的两个监测项必须有各自的身份键，实际只有 %d 个", len(before))
	}

	s.rebuildTasks(cfgWith(monitors...), false)
	after := snapshotNextRuns(s)
	for key, want := range before {
		if !after[key].Equal(want) {
			t.Errorf("回退到四元组身份键后仍应保留相位：%q before=%v after=%v", key, want, after[key])
		}
	}
}

// --- 堆外窗口：旧代任务不得被推回 ---

// runDispatchInHeapGap 把一个已到期任务放进堆，用占满的信号量把 dispatchDue 卡在
// runTask 的信号量等待处——此刻任务已被弹出、尚未推回，正是那个「堆外窗口」。
// 返回 dispatchDue 结束信号与解除阻塞的函数。
func runDispatchInHeapGap(t *testing.T, s *Scheduler, m config.ServiceConfig, interval time.Duration) (done chan struct{}, release func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	s.mu.Lock()
	s.running = true
	s.ctx = ctx
	s.sem = make(chan struct{}, 1)
	s.sem <- struct{}{} // 占满：runTask 会阻塞在获取信号量
	s.generation++
	s.tasks = s.tasks[:0]
	s.tasks = append(s.tasks, &task{
		monitor:    m,
		interval:   interval,
		nextRun:    time.Now().Add(-time.Second), // 已到期
		generation: s.generation,
	})
	s.mu.Unlock()

	done = make(chan struct{})
	go func() {
		defer close(done)
		s.dispatchDue()
	}()

	// 等到任务确实被弹出（堆空）——这就是确定性的窗口，不靠 sleep 猜
	deadline := time.Now().Add(3 * time.Second)
	for {
		s.mu.Lock()
		empty := len(s.tasks) == 0
		s.mu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("等不到 dispatchDue 弹出任务，堆外窗口没造出来")
		}
		time.Sleep(time.Millisecond)
	}
	return done, cancel
}

func countTasksByIdentity(s *Scheduler, m config.ServiceConfig) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := taskIdentityKey(m)
	n := 0
	for _, t := range s.tasks {
		if taskIdentityKey(t.monitor) == key {
			n++
		}
	}
	return n
}

func TestDispatchDue_DropsStaleTaskAfterRebuild(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	m := mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 150*time.Second)

	done, release := runDispatchInHeapGap(t, s, m, 150*time.Second)

	// 窗口内热更新：堆里会出现同一监测项的新任务
	s.rebuildTasks(cfgWith(m), false)
	if got := countTasksByIdentity(s, m); got != 1 {
		t.Fatalf("重建后该监测项应恰有 1 个任务，实际 %d", got)
	}

	release() // 让 runTask 从信号量等待中退出，dispatchDue 继续走到「推回堆」
	<-done

	if got := countTasksByIdentity(s, m); got != 1 {
		t.Errorf("堆外窗口里的旧代任务被推回，该监测项现在有 %d 个任务，会每周期被探测两次", got)
	}
}

// 代次写错的后果不是「多探测一次」而是「从此不再探测」：新建任务若没带上当前代次，
// 它第一次跑完就会被回填闸判成旧代丢弃，该通道永久从堆里消失。必须有测试钉住。
func TestDispatchDue_NewTaskIsRequeuedAfterItsFirstRun(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	m := mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 150*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 已取消：runTask 立即返回，不发真探测

	s.rebuildTasks(cfgWith(m), false)
	s.mu.Lock()
	s.running = true
	s.ctx = ctx
	s.tasks[0].nextRun = time.Now().Add(-time.Second) // 让它立刻到期
	s.mu.Unlock()

	s.dispatchDue()
	s.wg.Wait()

	if got := countTasksByIdentity(s, m); got != 1 {
		t.Errorf("新建任务跑完一轮后应重新入堆，实际 %d 个——代次写错会让该通道从此不再被探测", got)
	}
}

// rebuildTasks 的两条提前 return 路径同样要让堆外的旧任务失效，
// 否则「配置被清空/全禁用」之后旧任务还能爬回堆里继续探测。
func TestDispatchDue_DropsStaleTaskOnEarlyReturnPaths(t *testing.T) {
	m := mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 150*time.Second)

	cases := map[string]*config.AppConfig{
		"配置被清空": cfgWith(),
		"全部禁用": func() *config.AppConfig {
			disabled := m
			disabled.Disabled = true
			return cfgWith(disabled)
		}(),
	}

	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			s := newIdleScheduler(t, 150*time.Second)
			done, release := runDispatchInHeapGap(t, s, m, 150*time.Second)

			s.rebuildTasks(cfg, false)

			release()
			<-done

			if got := countTasksByIdentity(s, m); got != 0 {
				t.Errorf("%s 之后旧任务仍被推回堆，实际 %d 个", name, got)
			}
		})
	}
}

func TestDispatchDue_DropsTaskAfterStop(t *testing.T) {
	s := newIdleScheduler(t, 150*time.Second)
	m := mkMonitorWithID("pa", "cc", "ch", "Opus", "md_a1", 150*time.Second)

	done, release := runDispatchInHeapGap(t, s, m, 150*time.Second)

	s.Stop()

	release()
	<-done

	if got := countTasksByIdentity(s, m); got != 0 {
		t.Errorf("Stop 之后旧任务仍被推回堆，实际 %d 个", got)
	}
}
