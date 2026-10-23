package storage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStorage SQLite 存储实现
type SQLiteStorage struct {
	db *sql.DB
}

func normalizeSQLiteDSN(dsn string) string {
	// 兼容用户仅配置了 _journal_mode=WAL 的情况：追加 busy_timeout，避免启动时并发建表/写入直接 SQLITE_BUSY 失败。
	// 不覆盖用户已有配置。
	needJournal := !strings.Contains(dsn, "_journal_mode=")
	needTimeout := !strings.Contains(dsn, "_timeout=") && !strings.Contains(dsn, "timeout=")
	needBusyTimeout := !strings.Contains(dsn, "_busy_timeout=") && !strings.Contains(dsn, "busy_timeout=")

	if !needJournal && !needTimeout && !needBusyTimeout {
		return dsn
	}

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	var b strings.Builder
	b.WriteString(dsn)
	if needJournal {
		b.WriteString(sep)
		b.WriteString("_journal_mode=WAL")
		sep = "&"
	}
	if needTimeout {
		b.WriteString(sep)
		b.WriteString("_timeout=5000")
		sep = "&"
	}
	if needBusyTimeout {
		b.WriteString(sep)
		b.WriteString("_busy_timeout=5000")
	}
	return b.String()
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked")
}

func execWithRetry(ctx context.Context, db *sql.DB, query string, args ...any) error {
	backoff := 100 * time.Millisecond
	for attempt := 0; attempt < 6; attempt++ {
		_, err := db.ExecContext(ctx, query, args...)
		if err == nil {
			return nil
		}
		if !isSQLiteBusy(err) || ctx.Err() != nil {
			return err
		}
		time.Sleep(backoff)
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
	_, err := db.ExecContext(ctx, query, args...)
	if isSQLiteBusy(err) {
		return fmt.Errorf("SQLite 数据库被占用（可能有另一个 notifier 实例或 SQLite 工具正在使用同一数据库文件）: %w", err)
	}
	return err
}

// NewSQLiteStorage 创建 SQLite 存储
func NewSQLiteStorage(dsn string) (*SQLiteStorage, error) {
	dsn = normalizeSQLiteDSN(dsn)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// SQLite 配置
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	// 额外的 PRAGMA（不依赖 DSN 参数是否支持/是否被覆盖，失败则忽略）
	_, _ = db.Exec(`PRAGMA journal_mode=WAL`)
	_, _ = db.Exec(`PRAGMA busy_timeout=5000`)
	_, _ = db.Exec(`PRAGMA foreign_keys=ON`)

	return &SQLiteStorage{db: db}, nil
}

// Init 初始化数据库表
func (s *SQLiteStorage) Init(ctx context.Context) error {
	// 游标表
	if err := execWithRetry(ctx, s.db, `
		CREATE TABLE IF NOT EXISTS poll_cursor (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			last_event_id INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("创建 poll_cursor 表失败: %w", err)
	}

	// 初始化游标（如果不存在）
	if err := execWithRetry(ctx, s.db, `
		INSERT OR IGNORE INTO poll_cursor (id, last_event_id, updated_at) VALUES (1, 0, ?)
	`, time.Now().Unix()); err != nil {
		return fmt.Errorf("初始化游标失败: %w", err)
	}

	// 检查是否需要迁移到多平台 schema
	needsMigration, err := s.needsMultiPlatformMigration(ctx)
	if err != nil {
		return fmt.Errorf("检查迁移状态失败: %w", err)
	}

	if needsMigration {
		slog.Info("检测到旧版 schema，开始迁移到多平台支持...")
		if err := s.migrateLegacyToMultiPlatform(ctx); err != nil {
			return fmt.Errorf("迁移到多平台失败: %w", err)
		}
		slog.Info("多平台 schema 迁移完成")
	}

	// 确保多平台 schema 存在
	if err := s.ensureMultiPlatformSchema(ctx); err != nil {
		return fmt.Errorf("确保多平台 schema 失败: %w", err)
	}

	// 绑定 token 表
	if err := execWithRetry(ctx, s.db, `
		CREATE TABLE IF NOT EXISTS bind_tokens (
			token TEXT PRIMARY KEY,
			favorites TEXT NOT NULL,
			expires_at INTEGER NOT NULL,
			used_at INTEGER,
			created_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("创建 bind_tokens 表失败: %w", err)
	}

	if err := execWithRetry(ctx, s.db, `
		CREATE INDEX IF NOT EXISTS idx_bind_tokens_expires ON bind_tokens(expires_at)
	`); err != nil {
		return fmt.Errorf("创建 bind_tokens 索引失败: %w", err)
	}

	return nil
}

// needsMultiPlatformMigration 检查是否需要迁移
func (s *SQLiteStorage) needsMultiPlatformMigration(ctx context.Context) (bool, error) {
	// 检查旧的 telegram_chats 表是否存在
	oldTableExists, err := s.tableExists(ctx, "telegram_chats")
	if err != nil {
		return false, err
	}

	if !oldTableExists {
		// 旧表不存在，无需迁移（全新安装或已完成迁移）
		return false, nil
	}

	// 旧表存在，检查是否已有 platform 列
	hasPlatform, err := s.hasColumn(ctx, "subscriptions", "platform")
	if err != nil {
		// 表可能不存在
		return true, nil
	}

	// 如果 subscriptions 表没有 platform 列，需要迁移
	return !hasPlatform, nil
}

// tableExists 检查表是否存在
func (s *SQLiteStorage) tableExists(ctx context.Context, name string) (bool, error) {
	var got string
	err := s.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, name,
	).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询表信息失败: %w", err)
	}
	return got == name, nil
}

// tableExistsTx 在事务内检查表是否存在（避免死锁）
func tableExistsTx(ctx context.Context, tx *sql.Tx, name string) (bool, error) {
	var got string
	err := tx.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, name,
	).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询表信息失败: %w", err)
	}
	return got == name, nil
}

// hasColumn 检查表是否有指定列
func (s *SQLiteStorage) hasColumn(ctx context.Context, tableName, columnName string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, fmt.Errorf("查询表字段失败: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("扫描表字段失败: %w", err)
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, nil
}

// migrateLegacyToMultiPlatform 迁移旧数据到多平台 schema
func (s *SQLiteStorage) migrateLegacyToMultiPlatform(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开始迁移事务失败: %w", err)
	}
	defer tx.Rollback()

	// 清理可能存在的临时表（保证幂等）
	for _, table := range []string{"chats_new", "subscriptions_new", "deliveries_new"} {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)); err != nil {
			return fmt.Errorf("清理临时表失败: %w", err)
		}
	}

	// 创建新的 chats 表
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE chats_new (
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			username TEXT,
			first_name TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_command_at INTEGER,
			command_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (platform, chat_id)
		)
	`); err != nil {
		return fmt.Errorf("创建 chats_new 表失败: %w", err)
	}

	// 创建新的 subscriptions 表
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE subscriptions_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			service TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(platform, chat_id, provider, service, channel),
			FOREIGN KEY (platform, chat_id) REFERENCES chats_new(platform, chat_id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("创建 subscriptions_new 表失败: %w", err)
	}

	// 创建新的 deliveries 表
	if _, err := tx.ExecContext(ctx, `
		CREATE TABLE deliveries_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id INTEGER NOT NULL,
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			message_id TEXT,
			error_message TEXT,
			retry_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(event_id, platform, chat_id)
		)
	`); err != nil {
		return fmt.Errorf("创建 deliveries_new 表失败: %w", err)
	}

	// 迁移 telegram_chats 数据（旧数据一律视为 Telegram）
	telegramChatsExists, _ := tableExistsTx(ctx, tx, "telegram_chats")
	if telegramChatsExists {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO chats_new (platform, chat_id, username, first_name, status, last_command_at, command_count, created_at, updated_at)
			SELECT 'telegram', chat_id, username, first_name, status, last_command_at, command_count, created_at, updated_at
			FROM telegram_chats
		`); err != nil {
			return fmt.Errorf("迁移 telegram_chats 数据失败: %w", err)
		}
	}

	// 迁移 subscriptions 数据
	subsExists, _ := tableExistsTx(ctx, tx, "subscriptions")
	if subsExists {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO subscriptions_new (id, platform, chat_id, provider, service, channel, created_at)
			SELECT id, 'telegram', chat_id, provider, service, channel, created_at
			FROM subscriptions
		`); err != nil {
			return fmt.Errorf("迁移 subscriptions 数据失败: %w", err)
		}
	}

	// 迁移 deliveries 数据
	deliveriesExists, _ := tableExistsTx(ctx, tx, "deliveries")
	if deliveriesExists {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO deliveries_new (id, event_id, platform, chat_id, status, message_id, error_message, retry_count, created_at, updated_at)
			SELECT id, event_id, 'telegram', chat_id, status, message_id, error_message, retry_count, created_at, updated_at
			FROM deliveries
		`); err != nil {
			return fmt.Errorf("迁移 deliveries 数据失败: %w", err)
		}
	}

	// 旧表重命名为 *_legacy（保留回滚能力）
	if telegramChatsExists {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE telegram_chats RENAME TO telegram_chats_legacy`); err != nil {
			return fmt.Errorf("重命名 telegram_chats 失败: %w", err)
		}
	}
	if subsExists {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE subscriptions RENAME TO subscriptions_legacy`); err != nil {
			return fmt.Errorf("重命名 subscriptions 失败: %w", err)
		}
	}
	if deliveriesExists {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE deliveries RENAME TO deliveries_legacy`); err != nil {
			return fmt.Errorf("重命名 deliveries 失败: %w", err)
		}
	}

	// 新表切换为正式表名
	if _, err := tx.ExecContext(ctx, `ALTER TABLE chats_new RENAME TO chats`); err != nil {
		return fmt.Errorf("切换 chats 表失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE subscriptions_new RENAME TO subscriptions`); err != nil {
		return fmt.Errorf("切换 subscriptions 表失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE deliveries_new RENAME TO deliveries`); err != nil {
		return fmt.Errorf("切换 deliveries 表失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移事务失败: %w", err)
	}

	return nil
}

// ensureMultiPlatformSchema 确保多平台 schema 存在
func (s *SQLiteStorage) ensureMultiPlatformSchema(ctx context.Context) error {
	// chats 表
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS chats (
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			username TEXT,
			first_name TEXT,
			status TEXT NOT NULL DEFAULT 'active',
			last_command_at INTEGER,
			command_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (platform, chat_id)
		)
	`); err != nil {
		return fmt.Errorf("创建 chats 表失败: %w", err)
	}

	// subscriptions 表
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS subscriptions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			provider TEXT NOT NULL,
			service TEXT NOT NULL,
			channel TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			UNIQUE(platform, chat_id, provider, service, channel),
			FOREIGN KEY (platform, chat_id) REFERENCES chats(platform, chat_id) ON DELETE CASCADE
		)
	`); err != nil {
		return fmt.Errorf("创建 subscriptions 表失败: %w", err)
	}

	// 订阅索引
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_subscriptions_psc ON subscriptions(provider, service, channel)
	`); err != nil {
		return fmt.Errorf("创建订阅索引失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_subscriptions_chat ON subscriptions(platform, chat_id)
	`); err != nil {
		return fmt.Errorf("创建订阅索引失败: %w", err)
	}

	// deliveries 表
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS deliveries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id INTEGER NOT NULL,
			platform TEXT NOT NULL,
			chat_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			message_id TEXT,
			error_message TEXT,
			retry_count INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(event_id, platform, chat_id)
		)
	`); err != nil {
		return fmt.Errorf("创建 deliveries 表失败: %w", err)
	}

	// deliveries 索引
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_deliveries_pending ON deliveries(status, created_at) WHERE status = 'pending'
	`); err != nil {
		return fmt.Errorf("创建 deliveries 索引失败: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_deliveries_event ON deliveries(event_id)
	`); err != nil {
		return fmt.Errorf("创建 deliveries 索引失败: %w", err)
	}

	// 社媒草稿模块的键值状态
	if err := execWithRetry(ctx, s.db, `
		CREATE TABLE IF NOT EXISTS social_state (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)
	`); err != nil {
		return fmt.Errorf("创建 social_state 表失败: %w", err)
	}

	return nil
}

// Close 关闭数据库
func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// ===== 游标管理 =====

// GetCursor 获取轮询游标
func (s *SQLiteStorage) GetCursor(ctx context.Context) (int64, error) {
	var lastEventID int64
	err := s.db.QueryRowContext(ctx, `SELECT last_event_id FROM poll_cursor WHERE id = 1`).Scan(&lastEventID)
	if err != nil {
		return 0, fmt.Errorf("获取游标失败: %w", err)
	}
	return lastEventID, nil
}

// UpdateCursor 更新轮询游标
func (s *SQLiteStorage) UpdateCursor(ctx context.Context, lastEventID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE poll_cursor SET last_event_id = ?, updated_at = ? WHERE id = 1
	`, lastEventID, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("更新游标失败: %w", err)
	}
	return nil
}

// ===== 社媒草稿状态 =====

// GetSocialState 读取社媒模块的持久化状态
func (s *SQLiteStorage) GetSocialState(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM social_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("读取 social_state 失败: %w", err)
	}
	return value, true, nil
}

// SetSocialState 写入社媒模块的持久化状态
func (s *SQLiteStorage) SetSocialState(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO social_state (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("写入 social_state 失败: %w", err)
	}
	return nil
}

// ===== Chat 管理（多平台） =====

// UpsertChat 创建或更新 Chat
func (s *SQLiteStorage) UpsertChat(ctx context.Context, chat *Chat) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chats (platform, chat_id, username, first_name, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, chat_id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			updated_at = excluded.updated_at
	`, chat.Platform, chat.ChatID, chat.Username, chat.FirstName, ChatStatusActive, now, now)
	if err != nil {
		return fmt.Errorf("创建/更新用户失败: %w", err)
	}
	return nil
}

// GetChat 获取 Chat
func (s *SQLiteStorage) GetChat(ctx context.Context, platform string, chatID int64) (*Chat, error) {
	chat := &Chat{}
	var lastCommandAt sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT platform, chat_id, username, first_name, status, last_command_at, command_count, created_at, updated_at
		FROM chats WHERE platform = ? AND chat_id = ?
	`, platform, chatID).Scan(
		&chat.Platform, &chat.ChatID, &chat.Username, &chat.FirstName, &chat.Status,
		&lastCommandAt, &chat.CommandCount, &chat.CreatedAt, &chat.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询用户失败: %w", err)
	}

	if lastCommandAt.Valid {
		chat.LastCommandAt = lastCommandAt.Int64
	}

	return chat, nil
}

// UpdateChatStatus 更新用户状态
func (s *SQLiteStorage) UpdateChatStatus(ctx context.Context, platform string, chatID int64, status string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE chats SET status = ?, updated_at = ? WHERE platform = ? AND chat_id = ?
	`, status, time.Now().Unix(), platform, chatID)
	if err != nil {
		return fmt.Errorf("更新用户状态失败: %w", err)
	}
	return nil
}

// UpdateChatCommandTime 更新用户命令时间
func (s *SQLiteStorage) UpdateChatCommandTime(ctx context.Context, platform string, chatID int64) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		UPDATE chats SET last_command_at = ?, command_count = command_count + 1, updated_at = ?
		WHERE platform = ? AND chat_id = ?
	`, now, now, platform, chatID)
	if err != nil {
		return fmt.Errorf("更新命令时间失败: %w", err)
	}
	return nil
}

// ===== 订阅管理 =====

// AddSubscription 添加订阅
func (s *SQLiteStorage) AddSubscription(ctx context.Context, sub *Subscription) error {
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO subscriptions (platform, chat_id, provider, service, channel, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(platform, chat_id, provider, service, channel) DO NOTHING
	`, sub.Platform, sub.ChatID, sub.Provider, sub.Service, sub.Channel, now)
	if err != nil {
		return fmt.Errorf("添加订阅失败: %w", err)
	}
	return nil
}

// RemoveSubscription 移除订阅
// 支持级联删除：
// - service=="" && channel=="" → 删除该 provider 下所有订阅
// - service!="" && channel=="" → 删除该 service 下所有通道（含精确和通配）
// - service!="" && channel!="" → 精确删除一条
func (s *SQLiteStorage) RemoveSubscription(ctx context.Context, platform string, chatID int64, provider, service, channel string) error {
	var err error

	if service == "" && channel == "" {
		// 级联删除：删除该 provider 下所有订阅
		_, err = s.db.ExecContext(ctx, `
			DELETE FROM subscriptions WHERE platform = ? AND chat_id = ? AND provider = ?
		`, platform, chatID, provider)
	} else if channel == "" {
		// 删除该 service 下所有通道（含精确和通配）
		_, err = s.db.ExecContext(ctx, `
			DELETE FROM subscriptions WHERE platform = ? AND chat_id = ? AND provider = ? AND service = ?
		`, platform, chatID, provider, service)
	} else {
		// 精确删除一条
		_, err = s.db.ExecContext(ctx, `
			DELETE FROM subscriptions WHERE platform = ? AND chat_id = ? AND provider = ? AND service = ? AND channel = ?
		`, platform, chatID, provider, service, channel)
	}

	if err != nil {
		return fmt.Errorf("移除订阅失败: %w", err)
	}
	return nil
}

// GetSubscriptionsByChatID 获取用户的所有订阅
func (s *SQLiteStorage) GetSubscriptionsByChatID(ctx context.Context, platform string, chatID int64) ([]*Subscription, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, platform, chat_id, provider, service, channel, created_at
		FROM subscriptions WHERE platform = ? AND chat_id = ? ORDER BY created_at DESC
	`, platform, chatID)
	if err != nil {
		return nil, fmt.Errorf("查询订阅失败: %w", err)
	}
	defer rows.Close()

	var subs []*Subscription
	for rows.Next() {
		sub := &Subscription{}
		if err := rows.Scan(&sub.ID, &sub.Platform, &sub.ChatID, &sub.Provider, &sub.Service, &sub.Channel, &sub.CreatedAt); err != nil {
			return nil, fmt.Errorf("扫描订阅失败: %w", err)
		}
		subs = append(subs, sub)
	}

	return subs, nil
}

// GetSubscribersByMonitor 获取监测项的所有订阅者（返回平台+ChatID）
// 匹配规则：
// - service 为空时匹配所有 service，否则精确匹配
// - channel 为空时匹配所有 channel，否则精确匹配
// 使用 DISTINCT 避免重复（用户可能同时有通配和精确订阅）
func (s *SQLiteStorage) GetSubscribersByMonitor(ctx context.Context, provider, service, channel string) ([]*ChatRef, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT s.platform, s.chat_id FROM subscriptions s
		JOIN chats c ON s.platform = c.platform AND s.chat_id = c.chat_id
		WHERE s.provider = ?
		  AND (s.service = '' OR s.service = ?)
		  AND (s.channel = '' OR s.channel = ?)
		  AND c.status = 'active'
	`, provider, service, channel)
	if err != nil {
		return nil, fmt.Errorf("查询订阅者失败: %w", err)
	}
	defer rows.Close()

	var refs []*ChatRef
	for rows.Next() {
		ref := &ChatRef{}
		if err := rows.Scan(&ref.Platform, &ref.ChatID); err != nil {
			return nil, fmt.Errorf("扫描订阅者失败: %w", err)
		}
		refs = append(refs, ref)
	}

	return refs, nil
}

// CountSubscriptions 统计用户订阅数
func (s *SQLiteStorage) CountSubscriptions(ctx context.Context, platform string, chatID int64) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM subscriptions WHERE platform = ? AND chat_id = ?`,
		platform, chatID,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("统计订阅数失败: %w", err)
	}
	return count, nil
}

// ClearSubscriptions 清空用户所有订阅
func (s *SQLiteStorage) ClearSubscriptions(ctx context.Context, platform string, chatID int64) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM subscriptions WHERE platform = ? AND chat_id = ?`,
		platform, chatID,
	)
	if err != nil {
		return fmt.Errorf("清空订阅失败: %w", err)
	}
	return nil
}

// ===== 绑定 Token 管理 =====

// CreateBindToken 创建绑定 token
func (s *SQLiteStorage) CreateBindToken(ctx context.Context, token *BindToken) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO bind_tokens (token, favorites, expires_at, created_at)
		VALUES (?, ?, ?, ?)
	`, token.Token, token.Favorites, token.ExpiresAt, token.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建绑定 token 失败: %w", err)
	}
	return nil
}

// GetBindToken 获取绑定 token
func (s *SQLiteStorage) GetBindToken(ctx context.Context, token string) (*BindToken, error) {
	bt := &BindToken{}
	var usedAt sql.NullInt64

	err := s.db.QueryRowContext(ctx, `
		SELECT token, favorites, expires_at, used_at, created_at
		FROM bind_tokens WHERE token = ?
	`, token).Scan(&bt.Token, &bt.Favorites, &bt.ExpiresAt, &usedAt, &bt.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询绑定 token 失败: %w", err)
	}

	if usedAt.Valid {
		bt.UsedAt = usedAt.Int64
	}

	return bt, nil
}

// ConsumeBindToken 消费绑定 token
func (s *SQLiteStorage) ConsumeBindToken(ctx context.Context, token string) (*BindToken, error) {
	now := time.Now().Unix()

	// 使用事务确保原子性
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("开始事务失败: %w", err)
	}
	defer tx.Rollback()

	bt := &BindToken{}
	var usedAt sql.NullInt64

	err = tx.QueryRowContext(ctx, `
		SELECT token, favorites, expires_at, used_at, created_at
		FROM bind_tokens WHERE token = ?
	`, token).Scan(&bt.Token, &bt.Favorites, &bt.ExpiresAt, &usedAt, &bt.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询绑定 token 失败: %w", err)
	}

	// 检查是否已过期
	if bt.ExpiresAt < now {
		return nil, fmt.Errorf("token 已过期")
	}

	// 检查是否已使用
	if usedAt.Valid {
		return nil, fmt.Errorf("token 已使用")
	}

	// 标记已使用
	_, err = tx.ExecContext(ctx, `UPDATE bind_tokens SET used_at = ? WHERE token = ?`, now, token)
	if err != nil {
		return nil, fmt.Errorf("标记 token 已使用失败: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	bt.UsedAt = now
	return bt, nil
}

// CleanupExpiredTokens 清理过期 token
func (s *SQLiteStorage) CleanupExpiredTokens(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM bind_tokens WHERE expires_at < ?`, time.Now().Unix())
	if err != nil {
		return 0, fmt.Errorf("清理过期 token 失败: %w", err)
	}
	return result.RowsAffected()
}

// ===== 投递记录管理 =====

// CreateDelivery 创建投递记录
func (s *SQLiteStorage) CreateDelivery(ctx context.Context, delivery *Delivery) error {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO deliveries (event_id, platform, chat_id, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id, platform, chat_id) DO NOTHING
	`, delivery.EventID, delivery.Platform, delivery.ChatID, DeliveryStatusPending, now, now)
	if err != nil {
		return fmt.Errorf("创建投递记录失败: %w", err)
	}

	id, _ := result.LastInsertId()
	delivery.ID = id
	delivery.CreatedAt = now
	delivery.UpdatedAt = now

	return nil
}

// UpdateDeliveryStatus 更新投递状态
func (s *SQLiteStorage) UpdateDeliveryStatus(ctx context.Context, id int64, status string, messageID string, errorMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = ?, message_id = ?, error_message = ?, updated_at = ? WHERE id = ?
	`, status, messageID, errorMsg, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("更新投递状态失败: %w", err)
	}
	return nil
}

// GetPendingDeliveries 获取待发送的投递记录
func (s *SQLiteStorage) GetPendingDeliveries(ctx context.Context, limit int) ([]*Delivery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, event_id, platform, chat_id, status, message_id, error_message, retry_count, created_at, updated_at
		FROM deliveries WHERE status = 'pending' ORDER BY created_at ASC LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待发送投递失败: %w", err)
	}
	defer rows.Close()

	var deliveries []*Delivery
	for rows.Next() {
		d := &Delivery{}
		var messageID, errorMessage sql.NullString
		if err := rows.Scan(&d.ID, &d.EventID, &d.Platform, &d.ChatID, &d.Status, &messageID, &errorMessage, &d.RetryCount, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("扫描投递记录失败: %w", err)
		}
		if messageID.Valid {
			d.MessageID = messageID.String
		}
		if errorMessage.Valid {
			d.ErrorMessage = errorMessage.String
		}
		deliveries = append(deliveries, d)
	}

	return deliveries, nil
}

// IncrementRetryCount 增加重试次数
func (s *SQLiteStorage) IncrementRetryCount(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET retry_count = retry_count + 1, updated_at = ? WHERE id = ?
	`, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("增加重试次数失败: %w", err)
	}
	return nil
}

// CleanupOldDeliveries 清理旧的投递记录
func (s *SQLiteStorage) CleanupOldDeliveries(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM deliveries WHERE created_at < ? AND status IN ('sent', 'failed')
	`, before.Unix())
	if err != nil {
		return 0, fmt.Errorf("清理旧投递记录失败: %w", err)
	}
	return result.RowsAffected()
}
