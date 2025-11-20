package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Loader 配置加载器
type Loader struct {
	currentConfig *AppConfig
}

// NewLoader 创建配置加载器
func NewLoader() *Loader {
	return &Loader{}
}

// Load 加载并验证配置文件
func (l *Loader) Load(filename string) (*AppConfig, error) {
	// 读取文件
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	// 解析 YAML
	var cfg AppConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	configDir := filepath.Dir(absPath)

	// 验证配置
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	// 应用环境变量覆盖
	cfg.ApplyEnvOverrides()

	// 解析 body include
	if err := cfg.ResolveBodyIncludes(configDir); err != nil {
		return nil, err
	}

	// 规范化配置（填充默认值等）
	if err := cfg.Normalize(); err != nil {
		return nil, fmt.Errorf("配置规范化失败: %w", err)
	}

	// 处理占位符
	for i := range cfg.Monitors {
		cfg.Monitors[i].ProcessPlaceholders()
	}

	l.currentConfig = &cfg
	return &cfg, nil
}

// LoadOrRollback 加载配置，失败时保持旧配置
func (l *Loader) LoadOrRollback(filename string) (*AppConfig, error) {
	newConfig, err := l.Load(filename)
	if err != nil {
		// 返回错误但保持旧配置
		if l.currentConfig != nil {
			return l.currentConfig, fmt.Errorf("配置加载失败，保持旧配置: %w", err)
		}
		return nil, err
	}
	return newConfig, nil
}

// GetCurrent 获取当前配置
func (l *Loader) GetCurrent() *AppConfig {
	return l.currentConfig
}
