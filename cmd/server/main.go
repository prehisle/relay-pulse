package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"monitor/internal/announcements"
	"monitor/internal/api"
	"monitor/internal/apikey"
	"monitor/internal/automove"
	"monitor/internal/buildinfo"
	"monitor/internal/change"
	"monitor/internal/config"
	"monitor/internal/events"
	"monitor/internal/identity"
	"monitor/internal/logger"
	"monitor/internal/onboarding"
	"monitor/internal/probe"
	"monitor/internal/reloadstatus"
	"monitor/internal/rpdiag"
	"monitor/internal/scheduler"
	"monitor/internal/storage"
)

// errConfigReloadFailed 是热更新加载失败时暴露到 /ready 的**固定脱敏文案**。
// 原始 loader 错误可能含 yaml 路径与解析细节，而 /ready 无鉴权，故只把它写进服务端日志；
// 端点侧只回答"热更新自何时起、失败了几次"，定位细节去日志里取。
// （运行时 model_id 闸的错误只含 provider/service/channel/model + 固定文案，已知可公开，
// 仍按原样透传。）
var errConfigReloadFailed = errors.New("配置热更新失败，保持旧配置；详情见服务端日志")

// buildModelIDMappings 从配置构建 model_id 回填映射（含已禁用监测项——其历史也需重键；跳过未填 model_id 的行）
func buildModelIDMappings(monitors []config.ServiceConfig) []storage.ModelIDMigrationMapping {
	mappings := make([]storage.ModelIDMigrationMapping, 0, len(monitors))
	for _, m := range monitors {
		if m.ModelID == "" {
			continue
		}
		mappings = append(mappings, storage.ModelIDMigrationMapping{
			Provider: m.Provider,
			Service:  m.Service,
			Channel:  m.Channel,
			Model:    m.Model,
			ModelID:  m.ModelID,
		})
	}
	return mappings
}

// buildChannelMigrationMappings 从配置构建 channel 迁移映射（同一 provider+service 取第一个非空 channel）
func buildChannelMigrationMappings(monitors []config.ServiceConfig) []storage.ChannelMigrationMapping {
	seen := make(map[string]bool)
	mappings := make([]storage.ChannelMigrationMapping, 0, len(monitors))

	for _, monitor := range monitors {
		// 跳过已禁用的监测项
		if monitor.Disabled {
			continue
		}
		// 跳过空 channel
		if monitor.Channel == "" {
			continue
		}

		key := monitor.Provider + "|" + monitor.Service
		if seen[key] {
			continue
		}
		seen[key] = true

		mappings = append(mappings, storage.ChannelMigrationMapping{
			Provider: monitor.Provider,
			Service:  monitor.Service,
			Channel:  monitor.Channel,
		})
	}

	return mappings
}

// resolveConfigDir 解析配置文件所在目录的绝对路径
func resolveConfigDir(configFile string) string {
	configDir := filepath.Dir(configFile)
	if configDir == "" || configDir == "." {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
	}
	return configDir
}

// initProbeTemplates 初始化 probe 包的模板注册表
func initProbeTemplates(configFile string) error {
	dir := filepath.Join(resolveConfigDir(configFile), "templates")
	probe.SetTemplatesDir(dir)
	return probe.InitTemplates(dir)
}

// announcementsConfigChanged 检测公告配置是否需要重建 Service
func announcementsConfigChanged(oldCfg, newCfg *config.AppConfig) bool {
	if oldCfg == nil || newCfg == nil {
		return true
	}
	oEnabled, nEnabled := oldCfg.Announcements.IsEnabled(), newCfg.Announcements.IsEnabled()
	if oEnabled != nEnabled {
		return true
	}
	if !oEnabled && !nEnabled {
		return false
	}
	o, n := oldCfg.Announcements, newCfg.Announcements
	return o.Owner != n.Owner ||
		o.Repo != n.Repo ||
		o.CategoryName != n.CategoryName ||
		o.PollIntervalDuration != n.PollIntervalDuration ||
		o.WindowHours != n.WindowHours ||
		o.MaxItems != n.MaxItems ||
		o.APIMaxAge != n.APIMaxAge ||
		oldCfg.GitHub.Token != newCfg.GitHub.Token ||
		oldCfg.GitHub.Proxy != newCfg.GitHub.Proxy ||
		oldCfg.GitHub.TimeoutDuration != newCfg.GitHub.TimeoutDuration
}

func main() {
	// 打印版本信息
	logger.Info("main", "Relay Pulse Monitor 启动",
		"version", buildinfo.GetVersion(),
		"git_commit", buildinfo.GetGitCommit(),
		"build_time", buildinfo.GetBuildTime())

	// 配置文件路径
	configFile := "config.yaml"
	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	// 创建配置加载器
	loader := config.NewLoader()

	// 初始加载配置
	cfg, err := loader.Load(configFile)
	if err != nil {
		logger.Error("main", "无法加载配置文件", "error", err)
		os.Exit(1)
	}

	// fail-closed：运行时所有监测行必须有 model_id（展示读已按 model_id；缺 id = 历史不可见）
	if err := config.CheckRuntimeModelIDs(cfg.Monitors); err != nil {
		logger.Error("main", "配置缺少 model_id，启动中止", "error", err)
		os.Exit(1)
	}

	logger.Info("main", "配置加载完成",
		"monitors", len(cfg.Monitors),
		"interval", cfg.Interval,
		"max_concurrency", cfg.MaxConcurrency,
		"stagger_probes", cfg.StaggerProbes,
		"slow_latency", cfg.SlowLatency,
		"degraded_weight", cfg.DegradedWeight,
	)

	// 初始化存储（支持 SQLite 和 PostgreSQL）
	store, err := storage.New(&cfg.Storage)
	if err != nil {
		logger.Error("main", "初始化存储失败", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Init(); err != nil {
		logger.Error("main", "初始化数据库失败", "error", err)
		os.Exit(1)
	}

	// 自动迁移旧数据的 channel
	if err := store.MigrateChannelData(buildChannelMigrationMappings(cfg.Monitors)); err != nil {
		logger.Warn("main", "channel 数据迁移失败", "error", err)
	}

	// 回填 legacy probe_history 行的 model_id（幂等；歧义或 DB 错误均中止启动）
	if err := store.BackfillProbeHistoryModelIDs(buildModelIDMappings(cfg.Monitors)); err != nil {
		logger.Error("main", "probe_history model_id 回填失败，启动中止", "error", err)
		os.Exit(1)
	}

	storageType := cfg.Storage.Type
	if storageType == "" {
		storageType = "sqlite"
	}
	logger.Info("main", "存储已就绪", "type", storageType)

	// 创建上下文（用于优雅关闭）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动历史数据清理任务
	var cleaner *storage.Cleaner
	if cfg.Storage.Retention.IsEnabled() {
		cleaner = storage.NewCleaner(store, &cfg.Storage.Retention)
		go cleaner.Start(ctx)
		logger.Info("main", "历史数据清理任务已启动",
			"retention_days", cfg.Storage.Retention.Days,
			"cleanup_interval", cfg.Storage.Retention.CleanupInterval)
	}

	// 启动历史数据归档任务（仅 PostgreSQL 支持）
	var archiver *storage.Archiver
	if cfg.Storage.Archive.IsEnabled() {
		// 检查存储是否支持归档（仅 PostgreSQL 支持）
		if _, ok := store.(storage.ArchiveStorage); !ok {
			logger.Warn("main", "归档功能已启用但当前存储不支持（仅 PostgreSQL 支持），归档任务将不会执行",
				"storage_type", cfg.Storage.Type)
		} else {
			archiver = storage.NewArchiver(store, &cfg.Storage.Archive)
			go archiver.Start(ctx)
			logger.Info("main", "历史数据归档任务已启动",
				"archive_days", cfg.Storage.Archive.ArchiveDays,
				"backfill_days", cfg.Storage.Archive.BackfillDays,
				"output_dir", cfg.Storage.Archive.OutputDir,
				"format", cfg.Storage.Archive.Format)
		}
	}

	// 创建自动移板服务（始终创建，内部根据 enabled 决定是否实际评估）
	// 先从 DB 恢复持久化 override，再基于历史做首次评估，
	// 避免重启后丢失 sticky cold 状态，导致 scheduler 首轮调度到本应停探的通道。
	// 单例 rpdiag 客户端：automove（质量信号）与 API handler（质量列渲染）共用同一实例。
	// 必须在 Restore()/Evaluate() 之前构造并注入，否则重启后首次评估拿不到质量信号。
	// NewClientFromEnv 在未启用（opt-in env 未设）时返回 nil；此处以显式 nil 守卫避免
	// 把 typed-nil 指针塞进 qualitySource 接口（那会让接口非 nil，误判质量源已就绪）。
	rpdiagClient := rpdiag.NewClientFromEnv()
	autoMover := automove.NewService(store, cfg)
	if rpdiagClient != nil {
		autoMover.SetQualitySource(rpdiagClient)
	}
	if err := autoMover.Restore(); err != nil {
		logger.Error("main", "恢复自动移板 override 失败", "error", err)
		os.Exit(1)
	}
	autoMover.Evaluate(ctx)

	// 创建调度器（支持通过 config.yaml 配置 interval）
	interval := cfg.IntervalDuration
	if interval <= 0 {
		interval = time.Minute
	}
	userIDMgr := identity.NewUserIDManager()
	sched := scheduler.NewScheduler(store, interval, userIDMgr)
	sched.SetAutoMover(autoMover)

	// 创建事件服务（如果启用）
	eventSvc, err := events.NewService(events.ServiceConfig{
		DetectorConfig: events.DetectorConfig{
			DownThreshold: cfg.Events.DownThreshold,
			UpThreshold:   cfg.Events.UpThreshold,
		},
		ChannelDetectorConfig: events.ChannelDetectorConfig{
			DownThreshold: cfg.Events.ChannelDownThreshold,
		},
		Mode:             cfg.Events.Mode,
		ChannelCountMode: cfg.Events.ChannelCountMode,
		Enabled:          cfg.Events.Enabled,
	}, store)
	if err != nil {
		logger.Error("main", "创建事件服务失败", "error", err)
		os.Exit(1)
	}
	if eventSvc.IsEnabled() {
		sched.SetEventService(eventSvc)
		logger.Info("main", "事件服务已启用",
			"mode", eventSvc.GetMode(),
			"down_threshold", cfg.Events.DownThreshold,
			"up_threshold", cfg.Events.UpThreshold,
			"channel_down_threshold", cfg.Events.ChannelDownThreshold,
			"channel_count_mode", cfg.Events.ChannelCountMode)
	}

	sched.Start(ctx, cfg)

	// 初始化 monitors.d/ 目录和 MonitorStore
	monitorsDirPath := filepath.Join(resolveConfigDir(configFile), config.MonitorsDirName)
	if err := os.MkdirAll(monitorsDirPath, 0755); err != nil {
		logger.Warn("main", "创建 monitors.d 目录失败", "error", err)
	}
	monitorStore := config.NewMonitorStore(monitorsDirPath)

	// 创建API服务器
	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8080"
	}
	// reloadRecorder 记录热更新被 fail-closed 闸静默跳过的状态，经 /ready body 信息化暴露。
	// 生产恒非 nil，注入 api 与热更新回调，让「admin 保存 200 但运行态没变」这类静默事故留下可查痕迹。
	reloadRecorder := reloadstatus.New()
	server := api.NewServer(store, cfg, httpPort, autoMover, rpdiagClient, reloadRecorder)
	server.GetHandler().SetMonitorStore(monitorStore)

	// runtimeMu 保护热更新回调与关闭序列之间对 mutable 组件实例的并发访问
	var runtimeMu sync.Mutex
	runtimeCfg := cfg

	// 连接 override 变更回调：当 autoMover 产生新 cold override 时通知 scheduler/events 刷新
	autoMover.SetOnOverrideChange(func() {
		runtimeMu.Lock()
		defer runtimeMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		sched.UpdateConfig(runtimeCfg)
	})
	autoMover.Start(ctx)
	if cfg.Boards.Enabled && cfg.Boards.AutoMove.Enabled {
		logger.Info("main", "自动移板服务已启用",
			"threshold_cold", cfg.Boards.AutoMove.ThresholdCold,
			"threshold_down", cfg.Boards.AutoMove.ThresholdDown,
			"threshold_up", cfg.Boards.AutoMove.ThresholdUp,
			"check_interval", cfg.Boards.AutoMove.CheckInterval,
			"min_probes", cfg.Boards.AutoMove.MinProbes)
	}

	// 提前注册公告 API 路由（使用支持动态替换的 Handler），避免热启用后无路由入口
	announcementsHandler := announcements.NewHandler(nil)
	server.RegisterAnnouncementsHandler(announcementsHandler.GetAnnouncements)

	// announcementsAppliedCfg 初始为 nil：仅在服务创建成功或明确禁用时设置，
	// 避免启动失败时标记为"已应用"导致后续热更新不再重试
	var announcementsAppliedCfg *config.AppConfig

	// 初始化内联探测器（供收录测试和管理后台使用）
	if err := initProbeTemplates(configFile); err != nil {
		logger.Warn("main", "初始化 probe 模板失败（非致命）", "error", err)
	}
	inlineProber := probe.NewInlineProber(5, userIDMgr)
	probeLimiter := probe.NewIPLimiter(10, 10) // 每 IP 每分钟 10 次
	server.GetHandler().SetInlineProber(inlineProber)
	server.GetHandler().SetProbeLimiter(probeLimiter)
	logger.Info("main", "内联探测器已初始化")

	// 初始化自助收录服务（如果启用）
	var onboardingSvc *onboarding.Service
	if cfg.Onboarding.Enabled {
		var obStore onboarding.Store
		switch s := store.(type) {
		case *storage.SQLiteStorage:
			sqlStore := onboarding.NewSQLStore(s.SqlDB())
			if err := sqlStore.InitTable(ctx); err != nil {
				logger.Error("main", "初始化 onboarding 表失败", "error", err)
				os.Exit(1)
			}
			obStore = sqlStore
		case *storage.PostgresStorage:
			pgxStore := onboarding.NewPgxStore(s.PgxPool())
			if err := pgxStore.InitTable(ctx); err != nil {
				logger.Error("main", "初始化 onboarding 表失败", "error", err)
				os.Exit(1)
			}
			obStore = pgxStore
		default:
			logger.Error("main", "不支持的存储类型，onboarding 功能不可用")
			os.Exit(1)
		}

		configDir := resolveConfigDir(configFile)
		var err error
		onboardingSvc, err = onboarding.NewService(obStore, &cfg.Onboarding, configDir)
		if err != nil {
			logger.Error("main", "创建自助收录服务失败", "error", err)
			os.Exit(1)
		}
		onboardingSvc.SetMonitorStore(monitorStore)
		onboardingSvc.SetConfigMonitorCheck(func(provider, service, channel string) bool {
			runtimeMu.Lock()
			defer runtimeMu.Unlock()
			targetKey := config.MonitorFileKeyFromPSC(provider, service, channel)
			for _, m := range runtimeCfg.Monitors {
				if config.MonitorFileKeyFromPSC(m.Provider, m.Service, m.Channel) == targetKey {
					return true
				}
			}
			return false
		})
		server.GetHandler().SetOnboardingService(onboardingSvc)
		logger.Info("main", "自助收录功能已启用",
			"max_per_ip_per_day", cfg.Onboarding.MaxPerIPPerDay,
			"proof_ttl", cfg.Onboarding.ProofTTL)
	}

	// 初始化变更请求服务（如果启用）
	var changeSvc *change.Service
	if cfg.ChangeRequests.Enabled {
		// 需要 onboarding 的 encryption_key 和 proof_secret
		if cfg.Onboarding.EncryptionKey == "" || cfg.Onboarding.ProofSecret == "" {
			logger.Error("main", "变更请求需要 onboarding.encryption_key 和 onboarding.proof_secret")
			os.Exit(1)
		}

		cipher, err := apikey.NewKeyCipher(cfg.Onboarding.EncryptionKey)
		if err != nil {
			logger.Error("main", "创建 API Key 加密器失败", "error", err)
			os.Exit(1)
		}
		// 用途限定为 change：与 onboarding 共用同一份 proof_secret，但 proof 不得互通
		proofIssuer := apikey.NewProofIssuerForAudience(
			cfg.Onboarding.ProofSecret, change.ProofAudience, cfg.Onboarding.ProofTTLDuration)

		var chStore change.Store
		switch s := store.(type) {
		case *storage.SQLiteStorage:
			sqlStore := change.NewSQLStore(s.SqlDB())
			if err := sqlStore.InitTable(ctx); err != nil {
				logger.Error("main", "初始化 change_requests 表失败", "error", err)
				os.Exit(1)
			}
			chStore = sqlStore
		case *storage.PostgresStorage:
			pgxStore := change.NewPgxStore(s.PgxPool())
			if err := pgxStore.InitTable(ctx); err != nil {
				logger.Error("main", "初始化 change_requests 表失败", "error", err)
				os.Exit(1)
			}
			chStore = pgxStore
		default:
			logger.Error("main", "不支持的存储类型，变更请求功能不可用")
			os.Exit(1)
		}

		changeSvc = change.NewService(chStore, cipher, proofIssuer, &cfg.ChangeRequests)
		changeSvc.SetMonitorStore(monitorStore)
		changeSvc.UpdateConfig(&cfg.ChangeRequests, cfg.Monitors)
		server.GetHandler().SetChangeService(changeSvc)
		logger.Info("main", "变更请求功能已启用",
			"max_per_ip_per_day", cfg.ChangeRequests.MaxPerIPPerDay)
	}

	// 初始化公告服务（如果启用）
	var announcementsSvc *announcements.Service
	if cfg.Announcements.IsEnabled() {
		var err error
		announcementsSvc, err = announcements.NewService(cfg.Announcements, cfg.GitHub)
		if err != nil {
			logger.Error("main", "创建公告服务失败", "error", err)
			// 公告服务失败不影响主服务启动，仅警告
			// 注意：不设置 announcementsAppliedCfg，下次热更新将重试创建
		} else {
			announcementsHandler.SetService(announcementsSvc)
			announcementsSvc.Start(ctx)
			announcementsAppliedCfg = cfg
			logger.Info("main", "公告服务已启用",
				"owner", cfg.Announcements.Owner,
				"repo", cfg.Announcements.Repo,
				"category", cfg.Announcements.CategoryName,
				"poll_interval", cfg.Announcements.PollInterval,
				"window_hours", cfg.Announcements.WindowHours)
		}
	} else {
		announcementsAppliedCfg = cfg // 明确禁用也标记为已应用
	}

	// 启动配置监听器（热更新）
	watcher, err := config.NewWatcher(loader, configFile, func(newCfg *config.AppConfig) {
		// 关闭中不再处理热更新
		if ctx.Err() != nil {
			return
		}

		// 序列化热更新回调，防止与关闭序列竞态
		runtimeMu.Lock()
		defer runtimeMu.Unlock()

		if ctx.Err() != nil {
			return
		}

		// fail-closed：热更新带入缺 model_id 的监测行则跳过本次重载（保留旧配置），避免线上历史不可见
		if err := config.CheckRuntimeModelIDs(newCfg.Monitors); err != nil {
			logger.Error("main", "热更新配置缺少 model_id，跳过本次重载", "error", err)
			reloadRecorder.RecordSkip(err)
			return
		}

		// === 已有热更新支持的组件 ===
		// 顺序重要：先更新 autoMover（可能清除/生成 cold override），
		// 再更新 scheduler（基于最新 override 重建任务堆），最后更新 server
		runtimeCfg = newCfg
		server.UpdateConfig(newCfg)
		autoMover.UpdateConfig(newCfg)
		sched.UpdateConfig(newCfg)

		// 重新运行 channel 迁移（支持运行时添加 channel）
		if err := store.MigrateChannelData(buildChannelMigrationMappings(newCfg.Monitors)); err != nil {
			logger.Warn("main", "热更新时 channel 迁移失败", "error", err)
		}
		// 注意：不再调用 TriggerNow()，rebuildTasks 已安排错峰首次执行
		// 避免与 rebuildTasks 的首轮调度产生竞态导致重复探测

		// === Cleaner: ApplyConfig（在线更新配置 + 唤醒调度） ===
		switch {
		case newCfg.Storage.Retention.IsEnabled() && cleaner == nil:
			cleaner = storage.NewCleaner(store, &newCfg.Storage.Retention)
			go cleaner.Start(ctx)
			logger.Info("main", "历史数据清理任务已在热更新后启动",
				"retention_days", newCfg.Storage.Retention.Days,
				"cleanup_interval", newCfg.Storage.Retention.CleanupInterval)
		case newCfg.Storage.Retention.IsEnabled() && cleaner != nil:
			cleaner.UpdateRetentionConfig(&newCfg.Storage.Retention)
		case !newCfg.Storage.Retention.IsEnabled() && cleaner != nil:
			cleaner.Stop()
			cleaner = nil
			logger.Info("main", "历史数据清理任务已在热更新后停用")
		}

		// === Archiver: ApplyConfig（在线更新配置 + 唤醒调度） ===
		switch {
		case newCfg.Storage.Archive.IsEnabled() && archiver == nil:
			if _, ok := store.(storage.ArchiveStorage); !ok {
				logger.Warn("main", "热更新后启用了归档功能，但当前存储不支持（仅 PostgreSQL 支持）",
					"storage_type", newCfg.Storage.Type)
			} else {
				archiver = storage.NewArchiver(store, &newCfg.Storage.Archive)
				go archiver.Start(ctx)
				logger.Info("main", "历史数据归档任务已在热更新后启动",
					"archive_days", newCfg.Storage.Archive.ArchiveDays,
					"backfill_days", newCfg.Storage.Archive.BackfillDays,
					"output_dir", newCfg.Storage.Archive.OutputDir,
					"format", newCfg.Storage.Archive.Format)
			}
		case newCfg.Storage.Archive.IsEnabled() && archiver != nil:
			archiver.UpdateArchiveConfig(&newCfg.Storage.Archive)
		case !newCfg.Storage.Archive.IsEnabled() && archiver != nil:
			archiver.Stop()
			archiver = nil
			logger.Info("main", "历史数据归档任务已在热更新后停用")
		}

		// === Probe: 刷新模板变体注册表（模板文件可能已新增/删除） ===
		if err := initProbeTemplates(configFile); err != nil {
			logger.Warn("main", "热更新 probe 模板失败", "error", err)
		}

		// === ChangeRequests: 热更新认证索引 ===
		if changeSvc != nil && newCfg.ChangeRequests.Enabled {
			changeSvc.UpdateConfig(&newCfg.ChangeRequests, newCfg.Monitors)
		}

		// === Announcements: RecreateOnChange（stop + 重建） ===
		if announcementsConfigChanged(announcementsAppliedCfg, newCfg) {
			if !newCfg.Announcements.IsEnabled() {
				announcementsHandler.SetService(nil)
				if announcementsSvc != nil {
					announcementsSvc.Stop()
					announcementsSvc = nil
					logger.Info("main", "公告服务已在热更新后停用")
				}
				announcementsAppliedCfg = newCfg
			} else {
				newSvc, err := announcements.NewService(newCfg.Announcements, newCfg.GitHub)
				if err != nil {
					logger.Error("main", "热更新创建公告服务失败，继续使用旧实例", "error", err)
				} else {
					// 先启动新实例，再切换引用，最后停止旧实例（减少不可用窗口）
					newSvc.Start(ctx)
					oldSvc := announcementsSvc
					announcementsSvc = newSvc
					announcementsHandler.SetService(newSvc)
					if oldSvc != nil {
						oldSvc.Stop()
					}
					announcementsAppliedCfg = newCfg
					logger.Info("main", "公告服务配置热更新已生效",
						"owner", newCfg.Announcements.Owner,
						"repo", newCfg.Announcements.Repo,
						"category", newCfg.Announcements.CategoryName,
						"poll_interval", newCfg.Announcements.PollInterval,
						"window_hours", newCfg.Announcements.WindowHours)
				}
			}
		}
	})

	if err != nil {
		logger.Warn("main", "配置监听器创建失败，热更新功能不可用", "error", err)
	} else {
		// 配置加载/校验失败（坏 yaml、model_id 重复等）会保留旧配置继续服务，此前只有日志。
		// 与下方运行时闸共用同一 recorder，使 /ready 的 config_reload 覆盖两条静默失败路径。
		watcher.SetOnReloadError(func(error) {
			reloadRecorder.RecordSkip(errConfigReloadFailed)
		})

		if err := watcher.Start(ctx); err != nil {
			logger.Warn("main", "配置监听器启动失败，热更新功能不可用", "error", err)
		} else {
			logger.Info("main", "配置热更新已启用")
		}
	}

	// 监听中断信号（优雅关闭）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动HTTP服务器（阻塞）
	go func() {
		if err := server.Start(); err != nil {
			logger.Error("main", "HTTP服务器错误", "error", err)
			cancel()
			// 向信号通道发送信号，确保进程退出
			sigChan <- syscall.SIGTERM
		}
	}()

	// 等待中断信号
	<-sigChan
	logger.Info("main", "收到关闭信号，正在优雅退出")

	// 取消上下文
	cancel()

	// 停止调度器
	sched.Stop()

	// 停止自动移板服务
	autoMover.Stop()
	logger.Info("main", "自动移板服务已关闭")

	// 在 runtimeMu 保护下捕获当前实例引用，防止与热更新回调竞态
	runtimeMu.Lock()
	currentAnnouncementsSvc := announcementsSvc
	announcementsSvc = nil
	currentCleaner := cleaner
	cleaner = nil
	currentArchiver := archiver
	archiver = nil
	announcementsHandler.SetService(nil)
	runtimeMu.Unlock()

	// 停止公告服务（如果启用）
	if currentAnnouncementsSvc != nil {
		currentAnnouncementsSvc.Stop()
		logger.Info("main", "公告服务已关闭")
	}

	// 停止清理和归档任务
	if currentCleaner != nil {
		currentCleaner.Stop()
		logger.Info("main", "历史数据清理任务已关闭")
	}
	if currentArchiver != nil {
		currentArchiver.Stop()
		logger.Info("main", "历史数据归档任务已关闭")
	}

	// 停止HTTP服务器
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Stop(shutdownCtx); err != nil {
		logger.Warn("main", "HTTP服务器关闭错误", "error", err)
	}

	logger.Info("main", "服务已安全退出")
}
