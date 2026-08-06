package config

import (
	"reflect"
	"strings"
	"testing"
)

// findAnnotationByID 在注解列表中查找指定 ID 的注解
func findAnnotationByID(annotations []Annotation, id string) *Annotation {
	for i := range annotations {
		if annotations[i].ID == id {
			return &annotations[i]
		}
	}
	return nil
}

// Test consecutive hyphens in slug
func TestConsecutiveHyphensSlug(t *testing.T) {
	tests := []struct {
		name      string
		slug      string
		shouldErr bool
	}{
		{"单连字符", "easy-chat", false},
		{"连续两个连字符", "easy--chat", true},
		{"连续三个连字符", "easy---chat", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProviderSlug(tt.slug)
			if tt.shouldErr && err == nil {
				t.Errorf("ValidateProviderSlug(%q) should return error", tt.slug)
			}
			if !tt.shouldErr && err != nil {
				t.Errorf("ValidateProviderSlug(%q) should not return error, got: %v", tt.slug, err)
			}
		})
	}
}

// Test baseURL normalization
func TestBaseURLNormalization(t *testing.T) {
	tests := []struct {
		name        string
		inputURL    string
		expectedURL string
		shouldErr   bool
	}{
		{"正常 HTTPS URL", "https://relaypulse.top", "https://relaypulse.top", false},
		{"带尾随斜杠", "https://relaypulse.top/", "https://relaypulse.top", false},
		{"多个尾随斜杠", "https://relaypulse.top///", "https://relaypulse.top", false},
		{"HTTP URL（警告）", "http://example.com", "http://example.com", false},
		{"无效协议", "ftp://example.com", "", true},
		{"缺少协议", "example.com", "", true},
		{"缺少主机", "https://", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &AppConfig{PublicBaseURL: tt.inputURL}
			err := cfg.normalize()

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Normalize() should return error for %q", tt.inputURL)
				}
			} else {
				if err != nil {
					t.Errorf("Normalize() should not return error for %q, got: %v", tt.inputURL, err)
				}
				if cfg.PublicBaseURL != tt.expectedURL {
					t.Errorf("Normalize() URL = %q, want %q", cfg.PublicBaseURL, tt.expectedURL)
				}
			}
		})
	}
}

// TestAnnotationFamilyValidation tests AnnotationFamily isValid method
func TestAnnotationFamilyValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		family  AnnotationFamily
		isValid bool
	}{
		{AnnotationFamilyPositive, true},
		{AnnotationFamilyNeutral, true},
		{AnnotationFamilyNegative, true},
		{AnnotationFamily("invalid"), false},
		{AnnotationFamily(""), false},
	}

	for _, tt := range tests {
		t.Run("Family_"+string(tt.family), func(t *testing.T) {
			if got := tt.family.isValid(); got != tt.isValid {
				t.Errorf("AnnotationFamily(%q).isValid() = %v, want %v", tt.family, got, tt.isValid)
			}
		})
	}
}

// TestAnnotationRulesValidation tests Validate() for annotation_rules configuration
func TestAnnotationRulesValidation(t *testing.T) {
	t.Parallel()

	baseMonitor := ServiceConfig{
		Provider:   "demo",
		Service:    "cc",
		Category:   "commercial",
		Sponsor:    "test",
		BaseURL:    "https://example.com",
		URLPattern: "{{BASE_URL}}",
		Method:     "POST",
	}

	tests := []struct {
		name      string
		config    AppConfig
		shouldErr bool
		errMsg    string
	}{
		{
			name: "有效的 annotation_rules",
			config: AppConfig{
				AnnotationRules: []AnnotationRule{
					{
						Match: AnnotationMatch{Provider: "demo"},
						Add: []Annotation{
							{ID: "official_key", Label: "官方 Key", Family: AnnotationFamilyPositive},
						},
					},
				},
				Monitors: []ServiceConfig{baseMonitor},
			},
			shouldErr: false,
		},
		{
			name: "annotation id 为空",
			config: AppConfig{
				AnnotationRules: []AnnotationRule{
					{
						Match: AnnotationMatch{Provider: "demo"},
						Add:   []Annotation{{ID: "", Label: "test"}},
					},
				},
				Monitors: []ServiceConfig{baseMonitor},
			},
			shouldErr: true,
			errMsg:    "id 不能为空",
		},
		{
			name: "annotation family 无效",
			config: AppConfig{
				AnnotationRules: []AnnotationRule{
					{
						Match: AnnotationMatch{Provider: "demo"},
						Add:   []Annotation{{ID: "test", Label: "test", Family: "invalid"}},
					},
				},
				Monitors: []ServiceConfig{baseMonitor},
			},
			shouldErr: true,
			errMsg:    "family",
		},
		{
			name: "annotation priority 超出范围",
			config: AppConfig{
				AnnotationRules: []AnnotationRule{
					{
						Match: AnnotationMatch{Provider: "demo"},
						Add:   []Annotation{{ID: "test", Label: "test", Priority: 201}},
					},
				},
				Monitors: []ServiceConfig{baseMonitor},
			},
			shouldErr: true,
			errMsg:    "priority",
		},
		{
			name: "规则缺少 add 和 remove",
			config: AppConfig{
				AnnotationRules: []AnnotationRule{
					{Match: AnnotationMatch{Provider: "demo"}},
				},
				Monitors: []ServiceConfig{baseMonitor},
			},
			shouldErr: true,
			errMsg:    "必须至少包含",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.validate()

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Validate() should return error")
				} else if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() should not return error, got: %v", err)
				}
			}
		})
	}
}

// TestAnnotationsNormalize tests Normalize() for annotation resolution
func TestAnnotationsNormalize(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		AnnotationRules: []AnnotationRule{
			{
				Match: AnnotationMatch{Provider: "demo"},
				Add: []Annotation{
					{ID: "official_key", Label: "官方 Key", Family: AnnotationFamilyPositive, Icon: "shield-check", Priority: 80},
				},
			},
		},
		Monitors: []ServiceConfig{
			{
				Provider:     "demo",
				Service:      "cc",
				Category:     "commercial",
				Sponsor:      "test",
				SponsorLevel: SponsorLevelBeacon,
				BaseURL:      "https://example.com",
				URLPattern:   "{{BASE_URL}}",
				Method:       "POST",
			},
			{
				Provider:   "other",
				Service:    "chat",
				Category:   "public",
				Sponsor:    "community",
				BaseURL:    "https://other.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	// demo monitor 应有: key_type (system) + sponsor_beacon (system) + official_key (rule) + monitor_frequency (system)
	demoMonitor := cfg.Monitors[0]
	if findAnnotationByID(demoMonitor.Annotations, "sponsor_beacon") == nil {
		t.Error("demo monitor should have sponsor_beacon annotation")
	}
	if findAnnotationByID(demoMonitor.Annotations, "official_key") == nil {
		t.Error("demo monitor should have official_key annotation from rule")
	}
	if a := findAnnotationByID(demoMonitor.Annotations, "key_type"); a == nil {
		t.Error("demo monitor should have key_type annotation")
	} else if a.Label != "官方 API" {
		t.Errorf("demo key_type label = %q, want %q", a.Label, "官方 API")
	}

	// other monitor 应有: key_type (system) + public_service (system) + monitor_frequency (system)
	otherMonitor := cfg.Monitors[1]
	if findAnnotationByID(otherMonitor.Annotations, "public_service") == nil {
		t.Error("other monitor should have public_service annotation")
	}
	if a := findAnnotationByID(otherMonitor.Annotations, "key_type"); a == nil {
		t.Error("other monitor should have default key_type annotation")
	} else if a.Label != "官方 API" {
		t.Errorf("other monitor key_type label = %q, want %q", a.Label, "官方 API")
	}
}

// TestAnnotationsSystemDerived tests that monitor_frequency is always derived for monitors with valid interval
func TestAnnotationsSystemDerived(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	// 商业站无赞助无配置规则，应有系统派生的 key_type + monitor_frequency 注解
	anns := cfg.Monitors[0].Annotations
	if len(anns) != 2 {
		t.Errorf("commercial monitor with no rules should have 2 annotations (key_type + monitor_frequency), got %d", len(anns))
	}
	if findAnnotationByID(anns, "key_type") == nil {
		t.Error("expected key_type annotation")
	}
	if findAnnotationByID(anns, "monitor_frequency") == nil {
		t.Error("expected monitor_frequency annotation")
	}
}

// TestAnnotationsClone tests Clone() for annotations deep copy
func TestAnnotationsClone(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		AnnotationRules: []AnnotationRule{
			{
				Match:  AnnotationMatch{Provider: "demo"},
				Add:    []Annotation{{ID: "test", Label: "original"}},
				Remove: []string{"other"},
			},
		},
		Monitors: []ServiceConfig{
			{
				Provider: "demo",
				Service:  "cc",
				Annotations: []Annotation{
					{ID: "test", Label: "original", Family: AnnotationFamilyPositive},
				},
			},
		},
	}

	clone := cfg.clone()

	// 修改原始配置
	cfg.AnnotationRules[0].Add[0].Label = "modified"
	cfg.AnnotationRules[0].Remove[0] = "modified"
	cfg.Monitors[0].Annotations[0].Label = "modified"

	// 验证克隆不受影响
	if clone.AnnotationRules[0].Add[0].Label != "original" {
		t.Errorf("Clone AnnotationRules.Add should not be affected, got Label = %q", clone.AnnotationRules[0].Add[0].Label)
	}
	if clone.AnnotationRules[0].Remove[0] != "other" {
		t.Errorf("Clone AnnotationRules.Remove should not be affected, got = %q", clone.AnnotationRules[0].Remove[0])
	}
	if clone.Monitors[0].Annotations[0].Label != "original" {
		t.Errorf("Clone Monitors.Annotations should not be affected, got Label = %q", clone.Monitors[0].Annotations[0].Label)
	}
}

// TestCloneDeepCopiesMonitorPointerFields tests Clone() for monitor pointer fields deep copy
func TestCloneDeepCopiesMonitorPointerFields(t *testing.T) {
	t.Parallel()

	min := 0.05
	max := 0.2
	retry := 3
	jitter := 0.5

	wantMin := min
	wantMax := max
	wantRetry := retry
	wantJitter := jitter

	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				PriceMin:    &min,
				PriceMax:    &max,
				Retry:       &retry,
				RetryJitter: &jitter,
			},
		},
	}

	clone := cfg.clone()

	// 修改原始值
	*cfg.Monitors[0].PriceMin = 9.9
	*cfg.Monitors[0].PriceMax = 9.8
	*cfg.Monitors[0].Retry = 99
	*cfg.Monitors[0].RetryJitter = 0

	// 验证克隆不受影响
	if clone.Monitors[0].PriceMin == nil || *clone.Monitors[0].PriceMin != wantMin {
		t.Errorf("clone.PriceMin = %v, want %v", clone.Monitors[0].PriceMin, wantMin)
	}
	if clone.Monitors[0].PriceMax == nil || *clone.Monitors[0].PriceMax != wantMax {
		t.Errorf("clone.PriceMax = %v, want %v", clone.Monitors[0].PriceMax, wantMax)
	}
	if clone.Monitors[0].Retry == nil || *clone.Monitors[0].Retry != wantRetry {
		t.Errorf("clone.Retry = %v, want %v", clone.Monitors[0].Retry, wantRetry)
	}
	if clone.Monitors[0].RetryJitter == nil || *clone.Monitors[0].RetryJitter != wantJitter {
		t.Errorf("clone.RetryJitter = %v, want %v", clone.Monitors[0].RetryJitter, wantJitter)
	}
}

// TestCacheTTLNormalize tests CacheTTLConfig.Normalize() parsing and defaults
func TestCacheTTLNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      CacheTTLConfig
		expected90m string
		expected24h string
		expected7d  string
		expected30d string
		shouldErr   bool
	}{
		{
			name:        "全部使用默认值",
			config:      CacheTTLConfig{},
			expected90m: "10s",
			expected24h: "10s",
			expected7d:  "1m0s", // Go duration format: 60s -> 1m0s
			expected30d: "1m0s", // Go duration format: 60s -> 1m0s
			shouldErr:   false,
		},
		{
			name: "自定义所有周期",
			config: CacheTTLConfig{
				TTL90m: "5s",
				TTL24h: "15s",
				TTL7d:  "120s",
				TTL30d: "300s",
			},
			expected90m: "5s",
			expected24h: "15s",
			expected7d:  "2m0s", // Go duration format: 120s -> 2m0s
			expected30d: "5m0s", // Go duration format: 300s -> 5m0s
			shouldErr:   false,
		},
		{
			name: "部分自定义",
			config: CacheTTLConfig{
				TTL90m: "8s",
				// TTL24h 使用默认值
				TTL7d: "90s",
				// TTL30d 使用默认值
			},
			expected90m: "8s",
			expected24h: "10s",
			expected7d:  "1m30s", // Go duration format: 90s -> 1m30s
			expected30d: "1m0s",  // Go duration format: 60s -> 1m0s
			shouldErr:   false,
		},
		{
			name: "无效的 duration 格式",
			config: CacheTTLConfig{
				TTL90m: "invalid",
			},
			shouldErr: true,
		},
		{
			name: "负数 duration",
			config: CacheTTLConfig{
				TTL7d: "-10s",
			},
			shouldErr: true,
		},
		{
			name: "零值 duration",
			config: CacheTTLConfig{
				TTL30d: "0s",
			},
			shouldErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config
			err := cfg.normalize()

			if tt.shouldErr {
				if err == nil {
					t.Errorf("Normalize() should return error")
				}
				return
			}

			if err != nil {
				t.Fatalf("Normalize() should not return error, got: %v", err)
			}

			// 验证解析后的 duration
			if cfg.TTL90mDuration.String() != tt.expected90m {
				t.Errorf("TTL90mDuration = %v, want %s", cfg.TTL90mDuration, tt.expected90m)
			}
			if cfg.TTL24hDuration.String() != tt.expected24h {
				t.Errorf("TTL24hDuration = %v, want %s", cfg.TTL24hDuration, tt.expected24h)
			}
			if cfg.TTL7dDuration.String() != tt.expected7d {
				t.Errorf("TTL7dDuration = %v, want %s", cfg.TTL7dDuration, tt.expected7d)
			}
			if cfg.TTL30dDuration.String() != tt.expected30d {
				t.Errorf("TTL30dDuration = %v, want %s", cfg.TTL30dDuration, tt.expected30d)
			}
		})
	}
}

// TestCacheTTLForPeriod tests CacheTTLConfig.TTLForPeriod() returns correct TTL
func TestCacheTTLForPeriod(t *testing.T) {
	t.Parallel()

	cfg := CacheTTLConfig{
		TTL90m: "5s",
		TTL24h: "15s",
		TTL7d:  "120s",
		TTL30d: "300s",
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	tests := []struct {
		period   string
		expected string
	}{
		{"90m", "5s"},
		{"24h", "15s"},
		{"1d", "15s"}, // 1d 和 24h 相同
		{"7d", "2m0s"},
		{"30d", "5m0s"},
		{"unknown", "10s"}, // 未知周期使用默认值
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got := cfg.TTLForPeriod(tt.period)
			if got.String() != tt.expected {
				t.Errorf("TTLForPeriod(%q) = %v, want %s", tt.period, got, tt.expected)
			}
		})
	}
}

// TestCacheTTLForPeriodDefaults tests TTLForPeriod with zero durations falls back to defaults
func TestCacheTTLForPeriodDefaults(t *testing.T) {
	t.Parallel()

	// 未调用 Normalize() 的配置，Duration 都是零值
	cfg := CacheTTLConfig{}

	tests := []struct {
		period   string
		expected string
	}{
		{"90m", "10s"},
		{"24h", "10s"},
		{"1d", "10s"},
		{"7d", "1m0s"},
		{"30d", "1m0s"},
		{"unknown", "10s"},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			got := cfg.TTLForPeriod(tt.period)
			if got.String() != tt.expected {
				t.Errorf("TTLForPeriod(%q) with zero config = %v, want %s", tt.period, got, tt.expected)
			}
		})
	}
}

// TestAppConfigNormalizeWithCacheTTL tests that AppConfig.Normalize() properly integrates CacheTTL
func TestAppConfigNormalizeWithCacheTTL(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		CacheTTL: CacheTTLConfig{
			TTL90m: "8s",
			TTL24h: "12s",
			TTL7d:  "90s",
			TTL30d: "180s",
		},
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	// 验证 CacheTTL 被正确解析
	if cfg.CacheTTL.TTL90mDuration.String() != "8s" {
		t.Errorf("CacheTTL.TTL90mDuration = %v, want 8s", cfg.CacheTTL.TTL90mDuration)
	}
	if cfg.CacheTTL.TTL7dDuration.String() != "1m30s" {
		t.Errorf("CacheTTL.TTL7dDuration = %v, want 1m30s", cfg.CacheTTL.TTL7dDuration)
	}
}

// TestAppConfigNormalizeWithInvalidCacheTTL tests that invalid CacheTTL causes Normalize() to fail
func TestAppConfigNormalizeWithInvalidCacheTTL(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		CacheTTL: CacheTTLConfig{
			TTL90m: "invalid-duration",
		},
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	err := cfg.normalize()
	if err == nil {
		t.Errorf("Normalize() should return error for invalid cache_ttl")
	}
	if !strings.Contains(err.Error(), "cache_ttl") {
		t.Errorf("error should mention cache_ttl, got: %v", err)
	}
}

// TestKeyTypeUserDerived tests key_type=user derives "用户 Key" annotation with normalization
func TestKeyTypeUserDerived(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				KeyType:    " User ", // 大小写混合 + 空格，测试规范化
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	if cfg.Monitors[0].KeyType != "user" {
		t.Fatalf("monitor KeyType = %q, want %q", cfg.Monitors[0].KeyType, "user")
	}

	a := findAnnotationByID(cfg.Monitors[0].Annotations, "key_type")
	if a == nil {
		t.Fatal("expected key_type annotation")
	}
	if a.Label != "用户 Key" {
		t.Errorf("key_type label = %q, want %q", a.Label, "用户 Key")
	}
	if a.Family != AnnotationFamilyNeutral {
		t.Errorf("key_type family = %q, want %q", a.Family, AnnotationFamilyNeutral)
	}
	if a.Icon != "user" {
		t.Errorf("key_type icon = %q, want %q", a.Icon, "user")
	}
	if a.Priority != 75 {
		t.Errorf("key_type priority = %d, want %d", a.Priority, 75)
	}
	if a.Origin != "system" {
		t.Errorf("key_type origin = %q, want %q", a.Origin, "system")
	}
}

// TestKeyTypeValidation tests Validate() rejects invalid key_type values
func TestKeyTypeValidation(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				KeyType:    "custom",
			},
		},
	}

	err := cfg.validate()
	if err == nil {
		t.Fatal("Validate() should return error for invalid key_type")
	}
	if !strings.Contains(err.Error(), "key_type") {
		t.Fatalf("Validate() error = %q, should contain %q", err.Error(), "key_type")
	}
}

// TestKeyTypeAnnotationRuleOverride tests annotation_rules can override system key_type
func TestKeyTypeAnnotationRuleOverride(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		AnnotationRules: []AnnotationRule{
			{
				Match:  AnnotationMatch{Provider: "demo"},
				Remove: []string{"key_type"}, // 移除系统派生的 key_type
				Add: []Annotation{
					{ID: "key_type", Label: "平台托管 Key", Family: AnnotationFamilyPositive, Icon: "shield-check", Priority: 90},
				},
			},
		},
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	a := findAnnotationByID(cfg.Monitors[0].Annotations, "key_type")
	if a == nil {
		t.Fatal("expected key_type annotation after rule override")
	}
	if a.Label != "平台托管 Key" {
		t.Errorf("key_type label = %q, want %q", a.Label, "平台托管 Key")
	}
	if a.Origin != "rule" {
		t.Errorf("key_type origin = %q, want %q", a.Origin, "rule")
	}
	if a.Priority != 90 {
		t.Errorf("key_type priority = %d, want %d", a.Priority, 90)
	}
}

// TestKeyTypeParentInheritance tests child monitor inherits key_type from parent
func TestKeyTypeParentInheritance(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Channel:    "vip",
				Model:      "base",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
				KeyType:    "user",
			},
			{
				Model:  "child",
				Parent: "demo/cc/vip",
			},
		},
	}

	if err := cfg.validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	child := cfg.Monitors[1]
	if child.KeyType != "user" {
		t.Fatalf("child.KeyType = %q, want %q", child.KeyType, "user")
	}

	a := findAnnotationByID(child.Annotations, "key_type")
	if a == nil {
		t.Fatal("child should have inherited key_type annotation")
	}
	if a.Label != "用户 Key" {
		t.Errorf("child key_type label = %q, want %q", a.Label, "用户 Key")
	}
}

// TestKeyTypeRemoveOnly tests annotation_rules can remove system key_type without adding replacement
func TestKeyTypeRemoveOnly(t *testing.T) {
	t.Parallel()

	cfg := &AppConfig{
		AnnotationRules: []AnnotationRule{
			{
				Match:  AnnotationMatch{Provider: "demo"},
				Remove: []string{"key_type"},
			},
		},
		Monitors: []ServiceConfig{
			{
				Provider:   "demo",
				Service:    "cc",
				Category:   "commercial",
				Sponsor:    "test",
				BaseURL:    "https://example.com",
				URLPattern: "{{BASE_URL}}",
				Method:     "POST",
			},
		},
	}

	if err := cfg.normalize(); err != nil {
		t.Fatalf("Normalize() failed: %v", err)
	}

	if a := findAnnotationByID(cfg.Monitors[0].Annotations, "key_type"); a != nil {
		t.Errorf("key_type annotation should have been removed by rule, but found: %+v", *a)
	}
}

// TestClone_PreservesAllTopLevelBoolFlags 守卫 clone() 的显式字段复制不漏抄开关位。
//
// clone() 是一张手写的字段清单，加一个 bool 开关而忘了往清单里补，编译器一声不吭、
// 克隆出来的配置静默丢开关——这类漏抄发生过（AllowPrivateNetworks 就长期没被复制，
// 补 HideCategoryFilter 时才顺带发现）。所以这里守的是**整类**问题而不是某个字段：
// 用反射把所有顶层 bool 置 true，clone 后逐个核对，新增开关自动纳入。
//
// 只覆盖顶层 bool：嵌套结构体（Boards/Storage 等）是值类型整体复制，不属于这个 bug 类别。
func TestClone_PreservesAllTopLevelBoolFlags(t *testing.T) {
	cfg := &AppConfig{}
	v := reflect.ValueOf(cfg).Elem()
	typ := v.Type()

	var flags []string
	for i := 0; i < typ.NumField(); i++ {
		if typ.Field(i).Type.Kind() == reflect.Bool {
			v.Field(i).SetBool(true)
			flags = append(flags, typ.Field(i).Name)
		}
	}
	if len(flags) == 0 {
		t.Fatal("AppConfig 顶层一个 bool 字段都没扫到，反射逻辑失效（测试会假绿）")
	}

	clone := reflect.ValueOf(cfg.clone()).Elem()
	for _, name := range flags {
		if !clone.FieldByName(name).Bool() {
			t.Errorf("clone() 丢了 bool 开关 %s —— 在 lifecycle.go 的字段清单里补上它", name)
		}
	}
}
