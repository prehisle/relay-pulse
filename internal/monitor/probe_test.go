package monitor

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"

	"monitor/internal/config"
	"monitor/internal/storage"
)

// --- existing evaluateStatus tests ---

func TestEvaluateStatusWithoutSuccessContains(t *testing.T) {
	t.Parallel()

	status, subStatus := evaluateStatus(1, storage.SubStatusNone, []byte("anything"), "")
	if status != 1 {
		t.Fatalf("expected status 1 when success_contains is empty, got %d", status)
	}
	if subStatus != storage.SubStatusNone {
		t.Fatalf("expected SubStatusNone, got %s", subStatus)
	}
}

func TestEvaluateStatusWithMatchingContent(t *testing.T) {
	t.Parallel()

	body := []byte(`{"ok":true,"message":"pong"}`)
	status, subStatus := evaluateStatus(1, storage.SubStatusNone, body, "pong")
	if status != 1 {
		t.Fatalf("expected status 1 when body contains keyword, got %d", status)
	}
	if subStatus != storage.SubStatusNone {
		t.Fatalf("expected SubStatusNone, got %s", subStatus)
	}
}

func TestEvaluateStatusWithNonMatchingContent(t *testing.T) {
	t.Parallel()

	body := []byte(`{"ok":false,"message":"error"}`)
	status, subStatus := evaluateStatus(1, storage.SubStatusNone, body, "pong")
	if status != 0 {
		t.Fatalf("expected status 0 when body does not contain keyword, got %d", status)
	}
	if subStatus != storage.SubStatusContentMismatch {
		t.Fatalf("expected SubStatusContentMismatch, got %s", subStatus)
	}
}

func TestEvaluateStatusWithStreamingContentSplit(t *testing.T) {
	t.Parallel()

	// 模拟 SSE 流式增量：先返回 "p"，再返回 "ong"
	body := []byte(
		"event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"p\"}}\n\n" +
			"event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ong\"}}\n\n",
	)

	status, subStatus := evaluateStatus(1, storage.SubStatusNone, body, "pong")
	if status != 1 {
		t.Fatalf("expected status 1 for streaming body containing aggregated keyword, got %d", status)
	}
	if subStatus != storage.SubStatusNone {
		t.Fatalf("expected SubStatusNone for streaming body, got %s", subStatus)
	}
}

func TestEvaluateStatusWithGeminiSSE(t *testing.T) {
	t.Parallel()

	// 模拟 Gemini SSE 格式：没有 event: 行，只有 data: 行
	// 流式响应中 text 被拆分到多个 chunk
	body := []byte(
		`data: {"candidates":[{"content":{"parts":[{"text":"po"}],"role":"model"},"index":0}]}` + "\n\n" +
			`data: {"candidates":[{"content":{"parts":[{"text":"ng"}],"role":"model"},"index":0}]}` + "\n\n",
	)

	status, subStatus := evaluateStatus(1, storage.SubStatusNone, body, "pong")
	if status != 1 {
		t.Fatalf("expected status 1 for Gemini SSE body containing aggregated keyword, got %d", status)
	}
	if subStatus != storage.SubStatusNone {
		t.Fatalf("expected SubStatusNone for Gemini SSE body, got %s", subStatus)
	}
}

func TestEvaluateStatusWithGeminiSSENoEventLine(t *testing.T) {
	t.Parallel()

	// 完整的 Gemini 响应示例（只有 data: 行，文本完整在一个 chunk）
	body := []byte(
		`data: {"candidates":[{"content":{"parts":[{"text":"pong"}],"role":"model"},"index":0}],"usageMetadata":{"promptTokenCount":8}}` + "\n\n" +
			`data: {"candidates":[{"content":{"parts":[{"text":"","thoughtSignature":"xxx"}],"role":"model"},"finishReason":"MAX_TOKENS","index":0}]}` + "\n\n",
	)

	status, subStatus := evaluateStatus(1, storage.SubStatusNone, body, "pong")
	if status != 1 {
		t.Fatalf("expected status 1 for Gemini SSE with complete text, got %d", status)
	}
	if subStatus != storage.SubStatusNone {
		t.Fatalf("expected SubStatusNone, got %s", subStatus)
	}
}

// --- determineStatus ---

func TestDetermineStatus(t *testing.T) {
	t.Parallel()

	prober := NewProber(nil, nil)
	slow := 100 * time.Millisecond

	cases := []struct {
		name       string
		code       int
		latency    int
		wantStatus int
		wantSub    storage.SubStatus
	}{
		{"200_fast", 200, 50, 1, storage.SubStatusNone},
		{"200_slow", 200, 200, 2, storage.SubStatusSlowLatency},
		{"200_at_threshold", 200, 100, 1, storage.SubStatusNone},   // equal to threshold, not exceeding
		{"201_created", 201, 10, 1, storage.SubStatusNone},         // other 2xx
		{"204_no_content", 204, 10, 1, storage.SubStatusNone},      // other 2xx
		{"301_redirect", 301, 10, 0, storage.SubStatusClientError}, // 3xx 判红：client 已自动跟随合规重定向，漏到此处的是畸形重定向
		{"302_redirect", 302, 10, 0, storage.SubStatusClientError}, // 同上
		{"304_not_modified", 304, 10, 0, storage.SubStatusClientError},
		{"400_bad_request", 400, 10, 0, storage.SubStatusInvalidRequest},
		{"401_unauthorized", 401, 10, 0, storage.SubStatusAuthError},
		{"403_forbidden", 403, 10, 0, storage.SubStatusAuthError},
		{"404_not_found", 404, 10, 0, storage.SubStatusClientError},
		{"418_teapot", 418, 10, 0, storage.SubStatusClientError},
		{"429_rate_limit", 429, 10, 0, storage.SubStatusRateLimit},
		{"500_internal", 500, 10, 0, storage.SubStatusServerError},
		{"502_bad_gateway", 502, 10, 0, storage.SubStatusServerError},
		{"503_unavailable", 503, 10, 0, storage.SubStatusServerError},
		{"100_continue", 100, 10, 0, storage.SubStatusClientError},
		{"0_unknown", 0, 10, 0, storage.SubStatusClientError},
		{"200_no_slow_config", 200, 9999, 1, storage.SubStatusNone}, // slowLatency=0 means no threshold
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sl := slow
			if tc.name == "200_no_slow_config" {
				sl = 0
			}
			status, sub := prober.determineStatus(tc.code, tc.latency, sl)
			if status != tc.wantStatus {
				t.Errorf("status: want %d, got %d", tc.wantStatus, status)
			}
			if sub != tc.wantSub {
				t.Errorf("subStatus: want %q, got %q", tc.wantSub, sub)
			}
		})
	}
}

// --- computeRetryDelay ---

func TestComputeRetryDelay_ExponentialBackoff(t *testing.T) {
	t.Parallel()

	base := 100 * time.Millisecond
	max := 800 * time.Millisecond

	// No jitter → deterministic
	if got := computeRetryDelay(0, base, max, 0); got != 100*time.Millisecond {
		t.Errorf("index=0: want 100ms, got %v", got)
	}
	if got := computeRetryDelay(1, base, max, 0); got != 200*time.Millisecond {
		t.Errorf("index=1: want 200ms, got %v", got)
	}
	if got := computeRetryDelay(2, base, max, 0); got != 400*time.Millisecond {
		t.Errorf("index=2: want 400ms, got %v", got)
	}
	if got := computeRetryDelay(3, base, max, 0); got != 800*time.Millisecond {
		t.Errorf("index=3: want 800ms (maxDelay), got %v", got)
	}
	if got := computeRetryDelay(10, base, max, 0); got != 800*time.Millisecond {
		t.Errorf("index=10: want 800ms (capped), got %v", got)
	}
}

func TestComputeRetryDelay_Jitter(t *testing.T) {
	t.Parallel()

	base := 200 * time.Millisecond
	max := 2 * time.Second
	jitter := 0.2

	// Run multiple times to verify range
	for i := 0; i < 50; i++ {
		got := computeRetryDelay(1, base, max, jitter)
		// index=1 → 400ms ±20% → [320ms, 480ms]
		lo := time.Duration(float64(400*time.Millisecond) * 0.8)
		hi := time.Duration(float64(400*time.Millisecond) * 1.2)
		if got < lo || got > hi {
			t.Fatalf("jitter: want [%v, %v], got %v", lo, hi, got)
		}
	}
}

// --- decompression ---

func TestDecompressBody(t *testing.T) {
	t.Parallel()

	payload := []byte("hello decompression test")
	gzData := mustGzip(t, payload)
	brData := mustBrotli(t, payload)
	zstdData := mustZstd(t, payload)
	deflData := mustDeflate(t, payload)

	cases := []struct {
		name     string
		data     []byte
		encoding string
		want     []byte
	}{
		{"gzip_header", gzData, "gzip", payload},
		{"gzip_magic_no_header", gzData, "", payload},
		{"brotli", brData, "br", payload},
		{"zstd", zstdData, "zstd", payload},
		{"deflate", deflData, "deflate", payload},
		{"plain_text", payload, "", payload},
		{"empty_body", []byte{}, "", []byte{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: make(http.Header)}
			if tc.encoding != "" {
				resp.Header.Set("Content-Encoding", tc.encoding)
			}
			got := decompressBodyIfNeeded(resp, tc.data, "p", "s", "c", "m")
			if !bytes.Equal(got, tc.want) {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// --- magic byte helpers ---

func TestIsGzipMagic(t *testing.T) {
	t.Parallel()

	if !isGzipMagic([]byte{0x1f, 0x8b, 0x00}) {
		t.Error("expected gzip magic detected")
	}
	if isGzipMagic([]byte{0x00, 0x00}) {
		t.Error("false positive for non-gzip data")
	}
	if isGzipMagic([]byte{0x1f}) {
		t.Error("should not detect with only 1 byte")
	}
}

func TestIsZstdMagic(t *testing.T) {
	t.Parallel()

	if !isZstdMagic([]byte{0x28, 0xB5, 0x2F, 0xFD, 0x00}) {
		t.Error("expected zstd magic detected")
	}
	if isZstdMagic([]byte{0x28, 0xB5, 0x2F}) {
		t.Error("should not detect with 3 bytes")
	}
}

func TestLooksBinary(t *testing.T) {
	t.Parallel()

	if looksBinary([]byte("hello world")) {
		t.Error("plain text should not look binary")
	}
	if !looksBinary([]byte{0x00, 0x01, 0x02}) {
		t.Error("null bytes should look binary")
	}
	if looksBinary(nil) {
		t.Error("nil should not look binary")
	}
}

// --- Probe integration with httptest ---

func newTestCfg(url string) config.ServiceConfig {
	return config.ServiceConfig{
		Provider:            "test-provider",
		Service:             "test-service",
		Channel:             "test-ch",
		Model:               "test-model",
		BaseURL:             url,
		URLPattern:          "{{BASE_URL}}",
		Method:              http.MethodGet,
		Headers:             map[string]string{},
		SlowLatencyDuration: 200 * time.Millisecond,
		TimeoutDuration:     2 * time.Second,
	}
}

func TestProbe_200OK(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "pong"

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Errorf("want status 1, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	if result.HttpCode != 200 {
		t.Errorf("want httpCode 200, got %d", result.HttpCode)
	}
}

func TestProbe_500ServerError(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 {
		t.Errorf("want status 0, got %d", result.Status)
	}
	if result.SubStatus != storage.SubStatusServerError {
		t.Errorf("want subStatus server_error, got %s", result.SubStatus)
	}
	if !strings.Contains(result.ResponseSnippet, "internal error") {
		t.Errorf("want response snippet to include error body, got %q", result.ResponseSnippet)
	}
}

func TestProbe_429RateLimit(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 || result.SubStatus != storage.SubStatusRateLimit {
		t.Errorf("want (0, rate_limit), got (%d, %s)", result.Status, result.SubStatus)
	}
	if !strings.Contains(result.ResponseSnippet, "rate limited") {
		t.Errorf("want response snippet to include rate limit body, got %q", result.ResponseSnippet)
	}
}

func TestProbe_401AuthErrorCapturesResponseSnippet(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 {
		t.Fatalf("want status 0, got %d", result.Status)
	}
	if result.SubStatus != storage.SubStatusAuthError {
		t.Fatalf("want auth_error, got %s", result.SubStatus)
	}
	if !strings.Contains(result.ResponseSnippet, "unauthorized") {
		t.Errorf("want response snippet to include auth body, got %q", result.ResponseSnippet)
	}
}

func TestProbe_RetrySuccess(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(500)
			_, _ = w.Write([]byte("error"))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "pong"
	cfg.RetryCount = 2
	cfg.RetryBaseDelayDuration = 1 * time.Millisecond
	cfg.RetryMaxDelayDuration = 5 * time.Millisecond
	cfg.RetryJitterValue = 0

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Errorf("want status 1 after retry, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("want 2 attempts, got %d", got)
	}
}

func TestProbe_Timeout(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("late body"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.TimeoutDuration = 50 * time.Millisecond

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 {
		t.Errorf("want status 0 on timeout, got %d", result.Status)
	}
	if result.SubStatus != storage.SubStatusNetworkError {
		t.Errorf("want subStatus network_error, got %s", result.SubStatus)
	}
	if strings.Contains(result.ResponseSnippet, "late body") {
		t.Errorf("network timeout should not retain response body, got %q", result.ResponseSnippet)
	}
}

func TestProbe_ContextCancel(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(ctx, &cfg)
	if result.Status != 0 {
		t.Errorf("want status 0 on cancelled context, got %d", result.Status)
	}
}

func TestProbe_ContentMismatch(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"result":"wrong"}`))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "pong"

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 || result.SubStatus != storage.SubStatusContentMismatch {
		t.Errorf("want (0, content_mismatch), got (%d, %s)", result.Status, result.SubStatus)
	}
	if !strings.Contains(result.ResponseSnippet, "wrong") {
		t.Errorf("want content mismatch snippet to include body, got %q", result.ResponseSnippet)
	}
}

func TestProbe_200WithoutSuccessContainsDoesNotCaptureBody(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok body should only be drained"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Fatalf("want status 1, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	if result.ResponseSnippet != "" {
		t.Errorf("2xx without success_contains should not keep snippet, got %q", result.ResponseSnippet)
	}
}

func TestProbe_POST(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.Method = http.MethodPost
	cfg.Body = `{"test": true}`

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Errorf("want status 1 for POST, got %d (sub: %s)", result.Status, result.SubStatus)
	}
}

// --- completion latency tests ---

func TestProbe_LatencyIncludesBodyRead(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	bodyDelay := 150 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		// Flush headers immediately, then delay the body
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "pong"
	cfg.SlowLatencyDuration = 5 * time.Second // high threshold so it stays green

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Fatalf("want status 1, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	// Latency must include body read time (≥ bodyDelay)
	if result.Latency < int(bodyDelay.Milliseconds()) {
		t.Errorf("latency %dms should include body read delay ≥ %dms", result.Latency, bodyDelay.Milliseconds())
	}
}

func TestProbe_SlowLatencyBasedOnCompletionTime(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	bodyDelay := 150 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "pong"
	// Header arrives fast (~0ms), but body takes 150ms.
	// Set threshold between header time and completion time
	// so that only completion-based measurement triggers yellow.
	cfg.SlowLatencyDuration = 100 * time.Millisecond

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 2 {
		t.Errorf("want status 2 (slow_latency) based on completion time, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	if result.SubStatus != storage.SubStatusSlowLatency {
		t.Errorf("want sub_status slow_latency, got %s", result.SubStatus)
	}
}

func TestProbe_DrainPathIncludesBodyRead(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	bodyDelay := 150 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	// No SuccessContains → drain path
	cfg.SlowLatencyDuration = 5 * time.Second

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 1 {
		t.Fatalf("want status 1, got %d (sub: %s)", result.Status, result.SubStatus)
	}
	// Latency must include drain time (≥ bodyDelay)
	if result.Latency < int(bodyDelay.Milliseconds()) {
		t.Errorf("latency %dms should include drain delay ≥ %dms", result.Latency, bodyDelay.Milliseconds())
	}
}

func TestReadBodyPrefixAndDrain(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("a", httpFailureBodyCaptureLimit) + "TAIL"
	captured, err := readBodyPrefixAndDrain(strings.NewReader(body), httpFailureBodyCaptureLimit)
	if err != nil {
		t.Fatalf("readBodyPrefixAndDrain error: %v", err)
	}
	if len(captured) != httpFailureBodyCaptureLimit {
		t.Fatalf("want captured len %d, got %d", httpFailureBodyCaptureLimit, len(captured))
	}
	if strings.Contains(string(captured), "TAIL") {
		t.Fatalf("captured prefix should not contain tail marker")
	}
}

func TestProbe_HTTPFailureCapturesLimitedPrefixAndDrainLatency(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	bodyDelay := 150 * time.Millisecond
	prefix := "error-prefix: invalid api key\n"
	tail := strings.Repeat("x", httpFailureBodyCaptureLimit+1024)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(500)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte(prefix))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(bodyDelay)
		_, _ = w.Write([]byte(tail))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 {
		t.Fatalf("want status 0, got %d", result.Status)
	}
	if result.SubStatus != storage.SubStatusServerError {
		t.Fatalf("want server_error, got %s", result.SubStatus)
	}
	if result.Latency < int(bodyDelay.Milliseconds()) {
		t.Errorf("latency %dms should include HTTP failure drain delay ≥ %dms", result.Latency, bodyDelay.Milliseconds())
	}
	if !strings.Contains(result.ResponseSnippet, "error-prefix") {
		t.Errorf("want response snippet to include prefix, got %q", result.ResponseSnippet)
	}
	if strings.Contains(result.ResponseSnippet, strings.Repeat("x", 2048)) {
		t.Errorf("response snippet should not include oversized tail")
	}
}

// --- compression test helpers ---

func mustGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func mustBrotli(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := brotli.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("brotli write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("brotli close: %v", err)
	}
	return buf.Bytes()
}

func mustZstd(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

func mustDeflate(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("deflate write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("deflate close: %v", err)
	}
	return buf.Bytes()
}

// TestProbe_ContentMismatchSnippetIsDiagnostic 端到端锁住调度器侧的摘要形态：
// 上游 200 开了 SSE 流却没产出任何正文（生产上 saiai/cx 那条 GPT-5.6-Luna 的真实形态）。
// 旧实现在这里写库的是响应体**头部**——恒定的 response.created 握手，排障时等于没有。
func TestProbe_ContentMismatchSnippetIsDiagnostic(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	// 握手事件塞长，逼摘要必须做截断选择；真正的失败原因只在尾部
	padding := strings.Repeat("x", 600)
	body := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_` + padding + `","status":"in_progress","error":null}}` + "\n\n" +
		"event: response.failed\n" +
		`data: {"type":"response.failed","response":{"status":"failed","error":{"message":"upstream closed"}}}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.SuccessContains = "RP_ANSWER=79"

	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 || result.SubStatus != storage.SubStatusContentMismatch {
		t.Fatalf("want (0, content_mismatch), got (%d, %s)", result.Status, result.SubStatus)
	}

	for _, want := range []string{
		`expected="RP_ANSWER=79"`,
		"extracted=0chars",
		"last_event=response.failed",
		`error="upstream closed"`,
	} {
		if !strings.Contains(result.ResponseSnippet, want) {
			t.Errorf("写库摘要缺少 %q\n实际:\n%s", want, result.ResponseSnippet)
		}
	}
	if strings.HasPrefix(result.ResponseSnippet, "event: response.created") {
		t.Errorf("摘要不该再是响应体头部，实际:\n%s", result.ResponseSnippet)
	}
}

// TestProbe_ContentMismatchSnippetRecordsRandomizedKeyword 固定「每次重试都换一道随机题、
// 摘要必须记的是最后一次那道」这条契约：不记就无法事后复核判红是否正确。
func TestProbe_ContentMismatchSnippetRecordsRandomizedKeyword(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"answer":"nope"}`))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	cfg.Method = http.MethodPost
	cfg.Body = `{"prompt":"{{PROMPT}}"}`
	cfg.SuccessContains = "{{EXPECTED_ANSWER}}"

	result := prober.Probe(context.Background(), &cfg)
	if result.SubStatus != storage.SubStatusContentMismatch {
		t.Fatalf("want content_mismatch, got %s", result.SubStatus)
	}
	// 占位符必须已被注入后的真实关键字替换，而不是原样落进摘要
	if strings.Contains(result.ResponseSnippet, "{{EXPECTED_ANSWER}}") {
		t.Errorf("摘要记的应是注入后的关键字，实际:\n%s", result.ResponseSnippet)
	}
	if !strings.Contains(result.ResponseSnippet, `expected="RP_ANSWER=`) {
		t.Errorf("摘要应带上本次随机题的预期答案，实际:\n%s", result.ResponseSnippet)
	}
}

// TestProbe_SnippetIsAlwaysValidUTF8 —— 摘要要写进 Postgres 的 error_detail(TEXT)，
// 而 Postgres 会以 `invalid byte sequence for encoding "UTF8"` 拒收非法字节序列，
// 那会让**整条探测记录**写不进库。响应体在 readBodyPrefixAndDrain 处按 512 **字节**
// 截取，非 ASCII 的上游错误体必然可能被从字符中间劈开。
func TestProbe_SnippetIsAlwaysValidUTF8(t *testing.T) {
	prober := NewProber(nil, nil)
	defer prober.Close()

	// 中文错误体远超 512 字节的捕获上限，截断点几乎必然落在汉字中间
	longChinese := `{"error":"` + strings.Repeat("上游服务暂时不可用", 100) + `"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(longChinese))
	}))
	defer srv.Close()

	cfg := newTestCfg(srv.URL)
	result := prober.Probe(context.Background(), &cfg)
	if result.Status != 0 {
		t.Fatalf("want red, got %d", result.Status)
	}
	if !utf8.ValidString(result.ResponseSnippet) {
		t.Errorf("写库摘要含非法 UTF-8，Postgres 会拒收整条记录\n摘要尾部: %q",
			result.ResponseSnippet[max(0, len(result.ResponseSnippet)-16):])
	}
}
