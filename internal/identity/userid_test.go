package identity

import (
	"regexp"
	"testing"
)

// uuidShape 只校验形状，version/variant 位单独断言——形状对但格式位不对，
// 正是 DeriveUUID 的已知缺陷（见 DeriveUUIDv4 注释），必须区分开。
var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// TestDeriveUUIDv4_IsRFC4122Conformant 锁住格式位。探测模板拿它冒充真实客户端
// 标识，一个 variant 非法的串在任何做解析校验的网关那里都是破绽。
func TestDeriveUUIDv4_IsRFC4122Conformant(t *testing.T) {
	// 多个输入一起看：单个样本可能碰巧落在合法位上，掩盖「根本没设格式位」。
	for _, seed := range []string{"", "a", "abc", "0123456789abcdef", "通道/服务/名字"} {
		got := DeriveUUIDv4(sha256Hex(seed), "stable_1")
		if !uuidShape.MatchString(got) {
			t.Fatalf("seed=%q 产出 %q，形状就不对", seed, got)
		}
		if v := got[14]; v != '4' {
			t.Errorf("seed=%q version 位 = %c，期望 4（产出 %q）", seed, v, got)
		}
		switch got[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Errorf("seed=%q variant 位 = %c，RFC 4122 只允许 8/9/a/b（产出 %q）", seed, got[19], got)
		}
	}
}

// TestDeriveUUIDv4_StableAndSaltSeparated 稳定性与 salt 隔离：前者是「每通道
// 固定身份」的前提，后者是「installation id 与 account id 必须是两个值」的前提。
func TestDeriveUUIDv4_StableAndSaltSeparated(t *testing.T) {
	h := sha256Hex("provider/cx/channel")
	a1, a2 := DeriveUUIDv4(h, "stable_1"), DeriveUUIDv4(h, "stable_1")
	if a1 != a2 {
		t.Errorf("同 hash 同 salt 应稳定，得到 %q 与 %q", a1, a2)
	}
	if b := DeriveUUIDv4(h, "stable_2"); b == a1 {
		t.Errorf("不同 salt 必须产出不同值，都是 %q", b)
	}
	if other := DeriveUUIDv4(sha256Hex("provider/cx/另一通道"), "stable_1"); other == a1 {
		t.Errorf("不同通道必须产出不同值，都是 %q", other)
	}
	// 与旧函数必须不同：若哪天有人把 DeriveUUIDv4 实现成 DeriveUUID 的别名，
	// 上面的断言全都还能过，只有这条会红。
	if DeriveUUIDv4(h, "stable_1") == DeriveUUID(h, "stable_1") {
		t.Error("DeriveUUIDv4 与 DeriveUUID 产出相同，格式位没生效")
	}
}

// TestDeriveUUID_GoldenValueUnchanged 用 golden 值固化旧函数的**实际产出**。
//
// 这条不是在夸它——它产出的串格式位并不合法（见 DeriveUUIDv4 注释）——而是防止
// 有人「顺手修好」：cc-haiku-arith-20260506 的 {{USER_ACCOUNT_UUID}} 已在生产
// 使用，改算法会改变那个模板实际发出的值。
//
// ⚠️ 必须是写死的 golden。此处原本写成 `got := DeriveUUID(...)` 再 `want :=
// DeriveUUID(...)` 自比，对一个确定性函数那是恒真断言，算法整个换掉也照样绿。
func TestDeriveUUID_GoldenValueUnchanged(t *testing.T) {
	const golden = "bd931e3d-19fc-1d62-c148-880da180e959"
	got := DeriveUUID(sha256Hex("provider/cc/channel"), "account_uuid")
	if got != golden {
		t.Errorf("旧函数产出变了：got %q，golden %q。\n"+
			"若这是有意改动，先确认 cc-haiku-arith-20260506 在生产发出的 "+
			"{{USER_ACCOUNT_UUID}} 变化是可接受的，再更新本 golden。", got, golden)
	}
	// 顺带固化「格式位不合法」这个现状，免得有人只改位、不改 golden 就以为没事
	if got[14] == '4' && (got[19] == '8' || got[19] == '9' || got[19] == 'a' || got[19] == 'b') {
		t.Error("旧函数突然产出合法 v4 了——那说明算法被动过，见上面的 golden 说明")
	}
}
