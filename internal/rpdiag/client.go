// Package rpdiag fetches and caches the public quality-score export from
// rpdiag (diag.relaypulse.top), indexing it by the (provider, service,
// channel) triple that relaypulse listings expose.
//
// The rpdiag export is test_case-scoped (one board per request); the default
// board is claude (quick-probe-v1). Each refresh fetches the configured base
// URL (claude) plus a derived URL per additional board (currently the codex
// board, quick-probe-codex-v1) and merges the rows, so cc and cx channels both
// get a quality column. Boards share one wire schema and disjoint join keys
// (the service segment differs), so the merge needs no special handling.
//
// Cache TTL is intentionally generous (10 min) — the upstream score moves
// on a sampler-cadence (~hourly) so refreshing per request would only
// burn rate quota on rpdiag. singleflight collapses concurrent refreshes;
// a refresh failure falls back to the last good snapshot so a transient
// upstream blip doesn't strip the column from the listing.
//
// The package is opt-in: NewClientFromEnv returns nil when
// MONITOR_RPDIAG_ENABLED is unset or false, so callers can skip wiring
// without conditionals.
package rpdiag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor/internal/logger"

	"golang.org/x/sync/singleflight"
)

const (
	defaultExportURL = "https://diag.relaypulse.top/api/v1/ranking/export?scoring_version=all"

	// codexBoardTestCase is the rpdiag root test-case slug for the codex (cx)
	// quality board. The default export URL (no test_case) returns the claude
	// (quick-probe-v1) board; refresh additionally fetches this board so cx
	// channels get a quality column too. Both boards share one wire schema, so
	// the merged rows flow through buildScoresAt unchanged — it already buckets
	// active models by service (cc/cx) and the join key embeds the service, so
	// the two boards cannot cross-activate or collide.
	codexBoardTestCase = "quick-probe-codex-v1"

	defaultTTL       = 10 * time.Minute
	requestTimeout   = 10 * time.Second
	maxResponseBytes = 10 << 20 // 10 MiB; export payload is < 1 MiB today
	// scoreStaleWindow bounds how old a channel's latest fingerprint sample may
	// be and still count as a *current* quality signal. A row whose newest
	// sample predates this window (and is not already hard-fail active) carries
	// no current signal — its representative score is forced to 0 so a channel
	// that was measured well once and then went dark (e.g. a model retired from
	// the sampler pool) cannot float to the top of the quality sort on a frozen
	// score. Kept equal to rpdiag's own 7-day hard-fail stale window so the two
	// "no current signal" notions stay aligned; bump both together.
	scoreStaleWindow = 7 * 24 * time.Hour
)

// ScoreTrend mirrors rpdiag's per-row sparkline payload. Consumers can render
// either the original 3-point form (avg_30d → avg_7d → latest) or the
// ranking-export.v5.2+ 5-point form (avg_30d → avg_7d → up to 3 most-recent
// single samples in time-ascending order). All fields are optional; nil
// counts mean "no in-scope samples".
type ScoreTrend struct {
	Latest   *float64 `json:"latest,omitempty"`
	LatestAt *string  `json:"latest_at,omitempty"`
	Avg7D    *float64 `json:"avg_7d,omitempty"`
	Avg30D   *float64 `json:"avg_30d,omitempty"`
	// RecentScores holds up to 3 most-recent single fingerprint samples in
	// time-ascending order (oldest → newest). nil on pre-v5.2 wire or when
	// no in-scope samples exist; len may be 1 or 2 during cold start.
	RecentScores []float64 `json:"recent_scores,omitempty"`
	// RecentAttempts holds up to 3 most-recent quality-relevant terminal
	// attempts in time-ascending order (ranking-export.v5.4+; bounded to the
	// last 7 days in v5.5). A non-nil element is a scored fingerprint sample; a
	// nil element is a hard-fail attempt (rendered as a neutral grey marker).
	// A non-nil empty slice means v5.5 found no in-window attempt — the front
	// end then draws no recent dots (NOT a fallback). A nil slice means the
	// upstream field was absent/null (pre-v5.5 wire); only then does the front
	// end fall back to RecentScores. Hence NO `omitempty`: the empty-vs-absent
	// distinction must survive re-serialization to the browser. Unlike
	// RecentScores this is not used for the representative score.
	RecentAttempts []*float64 `json:"recent_attempts"`
	N7D            int        `json:"n_7d"`
	N30D           int        `json:"n_30d"`
}

// ModelScore captures one (channel, model) row from rpdiag.
//
// Failed marks a row rpdiag currently considers hard-fail active (its most
// recent evaluations died before scoring); for such rows Score and Trend are
// normalized to 0/grey so the column shows "scored, currently out" instead of a
// stale value or nothing. A stale (but not hard-fail) row keeps its real
// historical Score and Trend for display — only its contribution to the
// channel's active-model average ranking key is zeroed (see buildScoresAt).
// AvailabilityWarning carries rpdiag's user-facing reason string, surfaced in
// the cell tooltip.
type ModelScore struct {
	Model               string     `json:"model,omitempty"`
	ModelKey            string     `json:"model_key,omitempty"`
	Score               *float64   `json:"score,omitempty"`
	Trend               ScoreTrend `json:"trend"`
	DetailURL           string     `json:"detail_url,omitempty"`
	Failed              bool       `json:"failed,omitempty"`
	AvailabilityWarning string     `json:"availability_warning,omitempty"`
	// Unavailable marks a row rpdiag reports as quality_state="unavailable" and
	// NOT hard-fail-active (v5.10 stale-scored / never-scored aged rows). The
	// front end renders it grey ("can't measure") while keeping its historical
	// recent_attempts dots; unlike Failed it does NOT zero the display trend.
	Unavailable bool `json:"unavailable,omitempty"`
	// NoRecentAttempts 标记 rpdiag attempts_7d==0（近 7 天无终态评测记录）且非
	// hard-fail-active 的行。纯展示新鲜度信号：前端把该 model 的真实历史 sparkline
	// 降饱和并注「近7天无评测记录」，而不是让一周前的健康点读成当前状态。与排名正交
	// （排名另按 latest_at 沉 stale 行）。gate 在 !hard_fail_active 故与 Failed 互斥。
	NoRecentAttempts bool `json:"no_recent_attempts,omitempty"`
}

// Score is the aggregated quality view for one (provider, service, channel)
// triple. MaxScore is a historical wire name (`max_score`); today it holds the
// ranking key computed as the *average* current-signal score across this
// channel's globally active models. Fresh models contribute their 30-day mean
// (`trend.avg_30d`); active hard-fail/stale models contribute 0; globally
// retired models (no fresh row anywhere in the same service's snapshot) are
// removed from every channel's numerator and denominator alike. A channel with
// no active-model rows at all carries no current signal — MaxScore stays nil so
// the quality sort sinks it below every scored channel. Averaging (rather than
// max) means a channel that hosts several models but can only still deliver one
// ranks on its true availability, not on that lone survivor.
type Score struct {
	MaxScore   *float64     `json:"max_score,omitempty"`
	Models     []ModelScore `json:"models"`
	Trend      ScoreTrend   `json:"trend"`
	ChannelURL string       `json:"channel_url"`
}

// Client is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	exportURL  string
	// boardURLs is the set of export URLs fetched and merged on every refresh:
	// the base exportURL (claude board) plus one derived URL per additional
	// rpdiag board (currently codex). Populated by the constructors.
	boardURLs []string
	ttl       time.Duration
	// nowFn is the clock; defaults to time.Now. Tests override it to drive the
	// staleness gate (scoreStaleWindow) and cache expiry deterministically.
	nowFn func() time.Time

	mu    sync.RWMutex
	cache map[string]Score
	// qualityByBucket is the quality projection of the SAME refresh that produced
	// cache: both are written together under mu in refresh so Scores and
	// QualitySignals never observe a torn (scores-from-refresh-N,
	// signals-from-refresh-M) pair. Keyed like buildQualitySignalsAt.
	qualityByBucket map[string]ChannelQualitySignal
	// generation counts successful refreshes; it is bumped exactly once per
	// refresh (under mu, alongside the two projections) so QualitySignals'
	// consumers can detect a new load without inflating on cache hits.
	generation uint64
	expiresAt  time.Time

	sf singleflight.Group
}

// NewClient constructs a Client with explicit dependencies. Used by tests
// and rare callers that want to bypass env-based wiring; production code
// should call NewClientFromEnv. Passing enabled=false returns a Client that
// always serves an empty snapshot (useful for tests that need a non-nil
// reference).
func NewClient(httpClient *http.Client, exportURL string, ttl time.Duration, enabled bool) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}
	if ttl <= 0 {
		ttl = defaultTTL
	}
	exportURL = strings.TrimSpace(exportURL)
	if exportURL == "" {
		exportURL = defaultExportURL
	}
	c := &Client{
		httpClient: httpClient,
		exportURL:  exportURL,
		boardURLs:  boardURLsFromExportURL(exportURL),
		ttl:        ttl,
		nowFn:      time.Now,
	}
	if !enabled {
		// Disabled clients still need to honour the Scores() contract; tag
		// them so external code can branch if desired.
		c.cache = map[string]Score{}
		c.expiresAt = c.now().Add(time.Hour) // freeze empty snapshot
	}
	return c
}

// boardURLsFromExportURL derives the list of export URLs fetched and merged on
// each refresh. The base URL is the claude board (rpdiag's default when no
// test_case is set); each additional board is the same URL with test_case set
// to that board's root slug, so an operator override of MONITOR_RPDIAG_EXPORT_URL
// (e.g. a staging host) transparently carries to every board. If the base URL
// can't be parsed, only it is fetched.
func boardURLsFromExportURL(exportURL string) []string {
	boards := []string{exportURL}
	u, err := url.Parse(exportURL)
	if err != nil {
		return boards
	}
	q := u.Query()
	q.Set("test_case", codexBoardTestCase)
	u.RawQuery = q.Encode()
	return append(boards, u.String())
}

// Exported constants for tests.
const (
	DefaultExportURL = defaultExportURL
	DefaultTTL       = defaultTTL
)

// NewClientFromEnv returns a Client when MONITOR_RPDIAG_ENABLED is truthy,
// otherwise nil. Recognized env vars:
//
//	MONITOR_RPDIAG_ENABLED      "1"/"true"/"yes" → enable, default disabled
//	MONITOR_RPDIAG_EXPORT_URL   override the rpdiag export endpoint
//	MONITOR_RPDIAG_CACHE_TTL    Go duration string (e.g. "5m"), defaults 10m
func NewClientFromEnv() *Client {
	if !enabledFromEnv(os.Getenv("MONITOR_RPDIAG_ENABLED")) {
		return nil
	}

	exportURL := strings.TrimSpace(os.Getenv("MONITOR_RPDIAG_EXPORT_URL"))
	if exportURL == "" {
		exportURL = defaultExportURL
	}
	ttl := defaultTTL
	if raw := strings.TrimSpace(os.Getenv("MONITOR_RPDIAG_CACHE_TTL")); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			ttl = parsed
		}
	}

	return &Client{
		httpClient: &http.Client{Timeout: requestTimeout},
		exportURL:  exportURL,
		boardURLs:  boardURLsFromExportURL(exportURL),
		ttl:        ttl,
		nowFn:      time.Now,
	}
}

// now returns the client clock, tolerating a zero-value Client (nowFn unset)
// constructed by older callers or struct literals.
func (c *Client) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

// enabledFromEnv defaults to *disabled*; only explicit truthy strings flip
// it on. This is deliberate — operators must opt-in to surface a third-
// party signal on the listing.
func enabledFromEnv(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Scores returns the cached score index, refreshing from upstream when the
// cache has expired. A refresh failure with a previous snapshot still
// available returns the stale snapshot rather than an error.
func (c *Client) Scores(ctx context.Context) (map[string]Score, error) {
	if c == nil {
		return map[string]Score{}, nil
	}
	if err := c.ensureRefreshed(ctx); err != nil {
		return nil, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cache == nil {
		return map[string]Score{}, nil
	}
	return cloneScores(c.cache), nil
}

// QualitySignals returns the quality projection of the current cache, refreshing
// through the SAME singleflight-guarded path Scores uses (so one refresh feeds
// both projections and bumps generation once). It is fail-open: it never returns
// a non-nil error. When every refresh fails and no prior snapshot exists it
// serves an empty projection with Fresh=false; when a stale snapshot exists it
// serves that with Fresh=false. Fresh=false is the freeze signal automove treats
// as "don't move anything".
func (c *Client) QualitySignals(ctx context.Context) (QualitySnapshot, error) {
	if c == nil {
		return QualitySnapshot{}, nil
	}
	// Deliberately ignore the refresh error: unlike Scores (which surfaces a
	// first-ever-load failure), QualitySignals always degrades to a stale/empty
	// projection with Fresh=false rather than erroring, so a transient upstream
	// blip freezes automove instead of failing it.
	_ = c.ensureRefreshed(ctx)

	c.mu.RLock()
	defer c.mu.RUnlock()
	fresh := c.cache != nil && !c.now().After(c.expiresAt)
	bucket := make(map[string]ChannelQualitySignal, len(c.qualityByBucket))
	for k, v := range c.qualityByBucket {
		// Deep-copy TriggerModels: a shallow struct copy would leave the returned
		// slice aliasing the cached backing array, so a caller mutating it (or a
		// concurrent reader) would corrupt/race the cache. Mirrors cloneScores.
		if v.TriggerModels != nil {
			models := make([]string, len(v.TriggerModels))
			copy(models, v.TriggerModels)
			v.TriggerModels = models
		}
		bucket[k] = v
	}
	return QualitySnapshot{Generation: c.generation, Fresh: fresh, ByBucket: bucket}, nil
}

// ensureRefreshed shares one singleflight across Scores and QualitySignals so a
// single refresh populates both projections and bumps generation once. It returns
// nil when a usable snapshot exists after the call (fresh, or stale after a failed
// refresh) and returns the refresh error only when refresh failed AND no prior
// snapshot exists. The neutral "refresh" key (not "scores") reflects that the
// refresh now feeds more than the score index.
func (c *Client) ensureRefreshed(ctx context.Context) error {
	if c.isFresh(c.now()) {
		return nil
	}
	_, err, _ := c.sf.Do("refresh", func() (interface{}, error) {
		if c.isFresh(c.now()) {
			return nil, nil
		}
		if _, refreshErr := c.refresh(ctx); refreshErr != nil {
			if _, ok := c.staleSnapshot(); ok {
				return nil, nil // fail-open: the last good snapshot is still usable
			}
			return nil, refreshErr
		}
		return nil, nil
	})
	return err
}

// isFresh reports whether the cache holds a usable, unexpired snapshot. It is a
// bool check so ensureRefreshed and QualitySignals can test freshness without
// cloning the whole score index.
func (c *Client) isFresh(now time.Time) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cache != nil && !now.After(c.expiresAt)
}

func (c *Client) staleSnapshot() (map[string]Score, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.cache == nil {
		return nil, false
	}
	return cloneScores(c.cache), true
}

// exportPayload mirrors the rpdiag ranking-export wire schema we consume
// (v5.1 baseline + v5.2 recent_scores). Only the fields the client needs
// are bound; unknown fields are dropped.
type exportPayload struct {
	SchemaVersion string       `json:"schema_version"`
	Items         []rankingRow `json:"items"`
}

type rankingRow struct {
	ChannelName          string `json:"channel_name"`
	RelaypulseChannelKey string `json:"relaypulse_channel_key"`
	// RelaypulseChannelID is rpdiag export v5.9's immutable cross-product join
	// anchor (`ch_<uuidv4>`): rpdiag's sampler captured it from relay-pulse's own
	// /api/status channel_id, so it round-trips relay-pulse → rpdiag → here. ""/
	// absent on pre-v5.9 wire or rows rpdiag has not yet tagged. NOT the same as
	// RelaypulseChannelKey (a decoded display form) — this is the opaque stable id
	// that drives cid-priority bucketing (see resolveBucketKeys / buildScoresAt).
	RelaypulseChannelID string     `json:"relaypulse_channel_id"`
	ProviderName        string     `json:"provider_name"`
	ServiceCLICommand   string     `json:"service_cli_command"`
	SubmissionSource    string     `json:"submission_source"`
	Model               string     `json:"model"`
	ModelKey            string     `json:"model_key"`
	DetailURL           string     `json:"detail_url"`
	FinalQualityScore   *float64   `json:"final_quality_score"`
	ScoreTrend          ScoreTrend `json:"score_trend"`
	// HardFailActive is rpdiag's current-availability gate (export v5.13): the
	// newest two conclusive outcomes were hard-fails (FAILED with no
	// fingerprint score; a scored outcome ends the run, an unscored DONE step
	// is skipped) and the latest fail is within rpdiag's 7-day stale window.
	// rpdiag forces its own `final_quality_score` to 0 under the same
	// condition; we mirror that as a representative score of 0.
	HardFailActive      bool   `json:"hard_fail_active"`
	AvailabilityWarning string `json:"availability_warning"`
	// QualityState is rpdiag export v5.3+'s row state: "scored" (has/had a real
	// fingerprint sample) or "unavailable" (a registered model with no current
	// scoreable data — never scored, or scored once then went dark >30d, v5.10).
	// "" on pre-v5.3 wire → treated as "scored". An unavailable row that is NOT
	// hard-fail-active is kept for display but excluded from the channel's
	// active-model average, so a channel with no current signal sinks on a nil
	// MaxScore rather than a misleading 0 (see buildScoreRowView / buildScoresAt).
	QualityState string `json:"quality_state"`
	// Attempts7D 是 rpdiag export 每 descriptor 近 7 天 REVEALED 终态（DONE/FAILED）
	// task 计数（statement.py 的 cutoff_short）。它计入**我方侧 measurement 失败**——
	// 表示「近 7 天有没有终态评测记录」，不表示「探测真正打到了通道」。用 *int：旧 wire
	// 缺字段 → nil → 语义「未知」→ 绝不误判为 0。
	Attempts7D *int `json:"attempts_7d"`
}

func (c *Client) refresh(ctx context.Context) (map[string]Score, error) {
	boards := c.boardURLs
	if len(boards) == 0 {
		// Defensive: a struct-literal Client (httpClient/exportURL set by hand,
		// cf. now()'s nil-nowFn guard) never ran a constructor, so boardURLs is
		// unset. Derive from exportURL so refresh still fetches rather than
		// caching an empty snapshot. (A true zero-value Client would already
		// panic at httpClient.Do below.)
		base := strings.TrimSpace(c.exportURL)
		if base == "" {
			base = defaultExportURL
		}
		boards = boardURLsFromExportURL(base)
	}

	// All boards must refresh together. A single board's failure returns an
	// error so Scores() serves the last full good snapshot rather than caching
	// a snapshot missing a board — which would blank that board's whole column.
	// The served snapshot is the last *successful* refresh; if refresh keeps
	// failing it outlives the TTL (the TTL only paces retries), exactly as the
	// pre-multi-board single fetch already behaved. Any fetch error → the
	// existing stale-snapshot fallback in Scores().
	var rows []rankingRow
	for _, boardURL := range boards {
		items, err := c.fetchBoard(ctx, boardURL)
		if err != nil {
			return nil, fmt.Errorf("rpdiag board %q: %w", boardURL, err)
		}
		rows = append(rows, items...)
	}

	now := c.now()
	scores := c.buildScoresAt(rows, now)
	signals := buildQualitySignalsAt(rows, now)

	// One lock, one writeback: the score index, the quality projection, the
	// generation bump, and the expiry all become visible together. This is the
	// invariant that lets Scores and QualitySignals share a single refresh path
	// without ever exposing a half-updated cache.
	c.mu.Lock()
	c.cache = scores
	c.qualityByBucket = signals
	c.generation++
	c.expiresAt = now.Add(c.ttl)
	c.mu.Unlock()

	return cloneScores(scores), nil
}

// fetchBoard GETs and decodes one rpdiag export board, returning its rows. The
// schema-version guard is applied per board so an unexpected wire on any board
// fails the whole refresh (and triggers the stale-snapshot fallback) rather
// than silently dropping that board's rows.
func (c *Client) fetchBoard(ctx context.Context, boardURL string) ([]rankingRow, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, boardURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "relaypulse/rpdiag-client")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpdiag fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rpdiag export HTTP %d", resp.StatusCode)
	}

	var payload exportPayload
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("rpdiag decode: %w", err)
	}
	if payload.SchemaVersion != "" && !strings.HasPrefix(payload.SchemaVersion, "ranking-export.v5") {
		return nil, fmt.Errorf("rpdiag unsupported schema_version %q", payload.SchemaVersion)
	}

	return payload.Items, nil
}

// latestFingerprintSample returns the most recent single fingerprint sample
// from a trend — the value the sparkline's rightmost dot already renders
// (front-end uses recent_scores[-1] when present, falling back to trend.latest;
// rpdiag fills both with the same value, so the tooltip's "latest=" row stays
// aligned in practice). It drives the per-model display Score and the
// fresh/stale activity gates; the channel ranking key ranks fresh rows on
// trend.avg_30d instead (see buildScoreRowView).
//
// Prefers recent_scores[-1] when v5.2 wire carries it (one true sample,
// strictly time-ascending), falling back to trend.latest on v5.1.
func latestFingerprintSample(t ScoreTrend) *float64 {
	if n := len(t.RecentScores); n > 0 {
		v := t.RecentScores[n-1]
		return &v
	}
	return t.Latest
}

// normalizeHardFailTrend returns a display-only ScoreTrend for a row rpdiag has
// flagged as currently hard-fail active. The representative point is forced to
// 0 and tagged via ModelScore.Failed; the front end renders that endpoint in a
// neutral unavailable grey at the floor (grey = couldn't measure, distinct from
// the red qualityScoreColor uses for a genuinely poor measured response). The
// historical window averages are kept so the sparkline reads as "dropped from
// high to unavailable". The synthetic 0 has no real sample timestamp, so
// LatestAt is cleared.
//
// RecentScores is rebuilt into a fresh slice (last up-to-2 real samples, then
// the synthetic 0; just [0] when there is no history). It never aliases the
// decoded row's backing array, which stays cached and is handed to concurrent
// readers.
func normalizeHardFailTrend(t ScoreTrend) ScoreTrend {
	out := t
	zero := 0.0
	out.Latest = &zero
	out.LatestAt = nil

	recent := make([]float64, 0, 3)
	if n := len(t.RecentScores); n > 0 {
		start := 0
		if n > 2 {
			start = n - 2
		}
		recent = append(recent, t.RecentScores[start:]...)
	}
	recent = append(recent, 0)
	out.RecentScores = recent
	// RecentAttempts (v5.4) carries real per-attempt grey markers and is left
	// untouched — it is the preferred source for the sparkline's recent slots,
	// so it must not be overwritten by this v5.3-era synthetic RecentScores tail.
	return out
}

// isStaleScoreTrend reports whether a trend's latest fingerprint sample is
// older than scoreStaleWindow, i.e. carries no *current* quality signal.
//
// Fail-closed: a missing or unparseable latest_at counts as stale. v5.5 export
// always stamps latest_at on a scored row, so an absent value means the upstream
// regressed or the wire is untrustworthy — surfacing that loudly (the channel
// drops to a red 0) beats silently trusting a value we cannot date. The window
// is the same 7 days rpdiag uses for its hard-fail gate.
//
// latest_at is RFC3339 with sub-second precision (e.g. "...01.813413Z").
// RFC3339Nano is used to make that shape explicit; Go's time.Parse accepts a
// fractional second whether or not the layout names it, so both fractional and
// bare timestamps parse fine.
func isStaleScoreTrend(t ScoreTrend, now time.Time) bool {
	if t.LatestAt == nil || strings.TrimSpace(*t.LatestAt) == "" {
		return true
	}
	latestAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(*t.LatestAt))
	if err != nil {
		return true
	}
	return latestAt.Before(now.Add(-scoreStaleWindow))
}

// buildScores collapses many rpdiag rows into one entry per (provider,
// service, channel) triple. Rows that lack a representative fingerprint
// sample, or that come from the public /submit pipeline
// (`submission_source=user` / `U-` channel prefix), are skipped — those
// entries don't exist in relaypulse listings and would never join.
//
// Display vs ranking are deliberately decoupled. The per-model Trend (the
// sparkline) and Score are shown exactly as rpdiag exported them — the line is
// an honest history coloured by real quality, the only grey being a hard-fail
// row that rpdiag couldn't score. The ranking contribution of a fresh row is
// its 30-day mean (`trend.avg_30d`, falling back to the latest sample when the
// wire lacks it) — not the newest single sample, which is too noisy to order
// channels by, and not rpdiag's composite `final_quality_score`, which folds
// in latency/availability — that belongs to rpdiag's own ranking page.
//
// The channel ranking key (historical wire name MaxScore/max_score) is the
// average current-signal score over the channel's *globally active* models. A
// model counts as globally active when at least one consumable row in the same
// service has a fresh current sample anywhere in the snapshot. Each such model
// the channel hosts contributes its rankLatest to the average; two cases make
// that contribution 0:
//   - hard-fail active: rpdiag flags the channel as currently failing (also
//     normalized to a grey 0 trend for display, the only grey).
//   - stale: a non-hard-fail row whose latest sample predates scoreStaleWindow.
//     Its history is displayed untouched; only its ranking contribution is 0.
//
// Models that are retired platform-wide (no fresh row in any channel for that
// service — e.g. a model dropped from the sampler pool) are excluded from both
// the numerator and the denominator, so they neither help nor punish anyone.
// Averaging (rather than the old max()) means a channel that hosts several
// models but can only still deliver one is ranked on its true availability,
// not floated to the top on that lone survivor. A channel left with no active
// models keeps a nil MaxScore and sinks below every scored channel.
//
// The hard-fail check MUST come first: normalizeHardFailTrend clears LatestAt,
// so testing staleness afterwards would misread its 0 trend as stale.
func (c *Client) buildScores(rows []rankingRow) map[string]Score {
	return c.buildScoresAt(rows, c.now())
}

// scoreRowView is a single rpdiag row reduced to exactly what both passes of
// buildScoresAt need: the join key, the canonical (service, model) identity for
// the active-model set, the display/rank representative scores, and whether the
// row is a fresh current signal. Computing it once keeps the activeness pass and
// the aggregation pass from drifting apart on the hard-fail/stale rules.
type scoreRowView struct {
	row              rankingRow
	key              string // legacy triple at first; rewritten to the bucket key (cid: or legacy) in buildScoresAt
	cid              string // raw relaypulse_channel_id ("" if none); opaque, trimmed, never lower-cased
	service          string
	modelKey         string
	displayLatest    *float64
	rankLatest       *float64
	trend            ScoreTrend
	fresh            bool
	excludeFromRank  bool // quality_state=="unavailable" && !HardFailActive: kept for display, out of the average
	noRecentAttempts bool // attempts_7d==0 && !HardFailActive：展示层降饱和标志
}

// buildScoreRowView normalizes one row, returning ok=false for rows relaypulse
// never consumes (public /submit entries, non-unavailable rows without a
// representative sample, rows missing any part of the join triple or a model
// identity). quality_state="unavailable" rows are kept even without a sample so
// they can render the grey "can't measure" cell. The returned
// rankLatest is always non-nil when ok is true — EXCEPT on excludeFromRank rows
// (a non-hard-fail unavailable row with no representative sample leaves
// rankLatest nil), and those rows are `continue`d in buildScoresAt before any
// rankLatest deref, so no nil ever reaches the average.
func buildScoreRowView(row rankingRow, now time.Time) (scoreRowView, bool) {
	// displayLatest drives the per-model Score/Trend (honest history);
	// rankLatest drives the average ranking key (0 when unmeasurable now).
	displayLatest := latestFingerprintSample(row.ScoreTrend)
	rankLatest := displayLatest
	trend := row.ScoreTrend
	fresh := false
	switch {
	case row.HardFailActive:
		trend = normalizeHardFailTrend(row.ScoreTrend)
		displayLatest = trend.Latest // grey 0, shown + ranked
		rankLatest = trend.Latest
	case displayLatest != nil && isStaleScoreTrend(row.ScoreTrend, now):
		// Keep the historical trend exactly as exported; just don't let a
		// frozen old sample rank the channel as currently good. This gate must
		// stay ahead of the avg_30d ranking below: a stale row's avg_30d is a
		// frozen mean (failed probes add no samples, so it never decays) and
		// would otherwise keep ranking a dead channel for up to 30 days.
		zero := 0.0
		rankLatest = &zero
	case displayLatest != nil:
		fresh = true
		// Rank fresh rows on the 30-day mean, not the newest single sample:
		// one unlucky draw shouldn't swing the channel ranking, and once
		// rpdiag starts folding channel-side hard-fails into avg_30d as zero
		// samples the ranking inherits failure weighting with no change here.
		// Display (Score/Trend) keeps showing the newest sample untouched.
		// Fallback: a fresh row without avg_30d ranks on the sample itself —
		// v5.7+ scopes both to the same 30d window so a fresh row always
		// carries the mean, but older wire shapes may not.
		if row.ScoreTrend.Avg30D != nil {
			v := *row.ScoreTrend.Avg30D
			rankLatest = &v
		}
	}
	unavailable := strings.EqualFold(row.QualityState, "unavailable")
	// An unavailable row that isn't hard-fail-active carries no current signal:
	// keep it for the grey "can't measure" cell (bypass the displayLatest==nil
	// drop below) but keep it OUT of the active-model average so it can't fold a
	// 0 into MaxScore when its model is fresh elsewhere. Hard-fail-active
	// unavailable rows are untouched — they already flow through the
	// HardFailActive case above (grey 0, counted), so v5.9 wire stays no-op.
	excludeFromRank := unavailable && !row.HardFailActive
	// 近 7 天零终态评测记录。gate 在 !HardFailActive → 与 Failed 数据层互斥（一次
	// hard-fail 本身就是一次终态 attempt，一致快照下二者不会同真）。nil（旧 wire）→ false。
	noRecentAttempts := row.Attempts7D != nil && *row.Attempts7D == 0 && !row.HardFailActive
	if !unavailable && displayLatest == nil {
		return scoreRowView{}, false
	}
	if strings.EqualFold(row.SubmissionSource, "user") {
		return scoreRowView{}, false
	}

	provider := canonical(row.ProviderName)
	service := normalizeService(row.ServiceCLICommand)
	// Join on the raw channel_name, NOT relaypulse_channel_key. The latter
	// strips the leading O-/R-/M-/U- source prefix, which collapses distinct
	// channels whose only distinguishing part IS that prefix — e.g. a provider's
	// `o-cx` (paid) and `u-cx` (free) codex tiers both strip to "cx" and merge
	// into one cell. rpdiag ships the raw channel_name on every v5.x row and the
	// relaypulse monitor carries the same prefixed name, so a trim+lower match
	// keeps the tiers separate without either side sharing a prefix convention.
	// (relaypulse_channel_key stays decoded on the wire for observability.)
	channel := canonical(row.ChannelName)
	if provider == "" || service == "" || channel == "" {
		return scoreRowView{}, false
	}

	modelKey := canonical(row.ModelKey)
	if modelKey == "" {
		modelKey = canonical(row.Model)
	}
	if modelKey == "" {
		return scoreRowView{}, false
	}

	return scoreRowView{
		row:              row,
		key:              ScoreKey(provider, service, channel),
		cid:              strings.TrimSpace(row.RelaypulseChannelID),
		service:          service,
		modelKey:         modelKey,
		displayLatest:    displayLatest,
		rankLatest:       rankLatest,
		trend:            trend,
		fresh:            fresh,
		excludeFromRank:  excludeFromRank,
		noRecentAttempts: noRecentAttempts,
	}, true
}

// cidBucketKey is the score-index key for a channel identified by its immutable
// relay-pulse channel_id. The "cid:" prefix namespaces it apart from legacy
// ScoreKey triples ("provider|service|channel"), so both key forms coexist in
// one map without collision. cid is opaque — passed through trimmed, never
// lower-cased.
func cidBucketKey(cid string) string {
	return "cid:" + cid
}

// resolveBucketKeys decides, per legacy ScoreKey triple, whether that channel's
// rows bucket under a stable cid key ("cid:"+id) or stay on the legacy triple.
// It returns a legacy-key → bucket-key map. Rules (fail-closed; never silently
// pick an id):
//   - 0 distinct cids in the triple  → legacy key (no stable id yet; back-compat).
//   - exactly 1 cid X                → "cid:"+X for ALL the triple's rows, incl.
//     cid-less stragglers (e.g. a recently-retired model not yet re-tagged),
//     UNLESS X is observed under more than one service (a cid is per-channel
//     hence single-service; cross-service reuse = a duplicated channel_id
//     upstream) → then legacy key + error log.
//   - >1 distinct cids in one triple → legacy key + error log (ambiguous; a cid
//     bucket would be partial and the consumer would never fall back to legacy).
//
// Several legacy triples may resolve to the SAME "cid:"+X — the intended merge of
// a channel whose display name drifted (old + new name halves rejoin under the
// stable id). Per rpdiag's (cid,model)/(name,svc,prov,model) uniqueness each
// modelKey still appears at most once per cid bucket, so the downstream
// seen[modelKey] dedup and average stay correct regardless of row order.
func resolveBucketKeys(views []scoreRowView) map[string]string {
	cidsByLegacyKey := make(map[string]map[string]struct{}, len(views))
	servicesByCID := make(map[string]map[string]struct{})
	for _, view := range views {
		if _, ok := cidsByLegacyKey[view.key]; !ok {
			cidsByLegacyKey[view.key] = make(map[string]struct{})
		}
		if view.cid == "" {
			continue
		}
		cidsByLegacyKey[view.key][view.cid] = struct{}{}
		if servicesByCID[view.cid] == nil {
			servicesByCID[view.cid] = make(map[string]struct{})
		}
		servicesByCID[view.cid][view.service] = struct{}{}
	}

	bucketKeys := make(map[string]string, len(cidsByLegacyKey))
	for legacyKey, cids := range cidsByLegacyKey {
		switch len(cids) {
		case 0:
			bucketKeys[legacyKey] = legacyKey
		case 1:
			cid := soleKey(cids)
			if len(servicesByCID[cid]) > 1 {
				logger.Error("rpdiag",
					"relaypulse_channel_id reused across services; falling back to legacy join key",
					"legacy_key", legacyKey, "relaypulse_channel_id", cid,
					"service_count", len(servicesByCID[cid]))
				bucketKeys[legacyKey] = legacyKey
				continue
			}
			bucketKeys[legacyKey] = cidBucketKey(cid)
		default:
			logger.Error("rpdiag",
				"multiple relaypulse_channel_id in one channel bucket; falling back to legacy join key",
				"legacy_key", legacyKey, "distinct_cid_count", len(cids))
			bucketKeys[legacyKey] = legacyKey
		}
	}
	return bucketKeys
}

// soleKey returns the single key of a one-element set (caller guarantees len==1).
func soleKey(m map[string]struct{}) string {
	for k := range m {
		return k
	}
	return ""
}

func (c *Client) buildScoresAt(rows []rankingRow, now time.Time) map[string]Score {
	// Pass 1: reduce every consumable row to a view and learn which models are
	// still alive somewhere, bucketed by service so a future same-named model in
	// another service can't cross-activate.
	views := make([]scoreRowView, 0, len(rows))
	activeModels := make(map[string]map[string]bool)
	for _, row := range rows {
		view, ok := buildScoreRowView(row, now)
		if !ok {
			continue
		}
		views = append(views, view)
		if view.fresh {
			byModel := activeModels[view.service]
			if byModel == nil {
				byModel = make(map[string]bool)
				activeModels[view.service] = byModel
			}
			byModel[view.modelKey] = true
		}
	}

	// Consolidate to stable-id buckets where available: rewrite each view's key
	// from its legacy triple to the resolved bucket key (a "cid:"+id or the same
	// legacy triple). activeModels was already learned by (service, modelKey) and
	// is unaffected; every downstream pass keys off view.key, so the output map,
	// aggregation, and ChannelURL all follow the cid bucketing transparently.
	bucketKeys := resolveBucketKeys(views)
	for i := range views {
		views[i].key = bucketKeys[views[i].key]
	}

	// rankAgg accumulates the active-model average for one channel. byModel keeps
	// the largest rankLatest per active model: a (channel, model) is normally
	// unique, but cid consolidation can merge a renamed channel's halves and a
	// masquerading channel can report one effective model via two configs — taking
	// the max both stops a duplicate from inflating the divisor AND stops export
	// row order from picking a stale 0 over a fresh score. The distinct-model
	// average (sum of maxes / distinct count) is otherwise unchanged.
	type rankAgg struct {
		byModel map[string]float64
	}

	out := make(map[string]Score, len(views))
	aggs := make(map[string]*rankAgg, len(views))

	// Pass 2: build the per-model display entries (untouched from before) and,
	// for active models only, fold rankLatest into the channel average.
	for _, view := range views {
		row := view.row
		entry := out[view.key]
		if len(entry.Models) == 0 {
			// Channel-level Trend is not consumed by the front end (it reads
			// per-model trends); keep a representative one from the first row for
			// wire back-compat without letting it influence display.
			entry.Trend = view.trend
		}
		entry.Models = append(entry.Models, ModelScore{
			Model:               row.Model,
			ModelKey:            row.ModelKey,
			Score:               copyFloat(view.displayLatest),
			Trend:               view.trend,
			DetailURL:           row.DetailURL,
			Failed:              row.HardFailActive,
			AvailabilityWarning: row.AvailabilityWarning,
			Unavailable:         view.excludeFromRank,
			NoRecentAttempts:    view.noRecentAttempts,
		})
		if entry.ChannelURL == "" {
			// 通道整体跳转 = 首条可解析 detail_url 去掉 model 参数 → 落到 rpdiag 的
			// "服务商+通道"概览页（channel name 与大小写、前缀都来自 rpdiag，本地不再
			// 猜测路由规则）。均分后没有单一"最高分行"，任意一行去 model 后都指向同一
			// 通道页，取首条即可。
			entry.ChannelURL = channelURLFromDetailURL(row.DetailURL)
		}
		out[view.key] = entry

		if view.excludeFromRank || !activeModels[view.service][view.modelKey] {
			continue
		}
		agg := aggs[view.key]
		if agg == nil {
			agg = &rankAgg{byModel: make(map[string]float64)}
			aggs[view.key] = agg
		}
		if prev, ok := agg.byModel[view.modelKey]; !ok || *view.rankLatest > prev {
			agg.byModel[view.modelKey] = *view.rankLatest
		}
	}

	// Channels with no active-model rows are absent from aggs and keep the nil
	// MaxScore zero value, sinking below every scored channel in the sort.
	for key, agg := range aggs {
		if len(agg.byModel) == 0 {
			continue
		}
		// Sum in sorted modelKey order so the average is deterministic across runs
		// (Go map iteration is randomized and float addition is not associative).
		modelKeys := make([]string, 0, len(agg.byModel))
		for mk := range agg.byModel {
			modelKeys = append(modelKeys, mk)
		}
		sort.Strings(modelKeys)
		var sum float64
		for _, mk := range modelKeys {
			sum += agg.byModel[mk]
		}
		entry := out[key]
		avg := sum / float64(len(agg.byModel))
		entry.MaxScore = &avg
		out[key] = entry
	}

	for key, score := range out {
		sort.SliceStable(score.Models, func(i, j int) bool {
			return modelOrderScore(score.Models[i]) > modelOrderScore(score.Models[j])
		})
		out[key] = score
	}
	return out
}

// ScoreKey is the join key shape: lower-case "provider|service|channel".
// `service` should already be the relaypulse short code (cc/cx/gm), and
// `channel` is the raw rpdiag channel_name — no prefix stripping (see
// buildScoreRowView for why). ScoreKey trims + lower-cases each segment.
func ScoreKey(provider, service, channel string) string {
	return canonical(provider) + "|" + canonical(service) + "|" + canonical(channel)
}

// normalizeService maps rpdiag's CLI command name onto relaypulse's
// service code. Unknown services pass through unchanged so future tools
// integrate without code edits.
func normalizeService(cliCommand string) string {
	switch canonical(cliCommand) {
	case "claude":
		return "cc"
	case "codex":
		return "cx"
	case "gemini":
		return "gm"
	default:
		return canonical(cliCommand)
	}
}

func canonical(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

// channelURLFromDetailURL 从 model 级别的 detail_url 派生出 channel 级别链接：
// 解析 URL，丢弃 ?model= 查询参数后重新序列化。
//
// rpdiag 已经在 detail_url 里给了正确的 channel name（带前缀、大小写敏感）和
// 必要的 provider/service 限定符，去掉 model 后就是"服务商+通道"概览。这样
// relaypulse 不需要硬编码 rpdiag 路由规则，路由变化只需要 rpdiag 调整 detail_url
// 即可。detail_url 为空或不可解析时返回空，前端 nil-check 后不展示链接。
func channelURLFromDetailURL(detailURL string) string {
	trimmed := strings.TrimSpace(detailURL)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() {
		return ""
	}
	q := u.Query()
	q.Del("model")
	u.RawQuery = q.Encode()
	return u.String()
}

func modelOrderScore(m ModelScore) float64 {
	if m.Score == nil {
		return -1
	}
	return *m.Score
}

func copyFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

// cloneScoreTrend returns a copy whose slice fields are independent of the
// source. The other fields are value types or never-mutated pointers, so a
// shallow struct copy is enough for them.
func cloneScoreTrend(t ScoreTrend) ScoreTrend {
	if t.RecentScores != nil {
		t.RecentScores = append([]float64(nil), t.RecentScores...)
	}
	if t.RecentAttempts != nil {
		// Deep-copy: each element is a pointer, so a shallow slice copy would
		// still alias the underlying float64s with the cached snapshot. The nil
		// guard preserves the nil-vs-empty distinction (a non-nil empty slice
		// clones to a non-nil empty slice via make(.., 0)), which the front end
		// relies on to tell "no in-window attempt" from "old wire".
		attempts := make([]*float64, len(t.RecentAttempts))
		for i, v := range t.RecentAttempts {
			attempts[i] = copyFloat(v)
		}
		t.RecentAttempts = attempts
	}
	return t
}

func cloneScores(src map[string]Score) map[string]Score {
	dst := make(map[string]Score, len(src))
	for k, v := range src {
		models := make([]ModelScore, len(v.Models))
		copy(models, v.Models)
		for i := range models {
			models[i].Trend = cloneScoreTrend(models[i].Trend)
		}
		v.Models = models
		v.Trend = cloneScoreTrend(v.Trend)
		dst[k] = v
	}
	return dst
}

// ErrDisabled is returned by callers that want to distinguish "client not
// configured" from real upstream errors. The Client itself never returns
// it — callers should check NewClientFromEnv()==nil instead. Exposed
// so external tests can lean on the sentinel without copying the string.
var ErrDisabled = errors.New("rpdiag client disabled")
