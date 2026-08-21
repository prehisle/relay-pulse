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

// SameEndpoint 在 SameHostPort 之上再比 scheme、路径与查询串，即「两个 URL 指向同一个接入点」。
//
// 只比 host/port 会漏掉**路径替换**：拿 https://edge/good 测出 proof，提交
// base_url=https://edge/other，两者 host/port 相同、却是完全不同的上游——而中转商的多线路
// 恰恰常按路径区分。收录与变更的提交校验都用这一条。
//
// 路径按「剥尾部斜杠」归一（https://h 与 https://h/ 视为同一个），其余部分逐字比较：
// 大小写、编码形式的差异一律视为不同，因为它们对上游而言本就可能是不同资源。
// scheme 也必须相同：SameHostPort 只把端口按 scheme 归一化，故 http://h:443 与 https://h
// 在它眼里是同一个，而实际一个明文一个加密。
func SameEndpoint(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		SameHostPort(a, b) &&
		normalizedPath(a) == normalizedPath(b) &&
		a.RawQuery == b.RawQuery
}

// normalizedPath 返回剥掉尾部斜杠的路径（根路径与空路径同归一为空串）。
func normalizedPath(u *url.URL) string {
	return strings.TrimSuffix(u.EscapedPath(), "/")
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
