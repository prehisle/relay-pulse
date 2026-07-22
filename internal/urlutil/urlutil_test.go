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
