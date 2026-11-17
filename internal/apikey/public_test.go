package apikey

import "testing"

func TestValidatePublicKey(t *testing.T) {
	for _, ok := range []string{"sk-abcdefgh", "key-AbC_dE.f~g!@#$%^&*()+=/"} {
		if err := ValidatePublicKey(ok); err != nil {
			t.Errorf("%q 应通过，实际 %v", ok, err)
		}
	}
	// 「短串 + 空白凑长度」也要拒：首尾空白规则先于长度规则。
	for _, bad := range []string{"", "short", "abcdefghijk ", " abcdefghijk", "abcdefghijk\n", "abc       ", "钥匙钥匙", "abcdefghijk\u00a0", "\u3000abcdefghijk",
		// 非 ASCII：回显服务会把它改写成别的字节，回显检测因此失效（2026-11 实际绕过手法）
		"钥匙钥匙钥匙钥匙钥匙", "sk-abcé12345678", "sk-abc\x7f12345678", "sk-abc\tdefghijk"} {
		if err := ValidatePublicKey(bad); err == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
}
