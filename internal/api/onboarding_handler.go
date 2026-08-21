package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/apikey"
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
	// ModelVendors: 模型厂商受控词表，供前端展示厂商名与图标、以及自助表单里的厂商下拉。
	// 真相源在后端（internal/modelvendor），前端不自建一份，避免两边漂移。
	//
	// 自助表单**可以**填厂商（站长 2026-08-21 拍板，推翻此前「只对管理员开放」的策略）：
	// 撒谎的杀伤面有界——只有第一方厂商模型那条道需要填，且发布必过管理员审核，
	// rpdiag 的质量盲评是最终兜底。
	ModelVendors []modelvendor.Vendor `json:"model_vendors"`

	// ModelsByService: 「选厂商 → 选该厂商的模型」下拉的数据源，取代原先直接暴露模板名的做法。
	ModelsByService map[string][]OnboardingModelOption `json:"models_by_service"`
	// RequestShapesByService: 自填模型 ID 时可选的请求形态（native 模板的人话名）。
	RequestShapesByService map[string][]OnboardingRequestShape `json:"request_shapes_by_service"`
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

	// 获取可用测试类型（从 probe 注册表）。
	// 只下发自助可见的变体：内部/特化模板（历史冻结版本、单通道定制、中转商专用指纹版）
	// 不进公开表单，与 SubmitOnboarding / OnboardingTest 的模板闸同一判据。
	var testTypes []OnboardingTestType
	for _, t := range probe.ListTestTypes() {
		var variants []OnboardingVariant
		for _, v := range t.Variants {
			if v != nil && v.SelfServeVisible {
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
		TestTypes:              testTypes,
		ContactInfo:            contactInfo,
		ModelVendors:           modelvendor.Options(),
		ModelsByService:        buildModelCatalog(),
		RequestShapesByService: buildRequestShapes(),
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

	// 模板归属与自助可见性只有注册表知道（service 层拿不到），故在这道公开入口就闸掉；
	// 模板 ↔ 模型的组合规则在 Submit 里，两边都跑得到。
	if _, err := resolveSelfServeTemplate(req.ServiceType, req.TemplateName); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
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
//
// 刻意**没有** model / request_model / model_vendor 字段：变更流程要探的模型由服务端按
// TargetKey 从运行时配置取（见 buildChangeTestConfig）。多余的 JSON 键会被 ShouldBindJSON
// 丢弃，故变更流程的客户端塞模型字段也进不来——这条不变量由结构体形状本身保证，不靠纪律。
//
// 收录流程要填第一方厂商模型，用的是下面的 onboardingTestRequest（本结构体 + 两个模型字段），
// **不是**给这里加字段：那会把「变更流程不信客户端模型」这条闸重新打开。
type inlineTestRequest struct {
	ServiceType  string `json:"service_type" binding:"required"`
	TemplateName string `json:"template_name" binding:"required"`
	BaseURL      string `json:"base_url" binding:"required"`
	APIKey       string `json:"api_key" binding:"required"`
	// TargetKey 是变更流程的目标通道（`provider--service--channel`），**仅变更流程使用**。
	// 不能标 binding:"required"：收录流程共用本结构体，那里根本没有已存在的通道可指。
	TargetKey string `json:"target_key"`
}

// onboardingTestRequest 是自助收录专用的内联探测请求体：共用字段 + 行级模型。
//
// Model/ModelVendor 只在选中「第一方厂商模型」（native 族模板）时有值，其余情况必须为空——
// 判定与提交、上架同一条（onboarding.ValidateModelSelection）。
type onboardingTestRequest struct {
	inlineTestRequest
	Model       string `json:"model"`
	ModelVendor string `json:"model_vendor"`
}

// inlineTestGuards 是两条公开内联探测端点共用的前置闸：探测器就绪 → IP 限流。
// 失败时已写入响应并返回 false，调用方应直接返回。
func (h *Handler) inlineTestGuards(c *gin.Context) bool {
	if h.inlineProber == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "内联探测器未初始化")
		return false
	}
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return false
	}
	return true
}

// validateProbeTargetURL 对用户给出的探测目标做 SSRF 前置校验。
func (h *Handler) validateProbeTargetURL(c *gin.Context, rawURL string) bool {
	guard := probe.NewSSRFGuard()
	if err := guard.ValidateURL(rawURL); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "URL 安全校验失败: "+err.Error())
		return false
	}
	return true
}

// resolveSelfServeTemplate 校验「这个模板名对这条 service 合法，且对自助流程可见」，
// 返回注册表里的变体元数据。
//
// 用注册表而不是「模板文件在不在」判定，堵的是两件事：跨 service 引用（cc 的提交里写
// gm-flash-arith，文件确实存在、但请求形态完全不对），以及把模板名当路径片段用。
// 可见性只挡公开自助流程——管理员改 template_name 上架内部模板是**有意保留**的逃生口
// （与 target_provider/AdminConfigJSON 同类），变更流程的候选也一律不受影响。
func resolveSelfServeTemplate(serviceType, templateName string) (probe.PayloadVariant, error) {
	variant, ok := probe.LookupVariant(serviceType, templateName)
	if !ok {
		return probe.PayloadVariant{}, fmt.Errorf("所选模型不可用（%q），请刷新页面后重新选择", templateName)
	}
	if !variant.SelfServeVisible {
		return probe.PayloadVariant{}, fmt.Errorf("所选模型不开放自助收录（%q），如需收录请联系运营", templateName)
	}
	return variant, nil
}

// bindOnboardingTestRequest 绑定并校验自助收录的内联探测请求。
// 任一前置校验失败时已写入响应并返回 ok=false，调用方应直接返回。
func (h *Handler) bindOnboardingTestRequest(c *gin.Context) (onboardingTestRequest, bool) {
	var req onboardingTestRequest

	if !h.inlineTestGuards(c) {
		return req, false
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效: "+err.Error())
		return req, false
	}
	if !h.validateProbeTargetURL(c, req.BaseURL) {
		return req, false
	}
	variant, err := resolveSelfServeTemplate(req.ServiceType, req.TemplateName)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return req, false
	}
	// 用注册表解析出的变体 ID 覆盖请求原文：模板名随后既要拼文件路径、又要进 proof 绑定，
	// 两处都必须是规范形式，否则「签发时用规范名、提交时用原文」会把合法提交变成恒拒。
	req.TemplateName = variant.ID

	// 模型/厂商与模板的组合校验与提交、上架同源：测试时就用规范化后的值去探，
	// 避免「测试用了原串、提交用了规范串」这类两侧不一致。
	model, vendor, err := onboarding.ValidateModelSelection(req.TemplateName, req.Model, req.ModelVendor)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return req, false
	}
	req.Model, req.ModelVendor = model, vendor

	return req, true
}

// bindInlineTestRequest 执行两条公开内联探测端点共用的请求前置检查：
// 探测器就绪 → IP 限流 → 参数绑定 → SSRF 校验。
//
// 与 runResolvedProbe 拆开是因为两条流程的 ServiceConfig **来源不同**：收录流程从用户提交
// 字段构造，变更流程必须从服务端运行时配置取模型元数据。原先合成一个函数时，共用的构造逻辑
// 只能取两者的交集，于是变更流程永远拿不到 model——native 模板的通道因此在 wire 上发出
// `"model": ""`，测试恒红、改 base_url / 轮换 key 全部卡死。
//
// 任一前置校验失败时已写入响应并返回 ok=false，调用方应直接返回。
func (h *Handler) bindInlineTestRequest(c *gin.Context) (inlineTestRequest, bool) {
	var req inlineTestRequest

	if !h.inlineTestGuards(c) {
		return req, false
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效: "+err.Error())
		return req, false
	}
	if !h.validateProbeTargetURL(c, req.BaseURL) {
		return req, false
	}

	return req, true
}

// resolvedProbeModel 返回一份已解析配置最终会发到上游的模型标识。
//
// 回退链与 monitor.InjectVariables 的 {{MODEL}} 逐字一致：request_model 优先，为空回退 model。
// 抽成一处是因为这条链已经有三份平行实现（wire 注入、内联测试闸、配置期告警），再抄第四份
// 迟早漂移——而它同时是「这次测的到底是哪个模型」的唯一答案，proof 绑定也要用它。
func resolvedProbeModel(cfg config.ServiceConfig) string {
	if v := strings.TrimSpace(cfg.RequestModel); v != "" {
		return v
	}
	return strings.TrimSpace(cfg.Model)
}

// runResolvedProbe 对调用方构造好的临时 ServiceConfig 执行与 loader 一致的解析
// （ResolveSingleMonitor：模板填充 + Duration 派生）并发起一次 30 秒上限的内联探测。
//
// 让调用方自己构造 cfg、这里只管解析与探测，是为了保证"自助测试"与"发布后调度真探测"用
// 同一份 cfg 表达，规避"测试绿、上线红"的字段漂移。
// 任一前置校验失败时已写入响应并返回 ok=false，调用方应直接返回。
func (h *Handler) runResolvedProbe(c *gin.Context, cfg config.ServiceConfig) (*probe.Result, bool) {
	appCfg := h.snapshotAppConfig()
	if appCfg == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "运行时配置未就绪")
		return nil, false
	}
	if err := config.ResolveSingleMonitor(appCfg, &cfg, h.configDir()); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "解析测试配置失败: "+err.Error())
		return nil, false
	}

	// 解析完还是拿不到模型就**停下**，不许带着 `"model": ""` 去打上游。
	//
	// 这道闸放在解析之后、探测之前，是让"空 model 探测"在结构上不可能发生：两条流程、
	// 无论模型来自模板还是监测行，最终都要过这里。native 族（cc-native-* / cx-native-*）
	// 刻意不声明模型、要求行级填，是唯一会走到这个分支的形状——漏填时上游返回的是没头没脑
	// 的 400/内容不符，排障要绕一大圈；直接告诉调用方"这个模板必须指定模型"要诚实得多。
	//
	if resolvedProbeModel(cfg) == "" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam,
			"测试配置缺少模型：所选模板未声明模型，需由监测行指定")
		return nil, false
	}

	// 30 秒总超时
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	return h.inlineProber.ProbeConfig(ctx, cfg), true
}

// buildOnboardingTestConfig 用用户提交的字段构造自助收录测试用的 ServiceConfig。
// 走与 AdminPublish 同一映射器，使自助测试与上架后的真实探测同源——包括行级模型：
// 测的必须是将来真上架的那个模型，否则「测通了才准提交」这条闸就名不副实。
func buildOnboardingTestConfig(req onboardingTestRequest) config.ServiceConfig {
	virtualSub := &onboarding.Submission{
		ServiceType:  req.ServiceType,
		TemplateName: req.TemplateName,
		Model:        req.Model,
		ModelVendor:  req.ModelVendor,
		BaseURL:      req.BaseURL,
		// PSC 在自助测试阶段只用于日志/审计，不参与 monitor_store 唯一性校验，故用占位值
		ChannelCode: "__test__",
	}
	return onboarding.BuildServiceConfigFromSubmission(virtualSub, req.APIKey)
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

	req, ok := h.bindOnboardingTestRequest(c)
	if !ok {
		return
	}

	result, ok := h.runResolvedProbe(c, buildOnboardingTestConfig(req))
	if !ok {
		return
	}

	resp := inlineTestProbeResponse(result)

	// 探测成功时签发 proof，并下发其绝对过期时间（Unix 秒），
	// 让前端基于服务端真实 proof_ttl 做倒计时/提交前校验，而非硬编码。
	if result.ProbeStatus == 1 {
		// 绑定里带上模板与行级模型：proof 证明的是「用这个形态、测这个模型，通了」，
		// 提交时任一项被换掉都要验签失败（详见 apikey.ProofClaims）。
		proof, expiresAt := svc.IssueProofWithExpiry(apikey.ProofClaims{
			JobID:    result.ProbeID,
			TestType: req.ServiceType,
			APIURL:   req.BaseURL,
			Variant:  req.TemplateName,
			Model:    req.Model,
		}, req.APIKey)
		resp["test_proof"] = proof
		resp["proof_expires_at"] = expiresAt
	}

	c.JSON(http.StatusOK, resp)
}
