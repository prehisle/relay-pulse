package screenshot

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// 本文件只覆盖不需要真浏览器的部分：空闲回收判定、关闭时的信号量排空协议、生命周期。
// browser/pw 保持 nil 时 closeBrowser 会跳过真实关闭，其余状态流转与真实路径一致。

// markInitialized 伪造「浏览器已就绪」状态（browser/pw 留 nil）
func markInitialized(s *Service) {
	s.mu.Lock()
	s.initialized = true
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func isInitialized(s *Service) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized
}

// waitFor 轮询等待条件成立，超时返回 false
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return cond()
}

func TestNewService_Defaults(t *testing.T) {
	s := NewService("https://example.com/", time.Second, 0, 0)
	defer func() { _ = s.Close() }()

	if s.idleTimeout != defaultIdleTimeout {
		t.Fatalf("idleTimeout = %v, want %v", s.idleTimeout, defaultIdleTimeout)
	}
	if got := cap(s.sem); got != 3 {
		t.Fatalf("cap(sem) = %d, want 3", got)
	}
	if s.baseURL != "https://example.com" {
		t.Fatalf("baseURL = %q, 末尾斜杠应被剥掉", s.baseURL)
	}
}

func TestIdleReaper_ClosesIdleBrowser(t *testing.T) {
	s := NewService("https://example.com", time.Second, 2, 30*time.Millisecond)
	defer func() { _ = s.Close() }()

	markInitialized(s)

	if !waitFor(2*time.Second, func() bool { return !isInitialized(s) }) {
		t.Fatal("空闲超时后浏览器仍未被回收")
	}
	// 回收路径必须把占用的信号量槽还回去，否则后续截图会永远拿不到槽
	if got := len(s.sem); got != 0 {
		t.Fatalf("回收后 len(sem) = %d, want 0（信号量槽泄漏）", got)
	}
}

func TestIdleReaper_KeepsBusyBrowser(t *testing.T) {
	const idle = 40 * time.Millisecond
	s := NewService("https://example.com", time.Second, 2, idle)
	defer func() { _ = s.Close() }()

	markInitialized(s)

	// 持续活动期间不允许被回收
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s.touch()
				time.Sleep(idle / 8)
			}
		}
	}()

	time.Sleep(6 * idle)
	busyStillUp := isInitialized(s)
	close(stop)
	wg.Wait()

	if !busyStillUp {
		t.Fatal("持续有截图活动时浏览器被误回收")
	}
	// 活动停止后应当被回收，证明上面的「没被回收」不是因为 reaper 根本没跑
	if !waitFor(2*time.Second, func() bool { return !isInitialized(s) }) {
		t.Fatal("活动停止后浏览器仍未被回收")
	}
}

func TestCloseBrowser_WaitsForInflightCapture(t *testing.T) {
	s := NewService("https://example.com", time.Second, 1, time.Hour)
	defer func() { _ = s.Close() }()

	markInitialized(s)

	// 模拟一次在途截图：占住信号量槽
	s.sem <- struct{}{}

	done := make(chan struct{})
	go func() {
		_ = s.closeBrowser(false)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("在途截图未结束，关闭就返回了")
	case <-time.After(50 * time.Millisecond):
	}

	<-s.sem // 在途截图结束

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("在途截图结束后关闭仍未返回")
	}
	if isInitialized(s) {
		t.Fatal("关闭后 initialized 仍为 true")
	}
}

// 未初始化时也必须排空信号量：否则「已拿到槽、还没进 ensureInitialized」的请求
// 会在 Close 返回之后才把浏览器拉起来，进程退出时留下没人关的 chromium。
func TestCloseBrowser_DrainsEvenWhenNotInitialized(t *testing.T) {
	s := NewService("https://example.com", time.Second, 1, time.Hour)
	defer func() { _ = s.Close() }()

	s.sem <- struct{}{} // 请求已占槽，但尚未初始化

	done := make(chan struct{})
	go func() {
		_ = s.closeBrowser(false)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("未初始化时提前返回了，没有等待在途请求")
	case <-time.After(50 * time.Millisecond):
	}

	<-s.sem
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("槽位释放后关闭仍未返回")
	}
}

// 关闭失败后不许再起一个 chromium：旧进程可能还活着，再起一个就是内存翻倍。
func TestEnsureInitialized_RefusesAfterFailedClose(t *testing.T) {
	s := NewService("https://example.com", time.Second, 1, time.Hour)
	defer func() { _ = s.Close() }()

	s.mu.Lock()
	s.closeErr = errors.New("关不掉")
	s.mu.Unlock()

	err := s.ensureInitialized()
	if err == nil {
		t.Fatal("上次关闭失败后仍然允许重新启动浏览器")
	}
	if !errors.Is(err, s.closeErr) {
		t.Fatalf("错误未包住原始关闭失败: %v", err)
	}
	if isInitialized(s) {
		t.Fatal("被拒绝后 initialized 不该为 true")
	}
}

func TestClose_StopsReaperAndIsIdempotent(t *testing.T) {
	s := NewService("https://example.com", time.Second, 2, 10*time.Millisecond)
	markInitialized(s)

	if err := s.Close(); err != nil {
		t.Fatalf("首次 Close 报错: %v", err)
	}
	select {
	case <-s.reaperDone:
	default:
		t.Fatal("Close 返回后回收协程仍在运行")
	}

	// 重复 Close 不得 panic / 死锁 / 泄漏槽位
	if err := s.Close(); err != nil {
		t.Fatalf("重复 Close 报错: %v", err)
	}
	if got := len(s.sem); got != 0 {
		t.Fatalf("Close 后 len(sem) = %d, want 0", got)
	}
}
