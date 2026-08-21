package onboarding

import (
	"testing"
	"time"

	"monitor/internal/apikey"
)

// TestNewProofIssuer_BoundToOnboardingAudience 锁定本包的 ProofIssuer 构造器确实绑了收录用途。
//
// 签发/校验/过期/篡改这些通用行为由 internal/apikey 的测试覆盖（这里的类型就是它的别名），
// 本包只需守住一件它独有的事：用途隔离没被构造器绕过——否则一次探测签出的 proof 又能在
// 收录与变更两条流程各兑换一次提交（两边的一次性消费表是独立命名空间，拦不住跨流程重放）。
func TestNewProofIssuer_BoundToOnboardingAudience(t *testing.T) {
	const secret = "shared-proof-secret"
	claims := apikey.ProofClaims{
		JobID:          "job-1",
		TestType:       "cc",
		APIURL:         "https://api.example.com",
		KeyFingerprint: "fp",
	}

	proof := NewProofIssuer(secret, 5*time.Minute).Issue(claims)

	if err := apikey.NewProofIssuerForAudience(secret, ProofAudience, 5*time.Minute).Verify(proof, claims); err != nil {
		t.Fatalf("同用途应验证通过: %v", err)
	}
	if err := apikey.NewProofIssuer(secret, 5*time.Minute).Verify(proof, claims); err == nil {
		t.Fatal("收录 proof 不应能被无用途 issuer 验证通过——说明 audience 没进签名")
	}
}
