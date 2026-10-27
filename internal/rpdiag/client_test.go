package rpdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testNow is a fixed reference clock so the staleness gate (scoreStaleWindow)
// is deterministic regardless of wall-clock time. Test clients pin nowFn to it
// via newTestClient/fixedClock; fixtures stamp latest_at relative to it.
var testNow = time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

func fixedClock() time.Time { return testNow }

// freshAt returns a latest_at within the current-signal window (1h old).
func freshAt() *string {
	s := testNow.Add(-time.Hour).Format(time.RFC3339Nano)
	return &s
}

// staleAt returns a latest_at older than scoreStaleWindow.
func staleAt() *string {
	s := testNow.Add(-(scoreStaleWindow + time.Hour)).Format(time.RFC3339Nano)
	return &s
}

func TestNormalizeService(t *testing.T) {
	cases := map[string]string{
		"claude":  "cc",
		"codex":   "cx",
		"gemini":  "gm",
		"CLAUDE":  "cc",
		"unknown": "unknown",
		"":        "",
	}
	for in, want := range cases {
		if got := normalizeService(in); got != want {
			t.Errorf("normalizeService(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScoreKey(t *testing.T) {
	got := ScoreKey("SAIAi", "claude", "O-Max")
	want := "saiai|claude|o-max"
	if got != want {
		t.Errorf("ScoreKey = %q, want %q", got, want)
	}
}

func TestEnabledFromEnv(t *testing.T) {
	on := []string{"1", "true", "TRUE", "yes", "on", " On "}
	off := []string{"", "0", "false", "no", "off", "anything-else"}
	for _, raw := range on {
		if !enabledFromEnv(raw) {
			t.Errorf("enabledFromEnv(%q) = false, want true", raw)
		}
	}
	for _, raw := range off {
		if enabledFromEnv(raw) {
			t.Errorf("enabledFromEnv(%q) = true, want false", raw)
		}
	}
}

func TestBuildScoresAggregatesByTriple(t *testing.T) {
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // baseline (high)
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			DetailURL:         "https://diag.relaypulse.top/channel/Anthropic?window=30d&provider=Anthropic&service=claude&model=claude-haiku-4-5",
			FinalQualityScore: mk(100),
			ScoreTrend:        ScoreTrend{Latest: mk(100), LatestAt: freshAt(), Avg7D: mk(100), Avg30D: mk(96), N7D: 3, N30D: 9},
		},
		{ // baseline same channel, different model — should merge
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			FinalQualityScore: mk(98),
			ScoreTrend:        ScoreTrend{Latest: mk(98), LatestAt: freshAt(), Avg7D: mk(98), Avg30D: mk(94), N7D: 3, N30D: 9},
		},
		{ // user-submitted — dropped
			ChannelName: "U-DawAPI-86a39a", RelaypulseChannelKey: "dawapi-86a39a",
			ProviderName: "DawAPI", ServiceCLICommand: "claude",
			SubmissionSource: "user",
			Model:            "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(95)},
		},
		{ // missing trend representative — dropped (no recent_scores, no Latest)
			ChannelName: "Foo", RelaypulseChannelKey: "foo",
			ProviderName: "Bar", ServiceCLICommand: "claude",
		},
	}

	out := c.buildScores(rows)
	if len(out) != 1 {
		t.Fatalf("expected 1 aggregated entry (user + missing-score filtered), got %d (%v)", len(out), keysOf(out))
	}

	key := "anthropic|cc|anthropic"
	entry, ok := out[key]
	if !ok {
		t.Fatalf("expected key %q, got %v", key, keysOf(out))
	}
	// Both models are fresh and active → MaxScore averages their avg_30d
	// ranking contributions (96+94)/2, NOT their latest samples (100+98)/2.
	if entry.MaxScore == nil || *entry.MaxScore != 95 {
		t.Errorf("MaxScore = %v, want 95 (avg of two fresh models' avg_30d)", entry.MaxScore)
	}
	if len(entry.Models) != 2 {
		t.Errorf("Models len = %d, want 2", len(entry.Models))
	}
	// ChannelURL 取该通道首条可解析 detail_url（去掉 ?model=），保留 rpdiag 给的
	// 原始 channel name 与 provider/service 限定；这里首行 haiku 带 detail_url。
	wantChannelURL := "https://diag.relaypulse.top/channel/Anthropic?provider=Anthropic&service=claude&window=30d"
	if entry.ChannelURL != wantChannelURL {
		t.Errorf("ChannelURL = %q, want %q", entry.ChannelURL, wantChannelURL)
	}
}

func TestBuildScoresUsesRawChannelNameAsJoinKey(t *testing.T) {
	// The join key channel segment is the raw channel_name (trim+lower), not the
	// prefix-stripped relaypulse_channel_key. "O-Max" → "o-max", prefix kept.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{
			ChannelName:  "O-Max",
			ProviderName: "SAIAi", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(96)},
		},
	}
	out := c.buildScores(rows)
	if _, ok := out["saiai|cc|o-max"]; !ok {
		t.Errorf("expected raw channel_name join key saiai|cc|o-max, got %v", keysOf(out))
	}
}

func TestBuildScoresKeepsRawPrefixedCodexChannelsSeparate(t *testing.T) {
	// Regression: two codex channels under one provider — `o-cx` (paid) and
	// `u-cx` (free) — both ship relaypulse_channel_key "cx" (the source prefix
	// stripped down to the bare service code). Keying on that collapsed them into
	// a single `right.codes|cx|cx` cell, merging both tiers' models. Joining on the
	// raw channel_name keeps them as two distinct entries so each tier shows its
	// own quality.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	mkRow := func(channel, model string, score float64) rankingRow {
		return rankingRow{
			ChannelName: channel, RelaypulseChannelKey: "cx",
			ProviderName: "right.codes", ServiceCLICommand: "codex",
			Model: model, ModelKey: model,
			ScoreTrend: ScoreTrend{Latest: mk(score), LatestAt: freshAt()},
		}
	}
	rows := []rankingRow{
		mkRow("o-cx", "gpt-5.4", 98),
		mkRow("o-cx", "gpt-5.5", 92),
		mkRow("u-cx", "gpt-5.4", 80),
		mkRow("u-cx", "gpt-5.5", 100),
	}

	out := c.buildScores(rows)
	if _, ok := out["right.codes|cx|cx"]; ok {
		t.Fatalf("o-cx and u-cx collapsed into right.codes|cx|cx; got %v", keysOf(out))
	}
	paid, ok := out["right.codes|cx|o-cx"]
	if !ok {
		t.Fatalf("missing paid tier right.codes|cx|o-cx, got %v", keysOf(out))
	}
	free, ok := out["right.codes|cx|u-cx"]
	if !ok {
		t.Fatalf("missing free tier right.codes|cx|u-cx, got %v", keysOf(out))
	}
	if len(paid.Models) != 2 || len(free.Models) != 2 {
		t.Fatalf("each tier should carry its own 2 models, got paid=%d free=%d", len(paid.Models), len(free.Models))
	}
	if paid.MaxScore == nil || *paid.MaxScore != 95 {
		t.Errorf("paid MaxScore = %v, want 95 ((98+92)/2)", paid.MaxScore)
	}
	if free.MaxScore == nil || *free.MaxScore != 90 {
		t.Errorf("free MaxScore = %v, want 90 ((80+100)/2)", free.MaxScore)
	}
}

func TestScoresUpstreamRoundTrip(t *testing.T) {
	mk := func(v float64) *float64 { return &v }
	// 三个值全部错开：FinalQualityScore=95.2（含 latency 乘子，不用）、
	// trend.Latest=98（展示样本）、Avg30D=97.5（排名键）。MaxScore 必须跟
	// avg_30d，Models[0].Score 必须跟最新样本——三者可区分才防假绿。
	payload := exportPayload{
		SchemaVersion: "ranking-export.v5.1",
		Items: []rankingRow{{
			ChannelName: "cc", RelaypulseChannelKey: "cc",
			ProviderName: "InfAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			DetailURL:         "https://diag.relaypulse.top/channel/cc?window=30d&provider=InfAI&service=claude&model=claude-haiku-4-5",
			FinalQualityScore: mk(95.2),
			ScoreTrend:        ScoreTrend{Latest: mk(98), LatestAt: freshAt(), Avg7D: mk(98), Avg30D: mk(97.5)},
		}},
	}
	srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	client.nowFn = fixedClock
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores returned error: %v", err)
	}
	entry, ok := scores["infai|cc|cc"]
	if !ok {
		t.Fatalf("missing infai|cc|cc entry, got %v", keysOf(scores))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 97.5 {
		t.Errorf("MaxScore = %v, want 97.5 (avg_30d, NOT latest 98, NOT final_quality_score 95.2)", entry.MaxScore)
	}
	if entry.Models[0].Score == nil || *entry.Models[0].Score != 98 {
		t.Errorf("Models[0].Score = %v, want 98 (per-model display score stays the latest fingerprint sample)", entry.Models[0].Score)
	}
	wantChannelURL := "https://diag.relaypulse.top/channel/cc?provider=InfAI&service=claude&window=30d"
	if entry.ChannelURL != wantChannelURL {
		t.Errorf("ChannelURL = %q, want %q", entry.ChannelURL, wantChannelURL)
	}
}

func TestBuildScoresChannelURLEmptyWhenDetailURLMissing(t *testing.T) {
	// 若 rpdiag 没给 detail_url（理论上不该发生，但 schema 可选），ChannelURL
	// 必须留空，前端 nil-check 后不展示链接 — 避免回退到任何本地拼接的
	// "bare channel key" 死路。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SAIAi", ServiceCLICommand: "claude",
		Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
		ScoreTrend: ScoreTrend{Latest: mk(90)},
		// DetailURL 缺省为空字符串
	}}

	entry, ok := c.buildScores(rows)["saiai|cc|o-max"]
	if !ok {
		t.Fatalf("expected entry saiai|cc|o-max, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.ChannelURL != "" {
		t.Errorf("ChannelURL = %q, want empty", entry.ChannelURL)
	}
}

func TestBuildScoresChannelURLFromFirstParsableRow(t *testing.T) {
	// The first model row carries no detail_url; ChannelURL must come from the
	// first row that does yield a parsable one (here the second). Channel-level
	// Trend is intentionally taken from the first row (the front end never reads
	// it), so it is not asserted here.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	rows := []rankingRow{
		{ // no detail_url
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(90), LatestAt: freshAt()},
		},
		{ // has detail_url
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model:      "claude-sonnet-4-6",
			ModelKey:   "claude-sonnet-4-6",
			DetailURL:  "https://diag.relaypulse.top/channel/O-Max?provider=SaiAI&service=claude&model=claude-sonnet-4-6",
			ScoreTrend: ScoreTrend{Latest: mk(92), LatestAt: freshAt()},
		},
	}
	entry := c.buildScores(rows)["saiai|cc|o-max"]
	if entry.ChannelURL == "" {
		t.Fatal("ChannelURL empty; expected it to fall through to the second row's parsable URL")
	}
	if strings.Contains(entry.ChannelURL, "model=") {
		t.Errorf("ChannelURL = %q, must drop the ?model= param", entry.ChannelURL)
	}
	if !strings.Contains(entry.ChannelURL, "/channel/O-Max") {
		t.Errorf("ChannelURL = %q, want it derived from the second row's detail_url", entry.ChannelURL)
	}
}

func TestBuildScoresModelKeyFallsBackToModel(t *testing.T) {
	// An empty model_key falls back to `model`, normalized (trim + lower). The row
	// must still activate its model and contribute to the average — a regression
	// that skipped empty-model_key rows would silently drop the channel's score.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SaiAI", ServiceCLICommand: "claude",
		Model: "  Claude-Haiku-4-5  ", ModelKey: "",
		ScoreTrend: ScoreTrend{Latest: mk(91), LatestAt: freshAt()},
	}}
	entry, ok := c.buildScores(rows)["saiai|cc|o-max"]
	if !ok {
		t.Fatalf("expected entry, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 91 {
		t.Fatalf("MaxScore = %v, want 91 (model_key fell back to normalized model and activated)", entry.MaxScore)
	}
}

func TestLatestFingerprintSample(t *testing.T) {
	mk := func(v float64) *float64 { return &v }

	tests := []struct {
		name string
		in   ScoreTrend
		want *float64
	}{
		// recent_scores 优先：返回数组最末位（时间最新的 single sample）。
		{"recent_scores_wins_over_latest", ScoreTrend{RecentScores: []float64{82, 72, 76}, Latest: mk(99)}, mk(76)},
		{"recent_scores_single", ScoreTrend{RecentScores: []float64{88}}, mk(88)},
		// v5.1 wire 没 recent_scores 时 fallback latest。
		{"latest_fallback_when_recent_empty", ScoreTrend{Latest: mk(64)}, mk(64)},
		{"latest_fallback_when_recent_nil", ScoreTrend{RecentScores: nil, Latest: mk(70)}, mk(70)},
		// 两个都缺 → nil（调用方据此跳过 row）。
		{"both_missing_returns_nil", ScoreTrend{}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := latestFingerprintSample(tt.in)
			switch {
			case got == nil && tt.want == nil:
				return
			case got == nil || tt.want == nil:
				t.Fatalf("got %v, want %v", got, tt.want)
			case *got != *tt.want:
				t.Fatalf("got %v, want %v", *got, *tt.want)
			}
		})
	}
}

func TestBuildScoresDecouplesDisplaySampleFromRankingMean(t *testing.T) {
	// 展示与排名解耦：ModelScore.Score（sparkline 最右点）取 recent_scores
	// 末位（76）而不是 trend.latest（72）或 final_quality_score（85.9）；
	// 通道 MaxScore（排名键）取 30d 均值 avg_30d（76.7）而不是最新单 sample。
	// 覆盖 FastCode opus 在 prod 实际看到的形态。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "cc", RelaypulseChannelKey: "cc",
		ProviderName: "FastCode", ServiceCLICommand: "claude",
		Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
		FinalQualityScore: mk(85.9),
		ScoreTrend: ScoreTrend{
			RecentScores: []float64{82, 72, 76},
			Latest:       mk(72),
			LatestAt:     freshAt(),
			Avg7D:        mk(76.5),
			Avg30D:       mk(76.7),
		},
	}}

	entry, ok := c.buildScores(rows)["fastcode|cc|cc"]
	if !ok {
		t.Fatalf("expected fastcode|cc|cc, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 76.7 {
		t.Errorf("MaxScore = %v, want 76.7 (avg_30d ranking key)", entry.MaxScore)
	}
	if entry.Models[0].Score == nil || *entry.Models[0].Score != 76 {
		t.Errorf("Models[0].Score = %v, want 76 (recent_scores[-1] display sample)", entry.Models[0].Score)
	}
}

func TestBuildScoresFreshRowWithoutAvg30DRanksOnSample(t *testing.T) {
	// 回退路径：v5.7+ wire 上 fresh 行必带 avg_30d（窗口对齐），但旧 wire /
	// 异常形态可能缺失——此时排名键回退到最新单 sample，而不是把行丢掉或计 0。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "cc", RelaypulseChannelKey: "cc",
		ProviderName: "OldWire", ServiceCLICommand: "claude",
		Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
		ScoreTrend: ScoreTrend{Latest: mk(91), LatestAt: freshAt()},
	}}

	entry, ok := c.buildScores(rows)["oldwire|cc|cc"]
	if !ok {
		t.Fatalf("expected oldwire|cc|cc, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 91 {
		t.Errorf("MaxScore = %v, want 91 (fallback to latest sample when avg_30d missing)", entry.MaxScore)
	}
}

func TestBuildScoresStaleRowIgnoresAvg30D(t *testing.T) {
	// stale 行的 avg_30d 是冻结均值（失败探测不产生样本、均值不衰减）——
	// 即便 wire 带着高分 avg_30d，排名贡献也必须是 0，防止"测过几次高分后
	// 再也测不到"的通道靠冻结均值重新浮起（v2.47.2 修过的老坑在新键上复发）。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "cc", RelaypulseChannelKey: "cc",
		ProviderName: "Ghost", ServiceCLICommand: "claude",
		Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
		ScoreTrend: ScoreTrend{
			RecentScores: []float64{94, 95},
			Latest:       mk(95),
			LatestAt:     staleAt(),
			Avg30D:       mk(94.5),
		},
	}, { // fresh sibling elsewhere keeps the model globally active
		ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
		ProviderName: "Anthropic", ServiceCLICommand: "claude",
		Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
		ScoreTrend: ScoreTrend{Latest: mk(96), LatestAt: freshAt(), Avg30D: mk(96)},
	}}

	entry, ok := c.buildScores(rows)["ghost|cc|cc"]
	if !ok {
		t.Fatalf("expected ghost|cc|cc, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 0 {
		t.Errorf("MaxScore = %v, want 0 (stale row must not rank on its frozen avg_30d)", entry.MaxScore)
	}
}

func TestBuildScoresKeepsHardFailRowAsZero(t *testing.T) {
	// rpdiag 标记 hard_fail_active 的行：即便没有任何 fingerprint sample，
	// 也不再被跳过，而是以代表分 0 入列（红点贴底），并把故障文案带给 tooltip。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			HardFailActive:      true,
			AvailabilityWarning: "最近连续评测失败，当前不可用",
		},
		{
			// Sibling fresh haiku on another channel: makes haiku-4-5 a globally
			// active model, so SaiAI's hard-fail instance scores 0 (not excluded
			// as a retired-everywhere model). Mirrors prod, where haiku is fresh
			// on most channels and a hard-failing one genuinely ranks 0.
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(99), LatestAt: freshAt()},
		},
	}

	entry, ok := c.buildScores(rows)["saiai|cc|o-max"]
	if !ok {
		t.Fatalf("expected hard-fail row to be kept, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 0 {
		t.Fatalf("MaxScore = %v, want 0 (active model hard-failing on this channel)", entry.MaxScore)
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Models len = %d, want 1", len(entry.Models))
	}
	m := entry.Models[0]
	if !m.Failed {
		t.Errorf("Model.Failed = false, want true")
	}
	if m.Score == nil || *m.Score != 0 {
		t.Errorf("Model.Score = %v, want 0", m.Score)
	}
	if m.Trend.Latest == nil || *m.Trend.Latest != 0 {
		t.Errorf("Trend.Latest = %v, want 0", m.Trend.Latest)
	}
	if m.Trend.LatestAt != nil {
		t.Errorf("Trend.LatestAt = %v, want nil (synthetic 0 has no sample time)", *m.Trend.LatestAt)
	}
	if !reflect.DeepEqual(m.Trend.RecentScores, []float64{0}) {
		t.Errorf("RecentScores = %v, want [0]", m.Trend.RecentScores)
	}
	if m.AvailabilityWarning != "最近连续评测失败，当前不可用" {
		t.Errorf("AvailabilityWarning = %q, not propagated", m.AvailabilityWarning)
	}
}

func TestBuildScoresHardFailAppendsZeroWithoutMutatingInput(t *testing.T) {
	// 有历史成功分时，hard-fail 行应保留窗口均值、在 recent 末尾补 0（取末 2 真值 + 0），
	// 让 sparkline 读作"从高跌到 0"；且绝不能原地改 decode 出来的共享 backing array。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	latestAt := "2026-06-11T00:00:00Z"

	rows := []rankingRow{{
		ChannelName: "O-Max", RelaypulseChannelKey: "max",
		ProviderName: "SaiAI", ServiceCLICommand: "claude",
		Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
		ScoreTrend: ScoreTrend{
			Latest:       mk(93),
			LatestAt:     &latestAt,
			Avg7D:        mk(90),
			Avg30D:       mk(89),
			RecentScores: []float64{88, 91, 93},
		},
		HardFailActive: true,
	}}

	m := c.buildScores(rows)["saiai|cc|o-max"].Models[0]
	if want := []float64{91, 93, 0}; !reflect.DeepEqual(m.Trend.RecentScores, want) {
		t.Fatalf("RecentScores = %v, want %v", m.Trend.RecentScores, want)
	}
	if m.Trend.Avg30D == nil || *m.Trend.Avg30D != 89 {
		t.Errorf("Avg30D = %v, want 89 (historical average kept)", m.Trend.Avg30D)
	}
	if !reflect.DeepEqual(rows[0].ScoreTrend.RecentScores, []float64{88, 91, 93}) {
		t.Fatalf("input RecentScores mutated: %v", rows[0].ScoreTrend.RecentScores)
	}
	// Writing through the normalized slice must not reach the decoded input.
	m.Trend.RecentScores[0] = 1
	if rows[0].ScoreTrend.RecentScores[1] != 91 {
		t.Fatalf("normalized trend reused input backing array")
	}
}

func TestBuildScoresPartialHardFailDragsAverageDown(t *testing.T) {
	// 同通道一个活跃 model 故障(0)、一个健康(92)：均分把故障计 0 摊进分母，
	// MaxScore=(0+92)/2=46，反映"半边不可用"的真实可用面——不再像旧 max() 那样
	// 让健康 model 独自把整通道顶在 92。两个 model 都必须全站活跃才计入分母，
	// 故各补一条 fresh sibling。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // this channel: haiku hard-fail → 0
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			HardFailActive: true,
		},
		{ // this channel: sonnet healthy 92
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			ScoreTrend: ScoreTrend{Latest: mk(92), LatestAt: freshAt(), RecentScores: []float64{92}},
		},
		{ // sibling: haiku fresh elsewhere → haiku is globally active
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(99), LatestAt: freshAt()},
		},
	}

	entry := c.buildScores(rows)["saiai|cc|o-max"]
	if entry.MaxScore == nil || *entry.MaxScore != 46 {
		t.Fatalf("MaxScore = %v, want 46 ((0 hard-fail + 92 healthy)/2)", entry.MaxScore)
	}
	var failed int
	for _, m := range entry.Models {
		if m.Failed {
			failed++
			if m.Score == nil || *m.Score != 0 {
				t.Errorf("failed model score = %v, want 0", m.Score)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("failed models = %d, want 1", failed)
	}
}

func TestIsStaleScoreTrend(t *testing.T) {
	at := func(s string) ScoreTrend { return ScoreTrend{LatestAt: &s} }
	fresh := testNow.Add(-time.Hour).Format(time.RFC3339Nano) // microsecond precision
	old := testNow.Add(-(scoreStaleWindow + time.Hour)).Format(time.RFC3339Nano)
	bareFresh := testNow.Add(-time.Hour).Format(time.RFC3339) // no fractional seconds
	cases := []struct {
		name string
		in   ScoreTrend
		want bool
	}{
		{"fresh_fractional", at(fresh), false},
		{"fresh_bare_rfc3339", at(bareFresh), false}, // RFC3339Nano parses non-fractional too
		{"stale", at(old), true},
		{"missing_fail_closed", ScoreTrend{}, true},
		{"empty_string_fail_closed", at("  "), true},
		{"unparseable_fail_closed", at("not-a-timestamp"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStaleScoreTrend(tc.in, testNow); got != tc.want {
				t.Errorf("isStaleScoreTrend(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildScoresStaleRowRanksZeroButKeepsHistory(t *testing.T) {
	// A non-hard-fail row whose latest sample predates the 7d window (e.g. a
	// retired model whose per-channel score froze) contributes 0 to the channel
	// MaxScore ranking key — so it can't float to the top on a stale number.
	// But its per-model Score and Trend are displayed EXACTLY as exported: an
	// honest historical line, not flagged Failed, no synthetic point injected.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	staleStamp := staleAt()

	rows := []rankingRow{
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
			ScoreTrend: ScoreTrend{
				Latest:         mk(88),
				LatestAt:       staleStamp,
				Avg7D:          mk(90),
				Avg30D:         mk(91),
				RecentScores:   []float64{85, 88},
				RecentAttempts: []*float64{}, // v5.5: no in-7d attempt
			},
		},
		{
			// Sibling fresh opus-4-7 elsewhere → the model is globally active, so
			// TopRouterCN's stale instance ranks 0 (rather than being excluded as
			// retired-everywhere — that exclusion is covered separately).
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
			ScoreTrend: ScoreTrend{Latest: mk(94), LatestAt: freshAt()},
		},
	}

	entry, ok := c.buildScores(rows)["toproutercn|cc|o-max"]
	if !ok {
		t.Fatalf("expected stale row kept, got %v", keysOf(c.buildScores(rows)))
	}
	// Ranking: zeroed.
	if entry.MaxScore == nil || *entry.MaxScore != 0 {
		t.Fatalf("MaxScore = %v, want 0 (stale → no current ranking signal)", entry.MaxScore)
	}
	// Display: untouched history.
	m := entry.Models[0]
	if m.Failed {
		t.Errorf("Model.Failed = true, want false (stale is not hard-fail)")
	}
	if m.Score == nil || *m.Score != 88 {
		t.Errorf("Model.Score = %v, want 88 (real history shown, not ranking 0)", m.Score)
	}
	if m.Trend.LatestAt == nil || *m.Trend.LatestAt != *staleStamp {
		t.Errorf("Trend.LatestAt = %v, want preserved %q", m.Trend.LatestAt, *staleStamp)
	}
	if m.Trend.Avg30D == nil || *m.Trend.Avg30D != 91 {
		t.Errorf("Avg30D = %v, want 91 (history kept)", m.Trend.Avg30D)
	}
	if len(m.Trend.RecentAttempts) != 0 {
		t.Errorf("RecentAttempts = %v, want [] untouched (no synthetic point)", m.Trend.RecentAttempts)
	}
}

func TestBuildScoresRetiredSiblingExcludedFromAverage(t *testing.T) {
	// Channel with one fresh model (90) and one model that is stale here and has
	// NO fresh row anywhere (retired platform-wide, frozen 95). The retired model
	// is dropped from both numerator and denominator, so the channel ranks on its
	// one active model alone: MaxScore=90, not (95+90)/2 and not (0+90)/2. This is
	// the opus-4-7 case — a globally retired model must neither help nor punish.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // retired-everywhere, frozen high → excluded
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
			ScoreTrend: ScoreTrend{Latest: mk(95), LatestAt: staleAt()},
		},
		{ // fresh current model → the only thing the channel ranks on
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-8", ModelKey: "claude-opus-4-8",
			ScoreTrend: ScoreTrend{Latest: mk(90), LatestAt: freshAt()},
		},
	}

	entry := c.buildScores(rows)["toproutercn|cc|o-max"]
	if entry.MaxScore == nil || *entry.MaxScore != 90 {
		t.Fatalf("MaxScore = %v, want 90 (retired sibling excluded, ranks on active model alone)", entry.MaxScore)
	}
}

func TestBuildScoresTopRouterScenarioAveragesActiveModels(t *testing.T) {
	// The production case that motivated this change: a channel offering 4 models
	// where only haiku still works. haiku fresh (97); opus-4-7 stale and retired
	// everywhere (excluded); sonnet & opus-4-8 hard-fail but globally active
	// elsewhere (contribute 0). MaxScore = (97+0+0)/3 ≈ 32.33 — the channel sinks
	// to its true availability instead of floating at 97 on the lone survivor.
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{ // haiku fresh, the only working model
			ChannelName: "O-TopRouterCN", RelaypulseChannelKey: "toproutercn",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(97), LatestAt: freshAt()},
		},
		{ // opus-4-7 stale, retired everywhere → excluded
			ChannelName: "O-TopRouterCN", RelaypulseChannelKey: "toproutercn",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
			ScoreTrend: ScoreTrend{Latest: mk(88), LatestAt: staleAt()},
		},
		{ // sonnet hard-fail here, active elsewhere → 0
			ChannelName: "O-TopRouterCN", RelaypulseChannelKey: "toproutercn",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			HardFailActive: true,
		},
		{ // opus-4-8 hard-fail here, active elsewhere → 0
			ChannelName: "O-TopRouterCN", RelaypulseChannelKey: "toproutercn",
			ProviderName: "TopRouterCN", ServiceCLICommand: "claude",
			Model: "claude-opus-4-8", ModelKey: "claude-opus-4-8",
			HardFailActive: true,
		},
		{ // sibling channel keeps sonnet & opus-4-8 globally active
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-6", ModelKey: "claude-sonnet-4-6",
			ScoreTrend: ScoreTrend{Latest: mk(96), LatestAt: freshAt()},
		},
		{
			ChannelName: "Anthropic", RelaypulseChannelKey: "anthropic",
			ProviderName: "Anthropic", ServiceCLICommand: "claude",
			Model: "claude-opus-4-8", ModelKey: "claude-opus-4-8",
			ScoreTrend: ScoreTrend{Latest: mk(95), LatestAt: freshAt()},
		},
	}

	entry := c.buildScores(rows)["toproutercn|cc|o-toproutercn"]
	if entry.MaxScore == nil {
		t.Fatalf("MaxScore = nil, want ~32.33")
	}
	if got := *entry.MaxScore; got < 32.3 || got > 32.4 {
		t.Fatalf("MaxScore = %v, want (97+0+0)/3 ≈ 32.33", got)
	}
}

func TestBuildScoresAllRetiredChannelHasNilScore(t *testing.T) {
	// A channel whose only models are retired everywhere (no fresh row anywhere)
	// has no current quality signal: MaxScore is nil so the quality sort sinks it
	// below every scored channel. The display row is still kept (honest history).
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{{
		ChannelName: "O-Ghost", RelaypulseChannelKey: "ghost",
		ProviderName: "Ghost", ServiceCLICommand: "claude",
		Model: "claude-opus-4-7", ModelKey: "claude-opus-4-7",
		ScoreTrend: ScoreTrend{Latest: mk(88), LatestAt: staleAt()},
	}}

	entry, ok := c.buildScores(rows)["ghost|cc|o-ghost"]
	if !ok {
		t.Fatalf("expected entry kept for display, got %v", keysOf(c.buildScores(rows)))
	}
	if entry.MaxScore != nil {
		t.Fatalf("MaxScore = %v, want nil (no active model → no current signal)", *entry.MaxScore)
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Models len = %d, want 1 (display row kept)", len(entry.Models))
	}
}

func TestBuildScoresDedupesRepeatedModelInAverage(t *testing.T) {
	// Guard against a duplicated upstream (channel, model) row inflating the
	// divisor: two rows for the same model on the same channel count once. The
	// dedup keeps the LARGEST ranking contribution (deterministic, not
	// export-order). Latest 与 Avg30D 刻意错开：max-wins 必须比较 avg_30d
	// 排名值（55 vs 82 → 82），而不是最新样本（60 vs 80）。
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	rows := []rankingRow{
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(60), LatestAt: freshAt(), Avg30D: mk(55)},
		},
		{
			ChannelName: "O-Max", RelaypulseChannelKey: "max",
			ProviderName: "SaiAI", ServiceCLICommand: "claude",
			Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
			ScoreTrend: ScoreTrend{Latest: mk(80), LatestAt: freshAt(), Avg30D: mk(82)},
		},
	}

	entry := c.buildScores(rows)["saiai|cc|o-max"]
	if entry.MaxScore == nil || *entry.MaxScore != 82 {
		t.Fatalf("MaxScore = %v, want 82 (duplicate model counted once, max avg_30d kept)", entry.MaxScore)
	}
}

// When cid consolidation merges a renamed channel's halves, the same effective
// model can appear twice in one cid bucket — one stale (rankLatest 0), one fresh.
// The max-dedup must keep the fresh score regardless of export row order, so the
// channel does not regress to 0 (the bug the legacy per-name buckets never hit).
func TestBuildScoresCIDMergeKeepsFreshOverStaleModel(t *testing.T) {
	rows := []rankingRow{
		// Stale half (old name) first — under first-seen this would win at 0.
		cidRow("P", "claude", "Old-Name", "Opus", "ch_a", 95, staleAt()),
		// Fresh half (new name) second — its real rankLatest is 70.
		cidRow("P", "claude", "New-Name", "Opus", "ch_a", 70, freshAt()),
	}
	scores := newTestClient().buildScoresAt(rows, testNow)
	entry, ok := scores["cid:ch_a"]
	if !ok {
		t.Fatalf("missing cid:ch_a bucket, got %v", keysOf(scores))
	}
	if entry.MaxScore == nil || *entry.MaxScore != 70 {
		t.Fatalf("MaxScore = %v, want 70 (fresh kept over stale 0)", entry.MaxScore)
	}
}

func TestBuildScoresSkipsHardFailUserSubmission(t *testing.T) {
	// 公开提交(submission_source=user)通道即便 hard-fail 也不进 relaypulse 列表。
	c := newTestClient()
	rows := []rankingRow{{
		ChannelName: "U-foo-abc123", RelaypulseChannelKey: "foo-abc123",
		ProviderName: "Foo", ServiceCLICommand: "claude",
		Model: "claude-haiku-4-5", ModelKey: "claude-haiku-4-5",
		SubmissionSource: "user",
		HardFailActive:   true,
	}}
	if got := c.buildScores(rows); len(got) != 0 {
		t.Fatalf("expected user hard-fail row skipped, got %v", keysOf(got))
	}
}

func TestCloneScoresDeepCopiesRecentScores(t *testing.T) {
	// cloneScores 返回独立快照：改克隆里的 RecentScores 不应回写到源 cache。
	mk := func(v float64) *float64 { return &v }
	src := map[string]Score{
		"k": {
			MaxScore: mk(6),
			Trend:    ScoreTrend{RecentScores: []float64{1, 2, 3}},
			Models:   []ModelScore{{Trend: ScoreTrend{RecentScores: []float64{4, 5, 6}}}},
		},
	}

	cloned := cloneScores(src)["k"]
	cloned.Trend.RecentScores[0] = 99
	cloned.Models[0].Trend.RecentScores[0] = 88

	if src["k"].Trend.RecentScores[0] != 1 {
		t.Fatalf("aggregate trend recent_scores shared with clone")
	}
	if src["k"].Models[0].Trend.RecentScores[0] != 4 {
		t.Fatalf("model trend recent_scores shared with clone")
	}
}

func TestScoresRejectsUnsupportedSchema(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"schema_version":"ranking-export.v4","items":[]}`)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	if _, err := client.Scores(context.Background()); err == nil {
		t.Errorf("expected error for unsupported schema_version, got nil")
	}
}

func TestScoresAcceptsV53UnavailableRow(t *testing.T) {
	// v5.3 export feed: an export-only "unavailable" row for a model that never
	// scored and is still hard-failing (e.g. a 403-only model). The schema gate
	// accepts v5.x by prefix and the unknown `quality_state` field is ignored;
	// the row rides the existing hard_fail_active path and renders as a kept
	// gray-zero model carrying rpdiag's "couldn't measure" warning.
	const payload = `{"schema_version":"ranking-export.v5.3","items":[` +
		`{"channel_name":"M-Max","relaypulse_channel_key":"max","provider_name":"AIMZ",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"quality_state":"unavailable","hard_fail_active":true,` +
		`"availability_warning":"质量探测未取得可评分响应","final_quality_score":0,"score_trend":{}}` +
		`]}`
	srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, payload)
	})
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("v5.3 payload rejected: %v", err)
	}
	entry, ok := scores["aimz|cc|m-max"]
	if !ok {
		t.Fatalf("expected aimz|cc|m-max, got %v", keysOf(scores))
	}
	if len(entry.Models) != 1 {
		t.Fatalf("Models len = %d, want 1", len(entry.Models))
	}
	m := entry.Models[0]
	if !m.Failed {
		t.Errorf("Model.Failed = false, want true (unavailable row is hard-fail active)")
	}
	if m.Score == nil || *m.Score != 0 {
		t.Errorf("Model.Score = %v, want 0", m.Score)
	}
	if m.AvailabilityWarning != "质量探测未取得可评分响应" {
		t.Errorf("AvailabilityWarning = %q, want the unavailable-export wording", m.AvailabilityWarning)
	}
}

// TestRecentAttemptsEmptyVsAbsentRoundTrip locks the v5.5 contract: an upstream
// empty `recent_attempts:[]` ("no in-7d attempt") must survive decode → clone →
// re-encode as `[]`, while an absent/null field must survive as `null`. The
// front end keys off exactly this distinction — `[]` draws no recent dots,
// `null` falls back to recent_scores — so an `omitempty` regression that
// collapsed `[]` to absent would silently resurrect stale dots.
func TestRecentAttemptsEmptyVsAbsentRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		trendJSON string
		want      string
	}{
		// latest:83 keeps the row past buildScores' representative-score gate; the
		// recent_attempts field under test is independent of it.
		{"empty_stays_empty", `{"latest":83.0,"recent_attempts":[]}`, `"recent_attempts":[]`},
		{"absent_stays_null", `{"latest":83.0}`, `"recent_attempts":null`},
		{"values_preserved", `{"latest":83.0,"recent_attempts":[null,88.0]}`, `"recent_attempts":[null,88]`},
	}
	// Inject a fresh latest_at so the staleness gate leaves recent_attempts
	// untouched — this test isolates the empty-vs-absent round-trip, not staleness.
	freshStr := testNow.Add(-time.Hour).Format(time.RFC3339Nano)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			trendJSON := strings.Replace(tc.trendJSON, "{", `{"latest_at":"`+freshStr+`",`, 1)
			payload := `{"schema_version":"ranking-export.v5.5","items":[` +
				`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"TopRouterCN",` +
				`"service_cli_command":"claude","model":"claude-opus-4-7","model_key":"claude-opus-4-7",` +
				`"detail_url":"https://diag.relaypulse.top/channel/O-Max",` +
				`"score_trend":` + trendJSON + `}]}`
			srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintln(w, payload)
			})
			defer srv.Close()

			client := NewClient(nil, srv.URL, 0, true)
			client.nowFn = fixedClock
			scores, err := client.Scores(context.Background())
			if err != nil {
				t.Fatalf("Scores returned error: %v", err)
			}
			entry, ok := scores["toproutercn|cc|o-max"]
			if !ok {
				t.Fatalf("missing toproutercn|cc|o-max entry, got %v", keysOf(scores))
			}
			// Marshal the cloned model trend the way the HTTP handler serves it
			// to the browser, then assert the recent_attempts fragment.
			out, err := json.Marshal(entry.Models[0].Trend)
			if err != nil {
				t.Fatalf("marshal trend: %v", err)
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("trend JSON = %s, want substring %q", out, tc.want)
			}
		})
	}
}

func TestBoardURLsFromExportURL(t *testing.T) {
	// The base URL is fetched unchanged (claude board); the codex board is the
	// same URL with test_case added, preserving the existing query (scoring_version).
	got := boardURLsFromExportURL(DefaultExportURL)
	if len(got) != 2 {
		t.Fatalf("boardURLs len = %d, want 2: %v", len(got), got)
	}
	if got[0] != DefaultExportURL {
		t.Errorf("board[0] = %q, want the base URL unchanged %q", got[0], DefaultExportURL)
	}
	u, err := url.Parse(got[1])
	if err != nil {
		t.Fatalf("codex board URL unparseable: %v", err)
	}
	if tc := u.Query().Get("test_case"); tc != codexBoardTestCase {
		t.Errorf("codex board test_case = %q, want %q", tc, codexBoardTestCase)
	}
	if sv := u.Query().Get("scoring_version"); sv != "current" {
		t.Errorf("codex board scoring_version = %q, want current", sv)
	}
	base, err := url.Parse(got[0])
	if err != nil {
		t.Fatalf("base board URL unparseable: %v", err)
	}
	if sv := base.Query().Get("scoring_version"); sv != "current" {
		t.Errorf("base board scoring_version = %q, want current", sv)
	}
}

// TestScoresMergesClaudeAndCodexBoards locks the multi-board contract: the
// client fetches the base (claude) board AND the codex board (test_case=
// quick-probe-codex-v1) and merges their rows. A claude row and a codex row
// from the same provider must yield two distinct entries keyed by service
// (cc vs cx) — neither board is dropped, and the cx/cc service buckets don't
// cross-activate.
func TestScoresMergesClaudeAndCodexBoards(t *testing.T) {
	fresh := *freshAt()
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":97.0,"latest_at":"` + fresh + `"}}]}`
	codex := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Pro","relaypulse_channel_key":"pro","provider_name":"SAIAi",` +
		`"service_cli_command":"codex","model":"gpt-5.4","model_key":"gpt-5.4",` +
		`"score_trend":{"latest":92.0,"latest_at":"` + fresh + `"}}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("test_case") == codexBoardTestCase {
			_, _ = fmt.Fprintln(w, codex)
			return
		}
		_, _ = fmt.Fprintln(w, claude)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	client.nowFn = fixedClock
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores returned error: %v", err)
	}
	cc, ok := scores["saiai|cc|o-max"]
	if !ok {
		t.Fatalf("missing claude entry saiai|cc|o-max, got %v", keysOf(scores))
	}
	if cc.MaxScore == nil || *cc.MaxScore != 97 {
		t.Errorf("claude MaxScore = %v, want 97", cc.MaxScore)
	}
	cx, ok := scores["saiai|cx|o-pro"]
	if !ok {
		t.Fatalf("missing codex entry saiai|cx|o-pro, got %v", keysOf(scores))
	}
	if cx.MaxScore == nil || *cx.MaxScore != 92 {
		t.Errorf("codex MaxScore = %v, want 92", cx.MaxScore)
	}
}

// TestScoresCodexClaudeSameChannelKeyedByService proves the cc/cx boards can
// host the same (provider, channel) without colliding: the service segment of
// the join key separates them into two independent entries, so codex data can
// never overwrite or cross-activate a claude channel of the same name.
func TestScoresCodexClaudeSameChannelKeyedByService(t *testing.T) {
	fresh := *freshAt()
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":97.0,"latest_at":"` + fresh + `"}}]}`
	// Same provider + same channel key as the claude row; only the service differs.
	codex := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"codex","model":"gpt-5.4","model_key":"gpt-5.4",` +
		`"score_trend":{"latest":40.0,"latest_at":"` + fresh + `"}}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("test_case") == codexBoardTestCase {
			_, _ = fmt.Fprintln(w, codex)
			return
		}
		_, _ = fmt.Fprintln(w, claude)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, 0, true)
	client.nowFn = fixedClock
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores returned error: %v", err)
	}
	cc, ok := scores["saiai|cc|o-max"]
	if !ok || cc.MaxScore == nil || *cc.MaxScore != 97 {
		t.Errorf("claude entry saiai|cc|o-max = %+v (want MaxScore 97), keys=%v", cc, keysOf(scores))
	}
	cx, ok := scores["saiai|cx|o-max"]
	if !ok || cx.MaxScore == nil || *cx.MaxScore != 40 {
		t.Errorf("codex entry saiai|cx|o-max = %+v (want MaxScore 40), keys=%v", cx, keysOf(scores))
	}
}

// TestScoresBoardFailureFallsBackToStale locks the all-or-nothing refresh: if
// any board fetch fails, refresh returns an error so Scores() serves the last
// full good snapshot rather than caching a snapshot missing that board (which
// would blank its column for a TTL). Here the codex board 500s on the second
// refresh; the cached claude+codex snapshot from the first refresh must survive.
func TestScoresBoardFailureFallsBackToStale(t *testing.T) {
	fresh := *freshAt()
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":97.0,"latest_at":"` + fresh + `"}}]}`
	codex := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Pro","relaypulse_channel_key":"pro","provider_name":"SAIAi",` +
		`"service_cli_command":"codex","model":"gpt-5.4","model_key":"gpt-5.4",` +
		`"score_trend":{"latest":92.0,"latest_at":"` + fresh + `"}}]}`

	codexFails := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("test_case") == codexBoardTestCase {
			if codexFails {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintln(w, codex)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, claude)
	}))
	defer srv.Close()

	client := NewClient(nil, srv.URL, time.Nanosecond, true) // tiny TTL → always refresh
	client.nowFn = fixedClock

	if _, err := client.Scores(context.Background()); err != nil {
		t.Fatalf("first Scores returned error: %v", err)
	}
	// Codex board now fails; the next refresh must NOT wipe the cached snapshot.
	codexFails = true
	scores, err := client.Scores(context.Background())
	if err != nil {
		t.Fatalf("Scores after codex failure returned error (expected stale fallback): %v", err)
	}
	if _, ok := scores["saiai|cc|o-max"]; !ok {
		t.Errorf("claude entry vanished on codex-board failure; stale fallback should keep it, got %v", keysOf(scores))
	}
	if _, ok := scores["saiai|cx|o-pro"]; !ok {
		t.Errorf("codex entry vanished on codex-board failure; stale fallback should keep it, got %v", keysOf(scores))
	}
}

// singleBoardServer wraps a board handler so it answers only the base (claude)
// board request; the codex board the client now also fetches gets an empty
// items payload. Production boards return disjoint rows per service, so tests
// that populate just one board use this to keep the codex fetch from
// duplicating the claude rows.
func singleBoardServer(serve http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("test_case") != "" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"items":[]}`)
			return
		}
		serve(w, r)
	}))
}

// helpers ---------------------------------------------------------------

func newTestClient() *Client {
	c := NewClient(nil, DefaultExportURL, DefaultTTL, true)
	c.nowFn = fixedClock
	return c
}

func keysOf(m map[string]Score) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// intPtr is a package-level *int helper for constructing attempts_7d fixtures.
func intPtr(v int) *int { return &v }

// cidRow builds a minimal fresh, scorable rpdiag row for buildScoresAt unit
// tests. latest_at is left to the caller (freshAt()/staleAt()).
func cidRow(provider, service, channel, model, cid string, latest float64, at *string) rankingRow {
	v := latest
	return rankingRow{
		ChannelName:         channel,
		ProviderName:        provider,
		ServiceCLICommand:   service,
		Model:               model,
		ModelKey:            model,
		ScoreTrend:          ScoreTrend{Latest: &v, LatestAt: at},
		RelaypulseChannelID: cid,
	}
}

func TestRankingRowDecodesRelaypulseChannelID(t *testing.T) {
	const body = `{"schema_version":"ranking-export.v5.9","items":[` +
		`{"channel_name":"O-Max","provider_name":"P","service_cli_command":"claude",` +
		`"model":"Opus","model_key":"opus","score_trend":{"latest":90.0},` +
		`"relaypulse_channel_id":"ch_11111111-1111-4111-8111-111111111111"}]}`
	var p exportPayload
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := p.Items[0].RelaypulseChannelID; got != "ch_11111111-1111-4111-8111-111111111111" {
		t.Fatalf("RelaypulseChannelID = %q", got)
	}
}

// A row carrying a cid buckets under "cid:"+id, NOT the legacy triple.
func TestBuildScoresBucketsByChannelID(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{cidRow("P", "claude", "O-Max", "Opus", "ch_a", 90, at)}
	scores := newTestClient().buildScoresAt(rows, testNow)
	if _, ok := scores["cid:ch_a"]; !ok {
		t.Fatalf("missing cid bucket, got %v", keysOf(scores))
	}
	if _, ok := scores["p|cc|o-max"]; ok {
		t.Fatalf("legacy key must be absent when cid present, got %v", keysOf(scores))
	}
}

// No cid → legacy triple key (back-compat with every pre-v5.9 row).
func TestBuildScoresWithoutCIDKeepsLegacyKey(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{cidRow("P", "claude", "O-Max", "Opus", "", 90, at)}
	scores := newTestClient().buildScoresAt(rows, testNow)
	if _, ok := scores["p|cc|o-max"]; !ok {
		t.Fatalf("missing legacy key, got %v", keysOf(scores))
	}
}

// Mixed within one channel: one model tagged cid=X, a lagging (e.g. recently
// retired) model still cid="" → BOTH consolidate into cid:X (no split bucket).
func TestBuildScoresConsolidatesNullCIDStraggler(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{
		cidRow("P", "claude", "O-Max", "Opus", "ch_a", 90, at),
		cidRow("P", "claude", "O-Max", "Sonnet", "", 80, at),
	}
	scores := newTestClient().buildScoresAt(rows, testNow)
	entry, ok := scores["cid:ch_a"]
	if !ok {
		t.Fatalf("missing cid bucket, got %v", keysOf(scores))
	}
	if len(entry.Models) != 2 {
		t.Fatalf("cid bucket models = %d, want 2 (straggler consolidated)", len(entry.Models))
	}
	if _, ok := scores["p|cc|o-max"]; ok {
		t.Fatalf("legacy key must not survive consolidation, got %v", keysOf(scores))
	}
}

// >1 distinct cid in one legacy triple → ambiguous → fail-closed to legacy key,
// no cid bucket (else consumer's cid lookup sees a partial bucket, never falls back).
func TestBuildScoresAmbiguousMultiCIDFallsBackToLegacy(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{
		cidRow("P", "claude", "O-Max", "Opus", "ch_a", 90, at),
		cidRow("P", "claude", "O-Max", "Sonnet", "ch_b", 80, at),
	}
	scores := newTestClient().buildScoresAt(rows, testNow)
	if _, ok := scores["p|cc|o-max"]; !ok {
		t.Fatalf("missing legacy fallback key, got %v", keysOf(scores))
	}
	if _, ok := scores["cid:ch_a"]; ok {
		t.Fatalf("cid:ch_a must not exist under ambiguity, got %v", keysOf(scores))
	}
	if _, ok := scores["cid:ch_b"]; ok {
		t.Fatalf("cid:ch_b must not exist under ambiguity, got %v", keysOf(scores))
	}
}

// Same cid under two services (a per-channel id reused across cc/cx = upstream
// data bug) → guard: both triples fall back to legacy, no cross-service merge.
func TestBuildScoresCrossServiceCIDReuseFallsBackToLegacy(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{
		cidRow("P", "claude", "O-Max", "Opus", "ch_a", 90, at),
		cidRow("P", "codex", "O-Pro", "GPT", "ch_a", 70, at),
	}
	scores := newTestClient().buildScoresAt(rows, testNow)
	if _, ok := scores["cid:ch_a"]; ok {
		t.Fatalf("cross-service cid must not bucket, got %v", keysOf(scores))
	}
	if _, ok := scores["p|cc|o-max"]; !ok {
		t.Fatalf("missing cc legacy fallback, got %v", keysOf(scores))
	}
	if _, ok := scores["p|cx|o-pro"]; !ok {
		t.Fatalf("missing cx legacy fallback, got %v", keysOf(scores))
	}
}

// Display-name drift: same cid exported under an old name and a new name (each
// half captured at a different time) → both rejoin under one cid:X bucket. This
// is the whole point of cid (survives channel_name drift).
func TestBuildScoresMergesDriftedNamesUnderOneCID(t *testing.T) {
	at := freshAt()
	rows := []rankingRow{
		cidRow("P", "claude", "Old-Name", "Opus", "ch_a", 90, at),
		cidRow("P", "claude", "New-Name", "Sonnet", "ch_a", 80, at),
	}
	scores := newTestClient().buildScoresAt(rows, testNow)
	entry, ok := scores["cid:ch_a"]
	if !ok {
		t.Fatalf("drifted names must merge under cid:ch_a, got %v", keysOf(scores))
	}
	if len(entry.Models) != 2 {
		t.Fatalf("merged bucket models = %d, want 2", len(entry.Models))
	}
}

// f is a package-level *float64 helper for constructing recent_attempts /
// score fixtures (mirrors the per-test `mk` closures used elsewhere).
func f(v float64) *float64 { return &v }

// mustTime parses an RFC3339 timestamp for fixtures, failing the test on error.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustTime(%q): %v", s, err)
	}
	return ts
}

func TestBuildScores_UnavailableRowExcludedFromAverage(t *testing.T) {
	now := mustTime(t, "2026-07-04T00:00:00Z")
	fresh := func(v float64) ScoreTrend { // 一个新鲜代表分 + 当前时间戳
		at := "2026-07-03T23:00:00Z"
		return ScoreTrend{Latest: &v, LatestAt: &at, RecentScores: []float64{v}}
	}
	rows := []rankingRow{
		{ProviderName: "acme", ServiceCLICommand: "claude", ChannelName: "O-Max",
			ModelKey: "haiku", ScoreTrend: fresh(95)},
		{ProviderName: "acme", ServiceCLICommand: "claude", ChannelName: "O-Max",
			ModelKey: "opus", QualityState: "unavailable", HardFailActive: false,
			ScoreTrend: ScoreTrend{RecentAttempts: []*float64{f(90), f(88)}}},
		{ProviderName: "other", ServiceCLICommand: "claude", ChannelName: "O-Pro",
			ModelKey: "opus", ScoreTrend: fresh(80)},
	}
	c := NewClient(nil, "", 0, true)
	scores := c.buildScoresAt(rows, now)
	got := scores[ScoreKey("acme", "cc", "O-Max")]
	if got.MaxScore == nil || *got.MaxScore != 95 {
		t.Fatalf("MaxScore = %v, want 95 (opus unavailable must be excluded, not averaged as 0)", got.MaxScore)
	}
	var opus *ModelScore
	for i := range got.Models {
		if got.Models[i].ModelKey == "opus" {
			opus = &got.Models[i]
		}
	}
	if opus == nil {
		t.Fatal("opus unavailable row must be kept for display")
	}
	if !opus.Unavailable {
		t.Error("opus.Unavailable = false, want true")
	}
}

func TestBuildScores_HardFailUnavailableRowUnchanged(t *testing.T) {
	now := mustTime(t, "2026-07-04T00:00:00Z")
	rows := []rankingRow{
		{ProviderName: "acme", ServiceCLICommand: "claude", ChannelName: "O-Max",
			ModelKey: "opus", QualityState: "unavailable", HardFailActive: true,
			AvailabilityWarning: "质量探测未取得可评分响应",
			ScoreTrend:          ScoreTrend{RecentAttempts: []*float64{nil, nil}}},
	}
	c := NewClient(nil, "", 0, true)
	got := c.buildScoresAt(rows, now)[ScoreKey("acme", "cc", "O-Max")]
	if len(got.Models) != 1 || !got.Models[0].Failed {
		t.Fatalf("hard-fail unavailable row must stay Failed grey-0, got %+v", got.Models)
	}
	if got.Models[0].Unavailable {
		t.Error("hard-fail-active row must NOT be tagged Unavailable (Failed handles it)")
	}
}

func TestBuildScores_AllUnavailableChannelHasNilMaxScore(t *testing.T) {
	now := mustTime(t, "2026-07-04T00:00:00Z")
	rows := []rankingRow{
		{ProviderName: "acme", ServiceCLICommand: "claude", ChannelName: "O-Dark",
			ModelKey: "opus", QualityState: "unavailable", HardFailActive: false,
			ScoreTrend: ScoreTrend{RecentAttempts: []*float64{f(90)}}},
	}
	c := NewClient(nil, "", 0, true)
	got := c.buildScoresAt(rows, now)[ScoreKey("acme", "cc", "O-Dark")]
	if got.MaxScore != nil {
		t.Fatalf("MaxScore = %v, want nil (all-unavailable channel sinks)", *got.MaxScore)
	}
	if len(got.Models) != 1 {
		t.Fatalf("model must be kept for display, got %d models", len(got.Models))
	}
}

func TestBuildScoresNoRecentAttemptsFlag(t *testing.T) {
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }

	// 基础 scored 行工厂：不同 channel 隔开，避免同通道聚合互相干扰。
	base := func(channel string, attempts7d *int, hardFail bool) rankingRow {
		return rankingRow{
			ChannelName: channel, ProviderName: "prov", ServiceCLICommand: "claude",
			Model: "claude-sonnet-4-5", ModelKey: "claude-sonnet-4-5",
			HardFailActive: hardFail,
			Attempts7D:     attempts7d,
			ScoreTrend: ScoreTrend{
				Latest: mk(88), LatestAt: staleAt(), // stale：近7天无出分，与 attempts==0 一致
				Avg7D: mk(90), Avg30D: mk(91),
				RecentScores: []float64{85, 88}, RecentAttempts: []*float64{},
			},
		}
	}

	cases := []struct {
		name              string
		row               rankingRow
		wantNoRecent      bool
		wantFailed        bool
		wantScorePreserve bool // Score/Avg30D 是否原样保留
	}{
		{"zero_attempts_stale", base("c-zero", intPtr(0), false), true, false, true},
		{"has_attempts", base("c-has", intPtr(3), false), false, false, true},
		{"absent_attempts_old_wire", base("c-nil", nil, false), false, false, true},
		{"zero_attempts_but_hardfail", base("c-hf", intPtr(0), true), false, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scores := c.buildScoresAt([]rankingRow{tc.row}, testNow)
			key := ScoreKey("prov", "cc", tc.row.ChannelName)
			s, ok := scores[key]
			if !ok || len(s.Models) != 1 {
				t.Fatalf("expected 1 model for %s, got %+v", key, scores)
			}
			m := s.Models[0]
			if m.NoRecentAttempts != tc.wantNoRecent {
				t.Errorf("NoRecentAttempts = %v, want %v", m.NoRecentAttempts, tc.wantNoRecent)
			}
			if m.Failed != tc.wantFailed {
				t.Errorf("Failed = %v, want %v", m.Failed, tc.wantFailed)
			}
			if tc.wantScorePreserve {
				if m.Score == nil || *m.Score != 88 {
					t.Errorf("Score = %v, want 88 preserved", m.Score)
				}
				if m.Trend.Avg30D == nil || *m.Trend.Avg30D != 91 {
					t.Errorf("Avg30D = %v, want 91 preserved", m.Trend.Avg30D)
				}
			}
		})
	}
}

func TestRankingRowAttempts7DJSONDecode(t *testing.T) {
	cases := []struct {
		name string
		json string
		want *int
	}{
		{"absent", `{"channel_name":"c"}`, nil},
		{"null", `{"channel_name":"c","attempts_7d":null}`, nil},
		{"zero", `{"channel_name":"c","attempts_7d":0}`, intPtr(0)},
		{"positive", `{"channel_name":"c","attempts_7d":5}`, intPtr(5)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var row rankingRow
			if err := json.Unmarshal([]byte(tc.json), &row); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			switch {
			case tc.want == nil && row.Attempts7D != nil:
				t.Errorf("Attempts7D = %v, want nil", *row.Attempts7D)
			case tc.want != nil && (row.Attempts7D == nil || *row.Attempts7D != *tc.want):
				t.Errorf("Attempts7D = %v, want %v", row.Attempts7D, *tc.want)
			}
		})
	}
}

func TestCloneScoresPreservesNoRecentAttempts(t *testing.T) {
	src := map[string]Score{
		"k": {Models: []ModelScore{{Model: "m", NoRecentAttempts: true}}},
	}
	got := cloneScores(src)
	if !got["k"].Models[0].NoRecentAttempts {
		t.Error("clone dropped NoRecentAttempts")
	}
}

func TestNoRecentAttemptsDoesNotAffectRanking(t *testing.T) {
	c := newTestClient()
	mk := func(v float64) *float64 { return &v }
	fresh := ScoreTrend{Latest: mk(80), LatestAt: freshAt(), Avg7D: mk(80), Avg30D: mk(80)}
	staleTrend := ScoreTrend{Latest: mk(88), LatestAt: staleAt(), Avg7D: mk(90), Avg30D: mk(91)}
	rows := []rankingRow{
		// 目标通道：stale + attempts_7d==0
		{ChannelName: "target", ProviderName: "p", ServiceCLICommand: "claude",
			Model: "sonnet", ModelKey: "sonnet", Attempts7D: intPtr(0), ScoreTrend: staleTrend},
		// 别处 fresh sibling → sonnet 全站 active，故 target 的 stale 行 rankLatest=0（非剔除）
		{ChannelName: "other", ProviderName: "p", ServiceCLICommand: "claude",
			Model: "sonnet", ModelKey: "sonnet", Attempts7D: intPtr(9), ScoreTrend: fresh},
	}
	scores := c.buildScoresAt(rows, testNow)
	target := scores[ScoreKey("p", "cc", "target")]
	if !target.Models[0].NoRecentAttempts {
		t.Fatal("target should be NoRecentAttempts")
	}
	if target.MaxScore == nil || *target.MaxScore != 0 {
		t.Errorf("MaxScore = %v, want 0 (stale contributes 0, unchanged by display flag)", target.MaxScore)
	}
}

func TestExportWireCarriesAttempts7D(t *testing.T) {
	// 摘自 ranking-export.v5.12 真实一行（保留本消费方关心的字段）。若 rpdiag 未来在
	// v5.x 内改动 attempts_7d 语义/形状，本测试与 docs/contract-ranking-export.md 一起
	// 迫使双方重新对齐。
	const wire = `{
	  "schema_version": "ranking-export.v5.12",
	  "items": [{
	    "channel_name": "O-Max", "provider_name": "SaiAI",
	    "service_cli_command": "claude", "model": "claude-sonnet-4-5",
	    "model_key": "claude-sonnet-4-5", "quality_state": "scored",
	    "attempts_7d": 12, "attempts_30d": 40,
	    "score_trend": {"latest": 88, "latest_at": "2026-07-12T00:00:00Z", "avg_30d": 90}
	  }]
	}`
	var payload exportPayload
	if err := json.Unmarshal([]byte(wire), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(payload.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(payload.Items))
	}
	got := payload.Items[0].Attempts7D
	if got == nil || *got != 12 {
		t.Errorf("Attempts7D = %v, want 12 (wire contract regression?)", got)
	}
}

func qualityKeysOf(m map[string]ChannelQualitySignal) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestQualitySignals_GenerationBumpsOnRefresh_FreshDerived proves QualitySignals
// derives a fresh projection on the first call (Fresh, Generation>0, populated
// ByBucket) and that a second call served from the unexpired cache neither
// refetches nor inflates Generation — a single refresh bumps generation exactly
// once.
func TestQualitySignals_GenerationBumpsOnRefresh_FreshDerived(t *testing.T) {
	fresh := *freshAt()
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":97.0,"latest_at":"` + fresh + `"}}]}`

	var fetches int32
	srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fetches, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, claude)
	})
	defer srv.Close()

	client := NewClient(nil, srv.URL, DefaultTTL, true)
	client.nowFn = fixedClock

	snap, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("first QualitySignals returned error: %v", err)
	}
	if !snap.Fresh {
		t.Errorf("first snapshot Fresh = false, want true")
	}
	if snap.Generation == 0 {
		t.Errorf("first snapshot Generation = 0, want > 0")
	}
	if _, ok := snap.ByBucket["saiai|cc|o-max"]; !ok {
		t.Errorf("ByBucket missing saiai|cc|o-max, got keys %v", qualityKeysOf(snap.ByBucket))
	}

	snap2, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("second QualitySignals returned error: %v", err)
	}
	if snap2.Generation != snap.Generation {
		t.Errorf("Generation inflated on cached read: first=%d second=%d", snap.Generation, snap2.Generation)
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Errorf("base board fetched %d times, want 1 (unexpired cache must serve the second call)", got)
	}
}

// TestQualitySignals_StaleAfterExpiry_NotFresh proves the fail-open contract: once
// the cache expires and every upstream fetch fails, QualitySignals returns no error
// (never propagates the refresh failure), reports Fresh==false (the freeze signal
// automove treats as "don't move anything"), and still exposes the last-known
// ByBucket projection.
func TestQualitySignals_StaleAfterExpiry_NotFresh(t *testing.T) {
	fresh := *freshAt()
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Max","relaypulse_channel_key":"max","provider_name":"SAIAi",` +
		`"service_cli_command":"claude","model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":97.0,"latest_at":"` + fresh + `"}}]}`

	srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, claude)
	})

	clock := testNow
	client := NewClient(nil, srv.URL, time.Minute, true)
	client.nowFn = func() time.Time { return clock }

	snap, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("first QualitySignals returned error: %v", err)
	}
	if !snap.Fresh {
		t.Fatalf("first snapshot Fresh = false, want true")
	}
	if _, ok := snap.ByBucket["saiai|cc|o-max"]; !ok {
		t.Fatalf("ByBucket missing saiai|cc|o-max after first load, got %v", qualityKeysOf(snap.ByBucket))
	}

	// Kill upstream and advance past the TTL so the next call must refresh, fail,
	// and fail-open to the stale projection.
	srv.Close()
	clock = clock.Add(2 * time.Minute)

	stale, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("QualitySignals after expiry+fetch failure returned error, want nil (fail-open): %v", err)
	}
	if stale.Fresh {
		t.Errorf("stale snapshot Fresh = true, want false (freeze signal)")
	}
	if _, ok := stale.ByBucket["saiai|cc|o-max"]; !ok {
		t.Errorf("stale snapshot dropped last-known bucket saiai|cc|o-max, got %v", qualityKeysOf(stale.ByBucket))
	}
}

// TestQualitySignals_TriggerModelsNotAliasedIntoCache proves the returned
// snapshot's TriggerModels slice does not share a backing array with the cached
// projection: mutating the returned slice must not corrupt what a later read
// observes. This mirrors the deep-copy discipline in cloneScores /
// normalizeHardFailTrend — the cache is handed to concurrent readers, so a
// caller-visible slice aliasing into it is a data-race / corruption hazard.
func TestQualitySignals_TriggerModelsNotAliasedIntoCache(t *testing.T) {
	// O-Trigger is hard-fail-active with a tail-three all-null attempts history;
	// O-Healthy carries a fresh sample for the same (service, model) so that model
	// counts as globally active, which is the precondition for O-Trigger to emit a
	// HardFail signal with a non-empty TriggerModels.
	claude := `{"schema_version":"ranking-export.v5.6","items":[` +
		`{"channel_name":"O-Trigger","provider_name":"SAIAi","service_cli_command":"claude",` +
		`"model":"claude-opus-4-8","model_key":"claude-opus-4-8","hard_fail_active":true,` +
		`"score_trend":{"recent_attempts":[null,null,null]}},` +
		`{"channel_name":"O-Healthy","provider_name":"SAIAi","service_cli_command":"claude",` +
		`"model":"claude-opus-4-8","model_key":"claude-opus-4-8",` +
		`"score_trend":{"latest":95.0,"latest_at":"` + *freshAt() + `"}}]}`

	srv := singleBoardServer(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(w, claude)
	})
	defer srv.Close()

	client := NewClient(nil, srv.URL, DefaultTTL, true)
	client.nowFn = fixedClock

	const key = "saiai|cc|o-trigger"
	snap, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("QualitySignals returned error: %v", err)
	}
	sig, ok := snap.ByBucket[key]
	if !ok || sig.State != QualityHardFail || len(sig.TriggerModels) == 0 {
		t.Fatalf("want hard-fail bucket %q with non-empty TriggerModels, got %+v (keys %v)",
			key, sig, qualityKeysOf(snap.ByBucket))
	}
	original := sig.TriggerModels[0]

	// Mutate the returned slice in place; the cache must not observe it.
	snap.ByBucket[key].TriggerModels[0] = "mutated-by-caller"

	// Cache is unexpired, so the second read is served from c.qualityByBucket
	// without a refetch — it must still carry the original, unmutated name.
	snap2, err := client.QualitySignals(context.Background())
	if err != nil {
		t.Fatalf("second QualitySignals returned error: %v", err)
	}
	if got := snap2.ByBucket[key].TriggerModels[0]; got != original {
		t.Errorf("TriggerModels aliased into cache: after caller mutation, second read sees %q, want %q",
			got, original)
	}
}
