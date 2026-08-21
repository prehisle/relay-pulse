package onboarding

import (
	"context"
	"time"
)

// SubmissionStatus 申请状态
type SubmissionStatus string

const (
	StatusPending   SubmissionStatus = "pending"
	StatusApproved  SubmissionStatus = "approved"
	StatusRejected  SubmissionStatus = "rejected"
	StatusPublished SubmissionStatus = "published"
)

// Submission 收录申请
type Submission struct {
	ID       int64            `json:"id"`
	PublicID string           `json:"public_id"`
	Status   SubmissionStatus `json:"status"`

	// 服务商信息
	ProviderName string `json:"provider_name"`
	WebsiteURL   string `json:"website_url"`
	Category     string `json:"category"` // commercial / public

	// 服务与模板
	ServiceType  string `json:"service_type"` // cc / cx / gm
	TemplateName string `json:"template_name"`

	// 行级模型（仅第一方厂商通用模板需要）。非 native 模板的模型由模板自身声明，这里恒为空。
	// Model 直接写完整模型 ID：admin JSON API 传不了 request_model，靠 {{MODEL}} 的
	// request_model → model 回退链发出去；副作用正面——model 作 DB 业务键更稳。
	Model string `json:"model"`
	// ModelVendor 模型厂商受控 code（见 internal/modelvendor）。可为空，由管理员在上架前补。
	ModelVendor string `json:"model_vendor"`

	// 赞助等级
	SponsorLevel string `json:"sponsor_level"` // public / signal / pulse

	// 通道
	ChannelType    string  `json:"channel_type"`    // O / R / M
	ChannelSource  string  `json:"channel_source"`  // 受控词表代码（2-5 位小写，见 ChannelSourceCatalog）
	ChannelGroup   string  `json:"channel_group"`   // 中转商分组（1-8 位小写）；新提交默认 main，旧数据可为空
	ChannelCode    string  `json:"channel_code"`    // 派生: {type}-{source}-{group}（旧数据可为 {type}-{source}）
	TargetProvider string  `json:"target_provider"` // 发布时 provider 覆盖（可选）
	TargetService  string  `json:"target_service"`  // 发布时 service 覆盖（可选）
	TargetChannel  string  `json:"target_channel"`  // 发布时 channel 覆盖（可选）
	ChannelName    string  `json:"channel_name"`
	ListedSince    string  `json:"listed_since"`
	ExpiresAt      string  `json:"expires_at"` // 到期日期（可选，格式 YYYY-MM-DD）
	PriceMin       float64 `json:"price_min"`
	PriceMax       float64 `json:"price_max"`

	// 接入信息
	BaseURL           string `json:"base_url"`
	APIKeyEncrypted   string `json:"api_key_encrypted"`
	APIKeyFingerprint string `json:"api_key_fingerprint"`
	APIKeyLast4       string `json:"api_key_last4"`

	// 测试证明
	TestJobID    string `json:"test_job_id"`
	TestPassedAt int64  `json:"test_passed_at"`
	TestLatency  int    `json:"test_latency_ms"`
	TestHTTPCode int    `json:"test_http_code"`

	// 联系方式（可选）
	ContactInfo string `json:"-"`

	// 提交者元数据
	SubmitterIPHash string `json:"submitter_ip_hash"`
	Locale          string `json:"locale"`

	// 协议确认（提交时一次性落库，作审计留痕，后续不可改）
	AgreementAccepted   bool   `json:"agreement_accepted"`
	AgreementAcceptedAt int64  `json:"agreement_accepted_at"`
	AgreementVersion    string `json:"agreement_version"`

	// 管理员审核
	AdminNote       string `json:"admin_note"`
	AdminConfigJSON string `json:"admin_config_json"` // 管理员调整后的完整 ServiceConfig JSON
	ReviewedAt      *int64 `json:"reviewed_at"`

	// 时间戳
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

// Store 定义收录申请的持久化操作接口
type Store interface {
	// Save 保存新申请
	Save(ctx context.Context, s *Submission) error

	// ConsumeTestJobID 原子占用一个测试任务 ID，使同一次探测的 proof 只能兑换一次提交。
	// 返回 true 表示本次抢占成功；false 表示该 ID 此前已被消费（proof 重放）。
	ConsumeTestJobID(ctx context.Context, jobID string) (bool, error)

	// GetByPublicID 按公开 ID 查询申请
	GetByPublicID(ctx context.Context, publicID string) (*Submission, error)

	// GetByID 按内部 ID 查询申请
	GetByID(ctx context.Context, id int64) (*Submission, error)

	// List 列表查询（支持状态过滤与 public_id 模糊搜索）。
	// search 为已在调用层完成 LIKE 转义并包裹通配符的模式串；空串表示不按 public_id 过滤。
	List(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error)

	// Update 更新申请字段
	Update(ctx context.Context, s *Submission) error

	// CountByIPToday 统计指定 IP hash 今天的提交数
	CountByIPToday(ctx context.Context, ipHash string) (int, error)

	// CountByFingerprint 统计指定 API Key 指纹的未驳回申请数
	CountByFingerprint(ctx context.Context, fingerprint string) (int, error)

	// DeleteByPublicID 按公开 ID 删除申请（硬删除）
	DeleteByPublicID(ctx context.Context, publicID string) error
}

// todayRange 返回今天 UTC 00:00:00 和明天 UTC 00:00:00 的 Unix 时间戳
func todayRange() (int64, int64) {
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return today.Unix(), today.Add(24 * time.Hour).Unix()
}
