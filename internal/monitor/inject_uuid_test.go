package monitor

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"monitor/internal/config"
	"monitor/internal/identity"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// injectWith 用一份只含给定 body 的最小配置跑注入，返回替换后的 body。
func injectWith(t *testing.T, body string) (string, map[string]string) {
	t.Helper()
	cfg := &config.ServiceConfig{
		Provider: "prov", Service: "cx", Channel: "chan",
		Model: "M", RequestModel: "m-1",
		BaseURL: "https://example.invalid", APIKey: "k",
		Method: "POST", URLPattern: "{{BASE_URL}}/v1/responses",
		Body:    body,
		Headers: map[string]string{"x-a": "{{RAND_UUID_V7}}", "x-b": "{{STABLE_UUID}}"},
	}
	gotBody, gotHeaders := "", map[string]string{}
	_, gotBody, gotHeaders, _, _, _ = InjectVariables(cfg, identity.NewUserIDManager())
	return gotBody, gotHeaders
}

// TestInjectRandUUIDv7_IsVersion7WithLiveTimestamp 锁住 v7 的两个可被网关检测的
// 特征：version 位，以及前 48 位必须是**当前**毫秒时间戳。v4 两条都做不到。
func TestInjectRandUUIDv7_IsVersion7WithLiveTimestamp(t *testing.T) {
	before := time.Now().UnixMilli()
	body, _ := injectWith(t, `{"a":"{{RAND_UUID_V7}}"}`)
	after := time.Now().UnixMilli()

	got := strings.TrimSuffix(strings.TrimPrefix(body, `{"a":"`), `"}`)
	if !uuidRe.MatchString(got) {
		t.Fatalf("产出不是 UUID 形状: %q", got)
	}
	if got[14] != '7' {
		t.Errorf("version 位 = %c，期望 7（产出 %q）", got[14], got)
	}
	raw, err := hex.DecodeString(strings.ReplaceAll(got, "-", "")[:12])
	if err != nil {
		t.Fatalf("解前 48 位失败: %v", err)
	}
	var ms int64
	for _, b := range raw {
		ms = ms<<8 | int64(b)
	}
	if ms < before || ms > after {
		t.Errorf("内嵌时间戳 %d 不在本次调用区间 [%d, %d] 内——v7 的意义就在于它是真时间",
			ms, before, after)
	}
}

// TestNewUUIDv7_FailsLoudOnEntropyError 熵源失败时必须返回空串、不得伪造一个
// v4 顶替。退回 v4 会让请求照常发出、上游照常 200、探针照常绿，熵源故障彻底
// 不可见——那正是本仓「不要静默兜底」准则针对的形态。
func TestNewUUIDv7_FailsLoudOnEntropyError(t *testing.T) {
	orig := uuidV7Source
	t.Cleanup(func() { uuidV7Source = orig })
	uuidV7Source = func() (uuid.UUID, error) {
		return uuid.UUID{}, errors.New("熵源故障（注入）")
	}

	got := newUUIDv7()
	if got != "" {
		t.Errorf("熵源失败应返回空串让故障可见，实际返回 %q", got)
	}
	if uuidRe.MatchString(got) {
		t.Error("返回了一个像模像样的 UUID——那就是在静默伪造")
	}
}

// TestInjectPlaceholders_SameNameSameValue 同名处处同值、异名互不相同。
// 模板靠这条复现「session/thread/window 同源、turn 另一组」的关系。
func TestInjectPlaceholders_SameNameSameValue(t *testing.T) {
	body, headers := injectWith(t,
		`{"s1":"{{RAND_UUID_V7}}","s2":"{{RAND_UUID_V7}}","t1":"{{RAND_UUID_V7_2}}",`+
			`"k1":"{{STABLE_UUID}}","k2":"{{STABLE_UUID}}","k3":"{{STABLE_UUID2}}"}`)

	// 先确认真的发生了替换：占位符原样残留时下面的「同名同值」断言也会通过，
	// 删掉某个替换项这条测试却照常绿（codex 复审指出的真空路径）。
	if strings.Contains(body, "{{") {
		t.Fatalf("有占位符没被替换，后面的同值断言无意义: %s", body)
	}

	vals := map[string]string{}
	for _, kv := range strings.Split(strings.Trim(body, "{}"), ",") {
		parts := strings.SplitN(kv, ":", 2)
		vals[strings.Trim(parts[0], `"`)] = strings.Trim(parts[1], `"`)
	}
	for _, k := range []string{"s1", "s2", "t1", "k1", "k2", "k3"} {
		if !uuidRe.MatchString(vals[k]) {
			t.Fatalf("槽位 %s = %q 不是 UUID，说明没被真正替换", k, vals[k])
		}
	}
	if vals["s1"] != vals["s2"] {
		t.Errorf("同名占位符取值不一致: %q vs %q", vals["s1"], vals["s2"])
	}
	if vals["k1"] != vals["k2"] {
		t.Errorf("同名占位符取值不一致: %q vs %q", vals["k1"], vals["k2"])
	}
	if vals["s1"] == vals["t1"] {
		t.Error("{{RAND_UUID_V7}} 与 {{RAND_UUID_V7_2}} 必须是两个不同的值")
	}
	if vals["k1"] == vals["k3"] {
		t.Error("{{STABLE_UUID}} 与 {{STABLE_UUID2}} 必须是两个不同的值")
	}
	if vals["s1"] == vals["k1"] {
		t.Error("随机与稳定两族不应取到同值")
	}
	// headers 与 body 共用同一个 replacer，同名必须跨两侧也同值
	if headers["x-a"] != vals["s1"] {
		t.Errorf("header 与 body 的同名占位符不一致: %q vs %q", headers["x-a"], vals["s1"])
	}
	if headers["x-b"] != vals["k1"] {
		t.Errorf("header 与 body 的同名占位符不一致: %q vs %q", headers["x-b"], vals["k1"])
	}
}

// TestInjectPlaceholders_NoPrefixShadowing 所有 UUID 类占位符同时出现在一份
// 模板里，确认没有互相吃前缀。
//
// 这组名字互为前缀扩展（{{RAND_UUID}} / {{RAND_UUID2}} / {{RAND_UUID_V7}} /
// {{RAND_UUID_V7_2}}，以及 {{STABLE_UUID}} / {{STABLE_UUID2}}），而
// strings.NewReplacer 按**参数顺序**比较、短名在前。它们靠「以 }} 收尾」才在
// 分叉字符上错开——这是个容易被后续改名破坏的隐式前提：任何人加一个
// {{RAND_UUID_V8}} 之前，先让这条测试跑一遍。产出里残留 `_V7}}` 这类尾巴，
// 就是被吃了前缀。
func TestInjectPlaceholders_NoPrefixShadowing(t *testing.T) {
	body, _ := injectWith(t, `{"a":"{{RAND_UUID}}","b":"{{RAND_UUID2}}",`+
		`"c":"{{RAND_UUID_V7}}","d":"{{RAND_UUID_V7_2}}",`+
		`"e":"{{STABLE_UUID}}","f":"{{STABLE_UUID2}}"}`)

	if strings.Contains(body, "{{") || strings.Contains(body, "}}") {
		t.Fatalf("有占位符没被替换: %s", body)
	}

	vals := map[string]string{}
	for _, kv := range strings.Split(strings.Trim(body, "{}"), ",") {
		parts := strings.SplitN(kv, ":", 2)
		vals[strings.Trim(parts[0], `"`)] = strings.Trim(parts[1], `"`)
	}
	// 六个槽位必须各自是一个完整合法 UUID，且两两互不相同
	seen := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		v := vals[k]
		if !uuidRe.MatchString(v) {
			t.Errorf("槽位 %s = %q 不是完整 UUID（被吃前缀时这里会带尾巴）", k, v)
		}
		if prev, dup := seen[v]; dup {
			t.Errorf("槽位 %s 与 %s 取到同值 %q，六个占位符应互不相同", k, prev, v)
		}
		seen[v] = k
	}
	// 版本位各就各位：a/b 是 v4，c/d 是 v7，e/f 是 v4
	for _, k := range []string{"a", "b", "e", "f"} {
		if vals[k][14] != '4' {
			t.Errorf("槽位 %s 应是 v4，实际 version 位 %c（%q）", k, vals[k][14], vals[k])
		}
	}
	for _, k := range []string{"c", "d"} {
		if vals[k][14] != '7' {
			t.Errorf("槽位 %s 应是 v7，实际 version 位 %c（%q）", k, vals[k][14], vals[k])
		}
	}
}

// TestInjectStableUUID_PerChannelStableAndConformant 每通道稳定、跨通道不同、
// 且格式合法——三条缺一，模板伪装的客户端标识就站不住。
func TestInjectStableUUID_PerChannelStableAndConformant(t *testing.T) {
	uid := identity.NewUserIDManager()
	inject := func(channel string) string {
		cfg := &config.ServiceConfig{
			Provider: "prov", Service: "cx", Channel: channel,
			Model: "M", BaseURL: "https://example.invalid", APIKey: "k",
			Method: "POST", URLPattern: "{{BASE_URL}}", Body: `{"v":"{{STABLE_UUID}}"}`,
		}
		_, b, _, _, _, _ := InjectVariables(cfg, uid)
		return strings.TrimSuffix(strings.TrimPrefix(b, `{"v":"`), `"}`)
	}
	a1, a2, other := inject("chan-a"), inject("chan-a"), inject("chan-b")
	if a1 != a2 {
		t.Errorf("同通道两次注入应稳定: %q vs %q", a1, a2)
	}
	if a1 == other {
		t.Errorf("不同通道必须不同值，都是 %q", a1)
	}
	if !uuidRe.MatchString(a1) || a1[14] != '4' {
		t.Errorf("应是合法 v4: %q", a1)
	}
	switch a1[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Errorf("variant 位非法: %c（%q）", a1[19], a1)
	}
}

// TestInjectUnixMS_IsLiveJSONNumber 不带引号写即落成 JSON 数字，且是当前时刻。
// 模板里的时间戳字段靠它与 v7 UUID 的内嵌时间戳保持自洽。
func TestInjectUnixMS_IsLiveJSONNumber(t *testing.T) {
	before := time.Now().UnixMilli()
	body, _ := injectWith(t, `{"ts":{{UNIX_MS}}}`)
	after := time.Now().UnixMilli()

	raw := strings.TrimSuffix(strings.TrimPrefix(body, `{"ts":`), `}`)
	if strings.Contains(raw, `"`) {
		t.Fatalf("不应带引号（否则落成 JSON 字符串而非数字）: %q", raw)
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		t.Fatalf("不是合法数字: %q", raw)
	}
	if ms < before || ms > after {
		t.Errorf("时间戳 %d 不在本次调用区间 [%d, %d] 内", ms, before, after)
	}
}
