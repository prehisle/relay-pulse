package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"monitor/internal/logger"
)

// ProbeTemplate 描述一次探测请求的完整模板（来自 templates/*.json）
type ProbeTemplate struct {
	Model           string            // 模型系列名（展示/DB 键）
	RequestModel    string            // 实际请求模型 ID（可选，为空时回退 Model）
	ModelVendor     string            // 模型厂商受控 code（可选，见 internal/modelvendor；监测行可覆盖）
	URL             string            // URL 模式，支持 {{BASE_URL}} 等占位符
	Method          string            // HTTP 方法
	Headers         map[string]string // 请求头，支持占位符
	BodyRaw         json.RawMessage   // body 原始 JSON 对象
	SuccessContains string            // 响应校验关键字，支持 {{EXPECTED_ANSWER}}
	SlowLatency     string            // 慢请求阈值（可选，如 "4s"）
	Timeout         string            // 超时时间（可选，如 "10s"）
	Retry           *int              // 额外重试次数（*int 区分 nil vs 0）
	RetryBaseDelay  string            // 退避基准间隔（可选，如 "200ms"）
	RetryMaxDelay   string            // 退避最大间隔（可选，如 "2s"）
	RetryJitter     *float64          // 抖动比例（*float64 区分 nil vs 0）
}

// probeTemplateFile 是模板 JSON 文件的解析结构
type probeTemplateFile struct {
	Model        string            `json:"model"`
	RequestModel string            `json:"request_model"`
	ModelVendor  string            `json:"model_vendor"`
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Headers      map[string]string `json:"headers"`
	Body         json.RawMessage   `json:"body"`
	Response     struct {
		SuccessContains string `json:"success_contains"`
	} `json:"response"`
	Probe struct {
		SlowLatency    string   `json:"slow_latency"`
		Timeout        string   `json:"timeout"`
		Retry          *int     `json:"retry"`
		RetryBaseDelay string   `json:"retry_base_delay"`
		RetryMaxDelay  string   `json:"retry_max_delay"`
		RetryJitter    *float64 `json:"retry_jitter"`
	} `json:"probe"`
}

// isNativeProbeTemplate 判定模板是否属于「第一方厂商通用模板」族（命名约定 <service>-native-*，
// 如 cc-native-arith / cx-native-arith）。
//
// 这族模板服务于厂商自家模型的 Anthropic/OpenAI 兼容端点，是**厂商无关**的：它们刻意不声明
// model / request_model / model_vendor，三者都必须由监测行按厂商填写。若模板声明了 vendor，
// 行级漏填时会经 resolveTemplateForMonitor 的 config > template 回退链静默继承成错误厂商。
//
// 以文件名判定而非模板内新增字段，是沿用本仓库既有的「模板文件名 load-bearing」约定
// （internal/api/monitor_handler.go 就按 service_type + "-" 前缀过滤 admin 模板列表）。
// 判定集中在此一处，避免前缀字符串散落各调用点。
// 要求恰好三段且首尾非空：`cc-native` / `cc-native-` 不符合约定，判它们为 native 会让
// 一个命名不规范的模板悄悄套上「必须行级填厂商」的语义。
func isNativeProbeTemplate(templateName string) bool {
	segments := strings.SplitN(strings.TrimSpace(templateName), "-", 3)
	return len(segments) == 3 && segments[0] != "" && segments[1] == "native" && segments[2] != ""
}

// LoadProbeTemplate 从 JSON 文件加载探测模板
func LoadProbeTemplate(filePath string) (*ProbeTemplate, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取模板文件失败 %s: %w", filePath, err)
	}

	var parsed probeTemplateFile
	if err := json.Unmarshal(content, &parsed); err != nil {
		return nil, fmt.Errorf("解析模板 JSON 失败 %s: %w", filePath, err)
	}

	tmpl := &ProbeTemplate{
		Model:           strings.TrimSpace(parsed.Model),
		RequestModel:    strings.TrimSpace(parsed.RequestModel),
		ModelVendor:     strings.TrimSpace(parsed.ModelVendor),
		URL:             strings.TrimSpace(parsed.URL),
		Method:          strings.TrimSpace(parsed.Method),
		Headers:         parsed.Headers,
		BodyRaw:         parsed.Body,
		SuccessContains: strings.TrimSpace(parsed.Response.SuccessContains),
		SlowLatency:     strings.TrimSpace(parsed.Probe.SlowLatency),
		Timeout:         strings.TrimSpace(parsed.Probe.Timeout),
		Retry:           parsed.Probe.Retry,
		RetryBaseDelay:  strings.TrimSpace(parsed.Probe.RetryBaseDelay),
		RetryMaxDelay:   strings.TrimSpace(parsed.Probe.RetryMaxDelay),
		RetryJitter:     parsed.Probe.RetryJitter,
	}

	if tmpl.Method == "" {
		return nil, fmt.Errorf("模板 %s 未配置 method", filePath)
	}

	logger.Info("config", "模板加载完成", "path", filePath)
	return tmpl, nil
}
