package config

import (
	"strings"
	"testing"
	"time"
)

// validateAll 执行 validate + validateResolvedModelConstraints（模拟完整加载链路）
// 测试不走模板解析，因此直接在 validate() 后补调后置校验
func validateAll(cfg *AppConfig) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	return cfg.validateResolvedModelConstraints()
}

func TestValidateMonitorsUniqueByQuadruple(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "gpt-4o",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "gpt-4o",
				BaseURL:    "https://example.com/2",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
		},
	}

	err := validateAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "重复的监测项") {
		t.Fatalf("期望重复的监测项错误, got=%v", err)
	}
}

func TestValidateParentRequiresModel(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	err := validateAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "有 parent 但缺少 model") {
		t.Fatalf("期望子通道缺少 model 报错, got=%v", err)
	}
}

func TestValidateReferencedParentRequiresModel(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	err := validateAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "被引用为父") {
		t.Fatalf("期望父通道缺少 model 报错, got=%v", err)
	}
}

func TestValidateParentFormatError(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "bad-format",
				Category: "public",
			},
		},
	}

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "parent 格式错误") {
		t.Fatalf("期望 parent 格式错误, got=%v", err)
	}
}

func TestValidateParentNotFound(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	err := validateAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "找不到父通道") {
		t.Fatalf("期望找不到父通道错误, got=%v", err)
	}
}

func TestValidateParentMultipleDefinitions(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base-a",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base-b",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	err := validateAll(cfg)
	if err == nil || !strings.Contains(err.Error(), "存在多个定义") {
		t.Fatalf("期望父通道多定义报错, got=%v", err)
	}
}

func TestNormalizeAppliesParentInheritanceAndTemplates(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:        "demo",
				Service:         "cc",
				Channel:         "vip",
				Model:           "base",
				BaseURL:         "https://example.com",
				URLPattern:      "{{BASE_URL}}",
				Method:          "POST",
				Category:        "public",
				APIKey:          "k",
				Headers:         map[string]string{"Authorization": "Bearer {{API_KEY}}", "X-Model": "{{MODEL}}"},
				Body:            `{"model":"{{MODEL}}","api_key":"{{API_KEY}}"}`,
				SuccessContains: "ok",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	if child.APIKey != "k" {
		t.Fatalf("child.APIKey = %q, want %q", child.APIKey, "k")
	}
	if child.BaseURL != "https://example.com" {
		t.Fatalf("child.BaseURL = %q, want %q", child.BaseURL, "https://example.com")
	}
	if child.Method != "POST" {
		t.Fatalf("child.Method = %q, want %q", child.Method, "POST")
	}
	if child.SuccessContains != "ok" {
		t.Fatalf("child.SuccessContains = %q, want %q", child.SuccessContains, "ok")
	}
	if child.Body != `{"model":"{{MODEL}}","api_key":"{{API_KEY}}"}` {
		t.Fatalf("child.Body = %q, want inherited body", child.Body)
	}
	if child.Headers == nil || child.Headers["Authorization"] != "Bearer {{API_KEY}}" {
		t.Fatalf("child.Headers not inherited as expected, got=%v", child.Headers)
	}

	// 确保 headers 深拷贝（修改子不影响父）
	child.Headers["X-Test"] = "x"
	if _, exists := cfg.Monitors[0].Headers["X-Test"]; exists {
		t.Fatalf("期望 child.Headers 为深拷贝，但父 headers 被污染")
	}
}

// TestChildInheritsProviderServiceChannel 验证子项可以省略 provider/service/channel，从 parent 路径自动继承
func TestChildInheritsProviderServiceChannel(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				// 子项只需 parent + model，无需指定 provider/service/channel
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	// 验证 provider/service/channel 从 parent 继承
	if child.Provider != "demo" {
		t.Fatalf("child.Provider = %q, want %q", child.Provider, "demo")
	}
	if child.Service != "cc" {
		t.Fatalf("child.Service = %q, want %q", child.Service, "cc")
	}
	if child.Channel != "vip" {
		t.Fatalf("child.Channel = %q, want %q", child.Channel, "vip")
	}
}

// TestChildCannotOverrideProviderServiceChannel 验证子项不能覆盖 provider/service/channel 为不同值
func TestChildCannotOverrideProviderServiceChannel(t *testing.T) {
	tests := []struct {
		name    string
		child   ServiceConfig
		wantErr string
	}{
		{
			name: "覆盖 provider",
			child: ServiceConfig{
				Provider: "other",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
			wantErr: "provider 'other' 与 parent 'demo' 不一致",
		},
		{
			name: "覆盖 service",
			child: ServiceConfig{
				Service:  "other",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
			wantErr: "service 'other' 与 parent 'cc' 不一致",
		},
		{
			name: "覆盖 channel",
			child: ServiceConfig{
				Channel:  "other",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
			wantErr: "channel 'other' 与 parent 'vip' 不一致",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AppConfig{
				Monitors: []ServiceConfig{
					{
						Provider:   "demo",
						Service:    "cc",
						Channel:    "vip",
						Model:      "base",
						BaseURL:    "https://example.com",
						URLPattern: "{{BASE_URL}}",
						Method:     "POST",
						Category:   "public",
					},
					tt.child,
				},
			}

			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("期望错误包含 %q, got=%v", tt.wantErr, err)
			}
		})
	}
}

// TestChildInheritsCategoryFromParent 验证子项可以省略 category，在 Normalize 时从父通道继承
func TestChildInheritsCategoryFromParent(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "commercial",
			},
			{
				// 子项只需 parent + model，无需 category
				Model:  "child",
				Parent: "demo/cc/vip",
				// Category 留空，将从父继承
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	if child.Category != "commercial" {
		t.Fatalf("child.Category = %q, want %q (inherited from parent)", child.Category, "commercial")
	}
}

// TestChildInheritsDurationFieldsFromParent 验证子项继承 interval/slow_latency/timeout 后，Duration 字段也正确计算
func TestChildInheritsDurationFieldsFromParent(t *testing.T) {
	cfg := &AppConfig{
		Interval:    "5m",  // 全局默认
		SlowLatency: "3s",  // 全局默认
		Timeout:     "10s", // 全局默认
		Monitors: []ServiceConfig{
			{
				Provider:    "demo",
				Service:     "cc",
				Channel:     "vip",
				Model:       "base",
				BaseURL:     "https://example.com",
				URLPattern:  "{{BASE_URL}}",
				Method:      "POST",
				Category:    "public",
				Interval:    "1m",  // 父通道自定义 interval
				SlowLatency: "5s",  // 父通道自定义 slow_latency
				Timeout:     "15s", // 父通道自定义 timeout
			},
			{
				// 子项只需 parent + model，所有 Duration 相关字段从父继承
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	parent := &cfg.Monitors[0]
	child := &cfg.Monitors[1]

	// 验证字符串字段继承
	if child.Interval != "1m" {
		t.Errorf("child.Interval = %q, want %q (inherited from parent)", child.Interval, "1m")
	}
	if child.SlowLatency != "5s" {
		t.Errorf("child.SlowLatency = %q, want %q (inherited from parent)", child.SlowLatency, "5s")
	}
	if child.Timeout != "15s" {
		t.Errorf("child.Timeout = %q, want %q (inherited from parent)", child.Timeout, "15s")
	}

	// 验证 Duration 字段正确计算（核心测试点）
	if child.IntervalDuration != parent.IntervalDuration {
		t.Errorf("child.IntervalDuration = %v, want %v (same as parent)", child.IntervalDuration, parent.IntervalDuration)
	}
	if child.SlowLatencyDuration != parent.SlowLatencyDuration {
		t.Errorf("child.SlowLatencyDuration = %v, want %v (same as parent)", child.SlowLatencyDuration, parent.SlowLatencyDuration)
	}
	if child.TimeoutDuration != parent.TimeoutDuration {
		t.Errorf("child.TimeoutDuration = %v, want %v (same as parent)", child.TimeoutDuration, parent.TimeoutDuration)
	}

	// 验证继承的值是父通道的值，而不是全局默认值
	if child.IntervalDuration != 1*time.Minute {
		t.Errorf("child.IntervalDuration = %v, want 1m (from parent, not global 5m)", child.IntervalDuration)
	}
	if child.SlowLatencyDuration != 5*time.Second {
		t.Errorf("child.SlowLatencyDuration = %v, want 5s (from parent, not global 3s)", child.SlowLatencyDuration)
	}
	if child.TimeoutDuration != 15*time.Second {
		t.Errorf("child.TimeoutDuration = %v, want 15s (from parent, not global 10s)", child.TimeoutDuration)
	}
}

// TestChildOwnDurationFieldsNotOverwritten 验证子项自己配置的 Duration 字段不会被父通道覆盖
func TestChildOwnDurationFieldsNotOverwritten(t *testing.T) {
	cfg := &AppConfig{
		Interval:    "5m",
		SlowLatency: "3s",
		Timeout:     "10s",
		Monitors: []ServiceConfig{
			{
				Provider:    "demo",
				Service:     "cc",
				Channel:     "vip",
				Model:       "base",
				BaseURL:     "https://example.com",
				URLPattern:  "{{BASE_URL}}",
				Method:      "POST",
				Category:    "public",
				Interval:    "1m",
				SlowLatency: "5s",
				Timeout:     "15s",
			},
			{
				Model:       "child",
				Parent:      "demo/cc/vip",
				Category:    "public",
				Interval:    "30s", // 子通道自己配置，不应被父覆盖
				SlowLatency: "8s",  // 子通道自己配置
				Timeout:     "20s", // 子通道自己配置
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// 验证子项自己的配置保持不变
	if child.IntervalDuration != 30*time.Second {
		t.Errorf("child.IntervalDuration = %v, want 30s (child's own config, not parent's 1m)", child.IntervalDuration)
	}
	if child.SlowLatencyDuration != 8*time.Second {
		t.Errorf("child.SlowLatencyDuration = %v, want 8s (child's own config, not parent's 5s)", child.SlowLatencyDuration)
	}
	if child.TimeoutDuration != 20*time.Second {
		t.Errorf("child.TimeoutDuration = %v, want 20s (child's own config, not parent's 15s)", child.TimeoutDuration)
	}
}

// TestChildInheritsBoardFromParent 验证子项继承父通道的 board 配置
// board 和 cold_reason 都可以被继承
func TestChildInheritsBoardFromParent(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
				Board:      "cold",
				ColdReason: "服务不稳定",
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// Board 和 ColdReason 留空
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// board 从父通道继承
	if child.Board != "cold" {
		t.Errorf("child.Board = %q, want %q (inherited from parent)", child.Board, "cold")
	}

	// ColdReason 从父通道继承
	if child.ColdReason != "服务不稳定" {
		t.Errorf("child.ColdReason = %q, want %q (inherited from parent)", child.ColdReason, "服务不稳定")
	}
}

// TestChildExplicitBoardNotOverwritten 验证子项显式配置的 board 不会被父通道覆盖
// 子项显式配置 board=hot，不会继承父的 cold，cold_reason 也会在 post-inheritance 清理
func TestChildExplicitBoardNotOverwritten(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
				Board:      "cold",
				ColdReason: "父通道冷板原因",
			},
			{
				Model:      "child",
				Parent:     "demo/cc/vip",
				Category:   "public",
				Board:      "hot", // 子项显式配置为 hot
				ColdReason: "",    // 子项不配置冷板原因
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// 子项显式配置的 board 应保持不变
	if child.Board != "hot" {
		t.Errorf("child.Board = %q, want %q (child's own config)", child.Board, "hot")
	}

	// ColdReason 虽然会在继承阶段被复制，但 post-inheritance 会清理 board != cold 的 cold_reason
	if child.ColdReason != "" {
		t.Errorf("child.ColdReason = %q, want %q (cleaned up in post-inheritance because board=hot)",
			child.ColdReason, "")
	}
}

// TestChildInheritsProviderSlugFromParent 验证子项继承父通道的 provider_slug
// provider_slug 可以被继承，子项未配置时使用父通道的值
func TestChildInheritsProviderSlugFromParent(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:     "Demo-Provider",
				Service:      "cc",
				Channel:      "vip",
				Model:        "base",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				ProviderSlug: "custom-slug", // 父通道自定义 slug
			},
			{
				Model:    "child",
				Parent:   "Demo-Provider/cc/vip",
				Category: "public",
				// ProviderSlug 留空
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// provider_slug 从父通道继承
	if child.ProviderSlug != "custom-slug" {
		t.Errorf("child.ProviderSlug = %q, want %q (inherited from parent)",
			child.ProviderSlug, "custom-slug")
	}
}

// TestChildExplicitProviderSlugNotOverwritten 验证子项显式配置的 provider_slug 不会被父通道覆盖
func TestChildExplicitProviderSlugNotOverwritten(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip",
				Model:        "base",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				ProviderSlug: "parent-slug",
			},
			{
				Model:        "child",
				Parent:       "demo/cc/vip",
				Category:     "public",
				ProviderSlug: "child-slug", // 子项显式配置
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// 子项显式配置的 slug 应保持不变
	if child.ProviderSlug != "child-slug" {
		t.Errorf("child.ProviderSlug = %q, want %q (child's own config)", child.ProviderSlug, "child-slug")
	}
}

// TestChildInheritsProviderNameFromParent 验证子项继承父通道的 provider_name 等显示名称
func TestChildInheritsProviderNameFromParent(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip",
				Model:        "base",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				ProviderName: "演示服务商",
				ServiceName:  "Claude Code",
				ChannelName:  "VIP通道",
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// 所有显示名称留空
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]

	// 验证显示名称从父通道继承
	if child.ProviderName != "演示服务商" {
		t.Errorf("child.ProviderName = %q, want %q (inherited from parent)", child.ProviderName, "演示服务商")
	}
	if child.ServiceName != "Claude Code" {
		t.Errorf("child.ServiceName = %q, want %q (inherited from parent)", child.ServiceName, "Claude Code")
	}
	if child.ChannelName != "VIP通道" {
		t.Errorf("child.ChannelName = %q, want %q (inherited from parent)", child.ChannelName, "VIP通道")
	}
}

// TestAnnotationRulesApplyToParentAndChild 验证 annotation_rules 对父子通道独立生效
func TestAnnotationRulesApplyToParentAndChild(t *testing.T) {
	cfg := &AppConfig{
		EnableAnnotations: true,
		AnnotationRules: []AnnotationRule{
			{
				Match: AnnotationMatch{Provider: "demo"},
				Add: []Annotation{
					{ID: "official_api", Family: AnnotationFamilyPositive, Label: "官方 API", Priority: 100},
				},
			},
		},
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	// 父子都应独立匹配 annotation_rules
	parent := &cfg.Monitors[0]
	child := &cfg.Monitors[1]

	// 两者都应有 public_service（系统派生）+ official_api（规则匹配）
	findAnnotation := func(annotations []Annotation, id string) *Annotation {
		for i := range annotations {
			if annotations[i].ID == id {
				return &annotations[i]
			}
		}
		return nil
	}

	if a := findAnnotation(parent.Annotations, "official_api"); a == nil {
		t.Error("parent should have official_api annotation from rule match")
	}
	if a := findAnnotation(child.Annotations, "official_api"); a == nil {
		t.Error("child should have official_api annotation from rule match")
	}
	if a := findAnnotation(parent.Annotations, "public_service"); a == nil {
		t.Error("parent should have public_service annotation (category=public)")
	}
	if a := findAnnotation(child.Annotations, "public_service"); a == nil {
		t.Error("child should have public_service annotation (category=public)")
	}
}

// TestChildInheritsTemplateFromParent 验证子项继承 body 时同步继承 Template
func TestChildInheritsTemplateFromParent(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				Category:   "public",
				Body:       `{"foo":"bar"}`,
				Template:   "cc_base.json",
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// Body 留空：应继承父通道
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	if child.Body != `{"foo":"bar"}` {
		t.Fatalf("child.Body = %q, want inherited body", child.Body)
	}
	if child.Template != "cc_base.json" {
		t.Fatalf("child.Template = %q, want %q", child.Template, "cc_base.json")
	}
}

// TestChildOwnBodyDoesNotInheritParentSuccessContains 复现并锁定线上 bug：
// 子项有自己的 Body（当年的实例是 cc-opus-ping 模板，body 发 "ping"）且 SuccessContains
// 为空时，不应继承父项（cc-opus-arith）的算术校验串 {{EXPECTED_ANSWER}}。
// 否则会出现「探针发 ping、却用父项算术答案校验」的永久 content_mismatch。
//
// 注：那个具体实例 2026-09-10 起已不复存在（cc-opus-ping 换成带 "Only reply pong." 指令的
// 抓包形态后声明了 success_contains=pong），但不变量本身照旧——任何自带 body 的子项都适用。
func TestChildOwnBodyDoesNotInheritParentSuccessContains(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:        "demo",
				Service:         "cc",
				Channel:         "vip",
				Model:           "Opus",
				BaseURL:         "https://example.com",
				URLPattern:      "{{BASE_URL}}",
				Method:          "POST",
				Category:        "public",
				APIKey:          "k",
				Body:            `{"messages":[{"role":"user","content":"Calculate: {{PROMPT}}"}]}`,
				SuccessContains: "{{EXPECTED_ANSWER}}",
			},
			{
				// 子项有自己的 body（ping）+ 空 success_contains（= 仅判 HTTP 活性）
				Provider:        "demo",
				Service:         "cc",
				Channel:         "vip",
				Model:           "Opus-Ping",
				Parent:          "demo/cc/vip",
				Category:        "public",
				Body:            `{"messages":[{"role":"user","content":"ping"}]}`,
				SuccessContains: "",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	if child.Body != `{"messages":[{"role":"user","content":"ping"}]}` {
		t.Fatalf("child.Body 被改写 = %q，应保持自己的 ping body", child.Body)
	}
	if child.SuccessContains != "" {
		t.Fatalf("child.SuccessContains = %q，应保持空（不继承父项校验串）", child.SuccessContains)
	}
}

// TestChildExplicitSuccessContainsNotOverwritten 验证子项继承 body 但显式设置了
// 自己的 success_contains 时，不被父项覆盖。
func TestChildExplicitSuccessContainsNotOverwritten(t *testing.T) {
	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:        "demo",
				Service:         "cc",
				Channel:         "vip",
				Model:           "base",
				BaseURL:         "https://example.com",
				URLPattern:      "{{BASE_URL}}",
				Method:          "POST",
				Category:        "public",
				APIKey:          "k",
				Body:            `{"model":"{{MODEL}}"}`,
				SuccessContains: "parent-keyword",
			},
			{
				Provider: "demo",
				Service:  "cc",
				Channel:  "vip",
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// Body 留空：继承父 body
				SuccessContains: "child-keyword",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := &cfg.Monitors[1]
	if child.Body != `{"model":"{{MODEL}}"}` {
		t.Fatalf("child.Body = %q, want inherited body", child.Body)
	}
	if child.SuccessContains != "child-keyword" {
		t.Fatalf("child.SuccessContains = %q, want %q（显式值不应被覆盖）", child.SuccessContains, "child-keyword")
	}
}

// TestChildInheritsParentEffectiveIntervalDuration 验证 Part B 回归保护：
// 父通道为付费（beacon），无显式 interval，通过层级逻辑得到全局 1m；
// 子通道 SponsorLevel 空（不继承），无显式 interval，不应因此降到 5m——
// 应继承父通道已解析出的有效 IntervalDuration（1m）。
func TestChildInheritsParentEffectiveIntervalDuration(t *testing.T) {
	cfg := &AppConfig{
		// 全局 interval 1m
		Interval: "1m",
		Monitors: []ServiceConfig{
			{
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip",
				Model:        "base",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				SponsorLevel: SponsorLevelBeacon, // 付费通道，无显式 interval → 层级回退 1m
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// SponsorLevel 空（不继承）；Interval 也空——没有显式 interval 字符串
				// 但应继承父通道已解析的 IntervalDuration，而非自行降速到 5m
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() err = %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() err = %v", err)
	}

	parent := &cfg.Monitors[0]
	child := &cfg.Monitors[1]

	if parent.IntervalDuration != time.Minute {
		t.Fatalf("parent.IntervalDuration = %v, want 1m (beacon paid tier)", parent.IntervalDuration)
	}
	// 关键断言：子通道不应因 SponsorLevel 空降到 5m
	if child.IntervalDuration != time.Minute {
		t.Errorf("child.IntervalDuration = %v, want 1m (inherited from paid parent)", child.IntervalDuration)
	}
	if child.IntervalDuration != parent.IntervalDuration {
		t.Errorf("child.IntervalDuration = %v, want same as parent %v", child.IntervalDuration, parent.IntervalDuration)
	}
}

// TestChildInheritsParentEffectiveIntervalDuration_FreeMirror 验证对称情形：
// 父通道 FREE（pulse），无显式 interval，层级回退 5m；
// 子通道无显式 interval → 也应继承父的 5m（而非重新计算）。
func TestChildInheritsParentEffectiveIntervalDuration_FreeMirror(t *testing.T) {
	cfg := &AppConfig{
		Interval: "1m",
		Monitors: []ServiceConfig{
			{
				Provider:     "demo",
				Service:      "cc",
				Channel:      "vip",
				Model:        "base",
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
				Category:     "public",
				SponsorLevel: SponsorLevelPulse, // FREE，无显式 interval → 5m
			},
			{
				Model:    "child",
				Parent:   "demo/cc/vip",
				Category: "public",
				// 无显式 interval，无 SponsorLevel → 同样会算出 5m，但应由父继承
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() err = %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize() err = %v", err)
	}

	parent := &cfg.Monitors[0]
	child := &cfg.Monitors[1]

	if parent.IntervalDuration != 5*time.Minute {
		t.Fatalf("parent.IntervalDuration = %v, want 5m (pulse free tier)", parent.IntervalDuration)
	}
	if child.IntervalDuration != 5*time.Minute {
		t.Errorf("child.IntervalDuration = %v, want 5m (inherited from free parent)", child.IntervalDuration)
	}
	if child.IntervalDuration != parent.IntervalDuration {
		t.Errorf("child.IntervalDuration = %v, want same as parent %v", child.IntervalDuration, parent.IntervalDuration)
	}
}
