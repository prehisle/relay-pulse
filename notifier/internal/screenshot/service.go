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

// Service 提供基于 Playwright 的截图服务
//
// 设计要点：
// - Browser 进程级复用，懒加载初始化
// - 每次请求创建/销毁 BrowserContext + Page
// - 信号量限制并发
type Service struct {
	pw          *playwright.Playwright
	browser     playwright.Browser
	baseURL     string
	timeout     time.Duration
	sem         chan struct{}
	mu          sync.Mutex
	initialized bool
}

// NewService 创建截图服务
func NewService(baseURL string, timeout time.Duration, maxConcurrent int) *Service {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &Service{
		baseURL: strings.TrimRight(baseURL, "/"),
		timeout: timeout,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// ensureInitialized 懒加载初始化 Playwright 和 Browser
func (s *Service) ensureInitialized() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.initialized {
		return nil
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

// Close 关闭浏览器与 Playwright（可安全重复调用）
func (s *Service) Close() error {
	// 先检查是否已初始化（无锁检查，避免死锁）
	s.mu.Lock()
	if !s.initialized {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	// 等待在途请求结束：通过获取所有信号量槽来阻塞等待
	// 注意：必须在获取 mu 锁之前完成，因为 Capture 持有信号量时可能需要 mu 锁
	for i := 0; i < cap(s.sem); i++ {
		s.sem <- struct{}{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 再次检查（可能在等待信号量期间被其他 Close 调用处理了）
	if !s.initialized {
		// 释放信号量槽
		for i := 0; i < cap(s.sem); i++ {
			<-s.sem
		}
		return nil
	}

	slog.Info("关闭 Playwright...")

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

	// 释放信号量槽，允许后续 Capture 正常失败（因为 initialized=false）
	for i := 0; i < cap(s.sem); i++ {
		<-s.sem
	}

	return firstErr
}
