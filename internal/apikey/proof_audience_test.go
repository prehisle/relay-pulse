package apikey

import (
	"testing"
	"time"
)

// TestProofAudienceIsolation 锁定跨用途 proof 不可互兑。
//
// 背景：自助收录与变更请求共享同一份 proof_secret 和同源指纹密钥。
// 若 proof 不绑定用途，一次真实探测签出的 proof 就能在两条流程各兑换一次提交——
// 两边各自的一次性消费表是独立命名空间，拦不住这种跨流程重放。
func TestProofAudienceIsolation(t *testing.T) {
	const (
		secret = "shared-proof-secret"
		jobID  = "probe-cross-flow"
		apiURL = "https://api.example.com"
		fp     = "fingerprint-abc"
	)
	ttl := 5 * time.Minute

	onboarding := NewProofIssuerForAudience(secret, "onboarding", ttl)
	change := NewProofIssuerForAudience(secret, "change", ttl)

	claims := ProofClaims{JobID: jobID, TestType: "cc", APIURL: apiURL, KeyFingerprint: fp}
	proof := onboarding.Issue(claims)

	if err := onboarding.Verify(proof, claims); err != nil {
		t.Fatalf("同用途验证应通过: %v", err)
	}
	if err := change.Verify(proof, claims); err == nil {
		t.Fatal("onboarding 签发的 proof 不应能在 change 流程验证通过")
	}

	changeProof := change.Issue(claims)
	if err := onboarding.Verify(changeProof, claims); err == nil {
		t.Fatal("change 签发的 proof 不应能在 onboarding 流程验证通过")
	}
}

// TestProofAudienceDefaultIsEmpty NewProofIssuer 等价于空 audience，保持既有行为。
func TestProofAudienceDefaultIsEmpty(t *testing.T) {
	const secret = "s"
	ttl := time.Minute

	plain := NewProofIssuer(secret, ttl)
	explicit := NewProofIssuerForAudience(secret, "", ttl)

	claims := ProofClaims{JobID: "job", TestType: "cc", APIURL: "https://a.com", KeyFingerprint: "fp"}
	proof := plain.Issue(claims)
	if err := explicit.Verify(proof, claims); err != nil {
		t.Fatalf("NewProofIssuer 应等价于空 audience: %v", err)
	}
}
