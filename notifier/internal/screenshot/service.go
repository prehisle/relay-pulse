package screenshot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ErrConcurrencyLimit 表示截图并发已达到上限
var ErrConcurrencyLimit = errors.New("截图并发已达到上限")

// CaptureOptions 截图可选参数
type CaptureOptions struct {
	Title string // 截图标题（群名/用户名 + 专属状态）
	Board string // 板块过滤（如 "active"），为空时不传 board 参数（API 默认 hot）
}

// defaultIdleTimeout 是浏览器空闲多久后被回收的兜底值
const defaultIdleTimeout = 10 * time.Minute

// Service 提供基于 Playwright 的截图服务
//
// 设计要点：
// - Browser 进程级复用，懒加载初始化
// - 每次请求创建/销毁 BrowserContext + Page
// - 信号量限制并发
// - 空闲超过 idleTimeout 由后台协程关掉浏览器，下次请求重新懒加载
type Service struct {
	pw          *playwright.Playwright
	browser     playwright.Browser
	baseURL     string
	timeout     time.Duration
	idleTimeout time.Duration
	sem         chan struct{}
	mu          sync.Mutex
	initialized bool
	lastUsed    time.Time
	// closeErr 记住一次失败的关闭。关不掉意味着旧 chromium 可能还活着，
	// 此后必须拒绝再起一个（否则内存翻倍），由人重启容器收拾。
	closeErr error

	// closeMu 串行化关闭流程（reaper / 进程退出 / 重复 Close 三个入口）
	closeMu    sync.Mutex
	stopCh     chan struct{}
	stopOnce   sync.Once
	reaperDone chan struct{}
}

// NewService 创建截图服务
//
// idleTimeout ≤ 0 时取 defaultIdleTimeout。返回的 Service 会启动一个后台回收协程，
// 调用方必须在不再使用时调用 Close 停止它。
func NewService(baseURL string, timeout time.Duration, maxConcurrent int, idleTimeout time.Duration) *Service {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	s := &Service{
		baseURL:     strings.TrimRight(baseURL, "/"),
		timeout:     timeout,
		idleTimeout: idleTimeout,
		sem:         make(chan struct{}, maxConcurrent),
		stopCh:      make(chan struct{}),
		reaperDone:  make(chan struct{}),
	}
	go s.runIdleReaper()
	return s
}

// touch 记录一次截图活动，用于空闲判定
func (s *Service) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// runIdleReaper 周期性回收空闲的浏览器
//
// 浏览器进程（chromium + node driver）常驻约 115MB，而截图是低频操作——一次
// 失败的截图同样会让它挂到进程退出为止。冷启动实测 ~2s，用它换掉常驻内存是划算的。
func (s *Service) runIdleReaper() {
	defer close(s.reaperDone)

	// 半个空闲窗口检查一次：最坏情况下实际回收时刻不晚于 1.5 × idleTimeout
	interval := s.idleTimeout / 2
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.closeBrowser(true); err != nil {
				// 关不掉是终态：后续截图会被 ensureInitialized 显式拒绝，需人工重启
				slog.Error("空闲回收浏览器失败，截图功能已停用直至服务重启", "error", err)
			}
		case <-s.stopCh:
			return
		}
	}
}

// ensureInitialized 懒加载初始化 Playwright 和 Browser
func (s *Service) ensureInitialized() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
	}
	// 上一次关闭失败过：旧浏览器进程可能还在，再拉一个就是内存翻倍。
	// 这里显式停下而不是"重来一次"——需要人介入重启进程。
	if s.closeErr != nil {
		return fmt.Errorf("上次关闭浏览器失败、旧进程可能仍在运行，已停止再次启动（需重启服务）: %w", s.closeErr)
	}

	slog.Info("初始化 Playwright...")

	// 确保 Playwright driver 和浏览器已安装
	if err := playwright.Install(&playwright.RunOptions{
		Browsers: []string{"chromium"},
	}); err != nil {
		return fmt.Errorf("安装 Playwright 失败: %w", err)
	}

	pw, err := playwright.Run()
	if err != nil {
		return fmt.Errorf("启动 Playwright 失败: %w", err)
	}

	// 显式钉住启动超时：playwright-go v0.6201.1 起未指定时的默认值从 30s 变成了上游的 180s，
	// 而 Launch 期间 s.mu 是持锁的——启动卡死会让全部截图请求一起阻塞 3 分钟。
	launchTimeoutMs := float64(30000)
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Timeout:  &launchTimeoutMs,
		Args: []string{
			"--no-sandbox",
			"--disable-setuid-sandbox",
			"--disable-dev-shm-usage",
		},
	})
	if err != nil {
		_ = pw.Stop()
		return fmt.Errorf("启动 Chromium 失败: %w", err)
	}

	s.pw = pw
	s.browser = browser
	s.initialized = true
	s.lastUsed = time.Now()

	slog.Info("Playwright 初始化完成")
	return nil
}

// buildURL 构建截图 URL
// 格式: {baseURL}/?provider=p1,p2&service=s1,s2&period=90m&screenshot=1[&title=xxx]
func (s *Service) buildURL(providers, services []string, opts *CaptureOptions) (string, error) {
	u, err := url.Parse(s.baseURL)
	if err != nil {
		return "", fmt.Errorf("解析 baseURL 失败: %w", err)
	}

	q := u.Query()
	if len(providers) > 0 {
		q.Set("provider", strings.Join(providers, ","))
	}
	if len(services) > 0 {
		q.Set("service", strings.Join(services, ","))
	}
	q.Set("period", "90m")
	q.Set("screenshot", "1")
	if opts != nil && opts.Board != "" {
		q.Set("board", opts.Board)
	}
	if opts != nil && opts.Title != "" {
		// 规范化：去除控制字符，限制长度
		title := strings.TrimSpace(opts.Title)
		title = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(title)
		if title != "" {
			r := []rune(title)
			if len(r) > 60 {
				title = string(r[:60]) + "…"
			}
			q.Set("title", title)
		}
	}
	u.RawQuery = q.Encode()

	return u.String(), nil
}

// Capture 根据 providers 和 services 渲染页面并截图，返回 PNG 图片数据（向后兼容）
func (s *Service) Capture(ctx context.Context, providers, services []string) ([]byte, error) {
	return s.CaptureWithOptions(ctx, providers, services, nil)
}

// CaptureWithOptions 根据 providers、services 和可选参数渲染页面并截图
func (s *Service) CaptureWithOptions(ctx context.Context, providers, services []string, opts *CaptureOptions) ([]byte, error) {
	startTime := time.Now()

	// 尝试获取并发信号量（非阻塞）
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return nil, ErrConcurrencyLimit
	}

	// 检查 context 是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// 开始与结束各记一次活动时间：单次截图可能长于 idleTimeout（导航 30s + 等就绪 15s），
	// 只记开始会让它一结束就被判成空闲，只记结束则整个截图期间 reaper 都在白排空信号量。
	// 这个 defer 注册在信号量释放之后，故先于释放执行——回收方拿到全部槽位时 lastUsed 必已刷新。
	s.touch()
	defer s.touch()

	// 懒加载初始化
	if err := s.ensureInitialized(); err != nil {
		return nil, err
	}

	targetURL, err := s.buildURL(providers, services, opts)
	if err != nil {
		return nil, err
	}

	// 避免把群名等敏感信息（title）打进日志
	slog.Debug("开始截图", "providers", providers, "services", services, "has_title", opts != nil && opts.Title != "")

	// 创建浏览器上下文（固定宽度 1200px，强制中文语言）
	browserCtx, err := s.browser.NewContext(playwright.BrowserNewContextOptions{
		Viewport: &playwright.Size{
			Width:  1200,
			Height: 800,
		},
		// 强制中文语言
		Locale: playwright.String("zh-CN"),
		// 禁用动画
		ReducedMotion: playwright.ReducedMotionReduce,
	})
	if err != nil {
		return nil, fmt.Errorf("创建浏览器上下文失败: %w", err)
	}
	defer func() { _ = browserCtx.Close() }()

	page, err := browserCtx.NewPage()
	if err != nil {
		return nil, fmt.Errorf("创建页面失败: %w", err)
	}
	defer func() { _ = page.Close() }()

	// 导航到页面
	timeoutMs := float64(s.timeout.Milliseconds())
	waitUntil := playwright.WaitUntilStateNetworkidle
	if _, err := page.Goto(targetURL, playwright.PageGotoOptions{
		WaitUntil: waitUntil,
		Timeout:   &timeoutMs,
	}); err != nil {
		return nil, fmt.Errorf("打开页面失败: %w", err)
	}

	// 等待数据加载完成标记
	dataReadyTimeout := float64(15000) // 15秒等待数据加载
	state := playwright.WaitForSelectorStateAttached
	if _, err := page.WaitForSelector(`[data-ready="true"]`, playwright.PageWaitForSelectorOptions{
		State:   state,
		Timeout: &dataReadyTimeout,
	}); err != nil {
		return nil, fmt.Errorf("等待页面就绪失败: %w", err)
	}

	// 检查是否有错误标记
	errAttr, err := page.Evaluate(`() => {
		const el = document.querySelector('[data-error]');
		if (!el) return null;
		return el.getAttribute('data-error') || 'unknown error';
	}`)
	if err != nil {
		slog.Warn("检查页面错误标记失败", "error", err)
	} else if errAttr != nil {
		if errStr, ok := errAttr.(string); ok && errStr != "" {
			return nil, fmt.Errorf("页面渲染错误: %s", errStr)
		}
	}

	// 截取 data-ready 元素（精确匹配内容区域，避免多余空白）
	readyElement, err := page.QuerySelector(`[data-ready="true"]`)
	if err != nil || readyElement == nil {
		return nil, fmt.Errorf("未找到就绪元素: %w", err)
	}

	buf, err := readyElement.Screenshot(playwright.ElementHandleScreenshotOptions{
		Type: playwright.ScreenshotTypePng,
	})
	if err != nil {
		return nil, fmt.Errorf("截图失败: %w", err)
	}

	slog.Info("截图完成",
		"providers", providers,
		"size_bytes", len(buf),
		"duration_ms", time.Since(startTime).Milliseconds(),
	)

	return buf, nil
}

// Close 停止空闲回收协程并关闭浏览器与 Playwright（可安全重复调用）
func (s *Service) Close() error {
	s.stopIdleReaper()
	return s.closeBrowser(false)
}

// stopIdleReaper 停掉回收协程并等它退出
func (s *Service) stopIdleReaper() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.reaperDone
}

// closeBrowser 关闭浏览器与 Playwright
//
// idleOnly=true 时只在确实空闲超时才动手（reaper 走这条），false 表示无条件关闭（Close 走这条）。
func (s *Service) closeBrowser(idleOnly bool) error {
	// 串行化三个入口：reaper 的周期回收、进程退出的 Close、以及重复 Close
	s.closeMu.Lock()
	defer s.closeMu.Unlock()

	// 空闲回收先做一次廉价判定，避免每个 tick 都去排空信号量
	if idleOnly && !s.idleExpired() {
		return nil
	}

	// 等待在途请求结束：通过获取所有信号量槽来阻塞等待
	// 注意：必须在获取 mu 锁之前完成，因为 Capture 持有信号量时可能需要 mu 锁
	for i := 0; i < cap(s.sem); i++ {
		s.sem <- struct{}{}
	}
	// 释放信号量槽，允许后续 Capture 重新走懒加载初始化
	defer func() {
		for i := 0; i < cap(s.sem); i++ {
			<-s.sem
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()

	// 已经关过就不必再关。⚠️ 这个检查刻意放在排空**之后**：放在排空之前提前返回，
	// 会让「已拿到信号量、还没进 ensureInitialized」的那个请求在 Close 返回后才把
	// 浏览器拉起来，进程退出时便留下一个没人关的 chromium。
	if !s.initialized {
		return nil
	}
	// 排空期间可能有在途截图刚结束并刷新了 lastUsed，空闲判定要重来一次
	if idleOnly && time.Since(s.lastUsed) < s.idleTimeout {
		return nil
	}

	if idleOnly {
		slog.Info("浏览器空闲超时，关闭 Playwright...", "idle_timeout", s.idleTimeout)
	} else {
		slog.Info("关闭 Playwright...")
	}

	var firstErr error
	if s.browser != nil {
		if err := s.browser.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("关闭浏览器失败: %w", err)
		}
	}
	if s.pw != nil {
		if err := s.pw.Stop(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("停止 Playwright 失败: %w", err)
		}
	}

	s.browser = nil
	s.pw = nil
	s.initialized = false
	if firstErr != nil {
		// 句柄已经不可用，状态只能置回未初始化；但"关不掉"这件事必须留痕，
		// 否则下一次截图会在旧进程可能仍存活的情况下再起一个 chromium。
		s.closeErr = firstErr
	}

	return firstErr
}

// idleExpired 报告浏览器是否已初始化且空闲超过 idleTimeout
func (s *Service) idleExpired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized && time.Since(s.lastUsed) >= s.idleTimeout
}
