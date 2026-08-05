package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"monitor/internal/config"
	"monitor/internal/displayname"
	"monitor/internal/logger"
	"monitor/internal/urlutil"
)

// pscSegmentPattern 校验 PSC 段仅允许小写字母、数字、短横线，且不能以短横线开头或结尾。
var pscSegmentPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// 自助收录字段规范（提交即强制）：
//   - provider_name 为服务商展示名（允许中文等任意可见文本，拒不可见字符，≤100 rune）；发布时机器 slug 从它派生或由 target_provider 覆盖
//   - channel_source 必须是受控词表 ChannelSourceCatalog 中、对应 service 下的 2-5 位小写代码
//   - channel_group 为用户自定义 1-8 位小写分组代号（中转商自己的分组），留空回退 channelGroupDefault
//   - channel_name 为可选的通道展示名（允许中文等任意语言），仅用于 UI 显示，不参与 channel_code/PSC 派生
const channelGroupDefault = "main"

// allowedSelfServeSponsorLevels 是自助收录可提交的赞助等级白名单，与 /api/onboarding/meta
// 下发给前端的选项保持一致（当前只有 pulse；空值表示未指定，由管理员在发布前定夺）。
// 商务等级（beacon/backbone/core）须人工对接，不接受用户自选——它们会直接进入上架配置。
var allowedSelfServeSponsorLevels = map[string]bool{
	"":      true,
	"pulse": true,
}

var (
	channelSourcePattern = regexp.MustCompile(`^[a-z0-9]{2,5}$`)
	channelGroupPattern  = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// ChannelSourceOption 是自助收录「通道来源」词表的单一真相源条目。
// Category 既用于前端分组展示，也参与「通道类型↔来源」自洽校验（见 channelTypeAllowedCategories），
// 但不参与 channel code 派生。
type ChannelSourceOption struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// ChannelSourceCatalog 是后端校验与前端 meta 下发共用的唯一「通道来源」词表，按 service_type 划分。
// 人工新增/调整来源时只改这里，避免 Submit 校验、/meta 下发、前端选项三处漂移。
// 约束：每个 Value 必须满足 channelSourcePattern（2-5 位小写字母/数字）。
//
// cc/cx 的 nat 是第一方厂商（智谱 GLM / 月之暗面 Kimi / MiniMax / DeepSeek / Qwen 等）
// 用自家模型开放的 Anthropic Messages / OpenAI Responses 兼容端点——协议是那两家的，
// 模型是厂商自己的，两者由 model_vendor 正交轴分别表达。不复用 api：那两条的 Label
// 明确写着 Anthropic Console / OpenAI Platform，指的是那两家自己的官方入口。
// gm 不加：Gemini 的 api（AI Studio）本身就是第一方入口。
// Category=official 使其自动落入 channelTypeAllowedCategories 的 O（官方直连 / 官方转售），
// 那张映射表无需改动。
var ChannelSourceCatalog = map[string][]ChannelSourceOption{
	"cc": {
		{Value: "pro", Label: "Claude Pro 订阅", Category: "subscription"},
		{Value: "max", Label: "Claude Max 订阅", Category: "subscription"},
		{Value: "team", Label: "Claude Team", Category: "subscription"},
		{Value: "ent", Label: "Claude Enterprise", Category: "subscription"},
		{Value: "api", Label: "Anthropic Console API", Category: "official"},
		{Value: "nat", Label: "厂商官方 API（自有模型）", Category: "official"},
		{Value: "aws", Label: "AWS Bedrock", Category: "cloud"},
		{Value: "azr", Label: "Azure AI Foundry", Category: "cloud"},
		{Value: "gcp", Label: "Google Vertex AI", Category: "cloud"},
		{Value: "kiro", Label: "Kiro（逆向）", Category: "reverse"},
		{Value: "antg", Label: "Antigravity（逆向）", Category: "reverse"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
	"cx": {
		{Value: "plus", Label: "ChatGPT Plus", Category: "subscription"},
		{Value: "pro", Label: "ChatGPT Pro", Category: "subscription"},
		{Value: "team", Label: "ChatGPT Team", Category: "subscription"},
		{Value: "biz", Label: "ChatGPT Business", Category: "subscription"},
		{Value: "ent", Label: "ChatGPT Enterprise", Category: "subscription"},
		{Value: "api", Label: "OpenAI Platform API", Category: "official"},
		{Value: "nat", Label: "厂商官方 API（自有模型）", Category: "official"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
	"gm": {
		{Value: "free", Label: "Google 账号 Free", Category: "subscription"},
		{Value: "adv", Label: "Gemini Advanced", Category: "subscription"},
		{Value: "api", Label: "Gemini API (AI Studio)", Category: "official"},
		{Value: "gcp", Label: "Google Vertex AI", Category: "cloud"},
		{Value: "antg", Label: "Antigravity（逆向）", Category: "reverse"},
		{Value: "mix", Label: "混合 / 多上游", Category: "mixed"},
	},
}

// ChannelSourceOptionsByService 返回词表的深拷贝，供 API 层下发前端，避免外部修改污染真相源。
func ChannelSourceOptionsByService() map[string][]ChannelSourceOption {
	out := make(map[string][]ChannelSourceOption, len(ChannelSourceCatalog))
	for service, opts := range ChannelSourceCatalog {
		out[service] = append([]ChannelSourceOption(nil), opts...)
	}
	return out
}

// channelTypeAllowedCategories 定义每个通道类型（O/R/M）允许搭配的来源类别（ChannelSourceOption.Category）。
// 单一真相源：同时供 Submit/AdminUpdate 后端校验与 /api/onboarding/meta 下发前端做下拉过滤，
// 避免「官方通道却选逆向来源」一类不自洽提交，且杜绝前后端规则漂移。
// 当前为干净划分——每个 Category 恰属一个类型：官方上游归 O、逆向归 R、混合归 M。
var channelTypeAllowedCategories = map[string][]string{
	"O": {"subscription", "official", "cloud"},
	"R": {"reverse"},
	"M": {"mixed"},
}

// ChannelTypeAllowedCategories 返回映射的深拷贝，供 API 层下发前端，避免外部修改污染真相源。
func ChannelTypeAllowedCategories() map[string][]string {
	out := make(map[string][]string, len(channelTypeAllowedCategories))
	for ct, cats := range channelTypeAllowedCategories {
		out[ct] = append([]string(nil), cats...)
	}
	return out
}

// ChannelGroupRule 返回 channel_group 的校验规则，供前端做同步校验。
func ChannelGroupRule() (pattern, defaultValue string, maxLength int) {
	return channelGroupPattern.String(), channelGroupDefault, 8
}

// PSCConflictError 表示 PSC 冲突错误，包含冲突信息和建议值。
type PSCConflictError struct {
	Provider         string
	Service          string
	Channel          string
	SuggestedChannel string
}

func (e *PSCConflictError) Error() string {
	return fmt.Sprintf("PSC %s/%s/%s 已存在于当前运行配置中，请调整 target_channel（建议: %s）",
		e.Provider, e.Service, e.Channel, e.SuggestedChannel)
}

// InvalidProviderSlugError 表示发布时从服务商展示名派生的 provider slug 非法（通常因展示名含非
// 英文字符）、且管理员未经 target_provider 覆盖英文代号。区别于 PSCConflictError（唯一性冲突），
// 供 handler 特判为 4xx + 可操作指引，避免呈现为服务端 500。
type InvalidProviderSlugError struct {
	ProviderName string // 原始展示名（可能含中文）
	DerivedSlug  string // 派生出的非法 slug
}

func (e *InvalidProviderSlugError) Error() string {
	return fmt.Sprintf("服务商名 %q 无法自动生成合法的网址代号（派生值 %q 含中文或其它无法用于网址的字符）；请在「Provider 覆盖」(target_provider) 填写英文代号（小写字母、数字、短横线）后再上架。",
		e.ProviderName, e.DerivedSlug)
}

// Service 提供自助收录的核心业务逻辑。
type Service struct {
	store               Store
	cipher              *KeyCipher
	proofIssuer         *ProofIssuer
	cfg                 *config.OnboardingConfig
	configDir           string               // config.yaml 所在目录（用于定位 templates/ 等）
	monitorStore        *config.MonitorStore // monitors.d/ CRUD
	configMonitorExists func(provider, service, channel string) bool
	mu                  sync.RWMutex
}

// NewService 创建 Service。configDir 是 config.yaml 所在目录。
func NewService(store Store, cfg *config.OnboardingConfig, configDir string) (*Service, error) {
	cipher, err := NewKeyCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 API Key 加密器失败: %w", err)
	}

	proofIssuer := NewProofIssuer(cfg.ProofSecret, cfg.ProofTTLDuration)

	return &Service{
		store:       store,
		cipher:      cipher,
		proofIssuer: proofIssuer,
		cfg:         cfg,
		configDir:   configDir,
	}, nil
}

// SetMonitorStore 设置 monitors.d/ 存储（publish 时写入 monitors.d/）
func (s *Service) SetMonitorStore(store *config.MonitorStore) {
	s.monitorStore = store
}

// SetConfigMonitorCheck 设置主配置 PSC 冲突检查回调。
func (s *Service) SetConfigMonitorCheck(fn func(string, string, string) bool) {
	s.configMonitorExists = fn
}

// SubmitRequest 用户提交申请的请求参数
type SubmitRequest struct {
	ProviderName string `json:"provider_name" binding:"max=100"` // 服务商展示名（可中文）；binding:max 仅粗略上限，精校验/规范化在 displayname.ValidateProviderName
	// binding 用 http_url 而非 url：validator 的 url 标签只要求 scheme 非空，
	// javascript:alert(1) 这类伪协议能过——而该值会作为 provider_url 进入上架配置与前端外链
	WebsiteURL    string `json:"website_url" binding:"required,http_url,max=500"`
	Category      string `json:"category" binding:"required,oneof=commercial public"`
	ServiceType   string `json:"service_type" binding:"required,oneof=cc cx gm"`
	TemplateName  string `json:"template_name" binding:"required,max=100"`
	SponsorLevel  string `json:"sponsor_level" binding:"max=50"`
	ChannelType   string `json:"channel_type" binding:"required,oneof=O R M"`
	ChannelSource string `json:"channel_source" binding:"required,max=5"`
	ChannelGroup  string `json:"channel_group" binding:"max=8"`  // 留空回退 main
	ChannelName   string `json:"channel_name" binding:"max=100"` // 可选展示名（可中文）；精校验在 displayname.ValidateChannelName（业务上限 40 rune）
	BaseURL       string `json:"base_url" binding:"required,url,max=500"`
	APIKey        string `json:"api_key" binding:"required,min=10,max=500"`
	TestProof     string `json:"test_proof" binding:"required"`
	TestJobID     string `json:"test_job_id" binding:"required"`
	TestType      string `json:"test_type" binding:"required,max=100"`    // 测试类型（用于 proof 校验）
	TestAPIURL    string `json:"test_api_url" binding:"required,max=500"` // 测试 API URL（用于 proof 校验）
	TestLatency   int    `json:"test_latency"`
	TestHTTPCode  int    `json:"test_http_code"`
	Locale        string `json:"locale" binding:"max=10"`
	// AgreementAccepted: 用户在提交前逐条勾选「入驻须知与确认」的结果，必须为 true 才受理。
	// 落库的版本号与时间戳由后端盖戳（见 AgreementVersion），不信任客户端值。
	AgreementAccepted bool `json:"agreement_accepted"`
}

// AgreementVersion 标记当前《入驻须知与确认》(docs/user/sponsorship-agreement.md) 的生效版本。
// 协议要点发生实质调整时 bump 此值，便于审计「用户当时同意的是哪一版」。
const AgreementVersion = "2026-07-16"

// SubmitResponse 提交申请的响应
type SubmitResponse struct {
	PublicID    string `json:"public_id"`
	ContactInfo string `json:"contact_info"` // 运营联系方式
}

// Submit 处理用户提交申请。
func (s *Service) Submit(ctx context.Context, req *SubmitRequest, clientIP string) (*SubmitResponse, error) {
	// 赞助等级白名单：自助渠道只受理 /meta 实际下发的 pulse（留空视同 pulse 之下、由管理员定夺）。
	//
	// 此前是黑名单（只挡 public/signal），于是 beacon/backbone/core 这些付费档可由直连 API
	// 自选，且 BuildServiceConfigFromSubmission 会把该值直接灌进上架配置——管理员不改字段
	// 直接发布，自封的付费徽章即刻生效。商务等级须人工对接，不走自助。
	if !allowedSelfServeSponsorLevels[req.SponsorLevel] {
		return nil, fmt.Errorf("赞助等级 %q 不受理自助申请，请选择 pulse 或联系运营（QQ:18058344）", req.SponsorLevel)
	}

	// 必须确认《入驻须知与确认》全部要点（含商务等级付费赞助、API Key 授权等）后方可受理
	if !req.AgreementAccepted {
		return nil, fmt.Errorf("请先阅读并确认《入驻须知与确认》全部要点后再提交")
	}

	// 规范化并校验提交字段（服务商名/通道展示名允许中文但禁不可见字符、来源受控词表、分组格式）
	providerName, err := displayname.ValidateProviderName(req.ProviderName)
	if err != nil {
		return nil, err
	}
	channelName, err := displayname.ValidateChannelName(req.ChannelName)
	if err != nil {
		return nil, err
	}
	channelSource, err := validateChannelTypeSource(req.ChannelType, req.ServiceType, req.ChannelSource)
	if err != nil {
		return nil, err
	}
	channelGroup, err := normalizeGroup(req.ChannelGroup)
	if err != nil {
		return nil, err
	}

	// IP 限流
	ipHash := hashIP(clientIP)
	count, err := s.store.CountByIPToday(ctx, ipHash)
	if err != nil {
		return nil, fmt.Errorf("查询提交限额失败: %w", err)
	}
	if count >= s.cfg.MaxPerIPPerDay {
		return nil, fmt.Errorf("今日提交次数已达上限（%d/%d）", count, s.cfg.MaxPerIPPerDay)
	}

	// 验证 base_url HTTPS
	parsedBaseURL, err := url.Parse(req.BaseURL)
	if err != nil || parsedBaseURL.Scheme != "https" {
		return nil, fmt.Errorf("base_url 必须使用 HTTPS 协议")
	}

	// 验证 test_api_url 与 base_url 的 host+端口一致（共享 urlutil.SameHostPort，与 change 流程同一真相源），
	// 防止"在一个端口测出 proof、却把 base_url 收录成同 host 另一端口"绕过 proof 绑定。
	parsedTestURL, err := url.Parse(req.TestAPIURL)
	if err != nil || parsedTestURL.Hostname() == "" {
		return nil, fmt.Errorf("test_api_url 无效")
	}
	if !urlutil.SameHostPort(parsedBaseURL, parsedTestURL) {
		return nil, fmt.Errorf("base_url 与 test_api_url 的 host/port 必须一致")
	}

	// 加密 API Key
	encrypted, err := s.cipher.Encrypt(req.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密 API Key 失败: %w", err)
	}
	fingerprint := s.cipher.Fingerprint(req.APIKey)
	last4 := Last4(req.APIKey)

	// 验证 test proof（绑定探测参数）
	err = s.proofIssuer.Verify(
		req.TestProof,
		req.TestJobID,
		req.TestType,
		req.TestAPIURL,
		fingerprint,
	)
	if err != nil {
		return nil, fmt.Errorf("测试证明无效: %w", err)
	}

	// 派生 channel code（type-source-group 三段）
	channelCode := deriveChannelCode(req.ChannelType, channelSource, channelGroup)

	now := time.Now().Unix()
	sub := &Submission{
		PublicID:          uuid.New().String(),
		Status:            StatusPending,
		ProviderName:      providerName,
		WebsiteURL:        req.WebsiteURL,
		Category:          req.Category,
		ServiceType:       req.ServiceType,
		TemplateName:      req.TemplateName,
		SponsorLevel:      req.SponsorLevel,
		ChannelType:       req.ChannelType,
		ChannelSource:     channelSource,
		ChannelGroup:      channelGroup,
		ChannelCode:       channelCode,
		ChannelName:       channelName,
		BaseURL:           req.BaseURL,
		APIKeyEncrypted:   encrypted,
		APIKeyFingerprint: fingerprint,
		APIKeyLast4:       last4,
		TestJobID:         req.TestJobID,
		TestPassedAt:      now,
		TestLatency:       req.TestLatency,
		TestHTTPCode:      req.TestHTTPCode,
		SubmitterIPHash:   ipHash,
		Locale:            req.Locale,
		// 协议确认落库审计：后端盖戳版本与时间，不信任客户端
		AgreementAccepted:   true,
		AgreementAcceptedAt: now,
		AgreementVersion:    AgreementVersion,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	// proof 一次性消费：proof 本身是无状态 HMAC，TTL 内可无限次重放，
	// 一次真实探测即可兑换任意多条提交。这里用"抢占 test_job_id"把它钉成一次一用。
	// 紧挨 Save 之前执行，让前面所有纯内存校验失败的路径都不至于白烧掉用户的探测。
	consumed, err := s.store.ConsumeTestJobID(ctx, req.TestJobID)
	if err != nil {
		return nil, fmt.Errorf("记录测试证明消费状态失败: %w", err)
	}
	if !consumed {
		return nil, fmt.Errorf("该测试结果已被使用，请重新测试后再提交")
	}

	if err := s.store.Save(ctx, sub); err != nil {
		return nil, err
	}

	logger.Info("onboarding", "新申请已提交",
		"public_id", sub.PublicID,
		"provider", providerName,
		"service_type", req.ServiceType,
		"channel", channelCode)

	return &SubmitResponse{
		PublicID:    sub.PublicID,
		ContactInfo: s.cfg.ContactInfo,
	}, nil
}

// GetStatus 查询申请状态（用户端）
func (s *Service) GetStatus(ctx context.Context, publicID string) (*Submission, error) {
	return s.store.GetByPublicID(ctx, publicID)
}

// AdminList 管理员列表查询。
// search 为已在 handler 层完成 trim/ToLower/LIKE 转义的模式串，此处仅透传。
func (s *Service) AdminList(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, status, search, limit, offset)
}

// AdminGetDetail 管理员获取详情（含解密后的 API Key）
func (s *Service) AdminGetDetail(ctx context.Context, publicID string) (*Submission, string, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil || sub == nil {
		return sub, "", err
	}

	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return sub, "", fmt.Errorf("解密 API Key 失败: %w", err)
	}

	return sub, apiKey, nil
}

// AdminUpdate 管理员更新申请
func (s *Service) AdminUpdate(ctx context.Context, publicID string, updates map[string]any) (*Submission, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, fmt.Errorf("申请不存在")
	}

	// 记录通道组成字段原值，用于判断是否需要重派生 channel_code（避免误改 legacy 两段记录）
	origServiceType := sub.ServiceType
	origChannelType := sub.ChannelType
	origChannelSource := sub.ChannelSource
	origChannelGroup := sub.ChannelGroup

	// 应用允许的更新字段
	if v, ok := updates["provider_name"].(string); ok && v != "" {
		name, err := displayname.ValidateProviderName(v)
		if err != nil {
			return nil, err
		}
		sub.ProviderName = name
	}
	if v, ok := updates["website_url"].(string); ok && v != "" {
		sub.WebsiteURL = v
	}
	if v, ok := updates["category"].(string); ok && v != "" {
		sub.Category = v
	}
	if v, ok := updates["service_type"].(string); ok && v != "" {
		st := strings.ToLower(strings.TrimSpace(v))
		if st != "cc" && st != "cx" && st != "gm" {
			return nil, fmt.Errorf("service_type 无效（%q），仅支持 cc/cx/gm", v)
		}
		sub.ServiceType = st
	}
	if v, ok := updates["template_name"].(string); ok && v != "" {
		sub.TemplateName = v
	}
	if v, ok := updates["sponsor_level"].(string); ok && v != "" {
		sub.SponsorLevel = v
	}
	if v, ok := updates["channel_type"].(string); ok && v != "" {
		ct := strings.ToUpper(strings.TrimSpace(v))
		if ct != "O" && ct != "R" && ct != "M" {
			return nil, fmt.Errorf("channel_type 无效（%q），仅支持 O/R/M", v)
		}
		sub.ChannelType = ct
	}
	if v, ok := updates["channel_source"].(string); ok && v != "" {
		sub.ChannelSource = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := updates["channel_group"].(string); ok {
		sub.ChannelGroup = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := updates["target_provider"].(string); ok {
		sub.TargetProvider = v
	}
	if v, ok := updates["target_service"].(string); ok {
		sub.TargetService = v
	}
	if v, ok := updates["target_channel"].(string); ok {
		sub.TargetChannel = v
	}
	if v, ok := updates["base_url"].(string); ok && v != "" {
		sub.BaseURL = v
	}
	if v, ok := updates["channel_name"].(string); ok {
		name, err := displayname.ValidateChannelName(v)
		if err != nil {
			return nil, err
		}
		sub.ChannelName = name
	}
	if v, ok := updates["listed_since"].(string); ok {
		sub.ListedSince = v
	}
	if v, ok := updates["expires_at"].(string); ok {
		sub.ExpiresAt = v
	}
	if v, ok := updates["price_min"].(float64); ok {
		sub.PriceMin = v
	}
	if v, ok := updates["price_max"].(float64); ok {
		sub.PriceMax = v
	}
	if v, ok := updates["admin_note"].(string); ok {
		sub.AdminNote = v
	}
	if v, ok := updates["admin_config_json"].(string); ok {
		sub.AdminConfigJSON = v
	}

	// 仅当通道组成字段（service/type/source/group）真正变化时才重新校验并派生 channel_code，
	// 避免管理员仅编辑无关字段时把 legacy 两段记录意外改写成三段或撞新词表校验。
	if sub.ServiceType != origServiceType ||
		sub.ChannelType != origChannelType ||
		sub.ChannelSource != origChannelSource ||
		sub.ChannelGroup != origChannelGroup {
		source, err := validateChannelTypeSource(sub.ChannelType, sub.ServiceType, sub.ChannelSource)
		if err != nil {
			return nil, err
		}
		group, err := normalizeGroup(sub.ChannelGroup)
		if err != nil {
			return nil, err
		}
		sub.ChannelSource = source
		sub.ChannelGroup = group
		sub.ChannelCode = deriveChannelCode(sub.ChannelType, source, group)
	}
	sub.UpdatedAt = time.Now().Unix()

	if err := s.store.Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// AdminDelete 删除申请（硬删除，已上架的不允许删除）
func (s *Service) AdminDelete(ctx context.Context, publicID string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能删除，请先在通道管理中下架")
	}
	return s.store.DeleteByPublicID(ctx, publicID)
}

// AdminReject 驳回申请
func (s *Service) AdminReject(ctx context.Context, publicID, note string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能驳回")
	}

	now := time.Now().Unix()
	sub.Status = StatusRejected
	sub.AdminNote = note
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	return s.store.Update(ctx, sub)
}

// AdminPublish 上架：生成 ServiceConfig 并写入 monitors.d/。
// board 指定目标版块（hot/secondary/cold），优先级高于 AdminConfigJSON 中的同名字段。
// 使用原子文件写入（temp + fsync + rename）确保安全。
func (s *Service) AdminPublish(ctx context.Context, publicID, board string) error {
	if board == "" {
		board = "hot"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status != StatusPending && sub.Status != StatusApproved {
		return fmt.Errorf("只有待审核或已批准的申请可以上架，当前状态: %s", sub.Status)
	}

	// 解密 API Key
	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return fmt.Errorf("解密 API Key 失败: %w", err)
	}

	// 构建 ServiceConfig
	monitorCfg := s.buildServiceConfig(sub, apiKey)

	// 派生路径下（无 AdminConfigJSON 整份覆盖）：若展示名派生出的 provider slug 非法且未覆盖
	// target_provider，返回可操作指引（区别于下方通用 PSC 校验的难懂错误 + 500）。
	// AdminConfigJSON 覆盖路径自带 Provider，仍走下方 validateMonitorConfig 通用校验；
	// 管理员填了非法 target_provider/target_service/target_channel 覆盖值属另一类（预存、对称）
	// 问题，本轮不在此处理，仍走通用校验。
	if sub.AdminConfigJSON == "" &&
		strings.TrimSpace(sub.TargetProvider) == "" &&
		config.ValidateProviderSlug(monitorCfg.Provider) != nil {
		return &InvalidProviderSlugError{ProviderName: sub.ProviderName, DerivedSlug: monitorCfg.Provider}
	}

	// 如果管理员有自定义配置，覆盖
	if sub.AdminConfigJSON != "" {
		var adminCfg config.ServiceConfig
		if err := json.Unmarshal([]byte(sub.AdminConfigJSON), &adminCfg); err != nil {
			return fmt.Errorf("解析管理员配置失败: %w", err)
		}
		monitorCfg = adminCfg
		// 确保 API key 不会被管理员配置覆盖为空
		if monitorCfg.APIKey == "" {
			monitorCfg.APIKey = apiKey
		}
	}
	// board 参数优先级高于 AdminConfigJSON，显式覆盖
	monitorCfg.Board = board

	// 发布前校验：验证生成的 monitor 配置是否合法
	if err := s.validateMonitorConfig(monitorCfg); err != nil {
		return fmt.Errorf("待发布 monitor 配置无效: %w", err)
	}

	// PSC 冲突预检：确认不与已有 monitors 冲突
	if s.configMonitorExists != nil &&
		s.configMonitorExists(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel) {
		suggested := s.suggestUniqueChannel(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel)
		return &PSCConflictError{
			Provider:         monitorCfg.Provider,
			Service:          monitorCfg.Service,
			Channel:          monitorCfg.Channel,
			SuggestedChannel: suggested,
		}
	}

	// 写入 monitors.d/
	if s.monitorStore == nil {
		return fmt.Errorf("MonitorStore 未初始化，无法写入 monitors.d/")
	}

	monitorFile := &config.MonitorFile{
		Metadata: config.MonitorFileMetadata{
			Source:    "onboarding",
			Revision:  1,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Monitors: []config.ServiceConfig{monitorCfg},
	}

	if err := s.monitorStore.Create(monitorFile); err != nil {
		return fmt.Errorf("写入 monitors.d/ 失败: %w", err)
	}

	// 更新 DB 状态
	now := time.Now().Unix()
	sub.Status = StatusPublished
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	if err := s.store.Update(ctx, sub); err != nil {
		// 文件已写入但 DB 更新失败 — 记录错误但不回滚文件
		// 下次热更新会正常加载，管理员可通过 admin 面板修正状态
		logger.Error("onboarding", "更新申请状态失败（文件已写入）",
			"public_id", publicID, "error", err)
		return fmt.Errorf("已写入配置文件但更新数据库状态失败: %w", err)
	}

	logger.Info("onboarding", "申请已上架",
		"public_id", publicID,
		"provider", sub.ProviderName,
		"channel", sub.ChannelCode)

	return nil
}

// IssueProof 签发测试证明（供内联探测调用）。
// 参数来自探测结果：jobID, testType, apiURL, apiKey。
func (s *Service) IssueProof(jobID, testType, apiURL, apiKey string) string {
	proof, _ := s.IssueProofWithExpiry(jobID, testType, apiURL, apiKey)
	return proof
}

// IssueProofWithExpiry 签发测试证明，并返回其绝对过期时间（Unix 秒），供 API 层下发前端。
func (s *Service) IssueProofWithExpiry(jobID, testType, apiURL, apiKey string) (string, int64) {
	fingerprint := s.cipher.Fingerprint(apiKey)
	return s.proofIssuer.IssueWithExpiry(jobID, testType, apiURL, fingerprint)
}

// BuildServiceConfigFromSubmission 将 Submission（连同已解密的 apiKey）翻译成 ServiceConfig。
//
// 该函数是"用户提交字段" → "运行时监测配置"的官方映射点：
//   - 发布到 monitors.d/ 时（AdminPublish）调用
//   - 管理后台对申请做即时探测（AdminTestSubmission）时调用
//   - 用户提交前自助探测（OnboardingTest）时调用（构造虚拟 Submission）
//
// 字段映射规则：
//   - PSC 默认派生：provider=lower(ProviderName 去空格转-)，service=ServiceType，channel=ChannelCode
//   - 管理员可在审核阶段通过 TargetProvider/TargetService/TargetChannel 覆盖 PSC（用于规范化命名）
//
// 注意：返回的 cfg 字段尚未经过模板填充和 Duration 派生；调用方如需用于内联探测，
// 应再过一次 config.ResolveSingleMonitor。
func BuildServiceConfigFromSubmission(sub *Submission, apiKey string) config.ServiceConfig {
	if sub == nil {
		return config.ServiceConfig{}
	}

	// 派生默认 PSC 标识
	providerSlug := strings.ToLower(strings.ReplaceAll(sub.ProviderName, " ", "-"))
	serviceType := sub.ServiceType
	channelCode := sub.ChannelCode

	// 管理员可覆盖最终发布时的 PSC 标识
	if v := strings.TrimSpace(sub.TargetProvider); v != "" {
		providerSlug = v
	}
	if v := strings.TrimSpace(sub.TargetService); v != "" {
		serviceType = v
	}
	if v := strings.TrimSpace(sub.TargetChannel); v != "" {
		channelCode = v
	}

	cfg := config.ServiceConfig{
		Provider:     providerSlug,
		ProviderName: sub.ProviderName,
		ProviderURL:  sub.WebsiteURL,
		Service:      serviceType,
		Channel:      channelCode,
		ChannelName:  sub.ChannelName,
		Template:     sub.TemplateName,
		BaseURL:      sub.BaseURL,
		APIKey:       apiKey,
		Category:     sub.Category,
		ListedSince:  sub.ListedSince,
		ExpiresAt:    sub.ExpiresAt,
		SponsorLevel: config.SponsorLevel(sub.SponsorLevel),
	}
	if sub.PriceMin != 0 {
		v := sub.PriceMin
		cfg.PriceMin = &v
	}
	if sub.PriceMax != 0 {
		v := sub.PriceMax
		cfg.PriceMax = &v
	}
	return cfg
}

// buildServiceConfig 是 BuildServiceConfigFromSubmission 的 method 包装，
// 保留以兼容 Service 内部既有调用方（AdminPublish 等）。
func (s *Service) buildServiceConfig(sub *Submission, apiKey string) config.ServiceConfig {
	return BuildServiceConfigFromSubmission(sub, apiKey)
}

// hashIP 计算 IP 地址的 SHA256 哈希
func hashIP(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:])
}

// validateMonitorConfig 在发布前校验即将写入 monitors.d/ 的 monitor 配置。
func (s *Service) validateMonitorConfig(m config.ServiceConfig) error {
	if err := validatePSCSegment("provider", m.Provider); err != nil {
		return err
	}
	if err := validatePSCSegment("service", m.Service); err != nil {
		return err
	}
	if err := validatePSCSegment("channel", m.Channel); err != nil {
		return err
	}
	if strings.TrimSpace(m.BaseURL) == "" {
		return fmt.Errorf("base_url 不能为空")
	}

	if m.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", m.ExpiresAt); err != nil {
			return fmt.Errorf("expires_at 格式错误，应为 YYYY-MM-DD")
		}
	}

	templateName := strings.TrimSpace(m.Template)
	if templateName == "" {
		return fmt.Errorf("template 不能为空")
	}

	// 检查模板文件是否存在
	templatePath := filepath.Join(s.configDir, "templates", templateName+".json")
	if _, err := config.LoadProbeTemplate(templatePath); err != nil {
		return fmt.Errorf("template %q 不存在或无效: %w", templateName, err)
	}

	return nil
}

// validatePSCSegment 校验 PSC 段格式
func validatePSCSegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if !pscSegmentPattern.MatchString(value) {
		return fmt.Errorf("%s 格式无效（%q），仅允许小写字母、数字、短横线，且不能以短横线开头或结尾", field, value)
	}
	return nil
}

// suggestUniqueChannel 生成不冲突的 channel 名（追加 -2、-3...）
func (s *Service) suggestUniqueChannel(provider, service, channel string) string {
	if s.configMonitorExists == nil {
		return channel + "-2"
	}
	for i := 2; i <= 99; i++ {
		candidate := fmt.Sprintf("%s-%d", channel, i)
		if !s.configMonitorExists(provider, service, candidate) {
			return candidate
		}
	}
	return channel + "-new"
}

// lookupChannelSource 在对应 service 受控词表中查找 channel_source，返回完整 option。
// 词表成员资格是权威判定；格式正则仅用于在非法输入时给出更清晰的错误信息。
func lookupChannelSource(serviceType, source string) (ChannelSourceOption, error) {
	serviceType = strings.ToLower(strings.TrimSpace(serviceType))
	source = strings.ToLower(strings.TrimSpace(source))
	if !channelSourcePattern.MatchString(source) {
		return ChannelSourceOption{}, fmt.Errorf("channel_source 格式无效（%q），应为 2-5 位小写字母或数字", source)
	}
	options, ok := ChannelSourceCatalog[serviceType]
	if !ok {
		return ChannelSourceOption{}, fmt.Errorf("service_type %q 不支持", serviceType)
	}
	for _, opt := range options {
		if opt.Value == source {
			return opt, nil
		}
	}
	return ChannelSourceOption{}, fmt.Errorf("channel_source %q 不在 service_type=%q 的允许来源中，如需新增请联系运营（QQ:18058344）", source, serviceType)
}

// validateChannelSource 校验 channel_source 是否为对应 service 词表中的合法值，返回小写规范值。
func validateChannelSource(serviceType, source string) (string, error) {
	opt, err := lookupChannelSource(serviceType, source)
	if err != nil {
		return "", err
	}
	return opt.Value, nil
}

// validateChannelTypeSource 在 validateChannelSource 基础上追加「通道类型↔来源类别」自洽校验：
// 来源既要在该 service 词表内，其 Category 还须落在该 channelType 的允许集合中。
// channelType 须已规范为 O/R/M（调用方保证）。返回小写规范 source。
func validateChannelTypeSource(channelType, serviceType, source string) (string, error) {
	opt, err := lookupChannelSource(serviceType, source)
	if err != nil {
		return "", err
	}
	allowed, ok := channelTypeAllowedCategories[strings.ToUpper(strings.TrimSpace(channelType))]
	if !ok {
		return "", fmt.Errorf("channel_type 无效（%q），仅支持 O/R/M", channelType)
	}
	for _, cat := range allowed {
		if opt.Category == cat {
			return opt.Value, nil
		}
	}
	return "", fmt.Errorf("通道来源「%s」与通道类型「%s」不匹配，请选择与该类型相符的来源", opt.Label, channelTypeLabel(channelType))
}

// channelTypeLabel 返回通道类型的中文标签，用于校验错误信息。
func channelTypeLabel(channelType string) string {
	switch strings.ToUpper(strings.TrimSpace(channelType)) {
	case "O":
		return "官方通道"
	case "R":
		return "逆向通道"
	case "M":
		return "混合通道"
	default:
		return channelType
	}
}

// normalizeGroup 规范化 channel_group：留空回退默认值，并校验格式。返回小写规范值。
func normalizeGroup(group string) (string, error) {
	group = strings.ToLower(strings.TrimSpace(group))
	if group == "" {
		group = channelGroupDefault
	}
	if !channelGroupPattern.MatchString(group) {
		return "", fmt.Errorf("channel_group 格式无效（%q），应为 1-8 位小写字母或数字", group)
	}
	return group, nil
}

// deriveChannelCode 从通道类型、来源、分组派生通道代码 {type}-{source}-{group}（全小写）。
// group 为空时退化为两段 {type}-{source}，仅用于兼容旧申请与旧 monitors.d/ 通道。
func deriveChannelCode(channelType, channelSource, channelGroup string) string {
	t := strings.ToLower(strings.TrimSpace(channelType))
	source := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(channelSource), " ", ""))
	group := strings.ToLower(strings.TrimSpace(channelGroup))
	if group == "" {
		return fmt.Sprintf("%s-%s", t, source)
	}
	return fmt.Sprintf("%s-%s-%s", t, source, group)
}
