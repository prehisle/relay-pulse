package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"monitor/internal/change"
	"monitor/internal/logger"
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
		// 统一错误文案，防止枚举
		apiError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "API Key 验证失败")
		return
	}

	c.JSON(http.StatusOK, resp)
}

// ChangeTest 变更请求内联探测测试
// POST /api/change/test
//
// 与 /api/onboarding/test 共用 runInlineTestProbe 探测编排，但只依赖 change service
// 是否启用（而非 onboarding）。这样仅开启 change_requests、未开 onboarding 时，
// 涉及 base_url / API Key 轮换的变更流程不再因 onboarding 未启用而卡 503。
// 成功时用 change service 自己的 proofIssuer 签发 proof，可被 change.Submit 验证。
func (h *Handler) ChangeTest(c *gin.Context) {
	svc := h.getChangeService()
	if svc == nil {
		apiError(c, http.StatusServiceUnavailable, ErrCodeFeatureDisabled, "变更请求功能未启用")
		return
	}

	req, result, ok := h.runInlineTestProbe(c)
	if !ok {
		return
	}

	resp := inlineTestProbeResponse(result)

	// 探测成功时签发 proof，并下发其绝对过期时间（Unix 秒），供前端做倒计时/提交前校验。
	if result.ProbeStatus == 1 {
		proof, expiresAt := svc.IssueProofWithExpiry(result.ProbeID, req.ServiceType, req.BaseURL, req.APIKey)
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
