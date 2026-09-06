package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 通知服务配置
type Config struct {
	RelayPulse RelayPulseConfig `yaml:"relay_pulse"`
	Telegram   TelegramConfig   `yaml:"telegram"`
	QQ         QQConfig         `yaml:"qq"`
	Database   DatabaseConfig   `yaml:"database"`
	API        APIConfig        `yaml:"api"`
	Limits     LimitsConfig     `yaml:"limits"`
	Screenshot ScreenshotConfig `yaml:"screenshot"`
}

// RelayPulseConfig relay-pulse 事件 API 配置
type RelayPulseConfig struct {
	EventsURL    string        `yaml:"events_url"`
	APIToken     string        `yaml:"api_token"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

// TelegramConfig Telegram Bot 配置
type TelegramConfig struct {
	BotToken    string `yaml:"bot_token"`
	BotUsername string `yaml:"bot_username"`
}

// QQConfig QQ Bot 配置（OneBot v11 / NapCatQQ）
type QQConfig struct {
	Enabled        bool    `yaml:"enabled"`         // 是否启用 QQ 通知
	OneBotHTTPURL  string  `yaml:"onebot_http_url"` // OneBot HTTP API 地址
	AccessToken    string  `yaml:"access_token"`    // OneBot API Token（可选）
	CallbackPath   string  `yaml:"callback_path"`   // 接收上报的路径，默认 /qq/callback
	CallbackSecret string  `yaml:"callback_secret"` // Webhook 签名密钥（可选）
	AdminWhitelist []int64 `yaml:"admin_whitelist"` // 管理员白名单 QQ 号（可越权执行管理命令）
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Driver string `yaml:"driver"` // sqlite 或 postgres
	DSN    string `yaml:"dsn"`
}

// APIConfig HTTP API 配置
type APIConfig struct {
	Addr string `yaml:"addr"` // 监听地址，如 :8081

	// AuthToken 可选 Bearer Token，非空时要求 bind-token 端点携带 Authorization: Bearer <token>
	// 默认空（不鉴权），可通过环境变量 API_AUTH_TOKEN 覆盖
	AuthToken string `yaml:"auth_token"`

	// CORSAllowedOrigins 允许的 CORS Origin 列表
	// 默认 ["*"]（向后兼容），部署时建议收紧为实际前端域名
	// 可通过环境变量 API_CORS_ALLOWED_ORIGINS（逗号分隔）覆盖
	CORSAllowedOrigins []string `yaml:"cors_allowed_origins"`

	// TrustProxy 是否信任反向代理的 X-Forwarded-For / X-Real-IP 头
	// 默认 false（仅使用 RemoteAddr 提取客户端 IP）
	// 部署在 nginx/caddy/cloudflare 等反向代理后面时应设为 true
	TrustProxy bool `yaml:"trust_proxy"`
}

// LimitsConfig 限制配置
type LimitsConfig struct {
	MaxSubscriptionsPerUser int           `yaml:"max_subscriptions_per_user"`
	MaxRetries              int           `yaml:"max_retries"`
	BindTokenTTL            time.Duration `yaml:"bind_token_ttl"`

	// 平台独立限流配置
	TelegramRateLimitPerSecond int `yaml:"telegram_rate_limit_per_second"` // Telegram 发送限流（每秒消息数）
	QQRateLimitPerSecond       int `yaml:"qq_rate_limit_per_second"`       // QQ 发送限流（每秒消息数，建议 1-2）

	// QQ 发送抖动：在通过限流后额外 sleep 一段随机时间，用于错峰降低风控
	QQJitterMin time.Duration `yaml:"qq_jitter_min"`
	QQJitterMax time.Duration `yaml:"qq_jitter_max"`

	// RateLimitPerSecond 兼容旧配置（deprecated）：等价于 TelegramRateLimitPerSecond
	RateLimitPerSecond int `yaml:"rate_limit_per_second"`
}

// ScreenshotConfig 截图功能配置
type ScreenshotConfig struct {
	Enabled       bool          `yaml:"enabled"`        // 是否启用截图功能
	BaseURL       string        `yaml:"base_url"`       // 截图目标 URL，默认 https://relaypulse.top
	Timeout       time.Duration `yaml:"timeout"`        // 截图超时时间，默认 30s
	MaxConcurrent int           `yaml:"max_concurrent"` // 最大并发数，默认 3
	IdleTimeout   time.Duration `yaml:"idle_timeout"`   // 浏览器空闲多久后回收，默认 10m
}

// Load 从文件加载配置，并应用环境变量覆盖
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 应用环境变量覆盖
	cfg.applyEnvOverrides()

	// 设置默认值
	cfg.setDefaults()

	// 验证配置
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyEnvOverrides 从环境变量覆盖配置
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("RELAY_PULSE_EVENTS_URL"); v != "" {
		c.RelayPulse.EventsURL = v
	}
	if v := os.Getenv("RELAY_PULSE_API_TOKEN"); v != "" {
		c.RelayPulse.APIToken = v
	}
	if v := os.Getenv("TELEGRAM_BOT_TOKEN"); v != "" {
		c.Telegram.BotToken = v
	}
	if v := os.Getenv("DATABASE_DSN"); v != "" {
		c.Database.DSN = v
	}
	if v := os.Getenv("API_ADDR"); v != "" {
		c.API.Addr = v
	}
	if v := os.Getenv("API_AUTH_TOKEN"); v != "" {
		c.API.AuthToken = strings.TrimSpace(v)
	}
	if v := os.Getenv("API_CORS_ALLOWED_ORIGINS"); v != "" {
		c.API.CORSAllowedOrigins = splitAndTrimCSV(v)
	}
	if v := os.Getenv("API_TRUST_PROXY"); v == "true" || v == "1" {
		c.API.TrustProxy = true
	}
	// QQ 相关环境变量
	if v := os.Getenv("QQ_ONEBOT_HTTP_URL"); v != "" {
		c.QQ.OneBotHTTPURL = v
		c.QQ.Enabled = true
	}
	if v := os.Getenv("QQ_ACCESS_TOKEN"); v != "" {
		c.QQ.AccessToken = v
	}
	if v := os.Getenv("QQ_CALLBACK_SECRET"); v != "" {
		c.QQ.CallbackSecret = v
	}
}

// setDefaults 设置默认值
func (c *Config) setDefaults() {
	if c.RelayPulse.PollInterval == 0 {
		c.RelayPulse.PollInterval = 5 * time.Second
	}
	if c.Database.Driver == "" {
		c.Database.Driver = "sqlite"
	}
	if c.Database.DSN == "" {
		c.Database.DSN = "file:notifier.db?_journal_mode=WAL&_timeout=5000&_busy_timeout=5000"
	}
	if c.API.Addr == "" {
		c.API.Addr = ":8081"
	}
	c.API.AuthToken = strings.TrimSpace(c.API.AuthToken)
	if len(c.API.CORSAllowedOrigins) == 0 {
		c.API.CORSAllowedOrigins = []string{"*"}
	} else {
		c.API.CORSAllowedOrigins = normalizeCORSOrigins(c.API.CORSAllowedOrigins)
	}
	if c.Limits.MaxSubscriptionsPerUser == 0 {
		c.Limits.MaxSubscriptionsPerUser = 20
	}
	// 平台独立限流默认值
	// 兼容旧字段：rate_limit_per_second 视为 Telegram 限流
	if c.Limits.TelegramRateLimitPerSecond == 0 {
		if c.Limits.RateLimitPerSecond > 0 {
			c.Limits.TelegramRateLimitPerSecond = c.Limits.RateLimitPerSecond
		} else {
			c.Limits.TelegramRateLimitPerSecond = 25
		}
	}
	if c.Limits.QQRateLimitPerSecond == 0 {
		c.Limits.QQRateLimitPerSecond = 2 // QQ 保守限流
	}
	// QQ 抖动默认值：0-300ms
	if c.Limits.QQJitterMax == 0 {
		c.Limits.QQJitterMax = 300 * time.Millisecond
	}
	if c.Limits.MaxRetries == 0 {
		c.Limits.MaxRetries = 3
	}
	if c.Limits.BindTokenTTL == 0 {
		c.Limits.BindTokenTTL = 5 * time.Minute
	}
	// QQ 默认值
	if c.QQ.CallbackPath == "" {
		c.QQ.CallbackPath = "/qq/callback"
	}
	// Screenshot 默认值
	if c.Screenshot.BaseURL == "" {
		c.Screenshot.BaseURL = "https://relaypulse.top"
	}
	if c.Screenshot.Timeout == 0 {
		c.Screenshot.Timeout = 30 * time.Second
	}
	if c.Screenshot.MaxConcurrent == 0 {
		c.Screenshot.MaxConcurrent = 3
	}
	if c.Screenshot.IdleTimeout == 0 {
		c.Screenshot.IdleTimeout = 10 * time.Minute
	}
}

// splitAndTrimCSV 将逗号分隔的字符串拆分并去除空白
func splitAndTrimCSV(v string) []string {
	parts := strings.Split(v, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}

// normalizeCORSOrigins 规范化 CORS origin 列表：含 "*" 时直接返回 ["*"]
func normalizeCORSOrigins(origins []string) []string {
	result := make([]string, 0, len(origins))
	for _, o := range origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			return []string{"*"}
		}
		result = append(result, o)
	}
	if len(result) == 0 {
		return []string{"*"}
	}
	return result
}

// validate 验证配置
func (c *Config) validate() error {
	if c.RelayPulse.EventsURL == "" {
		return fmt.Errorf("relay_pulse.events_url 是必需的")
	}
	if c.RelayPulse.APIToken == "" {
		return fmt.Errorf("relay_pulse.api_token 是必需的（环境变量 RELAY_PULSE_API_TOKEN）")
	}
	// 负数是配置写错，不能像零值那样静默回退到默认值
	if c.Screenshot.IdleTimeout < 0 {
		return fmt.Errorf("screenshot.idle_timeout 不能为负数（当前 %s）；留空或 0 表示使用默认 10m", c.Screenshot.IdleTimeout)
	}
	// Telegram Bot Token 在开发环境可选（仅 API 服务启动）
	// 如果未设置，Bot 和 Poller 功能将不可用
	return nil
}

// HasTelegramToken 检查是否配置了 Telegram Bot Token
func (c *Config) HasTelegramToken() bool {
	return c.Telegram.BotToken != ""
}

// HasQQ 检查是否启用了 QQ 通知
func (c *Config) HasQQ() bool {
	return c.QQ.Enabled && c.QQ.OneBotHTTPURL != ""
}

// HasScreenshot 检查是否启用了截图功能
func (c *Config) HasScreenshot() bool {
	return c.Screenshot.Enabled
}
