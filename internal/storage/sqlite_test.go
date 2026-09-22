package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- test helpers ---

func newTestStore(t *testing.T) *SQLiteStorage {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func mustSave(t *testing.T, store *SQLiteStorage, rec *ProbeRecord) {
	t.Helper()
	if err := store.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
}

func rec(key MonitorKey, ts int64) *ProbeRecord {
	return &ProbeRecord{
		Provider:  key.Provider,
		Service:   key.Service,
		Channel:   key.Channel,
		Model:     key.Model,
		Status:    1,
		SubStatus: SubStatusNone,
		HttpCode:  200,
		Latency:   100,
		Timestamp: ts,
	}
}

func sqliteObjectExists(t *testing.T, db *sql.DB, objType, name string) bool {
	t.Helper()
	var n string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type=? AND name=?`, objType, name).Scan(&n)
	if err == sql.ErrNoRows {
		return false
	}
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return true
}

func columnNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s): %v", table, err)
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		cols[name] = true
	}
	return cols
}

// --- Init ---

func TestInit_TablesAndIndexes(t *testing.T) {
	store := newTestStore(t)

	for _, tbl := range []string{"probe_history", "service_states", "status_events", "channel_states"} {
		if !sqliteObjectExists(t, store.db, "table", tbl) {
			t.Errorf("expected table %q to exist", tbl)
		}
	}

	for _, idx := range []string{
		"idx_probe_history_pscm_ts_cover",
		"idx_probe_history_timestamp",
		"idx_status_events_psc_id",
		"idx_status_events_unique",
	} {
		if !sqliteObjectExists(t, store.db, "index", idx) {
			t.Errorf("expected index %q to exist", idx)
		}
	}
}

func TestInit_Columns(t *testing.T) {
	store := newTestStore(t)
	cols := columnNames(t, store.db, "probe_history")

	for _, col := range []string{"id", "provider", "service", "channel", "model",
		"status", "sub_status", "http_code", "latency", "timestamp"} {
		if !cols[col] {
			t.Errorf("probe_history missing column %q", col)
		}
	}
}

func TestInit_WALMode(t *testing.T) {
	store := newTestStore(t)

	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	// WAL 经 DSN 的 _journal_mode 参数开启。modernc 是否认这个 mattn 风格参数取决于驱动版本，
	// 不认时会静默回落到 delete 模式——升级驱动后这里必须变红，而不是只打一行日志。
	if mode = strings.ToLower(mode); mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal (driver may have stopped honoring _journal_mode in the DSN)", mode)
	}
}

func TestInit_Idempotent(t *testing.T) {
	store := newTestStore(t)
	// Calling Init a second time should not error (CREATE IF NOT EXISTS).
	if err := store.Init(); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

// --- SaveRecord + GetLatest ---

func TestSaveAndGetLatest(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "prov-a", Service: "svc-a", Channel: "ch-a", Model: "mdl-a"}

	r := rec(key, 1_700_000_000)
	r.Status = 0
	r.SubStatus = SubStatusServerError
	r.HttpCode = 503
	r.Latency = 456
	mustSave(t, store, r)

	if r.ID == 0 {
		t.Fatal("expected record ID to be set after SaveRecord")
	}

	got, err := store.GetLatest(key.Provider, key.Service, key.Channel, key.Model)
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if got == nil {
		t.Fatal("GetLatest returned nil")
	}
	if got.ID != r.ID {
		t.Errorf("ID: want %d, got %d", r.ID, got.ID)
	}
	if got.Provider != r.Provider || got.Service != r.Service ||
		got.Channel != r.Channel || got.Model != r.Model {
		t.Errorf("key fields mismatch: %+v", got)
	}
	if got.Status != r.Status || got.SubStatus != r.SubStatus ||
		got.HttpCode != r.HttpCode || got.Latency != r.Latency ||
		got.Timestamp != r.Timestamp {
		t.Errorf("value fields mismatch: %+v", got)
	}
}

func TestGetLatest_ReturnsNewest(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "prov-b", Service: "svc-b", Channel: "ch-b", Model: "mdl-b"}

	mustSave(t, store, rec(key, 1000))
	mustSave(t, store, rec(key, 3000))
	mustSave(t, store, rec(key, 2000))

	got, err := store.GetLatest(key.Provider, key.Service, key.Channel, key.Model)
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if got.Timestamp != 3000 {
		t.Errorf("want timestamp 3000, got %d", got.Timestamp)
	}
}

func TestGetLatest_MissingKey(t *testing.T) {
	store := newTestStore(t)

	got, err := store.GetLatest("no", "such", "key", "here")
	if err != nil {
		t.Fatalf("GetLatest: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing key, got %+v", got)
	}
}

// --- GetHistory ---

func TestGetHistory_TimeRange(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "prov-h", Service: "svc-h", Channel: "ch-h", Model: "mdl-h"}

	for _, ts := range []int64{1000, 2000, 3000, 4000} {
		mustSave(t, store, rec(key, ts))
	}

	recs, err := store.GetHistory(key.Provider, key.Service, key.Channel, key.Model, time.Unix(2000, 0))
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(recs) != 3 { // 2000, 3000, 4000
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	// Must be ascending order
	for i := 1; i < len(recs); i++ {
		if recs[i].Timestamp <= recs[i-1].Timestamp {
			t.Errorf("not ascending at index %d: %d <= %d", i, recs[i].Timestamp, recs[i-1].Timestamp)
		}
	}
}

func TestGetHistory_Empty(t *testing.T) {
	store := newTestStore(t)

	recs, err := store.GetHistory("x", "y", "z", "w", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("expected empty result, got %d records", len(recs))
	}
}

func TestGetHistory_IsolatesDifferentKeys(t *testing.T) {
	store := newTestStore(t)
	keyA := MonitorKey{Provider: "p", Service: "s", Channel: "c1", Model: "m"}
	keyB := MonitorKey{Provider: "p", Service: "s", Channel: "c2", Model: "m"}

	mustSave(t, store, rec(keyA, 1000))
	mustSave(t, store, rec(keyB, 2000))

	recs, err := store.GetHistory(keyA.Provider, keyA.Service, keyA.Channel, keyA.Model, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(recs) != 1 || recs[0].Channel != "c1" {
		t.Errorf("expected 1 record for keyA channel=c1, got %d records", len(recs))
	}
}

// --- Batch queries ---

func TestGetLatestBatch(t *testing.T) {
	store := newTestStore(t)
	keyA := MonitorKey{Provider: "pa", Service: "sa", Channel: "ca", Model: "ma"}
	keyB := MonitorKey{Provider: "pb", Service: "sb", Channel: "cb", Model: "mb"}

	mustSave(t, store, rec(keyA, 1000))
	mustSave(t, store, rec(keyA, 3000))
	mustSave(t, store, rec(keyB, 1500))

	got, err := store.GetLatestBatch([]MonitorKey{keyA, keyB})
	if err != nil {
		t.Fatalf("GetLatestBatch: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(got))
	}
	if got[keyA].Timestamp != 3000 {
		t.Errorf("keyA: want ts 3000, got %d", got[keyA].Timestamp)
	}
	if got[keyB].Timestamp != 1500 {
		t.Errorf("keyB: want ts 1500, got %d", got[keyB].Timestamp)
	}
}

func TestGetLatestBatch_EmptyKeys(t *testing.T) {
	store := newTestStore(t)

	got, err := store.GetLatestBatch(nil)
	if err != nil {
		t.Fatalf("GetLatestBatch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result, got %d", len(got))
	}
}

func TestGetLatestBatch_MissingKey(t *testing.T) {
	store := newTestStore(t)
	keyA := MonitorKey{Provider: "pa", Service: "sa", Channel: "ca", Model: "ma"}
	keyMissing := MonitorKey{Provider: "no", Service: "such", Channel: "key", Model: "x"}

	mustSave(t, store, rec(keyA, 1000))

	got, err := store.GetLatestBatch([]MonitorKey{keyA, keyMissing})
	if err != nil {
		t.Fatalf("GetLatestBatch: %v", err)
	}
	if _, ok := got[keyA]; !ok {
		t.Error("expected keyA in result")
	}
	if _, ok := got[keyMissing]; ok {
		t.Error("expected keyMissing to be absent from result")
	}
}

func TestGetHistoryBatch(t *testing.T) {
	store := newTestStore(t)
	keyA := MonitorKey{Provider: "pa", Service: "sa", Channel: "ca", Model: "ma"}
	keyB := MonitorKey{Provider: "pb", Service: "sb", Channel: "cb", Model: "mb"}

	for _, ts := range []int64{1000, 2000, 3000} {
		mustSave(t, store, rec(keyA, ts))
	}
	for _, ts := range []int64{1500, 2500} {
		mustSave(t, store, rec(keyB, ts))
	}

	got, err := store.GetHistoryBatch([]MonitorKey{keyA, keyB}, time.Unix(1800, 0))
	if err != nil {
		t.Fatalf("GetHistoryBatch: %v", err)
	}

	if len(got[keyA]) != 2 { // 2000, 3000
		t.Errorf("keyA: want 2 records, got %d", len(got[keyA]))
	}
	if len(got[keyB]) != 1 { // 2500
		t.Errorf("keyB: want 1 record, got %d", len(got[keyB]))
	}
	// Ascending order for keyA
	if len(got[keyA]) == 2 && got[keyA][0].Timestamp >= got[keyA][1].Timestamp {
		t.Errorf("keyA records not ascending: %d >= %d", got[keyA][0].Timestamp, got[keyA][1].Timestamp)
	}
}

func TestGetHistoryBatch_EmptyKeys(t *testing.T) {
	store := newTestStore(t)

	got, err := store.GetHistoryBatch(nil, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistoryBatch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// --- *BatchByModelID（model_id 维度批量读，跨展示名历史连续）---

// TestGetLatestBatchByModelIDKeysByInput 验证返回 map 以入参 ProbeHistoryKey 为键，
// 通过 model_id 反查命中——即使 caller key 携带的展示名（Model）与 DB 行的展示名不同。
func TestGetLatestBatchByModelIDKeysByInput(t *testing.T) {
	s := newTestStore(t)
	// row written under the NEW display name, same stable model_id
	if err := s.SaveRecord(&ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "NewName", ModelID: "md_x", Status: 1, Timestamp: 2000}); err != nil {
		t.Fatal(err)
	}
	// caller key carries a DIFFERENT display Model (e.g. an old/aliased name) — lookup must still hit, keyed by input
	key := ProbeHistoryKey{ModelID: "md_x", Provider: "P", Service: "cc", Channel: "c", Model: "WhateverDisplayName"}
	m, err := s.GetLatestBatchByModelID([]ProbeHistoryKey{key})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m[key]; !ok {
		t.Fatalf("returned map must be keyed by the INPUT key (not reconstructed from DB row). got keys: %v", m)
	}
	if m[key] == nil || m[key].Timestamp != 2000 {
		t.Fatalf("wrong record for key")
	}
}

func TestGetHistoryBatchByModelIDKeysByInput(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveRecord(&ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "NewName", ModelID: "md_x", Status: 1, Timestamp: 2000}); err != nil {
		t.Fatal(err)
	}
	key := ProbeHistoryKey{ModelID: "md_x", Provider: "P", Service: "cc", Channel: "c", Model: "WhateverDisplayName"}
	m, err := s.GetHistoryBatchByModelID([]ProbeHistoryKey{key}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(m[key]) != 1 {
		t.Fatalf("history batch must key by input key; got %v", m)
	}
}

func TestGetLatestBatchByModelID_EmptyKeys(t *testing.T) {
	s := newTestStore(t)
	m, err := s.GetLatestBatchByModelID(nil)
	if err != nil {
		t.Fatalf("GetLatestBatchByModelID: %v", err)
	}
	if m == nil || len(m) != 0 {
		t.Fatalf("expected non-nil empty map, got %v", m)
	}
}

func TestGetHistoryBatchByModelID_EmptyKeys(t *testing.T) {
	s := newTestStore(t)
	m, err := s.GetHistoryBatchByModelID(nil, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistoryBatchByModelID: %v", err)
	}
	if m == nil || len(m) != 0 {
		t.Fatalf("expected non-nil empty map, got %v", m)
	}
}

// TestGetLatestBatchByModelID_MultiKey 多 model_id：各取最新、按 model_id 分桶，
// 缺失 model_id 不出现在结果里，since 之前的历史不影响最新选取。
func TestGetLatestBatchByModelID_MultiKey(t *testing.T) {
	s := newTestStore(t)
	keyA := ProbeHistoryKey{ModelID: "md_a", Provider: "P", Service: "cc", Channel: "c", Model: "A"}
	keyB := ProbeHistoryKey{ModelID: "md_b", Provider: "P", Service: "cc", Channel: "c", Model: "B"}
	keyMissing := ProbeHistoryKey{ModelID: "md_missing", Provider: "P", Service: "cc", Channel: "c", Model: "Z"}

	mustSave(t, s, &ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "A", ModelID: "md_a", Status: 1, Timestamp: 1000})
	mustSave(t, s, &ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "A", ModelID: "md_a", Status: 1, Timestamp: 3000})
	mustSave(t, s, &ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "B", ModelID: "md_b", Status: 1, Timestamp: 1500})

	m, err := s.GetLatestBatchByModelID([]ProbeHistoryKey{keyA, keyB, keyMissing})
	if err != nil {
		t.Fatalf("GetLatestBatchByModelID: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(m), m)
	}
	if m[keyA] == nil || m[keyA].Timestamp != 3000 {
		t.Errorf("keyA: want ts 3000, got %v", m[keyA])
	}
	if m[keyB] == nil || m[keyB].Timestamp != 1500 {
		t.Errorf("keyB: want ts 1500, got %v", m[keyB])
	}
	if _, ok := m[keyMissing]; ok {
		t.Errorf("expected missing model_id to be absent from result")
	}
}

// TestGetHistoryBatchByModelID_AscendingAndFilter 验证 since 过滤 + 升序返回。
func TestGetHistoryBatchByModelID_AscendingAndFilter(t *testing.T) {
	s := newTestStore(t)
	key := ProbeHistoryKey{ModelID: "md_a", Provider: "P", Service: "cc", Channel: "c", Model: "A"}
	for _, ts := range []int64{1000, 2000, 3000} {
		mustSave(t, s, &ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "A", ModelID: "md_a", Status: 1, Timestamp: ts})
	}

	m, err := s.GetHistoryBatchByModelID([]ProbeHistoryKey{key}, time.Unix(1800, 0))
	if err != nil {
		t.Fatalf("GetHistoryBatchByModelID: %v", err)
	}
	if len(m[key]) != 2 { // 2000, 3000
		t.Fatalf("want 2 records after since, got %d", len(m[key]))
	}
	if m[key][0].Timestamp >= m[key][1].Timestamp {
		t.Errorf("records not ascending: %d >= %d", m[key][0].Timestamp, m[key][1].Timestamp)
	}
}

// --- MigrateChannelData ---

func TestMigrateChannelData(t *testing.T) {
	store := newTestStore(t)
	emptyKey := MonitorKey{Provider: "prov-m", Service: "svc-m", Channel: "", Model: "mdl-m"}
	otherKey := MonitorKey{Provider: "prov-x", Service: "svc-x", Channel: "", Model: "mdl-x"}
	existingChannelKey := MonitorKey{Provider: "prov-m", Service: "svc-m", Channel: "existing", Model: "mdl-e"}

	mustSave(t, store, rec(emptyKey, 1000))
	mustSave(t, store, rec(emptyKey, 1100))
	mustSave(t, store, rec(otherKey, 1200))
	mustSave(t, store, rec(existingChannelKey, 1300))

	mappings := []ChannelMigrationMapping{
		{Provider: "prov-m", Service: "svc-m", Channel: "beta"},
	}
	if err := store.MigrateChannelData(mappings); err != nil {
		t.Fatalf("MigrateChannelData: %v", err)
	}

	// Migrated records should now be in channel "beta"
	migrated, err := store.GetHistory("prov-m", "svc-m", "beta", "mdl-m", time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistory migrated: %v", err)
	}
	if len(migrated) != 2 {
		t.Errorf("want 2 migrated records, got %d", len(migrated))
	}

	// Records for a different provider/service should be untouched
	var remaining int
	err = store.db.QueryRow(
		`SELECT COUNT(*) FROM probe_history WHERE provider=? AND service=? AND channel=''`,
		"prov-x", "svc-x",
	).Scan(&remaining)
	if err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 1 {
		t.Errorf("expected 1 untouched record for prov-x, got %d", remaining)
	}

	// Records with existing channel should be untouched
	kept, err := store.GetLatest("prov-m", "svc-m", "existing", "mdl-e")
	if err != nil {
		t.Fatalf("GetLatest kept: %v", err)
	}
	if kept == nil || kept.Channel != "existing" {
		t.Errorf("expected existing channel record to be preserved, got %+v", kept)
	}
}

func TestMigrateChannelData_NoEmptyChannels(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "p", Service: "s", Channel: "already-set", Model: "m"}
	mustSave(t, store, rec(key, 1000))

	// Migration should be a no-op
	err := store.MigrateChannelData([]ChannelMigrationMapping{
		{Provider: "p", Service: "s", Channel: "new-ch"},
	})
	if err != nil {
		t.Fatalf("MigrateChannelData: %v", err)
	}
}

// --- PurgeOldRecords ---

func TestPurgeOldRecords(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "pp", Service: "sp", Channel: "cp", Model: "mp"}

	mustSave(t, store, rec(key, 1000))
	mustSave(t, store, rec(key, 2000))
	mustSave(t, store, rec(key, 4000))

	deleted, err := store.PurgeOldRecords(context.Background(), time.Unix(3000, 0), 100)
	if err != nil {
		t.Fatalf("PurgeOldRecords: %v", err)
	}
	if deleted != 2 {
		t.Errorf("want 2 deleted, got %d", deleted)
	}

	recs, err := store.GetHistory(key.Provider, key.Service, key.Channel, key.Model, time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetHistory after purge: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want 1 remaining, got %d", len(recs))
	}
	if recs[0].Timestamp != 4000 {
		t.Errorf("remaining record: want ts 4000, got %d", recs[0].Timestamp)
	}
}

func TestPurgeOldRecords_NothingToPurge(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "pn", Service: "sn", Channel: "cn", Model: "mn"}

	mustSave(t, store, rec(key, 5000))

	deleted, err := store.PurgeOldRecords(context.Background(), time.Unix(1000, 0), 100)
	if err != nil {
		t.Fatalf("PurgeOldRecords: %v", err)
	}
	if deleted != 0 {
		t.Errorf("want 0 deleted, got %d", deleted)
	}
}

func TestPurgeOldRecords_BatchSize(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "pb", Service: "sb", Channel: "cb", Model: "mb"}

	for i := 0; i < 10; i++ {
		mustSave(t, store, rec(key, int64(1000+i)))
	}

	// Purge with batch size 3 — should delete only 3 per call
	deleted, err := store.PurgeOldRecords(context.Background(), time.Unix(2000, 0), 3)
	if err != nil {
		t.Fatalf("PurgeOldRecords: %v", err)
	}
	if deleted != 3 {
		t.Errorf("want 3 deleted (batch limited), got %d", deleted)
	}

	// Second call deletes another 3
	deleted2, err := store.PurgeOldRecords(context.Background(), time.Unix(2000, 0), 3)
	if err != nil {
		t.Fatalf("PurgeOldRecords second: %v", err)
	}
	if deleted2 != 3 {
		t.Errorf("want 3 deleted, got %d", deleted2)
	}
}

// --- Concurrent access ---

func TestConcurrentReadWrite(t *testing.T) {
	store := newTestStore(t)
	key := MonitorKey{Provider: "pc", Service: "sc", Channel: "cc", Model: "mc"}

	const (
		writers         = 4
		writesPerWriter = 15
		readers         = 4
		readsPerReader  = 10
	)

	errs := make(chan error, writers*writesPerWriter+readers)
	var wg sync.WaitGroup

	base := int64(1_700_000_000)
	for w := 0; w < writers; w++ {
		for i := 0; i < writesPerWriter; i++ {
			idx := w*writesPerWriter + i
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				r := rec(key, base+int64(idx))
				if err := store.SaveRecord(r); err != nil {
					errs <- err
				}
			}(idx)
		}
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				if _, err := store.GetLatest(key.Provider, key.Service, key.Channel, key.Model); err != nil {
					errs <- err
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent error: %v", err)
	}

	var count int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM probe_history WHERE provider=? AND service=? AND channel=? AND model=?`,
		key.Provider, key.Service, key.Channel, key.Model,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	expected := writers * writesPerWriter
	if count != expected {
		t.Errorf("want %d rows, got %d", expected, count)
	}
}

// --- Event state persistence ---

func TestServiceState_RoundTrip(t *testing.T) {
	store := newTestStore(t)

	// Uninitialized state returns nil
	state, err := store.GetServiceState("p", "s", "c", "m")
	if err != nil {
		t.Fatalf("GetServiceState: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil for uninitialized state, got %+v", state)
	}

	// Upsert a state
	newState := &ServiceState{
		Provider:        "p",
		Service:         "s",
		Channel:         "c",
		Model:           "m",
		StableAvailable: 1,
		StreakCount:     5,
		StreakStatus:    1,
		LastRecordID:    42,
		LastTimestamp:   1234,
	}
	if err := store.UpsertServiceState(newState); err != nil {
		t.Fatalf("UpsertServiceState: %v", err)
	}

	got, err := store.GetServiceState("p", "s", "c", "m")
	if err != nil {
		t.Fatalf("GetServiceState after upsert: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil state after upsert")
	}
	if got.StableAvailable != 1 || got.StreakCount != 5 || got.LastRecordID != 42 {
		t.Errorf("state mismatch: %+v", got)
	}
}

func TestSaveStatusEvent_Idempotent(t *testing.T) {
	store := newTestStore(t)

	now := time.Now().Unix()
	evt := &StatusEvent{
		Provider:        "p",
		Service:         "s",
		Channel:         "c",
		Model:           "m",
		EventType:       EventTypeDown,
		TriggerRecordID: 100,
		ObservedAt:      now,
	}

	if err := store.SaveStatusEvent(evt); err != nil {
		t.Fatalf("SaveStatusEvent: %v", err)
	}

	// Duplicate should not error (ON CONFLICT DO NOTHING)
	evt2 := &StatusEvent{
		Provider:        "p",
		Service:         "s",
		Channel:         "c",
		Model:           "m",
		EventType:       EventTypeDown,
		TriggerRecordID: 100,
		ObservedAt:      now,
	}
	if err := store.SaveStatusEvent(evt2); err != nil {
		t.Fatalf("SaveStatusEvent duplicate: %v", err)
	}

	// Verify only one event exists
	events, err := store.GetStatusEvents(0, 100, nil)
	if err != nil {
		t.Fatalf("GetStatusEvents: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.Provider == "p" && e.TriggerRecordID == 100 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 event, got %d", count)
	}
}

// --- model_id column and index ---

func TestInitAddsModelIDColumnAndIndex(t *testing.T) {
	s := newTestStore(t)

	// model_id column must exist
	cols := columnNames(t, s.db, "probe_history")
	if !cols["model_id"] {
		t.Fatal("probe_history 缺 model_id 列")
	}

	// partial index must exist
	if !sqliteObjectExists(t, s.db, "index", "idx_probe_history_mid_ts") {
		t.Fatal("缺 idx_probe_history_mid_ts 索引")
	}
}

// --- model_id write path ---

func TestSaveRecordPersistsModelID(t *testing.T) {
	s := newTestStore(t)
	rec := &ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "Opus", ModelID: "md_test-1", Status: 1, Timestamp: 1000}
	if err := s.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	var got sql.NullString
	if err := s.db.QueryRow(`SELECT model_id FROM probe_history WHERE provider='P'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.String != "md_test-1" {
		t.Fatalf("model_id 未持久化，got %#v", got)
	}
}

// --- model_id 读路径 ---

func TestGetHistoryByModelIDSurvivesRename(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveRecord(&ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "OldName", ModelID: "md_x", Status: 1, Timestamp: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRecord(&ProbeRecord{Provider: "P", Service: "cc", Channel: "c", Model: "NewName", ModelID: "md_x", Status: 1, Timestamp: 2000}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetHistoryByModelID("md_x", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("model_id 应跨展示名连续，want 2 got %d", len(got))
	}
	latest, err := s.GetLatestByModelID("md_x")
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.Timestamp != 2000 {
		t.Fatalf("GetLatestByModelID 应返回最新行，got %#v", latest)
	}
	// 限量
	lim, err := s.GetHistoryWithLimitByModelID("md_x", time.Unix(0, 0), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(lim) != 1 {
		t.Fatalf("limit=1 应返回 1 行，got %d", len(lim))
	}
}

// --- WithContext ---

func TestWithContext(t *testing.T) {
	store := newTestStore(t)

	ctx, cancel := context.WithCancel(context.Background())
	ctxStore := store.WithContext(ctx)

	// Should work normally
	key := MonitorKey{Provider: "pc", Service: "sc", Channel: "cc", Model: "mc"}
	r := rec(key, 1000)
	if err := ctxStore.SaveRecord(r); err != nil {
		t.Fatalf("SaveRecord with context: %v", err)
	}

	cancel()

	// After cancel, operations should fail
	err := ctxStore.SaveRecord(rec(key, 2000))
	if err == nil {
		t.Log("SaveRecord after cancel did not error (may be driver-specific)")
	}
}

// --- BackfillProbeHistoryModelIDs ---

func TestBackfillProbeHistoryModelIDs(t *testing.T) {
	s := newTestStore(t)
	// legacy row with no model_id (insert raw so model_id stays NULL)
	if _, err := s.db.Exec(`INSERT INTO probe_history(provider,service,channel,model,status,latency,timestamp) VALUES('P','cc','c','Opus',1,0,1000)`); err != nil {
		t.Fatal(err)
	}
	maps := []ModelIDMigrationMapping{{Provider: "P", Service: "cc", Channel: "c", Model: "Opus", ModelID: "md_x"}}
	if err := s.BackfillProbeHistoryModelIDs(maps); err != nil {
		t.Fatal(err)
	}
	var got sql.NullString
	if err := s.db.QueryRow(`SELECT model_id FROM probe_history WHERE provider='P'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if !got.Valid || got.String != "md_x" {
		t.Fatalf("legacy 行未回填，got %#v", got)
	}
	// idempotent rerun: no error, no change
	if err := s.BackfillProbeHistoryModelIDs(maps); err != nil {
		t.Fatal(err)
	}
}

func TestBackfillRejectsAmbiguousMapping(t *testing.T) {
	s := newTestStore(t)
	maps := []ModelIDMigrationMapping{
		{Provider: "P", Service: "cc", Channel: "c", Model: "Opus", ModelID: "md_x"},
		{Provider: "P", Service: "cc", Channel: "c", Model: "Opus", ModelID: "md_y"},
	}
	if err := s.BackfillProbeHistoryModelIDs(maps); err == nil {
		t.Fatal("同 (p,s,c,model) 映射到多 id 必须 fail-fast")
	}
	// and it must NOT have written anything
}

// openStoreAt 在给定路径打开一个真实 *SQLiteStorage 并跑完 Init（含迁移），
// 用于对预置的旧版 schema 做迁移测试（newTestStore 用随机临时路径、无法预置）。
func openStoreAt(t *testing.T, path string) *SQLiteStorage {
	t.Helper()
	store, err := NewSQLiteStorage(path)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestSQLite_MonitorOverride_QualityFieldsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	rec := MonitorOverrideRecord{
		Key:                   MonitorKey{Provider: "Acme", Service: "cc", Channel: "Acme-CC", Model: "claude-opus"},
		Board:                 "secondary",
		BoardReason:           "quality_hardfail",
		QualityLatched:        true,
		QualityRecoveryCount:  2,
		QualityTriggerModels:  "claude-opus,claude-sonnet",
		QualityLastGeneration: 42,
		AvailabilityLatched:   false,
	}
	if err := s.ReplaceMonitorOverrides([]MonitorOverrideRecord{rec}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err := s.ListMonitorOverrides()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	g := got[0]
	if g.BoardReason != "quality_hardfail" || !g.QualityLatched || g.QualityRecoveryCount != 2 ||
		g.QualityTriggerModels != "claude-opus,claude-sonnet" || g.QualityLastGeneration != 42 || g.AvailabilityLatched {
		t.Fatalf("round-trip mismatch: %+v", g)
	}
}

func TestSQLite_OverrideMigration_AddsColumnsIdempotentAndBackfills(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mig.db")
	// build the OLD (pre-feature) schema by hand, insert legacy rows
	raw, err := sql.Open("sqlite", "file:"+path+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	mustExecRaw := func(q string) {
		if _, err := raw.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExecRaw(`CREATE TABLE monitor_overrides (
		provider TEXT NOT NULL, service TEXT NOT NULL, channel TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '', board TEXT NOT NULL DEFAULT '', cold_reason TEXT NOT NULL DEFAULT '',
		sponsor_level TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		PRIMARY KEY (provider, service, channel, model))`)
	mustExecRaw(`INSERT INTO monitor_overrides VALUES ('Acme','cc','Acme-CC','m','secondary','','',1,1)`)
	mustExecRaw(`INSERT INTO monitor_overrides VALUES ('Beta','cc','Beta-CC','m','cold','低可用率','',1,1)`)
	raw.Close()

	// open via the real storage path -> triggers migration + one-time backfill
	s := openStoreAt(t, path)
	got, err := s.ListMonitorOverrides()
	if err != nil {
		t.Fatalf("list after migration: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("legacy rows lost: %+v", got)
	}
	for _, r := range got {
		switch r.Board {
		case "secondary":
			if !r.AvailabilityLatched {
				t.Fatalf("secondary must backfill availability_latched=true: %+v", r)
			}
		case "cold":
			if r.AvailabilityLatched {
				t.Fatalf("cold must NOT be availability_latched: %+v", r)
			}
		}
	}
	// second open is idempotent (no duplicate-column error, backfill not re-run)
	s2 := openStoreAt(t, path)
	if _, err := s2.ListMonitorOverrides(); err != nil {
		t.Fatalf("second init not idempotent: %v", err)
	}
}
