package config

import (
	"fmt"

	"github.com/google/uuid"
)

// 通道/模型稳定 id 的语义前缀：跨产品自描述、防止两类 id 混用。
const (
	channelIDPrefix = "ch_"
	modelIDPrefix   = "md_"
)

// NewChannelID 生成通道级稳定 id（ch_<uuidv4>）。
func NewChannelID() string { return channelIDPrefix + uuid.NewString() }

// NewModelID 生成监测行级稳定 id（md_<uuidv4>）。
func NewModelID() string { return modelIDPrefix + uuid.NewString() }

// inlineModelIDNamespace 是 config.yaml 内联监测行确定性派生 model_id 的命名空间。
// 该值与 deriveInlineModelID 的名称编码是历史兼容契约：改动会让所有内联部署的
// 派生 id 变化、内部历史（probe_history 按 model_id 重键）断档，故**不得随版本修改**。
var inlineModelIDNamespace = uuid.NewSHA1(
	uuid.NameSpaceURL,
	[]byte("https://github.com/prehisle/relay-pulse/identity/inline-model/v1"),
)

// deriveInlineModelID 由 PSCM 四元组确定性派生 md_<uuidv5>，专供未显式写 model_id 的
// config.yaml 内联监测行（monitors.d 走随机持久化 id，不用此路径）。
// 确定性保证跨重启/热更新 id 稳定（随机 id 只存内存、每次 Load 都变，会抹掉可见历史）。
// 已知取舍：id 耦合 model 名——改 model 展示名会派生出新 id、内部历史断档；需要改名不断
// 历史的用户应在 config.yaml 里显式固定 model_id（显式值本函数不覆盖）。
// 名称输入唯一性由 validateMonitorUniqueness（PSCM 四元组唯一）保证；派生 id 的最终唯一性
// （含理论上极低概率的 uuidv5 碰撞）由 Load 末尾重跑的 validateModelIDs 兜底 fail-closed。
// 用「长度:值」前缀编码消除裸斜杠拼接歧义（字段本身可含 "/"）。
func deriveInlineModelID(m ServiceConfig) string {
	name := fmt.Sprintf("%d:%s/%d:%s/%d:%s/%d:%s",
		len(m.Provider), m.Provider,
		len(m.Service), m.Service,
		len(m.Channel), m.Channel,
		len(m.Model), m.Model,
	)
	return modelIDPrefix + uuid.NewSHA1(inlineModelIDNamespace, []byte(name)).String()
}

// IsValidChannelID 判断是否为带通道前缀的合法 id。
func IsValidChannelID(id string) bool { return isValidPrefixedUUID(id, channelIDPrefix) }

// IsValidModelID 判断是否为带模型前缀的合法 id。
func IsValidModelID(id string) bool { return isValidPrefixedUUID(id, modelIDPrefix) }

// isValidPrefixedUUID 校验 id = 指定前缀 + 合法 uuid。
// 仅拒绝畸形输入（前缀错误 / 空主体 / 非 uuid）；不强制版本/小写/非 nil——
// 随机生成路径用 v4（NewString），内联确定性派生用 v5（deriveInlineModelID），两者都须放行。
func isValidPrefixedUUID(id, prefix string) bool {
	if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
		return false
	}
	_, err := uuid.Parse(id[len(prefix):])
	return err == nil
}

// CollectModelIDs 跨文件收集所有非空 model_id，返回 id→定位串映射；
// 发现全局重复（同一 model_id 出现在两处）返回 error，错误信息含该重复 id。
// 空 model_id 跳过（回填前的现有行合法，空值由 validate 另判）。
func CollectModelIDs(files []MonitorFile) (map[string]string, error) {
	seen := make(map[string]string)
	for _, f := range files {
		if err := collectModelIDsInto(seen, f.Monitors); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

// collectModelIDsInto 把一组监测行的非空 model_id 累积进 seen，撞重即返回 error。
// 抽出供扁平的 AppConfig.Monitors（validate.go）与按文件的 CollectModelIDs 复用同一去重核。
func collectModelIDsInto(seen map[string]string, monitors []ServiceConfig) error {
	for _, m := range monitors {
		if m.ModelID == "" {
			continue
		}
		if prev, ok := seen[m.ModelID]; ok {
			return fmt.Errorf("model_id 重复: %s 同时用于 %s 和 %s", m.ModelID, prev, modelIDLocation(m))
		}
		seen[m.ModelID] = modelIDLocation(m)
	}
	return nil
}

// DuplicateModelIDError 表示一份监测文件内出现了重复的 model_id。
// 独立类型而非裸 error：monitors.d 写路径据此 fail-loud 拒绝落盘，api 层用 errors.As
// 把它映射成 4xx 可操作提示（与 InvalidProviderSlugError 同一模式），不必按错误文本子串猜。
type DuplicateModelIDError struct {
	Err error
}

// Error 对零值实例也安全：错误类型被 errors.As 当作 target 使用，调用方拿到的
// 可能是自己声明的空指针/零值，不该因此 panic。
func (e *DuplicateModelIDError) Error() string {
	if e == nil || e.Err == nil {
		return "model_id 重复"
	}
	return e.Err.Error()
}

func (e *DuplicateModelIDError) Unwrap() error { return e.Err }

// ValidateFileModelIDsUnique 校验单份监测文件内所有非空 model_id 互不相同。
//
// 这是 monitors.d 写路径的最后一道闸：一对一的子行合并已杜绝"从既有行复制出重复 id"，
// 但客户端 payload 自带的两条相同非空 model_id 仍会原样落盘（BackfillFileIDs 只补空值、
// 绝不覆盖既有 id）。重复 id 会让 loader 整份配置校验失败——运行中的旧配置虽被 fail-closed
// 保住，磁盘文件却已损坏、容器重启即加载失败。故在写盘前显式拒绝，绝不先污染磁盘再指望
// 加载期发现。跨文件重复由 loader 的全局 validateModelIDs 兜底（store 不读其它文件）。
func ValidateFileModelIDsUnique(file *MonitorFile) error {
	if file == nil {
		return nil
	}
	if err := collectModelIDsInto(make(map[string]string, len(file.Monitors)), file.Monitors); err != nil {
		return &DuplicateModelIDError{Err: err}
	}
	return nil
}

// modelIDLocation 给出监测行的人类可读定位串，用于重复/校验报错。
func modelIDLocation(m ServiceConfig) string {
	return fmt.Sprintf("%s/%s/%s/%s", m.Provider, m.Service, m.Channel, m.Model)
}

// BackfillFileIDs 给文件补齐缺失的 channel_id（文件级）与 model_id（每行），
// 绝不覆盖既有 id（幂等）。返回是否发生改动——供回填 CLI 报告，也供 store.Create 复用同一生成逻辑。
func BackfillFileIDs(f *MonitorFile) bool {
	changed := false
	if f.Metadata.ChannelID == "" {
		f.Metadata.ChannelID = NewChannelID()
		changed = true
	}
	for i := range f.Monitors {
		if f.Monitors[i].ModelID == "" {
			f.Monitors[i].ModelID = NewModelID()
			changed = true
		}
	}
	return changed
}
