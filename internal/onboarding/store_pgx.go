package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxStore 基于 pgxpool 的 Store 实现（适用于 PostgreSQL）。
type PgxStore struct {
	pool *pgxpool.Pool
}

// NewPgxStore 创建基于 pgxpool 的 Store。
func NewPgxStore(pool *pgxpool.Pool) *PgxStore {
	return &PgxStore{pool: pool}
}

// InitTable 创建 onboarding_submissions 表和索引。
func (s *PgxStore) InitTable(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS onboarding_submissions (
		id BIGSERIAL PRIMARY KEY,
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
		price_min DOUBLE PRECISION NOT NULL DEFAULT 0,
		price_max DOUBLE PRECISION NOT NULL DEFAULT 0,

		base_url TEXT NOT NULL,
		api_key_encrypted TEXT NOT NULL,
		api_key_fingerprint TEXT NOT NULL,
		api_key_last4 TEXT NOT NULL,

		test_job_id TEXT NOT NULL,
		test_passed_at BIGINT NOT NULL,
		test_latency_ms INTEGER NOT NULL DEFAULT 0,
		test_http_code INTEGER NOT NULL DEFAULT 0,

		contact_info TEXT,
		submitter_ip_hash TEXT,
		locale TEXT,

		admin_note TEXT,
		admin_config_json TEXT,
		reviewed_at BIGINT,

		created_at BIGINT NOT NULL,
		updated_at BIGINT NOT NULL,

		agreement_accepted BOOLEAN NOT NULL DEFAULT false,
		agreement_accepted_at BIGINT NOT NULL DEFAULT 0,
		agreement_version TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS onboarding_used_test_jobs (
		job_id TEXT PRIMARY KEY,
		used_at BIGINT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_onboarding_status ON onboarding_submissions(status, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_onboarding_fingerprint ON onboarding_submissions(api_key_fingerprint);
	`
	_, err := s.pool.Exec(ctx, schema)
	if err != nil {
		return err
	}

	for _, ddl := range []string{
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS channel_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS listed_since TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS price_min DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS price_max DOUBLE PRECISION NOT NULL DEFAULT 0`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS expires_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS target_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS target_service TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS target_channel TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS channel_group TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS agreement_accepted BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS agreement_accepted_at BIGINT NOT NULL DEFAULT 0`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS agreement_version TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE onboarding_submissions ADD COLUMN IF NOT EXISTS model_vendor TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.pool.Exec(ctx, ddl); err != nil {
			return fmt.Errorf("迁移 onboarding_submissions 失败: %w", err)
		}
	}
	return s.backfillUsedTestJobs(ctx)
}

// backfillUsedTestJobs 把历史提交用过的 test_job_id 标记为已消费。理由见 SQLStore 同名方法。
func (s *PgxStore) backfillUsedTestJobs(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO onboarding_used_test_jobs(job_id, used_at)
		SELECT test_job_id, MAX(created_at)
		FROM onboarding_submissions
		WHERE test_job_id <> ''
		GROUP BY test_job_id
		ON CONFLICT (job_id) DO NOTHING
	`)
	if err != nil {
		return fmt.Errorf("回填已消费 test_job_id 失败: %w", err)
	}
	return nil
}

// ConsumeTestJobID 原子占用测试任务 ID；已被占用时返回 false。
func (s *PgxStore) ConsumeTestJobID(ctx context.Context, jobID string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO onboarding_used_test_jobs(job_id, used_at)
		VALUES ($1, $2)
		ON CONFLICT (job_id) DO NOTHING
	`, jobID, time.Now().Unix())
	if err != nil {
		return false, fmt.Errorf("消费 test_job_id 失败: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Save 保存新申请
func (s *PgxStore) Save(ctx context.Context, sub *Submission) error {
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
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,$41)
	RETURNING id`

	err := s.pool.QueryRow(ctx, query,
		sub.PublicID, sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.Model, sub.ModelVendor, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL, sub.APIKeyEncrypted, sub.APIKeyFingerprint, sub.APIKeyLast4,
		sub.TestJobID, sub.TestPassedAt, sub.TestLatency, sub.TestHTTPCode,
		pgxNullStr(sub.ContactInfo), pgxNullStr(sub.SubmitterIPHash), pgxNullStr(sub.Locale),
		pgxNullStr(sub.AdminNote), pgxNullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.CreatedAt, sub.UpdatedAt,
		sub.AgreementAccepted, sub.AgreementAcceptedAt, sub.AgreementVersion,
	).Scan(&sub.ID)
	if err != nil {
		return fmt.Errorf("保存申请失败: %w", err)
	}
	return nil
}

// GetByPublicID 按公开 ID 查询
func (s *PgxStore) GetByPublicID(ctx context.Context, publicID string) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+pgxAllColumns+" FROM onboarding_submissions WHERE public_id = $1", publicID)
}

// GetByID 按内部 ID 查询
func (s *PgxStore) GetByID(ctx context.Context, id int64) (*Submission, error) {
	return s.scanOne(ctx, "SELECT "+pgxAllColumns+" FROM onboarding_submissions WHERE id = $1", id)
}

// List 列表查询
func (s *PgxStore) List(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error) {
	// count 与 list 共用 where 条件；占位符按追加顺序编号（$1、$2…），
	// list 再把分页参数接在过滤参数之后。
	var conditions []string
	var filterArgs []any

	if status != "" && status != "all" {
		filterArgs = append(filterArgs, status)
		conditions = append(conditions, fmt.Sprintf("status = $%d", len(filterArgs)))
	}
	if search != "" {
		filterArgs = append(filterArgs, search)
		conditions = append(conditions, fmt.Sprintf("public_id LIKE $%d ESCAPE '!'", len(filterArgs)))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = " WHERE " + strings.Join(conditions, " AND ")
	}

	countQuery := "SELECT COUNT(*) FROM onboarding_submissions" + whereClause
	listQuery := "SELECT " + pgxAllColumns + " FROM onboarding_submissions" + whereClause +
		fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(filterArgs)+1, len(filterArgs)+2)

	var total int
	if err := s.pool.QueryRow(ctx, countQuery, filterArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("统计申请数失败: %w", err)
	}

	listArgs := append(append([]any{}, filterArgs...), limit, offset)
	rows, err := s.pool.Query(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("查询申请列表失败: %w", err)
	}
	defer rows.Close()

	var results []*Submission
	for rows.Next() {
		sub, err := pgxScanSubmission(rows)
		if err != nil {
			return nil, 0, err
		}
		results = append(results, sub)
	}
	return results, total, rows.Err()
}

// Update 更新申请
func (s *PgxStore) Update(ctx context.Context, sub *Submission) error {
	query := `
	UPDATE onboarding_submissions SET
		status = $1, provider_name = $2, website_url = $3, category = $4,
		service_type = $5, template_name = $6, model = $7, model_vendor = $8, sponsor_level = $9,
		channel_type = $10, channel_source = $11, channel_group = $12, channel_code = $13,
		target_provider = $14, target_service = $15, target_channel = $16,
		channel_name = $17, listed_since = $18, expires_at = $19, price_min = $20, price_max = $21,
		base_url = $22,
		contact_info = $23,
		admin_note = $24, admin_config_json = $25, reviewed_at = $26,
		updated_at = $27
	WHERE id = $28`

	_, err := s.pool.Exec(ctx, query,
		sub.Status, sub.ProviderName, sub.WebsiteURL, sub.Category,
		sub.ServiceType, sub.TemplateName, sub.Model, sub.ModelVendor, sub.SponsorLevel,
		sub.ChannelType, sub.ChannelSource, sub.ChannelGroup, sub.ChannelCode,
		sub.TargetProvider, sub.TargetService, sub.TargetChannel,
		sub.ChannelName, sub.ListedSince, sub.ExpiresAt, sub.PriceMin, sub.PriceMax,
		sub.BaseURL,
		pgxNullStr(sub.ContactInfo),
		pgxNullStr(sub.AdminNote), pgxNullStr(sub.AdminConfigJSON), sub.ReviewedAt,
		sub.UpdatedAt,
		sub.ID,
	)
	if err != nil {
		return fmt.Errorf("更新申请失败: %w", err)
	}
	return nil
}

// CountByIPToday 统计今天的提交数
func (s *PgxStore) CountByIPToday(ctx context.Context, ipHash string) (int, error) {
	start, end := todayRange()
	var count int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE submitter_ip_hash = $1 AND created_at >= $2 AND created_at < $3",
		ipHash, start, end,
	).Scan(&count)
	return count, err
}

// CountByFingerprint 统计未驳回的同指纹申请数
func (s *PgxStore) CountByFingerprint(ctx context.Context, fingerprint string) (int, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM onboarding_submissions WHERE api_key_fingerprint = $1 AND status != 'rejected'",
		fingerprint,
	).Scan(&count)
	return count, err
}

// DeleteByPublicID 按公开 ID 删除申请
func (s *PgxStore) DeleteByPublicID(ctx context.Context, publicID string) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM onboarding_submissions WHERE public_id = $1", publicID)
	if err != nil {
		return fmt.Errorf("删除申请失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("申请不存在")
	}
	return nil
}

// === 内部辅助 ===

const pgxAllColumns = `id, public_id, status,
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

func pgxScanSubmission(row pgx.Row) (*Submission, error) {
	var sub Submission
	var contactInfo, ipHash, locale, adminNote, adminConfigJSON *string
	var reviewedAt *int64

	err := row.Scan(
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
		&sub.AgreementAccepted, &sub.AgreementAcceptedAt, &sub.AgreementVersion,
	)
	if err != nil {
		return nil, err
	}

	if contactInfo != nil {
		sub.ContactInfo = *contactInfo
	}
	if ipHash != nil {
		sub.SubmitterIPHash = *ipHash
	}
	if locale != nil {
		sub.Locale = *locale
	}
	if adminNote != nil {
		sub.AdminNote = *adminNote
	}
	if adminConfigJSON != nil {
		sub.AdminConfigJSON = *adminConfigJSON
	}
	sub.ReviewedAt = reviewedAt

	return &sub, nil
}

func (s *PgxStore) scanOne(ctx context.Context, query string, args ...any) (*Submission, error) {
	row := s.pool.QueryRow(ctx, query, args...)
	sub, err := pgxScanSubmission(row)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("查询申请失败: %w", err)
	}
	return sub, nil
}

// pgxNullStr 将空字符串转为 nil（pgx 自动处理 nil → NULL）
func pgxNullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
