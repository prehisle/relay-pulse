package config

import "testing"

// TestNormalizeGlobalDefaults_TrustedProxiesFallsBackToLoopback 锁定安全默认：
// 未配置时只信本机回环，绝不回落到 gin 的"信任所有代理"。
func TestNormalizeGlobalDefaults_TrustedProxiesFallsBackToLoopback(t *testing.T) {
	cfg := &AppConfig{}
	if err := cfg.normalizeGlobalDefaults(); err != nil {
		t.Fatalf("normalizeGlobalDefaults: %v", err)
	}

	got := cfg.Server.TrustedProxies
	if len(got) != 2 || got[0] != "127.0.0.1" || got[1] != "::1" {
		t.Fatalf("TrustedProxies=%v，期望 [127.0.0.1 ::1]", got)
	}
	if cfg.Server.TrustedPlatform != "" {
		t.Fatalf("TrustedPlatform=%q，默认必须为空（不预设厂商 Header）", cfg.Server.TrustedPlatform)
	}
}

// TestNormalizeGlobalDefaults_TrustedProxiesRespectsExplicitValue 显式配置不被默认值覆盖。
func TestNormalizeGlobalDefaults_TrustedProxiesRespectsExplicitValue(t *testing.T) {
	cfg := &AppConfig{Server: ServerConfig{TrustedProxies: []string{"10.0.0.0/8"}}}
	if err := cfg.normalizeGlobalDefaults(); err != nil {
		t.Fatalf("normalizeGlobalDefaults: %v", err)
	}
	if len(cfg.Server.TrustedProxies) != 1 || cfg.Server.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("显式配置被覆盖: %v", cfg.Server.TrustedProxies)
	}
}

// TestNormalizeGlobalDefaults_TrustedProxiesAreTrimmed 校验与实际生效的必须是同一字符串：
// 否则带空白的条目会过了加载期校验、却在运行时解析失败而触发收紧兜底。
func TestNormalizeGlobalDefaults_TrustedProxiesAreTrimmed(t *testing.T) {
	cfg := &AppConfig{Server: ServerConfig{TrustedProxies: []string{"  127.0.0.1 ", "\t::1"}}}
	if err := cfg.normalizeGlobalDefaults(); err != nil {
		t.Fatalf("normalizeGlobalDefaults: %v", err)
	}
	if cfg.Server.TrustedProxies[0] != "127.0.0.1" || cfg.Server.TrustedProxies[1] != "::1" {
		t.Fatalf("TrustedProxies 未被 trim: %q", cfg.Server.TrustedProxies)
	}
}

func TestValidateTrustedProxies(t *testing.T) {
	tests := []struct {
		name    string
		proxies []string
		wantErr bool
	}{
		{"IPv4", []string{"127.0.0.1"}, false},
		{"IPv6", []string{"::1"}, false},
		{"CIDR", []string{"10.0.0.0/8", "fd00::/8"}, false},
		{"空列表", nil, false},
		{"空条目", []string{""}, true},
		{"非法 IP", []string{"not-an-ip"}, true},
		{"非法 CIDR", []string{"10.0.0.0/99"}, true},
		{"主机名不接受", []string{"proxy.internal"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTrustedProxies(tt.proxies)
			if tt.wantErr && err == nil {
				t.Fatalf("proxies=%v 期望报错，实际 nil", tt.proxies)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("proxies=%v 期望通过，实际: %v", tt.proxies, err)
			}
		})
	}
}

// TestClone_DeepCopiesTrustedProxies 热更新期间新旧配置并存，切片必须深拷贝。
func TestClone_DeepCopiesTrustedProxies(t *testing.T) {
	cfg := &AppConfig{Server: ServerConfig{
		TrustedPlatform: "CF-Connecting-IP",
		TrustedProxies:  []string{"127.0.0.1", "::1"},
	}}
	clone := cfg.clone()

	cfg.Server.TrustedPlatform = "X-Spoofed"
	cfg.Server.TrustedProxies[0] = "0.0.0.0/0"

	if clone.Server.TrustedPlatform != "CF-Connecting-IP" {
		t.Fatalf("clone TrustedPlatform=%q，不应随原配置改变", clone.Server.TrustedPlatform)
	}
	if clone.Server.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("clone TrustedProxies 与原配置共享底层数组: %v", clone.Server.TrustedProxies)
	}
}
