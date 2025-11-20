package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ServiceConfig 单个服务监控配置
type ServiceConfig struct {
	Provider string            `yaml:"provider" json:"provider"`
	Service  string            `yaml:"service" json:"service"`
	URL      string            `yaml:"url" json:"url"`
	Method   string            `yaml:"method" json:"method"`
	Headers  map[string]string `yaml:"headers" json:"headers"`
	Body     string            `yaml:"body" json:"body"`

	// SuccessContains 可选：响应体需包含的关键字，用于判定请求语义是否成功
	SuccessContains string `yaml:"success_contains" json:"success_contains"`

	// 解析后的“慢请求”阈值（来自全局配置），用于黄灯判定
	SlowLatencyDuration time.Duration `yaml:"-" json:"-"`

	APIKey string `yaml:"api_key" json:"-"` // 不返回给前端
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

		// Method 枚举检查
		validMethods := map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true}
		if !validMethods[strings.ToUpper(m.Method)] {
			return fmt.Errorf("monitor[%d]: method '%s' 无效，必须是 GET/POST/PUT/DELETE/PATCH 之一", i, m.Method)
		}

		// 唯一性检查
		key := m.Provider + "/" + m.Service
		if seen[key] {
			return fmt.Errorf("重复的监控项: provider=%s, service=%s", m.Provider, m.Service)
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

	// 将全局慢请求阈值下发到每个监控项
	for i := range c.Monitors {
		if c.Monitors[i].SlowLatencyDuration == 0 {
			c.Monitors[i].SlowLatencyDuration = c.SlowLatencyDuration
		}
	}

	return nil
}

// ApplyEnvOverrides 应用环境变量覆盖
// 格式：MONITOR_<PROVIDER>_<SERVICE>_API_KEY
func (c *AppConfig) ApplyEnvOverrides() {
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

// Clone 深拷贝配置（用于热更新回滚）
func (c *AppConfig) Clone() *AppConfig {
	clone := &AppConfig{
		Monitors: make([]ServiceConfig, len(c.Monitors)),
	}
	copy(clone.Monitors, c.Monitors)
	return clone
}
