package apikey

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ProofIssuer 签发和验证探测成功后的测试证明。
// proof 是 HMAC-SHA256 签名令牌，绑定测试参数和过期时间。
type ProofIssuer struct {
	secret   []byte
	audience string
	ttl      time.Duration
}

// ProofClaims 是一次探测的完整身份：proof 声明「用这把 key、对这个地址、按这个形态、
// 测了这个模型，成功了」。**签发与校验必须逐字段相同**，任何一项对不上即视为伪造。
//
// 为什么每一项都必须进签名：
//   - JobID：一次探测一张票（配合一次性消费表挡重放）；
//   - TestType / APIURL / KeyFingerprint：测的是哪条服务、哪个地址、哪把 key；
//   - Variant：**探针模板**。不绑它就能「用最便宜的 haiku 测通、提交时换成 opus」，
//     上架即恒红；变更流程同理——要证明的是「这条通道照旧能用」，不是随便挑个模型作证。
//     前端切模板时会清掉测试状态，但那是 UX 不是安全边界：直接构造 HTTP 请求就绕过了。
//   - Model：行级模型（只有第一方厂商模型非空）。同理挡「测便宜模型、报贵模型」。
//   - MonitorKey：变更流程的目标通道。一把 key 常同时认证多条通道，不绑就能
//     「测 A 通道、改 B 通道」。收录流程没有既存通道，恒为空。
//
// 某条不适用于当前流程时留空即可——空值同样参与签名，故「该空的地方非空」也会验签失败。
type ProofClaims struct {
	JobID          string
	TestType       string
	APIURL         string
	KeyFingerprint string
	Variant        string
	Model          string
	MonitorKey     string
}

// NewProofIssuer 创建不限定用途的 ProofIssuer。
func NewProofIssuer(secret string, ttl time.Duration) *ProofIssuer {
	return NewProofIssuerForAudience(secret, "", ttl)
}

// NewProofIssuerForAudience 创建限定用途（audience）的 ProofIssuer。
//
// audience 参与签名，因此不同用途签发的 proof 互不通用。这是必要的：
// 自助收录与变更请求共享同一份 proof_secret 与指纹密钥，若不区分用途，
// 一次探测签出的 proof 能在两条流程各兑换一次提交——各自的一次性消费表
// 拦不住跨流程重放，因为它们本就是两个独立命名空间。
func NewProofIssuerForAudience(secret, audience string, ttl time.Duration) *ProofIssuer {
	return &ProofIssuer{
		secret:   []byte(secret),
		audience: audience,
		ttl:      ttl,
	}
}

// proofPayloadVersion 是签名载荷的版本前缀。绑定字段集变化时 bump，旧 proof 随即失效
// （TTL 只有几分钟，代价是发版瞬间在途测试需要重测，换来的是不必维护多套载荷解析）。
const proofPayloadVersion = "v2"

// proofPayload 构建待签名的 payload：版本 | audience | 各字段（长度前缀） | expiresAt。
//
// 每个字段写成 `|<len>:<value>`，而不是裸值用分隔符拼接：URL、模板名之类的字段一旦含有
// 分隔符，裸拼接就会出现「不同 claims 拼出同一串」的歧义，等于凭空放宽了绑定。
func (pi *ProofIssuer) proofPayload(claims ProofClaims, expiresAt int64) string {
	var b strings.Builder
	b.WriteString(proofPayloadVersion)
	for _, field := range []string{
		pi.audience,
		claims.JobID,
		claims.TestType,
		claims.APIURL,
		claims.KeyFingerprint,
		claims.Variant,
		claims.Model,
		claims.MonitorKey,
	} {
		fmt.Fprintf(&b, "|%d:%s", len(field), field)
	}
	b.WriteString("|")
	b.WriteString(strconv.FormatInt(expiresAt, 10))
	return b.String()
}

// sign 计算给定 claims 与过期时间的十六进制签名。
func (pi *ProofIssuer) sign(claims ProofClaims, expiresAt int64) string {
	mac := hmac.New(sha256.New, pi.secret)
	mac.Write([]byte(pi.proofPayload(claims, expiresAt)))
	return hex.EncodeToString(mac.Sum(nil))
}

// Issue 签发测试证明。返回格式：signature.expiresAt
func (pi *ProofIssuer) Issue(claims ProofClaims) string {
	proof, _ := pi.IssueWithExpiry(claims)
	return proof
}

// IssueWithExpiry 签发测试证明，并额外返回该 proof 的绝对过期时间（Unix 秒）。
// 过期时间即编码进 proof 字符串尾部的同一值，供前端做权威倒计时/提交前校验，
// 避免前端硬编码 TTL 与后端 proof_ttl 漂移。
func (pi *ProofIssuer) IssueWithExpiry(claims ProofClaims) (string, int64) {
	expiresAt := time.Now().Add(pi.ttl).Unix()
	return fmt.Sprintf("%s.%d", pi.sign(claims, expiresAt), expiresAt), expiresAt
}

// Verify 验证测试证明的签名和有效期。
func (pi *ProofIssuer) Verify(proof string, claims ProofClaims) error {
	sig, expiresAtStr, found := strings.Cut(proof, ".")
	if !found {
		return fmt.Errorf("proof 格式无效")
	}

	expiresAt, err := strconv.ParseInt(expiresAtStr, 10, 64)
	if err != nil {
		return fmt.Errorf("proof 过期时间无效")
	}

	if time.Now().Unix() > expiresAt {
		return fmt.Errorf("proof 已过期")
	}

	if !hmac.Equal([]byte(sig), []byte(pi.sign(claims, expiresAt))) {
		return fmt.Errorf("proof 签名不匹配")
	}

	return nil
}
