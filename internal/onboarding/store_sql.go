package onboarding

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
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

// InitTable 创建 onboarding_submissions 表和索引。
// 应在应用启动时由 storage Init() 调用。
func (s *SQLStore) InitTable(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS onboarding_submissions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		public_id TEXT NOT NULL UNIQUE,
		status TEXT NOT NULL DEFAULT 'pending',

		provider_name TEXT NOT NULL,
		website_url TEXT NOT NULL,
		category TEXT NOT NULL,

		service_type TEXT NOT NULL,
		template_name TEXT NOT NULL,
		model TEXT NOT NULL DEFAULT '',
		model_vendor TEXT NOT NULL DEFAULT '',

		sponsor_level TEXT NOT NULL,

		channel_type TEXT NOT NULL,
		channel_source TEXT NOT NULL,
		channel_group TEXT NOT NULL DEFAULT '',
		channel_code TEXT NOT NULL,
		target_provider TEXT NOT NULL DEFAULT '',
		target_service TEXT NOT NULL DEFAULT '',
		target_channel TEXT NOT NULL DEFAULT '',
		channel_name TEXT NOT NULL DEFAULT '',
		listed_since TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL DEFAULT '',
		price_min REAL NOT NULL DEFAULT 0,
		price_max REAL NOT NULL DEFAULT 0,

		base_url TEXT NOT NULL,
		api_key_encrypted TEXT NOT NULL,
		api_key_fingerprint TEXT NOT NULL,
		api_key_last4 TEXT NOT NULL,

		test_job_id TEXT NOT NULL,
		test_passed_at INTEGER NOT NULL,
		test_latency_ms INTEGER NOT NULL DEFAULT 0,
		test_http_code INTEGER NOT NULL DEFAULT 0,

		contact_info TEXT,
		submitter_ip_hash TEXT,
		locale TEXT,

		admin_note TEXT,
		admin_config_json TEXT,
		reviewed_at INTEGER,

		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,

		agreement_accepted INTEGER NOT NULL DEFAULT 0,
		agreement_accepted_at INTEGER NOT NULL DEFAULT 0,
		agreement_version TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS onboarding_used_test_jobs (
		job_id TEXT PRIMARY KEY,
		used_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_onboarding_status ON onboarding_submissions(status, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_onboarding_fingerprint ON onboarding_submissions(api_key_fingerprint);
	`
	_, err := s.db.ExecContext(ctx, schema)
	if err != nil {
		return err
	}
	if err := s.ensureColumns(ctx); err != nil {
		return err
	}
	return s.backfillUsedTestJobs(ctx)
}

// backfillUsedTestJobs 把历史提交用过的 test_job_id 标记为已消费。
//
// 没有这一步，升级瞬间那些"已提交过、但 proof 仍在 TTL 内"的任务 ID 会因消费表为空
// 而被判定为首次使用，等于白送每个历史 proof 一次额外重放。GROUP BY 去重后写入，
// 因此即便存量数据本身就有重复 job_id（重放攻击留下的）也不会触发主键冲突——
// 这也是不给 test_job_id 直接加唯一索引的原因：那会让带脏数据的库启动即失败。
func (s *SQLStore) backfillUsedTestJobs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO onboarding_used_test_jobs(job_id, used_at)
		SELECT test_job_id, MAX(created_at)
		FROM onboarding_submissions
		WHERE test_job_id <> ''
		GROUP BY test_job_id
	`)
	if err != nil {
		return fmt.Errorf("回填已消费 test_job_id 失败: %w", err)
	}
	return nil
}

// ConsumeTestJobID 原子占用测试任务 ID；已被占用时返回 false。
func (s *SQLStore) ConsumeTestJobID(ctx context.Context, jobID string) (bool, error) {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO onboarding_used_test_jobs(job_id, used_at) VALUES (?, ?)`,
		jobID, time.Now().Unix(),
	)
	if err != nil {
		return false, fmt.Errorf("消费 test_job_id 失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取 test_job_id 消费结果失败: %w", err)
	}
	return affected == 1, nil
}

// ensureColumns 为旧数据库补齐新列（兼容热升级）
func (s *SQLStore) ensureColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(onboarding_submissions)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var defaultVal any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			return fmt.Errorf("读取表结构失败: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	migrations := []struct {
		name string
		ddl  string
	}{
		{"channel_name", `ALTER TABLE onboarding_submissions ADD COLUMN channel_name TEXT NOT NULL DEFAULT ''`},
		{"listed_since", `ALTER TABLE onboarding_submissions ADD COLUMN listed_since TEXT NOT NULL DEFAULT ''`},
		{"price_min", `ALTER TABLE onboarding_submissions ADD COLUMN price_min REAL NOT NULL DEFAULT 0`},
		{"price_max", `ALTER TABLE onboarding_submissions ADD COLUMN price_max REAL NOT NULL DEFAULT 0`},
		{"expires_at", `ALTER TABLE onboarding_submissions ADD COLUMN expires_at TEXT NOT NULL DEFAULT ''`},
		{"target_provider", `ALTER TABLE onboarding_submissions ADD COLUMN target_provider TEXT NOT NULL DEFAULT ''`},
		{"target_service", `ALTER TABLE onboarding_submissions ADD COLUMN target_service TEXT NOT NULL DEFAULT ''`},
		{"target_channel", `ALTER TABLE onboarding_submissions ADD COLUMN target_channel TEXT NOT NULL DEFAULT ''`},
		{"channel_group", `ALTER TABLE onboarding_submissions ADD COLUMN channel_group TEXT NOT NULL DEFAULT ''`},
		{"agreement_accepted", `ALTER TABLE onboarding_submissions ADD COLUMN agreement_accepted INTEGER NOT NULL DEFAULT 0`},
		{"agreement_accepted_at", `ALTER TABLE onboarding_submissions ADD COLUMN agreement_accepted_at INTEGER NOT NULL DEFAULT 0`},
		{"agreement_version", `ALTER TABLE onboarding_submissions ADD COLUMN agreement_version TEXT NOT NULL DEFAULT ''`},
		{"model", `ALTER TABLE onboarding_submissions ADD COLUMN model TEXT NOT NULL DEFAULT ''`},
		{"model_vendor", `ALTER TABLE onboarding_submissions ADD COLUMN model_vendor TEXT NOT NULL DEFAULT ''`},
	}
	for _, m := range migrations {
		if existing[m.name] {
			continue
		}
		if _, err := s.db.ExecContext(ctx, m.ddl); err != nil {
			return fmt.Errorf("迁移列 %s 失败: %w", m.name, err)
		}
	}
	return nil
}

// Save 保存新申请
func (s *SQLStore) Save(ctx context.Context, sub *Submission) error {
	query := `
	INSERT INTO onboarding_submissions (
		public_id, status, provider_name, website_url, category,
		service_type, template_name, model, model_vendor, sponsor_level,
		channel_type, channel_source, channel_group, channel_code,
		target_provider, target_service, target_channel,
		channel_name, listed_since, expires_at, price_min, price_max,
		base_url, api_key_encrypted, api_key_fingerprint, api_key_last4,
		test_job_id, test_passed_at, test_latency_ms, test_http_code,
		contact_info, submitter_ip_hash, locale,
		admin_note, admin_config_json, reviewed_at,
		created_at, updated_at,
		agreement_accepted, agreement_accepted_at, agreement_version
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	result, err := s.db.ExecContext(ctx, query,
		sub.PublicID, sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.Model, sub.ModelVendor, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL, sub.APIKeyEncrypted, sub.APIKeyFingerprint, sub.APIKeyLast4,
		sub.TestJobID, sub.TestPassedAt, sub.TestLatency, sub.TestHTTPCode,
		nullStr(sub.ContactInfo), nullStr(sub.SubmitterIPHash), nullStr(sub.Locale),
		nullStr(sub.AdminNote), nullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.CreatedAt, sub.UpdatedAt,
		boolToInt(sub.AgreementAccepted), sub.AgreementAcceptedAt, sub.AgreementVersion,
	)
	if err != nil {
		return fmt.Errorf("保存申请失败: %w", err)
	}

	id, err := result.LastInsertId()
	if err == nil {
		sub.ID = id
	}
	return nil
}

// GetByPublicID 按公开 ID 查询
func (s *SQLStore) GetByPublicID(ctx context.Context, publicID string) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+allColumns+" FROM onboarding_submissions WHERE public_id = ?", publicID)
}

// GetByID 按内部 ID 查询
func (s *SQLStore) GetByID(ctx context.Context, id int64) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+allColumns+" FROM onboarding_submissions WHERE id = ?", id)
}

// List 列表查询
func (s *SQLStore) List(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error) {
	// count 与 list 共用同一组 where 条件与过滤参数，list 再追加分页参数，
	// 保证两次查询命中的行集合一致。
	var conditions []string
	var filterArgs []any

	if status != "" && status != "all" {
		conditions = append(conditions, "status = ?")
		filterArgs = append(filterArgs, status)
	}
	if search != "" {
		conditions = append(conditions, "public_id LIKE ? ESCAPE '!'")
		filterArgs = append(filterArgs, search)
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := "SELECT COUNT(*) FROM onboarding_submissions" + whereClause
	listQuery := "SELECT " + allColumns + " FROM onboarding_submissions" + whereClause +
		" ORDER BY created_at DESC LIMIT ? OFFSET ?"

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, filterArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计申请数失败: %w", err)
	}

	listArgs := append(append([]any{}, filterArgs...), limit, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询申请列表失败: %w", err)
	}
	defer rows.Close()

	var results []*Submission
	for rows.Next() {
		sub, err := scanSubmission(rows)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, sub)
	}
	return results, total, rows.Err()
}

// Update 更新申请
func (s *SQLStore) Update(ctx context.Context, sub *Submission) error {
	query := `
	UPDATE onboarding_submissions SET
		status = ?, provider_name = ?, website_url = ?, category = ?,
		service_type = ?, template_name = ?, model = ?, model_vendor = ?, sponsor_level = ?,
		channel_type = ?, channel_source = ?, channel_group = ?, channel_code = ?,
		target_provider = ?, target_service = ?, target_channel = ?,
		channel_name = ?, listed_since = ?, expires_at = ?, price_min = ?, price_max = ?,
		base_url = ?,
		contact_info = ?,
		admin_note = ?, admin_config_json = ?, reviewed_at = ?,
		updated_at = ?
	WHERE id = ?`

	_, err := s.db.ExecContext(ctx, query,
		sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.Model, sub.ModelVendor, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL,
		nullStr(sub.ContactInfo),
		nullStr(sub.AdminNote), nullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.UpdatedAt,
		sub.ID,
	)
	if err != nil {
		return fmt.Errorf("更新申请失败: %w", err)
	}
	return nil
}

// CountByIPToday 统计今天的提交数
func (s *SQLStore) CountByIPToday(ctx context.Context, ipHash string) (int, error) {
	start, end := todayRange()
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE submitter_ip_hash = ? AND created_at >= ? AND created_at < ?",
		ipHash, start, end,
	).Scan(&count)
	return count, err
}

// CountByFingerprint 统计未驳回的同指纹申请数
func (s *SQLStore) CountByFingerprint(ctx context.Context, fingerprint string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE api_key_fingerprint = ? AND status != 'rejected'",
		fingerprint,
	).Scan(&count)
	return count, err
}

// DeleteByPublicID 按公开 ID 删除申请
func (s *SQLStore) DeleteByPublicID(ctx context.Context, publicID string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM onboarding_submissions WHERE public_id = ?", publicID)
	if err != nil {
		return fmt.Errorf("删除申请失败: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("申请不存在")
	}
	return nil
}

// === 内部辅助 ===

const allColumns = `id, public_id, status,
	provider_name, website_url, category,
	service_type, template_name, model, model_vendor, sponsor_level,
	channel_type, channel_source, channel_group, channel_code,
	target_provider, target_service, target_channel,
	channel_name, listed_since, expires_at, price_min, price_max,
	base_url, api_key_encrypted, api_key_fingerprint, api_key_last4,
	test_job_id, test_passed_at, test_latency_ms, test_http_code,
	contact_info, submitter_ip_hash, locale,
	admin_note, admin_config_json, reviewed_at,
	created_at, updated_at,
	agreement_accepted, agreement_accepted_at, agreement_version`

// scanner 是 *sql.Row 和 *sql.Rows 的共同扫描接口
type scanner interface {
	Scan(dest ...any) error
}

func scanSubmission(s scanner) (*Submission, error) {
	var sub Submission
	var contactInfo, ipHash, locale, adminNote, adminConfigJSON sql.NullString
	var reviewedAt sql.NullInt64
	var agreementAccepted int // SQLite 以 INTEGER 0/1 存储 bool

	err := s.Scan(
		&sub.ID, &sub.PublicID, &sub.Status,
		&sub.ProviderName, &sub.WebsiteURL, &sub.Category,
		&sub.ServiceType, &sub.TemplateName, &sub.Model, &sub.ModelVendor, &sub.SponsorLevel,
		&sub.ChannelType, &sub.ChannelSource, &sub.ChannelGroup, &sub.ChannelCode,
		&sub.TargetProvider, &sub.TargetService, &sub.TargetChannel,
		&sub.ChannelName, &sub.ListedSince, &sub.ExpiresAt, &sub.PriceMin, &sub.PriceMax,
		&sub.BaseURL, &sub.APIKeyEncrypted, &sub.APIKeyFingerprint, &sub.APIKeyLast4,
		&sub.TestJobID, &sub.TestPassedAt, &sub.TestLatency, &sub.TestHTTPCode,
		&contactInfo, &ipHash, &locale,
		&adminNote, &adminConfigJSON, &reviewedAt,
		&sub.CreatedAt, &sub.UpdatedAt,
		&agreementAccepted, &sub.AgreementAcceptedAt, &sub.AgreementVersion,
	)
	if err != nil {
		return nil, err
	}

	sub.ContactInfo = contactInfo.String
	sub.SubmitterIPHash = ipHash.String
	sub.Locale = locale.String
	sub.AdminNote = adminNote.String
	sub.AdminConfigJSON = adminConfigJSON.String
	sub.AgreementAccepted = agreementAccepted != 0
	if reviewedAt.Valid {
		v := reviewedAt.Int64
		sub.ReviewedAt = &v
	}

	return &sub, nil
}

func (s *SQLStore) scanOne(ctx context.Context, query string, args ...any) (*Submission, error) {
	row := s.db.QueryRowContext(ctx, query, args...)
	sub, err := scanSubmission(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询申请失败: %w", err)
	}
	return sub, nil
}

// nullStr 将空字符串转为 sql.NullString
func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// boolToInt 将 bool 转为 SQLite 存储用的 0/1（SQLite 无原生 BOOLEAN 类型）
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
