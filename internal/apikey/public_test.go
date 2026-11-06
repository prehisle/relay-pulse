package apikey

import "testing"

func TestValidatePublicKey(t *testing.T) {
	for _, ok := range []string{"sk-abcdefgh", "钥匙钥匙钥匙钥匙钥匙"} {
		if err := ValidatePublicKey(ok); err != nil {
			t.Errorf("%q 应通过，实际 %v", ok, err)
		}
	}
	// 「短串 + 空白凑长度」也要拒：首尾空白规则先于长度规则。
	for _, bad := range []string{"", "short", "abcdefghijk ", " abcdefghijk", "abcdefghijk\n", "abc       ", "钥匙钥匙", "abcdefghijk\u00a0", "\u3000abcdefghijk"} {
		if err := ValidatePublicKey(bad); err == nil {
			t.Errorf("%q 应被拒", bad)
		}
	}
}
