package apikey

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// baseClaims 是一组「全字段非空」的基准 claims，各用例在其上改一项做对照。
func baseClaims() ProofClaims {
	return ProofClaims{
		JobID:          "job-123",
		TestType:       "cc",
		APIURL:         "https://api.example.com/v1",
		KeyFingerprint: "fingerprint-abc",
		Variant:        "cc-haiku-arith",
		Model:          "glm-5.2",
		MonitorKey:     "prov--cc--o-nat-main",
	}
}

func TestProofIssuer_IssueAndVerify(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)

	proof := pi.Issue(baseClaims())
	if proof == "" {
		t.Fatal("expected non-empty proof")
	}
	if err := pi.Verify(proof, baseClaims()); err != nil {
		t.Fatalf("expected valid proof, got error: %v", err)
	}
}

// IssueWithExpiry 返回的过期时间必须等于编码进 proof 尾部的同一值，
// 且落在 (now, now+ttl] 区间——前端据此做权威倒计时。
func TestProofIssuer_IssueWithExpiry(t *testing.T) {
	ttl := 5 * time.Minute
	pi := NewProofIssuer("test-secret", ttl)

	before := time.Now().Unix()
	proof, expiresAt := pi.IssueWithExpiry(baseClaims())
	after := time.Now().Unix()

	// 与 token 尾部编码值一致
	parts := strings.SplitN(proof, ".", 2)
	if len(parts) != 2 || parts[1] != strconv.FormatInt(expiresAt, 10) {
		t.Fatalf("expiresAt %d not encoded in proof %q", expiresAt, proof)
	}
	// 落在合理区间
	if expiresAt < before+int64(ttl.Seconds()) || expiresAt > after+int64(ttl.Seconds()) {
		t.Errorf("expiresAt %d outside expected window [%d, %d]",
			expiresAt, before+int64(ttl.Seconds()), after+int64(ttl.Seconds()))
	}
	if err := pi.Verify(proof, baseClaims()); err != nil {
		t.Errorf("proof from IssueWithExpiry failed verify: %v", err)
	}
}

// TestProofIssuer_EveryClaimIsBound 逐字段对照：任何一项被换掉都必须验签失败。
//
// 表驱动而非逐个函数，是为了让「新增绑定字段却忘了写用例」显眼——字段少一行就少一条防线，
// 而 Variant/Model/MonitorKey 三条正是这轮新加的（此前可以「测便宜模型、报贵模型」，
// 变更流程还能「测 A 通道、改 B 通道」）。
func TestProofIssuer_EveryClaimIsBound(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)
	proof := pi.Issue(baseClaims())

	tampered := map[string]func(*ProofClaims){
		"jobID":          func(c *ProofClaims) { c.JobID = "job-other" },
		"testType":       func(c *ProofClaims) { c.TestType = "cx" },
		"apiURL":         func(c *ProofClaims) { c.APIURL = "https://api.example.com/other" },
		"keyFingerprint": func(c *ProofClaims) { c.KeyFingerprint = "fingerprint-xyz" },
		"variant":        func(c *ProofClaims) { c.Variant = "cc-opus-ping" },
		"model":          func(c *ProofClaims) { c.Model = "claude-opus-5" },
		"monitorKey":     func(c *ProofClaims) { c.MonitorKey = "prov--cc--o-nat-other" },
		"variant 置空":     func(c *ProofClaims) { c.Variant = "" },
		"model 置空":       func(c *ProofClaims) { c.Model = "" },
		"monitorKey 置空":  func(c *ProofClaims) { c.MonitorKey = "" },
	}
	for name, mutate := range tampered {
		t.Run(name, func(t *testing.T) {
			claims := baseClaims()
			mutate(&claims)
			if err := pi.Verify(proof, claims); err == nil {
				t.Errorf("改动 %s 后仍验签通过", name)
			}
		})
	}
}

// TestProofIssuer_NoDelimiterAmbiguity 载荷用长度前缀而非裸拼接，故「把一段值挪到相邻字段」
// 不能拼出同一份签名。裸 `a|b` 拼接下，(Variant="x|y", Model="") 与 (Variant="x", Model="y")
// 会得到同一串——等于凭空放宽了绑定。
func TestProofIssuer_NoDelimiterAmbiguity(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)

	left := ProofClaims{JobID: "j", Variant: "cc-a|cc-b", Model: ""}
	right := ProofClaims{JobID: "j", Variant: "cc-a", Model: "cc-b"}

	proof := pi.Issue(left)
	if err := pi.Verify(proof, right); err == nil {
		t.Error("字段边界被 | 打穿：两组不同 claims 拼出了同一份签名")
	}
	if err := pi.Verify(proof, left); err != nil {
		t.Errorf("同一组 claims 应验签通过: %v", err)
	}
}

func TestProofIssuer_Expired(t *testing.T) {
	// Use negative TTL to ensure proof is immediately expired
	pi := NewProofIssuer("test-secret", -1*time.Second)

	proof := pi.Issue(baseClaims())
	err := pi.Verify(proof, baseClaims())
	if err == nil {
		t.Fatal("expected error for expired proof")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Errorf("error should mention expiration, got: %v", err)
	}
}

func TestProofIssuer_InvalidFormat(t *testing.T) {
	pi := NewProofIssuer("test-secret", 5*time.Minute)

	if err := pi.Verify("no-dot-separator", baseClaims()); err == nil {
		t.Error("expected error for invalid format")
	}
	if err := pi.Verify("sig.not-a-number", baseClaims()); err == nil {
		t.Error("expected error for invalid expiry")
	}
}

func TestProofIssuer_DifferentSecrets(t *testing.T) {
	pi1 := NewProofIssuer("secret-a", 5*time.Minute)
	pi2 := NewProofIssuer("secret-b", 5*time.Minute)

	proof := pi1.Issue(baseClaims())
	if err := pi2.Verify(proof, baseClaims()); err == nil {
		t.Error("expected error when verifying with different secret")
	}
}
