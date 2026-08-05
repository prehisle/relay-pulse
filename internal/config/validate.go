package config

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"monitor/internal/logger"
	"monitor/internal/modelvendor"
)

// validateContext 承载 Validate() 过程中的中间数据
// 采用结构体而非多返回值，便于扩展且更具可读性
type validateContext struct {
	// 四元组唯一性集合 (provider/service/channel/model)
	quadrupleKeySet map[string]struct{}
	// 父通道索引 (三元组 path -> *ServiceConfig, nil 表示多定义冲突)
	rootByPath map[string]*ServiceConfig
	// 父子关系图 (childKey -> parentKey，单父映射)
	parentOf map[string]string
}

// newValidateContext 创建并初始化 validateContext
// 集中初始化避免各步骤遗漏 make() 导致 nil map panic
func newValidateContext() *validateContext {
	return &validateContext{
		quadrupleKeySet: make(map[string]struct{}),
		rootByPath:      make(map[string]*ServiceConfig),
		parentOf:        make(map[string]string),
	}
}

// Validate 验证配置合法性
// 注意：此方法有副作用，会预处理子通道的 provider/service/channel 继承
func (c *AppConfig) validate() error {
	if len(c.Monitors) == 0 && !c.Onboarding.Enabled {
		return fmt.Errorf("至少需要配置一个监测项")
	}

	// 信任边界先于一切业务校验：写错就 fail-closed，绝不让服务带着错误的客户端 IP 信任面起来
	if err := ValidateTrustedProxies(c.Server.TrustedProxies); err != nil {
		return err
	}

	// 0. 预处理：子项的 provider/service/channel 从 parent 路径继承
	if err := c.preprocessParentInheritance(); err != nil {
		return err
	}

	// 1. 监测项字段校验（不依赖最终 model，model 可能来自 template）
	if err := c.validateMonitorFields(); err != nil {
		return err
	}

	// model_id 是 YAML 字面字段，不来自 template、不参与继承，故可在模板解析前 fail-fast
	// 校验格式与全局唯一（四元组唯一性因依赖 template 的 model 仍在 resolveTemplates 后校验）。
	if err := c.validateModelIDs(); err != nil {
		return err
	}

	// 2. 自动移板配置校验
	if err := c.validateBoardAutoMove(); err != nil {
		return err
	}

	// 3. Provider 配置校验
	if err := c.validateProviderConfigs(); err != nil {
		return err
	}

	// 4. Annotation 规则校验
	if err := c.validateAnnotationRules(); err != nil {
		return err
	}

	return nil
}

// validateResolvedModelConstraints 在模板解析后执行依赖最终 model 的校验。
// model 可能来自 monitor.model，也可能来自 template.model，
// 因此四元组唯一性、父子关系等校验必须在 resolveTemplates() 之后才能执行。
func (c *AppConfig) validateResolvedModelConstraints() error {
	ctx := newValidateContext()

	if err := c.validateMonitorUniqueness(ctx); err != nil {
		return err
	}
	if err := c.buildAndValidateParentGraph(ctx); err != nil {
		return err
	}
	if err := c.validateNoCycles(ctx); err != nil {
		return err
	}
	// vendor 与 model 同属「可能来自 monitor 行、也可能来自 template」的字段，故与四元组唯一性
	// 一样必须等模板解析完才校验得全——放在 validate() 里则模板声明的 vendor 永远不过校验。
	if err := c.validateModelVendors(); err != nil {
		return err
	}

	c.warnMultipleParentLayers()
	return nil
}

// validateModelVendors 宽松校验模型厂商，并把合法值写回规范形式。
//
// 两条规则：
//  1. 非空值必须是受控词表内的 code（见 internal/modelvendor）；空值放行。
//  2. 同一通道（PSC 三元组）下**均非空**的 vendor 必须一致——「一个通道一个厂商」不变量，
//     它让 vendor 退化为通道级单值，前端列可排序/筛选，也给 rpdiag 干净的分组键。
//
// 只比较非空值是刻意的：既容忍「同通道一行已填、一行还空」的中间态，也容忍完全不关心
// 厂商的自托管部署。
//
// ⚠️ 与 model_id 不同，vendor **没有** fail-closed 运行时闸，将来也别加：它无法像
// model_id 那样由 loader 自动派生（禁止从 request_model 前缀反推厂商——模型 ID 命名不稳、
// 中转商可改写、同模型多别名，必然产生跨产品 join 漂移），加闸只会让自己手写内联监测行的
// 自托管用户升级即 crash-loop（v2.69.2 修的正是 CheckRuntimeModelIDs 造成的同类伤害）。
// 我方生产的覆盖靠「内置模板全部声明 vendor」保证，守卫见
// TestBundledTemplatesDeclareModelVendor；native 模板那条唯一的例外由 validateFinal 告警兜。
func (c *AppConfig) validateModelVendors() error {
	// key = provider/service/channel，value = 该通道首个非空 vendor（已规范化）。
	// 键的拼法与 validateMonitorUniqueness 的四元组键保持一致（PSC 各段的合法字符集
	// 已由 validateMonitorFields 在更早阶段限定为小写，故无需再额外归一化）。
	firstByChannel := make(map[string]string, len(c.Monitors))
	for i := range c.Monitors {
		m := &c.Monitors[i]

		code, err := modelvendor.Normalize(m.ModelVendor)
		if err != nil {
			return fmt.Errorf("monitor[%d] %s: %w", i, modelIDLocation(*m), err)
		}
		// 写回规范形式，使下游（/api/status wire、跨产品 join）只需处理规范值。
		m.ModelVendor = code
		if code == "" {
			continue
		}

		key := fmt.Sprintf("%s/%s/%s", m.Provider, m.Service, m.Channel)
		if first, seen := firstByChannel[key]; seen {
			if first != code {
				return fmt.Errorf("monitor[%d] %s: 同一通道 %s 内 model_vendor 不一致（已有 %q，本行 %q）——"+
					"一个通道只能属于一个厂商，聚合平台请按厂商拆成不同通道（channel_group）",
					i, modelIDLocation(*m), key, first, code)
			}
			continue
		}
		firstByChannel[key] = code
	}
	return nil
}

// ValidateFinal 检查环境变量覆盖、模板解析、继承和 Normalize 之后的最终配置。
// Phase 1: 仅返回告警项，由调用方记录日志，不阻断启动或热更新。
func (c *AppConfig) validateFinal() []error {
	var warns []error

	// 存储类型校验
	storageType := strings.ToLower(strings.TrimSpace(c.Storage.Type))
	switch storageType {
	case "", "sqlite", "postgres", "postgresql":
		// 合法值
	default:
		warns = append(warns, fmt.Errorf("storage.type '%s' 无效，支持 sqlite/postgres", c.Storage.Type))
	}

	// PostgreSQL 端口范围校验
	if storageType == "postgres" || storageType == "postgresql" {
		if c.Storage.Postgres.Port <= 0 || c.Storage.Postgres.Port > 65535 {
			warns = append(warns, fmt.Errorf("storage.postgres.port(%d) 超出有效范围 1-65535", c.Storage.Postgres.Port))
		}
	}

	// 逐监测项检查最终态合理性
	for i := range c.Monitors {
		m := &c.Monitors[i]
		if m.Disabled {
			continue
		}
		hasParent := strings.TrimSpace(m.Parent) != ""

		// 非子通道：最终必须有可用的探测地址
		if !hasParent && !hasUsableProbeTarget(m) {
			warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 最终探测地址缺失（需要 base_url 或 url_pattern）",
				i, m.Provider, m.Service, m.Channel))
		}

		// 占位符依赖校验：模板/配置引用了占位符，但对应字段为空
		if serviceConfigUsesPlaceholder(m, "{{BASE_URL}}") && strings.TrimSpace(m.BaseURL) == "" {
			warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 引用了 {{BASE_URL}}，但最终 base_url 为空",
				i, m.Provider, m.Service, m.Channel))
		}
		if serviceConfigUsesPlaceholder(m, "{{API_KEY}}") && strings.TrimSpace(m.APIKey) == "" {
			warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 引用了 {{API_KEY}}，但最终 api_key 为空",
				i, m.Provider, m.Service, m.Channel))
		}
		resolvedRequestModel := strings.TrimSpace(m.RequestModel)
		if resolvedRequestModel == "" {
			resolvedRequestModel = strings.TrimSpace(m.Model)
		}
		if (serviceConfigUsesPlaceholder(m, "{{MODEL}}") ||
			serviceConfigUsesPlaceholder(m, "{{REQUEST_MODEL}}")) &&
			resolvedRequestModel == "" {
			warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 引用了 {{MODEL}}/{{REQUEST_MODEL}}，但最终 request_model 与 model 均为空",
				i, m.Provider, m.Service, m.Channel))
		}

		// native 模板厂商无关（见 isNativeProbeTemplate），vendor 只能由监测行填写，没有模板兜底。
		// 只告警不阻断：本仓库对 vendor 刻意不设 fail-closed 运行时闸（理由见
		// TestBundledTemplatesDeclareModelVendor），且漏填的后果是前端厂商列显示「未知」——
		// 是缺信息，不是错信息。
		// 必须挂在 validateFinal 而非 validateModelVendors：后者跟在 resolveTemplates 里执行，
		// 早于父子继承（normalize 第 7 步），在那里判空会误伤「vendor 只写父行、子行靠继承」。
		if isNativeProbeTemplate(m.Template) && strings.TrimSpace(m.ModelVendor) == "" {
			warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 使用第一方厂商通用模板 %q 却未填 model_vendor，厂商列将显示为未知",
				i, m.Provider, m.Service, m.Channel, m.Template))
		}

		// URL 安全告警：默认不允许探测目标直接指向私有网络 IP
		if !c.AllowPrivateNetworks && !m.SkipURLValidation {
			if err := validateNoPrivateIPURL(m.BaseURL, "base_url"); err != nil {
				warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: %w",
					i, m.Provider, m.Service, m.Channel, err))
			}
			// url_pattern 为绝对 URL（不依赖 {{BASE_URL}}）时也检查
			if urlPattern := strings.TrimSpace(m.URLPattern); urlPattern != "" &&
				!strings.Contains(urlPattern, "{{BASE_URL}}") {
				if err := validateNoPrivateIPURL(urlPattern, "url_pattern"); err != nil {
					warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: %w",
						i, m.Provider, m.Service, m.Channel, err))
				}
			}
		}

		// 非子通道：最终 method 必须有效
		if !hasParent {
			method := strings.TrimSpace(m.Method)
			if method == "" {
				warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 最终 method 为空",
					i, m.Provider, m.Service, m.Channel))
			} else if !isValidHTTPMethod(method) {
				warns = append(warns, fmt.Errorf("monitor[%d] %s/%s/%s: 最终 method '%s' 无效",
					i, m.Provider, m.Service, m.Channel, m.Method))
			}
		}
	}

	return warns
}

// isValidHTTPMethod 检查是否为合法 HTTP 方法
func isValidHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case "GET", "POST", "PUT", "DELETE", "PATCH":
		return true
	}
	return false
}

// hasUsableProbeTarget 检查监测项最终是否有可用的探测地址
func hasUsableProbeTarget(m *ServiceConfig) bool {
	return strings.TrimSpace(m.BaseURL) != "" || strings.TrimSpace(m.URLPattern) != ""
}

// serviceConfigUsesPlaceholder 检查监测项的配置字段中是否引用了指定占位符
func serviceConfigUsesPlaceholder(m *ServiceConfig, placeholder string) bool {
	if strings.Contains(m.BaseURL, placeholder) ||
		strings.Contains(m.URLPattern, placeholder) ||
		strings.Contains(m.Body, placeholder) ||
		strings.Contains(m.SuccessContains, placeholder) {
		return true
	}
	for k, v := range m.Headers {
		if strings.Contains(k, placeholder) || strings.Contains(v, placeholder) {
			return true
		}
	}
	return false
}

// preprocessParentInheritance 预处理子通道的 provider/service/channel 继承
// 必须在唯一性检查前完成，否则子项的 key 不完整
// 注意：此方法会修改 c.Monitors，需保证幂等
func (c *AppConfig) preprocessParentInheritance() error {
	for i := range c.Monitors {
		m := &c.Monitors[i]
		parentPath := strings.TrimSpace(m.Parent)
		if parentPath == "" {
			continue
		}

		parts := strings.Split(parentPath, "/")
		if len(parts) != 3 {
			return fmt.Errorf("monitor[%d]: parent 格式错误: %s (应为 provider/service/channel)", i, parentPath)
		}
		parentProvider, parentService, parentChannel := parts[0], parts[1], parts[2]

		// 子的 provider/service/channel：为空则从 parent 继承；非空则必须与 parent 一致
		if m.Provider == "" {
			m.Provider = parentProvider
		} else if m.Provider != parentProvider {
			return fmt.Errorf("monitor[%d]: 子通道 provider '%s' 与 parent '%s' 不一致，不支持覆盖", i, m.Provider, parentProvider)
		}
		if m.Service == "" {
			m.Service = parentService
		} else if m.Service != parentService {
			return fmt.Errorf("monitor[%d]: 子通道 service '%s' 与 parent '%s' 不一致，不支持覆盖", i, m.Service, parentService)
		}
		if m.Channel == "" {
			m.Channel = parentChannel
		} else if m.Channel != parentChannel {
			return fmt.Errorf("monitor[%d]: 子通道 channel '%s' 与 parent '%s' 不一致，不支持覆盖", i, m.Channel, parentChannel)
		}
	}
	return nil
}

// validateMonitorUniqueness 检查四元组唯一性 (provider/service/channel/model)
func (c *AppConfig) validateMonitorUniqueness(ctx *validateContext) error {
	for _, m := range c.Monitors {
		key := fmt.Sprintf("%s/%s/%s/%s", m.Provider, m.Service, m.Channel, m.Model)
		if _, exists := ctx.quadrupleKeySet[key]; exists {
			return fmt.Errorf("重复的监测项: %s", key)
		}
		ctx.quadrupleKeySet[key] = struct{}{}
	}
	return nil
}

// buildAndValidateParentGraph 构建并校验父子关系图
// 包括：收集父通道引用、构建索引、验证父存在性
func (c *AppConfig) buildAndValidateParentGraph(ctx *validateContext) error {
	// 收集父通道引用
	parentRefs := make(map[string]struct{})
	for i, m := range c.Monitors {
		parentPath := strings.TrimSpace(m.Parent)
		if parentPath == "" {
			continue
		}

		// 子通道必须有 model
		if strings.TrimSpace(m.Model) == "" {
			return fmt.Errorf("monitor[%d]: 子通道 %s/%s/%s 有 parent 但缺少 model", i, m.Provider, m.Service, m.Channel)
		}

		parentRefs[parentPath] = struct{}{}
	}

	// 被引用为父的监测项必须有 model
	for i, m := range c.Monitors {
		path := fmt.Sprintf("%s/%s/%s", m.Provider, m.Service, m.Channel)
		if _, isReferencedAsParent := parentRefs[path]; isReferencedAsParent {
			if strings.TrimSpace(m.Model) == "" {
				return fmt.Errorf("monitor[%d]: 监测项 %s 被引用为父但缺少 model", i, path)
			}
		}
	}

	// 构建父通道索引（parent 为空的 monitor 定义）
	// 注意：ctx.rootByPath 已在 newValidateContext() 中初始化
	for i := range c.Monitors {
		if strings.TrimSpace(c.Monitors[i].Parent) != "" {
			continue
		}
		path := fmt.Sprintf("%s/%s/%s", c.Monitors[i].Provider, c.Monitors[i].Service, c.Monitors[i].Channel)
		if existing, exists := ctx.rootByPath[path]; exists {
			// 标记为多定义（nil 表示冲突）
			if existing != nil {
				ctx.rootByPath[path] = nil
			}
			continue
		}
		ctx.rootByPath[path] = &c.Monitors[i]
	}

	// 父存在性校验，并构建 parent 关系图（用于循环检测）
	// 注意：ctx.parentOf 已在 newValidateContext() 中初始化
	for i, m := range c.Monitors {
		parentPath := strings.TrimSpace(m.Parent)
		if parentPath == "" {
			continue
		}

		// 验证父存在且唯一
		parent := ctx.rootByPath[parentPath]
		if parent == nil {
			if _, pathExists := ctx.rootByPath[parentPath]; pathExists {
				return fmt.Errorf("monitor[%d]: 父通道 %s 存在多个定义", i, parentPath)
			}
			return fmt.Errorf("monitor[%d]: 找不到父通道: %s", i, parentPath)
		}

		// 构建父子关系图
		childKey := fmt.Sprintf("%s/%s/%s/%s", m.Provider, m.Service, m.Channel, m.Model)
		parentKey := fmt.Sprintf("%s/%s/%s/%s", parent.Provider, parent.Service, parent.Channel, parent.Model)
		ctx.parentOf[childKey] = parentKey
	}

	return nil
}

// validateNoCycles 检测循环引用（DFS 颜色标记：0=白, 1=灰, 2=黑）
func (c *AppConfig) validateNoCycles(ctx *validateContext) error {
	color := make(map[string]int)
	var dfsCheckCycle func(key string) error
	dfsCheckCycle = func(key string) error {
		switch color[key] {
		case 1:
			return fmt.Errorf("检测到循环引用: %s", key)
		case 2:
			return nil
		}

		color[key] = 1 // 标记为灰色（访问中）

		if parentKey, hasParent := ctx.parentOf[key]; hasParent {
			if err := dfsCheckCycle(parentKey); err != nil {
				return err
			}
		}

		color[key] = 2 // 标记为黑色（已完成）
		return nil
	}

	for key := range ctx.quadrupleKeySet {
		if color[key] == 0 {
			if err := dfsCheckCycle(key); err != nil {
				return err
			}
		}
	}

	return nil
}

// warnMultipleParentLayers 警告同一 PSC 下存在多个父层
// 只有第一个会被视为父层，其他会从 API 输出中丢失
func (c *AppConfig) warnMultipleParentLayers() {
	pscNoParentCount := make(map[string]int)
	for _, m := range c.Monitors {
		if strings.TrimSpace(m.Parent) == "" && strings.TrimSpace(m.Model) != "" {
			psc := fmt.Sprintf("%s/%s/%s", m.Provider, m.Service, m.Channel)
			pscNoParentCount[psc]++
		}
	}

	// 收集需要警告的 PSC 并排序，保证输出稳定性
	var warnings []string
	for psc, count := range pscNoParentCount {
		if count > 1 {
			warnings = append(warnings, psc)
		}
	}
	sort.Strings(warnings)

	for _, psc := range warnings {
		logger.Warn("config", "同一 PSC 下存在多个父层 (Parent='', Model!='')，只有第一个会作为父层，其他会丢失",
			"psc", psc, "count", pscNoParentCount[psc])
	}
}

// validateMonitorFields 校验监测项的必填字段和字段合法性
func (c *AppConfig) validateMonitorFields() error {
	for i, m := range c.Monitors {
		hasParent := strings.TrimSpace(m.Parent) != ""

		// 基础必填字段（provider/service/channel 已在预处理步骤处理）
		if m.Provider == "" {
			return fmt.Errorf("monitor[%d]: provider 不能为空", i)
		}
		if m.Service == "" {
			return fmt.Errorf("monitor[%d]: service 不能为空", i)
		}

		// Category: 非子通道留空时默认 commercial（子通道留空走父继承）
		if !hasParent && m.Category == "" {
			c.Monitors[i].Category = "commercial"
			m.Category = "commercial"
		}

		// 非子通道：需要有 template 或 base_url + method
		// 有 template 时允许 base_url/method 为空（由模板提供）
		if !hasParent && m.Template == "" {
			if m.BaseURL == "" {
				return fmt.Errorf("monitor[%d]: 未配置 template 时 base_url 不能为空", i)
			}
			if m.Method == "" {
				return fmt.Errorf("monitor[%d]: 未配置 template 时 method 不能为空", i)
			}
		}

		// Method 枚举检查（子通道允许留空继承）
		if m.Method != "" && !isValidHTTPMethod(m.Method) {
			return fmt.Errorf("monitor[%d]: method '%s' 无效，必须是 GET/POST/PUT/DELETE/PATCH 之一", i, m.Method)
		}

		// Category 枚举检查（子通道允许留空继承）
		if m.Category != "" && !isValidCategory(m.Category) {
			return fmt.Errorf("monitor[%d]: category '%s' 无效，必须是 commercial 或 public", i, m.Category)
		}

		// SponsorLevel 枚举检查（可选字段，空值有效）
		if !m.SponsorLevel.isValid() {
			return fmt.Errorf("monitor[%d]: sponsor_level '%s' 无效，必须是 public/signal/pulse/beacon/backbone/core 之一（或留空）", i, m.SponsorLevel)
		}

		// KeyType 枚举检查（可选字段，空值视为 official）
		switch strings.ToLower(strings.TrimSpace(m.KeyType)) {
		case "", "official", "user":
			// 有效值
		default:
			return fmt.Errorf("monitor[%d]: key_type '%s' 无效，必须是 official/user（或留空）", i, m.KeyType)
		}

		// Board 枚举检查（可选字段，空值视为 hot）
		normalizedBoard := strings.ToLower(strings.TrimSpace(m.Board))
		switch normalizedBoard {
		case "", "hot", "secondary", "cold":
			// 有效值
		default:
			return fmt.Errorf("monitor[%d]: board '%s' 无效，必须是 hot/secondary/cold（或留空）", i, m.Board)
		}
		// 注意：cold_reason 的有效性检查在 Normalize() 中进行（非致命，仅警告并清空）

		// PriceMin/PriceMax 验证（可选字段）
		if m.PriceMin != nil && *m.PriceMin < 0 {
			return fmt.Errorf("monitor[%d]: price_min 不能为负数", i)
		}
		if m.PriceMax != nil && *m.PriceMax < 0 {
			return fmt.Errorf("monitor[%d]: price_max 不能为负数", i)
		}
		// 若同时配置了 min 和 max，min 必须 <= max
		if m.PriceMin != nil && m.PriceMax != nil && *m.PriceMin > *m.PriceMax {
			return fmt.Errorf("monitor[%d]: price_min 不能大于 price_max", i)
		}

		// ListedSince 验证（可选字段，格式必须为 "2006-01-02"）
		if m.ListedSince != "" {
			if _, err := time.Parse("2006-01-02", m.ListedSince); err != nil {
				return fmt.Errorf("monitor[%d]: listed_since 格式错误，应为 YYYY-MM-DD", i)
			}
		}

		// ExpiresAt 验证（可选字段，格式必须为 "2006-01-02"）
		if m.ExpiresAt != "" {
			if _, err := time.Parse("2006-01-02", m.ExpiresAt); err != nil {
				return fmt.Errorf("monitor[%d]: expires_at 格式错误，应为 YYYY-MM-DD", i)
			}
		}

		// ProviderURL 验证（可选字段）
		if m.ProviderURL != "" {
			if err := validateURL(m.ProviderURL, "provider_url"); err != nil {
				return fmt.Errorf("monitor[%d]: %w", i, err)
			}
		}

		// SponsorURL 验证（可选字段）
		if m.SponsorURL != "" {
			if err := validateURL(m.SponsorURL, "sponsor_url"); err != nil {
				return fmt.Errorf("monitor[%d]: %w", i, err)
			}
		}

		// Proxy 验证（可选字段）
		if trimmedProxy := strings.TrimSpace(m.Proxy); trimmedProxy != "" {
			if err := validateProxyURL(trimmedProxy); err != nil {
				return fmt.Errorf("monitor[%d]: %w", i, err)
			}
		}
	}
	return nil
}

// CheckRuntimeModelIDs 运行时硬校验：所有监测行必须有非空 model_id。
// 与 validateModelIDs（只校验格式/唯一、允许空，回填前兼容）分开——
// Phase 1 起内部历史按 model_id 重键，展示读已切 model_id，缺 id 即该通道历史不可见，必须 fail-closed。
// 故意不放进 validate()：present 是「已部署/已回填」运行时不变量，非配置语法不变量；
// 放 validate() 会误伤大量用 model_id-less fixture 的 config 单测。
func CheckRuntimeModelIDs(monitors []ServiceConfig) error {
	for i, m := range monitors {
		if strings.TrimSpace(m.ModelID) == "" {
			return fmt.Errorf("monitor[%d] %s: 缺 model_id（内部历史已按 model_id 重键，缺即该通道历史不可见）。请运行 cmd/backfillids 回填 monitors.d/", i, modelIDLocation(m))
		}
	}
	return nil
}

// validateModelIDs 校验监测行稳定 id：非空 model_id 须格式合法（md_<uuidv4>）且全局唯一。
// 空 model_id 合法——回填前的既有行 / 待 Create 生成，不在此报错（由回填 CLI 与创建路径补齐）。
func (c *AppConfig) validateModelIDs() error {
	for i, m := range c.Monitors {
		if m.ModelID == "" {
			continue
		}
		if !IsValidModelID(m.ModelID) {
			return fmt.Errorf("monitor[%d] %s: model_id 格式非法 %q（应为 md_<uuidv4>）", i, modelIDLocation(m), m.ModelID)
		}
	}
	// 全局唯一：复用 identity.go 的去重核，撞重 error 已含重复 id。
	if err := collectModelIDsInto(make(map[string]string), c.Monitors); err != nil {
		return err
	}
	return nil
}

// validateBoardAutoMove 校验自动移板配置
// 注意：此方法在 Normalize() 之前调用，仅校验 YAML 原始值（不检查 Duration 等解析后字段）
func (c *AppConfig) validateBoardAutoMove() error {
	am := c.Boards.AutoMove
	if !am.Enabled {
		return nil // 未启用时跳过校验
	}
	// 阈值范围校验（0 值视为未配置，Normalize 将填充默认值）
	if am.ThresholdCold != 0 && (am.ThresholdCold < 0 || am.ThresholdCold > 100) {
		return fmt.Errorf("boards.auto_move.threshold_cold 必须在 0-100 范围内，当前值: %.2f", am.ThresholdCold)
	}
	if am.ThresholdDown != 0 && (am.ThresholdDown < 0 || am.ThresholdDown > 100) {
		return fmt.Errorf("boards.auto_move.threshold_down 必须在 0-100 范围内，当前值: %.2f", am.ThresholdDown)
	}
	// cold < down 关系校验（两者都非零时检查）
	if am.ThresholdCold != 0 && am.ThresholdDown != 0 && am.ThresholdCold >= am.ThresholdDown {
		return fmt.Errorf("boards.auto_move.threshold_cold(%.2f) 必须小于 threshold_down(%.2f)", am.ThresholdCold, am.ThresholdDown)
	}
	if am.ThresholdUp != 0 && (am.ThresholdUp < 0 || am.ThresholdUp > 100) {
		return fmt.Errorf("boards.auto_move.threshold_up 必须在 0-100 范围内，当前值: %.2f", am.ThresholdUp)
	}
	// 两个都非零时检查关系
	if am.ThresholdDown != 0 && am.ThresholdUp != 0 && am.ThresholdDown >= am.ThresholdUp {
		return fmt.Errorf("boards.auto_move.threshold_down(%.2f) 必须小于 threshold_up(%.2f)，中间缓冲带用于防抖", am.ThresholdDown, am.ThresholdUp)
	}
	if am.MinProbes < 0 {
		return fmt.Errorf("boards.auto_move.min_probes 必须 >= 0，当前值: %d", am.MinProbes)
	}
	// check_interval 字符串格式校验（Duration 解析在 Normalize 中完成）
	if ci := strings.TrimSpace(am.CheckInterval); ci != "" {
		if _, err := time.ParseDuration(ci); err != nil {
			return fmt.Errorf("boards.auto_move.check_interval 格式无效: %w", err)
		}
	}
	return nil
}

// validateProviderConfigs 校验 Provider 相关配置
func (c *AppConfig) validateProviderConfigs() error {
	// 验证 disabled_providers
	disabledProviderSet := make(map[string]struct{})
	for i, dp := range c.DisabledProviders {
		provider := strings.ToLower(strings.TrimSpace(dp.Provider))
		if provider == "" {
			return fmt.Errorf("disabled_providers[%d]: provider 不能为空", i)
		}
		if _, exists := disabledProviderSet[provider]; exists {
			return fmt.Errorf("disabled_providers[%d]: provider '%s' 重复配置", i, dp.Provider)
		}
		disabledProviderSet[provider] = struct{}{}
	}

	return nil
}

// validateAnnotationRules 校验 annotation_rules 配置
func (c *AppConfig) validateAnnotationRules() error {
	for i, rule := range c.AnnotationRules {
		// 至少需要 add 或 remove 之一
		if len(rule.Add) == 0 && len(rule.Remove) == 0 {
			return fmt.Errorf("annotation_rules[%d]: 必须至少包含 add 或 remove", i)
		}

		// 校验 add 中的注解
		for j, ann := range rule.Add {
			if strings.TrimSpace(ann.ID) == "" {
				return fmt.Errorf("annotation_rules[%d].add[%d]: id 不能为空", i, j)
			}
			if strings.TrimSpace(ann.Label) == "" && strings.TrimSpace(ann.ID) == "" {
				return fmt.Errorf("annotation_rules[%d].add[%d]: label 不能为空", i, j)
			}
			if ann.Family != "" && !ann.Family.isValid() {
				return fmt.Errorf("annotation_rules[%d].add[%d]: family '%s' 无效，必须是 positive/neutral/negative", i, j, ann.Family)
			}
			if ann.Priority < 0 || ann.Priority > 200 {
				return fmt.Errorf("annotation_rules[%d].add[%d]: priority 必须在 0-200 范围内", i, j)
			}
			if ann.Href != "" {
				if err := validateURL(ann.Href, "href"); err != nil {
					return fmt.Errorf("annotation_rules[%d].add[%d]: %w", i, j, err)
				}
			}
		}

		// 校验 remove 中的 ID
		for j, id := range rule.Remove {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("annotation_rules[%d].remove[%d]: id 不能为空", i, j)
			}
		}
	}

	return nil
}
