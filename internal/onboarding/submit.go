package onboarding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"monitor/internal/apikey"
	"monitor/internal/displayname"
	"monitor/internal/logger"
	"monitor/internal/urlutil"
)

// allowedSelfServeSponsorLevels 是自助收录可提交的赞助等级白名单，与 /api/onboarding/meta
// 下发给前端的选项保持一致（当前只有 pulse；空值表示未指定，由管理员在发布前定夺）。
// 商务等级（beacon/backbone/core）须人工对接，不接受用户自选——它们会直接进入上架配置。
var allowedSelfServeSponsorLevels = map[string]bool{
	"":      true,
	"pulse": true,
}

// SubmitRequest 用户提交申请的请求参数
type SubmitRequest struct {
	ProviderName string `json:"provider_name" binding:"max=100"` // 服务商展示名（可中文）；binding:max 仅粗略上限，精校验/规范化在 displayname.ValidateProviderName
	// binding 用 http_url 而非 url：validator 的 url 标签只要求 scheme 非空，
	// javascript:alert(1) 这类伪协议能过——而该值会作为 provider_url 进入上架配置与前端外链
	WebsiteURL   string `json:"website_url" binding:"required,http_url,max=500"`
	Category     string `json:"category" binding:"required,oneof=commercial public"`
	ServiceType  string `json:"service_type" binding:"required,oneof=cc cx gm"`
	TemplateName string `json:"template_name" binding:"required,max=100"`
	// ⚠️ 刻意**没有** model / model_vendor 字段（2026-08-21 起）：模型与厂商由所选模板声明，
	// 提交方无从指定。留着字段就意味着「非 native 模板 + 仅 vendor 非空」这条组合能过
	// ValidateModelSelection（它不对非 native 校验 vendor），而行级 vendor 在 config > template
	// 回退链里优先于模板值——直连 API 就能把 GLM 通道标成 Anthropic。字段不存在则无从谈起。
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
	// 公开提交不接受行级模型/厂商（SubmitRequest 没有这两个字段），用空值过闸：校验的是
	// 「这个模板自己声明得起模型吗」。唯一失败形状是 native 族，它在 handler 的
	// resolveSelfServeTemplate 处已被可见性挡掉，这里是第二道 fail-closed。
	model, modelVendor, err := ValidateModelSelection(req.TemplateName, "", "")
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

	// 验证 test_api_url 与 base_url 指向同一接入点（共享 urlutil.SameEndpoint，与 change 流程
	// 同一真相源）：不仅 host/port，路径与查询串也要一致。只比 host/port 时，可以拿
	// https://edge/good 测出 proof、再提交 base_url=https://edge/other——中转商的多条线路
	// 恰恰常按路径区分，那等于绕开了「先证明这条线路可用」。
	parsedTestURL, err := url.Parse(req.TestAPIURL)
	if err != nil || parsedTestURL.Hostname() == "" {
		return nil, fmt.Errorf("test_api_url 无效")
	}
	if !urlutil.SameEndpoint(parsedBaseURL, parsedTestURL) {
		return nil, fmt.Errorf("base_url 与 test_api_url 必须完全一致（协议、host/port 与路径）")
	}

	// test_type 必须与本次提交的 service 一致。
	//
	// proof 绑的是**测试时**的 service，而校验侧用的是客户端提交的 test_type——两者不比一下，
	// 就得靠「模板必须属于所提交的 service」那条闸间接推出它们相等（handler 里的
	// resolveSelfServeTemplate + proof 绑定模板）。那条推理链成立，但一旦哪天模板闸挪了位置就会
	// 悄悄失效；这里一行显式比较，把它变成不依赖别处的局部事实。
	if strings.TrimSpace(req.TestType) != strings.TrimSpace(req.ServiceType) {
		return nil, fmt.Errorf("test_type 与 service_type 不一致，请重新测试后提交")
	}

	// 加密 API Key
	encrypted, err := s.cipher.Encrypt(req.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密 API Key 失败: %w", err)
	}
	fingerprint := s.cipher.Fingerprint(req.APIKey)
	last4 := Last4(req.APIKey)

	// 验证 test proof（绑定探测参数）。
	//
	// 模板在绑定内：不绑就能「用最便宜的模型测通、提交时换成贵的」，上架即恒红。
	// 模板名在 handler 已按注册表核过并规范化，与签发侧同一口径。
	// Model 恒为空——公开流程的模型由模板唯一决定，绑住模板即绑住模型（签发侧同样留空）。
	err = s.proofIssuer.Verify(req.TestProof, apikey.ProofClaims{
		JobID:          req.TestJobID,
		TestType:       req.TestType,
		APIURL:         req.TestAPIURL,
		KeyFingerprint: fingerprint,
		Variant:        strings.TrimSpace(req.TemplateName),
		Model:          model,
	})
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
		Model:             model,
		ModelVendor:       modelVendor,
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

// hashIP 计算 IP 地址的 SHA256 哈希
func hashIP(ip string) string {
	h := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(h[:])
}
