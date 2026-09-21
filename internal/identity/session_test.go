package identity

import (
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"
)

var sessionUUIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// embeddedMillis 解出 UUIDv7 前 48 位的毫秒时间戳。
func embeddedMillis(t *testing.T, u string) int64 {
	t.Helper()
	raw, err := hex.DecodeString(strings.ReplaceAll(u, "-", "")[:12])
	if err != nil {
		t.Fatalf("解 %q 的前 48 位失败: %v", u, err)
	}
	var ms int64
	for _, b := range raw {
		ms = ms<<8 | int64(b)
	}
	return ms
}

// TestDeriveSessionUUIDv7_Shape 锁住三个会被网关解析的特征：UUID 形状、version=7、
// variant=RFC4122。少任何一条，这个值就不再像真客户端发出来的 session id。
func TestDeriveSessionUUIDv7_Shape(t *testing.T) {
	now := time.Now()
	got := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", now)

	// 前 48 位必须精确等于窗口起点：位运算里 copy(u[6:], …) 若多覆盖一个字节就会
	// 吃掉时间戳低位，产出的 UUID 形状照样合法、时间却是错的。
	wantMs := sessionWindowStart(sessionKey("prov", "cx", "o-api", "md_1", "gpt-5.6-sol"), now).UnixMilli()
	if gotMs := embeddedMillis(t, got); gotMs != wantMs {
		t.Errorf("内嵌时间戳 = %d，期望窗口起点 %d（差 %dms）", gotMs, wantMs, gotMs-wantMs)
	}
	if !sessionUUIDRe.MatchString(got) {
		t.Fatalf("产出不是 UUID 形状: %q", got)
	}
	if got[14] != '7' {
		t.Errorf("version 位 = %c，期望 7（产出 %q）", got[14], got)
	}
	switch got[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("variant 位 = %c，RFC 4122 只允许 8/9/a/b（产出 %q）", got[19], got)
	}
}

// TestDeriveSessionUUIDv7_StableWithinWindow 是本特性的核心断言：窗口内同值。
// 取样点刻意跨越「窗口起点 + 各种偏移」，而不是只打两次相邻时刻。
func TestDeriveSessionUUIDv7_StableWithinWindow(t *testing.T) {
	base := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := sessionWindowStart(sessionKey("prov", "cx", "o-api", "md_1", "gpt-5.6-sol"), base)

	want := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", start)
	for _, d := range []time.Duration{0, time.Millisecond, 5 * time.Minute, 30 * time.Minute, SessionWindow - time.Millisecond} {
		got := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", start.Add(d))
		if got != want {
			t.Errorf("窗口内 +%v 就换了值: %q != %q", d, got, want)
		}
	}
}

// TestDeriveSessionUUIDv7_RotatesAcrossWindows 锁住另一半：跨窗口必须换新，且新值的
// 内嵌时间戳要跟着前移。只换值不换时间戳等于把「永久冻结」的破绽保留了下来。
func TestDeriveSessionUUIDv7_RotatesAcrossWindows(t *testing.T) {
	base := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	start := sessionWindowStart(sessionKey("prov", "cx", "o-api", "md_1", "gpt-5.6-sol"), base)

	cur := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", start)
	next := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", start.Add(SessionWindow))

	if cur == next {
		t.Fatalf("跨窗口必须换会话 id，两次都是 %q", cur)
	}
	if gotMs, wantMs := embeddedMillis(t, next), embeddedMillis(t, cur)+SessionWindow.Milliseconds(); gotMs != wantMs {
		t.Errorf("新窗口的内嵌时间戳 = %d，期望比上一窗口正好晚一个窗口 = %d", gotMs, wantMs)
	}
}

// TestDeriveSessionUUIDv7_TimestampIsRecentPast 是「为什么不永久固定」那条理由的守卫：
// 内嵌时间戳必须落在 (now-窗口, now] 内。落到未来 = turn 早于会话本身；落得更旧 =
// 会话陈旧，两者都是解析 v7 的网关一眼能看出的矛盾。
func TestDeriveSessionUUIDv7_TimestampIsRecentPast(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 34, 56, 789_000_000, time.UTC)

	// 多跑几条 key：相位是按 key 派生的，单条 key 可能恰好落在安全区间里。
	for _, ch := range []string{"o-api", "o-max-main", "r-kiro-us", "m-mix-1", "o-nat-glm"} {
		got := DeriveSessionUUIDv7("prov", "cx", ch, "md_1", "gpt-5.6-sol", now)
		ms := embeddedMillis(t, got)
		if ms > now.UnixMilli() {
			t.Errorf("%s: 会话创建时刻 %d 在请求时刻 %d 之后——turn 不可能早于会话本身", ch, ms, now.UnixMilli())
		}
		if age := now.UnixMilli() - ms; age >= SessionWindow.Milliseconds() {
			t.Errorf("%s: 会话已存在 %dms，超过窗口 %dms", ch, age, SessionWindow.Milliseconds())
		}
	}
}

// TestDeriveSessionUUIDv7_DistinctPerDimension 锁住派生键的每一维都真的参与派生。
// 漏掉任一维的后果是两条监测行共用一个会话身份——只有上游看得见，我方永远发现不了。
func TestDeriveSessionUUIDv7_DistinctPerDimension(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	base := DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-sol", now)

	cases := map[string]string{
		"provider":      DeriveSessionUUIDv7("prov2", "cx", "o-api", "md_1", "gpt-5.6-sol", now),
		"service":       DeriveSessionUUIDv7("prov", "cc", "o-api", "md_1", "gpt-5.6-sol", now),
		"channel":       DeriveSessionUUIDv7("prov", "cx", "o-api2", "md_1", "gpt-5.6-sol", now),
		"row_key":       DeriveSessionUUIDv7("prov", "cx", "o-api", "md_2", "gpt-5.6-sol", now),
		"request_model": DeriveSessionUUIDv7("prov", "cx", "o-api", "md_1", "gpt-5.6-terra", now),
	}
	for dim, got := range cases {
		if got == base {
			t.Errorf("换了 %s 却产出同一个会话 id %q——该维度没有参与派生", dim, got)
		}
	}
}

// TestSessionKey_IsInjective 守住长度前缀编码：分隔符方案下 ("a|b","c") 与 ("a","b|c")
// 会撞成同一个键。model 是展示名、字符集不受 slug 规则约束，这不是理论风险。
func TestSessionKey_IsInjective(t *testing.T) {
	if sessionKey("a|b", "c") == sessionKey("a", "b|c") {
		t.Error("派生键对含分隔符的输入发生碰撞——长度前缀编码失效了")
	}
	if sessionKey("", "ab") == sessionKey("ab", "") {
		t.Error("派生键对空段发生碰撞")
	}
}

// TestSessionWindowStart_PhaseIsChannelSpecific 守住相位错开。没有它，全站所有监测行
// 的会话都在整点同时轮转、且前 48 位逐字节相同——等于告诉上游「这些账号的会话在同一
// 毫秒创建」，比会话数偏多显眼得多。
func TestSessionWindowStart_PhaseIsChannelSpecific(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	channels := []string{"o-api", "o-max-main", "r-kiro-us", "m-mix-1", "o-nat-glm", "o-sub-jp"}
	seen := map[int64]string{}
	for _, ch := range channels {
		start := sessionWindowStart(sessionKey("prov", "cx", ch), now).UnixMilli()
		if prev, dup := seen[start]; dup {
			// 相位空间是 3.6e6 毫秒，6 条 key 真随机撞上的概率约 4e-6——
			// 出现重复基本只有一个原因：相位没参与派生。
			t.Errorf("%s 与 %s 的窗口起点相同（%d）——相位没有按 key 错开", ch, prev, start)
		}
		seen[start] = ch
	}
}
