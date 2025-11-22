package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ServiceConfig 单个服务监控配置
type ServiceConfig struct {
	Provider    string            `yaml:"provider" json:"provider"`
	ProviderURL string            `yaml:"provider_url" json:"provider_url"` // 服务商官网链接（可选）
	Service     string            `yaml:"service" json:"service"`
	Category    string            `yaml:"category" json:"category"` // 分类：commercial（推广站）或 public（公益站）
	Sponsor     string            `yaml:"sponsor" json:"sponsor"`   // 赞助者：提供 API Key 的个人或组织
	SponsorURL  string            `yaml:"sponsor_url" json:"sponsor_url"` // 赞助者链接（可选）
	Channel     string            `yaml:"channel" json:"channel"`   // 业务通道标识（如 "vip-channel"、"standard-channel"），用于分类和过滤
	URL         string            `yaml:"url" json:"url"`
	Method      string            `yaml:"method" json:"method"`
	Headers     map[string]string `yaml:"headers" json:"headers"`
	Body        string            `yaml:"body" json:"body"`

	// SuccessContains 可选：响应体需包含的关键字，用于判定请求语义是否成功
	SuccessContains string `yaml:"success_contains" json:"success_contains"`

	// 解析后的"慢请求"阈值（来自全局配置），用于黄灯判定
	SlowLatencyDuration time.Duration `yaml:"-" json:"-"`

	APIKey string `yaml:"api_key" json:"-"` // 不返回给前端
}

// StorageConfig 存储配置
type StorageConfig struct {
	Type string `yaml:"type" json:"type"` // "sqlite" 或 "postgres"

	// SQLite 配置
	SQLite SQLiteConfig `yaml:"sqlite" json:"sqlite"`

	// PostgreSQL 配置
	Postgres PostgresConfig `yaml:"postgres" json:"postgres"`
}

// SQLiteConfig SQLite 配置
type SQLiteConfig struct {
	Path string `yaml:"path" json:"path"` // 数据库文件路径
}

// PostgresConfig PostgreSQL 配置
type PostgresConfig struct {
	Host            string `yaml:"host" json:"host"`
	Port            int    `yaml:"port" json:"port"`
	User            string `yaml:"user" json:"user"`
	Password        string `yaml:"password" json:"-"` // 不输出到 JSON
	Database        string `yaml:"database" json:"database"`
	SSLMode         string `yaml:"sslmode" json:"sslmode"`
	MaxOpenConns    int    `yaml:"max_open_conns" json:"max_open_conns"`
	MaxIdleConns    int    `yaml:"max_idle_conns" json:"max_idle_conns"`
	ConnMaxLifetime string `yaml:"conn_max_lifetime" json:"conn_max_lifetime"`
}

// AppConfig 应用配置
type AppConfig struct {
	// 巡检间隔（支持 Go duration 格式，例如 "30s"、"1m", "5m"）
	Interval string `yaml:"interval" json:"interval"`

	// 解析后的巡检间隔（内部使用，不序列化）
	IntervalDuration time.Duration `yaml:"-" json:"-"`

	// 慢请求阈值（超过则从绿降为黄），支持 Go duration 格式，例如 "5s"、"3s"
	SlowLatency string `yaml:"slow_latency" json:"slow_latency"`

	// 解析后的慢请求阈值（内部使用，不序列化）
	SlowLatencyDuration time.Duration `yaml:"-" json:"-"`

	// 可用率中黄色状态的权重（0-1，默认 0.7）
	// 绿色=1.0, 黄色=degraded_weight, 红色=0.0
	DegradedWeight float64 `yaml:"degraded_weight" json:"degraded_weight"`

	// 存储配置
	Storage StorageConfig `yaml:"storage" json:"storage"`

	Monitors []ServiceConfig `yaml:"monitors"`
}

// Validate 验证配置合法性
func (c *AppConfig) Validate() error {
	if len(c.Monitors) == 0 {
		return fmt.Errorf("至少需要配置一个监控项")
	}

	// 检查重复和必填字段
	seen := make(map[string]bool)
	for i, m := range c.Monitors {
		// 必填字段检查
		if m.Provider == "" {
			return fmt.Errorf("monitor[%d]: provider 不能为空", i)
		}
		if m.Service == "" {
			return fmt.Errorf("monitor[%d]: service 不能为空", i)
		}
		if m.URL == "" {
			return fmt.Errorf("monitor[%d]: URL 不能为空", i)
		}
		if m.Method == "" {
			return fmt.Errorf("monitor[%d]: method 不能为空", i)
		}
		if m.Category == "" {
			return fmt.Errorf("monitor[%d]: category 不能为空（必须是 commercial 或 public）", i)
		}
		if strings.TrimSpace(m.Sponsor) == "" {
			return fmt.Errorf("monitor[%d]: sponsor 不能为空", i)
		}

		// Method 枚举检查
		validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true}
		if !validMethods[strings.ToUpper(m.Method)] {
			return fmt.Errorf("monitor[%d]: method '%s' 无效，必须是 GET/POST/PUT/DELETE/PATCH 之一", i, m.Method)
		}

		// Category 枚举检查
		if !isValidCategory(m.Category) {
			return fmt.Errorf("monitor[%d]: category '%s' 无效，必须是 commercial 或 public", i, m.Category)
		}

		// ProviderURL 验证（可选字段）
		if m.ProviderURL != "" {
			if err := validateURL(m.ProviderURL, "provider_url"); err != nil {
				return fmt.Errorf("monitor[%d]: %w", i, err)
			}
		}

		// SponsorURL 验证（可选字段）
		if m.SponsorURL != "" {
			if err := validateURL(m.SponsorURL, "sponsor_url"); err != nil {
				return fmt.Errorf("monitor[%d]: %w", i, err)
			}
		}

		// 唯一性检查（provider + service + channel 组合唯一）
		key := m.Provider + "/" + m.Service + "/" + m.Channel
		if seen[key] {
			return fmt.Errorf("重复的监控项: provider=%s, service=%s, channel=%s", m.Provider, m.Service, m.Channel)
		}
		seen[key] = true
	}

	return nil
}

// Normalize 规范化配置（填充默认值等）
func (c *AppConfig) Normalize() error {
	// 巡检间隔
	if c.Interval == "" {
		c.IntervalDuration = time.Minute
	} else {
		d, err := time.ParseDuration(c.Interval)
		if err != nil {
			return fmt.Errorf("解析 interval 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("interval 必须大于 0")
		}
		c.IntervalDuration = d
	}

	// 慢请求阈值
	if c.SlowLatency == "" {
		c.SlowLatencyDuration = 5 * time.Second
	} else {
		d, err := time.ParseDuration(c.SlowLatency)
		if err != nil {
			return fmt.Errorf("解析 slow_latency 失败: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("slow_latency 必须大于 0")
		}
		c.SlowLatencyDuration = d
	}

	// 黄色状态权重（默认 0.7，允许 0.01-1.0）
	// 注意：0 被视为未配置，将使用默认值 0.7
	// 如果需要极低权重，请使用 0.01 或更小的正数
	if c.DegradedWeight == 0 {
		c.DegradedWeight = 0.7 // 未配置时使用默认值
	}
	if c.DegradedWeight < 0 || c.DegradedWeight > 1 {
		return fmt.Errorf("degraded_weight 必须在 0 到 1 之间（0 表示使用默认值 0.7），当前值: %.2f", c.DegradedWeight)
	}

	// 存储配置默认值
	if c.Storage.Type == "" {
		c.Storage.Type = "sqlite" // 默认使用 SQLite
	}
	if c.Storage.Type == "sqlite" && c.Storage.SQLite.Path == "" {
		c.Storage.SQLite.Path = "monitor.db" // 默认路径
	}
	if c.Storage.Type == "postgres" {
		if c.Storage.Postgres.Port == 0 {
			c.Storage.Postgres.Port = 5432
		}
		if c.Storage.Postgres.SSLMode == "" {
			c.Storage.Postgres.SSLMode = "disable"
		}
		if c.Storage.Postgres.MaxOpenConns == 0 {
			c.Storage.Postgres.MaxOpenConns = 25
		}
		if c.Storage.Postgres.MaxIdleConns == 0 {
			c.Storage.Postgres.MaxIdleConns = 5
		}
		if c.Storage.Postgres.ConnMaxLifetime == "" {
			c.Storage.Postgres.ConnMaxLifetime = "1h"
		}
	}

	// 将全局慢请求阈值下发到每个监控项，并标准化 category、URLs
	for i := range c.Monitors {
		if c.Monitors[i].SlowLatencyDuration == 0 {
			c.Monitors[i].SlowLatencyDuration = c.SlowLatencyDuration
		}
		// 标准化 category 为小写
		c.Monitors[i].Category = strings.ToLower(c.Monitors[i].Category)

		// 规范化 URLs：去除首尾空格和末尾的 /
		c.Monitors[i].ProviderURL = strings.TrimRight(strings.TrimSpace(c.Monitors[i].ProviderURL), "/")
		c.Monitors[i].SponsorURL = strings.TrimRight(strings.TrimSpace(c.Monitors[i].SponsorURL), "/")
	}

	return nil
}

// ApplyEnvOverrides 应用环境变量覆盖
// API Key 格式：MONITOR_<PROVIDER>_<SERVICE>_API_KEY
// 存储配置格式：MONITOR_STORAGE_TYPE, MONITOR_POSTGRES_HOST 等
func (c *AppConfig) ApplyEnvOverrides() {
	// 存储配置环境变量覆盖
	if envType := os.Getenv("MONITOR_STORAGE_TYPE"); envType != "" {
		c.Storage.Type = envType
	}

	// PostgreSQL 配置环境变量覆盖
	if envHost := os.Getenv("MONITOR_POSTGRES_HOST"); envHost != "" {
		c.Storage.Postgres.Host = envHost
	}
	if envPort := os.Getenv("MONITOR_POSTGRES_PORT"); envPort != "" {
		if port, err := fmt.Sscanf(envPort, "%d", &c.Storage.Postgres.Port); err == nil && port == 1 {
			// Port parsed successfully
		}
	}
	if envUser := os.Getenv("MONITOR_POSTGRES_USER"); envUser != "" {
		c.Storage.Postgres.User = envUser
	}
	if envPass := os.Getenv("MONITOR_POSTGRES_PASSWORD"); envPass != "" {
		c.Storage.Postgres.Password = envPass
	}
	if envDB := os.Getenv("MONITOR_POSTGRES_DATABASE"); envDB != "" {
		c.Storage.Postgres.Database = envDB
	}
	if envSSL := os.Getenv("MONITOR_POSTGRES_SSLMODE"); envSSL != "" {
		c.Storage.Postgres.SSLMode = envSSL
	}

	// SQLite 配置环境变量覆盖
	if envPath := os.Getenv("MONITOR_SQLITE_PATH"); envPath != "" {
		c.Storage.SQLite.Path = envPath
	}

	// API Key 覆盖
	for i := range c.Monitors {
		m := &c.Monitors[i]
		envKey := fmt.Sprintf("MONITOR_%s_%s_API_KEY",
			strings.ToUpper(strings.ReplaceAll(m.Provider, "-", "_")),
			strings.ToUpper(strings.ReplaceAll(m.Service, "-", "_")))

		if envVal := os.Getenv(envKey); envVal != "" {
			m.APIKey = envVal
		}
	}
}

// ProcessPlaceholders 处理 {{API_KEY}} 占位符替换（headers 和 body）
func (m *ServiceConfig) ProcessPlaceholders() {
	// Headers 中替换
	for k, v := range m.Headers {
		m.Headers[k] = strings.ReplaceAll(v, "{{API_KEY}}", m.APIKey)
	}

	// Body 中替换
	m.Body = strings.ReplaceAll(m.Body, "{{API_KEY}}", m.APIKey)
}

// ResolveBodyIncludes 允许 body 字段引用 data/ 目录下的 JSON 文件
func (c *AppConfig) ResolveBodyIncludes(configDir string) error {
	for i := range c.Monitors {
		if err := c.Monitors[i].resolveBodyInclude(configDir); err != nil {
			return err
		}
	}
	return nil
}

func (m *ServiceConfig) resolveBodyInclude(configDir string) error {
	const includePrefix = "!include "
	trimmed := strings.TrimSpace(m.Body)
	if trimmed == "" || !strings.HasPrefix(trimmed, includePrefix) {
		return nil
	}

	relativePath := strings.TrimSpace(trimmed[len(includePrefix):])
	if relativePath == "" {
		return fmt.Errorf("monitor provider=%s service=%s: body include 路径不能为空", m.Provider, m.Service)
	}

	if filepath.IsAbs(relativePath) {
		return fmt.Errorf("monitor provider=%s service=%s: body include 必须使用相对路径", m.Provider, m.Service)
	}

	cleanPath := filepath.Clean(relativePath)
	targetPath := filepath.Join(configDir, cleanPath)

	dataDir := filepath.Clean(filepath.Join(configDir, "data"))
	targetPath = filepath.Clean(targetPath)

	// 确保引用的文件位于 data/ 目录内
	if targetPath != dataDir && !strings.HasPrefix(targetPath, dataDir+string(os.PathSeparator)) {
		return fmt.Errorf("monitor provider=%s service=%s: body include 路径必须位于 data/ 目录", m.Provider, m.Service)
	}

	content, err := os.ReadFile(targetPath)
	if err != nil {
		return fmt.Errorf("monitor provider=%s service=%s: 读取 body include 文件失败: %w", m.Provider, m.Service, err)
	}

	m.Body = string(content)
	return nil
}

// isValidCategory 检查 category 是否为有效值
func isValidCategory(category string) bool {
	normalized := strings.ToLower(strings.TrimSpace(category))
	return normalized == "commercial" || normalized == "public"
}

// Clone 深拷贝配置（用于热更新回滚）
func (c *AppConfig) Clone() *AppConfig {
	clone := &AppConfig{
		Interval:            c.Interval,
		IntervalDuration:    c.IntervalDuration,
		SlowLatency:         c.SlowLatency,
		SlowLatencyDuration: c.SlowLatencyDuration,
		DegradedWeight:      c.DegradedWeight,
		Storage:             c.Storage,
		Monitors:            make([]ServiceConfig, len(c.Monitors)),
	}
	copy(clone.Monitors, c.Monitors)
	return clone
}

// validateURL 验证 URL 格式和协议安全性
func validateURL(rawURL, fieldName string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}

	parsed, err := url.ParseRequestURI(trimmed)
	if err != nil {
		return fmt.Errorf("%s 格式无效: %w", fieldName, err)
	}

	// 只允许 http 和 https 协议
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%s 只支持 http:// 或 https:// 协议，收到: %s", fieldName, parsed.Scheme)
	}

	// 非 HTTPS 警告
	if scheme == "http" {
		log.Printf("[Config] 警告: %s 使用了非加密的 http:// 协议: %s", fieldName, trimmed)
	}

	return nil
}
