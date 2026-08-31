package onboarding

import (
	"context"
	"fmt"
	"strings"
	"time"

	"monitor/internal/displayname"
)

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
	// 三个 PSC 覆盖值在保存时就校验，别等到上架才报错：它们最终会成为 monitors.d/ 的文件名分段
	// 与公开 URL slug，坏值先入库只会让「保存成功、上架失败」多绕一圈。空串是合法输入（表示清空
	// 覆盖、回到派生值），故这里刻意不带 `v != ""` 短路；存量脏数据由 AdminPublish 的同源校验兜底。
	for _, f := range []struct {
		key    string
		assign func(string)
	}{
		{"target_provider", func(s string) { sub.TargetProvider = s }},
		{"target_service", func(s string) { sub.TargetService = s }},
		{"target_channel", func(s string) { sub.TargetChannel = s }},
	} {
		v, ok := updates[f.key].(string)
		if !ok {
			continue
		}
		if err := validatePSCOverride(f.key, v); err != nil {
			return nil, err
		}
		f.assign(strings.TrimSpace(v))
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
