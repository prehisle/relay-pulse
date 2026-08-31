package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// PSCConflictError 表示 PSC 冲突错误，包含冲突信息和建议值。
type PSCConflictError struct {
	Provider         string
	Service          string
	Channel          string
	SuggestedChannel string
}

func (e *PSCConflictError) Error() string {
	return fmt.Sprintf("PSC %s/%s/%s 已存在于当前运行配置中，请调整 target_channel（建议: %s）",
		e.Provider, e.Service, e.Channel, e.SuggestedChannel)
}

// InvalidProviderSlugError 表示发布时从服务商展示名派生的 provider slug 非法（通常因展示名含非
// 英文字符）、且管理员未经 target_provider 覆盖英文代号。区别于 PSCConflictError（唯一性冲突），
// 供 handler 特判为 4xx + 可操作指引，避免呈现为服务端 500。
type InvalidProviderSlugError struct {
	ProviderName string // 原始展示名（可能含中文）
	DerivedSlug  string // 派生出的非法 slug
}

func (e *InvalidProviderSlugError) Error() string {
	return fmt.Sprintf("服务商名 %q 无法自动生成合法的网址代号（派生值 %q 含中文或其它无法用于网址的字符）；请在「Provider 覆盖」(target_provider) 填写英文代号（小写字母、数字、短横线）后再上架。",
		e.ProviderName, e.DerivedSlug)
}

// pscOverrideFieldLabels 把字段名映射成管理后台里的可见标签，让错误消息能直接指向那个输入框。
var pscOverrideFieldLabels = map[string]string{
	"target_provider": "Provider 覆盖",
	"target_service":  "Service 覆盖",
	"target_channel":  "Channel 覆盖",
}

// InvalidPSCOverrideError 表示管理员填写的 PSC 覆盖值本身非法。
//
// 与 InvalidProviderSlugError 分成两个类型是因为处置动作不同：那条说的是「展示名派生不出代号、
// 你还没填覆盖值」，这条说的是「你填的这个值不能用」。此前后者没有专属类型，一路落进
// validateMonitorConfig 的通用校验、在 handler 被当成服务端故障报 500——管理员看到的是一个
// 5xx，而实际是自己填错了一格。
type InvalidPSCOverrideError struct {
	Field  string // target_provider / target_service / target_channel
	Value  string // 管理员填写的原值（已 TrimSpace）
	Reason error  // 来自 config.ValidateProviderSlug 的具体原因
}

func (e *InvalidPSCOverrideError) Error() string {
	label := pscOverrideFieldLabels[e.Field]
	if label == "" {
		label = e.Field
	}
	return fmt.Sprintf("「%s」(%s) 填写的 %q 不能用作通道代号（%v）；它是英文网址代号、不是展示名称，仅允许小写字母、数字、短横线，不能以短横线开头或结尾、不能出现连续短横线，长度 ≤100。",
		label, e.Field, e.Value, e.Reason)
}

func (e *InvalidPSCOverrideError) Unwrap() error { return e.Reason }

// validatePSCOverride 校验单个管理员填写的 PSC 覆盖值；空值表示「不覆盖」，直接放行。
//
// 校验规则刻意与 loader 的 config.ValidateProviderSlug 同源：覆盖值最终会成为 monitors.d/
// 的文件名分段与公开 URL slug，用任何更宽松的规则都会造出「写盘成功、热加载失败」的坏文件。
func validatePSCOverride(field, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	if err := config.ValidateProviderSlug(v); err != nil {
		return &InvalidPSCOverrideError{Field: field, Value: v, Reason: err}
	}
	return nil
}

// validatePSCOverrides 校验申请上三个 PSC 覆盖值。
func validatePSCOverrides(sub *Submission) error {
	for _, f := range []struct {
		name  string
		value string
	}{
		{"target_provider", sub.TargetProvider},
		{"target_service", sub.TargetService},
		{"target_channel", sub.TargetChannel},
	} {
		if err := validatePSCOverride(f.name, f.value); err != nil {
			return err
		}
	}
	return nil
}

// AdminPublish 上架：生成 ServiceConfig 并写入 monitors.d/。
// board 指定目标版块（hot/secondary/cold），优先级高于 AdminConfigJSON 中的同名字段。
// 使用原子文件写入（temp + fsync + rename）确保安全。
func (s *Service) AdminPublish(ctx context.Context, publicID, board string) error {
	if board == "" {
		board = "hot"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status != StatusPending && sub.Status != StatusApproved {
		return fmt.Errorf("只有待审核或已批准的申请可以上架，当前状态: %s", sub.Status)
	}

	// 解密 API Key
	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return fmt.Errorf("解密 API Key 失败: %w", err)
	}

	// 构建 ServiceConfig
	monitorCfg := s.buildServiceConfig(sub, apiKey)

	// 派生路径下（无 AdminConfigJSON 整份覆盖）先分流两类「管理员填错了」的错误，让它们带着
	// 可操作指引以 4xx 返回，而不是落进下方通用校验被当成服务端故障报 500：
	//   1. 管理员填了非法覆盖值 → InvalidPSCOverrideError（指名是哪一格、合法格式是什么）；
	//   2. 覆盖值留空、展示名又派生不出合法 slug（中文名的常态）→ InvalidProviderSlugError。
	// AdminConfigJSON 整份覆盖路径不经这两道分流：那条逃生口自带完整 Provider/Service/Channel，
	// 三个 target_* 根本不参与最终配置，在这里拦它们等于拦一个不生效的字段。它仍由下方
	// validateMonitorConfig 兜底（同源规则，坏值绝进不了 monitors.d/）。
	if sub.AdminConfigJSON == "" {
		if err := validatePSCOverrides(sub); err != nil {
			return err
		}
		if strings.TrimSpace(sub.TargetProvider) == "" &&
			config.ValidateProviderSlug(monitorCfg.Provider) != nil {
			return &InvalidProviderSlugError{ProviderName: sub.ProviderName, DerivedSlug: monitorCfg.Provider}
		}
	}

	// 如果管理员有自定义配置，覆盖
	if sub.AdminConfigJSON != "" {
		var adminCfg config.ServiceConfig
		if err := json.Unmarshal([]byte(sub.AdminConfigJSON), &adminCfg); err != nil {
			return fmt.Errorf("解析管理员配置失败: %w", err)
		}
		monitorCfg = adminCfg
		// 确保 API key 不会被管理员配置覆盖为空
		if monitorCfg.APIKey == "" {
			monitorCfg.APIKey = apiKey
		}
	}
	// board 参数优先级高于 AdminConfigJSON，显式覆盖
	monitorCfg.Board = board

	// 发布前校验：验证生成的 monitor 配置是否合法
	if err := s.validateMonitorConfig(monitorCfg); err != nil {
		return fmt.Errorf("待发布 monitor 配置无效: %w", err)
	}

	// PSC 冲突预检：确认不与已有 monitors 冲突
	if s.configMonitorExists != nil &&
		s.configMonitorExists(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel) {
		suggested := s.suggestUniqueChannel(monitorCfg.Provider, monitorCfg.Service, monitorCfg.Channel)
		return &PSCConflictError{
			Provider:         monitorCfg.Provider,
			Service:          monitorCfg.Service,
			Channel:          monitorCfg.Channel,
			SuggestedChannel: suggested,
		}
	}

	// 写入 monitors.d/
	if s.monitorStore == nil {
		return fmt.Errorf("MonitorStore 未初始化，无法写入 monitors.d/")
	}

	monitorFile := &config.MonitorFile{
		Metadata: config.MonitorFileMetadata{
			Source:    "onboarding",
			Revision:  1,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Monitors: []config.ServiceConfig{monitorCfg},
	}

	if err := s.monitorStore.Create(monitorFile); err != nil {
		return fmt.Errorf("写入 monitors.d/ 失败: %w", err)
	}

	// 更新 DB 状态
	now := time.Now().Unix()
	sub.Status = StatusPublished
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	if err := s.store.Update(ctx, sub); err != nil {
		// 文件已写入但 DB 更新失败 — 记录错误但不回滚文件
		// 下次热更新会正常加载，管理员可通过 admin 面板修正状态
		logger.Error("onboarding", "更新申请状态失败（文件已写入）",
			"public_id", publicID, "error", err)
		return fmt.Errorf("已写入配置文件但更新数据库状态失败: %w", err)
	}

	logger.Info("onboarding", "申请已上架",
		"public_id", publicID,
		"provider", sub.ProviderName,
		"channel", sub.ChannelCode)

	return nil
}

// validateMonitorConfig 在发布前校验即将写入 monitors.d/ 的 monitor 配置。
func (s *Service) validateMonitorConfig(m config.ServiceConfig) error {
	if err := validatePSCSegment("provider", m.Provider); err != nil {
		return err
	}
	if err := validatePSCSegment("service", m.Service); err != nil {
		return err
	}
	if err := validatePSCSegment("channel", m.Channel); err != nil {
		return err
	}
	if strings.TrimSpace(m.BaseURL) == "" {
		return fmt.Errorf("base_url 不能为空")
	}

	if m.ExpiresAt != "" {
		if _, err := time.Parse("2006-01-02", m.ExpiresAt); err != nil {
			return fmt.Errorf("expires_at 格式错误，应为 YYYY-MM-DD")
		}
	}

	templateName := strings.TrimSpace(m.Template)
	if templateName == "" {
		return fmt.Errorf("template 不能为空")
	}

	// 检查模板文件是否存在
	templatePath := filepath.Join(s.configDir, "templates", templateName+".json")
	tmpl, err := config.LoadProbeTemplate(templatePath)
	if err != nil {
		return fmt.Errorf("template %q 不存在或无效: %w", templateName, err)
	}

	// 上架前把「模型最终仍为空」挡在写盘之前。
	//
	// 回退链与 InjectVariables 的 {{MODEL}} 一致：行级 request_model > 行级 model >
	// 模板 request_model > 模板 model。native 族刻意不声明模型，是唯一会走到这里的形状——
	// 让它上架，等于写出一条上线即在 wire 上发 `"model": ""` 的通道，只能靠事后看红点发现。
	// 这道闸位于 AdminConfigJSON 整份覆盖**之后**（AdminPublish 先覆盖再校验），故管理员那条
	// 逃生口也绕不过它；这与「管理员可以覆盖 PSC/模板」不冲突：那些是选择，这个是坏配置。
	// 这里**不**校验「非 native 模板不得带行级 model」——那是**提交侧**的规则（用户不该填），
	// 不是配置层的不变量：管理员给模板驱动的行显式写 model 展示名是既有且推荐的做法
	// （`model` 是 DB 业务键，写死它反而让改展示名不断历史）。非 native 行带 model 时
	// `{{MODEL}}` 仍取模板的 request_model，wire 上请求的模型不变，差别只在展示名。
	if firstNonEmpty(m.RequestModel, m.Model, tmpl.RequestModel, tmpl.Model) == "" {
		return fmt.Errorf("模板 %q 未声明模型，需要在监测行填写模型 ID（model）后再上架", templateName)
	}

	return nil
}

// firstNonEmpty 返回首个非空白字符串（已 TrimSpace）。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// validatePSCSegment 校验 PSC 段格式。
//
// 规则直接复用 loader 的 config.ValidateProviderSlug（比历史正则多禁「连续短横线」与「>100 字符」），
// 这是写盘前的最后一道闸，必须比加载期更严或等严，否则就会造出「上架返回 200、热加载整份配置失败」
// 的坏文件——重启即拉不起来。连续短横线尤其危险：monitors.d/ 的文件名是
// `{provider}--{service}--{channel}`，段内再出现 `--` 会让 ParseMonitorFileKey 把分段切错位
// （provider=`sai--ai` 会被解析成 provider=`sai`、service=`ai`），文件从此对不上自己的 PSC。
func validatePSCSegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if err := config.ValidateProviderSlug(value); err != nil {
		return fmt.Errorf("%s 格式无效（%q）: %w", field, value, err)
	}
	return nil
}

// suggestUniqueChannel 生成不冲突的 channel 名（追加 -2、-3...）
func (s *Service) suggestUniqueChannel(provider, service, channel string) string {
	if s.configMonitorExists == nil {
		return channel + "-2"
	}
	for i := 2; i <= 99; i++ {
		candidate := fmt.Sprintf("%s-%d", channel, i)
		if !s.configMonitorExists(provider, service, candidate) {
			return candidate
		}
	}
	return channel + "-new"
}
