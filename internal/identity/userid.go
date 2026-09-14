// Package identity 管理运行时用户标识，与配置加载/校验无关。
package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// UserIDManager 按通道维度管理 user_id，支持确定性生成和定期刷新
type UserIDManager struct {
	mu      sync.Mutex
	entries map[string]*userIDEntry
}

type userIDEntry struct {
	id        string
	hash      string
	expiresAt time.Time
}

// NewUserIDManager 创建 UserIDManager
func NewUserIDManager() *UserIDManager {
	return &UserIDManager{
		entries: make(map[string]*userIDEntry),
	}
}

// GetUserIDPair 原子获取 user_id 和 hash（确保同一次调用中两者一致）
func (m *UserIDManager) GetUserIDPair(provider, service, channel string, refreshMinutes int) (string, string) {
	return m.resolve(provider, service, channel, refreshMinutes)
}

func (m *UserIDManager) resolve(provider, service, channel string, refreshMinutes int) (string, string) {
	channelKey := strings.TrimSpace(fmt.Sprintf("%s/%s/%s", provider, service, channel))
	cacheKey := fmt.Sprintf("%s|%d", channelKey, refreshMinutes)

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	if entry, ok := m.entries[cacheKey]; ok {
		if refreshMinutes <= 0 || now.Before(entry.expiresAt) {
			return entry.id, entry.hash
		}
	}

	// 生成哈希
	var hash string
	if refreshMinutes <= 0 {
		hash = sha256Hex(channelKey)
	} else {
		period := time.Duration(refreshMinutes) * time.Minute
		periodStart := now.Truncate(period)
		hash = sha256Hex(fmt.Sprintf("%s|%d", channelKey, periodStart.Unix()))
	}

	id := formatUserID(hash)

	var expiresAt time.Time
	if refreshMinutes > 0 {
		period := time.Duration(refreshMinutes) * time.Minute
		expiresAt = now.Truncate(period).Add(period)
	}

	m.entries[cacheKey] = &userIDEntry{
		id:        id,
		hash:      hash,
		expiresAt: expiresAt,
	}

	return id, hash
}

func sha256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// formatUserID 生成与生产一致的 user_id 格式
// 格式: user_<sha256_64hex>_account__session_<uuid_format>
func formatUserID(hash string) string {
	uuid := uuidFromHash(hash)
	return fmt.Sprintf("user_%s_account__session_%s", hash, uuid)
}

// uuidFromHash 从哈希中提取 UUID 格式字符串
func uuidFromHash(hash string) string {
	if len(hash) < 32 {
		hash = sha256Hex(hash)
	}
	s := hash[:32]
	return fmt.Sprintf("%s-%s-%s-%s-%s", s[0:8], s[8:12], s[12:16], s[16:20], s[20:32])
}

// DeriveUUID 用 hash + salt 派生一个稳定的 UUID，不同 salt 产生不同结果。
func DeriveUUID(hash, salt string) string {
	salted := sha256Hex(hash + salt)
	return uuidFromHash(salted)
}

// DeriveUUIDv4 同样按 hash + salt 派生稳定值，但产出的是**格式合法**的 UUID
// （version=4、variant=RFC4122）。
//
// 探测模板要伪装成真实客户端发出的标识时必须用本函数：真客户端发的是合法
// UUID，而 DeriveUUID 把 sha256 直接切成 8-4-4-4-12 的形状、不设 version/
// variant 位，产出的串通不过格式校验（线上实测见过 variant=3，合法值只能是
// 8/9/a/b），在任何做解析校验的网关那里都是破绽。
//
// ⚠️ 本函数**不是** DeriveUUID 的「修了格式位」版本，两者产出完全无关：
// 除格式位外，哈希输入也不同（DeriveUUID 用 hash+salt，本函数用 hash+"|"+salt）。
// 别指望拿它复现旧值。
//
// ⚠️ salt 分隔符只保证**固定 salt + 定长 hash** 这个调用域内无歧义：拼接对任意
// 字符串并非单射（hash="a|",salt="b" 与 hash="a",salt="|b" 会撞）。当前调用方
// 传的 hash 恒为 64 位十六进制、salt 是代码里的字面常量，不构成风险；若将来要
// 开放成通用 API 接受任意输入，得改成带长度前缀的编码。
//
// DeriveUUID 刻意保持原状：cc-haiku-arith-20260506 的 {{USER_ACCOUNT_UUID}}
// 已在生产使用，修它的格式位会改变该模板实际发出的值。
func DeriveUUIDv4(hash, salt string) string {
	sum := sha256.Sum256([]byte(hash + "|" + salt))
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0f) | 0x40 // version 4
	u[8] = (u[8] & 0x3f) | 0x80 // variant RFC4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}
