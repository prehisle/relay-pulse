package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 展示名唯一：同一 (provider 展示名, service, channel 展示名) 只能属于一条通道（provider + channel slug）。
//
// rpdiag 按这组展示名加它自己选的模型给通道建身份，不认 slug、也不看监测行的 model。两条
// slug 不同、展示名相同的通道在 rpdiag 会撞成同一行，后来那条永远采不到。loader 在加载时
// 拒绝这种配置（validateMonitorDisplayKeyUniqueness），monitors.d 写路径在落盘前拒绝
// （MonitorStore.checkDisplayNameConflict），两处共用下面的判定口径。

// DisplayNameConflictError 表示一组展示名同时属于两条不同的通道。
// 独立类型：monitors.d 写路径据此拒绝落盘，api 层用 errors.As 映射成 409。
type DisplayNameConflictError struct {
	ProviderName string
	Service      string
	ChannelName  string
	First        string // 先出现的一方位置
	Second       string // 冲突的一方位置
}

// Error 对零值实例也安全（errors.As 的 target 可能是调用方声明的空指针）。
func (e *DisplayNameConflictError) Error() string {
	if e == nil {
		return "展示名重复"
	}
	return fmt.Sprintf(
		"展示名重复: %s/%s/%s 同时属于 %s 与 %s。rpdiag 按 provider/service/channel_name 识别通道，二者会被当成同一条；请给其中一条改 channel_name 使之唯一",
		e.ProviderName, e.Service, e.ChannelName, e.First, e.Second,
	)
}

// displayKey 用结构体而非拼接字符串：展示名允许含 "/"，拼接会让不同身份撞成同一串。
type displayKey struct{ provider, service, channel string }

type displayOwner struct {
	provider, channel string // slug
	where             string
}

// displayIdentity 返回监测行的展示键与归属通道。m 须已 trim 展示名并完成父子继承；
// 展示名为空时回退 slug，与 /api/status 下发、rpdiag 读取的口径一致。
func displayIdentity(m ServiceConfig, where string) (displayKey, displayOwner) {
	provider := m.ProviderName
	if provider == "" {
		provider = m.Provider
	}
	channel := m.ChannelName
	if channel == "" {
		channel = m.Channel
	}
	return displayKey{provider: provider, service: m.Service, channel: channel},
		displayOwner{provider: m.Provider, channel: m.Channel, where: where}
}

// conflict 在两方属于不同通道时返回 *DisplayNameConflictError。同一通道的多条监测行
// （父子、多模型）共享 slug，不算冲突。
func (k displayKey) conflict(first, second displayOwner) error {
	if first.provider == second.provider && first.channel == second.channel {
		return nil
	}
	return &DisplayNameConflictError{
		ProviderName: k.provider,
		Service:      k.service,
		ChannelName:  k.channel,
		First:        first.where,
		Second:       second.where,
	}
}

// displayRow 是写前检查用的一条监测行投影。
type displayRow struct {
	monitor ServiceConfig
	where   string
	// affected 表示这条行的最终展示身份受本次写入影响：属于目标文件，或继承自目标文件的根。
	affected bool
	resolved bool // 子行找到了唯一父行（或本身是根行）
}

// checkDisplayNameConflict 在写盘前检查 file 写到 targetPath 后是否与 monitors.d/ 里其它
// 通道撞展示名。调用方须持有 s.mu。
//
// 口径镜像 loader：子行 slug 由 preprocessParentInheritance 按 parent 补全；展示名先 trim，
// 空的再从父根继承。只报告受本次写入影响的冲突，磁盘上既有的 other-vs-other 冲突不阻断
// 无关编辑（否则连修复它的写入都会被拒）。
//
// 这是尽力预检，不等价于完整加载：读不出的文件、config.yaml 内联监测行、父行不在
// monitors.d/ 的子行都看不到；也不防同进程外（CLI、手工编辑）的并发写。完整合法性仍由
// loader 在热更新时 fail-closed 把关。
func (s *MonitorStore) checkDisplayNameConflict(targetPath string, file *MonitorFile) error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("读取 %s 失败，无法检查展示名: %w", MonitorsDirName, err)
	}

	rows := displayRowsOf(file.Monitors, filepath.Base(targetPath), true)
	for _, entry := range entries {
		if entry.IsDir() || !isMonitorDefinitionFile(entry.Name()) {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		if path == targetPath {
			// Update：这份文件将被 file 替换。不能留着旧内容：旧根行与新根行同路径，
			// 会让父行索引判成歧义，新子行因此无法解析、漏检。
			continue
		}
		// 读不出、或推不出唯一 PSC 的文件由 loader 拒绝；在这里报错会堵住所有无关写入。
		other, err := loadMonitorFile(path)
		if err != nil {
			continue
		}
		if _, err := DeriveMonitorFileKey(other); err != nil {
			continue
		}
		rows = append(rows, displayRowsOf(other.Monitors, entry.Name(), false)...)
	}

	resolveDisplayInheritance(rows)
	return firstAffectedDisplayConflict(rows)
}

// displayRowsOf 把一份文件的监测行投影成 displayRow：补全子行 slug、trim 展示名。
// parent 写法非法的文件无法确定身份，整份跳过（loader 会拒绝它）。
func displayRowsOf(monitors []ServiceConfig, fileName string, affected bool) []displayRow {
	tmp := AppConfig{Monitors: append([]ServiceConfig(nil), monitors...)}
	if err := tmp.preprocessParentInheritance(); err != nil {
		return nil
	}
	rows := make([]displayRow, 0, len(tmp.Monitors))
	for i, m := range tmp.Monitors {
		m.ProviderName = strings.TrimSpace(m.ProviderName)
		m.ChannelName = strings.TrimSpace(m.ChannelName)
		rows = append(rows, displayRow{
			monitor:  m,
			where:    fmt.Sprintf("%s monitor[%d] %s/%s/%s", fileName, i, m.Provider, m.Service, m.Channel),
			affected: affected,
		})
	}
	return rows
}

// resolveDisplayInheritance 让子行从父根继承空展示名，并传播 affected。
// 与 applyParentInheritance 一致：同一路径有多个根行视为歧义，挂在它下面的子行无法解析。
func resolveDisplayInheritance(rows []displayRow) {
	const ambiguous = -1
	roots := make(map[string]int)
	for i := range rows {
		m := rows[i].monitor
		if strings.TrimSpace(m.Parent) != "" {
			continue
		}
		path := fmt.Sprintf("%s/%s/%s", m.Provider, m.Service, m.Channel)
		if _, exists := roots[path]; exists {
			roots[path] = ambiguous
		} else {
			roots[path] = i
		}
		rows[i].resolved = true
	}

	for i := range rows {
		child := &rows[i]
		parentPath := strings.TrimSpace(child.monitor.Parent)
		if parentPath == "" {
			continue
		}
		j, ok := roots[parentPath]
		if !ok || j == ambiguous {
			continue // 父行不在 monitors.d/ 或有歧义，身份无法确定
		}
		parent := rows[j]
		if child.monitor.ProviderName == "" {
			child.monitor.ProviderName = parent.monitor.ProviderName
		}
		if child.monitor.ChannelName == "" {
			child.monitor.ChannelName = parent.monitor.ChannelName
		}
		child.affected = child.affected || parent.affected
		child.resolved = true
	}
}

// firstAffectedDisplayConflict 先登记受影响的行（它们之间的冲突也在这一步报出），
// 再拿其余行去比；其余行之间的既有冲突不报告。
func firstAffectedDisplayConflict(rows []displayRow) error {
	seen := make(map[displayKey]displayOwner)
	for _, r := range rows {
		if !r.resolved || !r.affected {
			continue
		}
		key, cur := displayIdentity(r.monitor, r.where)
		prev, exists := seen[key]
		if !exists {
			seen[key] = cur
			continue
		}
		if err := key.conflict(prev, cur); err != nil {
			return err
		}
	}
	for _, r := range rows {
		if !r.resolved || r.affected {
			continue
		}
		key, cur := displayIdentity(r.monitor, r.where)
		if prev, exists := seen[key]; exists {
			if err := key.conflict(prev, cur); err != nil {
				return err
			}
		}
	}
	return nil
}
