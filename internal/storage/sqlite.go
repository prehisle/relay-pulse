package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // 纯Go实现的SQLite驱动
)

// SQLiteStorage SQLite存储实现
type SQLiteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage 创建SQLite存储
func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	// 使用WAL模式和其他参数解决并发锁问题
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_timeout=5000&_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}

	// 设置连接池参数（WAL模式支持更好的并发）
	db.SetMaxOpenConns(1)  // SQLite建议单个写连接
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(time.Hour)

	return &SQLiteStorage{db: db}, nil
}

// Init 初始化数据库表
func (s *SQLiteStorage) Init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS probe_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL,
		sub_status TEXT NOT NULL DEFAULT '',
		latency INTEGER NOT NULL,
		timestamp INTEGER NOT NULL
	);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("初始化数据库失败: %w", err)
	}

	// 兼容旧数据库：添加缺失的列
	if err := s.ensureSubStatusColumn(); err != nil {
		return err
	}
	if err := s.ensureChannelColumn(); err != nil {
		return err
	}

	// 在列迁移完成后创建索引
	indexSQL := `
	CREATE INDEX IF NOT EXISTS idx_provider_service_channel_timestamp
	ON probe_history(provider, service, channel, timestamp DESC);
	`
	if _, err := s.db.Exec(indexSQL); err != nil {
		return fmt.Errorf("创建索引失败: %w", err)
	}

	return nil
}

// ensureSubStatusColumn 在旧表上添加 sub_status 列（向后兼容）
func (s *SQLiteStorage) ensureSubStatusColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "sub_status" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.Exec(`ALTER TABLE probe_history ADD COLUMN sub_status TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 sub_status 列失败: %w", err)
	}

	fmt.Println("[Storage] 已为 probe_history 表添加 sub_status 列")
	return nil
}

// ensureChannelColumn 在旧表上添加 channel 列（向后兼容）
func (s *SQLiteStorage) ensureChannelColumn() error {
	rows, err := s.db.Query(`PRAGMA table_info(probe_history)`)
	if err != nil {
		return fmt.Errorf("查询表结构失败: %w", err)
	}
	defer rows.Close()

	hasColumn := false
	for rows.Next() {
		var (
			cid          int
			name         string
			colType      string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return fmt.Errorf("扫描表结构失败: %w", err)
		}
		if name == "channel" {
			hasColumn = true
			break
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历表结构失败: %w", err)
	}

	if hasColumn {
		return nil // 列已存在，无需添加
	}

	// 添加列
	if _, err := s.db.Exec(`ALTER TABLE probe_history ADD COLUMN channel TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("添加 channel 列失败: %w", err)
	}

	fmt.Println("[Storage] 已为 probe_history 表添加 channel 列")
	return nil
}

// Close 关闭数据库
func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// SaveRecord 保存探测记录
func (s *SQLiteStorage) SaveRecord(record *ProbeRecord) error {
	query := `
		INSERT INTO probe_history (provider, service, channel, status, sub_status, latency, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(query,
		record.Provider,
		record.Service,
		record.Channel,
		record.Status,
		string(record.SubStatus),
		record.Latency,
		record.Timestamp,
	)

	if err != nil {
		return fmt.Errorf("保存记录失败: %w", err)
	}

	id, _ := result.LastInsertId()
	record.ID = id
	return nil
}

// GetLatest 获取最新记录
func (s *SQLiteStorage) GetLatest(provider, service, channel string) (*ProbeRecord, error) {
	query := `
		SELECT id, provider, service, channel, status, sub_status, latency, timestamp
		FROM probe_history
		WHERE provider = ? AND service = ? AND channel = ?
		ORDER BY timestamp DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.db.QueryRow(query, provider, service, channel).Scan(
		&record.ID,
		&record.Provider,
		&record.Service,
		&record.Channel,
		&record.Status,
		&subStatusStr,
		&record.Latency,
		&record.Timestamp,
	)

	if err == sql.ErrNoRows {
		return nil, nil // 没有记录不算错误
	}

	if err != nil {
		return nil, fmt.Errorf("查询最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	return &record, nil
}

// GetHistory 获取历史记录
func (s *SQLiteStorage) GetHistory(provider, service, channel string, since time.Time) ([]*ProbeRecord, error) {
	query := `
		SELECT id, provider, service, channel, status, sub_status, latency, timestamp
		FROM probe_history
		WHERE provider = ? AND service = ? AND channel = ? AND timestamp >= ?
		ORDER BY timestamp ASC
	`

	rows, err := s.db.Query(query, provider, service, channel, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询历史记录失败: %w", err)
	}
	defer rows.Close()

	var records []*ProbeRecord
	for rows.Next() {
		var record ProbeRecord
		var subStatusStr string
		err := rows.Scan(
			&record.ID,
			&record.Provider,
			&record.Service,
			&record.Channel,
			&record.Status,
			&subStatusStr,
			&record.Latency,
			&record.Timestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("扫描记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	// 检查迭代过程中是否发生错误
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代记录失败: %w", err)
	}

	return records, nil
}

// CleanOldRecords 清理旧记录
func (s *SQLiteStorage) CleanOldRecords(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	query := `DELETE FROM probe_history WHERE timestamp < ?`

	result, err := s.db.Exec(query, cutoff)
	if err != nil {
		return fmt.Errorf("清理旧记录失败: %w", err)
	}

	deleted, _ := result.RowsAffected()
	if deleted > 0 {
		fmt.Printf("[Storage] 已清理 %d 条超过 %d 天的旧记录\n", deleted, days)
	}

	return nil
}
