package monitor

import (
	"strings"
	"testing"
)

// 本文件锁住 2026-09-11 的判定变更：SSE 正文提取从「见到像正文的字段就累加」
// 改成「按事件类型授权」，且 SSE 抽不到正文时不再回退整包协议信封。
//
// 背景（真实生产形态，不是构造的边界）：OpenAI Responses 协议把模型正文与**思考摘要**
// 放在同名同型的字段里——`response.output_text.delta` 与 `response.reasoning_summary_text.delta`
// 都是顶层字符串 `delta`，`.done` 那对都是顶层 `text`。旧实现一视同仁，于是
// 「模型只在思考里写出答案、正文一个字没输出」也判绿。

// TestExtractText_ReasoningIsNotBody 是这轮改动的核心断言：
// 思考摘要既不能进正文，也不能因此把红判成绿。
func TestExtractText_ReasoningIsNotBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			// 增量形态：reasoning 与正文同为顶层字符串 delta
			name: "reasoning_summary_text.delta",
			body: sseFrame("response.reasoning_summary_text.delta",
				`{"type":"response.reasoning_summary_text.delta","delta":"We need RP_ANSWER=127 exactly."}`) +
				sseFrame("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[]}}`),
		},
		{
			// 完成形态：这是**生产上真正在发生的那条**——8 条火山方舟 native 通道里，
			// reasoning 的 .done 恰好排在正文事件**之前**，于是旧实现那个
			// 「builder 为空就采纳顶层 text」的兜底把整段思考摘要吸成了正文。
			name: "reasoning_summary_text.done 先于正文到达",
			body: sseFrame("response.reasoning_summary_text.done",
				`{"type":"response.reasoning_summary_text.done","text":"We need answer only with RP_ANSWER=127. Need ensure no spaces."}`) +
				sseFrame("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[]}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromSSE([]byte(tt.body)); got != "" {
				t.Errorf("思考摘要不该被当成正文，抽到了 %q", got)
			}
			// 端到端：模型正文为空 => 必须判红，哪怕思考摘要里出现了关键字
			status, sub := evaluateStatus(1, "", []byte(tt.body), "RP_ANSWER=127")
			if status != 0 || sub != "content_mismatch" {
				t.Errorf("正文为空却判成 status=%d sub=%q，这正是要修的假绿", status, sub)
			}
		})
	}
}

// TestExtractText_ReasoningDoesNotContaminateRealBody ——
// 思考摘要与正文同时存在时，只能抽到正文，不能把两者拼在一起。
// 拼接不改红绿，但会让 content_mismatch 摘要印出一段根本不是模型输出的文本。
func TestExtractText_ReasoningDoesNotContaminateRealBody(t *testing.T) {
	body := sseFrame("response.reasoning_summary_text.done",
		`{"type":"response.reasoning_summary_text.done","text":"thinking: the sum is 127"}`) +
		sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER=127"}`)

	got := ExtractTextFromSSE([]byte(body))
	if got != "RP_ANSWER=127" {
		t.Errorf("应只抽到正文，得到 %q", got)
	}
}

// TestExtractText_BufferedShapes 覆盖 Responses 协议的非增量投递形态。
// 上游只发缓冲事件（不发 delta）时，旧实现一个字都抽不到。
func TestExtractText_BufferedShapes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "response.completed",
			body: sseFrame("response.completed",
				`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"RP_ANSWER=127"}]}]}}`),
		},
		{
			name: "response.output_item.done",
			body: sseFrame("response.output_item.done",
				`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"RP_ANSWER=127"}]}}`),
		},
		{
			name: "response.content_part.done",
			body: sseFrame("response.content_part.done",
				`{"type":"response.content_part.done","part":{"type":"output_text","text":"RP_ANSWER=127"}}`),
		},
		{
			name: "response.output_text.done",
			body: sseFrame("response.output_text.done",
				`{"type":"response.output_text.done","text":"RP_ANSWER=127"}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromSSE([]byte(tt.body)); got != "RP_ANSWER=127" {
				t.Errorf("抽取结果 = %q, want %q", got, "RP_ANSWER=127")
			}
		})
	}
}

// TestExtractText_NoDuplicateAcrossShapes ——
// Responses 协议会把同一份正文重复投递三到四次。各形态必须**择一**，不能拼接。
// 旧实现的 `b.Len() == 0` 只挡住了顶层 text 一个来源，缓冲形态补齐后就挡不住了。
func TestExtractText_NoDuplicateAcrossShapes(t *testing.T) {
	body := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER="}`) +
		sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"127"}`) +
		sseFrame("response.output_text.done", `{"type":"response.output_text.done","text":"RP_ANSWER=127"}`) +
		sseFrame("response.content_part.done", `{"type":"response.content_part.done","part":{"text":"RP_ANSWER=127"}}`) +
		sseFrame("response.output_item.done", `{"type":"response.output_item.done","item":{"content":[{"text":"RP_ANSWER=127"}]}}`) +
		sseFrame("response.completed",
			`{"type":"response.completed","response":{"output":[{"content":[{"text":"RP_ANSWER=127"}]}]}}`)

	got := ExtractTextFromSSE([]byte(body))
	if got != "RP_ANSWER=127" {
		t.Errorf("同一份正文被投递 4 次，应择一取用，得到 %q", got)
	}
	if strings.Count(got, "RP_ANSWER") != 1 {
		t.Errorf("正文被重复累加了：%q", got)
	}
}

// TestExtractText_DeltaSurvivesWhenBufferedIsEmpty ——
// 优先级不能退化成「有缓冲事件就用缓冲事件」：completed 里没有正文时
// 必须回落到 delta，否则「只发增量 + 一个空 completed 收尾」的上游会被误判成没有正文。
func TestExtractText_DeltaSurvivesWhenBufferedIsEmpty(t *testing.T) {
	body := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER=127"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"status":"completed","output":[]}}`)

	if got := ExtractTextFromSSE([]byte(body)); got != "RP_ANSWER=127" {
		t.Errorf("空的 completed 不该盖掉 delta，得到 %q", got)
	}
}

// TestExtractText_UpstreamErrorIsNotBody ——
// `{"type":"error","message":"..."}` 是上游错误信封，不是模型正文。
// 旧实现的顶层 message 兜底会把它抽出来，摘要于是印 extracted=61chars，
// 而实际上模型一个字都没输出（生产实例：ikuncode/cx 的 server_is_overloaded）。
func TestExtractText_UpstreamErrorIsNotBody(t *testing.T) {
	body := sseFrame("error",
		`{"type":"error","code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}`)

	if got := ExtractTextFromSSE([]byte(body)); got != "" {
		t.Errorf("上游错误信封不该算正文，抽到了 %q", got)
	}
}

// TestExtractText_UnknownEventContributesNothing ——
// 对未见过的事件类型必须有确定的默认行为：不贡献正文。
// 这类私有事件会不断新增（codex.* / responsesapi.* / keepalive 都是实测见过的），
// 白名单之外一律不算，宁可抽不到判红，也不能把非正文当正文判绿。
func TestExtractText_UnknownEventContributesNothing(t *testing.T) {
	body := sseFrame("codex.some_future_event",
		`{"type":"codex.some_future_event","delta":"RP_ANSWER=127","text":"RP_ANSWER=127","message":"RP_ANSWER=127"}`)

	if got := ExtractTextFromSSE([]byte(body)); got != "" {
		t.Errorf("未知事件不该贡献正文，抽到了 %q", got)
	}
}

// TestExtractText_UnknownEventCannotSmuggleViaNestedShape ——
// 「未知事件不贡献正文」必须对**嵌套结构**也成立。Anthropic 的 `delta.text`、
// Chat 的 `choices[]`、Gemini 的 `candidates[]` 走的是结构分派而非事件白名单，
// 若不加一道弱事件门，任何未来的私有事件都能借这些结构把非正文塞进正文桶。
func TestExtractText_UnknownEventCannotSmuggleViaNestedShape(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "借 anthropic delta.text",
			body: sseFrame("codex.future", `{"type":"codex.future","delta":{"type":"text_delta","text":"RP_ANSWER=127"}}`),
		},
		{
			name: "借 chat choices",
			body: sseFrame("codex.future", `{"type":"codex.future","choices":[{"delta":{"content":"RP_ANSWER=127"}}]}`),
		},
		{
			name: "借 gemini candidates",
			body: sseFrame("codex.future", `{"type":"codex.future","candidates":[{"content":{"parts":[{"text":"RP_ANSWER=127"}]}}]}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromSSE([]byte(tt.body)); got != "" {
				t.Errorf("未知事件借嵌套结构混进了正文：%q", got)
			}
		})
	}
}

// TestExtractText_IncompleteSnapshotDoesNotOverrideDelta ——
// completed 快照在 pick 里优先级最高，但只有 status=completed 的才可信。
// 残缺快照盖掉完整增量 = 把一次成功的探测判成红。
func TestExtractText_IncompleteSnapshotDoesNotOverrideDelta(t *testing.T) {
	for _, status := range []string{"incomplete", "failed"} {
		t.Run(status, func(t *testing.T) {
			body := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER=127"}`) +
				sseFrame("response.completed",
					`{"type":"response.completed","response":{"status":"`+status+`","output":[{"content":[{"text":"RP_ANSWER="}]}]}}`)

			if got := ExtractTextFromSSE([]byte(body)); got != "RP_ANSWER=127" {
				t.Errorf("status=%s 的残缺快照不该盖掉完整 delta，得到 %q", status, got)
			}
		})
	}
}

// TestExtractText_TruncatedJSONIsNotBody ——
// 补一处不对称：`{"type":"error","message":"…"}` 完整时被事件类型挡住，
// 一旦在传输中被截断就会解析失败，整段错误体反而混进正文。
// 私有纯文本正文不会以 { 或 [ 开头，故用首字符判据把两者分开。
func TestExtractText_TruncatedJSONIsNotBody(t *testing.T) {
	truncated := "data: {\"type\":\"error\",\"message\":\"RP_ANSWER=127\"\n\n"
	if got := ExtractTextFromSSE([]byte(truncated)); got != "" {
		t.Errorf("截断的 JSON 错误体不该当正文，抽到了 %q", got)
	}

	// 真正的私有纯文本格式仍然支持
	plain := "data: RP_ANSWER=127\n\n"
	if got := ExtractTextFromSSE([]byte(plain)); got != "RP_ANSWER=127" {
		t.Errorf("私有纯文本正文应照常抽到，得到 %q", got)
	}
}

// TestExtractText_EventLineAuthorizesWhenPayloadHasNoType ——
// payload 不带 type 时，授权来自本帧的 event: 行。
func TestExtractText_EventLineAuthorizesWhenPayloadHasNoType(t *testing.T) {
	body := sseFrame("response.output_text.delta", `{"delta":"RP_ANSWER=127"}`)
	if got := ExtractTextFromSSE([]byte(body)); got != "RP_ANSWER=127" {
		t.Errorf("event: 行应能授权正文，得到 %q", got)
	}

	reasoning := sseFrame("response.reasoning_summary_text.delta", `{"delta":"RP_ANSWER=127"}`)
	if got := ExtractTextFromSSE([]byte(reasoning)); got != "" {
		t.Errorf("event: 行同样要挡住思考摘要，抽到了 %q", got)
	}
}

// TestExtractText_EventScopeIsCurrentFrameNotSticky ——
// 事件作用域必须止于空行，且**不粘滞**。
// SSEDigest.LastEvent 刻意保留「最后一个叫得出名字的事件」，那个语义搬到这里
// 会让紧跟在正文事件后面的无名帧继续被当成正文——reasoning 内容就是这么混进来的。
func TestExtractText_EventScopeIsCurrentFrameNotSticky(t *testing.T) {
	body := sseFrame("response.output_text.delta", `{"delta":"ok"}`) +
		// 下一帧没有 event: 行、payload 也没有 type，不能沿用上一帧的授权
		"data: {\"delta\":\"RP_ANSWER=127\"}\n\n"

	got := ExtractTextFromSSE([]byte(body))
	if strings.Contains(got, "RP_ANSWER=127") {
		t.Errorf("上一帧的事件类型不该粘到下一帧，抽到了 %q", got)
	}
	if got != "ok" {
		t.Errorf("本帧内的正文应照常抽到，得到 %q", got)
	}
}

// TestExtractText_OtherProtocolsUnchanged 是回归保护：
// Anthropic / OpenAI Chat / Gemini 三种形态靠字段结构就能唯一识别，
// 不属于 Responses 协议，不该被事件白名单误伤。
func TestExtractText_OtherProtocolsUnchanged(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "anthropic content_block_delta",
			body: sseFrame("content_block_delta",
				`{"type":"content_block_delta","delta":{"type":"text_delta","text":"RP_ANSWER=127"}}`),
			want: "RP_ANSWER=127",
		},
		{
			name: "anthropic 跨 chunk 拆分",
			body: sseFrame("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"RP_ANS"}}`) +
				sseFrame("content_block_delta", `{"type":"content_block_delta","delta":{"type":"text_delta","text":"WER=127"}}`),
			want: "RP_ANSWER=127",
		},
		{
			name: "openai chat chunk",
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"RP_ANSWER=127\"}}]}\n\n",
			want: "RP_ANSWER=127",
		},
		{
			name: "gemini 无 event 行",
			body: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"RP_ANSWER=127\"}]}}]}\n\n",
			want: "RP_ANSWER=127",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractTextFromSSE([]byte(tt.body)); got != tt.want {
				t.Errorf("抽取结果 = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAggregateResponseText_NoRawFallbackForSSE ——
// SSE 抽不到正文时匹配对象是**空文本**，不再是整包协议信封。
// 回退的危害是具体的：信封里有事件名、request-id、usage 和思考摘要，
// success_contains 撞上其中任何一处都会把「模型没输出」判成绿。
func TestAggregateResponseText_NoRawFallbackForSSE(t *testing.T) {
	// 关键字只出现在协议信封里（思考摘要），正文一个字没有
	body := sseFrame("response.reasoning_summary_text.delta",
		`{"type":"response.reasoning_summary_text.delta","delta":"answer is RP_ANSWER=127"}`)

	text, source := aggregateResponseText([]byte(body))
	if text != "" {
		t.Errorf("SSE 抽不到正文时应返回空，得到 %q", text)
	}
	if source != textSourceNone {
		t.Errorf("source = %q, want %q", source, textSourceNone)
	}
	if strings.Contains(AggregateResponseText([]byte(body)), "RP_ANSWER=127") {
		t.Error("协议信封里的关键字泄进了匹配对象，假绿又回来了")
	}
}

// TestAggregateResponseText_NonSSEStillUsesWholeBody ——
// 非流式响应**不受影响**：响应体本身就是模型输出（或上游错误体），整体即匹配对象。
// 生产上走 raw 的绝大多数是 401/429/503 错误体，收窄回退绝不能碰到它们。
func TestAggregateResponseText_NonSSEStillUsesWholeBody(t *testing.T) {
	body := []byte(`{"content":[{"type":"text","text":"RP_ANSWER=127"}]}`)

	text, source := aggregateResponseText(body)
	if source != textSourceBody {
		t.Fatalf("source = %q, want %q", source, textSourceBody)
	}
	if text != string(body) {
		t.Errorf("非流式响应应整体作为匹配对象，得到 %q", text)
	}

	if _, source := aggregateResponseText(nil); source != textSourceEmpty {
		t.Errorf("空响应体 source = %q, want %q", source, textSourceEmpty)
	}
}

// TestResponseSnippetText_FallsBackForDisplay ——
// 展示用语义与匹配用**刻意相反**：抽不到正文时退回响应体原文。
// 日志与管理后台要的是「看懂发生了什么」，此时上游错误信封正是关键信息。
func TestResponseSnippetText_FallsBackForDisplay(t *testing.T) {
	body := []byte(sseFrame("error",
		`{"type":"error","message":"Our servers are currently overloaded."}`))

	if got := AggregateResponseText(body); got != "" {
		t.Errorf("匹配对象应为空，得到 %q", got)
	}
	snippet := ResponseSnippetText(body)
	if !strings.Contains(snippet, "overloaded") {
		t.Errorf("展示片段应退回响应体原文，得到 %q", snippet)
	}

	// 抽得到正文时给正文，而不是整包信封
	ok := []byte(sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"RP_ANSWER=127"}`))
	if got := ResponseSnippetText(ok); got != "RP_ANSWER=127" {
		t.Errorf("抽得到正文时应给正文，得到 %q", got)
	}
}
