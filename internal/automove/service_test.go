package automove

import (
	"context"
	"errors"
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/rpdiag"
	"monitor/internal/storage"
)

// mockStorage 实现 storage.Storage 接口的测试替身
type mockStorage struct {
	history map[storage.MonitorKey][]*storage.ProbeRecord
	// batchErr 非 nil 时，GetHistoryBatch 返回该错误，用于测试查询失败 fallback。
	batchErr error
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		history: make(map[storage.MonitorKey][]*storage.ProbeRecord),
	}
}

func (m *mockStorage) Init() error                                   { return nil }
func (m *mockStorage) Close() error                                  { return nil }
func (m *mockStorage) Ping() error                                   { return nil }
func (m *mockStorage) WithContext(_ context.Context) storage.Storage { return m }
func (m *mockStorage) SaveRecord(_ *storage.ProbeRecord) error       { return nil }
func (m *mockStorage) GetLatest(_, _, _, _ string) (*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistory(_, _, _, _ string, _ time.Time) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryWithLimit(_, _, _, _ string, _ time.Time, _ int) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetLatestByModelID(_ string) (*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryByModelID(_ string, _ time.Time) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryWithLimitByModelID(_ string, _ time.Time, _ int) ([]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetLatestBatch(_ []storage.MonitorKey) (map[storage.MonitorKey]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryBatch(keys []storage.MonitorKey, _ time.Time) (map[storage.MonitorKey][]*storage.ProbeRecord, error) {
	if m.batchErr != nil {
		return nil, m.batchErr
	}
	result := make(map[storage.MonitorKey][]*storage.ProbeRecord)
	for _, k := range keys {
		if records, ok := m.history[k]; ok {
			result[k] = records
		}
	}
	return result, nil
}
func (m *mockStorage) GetLatestBatchByModelID(_ []storage.ProbeHistoryKey) (map[storage.ProbeHistoryKey]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) GetHistoryBatchByModelID(_ []storage.ProbeHistoryKey, _ time.Time) (map[storage.ProbeHistoryKey][]*storage.ProbeRecord, error) {
	return nil, nil
}
func (m *mockStorage) MigrateChannelData(_ []storage.ChannelMigrationMapping) error { return nil }
func (m *mockStorage) BackfillProbeHistoryModelIDs(_ []storage.ModelIDMigrationMapping) error {
	return nil
}
func (m *mockStorage) GetServiceState(_, _, _, _ string) (*storage.ServiceState, error) {
	return nil, nil
}
func (m *mockStorage) UpsertServiceState(_ *storage.ServiceState) error { return nil }
func (m *mockStorage) GetChannelState(_, _, _ string) (*storage.ChannelState, error) {
	return nil, nil
}
func (m *mockStorage) UpsertChannelState(_ *storage.ChannelState) error { return nil }
func (m *mockStorage) GetModelStatesForChannel(_, _, _ string) ([]*storage.ServiceState, error) {
	return nil, nil
}
func (m *mockStorage) SaveStatusEvent(_ *storage.StatusEvent) error { return nil }
func (m *mockStorage) GetStatusEvents(_ int64, _ int, _ *storage.EventFilters) ([]*storage.StatusEvent, error) {
	return nil, nil
}
func (m *mockStorage) GetLatestEventID() (int64, error) { return 0, nil }
func (m *mockStorage) PurgeOldRecords(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 0, nil
}

// makeRecords 生成指定状态的探测记录。
// 所有记录使用相同时间戳（当前时间），确保落在同一 bucket 内，
// 避免因 UTC 午夜附近运行导致的跨 bucket 脆弱测试。
func makeRecords(status int, count int) []*storage.ProbeRecord {
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, count)
	for i := range records {
		records[i] = &storage.ProbeRecord{Status: status, Timestamp: ts}
	}
	return records
}

func TestEvaluate_DualThreshold_DemoteHot(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}

	// 100% red → availability=0% < threshold_down=50%
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for bad/cc/vip")
	}
	if ov.Board != "secondary" {
		t.Errorf("expected board=secondary, got %s", ov.Board)
	}
}

// 新语义：board 是"锚点/天花板"——自动移板只能在配置板位及以下浮动，绝不向上越板。
// 手动配 board=secondary 的通道，无论可用率多高都不会被自动升到 hot。
func TestEvaluate_SecondaryAnchor_DoesNotPromoteToHot(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "good", Service: "cc", Channel: "vip"}

	// 100% green → availability=100% >= threshold_up=55%，旧语义会升 hot，新语义留 secondary。
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "good", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Errorf("expected no override (secondary anchor stays secondary), got board=%s", ov.Board)
	}
}

// 存量回落：升级前被旧 promote 语义升到 hot 的 secondary 通道（持久化了 Board=hot override），
// 升级后下一轮评估应自动落回配置的 secondary。
// 可用率刻意取 20%（> threshold_cold 10% 且 < threshold_down 50%）以区分两种实现：
//   - 正确（按 configBoard 分流）：secondary 通道不进 hot 分支 → 无 override；
//   - 错误（仅删 promote、仍按 effectiveBoard 分流）：旧 hot override 使其走 case hot，
//     在 avail<down 时写出一个冗余的 secondary 孤儿 override → 本测试失败。
func TestEvaluate_SecondaryAnchor_DropsLegacyHotOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "legacy", Service: "cc", Channel: "vip"}

	// 20% availability：20 绿 + 80 红，同一时间戳落同一 bucket。
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 20; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 20; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "legacy", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	// 模拟升级前遗留的 Board=hot override。
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "hot"},
	})

	svc.Evaluate(context.Background())

	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Errorf("expected legacy hot override dropped (fall back to config secondary), got board=%s", ov.Board)
	}
}

// secondary 通道可用率跌破 threshold_cold 仍应进冷板（停探省资源）——
// 锚点语义只挡"向上越板"，不挡"向下冷板"。
func TestEvaluate_SecondaryAnchor_MovesToColdBelowThreshold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "dying", Service: "cc", Channel: "vip"}

	// 0% availability < threshold_cold=10%。
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "dying", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected cold override for secondary monitor below threshold_cold")
	}
	if ov.Board != "cold" {
		t.Errorf("expected board=cold, got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Error("expected non-empty cold reason")
	}
}

// 回归护栏：configBoard=hot 的通道，降级→恢复双向逻辑不受锚点改动影响。
func TestEvaluate_HotAnchor_DemoteThenRecover(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "flaky", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "flaky", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)

	// Demote: 0% availability < threshold_down → secondary override。
	store.history[key] = makeRecords(0, 20)
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("expected demote to secondary, got ok=%v board=%s", ok, ov.Board)
	}

	// Recover: 100% availability >= threshold_up → override 清除，回到配置 hot。
	store.history[key] = makeRecords(1, 20)
	svc.Evaluate(context.Background())
	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Errorf("expected override cleared (config hot restored), got board=%s", ov.Board)
	}
}

// 热更新快路径：configBoard=secondary 的遗留 Board=hot override 应在 purgeStaleOverrides
// 立即清除，无需等下一轮定期评估（与 evaluate 锚点语义保持一致，避免短暂旧状态窗口）。
func TestUpdateConfig_PurgesLegacyHotOverrideForSecondaryAnchor(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "legacy", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "legacy", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	// 遗留 Board=hot override（旧 promote 语义残留）。
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "hot"},
	})

	// 热更新触发 purge：configBoard=secondary 的 hot override 应被立即清除。
	svc.UpdateConfig(cfg)

	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Errorf("expected legacy hot override purged on hot-reload, got board=%s", ov.Board)
	}
}

func TestEvaluate_DualThreshold_HysteresisBuffer(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "mid", Service: "cc", Channel: "vip"}

	// 52% availability: between threshold_down(50%) and threshold_up(55%)
	// 所有记录使用相同时间戳，确保落在同一 bucket，避免跨 bucket 脆弱测试
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 52; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 52; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records

	// As secondary: board=secondary is the anchor/ceiling — never auto-promotes to hot
	// regardless of availability (52% is just a representative non-cold value).
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "mid", Service: "cc", Channel: "vip", Board: "secondary"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for secondary monitor at 52% (between thresholds)")
	}

	// As hot with 52% (> threshold_down 50%): should NOT demote
	cfg.Monitors[0].Board = "hot"
	svc2 := NewService(store, cfg)
	svc2.Evaluate(context.Background())

	_, ok = svc2.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for hot monitor at 52% (between thresholds)")
	}
}

func TestEvaluate_DualThreshold_PreviousOverridePreserved(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "mid", Service: "cc", Channel: "vip"}

	// 52% availability: between threshold_down(50%) and threshold_up(55%)
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 52; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 52; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "mid", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)

	// First: demote with 0% availability
	store.history[key] = makeRecords(0, 100)
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatal("expected demote to secondary")
	}

	// Second: availability recovers to 52% (in buffer zone)
	// Override should be preserved — still secondary
	store.history[key] = records
	svc.Evaluate(context.Background())
	ov, ok = svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Errorf("expected override preserved as secondary in buffer zone, got ok=%v board=%s", ok, ov.Board)
	}
}

func TestEvaluate_MinProbes_Skip(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "new", Service: "cc", Channel: "vip"}

	// Only 5 records < min_probes=10: should skip
	store.history[key] = makeRecords(0, 5)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "new", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override when probes < min_probes")
	}
}

func TestEvaluate_ColdExcluded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "cold", Service: "cc", Channel: "vip"}

	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "cold", Service: "cc", Channel: "vip", Board: "cold"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for cold board monitors")
	}
}

func TestEvaluate_DisabledClears(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// Verify override exists
	_, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override after evaluate")
	}

	// Disable auto_move → UpdateConfig should clear overrides
	cfg2 := *cfg
	cfg2.Boards.AutoMove.Enabled = false
	svc.UpdateConfig(&cfg2)

	_, ok = svc.GetBoardOverride(key)
	if ok {
		t.Error("expected overrides cleared after disabling auto_move")
	}
}

func TestUpdateConfig_PurgesStaleOverrides(t *testing.T) {
	store := newMockStorage()

	hotKey := storage.MonitorKey{Provider: "hot-provider", Service: "cc", Channel: "vip"}
	coldKey := storage.MonitorKey{Provider: "cold-provider", Service: "cc", Channel: "vip"}
	disabledKey := storage.MonitorKey{Provider: "disabled-provider", Service: "cc", Channel: "vip"}
	removedKey := storage.MonitorKey{Provider: "removed-provider", Service: "cc", Channel: "vip"}

	store.history[hotKey] = makeRecords(0, 20)
	store.history[coldKey] = makeRecords(0, 20)
	store.history[disabledKey] = makeRecords(0, 20)
	store.history[removedKey] = makeRecords(0, 20)

	// 初始配置：所有通道在 hot 板
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hot-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "cold-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "disabled-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "removed-provider", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 验证：4 个通道都有 override（全部被降级到 secondary）
	for _, k := range []storage.MonitorKey{hotKey, coldKey, disabledKey, removedKey} {
		if _, ok := svc.GetBoardOverride(k); !ok {
			t.Fatalf("expected override for %s after initial evaluate", k.Provider)
		}
	}

	// 新配置：cold-provider 移入冷板，disabled-provider 被禁用，removed-provider 被移除
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hot-provider", Service: "cc", Channel: "vip", Board: "hot"},
			{Provider: "cold-provider", Service: "cc", Channel: "vip", Board: "cold"},
			{Provider: "disabled-provider", Service: "cc", Channel: "vip", Board: "hot", Disabled: true},
			// removed-provider 不再出现
		},
	}
	svc.UpdateConfig(cfg2)

	// hot-provider: 仍在 hot 板，override 应保留
	if _, ok := svc.GetBoardOverride(hotKey); !ok {
		t.Error("hot-provider override should be preserved")
	}

	// cold-provider: 已移入冷板，override 应被清除
	if _, ok := svc.GetBoardOverride(coldKey); ok {
		t.Error("cold-provider override should be purged after board changed to cold")
	}

	// disabled-provider: 已被禁用，override 应被清除
	if _, ok := svc.GetBoardOverride(disabledKey); ok {
		t.Error("disabled-provider override should be purged after being disabled")
	}

	// removed-provider: 已从配置移除，override 应被清除
	if _, ok := svc.GetBoardOverride(removedKey); ok {
		t.Error("removed-provider override should be purged after being removed from config")
	}
}

func TestUpdateConfig_PurgesHiddenOverrides(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "hidden", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hidden", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); !ok {
		t.Fatal("expected override after evaluate")
	}

	// 隐藏该通道
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "hidden", Service: "cc", Channel: "vip", Board: "hot", Hidden: true},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Error("hidden monitor override should be purged")
	}
}

func TestUpdateConfig_NoOverrides_Noop(t *testing.T) {
	store := newMockStorage()
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "s", Channel: "c", Board: "cold"},
		},
	}

	svc := NewService(store, cfg)
	// 无 override 时 UpdateConfig 不应 panic
	svc.UpdateConfig(cfg)

	if overrides := svc.Overrides(); overrides != nil {
		t.Error("expected nil overrides when no prior overrides exist")
	}
}

func TestUpdateConfig_PurgesParentOverrides(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); !ok {
		t.Fatal("expected override after evaluate")
	}

	// 通道变为子通道（设置了 Parent），不再是根通道
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Board: "hot", Parent: "other/cc/root"},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Error("child monitor override should be purged after gaining parent")
	}
}

// TestEvaluate_ExpiredChannel_HealthyStaysOnBoardLevelDowngraded 验证解耦后的新语义：
// 到期不再强制移入备板——板块位置完全由可用率决定。
// 到期 + 可用率健康（100%，configBoard=hot）→ 板块保持 hot，仅 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredChannel_HealthyStaysOnBoardLevelDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}

	// 100% 可用率：应保持热板，不因到期强制移入备板
	store.history[key] = makeRecords(1, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "expired",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelBackbone,
				ExpiresAt:    yesterday,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 可能有 override（仅含 SponsorLevel=pulse），但板块不得是 secondary 或 cold
	if ok {
		if ov.Board == "secondary" || ov.Board == "cold" {
			t.Errorf("到期健康通道不应被降至 %s（板块应由可用率决定，不由到期决定）", ov.Board)
		}
		if ov.SponsorLevel != config.SponsorLevelPulse {
			t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
		}
	} else {
		// 无 override 也可接受（level 已是 pulse 无需降级时），但本 case SponsorLevel=backbone，必须有降级
		t.Error("expected override with SponsorLevel=pulse for expired backbone channel")
	}
}

func TestEvaluate_NotYetExpired_NoExpiryOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "active", Service: "cc", Channel: "vip"}

	// 100% green → should not be demoted by availability or expiry
	store.history[key] = makeRecords(1, 20)

	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "active",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelBackbone,
				ExpiresAt:    tomorrow,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for not-yet-expired channel")
	}
}

func TestEvaluate_ExpiresToday_StillValid(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "today", Service: "cc", Channel: "vip"}

	store.history[key] = makeRecords(1, 20)

	// "今天"按 isSponsorExpired 的业务时区（CST）计算，而非 UTC——
	// 用 UTC 计算会在 UTC 16:00-23:59（CST 已跨入次日）这个每天固定窗口内让本测试假失败。
	today := time.Now().In(sponsorExpiryTZ).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:     "today",
				Service:      "cc",
				Channel:      "vip",
				Board:        "hot",
				SponsorLevel: config.SponsorLevelCore,
				ExpiresAt:    today,
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("expected no override for channel expiring today (still valid)")
	}
}

// TestEvaluate_ExpiredAndAvailability_Coexist 验证到期与可用率独立工作：
// 到期通道（可用率健康）→ 板块保持，仅 SponsorLevel 降级；
// 可用率差通道（未到期）→ 板块由可用率降级，SponsorLevel 不变。
func TestEvaluate_ExpiredAndAvailability_Coexist(t *testing.T) {
	store := newMockStorage()
	expiredKey := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	badKey := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}

	store.history[expiredKey] = makeRecords(1, 20) // 可用率 100%，但已到期
	store.history[badKey] = makeRecords(0, 20)     // 可用率 0%，未到期

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "expired", Service: "cc", Channel: "vip", Board: "hot", SponsorLevel: config.SponsorLevelBeacon, ExpiresAt: yesterday},
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 到期通道：可用率健康，板块不降 secondary；但 SponsorLevel 降为 pulse
	ov, ok := svc.GetBoardOverride(expiredKey)
	if !ok {
		t.Fatal("expected override for expired channel (SponsorLevel=pulse)")
	}
	if ov.Board == "secondary" {
		t.Errorf("expired healthy channel should NOT be demoted to secondary by expiry alone, got board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired channel, got %s", ov.SponsorLevel)
	}

	// 可用率差通道（未到期）：板块由可用率降至 secondary，SponsorLevel 不变
	ov2, ok := svc.GetBoardOverride(badKey)
	if !ok {
		t.Fatal("expected override for bad availability channel")
	}
	if ov2.Board != "secondary" {
		t.Errorf("bad availability: expected board=secondary (availability-driven), got %s", ov2.Board)
	}
	if ov2.SponsorLevel != "" {
		t.Errorf("bad availability: expected empty sponsor_level (no downgrade), got %s", ov2.SponsorLevel)
	}
}

func TestEvaluate_ChildMonitorsExcluded(t *testing.T) {
	store := newMockStorage()
	childKey := storage.MonitorKey{Provider: "p", Service: "s", Channel: "c", Model: "child-model"}
	store.history[childKey] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "s", Channel: "c", Model: "child-model", Parent: "p/s/c", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(childKey)
	if ok {
		t.Error("expected no override for child monitors (have parent)")
	}
}

// TestEvaluate_MultiModelChannel_AggregatesAllRows 锁死「通道可用率 = 该通道
// 所有模型行的探测汇总」：父行全绿、子行全红，只看父行会保持 hot，
// 汇总后 25% 才会跌破 threshold_down 降到 secondary。
func TestEvaluate_MultiModelChannel_AggregatesAllRows(t *testing.T) {
	store := newMockStorage()
	parentKey := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip", Model: "parent-model"}
	childKey := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip", Model: "child-model"}
	store.history[parentKey] = makeRecords(1, 10) // 100%
	store.history[childKey] = makeRecords(0, 30)  // 0%
	// 汇总：(10*1.0 + 30*0.0) / 40 * 100 = 25%

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Model: "parent-model", Board: "hot"},
			{Provider: "p", Service: "cc", Channel: "vip", Model: "child-model", Parent: "p/cc/vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(parentKey)
	if !ok {
		t.Fatal("expected override on parent key (child rows must be aggregated)")
	}
	if ov.Board != "secondary" {
		t.Errorf("expected board=secondary (aggregated availability 25%%), got %s", ov.Board)
	}
}

// TestEvaluate_MinProbes_NormalizedByActiveRows 锁死 min_probes 的「每行平均
// 样本数」语义：通道有 2 个活跃行、min_probes=10 → 需要 20 条汇总样本，
// 只有 15 条时必须冻结不判（否则多模型通道会白拿 N 倍宽松度）。
func TestEvaluate_MinProbes_NormalizedByActiveRows(t *testing.T) {
	store := newMockStorage()
	parentKey := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip", Model: "parent-model"}
	childKey := storage.MonitorKey{Provider: "p", Service: "cc", Channel: "vip", Model: "child-model"}
	store.history[parentKey] = makeRecords(0, 15) // 0%，样本数够单行阈值但不够两行
	store.history[childKey] = nil

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "p", Service: "cc", Channel: "vip", Model: "parent-model", Board: "hot"},
			{Provider: "p", Service: "cc", Channel: "vip", Model: "child-model", Parent: "p/cc/vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	if ov, ok := svc.GetBoardOverride(parentKey); ok {
		t.Errorf("expected no override (15 probes < min_probes 10 × 2 active rows), got board=%s", ov.Board)
	}
}

// === 自动冷板测试 ===

func TestEvaluate_AutoCold_DemotesToCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "bad", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "bad", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected cold override")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold, got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Fatal("expected ColdReason to be populated")
	}
}

func TestEvaluate_AutoCold_Sticky(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "sticky", Service: "cc", Channel: "vip"}
	// 即使可用率恢复到 100%，sticky cold 也不应被清除
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "sticky", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	// 预注入 cold override
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected sticky cold override to be preserved")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold, got %s", ov.Board)
	}
	if ov.ColdReason != "之前自动冷板" {
		t.Fatalf("expected original ColdReason, got %q", ov.ColdReason)
	}
}

func TestEvaluate_AutoCold_MinProbesProtection(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "new", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 5) // 不足 min_probes

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "new", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Fatal("expected no override: min_probes not met")
	}
}

func TestEvaluate_AutoColdExempt_SkipsColdDecision(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "exempt", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%，但已 exempt

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "exempt", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override (should demote to secondary, not cold)")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt should prevent cold board")
	}
	if ov.Board != "secondary" {
		t.Fatalf("expected board=secondary, got %s", ov.Board)
	}
}

func TestEvaluate_AutoMoveExempt_NoOverrideProduced(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "manual", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%，理论上会触发冷板

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "manual", Service: "cc", Channel: "vip", Board: "hot", AutoMoveExempt: true},
		},
	}

	svc := NewService(store, cfg)
	// 预先注入一个旧的 secondary override，验证 exempt 会将其清除
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "secondary"},
	})

	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("auto_move_exempt 通道不应产生任何 availability-based override")
	}
}

// TestEvaluate_AutoMoveExempt_ExpiryLevelDowngradeApplies 验证解耦后新语义：
// auto_move_exempt 跳过板块移动，但到期的 SponsorLevel 降级仍生效。
// 期望：override 含 SponsorLevel=pulse，Board 为空（不产生 secondary）。
func TestEvaluate_AutoMoveExempt_ExpiryLevelDowngradeApplies(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expired", Service: "cc", Channel: "vip"}
	// 可用率 100%（可用率逻辑不触发），但通道已到期且等级高于 pulse
	store.history[key] = makeRecords(1, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{
				Provider:       "expired",
				Service:        "cc",
				Channel:        "vip",
				Board:          "hot",
				SponsorLevel:   config.SponsorLevelBackbone,
				AutoMoveExempt: true,
				ExpiresAt:      "2020-01-01", // 明确过期
			},
		},
	}

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("auto_move_exempt 到期通道应产生 SponsorLevel=pulse override")
	}
	// 板块不因到期移动（auto_move_exempt 阻止板块评估）
	if ov.Board == "secondary" {
		t.Errorf("auto_move_exempt 应阻止板块降级，不应产生 secondary，实际 board=%s", ov.Board)
	}
	// 但 SponsorLevel 仍应降级
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

func TestUpdateConfig_AutoMoveExemptPurgesExistingOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "manual", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "manual", Service: "cc", Channel: "vip", Board: "hot", AutoMoveExempt: true},
		},
	}

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "secondary"},
	})

	svc.UpdateConfig(cfg)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("热更新后 auto_move_exempt 通道的旧 override 应被 purgeStaleOverrides 清除")
	}
}

// expiredAutoMoveCfg 构造一个启用冷板阈值的到期测试配置。
func expiredAutoMoveCfg(monitors ...config.ServiceConfig) *config.AppConfig {
	return &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors:          monitors,
	}
}

// TestEvaluate_ExpiredAndDead_MovesToCold 验证：
// 已到期 且 7 天可用率低于 threshold_cold 且探测数充足 → 移入冷板（availability-driven），
// 同时 SponsorLevel 降为 pulse（解耦后两者独立叠加）。
func TestEvaluate_ExpiredAndDead_MovesToCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "deadexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0% < threshold_cold=10%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "deadexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for expired+dead channel")
	}
	if ov.Board != "cold" {
		t.Fatalf("expected board=cold (availability < threshold_cold)，got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Fatal("expected ColdReason to be populated")
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_MinProbesSkipsBoard 验证解耦后新语义：
// 已到期但探测数不足 min_probes → 无法判断可用率，跳过板块移板；SponsorLevel 仍降为 pulse。
func TestEvaluate_ExpiredAndDead_MinProbesSkipsBoard(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "newexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 5) // 探测不足 min_probes=10

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "newexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 探测不足时跳过板块移板，但仍应有 SponsorLevel=pulse override（到期降级等级）
	if !ok {
		t.Fatal("expected override with SponsorLevel=pulse for expired channel")
	}
	// 探测不足不应产生 board override（不应因无法判断可用率而强制 secondary）
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("min_probes 不足时不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredButHealthy_BoardPreservedLevelDowngraded 验证解耦后新语义：
// 已到期但 7 天可用率健康（100%，高于所有阈值）→ 板块不降 secondary，仅 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredButHealthy_BoardPreservedLevelDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "healthyexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 可用率 100%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "healthyexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	// 必须有 override（含 SponsorLevel=pulse），但板块不得是 secondary 或 cold
	if !ok {
		t.Fatal("expected override with SponsorLevel=pulse for expired backbone channel")
	}
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("到期健康通道板块不应因到期降级，期望不是 secondary/cold，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredSecondaryAnchor_HealthyStaysSecondaryLevelDowngraded 验证
// 到期解耦 × 锚点语义的叠加：configBoard=secondary 的到期通道即使可用率 100%
// 也不得被升入 hot（锚点=天花板），同时 SponsorLevel 正常降为 pulse。
func TestEvaluate_ExpiredSecondaryAnchor_HealthyStaysSecondaryLevelDowngraded(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "expiredsecondary", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 可用率 100%，旧逻辑会触发 promote

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "expiredsecondary", Service: "cc", Channel: "vip", Board: "secondary",
		SponsorLevel: config.SponsorLevelBeacon, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override with SponsorLevel=pulse for expired beacon channel")
	}
	if ov.Board != "" {
		t.Errorf("configBoard=secondary 的到期通道不应写任何 board override（锚点语义），实际 board=%q", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_AutoColdExemptDemotesSecondary 验证解耦后语义：
// auto_cold_exempt + 到期 + 可用率 0% → 走可用率降级路径（hot→secondary），同时降级 SponsorLevel。
// auto_cold_exempt 阻止冷板，但不阻止 secondary。
func TestEvaluate_ExpiredAndDead_AutoColdExemptDemotesSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "exemptexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "exemptexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoColdExempt: true,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for auto_cold_exempt expired dead channel")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt 应阻止冷板")
	}
	// 可用率 0% < threshold_down=50%，应触发 hot→secondary
	if ov.Board != "secondary" {
		t.Errorf("可用率降级应产生 secondary，实际 board=%s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并进同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_AutoMoveExemptBoardUntouched 验证解耦后语义：
// auto_move_exempt + 到期 → 跳过板块评估，仅降级 SponsorLevel（Board 为空）。
func TestEvaluate_ExpiredAndDead_AutoMoveExemptBoardUntouched(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "moveexemptexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%（不影响，因为 exempt 跳过板块评估）

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "moveexemptexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoMoveExempt: true,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("auto_move_exempt 到期通道应产生 SponsorLevel=pulse override")
	}
	// auto_move_exempt 跳过板块评估，不产生 board
	if ov.Board != "" {
		t.Errorf("auto_move_exempt 不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_BoundaryNotCold 锁定严格 `<`：
// 可用率正好等于 threshold_cold 时不冷板；到期通道走普通 availability 路径，
// 10%==threshold_cold 且低于 threshold_down(50%) → hot→secondary（availability-driven）。
func TestEvaluate_ExpiredAndDead_BoundaryNotCold(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "boundaryexpired", Service: "cc", Channel: "vip"}
	// 全黄 + degraded_weight=0.10 → 可用率恰为 10.0%，等于 threshold_cold。
	store.history[key] = makeRecords(2, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "boundaryexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})
	cfg.DegradedWeight = 0.10

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for boundary expired channel")
	}
	// 可用率==threshold_cold 不触发冷板（严格 <）；10% < threshold_down(50%) → hot→secondary
	if ov.Board == "cold" {
		t.Fatalf("可用率==threshold_cold 不应冷板，实际 board=%s", ov.Board)
	}
	if ov.Board != "secondary" {
		t.Fatalf("可用率 10%% < threshold_down 50%%，期望 board=secondary（availability-driven），实际 %s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_HistoryErrorAppliesLevelDowngrade 验证解耦后新语义：
// 历史查询失败时无法判定可用率 → 无板块 override；但到期的 SponsorLevel 降级仍应生效。
func TestEvaluate_ExpiredAndDead_HistoryErrorAppliesLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	store.batchErr = errors.New("db unavailable")
	key := storage.MonitorKey{Provider: "errexpired", Service: "cc", Channel: "vip"}

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "errexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("查询失败时到期项的 SponsorLevel 降级仍应产生 override")
	}
	// 查询失败无法判断可用率，不应产生板块 override
	if ov.Board == "secondary" || ov.Board == "cold" {
		t.Errorf("历史查询失败不应产生板块 override，实际 board=%s", ov.Board)
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAndDead_ColdExemptBreaksStickyToSecondary 验证：
// 已是 sticky cold 的到期项，设置 auto_cold_exempt 后打破 sticky；
// 可用率 0% < threshold_down → availability-driven hot→secondary；同时 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredAndDead_ColdExemptBreaksStickyToSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "stickyexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20) // 可用率 0%

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "stickyexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday, AutoColdExempt: true,
	})

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override after exempt breaks sticky cold")
	}
	if ov.Board == "cold" {
		t.Fatal("auto_cold_exempt 应打破 sticky cold")
	}
	// auto_cold_exempt 打破 sticky 后，按 configBoard=hot 评估：0% < threshold_down → secondary
	if ov.Board != "secondary" {
		t.Fatalf("0%% 可用率应 hot→secondary，期望 board=secondary，实际 %s", ov.Board)
	}
	// 到期降级 SponsorLevel 合并到同一 override
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired backbone channel, got %s", ov.SponsorLevel)
	}
}

func TestUpdateConfig_AutoColdExemptPurgesColdOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "recover", Service: "cc", Channel: "vip"}

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "recover", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "auto cold"},
	})

	// 热更新：设置 auto_cold_exempt
	cfg2 := &config.AppConfig{
		Boards:            cfg.Boards,
		DegradedWeight:    cfg.DegradedWeight,
		BatchQueryMaxKeys: cfg.BatchQueryMaxKeys,
		Monitors: []config.ServiceConfig{
			{Provider: "recover", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true},
		},
	}
	svc.UpdateConfig(cfg2)

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("expected cold override to be purged by auto_cold_exempt")
	}
}

func TestOnOverrideChange_CalledOnColdTransition(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "cb", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(0, 20)

	cfg := &config.AppConfig{
		Boards: config.BoardsConfig{
			Enabled: true,
			AutoMove: config.BoardAutoMoveConfig{
				Enabled:               true,
				ThresholdCold:         10.0,
				ThresholdDown:         50.0,
				ThresholdUp:           55.0,
				CheckInterval:         "30m",
				CheckIntervalDuration: 30 * time.Minute,
				MinProbes:             10,
			},
		},
		DegradedWeight:    0.7,
		BatchQueryMaxKeys: 300,
		Monitors: []config.ServiceConfig{
			{Provider: "cb", Service: "cc", Channel: "vip", Board: "hot"},
		},
	}

	called := make(chan struct{}, 1)
	svc := NewService(store, cfg)
	svc.SetOnOverrideChange(func() {
		select {
		case called <- struct{}{}:
		default:
		}
	})
	svc.Evaluate(context.Background())

	select {
	case <-called:
		// ok
	case <-time.After(time.Second):
		t.Fatal("expected onOverrideChange callback to fire")
	}
}

func TestIsCold_PSCPropagation(t *testing.T) {
	svc := NewService(nil, &config.AppConfig{})
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c", Model: "root"}: {Board: "cold", ColdReason: "test"},
	})

	// 同 PSC 的子模型也应被判定为 cold
	if !svc.IsCold(storage.MonitorKey{Provider: "p", Service: "s", Channel: "c", Model: "child"}) {
		t.Fatal("expected IsCold to propagate to child model via PSC")
	}
	// 不同 PSC 不应被判定
	if svc.IsCold(storage.MonitorKey{Provider: "p", Service: "s", Channel: "other"}) {
		t.Fatal("expected IsCold to not propagate to different channel")
	}
}

func TestApplyOverrides_SponsorLevelDowngradeRefreshesAnnotations(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {SponsorLevel: config.SponsorLevelPulse},
	}

	// 生产实况同款：一条 annotation_rules 配置的、与 sponsor 无关的规则注解（如风险提示）。
	// 用真实规则驱动、而不是把它写死进 fixture 的 Annotations 字段——
	// 因为 ApplyOverrides 走的是完整 resolveAnnotations 重算，规则注解必须靠规则本身重新命中，
	// 而不是"沿用旧数组里的值"，这正是选完整重算而非手术式局部替换的关键行为。
	rules := []config.AnnotationRule{
		{
			Match: config.AnnotationMatch{Provider: "p"},
			Add: []config.Annotation{
				{ID: "risk_warning", Family: config.AnnotationFamilyNegative, Label: "无关注解", Priority: 100},
			},
		},
	}

	monitors := []config.ServiceConfig{
		{
			Provider:     "p",
			Service:      "s",
			Channel:      "c",
			SponsorLevel: config.SponsorLevelBeacon,
			// 模拟配置热加载时算好、存死在字段里的旧注解（与生产 worldbase 实况一致）。
			Annotations: []config.Annotation{
				{ID: "sponsor_beacon", Family: config.AnnotationFamilyPositive, Icon: "beacon", Label: "信标链路", Priority: 60, Origin: "system"},
				{ID: "risk_warning", Family: config.AnnotationFamilyNegative, Label: "无关注解", Priority: 100, Origin: "rule"},
			},
		},
	}

	result := ApplyOverrides(monitors, overrides, rules, 0)

	if result[0].SponsorLevel != config.SponsorLevelPulse {
		t.Fatalf("sponsor_level=%s, want pulse", result[0].SponsorLevel)
	}

	var sawStaleBeacon, sawPulse, sawUnrelated bool
	for _, ann := range result[0].Annotations {
		switch ann.ID {
		case "sponsor_beacon":
			sawStaleBeacon = true
		case "sponsor_pulse":
			sawPulse = true
		case "risk_warning":
			sawUnrelated = true
		}
	}
	if sawStaleBeacon {
		t.Errorf("stale sponsor_beacon annotation still present: %+v", result[0].Annotations)
	}
	if !sawPulse {
		t.Errorf("expected sponsor_pulse annotation after downgrade, got %+v", result[0].Annotations)
	}
	if !sawUnrelated {
		t.Errorf("rule-driven annotation should survive the refresh (rule re-applied), got %+v", result[0].Annotations)
	}
}

func TestApplyOverrides_ColdReasonPropagation(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {Board: "cold", ColdReason: "auto cold test"},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "hot"},
		{Provider: "p", Service: "s", Channel: "c", Model: "gpt-4o", Parent: "p/s/c", Board: "hot"},
	}

	result := ApplyOverrides(monitors, overrides, nil, 0)

	if result[0].Board != "cold" || result[0].ColdReason != "auto cold test" {
		t.Fatalf("root: board=%s cold_reason=%q", result[0].Board, result[0].ColdReason)
	}
	if result[1].Board != "cold" || result[1].ColdReason != "auto cold test" {
		t.Fatalf("child: board=%s cold_reason=%q", result[1].Board, result[1].ColdReason)
	}
}

// TestApplyOverrides_ColdOverrideWithLatchedModelsHidesBoardReasonModels 验证契约互斥：
// cold override 为保留质量闩锁会留存 QualityTriggerModels，但 BoardReason 为空——注入到
// ServiceConfig 时 BoardReasonModels 必须一并置空，避免响应出现"孤立"的 board_reason_models。
func TestApplyOverrides_ColdOverrideWithLatchedModelsHidesBoardReasonModels(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {
			Board:                "cold",
			ColdReason:           "auto cold test",
			QualityLatched:       true,
			QualityTriggerModels: "claude-opus", // 闩锁状态留存，但不应作为展示原因泄漏
		},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "hot"},
	}

	result := ApplyOverrides(monitors, overrides, nil, 0)

	if result[0].BoardReason != "" {
		t.Fatalf("board_reason=%q, want empty (cold, no quality reason)", result[0].BoardReason)
	}
	if result[0].BoardReasonModels != "" {
		t.Fatalf("board_reason_models=%q, want empty (must not leak without board_reason)", result[0].BoardReasonModels)
	}
}

// TestApplyOverrides_QualityBoardReasonPropagation 验证质量移板原因+模型名正常传播（含子通道）。
func TestApplyOverrides_QualityBoardReasonPropagation(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {
			Board:                "secondary",
			BoardReason:          "quality_hardfail",
			QualityTriggerModels: "claude-opus",
			QualityLatched:       true,
		},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "hot"},
		{Provider: "p", Service: "s", Channel: "c", Model: "gpt-4o", Parent: "p/s/c", Board: "hot"},
	}

	result := ApplyOverrides(monitors, overrides, nil, 0)

	for i, r := range result {
		if r.Board != "secondary" || r.BoardReason != "quality_hardfail" || r.BoardReasonModels != "claude-opus" {
			t.Fatalf("row %d: board=%s board_reason=%q models=%q", i, r.Board, r.BoardReason, r.BoardReasonModels)
		}
	}
}

func TestApplyOverrides_ClearsColdReasonOnNonCold(t *testing.T) {
	overrides := map[storage.MonitorKey]MonitorOverride{
		{Provider: "p", Service: "s", Channel: "c"}: {Board: "secondary"},
	}

	monitors := []config.ServiceConfig{
		{Provider: "p", Service: "s", Channel: "c", Board: "cold", ColdReason: "旧原因"},
	}

	result := ApplyOverrides(monitors, overrides, nil, 0)

	if result[0].Board != "secondary" {
		t.Fatalf("board=%s, want secondary", result[0].Board)
	}
	if result[0].ColdReason != "" {
		t.Fatalf("cold_reason=%q, want empty", result[0].ColdReason)
	}
}

// === 到期解耦新增测试 ===

// TestEvaluate_ExpiredAndCold_ColdWithLevelDowngrade 验证解耦后的叠加语义：
// 到期 + 可用率低于 threshold_cold（探测充足）→ availability-driven 冷板 + SponsorLevel=pulse 叠加。
func TestEvaluate_ExpiredAndCold_ColdWithLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "coldexpired", Service: "cc", Channel: "vip"}
	// 全红 20 条 → 可用率 0% < threshold_cold=10%，探测充足
	store.history[key] = makeRecords(0, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "coldexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBeacon, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for expired+low-availability channel")
	}
	// availability < threshold_cold → 冷板（availability-driven）
	if ov.Board != "cold" {
		t.Errorf("expected board=cold (availability-driven), got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Error("expected ColdReason to be populated")
	}
	// 到期降级 SponsorLevel 合并到同一 override（board 和 level 独立，互不覆盖）
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expected sponsor_level=pulse for expired beacon channel, got %s", ov.SponsorLevel)
	}
}

// TestEvaluate_ExpiredAlreadyPulse_NoSponsorDowngrade 验证：
// 到期但 SponsorLevel 已是 pulse → 不产生 sponsor 降级（不"升级"），且可用率健康不产生板块 override。
func TestEvaluate_ExpiredAlreadyPulse_NoSponsorDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "pulsexpired", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 可用率 100%，不触发板块降级

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "pulsexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelPulse, ExpiresAt: yesterday,
	})

	svc := NewService(store, cfg)
	svc.Evaluate(context.Background())

	// 可用率 100% 且已是 pulse 等级 → 无任何 override
	_, ok := svc.GetBoardOverride(key)
	if ok {
		t.Error("已是 pulse 等级的到期通道（可用率健康）不应产生任何 override")
	}
}

// TestEvaluate_ExpiredStickyNotExempt_ColdWithLevelDowngrade 验证排序依赖：
// expiry check 必须在 sticky-cold 块之前。
// 非 exempt 的 sticky cold 到期通道：sticky cold 保留，且 SponsorLevel 降为 pulse。
func TestEvaluate_ExpiredStickyNotExempt_ColdWithLevelDowngrade(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "stickycoldexpired", Service: "cc", Channel: "vip"}
	// 即使可用率健康，sticky cold 也不被清除
	store.history[key] = makeRecords(1, 20)

	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "stickycoldexpired", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelCore, ExpiresAt: yesterday,
		// AutoColdExempt=false（默认）：sticky cold 会保留
	})

	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{
		key: {Board: "cold", ColdReason: "之前自动冷板"},
	})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override (sticky cold should be preserved)")
	}
	// sticky cold 保留
	if ov.Board != "cold" {
		t.Errorf("expected board=cold (sticky), got %s", ov.Board)
	}
	if ov.ColdReason == "" {
		t.Error("expected ColdReason to be preserved")
	}
	// 到期降级 SponsorLevel 必须叠加（expiry check 在 sticky-cold 前记录，applySponsorDowngrades 在 return 前合并）
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("expired sticky-cold channel must get SponsorLevel=pulse, got %s", ov.SponsorLevel)
	}
}

func TestMonitorOverride_QualityFields_EqualityByValue(t *testing.T) {
	a := MonitorOverride{Board: "secondary", BoardReason: "quality_hardfail", QualityLatched: true, QualityTriggerModels: "m1,m2"}
	b := a
	am := map[storage.MonitorKey]MonitorOverride{{Provider: "p"}: a}
	bm := map[storage.MonitorKey]MonitorOverride{{Provider: "p"}: b}
	if !overridesEqual(am, bm) {
		t.Fatalf("identical overrides must be equal")
	}
	b.QualityRecoveryCount = 1
	bm2 := map[storage.MonitorKey]MonitorOverride{{Provider: "p"}: b}
	if overridesEqual(am, bm2) {
		t.Fatalf("differing recovery count must be unequal")
	}
}

func TestOverrideRecordMapping_QualityRoundTrip(t *testing.T) {
	key := storage.MonitorKey{Provider: "Acme", Service: "cc", Channel: "Acme-CC", Model: "m"}
	ov := MonitorOverride{
		Board: "secondary", ColdReason: "", SponsorLevel: config.SponsorLevelPulse,
		BoardReason: "quality_hardfail", QualityLatched: true, QualityRecoveryCount: 2,
		QualityTriggerModels: "claude-opus", QualityLastGeneration: 7, AvailabilityLatched: false,
	}
	recs := overridesToRecords(map[storage.MonitorKey]MonitorOverride{key: ov})
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	back := recordsToOverrides(recs)
	if back[key] != ov {
		t.Fatalf("round-trip mismatch: %+v != %+v", back[key], ov)
	}
}

// ============================================================================
// Task 6: 双闩锁（可用率 + 质量）复合评估测试。
// fakeQuality 是 qualitySource 的测试替身；qsnap 构造一份 Fresh 快照，
// 单通道按 canonical 三元组键（ScoreKey）落桶。
// ============================================================================

type fakeQuality struct {
	snap rpdiag.QualitySnapshot
	err  error
}

func (f fakeQuality) QualitySignals(context.Context) (rpdiag.QualitySnapshot, error) {
	return f.snap, f.err
}

// qsnap 构造一份 Fresh=true 的质量快照，单通道信号按三元组落桶。
func qsnap(gen uint64, provider, service, channel string, sig rpdiag.ChannelQualitySignal) rpdiag.QualitySnapshot {
	return rpdiag.QualitySnapshot{
		Generation: gen,
		Fresh:      true,
		ByBucket:   map[string]rpdiag.ChannelQualitySignal{rpdiag.ScoreKey(provider, service, channel): sig},
	}
}

// 场景 1：hot + 可用率健康 + 质量 HardFail → secondary、BoardReason quality_hardfail、QualityLatched。
func TestEvaluate_HardFail_MovesHotToSecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q1", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 100% 健康
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q1", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q1", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"claude-opus"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override for quality hard-fail channel")
	}
	if ov.Board != "secondary" {
		t.Errorf("board=%s, want secondary", ov.Board)
	}
	if ov.BoardReason != "quality_hardfail" {
		t.Errorf("BoardReason=%q, want quality_hardfail", ov.BoardReason)
	}
	if !ov.QualityLatched {
		t.Error("want QualityLatched=true")
	}
	if ov.QualityTriggerModels != "claude-opus" {
		t.Errorf("QualityTriggerModels=%q, want claude-opus", ov.QualityTriggerModels)
	}
}

// 场景 2：闩锁后需要 K 个代次互异的 Recovered 才升板。
func TestEvaluate_RecoveryDebounce_NeedsKDistinctGenerations(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q2", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q2", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)

	// HardFail gen=5 建立闩锁
	svc.SetQualitySource(fakeQuality{snap: qsnap(5, "q2", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())
	if ov, ok := svc.GetBoardOverride(key); !ok || ov.Board != "secondary" || !ov.QualityLatched {
		t.Fatalf("hardfail 后应为闩锁 secondary，得到 ok=%v %+v", ok, ov)
	}

	// Recovered gen=6 → count 1，仍 secondary
	svc.SetQualitySource(fakeQuality{snap: qsnap(6, "q2", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("gen6 应仍 secondary，得到 ok=%v %+v", ok, ov)
	}
	if ov.QualityRecoveryCount != 1 {
		t.Fatalf("gen6 应 count=1，得到 %d", ov.QualityRecoveryCount)
	}

	// Recovered gen=6 同一代次 → count 保持 1
	svc.SetQualitySource(fakeQuality{snap: qsnap(6, "q2", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	ov, ok = svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("同代次应仍 secondary，得到 ok=%v %+v", ok, ov)
	}
	if ov.QualityRecoveryCount != 1 {
		t.Fatalf("同代次不得重复计数，得到 count=%d", ov.QualityRecoveryCount)
	}

	// Recovered gen=7 → count 2 → 升板 hot（无 override）
	svc.SetQualitySource(fakeQuality{snap: qsnap(7, "q2", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Fatalf("debounce 达标应升板 hot（无 override），得到 %+v", ov)
	}
}

// 场景 3：闩锁 secondary，Fresh=false → 板块与全部质量字段逐字节冻结。
func TestEvaluate_FeedNotFresh_FreezesLatch(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q3", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q3", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)
	prior := MonitorOverride{
		Board: "secondary", BoardReason: "quality_hardfail",
		QualityLatched: true, QualityRecoveryCount: 1,
		QualityTriggerModels: "claude-opus,claude-sonnet", QualityLastGeneration: 42,
		AvailabilityLatched: false,
	}
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{key: prior})
	// Fresh=false → 冻结
	svc.SetQualitySource(fakeQuality{snap: rpdiag.QualitySnapshot{Generation: 99, Fresh: false}})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected frozen override")
	}
	if ov != prior {
		t.Fatalf("非新鲜信号必须逐字节冻结\n got %+v\nwant %+v", ov, prior)
	}
}

// 场景 4：未注入 qualitySource → 已有闩锁保持 secondary（冻结而非升板）。
func TestEvaluate_NilQualitySource_FreezesNotClears(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q4", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q4", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{key: {
		Board: "secondary", BoardReason: "quality_hardfail",
		QualityLatched: true, QualityTriggerModels: "m", QualityLastGeneration: 3,
	}})
	// 不调用 SetQualitySource → qualitySource==nil → 冻结
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("nil 源必须冻结闩锁为 secondary，得到 ok=%v %+v", ok, ov)
	}
	if !ov.QualityLatched || ov.BoardReason != "quality_hardfail" {
		t.Fatalf("nil 源不得清除闩锁，得到 %+v", ov)
	}
}

// 场景 5：52% 可用率自身应留 hot；质量短暂降板再恢复(>=K) → 板块回 hot
// （可用率闩锁独立判定，绝不因质量降板被卡在 secondary）。
func TestEvaluate_DualLatch_QualityDoesNotPolluteAvailability(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q5", Service: "cc", Channel: "vip"}
	ts := time.Now().UTC().Unix()
	records := make([]*storage.ProbeRecord, 100)
	for i := 0; i < 52; i++ {
		records[i] = &storage.ProbeRecord{Status: 1, Timestamp: ts}
	}
	for i := 52; i < 100; i++ {
		records[i] = &storage.ProbeRecord{Status: 0, Timestamp: ts}
	}
	store.history[key] = records
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q5", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)

	// 基线：52% 无质量信号（nil 源）留 hot（自身可用率闩锁不闭合）。
	svc.Evaluate(context.Background())
	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("52% 无质量信号应留 hot（无 override）")
	}

	// 质量 HardFail gen=1 → 仅质量降板 secondary；可用率闩锁必须仍 false。
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q5", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("质量 hardfail 应降板，得到 ok=%v %+v", ok, ov)
	}
	if ov.AvailabilityLatched {
		t.Fatal("52% 处可用率闩锁绝不能闭合（质量不得污染可用率闩锁）")
	}

	// Recovered gen=2（count 1）再 gen=3（count 2 → 升板）。
	svc.SetQualitySource(fakeQuality{snap: qsnap(2, "q5", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	svc.SetQualitySource(fakeQuality{snap: qsnap(3, "q5", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())

	// 板块必须回 hot——可用率闩锁独立判定（52% 从未闭合它）。
	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Fatalf("质量恢复 >=K 应回 hot（可用率闩锁独立），得到 %+v", ov)
	}
}

// 场景 6：sticky cold 通道，质量 Recovered → 仍 cold（质量不解 cold）。
func TestEvaluate_StickyCold_QualityDoesNotUnstick(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q6", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q6", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{key: {Board: "cold", ColdReason: "prior auto cold"}})
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q6", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "cold" {
		t.Fatalf("sticky cold 应持续（质量恢复不解 cold），得到 ok=%v %+v", ok, ov)
	}
}

// 场景 7：auto_move_exempt 通道 + 质量 HardFail → 不移板。
func TestEvaluate_AutoMoveExempt_SkipsQualityDemotion(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q7", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q7", Service: "cc", Channel: "vip", Board: "hot", AutoMoveExempt: true})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q7", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	if _, ok := svc.GetBoardOverride(key); ok {
		t.Fatal("auto_move_exempt 应跳过质量降板（无 override）")
	}
}

// 场景 8：auto_cold_exempt（仅冷板豁免）+ 质量 HardFail → 降板 secondary。
func TestEvaluate_AutoColdExempt_StillQualitySecondary(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q8", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q8", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q8", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("auto_cold_exempt + 质量 hardfail 应降板 secondary，得到 ok=%v %+v", ok, ov)
	}
	if ov.BoardReason != "quality_hardfail" {
		t.Errorf("BoardReason=%q, want quality_hardfail", ov.BoardReason)
	}
}

// 场景 9：configBoard=secondary + 质量 HardFail → 保持 secondary 且 BoardReason ""
// （已在备板，质量未造成实际移板，不作虚假"因质量移板"声明）。
func TestEvaluate_ConfigSecondary_NoQualityOverride(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q9", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q9", Service: "cc", Channel: "vip", Board: "secondary"})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q9", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if ok && ov.BoardReason != "" {
		t.Fatalf("config secondary + 质量不得声明质量移板，BoardReason=%q", ov.BoardReason)
	}
	if ok && ov.Board != "secondary" {
		t.Fatalf("config secondary 必须保持 secondary，得到 %s", ov.Board)
	}
}

// 场景 10：HardFail→Recovered(count1)→Unknown(count 保持1，代次推进)→Recovered(count2→升板)。
func TestEvaluate_Recovered_UnknownDoesNotConsumeCount(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q10", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20)
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q10", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)

	svc.SetQualitySource(fakeQuality{snap: qsnap(10, "q10", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	svc.SetQualitySource(fakeQuality{snap: qsnap(11, "q10", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	if ov, _ := svc.GetBoardOverride(key); ov.QualityRecoveryCount != 1 {
		t.Fatalf("首个 Recovered 应 count=1，得到 %d", ov.QualityRecoveryCount)
	}

	// Unknown 必须保持：不动计数、不清闩锁、推进代次。
	svc.SetQualitySource(fakeQuality{snap: qsnap(12, "q10", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityUnknown})})
	svc.Evaluate(context.Background())
	ov, ok := svc.GetBoardOverride(key)
	if !ok || ov.Board != "secondary" {
		t.Fatalf("Unknown 应保持闩锁 secondary，得到 ok=%v %+v", ok, ov)
	}
	if ov.QualityRecoveryCount != 1 {
		t.Fatalf("Unknown 不得消耗计数，得到 %d", ov.QualityRecoveryCount)
	}
	if ov.QualityLastGeneration != 12 {
		t.Fatalf("Unknown 应推进代次到 12，得到 %d", ov.QualityLastGeneration)
	}

	// 新代次 Recovered → count 2 → 升板。
	svc.SetQualitySource(fakeQuality{snap: qsnap(13, "q10", "cc", "vip", rpdiag.ChannelQualitySignal{State: rpdiag.QualityRecovered})})
	svc.Evaluate(context.Background())
	if ov, ok := svc.GetBoardOverride(key); ok {
		t.Fatalf("第二个代次互异的 Recovered 应升板 hot，得到 %+v", ov)
	}
}

// 场景 11：质量 HardFail secondary + 赞助到期 → 二者共存（board secondary、
// BoardReason quality_hardfail、SponsorLevel 降级）。
func TestEvaluate_HardFailAndSponsorExpiry_MergeNotClobber(t *testing.T) {
	store := newMockStorage()
	key := storage.MonitorKey{Provider: "q11", Service: "cc", Channel: "vip"}
	store.history[key] = makeRecords(1, 20) // 健康：板块移动纯由质量驱动
	yesterday := time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02")
	cfg := expiredAutoMoveCfg(config.ServiceConfig{
		Provider: "q11", Service: "cc", Channel: "vip", Board: "hot",
		SponsorLevel: config.SponsorLevelBackbone, ExpiresAt: yesterday,
	})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q11", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"claude-opus"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("expected override")
	}
	if ov.Board != "secondary" {
		t.Errorf("board=%s, want secondary", ov.Board)
	}
	if ov.BoardReason != "quality_hardfail" {
		t.Errorf("BoardReason=%q, want quality_hardfail", ov.BoardReason)
	}
	if !ov.QualityLatched {
		t.Error("want QualityLatched=true")
	}
	if ov.SponsorLevel != config.SponsorLevelPulse {
		t.Errorf("SponsorLevel=%s, want pulse", ov.SponsorLevel)
	}
}

// 场景 12：历史查询失败 → 冻结可用率但仍应用质量决策（fresh HardFail 把 hot 降板 secondary）。
func TestEvaluate_HistoryQueryFails_FreezesAvailabilityButAppliesQuality(t *testing.T) {
	store := newMockStorage()
	store.batchErr = errors.New("db unavailable")
	key := storage.MonitorKey{Provider: "q12", Service: "cc", Channel: "vip"}
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "q12", Service: "cc", Channel: "vip", Board: "hot"})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "q12", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("DB 失败仍应应用质量降板")
	}
	if ov.Board != "secondary" {
		t.Errorf("board=%s, want secondary（质量在可用率冻结时仍能上限压 secondary）", ov.Board)
	}
	if ov.BoardReason != "quality_hardfail" || !ov.QualityLatched {
		t.Errorf("DB 失败时质量闩锁必须应用，得到 %+v", ov)
	}
}

// ============================================================================
// 代码质量复审修复（Fix 1 + Fix 2）回归测试。
// ============================================================================

// Fix 1：computeQualityLatch 冻结时必须逐字节沿用 prev.BoardReason，绝不"发明"
// reason。config-secondary 通道被持久化为 QualityLatched=true 且 BoardReason=""，
// 冻结必须保持 ""（不能凭 QualityLatched 反推 "quality_hardfail"）。
func TestComputeQualityLatch_FreezeReasonVerbatim(t *testing.T) {
	prev := MonitorOverride{Board: "secondary", QualityLatched: true, BoardReason: ""}
	d := computeQualityLatch(prev, false, 7, rpdiag.ChannelQualitySignal{})
	if d.reason != "" {
		t.Fatalf("冻结不得发明 reason（应逐字节沿用 prev.BoardReason=%q），得到 %q", prev.BoardReason, d.reason)
	}
	if !d.latched {
		t.Fatal("冻结应保持闩锁")
	}

	// 反向：prev 带 reason 时冻结必须原样保留。
	prev2 := MonitorOverride{Board: "secondary", QualityLatched: true, BoardReason: "quality_hardfail"}
	if d2 := computeQualityLatch(prev2, false, 7, rpdiag.ChannelQualitySignal{}); d2.reason != "quality_hardfail" {
		t.Fatalf("冻结应保留 prev.BoardReason=quality_hardfail，得到 %q", d2.reason)
	}
}

// Fix 2：config-secondary 通道 + 历史查询失败 + fresh HardFail →
// 冻结须对齐配置锚点 secondary，reason 保持 ""（不作虚假质量移板声明），QualityLatched=true。
func TestEvaluate_ConfigSecondary_HistoryFails_FreezesToAnchorNoReason(t *testing.T) {
	store := newMockStorage()
	store.batchErr = errors.New("db unavailable")
	key := storage.MonitorKey{Provider: "fs1", Service: "cc", Channel: "vip"}
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "fs1", Service: "cc", Channel: "vip", Board: "secondary"})
	svc := NewService(store, cfg)
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "fs1", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("config-secondary + DB 失败 + 质量闩锁应产生冻结 override")
	}
	if ov.Board != "secondary" {
		t.Errorf("board=%s, want secondary（对齐配置锚点）", ov.Board)
	}
	if ov.BoardReason != "" {
		t.Errorf("config-secondary 不得声明质量移板，BoardReason=%q want \"\"", ov.BoardReason)
	}
	if !ov.QualityLatched {
		t.Error("want QualityLatched=true")
	}
}

// Fix 2：auto_cold_exempt 通道其 prev override 为 cold + 历史查询失败 + fresh HardFail →
// 冻结须对齐配置锚点 hot（而非沿用 prev 的 stale cold），质量把上限压到 secondary（不是 cold）。
func TestEvaluate_AutoColdExemptStaleCold_HistoryFails_FreezesToSecondaryNotCold(t *testing.T) {
	store := newMockStorage()
	store.batchErr = errors.New("db unavailable")
	key := storage.MonitorKey{Provider: "fs2", Service: "cc", Channel: "vip"}
	cfg := expiredAutoMoveCfg(config.ServiceConfig{Provider: "fs2", Service: "cc", Channel: "vip", Board: "hot", AutoColdExempt: true})
	svc := NewService(store, cfg)
	// prev override 遗留 cold（auto_cold_exempt 会打破 sticky，进入候选正常评估）。
	svc.SetOverrides(map[storage.MonitorKey]MonitorOverride{key: {Board: "cold", ColdReason: "prior auto cold"}})
	svc.SetQualitySource(fakeQuality{snap: qsnap(1, "fs2", "cc", "vip",
		rpdiag.ChannelQualitySignal{State: rpdiag.QualityHardFail, TriggerModels: []string{"m"}})})
	svc.Evaluate(context.Background())

	ov, ok := svc.GetBoardOverride(key)
	if !ok {
		t.Fatal("auto_cold_exempt + DB 失败 + 质量闩锁应产生冻结 override")
	}
	if ov.Board != "secondary" {
		t.Errorf("board=%s, want secondary（对齐锚点 hot 后被质量压到 secondary，绝不沿用 stale cold）", ov.Board)
	}
	if ov.ColdReason != "" {
		t.Errorf("冻结不得残留 ColdReason，得到 %q", ov.ColdReason)
	}
	if !ov.QualityLatched {
		t.Error("want QualityLatched=true")
	}
}

func hasAnn(items []config.Annotation, id string) bool {
	for _, a := range items {
		if a.ID == id {
			return true
		}
	}
	return false
}

func TestApplyOverrideToMonitor_QualityBadgeWithoutSponsor(t *testing.T) {
	m := &config.ServiceConfig{Provider: "p", Service: "cc", Channel: "c"}
	ov := MonitorOverride{Board: "secondary", BoardReason: "quality_hardfail", QualityTriggerModels: "opus-4-8"}
	applyOverrideToMonitor(m, ov, nil, 0)
	if m.BoardReason != "quality_hardfail" {
		t.Fatalf("BoardReason not injected: %q", m.BoardReason)
	}
	if !hasAnn(m.Annotations, "quality_hardfail") {
		t.Error("expected quality_hardfail annotation on non-sponsor channel (recompute timing)")
	}
}

func TestApplyOverrideToMonitor_BoardAndSponsor_RecomputesWithQuality(t *testing.T) {
	m := &config.ServiceConfig{Provider: "p", Service: "cc", Channel: "c"}
	ov := MonitorOverride{Board: "secondary", BoardReason: "quality_hardfail", QualityTriggerModels: "opus-4-8", SponsorLevel: config.SponsorLevelPulse}
	applyOverrideToMonitor(m, ov, nil, 0)
	if !hasAnn(m.Annotations, "quality_hardfail") {
		t.Error("expected quality annotation when both board+sponsor overridden")
	}
	if !hasAnn(m.Annotations, "sponsor_pulse") {
		t.Error("expected sponsor annotation recomputed with new level")
	}
}

func TestApplyOverrideToMonitor_EmptyOverride_NoRecompute(t *testing.T) {
	m := &config.ServiceConfig{Provider: "p", Service: "cc", Channel: "c",
		Annotations: []config.Annotation{{ID: "sentinel"}}}
	applyOverrideToMonitor(m, MonitorOverride{}, nil, 0)
	if !hasAnn(m.Annotations, "sentinel") {
		t.Error("empty override must not recompute/clobber annotations")
	}
}
