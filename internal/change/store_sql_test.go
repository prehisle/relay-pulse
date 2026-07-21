package change

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestStore 创建基于内存 SQLite 的 Store。
func newTestStore(t *testing.T) *SQLStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewSQLStore(db)
	if err := store.InitTable(context.Background()); err != nil {
		t.Fatalf("InitTable: %v", err)
	}
	return store
}

// makeRequest 创建一个可保存的最小 ChangeRequest。
func makeRequest(publicID string) *ChangeRequest {
	now := time.Now().Unix()
	return &ChangeRequest{
		PublicID:        publicID,
		Status:          StatusPending,
		TargetProvider:  "prov",
		TargetService:   "cc",
		TargetChannel:   "ch1",
		TargetKey:       "prov--cc--ch1",
		ApplyMode:       "auto",
		AuthFingerprint: "fp-abc123",
		AuthLast4:       "0001",
		CurrentSnapshot: `{"base_url":"https://a.com"}`,
		ProposedChanges: `{"base_url":"https://b.com"}`,
		SubmitterIPHash: hashIP("127.0.0.1"),
		Locale:          "zh-CN",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestSQLStore_SaveAndGetByPublicID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	cr := makeRequest("pub-001")

	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if cr.ID == 0 {
		t.Error("expected auto-increment ID to be set")
	}

	got, err := store.GetByPublicID(ctx, "pub-001")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result")
	}

	if got.PublicID != "pub-001" {
		t.Errorf("PublicID: got %q, want %q", got.PublicID, "pub-001")
	}
	if got.Status != StatusPending {
		t.Errorf("Status: got %q, want %q", got.Status, StatusPending)
	}
	if got.TargetKey != "prov--cc--ch1" {
		t.Errorf("TargetKey: got %q, want %q", got.TargetKey, "prov--cc--ch1")
	}
	if got.ApplyMode != "auto" {
		t.Errorf("ApplyMode: got %q, want %q", got.ApplyMode, "auto")
	}
	if got.Locale != "zh-CN" {
		t.Errorf("Locale: got %q, want %q", got.Locale, "zh-CN")
	}
}

func TestSQLStore_GetByPublicID_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	got, err := store.GetByPublicID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent public_id")
	}
}

func TestSQLStore_SaveWithNullableFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cr := makeRequest("pub-nullable")
	cr.NewKeyEncrypted = "enc-abc"
	cr.NewKeyFingerprint = "fp-new"
	cr.NewKeyLast4 = "9999"
	cr.RequiresTest = true
	cr.TestJobID = "job-123"
	cr.TestPassedAt = time.Now().Unix()
	cr.TestLatency = 234
	cr.TestHTTPCode = 200

	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.GetByPublicID(ctx, "pub-nullable")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}

	if got.NewKeyEncrypted != "enc-abc" {
		t.Errorf("NewKeyEncrypted: got %q, want %q", got.NewKeyEncrypted, "enc-abc")
	}
	if got.NewKeyFingerprint != "fp-new" {
		t.Errorf("NewKeyFingerprint: got %q, want %q", got.NewKeyFingerprint, "fp-new")
	}
	if got.NewKeyLast4 != "9999" {
		t.Errorf("NewKeyLast4: got %q, want %q", got.NewKeyLast4, "9999")
	}
	if !got.RequiresTest {
		t.Error("RequiresTest should be true")
	}
	if got.TestJobID != "job-123" {
		t.Errorf("TestJobID: got %q, want %q", got.TestJobID, "job-123")
	}
	if got.TestLatency != 234 {
		t.Errorf("TestLatency: got %d, want %d", got.TestLatency, 234)
	}
	if got.TestHTTPCode != 200 {
		t.Errorf("TestHTTPCode: got %d, want %d", got.TestHTTPCode, 200)
	}
}

func TestSQLStore_List_All(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		cr := makeRequest("pub-list-" + string(rune('a'+i)))
		cr.CreatedAt = int64(1000 + i)
		if err := store.Save(ctx, cr); err != nil {
			t.Fatalf("Save #%d: %v", i, err)
		}
	}

	results, total, err := store.List(ctx, "", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 5 {
		t.Errorf("total: got %d, want 5", total)
	}
	if len(results) != 5 {
		t.Errorf("results: got %d, want 5", len(results))
	}

	// 应按 created_at DESC 排序
	if results[0].CreatedAt < results[len(results)-1].CreatedAt {
		t.Error("results should be ordered by created_at DESC")
	}
}

func TestSQLStore_List_StatusFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	pending := makeRequest("pub-pending")
	pending.Status = StatusPending
	approved := makeRequest("pub-approved")
	approved.PublicID = "pub-approved"
	approved.Status = StatusApproved

	if err := store.Save(ctx, pending); err != nil {
		t.Fatalf("Save pending: %v", err)
	}
	if err := store.Save(ctx, approved); err != nil {
		t.Fatalf("Save approved: %v", err)
	}

	results, total, err := store.List(ctx, "pending", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 {
		t.Errorf("total: got %d, want 1", total)
	}
	if len(results) != 1 {
		t.Errorf("results: got %d, want 1", len(results))
	}
	if results[0].Status != StatusPending {
		t.Errorf("status: got %q, want %q", results[0].Status, StatusPending)
	}
}

func TestSQLStore_List_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		cr := makeRequest("pub-page-" + string(rune('a'+i)))
		cr.CreatedAt = int64(1000 + i)
		if err := store.Save(ctx, cr); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	// 第一页
	page1, total, err := store.List(ctx, "", 3, 0)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if total != 10 {
		t.Errorf("total: got %d, want 10", total)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: got %d, want 3", len(page1))
	}

	// 验证第一页严格 DESC 排序
	for i := 1; i < len(page1); i++ {
		if page1[i].CreatedAt > page1[i-1].CreatedAt {
			t.Errorf("page1 not DESC: [%d].CreatedAt=%d > [%d].CreatedAt=%d",
				i, page1[i].CreatedAt, i-1, page1[i-1].CreatedAt)
		}
	}

	// 第二页
	page2, _, err := store.List(ctx, "", 3, 3)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("page2: got %d, want 3", len(page2))
	}

	// 第二页也应 DESC 排序
	for i := 1; i < len(page2); i++ {
		if page2[i].CreatedAt > page2[i-1].CreatedAt {
			t.Errorf("page2 not DESC: [%d].CreatedAt=%d > [%d].CreatedAt=%d",
				i, page2[i].CreatedAt, i-1, page2[i-1].CreatedAt)
		}
	}

	// 第一页最后一条应比第二页第一条更新（无重叠、无间隙）
	if page1[len(page1)-1].CreatedAt <= page2[0].CreatedAt {
		t.Errorf("page boundary: page1 last (%d) should be > page2 first (%d)",
			page1[len(page1)-1].CreatedAt, page2[0].CreatedAt)
	}

	// 收集所有 PublicID 确保无重叠
	seen := make(map[string]bool)
	for _, cr := range page1 {
		seen[cr.PublicID] = true
	}
	for _, cr := range page2 {
		if seen[cr.PublicID] {
			t.Errorf("duplicate PublicID across pages: %s", cr.PublicID)
		}
	}
}

func TestSQLStore_Update(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cr := makeRequest("pub-update")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 修改状态和备注
	now := time.Now().Unix()
	cr.Status = StatusApproved
	cr.AdminNote = "LGTM"
	cr.ReviewedAt = &now
	cr.UpdatedAt = now

	if err := store.Update(ctx, cr); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetByPublicID(ctx, "pub-update")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if got.Status != StatusApproved {
		t.Errorf("Status: got %q, want %q", got.Status, StatusApproved)
	}
	if got.AdminNote != "LGTM" {
		t.Errorf("AdminNote: got %q, want %q", got.AdminNote, "LGTM")
	}
	if got.ReviewedAt == nil || *got.ReviewedAt != now {
		t.Errorf("ReviewedAt: got %v, want %d", got.ReviewedAt, now)
	}
}

func TestSQLStore_CountByIPToday(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	ipHash := hashIP("192.168.1.1")

	// 今天的请求
	cr1 := makeRequest("pub-ip-1")
	cr1.SubmitterIPHash = ipHash
	cr1.CreatedAt = time.Now().Unix()
	if err := store.Save(ctx, cr1); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cr2 := makeRequest("pub-ip-2")
	cr2.SubmitterIPHash = ipHash
	cr2.CreatedAt = time.Now().Unix()
	if err := store.Save(ctx, cr2); err != nil {
		t.Fatalf("Save: %v", err)
	}

	count, err := store.CountByIPToday(ctx, ipHash)
	if err != nil {
		t.Fatalf("CountByIPToday: %v", err)
	}
	if count != 2 {
		t.Errorf("count today: got %d, want 2", count)
	}

	// 昨天的请求不应计入
	cr3 := makeRequest("pub-ip-3")
	cr3.SubmitterIPHash = ipHash
	cr3.CreatedAt = time.Now().Add(-25 * time.Hour).Unix()
	if err := store.Save(ctx, cr3); err != nil {
		t.Fatalf("Save: %v", err)
	}

	count, err = store.CountByIPToday(ctx, ipHash)
	if err != nil {
		t.Fatalf("CountByIPToday: %v", err)
	}
	if count != 2 {
		t.Errorf("count should still be 2 (yesterday excluded), got %d", count)
	}

	// 不同 IP 不应计入
	otherIPHash := hashIP("10.0.0.1")
	count, err = store.CountByIPToday(ctx, otherIPHash)
	if err != nil {
		t.Fatalf("CountByIPToday: %v", err)
	}
	if count != 0 {
		t.Errorf("count for other IP: got %d, want 0", count)
	}
}

func TestSQLStore_DeleteByPublicID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cr := makeRequest("pub-delete")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := store.DeleteByPublicID(ctx, "pub-delete"); err != nil {
		t.Fatalf("DeleteByPublicID: %v", err)
	}

	// 删除后应查不到
	got, err := store.GetByPublicID(ctx, "pub-delete")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestSQLStore_DeleteByPublicID_NotFound(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	err := store.DeleteByPublicID(ctx, "nonexistent")
	if err == nil {
		t.Error("expected error for deleting non-existent record")
	}
}

// TestSQLStore_EnsureColumns_MigratesPreExistingTable 用一份「Task 2 上线前」的旧 schema
// （不含 agreement_* 三列，但含更早已上线的 test_type/test_variant）手动建表并插入一条
// 历史行，再调用 InitTable（内含 ensureColumns 在线迁移），验证：
//  1. ALTER TABLE ADD COLUMN 迁移路径本身跑得通（不同于 NullHistoricalRow 测试里
//     「新 schema 建表后裸 INSERT 省略新列」那种只测 scan 容忍 NULL、不真正走迁移代码的路子）；
//  2. 迁移后读取该历史行不报错，3 个新列呈现预期零值。
func TestSQLStore_EnsureColumns_MigratesPreExistingTable(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	// Task 2 之前的表结构快照：无 agreement_accepted/agreement_accepted_at/agreement_version。
	preTask2Schema := `
	CREATE TABLE change_requests (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		public_id         TEXT NOT NULL UNIQUE,
		status            TEXT NOT NULL DEFAULT 'pending',

		target_provider   TEXT NOT NULL,
		target_service    TEXT NOT NULL,
		target_channel    TEXT NOT NULL,
		target_key        TEXT NOT NULL,
		apply_mode        TEXT NOT NULL,

		auth_fingerprint  TEXT NOT NULL,
		auth_last4        TEXT NOT NULL,

		current_snapshot  TEXT NOT NULL,
		proposed_changes  TEXT NOT NULL,

		new_key_encrypted   TEXT,
		new_key_fingerprint TEXT,
		new_key_last4       TEXT,

		requires_test     BOOLEAN NOT NULL DEFAULT FALSE,
		test_type         TEXT,
		test_variant      TEXT,
		test_job_id       TEXT,
		test_passed_at    INTEGER,
		test_latency_ms   INTEGER,
		test_http_code    INTEGER,

		admin_note        TEXT,
		reviewed_at       INTEGER,
		applied_at        INTEGER,

		submitter_ip_hash TEXT,
		locale            TEXT,

		created_at        INTEGER NOT NULL,
		updated_at        INTEGER NOT NULL
	);`
	if _, err := db.ExecContext(ctx, preTask2Schema); err != nil {
		t.Fatalf("create pre-Task-2 schema: %v", err)
	}

	now := time.Now().Unix()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO change_requests (
			public_id, status,
			target_provider, target_service, target_channel, target_key, apply_mode,
			auth_fingerprint, auth_last4,
			current_snapshot, proposed_changes,
			requires_test,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"cr-pre-migration", StatusPending,
		"prov", "cc", "ch1", "prov--cc--ch1", "auto",
		"fp-pre", "0003",
		`{"base_url":"https://a.com"}`, `{"base_url":"https://b.com"}`,
		false,
		now, now,
	); err != nil {
		t.Fatalf("insert legacy row into pre-Task-2 schema: %v", err)
	}

	// InitTable 对已存在的表是 CREATE TABLE IF NOT EXISTS（no-op）+ ensureColumns
	// （检测缺列后逐条 ALTER TABLE ADD COLUMN）——这正是生产环境滚动升级时会真实走到的路径。
	store := NewSQLStore(db)
	if err := store.InitTable(ctx); err != nil {
		t.Fatalf("InitTable (online migration of pre-existing table): %v", err)
	}

	got, err := store.GetByPublicID(ctx, "cr-pre-migration")
	if err != nil {
		t.Fatalf("GetByPublicID after migration must not error: %v", err)
	}
	if got == nil {
		t.Fatal("expected migrated legacy row to be found")
	}
	if got.AgreementAccepted {
		t.Error("migrated legacy row AgreementAccepted: got true want false")
	}
	if got.AgreementAcceptedAt != nil {
		t.Errorf("migrated legacy row AgreementAcceptedAt: got %v want nil", got.AgreementAcceptedAt)
	}
	if got.AgreementVersion != "" {
		t.Errorf("migrated legacy row AgreementVersion: got %q want empty", got.AgreementVersion)
	}
}

// TestSQLStore_Update_DoesNotMutateAgreementFields 是回归闸：即便调用方在内存对象里
// （不慎）篡改了这 3 个 audit-immutable 字段再调 Update，落库值也必须原封不动——
// 防止未来有人把 agreement_* 三列误加进 Update 的 SET 子句。
func TestSQLStore_Update_DoesNotMutateAgreementFields(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	r := makeRequest("cr-immutable-1")
	r.AgreementAccepted = true
	at := int64(1721563200)
	r.AgreementAcceptedAt = &at
	r.AgreementVersion = "2026-07-16"
	if err := store.Save(ctx, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// 模拟调用方在内存对象里篡改了这三个字段，再走正常的审核 Update 流程。
	r.AgreementAccepted = false
	r.AgreementAcceptedAt = nil
	r.AgreementVersion = "tampered"
	r.Status = StatusApproved
	r.AdminNote = "LGTM"
	r.UpdatedAt = time.Now().Unix()
	if err := store.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := store.GetByPublicID(ctx, "cr-immutable-1")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if !got.AgreementAccepted {
		t.Error("Update must not mutate AgreementAccepted: got false want true (unchanged)")
	}
	if got.AgreementAcceptedAt == nil || *got.AgreementAcceptedAt != at {
		t.Errorf("Update must not mutate AgreementAcceptedAt: got %v want %d (unchanged)", got.AgreementAcceptedAt, at)
	}
	if got.AgreementVersion != "2026-07-16" {
		t.Errorf("Update must not mutate AgreementVersion: got %q want %q (unchanged)", got.AgreementVersion, "2026-07-16")
	}
	// 合法字段仍应正常生效，证明这不是 Update 整体失效。
	if got.Status != StatusApproved {
		t.Errorf("Update should still apply legitimate Status change: got %q want %q", got.Status, StatusApproved)
	}
	if got.AdminNote != "LGTM" {
		t.Errorf("Update should still apply legitimate AdminNote change: got %q want %q", got.AdminNote, "LGTM")
	}
}

func TestSQLStore_AgreementFields_RoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	r := makeRequest("cr-agreement-1")
	r.RequiresTest = true
	r.AgreementAccepted = true
	at := int64(1721563200)
	r.AgreementAcceptedAt = &at
	r.AgreementVersion = "2026-07-16"
	if err := store.Save(ctx, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.GetByPublicID(ctx, "cr-agreement-1")
	if err != nil {
		t.Fatalf("GetByPublicID: %v", err)
	}
	if !got.AgreementAccepted {
		t.Error("AgreementAccepted: got false want true")
	}
	if got.AgreementAcceptedAt == nil || *got.AgreementAcceptedAt != at {
		t.Errorf("AgreementAcceptedAt: got %v want %d", got.AgreementAcceptedAt, at)
	}
	if got.AgreementVersion != "2026-07-16" {
		t.Errorf("AgreementVersion: got %q", got.AgreementVersion)
	}
}

// 模拟功能上线前就存在的历史行：agreement_accepted_at/version 为 NULL，
// scan 必须不报错（否则 admin 列表读到旧行整体 500）。
func TestSQLStore_AgreementFields_NullHistoricalRow(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	r := makeRequest("cr-legacy-1") // 未设任何 agreement 字段
	if err := store.Save(ctx, r); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.GetByPublicID(ctx, "cr-legacy-1")
	if err != nil {
		t.Fatalf("GetByPublicID on legacy row must not error: %v", err)
	}
	if got.AgreementAccepted {
		t.Error("legacy row AgreementAccepted: got true want false")
	}
	if got.AgreementAcceptedAt != nil {
		t.Errorf("legacy row AgreementAcceptedAt: got %v want nil", got.AgreementAcceptedAt)
	}
	if got.AgreementVersion != "" {
		t.Errorf("legacy row AgreementVersion: got %q want empty", got.AgreementVersion)
	}

	// 直接对底层 DB 插入一条「功能上线前」的行：agreement_* 三列完全不写
	// （模拟真实历史行，而非 Save 写入的零值），验证 GetByPublicID 的 scan
	// 不会因该列在物理上是 NULL 而报错。
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO change_requests (
			public_id, status,
			target_provider, target_service, target_channel, target_key, apply_mode,
			auth_fingerprint, auth_last4,
			current_snapshot, proposed_changes,
			requires_test,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"cr-legacy-raw", StatusPending,
		"prov", "cc", "ch1", "prov--cc--ch1", "auto",
		"fp-raw", "0002",
		`{"base_url":"https://a.com"}`, `{"base_url":"https://b.com"}`,
		false,
		time.Now().Unix(), time.Now().Unix(),
	)
	if err != nil {
		t.Fatalf("raw insert (legacy row simulation): %v", err)
	}

	gotRaw, err := store.GetByPublicID(ctx, "cr-legacy-raw")
	if err != nil {
		t.Fatalf("GetByPublicID on raw-inserted legacy row must not error: %v", err)
	}
	if gotRaw.AgreementAccepted {
		t.Error("raw legacy row AgreementAccepted: got true want false")
	}
	if gotRaw.AgreementAcceptedAt != nil {
		t.Errorf("raw legacy row AgreementAcceptedAt: got %v want nil", gotRaw.AgreementAcceptedAt)
	}
	if gotRaw.AgreementVersion != "" {
		t.Errorf("raw legacy row AgreementVersion: got %q want empty", gotRaw.AgreementVersion)
	}
}

func TestSQLStore_List_StatusAll(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	cr := makeRequest("pub-all-filter")
	if err := store.Save(ctx, cr); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// status="all" 应返回全部
	results, total, err := store.List(ctx, "all", 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(results) != 1 {
		t.Errorf("expected 1 result for status=all, got total=%d len=%d", total, len(results))
	}
}
