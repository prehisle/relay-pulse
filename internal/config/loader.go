package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"monitor/internal/logger"
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

	// 记录 config.yaml 内联监测行数量：mergeExternalMonitorSources 只把 monitors.d 追加到
	// 切片尾部，此后 validate/继承/normalize 均原地修改、不增删不重排，故 [0,inlineMonitorCount)
	// 恒为内联行——供末尾按此边界只给内联行补 model_id。
	inlineMonitorCount := len(cfg.Monitors)

	absPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件路径失败: %w", err)
	}
	configDir := filepath.Dir(absPath)

	// 合并 monitors.d/ 外部 monitor 源
	// 必须在 validate 之前合并，确保所有 monitors 走完整校验/继承/规范化流程
	if err := cfg.mergeExternalMonitorSources(configDir); err != nil {
		return nil, fmt.Errorf("合并外部 monitor 配置失败: %w", err)
	}

	// 验证配置
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	// 应用环境变量覆盖
	cfg.applyEnvOverrides()

	// 解析模板引用
	if err := cfg.resolveTemplates(configDir); err != nil {
		return nil, err
	}

	// 规范化配置（填充默认值等）
	if err := cfg.normalize(); err != nil {
		return nil, fmt.Errorf("配置规范化失败: %w", err)
	}

	// 给未显式写 model_id 的 config.yaml 内联监测行补齐确定性派生 id（仅内存，不改写文件）。
	// 官方 config.yaml.example 不含 model_id，若不补则被 CheckRuntimeModelIDs 运行时闸挡下、
	// 容器启动即崩——这是新手按 QUICKSTART 部署的必经路径。放在 normalize 之后，
	// 确保模板解析完成、按最终 PSCM 字段派生（model 可能来自模板；缺 model 时派生仍确定、
	// 唯一性由后置 validateModelIDs 兜底）。monitors.d 语义完全不动
	// （其缺 id 仍由运行时闸 fail-closed 提示走 backfillids，因它有写时补 id + 持久化流程）。
	for i := 0; i < inlineMonitorCount; i++ {
		if cfg.Monitors[i].ModelID == "" {
			cfg.Monitors[i].ModelID = deriveInlineModelID(cfg.Monitors[i])
		}
	}

	// validate() 早于派生执行、跳过了当时为空的 model_id；此处对派生结果重跑格式+全局唯一性
	// 校验，兜住派生 id 与既有 id（内联或 monitors.d、含手工填的 v5）的冲突。O(n) 扫描、无锁。
	if err := cfg.validateModelIDs(); err != nil {
		return nil, fmt.Errorf("内联 model_id 派生后校验失败: %w", err)
	}

	// Phase 1: 最终态校验仅告警，不阻断启动或热更新
	// 捕获 env 覆盖、模板注入、继承和 Normalize 之后可能遗留的不一致配置
	for _, warn := range cfg.validateFinal() {
		logger.Warn("config", "最终配置校验告警", "detail", warn.Error())
	}

	l.currentConfig = &cfg
	return &cfg, nil
}

// mergeExternalMonitorSources 合并 monitors.d/ 到主配置，并执行跨源 PSC 冲突检测。
func (cfg *AppConfig) mergeExternalMonitorSources(configDir string) error {
	dirMonitors, dirFiles, err := loadMonitorsDir(configDir)
	if err != nil {
		return fmt.Errorf("加载 %s 失败: %w", MonitorsDirName, err)
	}

	// config.yaml vs monitors.d/ PSC 冲突检测
	if err := detectPSCConflicts(cfg.Monitors, dirMonitors); err != nil {
		return err
	}

	if len(dirMonitors) > 0 {
		cfg.Monitors = append(cfg.Monitors, dirMonitors...)
		logger.Info("config", "已合并 monitors.d", "files", len(dirFiles), "monitors", len(dirMonitors))
	}

	return nil
}

// detectPSCConflicts 检测 config.yaml 与 monitors.d/ 之间的 PSC 冲突。
func detectPSCConflicts(staticMonitors, dirMonitors []ServiceConfig) error {
	staticKeys := CollectPSCKeys(staticMonitors)
	dirKeys := CollectPSCKeys(dirMonitors)

	var conflicts []string
	for key := range staticKeys {
		if _, ok := dirKeys[key]; ok {
			conflicts = append(conflicts, fmt.Sprintf("PSC %s 同时出现在 config.yaml 与 %s/", key, MonitorsDirName))
		}
	}

	if len(conflicts) == 0 {
		return nil
	}
	sort.Strings(conflicts)
	return fmt.Errorf("跨源 PSC 冲突:\n  %s", strings.Join(conflicts, "\n  "))
}

// CollectPSCKeys 从 monitor 列表收集去重后的 PSC key 集合（格式 "provider/service/channel"，小写）。
// 对子通道执行 parent 继承以填充 PSC 字段。
func CollectPSCKeys(monitors []ServiceConfig) map[string]struct{} {
	keys := make(map[string]struct{})
	if len(monitors) == 0 {
		return keys
	}

	// 复制一份执行 parent 继承，避免修改原始数据
	normalized := make([]ServiceConfig, len(monitors))
	copy(normalized, monitors)
	tmp := AppConfig{Monitors: normalized}
	_ = tmp.preprocessParentInheritance() // 容忍继承失败，后续 validate 会捕获

	for _, m := range tmp.Monitors {
		p := strings.TrimSpace(m.Provider)
		s := strings.TrimSpace(m.Service)
		c := strings.TrimSpace(m.Channel)
		if p == "" || s == "" || c == "" {
			continue
		}
		key := strings.ToLower(p) + "/" + strings.ToLower(s) + "/" + strings.ToLower(c)
		keys[key] = struct{}{}
	}
	return keys
}

// loadOrRollback 加载配置，失败时保持旧配置
func (l *Loader) loadOrRollback(filename string) (*AppConfig, error) {
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

// getCurrent 获取当前配置（仅包内使用）
func (l *Loader) getCurrent() *AppConfig {
	return l.currentConfig
}
