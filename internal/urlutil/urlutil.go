// Package urlutil 提供 URL 比较的小工具，供 onboarding 与 change 两条自助流程共享，作为
// 「base_url ↔ test_api_url host/port 一致性」校验的单一真相源——避免该校验在两处各自实现而漂移
// （历史上正因两份各自只比 host、漏比端口，可在一个端口测出 proof 却把 base_url 应用成同 host 另一端口）。
package urlutil

import (
	"net/url"
	"strings"
)

// SameHostPort 判断两个 URL 是否指向同一 host+端口（端口按各自 scheme 默认归一化：
// https→443、http→80、显式端口优先）。用于把 test_api_url 与将要落地的 base_url 绑定到同一来源，
// 比只比 hostname 更严：拦下「同 host 换端口」的绕过。
func SameHostPort(a, b *url.URL) bool {
	return strings.EqualFold(a.Hostname(), b.Hostname()) && normalizedPort(a) == normalizedPort(b)
}

// normalizedPort 返回 URL 的有效端口：显式端口优先，缺省时按 scheme 默认（https→443 / http→80）。
func normalizedPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
