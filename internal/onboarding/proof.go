package onboarding

import (
	"time"

	"monitor/internal/apikey"
)

// ProofIssuer 是 apikey.ProofIssuer 的类型别名，保持向后兼容。
type ProofIssuer = apikey.ProofIssuer

// ProofAudience 是自助收录流程签发/验证 proof 时的用途标识。
//
// 与变更请求流程（change.ProofAudience）区分，使二者即便共用同一份 proof_secret
// 也不能互相兑换 proof——否则一次探测可在两条流程各换一次提交，
// 而各自的一次性消费表是两个独立命名空间、拦不住这种跨流程重放。
const ProofAudience = "onboarding"

// NewProofIssuer 创建限定为自助收录用途的 ProofIssuer。
var NewProofIssuer = func(secret string, ttl time.Duration) *ProofIssuer {
	return apikey.NewProofIssuerForAudience(secret, ProofAudience, ttl)
}
