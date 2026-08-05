package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"monitor/internal/logger"
)

// ApplyEnvOverrides 应用环境变量覆盖
// API Key 格式：MONITOR_<PROVIDER>_<SERVICE>_<CHANNEL>_API_KEY（优先）或 MONITOR_<PROVIDER>_<SERVICE>_API_KEY（向后兼容）
// 存储配置格式：MONITOR_STORAGE_TYPE, MONITOR_POSTGRES_HOST 等
func (c *AppConfig) applyEnvOverrides() {
	// PublicBaseURL 环境变量覆盖
	if envBaseURL := os.Getenv("MONITOR_PUBLIC_BASE_URL"); envBaseURL != "" {
		c.PublicBaseURL = envBaseURL
	}

	// 存储配置环境变量覆盖
	if envType := os.Getenv("MONITOR_STORAGE_TYPE"); envType != "" {
		c.Storage.Type = envType
	}

	// PostgreSQL 配置环境变量覆盖
	if envHost := os.Getenv("MONITOR_POSTGRES_HOST"); envHost != "" {
		c.Storage.Postgres.Host = envHost
	}
	if envPort := os.Getenv("MONITOR_POSTGRES_PORT"); envPort != "" {
		port, err := strconv.Atoi(strings.TrimSpace(envPort))
		if err != nil {
			logger.Warn("config", "MONITOR_POSTGRES_PORT 无效，已忽略",
				"value", envPort, "error", err)
		} else {
			c.Storage.Postgres.Port = port
		}
	}
	if envUser := os.Getenv("MONITOR_POSTGRES_USER"); envUser != "" {
		c.Storage.Postgres.User = envUser
	}
	if envPass := os.Getenv("MONITOR_POSTGRES_PASSWORD"); envPass != "" {
		c.Storage.Postgres.Password = envPass
	}
	if envDB := os.Getenv("MONITOR_POSTGRES_DATABASE"); envDB != "" {
		c.Storage.Postgres.Database = envDB
	}
	if envSSL := os.Getenv("MONITOR_POSTGRES_SSLMODE"); envSSL != "" {
		c.Storage.Postgres.SSLMode = envSSL
	}

	// SQLite 配置环境变量覆盖
	if envPath := os.Getenv("MONITOR_SQLITE_PATH"); envPath != "" {
		c.Storage.SQLite.Path = envPath
	}

	// Events API Token 环境变量覆盖
	if envToken := os.Getenv("EVENTS_API_TOKEN"); envToken != "" {
		c.Events.APIToken = envToken
	}

	// API Key 覆盖
	for i := range c.Monitors {
		m := &c.Monitors[i]

		// 优先使用自定义环境变量名（解决channel名称冲突）
		if m.EnvVarName != "" {
			if envVal := os.Getenv(m.EnvVarName); envVal != "" {
				m.APIKey = envVal
				continue
			}
		}

		// 优先查找包含 channel 的环境变量（新格式）
		envKeyWithChannel := fmt.Sprintf("MONITOR_%s_%s_%s_API_KEY",
			strings.ToUpper(strings.ReplaceAll(m.Provider, "-", "_")),
			strings.ToUpper(strings.ReplaceAll(m.Service, "-", "_")),
			strings.ToUpper(strings.ReplaceAll(m.Channel, "-", "_")))

		if envVal := os.Getenv(envKeyWithChannel); envVal != "" {
			m.APIKey = envVal
			continue
		}

		// 向后兼容：查找不带 channel 的环境变量（旧格式）
		envKey := fmt.Sprintf("MONITOR_%s_%s_API_KEY",
			strings.ToUpper(strings.ReplaceAll(m.Provider, "-", "_")),
			strings.ToUpper(strings.ReplaceAll(m.Service, "-", "_")))

		if envVal := os.Getenv(envKey); envVal != "" {
			m.APIKey = envVal
		}
	}
}

// resolveTemplateForMonitor 加载单个 monitor 的 template 引用并填充 ServiceConfig 中为空的字段。
//
// 填充策略（config > template）：
//   - Model / RequestModel / Method / Body / SuccessContains：monitor 已设值则保留
//   - SlowLatency / Timeout / Retry* 系列字符串字段：同上
//   - Headers：模板为基础，monitor 同名 key 覆盖（合并而非替换）
//   - URLPattern：仅当为空时填充
//
// 返回的 error 不包含 monitor index 上下文；调用方（多条版本）负责包一层 "monitor[%d] ..." 前缀。
func resolveTemplateForMonitor(m *ServiceConfig, configDir string) error {
	if m == nil || m.Template == "" {
		return nil
	}

	filePath := filepath.Join(configDir, "templates", m.Template+".json")
	tmpl, err := LoadProbeTemplate(filePath)
	if err != nil {
		return err
	}

	// 模型元数据：config > template
	if strings.TrimSpace(m.Model) == "" && tmpl.Model != "" {
		m.Model = tmpl.Model
	}
	if strings.TrimSpace(m.RequestModel) == "" && tmpl.RequestModel != "" {
		m.RequestModel = tmpl.RequestModel
	}
	if strings.TrimSpace(m.ModelVendor) == "" && tmpl.ModelVendor != "" {
		m.ModelVendor = tmpl.ModelVendor
	}

	// 仅填充为空的字段（config > template）
	if m.Method == "" {
		m.Method = tmpl.Method
	}
	if m.Body == "" && len(tmpl.BodyRaw) > 0 {
		m.Body = string(tmpl.BodyRaw)
	}
	if m.SuccessContains == "" {
		m.SuccessContains = tmpl.SuccessContains
	}

	// 模板默认探测参数（仅当 monitor 未显式配置时填充）
	if strings.TrimSpace(m.SlowLatency) == "" && tmpl.SlowLatency != "" {
		m.SlowLatency = tmpl.SlowLatency
	}
	if strings.TrimSpace(m.Timeout) == "" && tmpl.Timeout != "" {
		m.Timeout = tmpl.Timeout
	}
	if m.Retry == nil && tmpl.Retry != nil {
		v := *tmpl.Retry
		m.Retry = &v
	}
	if strings.TrimSpace(m.RetryBaseDelay) == "" && tmpl.RetryBaseDelay != "" {
		m.RetryBaseDelay = tmpl.RetryBaseDelay
	}
	if strings.TrimSpace(m.RetryMaxDelay) == "" && tmpl.RetryMaxDelay != "" {
		m.RetryMaxDelay = tmpl.RetryMaxDelay
	}
	if m.RetryJitter == nil && tmpl.RetryJitter != nil {
		v := *tmpl.RetryJitter
		m.RetryJitter = &v
	}

	// Headers 合并策略：模板为基础，config 覆盖
	if len(tmpl.Headers) > 0 {
		merged := make(map[string]string, len(tmpl.Headers)+len(m.Headers))
		for k, v := range tmpl.Headers {
			merged[k] = v
		}
		for k, v := range m.Headers {
			merged[k] = v // config 覆盖模板
		}
		m.Headers = merged
	}

	// URL 模式：模板的 url 字段存入 URLPattern，探测期通过 InjectVariables 替换
	if m.URLPattern == "" && tmpl.URL != "" {
		m.URLPattern = tmpl.URL
	}

	return nil
}

// ResolveTemplates 加载 template 字段引用的 JSON 模板文件，填充 ServiceConfig 中为空的字段。
func (c *AppConfig) resolveTemplates(configDir string) error {
	for i := range c.Monitors {
		m := &c.Monitors[i]
		if err := resolveTemplateForMonitor(m, configDir); err != nil {
			return fmt.Errorf("monitor[%d] provider=%s service=%s: %w", i, m.Provider, m.Service, err)
		}
	}

	// 模板解析后执行依赖最终 model 的校验（四元组唯一性、父子关系等）
	if err := c.validateResolvedModelConstraints(); err != nil {
		return fmt.Errorf("模板解析后校验失败: %w", err)
	}
	return nil
}

// ResolveSingleMonitor 对单条 ServiceConfig 执行 = 模板填充 + Duration/Retry 派生。
//
// 适用场景：管理后台/用户自助测试构造的临时 ServiceConfig，需要与 scheduler 在用的
// 配置走相同的解析路径，但不需要触发"多条联动"的全局校验。
//
// **不会**执行的步骤（这些是 loader 路径独有的）：
//   - 父子继承（测试场景认为 cfg 已自包含；如需父子继承请走 loader）
//   - 四元组唯一性校验（validateResolvedModelConstraints）
//   - Provider 级 disabled/hidden 注入
//   - provider_slug 默认值填充（属于 post-inheritance 步骤）
//   - Annotation 解析
//
// **前置条件**：app 必须已经经过 normalize（IntervalDuration / TimeoutDuration 等全局默认值已派生），
// 否则当 cfg 本身没指定 timeout 时将回退到 0。
func ResolveSingleMonitor(app *AppConfig, cfg *ServiceConfig, configDir string) error {
	if app == nil {
		return fmt.Errorf("ResolveSingleMonitor: app config 不能为空")
	}
	if cfg == nil {
		return fmt.Errorf("ResolveSingleMonitor: service config 不能为空")
	}
	if err := resolveTemplateForMonitor(cfg, configDir); err != nil {
		return fmt.Errorf("template 解析失败: %w", err)
	}
	return normalizeDurationsForMonitor(app, cfg)
}

// Clone 深拷贝配置（用于热更新回滚）
func (c *AppConfig) clone() *AppConfig {
	// 深拷贝指针字段
	var staggerPtr *bool
	if c.StaggerProbes != nil {
		value := *c.StaggerProbes
		staggerPtr = &value
	}

	// 深拷贝 SponsorPin.Enabled 指针
	var sponsorPinEnabledPtr *bool
	if c.SponsorPin.Enabled != nil {
		value := *c.SponsorPin.Enabled
		sponsorPinEnabledPtr = &value
	}

	// 深拷贝 ExposeChannelDetails 指针
	var exposeChannelDetailsPtr *bool
	if c.ExposeChannelDetails != nil {
		value := *c.ExposeChannelDetails
		exposeChannelDetailsPtr = &value
	}

	clone := &AppConfig{
		Interval:               c.Interval,
		IntervalDuration:       c.IntervalDuration,
		SlowLatency:            c.SlowLatency,
		SlowLatencyDuration:    c.SlowLatencyDuration,
		Timeout:                c.Timeout,
		TimeoutDuration:        c.TimeoutDuration,
		Retry:                  cloneIntPtr(c.Retry),
		RetryCount:             c.RetryCount,
		RetryBaseDelay:         c.RetryBaseDelay,
		RetryBaseDelayDuration: c.RetryBaseDelayDuration,
		RetryMaxDelay:          c.RetryMaxDelay,
		RetryMaxDelayDuration:  c.RetryMaxDelayDuration,
		RetryJitter:            cloneFloat64Ptr(c.RetryJitter),
		RetryJitterValue:       c.RetryJitterValue,
		DegradedWeight:         c.DegradedWeight,
		MaxConcurrency:         c.MaxConcurrency,
		StaggerProbes:          staggerPtr,
		EnableConcurrentQuery:  c.EnableConcurrentQuery,
		ConcurrentQueryLimit:   c.ConcurrentQueryLimit,
		EnableBatchQuery:       c.EnableBatchQuery,
		EnableDBTimelineAgg:    c.EnableDBTimelineAgg,
		BatchQueryMaxKeys:      c.BatchQueryMaxKeys,
		CacheTTL:               c.CacheTTL, // CacheTTL 是值类型，直接复制
		Storage:                c.Storage,
		Server: ServerConfig{
			TrustedPlatform: c.Server.TrustedPlatform,
			// 切片必须深拷贝：热更新期间新旧配置并存，共享 backing array 会让一份的改动串到另一份
			TrustedProxies: append([]string(nil), c.Server.TrustedProxies...),
		},
		PublicBaseURL:           c.PublicBaseURL,
		DisabledProviders:       make([]disabledProviderConfig, len(c.DisabledProviders)),
		HiddenProviders:         make([]hiddenProviderConfig, len(c.HiddenProviders)),
		HidePriceColumn:         c.HidePriceColumn,
		Boards:                  c.Boards, // Boards 是值类型（含 AutoMove），直接复制
		ExposeChannelDetails:    exposeChannelDetailsPtr,
		ChannelDetailsProviders: make([]channelDetailsProviderConfig, len(c.ChannelDetailsProviders)),
		EnableAnnotations:       c.EnableAnnotations,
		AnnotationRules:         make([]AnnotationRule, len(c.AnnotationRules)),
		SponsorPin: SponsorPinConfig{
			Enabled:   sponsorPinEnabledPtr,
			MaxPinned: c.SponsorPin.MaxPinned,
			MinUptime: c.SponsorPin.MinUptime,
			MinLevel:  c.SponsorPin.MinLevel,
		},
		Events:        c.Events,        // Events 是值类型，直接复制
		Announcements: c.Announcements, // Announcements 是值类型，直接复制
		GitHub:        c.GitHub,        // GitHub 是值类型，直接复制
		Monitors:      make([]ServiceConfig, len(c.Monitors)),
	}

	// 复制 slice
	copy(clone.DisabledProviders, c.DisabledProviders)
	copy(clone.HiddenProviders, c.HiddenProviders)
	copy(clone.ChannelDetailsProviders, c.ChannelDetailsProviders)
	copy(clone.AnnotationRules, c.AnnotationRules)
	copy(clone.Monitors, c.Monitors)

	// 深拷贝 annotation_rules 中的 slices
	for i := range clone.AnnotationRules {
		if len(c.AnnotationRules[i].Add) > 0 {
			clone.AnnotationRules[i].Add = make([]Annotation, len(c.AnnotationRules[i].Add))
			copy(clone.AnnotationRules[i].Add, c.AnnotationRules[i].Add)
			deepCopyAnnotationMetadata(clone.AnnotationRules[i].Add)
		}
		if len(c.AnnotationRules[i].Remove) > 0 {
			clone.AnnotationRules[i].Remove = make([]string, len(c.AnnotationRules[i].Remove))
			copy(clone.AnnotationRules[i].Remove, c.AnnotationRules[i].Remove)
		}
	}

	// 深拷贝 monitors 中的 slice/map/指针 字段
	for i := range clone.Monitors {
		// headers map
		if c.Monitors[i].Headers != nil {
			clone.Monitors[i].Headers = make(map[string]string, len(c.Monitors[i].Headers))
			for k, v := range c.Monitors[i].Headers {
				clone.Monitors[i].Headers[k] = v
			}
		}
		// annotations slice
		if len(c.Monitors[i].Annotations) > 0 {
			clone.Monitors[i].Annotations = make([]Annotation, len(c.Monitors[i].Annotations))
			copy(clone.Monitors[i].Annotations, c.Monitors[i].Annotations)
			deepCopyAnnotationMetadata(clone.Monitors[i].Annotations)
		}
		// 指针字段深拷贝
		clone.Monitors[i].PriceMin = cloneFloat64Ptr(c.Monitors[i].PriceMin)
		clone.Monitors[i].PriceMax = cloneFloat64Ptr(c.Monitors[i].PriceMax)
		clone.Monitors[i].Retry = cloneIntPtr(c.Monitors[i].Retry)
		clone.Monitors[i].RetryJitter = cloneFloat64Ptr(c.Monitors[i].RetryJitter)
	}

	return clone
}

// deepCopyAnnotationMetadata 深拷贝 Annotation slice 中每个元素的 Metadata map。
// 递归处理 JSON-like 值：标量直接复制，map[string]any 和 []any 递归拷贝。
func deepCopyAnnotationMetadata(anns []Annotation) {
	for i := range anns {
		if anns[i].Metadata != nil {
			anns[i].Metadata = deepCopyAnyMap(anns[i].Metadata)
		}
	}
}

// deepCopyAnyMap 递归深拷贝 map[string]any
func deepCopyAnyMap(m map[string]any) map[string]any {
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = deepCopyAnyValue(v)
	}
	return cp
}

// deepCopyAnyValue 递归深拷贝 JSON-like 值（标量/map[string]any/[]any）
func deepCopyAnyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyAnyMap(val)
	case []any:
		cp := make([]any, len(val))
		for i, elem := range val {
			cp[i] = deepCopyAnyValue(elem)
		}
		return cp
	default:
		return v // 标量（int64, float64, string, bool, nil）直接返回
	}
}

// cloneIntPtr 深拷贝 *int 指针
func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// cloneFloat64Ptr 深拷贝 *float64 指针
func cloneFloat64Ptr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// ShouldStaggerProbes 返回当前配置是否启用错峰探测
func (c *AppConfig) ShouldStaggerProbes() bool {
	if c == nil {
		return false
	}
	if c.StaggerProbes == nil {
		return true // 默认开启
	}
	return *c.StaggerProbes
}

// ShouldExposeChannelDetails 判断指定 provider 是否应该暴露通道技术细节
// 优先查找 provider 级覆盖配置，否则回退到全局配置
func (c *AppConfig) ShouldExposeChannelDetails(provider string) bool {
	if c == nil {
		return true // 默认暴露
	}

	// 先查 provider 级覆盖
	normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
	for _, p := range c.ChannelDetailsProviders {
		if strings.ToLower(strings.TrimSpace(p.Provider)) == normalizedProvider {
			return p.Expose
		}
	}

	// 回退全局配置
	if c.ExposeChannelDetails == nil {
		return true // 默认暴露
	}
	return *c.ExposeChannelDetails
}
