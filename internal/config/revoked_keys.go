package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// revokedKeyFileMaxBytes 名单文件体积上限。100 条摘要约 6.5 KB，1 MiB 留足余量，
// 同时防 FIFO/设备文件或误配的巨文件把配置加载拖死。
const revokedKeyFileMaxBytes = 1 << 20

// resolveRevokedKeyFilePath 把配置里的名单文件名解析为绝对路径。
//
// 只接受**主配置目录下的直接子文件名**（不接受绝对路径、子目录、`..` 穿越）。这条约束不是洁癖：
// 配置监听器对主配置目录是**非递归**监听，只有直接子文件的变更才会产生 fsnotify 事件。
// 若允许任意路径，就会出现「配置能加载到、改动却永远不触发热更新」的静默不一致。
//
// 返回空字符串表示未配置（功能关闭）。
func resolveRevokedKeyFilePath(configDir, configured string) (string, error) {
	name := strings.TrimSpace(configured)
	if name == "" {
		return "", nil
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("revoked_key_file 必须是主配置目录下的文件名，不能是绝对路径")
	}
	if cleaned := filepath.Clean(name); cleaned != filepath.Base(cleaned) || cleaned == "." {
		return "", fmt.Errorf("revoked_key_file 必须是主配置目录下的直接子文件，不能含目录分隔或 ..")
	}
	return filepath.Join(configDir, filepath.Clean(name)), nil
}

// loadRevokedKeyFile 加载「已公开泄露的监测 API Key」拒绝名单。
//
// 名单是每行一个 sha256(明文 api_key) 的小写十六进制摘要；`#` 开头为注释，空行忽略。
// 用**无密钥 SHA-256**而非 apikey.KeyCipher 的 HMAC 指纹，是刻意的：这些 key 已经公开，
// 其摘要不构成新增泄露，而无密钥摘要让名单可以离线生成、不必把生产 encryption_key
// 拽进名单制作流程。若将来名单要收录**尚未公开**的凭据，这个前提不再成立，须改用带 pepper 的摘要。
//
// 任何读取或内容错误都返回 error，由调用方（Loader.Load）让整次加载失败：
// 启动期即拒绝启动，热更新期由 loadOrRollback 保留上一份完整配置（含上一份名单）。
// 失败时不留半份名单——静默接受一份变短的名单等于让部分已泄露 key 复活。
func (c *AppConfig) loadRevokedKeyFile(configDir string) error {
	c.ChangeRequests.RevokedKeySHA256 = nil

	path, err := resolveRevokedKeyFilePath(configDir, c.ChangeRequests.RevokedKeyFile)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}

	// 期望条目数是防「部分写入/意外截断」的唯一手段：截断后的文件前缀往往仍是合法摘要行，
	// 只靠格式校验发现不了。要求显式声明条目数，让数量变化必须是有意为之。
	if c.ChangeRequests.RevokedKeyCount <= 0 {
		return fmt.Errorf("配置了 revoked_key_file 时 revoked_key_count 必须为正数（当前 %d）", c.ChangeRequests.RevokedKeyCount)
	}

	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("读取拒绝名单文件属性失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("拒绝名单 %s 必须是普通文件", c.ChangeRequests.RevokedKeyFile)
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("打开拒绝名单文件失败: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, revokedKeyFileMaxBytes+1))
	if err != nil {
		return fmt.Errorf("读取拒绝名单文件失败: %w", err)
	}
	if len(data) > revokedKeyFileMaxBytes {
		return fmt.Errorf("拒绝名单文件超过 %d 字节上限", revokedKeyFileMaxBytes)
	}

	hashes := make(map[string]struct{}, c.ChangeRequests.RevokedKeyCount)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 只报行号，绝不回显行内容——非法行有可能正是一把误粘进来的明文密钥。
		decoded, decodeErr := hex.DecodeString(line)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("拒绝名单第 %d 行不是合法的 sha256 十六进制摘要", i+1)
		}
		hashes[strings.ToLower(line)] = struct{}{}
	}

	if len(hashes) != c.ChangeRequests.RevokedKeyCount {
		return fmt.Errorf("拒绝名单去重后 %d 条，与 revoked_key_count=%d 不一致",
			len(hashes), c.ChangeRequests.RevokedKeyCount)
	}

	c.ChangeRequests.RevokedKeySHA256 = hashes
	return nil
}

// RevokedKeySHA256Hex 返回明文 API Key 在拒绝名单哈希空间里的摘要。
// 供 change 包在认证时比对，保证两侧算法只有这一处定义。
func RevokedKeySHA256Hex(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(sum[:])
}
