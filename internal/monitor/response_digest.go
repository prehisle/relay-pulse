package monitor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// contentMismatchExcerptLimit 是摘要里原文片段的字节上限。
	contentMismatchExcerptLimit = 512
	// sseErrorHintLimit 限制上游自报错误信息的长度，避免一条 stack trace 顶掉整段摘要。
	sseErrorHintLimit = 200
	// sseFieldLimit 给事件名/截断原因这类短枚举封顶，防止畸形 payload 把摘要撑爆。
	sseFieldLimit = 128
	// expectedKeywordLimit 给 expected 封顶。success_contains 来自模板、长度不受我们控制，
	// 不封顶就等于让每条红态记录按模板长度写库。合法用法（RP_ANSWER=79 / pong）远在此之下。
	expectedKeywordLimit = 200
)

// SSEDigest 是一次 SSE 响应体的结构化速览。
//
// 存在的理由：content_mismatch 的证据（流怎么结束的、上游报了什么错、生成为什么被截断）
// 全在流的**尾部**，而摘要受长度限制只能留一小段原文。先把这几项抽成字段，
// 摘要就不再依赖「恰好截到了有用的那一段」。
type SSEDigest struct {
	Events int // data: 事件条数（跳过空 payload 与 [DONE]）
	// LastEvent 是最后一个**可识别**的事件类型。认不出类型的事件（既无 type 字段、
	// 也无 event: 行，如 OpenAI Chat 的 chunk）不清空本字段——「最后一个叫得出名字的
	// 事件」比空串有用，但它不等于「流的最后一条事件」，别按后者解读。
	LastEvent  string
	StopReason string // 生成侧给出的截断原因（Anthropic stop_reason / OpenAI incomplete_details.reason / finish_reason）
	ErrorHint  string // 上游在流内自报的错误信息
}

// DigestSSE 单遍扫描 SSE 响应体，产出结构化速览。
// 不做文本提取（那是 ExtractTextFromSSE 的活），两者刻意分开：
// 「抽到了什么正文」与「这条流长什么样」是两个独立问题，合在一起会让任一方的改动波及另一方。
func DigestSSE(body []byte) SSEDigest {
	var digest SSEDigest

	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// SSE 的 event: 行在 data: 行之前，故需跨行记忆；仅在 data payload 认不出 type 时才用它。
	pendingEvent := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// 空行是 SSE 的帧分隔符：event: 行的作用域到此为止，
		// 不清掉会让一个没带 data: 的孤立事件名漏进下一帧。
		if line == "" {
			pendingEvent = ""
			continue
		}

		if strings.HasPrefix(line, "event:") {
			pendingEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		digest.Events++

		var obj map[string]any
		if err := json.Unmarshal([]byte(payload), &obj); err != nil {
			// 非 JSON payload（Gemini 之外的私有格式）：只能靠 event: 行认类型
			if pendingEvent != "" {
				digest.LastEvent = pendingEvent
			}
			continue
		}

		if eventType, ok := obj["type"].(string); ok && eventType != "" {
			digest.LastEvent = eventType
		} else if pendingEvent != "" {
			digest.LastEvent = pendingEvent
		}

		digest.absorb(obj)
	}

	return digest
}

// absorb 从单条事件里捞取截断原因与错误信息。
// 后来的事件覆盖先前的：流尾部的结论比中途的快照更接近真相
// （典型是 response.created 里 error/incomplete_details 恒为 null，真值只在终止事件里）。
func (d *SSEDigest) absorb(obj map[string]any) {
	// Anthropic: {"type":"message_delta","delta":{"stop_reason":"max_tokens"}}
	// 这条正是「不关思考 → 预算被 thinking 吃光 → 200 却恒红」的直接证据。
	if delta, ok := obj["delta"].(map[string]any); ok {
		if reason, ok := delta["stop_reason"].(string); ok && reason != "" {
			d.StopReason = reason
		}
	}

	// Anthropic: {"type":"error","error":{"message":"..."}}
	if hint := errorMessageOf(obj["error"]); hint != "" {
		d.ErrorHint = hint
	}

	// OpenAI Responses: {"type":"response.failed","response":{"error":{...},"incomplete_details":{"reason":"..."}}}
	if response, ok := obj["response"].(map[string]any); ok {
		if hint := errorMessageOf(response["error"]); hint != "" {
			d.ErrorHint = hint
		}
		if incomplete, ok := response["incomplete_details"].(map[string]any); ok {
			if reason, ok := incomplete["reason"].(string); ok && reason != "" {
				d.StopReason = reason
			}
		}
	}

	// OpenAI Chat: {"choices":[{"finish_reason":"length"}]}
	if choices, ok := obj["choices"].([]any); ok {
		for _, raw := range choices {
			choice, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
				d.StopReason = reason
			}
		}
	}
}

// fields 把速览渲染成摘要字段，只输出取到值的项——
// 摘要要能一眼扫完，恒定打印一串空值是噪音。
func (d SSEDigest) fields() []string {
	// 事件名与截断原因同样来自上游、长度不受我们控制，一并封顶：
	// 摘要的总长必须只由本文件的常量决定，不能由响应体决定。
	out := []string{fmt.Sprintf("sse_events=%d", d.Events)}
	if d.LastEvent != "" {
		out = append(out, "last_event="+truncateHead(d.LastEvent, sseFieldLimit))
	}
	if d.StopReason != "" {
		out = append(out, "stop_reason="+truncateHead(d.StopReason, sseFieldLimit))
	}
	if d.ErrorHint != "" {
		out = append(out, fmt.Sprintf("error=%q", truncateHead(d.ErrorHint, sseErrorHintLimit)))
	}
	return out
}

// errorMessageOf 从 error 字段取人可读信息：既见过 {"message":"..."} 对象，也见过直接一个字符串。
// 取不到返回空串——包括最常见的 "error":null（握手事件的常态）。
func errorMessageOf(v any) string {
	switch e := v.(type) {
	case string:
		return strings.TrimSpace(e)
	case map[string]any:
		if msg, ok := e["message"].(string); ok {
			return strings.TrimSpace(msg)
		}
	}
	return ""
}

// BuildContentMismatchSummary 为 content_mismatch 组装诊断摘要。
//
// 为什么不能沿用「原样截响应体前 512 字节」：SSE 流的开头恒为握手元数据
// （response.created / message_start），判红所需的证据一个都不在那里——
// 抽到了什么正文、流怎么结束的、上游报了什么错，分别在别处或尾部。
//
// 摘要固定两行：首行是判据本身（期望什么、抽到多少、流的形状），
// 次行是原文片段，且**取哪一端由首行的结论决定**——抽到正文就给正文，
// 一个字没抽到就给响应体尾部。
func BuildContentMismatchSummary(body []byte, expected string) string {
	fields := []string{"content_mismatch", fmt.Sprintf("expected=%q", truncateHead(expected, expectedKeywordLimit))}

	var excerpt string

	switch {
	case len(body) == 0:
		fields = append(fields, "body_bytes=0")

	case looksLikeSSE(body):
		// 判空必须与 evaluateStatus 用同一个谓词（TrimSpace 后为空即视作没有正文）。
		// 用未 trim 的 text != "" 判，纯空白正文会让摘要一边印 extracted>0chars、
		// 一边给不出正文行，也不回退到 body_tail——恰好是「摘要描述了没发生的事」。
		// trim 不会抹掉匹配：关键字是非空白串，trim 只动首尾空白。
		text := strings.TrimSpace(ExtractTextFromSSE(body))
		fields = append(fields, fmt.Sprintf("extracted=%dchars", utf8.RuneCountInString(text)))
		if text == "" {
			// 提取器一个字都没抽到时，内容校验实际是拿整包协议信封在 grep
			// （AggregateResponseText 的 raw 回退）。这个事实必须写明，
			// 否则「未包含预期关键字」会被误读成「模型答错了」。
			fields = append(fields, "matched_against=raw_body")
		}
		fields = append(fields, DigestSSE(body).fields()...)

		if text != "" {
			excerpt = excerptLine("text", text, false)
		} else {
			excerpt = excerptLine("body_tail", string(body), true)
		}

	default:
		// 非流式响应：响应体本身就是一份完整 JSON，头部即有效信息。
		fields = append(fields, fmt.Sprintf("body_bytes=%d", len(body)))
		excerpt = excerptLine("body", string(body), false)
	}

	summary := strings.Join(fields, " ")
	if excerpt != "" {
		summary += "\n" + excerpt
	}
	return sanitizeForStorage(summary)
}

// sanitizeForStorage 剔除非法 UTF-8 字节序列。
//
// 这不是防御性编程，是一条已实证的数据丢失路径：摘要写进 Postgres 的
// probe_history.error_detail（TEXT），而 Postgres 对非法字节序列直接报
// `invalid byte sequence for encoding "UTF8"` —— 失败的是整条 INSERT，
// 于是这次探测的记录**整条丢掉**，可用率跟着算错。
//
// 非法字节不是假想：响应体在 readBodyPrefixAndDrain 处按 512 **字节**截取，
// 上游返回中文错误体时截断点几乎必然落在汉字中间。对齐字符边界的 truncateHead
// 挡不住这种情况——它只管自己那一刀，管不了传进来的 body 本身就是断的。
func sanitizeForStorage(s string) string {
	return strings.ToValidUTF8(s, "")
}

// excerptLine 渲染原文片段行。标签里带「取了多少 / 总共多少」，
// 读的人一眼就知道有没有被截断、以及截掉的是哪一端。
//
// ⚠️ 字节数必须基于**未 trim 的源**计算：先 trim 再算，标签印的就不是响应体的真实
// 长度，`body_tail` 也不再是真正的最后 512 字节（SSE 体尾部恒有空行）。同一份摘要里
// 还会同时出现 `body_bytes=N`（未 trim）和 `(…/MB)`（trim 过）两个不同的数。
// trim 只用于显示。
func excerptLine(label, s string, fromTail bool) string {
	total := len(s)
	if total == 0 {
		return ""
	}

	cut := truncateHead(s, contentMismatchExcerptLimit)
	if fromTail {
		cut = truncateTail(s, contentMismatchExcerptLimit)
	}
	taken := len(cut)

	display := strings.TrimSpace(cut)
	if display == "" {
		return ""
	}
	if taken == total {
		return fmt.Sprintf("%s(%dB): %s", label, total, display)
	}
	return fmt.Sprintf("%s(%d/%dB): %s", label, taken, total, display)
}

// truncateHead / truncateTail 按字节上限截取，但落点对齐到 UTF-8 字符边界，
// 不制造新的断字节（传进来就断了的由 sanitizeForStorage 兜底，两者分工不重叠）。
func truncateHead(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func truncateTail(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	for cut < len(s) && !utf8.RuneStart(s[cut]) {
		cut++
	}
	return s[cut:]
}
