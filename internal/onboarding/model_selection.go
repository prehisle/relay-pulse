package onboarding

import (
	"fmt"
	"regexp"
	"strings"

	"monitor/internal/config"
	"monitor/internal/modelvendor"
)

// MaxModelIDLen 是行级模型 ID 的长度上限（按字节计，ID 恒为 ASCII）。
// 真实模型 ID 最长的一档是云平台的带前缀形式（如 us.anthropic.claude-…-v2:0），128 足够宽松。
const MaxModelIDLen = 128

// modelIDPattern 是行级模型 ID 的严格白名单。
//
// 这不是洁癖，是注入边界：模板 body 按**原始字节**发送（仅 TrimSpace，不 re-marshal），
// {{MODEL}} 是 monitor.InjectVariables 里的纯字符串替换，且同一次替换会作用于 URL、headers、
// body 与 success_contains 四处。一个双引号就能打烂 body 的 JSON，一个 \r\n 就能在 header 里
// 另起一行——所以宁可让极少数含怪字符的模型 ID 走人工收录，也不放开引号/反斜杠/控制字符。
//
// 允许的字符取自真实模型 ID 的形态：字母数字加 `. _ - : / @ +`
// （glm-4.7 / kimi-k2.7-code / deepseek-ai/DeepSeek-V3 / us.anthropic.claude-…-v2:0 / ep-2026… ）。
// 首字符必须是字母或数字，避免出现以分隔符开头的可疑值。
var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@/+-]*$`)

// ValidateModelSelection 校验「探针模板 ↔ 行级模型/厂商」这组组合，返回规范化后的值。
//
// 判据只有一条、由模板族决定（config.IsNativeProbeTemplate 是唯一判定，不在这里抄第二份）：
//   - native 族（<service>-native-*）厂商无关、刻意不声明模型，模型**必须**由提交方给出；
//     漏填会让请求在 wire 上发出 `"model": ""`，上游返回没头没脑的错误、通道恒红。
//   - 其余模板已经把模型钉死在模板里，提交方再填一个只会造成「以为在测 A、实际在测 B」，
//     故必须为空——这是拒绝而非静默忽略：静默忽略等于让用户带着错误预期上架。
//
// 厂商可为空（词表外的厂商就该留空，由管理员后补），非空则必须在受控词表内。
func ValidateModelSelection(templateName, model, vendor string) (string, string, error) {
	normModel := strings.TrimSpace(model)
	native := config.IsNativeProbeTemplate(templateName)

	switch {
	case native && normModel == "":
		return "", "", fmt.Errorf("所选模型需要填写厂商的模型 ID（如 glm-5.2、kimi-k2.7-code）")
	case !native && normModel != "":
		return "", "", fmt.Errorf("所选模型已由探针模板确定，请勿另填模型 ID（%q）", model)
	}

	if normModel != "" {
		if len(normModel) > MaxModelIDLen {
			return "", "", fmt.Errorf("模型 ID 过长（%d 字节，上限 %d）", len(normModel), MaxModelIDLen)
		}
		if !modelIDPattern.MatchString(normModel) {
			return "", "", fmt.Errorf("模型 ID 含不支持的字符（%q）：仅允许字母、数字与 . _ - : / @ +", model)
		}
	}

	normVendor, err := modelvendor.Normalize(vendor)
	if err != nil {
		return "", "", err
	}
	return normModel, normVendor, nil
}
