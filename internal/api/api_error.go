package api

import "github.com/gin-gonic/gin"

// 统一 API 错误码常量
const (
	ErrCodeInvalidParam       = "INVALID_PARAM"
	ErrCodeInternalError      = "INTERNAL_ERROR"
	ErrCodeNotFound           = "NOT_FOUND"
	ErrCodeUnauthorized       = "UNAUTHORIZED"
	ErrCodeForbidden          = "FORBIDDEN"
	ErrCodeServiceUnavailable = "SERVICE_UNAVAILABLE"
	ErrCodeRateLimited        = "RATE_LIMITED"
	ErrCodeFeatureDisabled    = "FEATURE_DISABLED"
	ErrCodeQueueFull          = "QUEUE_FULL"
	ErrCodeNotAcceptable      = "NOT_ACCEPTABLE"

	// ErrCodeTemplateChangeRequiresSave: 管理后台 probe 收到 template 覆盖，
	// 但模板变更涉及 URLPattern/Headers/Body/SuccessContains 等派生字段重新解析，
	// 不能简单替换 cfg.Template 字段；必须先保存监测项后再测试。
	ErrCodeTemplateChangeRequiresSave = "TEMPLATE_CHANGE_REQUIRES_SAVE"

	// ErrCodeRevokedAPIKey: 提交所用的 API Key 命中「已公开泄露」拒绝名单。
	// 与 UNAUTHORIZED 分开，是为了让被动受害的中转商拿到可行动的提示（联系我们换 key），
	// 而不是与"key 查不到通道"混成同一条统一文案。
	ErrCodeRevokedAPIKey = "REVOKED_API_KEY"
)

// APIErrorDetail 统一错误对象
type APIErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// APIErrorEnvelope 统一错误响应 envelope
type APIErrorEnvelope struct {
	Error APIErrorDetail `json:"error"`
}

// WriteAPIError 写入统一 API 错误响应（导出供跨包使用）
func WriteAPIError(c *gin.Context, status int, code string, message string) {
	resp := APIErrorEnvelope{
		Error: APIErrorDetail{
			Code:    code,
			Message: message,
		},
	}
	if requestID := c.GetString("request_id"); requestID != "" {
		resp.Error.RequestID = requestID
	}
	c.JSON(status, resp)
}

// AbortWithAPIError 写入错误响应并终止后续处理（导出供中间件使用）
func AbortWithAPIError(c *gin.Context, status int, code string, message string) {
	resp := APIErrorEnvelope{
		Error: APIErrorDetail{
			Code:    code,
			Message: message,
		},
	}
	if requestID := c.GetString("request_id"); requestID != "" {
		resp.Error.RequestID = requestID
	}
	c.AbortWithStatusJSON(status, resp)
}

// apiError 包内简写：写入统一 API 错误响应
func apiError(c *gin.Context, status int, code string, message string) {
	WriteAPIError(c, status, code, message)
}

// abortAPIError 包内简写：写入错误响应并终止后续处理
func abortAPIError(c *gin.Context, status int, code string, message string) {
	AbortWithAPIError(c, status, code, message)
}
