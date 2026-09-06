package notifier

import (
	"context"
	"fmt"
	"strings"

	"notifier/internal/poller"
	"notifier/internal/qq"
	"notifier/internal/storage"
)

// qqChannel 实现 ChannelSender，负责 QQ 消息格式化与发送。
type qqChannel struct {
	client *qq.Client
}

func (c *qqChannel) Platform() string {
	return storage.PlatformQQ
}

func (c *qqChannel) FormatMessage(event *poller.Event) string {
	emoji, statusText := resolveStatusLabel(event)

	location := fmt.Sprintf("%s / %s", event.Provider, event.Service)
	if channel := displayChannel(event); channel != "" {
		location += fmt.Sprintf(" / %s", channel)
	}

	var modelLine string
	models := extractModels(event)
	if len(models) == 1 {
		modelLine = fmt.Sprintf("\n模型: %s", models[0])
	} else if len(models) > 1 {
		modelLine = fmt.Sprintf("\n模型: %s", strings.Join(models, ", "))
	}

	var details string
	if subStatus, ok := event.Meta["sub_status"]; ok {
		details = fmt.Sprintf("\n原因: %v", subStatus)
	}

	eventTime := formatEventTime(event)

	return fmt.Sprintf("%s %s\n\n%s%s%s\n\n时间: %s", emoji, statusText, location, modelLine, details, eventTime)
}

func (c *qqChannel) Send(ctx context.Context, chatID int64, message string) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("qq client not configured")
	}

	var mid int64
	var err error

	// 负数 chatID 表示群聊，正数表示私聊
	if chatID < 0 {
		mid, err = c.client.SendGroupMessage(ctx, -chatID, message)
	} else {
		mid, err = c.client.SendPrivateMessage(ctx, chatID, message)
	}

	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d", mid), nil
}

func (c *qqChannel) HandleSendError(_ context.Context, _ *storage.Delivery, _ error) bool {
	// QQ 无平台特定错误处理，走通用重试
	return false
}
