package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"monitor/internal/api"
	"monitor/internal/config"
	"monitor/internal/scheduler"
	"monitor/internal/storage"
)

func main() {
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
		log.Fatalf("❌ 无法加载配置文件: %v", err)
	}

	log.Printf("✅ 已加载 %d 个监控任务", len(cfg.Monitors))

	// 初始化存储
	store, err := storage.NewSQLiteStorage("monitor.db")
	if err != nil {
		log.Fatalf("❌ 初始化存储失败: %v", err)
	}
	defer store.Close()

	if err := store.Init(); err != nil {
		log.Fatalf("❌ 初始化数据库失败: %v", err)
	}

	log.Println("✅ SQLite 存储已就绪")

	// 创建上下文（用于优雅关闭）
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 创建调度器（支持通过 config.yaml 配置 interval）
	interval := cfg.IntervalDuration
	if interval <= 0 {
		interval = time.Minute
	}
	sched := scheduler.NewScheduler(store, interval)
	sched.Start(ctx, cfg)

	// 创建API服务器
	server := api.NewServer(store, cfg, "8080")

	// 启动配置监听器（热更新）
	watcher, err := config.NewWatcher(loader, configFile, func(newCfg *config.AppConfig) {
		// 配置热更新回调
		sched.UpdateConfig(newCfg)
		server.UpdateConfig(newCfg)
		// 立即触发一次巡检，确保新配置立即生效
		sched.TriggerNow()
	})

	if err != nil {
		log.Printf("⚠️  配置监听器创建失败: %v (热更新功能不可用)", err)
	} else {
		if err := watcher.Start(ctx); err != nil {
			log.Printf("⚠️  配置监听器启动失败: %v (热更新功能不可用)", err)
		} else {
			log.Printf("✅ 配置热更新已启用")
		}
	}

	// 启动定期清理任务（保留30天数据）
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.CleanOldRecords(30); err != nil {
					log.Printf("⚠️  清理旧记录失败: %v", err)
				}
			}
		}
	}()

	// 监听中断信号（优雅关闭）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 启动HTTP服务器（阻塞）
	go func() {
		if err := server.Start(); err != nil {
			log.Printf("❌ HTTP服务器错误: %v", err)
			cancel()
			// 向信号通道发送信号，确保进程退出
			sigChan <- syscall.SIGTERM
		}
	}()

	// 等待中断信号
	<-sigChan
	log.Println("\n⚠️  收到关闭信号，正在优雅退出...")

	// 取消上下文
	cancel()

	// 停止调度器
	sched.Stop()

	// 停止HTTP服务器
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Stop(shutdownCtx); err != nil {
		log.Printf("⚠️  HTTP服务器关闭错误: %v", err)
	}

	log.Println("👋 服务已安全退出")
}
