package social

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"notifier/internal/config"
)

const (
	// 上次定时周报：「ISO 周 整体可用率」写在同一条记录里，周与基准不会错配
	stateWeeklyLast = "weekly.last"

	maxPendingAge = 2 * time.Hour

	weeklyWeekday = time.Monday
	weeklyHour    = 10
)

// Messenger 把文本私发给站长（纯文本，不带 parse mode，方便原样转发/复制）
type Messenger interface {
	SendText(ctx context.Context, chatID int64, text string) error
}

// StateStore 持久化周报进度（由 storage.Storage 实现）
type StateStore interface {
	GetSocialState(ctx context.Context, key string) (string, bool, error)
	SetSocialState(ctx context.Context, key, value string) error
}

type pendingDraft struct {
	Draft
	createdAt time.Time
}

// Service 定时检查并生成草稿
type Service struct {
	cfg      config.SocialConfig
	store    StateStore
	status   *StatusClient
	out      Messenger
	detector *OutageDetector
	now      func() time.Time

	sendMu  sync.Mutex     // 一份草稿的说明与正文成对发出，不与 /weekly 预览交错
	pending []pendingDraft // 已生成但没送达的异常草稿，按生成顺序重发；只由检查循环访问
}

// NewService 创建社媒草稿服务
func NewService(cfg config.SocialConfig, store StateStore, out Messenger) *Service {
	return &Service{
		cfg:      cfg,
		store:    store,
		status:   NewStatusClient(cfg.SiteURL),
		out:      out,
		detector: NewOutageDetector(cfg.OutageMinProviders, cfg.OutageRatio, 2*cfg.CheckInterval+time.Minute, cfg.SiteURL),
		now:      time.Now,
	}
}

// Start 运行检查循环，直到 ctx 结束
func (s *Service) Start(ctx context.Context) error {
	slog.Info("社媒草稿服务启动",
		"admin_chat_id", s.cfg.AdminChatID,
		"site_url", s.cfg.SiteURL,
		"check_interval", s.cfg.CheckInterval,
		"outage_min_providers", s.cfg.OutageMinProviders,
		"outage_ratio", s.cfg.OutageRatio,
	)
	ticker := time.NewTicker(s.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	if err := s.checkOutage(ctx); err != nil {
		slog.Warn("社媒异常检查失败", "error", err)
	}
	if err := s.maybeWeekly(ctx); err != nil {
		slog.Warn("社媒周报生成失败", "error", err)
	}
}

func (s *Service) checkOutage(ctx context.Context) error {
	now := s.now()
	groups, fetchErr := s.status.FetchHot(ctx, "90m")
	if fetchErr == nil {
		for _, d := range s.detector.Observe(Snapshot(groups, now), now) {
			s.pending = append(s.pending, pendingDraft{Draft: d, createdAt: now})
		}
	}
	// 拉取失败不喂检测器（不当作恢复），但积压的草稿照样重发。
	// 过时的直接丢掉——「现在有点不对」隔几个小时再发就是错的；
	// 一份失败就停下，保证同一次异常的说明先于恢复送达。
	for len(s.pending) > 0 {
		d := s.pending[0]
		if now.Sub(d.createdAt) > maxPendingAge {
			slog.Warn("社媒草稿积压过久，已丢弃", "created_at", d.createdAt, "note", firstLine(d.Note))
			s.pending = s.pending[1:]
			continue
		}
		if err := s.sendDraft(ctx, d.Draft); err != nil {
			if fetchErr != nil {
				return fetchErr
			}
			return fmt.Errorf("%d 份异常草稿未送达，下一轮重发: %w", len(s.pending), err)
		}
		s.pending = s.pending[1:]
	}
	return fetchErr
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// maybeWeekly 每周一 10 点后出一份；只在周一当天补发，错过就等下周
func (s *Service) maybeWeekly(ctx context.Context) error {
	now := s.now().In(cst)
	if now.Weekday() != weeklyWeekday || now.Hour() < weeklyHour {
		return nil
	}
	week := isoWeekKey(now)
	lastWeek, lastOverall, err := s.loadLast(ctx)
	if err != nil {
		return err
	}
	if lastWeek == week {
		return nil
	}
	prev := -1.0
	if lastWeek == isoWeekKey(now.AddDate(0, 0, -7)) {
		prev = lastOverall
	}
	report, err := s.buildWeekly(ctx, prev)
	if err != nil {
		return err
	}
	if err := s.sendDraft(ctx, Draft{Note: report.Note, Text: report.Text}); err != nil {
		return err
	}
	return s.store.SetSocialState(ctx, stateWeeklyLast, week+" "+strconv.FormatFloat(report.Overall, 'f', 4, 64))
}

// PreviewWeekly 立即生成一份周报草稿私发站长，不影响定时周报和周环比基准
func (s *Service) PreviewWeekly(ctx context.Context) error {
	report, err := s.buildWeekly(ctx, -1)
	if err != nil {
		return err
	}
	return s.sendDraft(ctx, Draft{Note: "（手动预览，不计入定时周报，也不带周环比）\n" + report.Note, Text: report.Text})
}

// IsAdmin 是否为接收草稿的站长私聊
func (s *Service) IsAdmin(chatID int64) bool {
	return chatID == s.cfg.AdminChatID
}

func (s *Service) loadLast(ctx context.Context) (week string, overall float64, err error) {
	v, ok, err := s.store.GetSocialState(ctx, stateWeeklyLast)
	if err != nil || !ok {
		return "", -1, err
	}
	week, num, found := strings.Cut(v, " ")
	if !found {
		return week, -1, nil
	}
	overall, perr := strconv.ParseFloat(num, 64)
	if perr != nil {
		return week, -1, nil
	}
	return week, overall, nil
}

func (s *Service) buildWeekly(ctx context.Context, prev float64) (*WeeklyReport, error) {
	groups, err := s.status.FetchHot(ctx, "7d")
	if err != nil {
		return nil, err
	}
	return BuildWeeklyReport(groups, prev, s.cfg.SiteURL, s.now())
}

// sendDraft 先发说明、再发正文：正文单独一条，站长可直接转发到群或复制到 X
func (s *Service) sendDraft(ctx context.Context, d Draft) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if err := s.out.SendText(ctx, s.cfg.AdminChatID, d.Note); err != nil {
		return fmt.Errorf("发送草稿说明失败: %w", err)
	}
	if err := s.out.SendText(ctx, s.cfg.AdminChatID, d.Text); err != nil {
		return fmt.Errorf("发送草稿正文失败: %w", err)
	}
	return nil
}

func isoWeekKey(t time.Time) string {
	y, w := t.ISOWeek()
	return fmt.Sprintf("%d-W%02d", y, w)
}
