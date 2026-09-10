package monitor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/logger"
	"monitor/internal/storage"
)

// ProbeResult 探测结果
type ProbeResult struct {
	Provider        string
	Service         string
	Channel         string
	Model           string            // 模型展示名
	ModelID         string            // 稳定模型 id（md_<uuidv4>），为空表示配置未填
	Status          int               // 1=绿, 0=红, 2=黄
	SubStatus       storage.SubStatus // 细分状态（黄色/红色原因）
	HttpCode        int               // HTTP 状态码（0 表示非 HTTP 错误）
	Latency         int               // ms
	Timestamp       int64
	Error           error
	ResponseSnippet string // 失败响应摘要（status=0 时组装，见 buildFailureSnippet）
}

// Prober 探测器
type Prober struct {
	clientPool *ClientPool
	storage    storage.RecordStorage
	userIDMgr  *identity.UserIDManager
}

const httpFailureBodyCaptureLimit = 512

// NewProber 创建探测器
func NewProber(storage storage.RecordStorage, userIDMgr *identity.UserIDManager) *Prober {
	return &Prober{
		clientPool: NewClientPool(),
		storage:    storage,
		userIDMgr:  userIDMgr,
	}
}

// InjectVariables 替换所有占位符，返回临时副本（不修改共享 ServiceConfig）
func InjectVariables(cfg *config.ServiceConfig, uidMgr *identity.UserIDManager) (url, body string, headers map[string]string, successContains, prompt, expectedAnswer string) {
	// 回退：非模板监测项可能没有 URLPattern，直接使用 BaseURL
	url = cfg.URLPattern
	if url == "" {
		url = cfg.BaseURL
	}
	body = cfg.Body
	successContains = cfg.SuccessContains

	// 复制 headers
	headers = make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = v
	}

	// 生成动态提示词/算术题（仅在模板使用相关占位符时）
	a, b := 0, 0
	prompt, expectedAnswer = "", ""
	if strings.Contains(body, "{{PROMPT}}") ||
		strings.Contains(body, "{{ARITH_A}}") ||
		strings.Contains(body, "{{ARITH_B}}") ||
		strings.Contains(body, "{{EXPECTED_ANSWER}}") ||
		strings.Contains(successContains, "{{EXPECTED_ANSWER}}") {
		a, b, prompt, expectedAnswer = GenerateArithmeticPrompt()
	}

	// 原子获取 user_id 和 hash（确保同一生命周期内一致）
	userID, userIDHash := "", ""
	if uidMgr != nil {
		userID, userIDHash = uidMgr.GetUserIDPair(cfg.Provider, cfg.Service, cfg.Channel, cfg.UserIDRefreshMinutes)
	}

	// 请求模型回退链：request_model > model
	requestModel := strings.TrimSpace(cfg.RequestModel)
	if requestModel == "" {
		requestModel = cfg.Model
	}

	// 构建替换器
	replacePairs := []string{
		"{{BASE_URL}}", cfg.BaseURL,
		"{{API_KEY}}", cfg.APIKey,
		"{{MODEL}}", requestModel,
		"{{REQUEST_MODEL}}", requestModel,
		"{{USER_ID}}", userID,
		"{{USER_ID_HASH}}", userIDHash,
		"{{RAND_UUID}}", uuid.New().String(),
		"{{RAND_UUID2}}", uuid.New().String(),
		"{{USER_ACCOUNT_UUID}}", identity.DeriveUUID(userIDHash, "account_uuid"),
		"{{PROMPT}}", prompt,
		"{{EXPECTED_ANSWER}}", expectedAnswer,
		"{{ARITH_A}}", strconv.Itoa(a),
		"{{ARITH_B}}", strconv.Itoa(b),
	}
	replacer := strings.NewReplacer(replacePairs...)

	url = replacer.Replace(url)
	body = replacer.Replace(body)
	successContains = replacer.Replace(successContains)
	for k, v := range headers {
		headers[k] = replacer.Replace(v)
	}

	return url, body, headers, successContains, prompt, expectedAnswer
}

// Probe 执行单次探测（支持可配置重试）
func (p *Prober) Probe(ctx context.Context, cfg *config.ServiceConfig) *ProbeResult {
	result := &ProbeResult{
		Provider:  cfg.Provider,
		Service:   cfg.Service,
		Channel:   cfg.Channel,
		Model:     cfg.Model,
		ModelID:   cfg.ModelID,
		Timestamp: time.Now().Unix(),
		SubStatus: storage.SubStatusNone,
		HttpCode:  0, // 默认为 0，表示非 HTTP 错误
	}

	// 使用配置的超时时间包装 context
	// 兜底：防止 TimeoutDuration 未下发导致请求无期限挂起
	timeout := cfg.TimeoutDuration
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	// 获取对应 provider 的客户端（考虑代理配置）
	client, err := p.clientPool.GetClient(cfg.Provider, cfg.Proxy)
	if err != nil {
		result.Error = fmt.Errorf("获取 HTTP 客户端失败: %w", err)
		result.Status = 0
		result.SubStatus = storage.SubStatusNetworkError
		return result
	}

	// 重试配置：从 config 获取（已在 Normalize 阶段下发到 monitor 级别）
	maxAttempts := cfg.RetryCount + 1 // RetryCount 是额外重试次数，总尝试次数 = 1 + RetryCount
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	baseDelay := cfg.RetryBaseDelayDuration
	if baseDelay <= 0 {
		baseDelay = 200 * time.Millisecond
	}
	maxDelay := cfg.RetryMaxDelayDuration
	if maxDelay <= 0 {
		maxDelay = 2 * time.Second
	}
	jitter := cfg.RetryJitterValue
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}

	// 累计延迟（所有 attempt 的延迟总和）
	var totalLatency int
	// 实际执行的 attempt 次数（用于最终日志）
	var actualAttempts int
	// 保存最后一次的响应体（用于最终诊断日志）
	var lastBodyBytes []byte
	// 保存最后一次的算术题信息（用于日志）
	var lastPrompt, lastExpectedAnswer string
	// 保存最后一次注入后的内容校验关键字：arith 模板每次探测都换一道随机题，
	// 不留存就无法事后判断「这次判红判得对不对」。摘要组装在重试循环之外，故需带出来。
	var lastSuccessContains string

	// 重试循环（使用标签以便从 select 中正确跳出）
retryLoop:
	for attempt := 0; attempt < maxAttempts; attempt++ {
		actualAttempts = attempt + 1

		// 检查 context 是否已超时/取消
		if ctx.Err() != nil {
			// 超时或取消，不再重试
			if attempt == 0 {
				// 首次尝试就超时/取消，设置错误
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					result.Error = fmt.Errorf("请求超时(%v): %w", timeout, ctx.Err())
				} else {
					result.Error = fmt.Errorf("请求取消: %w", ctx.Err())
				}
				result.Status = 0
				result.SubStatus = storage.SubStatusNetworkError
			}
			// 否则保留上一次尝试的结果
			break retryLoop
		}

		// 变量注入：创建临时副本替换所有占位符（不修改共享 ServiceConfig）
		probeURL, probeBody, probeHeaders, probeSuccessContains, probePrompt, probeExpectedAnswer := InjectVariables(cfg, p.userIDMgr)
		// 保留最后一次的算术题信息用于日志
		lastPrompt, lastExpectedAnswer = probePrompt, probeExpectedAnswer
		lastSuccessContains = probeSuccessContains

		// 准备请求体（去除首尾空白，某些 API 对此敏感）
		reqBody := bytes.NewBuffer([]byte(strings.TrimSpace(probeBody)))
		req, err := http.NewRequestWithContext(ctx, cfg.Method, probeURL, reqBody)
		if err != nil {
			result.Error = fmt.Errorf("创建请求失败: %w", err)
			result.Status = 0
			result.SubStatus = storage.SubStatusNetworkError
			// 请求创建失败是配置问题，重试无意义
			break retryLoop
		}
		// 冷启动口径：显式关闭长连接，避免请求层复用上一次连接。
		req.Close = true

		// 设置 Headers（已替换占位符）
		for k, v := range probeHeaders {
			req.Header.Set(k, v)
		}

		// 发送请求并计时（响应头到达）
		start := time.Now()
		resp, err := client.Do(req)
		headerLatency := int(time.Since(start).Milliseconds())
		totalLatency += headerLatency

		if err != nil {
			// 极少数情况下 err != nil 但 resp != nil，需要关闭 body，避免资源泄漏
			drainAndClose(resp)

			// 超时/取消：不重试（总超时已到，继续重试无意义）
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				if errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("请求超时(%v): %w", timeout, err)
				} else {
					err = fmt.Errorf("请求取消: %w", err)
				}
				logger.Error("probe", "请求失败（不重试）",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
					"attempt", attempt+1, "max_attempts", maxAttempts, "error", err)
				result.Error = err
				result.Status = 0
				result.SubStatus = storage.SubStatusNetworkError
				result.Latency = totalLatency
				break retryLoop // 超时不重试
			}

			// 其他网络错误，设置结果并继续重试
			logger.Error("probe", "请求失败",
				"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
				"attempt", attempt+1, "max_attempts", maxAttempts, "error", err)
			result.Error = err
			result.Status = 0
			result.SubStatus = storage.SubStatusNetworkError
			result.Latency = totalLatency
			result.HttpCode = 0
			lastBodyBytes = nil // 网络错误无响应体

			// 检查是否需要重试
			if attempt+1 < maxAttempts {
				delay := computeRetryDelay(attempt, baseDelay, maxDelay, jitter)
				logger.Info("probe", "准备重试",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
					"attempt", attempt+1, "next_attempt", attempt+2, "delay_ms", delay.Milliseconds())

				// 等待退避时间，同时监听 context
				select {
				case <-ctx.Done():
					// 超时，停止重试
					break retryLoop
				case <-time.After(delay):
					// 继续下一次重试
					continue
				}
			}
			continue
		}

		// 记录 HTTP 状态码
		result.HttpCode = resp.StatusCode

		// 完整读取响应体（避免连接泄漏），在需要内容匹配时保留文本
		var bodyBytes []byte
		bodyReadLatency := 0
		switch {
		case probeSuccessContains != "":
			bodyReadStart := time.Now()
			data, readErr := io.ReadAll(resp.Body)
			bodyReadLatency = int(time.Since(bodyReadStart).Milliseconds())
			switch {
			case readErr == nil:
				totalLatency += bodyReadLatency
				bodyBytes = data
			case isTolerableReadError(readErr):
				// 可容忍的传输错误（EOF、HTTP/2 流错误等），已读内容仍可用于匹配
				totalLatency += bodyReadLatency
				bodyBytes = data
				logger.Debug("probe", "读取响应体遇到可容忍错误，使用已读数据",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model, "error", readErr, "bytes", len(data))
			default:
				logger.Warn("probe", "读取响应体失败",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model, "error", readErr)
				totalLatency += bodyReadLatency // body 读取耗时计入 completion latency，无论成败
				// 读取响应体超时：仅 2xx 响应标记为 response_timeout，非 2xx 走正常 HTTP 状态分类
				if errors.Is(readErr, context.DeadlineExceeded) && resp.StatusCode >= 200 && resp.StatusCode < 300 {
					_ = resp.Body.Close()
					result.Status = 0
					result.SubStatus = storage.SubStatusResponseTimeout
					result.HttpCode = resp.StatusCode
					result.Latency = totalLatency
					result.Error = readErr
					lastBodyBytes = data
					if attempt+1 < maxAttempts {
						p.logFailedProbe(cfg, result, data, lastSuccessContains)
						delay := computeRetryDelay(attempt, baseDelay, maxDelay, jitter)
						logger.Info("probe", "探测失败，准备重试",
							"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
							"attempt", attempt+1, "next_attempt", attempt+2, "status", result.Status, "sub_status", result.SubStatus,
							"http_code", result.HttpCode, "delay_ms", delay.Milliseconds())
						select {
						case <-ctx.Done():
							break retryLoop
						case <-time.After(delay):
							continue
						}
					}
					break retryLoop
				}
			}

			// 内容解压：根据 Content-Encoding 处理 br/zstd/gzip/deflate
			// Go 的 http.Transport 在用户显式设置 Accept-Encoding 请求头时不会自动解压
			bodyBytes = decompressBodyIfNeeded(resp, bodyBytes, cfg.Provider, cfg.Service, cfg.Channel, cfg.Model)
		case shouldCaptureHTTPFailureBody(resp.StatusCode):
			bodyReadStart := time.Now()
			data, readErr := readBodyPrefixAndDrain(resp.Body, httpFailureBodyCaptureLimit)
			bodyReadLatency = int(time.Since(bodyReadStart).Milliseconds())
			totalLatency += bodyReadLatency
			bodyBytes = decompressBodyIfNeeded(resp, data, cfg.Provider, cfg.Service, cfg.Channel, cfg.Model)
			switch {
			case readErr == nil:
			case isTolerableReadError(readErr):
				logger.Debug("probe", "读取 HTTP 红态响应体遇到可容忍错误，使用已读数据",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model, "error", readErr, "bytes", len(data))
			default:
				logger.Warn("probe", "读取 HTTP 红态响应体失败",
					"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model, "error", readErr, "bytes", len(data))
			}
		default:
			drainStart := time.Now()
			_, _ = io.Copy(io.Discard, resp.Body)
			bodyReadLatency = int(time.Since(drainStart).Milliseconds())
			totalLatency += bodyReadLatency
		}
		_ = resp.Body.Close()

		// 保存响应体用于最终诊断
		lastBodyBytes = bodyBytes

		// 判定状态（先按 HTTP/延迟，再根据响应内容做二次判断）
		attemptLatency := headerLatency + bodyReadLatency
		status, subStatus := p.determineStatus(resp.StatusCode, attemptLatency, cfg.SlowLatencyDuration)
		result.Status = status
		result.SubStatus = subStatus
		result.Status, result.SubStatus = evaluateStatus(result.Status, result.SubStatus, bodyBytes, probeSuccessContains)
		result.Latency = totalLatency
		result.Error = nil

		// 检查是否需要重试
		// 重试条件：status=0（红色）且非超时
		if result.Status == 0 && attempt+1 < maxAttempts {
			// 输出诊断信息（重试前输出）
			p.logFailedProbe(cfg, result, bodyBytes, lastSuccessContains)

			delay := computeRetryDelay(attempt, baseDelay, maxDelay, jitter)
			logger.Info("probe", "探测失败，准备重试",
				"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
				"attempt", attempt+1, "next_attempt", attempt+2, "status", result.Status, "sub_status", result.SubStatus,
				"http_code", result.HttpCode, "delay_ms", delay.Milliseconds())

			// 等待退避时间，同时监听 context
			select {
			case <-ctx.Done():
				// 超时，停止重试，保留当前结果
				break retryLoop
			case <-time.After(delay):
				// 继续下一次重试
				continue
			}
		}

		// 成功（绿色/黄色）或已达最大重试次数，退出循环
		break retryLoop
	}

	// 最终诊断日志（仅在最终结果为红色时输出）
	if result.Status == 0 {
		// 输出诊断信息（使用保存的最后一次响应体）
		p.logFailedProbe(cfg, result, lastBodyBytes, lastSuccessContains)

		logger.Warn("probe", "探测最终失败",
			"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
			"actual_attempts", actualAttempts, "max_attempts", maxAttempts,
			"total_latency_ms", result.Latency, "status", result.Status, "sub_status", result.SubStatus,
			"http_code", result.HttpCode)
	}

	// 日志（不打印敏感信息）
	logArgs := []any{
		"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
		"code", result.HttpCode, "latency_ms", result.Latency, "status", result.Status, "sub_status", result.SubStatus,
	}
	if lastPrompt != "" {
		logArgs = append(logArgs, "prompt", lastPrompt, "expected", lastExpectedAnswer)
	}
	if len(lastBodyBytes) > 0 {
		snippet := ResponseSnippetText(lastBodyBytes)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "..."
		}
		if snippet != "" {
			logArgs = append(logArgs, "response", snippet)
		}
	}
	logger.Info("probe", "探测完成", logArgs...)

	// 组装失败响应摘要：仅 status=0 时写入，用于持久化排障
	if result.Status == 0 {
		result.ResponseSnippet = buildFailureSnippet(result, lastBodyBytes, lastSuccessContains)
	}

	return result
}

// maxErrorDetailLen 是非 content_mismatch 红态摘要的长度上限。
// content_mismatch 走结构化摘要、自带更细的分段预算，不受这个值约束。
const maxErrorDetailLen = 512

// buildFailureSnippet 组装写库的失败摘要。
//
// content_mismatch 单独走结构化摘要：它是唯一「HTTP 层完全正常、判红理由只存在于
// 响应内容里」的红态，原样截一段响应体说明不了任何事——SSE 流的开头恒为握手元数据。
// 其余红态的响应体本身就是上游的错误信息，头部即有效信息，保持原样。
func buildFailureSnippet(result *ProbeResult, body []byte, successContains string) string {
	if result.SubStatus == storage.SubStatusContentMismatch {
		return BuildContentMismatchSummary(body, successContains)
	}

	var snippet string
	if len(body) > 0 {
		snippet = ResponseSnippetText(body)
	} else if result.Error != nil {
		snippet = result.Error.Error()
	}
	return sanitizeForStorage(truncateHead(snippet, maxErrorDetailLen))
}

// logFailedProbe 输出探测失败的诊断信息
func (p *Prober) logFailedProbe(cfg *config.ServiceConfig, result *ProbeResult, bodyBytes []byte, successContains string) {
	const maxSnippetLen = 512 // 防止日志过长

	// content_mismatch 与写库摘要同源：日志和管理后台对同一次失败必须给出同一套说法，
	// 否则排障时两边对不上。响应体为空/无法提取文本的情况也由摘要自行表达。
	if result.SubStatus == storage.SubStatusContentMismatch {
		logger.Warn("probe", "内容校验失败",
			"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model,
			"body_bytes", len(bodyBytes), "summary", BuildContentMismatchSummary(bodyBytes, successContains))
	} else if len(bodyBytes) > 0 {
		// 其他红色状态：保持原有行为，在有响应体时输出片段
		snippet := ResponseSnippetText(bodyBytes)
		if snippet != "" {
			if len(snippet) > maxSnippetLen {
				snippet = snippet[:maxSnippetLen] + "... (truncated)"
			}
			logger.Warn("probe", "响应片段",
				"provider", cfg.Provider, "service", cfg.Service, "channel", cfg.Channel, "model", cfg.Model, "snippet", snippet)
		}
	}
}

// evaluateStatus 在基础状态上叠加响应内容匹配规则
// 对所有 2xx 响应（绿色和慢速黄色）进行内容校验
// 红色（已失败）和 429 黄色（非正常响应）不做校验。
// 为兼容流式响应，这里会尝试从 SSE/增量数据中聚合出最终文本再做匹配。
func evaluateStatus(baseStatus int, baseSubStatus storage.SubStatus, body []byte, successContains string) (int, storage.SubStatus) {
	if successContains == "" {
		return baseStatus, baseSubStatus
	}

	// 红色已是最差状态，不需要校验
	if baseStatus == 0 {
		return baseStatus, baseSubStatus
	}

	// 429 限流：响应体是错误信息，不做内容校验
	if baseStatus == 2 && baseSubStatus == storage.SubStatusRateLimit {
		return baseStatus, baseSubStatus
	}

	// 对 2xx 响应（绿色或慢速黄色）做内容校验
	text := AggregateResponseText(body)
	if strings.TrimSpace(text) == "" {
		// 没有响应内容，降级为红
		return 0, storage.SubStatusContentMismatch
	}

	if !strings.Contains(text, successContains) {
		// 未包含预期内容，认为请求语义失败
		return 0, storage.SubStatusContentMismatch
	}

	return baseStatus, baseSubStatus
}

// decompressBodyIfNeeded 根据 Content-Encoding 解压响应体，失败则保留原始数据。
// 支持 br/zstd/gzip/deflate，并保留 gzip/zstd 魔术头兜底处理。
func decompressBodyIfNeeded(resp *http.Response, data []byte, provider, service, channel, model string) []byte {
	if len(data) == 0 {
		return data
	}

	contentEncoding := strings.ToLower(resp.Header.Get("Content-Encoding"))

	switch {
	case strings.Contains(contentEncoding, "br"):
		return decompressBrotli(data, provider, service, channel, model, true)
	case strings.Contains(contentEncoding, "zstd"):
		return decompressZstd(data, provider, service, channel, model)
	case strings.Contains(contentEncoding, "gzip"):
		return decompressGzip(data, provider, service, channel, model)
	case strings.Contains(contentEncoding, "deflate"):
		return decompressDeflate(data, provider, service, channel, model)
	default:
		// 兜底：服务端未声明 Content-Encoding 时，检查魔术头
		if isGzipMagic(data) {
			return decompressGzip(data, provider, service, channel, model)
		}
		if isZstdMagic(data) {
			return decompressZstd(data, provider, service, channel, model)
		}
		if looksBinary(data) {
			return decompressBrotli(data, provider, service, channel, model, false)
		}
	}

	return data
}

func decompressBrotli(data []byte, provider, service, channel, model string, logError bool) []byte {
	reader := brotli.NewReader(bytes.NewReader(data))
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		if logError {
			logger.Warn("probe", "br 解压失败，使用原始响应体",
				"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		}
		return data
	}

	logger.Debug("probe", "br 解压成功",
		"provider", provider, "service", service, "channel", channel, "model", model,
		"compressed_size", len(data), "decompressed_size", len(decompressed))
	return decompressed
}

func decompressZstd(data []byte, provider, service, channel, model string) []byte {
	decoder, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		logger.Warn("probe", "zstd 解压初始化失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}
	defer decoder.Close()

	decompressed, err := io.ReadAll(decoder)
	if err != nil {
		logger.Warn("probe", "zstd 解压读取失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}

	logger.Debug("probe", "zstd 解压成功",
		"provider", provider, "service", service, "channel", channel, "model", model,
		"compressed_size", len(data), "decompressed_size", len(decompressed))
	return decompressed
}

func decompressGzip(data []byte, provider, service, channel, model string) []byte {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		logger.Warn("probe", "gzip 解压初始化失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}
	defer gr.Close()

	decompressed, err := io.ReadAll(gr)
	if err != nil {
		logger.Warn("probe", "gzip 解压读取失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}

	logger.Debug("probe", "gzip 解压成功",
		"provider", provider, "service", service, "channel", channel, "model", model,
		"compressed_size", len(data), "decompressed_size", len(decompressed))
	return decompressed
}

func decompressDeflate(data []byte, provider, service, channel, model string) []byte {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		logger.Warn("probe", "deflate 解压初始化失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}
	defer reader.Close()

	decompressed, err := io.ReadAll(reader)
	if err != nil {
		logger.Warn("probe", "deflate 解压读取失败，使用原始响应体",
			"provider", provider, "service", service, "channel", channel, "model", model, "error", err)
		return data
	}

	logger.Debug("probe", "deflate 解压成功",
		"provider", provider, "service", service, "channel", channel, "model", model,
		"compressed_size", len(data), "decompressed_size", len(decompressed))
	return decompressed
}

func isGzipMagic(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

func isZstdMagic(data []byte) bool {
	return len(data) >= 4 && data[0] == 0x28 && data[1] == 0xB5 && data[2] == 0x2F && data[3] == 0xFD
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	sample := data
	if len(sample) > 2048 {
		sample = sample[:2048]
	}

	var nonPrintable int
	for _, b := range sample {
		if b == 0x00 {
			return true
		}
		if b < 0x09 || (b > 0x0D && b < 0x20) {
			nonPrintable++
		}
	}

	return float64(nonPrintable)/float64(len(sample)) > 0.2
}

// determineStatus 根据HTTP状态码和延迟判定监测状态
func (p *Prober) determineStatus(statusCode, latency int, slowLatency time.Duration) (int, storage.SubStatus) {
	// 2xx = 绿色
	if statusCode >= 200 && statusCode < 300 {
		// 如果延迟超过 slowLatency，降级为黄色
		if slowLatency > 0 && latency > int(slowLatency/time.Millisecond) {
			return 2, storage.SubStatusSlowLatency
		}
		return 1, storage.SubStatusNone
	}

	// 3xx = 红色（不合规）。HTTP client 默认已自动跟随重定向（最多 10 跳），
	// 合规的 http→https / 尾斜杠跳转早被透明跟完，到这里只剩跟不动的畸形重定向
	// （无 Location 头、非法 scheme、超跳数等）——对 LLM API 而言这不是可用响应，
	// 与 inline 自助探测的 redirect_blocked 口径一致，归入 client_error 桶。
	if statusCode >= 300 && statusCode < 400 {
		return 0, storage.SubStatusClientError
	}

	// 401/403 = 红色（认证/权限失败）
	if statusCode == 401 || statusCode == 403 {
		return 0, storage.SubStatusAuthError
	}

	// 400 = 红色（请求参数错误）
	if statusCode == 400 {
		return 0, storage.SubStatusInvalidRequest
	}

	// 429 = 红色（速率限制，视为不可用）
	if statusCode == 429 {
		return 0, storage.SubStatusRateLimit
	}

	// 5xx = 红色（服务器错误，视为不可用）
	if statusCode >= 500 {
		return 0, storage.SubStatusServerError
	}

	// 其他4xx = 红色（客户端错误）
	if statusCode >= 400 {
		return 0, storage.SubStatusClientError
	}

	// 1xx 和其他非标准状态码 = 红色（客户端错误，因为 LLM API 不应返回这些）
	if statusCode >= 100 && statusCode < 200 {
		return 0, storage.SubStatusClientError
	}

	// 完全异常的状态码（< 100 或无法识别）
	return 0, storage.SubStatusClientError
}

// responseTextSource 说明内容校验最终拿什么在做匹配。
type responseTextSource string

const (
	// textSourceEmpty：响应体为空。
	textSourceEmpty responseTextSource = "empty"
	// textSourceSSE：从 SSE 流里抽到了模型正文。
	textSourceSSE responseTextSource = "sse_text"
	// textSourceBody：非流式响应，响应体本身就是匹配对象。
	textSourceBody responseTextSource = "body"
	// textSourceNone：确实是 SSE 流，但一个字正文都没抽到。
	//
	// 它必须与 textSourceBody 分开：2026-09-11 之前两者被合并成「回退到整包 body」，
	// 于是 success_contains 变成拿协议信封（事件名、request-id、usage、**思考摘要**）
	// 做 grep——模型正文一个字没输出，也可能被信封里恰好出现的关键字判绿。
	textSourceNone responseTextSource = "none"
)

// aggregateResponseText 给出内容校验应当匹配的文本及其来源。
func aggregateResponseText(body []byte) (string, responseTextSource) {
	if len(body) == 0 {
		return "", textSourceEmpty
	}

	if !looksLikeSSE(body) {
		// 非流式响应：响应体本身就是模型输出（或上游错误体），整体即匹配对象。
		return string(body), textSourceBody
	}

	text := ExtractTextFromSSE(body)
	if strings.TrimSpace(text) == "" {
		// ⚠️ 这里刻意**不回退整包 body**，理由见 textSourceNone。
		return "", textSourceNone
	}
	return text, textSourceSSE
}

// AggregateResponseText 是内容校验（success_contains）的匹配对象。
//
// ⚠️ SSE 流抽不到正文时返回空串，**不回退到整包响应体**。要「给人看」的片段
// 请用 ResponseSnippetText——同一份响应体，两个问题的正确答案在这一点上相反。
func AggregateResponseText(body []byte) string {
	text, _ := aggregateResponseText(body)
	return text
}

// ResponseSnippetText 给出**展示用**片段：抽得到正文就给正文，抽不到就退回响应体原文。
//
// 与匹配用的 AggregateResponseText 分开是必须的：日志与管理后台要的是「看懂发生了
// 什么」，此时上游的错误信封恰恰是关键信息；内容校验要的是「模型到底输出了什么」，
// 信封必须排除。此前两者共用一个函数，三个调用点各自手写
// `if snippet == "" { snippet = string(body) }` 找补，第四个（internal/probe/inline.go）
// 漏写——管理后台点一次探测就看不到上游错误。语义收进函数即不再有第五次漏写。
func ResponseSnippetText(body []byte) string {
	if text, source := aggregateResponseText(body); source == textSourceSSE || source == textSourceBody {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(string(body))
}

// looksLikeSSE 启发式判定响应体是否为 text/event-stream 风格：
// - 标准 SSE：同时包含 "event:" 和 "data:"
// - Gemini SSE：只有 "data:" 行（以 "data:" 开头或包含 "\ndata:"）
func looksLikeSSE(body []byte) bool {
	if bytes.Contains(body, []byte("event:")) && bytes.Contains(body, []byte("data:")) {
		return true
	}
	return bytes.HasPrefix(body, []byte("data:")) || bytes.Contains(body, []byte("\ndata:"))
}

// 承载模型正文的事件名。必须显式列出：Responses 协议把正文与**思考摘要**放在同名
// 同型的字段里（顶层 `delta` / 顶层 `text`），只看字段形状分不出来。
const (
	// OpenAI Responses 协议
	eventOutputTextDelta   = "response.output_text.delta"
	eventOutputTextDone    = "response.output_text.done"
	eventContentPartDone   = "response.content_part.done"
	eventOutputItemDone    = "response.output_item.done"
	eventResponseCompleted = "response.completed"

	// Anthropic Messages 协议。它的正文只出现在这一个事件里
	// （message_delta 的 delta 装的是 stop_reason，没有 text）。
	eventContentBlockDelta = "content_block_delta"
)

// sseTextCandidates 按来源分桶收集正文候选。
//
// 为什么不能像 2026-09-11 之前那样边扫边往同一个 builder 里写：Responses 协议会把
// 同一份正文分五种形态重复投递（delta 增量 / output_text.done / content_part.done /
// output_item.done / completed 快照），混在一起就是同一句话被拼好几遍；而「先到先得」
// （旧代码的 `b.Len() == 0`）又会让先到的半截 delta 挡住后到的完整快照。
// 分桶 + 只取一个，两个方向的错都不会发生。
type sseTextCandidates struct {
	completed strings.Builder // response.completed（整份响应快照，最完整）
	itemDone  strings.Builder // response.output_item.done
	partDone  strings.Builder // response.content_part.done
	textDone  strings.Builder // response.output_text.done
	textDelta strings.Builder // response.output_text.delta（增量，按到达顺序拼接）

	// nonResponses 装 Responses 之外的协议：Anthropic content_block_delta、
	// OpenAI Chat chunk、Gemini candidates，以及无事件类型的私有纯文本格式。
	// 它们**主要**靠字段结构识别，另受一道弱事件门（见写入处），
	// 以免未知私有事件借嵌套结构绕过 Responses 白名单。
	nonResponses strings.Builder
}

// pick 取最高优先级的非空候选。**绝不跨桶拼接**——各桶装的是同一段正文的不同
// 投递形态，拼起来等于把同一句话说好几遍。
func (c *sseTextCandidates) pick() string {
	for _, b := range []*strings.Builder{
		&c.completed, &c.itemDone, &c.partDone, &c.textDone, &c.textDelta, &c.nonResponses,
	} {
		if b.Len() > 0 {
			return b.String()
		}
	}
	return ""
}

// ExtractTextFromSSE 从 text/event-stream 风格的响应体中抽取**模型正文**。
// 支持的形态：
//   - Anthropic: event: content_block_delta + data: {"delta":{"type":"text_delta","text":"..."}}
//   - OpenAI Chat: data: {"choices":[{"delta":{"content":"..."}}]}
//   - OpenAI Responses: output_text.delta / output_text.done / content_part.done /
//     output_item.done / completed，见上方常量
//   - Gemini: data: {"candidates":[{"content":{"parts":[{"text":"..."}]}}]}
//     （Gemini SSE 没有 event: 行，且 text 可能被拆到多个 chunk，需累积拼接）
//
// ⚠️ **只有正文算数，思考摘要不算**。Responses 协议的 reasoning_summary_text.delta /
// .done 与正文事件用同名同型的字段传内容，2026-09-11 之前它们被一视同仁地累加，
// 于是「模型只在思考里写出答案、正文一个字没输出」也判绿——生产上 8 条火山方舟
// native 通道实测命中这条路径（reasoning done 恰好排在正文之前，把当时还空的
// builder 填满）。所以顶层 `delta` / `text` 一律需要事件类型授权。
func ExtractTextFromSSE(body []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	// 提升单行上限，避免极端情况下行太长
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var cand sseTextCandidates

	// SSE 的 event: 行在 data: 行之前，故需跨行记忆；空行是帧分隔符，作用域到此为止。
	//
	// ⚠️ 与 SSEDigest.LastEvent 的**粘滞**语义刻意不同：那里「最后一个叫得出名字的
	// 事件」是有用的摘要字段；这里必须是**当前帧**的类型，沿用上一帧会把 reasoning
	// 的内容记到 output 名下——那正是本函数要防的事。
	pendingEvent := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "" {
			pendingEvent = ""
			continue
		}
		if strings.HasPrefix(line, "event:") {
			pendingEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		event := pendingEvent

		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			// 私有的**纯文本** SSE 格式（`data: RP_ANSWER=127` 这种）仍当正文，
			// 但要同时排掉两类冒牌货：
			//   ① 带事件名的——那属于某个已知协议，正文该由该协议的规则决定；
			//   ② 看起来是 JSON 却解不出来的——那是被截断/损坏的结构化 payload。
			// ② 这道门补的是一处真实的不对称：`{"type":"error","message":"…"}` 完整时
			// 会被事件类型挡住，一旦在传输中被截断反而解析失败、整段错误体混进正文。
			// 判据用首字符而非「解析失败」本身——纯文本正文不会以 { 或 [ 开头。
			if event == "" && !looksLikeJSON(payload) {
				if cand.nonResponses.Len() > 0 {
					cand.nonResponses.WriteByte(' ')
				}
				cand.nonResponses.WriteString(payload)
			}
			continue
		}

		// payload 自带的 type 优先于 event: 行——它与 payload 同帧，不会被跨帧串味。
		if eventType, ok := obj["type"].(string); ok {
			if trimmed := strings.TrimSpace(eventType); trimmed != "" {
				event = trimmed
			}
		}

		// —— 以下三种靠字段结构识别，属于 Responses 之外的协议 ——
		//
		// 它们仍受一道**弱**事件门：类型为空（OpenAI Chat chunk 与 Gemini 本来就不带
		// type，也没有 event: 行）或恰是 Anthropic 的 content_block_delta 才采纳。
		// 没有这道门，`{"type":"codex.future","delta":{"text":"…"}}` 这类未来才出现的
		// 私有事件会绕过下面的 Responses 白名单，把非正文写进正文桶——「未知事件不
		// 贡献正文」那条原则就只对顶层字段成立、对嵌套结构不成立了。
		// 111 份真实样本里「带 type 且非 content_block_delta、却携嵌套正文结构」为 0 条，
		// 故这道门当前零兼容代价。
		if event == "" || event == eventContentBlockDelta {
			// Anthropic: {"type":"content_block_delta","delta":{"type":"text_delta","text":"..."}}
			if delta, ok := obj["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok {
					cand.nonResponses.WriteString(text)
				}
			}

			// OpenAI Chat: {"choices":[{"delta":{"content":"..."}}]}
			if choices, ok := obj["choices"].([]any); ok {
				for _, ch := range choices {
					chMap, ok := ch.(map[string]any)
					if !ok {
						continue
					}
					delta, ok := chMap["delta"].(map[string]any)
					if !ok {
						continue
					}
					if text, ok := delta["content"].(string); ok {
						cand.nonResponses.WriteString(text)
					}
				}
			}

			// Gemini: {"candidates":[{"content":{"parts":[{"text":"..."}]}}]}
			if candidates, ok := obj["candidates"].([]any); ok {
				for _, c := range candidates {
					cm, ok := c.(map[string]any)
					if !ok {
						continue
					}
					content, ok := cm["content"].(map[string]any)
					if !ok {
						continue
					}
					parts, ok := content["parts"].([]any)
					if !ok {
						continue
					}
					for _, p := range parts {
						pm, ok := p.(map[string]any)
						if !ok {
							continue
						}
						if text, ok := pm["text"].(string); ok {
							cand.nonResponses.WriteString(text)
						}
					}
				}
			}
		}

		// —— 以下是 Responses 协议：字段本身有歧义，一律由事件类型授权 ——
		// 未知事件类型（含将来新增的 codex.* / responsesapi.* 一类私有事件）在这里
		// 走 default，在上面那道弱事件门也进不去 nonResponses——**两条路都不贡献
		// 正文**，这条原则因此对顶层字段与嵌套结构同时成立。
		// 宁可抽不到判红，也不能把非正文当正文判绿。
		switch event {
		case eventOutputTextDelta:
			if delta, ok := obj["delta"].(string); ok {
				cand.textDelta.WriteString(delta)
			}
		case eventOutputTextDone:
			if text, ok := obj["text"].(string); ok {
				cand.textDone.WriteString(text)
			}
		case eventContentPartDone:
			appendContentText(obj["part"], &cand.partDone)
		case eventOutputItemDone:
			if item, ok := obj["item"].(map[string]any); ok {
				appendContentTexts(item["content"], &cand.itemDone)
			}
		case eventResponseCompleted:
			// ⚠️ 只认 status=completed 的快照。它在 pick 里优先级最高，若上游发来的是
			// incomplete/failed 的**残缺**快照，采纳它就会盖掉本来完整的 delta，
			// 把一次成功的探测判成红。实测 49 条 completed 快照全是 status=completed，
			// 故这道门当前零代价；它防的是残缺快照压过完整增量这一类误红。
			if response, ok := obj["response"].(map[string]any); ok {
				if status, ok := response["status"].(string); ok && status != "completed" {
					break
				}
				if output, ok := response["output"].([]any); ok {
					for _, rawItem := range output {
						item, ok := rawItem.(map[string]any)
						if !ok {
							continue
						}
						appendContentTexts(item["content"], &cand.completed)
					}
				}
			}
		}

		// 私有格式的顶层正文字段：同样只在本帧没有事件类型时采纳。
		// 正是这个条件挡住了 `{"type":"error","message":"Our servers are currently
		// overloaded…"}`——它的 type 非空，走不进来。旧实现没有这道门，于是上游错误
		// 信封被当成正文，摘要印出 `extracted=61chars` 而模型一个字都没输出。
		if event == "" {
			if content, ok := obj["content"].(string); ok {
				cand.nonResponses.WriteString(content)
			}
			if msg, ok := obj["message"].(string); ok {
				cand.nonResponses.WriteString(msg)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// 扫描出错时，尽量返回已抽到的内容；彻底失败交由上层按「抽不到」处理。
		_ = err
	}

	return cand.pick()
}

// looksLikeJSON 判断这段 payload 是否**打算**是 JSON（哪怕解析失败）。
// 用于把「被截断的结构化 payload」与「私有的纯文本正文」区分开。
func looksLikeJSON(payload string) bool {
	return strings.HasPrefix(payload, "{") || strings.HasPrefix(payload, "[")
}

// appendContentTexts 收 `content: [{"text": "..."}]` 这种列表形态的正文。
func appendContentTexts(v any, dst *strings.Builder) {
	contents, ok := v.([]any)
	if !ok {
		return
	}
	for _, raw := range contents {
		appendContentText(raw, dst)
	}
}

// appendContentText 收单个 content part 的 text 字段。
func appendContentText(v any, dst *strings.Builder) {
	part, ok := v.(map[string]any)
	if !ok {
		return
	}
	if text, ok := part["text"].(string); ok {
		dst.WriteString(text)
	}
}

// SaveResult 保存探测结果到存储
// 返回保存后的记录（包含生成的 ID）和错误
func (p *Prober) SaveResult(result *ProbeResult) (*storage.ProbeRecord, error) {
	record := &storage.ProbeRecord{
		Provider:    result.Provider,
		Service:     result.Service,
		Channel:     result.Channel,
		Model:       result.Model,
		ModelID:     result.ModelID,
		Status:      result.Status,
		SubStatus:   result.SubStatus,
		HttpCode:    result.HttpCode,
		Latency:     result.Latency,
		Timestamp:   result.Timestamp,
		ErrorDetail: result.ResponseSnippet,
	}

	if err := p.storage.SaveRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

// Close 关闭探测器
func (p *Prober) Close() {
	p.clientPool.Close()
}

// MaskSensitiveInfo 脱敏敏感信息（用于日志）
func MaskSensitiveInfo(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	// 只显示前4位和后4位
	return s[:4] + "***" + s[len(s)-4:]
}

// isTolerableReadError 判断是否为可容忍的响应体读取错误
// 部分中转服务器实现不严谨，可能在响应内容已完整返回后仍触发以下错误：
// - io.EOF / io.ErrUnexpectedEOF: Content-Length 不匹配或 chunked encoding 未正确终止
// - HTTP/2 stream error: 服务端发送 RST_STREAM 帧提前关闭流
// - HTTP/2 连接关闭: GOAWAY 帧或连接被关闭
// 这些情况下已读取的数据通常仍可用于内容匹配
func isTolerableReadError(err error) bool {
	if err == nil {
		return false
	}

	// 标准 EOF 错误
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	// HTTP/2 相关错误（通过错误消息匹配，因为 net/http2 未导出具体错误类型）
	errStr := err.Error()

	// HTTP/2 流错误：必须包含 "stream error:" 前缀，且带有常见错误码
	if strings.Contains(errStr, "stream error:") {
		// 常见的 HTTP/2 流错误码，这些通常表示服务端提前关闭而非真正的传输失败
		tolerableCodes := []string{
			"INTERNAL_ERROR",
			"CANCEL",
			"NO_ERROR",
			"PROTOCOL_ERROR",
			"REFUSED_STREAM",
		}
		for _, code := range tolerableCodes {
			if strings.Contains(errStr, code) {
				return true
			}
		}
	}

	// HTTP/2 连接级错误
	if strings.Contains(errStr, "GOAWAY") ||
		strings.Contains(errStr, "http2: response body closed") ||
		strings.Contains(errStr, "http2: server sent GOAWAY") {
		return true
	}

	return false
}

// computeRetryDelay 计算指数退避延迟
// 公式: delay = min(maxDelay, baseDelay * 2^retryIndex) * (1 ± jitter)
// retryIndex 从 0 开始（首次重试 retryIndex=0）
// 注意：最终结果会再次 cap 到 maxDelay，确保不超过上限
func computeRetryDelay(retryIndex int, baseDelay, maxDelay time.Duration, jitter float64) time.Duration {
	// 指数退避：baseDelay * 2^retryIndex
	delay := baseDelay
	for i := 0; i < retryIndex; i++ {
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
			break
		}
	}

	// 上限保护
	if delay > maxDelay {
		delay = maxDelay
	}

	// 应用抖动：delay * (1 ± jitter)
	if jitter > 0 {
		// 生成 [-jitter, +jitter] 范围的随机偏移
		jitterRange := float64(delay) * jitter
		offset := (rand.Float64()*2 - 1) * jitterRange // [-jitterRange, +jitterRange]
		delay = time.Duration(float64(delay) + offset)
	}

	// 确保延迟不为负
	if delay < 0 {
		delay = baseDelay
	}

	// 抖动后再次 cap 到 maxDelay，确保最终结果不超过上限
	if delay > maxDelay {
		delay = maxDelay
	}

	return delay
}

func shouldCaptureHTTPFailureBody(statusCode int) bool {
	return statusCode >= 400 && statusCode < 600
}

type limitedBodyCaptureWriter struct {
	limit int
	buf   bytes.Buffer
}

func (w *limitedBodyCaptureWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = w.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func readBodyPrefixAndDrain(r io.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, nil
	}
	capture := &limitedBodyCaptureWriter{limit: limit}
	_, err := io.Copy(capture, r)
	return capture.buf.Bytes(), err
}

// drainAndClose 排空响应体并关闭，便于连接复用
// 当重试时需要释放上一次响应的资源
func drainAndClose(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	// 限制读取量，防止恶意大响应阻塞
	const maxDrainBytes = 64 * 1024
	_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)
	_ = resp.Body.Close()
}
