package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSplitAndTrimCSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "正常分割",
			input: "alpha,beta,gamma",
			want:  []string{"alpha", "beta", "gamma"},
		},
		{
			name:  "空白处理",
			input: " alpha , , beta ,  gamma  ",
			want:  []string{"alpha", "beta", "gamma"},
		},
		{
			name:  "纯空输入",
			input: "",
			want:  []string{},
		},
		{
			name:  "单元素",
			input: "  only  ",
			want:  []string{"only"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitAndTrimCSV(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("splitAndTrimCSV(%q) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeCORSOrigins(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "包含星号短路",
			input: []string{"https://a.example", " * ", "https://b.example"},
			want:  []string{"*"},
		},
		{
			name:  "过滤空白",
			input: []string{" https://a.example ", "", "   ", "https://b.example"},
			want:  []string{"https://a.example", "https://b.example"},
		},
		{
			name:  "全空时回退星号",
			input: []string{" ", ""},
			want:  []string{"*"},
		},
		{
			name:  "仅星号",
			input: []string{"*"},
			want:  []string{"*"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCORSOrigins(tt.input)
			if !slices.Equal(got, tt.want) {
				t.Fatalf("normalizeCORSOrigins(%#v) = %#v, want %#v", tt.input, got, tt.want)
			}
		})
	}
}

func TestConfigSetDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	// 逐字段验证默认值，便于定位失败原因
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"PollInterval", cfg.RelayPulse.PollInterval, 5 * time.Second},
		{"Database.Driver", cfg.Database.Driver, "sqlite"},
		{"Database.DSN", cfg.Database.DSN, "file:notifier.db?_journal_mode=WAL&_timeout=5000&_busy_timeout=5000"},
		{"API.Addr", cfg.API.Addr, ":8081"},
		{"API.CORSAllowedOrigins", strings.Join(cfg.API.CORSAllowedOrigins, ","), "*"},
		{"Limits.MaxSubscriptionsPerUser", cfg.Limits.MaxSubscriptionsPerUser, 20},
		{"Limits.MaxRetries", cfg.Limits.MaxRetries, 3},
		{"Limits.BindTokenTTL", cfg.Limits.BindTokenTTL, 5 * time.Minute},
		{"Limits.TelegramRateLimitPerSecond", cfg.Limits.TelegramRateLimitPerSecond, 25},
		{"Limits.QQRateLimitPerSecond", cfg.Limits.QQRateLimitPerSecond, 2},
		{"Limits.QQJitterMax", cfg.Limits.QQJitterMax, 300 * time.Millisecond},
		{"QQ.CallbackPath", cfg.QQ.CallbackPath, "/qq/callback"},
		{"Screenshot.BaseURL", cfg.Screenshot.BaseURL, "https://relaypulse.top"},
		{"Screenshot.Timeout", cfg.Screenshot.Timeout, 30 * time.Second},
		{"Screenshot.MaxConcurrent", cfg.Screenshot.MaxConcurrent, 3},
		{"Screenshot.IdleTimeout", cfg.Screenshot.IdleTimeout, 10 * time.Minute},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("setDefaults() %s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestConfigSetDefaults_PreservesExplicitValues(t *testing.T) {
	// 已有显式配置不应被默认值覆盖
	cfg := &Config{
		RelayPulse: RelayPulseConfig{PollInterval: 10 * time.Second},
		Database:   DatabaseConfig{Driver: "postgres", DSN: "postgres://localhost/notifier"},
		API:        APIConfig{Addr: ":9090", CORSAllowedOrigins: []string{"https://example.com"}},
		Limits: LimitsConfig{
			MaxSubscriptionsPerUser:    50,
			TelegramRateLimitPerSecond: 30,
			QQRateLimitPerSecond:       5,
			MaxRetries:                 5,
			BindTokenTTL:               10 * time.Minute,
			QQJitterMax:                500 * time.Millisecond,
		},
		QQ:         QQConfig{CallbackPath: "/custom/callback"},
		Screenshot: ScreenshotConfig{BaseURL: "https://custom.example", Timeout: 60 * time.Second, MaxConcurrent: 5},
	}
	cfg.setDefaults()

	if cfg.RelayPulse.PollInterval != 10*time.Second {
		t.Errorf("PollInterval was overwritten: %v", cfg.RelayPulse.PollInterval)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("Database.Driver was overwritten: %s", cfg.Database.Driver)
	}
	if cfg.API.Addr != ":9090" {
		t.Errorf("API.Addr was overwritten: %s", cfg.API.Addr)
	}
	if cfg.Limits.MaxSubscriptionsPerUser != 50 {
		t.Errorf("MaxSubscriptionsPerUser was overwritten: %d", cfg.Limits.MaxSubscriptionsPerUser)
	}
	if cfg.Limits.TelegramRateLimitPerSecond != 30 {
		t.Errorf("TelegramRateLimitPerSecond was overwritten: %d", cfg.Limits.TelegramRateLimitPerSecond)
	}
	if cfg.QQ.CallbackPath != "/custom/callback" {
		t.Errorf("QQ.CallbackPath was overwritten: %s", cfg.QQ.CallbackPath)
	}
	// CORSAllowedOrigins 非空时应走 normalizeCORSOrigins 而非覆盖
	if len(cfg.API.CORSAllowedOrigins) != 1 || cfg.API.CORSAllowedOrigins[0] != "https://example.com" {
		t.Errorf("CORSAllowedOrigins was overwritten: %v", cfg.API.CORSAllowedOrigins)
	}
}

func TestConfigSetDefaults_RateLimitLegacyFallback(t *testing.T) {
	// 旧字段 RateLimitPerSecond 应回退为 TelegramRateLimitPerSecond
	cfg := &Config{
		Limits: LimitsConfig{RateLimitPerSecond: 10},
	}
	cfg.setDefaults()

	if cfg.Limits.TelegramRateLimitPerSecond != 10 {
		t.Fatalf("TelegramRateLimitPerSecond = %d, want 10 (legacy fallback)", cfg.Limits.TelegramRateLimitPerSecond)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		wantErrPart string
	}{
		{
			name: "缺少 events_url",
			cfg: &Config{
				RelayPulse: RelayPulseConfig{APIToken: "token"},
			},
			wantErrPart: "relay_pulse.events_url",
		},
		{
			name: "缺少 api_token",
			cfg: &Config{
				RelayPulse: RelayPulseConfig{EventsURL: "https://example.com/api/events"},
			},
			wantErrPart: "relay_pulse.api_token",
		},
		{
			name: "必填项齐全",
			cfg: &Config{
				RelayPulse: RelayPulseConfig{
					EventsURL: "https://example.com/api/events",
					APIToken:  "token",
				},
			},
		},
		{
			// 零值走默认 10m，负数是写错了，不能静默回退
			name: "screenshot.idle_timeout 为负数",
			cfg: &Config{
				RelayPulse: RelayPulseConfig{
					EventsURL: "https://example.com/api/events",
					APIToken:  "token",
				},
				Screenshot: ScreenshotConfig{IdleTimeout: -time.Second},
			},
			wantErrPart: "screenshot.idle_timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if tt.wantErrPart == "" {
				if err != nil {
					t.Fatalf("validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() error = nil, want contains %q", tt.wantErrPart)
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Fatalf("validate() error = %q, want contains %q", err.Error(), tt.wantErrPart)
			}
		})
	}
}

func TestConfigFeatureFlags(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		wantTelegram bool
		wantQQ       bool
		wantShot     bool
	}{
		{
			name: "全部关闭",
			cfg:  Config{},
		},
		{
			name: "全部启用",
			cfg: Config{
				Telegram:   TelegramConfig{BotToken: "bot-token"},
				QQ:         QQConfig{Enabled: true, OneBotHTTPURL: "http://127.0.0.1:3000"},
				Screenshot: ScreenshotConfig{Enabled: true},
			},
			wantTelegram: true,
			wantQQ:       true,
			wantShot:     true,
		},
		{
			name: "QQ 缺少 URL 时不生效",
			cfg:  Config{QQ: QQConfig{Enabled: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.HasTelegramToken(); got != tt.wantTelegram {
				t.Fatalf("HasTelegramToken() = %v, want %v", got, tt.wantTelegram)
			}
			if got := tt.cfg.HasQQ(); got != tt.wantQQ {
				t.Fatalf("HasQQ() = %v, want %v", got, tt.wantQQ)
			}
			if got := tt.cfg.HasScreenshot(); got != tt.wantShot {
				t.Fatalf("HasScreenshot() = %v, want %v", got, tt.wantShot)
			}
		})
	}
}
