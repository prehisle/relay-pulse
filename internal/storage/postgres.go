package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"monitor/internal/config"
)

// PostgresStorage PostgreSQL 存储实现
type PostgresStorage struct {
	pool *pgxpool.Pool
	ctx  context.Context
}

// NewPostgresStorage 创建 PostgreSQL 存储
func NewPostgresStorage(cfg *config.PostgresConfig) (*PostgresStorage, error) {
	// 构建连接字符串
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host,
		cfg.Port,
		cfg.User,
		cfg.Password,
		cfg.Database,
		cfg.SSLMode,
	)

	// 解析连接池配置
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 PostgreSQL 连接配置失败: %w", err)
	}

	// 设置连接池参数
	poolConfig.MaxConns = int32(cfg.MaxOpenConns)
	poolConfig.MinConns = int32(cfg.MaxIdleConns)

	// 解析连接最大生命周期
	if cfg.ConnMaxLifetime != "" {
		lifetime, err := time.ParseDuration(cfg.ConnMaxLifetime)
		if err != nil {
			log.Printf("[Storage] 警告: 解析 conn_max_lifetime 失败，使用默认值 1h: %v", err)
			lifetime = time.Hour
		}
		poolConfig.MaxConnLifetime = lifetime
	} else {
		poolConfig.MaxConnLifetime = time.Hour
	}

	// 创建连接池
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("创建 PostgreSQL 连接池失败: %w", err)
	}

	// 测试连接
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	return &PostgresStorage{
		pool: pool,
		ctx:  ctx,
	}, nil
}

// Init 初始化数据库表
func (s *PostgresStorage) Init() error {
	schema := `
	CREATE TABLE IF NOT EXISTS probe_history (
		id BIGSERIAL PRIMARY KEY,
		provider TEXT NOT NULL,
		service TEXT NOT NULL,
		channel TEXT NOT NULL DEFAULT '',
		status INTEGER NOT NULL,
		sub_status TEXT NOT NULL DEFAULT '',
		latency INTEGER NOT NULL,
		timestamp BIGINT NOT NULL
	);
	`

	_, err := s.pool.Exec(s.ctx, schema)
	if err != nil {
		return fmt.Errorf("初始化 PostgreSQL 数据库失败: %w", err)
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
	if _, err := s.pool.Exec(s.ctx, indexSQL); err != nil {
		return fmt.Errorf("创建索引失败: %w", err)
	}

	return nil
}

// ensureSubStatusColumn 在旧表上添加 sub_status 列（向后兼容）
func (s *PostgresStorage) ensureSubStatusColumn() error {
	// PostgreSQL 使用 information_schema 查询列是否存在
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = 'probe_history' AND column_name = 'sub_status'
	`

	var count int
	err := s.pool.QueryRow(s.ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN sub_status TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(s.ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 sub_status 列失败: %w", err)
	}

	log.Println("[Storage] 已为 probe_history 表添加 sub_status 列 (PostgreSQL)")
	return nil
}

// ensureChannelColumn 在旧表上添加 channel 列（向后兼容）
func (s *PostgresStorage) ensureChannelColumn() error {
	checkQuery := `
		SELECT COUNT(*)
		FROM information_schema.columns
		WHERE table_name = 'probe_history' AND column_name = 'channel'
	`

	var count int
	err := s.pool.QueryRow(s.ctx, checkQuery).Scan(&count)
	if err != nil {
		return fmt.Errorf("查询 PostgreSQL 表结构失败: %w", err)
	}

	if count > 0 {
		return nil // 列已存在，无需添加
	}

	// 添加列
	alterQuery := `ALTER TABLE probe_history ADD COLUMN channel TEXT NOT NULL DEFAULT ''`
	if _, err := s.pool.Exec(s.ctx, alterQuery); err != nil {
		return fmt.Errorf("添加 channel 列失败: %w", err)
	}

	log.Println("[Storage] 已为 probe_history 表添加 channel 列 (PostgreSQL)")
	return nil
}

// Close 关闭数据库连接
func (s *PostgresStorage) Close() error {
	s.pool.Close()
	return nil
}

// SaveRecord 保存探测记录
func (s *PostgresStorage) SaveRecord(record *ProbeRecord) error {
	query := `
		INSERT INTO probe_history (provider, service, channel, status, sub_status, latency, timestamp)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`

	err := s.pool.QueryRow(s.ctx, query,
		record.Provider,
		record.Service,
		record.Channel,
		record.Status,
		string(record.SubStatus),
		record.Latency,
		record.Timestamp,
	).Scan(&record.ID)

	if err != nil {
		return fmt.Errorf("保存 PostgreSQL 记录失败: %w", err)
	}

	return nil
}

// GetLatest 获取最新记录
func (s *PostgresStorage) GetLatest(provider, service, channel string) (*ProbeRecord, error) {
	query := `
		SELECT id, provider, service, channel, status, sub_status, latency, timestamp
		FROM probe_history
		WHERE provider = $1 AND service = $2 AND channel = $3
		ORDER BY timestamp DESC
		LIMIT 1
	`

	var record ProbeRecord
	var subStatusStr string
	err := s.pool.QueryRow(s.ctx, query, provider, service, channel).Scan(
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
		// pgx 使用 ErrNoRows 的方式不同，需要检查错误消息
		if err.Error() == "no rows in result set" {
			return nil, nil // 没有记录不算错误
		}
		return nil, fmt.Errorf("查询 PostgreSQL 最新记录失败: %w", err)
	}

	record.SubStatus = SubStatus(subStatusStr)
	return &record, nil
}

// GetHistory 获取历史记录
func (s *PostgresStorage) GetHistory(provider, service, channel string, since time.Time) ([]*ProbeRecord, error) {
	query := `
		SELECT id, provider, service, channel, status, sub_status, latency, timestamp
		FROM probe_history
		WHERE provider = $1 AND service = $2 AND channel = $3 AND timestamp >= $4
		ORDER BY timestamp ASC
	`

	rows, err := s.pool.Query(s.ctx, query, provider, service, channel, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("查询 PostgreSQL 历史记录失败: %w", err)
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
			return nil, fmt.Errorf("扫描 PostgreSQL 记录失败: %w", err)
		}
		record.SubStatus = SubStatus(subStatusStr)
		records = append(records, &record)
	}

	// 检查迭代过程中是否发生错误
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代 PostgreSQL 记录失败: %w", err)
	}

	return records, nil
}

// CleanOldRecords 清理旧记录
func (s *PostgresStorage) CleanOldRecords(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days).Unix()
	query := `DELETE FROM probe_history WHERE timestamp < $1`

	result, err := s.pool.Exec(s.ctx, query, cutoff)
	if err != nil {
		return fmt.Errorf("清理 PostgreSQL 旧记录失败: %w", err)
	}

	deleted := result.RowsAffected()
	if deleted > 0 {
		log.Printf("[Storage] 已清理 %d 条超过 %d 天的旧记录 (PostgreSQL)\n", deleted, days)
	}

	return nil
}
