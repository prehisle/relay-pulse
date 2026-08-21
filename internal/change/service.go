package change

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"monitor/internal/apikey"
	"monitor/internal/config"
	"monitor/internal/displayname"
	"monitor/internal/logger"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
	"monitor/internal/urlutil"
)

// 需要测试的 proposed_changes 字段（顶层 new_api_key 单独处理）
var fieldsRequiringTest = map[string]bool{
	"base_url": true,
}

// ProofAudience 是变更请求流程签发/验证 proof 时的用途标识。
// 与 onboarding.ProofAudience 区分，理由见该常量注释（防跨流程 proof 重放）。
const ProofAudience = "change"

// allowedFields 是用户可自助变更的 proposed_changes 字段白名单。
// 刻意排除 category / sponsor_level：二者涉及商业分类与赞助权益，须人工对接；
// 如需调整通道字段，请通过通道管理（monitors.d/）直接编辑，变更请求采用只读审 diff 模型。
var allowedFields = map[string]bool{
	"provider_name": true,
	"provider_url":  true,
	"channel_name":  true,
	"base_url":      true,
}

// Service 变更请求核心业务逻辑。
type Service struct {
	store        Store
	cipher       *apikey.KeyCipher
	proofIssuer  *apikey.ProofIssuer
	authIndex    *AuthIndex
	monitorStore *config.MonitorStore
	cfg          *config.ChangeRequestConfig

	mu sync.RWMutex
}

// NewService 创建变更请求服务。
func NewService(
	store Store,
	cipher *apikey.KeyCipher,
	proofIssuer *apikey.ProofIssuer,
	cfg *config.ChangeRequestConfig,
) *Service {
	return &Service{
		store:       store,
		cipher:      cipher,
		proofIssuer: proofIssuer,
		authIndex:   NewAuthIndex(),
		cfg:         cfg,
	}
}

// SetMonitorStore 设置 monitors.d/ 存储。
func (s *Service) SetMonitorStore(ms *config.MonitorStore) {
	s.mu.Lock()
	s.monitorStore = ms
	s.mu.Unlock()
}

// UpdateConfig 热更新配置并重建认证索引。
func (s *Service) UpdateConfig(cfg *config.ChangeRequestConfig, monitors []config.ServiceConfig) {
	s.mu.Lock()
	s.cfg = cfg
	ms := s.monitorStore
	s.mu.Unlock()

	s.authIndex.Rebuild(monitors, s.cipher, ms, cfg.RevokedKeySHA256)
}

// === 用户端 API ===

// RevokedAPIKeyError 表示凭据命中「已公开泄露 API Key」拒绝名单。
//
// 单独成类型而非普通 error，是为了让 API 层能映射出对合法中转商有意义的提示
// （"该 key 已因公开泄露被停用，请联系我们更换"），而不是复用"查不到通道"的统一文案——
// 后者会让被动受害的中转商完全无从判断发生了什么。
type RevokedAPIKeyError struct{}

func (*RevokedAPIKeyError) Error() string {
	return "该 API Key 已因公开泄露被停用，请联系我们更换后再提交变更"
}

// AuthRequest 认证请求
type AuthRequest struct {
	APIKey string `json:"api_key" binding:"required,min=10,max=500"`
}

// AuthResponse 认证响应
type AuthResponse struct {
	Candidates []AuthCandidate `json:"candidates"`
}

// Auth 验证 API Key 并返回匹配的通道列表。
func (s *Service) Auth(apiKey string) (*AuthResponse, error) {
	candidates, revoked := s.authIndex.Lookup(apiKey, s.cipher)
	if revoked {
		return nil, &RevokedAPIKeyError{}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("API Key 无法匹配任何已收录通道")
	}
	return &AuthResponse{Candidates: candidates}, nil
}

// SubmitRequest 提交变更请求
type SubmitRequest struct {
	APIKey            string            `json:"api_key" binding:"required,min=10,max=500"`
	TargetKey         string            `json:"target_key" binding:"required,max=200"`
	ProposedChanges   map[string]string `json:"proposed_changes" binding:"required"`
	NewAPIKey         string            `json:"new_api_key,omitempty"`
	TestProof         string            `json:"test_proof,omitempty"`
	TestJobID         string            `json:"test_job_id,omitempty"`
	TestType          string            `json:"test_type,omitempty"`
	TestVariant       string            `json:"test_variant,omitempty"`
	TestAPIURL        string            `json:"test_api_url,omitempty"`
	TestLatency       int               `json:"test_latency,omitempty"`
	TestHTTPCode      int               `json:"test_http_code,omitempty"`
	Locale            string            `json:"locale,omitempty"`
	AgreementAccepted bool              `json:"agreement_accepted"`
}

// SubmitResponse 提交响应
type SubmitResponse struct {
	PublicID string `json:"public_id"`
}

// Submit 处理用户提交变更请求。
func (s *Service) Submit(ctx context.Context, req *SubmitRequest, clientIP string) (*SubmitResponse, error) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	// IP 限流
	ipHash := hashIP(clientIP)
	count, err := s.store.CountByIPToday(ctx, ipHash)
	if err != nil {
		return nil, fmt.Errorf("查询提交限额失败: %w", err)
	}
	if count >= cfg.MaxPerIPPerDay {
		return nil, fmt.Errorf("今日提交次数已达上限（%d/%d）", count, cfg.MaxPerIPPerDay)
	}

	// 验证 API Key 匹配目标通道
	candidates, revoked := s.authIndex.Lookup(req.APIKey, s.cipher)
	if revoked {
		return nil, &RevokedAPIKeyError{}
	}
	var target *AuthCandidate
	for i, c := range candidates {
		if c.MonitorKey == req.TargetKey {
			target = &candidates[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("API Key 与目标通道不匹配")
	}

	// 校验变更字段
	for field := range req.ProposedChanges {
		if !allowedFields[field] {
			return nil, fmt.Errorf("字段 %q 不允许自助变更", field)
		}
	}

	// 展示名安全校验（与 onboarding 同一叶子包 internal/displayname）：拒不可见字符欺骗，并把
	// **规范值写回** proposed_changes——使管理员只读 diff / Apply / 落盘看到同一干净字符串（否则
	// 首尾隐藏字符仍会进入只读 diff，Apply 再规范化已太晚）。provider_name 必填、channel_name 可空。
	for field, value := range req.ProposedChanges {
		switch field {
		case "provider_name":
			canonical, err := displayname.ValidateProviderName(value)
			if err != nil {
				return nil, err
			}
			req.ProposedChanges[field] = canonical
		case "channel_name":
			canonical, err := displayname.ValidateChannelName(value)
			if err != nil {
				return nil, err
			}
			req.ProposedChanges[field] = canonical
		}
	}

	// 校验 provider_url：键存在即视为变更，必须是 http/https 且有 hostname。
	//
	// 此前该字段全程无校验，javascript:/data: 之类的伪协议可一路存进待审队列并被 Apply
	// 写进 monitors.d/——虽然完整配置校验会在热加载时拒掉整份配置（公开看板因而不受影响），
	// 但那意味着「文件已落盘、DB 已标 applied、运行态却没变」，且下次重启直接起不来。
	// 在入口拒掉，比留给下游兜底更早、也更容易理解。
	if providerURL, ok := req.ProposedChanges["provider_url"]; ok {
		if err := validateHTTPURL(providerURL); err != nil {
			return nil, fmt.Errorf("provider_url 无效: %w", err)
		}
	}

	// 校验新 base_url 合法性：键存在即视为变更，必须 HTTPS + 有效 hostname——空串亦拒（否则会把通道
	// 地址抹空，且旁路下方 base_url↔test_api_url 的 host/port 一致性校验）。
	if newBaseURL, ok := req.ProposedChanges["base_url"]; ok {
		parsedNew, err := url.Parse(newBaseURL)
		if err != nil || parsedNew.Hostname() == "" || parsedNew.Scheme != "https" {
			return nil, fmt.Errorf("base_url 必须使用 HTTPS 协议且包含有效 hostname")
		}
	}

	// 判断是否需要测试
	requiresTest := false
	for field := range req.ProposedChanges {
		if fieldsRequiringTest[field] {
			requiresTest = true
			break
		}
	}
	// 新 API Key 也需要测试
	if req.NewAPIKey != "" {
		requiresTest = true

		// 轮换目标不能又是一把已公开泄露的 key——否则"换 key"等于原地踏步，
		// 且会把一把公开可用的凭据重新写回监测配置。
		if s.authIndex.IsRevokedKey(req.NewAPIKey) {
			return nil, fmt.Errorf("新 API Key 命中已公开泄露名单，请改用一把全新的 key: %w", &RevokedAPIKeyError{})
		}
	}

	// 变更触及监测相关字段（base_url / API Key）时，须重新确认「禁止监测作弊」条款。
	// fail-closed，早于 proof 校验；非 requiresTest 的展示类变更不要求、也不盖戳。
	if requiresTest && !req.AgreementAccepted {
		return nil, fmt.Errorf("变更监测相关字段（base_url/API Key）需先确认禁止监测作弊条款")
	}

	// 如果需要测试，验证 proof
	if requiresTest {
		if req.TestProof == "" || req.TestJobID == "" {
			return nil, fmt.Errorf("变更监测相关字段（base_url/new_api_key）需要先通过测试")
		}

		// 校验 test_api_url 与目标 base_url 的 host 一致，防止复用其他地址的测试结果
		targetBaseURL := target.BaseURL
		if newBaseURL, ok := req.ProposedChanges["base_url"]; ok {
			targetBaseURL = newBaseURL
		}
		if targetBaseURL != "" {
			parsedBaseURL, err := url.Parse(targetBaseURL)
			if err != nil || parsedBaseURL.Hostname() == "" {
				return nil, fmt.Errorf("base_url 无效")
			}
			parsedTestURL, err := url.Parse(req.TestAPIURL)
			if err != nil || parsedTestURL.Hostname() == "" {
				return nil, fmt.Errorf("test_api_url 无效")
			}
			// 与收录侧同一条：路径也要一致，否则可以拿同 host 的另一条线路（按路径区分）
			// 测出 proof 再把 base_url 换掉。
			if !urlutil.SameEndpoint(parsedBaseURL, parsedTestURL) {
				return nil, fmt.Errorf("base_url 与 test_api_url 必须完全一致（协议、host/port 与路径）")
			}
		}

		// 计算用于 proof 的 API Key 指纹（使用新 key 或原 key）
		proofKey := req.APIKey
		if req.NewAPIKey != "" {
			proofKey = req.NewAPIKey
		}
		proofFingerprint := s.cipher.Fingerprint(proofKey)

		// proof 还绑了**探针模板**与**目标通道**：
		//   - 不绑模板 → 可以用最便宜的模型测通、提交时报另一个模型，而变更要证明的正是
		//     「这条通道照旧能用」；前端切模板会清测试状态，但那是 UX，不是安全边界。
		//   - 不绑目标通道 → 一把 key 常同时认证多条通道，可以「测 A、改 B」。
		// 模板名两侧都按注册表规范化（canonicalTestVariant），避免「签发时用解析后的 ID、
		// 校验时用请求原文」这种自造的恒拒。
		if err := s.proofIssuer.Verify(req.TestProof, apikey.ProofClaims{
			JobID:          req.TestJobID,
			TestType:       req.TestType,
			APIURL:         req.TestAPIURL,
			KeyFingerprint: proofFingerprint,
			Variant:        CanonicalTestVariant(target.Service, req.TestVariant),
			MonitorKey:     target.MonitorKey,
		}); err != nil {
			return nil, fmt.Errorf("测试证明无效: %w", err)
		}
	}

	// 构建当前快照
	snapshot := map[string]string{
		"provider_name": target.ProviderName,
		"provider_url":  target.ProviderURL,
		"channel_name":  target.ChannelName,
		"category":      target.Category,
		"sponsor_level": target.SponsorLevel,
		"listed_since":  target.ListedSince,
		"expires_at":    target.ExpiresAt,
		"price_min":     target.PriceMin,
		"price_max":     target.PriceMax,
		"base_url":      target.BaseURL,
	}
	snapshotJSON, _ := json.Marshal(snapshot)
	changesJSON, _ := json.Marshal(req.ProposedChanges)

	now := time.Now().Unix()
	cr := &ChangeRequest{
		PublicID:        uuid.New().String(),
		Status:          StatusPending,
		TargetProvider:  target.Provider,
		TargetService:   target.Service,
		TargetChannel:   target.Channel,
		TargetKey:       target.MonitorKey,
		ApplyMode:       target.ApplyMode,
		AuthFingerprint: s.cipher.Fingerprint(req.APIKey),
		AuthLast4:       apikey.Last4(req.APIKey),
		CurrentSnapshot: string(snapshotJSON),
		ProposedChanges: string(changesJSON),
		RequiresTest:    requiresTest,
		SubmitterIPHash: ipHash,
		Locale:          req.Locale,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	// 测试结果
	if requiresTest {
		cr.TestType = req.TestType
		cr.TestVariant = req.TestVariant
		cr.TestJobID = req.TestJobID
		cr.TestPassedAt = now
		cr.TestLatency = req.TestLatency
		cr.TestHTTPCode = req.TestHTTPCode

		// 反作弊 re-attestation 盖戳（版本/时间由后端定，不信客户端；此分支已保证 AgreementAccepted==true）
		cr.AgreementAccepted = true
		acceptedAt := now
		cr.AgreementAcceptedAt = &acceptedAt
		cr.AgreementVersion = onboarding.AgreementVersion
	}

	// 加密新 API Key（如有）
	if req.NewAPIKey != "" {
		encrypted, err := s.cipher.Encrypt(req.NewAPIKey)
		if err != nil {
			return nil, fmt.Errorf("加密新 API Key 失败: %w", err)
		}
		cr.NewKeyEncrypted = encrypted
		cr.NewKeyFingerprint = s.cipher.Fingerprint(req.NewAPIKey)
		cr.NewKeyLast4 = apikey.Last4(req.NewAPIKey)
	}

	// proof 一次性消费（理由同 onboarding.Submit）。纯展示类变更不涉及探测、也不落 test_job_id，
	// 故不在此消费——否则客户端随手附带的任意 job_id 都会被无谓烧掉。
	if requiresTest {
		consumed, err := s.store.ConsumeTestJobID(ctx, req.TestJobID)
		if err != nil {
			return nil, fmt.Errorf("记录测试证明消费状态失败: %w", err)
		}
		if !consumed {
			return nil, fmt.Errorf("该测试结果已被使用，请重新测试后再提交")
		}
	}

	if err := s.store.Save(ctx, cr); err != nil {
		return nil, err
	}

	logger.Info("change", "变更请求已提交",
		"public_id", cr.PublicID,
		"target", cr.TargetKey,
		"apply_mode", cr.ApplyMode,
		"requires_test", cr.RequiresTest)

	return &SubmitResponse{PublicID: cr.PublicID}, nil
}

// GetStatus 查询变更请求状态（用户端）
func (s *Service) GetStatus(ctx context.Context, publicID string) (*ChangeRequest, error) {
	return s.store.GetByPublicID(ctx, publicID)
}

// IssueProof 签发测试证明（供内联探测调用）。
func (s *Service) IssueProof(claims apikey.ProofClaims, apiKey string) string {
	proof, _ := s.IssueProofWithExpiry(claims, apiKey)
	return proof
}

// CanonicalTestVariant 把请求里的模板名归一成注册表里的变体 ID：空值回退该 service 的默认
// 变体（与探测端点 ResolveVariant 同语义），查不到就退回剥空白的原文。
//
// 签发与校验两侧必须用同一个函数：探测端点绑的是解析后的变体 ID，而提交请求带的是原文
// （前端在默认变体的情况下甚至可能不带），两侧各自处理必然对不上、把合法提交变成恒拒。
func CanonicalTestVariant(service, variant string) string {
	if tt, ok := probe.GetTestType(service); ok {
		if v, err := tt.ResolveVariant(variant); err == nil && v != nil {
			return v.ID
		}
	}
	return strings.TrimSpace(variant)
}

// IssueProofWithExpiry 签发测试证明，并返回其绝对过期时间（Unix 秒），供 API 层下发前端。
// 与 onboarding.Service.IssueProofWithExpiry 同口径：change.Service 持有独立但
// 同源（共享 onboarding.proof_secret）的 proofIssuer，故 /api/change/test 不依赖
// onboarding 是否启用，签发的 proof 也能被 change.Submit 的同一 proofIssuer 验证。
func (s *Service) IssueProofWithExpiry(claims apikey.ProofClaims, apiKey string) (string, int64) {
	claims.KeyFingerprint = s.cipher.Fingerprint(apiKey)
	return s.proofIssuer.IssueWithExpiry(claims)
}

// === 管理端 API ===

// fillLiveCurrent 给变更请求填充通道的实时当前值（仅 proposed 涉及字段）。
// 永不返回错误：manual/已删/读失败时仅置 LiveCurrentSource，LiveCurrent 留空，
// 前端据此回退展示 current_snapshot，绝不让列表因实时读失败而整体不可用。
func (s *Service) fillLiveCurrent(cr *ChangeRequest) {
	s.mu.RLock()
	ms := s.monitorStore
	s.mu.RUnlock()

	// 先判 manual：manual 通道本就不在 monitors.d/，与 ms 是否就绪无关，
	// 不应在 ms==nil 时被误标 error。
	if cr.ApplyMode != "auto" {
		cr.LiveCurrentSource = "manual"
		return
	}
	if ms == nil {
		cr.LiveCurrentSource = "error"
		return
	}
	mf, err := ms.Get(cr.TargetKey)
	if err != nil {
		cr.LiveCurrentSource = "error"
		return
	}
	if mf == nil {
		cr.LiveCurrentSource = "deleted"
		return
	}
	root := config.RootMonitor(mf)
	if root == nil {
		cr.LiveCurrentSource = "deleted"
		return
	}
	var changes map[string]string
	if err := json.Unmarshal([]byte(cr.ProposedChanges), &changes); err != nil {
		cr.LiveCurrentSource = "error"
		return
	}
	live := make(map[string]string, len(changes))
	for field := range changes {
		live[field] = currentFieldValue(root, field)
	}
	cr.LiveCurrent = live
	cr.LiveCurrentSource = "auto"
}

// currentFieldValue 读取通道某字段的当前字符串值（覆盖全部可能出现在 proposed_changes 的字段，
// 含历史脏数据写入的商业字段，保证显示健壮）。
func currentFieldValue(m *config.ServiceConfig, field string) string {
	switch field {
	case "provider_name":
		return m.ProviderName
	case "provider_url":
		return m.ProviderURL
	case "channel_name":
		return m.ChannelName
	case "base_url":
		return m.BaseURL
	case "category":
		return m.Category
	case "sponsor_level":
		return string(m.SponsorLevel)
	case "listed_since":
		return m.ListedSince
	case "expires_at":
		return m.ExpiresAt
	case "price_min":
		if m.PriceMin != nil {
			return strconv.FormatFloat(*m.PriceMin, 'f', -1, 64)
		}
		return ""
	case "price_max":
		if m.PriceMax != nil {
			return strconv.FormatFloat(*m.PriceMax, 'f', -1, 64)
		}
		return ""
	default:
		return ""
	}
}

// AdminList 管理员列表查询
func (s *Service) AdminList(ctx context.Context, status string, limit, offset int) ([]*ChangeRequest, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	list, total, err := s.store.List(ctx, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	for _, cr := range list {
		s.fillLiveCurrent(cr)
	}
	return list, total, nil
}

// AdminGetDetail 管理员获取详情（含解密新 API Key）
func (s *Service) AdminGetDetail(ctx context.Context, publicID string) (*ChangeRequest, string, error) {
	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil || cr == nil {
		return cr, "", err
	}

	var newKey string
	if cr.NewKeyEncrypted != "" {
		newKey, err = s.cipher.Decrypt(cr.NewKeyEncrypted)
		if err != nil {
			return cr, "", fmt.Errorf("解密新 API Key 失败: %w", err)
		}
	}

	s.fillLiveCurrent(cr)
	return cr, newKey, nil
}

// AdminUpdate 管理员更新变更请求的审核备注（admin_note）。
// 变更请求采用「只读审 diff」：proposed_changes 不再经此端点编辑——要调整通道字段请到
// 通道管理（monitors.d/）。传入非 admin_note 字段一律 fail-loud 拒绝，根除覆盖入口。
func (s *Service) AdminUpdate(ctx context.Context, publicID string, updates map[string]any) (*ChangeRequest, error) {
	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if cr == nil {
		return nil, fmt.Errorf("变更请求不存在")
	}
	if cr.Status == StatusApplied {
		return nil, fmt.Errorf("已应用的请求不能更新")
	}

	for field := range updates {
		if field != "admin_note" {
			return nil, fmt.Errorf("字段 %q 不可经此端点修改（变更请求只读；通道字段请在通道管理修改）", field)
		}
	}
	// 无可更新字段：直接返回，不空写库、不动 UpdatedAt。
	if len(updates) == 0 {
		return cr, nil
	}

	noteRaw, exists := updates["admin_note"]
	if exists {
		v, ok := noteRaw.(string)
		if !ok {
			return nil, fmt.Errorf("字段 \"admin_note\" 的值必须是字符串")
		}
		cr.AdminNote = v // 空串视为清空备注，合法。
	}
	cr.UpdatedAt = time.Now().Unix()

	if err := s.store.Update(ctx, cr); err != nil {
		return nil, err
	}
	logger.Info("change", "管理员更新审核备注", "public_id", publicID)
	return cr, nil
}

// AdminApprove 批准变更请求
func (s *Service) AdminApprove(ctx context.Context, publicID, note string) error {
	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if cr == nil {
		return fmt.Errorf("变更请求不存在")
	}
	if cr.Status != StatusPending {
		return fmt.Errorf("只有待审核的请求可以批准，当前状态: %s", cr.Status)
	}
	if cr.RequiresTest && !cr.AgreementAccepted {
		return fmt.Errorf("该变更涉及监测字段（base_url/API Key）但未确认禁止监测作弊条款，不能批准；请驳回并要求重新提交")
	}
	if err := s.ensureRequestKeysNotRevoked(cr); err != nil {
		return err
	}

	now := time.Now().Unix()
	cr.Status = StatusApproved
	cr.AdminNote = note
	cr.ReviewedAt = &now
	cr.UpdatedAt = now
	return s.store.Update(ctx, cr)
}

// AdminReject 驳回变更请求
func (s *Service) AdminReject(ctx context.Context, publicID, note string) error {
	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if cr == nil {
		return fmt.Errorf("变更请求不存在")
	}
	if cr.Status == StatusApplied {
		return fmt.Errorf("已应用的请求不能驳回")
	}

	now := time.Now().Unix()
	cr.Status = StatusRejected
	cr.AdminNote = note
	cr.ReviewedAt = &now
	cr.UpdatedAt = now
	return s.store.Update(ctx, cr)
}

// ensureRequestKeysNotRevoked 拦下「涉及已公开泄露 API Key」的历史请求。
//
// 拒绝名单只在 Submit 入口生效，对名单生效**之前**已落库的 pending/approved 请求不追溯；
// 若不在 admin 侧补同一道闸，攻击者在窗口期塞进来的 payload 仍可能被批准/应用。
// 与 v2.69.0 反作弊条款的 admin 闸是同一类后门，处置方式也一致：只能驳回，不能批准或应用。
//
// 查两处：
//  1. **认证指纹**——已落库请求只存 HMAC 指纹、无明文，故只能比对 Rebuild 派生的
//     「名单 ∩ 当前配置在用 key」的 HMAC 集合。**这不是完备覆盖**，两种情况会漏检：
//     (a) 某把泄露 key 此后已被轮换出配置或整条 monitor 被删 —— 无法从 SHA-256 名单反推其
//     HMAC 指纹；(b) `onboarding.encryption_key` 轮换过 —— 历史请求的指纹是用**旧**密钥算的，
//     而 Rebuild 用新密钥派生，两边对不上（哪怕那把 key 仍在配置里）。
//     名单上线时须另行人工冻结/驳回存量队列来补这个缺口（见 docs/user/config.md）。
//  2. **请求携带的新 key**——密文可解，故这一侧是精确判定：既拦「提交时新 key 就在名单里但早于
//     本闸落库」的请求，也拦「新 key 事后才进名单」的请求。放在 Approve 与 Apply 两处而非只在
//     Apply，是因为 manual 模式的请求根本不走 Apply。
func (s *Service) ensureRequestKeysNotRevoked(cr *ChangeRequest) error {
	if s.authIndex.IsRevokedAuthFingerprint(cr.AuthFingerprint) {
		return fmt.Errorf("该请求的认证 API Key 已因公开泄露被撤销，只能驳回: %w", &RevokedAPIKeyError{})
	}

	if cr.NewKeyEncrypted == "" {
		return nil
	}
	newKey, err := s.cipher.Decrypt(cr.NewKeyEncrypted)
	if err != nil {
		// 解不开就无法判定，fail-closed：宁可让管理员驳回重提，也不把无法校验的凭据写进配置。
		return fmt.Errorf("无法解密该请求携带的新 API Key，不能批准或应用: %w", err)
	}
	if s.authIndex.IsRevokedKey(newKey) {
		return fmt.Errorf("该请求要换成的新 API Key 命中已公开泄露名单，只能驳回: %w", &RevokedAPIKeyError{})
	}
	return nil
}

// AdminApply 应用变更到 monitors.d/（仅 auto 模式）。
func (s *Service) AdminApply(ctx context.Context, publicID string) error {
	s.mu.Lock()
	ms := s.monitorStore
	s.mu.Unlock()

	if ms == nil {
		return fmt.Errorf("MonitorStore 未初始化")
	}

	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if cr == nil {
		return fmt.Errorf("变更请求不存在")
	}
	if cr.Status != StatusPending && cr.Status != StatusApproved {
		return fmt.Errorf("只有待审核或已批准的请求可以应用，当前状态: %s", cr.Status)
	}
	if cr.RequiresTest && !cr.AgreementAccepted {
		return fmt.Errorf("该变更涉及监测字段（base_url/API Key）但未确认禁止监测作弊条款，不能应用；请驳回并要求重新提交")
	}
	if err := s.ensureRequestKeysNotRevoked(cr); err != nil {
		return err
	}
	if cr.ApplyMode != "auto" {
		return fmt.Errorf("该通道为 manual 模式，不能自动应用（通道不在 monitors.d/ 中）")
	}

	// 读取当前 monitor 配置
	mf, err := ms.Get(cr.TargetKey)
	if err != nil {
		return fmt.Errorf("读取通道配置失败: %w", err)
	}
	if mf == nil {
		return fmt.Errorf("通道已不存在（可能已被归档/删除），无法应用变更")
	}
	m := config.RootMonitor(mf)
	if m == nil {
		return fmt.Errorf("通道配置为空")
	}

	// 解析变更
	var changes map[string]string
	if err := json.Unmarshal([]byte(cr.ProposedChanges), &changes); err != nil {
		return fmt.Errorf("解析变更内容失败: %w", err)
	}

	// 应用变更到 ServiceConfig
	for field, value := range changes {
		switch field {
		case "provider_name":
			// 防御闸：即便 Submit 已校验，历史脏数据 / 直改 DB 仍可能带入不可见字符——
			// 此处再校验并取规范值，fail-closed 早于循环后的 AtomicWrite。
			canonical, err := displayname.ValidateProviderName(value)
			if err != nil {
				return fmt.Errorf("变更内容的服务商展示名非法: %w", err)
			}
			m.ProviderName = canonical
		case "provider_url":
			// 防御闸：与 provider_name/channel_name/base_url 对称——即便 Submit 已校验，
			// 历史脏数据 / 直改 DB 仍可能带入伪协议，此处 fail-closed 早于 AtomicWrite，
			// 免得把整份配置写坏（热加载会整体拒绝，重启则直接起不来）。
			if err := validateHTTPURL(value); err != nil {
				return fmt.Errorf("变更内容的 provider_url 非法: %w", err)
			}
			m.ProviderURL = value
		case "channel_name":
			canonical, err := displayname.ValidateChannelName(value)
			if err != nil {
				return fmt.Errorf("变更内容的通道展示名非法: %w", err)
			}
			m.ChannelName = canonical
		case "category":
			m.Category = value
		case "sponsor_level":
			m.SponsorLevel = config.SponsorLevel(value)
		case "listed_since":
			m.ListedSince = value
		case "expires_at":
			m.ExpiresAt = value
		case "price_min":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				m.PriceMin = &f
			}
		case "price_max":
			if f, err := strconv.ParseFloat(value, 64); err == nil {
				m.PriceMax = &f
			}
		case "base_url":
			// 防御闸：与 provider_name/channel_name 对称——即便 Submit 已校验，历史脏数据 / 直改 DB
			// 仍可能带入空或非 HTTPS 的 base_url，此处 fail-closed 早于循环后的 AtomicWrite。
			parsed, err := url.Parse(value)
			if err != nil || parsed.Hostname() == "" || parsed.Scheme != "https" {
				return fmt.Errorf("变更内容的 base_url 非法（须 HTTPS + 有效 hostname）")
			}
			m.BaseURL = value
		}
	}

	// 新 API Key
	if cr.NewKeyEncrypted != "" {
		newKey, err := s.cipher.Decrypt(cr.NewKeyEncrypted)
		if err != nil {
			return fmt.Errorf("解密新 API Key 失败: %w", err)
		}
		m.APIKey = newKey
	}

	// 更新 metadata
	oldRevision := mf.Metadata.Revision
	mf.Metadata.Revision++
	mf.Metadata.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	// 故意先写 monitors.d/ 文件、再标记 DB applied：文件写失败时 CR 仍是 pending/approved、可重试；
	// DB 更新失败时配置已生效（符合变更意图）但请求保持 pending/approved + loud error，管理员可再次
	// Apply 自愈（幂等）。反过来（DB 先）会造成「DB 已 applied 但配置未生效且不可重试」的更差状态。
	if err := ms.Update(cr.TargetKey, mf, oldRevision); err != nil {
		return fmt.Errorf("写入 monitors.d/ 失败: %w", err)
	}

	// 更新 DB 状态
	now := time.Now().Unix()
	cr.Status = StatusApplied
	cr.AppliedAt = &now
	cr.UpdatedAt = now
	if err := s.store.Update(ctx, cr); err != nil {
		logger.Error("change", "更新请求状态失败（文件已写入）",
			"public_id", publicID, "error", err)
		return fmt.Errorf("已写入配置文件但更新数据库状态失败: %w", err)
	}

	logger.Info("change", "变更已应用",
		"public_id", publicID,
		"target", cr.TargetKey)

	return nil
}

// AdminDelete 删除变更请求
func (s *Service) AdminDelete(ctx context.Context, publicID string) error {
	cr, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if cr == nil {
		return fmt.Errorf("变更请求不存在")
	}
	return s.store.DeleteByPublicID(ctx, publicID)
}

// validateHTTPURL 校验用于展示的外链：必须是 http/https 且带 hostname。
//
// 与 config 层加载期校验（config.validateURL）同判据，在此提前一道，
// 使非法值止步于提交入口，而不是等到写盘后由热加载整份拒绝。
func validateHTTPURL(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return fmt.Errorf("不能为空")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("格式无效: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("只允许 http/https 协议，当前为 %q", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("缺少有效 hostname")
	}
	return nil
}

// hashIP 计算 IP 地址的 SHA256 哈希
func hashIP(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:])
}
