package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
)

// buildLikePattern 把用户输入转成可安全用于 SQL LIKE 的模式串。
// 先用 '!' 作为转义字符把 LIKE 元字符（! % _）转义，再用 % 包裹实现子串模糊匹配。
// 对应的 SQL 须声明 ESCAPE '!'。返回空串表示无搜索条件。
func buildLikePattern(raw string) string {
	if raw == "" {
		return ""
	}
	escaped := strings.ReplaceAll(raw, "!", "!!")
	escaped = strings.ReplaceAll(escaped, "%", "!%")
	escaped = strings.ReplaceAll(escaped, "_", "!_")
	return "%" + escaped + "%"
}

// checkAdminToken 验证管理员 Bearer token。
// 返回 true 表示验证通过，false 表示已返回错误响应。
func (h *Handler) checkAdminToken(c *gin.Context) bool {
	h.cfgMu.RLock()
	adminToken := h.config.Onboarding.AdminToken
	h.cfgMu.RUnlock()

	if adminToken == "" {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "管理后台暂不可用")
		return false
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "缺少 Authorization 请求头")
		return false
	}

	const bearerPrefix = "Bearer "
	if !strings.HasPrefix(authHeader, bearerPrefix) {
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "Authorization 格式错误，应为 Bearer <token>")
		return false
	}

	token := strings.TrimPrefix(authHeader, bearerPrefix)
	if subtle.ConstantTimeCompare([]byte(token), []byte(adminToken)) != 1 {
		apiError(c, http.StatusForbidden, ErrCodeForbidden, "管理员 token 无效")
		return false
	}

	return true
}

// AdminListSubmissions 管理员获取申请列表
// GET /api/admin/submissions
func (h *Handler) AdminListSubmissions(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	status := c.DefaultQuery("status", "all")
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	// 按编号（public_id，小写 hex UUID）模糊搜索：统一转小写后转义为 LIKE 模式串。
	search := buildLikePattern(strings.ToLower(strings.TrimSpace(c.Query("q"))))

	submissions, total, err := svc.AdminList(c.Request.Context(), status, search, limit, offset)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "查询申请列表失败")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"submissions": submissions,
		"total":       total,
		"limit":       limit,
		"offset":      offset,
	})
}

// AdminGetSubmission 管理员获取申请详情（含解密 API Key）
// GET /api/admin/submissions/:id
func (h *Handler) AdminGetSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	sub, apiKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		logger.Error("admin", "获取申请详情失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取申请详情失败")
		return
	}
	if sub == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "申请不存在")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"submission": sub,
		"api_key":    apiKey,
	})
}

// AdminUpdateSubmission 管理员更新申请
// PUT /api/admin/submissions/:id
func (h *Handler) AdminUpdateSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	var updates map[string]any
	if err := c.ShouldBindJSON(&updates); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "请求参数无效")
		return
	}

	sub, err := svc.AdminUpdate(c.Request.Context(), publicID, updates)
	if err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"submission": sub})
}

// AdminDeleteSubmission 管理员删除申请
// DELETE /api/admin/submissions/:id
func (h *Handler) AdminDeleteSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	if err := svc.AdminDelete(c.Request.Context(), publicID); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// AdminRejectSubmission 管理员驳回申请
// POST /api/admin/submissions/:id/reject
func (h *Handler) AdminRejectSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")
	var body struct {
		Note string `json:"note"`
	}
	_ = c.ShouldBindJSON(&body) // note is optional

	if err := svc.AdminReject(c.Request.Context(), publicID, body.Note); err != nil {
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "rejected"})
}

// AdminPublishSubmission 管理员上架申请
// POST /api/admin/submissions/:id/publish
func (h *Handler) AdminPublishSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	publicID := c.Param("id")

	var req struct {
		Board string `json:"board"`
	}
	// 忽略解析错误（空 body 合法，使用默认值）
	_ = c.ShouldBindJSON(&req)
	board := strings.TrimSpace(req.Board)
	if board == "" {
		board = "hot"
	}
	switch board {
	case "hot", "secondary", "cold":
	default:
		apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, "board 参数无效，可选值：hot/secondary/cold")
		return
	}

	if err := svc.AdminPublish(c.Request.Context(), publicID, board); err != nil {
		var conflict *onboarding.PSCConflictError
		if errors.As(err, &conflict) {
			logger.Warn("admin", "上架 PSC 冲突", "public_id", publicID,
				"psc", conflict.Provider+"/"+conflict.Service+"/"+conflict.Channel,
				"suggested", conflict.SuggestedChannel)
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{
					"code":    "PSC_CONFLICT",
					"message": conflict.Error(),
				},
				"suggested_channel": conflict.SuggestedChannel,
			})
			return
		}
		var invalidSlug *onboarding.InvalidProviderSlugError
		if errors.As(err, &invalidSlug) {
			logger.Warn("admin", "上架失败：provider slug 需覆盖", "public_id", publicID,
				"provider_name", invalidSlug.ProviderName, "derived_slug", invalidSlug.DerivedSlug)
			apiError(c, http.StatusBadRequest, ErrCodeInvalidParam, invalidSlug.Error())
			return
		}
		logger.Error("admin", "上架失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "published"})
}

// AdminTestSubmission 管理员对申请执行内联探测测试
// POST /api/admin/submissions/:id/test
func (h *Handler) AdminTestSubmission(c *gin.Context) {
	if !h.checkAdminToken(c) {
		return
	}

	svc := h.getOnboardingService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "自助收录功能未启用")
		return
	}

	if h.inlineProber == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "内联探测器未初始化")
		return
	}

	publicID := c.Param("id")
	sub, apiKey, err := svc.AdminGetDetail(c.Request.Context(), publicID)
	if err != nil {
		apiError(c, http.StatusInternalServerError, ErrCodeInternalError, "获取申请详情失败")
		return
	}
	if sub == nil {
		apiError(c, http.StatusNotFound, ErrCodeNotFound, "申请不存在")
		return
	}

	// 走与 AdminPublish 同一映射器构造 ServiceConfig，确保"管理员审核测一下"与
	// "发布后调度真探测"使用字段级一致的 cfg。
	cfg := onboarding.BuildServiceConfigFromSubmission(sub, apiKey)

	appCfg := h.snapshotAppConfig()
	if appCfg == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeServiceUnavailable, "运行时配置未就绪")
		return
	}
	if err := config.ResolveSingleMonitor(appCfg, &cfg, h.configDir()); err != nil {
		// 多数失败源于 submission 字段不合法或模板名错误（用户/审核员输入），按 422 返回；
		// 模板文件损坏等服务端问题罕见，但合并到同分支简化处理。
		logger.Error("admin", "ResolveSingleMonitor 失败", "public_id", publicID, "error", err)
		apiError(c, http.StatusUnprocessableEntity, ErrCodeInvalidParam, "解析测试配置失败: "+err.Error())
		return
	}

	// 与两条公开流程同一道闸：解析完仍拿不到模型就停下，绝不带着 `"model": ""` 去打上游。
	// 本入口不走 runResolvedProbe（它要自己传 WithCurlCapture），闸得单独接一次——漏接的话
	// 审核员会看到一个没头没脑的上游 400，而真正的原因是这条申请压根没填模型。
	if resolvedProbeModel(cfg) == "" {
		apiError(c, http.StatusUnprocessableEntity, ErrCodeInvalidParam,
			"测试配置缺少模型：所选模板未声明模型，需在申请里填写模型 ID")
		return
	}

	// 使用内联探测器同步执行
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result := h.inlineProber.ProbeConfig(ctx, cfg, probe.WithCurlCapture())

	c.JSON(http.StatusOK, gin.H{
		"probe_status":     result.ProbeStatus,
		"sub_status":       result.SubStatus,
		"http_code":        result.HTTPCode,
		"latency":          result.Latency,
		"error_message":    result.ErrorMessage,
		"response_snippet": result.ResponseSnippet,
		"probe_id":         result.ProbeID,
		"curl":             result.Curl,
	})
}
