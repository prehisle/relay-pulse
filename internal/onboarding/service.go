package onboarding

import (
	"context"
	"fmt"
	"sync"

	"monitor/internal/apikey"
	"monitor/internal/config"
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
