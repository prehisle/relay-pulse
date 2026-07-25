package config

import (
	"fmt"
	"net"
	"strings"
)

// defaultTrustedProxies 是未显式配置 server.trusted_proxies 时的兜底：只信本机回环。
//
// 取这个默认值而不是 gin 的"信任所有代理"，是因为后者会让任何客户端自带
// X-Forwarded-For 就顶替真实来源 IP，从而同时架空探测限流、每日提交配额与
// submitter_ip_hash 审计。典型部署（cloudflared / nginx 与应用同机）恰好落在回环内。
var defaultTrustedProxies = []string{"127.0.0.1", "::1"}

// ValidateTrustedProxies 校验代理条目是否为合法 IP 或 CIDR。
//
// 与 gin 的 SetTrustedProxies 采用同一判据，供配置加载期 fail-closed 使用——
// 把"信任边界写错"暴露在启动/热更新校验阶段，而不是等到运行时静默退化成错误的信任面。
func ValidateTrustedProxies(proxies []string) error {
	for _, raw := range proxies {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return fmt.Errorf("server.trusted_proxies 含空条目")
		}
		if strings.Contains(entry, "/") {
			if _, _, err := net.ParseCIDR(entry); err != nil {
				return fmt.Errorf("server.trusted_proxies 条目 %q 不是合法 CIDR: %w", entry, err)
			}
			continue
		}
		if net.ParseIP(entry) == nil {
			return fmt.Errorf("server.trusted_proxies 条目 %q 不是合法 IP 或 CIDR", entry)
		}
	}
	return nil
}
