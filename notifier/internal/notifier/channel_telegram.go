package notifier

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"notifier/internal/poller"
	"notifier/internal/storage"
	"notifier/internal/telegram"
)

// telegramChannel 实现 ChannelSender，负责 Telegram 消息格式化与发送。
type telegramChannel struct {
	client  *telegram.Client
	storage storage.Storage
}

func (c *telegramChannel) Platform() string {
	return storage.PlatformTelegram
}

func (c *telegramChannel) FormatMessage(event *poller.Event) string {
	emoji, statusText := resolveStatusLabel(event)

	// 转义 HTML 防止注入
	provider := html.EscapeString(event.Provider)
	service := html.EscapeString(event.Service)
	channel := html.EscapeString(displayChannel(event))

	location := fmt.Sprintf("<b>%s</b> / <b>%s</b>", provider, service)
	if channel != "" {
		location += fmt.Sprintf(" / <b>%s</b>", channel)
	}

	var modelLine string
	models := extractModels(event)
	if len(models) == 1 {
		modelLine = fmt.Sprintf("\n模型: %s", html.EscapeString(models[0]))
	} else if len(models) > 1 {
		modelLine = fmt.Sprintf("\n模型: %s", html.EscapeString(strings.Join(models, ", ")))
	}

	var details string
	if subStatus, ok := event.Meta["sub_status"]; ok {
		details = fmt.Sprintf("\n原因: %s", html.EscapeString(fmt.Sprintf("%v", subStatus)))
	}

	eventTime := formatEventTime(event)

	return fmt.Sprintf(`%s <b>%s</b>

%s%s%s

时间: %s`,
		emoji, statusText,
		location,
		modelLine,
		details,
		eventTime,
	)
}

func (c *telegramChannel) Send(ctx context.Context, chatID int64, message string) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("telegram client not configured")
	}

	result, err := c.client.SendMessageHTML(ctx, chatID, message)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d", result.MessageID), nil
}

func (c *telegramChannel) HandleSendError(ctx context.Context, delivery *storage.Delivery, sendErr error) bool {
	if !telegram.IsForbiddenError(sendErr) {
		return false
	}

	// 用户封禁了 bot → 终态处理
	if err := c.storage.UpdateChatStatus(ctx, delivery.Platform, delivery.ChatID, storage.ChatStatusBlocked); err != nil {
		slog.Error("更新用户状态失败", "error", err)
	}
	if err := c.storage.UpdateDeliveryStatus(ctx, delivery.ID, storage.DeliveryStatusFailed, "", "user blocked bot"); err != nil {
		slog.Error("更新投递状态失败", "error", err)
	}
	return true
}

// ─── 共享工具函数 ────────────────────────────────────────────

// resolveStatusLabel 根据事件类型/状态返回 emoji 和状态文本。
func resolveStatusLabel(event *poller.Event) (emoji, statusText string) {
	switch event.Type {
	case "UP":
		return "🟢", "服务已恢复"
	case "DOWN":
		return "🔴", "服务不可用"
	default:
		switch event.ToStatus {
		case 1:
			return "🟢", "服务已恢复"
		case 2:
			return "🟡", "服务波动"
		case 0:
			return "🔴", "服务不可用"
		default:
			return "⚪", "状态变更"
		}
	}
}

// formatEventTime 格式化事件时间为 CST 字符串。
func formatEventTime(event *poller.Event) string {
	eventTs := event.ObservedAt
	if eventTs == 0 {
		eventTs = event.CreatedAt
	}
	cst := time.FixedZone("CST", 8*60*60)
	return time.Unix(eventTs, 0).In(cst).Format("2006-01-02 15:04:05")
}
