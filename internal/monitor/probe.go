package monitor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// ProbeResult 探测结果
type ProbeResult struct {
	Provider  string
	Service   string
	Channel   string
	Status    int               // 1=绿, 0=红, 2=黄
	SubStatus storage.SubStatus // 细分状态（黄色/红色原因）
	Latency   int               // ms
	Timestamp int64
	Error     error
}

// Prober 探测器
type Prober struct {
	clientPool *ClientPool
	storage    storage.Storage
}

// NewProber 创建探测器
func NewProber(storage storage.Storage) *Prober {
	return &Prober{
		clientPool: NewClientPool(),
		storage:    storage,
	}
}

// Probe 执行单次探测
func (p *Prober) Probe(ctx context.Context, cfg *config.ServiceConfig) *ProbeResult {
	result := &ProbeResult{
		Provider:  cfg.Provider,
		Service:   cfg.Service,
		Channel:   cfg.Channel,
		Timestamp: time.Now().Unix(),
		SubStatus: storage.SubStatusNone,
	}

	// 准备请求体
	reqBody := bytes.NewBuffer([]byte(cfg.Body))
	req, err := http.NewRequestWithContext(ctx, cfg.Method, cfg.URL, reqBody)
	if err != nil {
		result.Error = fmt.Errorf("创建请求失败: %w", err)
		result.Status = 0
		result.SubStatus = storage.SubStatusNetworkError
		return result
	}

	// 设置Headers（已处理过占位符）
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	// 获取对应provider的客户端
	client := p.clientPool.GetClient(cfg.Provider)

	// 发送请求并计时
	start := time.Now()
	resp, err := client.Do(req)
	latency := int(time.Since(start).Milliseconds())
	result.Latency = latency

	if err != nil {
		log.Printf("[Probe] ERROR %s-%s-%s: %v", cfg.Provider, cfg.Service, cfg.Channel, err)
		result.Error = err
		result.Status = 0
		result.SubStatus = storage.SubStatusNetworkError
		return result
	}
	defer resp.Body.Close()

	// 完整读取响应体（避免连接泄漏），在需要内容匹配时保留文本
	var bodyBytes []byte
	if cfg.SuccessContains != "" {
		if data, readErr := io.ReadAll(resp.Body); readErr == nil {
			bodyBytes = data
		} else {
			log.Printf("[Probe] 读取响应体失败 %s-%s-%s: %v", cfg.Provider, cfg.Service, cfg.Channel, readErr)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}

	// 判定状态（先按 HTTP/延迟，再根据响应内容做二次判断）
	status, subStatus := p.determineStatus(resp.StatusCode, latency, cfg.SlowLatencyDuration)
	result.Status = status
	result.SubStatus = subStatus
	result.Status, result.SubStatus = evaluateStatus(result.Status, result.SubStatus, bodyBytes, cfg.SuccessContains)

	// 日志（不打印敏感信息）
	log.Printf("[Probe] %s-%s-%s | Code: %d | Latency: %dms | Status: %d | SubStatus: %s",
		cfg.Provider, cfg.Service, cfg.Channel, resp.StatusCode, latency, result.Status, result.SubStatus)

	return result
}

// evaluateStatus 在基础状态上叠加响应内容匹配规则
func evaluateStatus(baseStatus int, baseSubStatus storage.SubStatus, body []byte, successContains string) (int, storage.SubStatus) {
	if successContains == "" {
		return baseStatus, baseSubStatus
	}
	if baseStatus != 1 {
		// 只有在 HTTP 判定为"绿"时才用内容做二次校验
		return baseStatus, baseSubStatus
	}

	if len(body) == 0 {
		// 没有响应内容，降级为红
		return 0, storage.SubStatusContentMismatch
	}

	if !strings.Contains(string(body), successContains) {
		// 未包含预期内容，认为请求语义失败
		return 0, storage.SubStatusContentMismatch
	}

	return baseStatus, baseSubStatus
}

// determineStatus 根据HTTP状态码和延迟判定监控状态
func (p *Prober) determineStatus(statusCode, latency int, slowLatency time.Duration) (int, storage.SubStatus) {
	// 2xx = 绿色
	if statusCode >= 200 && statusCode < 300 {
		// 如果延迟超过 slowLatency，降级为黄色
		if slowLatency > 0 && latency > int(slowLatency/time.Millisecond) {
			return 2, storage.SubStatusSlowLatency
		}
		return 1, storage.SubStatusNone
	}

	// 3xx = 绿色（重定向，通常由客户端自动处理，视为正常）
	if statusCode >= 300 && statusCode < 400 {
		return 1, storage.SubStatusNone
	}

	// 400/401/403 = 灰色（未配置/认证失败）
	if statusCode == 400 || statusCode == 401 || statusCode == 403 {
		return 3, storage.SubStatusNone
	}

	// 429 = 黄色（速率限制，提醒慢下来）
	if statusCode == 429 {
		return 2, storage.SubStatusRateLimit
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

// SaveResult 保存探测结果到存储
func (p *Prober) SaveResult(result *ProbeResult) error {
	record := &storage.ProbeRecord{
		Provider:  result.Provider,
		Service:   result.Service,
		Channel:   result.Channel,
		Status:    result.Status,
		SubStatus: result.SubStatus,
		Latency:   result.Latency,
		Timestamp: result.Timestamp,
	}

	return p.storage.SaveRecord(record)
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
