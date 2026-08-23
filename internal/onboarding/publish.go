package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"monitor/internal/config"
	"monitor/internal/logger"
)

// pscSegmentPattern 校验 PSC 段仅允许小写字母、数字、短横线，且不能以短横线开头或结尾。
var pscSegmentPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

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

	// 派生路径下（无 AdminConfigJSON 整份覆盖）：若展示名派生出的 provider slug 非法且未覆盖
	// target_provider，返回可操作指引（区别于下方通用 PSC 校验的难懂错误 + 500）。
	// AdminConfigJSON 覆盖路径自带 Provider，仍走下方 validateMonitorConfig 通用校验；
	// 管理员填了非法 target_provider/target_service/target_channel 覆盖值属另一类（预存、对称）
	// 问题，本轮不在此处理，仍走通用校验。
	if sub.AdminConfigJSON == "" &&
		strings.TrimSpace(sub.TargetProvider) == "" &&
		config.ValidateProviderSlug(monitorCfg.Provider) != nil {
		return &InvalidProviderSlugError{ProviderName: sub.ProviderName, DerivedSlug: monitorCfg.Provider}
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

// validatePSCSegment 校验 PSC 段格式
func validatePSCSegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if !pscSegmentPattern.MatchString(value) {
		return fmt.Errorf("%s 格式无效（%q），仅允许小写字母、数字、短横线，且不能以短横线开头或结尾", field, value)
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
