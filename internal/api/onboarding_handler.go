package api

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/modelvendor"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
)

// OnboardingMetaResponse 申请表单元数据
type OnboardingMetaResponse struct {
	ServiceTypes            []string                                    `json:"service_types"`
	SponsorLevels           []SponsorLevelInfo                          `json:"sponsor_levels"`
	ChannelTypes            []ChannelTypeInfo                           `json:"channel_types"`
	ChannelSourcesByService map[string][]onboarding.ChannelSourceOption `json:"channel_sources_by_service"`
	// ChannelTypeAllowedCategories: 通道类型(O/R/M) → 允许的来源 Category 列表，
	// 供前端按已选类型过滤来源下拉，与后端 validateChannelTypeSource 同源。
	ChannelTypeAllowedCategories map[string][]string  `json:"channel_type_allowed_categories"`
	ChannelGroupRule             ChannelGroupRule     `json:"channel_group_rule"`
	TestTypes                    []OnboardingTestType `json:"test_types"`
	ContactInfo                  string               `json:"contact_info"`
	// ModelVendors: 模型厂商受控词表，供前端展示厂商名与图标。真相源在后端
	// （internal/modelvendor），前端不自建一份，避免两边漂移。
	// 注意自助收录表单**不含**该字段——行级 vendor 只对管理员开放，与 request_model 同口径，
	// 避免用户自填 anthropic/openai 蹭官方印象。
	ModelVendors []modelvendor.Vendor `json:"model_vendors"`
}

// ChannelGroupRule 下发给前端的 channel_group 同步校验规则。
type ChannelGroupRule struct {
	Pattern   string `json:"pattern"`
	Default   string `json:"default"`
	MaxLength int    `json:"max_length"`
}

// OnboardingTestType 收录测试类型信息
type OnboardingTestType struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	Description    string              `json:"description"`
	DefaultVariant string              `json:"default_variant"`
	Variants       []OnboardingVariant `json:"variants"`
}

// OnboardingVariant 收录测试变体信息
type OnboardingVariant struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
}

// SponsorLevelInfo 赞助等级信息
type SponsorLevelInfo struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ChannelTypeInfo 通道类型信息
type ChannelTypeInfo struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// GetOnboardingMeta 获取申请表单元数据
// GET /api/onboarding/meta
func (h *Handler) GetOnboardingMeta(c *gin.Context) {
	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	h.cfgMu.RLock()
	contactInfo := h.config.Onboarding.ContactInfo
	h.cfgMu.RUnlock()

	// 获取可用测试类型（从 probe 注册表）
	var testTypes []OnboardingTestType
	for _, t := range probe.ListTestTypes() {
		var variants []OnboardingVariant
		for _, v := range t.Variants {
			if v != nil {
				variants = append(variants, OnboardingVariant{ID: v.ID, Order: v.Order})
			}
		}
		testTypes = append(testTypes, OnboardingTestType{
			ID:             t.ID,
			Name:           t.Name,
			Description:    t.Description,
			DefaultVariant: t.DefaultVariant,
			Variants:       variants,
		})
	}

	groupPattern, groupDefault, groupMaxLength := onboarding.ChannelGroupRule()
	resp := OnboardingMetaResponse{
		ServiceTypes: []string{"cc", "cx", "gm"},
		SponsorLevels: []SponsorLevelInfo{
			// public/signal 自 2026-04-17 停止自助受理，不再下发给前端
			{Value: "pulse", Label: "Pulse", Description: "脉冲链路"},
		},
		ChannelTypes: []ChannelTypeInfo{
			{Value: "O", Label: "官方通道"},
			{Value: "R", Label: "逆向通道"},
			{Value: "M", Label: "混合通道"},
		},
		ChannelSourcesByService:      onboarding.ChannelSourceOptionsByService(),
		ChannelTypeAllowedCategories: onboarding.ChannelTypeAllowedCategories(),
		ChannelGroupRule: ChannelGroupRule{
			Pattern:   groupPattern,
			Default:   groupDefault,
			MaxLength: groupMaxLength,
		},
		TestTypes:    testTypes,
		ContactInfo:  contactInfo,
		ModelVendors: modelvendor.Options(),
	}

	c.JSON(http.StatusOK, resp)
}

// SubmitOnboarding 提交收录申请
// POST /api/onboarding/submit
func (h *Handler) SubmitOnboarding(c *gin.Context) {
	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	// 与 /test 共用 token bucket：服务层的每日配额是"当天总量"闸，挡不住秒级突发，
	// 且一次成功探测本就只对应一次提交，这里按分钟维度先收住洪峰。
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return
	}

	var req onboarding.SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("onboarding", "提交参数校验失败", "error", err)
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效，请检查必填字段: "+err.Error())
		return
	}

	clientIP := c.ClientIP()
	resp, err := svc.Submit(c.Request.Context(), &req, clientIP)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// inlineTestRequest 内联探测请求体，由 /api/onboarding/test 与 /api/change/test 共用。
type inlineTestRequest struct {
	ServiceType  string `json:"service_type" binding:"required"`
	TemplateName string `json:"template_name" binding:"required"`
	BaseURL      string `json:"base_url" binding:"required"`
	APIKey       string `json:"api_key" binding:"required"`
}

// runInlineTestProbe 执行自助收录 / 变更请求共用的内联探测编排：
// 探测器就绪检查 → IP 限流 → 参数绑定 → SSRF 校验 → 构造 ServiceConfig →
// 与 loader 一致的 ResolveSingleMonitor（模板填充 + Duration 派生）→ 30s 超时探测。
// 调用方各自负责功能开关判断（onboarding / change service）与探测成功后的 proof 签发。
// 任一前置校验失败时已写入响应并返回 ok=false，调用方应直接返回。
func (h *Handler) runInlineTestProbe(c *gin.Context) (inlineTestRequest, *probe.Result, bool) {
	var req inlineTestRequest

	if h.inlineProber == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "内联探测器未初始化")
		return req, nil, false
	}

	// IP 限流
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return req, nil, false
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效: "+err.Error())
		return req, nil, false
	}

	// SSRF 前置校验
	guard := probe.NewSSRFGuard()
	if err := guard.ValidateURL(req.BaseURL); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "URL 安全校验失败: "+err.Error())
		return req, nil, false
	}

	// 走与 AdminPublish 同一映射器构造 ServiceConfig，再经 ResolveSingleMonitor 走与 loader 一致
	// 的解析路径（模板填充 + Duration 派生）。这样"用户自助测试"与"发布后调度真探测"用同一份
	// cfg 表达，规避"测试绿、上线红"的字段漂移。
	virtualSub := &onboarding.Submission{
		ServiceType:  req.ServiceType,
		TemplateName: req.TemplateName,
		BaseURL:      req.BaseURL,
		// PSC 在自助测试阶段只用于日志/审计，不参与 monitor_store 唯一性校验，故用占位值
		ChannelCode: "__test__",
	}
	cfg := onboarding.BuildServiceConfigFromSubmission(virtualSub, req.APIKey)

	appCfg := h.snapshotAppConfig()
	if appCfg == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "运行时配置未就绪")
		return req, nil, false
	}
	if err := config.ResolveSingleMonitor(appCfg, &cfg, h.configDir()); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "解析测试配置失败: "+err.Error())
		return req, nil, false
	}

	// 30 秒总超时
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	return req, h.inlineProber.ProbeConfig(ctx, cfg), true
}

// inlineTestProbeResponse 组装内联探测结果的公共响应字段（不含 proof，由各端点按需追加）。
func inlineTestProbeResponse(result *probe.Result) gin.H {
	return gin.H{
		"probe_status":     result.ProbeStatus,
		"sub_status":       result.SubStatus,
		"http_code":        result.HTTPCode,
		"latency":          result.Latency,
		"error_message":    result.ErrorMessage,
		"response_snippet": result.ResponseSnippet,
		"probe_id":         result.ProbeID,
	}
}

// OnboardingTest 收录内联探测测试
// POST /api/onboarding/test
func (h *Handler) OnboardingTest(c *gin.Context) {
	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	req, result, ok := h.runInlineTestProbe(c)
	if !ok {
		return
	}

	resp := inlineTestProbeResponse(result)

	// 探测成功时签发 proof，并下发其绝对过期时间（Unix 秒），
	// 让前端基于服务端真实 proof_ttl 做倒计时/提交前校验，而非硬编码。
	if result.ProbeStatus == 1 {
		proof, expiresAt := svc.IssueProofWithExpiry(result.ProbeID, req.ServiceType, req.BaseURL, req.APIKey)
		resp["test_proof"] = proof
		resp["proof_expires_at"] = expiresAt
	}

	c.JSON(http.StatusOK, resp)
}
