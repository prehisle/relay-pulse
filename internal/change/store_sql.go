package change

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SQLStore 基于 database/sql 的 Store 实现（适用于 SQLite）。
type SQLStore struct {
	db *sql.DB
}

// NewSQLStore 创建基于 database/sql 的 Store。
func NewSQLStore(db *sql.DB) *SQLStore {
	return &SQLStore{db: db}
}

// InitTable 创建 change_requests 表和索引。
func (s *SQLStore) InitTable(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS change_requests (
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
		updated_at        INTEGER NOT NULL,

		agreement_accepted    BOOLEAN NOT NULL DEFAULT FALSE,
		agreement_accepted_at INTEGER,
		agreement_version     TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_change_status ON change_requests(status, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_change_target ON change_requests(target_key);
	`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}
	return s.ensureColumns(ctx)
}

const changeAllColumns = `id, public_id, status,
	target_provider, target_service, target_channel, target_key, apply_mode,
	auth_fingerprint, auth_last4,
	current_snapshot, proposed_changes,
	new_key_encrypted, new_key_fingerprint, new_key_last4,
	requires_test, test_type, test_variant, test_job_id, test_passed_at, test_latency_ms, test_http_code,
	admin_note, reviewed_at, applied_at,
	submitter_ip_hash, locale,
	created_at, updated_at,
	agreement_accepted, agreement_accepted_at, agreement_version`

// Save 保存新变更请求
func (s *SQLStore) Save(ctx context.Context, r *ChangeRequest) error {
	query := `
	INSERT INTO change_requests (
		public_id, status,
		target_provider, target_service, target_channel, target_key, apply_mode,
		auth_fingerprint, auth_last4,
		current_snapshot, proposed_changes,
		new_key_encrypted, new_key_fingerprint, new_key_last4,
		requires_test, test_type, test_variant, test_job_id, test_passed_at, test_latency_ms, test_http_code,
		admin_note, reviewed_at, applied_at,
		submitter_ip_hash, locale,
		created_at, updated_at,
		agreement_accepted, agreement_accepted_at, agreement_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := s.db.ExecContext(ctx, query,
		r.PublicID, r.Status,
		r.TargetProvider, r.TargetService, r.TargetChannel, r.TargetKey, r.ApplyMode,
		r.AuthFingerprint, r.AuthLast4,
		r.CurrentSnapshot, r.ProposedChanges,
		nullStr(r.NewKeyEncrypted), nullStr(r.NewKeyFingerprint), nullStr(r.NewKeyLast4),
		r.RequiresTest, nullStr(r.TestType), nullStr(r.TestVariant), nullStr(r.TestJobID), nullInt64(r.TestPassedAt), nullInt(r.TestLatency), nullInt(r.TestHTTPCode),
		nullStr(r.AdminNote), r.ReviewedAt, r.AppliedAt,
		nullStr(r.SubmitterIPHash), nullStr(r.Locale),
		r.CreatedAt, r.UpdatedAt,
		r.AgreementAccepted, r.AgreementAcceptedAt, nullStr(r.AgreementVersion),
	)
	if err != nil {
		return fmt.Errorf("保存变更请求失败: %w", err)
	}

	id, err := result.LastInsertId()
	if err == nil {
		r.ID = id
	}
	return nil
}

// GetByPublicID 按公开 ID 查询
func (s *SQLStore) GetByPublicID(ctx context.Context, publicID string) (*ChangeRequest, error) {
	return s.scanOne(ctx, "SELECT "+changeAllColumns+" FROM change_requests WHERE public_id = ?", publicID)
}

// List 列表查询
func (s *SQLStore) List(ctx context.Context, status string, limit, offset int) ([]*ChangeRequest, int, error) {
	var countQuery, listQuery string
	var args []any

	if status != "" && status != "all" {
		countQuery = "SELECT COUNT(*) FROM change_requests WHERE status = ?"
		listQuery = "SELECT " + changeAllColumns + " FROM change_requests WHERE status = ? ORDER BY created_at DESC LIMIT ? OFFSET ?"
		args = []any{status}
	} else {
		countQuery = "SELECT COUNT(*) FROM change_requests"
		listQuery = "SELECT " + changeAllColumns + " FROM change_requests ORDER BY created_at DESC LIMIT ? OFFSET ?"
	}

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计变更请求数失败: %w", err)
	}

	listArgs := append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询变更请求列表失败: %w", err)
	}
	defer rows.Close()

	var results []*ChangeRequest
	for rows.Next() {
		cr, err := scanChangeRequest(rows)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, cr)
	}
	return results, total, rows.Err()
}

// Update 更新变更请求
func (s *SQLStore) Update(ctx context.Context, r *ChangeRequest) error {
	query := `
	UPDATE change_requests SET
		status = ?, apply_mode = ?,
		current_snapshot = ?, proposed_changes = ?,
		new_key_encrypted = ?, new_key_fingerprint = ?, new_key_last4 = ?,
		requires_test = ?, test_type = ?, test_variant = ?, test_job_id = ?, test_passed_at = ?, test_latency_ms = ?, test_http_code = ?,
		admin_note = ?, reviewed_at = ?, applied_at = ?,
		updated_at = ?
	WHERE id = ?`

	_, err := s.db.ExecContext(ctx, query,
		r.Status, r.ApplyMode,
		r.CurrentSnapshot, r.ProposedChanges,
		nullStr(r.NewKeyEncrypted), nullStr(r.NewKeyFingerprint), nullStr(r.NewKeyLast4),
		r.RequiresTest, nullStr(r.TestType), nullStr(r.TestVariant), nullStr(r.TestJobID), nullInt64(r.TestPassedAt), nullInt(r.TestLatency), nullInt(r.TestHTTPCode),
		nullStr(r.AdminNote), r.ReviewedAt, r.AppliedAt,
		r.UpdatedAt,
		r.ID,
	)
	if err != nil {
		return fmt.Errorf("更新变更请求失败: %w", err)
	}
	return nil
}

// CountByIPToday 统计今天的提交数
func (s *SQLStore) CountByIPToday(ctx context.Context, ipHash string) (int, error) {
	start, end := todayRange()
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM change_requests WHERE submitter_ip_hash = ? AND created_at >= ? AND created_at < ?",
		ipHash, start, end,
	).Scan(&count)
	return count, err
}

// DeleteByPublicID 按公开 ID 删除
func (s *SQLStore) DeleteByPublicID(ctx context.Context, publicID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM change_requests WHERE public_id = ?", publicID)
	if err != nil {
		return fmt.Errorf("删除变更请求失败: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("变更请求不存在")
	}
	return nil
}

// === 内部辅助 ===

type scanner interface {
	Scan(dest ...any) error
}

func scanChangeRequest(s scanner) (*ChangeRequest, error) {
	var cr ChangeRequest
	var newKeyEnc, newKeyFP, newKeyL4 sql.NullString
	var testType, testVariant, testJobID sql.NullString
	var testPassedAt, testLatency, testHTTPCode sql.NullInt64
	var adminNote sql.NullString
	var reviewedAt, appliedAt sql.NullInt64
	var ipHash, locale sql.NullString
	var agreementAcceptedAt sql.NullInt64
	var agreementVersion sql.NullString

	err := s.Scan(
		&cr.ID, &cr.PublicID, &cr.Status,
		&cr.TargetProvider, &cr.TargetService, &cr.TargetChannel, &cr.TargetKey, &cr.ApplyMode,
		&cr.AuthFingerprint, &cr.AuthLast4,
		&cr.CurrentSnapshot, &cr.ProposedChanges,
		&newKeyEnc, &newKeyFP, &newKeyL4,
		&cr.RequiresTest, &testType, &testVariant, &testJobID, &testPassedAt, &testLatency, &testHTTPCode,
		&adminNote, &reviewedAt, &appliedAt,
		&ipHash, &locale,
		&cr.CreatedAt, &cr.UpdatedAt,
		&cr.AgreementAccepted, &agreementAcceptedAt, &agreementVersion,
	)
	if err != nil {
		return nil, err
	}

	cr.NewKeyEncrypted = newKeyEnc.String
	cr.NewKeyFingerprint = newKeyFP.String
	cr.NewKeyLast4 = newKeyL4.String
	cr.TestType = testType.String
	cr.TestVariant = testVariant.String
	cr.TestJobID = testJobID.String
	if testPassedAt.Valid {
		cr.TestPassedAt = testPassedAt.Int64
	}
	if testLatency.Valid {
		cr.TestLatency = int(testLatency.Int64)
	}
	if testHTTPCode.Valid {
		cr.TestHTTPCode = int(testHTTPCode.Int64)
	}
	cr.AdminNote = adminNote.String
	if reviewedAt.Valid {
		v := reviewedAt.Int64
		cr.ReviewedAt = &v
	}
	if appliedAt.Valid {
		v := appliedAt.Int64
		cr.AppliedAt = &v
	}
	cr.SubmitterIPHash = ipHash.String
	cr.Locale = locale.String
	if agreementAcceptedAt.Valid {
		v := agreementAcceptedAt.Int64
		cr.AgreementAcceptedAt = &v
	}
	cr.AgreementVersion = agreementVersion.String

	return &cr, nil
}

func (s *SQLStore) scanOne(ctx context.Context, query string, args ...any) (*ChangeRequest, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	cr, err := scanChangeRequest(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询变更请求失败: %w", err)
	}
	return cr, nil
}

// ensureColumns 补全 test_type/test_variant/agreement_* 等新增列（在线迁移，幂等）
func (s *SQLStore) ensureColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(change_requests)`)
	if err != nil {
		return fmt.Errorf("查询 change_requests 表结构失败: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("读取 change_requests 表结构失败: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 change_requests 表结构失败: %w", err)
	}

	for _, col := range []struct {
		name string
		ddl  string
	}{
		{name: "test_type", ddl: `ALTER TABLE change_requests ADD COLUMN test_type TEXT`},
		{name: "test_variant", ddl: `ALTER TABLE change_requests ADD COLUMN test_variant TEXT`},
		{name: "agreement_accepted", ddl: `ALTER TABLE change_requests ADD COLUMN agreement_accepted BOOLEAN NOT NULL DEFAULT FALSE`},
		{name: "agreement_accepted_at", ddl: `ALTER TABLE change_requests ADD COLUMN agreement_accepted_at INTEGER`},
		{name: "agreement_version", ddl: `ALTER TABLE change_requests ADD COLUMN agreement_version TEXT`},
	} {
		if existing[col.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, col.ddl); err != nil {
			return fmt.Errorf("迁移 change_requests.%s 失败: %w", col.name, err)
		}
	}
	return nil
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func nullInt(v int) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(v), Valid: true}
}

func todayRange() (int64, int64) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today.Unix(), today.Add(24 * time.Hour).Unix()
}
