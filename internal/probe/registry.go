package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// templatesDir 存储 templates/ 目录的路径（由启动流程初始化时设置）。
var (
	templatesDir     string
	templatesDirOnce sync.Once
)

// SetTemplatesDir 设置模板目录路径。
func SetTemplatesDir(dir string) {
	templatesDirOnce.Do(func() {
		templatesDir = dir
		logger.Info("probe", "模板目录已设置", "path", templatesDir)
	})
}

// PayloadVariant 描述请求体模板的一个变体。
//
// Model / SuccessContains 是**调用方的 runtime 覆盖值**（TemplateBuilder.Build 里非空即压过模板
// 自身声明），下面的 Template* 一组则是 InitTemplates 从模板文件读出的**只读元数据快照**。
// 两者语义相反，绝不能合并成一个字段：把模板值塞进 Model 会让「覆盖」退化成「缓存」，模板
// 换了内容而缓存还在时，探测发出的模型就与磁盘上的模板不一致。
type PayloadVariant struct {
	ID              string `json:"id"`
	Filename        string `json:"filename"`
	Order           int    `json:"order"`
	Model           string `json:"model,omitempty"`
	SuccessContains string `json:"success_contains,omitempty"`

	// TemplateModel 模板声明的展示模型名（如 "Haiku"）；native 族为空。
	TemplateModel string `json:"-"`
	// TemplateRequestModel 模板声明的真实请求模型 ID（如 "claude-haiku-4-5-20251001"）；native 族为空。
	TemplateRequestModel string `json:"-"`
	// TemplateVendor 模板声明的模型厂商 code；native 族为空（厂商由监测行填）。
	TemplateVendor string `json:"-"`
	// TemplateLabel 模板给自助收录用的人话名；为空时由调用方兜底。
	TemplateLabel string `json:"-"`
	// Native 标记本模板属于「第一方厂商通用模板」族，即模型必须由监测行填写
	// （admin 写路径；本族自 2026-08-21 起对自助收录一律不可见）。
	Native bool `json:"-"`
	// SelfServeVisible 标记本模板可否出现在自助收录的可选模型里。**模板读不动时按可见处理**：
	// 拿不到声明不等于作者想隐藏它，而静默隐藏会让一次坏部署悄悄缩短公开可选项。
	SelfServeVisible bool `json:"-"`
}

// TestConfigBuilder 用于根据测试类型构建探测配置。
type TestConfigBuilder interface {
	Build(apiURL, apiKey string, variant *PayloadVariant) (*config.ServiceConfig, error)
}

// TestType 表示一种可执行的探测类型。
type TestType struct {
	ID             string
	Name           string
	Description    string
	DefaultVariant string
	Variants       []*PayloadVariant
	Builder        TestConfigBuilder
}

// ResolveVariant 根据 variantID 解析 payload 变体；空 ID 回退到默认变体。
func (t *TestType) ResolveVariant(variantID string) (*PayloadVariant, error) {
	id := strings.TrimSpace(variantID)
	if id == "" {
		id = t.DefaultVariant
	}
	if id == "" {
		return nil, fmt.Errorf("default payload variant not set for test type: %s", t.ID)
	}

	for _, v := range t.Variants {
		// nil 守卫与 LookupVariant 对称：Variants 由外部注册（含测试），一条 nil 会让这里 panic。
		if v != nil && v.ID == id {
			return v, nil
		}
	}

	return nil, fmt.Errorf("不支持的 payload 变体: %q", id)
}

// testTypeRegistry 全局注册表。
var (
	registryMu       sync.RWMutex
	testTypeRegistry = make(map[string]*TestType)
)

// RegisterTestType 在全局注册表中注册探测类型。
func RegisterTestType(t *TestType) {
	registryMu.Lock()
	testTypeRegistry[t.ID] = t
	registryMu.Unlock()
}

// UnregisterTestType 从全局注册表中移除探测类型；ID 不存在时是空操作。
//
// 与 RegisterTestType 对称。注册表是包级全局的，跨包的测试注册了合成 service 后若没有真正的
// 移除手段，只能"再注册一个空壳"来假装清理——空壳会永久留在 ListTestTypes 里，被
// /api/onboarding/meta 一路下发给前端，污染后续任何断言 service 集合的测试。
func UnregisterTestType(id string) {
	registryMu.Lock()
	delete(testTypeRegistry, id)
	registryMu.Unlock()
}

// GetTestType 根据 ID 获取探测类型。
func GetTestType(id string) (*TestType, bool) {
	registryMu.RLock()
	t, ok := testTypeRegistry[id]
	registryMu.RUnlock()
	return t, ok
}

// LookupVariant 按「service + 模板名」查已注册变体，返回值拷贝（调用方改不到注册表）。
//
// 这是「这个模板名对这条 service 合法吗」的唯一判定：跨 service 引用（cc 的提交里写
// gm-flash-arith）一律未命中。别改用「按文件是否存在」判断——那是另一套口径，会放行跨
// service 模板，且把模板名当路径片段用。
func LookupVariant(service, variantID string) (PayloadVariant, bool) {
	id := strings.TrimSpace(variantID)
	if id == "" {
		return PayloadVariant{}, false
	}

	registryMu.RLock()
	defer registryMu.RUnlock()

	t, ok := testTypeRegistry[strings.TrimSpace(service)]
	if !ok {
		return PayloadVariant{}, false
	}
	for _, v := range t.Variants {
		if v != nil && v.ID == id {
			return *v, true
		}
	}
	return PayloadVariant{}, false
}

// ListTestTypes 返回所有已注册探测类型，按 ID 排序。
func ListTestTypes() []*TestType {
	registryMu.RLock()
	types := make([]*TestType, 0, len(testTypeRegistry))
	for _, t := range testTypeRegistry {
		types = append(types, t)
	}
	registryMu.RUnlock()

	sort.Slice(types, func(i, j int) bool {
		return types[i].ID < types[j].ID
	})
	return types
}

// InitTemplates 扫描 templates/ 目录，按文件名约定（{service}-*.json）
// 动态填充已注册 TestType 的 Variants 和 DefaultVariant。
//
// DefaultVariant 取模板自己声明的 `"self_serve_default": true`（每个 service 至多一份）；
// 一份都没声明时回退到字典序第一个——即本函数的历史行为，自建部署带自己的模板目录时零影响。
//
// 这个默认值决定了自助收录第二步与变更请求测试步**首次探测打哪个模型**，因此不能是文件名
// 字典序的副产物：2026-08-06 新增 `cc-fable-ping-20260806`（`cc-f…` 排在 `cc-h…` 前）就把
// cc 的默认目标从 haiku 换成了几乎无中转商提供的 fable-5，申请人与老通道一律测不过、卡在
// 测试步无法提交。可选项不受影响（下拉仍是全部模板，Order 仍是纯字典序），只有默认值被钉死。
func InitTemplates(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("读取模板目录失败 %s: %w", dir, err)
	}

	grouped := make(map[string][]*PayloadVariant)
	declaredDefaults := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		filename := entry.Name()
		variantID := strings.TrimSuffix(filename, ".json")

		idx := strings.IndexByte(variantID, '-')
		if idx <= 0 {
			continue
		}
		service := variantID[:idx]

		variant := &PayloadVariant{
			ID:       variantID,
			Filename: filename,
			Native:   config.IsNativeProbeTemplate(variantID),
			// 读不动的模板仍按可见处理，见字段注释
			SelfServeVisible: true,
		}
		grouped[service] = append(grouped[service], variant)

		// 读不动的模板只是拿不到声明，仍留在可选列表里：踢掉它会让一次坏部署静默缩短
		// 公开可选项，而选中它本就会在内联探测处返回可读的解析错误。
		tmpl, loadErr := config.LoadProbeTemplate(filepath.Join(dir, filename))
		if loadErr != nil {
			logger.Warn("probe", "模板加载失败，跳过其自助元数据", "template", variantID, "error", loadErr)
			continue
		}
		variant.TemplateModel = tmpl.Model
		variant.TemplateRequestModel = tmpl.RequestModel
		variant.TemplateVendor = tmpl.ModelVendor
		variant.TemplateLabel = tmpl.SelfServeLabel
		variant.SelfServeVisible = tmpl.SelfServeVisible
		if tmpl.SelfServeDefault {
			declaredDefaults[service] = append(declaredDefaults[service], variantID)
		}
	}

	defaultVariants := make(map[string]string, len(grouped))
	for service, variants := range grouped {
		sort.Slice(variants, func(i, j int) bool {
			return variants[i].ID < variants[j].ID
		})
		for i := range variants {
			variants[i].Order = i + 1
		}

		declared := declaredDefaults[service]
		sort.Strings(declared)
		switch {
		case len(declared) == 0:
			defaultVariants[service] = variants[0].ID
		default:
			defaultVariants[service] = declared[0]
			if len(declared) > 1 {
				// 多份声明属配置事故，但默认值必须确定：取字典序第一个并留痕。
				logger.Warn("probe", "同一 service 声明了多个自助默认模板，取字典序第一个",
					"service", service, "declared", strings.Join(declared, ","),
					"chosen", declared[0])
			}
		}
	}

	registryMu.Lock()
	next := make(map[string]*TestType, len(testTypeRegistry))
	totalVariants := 0
	for id, current := range testTypeRegistry {
		updated := *current
		if variants, ok := grouped[id]; ok && len(variants) > 0 {
			updated.Variants = variants
			updated.DefaultVariant = defaultVariants[id]
			totalVariants += len(variants)
		} else {
			updated.Variants = nil
			updated.DefaultVariant = ""
		}
		next[id] = &updated
	}
	testTypeRegistry = next
	registryMu.Unlock()

	logger.Info("probe", "探测模板已刷新",
		"templates_dir", dir, "variants", totalVariants,
		"defaults", formatDefaults(defaultVariants))
	return nil
}

// formatDefaults 把「service → 默认模板」拍成确定性的一行日志值（如 "cc=cc-haiku-arith cx=..."），
// 供部署后直接从日志核对默认探测目标有没有被新模板挤掉。
func formatDefaults(defaults map[string]string) string {
	services := make([]string, 0, len(defaults))
	for service := range defaults {
		services = append(services, service)
	}
	sort.Strings(services)

	pairs := make([]string, 0, len(services))
	for _, service := range services {
		pairs = append(pairs, service+"="+defaults[service])
	}
	return strings.Join(pairs, " ")
}

// TemplateBuilder 从 templates/ 目录加载 JSON 模板构建探测配置。
type TemplateBuilder struct {
	Service string
}

// Build 根据模板和变体构建内部探测配置。
func (b *TemplateBuilder) Build(apiURL, apiKey string, variant *PayloadVariant) (*config.ServiceConfig, error) {
	if apiURL == "" {
		return nil, fmt.Errorf("api_url is required")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("api_key is required")
	}
	if variant == nil || variant.Filename == "" {
		return nil, fmt.Errorf("payload variant is required")
	}
	if templatesDir == "" {
		return nil, fmt.Errorf("templates directory not set, call SetTemplatesDir first")
	}

	tmpl, err := config.LoadProbeTemplate(filepath.Join(templatesDir, variant.Filename))
	if err != nil {
		return nil, fmt.Errorf("failed to load template %s: %w", variant.Filename, err)
	}

	headers := make(map[string]string, len(tmpl.Headers))
	for k, v := range tmpl.Headers {
		headers[k] = v
	}

	successContains := tmpl.SuccessContains
	if variant.SuccessContains != "" {
		successContains = variant.SuccessContains
	}

	model := variant.Model
	if model == "" {
		model = tmpl.Model
	}
	requestModel := tmpl.RequestModel

	slowLatency := 5 * time.Second
	if tmpl.SlowLatency != "" {
		if d, err := time.ParseDuration(tmpl.SlowLatency); err == nil && d > 0 {
			slowLatency = d
		}
	}

	timeout := 10 * time.Second
	if tmpl.Timeout != "" {
		if d, err := time.ParseDuration(tmpl.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	return &config.ServiceConfig{
		Provider:            "probe",
		Service:             b.Service,
		BaseURL:             apiURL,
		APIKey:              apiKey,
		Model:               model,
		RequestModel:        requestModel,
		URLPattern:          tmpl.URL,
		Method:              tmpl.Method,
		Headers:             headers,
		Body:                string(tmpl.BodyRaw),
		SuccessContains:     successContains,
		SlowLatencyDuration: slowLatency,
		TimeoutDuration:     timeout,
	}, nil
}

// init 注册内置探测类型元数据。
// Variants 会在 InitTemplates 之后动态填充。
func init() {
	RegisterTestType(&TestType{
		ID:      "cc",
		Name:    "Claude Code (cc)",
		Builder: &TemplateBuilder{Service: "cc"},
	})

	RegisterTestType(&TestType{
		ID:      "cx",
		Name:    "Codex (cx)",
		Builder: &TemplateBuilder{Service: "cx"},
	})

	RegisterTestType(&TestType{
		ID:      "gm",
		Name:    "Gemini (gm)",
		Builder: &TemplateBuilder{Service: "gm"},
	})
}
