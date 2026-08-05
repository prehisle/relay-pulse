// Package modelvendor 是「模型厂商」受控词表的单一真相源。
//
// 背景：service（cc/cx/gm）表达的是**协议族**、channel_type（O/R/M）表达的是**线路性质**，
// 二者都不回答「这条通道跑的是谁家的模型」——第一方厂商开放 Anthropic/OpenAI 兼容端点后
// （用 Claude 的协议、跑智谱的模型），该问题成为独立的一根正交轴，即本包的 model_vendor。
//
// 不变量：
//   - code 是稳定标识符，一经发布不可复用于另一厂商——它会进入 /api/status wire，
//     并作为跨产品契约的一部分被 rpdiag 消费（见 meta 仓 docs/contract-ranking-export.md）。
//   - 空 code 合法，且**没有** fail-closed 运行时闸（与 model_id 不同，理由见
//     config.validateModelVendors 的注释）。我方生产的覆盖靠「内置探针模板全部声明 vendor」
//     保证，自托管部署不关心厂商时留空即可。
//   - 本包 stdlib-only，不依赖仓库内其它包（config 反向 import 它）。
//
// Label 是默认（中文）展示名，仅作后端下发的兜底；多语言本地化由前端负责，别在这里塞 i18n。
package modelvendor

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxCodeLen 是厂商 code 的长度上限（按字节计，code 恒为 ASCII）。
const MaxCodeLen = 24

// codePattern 与仓库 PSC 段的口径一致：小写字母/数字，中间可含短横线，首尾必须是字母或数字。
// 格式校验独立于词表命中，是为了把「拼错/脏数据」与「厂商尚未收录」这两类错误分开报。
var codePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)

// Vendor 是词表中的一个厂商条目。
type Vendor struct {
	// Code 稳定标识符，进 wire、参与跨产品 join，不可复用。
	Code string `json:"code"`
	// Label 默认展示名（中文），前端可覆盖为本地化文案。
	Label string `json:"label"`
	// IconKey 供前端选取厂商图标，后端不解释其含义。
	IconKey string `json:"icon_key"`
}

// catalog 是词表本身，顺序即前端默认展示顺序（先三家 Claude/GPT/Gemini 原厂，再国内厂商）。
// 新增厂商只改这里；code 一旦发布即冻结，改名只能改 Label。
var catalog = []Vendor{
	{Code: "anthropic", Label: "Anthropic", IconKey: "anthropic"},
	{Code: "openai", Label: "OpenAI", IconKey: "openai"},
	{Code: "google", Label: "Google", IconKey: "google"},
	{Code: "zhipu", Label: "智谱", IconKey: "zhipu"},
	{Code: "moonshot", Label: "月之暗面", IconKey: "moonshot"},
	{Code: "minimax", Label: "MiniMax", IconKey: "minimax"},
	{Code: "deepseek", Label: "DeepSeek", IconKey: "deepseek"},
	{Code: "qwen", Label: "Qwen", IconKey: "qwen"},
}

// byCode 是 catalog 的查询索引，与 catalog 同源构建，避免两份词表漂移。
var byCode = func() map[string]Vendor {
	m := make(map[string]Vendor, len(catalog))
	for _, v := range catalog {
		m[v.Code] = v
	}
	return m
}()

// canonical 统一入口口径：剥首尾空白 + 转小写。与 template/monitor 层对 model/request_model
// 的 TrimSpace 处理同款，额外加转小写是因为 code 是机器标识符而非展示名。
func canonical(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// Normalize 规范化并校验厂商 code：空值放行（返回空串、无错），非空则必须格式合法且在词表内。
// 返回的规范值应当被写回配置，使下游只需处理规范形式。
func Normalize(value string) (string, error) {
	code := canonical(value)
	if code == "" {
		return "", nil
	}
	if len(code) > MaxCodeLen || !codePattern.MatchString(code) {
		return "", fmt.Errorf("model_vendor 格式无效（%q）：应为小写字母、数字与短横线组成的 %d 位以内 code", value, MaxCodeLen)
	}
	if _, ok := byCode[code]; !ok {
		return "", fmt.Errorf("model_vendor 未知（%q）：不在受控词表内，如需新增厂商请扩充 internal/modelvendor 词表", value)
	}
	return code, nil
}

// Validate 只判合法性、不关心规范值，供无需回写的调用方使用。
func Validate(value string) error {
	_, err := Normalize(value)
	return err
}

// Lookup 按与 Normalize 同款规范化后查词表。空值与词表外的值一律未命中。
func Lookup(value string) (Vendor, bool) {
	v, ok := byCode[canonical(value)]
	return v, ok
}

// Options 返回词表的深拷贝，供 API 层下发前端，避免外部修改污染真相源
// （与 onboarding.ChannelSourceOptionsByService 同款约定）。
// Vendor 各字段均为值类型，故一次 slice 拷贝即完整隔离。
func Options() []Vendor {
	return append([]Vendor(nil), catalog...)
}
