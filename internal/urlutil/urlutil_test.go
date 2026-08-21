package urlutil

import (
	"net/url"
	"testing"
)

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}

func TestSameHostPort(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"同 host 同缺省端口（base_url 前缀 vs 完整 endpoint）", "https://api.example.com", "https://api.example.com/v1/messages", true},
		{"同 host 缺省 vs 显式 443 归一化相等", "https://api.example.com", "https://api.example.com:443", true},
		{"同 host 不同端口须判不等（bug② 核心）", "https://api.example.com:443", "https://api.example.com:9999", false},
		{"不同 host", "https://api.example.com", "https://api.evil.com", false},
		{"host 大小写不敏感", "https://API.Example.COM", "https://api.example.com", true},
		{"http 缺省 80 vs 显式 80 归一化相等", "http://api.example.com", "http://api.example.com:80", true},
		{"同 host 但 http(80) vs https(443) 有效端口不同", "http://api.example.com", "https://api.example.com", false},
		{"IPv6 同 host 同端口", "https://[2001:db8::1]:8443", "https://[2001:db8::1]:8443/x", true},
		{"IPv6 同 host 不同端口", "https://[2001:db8::1]:8443", "https://[2001:db8::1]:9443", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameHostPort(mustParse(t, tc.a), mustParse(t, tc.b)); got != tc.want {
				t.Errorf("SameHostPort(%q,%q)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestSameEndpoint 覆盖「同一接入点」的四个维度：scheme、host/port、路径、查询串。
//
// 路径这一维是本函数存在的理由：SameHostPort 放行 https://edge/good ↔ https://edge/other，
// 而中转商的多条线路常按路径区分，那等于绕开「先证明这条线路可用」。
func TestSameEndpoint(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"完全相同", "https://api.example.com/v1", "https://api.example.com/v1", true},
		{"仅尾部斜杠之差", "https://api.example.com/v1/", "https://api.example.com/v1", true},
		{"根路径与空路径", "https://api.example.com/", "https://api.example.com", true},
		{"默认端口与显式 443", "https://api.example.com/v1", "https://api.example.com:443/v1", true},
		{"路径不同", "https://api.example.com/good", "https://api.example.com/other", false},
		{"一方无路径", "https://api.example.com", "https://api.example.com/other", false},
		{"路径大小写不同", "https://api.example.com/API", "https://api.example.com/api", false},
		{"host 不同", "https://a.example.com/v1", "https://b.example.com/v1", false},
		{"端口不同", "https://api.example.com:8443/v1", "https://api.example.com/v1", false},
		{"scheme 不同但归一化端口相同", "http://api.example.com:443/v1", "https://api.example.com/v1", false},
		{"查询串不同", "https://api.example.com/v1?a=1", "https://api.example.com/v1?a=2", false},
		// 内嵌凭据是端点身份的一部分：同一台机器、不同账户
		{"内嵌用户不同", "https://alice@api.example.com/v1", "https://bob@api.example.com/v1", false},
		{"一方带内嵌用户", "https://alice@api.example.com/v1", "https://api.example.com/v1", false},
		{"内嵌用户相同", "https://alice@api.example.com/v1", "https://alice@api.example.com/v1", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := url.Parse(tc.a)
			if err != nil {
				t.Fatalf("解析 %q: %v", tc.a, err)
			}
			b, err := url.Parse(tc.b)
			if err != nil {
				t.Fatalf("解析 %q: %v", tc.b, err)
			}
			if got := SameEndpoint(a, b); got != tc.want {
				t.Errorf("SameEndpoint(%q, %q) = %v，期望 %v", tc.a, tc.b, got, tc.want)
			}
		})
	}

	if SameEndpoint(nil, nil) {
		t.Error("nil 输入不应判为同一接入点")
	}
}
