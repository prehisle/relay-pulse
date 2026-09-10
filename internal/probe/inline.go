package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/logger"
	"monitor/internal/monitor"
)

// DefaultMaxResponseBytes 响应体读取上限。
const DefaultMaxResponseBytes int64 = 10 << 20 // 10MB

// probeResult 内部探测结果。
type probeResult struct {
	Status          int
	SubStatus       string
	HTTPCode        int
	Latency         int // ms
	ResponseSnippet string
	CurlCommand     string // 仅 captureCurl 时填充，见 buildCurlCommand
	Err             error
}

// internalProber 为底层安全探测器。
type internalProber struct {
	client       *http.Client
	maxBodyBytes int64
	uidMgr       *identity.UserIDManager
}

func newInternalProber(guard *SSRFGuard, maxBodyBytes int64, uidMgr *identity.UserIDManager) *internalProber {
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultMaxResponseBytes
	}
	return &internalProber{
		client:       newSafeHTTPClient(guard),
		maxBodyBytes: maxBodyBytes,
		uidMgr:       uidMgr,
	}
}

func (p *internalProber) probe(ctx context.Context, cfg *config.ServiceConfig, captureCurl bool, proxyURL string) *probeResult {
	result := &probeResult{
		Status:    0,
		SubStatus: "none",
	}

	// 默认走预建的 safe 直连客户端；仅当调用方显式传入 proxyURL（管理员路径）时，
	// 为本次探测临时构建带代理的客户端（admin 低频，建后即用、用完关闲连）。
	client := p.client
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL != "" {
		proxyClient, err := newProxyHTTPClient(proxyURL)
		if err != nil {
			result.SubStatus = "network_error"
			result.Err = fmt.Errorf("创建代理客户端失败（proxy=%s）: %s",
				maskProxyURL(proxyURL), maskProxyURLInMessage(err.Error(), proxyURL))
			return result
		}
		client = proxyClient
		defer proxyClient.CloseIdleConnections()
	}

	probeURL, probeBody, probeHeaders, probeSuccessContains, _, _ := monitor.InjectVariables(cfg, p.uidMgr)

	bodyBytes := []byte(strings.TrimSpace(probeBody))
	reqBody := bytes.NewBuffer(bodyBytes)
	req, err := http.NewRequestWithContext(ctx, cfg.Method, probeURL, reqBody)
	if err != nil {
		result.SubStatus = "invalid_request"
		result.Err = fmt.Errorf("创建请求失败: %w", err)
		return result
	}
	req.Close = true

	for k, v := range probeHeaders {
		req.Header.Set(k, v)
	}

	// 在发送前用「实际要发的」请求快照生成 curl（仅 admin 测试路径开启）。
	// 复用同一次 InjectVariables 的填充结果，保证 curl 与真实请求逐项一致。
	if captureCurl {
		result.CurlCommand = buildCurlCommand(req, bodyBytes, cfg.APIKey)
	}

	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency

	if err != nil {
		result.SubStatus = "network_error"
		if proxyURL != "" {
			// 传输错误经代理时可能回显 proxy URL（含 userinfo），统一脱敏后再外露。
			result.Err = fmt.Errorf("%s", maskProxyURLInMessage(err.Error(), proxyURL))
		} else {
			result.Err = err
		}
		return result
	}
	defer resp.Body.Close()

	result.HTTPCode = resp.StatusCode

	body, err := readBodyLimited(resp.Body, p.maxBodyBytes)
	if err != nil {
		// 区分读 body 失败的真实原因，避免把"读取超时/连接中断"误标成"响应体过大"
		// （response_timeout 语义：HTTP 头已回但读响应体超时，常见于流式响应经代理变慢）。
		result.SubStatus = bodyReadSubStatus(err)
		result.Err = err
		return result
	}

	status, sub := classifyHTTPStatus(resp.StatusCode, latency, cfg.SlowLatencyDuration)
	result.Status = status
	result.SubStatus = sub

	if len(body) > 0 {
		// 展示用语义：抽不到正文时退回响应体原文，否则管理后台点一次探测
		// 就看不到上游错误（内容校验那侧刻意相反，见 monitor.ResponseSnippetText）。
		snippet := monitor.ResponseSnippetText(body)
		const maxSnippetLen = 512
		if len(snippet) > maxSnippetLen {
			snippet = snippet[:maxSnippetLen] + "... (truncated)"
		}
		result.ResponseSnippet = snippet
	}

	if result.Status != 0 && strings.TrimSpace(probeSuccessContains) != "" {
		text := monitor.AggregateResponseText(body)
		if text == "" || !strings.Contains(text, probeSuccessContains) {
			result.Status = 0
			result.SubStatus = "content_mismatch"
			// 与 scheduler 侧逐字同口径：内容校验失败时给结构化诊断，而不是响应体头部片段。
			// 两边说法必须一致——同一次失败，管理员从「点探测」和从「探测历史」看到的
			// 若是两种解释，排障时无从判断该信哪个。
			result.ResponseSnippet = monitor.BuildContentMismatchSummary(body, probeSuccessContains)
			result.Err = fmt.Errorf("响应内容未包含预期关键字")
			return result
		}
	}

	return result
}

// errResponseTooLarge 标记"响应体超过上限"这一**特定**失败，与读取过程中的 I/O
// 错误（超时/连接中断）区分开，供 bodyReadSubStatus 精确归类。
var errResponseTooLarge = errors.New("响应体超过上限")

func readBodyLimited(r io.Reader, limit int64) ([]byte, error) {
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return data, err
	}
	if int64(len(data)) > limit {
		return data[:limit], fmt.Errorf("%w（%d bytes）", errResponseTooLarge, limit)
	}
	return data, nil
}

// bodyReadSubStatus 把读响应体失败归类到正确的细分状态：
//   - 真正超过大小上限 → response_too_large
//   - 读取超时（context deadline / net 超时 / read deadline）→ response_timeout
//     （HTTP 头已回但读 body 超时，常见于流式响应经代理变慢）
//   - 其余读取中断（连接重置、EOF 等）→ network_error
func bodyReadSubStatus(err error) string {
	switch {
	case errors.Is(err, errResponseTooLarge):
		return "response_too_large"
	case isTimeoutError(err):
		return "response_timeout"
	default:
		return "network_error"
	}
}

// isTimeoutError 判定错误是否为超时类（context 截止、net.Error.Timeout、read deadline）。
func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func classifyHTTPStatus(statusCode, latency int, slowLatency time.Duration) (int, string) {
	if statusCode >= 200 && statusCode < 300 {
		if slowLatency > 0 && latency > int(slowLatency/time.Millisecond) {
			return 2, "slow_latency"
		}
		return 1, "none"
	}

	if statusCode >= 300 && statusCode < 400 {
		return 0, "redirect_blocked"
	}

	if statusCode == 401 || statusCode == 403 {
		return 0, "auth_error"
	}

	if statusCode == 400 {
		return 0, "invalid_request"
	}

	if statusCode == 429 {
		// 与 scheduler/storage 口径对齐（monitor/probe.go 用 "rate_limit"），
		// 否则前端 i18n 与可用率统计会按两套字符串割裂。
		return 0, "rate_limit"
	}

	if statusCode >= 500 {
		return 0, "server_error"
	}

	if statusCode >= 400 {
		return 0, "client_error"
	}

	return 0, "unknown_error"
}

// Result 为对外暴露的内联探测结果。
type Result struct {
	ProbeStatus     int    `json:"probe_status"`
	SubStatus       string `json:"sub_status"`
	HTTPCode        int    `json:"http_code"`
	Latency         int    `json:"latency"`
	ErrorMessage    string `json:"error_message,omitempty"`
	ResponseSnippet string `json:"response_snippet,omitempty"`
	ProbeID         string `json:"probe_id"`
	// Curl 为本次实际请求快照对应的可复制 curl 命令；默认脱敏（密钥用 $RP_API_KEY
	// 占位）。仅在调用方传入 WithCurlCapture() 时填充，公开自测路径不下发。
	Curl string `json:"curl,omitempty"`
	// ViaProxy 标记本次探测是否显式经 cfg.Proxy 代理。仅 AdminProbeMonitor 传 WithProxy；
	// 公开 onboarding/change 自测永不传，故恒为 false。供前端展示"经代理"标签。
	ViaProxy bool `json:"via_proxy"`
}

// InlineProber 提供同步内联探测能力。
type InlineProber struct {
	prober *internalProber
	sem    chan struct{}
}

// probeOptions 聚合单次内联探测的可选诊断开关。
type probeOptions struct {
	captureCurl bool
	proxyURL    string
}

// ProbeOption 控制单次内联探测的可选诊断输出。
type ProbeOption func(*probeOptions)

// WithCurlCapture 让 ProbeConfig 返回本次实际请求快照对应的 curl 命令（默认脱敏）。
// 仅供管理员测试入口使用；公开 onboarding/change 自测不要传入。
func WithCurlCapture() ProbeOption {
	return func(o *probeOptions) { o.captureCurl = true }
}

// WithProxy 让本次 ProbeConfig 经显式 proxyURL 探测（空字符串=直连，no-op）。
//
// 不变量：这是调用方**显式 opt-in 的安全边界**——只有管理员通道管理探测（AdminProbeMonitor）
// 传入；onboarding/change 等公开自测路径绝不传，因而即使其 cfg 将来出现 proxy 字段，也不会
// 把本服务当作 SSRF 跳板。proxyURL 恒取自管理员保存的通道配置，请求体不可覆盖。
func WithProxy(proxyURL string) ProbeOption {
	return func(o *probeOptions) { o.proxyURL = strings.TrimSpace(proxyURL) }
}

// NewInlineProber 创建内联探测器。
//
// uidMgr 用于注入 metadata.user_id 占位符；传 nil 会让严校验的 provider
// （如 TopRouterCN）判为"非 CLI 客户端"并返回 403。主程序应传入共享的
// UserIDManager 实例，与 scheduler 的请求构造保持一致。
func NewInlineProber(maxConcurrency int, uidMgr *identity.UserIDManager) *InlineProber {
	if maxConcurrency <= 0 {
		maxConcurrency = 5
	}
	return &InlineProber{
		prober: newInternalProber(NewSSRFGuard(), DefaultMaxResponseBytes, uidMgr),
		sem:    make(chan struct{}, maxConcurrency),
	}
}

// Probe 同步执行一次探测并返回结果。
func (p *InlineProber) Probe(ctx context.Context, serviceType, templateName, baseURL, apiKey string) *Result {
	result := &Result{
		ProbeID:     "probe-" + uuid.New().String(),
		ProbeStatus: 0,
		SubStatus:   "none",
	}
	// defer 单条日志，确保所有 early-return 分支都能被串联起来
	defer logInlineProbeResult(result, "service", strings.TrimSpace(serviceType),
		"template", strings.TrimSpace(templateName), "base_url", baseURL)

	if err := ctx.Err(); err != nil {
		result.SubStatus = "canceled"
		result.ErrorMessage = err.Error()
		return result
	}

	// 尝试获取信号量（满时立即拒绝）
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		result.SubStatus = "concurrency_limited"
		result.ErrorMessage = "探测并发已达上限，请稍后再试"
		return result
	}

	// 查找测试类型
	testType, ok := GetTestType(strings.TrimSpace(serviceType))
	if !ok {
		result.SubStatus = "unknown_test_type"
		result.ErrorMessage = fmt.Sprintf("不支持的服务类型: %s", serviceType)
		return result
	}

	// 解析模板变体
	variant, err := testType.ResolveVariant(templateName)
	if err != nil {
		result.SubStatus = "unknown_variant"
		result.ErrorMessage = err.Error()
		return result
	}

	// 构建探测配置
	cfg, err := testType.Builder.Build(baseURL, apiKey, variant)
	if err != nil {
		result.SubStatus = "build_failed"
		result.ErrorMessage = fmt.Sprintf("构建探测配置失败: %v", err)
		return result
	}

	// 使用模板超时（兜底 15s），外层 context 硬上限 30s
	timeout := cfg.TimeoutDuration
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 执行底层探测（模板自测路径不输出 curl、不走代理）
	pr := p.prober.probe(probeCtx, cfg, false, "")
	if pr == nil {
		result.SubStatus = "internal_error"
		result.ErrorMessage = "探测器返回空结果"
		return result
	}

	result.ProbeStatus = pr.Status
	result.SubStatus = pr.SubStatus
	result.HTTPCode = pr.HTTPCode
	result.Latency = pr.Latency
	result.ResponseSnippet = pr.ResponseSnippet
	if pr.Err != nil {
		// 传输层错误（*url.Error）会带完整请求 URL，URL 内嵌 key 的模板会泄漏密钥；
		// 在写入响应/日志前统一脱敏。
		result.ErrorMessage = redactSecrets(pr.Err.Error(), secretVariants(apiKey))
	}
	return result
}

// logInlineProbeResult 在 InlineProber 的每次探测结束时打印一条结构化日志，
// 让运维可以 `grep probe_id=probe-xxx` 把一次 inline 探测的所有上下文串起来。
//
// 日志级别按主状态分级：绿 → Info；黄/红 → Warn（避免 Error 污染告警通道）。
//
// 字段说明：
//   - probe_id / status / sub_status / http_code / latency_ms：result 自身字段
//   - error：截断到 200 字节，避免日志被超长 payload 撑爆
//   - 不记录 ResponseSnippet：可能含上游返回的敏感数据（token / cookie / 内部 URL），
//     由 API 响应层按需返回给管理员前端
//   - extraFields：调用点已知的上下文（PSCM、template、base_url），按 slog 键值对追加
func logInlineProbeResult(r *Result, extraFields ...any) {
	if r == nil {
		return
	}
	fields := []any{
		"probe_id", r.ProbeID,
		"status", r.ProbeStatus,
		"sub_status", r.SubStatus,
		"http_code", r.HTTPCode,
		"latency_ms", r.Latency,
	}
	if r.ErrorMessage != "" {
		fields = append(fields, "error", truncateForLog(r.ErrorMessage, 200))
	}
	fields = append(fields, extraFields...)

	switch r.ProbeStatus {
	case 1:
		logger.Info("inline_probe", "探测完成", fields...)
	default:
		logger.Warn("inline_probe", "探测异常或不可用", fields...)
	}
}

// truncateForLog 安全截断字符串到 max 字节，避免日志被超长 payload 撑爆。
func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// maskProxyURL 把代理 URL 脱敏为 scheme://host:port，剥掉 userinfo（含密码），
// 供错误信息/展示使用——绝不外露代理凭据。
func maskProxyURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "<invalid-proxy-url>"
	}
	return u.Scheme + "://" + u.Host
}

// maskProxyURLInMessage 把错误文本里出现的原始代理 URL（可能含 userinfo）替换为脱敏形态。
// 传输层错误偶尔会回显完整 proxy URL；统一在外露前清掉凭据。
func maskProxyURLInMessage(msg, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return msg
	}
	masked := maskProxyURL(raw)
	msg = strings.ReplaceAll(msg, raw, masked)
	// 兜底：去掉 userinfo 后的形态也替换（部分库会规整化 URL 再回显）。
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		u.User = nil
		msg = strings.ReplaceAll(msg, u.String(), masked)
	}
	return msg
}

// ProbeConfig 使用已解析完成的 ServiceConfig 执行一次内联探测。
//
// 适用场景：调用方持有一份**已经过模板填充 + Duration 派生**的 ServiceConfig
// （来自运行时 AppConfig.Monitors，或者经 config.ResolveSingleMonitor 处理过的
// 临时配置），希望以"沙箱测试"语义复用 InlineProber 的执行内核。
//
// 与 Probe 方法的区别：
//   - Probe(serviceType, templateName, baseURL, apiKey) 走 Builder.Build 从模板构造 cfg
//   - ProbeConfig(cfg) 跳过 Builder，直接使用调用方传入的 cfg
//
// 因此本方法**不会**对 cfg 做任何模板解析、父子继承、env 注入或 Duration 派生 ——
// 这些都是调用方的责任。返回值与 Probe 完全同构（携带 probe_id，可与日志/审计串联）。
//
// 仍保留的安全限制（继承自底层 internalProber + safe HTTP client）：
//   - SSRF 守卫：私网/回环/链路本地 IP 阻断（默认 safe client；走代理时由代理负责
//     上游解析/连接，该上游 IP 校验天然失效，见 newProxyHTTPClient 说明）
//   - 代理：默认直连、忽略 cfg.Proxy 与环境代理变量；**仅当**调用方显式传入
//     WithProxy(proxyURL) 时（只有管理员通道管理探测会传）才经该代理，复用 scheduler
//     的 http/socks5 语义。公开 onboarding/change 自测不传，绝不走代理。
//   - 禁用自动重定向：3xx 直接归类为 redirect_blocked
//   - 响应体读取上限：DefaultMaxResponseBytes (10 MB)
//   - 并发上限：与 Probe 共享同一 semaphore
func (p *InlineProber) ProbeConfig(ctx context.Context, cfg config.ServiceConfig, opts ...ProbeOption) *Result {
	var probeOpts probeOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&probeOpts)
		}
	}
	probeOpts.proxyURL = strings.TrimSpace(probeOpts.proxyURL)

	result := &Result{
		ProbeID:     "probe-" + uuid.New().String(),
		ProbeStatus: 0,
		SubStatus:   "none",
		ViaProxy:    probeOpts.proxyURL != "",
	}
	// defer 单条日志，把 probe_id 与 PSCM 上下文一起记下来；
	// 让运维 `grep probe_id=probe-xxx` 一行串联整次 inline 探测。
	// proxied 只记布尔，绝不记 proxy URL（含凭据）。
	defer logInlineProbeResult(result,
		"provider", cfg.Provider,
		"service", cfg.Service,
		"channel", cfg.Channel,
		"model", cfg.Model,
		"base_url", cfg.BaseURL,
		"template", cfg.Template,
		"proxied", probeOpts.proxyURL != "")

	if err := ctx.Err(); err != nil {
		result.SubStatus = "canceled"
		result.ErrorMessage = err.Error()
		return result
	}

	// 尝试获取信号量（满时立即拒绝，避免与定时调度抢资源）
	select {
	case p.sem <- struct{}{}:
		defer func() { <-p.sem }()
	default:
		result.SubStatus = "concurrency_limited"
		result.ErrorMessage = "探测并发已达上限，请稍后再试"
		return result
	}

	// 探测期超时：优先使用 cfg.TimeoutDuration（已经过 ResolveSingleMonitor 派生），
	// 兜底 15s；外层硬上限由调用方传入的 ctx 控制（通常 handler 套 30s）。
	timeout := cfg.TimeoutDuration
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pr := p.prober.probe(probeCtx, &cfg, probeOpts.captureCurl, probeOpts.proxyURL)
	if pr == nil {
		result.SubStatus = "internal_error"
		result.ErrorMessage = "探测器返回空结果"
		return result
	}

	result.ProbeStatus = pr.Status
	result.SubStatus = pr.SubStatus
	result.HTTPCode = pr.HTTPCode
	result.Latency = pr.Latency
	result.ResponseSnippet = pr.ResponseSnippet
	result.Curl = pr.CurlCommand
	if pr.Err != nil {
		// 传输层错误（*url.Error）会带完整请求 URL，URL 内嵌 key 的模板会泄漏密钥；
		// 在写入响应/日志前统一脱敏。
		result.ErrorMessage = redactSecrets(pr.Err.Error(), secretVariants(cfg.APIKey))
	}
	return result
}
