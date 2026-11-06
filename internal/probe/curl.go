package probe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// curlAPIKeyEnvVar 是脱敏 curl 中代替明文密钥的 shell 变量名。
const curlAPIKeyEnvVar = "RP_API_KEY"

// curlAPIKeyEnvExpr 是拼进单引号片段之间的变量展开式。
// 必须用双引号包裹：单引号串内 $VAR 不会被展开，因此脱敏处采用
// '<字面量>'"$RP_API_KEY"'<字面量>' 这种「单引号字面量 + 双引号变量」交替拼接。
const curlAPIKeyEnvExpr = `"$` + curlAPIKeyEnvVar + `"`

// redactedKeyMarker 用于非 shell 上下文（如错误文案/日志）的明文 key 占位。
const redactedKeyMarker = "<api-key>"

// minRedactableKeyLen 是启用密钥脱敏的最短 apiKey 长度。
// 过短的 apiKey 作为分隔符会误命中正常文本子串（如 "key"、"sk" 出现在 URL/body 里），
// 把无关文本替换成变量、破坏命令。真实 API key / OAuth token 远长于此；低于该长度
// 直接放弃脱敏——这类短串本身也不构成真正的密钥泄漏面。
const minRedactableKeyLen = 8

// secretVariants 返回 apiKey 在请求各位置可能出现的形态，用于脱敏匹配：
// 原文（header/raw query 常见）、QueryEscape、PathEscape（key 落在 URL path/query
// 且含特殊字符时，req.URL.String() 会重编码成 %xx）。去重、按长度降序，保证更长的
// 编码形态优先匹配，避免子串截断。apiKey 过短时返回 nil（放弃脱敏）。
func secretVariants(apiKey string) []string {
	if len(apiKey) < minRedactableKeyLen {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, v := range []string{apiKey, url.QueryEscape(apiKey), url.PathEscape(apiKey)} {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// echoedKeyForms 是 apiKey 在回显响应里可能呈现的形态：secretVariants 的原文/URL 编码，
// 外加 JSON 字符串转义后的样子（回显服务多以 JSON 返回请求头，`/` 可能写成 `\/`，
// `<>&` 会被 Go 一类序列化器写成 `\u003c` 等）。长度门槛沿用 secretVariants：
// 太短的串在正常响应里也可能偶然出现，宁可不检。按长度降序，供 redactSecrets 先替换长的。
func echoedKeyForms(apiKey string) []string {
	base := secretVariants(apiKey)
	if len(base) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) {
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range base {
		add(v)
		// 两种常见的 JSON 字符串写法：HTML 转义开（Go 默认，`<>&` 写成 `\u003c` 等）与关
		// （多数语言的默认），各自再配一个 `/` 写成 `\/` 的版本。任意 `\uXXXX` 改写不在范围内。
		for _, escapeHTML := range []bool{true, false} {
			escaped, ok := jsonStringBody(v, escapeHTML)
			if !ok {
				continue
			}
			add(escaped)
			add(strings.ReplaceAll(escaped, "/", `\/`))
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// jsonStringBody 返回 v 序列化成 JSON 字符串后去掉外围那一对引号的内容。只剥首尾各一个
// 字节——strings.Trim 会把 key 末尾转义引号的那个 `"` 也吃掉，留下悬空的反斜杠。
func jsonStringBody(v string, escapeHTML bool) (string, bool) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(escapeHTML)
	if err := enc.Encode(v); err != nil {
		return "", false
	}
	quoted := bytes.TrimSuffix(buf.Bytes(), []byte("\n")) // Encode 末尾自带换行
	if len(quoted) < 2 {
		return "", false
	}
	return string(quoted[1 : len(quoted)-1]), true
}

// containsAny 报告 body 是否包含 needles 中任一非空串。
func containsAny(body []byte, needles []string) bool {
	for _, n := range needles {
		if n != "" && bytes.Contains(body, []byte(n)) {
			return true
		}
	}
	return false
}

// redactSecrets 把 s 中所有密钥变体替换成中性占位符，用于错误文案/日志等非 shell 文本。
// 例如 client.Do 失败时 *url.Error 会带完整请求 URL，URL 内嵌 key 的模板会因此泄漏。
func redactSecrets(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, redactedKeyMarker)
		}
	}
	return s
}

// buildCurlCommand 把一次实际发出的探测请求序列化成一条可复制的 curl 命令，
// 供管理员在测试失败时复制给通道方复现排障。
//
// 安全契约：
//   - 输出不含任何明文密钥（apiKey 长度 >= minRedactableKeyLen 时）。命中 apiKey
//     任一变体的子串被替换成 $RP_API_KEY shell 变量；变体本身只作为分隔符使用、
//     绝不写入输出，因此其内容（即便含单引号或 $）不影响转义。
//   - 仅在 captureCurl 开启时由 admin 路径生成；不写日志、不入库（probe() 本就不落地）。
//
// 保真度：method / 最终 URL / canonical 排序后的 headers / TrimSpace 后的 body 逐项
// 还原为「应用层请求快照」。不承诺 wire 级逐字节一致（Go 与 curl 各自的默认
// User-Agent、Accept-Encoding、传输层细节不同）。--http1.1 呼应 inline 探针禁用 HTTP/2 的行为。
func buildCurlCommand(req *http.Request, body []byte, apiKey string) string {
	if req == nil || req.URL == nil {
		return ""
	}
	secrets := secretVariants(apiKey)

	headerLines := sortedHeaderLines(req.Header)
	rawURL := req.URL.String()

	var b strings.Builder
	if requestContainsKey(rawURL, body, headerLines, secrets) {
		// 提示如何注入真实密钥；注释里绝不含真实 key 值。
		b.WriteString("# 复现前设置真实密钥：export ")
		b.WriteString(curlAPIKeyEnvVar)
		b.WriteString("='<your-api-key>'\n")
	}

	b.WriteString("curl -i --http1.1 -X ")
	b.WriteString(shellQuote(req.Method))

	for _, line := range headerLines {
		b.WriteString(" -H ")
		b.WriteString(quoteRedactingKey(line, secrets))
	}

	if len(body) > 0 {
		b.WriteString(" --data-binary ")
		b.WriteString(quoteRedactingKey(string(body), secrets))
	}

	b.WriteByte(' ')
	b.WriteString(quoteRedactingKey(rawURL, secrets))

	return b.String()
}

// requestContainsKey 判断任一密钥变体是否出现在请求中，用于决定是否输出 env 提示行。
func requestContainsKey(rawURL string, body []byte, headerLines []string, secrets []string) bool {
	for _, sec := range secrets {
		if strings.Contains(rawURL, sec) || strings.Contains(string(body), sec) {
			return true
		}
		for _, line := range headerLines {
			if strings.Contains(line, sec) {
				return true
			}
		}
	}
	return false
}

// sortedHeaderLines 把 http.Header 拍平成 "Canonical-Key: value" 行并按字典序排序，
// 保证同一请求每次生成的 curl 顺序稳定（便于测试断言与人工比对）。
func sortedHeaderLines(h http.Header) []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		canonical := http.CanonicalHeaderKey(k)
		for _, v := range h[k] {
			lines = append(lines, canonical+": "+v)
		}
	}
	return lines
}

// quoteRedactingKey 单引号包裹 s；若命中任一密钥变体，则把命中处换成 $RP_API_KEY
// 变量展开——字面量片段各自单引号包裹、片段之间插入变量展开式。逐字符扫描，每步取
// 当前位置之后最早出现（同位置取最长）的变体，保证多次出现、整串即密钥等情形都覆盖。
func quoteRedactingKey(s string, secrets []string) string {
	if len(secrets) == 0 {
		return shellQuote(s)
	}

	var b strings.Builder
	for len(s) > 0 {
		bestIdx, bestLen := -1, 0
		for _, sec := range secrets {
			if sec == "" {
				continue
			}
			idx := strings.Index(s, sec)
			if idx < 0 {
				continue
			}
			if bestIdx == -1 || idx < bestIdx || (idx == bestIdx && len(sec) > bestLen) {
				bestIdx, bestLen = idx, len(sec)
			}
		}
		if bestIdx == -1 {
			b.WriteString(shellQuote(s))
			break
		}
		if bestIdx > 0 {
			b.WriteString(shellQuote(s[:bestIdx]))
		}
		b.WriteString(curlAPIKeyEnvExpr)
		s = s[bestIdx+bestLen:]
	}
	return b.String()
}

// shellQuote 用单引号安全包裹任意字符串，内部单引号转义成 '\”。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
