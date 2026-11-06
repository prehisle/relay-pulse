package probe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"monitor/internal/config"
	"monitor/internal/identity"
	"monitor/internal/monitor"
)

// classifyHTTPStatus 的 sub_status 字符串必须与 scheduler/storage 口径
// （monitor/probe.go）一致，否则前端 i18n 与可用率统计会按两套字符串割裂。
// 429 历史上 inline 误用 "rate_limited"（带 -ed），此处锁定为 "rate_limit"。
func TestClassifyHTTPStatus_SubStatusMatchesScheduler(t *testing.T) {
	const slow = 5 * time.Second
	cases := []struct {
		name       string
		statusCode int
		latency    int
		wantStatus int
		wantSub    string
	}{
		{"rate_limit", 429, 100, 0, "rate_limit"},
		{"auth_error", 401, 100, 0, "auth_error"},
		{"invalid_request", 400, 100, 0, "invalid_request"},
		{"server_error", 503, 100, 0, "server_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotSub := classifyHTTPStatus(tc.statusCode, tc.latency, slow)
			if gotStatus != tc.wantStatus || gotSub != tc.wantSub {
				t.Errorf("classifyHTTPStatus(%d) = (%d, %q), want (%d, %q)",
					tc.statusCode, gotStatus, gotSub, tc.wantStatus, tc.wantSub)
			}
		})
	}
}

// TestInternalProber_InjectsUserID 验证 InlineProber 走真实 UserIDManager 时
// 生成的请求 body 里 metadata.user_id 非空且格式正确。回归 TopRouterCN 403 bug。
func TestInternalProber_InjectsUserID(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"answer":"RP_ANSWER=7"}`))
	}))
	defer srv.Close()

	cfg := &config.ServiceConfig{
		Provider:        "probe",
		Service:         "cc",
		Channel:         "",
		BaseURL:         srv.URL,
		URLPattern:      srv.URL,
		APIKey:          "test-key",
		Method:          "POST",
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            `{"metadata":{"user_id":"{{USER_ID}}"}}`,
		TimeoutDuration: 5 * time.Second,
	}

	// 构造 internalProber 时跳过 SSRF（httptest 用 127.0.0.1，SSRF 会拒）
	p := &internalProber{
		client:       srv.Client(),
		maxBodyBytes: DefaultMaxResponseBytes,
		uidMgr:       identity.NewUserIDManager(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := p.probe(ctx, cfg, false, "")
	if result.Err != nil {
		t.Fatalf("probe failed: %v", result.Err)
	}
	if result.HTTPCode != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", result.HTTPCode)
	}

	var parsed struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("captured body not JSON: %v\nbody=%s", err, capturedBody)
	}

	uidPattern := regexp.MustCompile(`^user_[0-9a-f]{64}_account__session_[0-9a-f-]+$`)
	if !uidPattern.MatchString(parsed.Metadata.UserID) {
		t.Fatalf("metadata.user_id does not match expected format: %q", parsed.Metadata.UserID)
	}
}

// TestInternalProber_NilUidMgrLeavesUserIDEmpty 文档化旧 bug 行为：不传 uidMgr
// 时 user_id 为空字符串（这是我们修好的 403 路径）。
func TestInternalProber_NilUidMgrLeavesUserIDEmpty(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.ServiceConfig{
		Provider:        "probe",
		Service:         "cc",
		BaseURL:         srv.URL,
		URLPattern:      srv.URL,
		APIKey:          "test-key",
		Method:          "POST",
		Headers:         map[string]string{"Content-Type": "application/json"},
		Body:            `{"metadata":{"user_id":"{{USER_ID}}"}}`,
		TimeoutDuration: 5 * time.Second,
	}

	p := &internalProber{
		client:       srv.Client(),
		maxBodyBytes: DefaultMaxResponseBytes,
		uidMgr:       nil,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = p.probe(ctx, cfg, false, "")

	if !strings.Contains(string(capturedBody), `"user_id":""`) {
		t.Fatalf("expected empty user_id when uidMgr is nil; body=%s", capturedBody)
	}
}

// TestInlineProbe_ContentMismatchSnippetMatchesScheduler 锁住 inline 与 scheduler 两条
// 探测路径对同一次内容校验失败给出**逐字相同**的说法。inline.go 的 snippet 逻辑是
// monitor/probe.go 的平行拷贝，历史上正是这种拷贝导致「点探测」与「探测历史」
// 对同一个失败各说各话，排障时无从判断该信哪个。
func TestInlineProbe_ContentMismatchSnippetMatchesScheduler(t *testing.T) {
	const keyword = "RP_ANSWER=79"
	body := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1","status":"in_progress","error":null}}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"upstream closed"}}}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := &config.ServiceConfig{
		Provider:        "probe",
		Service:         "cx",
		BaseURL:         srv.URL,
		URLPattern:      "{{BASE_URL}}",
		Method:          http.MethodGet,
		Headers:         map[string]string{},
		SuccessContains: keyword,
		TimeoutDuration: 5 * time.Second,
	}

	// 跳过 SSRF（httptest 用 127.0.0.1，SSRF 会拒）
	p := &internalProber{
		client:       srv.Client(),
		maxBodyBytes: DefaultMaxResponseBytes,
		uidMgr:       identity.NewUserIDManager(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result := p.probe(ctx, cfg, false, "")
	if result.Status != 0 || result.SubStatus != "content_mismatch" {
		t.Fatalf("want (0, content_mismatch), got (%d, %q)", result.Status, result.SubStatus)
	}

	want := monitor.BuildContentMismatchSummary([]byte(body), keyword)
	if result.ResponseSnippet != want {
		t.Errorf("inline 摘要与 scheduler 口径不一致\ngot:\n%s\nwant:\n%s", result.ResponseSnippet, want)
	}
	if !strings.Contains(result.ResponseSnippet, "last_event=response.failed") {
		t.Errorf("摘要应指认终止事件，实际:\n%s", result.ResponseSnippet)
	}
}

// TestInlineProbe_EchoedAPIKeyFailsContentCheck 锁住回显检测：2xx 响应里出现本次请求的 key
// （原文或 JSON 转义后的样子）就判 content_mismatch，哪怕 success_contains 命中。这是 2026-11
// 生产实际被用的手法——ping 模板的 `pong` 写在请求里，httpbin /anything 把整个请求连同
// 认证头吐回来即判绿。返回给调用方的摘要里 key 必须已脱敏。
func TestInlineProbe_EchoedAPIKeyFailsContentCheck(t *testing.T) {
	const apiKey = "sk-echo/Test+Key<1234567890"
	cases := []struct {
		name   string
		echo   func(r *http.Request, body []byte) []byte
		wantOK bool
	}{
		{
			name: "httpbin 式回显（请求头 JSON 化，含 key 原文）",
			echo: func(r *http.Request, body []byte) []byte {
				out, _ := json.Marshal(map[string]any{
					"headers": map[string]string{"Authorization": r.Header.Get("Authorization")},
					"data":    string(body),
				})
				return out
			},
		},
		{
			name: "斜杠被转义成 \\/ 的回显",
			echo: func(r *http.Request, body []byte) []byte {
				out, _ := json.Marshal(map[string]any{
					"headers": map[string]string{"Authorization": r.Header.Get("Authorization")},
					"data":    string(body),
				})
				return []byte(strings.ReplaceAll(string(out), "/", `\/`))
			},
		},
		{
			name:   "正常模型应答不受影响",
			echo:   func(*http.Request, []byte) []byte { return []byte(`{"content":[{"type":"text","text":"pong"}]}`) },
			wantOK: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(tc.echo(r, body))
			}))
			defer srv.Close()

			cfg := &config.ServiceConfig{
				Provider:        "probe",
				Service:         "cc",
				BaseURL:         srv.URL,
				URLPattern:      "{{BASE_URL}}",
				Method:          http.MethodPost,
				Headers:         map[string]string{"Authorization": "Bearer {{API_KEY}}"},
				Body:            `{"system":"Only reply pong.","messages":[{"role":"user","content":"ping"}]}`,
				APIKey:          apiKey,
				SuccessContains: "pong",
				TimeoutDuration: 5 * time.Second,
			}
			p := &internalProber{
				client:       srv.Client(),
				maxBodyBytes: DefaultMaxResponseBytes,
				uidMgr:       identity.NewUserIDManager(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result := p.probe(ctx, cfg, false, "")
			if tc.wantOK {
				if result.Status != 1 {
					t.Fatalf("正常应答应判绿，实际 (%d, %q) err=%v", result.Status, result.SubStatus, result.Err)
				}
				return
			}
			if result.Status != 0 || result.SubStatus != "content_mismatch" {
				t.Fatalf("回显应判 (0, content_mismatch)，实际 (%d, %q)", result.Status, result.SubStatus)
			}
			if result.Err == nil || !strings.Contains(result.Err.Error(), "回显") {
				t.Errorf("错误信息应说明是回显，实际: %v", result.Err)
			}
			for _, form := range echoedKeyForms(apiKey) {
				if strings.Contains(result.ResponseSnippet, form) {
					t.Errorf("摘要里不应出现 key（形态 %q）:\n%s", form, result.ResponseSnippet)
				}
			}
			if strings.Contains(result.ResponseSnippet, "1234567890") {
				t.Errorf("摘要里不应残留 key 片段:\n%s", result.ResponseSnippet)
			}
		})
	}
}

// TestInlineProbe_EchoFormsAndRedactionEdges 补三种边界：
//   - 手写的 JSON 回显，`/` 写成 `\/` 而 `<` 保留原样（多数语言序列化器关 HTML 转义时的样子），
//     不能靠 Go json.Marshal 生成样本，否则只是在验证实现自己生成的组合；
//   - 非 2xx 响应同样脱敏（这条路径不进回显判定，但摘要会返回给调用方）；
//   - key 正好跨过 512 字节截断点时不能残留前半截（先脱敏再截断）。
func TestInlineProbe_EchoFormsAndRedactionEdges(t *testing.T) {
	const apiKey = "sk-edge/Key<abcdef123"
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
	}{
		{
			name:       "手写 JSON：斜杠转义、尖括号原样",
			status:     http.StatusOK,
			body:       `{"headers":{"Authorization":"Bearer sk-edge\/Key<abcdef123"},"data":"Only reply pong."}`,
			wantStatus: 0,
		},
		{
			name:       "非 2xx 回显也脱敏",
			status:     http.StatusUnauthorized,
			body:       `{"error":"invalid key: sk-edge/Key<abcdef123"}`,
			wantStatus: 0,
		},
		{
			name:       "key 跨过 512 字节截断点",
			status:     http.StatusUnauthorized,
			body:       strings.Repeat("x", 500) + apiKey + strings.Repeat("y", 100),
			wantStatus: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			cfg := &config.ServiceConfig{
				Provider: "probe", Service: "cc", BaseURL: srv.URL, URLPattern: "{{BASE_URL}}",
				Method: http.MethodPost, Headers: map[string]string{"Authorization": "Bearer {{API_KEY}}"},
				Body: `{}`, APIKey: apiKey, SuccessContains: "pong", TimeoutDuration: 5 * time.Second,
			}
			p := &internalProber{client: srv.Client(), maxBodyBytes: DefaultMaxResponseBytes, uidMgr: identity.NewUserIDManager()}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			result := p.probe(ctx, cfg, false, "")
			if result.Status != tc.wantStatus {
				t.Fatalf("status 期望 %d，实际 %d (%q)", tc.wantStatus, result.Status, result.SubStatus)
			}
			if tc.status == http.StatusOK && (result.Err == nil || !strings.Contains(result.Err.Error(), "回显")) {
				t.Errorf("2xx 回显应按回显拒绝，实际 err=%v", result.Err)
			}
			for _, leak := range []string{"abcdef123", `sk-edge\/Key`, "sk-edge/Key"} {
				if strings.Contains(result.ResponseSnippet, leak) {
					t.Errorf("摘要残留 key 片段 %q:\n%s", leak, result.ResponseSnippet)
				}
			}
		})
	}
}
