package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MonitorStore 提供 monitors.d/ 文件级 CRUD 操作。
// 所有写操作通过 mutex 串行化，使用 AtomicWriteYAML 确保崩溃安全。
// 写入后由 fsnotify 自动触发热更新，无需手动调用 reload。
type MonitorStore struct {
	dir string     // monitors.d/ 绝对路径
	mu  sync.Mutex // 写操作串行化
}

// NewMonitorStore 创建 MonitorStore。dir 是 monitors.d/ 的绝对路径。
func NewMonitorStore(dir string) *MonitorStore {
	return &MonitorStore{dir: dir}
}

// Dir 返回 monitors.d/ 目录路径。
func (s *MonitorStore) Dir() string {
	return s.dir
}

// validateKeySegment 校验 PSC 字段不含路径分隔符或目录穿越字符。
func validateKeySegment(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s 不能为空", field)
	}
	if strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("%s 不能包含路径分隔符", field)
	}
	if value == "." || value == ".." || strings.Contains(value, "..") {
		return fmt.Errorf("%s 不能包含 '..'", field)
	}
	return nil
}

// SanitizeMonitorKey 规范化并校验 monitor file key，防止路径穿越。
func SanitizeMonitorKey(key string) (string, error) {
	key = strings.ToLower(strings.TrimSpace(key))
	provider, service, channel, err := ParseMonitorFileKey(key)
	if err != nil {
		return "", err
	}
	if err := validateKeySegment("provider", provider); err != nil {
		return "", err
	}
	if err := validateKeySegment("service", service); err != nil {
		return "", err
	}
	if err := validateKeySegment("channel", channel); err != nil {
		return "", err
	}
	return MonitorFileKeyFromPSC(provider, service, channel), nil
}

// findExistingPath 查找 key 对应的 .yaml 或 .yml 文件。
func (s *MonitorStore) findExistingPath(key string) (string, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(s.dir, key+ext)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return "", nil
}

// MonitorSummary 是列表 API 返回的精简摘要。
type MonitorSummary struct {
	Key         string `json:"key"`
	ChannelID   string `json:"channel_id,omitempty"` // 通道稳定 id（跨产品 join 锚，供 rpdiag sampler 发现）
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	Channel     string `json:"channel"`
	ChannelName string `json:"channel_name,omitempty"`
	ModelCount  int    `json:"model_count"`
	Disabled    bool   `json:"disabled"`
	Hidden      bool   `json:"hidden"`
	Board       string `json:"board"`
	Category    string `json:"category"`
	Template    string `json:"template"`
	Source      string `json:"source"`
	Revision    int64  `json:"revision"`
	UpdatedAt   string `json:"updated_at"`

	// LatestProbe 是该监测项下所有 model 最新一条探测记录的快照（按 timestamp 取最大）。
	// 由 api 层在 List 之后注入；store.List 本身不填充（store 层不依赖 storage / runtime config）。
	// nil 表示没有任何探测记录（新创建或刚归档的通道）。
	LatestProbe *LatestProbeSnapshot `json:"latest_probe,omitempty"`
}

// LatestProbeSnapshot 列表页"列表活化"用的最新探测快照。
type LatestProbeSnapshot struct {
	Status    int    `json:"status"` // 1=绿 2=黄 0=红
	SubStatus string `json:"sub_status,omitempty"`
	HTTPCode  int    `json:"http_code,omitempty"`
	Latency   int    `json:"latency"`         // ms
	Timestamp int64  `json:"timestamp"`       // Unix 秒
	Model     string `json:"model,omitempty"` // 这条记录归属的 model（多 model 通道用得着）
}

// RootMonitor 返回监测文件的父通道（无 parent 字段者）。
// 空文件返回 nil。文件中父通道未必排在 Monitors[0]，故须遍历。
func RootMonitor(mf *MonitorFile) *ServiceConfig {
	if mf == nil || len(mf.Monitors) == 0 {
		return nil
	}
	for i := range mf.Monitors {
		if strings.TrimSpace(mf.Monitors[i].Parent) == "" {
			return &mf.Monitors[i]
		}
	}
	return &mf.Monitors[0]
}

// List 列出 monitors.d/ 下所有监测文件的摘要。
func (s *MonitorStore) List() ([]MonitorSummary, error) {
	_, files, err := loadMonitorsDir(filepath.Dir(s.dir))
	if err != nil {
		return nil, err
	}

	summaries := make([]MonitorSummary, 0, len(files))
	for _, f := range files {
		if len(f.Monitors) == 0 {
			continue
		}

		rootPtr := RootMonitor(&f)
		if rootPtr == nil {
			continue
		}
		root := *rootPtr

		summaries = append(summaries, MonitorSummary{
			Key:         f.Key,
			ChannelID:   f.Metadata.ChannelID,
			Provider:    root.Provider,
			Service:     root.Service,
			Channel:     root.Channel,
			ChannelName: root.ChannelName,
			ModelCount:  len(f.Monitors),
			Disabled:    root.Disabled,
			Hidden:      root.Hidden,
			Board:       root.Board,
			Category:    root.Category,
			Template:    root.Template,
			Source:      f.Metadata.Source,
			Revision:    f.Metadata.Revision,
			UpdatedAt:   f.Metadata.UpdatedAt,
		})
	}
	return summaries, nil
}

// Get 读取指定 key 的监测文件。key 格式: provider--service--channel
func (s *MonitorStore) Get(key string) (*MonitorFile, error) {
	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return nil, err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}

	file, err := loadMonitorFile(path)
	if err != nil {
		return nil, err
	}
	file.Path = path
	file.Key = key
	return &file, nil
}

// Create 创建新监测文件。PSC 不能已存在于 monitors.d/ 中。
func (s *MonitorStore) Create(file *MonitorFile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key, err := DeriveMonitorFileKey(*file)
	if err != nil {
		return fmt.Errorf("推导 PSC key 失败: %w", err)
	}
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return fmt.Errorf("PSC key 无效: %w", err)
	}

	existing, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if existing != "" {
		return fmt.Errorf("PSC %s 已存在", key)
	}

	path := filepath.Join(s.dir, key+".yaml")

	now := time.Now().UTC().Format(time.RFC3339)
	file.Metadata.Revision = 1
	if file.Metadata.CreatedAt == "" {
		file.Metadata.CreatedAt = now
	}
	file.Metadata.UpdatedAt = now

	// 生成缺失的稳定 id（幂等：已有则不动）。与回填 CLI 共用同一生成逻辑。
	BackfillFileIDs(file)

	// 写盘前 fail-loud：payload 自带的重复 model_id 不会被 BackfillFileIDs 覆盖，
	// 落盘即造出一份 loader 拒绝加载的坏文件。
	if err := ValidateFileModelIDsUnique(file); err != nil {
		return err
	}

	if err := AtomicWriteYAML(path, file); err != nil {
		return err
	}

	file.Path = path
	file.Key = key
	return nil
}

// preserveAdminHiddenFields 把 admin PUT 不应改写的持久化字段从 existing 合并到 updated：
// 既含不参与 JSON round-trip 的 json:"-" 字段，也含对客户端可见但系统维护、不可变的稳定 id。
func preserveAdminHiddenFields(updated, existing *MonitorFile) {
	// channel_id 文件级不可变：先无条件从 existing 还原，防止 admin PUT 篡改；legacy 空值由
	// 调用方 Update 在写盘前的 BackfillFileIDs 自动补齐（不再依赖手动跑回填 CLI）。
	updated.Metadata.ChannelID = existing.Metadata.ChannelID

	// root 对 root
	updatedRoot := findRootMonitor(updated.Monitors)
	existingRoot := findRootMonitor(existing.Monitors)
	if updatedRoot != nil && existingRoot != nil {
		copyAdminHiddenFields(updatedRoot, existingRoot)
	}

	// child 按双通道匹配：model_id 优先，展示名兜底（zero regression for legacy children without id）。
	//
	// **匹配必须一对一**：既有子行一旦被某个 updated 行认领即移出候选，后续行匹配不到就
	// 走 BackfillFileIDs 铸新 id。多对一会直接造出重复 model_id——模板驱动的子行展示名来自
	// 模板、行里不写 model，磁盘上 model 是空串，于是新加的空 model 子行会集体命中同一个既有
	// 子行、被 copyAdminHiddenFields 复制走同一个 id，写出一份 loader 拒绝加载的坏文件
	// （admin 返回 200、热更新 fail-closed 保住旧配置，但容器重启即崩）。
	//
	// 两个 pass 是两个完整循环而非行内二级 fallback：保证**全局** model_id 匹配优先于展示名
	// 兜底，否则排在前面的无 id 行可能先按展示名抢走某个既有行，而后面本该按 id 配到它的行
	// 反倒落空。代价是 updated 数组顺序不再决定 id/展示名冲突时的归属——稳定 id 恒胜出。
	//
	// 子行数量是个位数，线性扫描（O(U×E)）比双索引队列更易审计，且两个 pass 天然共享同一份
	// 认领状态。
	claimed := make([]bool, len(existing.Monitors))
	matched := make([]bool, len(updated.Monitors))

	// Pass 1: 按 model_id 匹配（跨展示名改名保留 hidden fields）
	for i := range updated.Monitors {
		if strings.TrimSpace(updated.Monitors[i].Parent) == "" {
			continue
		}
		key, ok := childMatchKeyByModelID(updated.Monitors[i])
		if !ok {
			continue
		}
		if src := claimExistingChild(existing.Monitors, claimed, func(m ServiceConfig) bool {
			existingKey, ok := childMatchKeyByModelID(m)
			return ok && existingKey == key
		}); src != nil {
			copyAdminHiddenFields(&updated.Monitors[i], src)
			matched[i] = true
		}
	}

	// Pass 2: 按展示名匹配（legacy 无 id / id 未命中时的兜底）
	for i := range updated.Monitors {
		if matched[i] || strings.TrimSpace(updated.Monitors[i].Parent) == "" {
			continue
		}
		key := childMatchKeyByModel(updated.Monitors[i])
		if src := claimExistingChild(existing.Monitors, claimed, func(m ServiceConfig) bool {
			return childMatchKeyByModel(m) == key
		}); src != nil {
			copyAdminHiddenFields(&updated.Monitors[i], src)
		}
	}
	// 新增 child（无匹配）不继承，删除 child（不在 updated 中）自然消失
}

// claimExistingChild 返回第一个满足 match 且尚未被认领的既有子行，并就地标记为已认领；
// 无候选时返回 nil。按 existing 的文件顺序扫描，使同键多行的分配结果确定可复现。
func claimExistingChild(existing []ServiceConfig, claimed []bool, match func(ServiceConfig) bool) *ServiceConfig {
	for j := range existing {
		if claimed[j] || strings.TrimSpace(existing[j].Parent) == "" {
			continue
		}
		if !match(existing[j]) {
			continue
		}
		claimed[j] = true
		return &existing[j]
	}
	return nil
}

// findRootMonitor 返回第一个无 parent 字段的监测项指针。
func findRootMonitor(monitors []ServiceConfig) *ServiceConfig {
	for i := range monitors {
		if strings.TrimSpace(monitors[i].Parent) == "" {
			return &monitors[i]
		}
	}
	return nil
}

// childMatchKeyByModelID 返回按 parent+model_id 的稳定匹配键。
// model_id 为空时 ok=false，调用方应回退到展示名匹配。
func childMatchKeyByModelID(m ServiceConfig) (string, bool) {
	id := strings.TrimSpace(m.ModelID)
	if id == "" {
		return "", false
	}
	return strings.TrimSpace(m.Parent) + "\x00" + id, true
}

// childMatchKeyByModel 返回按 parent+展示名 的匹配键（legacy 兜底）。
func childMatchKeyByModel(m ServiceConfig) string {
	return strings.TrimSpace(m.Parent) + "\x00" + strings.TrimSpace(m.Model)
}

// copyAdminHiddenFields 把 src 中 admin PUT 不应改写的监测行字段复制到 dst：
// 含 json:"-" 持久化字段，以及对客户端可见但不可变的 model_id（强制从 existing 还原以保证 id 不可变）。
// 注意：KeyType 和 AutoColdExempt 已改为 JSON 可见字段，通过 API round-trip 传递，无需在此回填。
func copyAdminHiddenFields(dst, src *ServiceConfig) {
	dst.ModelID = src.ModelID
	dst.EnvVarName = src.EnvVarName
	dst.RequestModel = src.RequestModel
	dst.SkipURLValidation = src.SkipURLValidation
	dst.URLPattern = src.URLPattern
}

// Update 更新监测文件。使用 revision 乐观锁防止并发覆盖。
func (s *MonitorStore) Update(key string, file *MonitorFile, expectedRevision int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("PSC %s 不存在", key)
	}

	existing, err := loadMonitorFile(path)
	if err != nil {
		return err
	}

	if existing.Metadata.Revision != expectedRevision {
		return fmt.Errorf("revision 不匹配: 期望 %d，实际 %d（文件已被其他操作修改）",
			expectedRevision, existing.Metadata.Revision)
	}

	// 校验 PSC 不可变：更新后的内容推导出的 key 必须与 URL key 一致
	newKey, err := DeriveMonitorFileKey(*file)
	if err != nil {
		return fmt.Errorf("推导 PSC key 失败: %w", err)
	}
	newKey, err = SanitizeMonitorKey(newKey)
	if err != nil {
		return fmt.Errorf("PSC key 无效: %w", err)
	}
	if newKey != key {
		return fmt.Errorf("PSC 不可变更: %s -> %s", key, newKey)
	}

	// 回填 json:"-" 字段与既有稳定 id（按 parent+model_id/展示名匹配），防止 admin API round-trip 丢失。
	preserveAdminHiddenFields(file, &existing)

	// 给仍缺失稳定 id 的行铸新 id（真新增的父/子行，或 legacy 空 id）；与 Create / 回填 CLI
	// 共用同一幂等逻辑，已有 id 绝不覆盖。缺这步时，经 admin 编辑新增的子通道行会无 model_id，
	// 触发 CheckRuntimeModelIDs fail-closed 跳过整份配置热更新（admin 保存返回 200，运行态却静默不变）。
	BackfillFileIDs(file)

	// 写盘前 fail-loud：一对一合并已杜绝"复制既有 id"，但 payload 自带的重复 id 仍会原样落盘。
	// 拒绝时磁盘文件与 revision 均不改动。
	if err := ValidateFileModelIDsUnique(file); err != nil {
		return err
	}

	file.Metadata.Revision = expectedRevision + 1
	file.Metadata.CreatedAt = existing.Metadata.CreatedAt // 保留创建时间
	file.Metadata.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if err := AtomicWriteYAML(path, file); err != nil {
		return err
	}

	file.Path = path
	file.Key = key
	return nil
}

// Delete 归档删除：移动到 monitors.d/.archive/{filename}.{timestamp}.yaml。
func (s *MonitorStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	key, err = SanitizeMonitorKey(key)
	if err != nil {
		return err
	}

	path, err := s.findExistingPath(key)
	if err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("PSC %s 不存在", key)
	}

	archiveDir := filepath.Join(s.dir, ".archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("创建 .archive 目录失败: %w", err)
	}

	ts := time.Now().UTC().Format("20060102T150405Z")
	archiveName := fmt.Sprintf("%s.%s%s", key, ts, filepath.Ext(path))
	archivePath := filepath.Join(archiveDir, archiveName)

	if err := os.Rename(path, archivePath); err != nil {
		return fmt.Errorf("归档文件失败: %w", err)
	}

	return nil
}
