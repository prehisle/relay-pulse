package storage

import (
	"context"
	"time"
)

// 平台常量
const (
	PlatformTelegram = "telegram"
	PlatformQQ       = "qq"
)

// ChatRef 投递目标引用（平台 + ChatID）
type ChatRef struct {
	Platform string
	ChatID   int64
}

// Storage 存储接口
type Storage interface {
	// Init 初始化存储（创建表）
	Init(ctx context.Context) error

	// Close 关闭存储
	Close() error

	// ===== 游标管理 =====

	// GetCursor 获取轮询游标
	GetCursor(ctx context.Context) (int64, error)

	// UpdateCursor 更新轮询游标
	UpdateCursor(ctx context.Context, lastEventID int64) error

	// ===== Chat 管理（多平台） =====

	// UpsertChat 创建或更新 Chat
	UpsertChat(ctx context.Context, chat *Chat) error

	// GetChat 获取 Chat
	GetChat(ctx context.Context, platform string, chatID int64) (*Chat, error)

	// UpdateChatStatus 更新用户状态（active/blocked）
	UpdateChatStatus(ctx context.Context, platform string, chatID int64, status string) error

	// UpdateChatCommandTime 更新用户命令时间（防滥用）
	UpdateChatCommandTime(ctx context.Context, platform string, chatID int64) error

	// ===== 订阅管理 =====

	// AddSubscription 添加订阅
	AddSubscription(ctx context.Context, sub *Subscription) error

	// RemoveSubscription 移除订阅
	RemoveSubscription(ctx context.Context, platform string, chatID int64, provider, service, channel string) error

	// GetSubscriptionsByChatID 获取用户的所有订阅
	GetSubscriptionsByChatID(ctx context.Context, platform string, chatID int64) ([]*Subscription, error)

	// GetSubscribersByMonitor 获取监测项的所有订阅者（返回平台+ChatID）
	GetSubscribersByMonitor(ctx context.Context, provider, service, channel string) ([]*ChatRef, error)

	// CountSubscriptions 统计用户订阅数
	CountSubscriptions(ctx context.Context, platform string, chatID int64) (int, error)

	// ClearSubscriptions 清空用户所有订阅
	ClearSubscriptions(ctx context.Context, platform string, chatID int64) error

	// ===== 绑定 Token 管理 =====

	// CreateBindToken 创建绑定 token
	CreateBindToken(ctx context.Context, token *BindToken) error

	// GetBindToken 获取绑定 token（不标记已使用）
	GetBindToken(ctx context.Context, token string) (*BindToken, error)

	// ConsumeBindToken 消费绑定 token（标记已使用）
	ConsumeBindToken(ctx context.Context, token string) (*BindToken, error)

	// CleanupExpiredTokens 清理过期 token
	CleanupExpiredTokens(ctx context.Context) (int64, error)

	// ===== 投递记录管理 =====

	// CreateDelivery 创建投递记录（pending 状态）
	CreateDelivery(ctx context.Context, delivery *Delivery) error

	// UpdateDeliveryStatus 更新投递状态
	UpdateDeliveryStatus(ctx context.Context, id int64, status string, messageID string, errorMsg string) error

	// GetPendingDeliveries 获取待发送的投递记录
	GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error)

	// IncrementRetryCount 增加重试次数
	IncrementRetryCount(ctx context.Context, id int64) error

	// CleanupOldDeliveries 清理旧的投递记录
	CleanupOldDeliveries(ctx context.Context, before time.Time) (int64, error)

	// ===== 社媒草稿状态 =====

	// GetSocialState 读取社媒模块的持久化状态（如上次周报的周编号），不存在时 ok=false
	GetSocialState(ctx context.Context, key string) (value string, ok bool, err error)

	// SetSocialState 写入社媒模块的持久化状态
	SetSocialState(ctx context.Context, key, value string) error
}

// Chat 多平台用户/群
type Chat struct {
	Platform      string
	ChatID        int64
	Username      string
	FirstName     string
	Status        string // active/blocked
	LastCommandAt int64
	CommandCount  int
	CreatedAt     int64
	UpdatedAt     int64
}

// Subscription 订阅关系
type Subscription struct {
	ID        int64
	Platform  string
	ChatID    int64
	Provider  string
	Service   string
	Channel   string
	CreatedAt int64
}

// BindToken 绑定 token
type BindToken struct {
	Token     string
	Favorites string // JSON 格式的收藏列表
	ExpiresAt int64
	UsedAt    int64 // 0 表示未使用
	CreatedAt int64
}

// Delivery 投递记录
type Delivery struct {
	ID           int64
	EventID      int64
	Platform     string
	ChatID       int64
	Status       string // pending/sent/failed
	MessageID    string
	ErrorMessage string
	RetryCount   int
	CreatedAt    int64
	UpdatedAt    int64
}

// DeliveryStatus 投递状态常量
const (
	DeliveryStatusPending = "pending"
	DeliveryStatusSent    = "sent"
	DeliveryStatusFailed  = "failed"
)

// ChatStatus 用户状态常量
const (
	ChatStatusActive  = "active"
	ChatStatusBlocked = "blocked"
)
