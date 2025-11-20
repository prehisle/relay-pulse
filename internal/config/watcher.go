package config

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher 配置文件监听器
type Watcher struct {
	loader       *Loader
	filename     string
	watcher      *fsnotify.Watcher
	onReload     func(*AppConfig)
	debounceTime time.Duration
}

// NewWatcher 创建配置监听器
func NewWatcher(loader *Loader, filename string, onReload func(*AppConfig)) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		loader:       loader,
		filename:     filename,
		watcher:      watcher,
		onReload:     onReload,
		debounceTime: 200 * time.Millisecond, // 防抖延迟
	}, nil
}

// Start 启动监听（监听父目录以兼容不同编辑器）
func (w *Watcher) Start(ctx context.Context) error {
	// 监听父目录而非文件本身，避免编辑器 rename 导致监听失效
	dir := filepath.Dir(w.filename)
	if err := w.watcher.Add(dir); err != nil {
		return err
	}

	// data 目录（用于 body include JSON）
	dataDir := filepath.Clean(filepath.Join(dir, "data"))

	log.Printf("[Config] 开始监听配置文件: %s (监听目录: %s)", w.filename, dir)

	go func() {
		var debounceTimer *time.Timer
		for {
			select {
			case <-ctx.Done():
				log.Println("[Config] 配置监听器已停止")
				w.watcher.Close()
				return

			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}

				// 只关心目标配置文件和 data/ 目录下 JSON 的写入和创建事件
				isConfigFile := event.Name == w.filename
				isDataFile := strings.HasPrefix(filepath.Clean(event.Name), dataDir+string(filepath.Separator))
				if !isConfigFile && !isDataFile {
					continue
				}

				if event.Op&fsnotify.Write == fsnotify.Write ||
					event.Op&fsnotify.Create == fsnotify.Create {
					// 防抖：延迟执行，避免编辑器多次写入
					if debounceTimer != nil {
						debounceTimer.Stop()
					}

					debounceTimer = time.AfterFunc(w.debounceTime, func() {
						log.Println("[Config] 检测到配置文件变更，正在重载...")
						w.reload()
					})
				}

			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				// 不使用 log.Fatal，只记录错误
				log.Printf("[Config] 监听错误: %v", err)
			}
		}
	}()

	return nil
}

// reload 重新加载配置
func (w *Watcher) reload() {
	newConfig, err := w.loader.LoadOrRollback(w.filename)
	if err != nil {
		log.Printf("[Config] 重载失败: %v", err)
		return
	}

	log.Printf("[Config] 热更新成功！已加载 %d 个监控任务", len(newConfig.Monitors))

	// 回调通知
	if w.onReload != nil {
		w.onReload(newConfig)
	}
}

// Stop 停止监听
func (w *Watcher) Stop() error {
	return w.watcher.Close()
}
