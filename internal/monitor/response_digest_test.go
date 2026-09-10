package monitor

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// sseFrame 拼一条 SSE 事件，避免测试里到处手写 "\n\n"。
func sseFrame(event, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
}

func TestDigestSSE(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		events     int
		lastEvent  string
		stopReason string
		errorHint  string
	}{
		{
			name:      "仅握手就断流",
			body:      sseFrame("response.created", `{"type":"response.created","response":{"id":"resp_1","status":"in_progress","error":null,"incomplete_details":null}}`),
			events:    1,
			lastEvent: "response.created",
			// 握手事件里 error/incomplete_details 恒为 null，绝不能被当成真值记下来
			stopReason: "",
			errorHint:  "",
		},
		{
			name: "以 response.failed 收尾",
			body: sseFrame("response.created", `{"type":"response.created","response":{"id":"resp_2","status":"in_progress","error":null}}`) +
				sseFrame("response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"upstream_error","message":"provider returned 502"}}}`),
			events:    2,
			lastEvent: "response.failed",
			errorHint: "provider returned 502",
		},
		{
			name:       "OpenAI 生成被截断",
			body:       sseFrame("response.incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`),
			events:     1,
			lastEvent:  "response.incomplete",
			stopReason: "max_output_tokens",
		},
		{
			name: "Anthropic 预算被思考吃光",
			body: sseFrame("message_start", `{"type":"message_start","message":{"id":"msg_1"}}`) +
				sseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":64}}`) +
				sseFrame("message_stop", `{"type":"message_stop"}`),
			events:     3,
			lastEvent:  "message_stop",
			stopReason: "max_tokens",
		},
		{
			name:      "Anthropic 流内错误事件",
			body:      sseFrame("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
			events:    1,
			lastEvent: "error",
			errorHint: "Overloaded",
		},
		{
			name:       "OpenAI Chat finish_reason",
			body:       "data: " + `{"choices":[{"delta":{"content":"hi"},"finish_reason":"length"}]}` + "\n\n" + "data: [DONE]\n\n",
			events:     1, // [DONE] 不计入
			stopReason: "length",
		},
		{
			name:      "Gemini 无 event 行",
			body:      "data: " + `{"candidates":[{"content":{"parts":[{"text":"pong"}]}}]}` + "\n\n",
			events:    1,
			lastEvent: "", // 既没有 event: 行也没有 type 字段，认不出就留空，不硬猜
		},
		{
			name:      "非 JSON payload 回退到 event 行",
			body:      sseFrame("ping", "keep-alive"),
			events:    1,
			lastEvent: "ping",
		},
		{
			name: "空体",
			body: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DigestSSE([]byte(tt.body))
			if got.Events != tt.events {
				t.Errorf("Events = %d, want %d", got.Events, tt.events)
			}
			if got.LastEvent != tt.lastEvent {
				t.Errorf("LastEvent = %q, want %q", got.LastEvent, tt.lastEvent)
			}
			if got.StopReason != tt.stopReason {
				t.Errorf("StopReason = %q, want %q", got.StopReason, tt.stopReason)
			}
			if got.ErrorHint != tt.errorHint {
				t.Errorf("ErrorHint = %q, want %q", got.ErrorHint, tt.errorHint)
			}
		})
	}
}

// TestBuildContentMismatchSummary_SSEWithoutText 锁住本次改动的核心场景：
// 上游 200 开了流却一个字正文都没产出。旧实现在这里输出响应体**头部**，
// 也就是恒定的 response.created 握手，对排障零帮助。
func TestBuildContentMismatchSummary_SSEWithoutText(t *testing.T) {
	// 造一条超过 512 字节的流，且把真正的失败原因放在尾部
	padding := strings.Repeat("x", 600)
	body := sseFrame("response.created", fmt.Sprintf(`{"type":"response.created","response":{"id":"resp_%s","status":"in_progress","error":null}}`, padding)) +
		sseFrame("response.failed", `{"type":"response.failed","response":{"status":"failed","error":{"message":"upstream closed connection"}}}`)

	summary := BuildContentMismatchSummary([]byte(body), "RP_ANSWER=79")

	for _, want := range []string{
		`expected="RP_ANSWER=79"`,  // 判据本身：arith 每次随机，不记就无法复核
		"extracted=0chars",         // 一个字都没抽到
		"matched_against=raw_body", // 于是内容校验实际在 grep 协议信封
		"sse_events=2",
		"last_event=response.failed",
		`error="upstream closed connection"`,
		"body_tail(",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("摘要缺少 %q\n实际:\n%s", want, summary)
		}
	}

	// 尾部原文必须真的带上失败事件，而不是又截到了握手
	if !strings.Contains(summary, "upstream closed") {
		t.Errorf("原文片段应取自尾部，实际:\n%s", summary)
	}
	if strings.Contains(summary, "response.created") {
		t.Errorf("原文片段不该再是头部的握手事件，实际:\n%s", summary)
	}
}

func TestBuildContentMismatchSummary_SSEWithWrongText(t *testing.T) {
	body := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER=78"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"status":"completed"}}`)

	summary := BuildContentMismatchSummary([]byte(body), "RP_ANSWER=79")

	for _, want := range []string{
		`expected="RP_ANSWER=79"`,
		"extracted=12chars",
		"last_event=response.completed",
		"text(12B): RP_ANSWER=78", // 抽到正文时给正文，让人一眼看出是答错而非没答
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("摘要缺少 %q\n实际:\n%s", want, summary)
		}
	}
	// 抽到正文就不是「拿信封 grep」，不该打这个标记
	if strings.Contains(summary, "matched_against=raw_body") {
		t.Errorf("抽到正文时不应标记 raw_body 回退，实际:\n%s", summary)
	}
}

// TestBuildContentMismatchSummary_AnthropicMaxTokens 覆盖 CLAUDE.md 记的那个恒红形态：
// Claude 5 世代不关思考 → 预算被 thinking 吃光 → HTTP 200 但正文为空。
// 摘要要能一眼指认，而不是让人再去翻模板。
func TestBuildContentMismatchSummary_AnthropicMaxTokens(t *testing.T) {
	body := sseFrame("message_start", `{"type":"message_start","message":{"id":"msg_1"}}`) +
		sseFrame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`) +
		sseFrame("message_stop", `{"type":"message_stop"}`)

	summary := BuildContentMismatchSummary([]byte(body), "RP_ANSWER=42")
	if !strings.Contains(summary, "stop_reason=max_tokens") {
		t.Errorf("摘要应指认 stop_reason，实际:\n%s", summary)
	}
	if !strings.Contains(summary, "extracted=0chars") {
		t.Errorf("摘要应说明未抽到正文，实际:\n%s", summary)
	}
}

func TestBuildContentMismatchSummary_NonSSEAndEmpty(t *testing.T) {
	jsonBody := `{"error":{"message":"model not found"}}`
	summary := BuildContentMismatchSummary([]byte(jsonBody), "pong")
	for _, want := range []string{`expected="pong"`, "body_bytes=39", "body(39B): " + jsonBody} {
		if !strings.Contains(summary, want) {
			t.Errorf("非 SSE 摘要缺少 %q\n实际:\n%s", want, summary)
		}
	}
	// 非流式响应头部即有效信息，不该出现流相关字段
	if strings.Contains(summary, "sse_events") {
		t.Errorf("非 SSE 不应输出 sse 字段，实际:\n%s", summary)
	}

	empty := BuildContentMismatchSummary(nil, "pong")
	if !strings.Contains(empty, "body_bytes=0") {
		t.Errorf("空体摘要应说明 body_bytes=0，实际: %s", empty)
	}
	if strings.Contains(empty, "\n") {
		t.Errorf("空体没有原文片段，摘要应只有一行，实际: %q", empty)
	}
}

// TestTruncateRuneSafe 摘要会原样写进 probe_history.error_detail 并经 JSON 下发，
// 从中间劈开的多字节字符轻则乱码、重则被 Postgres 的 UTF8 校验拒收。
func TestTruncateRuneSafe(t *testing.T) {
	// 每个汉字 3 字节，取 512 上限必然落在字符中间
	s := strings.Repeat("中", 400)

	head := truncateHead(s, contentMismatchExcerptLimit)
	if !utf8.ValidString(head) {
		t.Errorf("truncateHead 产出非法 UTF-8")
	}
	if len(head) > contentMismatchExcerptLimit {
		t.Errorf("truncateHead 超出上限: %d", len(head))
	}

	tail := truncateTail(s, contentMismatchExcerptLimit)
	if !utf8.ValidString(tail) {
		t.Errorf("truncateTail 产出非法 UTF-8")
	}
	if len(tail) > contentMismatchExcerptLimit {
		t.Errorf("truncateTail 超出上限: %d", len(tail))
	}
	if !strings.HasSuffix(s, tail) {
		t.Errorf("truncateTail 应取自尾部")
	}

	// 未超限时原样返回
	if got := truncateHead("abc", 10); got != "abc" {
		t.Errorf("truncateHead 未超限时应原样返回，got %q", got)
	}
	if got := truncateTail("abc", 10); got != "abc" {
		t.Errorf("truncateTail 未超限时应原样返回，got %q", got)
	}
}

func TestLooksLikeSSE(t *testing.T) {
	tests := map[string]bool{
		"event: x\ndata: {}\n\n":  true,
		"data: {}\n\n":            true, // Gemini 只有 data: 行
		"a\ndata: {}\n":           true,
		`{"error":"nope"}`:        false,
		"":                        false,
		"plain text with data in": false, // "data" 不带冒号不算
	}
	for body, want := range tests {
		if got := looksLikeSSE([]byte(body)); got != want {
			t.Errorf("looksLikeSSE(%q) = %v, want %v", body, got, want)
		}
	}
}

// TestBuildContentMismatchSummary_CapsExpectedKeyword —— success_contains 来自模板、
// 长度不受我们控制，不封顶就等于让每条红态记录按模板长度写库。
func TestBuildContentMismatchSummary_CapsExpectedKeyword(t *testing.T) {
	huge := strings.Repeat("k", 5000)
	summary := BuildContentMismatchSummary([]byte(`{"a":1}`), huge)
	if len(summary) > expectedKeywordLimit+contentMismatchExcerptLimit+200 {
		t.Errorf("摘要未对 expected 封顶，长度 %d", len(summary))
	}
	if strings.Contains(summary, strings.Repeat("k", expectedKeywordLimit+1)) {
		t.Errorf("expected 应被截断到 %d 字节", expectedKeywordLimit)
	}
}

// TestDigestSSE_EventScopeEndsAtBlankLine —— 空行是 SSE 的帧分隔符。
// 一个没带 data: 的孤立事件名不得漏进下一帧，否则末事件类型会被指认成错的那个。
func TestDigestSSE_EventScopeEndsAtBlankLine(t *testing.T) {
	body := "event: response.output_item.added\n\n" + // 只有事件名，没有 data
		"data: " + `{"candidates":[{"content":{"parts":[{"text":"x"}]}}]}` + "\n\n"

	if got := DigestSSE([]byte(body)).LastEvent; got != "" {
		t.Errorf("上一帧的事件名不该漏进下一帧，LastEvent = %q", got)
	}
}

// TestBuildContentMismatchSummary_SanitizesInvalidUTF8 —— content_mismatch 这条入口
// 同样要挡住非法字节：body 在 readBodyPrefixAndDrain 处按字节截取，传进来时可能本身就是断的。
func TestBuildContentMismatchSummary_SanitizesInvalidUTF8(t *testing.T) {
	// 以一个被劈开的汉字（0xE4 0xB8，缺第三字节）结尾
	broken := append([]byte("data: "+`{"type":"x","note":"上游`), 0xE4, 0xB8)

	summary := BuildContentMismatchSummary(broken, "pong")
	if !utf8.ValidString(summary) {
		t.Errorf("摘要含非法 UTF-8，Postgres 会拒收整条记录: %q", summary)
	}
	// 合法部分必须留下，不能因为清洗把有用内容一起丢了
	if !strings.Contains(summary, "上游") {
		t.Errorf("清洗不应丢掉合法内容: %q", summary)
	}
}
