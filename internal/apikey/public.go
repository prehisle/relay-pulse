package apikey

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinPublicKeyLen 是公开自助流程（收录测试/提交、变更测试/轮换）接受的 API Key 最短字符数。
//
// 下限与首尾空白两条规则都服务于 probe.InlineProber 的回显检测：它在 2xx 响应里找本次请求的
// key，太短的串不检（正常响应里也可能偶然出现）；而 Go 发请求时会裁掉请求头值的首尾空白，
// 带空格的 key 被回显回来时已不是原样，同样匹配不上。
const MinPublicKeyLen = 10

// ValidatePublicKey 校验公开流程提交的 API Key：首尾不能有空白，且至少 MinPublicKeyLen 个字符。
// 不做静默 trim——key 同时参与探测、proof 指纹与存储，悄悄改写会让这几处各用各的值。
func ValidatePublicKey(key string) error {
	if strings.TrimFunc(key, unicode.IsSpace) != key {
		return fmt.Errorf("API Key 首尾不能有空格或换行，请检查是否多复制了空白")
	}
	// 只收可打印 ASCII：真实 API Key 不会有别的字符。非 ASCII 字节会被回显服务按 latin-1
	// 解码后重新编码（é 回来变成 Ã©），回显检测就认不出这把 key 了——2026-11 有人这样绕过。
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("API Key 只能包含英文字母、数字和常见符号，不能有空格、中文或其他特殊字符")
		}
	}
	if utf8.RuneCountInString(key) < MinPublicKeyLen {
		return fmt.Errorf("API Key 过短（至少 %d 个字符）", MinPublicKeyLen)
	}
	return nil
}
