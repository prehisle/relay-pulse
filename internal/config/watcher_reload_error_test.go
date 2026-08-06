package config

import (
	"os"
	"path/filepath"
	"testing"
)

const watcherTestConfig = `monitors:
  - provider: acme
    service: cc
    channel: vip
    model: Haiku
    base_url: https://a.example.com
    method: POST
`

// newTestWatcher 直接构造 Watcher（不起 fsnotify）：reload() 只依赖 loader/filename
// 与两个回调，直连调用它可确定性地覆盖失败分支，无需等待文件系统事件。
func newTestWatcher(filename string, onReload func(*AppConfig), onReloadError func(error)) *Watcher {
	return &Watcher{
		loader:        NewLoader(),
		filename:      filename,
		onReload:      onReload,
		onReloadError: onReloadError,
		debounceTime:  0,
	}
}

// writeWatcherConfig 写一份 config.yaml 到临时目录并返回路径。
func writeWatcherConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestWatcherReload_FailureInvokesErrorCallback 是本轮的核心回归：热更新因配置加载/
// 校验失败而保留旧配置时（如 model_id 重复），必须通知失败回调——此前这条路径只打一行
// 日志，/ready 完全看不到，事故要靠人翻日志才能发现。
func TestWatcherReload_FailureInvokesErrorCallback(t *testing.T) {
	path := writeWatcherConfig(t, "monitors: [oops\n") // 畸形 YAML：解析必失败

	var gotErr error
	errCalls := 0
	reloadCalls := 0
	w := newTestWatcher(path,
		func(*AppConfig) { reloadCalls++ },
		func(err error) { errCalls++; gotErr = err },
	)

	w.reload()

	if errCalls != 1 {
		t.Fatalf("onReloadError 调用次数 = %d, want 1", errCalls)
	}
	if gotErr == nil {
		t.Error("onReloadError 应收到非 nil error")
	}
	if reloadCalls != 0 {
		t.Errorf("加载失败时不得触发 onReload，调用了 %d 次", reloadCalls)
	}
}

// TestWatcherReload_SuccessDoesNotInvokeErrorCallback 锁死失败回调不会在成功路径误报——
// 否则 /ready 会长期挂着一条并不存在的热更新故障。
func TestWatcherReload_SuccessDoesNotInvokeErrorCallback(t *testing.T) {
	path := writeWatcherConfig(t, watcherTestConfig)

	errCalls := 0
	reloadCalls := 0
	w := newTestWatcher(path,
		func(*AppConfig) { reloadCalls++ },
		func(error) { errCalls++ },
	)

	w.reload()

	if reloadCalls != 1 {
		t.Fatalf("onReload 调用次数 = %d, want 1", reloadCalls)
	}
	if errCalls != 0 {
		t.Errorf("成功热更新不得触发 onReloadError，调用了 %d 次", errCalls)
	}
}

// TestWatcherReload_NilErrorCallbackIsSafe 未注入失败回调的部署（含既有测试与
// 私有部署路径）必须照常工作，不能 panic。
func TestWatcherReload_NilErrorCallbackIsSafe(t *testing.T) {
	path := writeWatcherConfig(t, "monitors: [oops\n")

	w := newTestWatcher(path, nil, nil)

	w.reload() // 不 panic 即通过
}

// TestSetOnReloadError_Wires 验证 setter 确实把回调接到 reload 失败分支上
// （生产注入路径是 cmd/server/main.go 在 Start 之前调用它）。
func TestSetOnReloadError_Wires(t *testing.T) {
	path := writeWatcherConfig(t, "monitors: [oops\n")

	called := false
	w := newTestWatcher(path, nil, nil)
	w.SetOnReloadError(func(error) { called = true })

	w.reload()

	if !called {
		t.Error("SetOnReloadError 注入的回调未被调用")
	}
}
