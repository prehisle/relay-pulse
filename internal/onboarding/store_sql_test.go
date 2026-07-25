package onboarding

import (
	"context"
	"database/sql"
	"testing"

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

// saveSubmission 写入一条最小可用的申请记录。
func saveSubmission(t *testing.T, s *SQLStore, publicID, status string, createdAt int64) {
	t.Helper()
	sub := &Submission{
		PublicID:          publicID,
		Status:            SubmissionStatus(status),
		ProviderName:      "prov-" + publicID,
		WebsiteURL:        "https://example.com",
		Category:          "commercial",
		ServiceType:       "cc",
		TemplateName:      "tpl",
		SponsorLevel:      "pulse",
		ChannelType:       "O",
		ChannelSource:     "api",
		ChannelCode:       "o-api",
		BaseURL:           "https://api.example.com",
		APIKeyEncrypted:   "enc",
		APIKeyFingerprint: "fp-" + publicID,
		APIKeyLast4:       "0001",
		TestJobID:         "job",
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
	if err := s.Save(context.Background(), sub); err != nil {
		t.Fatalf("Save(%s): %v", publicID, err)
	}
}

// collectIDs 取列表结果里的 public_id 集合。
func collectIDs(subs []*Submission) map[string]bool {
	ids := make(map[string]bool, len(subs))
	for _, s := range subs {
		ids[s.PublicID] = true
	}
	return ids
}

func TestList_NoSearchReturnsAll(t *testing.T) {
	s := newTestStore(t)
	saveSubmission(t, s, "abc-111", "pending", 100)
	saveSubmission(t, s, "abc-222", "approved", 200)
	saveSubmission(t, s, "xyz-333", "pending", 300)

	subs, total, err := s.List(context.Background(), "all", "", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 || len(subs) != 3 {
		t.Fatalf("期望 total=3 len=3，实际 total=%d len=%d", total, len(subs))
	}
	// created_at DESC 排序
	if subs[0].PublicID != "xyz-333" {
		t.Errorf("期望首行 xyz-333（created_at 最大），实际 %s", subs[0].PublicID)
	}
}

func TestList_SearchByPublicIDSubstring(t *testing.T) {
	s := newTestStore(t)
	saveSubmission(t, s, "abc-111", "pending", 100)
	saveSubmission(t, s, "abc-222", "approved", 200)
	saveSubmission(t, s, "xyz-333", "pending", 300)

	subs, total, err := s.List(context.Background(), "all", "%abc%", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(subs) != 2 {
		t.Fatalf("期望 total=2 len=2，实际 total=%d len=%d", total, len(subs))
	}
	ids := collectIDs(subs)
	if !ids["abc-111"] || !ids["abc-222"] || ids["xyz-333"] {
		t.Errorf("搜索 %%abc%% 结果不符，实际 %v", ids)
	}
}

func TestList_SearchCombinesWithStatus(t *testing.T) {
	s := newTestStore(t)
	saveSubmission(t, s, "abc-111", "pending", 100)
	saveSubmission(t, s, "abc-222", "approved", 200)
	saveSubmission(t, s, "xyz-333", "pending", 300)

	subs, total, err := s.List(context.Background(), "pending", "%abc%", 20, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(subs) != 1 {
		t.Fatalf("期望 total=1 len=1，实际 total=%d len=%d", total, len(subs))
	}
	if subs[0].PublicID != "abc-111" {
		t.Errorf("期望仅命中 abc-111，实际 %s", subs[0].PublicID)
	}
}

// TestList_SearchEscapesLikeWildcard 验证 ESCAPE '!' 生效：
// 转义后的 '_' 是字面下划线，只能精确匹配含下划线的 id，不会把 '_' 当通配符。
func TestList_SearchEscapesLikeWildcard(t *testing.T) {
	s := newTestStore(t)
	saveSubmission(t, s, "a_b", "pending", 100)
	saveSubmission(t, s, "axb", "pending", 200)

	// 已转义模式：'_' → '!_'，应只命中字面 "a_b"。
	escaped, total, err := s.List(context.Background(), "all", "%a!_b%", 20, 0)
	if err != nil {
		t.Fatalf("List(escaped): %v", err)
	}
	if total != 1 || escaped[0].PublicID != "a_b" {
		t.Fatalf("转义模式应只命中 a_b，实际 total=%d ids=%v", total, collectIDs(escaped))
	}

	// 未转义的裸 '_' 仍是单字符通配符，会同时命中 a_b 与 axb——印证转义确有必要。
	raw, total, err := s.List(context.Background(), "all", "%a_b%", 20, 0)
	if err != nil {
		t.Fatalf("List(raw): %v", err)
	}
	if total != 2 {
		t.Fatalf("裸下划线通配应命中 2 条，实际 total=%d ids=%v", total, collectIDs(raw))
	}
}

func TestList_SearchPagination(t *testing.T) {
	s := newTestStore(t)
	saveSubmission(t, s, "abc-1", "pending", 100)
	saveSubmission(t, s, "abc-2", "pending", 200)
	saveSubmission(t, s, "abc-3", "pending", 300)

	page1, total, err := s.List(context.Background(), "all", "%abc%", 2, 0)
	if err != nil {
		t.Fatalf("List page1: %v", err)
	}
	if total != 3 || len(page1) != 2 {
		t.Fatalf("page1 期望 total=3 len=2，实际 total=%d len=%d", total, len(page1))
	}
	page2, _, err := s.List(context.Background(), "all", "%abc%", 2, 2)
	if err != nil {
		t.Fatalf("List page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 期望 len=1，实际 len=%d", len(page2))
	}
}

// TestSQLStore_ConsumeTestJobID 锁定原子占用语义：同一 job_id 只有首次能抢到。
func TestSQLStore_ConsumeTestJobID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	consumed, err := store.ConsumeTestJobID(ctx, "probe-abc")
	if err != nil {
		t.Fatalf("首次 ConsumeTestJobID: %v", err)
	}
	if !consumed {
		t.Fatal("首次消费应抢占成功")
	}

	consumed, err = store.ConsumeTestJobID(ctx, "probe-abc")
	if err != nil {
		t.Fatalf("二次 ConsumeTestJobID: %v", err)
	}
	if consumed {
		t.Fatal("同一 job_id 第二次消费必须失败")
	}

	if consumed, err = store.ConsumeTestJobID(ctx, "probe-other"); err != nil || !consumed {
		t.Fatalf("不同 job_id 应能独立抢占: consumed=%v err=%v", consumed, err)
	}
}

// TestSQLStore_BackfillUsedTestJobsTolerscatesDuplicates 锁定升级路径：
// 存量数据里已存在重复 test_job_id（重放留下的）时，InitTable 必须成功而非因唯一约束崩掉，
// 且回填后这些历史 job_id 不能再被兑换一次。
func TestSQLStore_BackfillUsedTestJobsToleratesDuplicates(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	// saveSubmission 固定写 TestJobID="job"，两条即构成存量重复
	saveSubmission(t, store, "dup-1", "pending", 100)
	saveSubmission(t, store, "dup-2", "pending", 200)

	// 模拟"升级前消费表尚不存在"
	if _, err := store.db.ExecContext(ctx, `DROP TABLE onboarding_used_test_jobs`); err != nil {
		t.Fatalf("DROP: %v", err)
	}
	if err := store.InitTable(ctx); err != nil {
		t.Fatalf("存量含重复 test_job_id 时 InitTable 应成功: %v", err)
	}

	consumed, err := store.ConsumeTestJobID(ctx, "job")
	if err != nil {
		t.Fatalf("ConsumeTestJobID: %v", err)
	}
	if consumed {
		t.Fatal("历史已用过的 test_job_id 必须已被回填为已消费")
	}
}
