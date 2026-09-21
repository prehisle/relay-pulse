package identity

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SessionWindow 是会话类标识（session/thread/window/context-window）的滚动窗口长度。
//
// 刻意写死、**不做成配置项**：这个值有一个正确区间，而配置项只会把踩错区间的机会
// 暴露出去。两侧的失败形态都真实存在——
//   - 窗口过短（趋近每请求一换）：上游看到的会话数等于探测次数（5 分钟一次 ⇒ 288/天/行），
//     站长判断这对 codex 订阅账号池而言过于反常。
//   - 窗口过长（趋近永不轮转）：UUIDv7 前 48 位内嵌的是会话**创建时刻**，而同一请求里的
//     `turn_started_at_unix_ms`（{{UNIX_MS}}）是此刻。冻死的会话 id 会让两者越差越远，
//     任何解析 v7 的网关都能看出「一条创建于三个月前、至今仍在发第 25000 轮的会话」。
//     这比会话数偏多更容易被识别——模板从 v4 换到 v7 换来的真实性会被这一条抵消掉。
//
// 一小时同时满足两侧：会话数降到 24/天/行，而「一个持续一小时、每 5 分钟一轮的交互式
// 会话」是真实客户端的常见形态。
const SessionWindow = time.Hour

// DeriveSessionUUIDv7 为一条监测行派生**窗口内稳定**的会话标识（合法 UUIDv7）。
//
// 与 {{RAND_UUID_V7}}（每次注入重新生成）的分工是语义上的，别混用：
//   - 会话级字段（session-id / thread-id / window-id / context_window_id / prompt_cache_key）
//     用本函数——同一条 (provider, service, channel, model) 在一个窗口内发出的所有探测
//     共用一个值，这才是「一条会话里的多个 turn」的真实形态。（唯一例外是恰好跨过窗口
//     边界的重试，那时会换新会话；cx-gpt 族模板 retry:0，现网不会发生。）
//   - 请求级字段（x-client-request-id）与轮次级字段（turn_id / root_turn_id）必须保持每次
//     随机。真 claude-cli 抓包实证：同一次调用的 6 次重试里，唯一在变的就是
//     x-client-request-id。把它冻住不仅不真实，还可能撞上网关的去重/幂等，
//     后果是探针被静默丢弃、面板出现查不出原因的假红。
//
// 派生键含 model 是站长的要求（「同一个通道同一个模型使用固定 session」）。注意这与
// {{STABLE_UUID}} 那套不同：GetUserIDPair 的 channelKey 只有 provider/service/channel、
// 不含 model，所以同通道多模型共用一个账号/安装身份是**有意的**（它们模拟的是同一台机器
// 上的同一个账号），而会话必须逐模型分开——真客户端不会在一条会话里换模型。
//
// model 维度刻意取两个值而不是一个：rowKey 是监测行身份（`model_id`，改展示名不变），
// 保证两条不同的监测行永不撞同一个会话；requestModel 是真正上 wire 的模型，它一变就该换
// 会话（换模型继续同一条会话，真客户端做不出来这种事）。只取其一都会漏掉另一侧。
//
// 时间戳不取窗口整点，而是按通道相位错开（见 sessionWindowStart）。
func DeriveSessionUUIDv7(provider, service, channel, rowKey, requestModel string, now time.Time) string {
	key := sessionKey(provider, service, channel, rowKey, requestModel)
	return deriveUUIDv7(key, sessionWindowStart(key, now))
}

// sessionKey 把四元组拼成无歧义的派生键。
//
// 用长度前缀而不是分隔符拼接：分隔符方案对任意输入并非单射（("a|b","c") 与 ("a","b|c")
// 会撞成同一个键），而 model 是展示名、字符集不像 PSC 那样被 slug 规则约束。撞键的后果
// 是两条不同监测行共用一个会话身份，属于那种「只有上游看得见、我方永远发现不了」的错误。
func sessionKey(fields ...string) string {
	var b strings.Builder
	for _, f := range fields {
		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)
	}
	return b.String()
}

// sessionWindowStart 返回本窗口的会话创建时刻，落在 (now-SessionWindow, now] 内。
//
// 相位按 key 派生、逐行不同，有两个作用：① 各行的会话不会在整点一起换，轮转被摊平到整个
// 窗口里；② 同一家中转商下的多条监测行不会发出前 48 位完全相同的 UUIDv7——那等于告诉对方
// 「这几个账号的会话在同一毫秒创建」，是比会话数偏多明显得多的破绽。
//
// 先减相位再 Truncate 再加回，保证结果恒 ≤ now：会话创建时刻若跑到未来，就成了「turn 早于
// 会话本身」，那是比时间戳陈旧更硬的自相矛盾。
// 相位取整毫秒：UUIDv7 只编码到毫秒，带纳秒的相位会让内嵌时间戳比真实窗口起点早最多
// 1ms，进而让「会话年龄 < 窗口」这条不变量在窗口末端差 1ms 不成立。
func sessionWindowStart(key string, now time.Time) time.Time {
	digest := sha256.Sum256([]byte("session-phase|" + key))
	windowMillis := uint64(SessionWindow / time.Millisecond)
	offset := time.Duration(binary.BigEndian.Uint64(digest[:8])%windowMillis) * time.Millisecond
	return now.Add(-offset).Truncate(SessionWindow).Add(offset)
}

// deriveUUIDv7 产出内嵌 ts 毫秒时间戳的确定性 UUIDv7（version=7、variant=RFC4122）。
//
// 与 DeriveUUIDv4 一样是「同输入必同输出」，区别只在前 48 位不是哈希而是真实时间——
// 这正是 v7 的全部意义，也是本包里唯一一处「哈希只填随机位」的派生。
func deriveUUIDv7(key string, ts time.Time) string {
	digest := sha256.Sum256([]byte("session-uuid|" + key + "|" + strconv.FormatInt(ts.UnixMilli(), 10)))

	var u [16]byte
	binary.BigEndian.PutUint64(u[:8], uint64(ts.UnixMilli())<<16) // 前 48 位时间戳，低 16 位随后被覆盖
	copy(u[6:], digest[:10])
	u[6] = (u[6] & 0x0f) | 0x70 // version 7
	u[8] = (u[8] & 0x3f) | 0x80 // variant RFC4122

	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
