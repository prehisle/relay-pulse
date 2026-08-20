package change

import (
	"fmt"
	"sync"

	"monitor/internal/apikey"
	"monitor/internal/config"
	"monitor/internal/logger"
	"monitor/internal/probe"
)

// AuthIndex 运行时 API Key 指纹索引。
// 基于当前配置（已合并 env 覆盖）构建内存索引，热更新时原子重建。
type AuthIndex struct {
	mu    sync.RWMutex
	index map[string][]AuthCandidate // HMAC 指纹 → candidates

	// revokedSHA256 是「已公开泄露 API Key」的无密钥 sha256 摘要集合（来自配置里的拒绝名单）。
	// 认证时先查这里：命中即拒，与该 key 是否仍是某通道的在用 key 无关。
	revokedSHA256 map[string]struct{}

	// revokedAuthFP 是上面那批 key 里**当前仍出现在配置中**的那些，按 cipher 的 HMAC 指纹存。
	// 已落库的历史变更请求只保存 HMAC 指纹、不保存明文，因此 admin 侧要追溯校验只能靠这一份
	// 派生集合（覆盖面 = 泄露名单 ∩ 当前配置，正好是历史请求当初能认证成功的那批 key）。
	revokedAuthFP map[string]struct{}
}

// NewAuthIndex 创建空索引。
func NewAuthIndex() *AuthIndex {
	return &AuthIndex{
		index:         make(map[string][]AuthCandidate),
		revokedSHA256: make(map[string]struct{}),
		revokedAuthFP: make(map[string]struct{}),
	}
}

// Rebuild 基于当前配置和 cipher 重建索引。
// monitorStore 用于判断 apply_mode（auto/manual）。
// revokedKeySHA256 是已泄露 key 的 sha256 摘要集合（可为 nil=功能关闭），整体替换而非并集——
// 从名单里移除一条必须能通过热更新生效。
func (ai *AuthIndex) Rebuild(
	monitors []config.ServiceConfig,
	cipher *apikey.KeyCipher,
	monitorStore *config.MonitorStore,
	revokedKeySHA256 map[string]struct{},
) {
	newIndex := make(map[string][]AuthCandidate)

	// 拷贝入参，避免与调用方共享同一 map（调用方之后改动不得串改索引内部状态）。
	newRevoked := make(map[string]struct{}, len(revokedKeySHA256))
	for h := range revokedKeySHA256 {
		newRevoked[h] = struct{}{}
	}

	// 派生 HMAC 指纹集合：覆盖**所有**带 key 的配置行，不套用下方索引的跳过规则
	// （disabled/子通道当初可能是启用的父通道，其历史请求同样要能被追溯拦下）。
	newRevokedFP := make(map[string]struct{})
	if len(newRevoked) > 0 {
		for _, m := range monitors {
			if m.APIKey == "" {
				continue
			}
			if _, hit := newRevoked[config.RevokedKeySHA256Hex(m.APIKey)]; hit {
				newRevokedFP[cipher.Fingerprint(m.APIKey)] = struct{}{}
			}
		}
	}

	for _, m := range monitors {
		// 跳过 disabled、有 parent 的子通道、无 API Key 的条目
		if m.Disabled || m.Parent != "" || m.APIKey == "" {
			continue
		}

		fingerprint := cipher.Fingerprint(m.APIKey)
		pscKey := fmt.Sprintf("%s--%s--%s", m.Provider, m.Service, m.Channel)

		applyMode := "manual"
		if monitorStore != nil {
			if mf, err := monitorStore.Get(pscKey); err == nil && mf != nil {
				applyMode = "auto"
			}
		}

		candidate := AuthCandidate{
			Provider:     m.Provider,
			Service:      m.Service,
			Channel:      m.Channel,
			MonitorKey:   pscKey,
			ApplyMode:    applyMode,
			ProviderName: m.ProviderName,
			ProviderURL:  m.ProviderURL,
			ChannelName:  m.ChannelName,
			Category:     m.Category,
			SponsorLevel: string(m.SponsorLevel),
			ListedSince:  m.ListedSince,
			ExpiresAt:    m.ExpiresAt,
			BaseURL:      m.BaseURL,
			KeyLast4:     apikey.Last4(m.APIKey),
		}
		if m.PriceMin != nil {
			candidate.PriceMin = fmt.Sprintf("%g", *m.PriceMin)
		}
		if m.PriceMax != nil {
			candidate.PriceMax = fmt.Sprintf("%g", *m.PriceMax)
		}

		// 使用 provider name 回退
		if candidate.ProviderName == "" {
			candidate.ProviderName = m.Provider
		}
		if candidate.ChannelName == "" {
			candidate.ChannelName = m.Channel
		}

		// 填充测试元数据（从 probe 注册表查询）
		candidate.TestType = m.Service
		candidate.TestTypeName = m.Service
		if tt, ok := probe.GetTestType(m.Service); ok {
			candidate.TestType = tt.ID
			candidate.TestTypeName = tt.Name
			candidate.DefaultTestVariant = defaultTestVariant(tt, m.Template)
			if len(tt.Variants) > 0 {
				candidate.TestVariants = make([]TestVariant, 0, len(tt.Variants))
				for _, v := range tt.Variants {
					if v == nil {
						continue
					}
					candidate.TestVariants = append(candidate.TestVariants, TestVariant{
						ID:    v.ID,
						Order: v.Order,
					})
				}
			}
		}

		newIndex[fingerprint] = append(newIndex[fingerprint], candidate)
	}

	ai.mu.Lock()
	ai.index = newIndex
	ai.revokedSHA256 = newRevoked
	ai.revokedAuthFP = newRevokedFP
	ai.mu.Unlock()

	total := 0
	for _, cs := range newIndex {
		total += len(cs)
	}
	logger.Info("change", "API Key 认证索引已重建",
		"keys", len(newIndex), "candidates", total,
		"revoked_keys", len(newRevoked), "revoked_in_use", len(newRevokedFP))
}

// defaultTestVariant 决定变更流程测试步默认选中的探针模板。
//
// 优先用**这条通道自己正在跑的模板**：改 base_url / 轮换 key 时要证明的是「这条通道照旧能用」，
// 拿注册表的通用默认值去探，等于让中转商为一个自己根本没上架的模型作证——2026-08-06 新增
// cc-fable-ping-20260806 抢走 cc 默认值后，老通道走变更流程默认就在探 fable-5，一律测不过。
//
// 只在模板确实是该 service 已注册的变体时采用（模板文件被删、或历史行填了别的 service 的
// 模板名时回退），避免把一个探测端拿不到的模板名当默认值发给前端。
func defaultTestVariant(tt *probe.TestType, monitorTemplate string) string {
	if tt == nil {
		return ""
	}
	if monitorTemplate != "" {
		for _, v := range tt.Variants {
			if v != nil && v.ID == monitorTemplate {
				return v.ID
			}
		}
	}
	return tt.DefaultVariant
}

// Lookup 根据 API Key 查找匹配的通道候选。
//
// revoked=true 表示该 key 命中已公开泄露拒绝名单，此时不返回任何候选——无论它是否仍是
// 某通道的在用 key。revoked=false 时空切片表示无匹配（不区分 key 不存在和 key 存在但无通道）。
func (ai *AuthIndex) Lookup(apiKey string, cipher *apikey.KeyCipher) (candidates []AuthCandidate, revoked bool) {
	revokedHash := config.RevokedKeySHA256Hex(apiKey)
	fingerprint := cipher.Fingerprint(apiKey)

	// 名单查询与候选查询共用同一次读锁，避免两者之间夹进一次热更新替换。
	ai.mu.RLock()
	defer ai.mu.RUnlock()

	if _, hit := ai.revokedSHA256[revokedHash]; hit {
		return nil, true
	}

	found := ai.index[fingerprint]
	if len(found) == 0 {
		return nil, false
	}

	// 返回深拷贝，防止外部修改内部状态
	result := make([]AuthCandidate, len(found))
	copy(result, found)
	return result, false
}

// IsRevokedKey 判断明文 API Key 是否命中已公开泄露拒绝名单。
// 与 Lookup 的名单判定同一集合，供「新 key 不得又是泄露 key」这类边界校验单独调用。
func (ai *AuthIndex) IsRevokedKey(apiKey string) bool {
	hash := config.RevokedKeySHA256Hex(apiKey)
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	_, hit := ai.revokedSHA256[hash]
	return hit
}

// IsRevokedAuthFingerprint 判断某个已落库请求的认证指纹是否属于已泄露 key。
// 供 admin 批准/应用路径追溯校验名单生效前提交的历史请求。空指纹恒为 false。
func (ai *AuthIndex) IsRevokedAuthFingerprint(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	_, hit := ai.revokedAuthFP[fingerprint]
	return hit
}
