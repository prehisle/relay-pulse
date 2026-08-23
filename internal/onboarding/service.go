package onboarding

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"monitor/internal/apikey"
	"monitor/internal/config"
	"monitor/internal/displayname"
)

// Service 提供自助收录的核心业务逻辑。
type Service struct {
	store               Store
	cipher              *KeyCipher
	proofIssuer         *ProofIssuer
	cfg                 *config.OnboardingConfig
	configDir           string               // config.yaml 所在目录（用于定位 templates/ 等）
	monitorStore        *config.MonitorStore // monitors.d/ CRUD
	configMonitorExists func(provider, service, channel string) bool
	mu                  sync.RWMutex
}

// NewService 创建 Service。configDir 是 config.yaml 所在目录。
func NewService(store Store, cfg *config.OnboardingConfig, configDir string) (*Service, error) {
	cipher, err := NewKeyCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 API Key 加密器失败: %w", err)
	}

	proofIssuer := NewProofIssuer(cfg.ProofSecret, cfg.ProofTTLDuration)

	return &Service{
		store:       store,
		cipher:      cipher,
		proofIssuer: proofIssuer,
		cfg:         cfg,
		configDir:   configDir,
	}, nil
}

// SetMonitorStore 设置 monitors.d/ 存储（publish 时写入 monitors.d/）
func (s *Service) SetMonitorStore(store *config.MonitorStore) {
	s.monitorStore = store
}

// SetConfigMonitorCheck 设置主配置 PSC 冲突检查回调。
func (s *Service) SetConfigMonitorCheck(fn func(string, string, string) bool) {
	s.configMonitorExists = fn
}

// GetStatus 查询申请状态（用户端）
func (s *Service) GetStatus(ctx context.Context, publicID string) (*Submission, error) {
	return s.store.GetByPublicID(ctx, publicID)
}

// AdminList 管理员列表查询。
// search 为已在 handler 层完成 trim/ToLower/LIKE 转义的模式串，此处仅透传。
func (s *Service) AdminList(ctx context.Context, status, search string, limit, offset int) ([]*Submission, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.List(ctx, status, search, limit, offset)
}

// AdminGetDetail 管理员获取详情（含解密后的 API Key）
func (s *Service) AdminGetDetail(ctx context.Context, publicID string) (*Submission, string, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil || sub == nil {
		return sub, "", err
	}

	apiKey, err := s.cipher.Decrypt(sub.APIKeyEncrypted)
	if err != nil {
		return sub, "", fmt.Errorf("解密 API Key 失败: %w", err)
	}

	return sub, apiKey, nil
}

// AdminUpdate 管理员更新申请
func (s *Service) AdminUpdate(ctx context.Context, publicID string, updates map[string]any) (*Submission, error) {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, fmt.Errorf("申请不存在")
	}

	// 记录通道组成字段原值，用于判断是否需要重派生 channel_code（避免误改 legacy 两段记录）
	origServiceType := sub.ServiceType
	origChannelType := sub.ChannelType
	origChannelSource := sub.ChannelSource
	origChannelGroup := sub.ChannelGroup

	// 同理记录「模板 ↔ 模型」这组的原值：只在真被改动时才跑组合校验，
	// 免得管理员编辑无关字段时被历史脏数据卡住。
	origTemplate := sub.TemplateName
	origModel := sub.Model
	origModelVendor := sub.ModelVendor

	// 应用允许的更新字段
	if v, ok := updates["provider_name"].(string); ok && v != "" {
		name, err := displayname.ValidateProviderName(v)
		if err != nil {
			return nil, err
		}
		sub.ProviderName = name
	}
	if v, ok := updates["website_url"].(string); ok && v != "" {
		sub.WebsiteURL = v
	}
	if v, ok := updates["category"].(string); ok && v != "" {
		sub.Category = v
	}
	if v, ok := updates["service_type"].(string); ok && v != "" {
		st := strings.ToLower(strings.TrimSpace(v))
		if st != "cc" && st != "cx" && st != "gm" {
			return nil, fmt.Errorf("service_type 无效（%q），仅支持 cc/cx/gm", v)
		}
		sub.ServiceType = st
	}
	if v, ok := updates["template_name"].(string); ok && v != "" {
		sub.TemplateName = v
	}
	// model / model_vendor 刻意**不**带 `v != ""` 短路：管理员把模板从 native 改成普通模板时，
	// 必须能把行级模型清空，否则组合校验会一直拒、这条申请再也改不动。
	if v, ok := updates["model"].(string); ok {
		sub.Model = strings.TrimSpace(v)
	}
	if v, ok := updates["model_vendor"].(string); ok {
		sub.ModelVendor = strings.TrimSpace(v)
	}
	if v, ok := updates["sponsor_level"].(string); ok && v != "" {
		sub.SponsorLevel = v
	}
	if v, ok := updates["channel_type"].(string); ok && v != "" {
		ct := strings.ToUpper(strings.TrimSpace(v))
		if ct != "O" && ct != "R" && ct != "M" {
			return nil, fmt.Errorf("channel_type 无效（%q），仅支持 O/R/M", v)
		}
		sub.ChannelType = ct
	}
	if v, ok := updates["channel_source"].(string); ok && v != "" {
		sub.ChannelSource = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := updates["channel_group"].(string); ok {
		sub.ChannelGroup = strings.ToLower(strings.TrimSpace(v))
	}
	if v, ok := updates["target_provider"].(string); ok {
		sub.TargetProvider = v
	}
	if v, ok := updates["target_service"].(string); ok {
		sub.TargetService = v
	}
	if v, ok := updates["target_channel"].(string); ok {
		sub.TargetChannel = v
	}
	if v, ok := updates["base_url"].(string); ok && v != "" {
		sub.BaseURL = v
	}
	if v, ok := updates["channel_name"].(string); ok {
		name, err := displayname.ValidateChannelName(v)
		if err != nil {
			return nil, err
		}
		sub.ChannelName = name
	}
	if v, ok := updates["listed_since"].(string); ok {
		sub.ListedSince = v
	}
	if v, ok := updates["expires_at"].(string); ok {
		sub.ExpiresAt = v
	}
	if v, ok := updates["price_min"].(float64); ok {
		sub.PriceMin = v
	}
	if v, ok := updates["price_max"].(float64); ok {
		sub.PriceMax = v
	}
	if v, ok := updates["admin_note"].(string); ok {
		sub.AdminNote = v
	}
	if v, ok := updates["admin_config_json"].(string); ok {
		sub.AdminConfigJSON = v
	}

	// 仅当通道组成字段（service/type/source/group）真正变化时才重新校验并派生 channel_code，
	// 避免管理员仅编辑无关字段时把 legacy 两段记录意外改写成三段或撞新词表校验。
	if sub.ServiceType != origServiceType ||
		sub.ChannelType != origChannelType ||
		sub.ChannelSource != origChannelSource ||
		sub.ChannelGroup != origChannelGroup {
		source, err := validateChannelTypeSource(sub.ChannelType, sub.ServiceType, sub.ChannelSource)
		if err != nil {
			return nil, err
		}
		group, err := normalizeGroup(sub.ChannelGroup)
		if err != nil {
			return nil, err
		}
		sub.ChannelSource = source
		sub.ChannelGroup = group
		sub.ChannelCode = deriveChannelCode(sub.ChannelType, source, group)
	}

	// 「模板 ↔ 模型/厂商」组合校验：与用户提交同一条规则（native 必须有模型、其余必须没有），
	// 挡住管理员把模板改成 native 却忘了填模型这类改法——那种行上架后会在 wire 上发空 model。
	if sub.TemplateName != origTemplate || sub.Model != origModel || sub.ModelVendor != origModelVendor {
		model, vendor, err := ValidateModelSelection(sub.TemplateName, sub.Model, sub.ModelVendor)
		if err != nil {
			return nil, err
		}
		sub.Model = model
		sub.ModelVendor = vendor
	}
	sub.UpdatedAt = time.Now().Unix()

	if err := s.store.Update(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

// AdminDelete 删除申请（硬删除，已上架的不允许删除）
func (s *Service) AdminDelete(ctx context.Context, publicID string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能删除，请先在通道管理中下架")
	}
	return s.store.DeleteByPublicID(ctx, publicID)
}

// AdminReject 驳回申请
func (s *Service) AdminReject(ctx context.Context, publicID, note string) error {
	sub, err := s.store.GetByPublicID(ctx, publicID)
	if err != nil {
		return err
	}
	if sub == nil {
		return fmt.Errorf("申请不存在")
	}
	if sub.Status == StatusPublished {
		return fmt.Errorf("已上架的申请不能驳回")
	}

	now := time.Now().Unix()
	sub.Status = StatusRejected
	sub.AdminNote = note
	sub.ReviewedAt = &now
	sub.UpdatedAt = now
	return s.store.Update(ctx, sub)
}

// IssueProof 签发测试证明（供内联探测调用）。claims 的 KeyFingerprint 由本方法按 apiKey 计算，
// 调用方不必（也不应该）自己算。
func (s *Service) IssueProof(claims apikey.ProofClaims, apiKey string) string {
	proof, _ := s.IssueProofWithExpiry(claims, apiKey)
	return proof
}

// IssueProofWithExpiry 签发测试证明，并返回其绝对过期时间（Unix 秒），供 API 层下发前端。
func (s *Service) IssueProofWithExpiry(claims apikey.ProofClaims, apiKey string) (string, int64) {
	claims.KeyFingerprint = s.cipher.Fingerprint(apiKey)
	return s.proofIssuer.IssueWithExpiry(claims)
}

// BuildServiceConfigFromSubmission 将 Submission（连同已解密的 apiKey）翻译成 ServiceConfig。
//
// 该函数是"用户提交字段" → "运行时监测配置"的官方映射点：
//   - 发布到 monitors.d/ 时（AdminPublish）调用
//   - 管理后台对申请做即时探测（AdminTestSubmission）时调用
//   - 用户提交前自助探测（OnboardingTest）时调用（构造虚拟 Submission）
//
// 字段映射规则：
//   - PSC 默认派生：provider=lower(ProviderName 去空格转-)，service=ServiceType，channel=ChannelCode
//   - 管理员可在审核阶段通过 TargetProvider/TargetService/TargetChannel 覆盖 PSC（用于规范化命名）
//
// 注意：返回的 cfg 字段尚未经过模板填充和 Duration 派生；调用方如需用于内联探测，
// 应再过一次 config.ResolveSingleMonitor。
func BuildServiceConfigFromSubmission(sub *Submission, apiKey string) config.ServiceConfig {
	if sub == nil {
		return config.ServiceConfig{}
	}

	// 派生默认 PSC 标识
	providerSlug := strings.ToLower(strings.ReplaceAll(sub.ProviderName, " ", "-"))
	serviceType := sub.ServiceType
	channelCode := sub.ChannelCode

	// 管理员可覆盖最终发布时的 PSC 标识
	if v := strings.TrimSpace(sub.TargetProvider); v != "" {
		providerSlug = v
	}
	if v := strings.TrimSpace(sub.TargetService); v != "" {
		serviceType = v
	}
	if v := strings.TrimSpace(sub.TargetChannel); v != "" {
		channelCode = v
	}

	cfg := config.ServiceConfig{
		Provider:     providerSlug,
		ProviderName: sub.ProviderName,
		ProviderURL:  sub.WebsiteURL,
		Service:      serviceType,
		Channel:      channelCode,
		ChannelName:  sub.ChannelName,
		Template:     sub.TemplateName,
		// 行级模型只对 native 族非空；非 native 提交这两项恒为空，模板解析时按
		// config > template 回退链自动填上模板声明的值（lifecycle.resolveTemplateForMonitor）。
		Model:        sub.Model,
		ModelVendor:  sub.ModelVendor,
		BaseURL:      sub.BaseURL,
		APIKey:       apiKey,
		Category:     sub.Category,
		ListedSince:  sub.ListedSince,
		ExpiresAt:    sub.ExpiresAt,
		SponsorLevel: config.SponsorLevel(sub.SponsorLevel),
	}
	if sub.PriceMin != 0 {
		v := sub.PriceMin
		cfg.PriceMin = &v
	}
	if sub.PriceMax != 0 {
		v := sub.PriceMax
		cfg.PriceMax = &v
	}
	return cfg
}

// buildServiceConfig 是 BuildServiceConfigFromSubmission 的 method 包装，
// 保留以兼容 Service 内部既有调用方（AdminPublish 等）。
func (s *Service) buildServiceConfig(sub *Submission, apiKey string) config.ServiceConfig {
	return BuildServiceConfigFromSubmission(sub, apiKey)
}
