package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"monitor/internal/apikey"
	"monitor/internal/change"
	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/probe"
)

// AuthChange 验证 API Key 并返回匹配通道列表
// POST /api/change/auth
func (h *Handler) AuthChange(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	// IP 限流：这是匿名 pre-auth 端点，按 API Key 指纹查候选通道，
	// 不限流则可被高频枚举。复用公共探测 limiter（main.go 无条件初始化）。
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return
	}

	var req change.AuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	resp, err := svc.Auth(req.APIKey)
	if err != nil {
		// 已公开泄露的 key 单独给可行动提示：持有者往往是被动受害的中转商，
		// 混进"验证失败"的统一文案里会让他们完全无从判断发生了什么。
		if writeRevokedAPIKeyError(c, err) {
			return
		}
		// 其余情况保持统一错误文案，防止枚举
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "API Key 验证失败")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// buildChangeTestConfig 构造变更请求测试用的 ServiceConfig。
//
// **模型元数据一律从服务端运行时配置取，绝不信客户端**——这条别改回去：
//   - 变更测试发的 api_key 可能是**待轮换的新 key**（前端 `newApiKey || apiKey`），它还没进
//     AuthIndex，所以无法靠 api_key 反查通道；target_key 才是能定位已认证候选的通道身份。
//   - 变更要证明的是「这条通道照旧能用」。模型若由客户端指定，就能拿一个便宜模型测通、而
//     实际在跑的是另一个，测试与被测对象脱钩，proof 也就失去意义。
//
// 取的是**父通道行**：变更的认证候选本身只由父行构建（internal/change/index.go 跳过子行），
// 两处必须同口径，否则「测哪一层」会出现两种答案。
//
// base_url 与 api_key 仍用请求里变更后的值（那正是本次要验证的东西）；template 允许请求指定
// （用户可在测试步切变体排障），但必须是该 service 已注册的变体。
//
// 已知边界：这里按 PSC + 模板重新构造，不继承根行的行级覆盖（自定义 headers / timeout / proxy
// 等）。这与本函数抽出前的行为一致，不是本轮引入的回归；真要对齐得改用根行的 resolved cfg 再
// 覆盖，但那样切换模板时会带上旧模板已解析的 body，另有代价。
func (h *Handler) buildChangeTestConfig(c *gin.Context, req inlineTestRequest) (config.ServiceConfig, bool) {
	targetKey := strings.TrimSpace(req.TargetKey)
	if targetKey == "" {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "缺少 target_key：变更测试必须指定目标通道")
		return config.ServiceConfig{}, false
	}

	provider, service, channel, err := config.ParseMonitorFileKey(targetKey)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "target_key 格式无效")
		return config.ServiceConfig{}, false
	}

	appCfg := h.snapshotAppConfig()
	if appCfg == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "运行时配置未就绪")
		return config.ServiceConfig{}, false
	}

	// 找不到就 400 收场，**不许**回落到「空 model 照样探一次」——那正是本次要修的缺陷形态：
	// 探测会带着 `"model": ""` 打上游，红得莫名其妙。
	root, ok := findRuntimeRootByPSC(appCfg, provider, service, channel)
	if !ok {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "target_key 对应的通道不存在或尚未生效")
		return config.ServiceConfig{}, false
	}

	if req.ServiceType != root.Service {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "service_type 与 target_key 不匹配")
		return config.ServiceConfig{}, false
	}

	// 模板必须属于目标通道所在 service 的已注册变体，挡住「拿 cx 模板去测 cc 通道」。
	testType, found := probe.GetTestType(root.Service)
	if !found {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "target_key 对应的服务类型未注册探测模板")
		return config.ServiceConfig{}, false
	}
	variant, err := testType.ResolveVariant(req.TemplateName)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "测试模板无效: "+err.Error())
		return config.ServiceConfig{}, false
	}

	return config.ServiceConfig{
		Provider: root.Provider,
		Service:  root.Service,
		Channel:  root.Channel,
		// 模型三项来自服务端根行；ResolveSingleMonitor 是 config > template 的回退链
		// （lifecycle.go 只在字段为空时才填模板值），故这里灌进去的值能活到 wire 上。
		Model:        root.Model,
		RequestModel: root.RequestModel,
		ModelVendor:  root.ModelVendor,
		// 用注册表里解析出的变体 ID 而非请求原文：ResolveVariant 会 TrimSpace，
		// 而模板名随后要拼成文件路径，带空白的原文能过注册表校验却读不到文件。
		Template: variant.ID,
		BaseURL:  req.BaseURL,
		APIKey:   req.APIKey,
	}, true
}

// ChangeTest 变更请求内联探测测试
// POST /api/change/test
//
// 与 /api/onboarding/test 共用 bindInlineTestRequest + runResolvedProbe 编排，但只依赖
// change service 是否启用（而非 onboarding）。这样仅开启 change_requests、未开 onboarding 时，
// 涉及 base_url / API Key 轮换的变更流程不再因 onboarding 未启用而卡 503。
// 成功时用 change service 自己的 proofIssuer 签发 proof，可被 change.Submit 验证。
func (h *Handler) ChangeTest(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	req, ok := h.bindInlineTestRequest(c)
	if !ok {
		return
	}

	cfg, ok := h.buildChangeTestConfig(c, req)
	if !ok {
		return
	}

	result, ok := h.runResolvedProbe(c, cfg)
	if !ok {
		return
	}

	resp := inlineTestProbeResponse(result)

	// 探测成功时签发 proof，并下发其绝对过期时间（Unix 秒），供前端做倒计时/提交前校验。
	if result.ProbeStatus == 1 {
		// 绑定里带上模板与**目标通道**：proof 证明的是「这条通道用这个形态测通了」。
		// 目标通道取解析后的 PSC（而非请求原文），与 change.Submit 侧的 target.MonitorKey 同形。
		proof, expiresAt := svc.IssueProofWithExpiry(apikey.ProofClaims{
			JobID:      result.ProbeID,
			TestType:   req.ServiceType,
			APIURL:     req.BaseURL,
			Variant:    cfg.Template,
			MonitorKey: fmt.Sprintf("%s--%s--%s", cfg.Provider, cfg.Service, cfg.Channel),
		}, req.APIKey)
		resp["test_proof"] = proof
		resp["proof_expires_at"] = expiresAt
	}

	c.JSON(http.StatusOK, resp)
}

// SubmitChange 提交变更请求
// POST /api/change/submit
func (h *Handler) SubmitChange(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	// 与 /auth、/test 共用 token bucket，理由同 SubmitOnboarding：每日配额挡不住秒级突发。
	if h.probeLimiter != nil && !h.probeLimiter.Allow(c.ClientIP()) {
		apiError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "请求过于频繁，请稍后再试")
		return
	}

	var req change.SubmitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.Warn("change", "提交参数校验失败", "error", err)
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效，请检查必填字段: "+err.Error())
		return
	}

	clientIP := c.ClientIP()
	resp, err := svc.Submit(c.Request.Context(), &req, clientIP)
	if err != nil {
		if writeRevokedAPIKeyError(c, err) {
			return
		}
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusCreated, resp)
}

// === 管理端 ===

// AdminListChanges 管理员获取变更请求列表
// GET /api/admin/changes
func (h *Handler) AdminListChanges(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	status := c.DefaultQuery("status", "all")
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	changes, total, err := svc.AdminList(c.Request.Context(), status, limit, offset)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询变更请求列表失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"changes": changes,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}

// AdminGetChange 管理员获取变更请求详情
// GET /api/admin/changes/:id
func (h *Handler) AdminGetChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	cr, newKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		logger.Error("admin", "获取变更请求详情失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取变更请求详情失败")
		return
	}
	if cr == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "变更请求不存在")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"change":  cr,
		"new_key": newKey,
	})
}

// AdminUpdateChange 管理员更新变更请求内容（proposed_changes 字段 + admin_note）
// PUT /api/admin/changes/:id
func (h *Handler) AdminUpdateChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	cr, err := svc.AdminUpdate(c.Request.Context(), publicID, updates)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"change": cr})
}

// AdminApproveChange 管理员批准变更请求
// POST /api/admin/changes/:id/approve
func (h *Handler) AdminApproveChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := svc.AdminApprove(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "approved"})
}

// AdminRejectChange 管理员驳回变更请求
// POST /api/admin/changes/:id/reject
func (h *Handler) AdminRejectChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body)

	if err := svc.AdminReject(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}

// AdminApplyChange 管理员应用变更到 monitors.d/
// POST /api/admin/changes/:id/apply
func (h *Handler) AdminApplyChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminApply(c.Request.Context(), publicID); err != nil {
		logger.Error("admin", "应用变更失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "applied"})
}

// AdminDeleteChange 管理员删除变更请求
// DELETE /api/admin/changes/:id
func (h *Handler) AdminDeleteChange(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminDelete(c.Request.Context(), publicID); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// writeRevokedAPIKeyError 若 err 链上带 change.RevokedAPIKeyError，则写出专用错误码并返回 true。
// 供 auth / submit 两个入口共用，保证两处的状态码与文案不漂移。
func writeRevokedAPIKeyError(c *gin.Context, err error) bool {
	var revoked *change.RevokedAPIKeyError
	if !errors.As(err, &revoked) {
		return false
	}
	apiError(c, http.StatusUnauthorized, ErrCodeRevokedAPIKey, err.Error())
	return true
}
