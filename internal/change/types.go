package change

import "context"

// RequestStatus 变更请求状态
type RequestStatus string

const (
	StatusPending  RequestStatus = "pending"
	StatusApproved RequestStatus = "approved"
	StatusRejected RequestStatus = "rejected"
	StatusApplied  RequestStatus = "applied"
)

// TestVariant 自助测试 payload 变体元数据（供前端展示）
type TestVariant struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
}

// ChangeRequest 变更请求
type ChangeRequest struct {
	ID       int64         `json:"id"`
	PublicID string        `json:"public_id"`
	Status   RequestStatus `json:"status"`

	// 目标通道
	TargetProvider string `json:"target_provider"`
	TargetService  string `json:"target_service"`
	TargetChannel  string `json:"target_channel"`
	TargetKey      string `json:"target_key"` // provider--service--channel
	ApplyMode      string `json:"apply_mode"` // "auto" | "manual"

	// 认证
	AuthFingerprint string `json:"auth_fingerprint"`
	AuthLast4       string `json:"auth_last4"`

	// 变更内容
	CurrentSnapshot string `json:"current_snapshot"` // JSON: 提交时的当前值快照
	ProposedChanges string `json:"proposed_changes"` // JSON: {field: newValue} patch

	// 新 API Key（如有变更）
	NewKeyEncrypted   string `json:"new_key_encrypted,omitempty"`
	NewKeyFingerprint string `json:"new_key_fingerprint,omitempty"`
	NewKeyLast4       string `json:"new_key_last4,omitempty"`

	// 测试（base_url 变更或 new_api_key 不为空时必须）
	RequiresTest bool   `json:"requires_test"`
	TestType     string `json:"test_type,omitempty"`
	TestVariant  string `json:"test_variant,omitempty"`
	TestJobID    string `json:"test_job_id,omitempty"`
	TestPassedAt int64  `json:"test_passed_at,omitempty"`
	TestLatency  int    `json:"test_latency_ms,omitempty"`
	TestHTTPCode int    `json:"test_http_code,omitempty"`

	// 管理
	AdminNote  string `json:"admin_note,omitempty"`
	ReviewedAt *int64 `json:"reviewed_at,omitempty"`
	AppliedAt  *int64 `json:"applied_at,omitempty"`

	// 提交者元数据
	SubmitterIPHash string `json:"submitter_ip_hash,omitempty"`
	Locale          string `json:"locale,omitempty"`

	// 反作弊 re-attestation（仅在变更触及 base_url/API Key 时由后端盖戳；历史行为空）
	AgreementAccepted   bool   `json:"agreement_accepted"`
	AgreementAcceptedAt *int64 `json:"agreement_accepted_at,omitempty"`
	AgreementVersion    string `json:"agreement_version,omitempty"`

	// 时间戳
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`

	// === 以下为 API 响应 transient 字段：不入库，由 AdminList/AdminGetDetail 实时从 MonitorStore 填充 ===
	// LiveCurrent 仅含 proposed_changes 涉及字段的通道「真实当前值」（取代提交时 current_snapshot）。
	// 通道为 manual / 已删除 / 读失败时为空，前端回退展示 current_snapshot。
	LiveCurrent       map[string]string `json:"live_current,omitempty"`
	LiveCurrentSource string            `json:"live_current_source,omitempty"` // auto | manual | deleted | error
}

// AuthCandidate 认证后返回的通道候选
type AuthCandidate struct {
	Provider   string `json:"provider"`
	Service    string `json:"service"`
	Channel    string `json:"channel"`
	MonitorKey string `json:"monitor_key"` // provider--service--channel
	ApplyMode  string `json:"apply_mode"`  // "auto" | "manual"

	// 当前可编辑值
	ProviderName string `json:"provider_name"`
	ProviderURL  string `json:"provider_url"`
	ChannelName  string `json:"channel_name"`
	Category     string `json:"category"`
	SponsorLevel string `json:"sponsor_level"`
	ListedSince  string `json:"listed_since"`
	ExpiresAt    string `json:"expires_at"`
	PriceMin     string `json:"price_min"`
	PriceMax     string `json:"price_max"`
	BaseURL      string `json:"base_url"`
	KeyLast4     string `json:"key_last4"`

	// 测试元数据（由 AuthIndex.Rebuild 从 probe 注册表填充）
	TestType           string        `json:"test_type"`
	TestTypeName       string        `json:"test_type_name"`
	DefaultTestVariant string        `json:"default_test_variant,omitempty"`
	TestVariants       []TestVariant `json:"test_variants,omitempty"`
}

// Store 变更请求持久化接口
type Store interface {
	Save(ctx context.Context, r *ChangeRequest) error
	GetByPublicID(ctx context.Context, publicID string) (*ChangeRequest, error)
	List(ctx context.Context, status string, limit, offset int) ([]*ChangeRequest, int, error)
	Update(ctx context.Context, r *ChangeRequest) error
	CountByIPToday(ctx context.Context, ipHash string) (int, error)
	DeleteByPublicID(ctx context.Context, publicID string) error
}
