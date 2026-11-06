package rpdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"monitor/internal/logger"
)

// The drawing column shows the pelican SVG from a channel's latest rpdiag runs.
// rpdiag's public `GET /tasks/drawings` resolves a run's scored task to its
// flow's drawing step and pairs it with the same group's official drawing; we
// proxy it so browsers never call rpdiag per row, and cache per task id since a
// finished drawing never changes.
const (
	// MaxDrawingIDs caps one Drawings call; the front end batches visible rows.
	MaxDrawingIDs = 30
	// upstreamDrawingBatch is rpdiag's own per-request limit.
	upstreamDrawingBatch = 24
	// maxSVGBytes drops drawings too large to be a reasonable thumbnail;
	// official drawings run about 5 KB.
	maxSVGBytes = 200 << 10
	// maxDrawingCacheBytes bounds the cached SVG text in total.
	maxDrawingCacheBytes = 32 << 20
	// A run whose two sides are both final keeps its entry for a day; anything
	// still pending, or omitted by rpdiag (group not yet revealed), is asked
	// again after a few minutes.
	finalDrawingTTL   = 24 * time.Hour
	pendingDrawingTTL = 5 * time.Minute
	// Upstream calls in flight at once, across all visitors.
	maxDrawingFetches = 2
	// Entries kept at most; runs rotate every hour or so, so expired entries
	// pile up without this even while the byte budget is far away.
	maxDrawingEntries = 4096
)

// svgPattern matches rpdiag's front-end extractSvg: greedy, case-insensitive.
var svgPattern = regexp.MustCompile(`(?is)<svg.*</svg>`)

// DrawingSide is one side of a run's drawing. State follows rpdiag
// (done / failed / pending / not_reached / none); SVG is set only for a done
// side whose answer held a complete, not oversized <svg>.
type DrawingSide struct {
	State string `json:"state"`
	SVG   string `json:"svg,omitempty"`
}

// Drawing is one run's drawing beside its group's official one. Official is
// nil when rpdiag could not name a single official run for the group.
type Drawing struct {
	TaskID    string       `json:"task_id"`
	Candidate DrawingSide  `json:"candidate"`
	Official  *DrawingSide `json:"official,omitempty"`
}

type drawingEntry struct {
	drawing   *Drawing // nil: rpdiag omitted the run
	expiresAt time.Time
	bytes     int
}

type drawingCache struct {
	mu      sync.Mutex
	entries map[string]drawingEntry
	bytes   int
	sem     chan struct{}
	once    sync.Once
}

func (dc *drawingCache) init() {
	dc.once.Do(func() {
		dc.entries = make(map[string]drawingEntry)
		dc.sem = make(chan struct{}, maxDrawingFetches)
	})
}

// Drawings returns the drawings of the requested runs, in request order. Ids
// that are not a RecentRuns entry of the current snapshot are skipped, as are
// runs rpdiag does not publish (yet). An upstream failure skips the runs that
// were not cached instead of failing the whole call; complete is then false so
// the caller can keep the partial answer out of shared caches.
func (c *Client) Drawings(ctx context.Context, taskIDs []string) (items []Drawing, complete bool, err error) {
	if c == nil {
		return nil, true, nil
	}
	if len(taskIDs) > MaxDrawingIDs {
		return nil, false, fmt.Errorf("at most %d task ids", MaxDrawingIDs)
	}
	if err := c.ensureRefreshed(ctx); err != nil {
		return nil, false, err
	}

	ids := c.filterKnownRuns(taskIDs)
	dc := &c.drawings
	dc.init()

	complete = true
	missing := dc.missing(ids, c.now())
	sort.Strings(missing)
	for start := 0; start < len(missing); start += upstreamDrawingBatch {
		end := min(start+upstreamDrawingBatch, len(missing))
		batch := missing[start:end]
		// Concurrent visitors asking for the same rows share one upstream call.
		key := strings.Join(batch, ",")
		_, err, _ := c.sf.Do("drawings:"+key, func() (interface{}, error) {
			if len(dc.missing(batch, c.now())) == 0 {
				return nil, nil
			}
			// The call is shared, so it must not die with whichever visitor
			// started it; it gets its own deadline instead.
			fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), requestTimeout)
			defer cancel()
			select {
			case dc.sem <- struct{}{}:
			case <-fetchCtx.Done():
				return nil, fetchCtx.Err()
			}
			defer func() { <-dc.sem }()
			items, err := c.fetchDrawings(fetchCtx, batch)
			if err != nil {
				return nil, err
			}
			dc.store(batch, items, c.now())
			return nil, nil
		})
		if err != nil {
			complete = false
			logger.FromContext(ctx, "rpdiag").Warn("获取 rpdiag 画作失败", "error", err, "count", len(batch))
		}
	}

	return dc.lookup(ids, c.now()), complete, nil
}

func (c *Client) filterKnownRuns(taskIDs []string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]bool, len(taskIDs))
	out := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		if _, ok := c.knownRuns[id]; ok && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (c *Client) drawingsURL() (string, error) {
	u, err := url.Parse(c.exportURL)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(u.Path, "/ranking/export") {
		return "", fmt.Errorf("export url %q has no /ranking/export path", c.exportURL)
	}
	u.Path = strings.TrimSuffix(u.Path, "/ranking/export") + "/tasks/drawings"
	u.RawQuery = ""
	return u.String(), nil
}

// rpdiag's DrawingListOut, reduced to what we keep.
type upstreamDrawingSide struct {
	State           string  `json:"state"`
	ResponsePreview *string `json:"response_preview"`
}

type upstreamDrawingItem struct {
	TaskID    string               `json:"task_id"`
	Candidate upstreamDrawingSide  `json:"candidate"`
	Official  *upstreamDrawingSide `json:"official"`
}

func (c *Client) fetchDrawings(ctx context.Context, taskIDs []string) ([]upstreamDrawingItem, error) {
	base, err := c.drawingsURL()
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	for _, id := range taskIDs {
		q.Add("task_ids", id)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "relaypulse/rpdiag-client")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rpdiag drawings fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rpdiag drawings HTTP %d", resp.StatusCode)
	}
	var payload struct {
		Items []upstreamDrawingItem `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&payload); err != nil {
		return nil, fmt.Errorf("rpdiag drawings decode: %w", err)
	}
	return payload.Items, nil
}

func drawingSideFrom(up upstreamDrawingSide) DrawingSide {
	side := DrawingSide{State: up.State}
	if up.State == "done" && up.ResponsePreview != nil {
		if svg := svgPattern.FindString(*up.ResponsePreview); len(svg) <= maxSVGBytes {
			// Clone: a substring would keep the whole decoded preview alive.
			side.SVG = strings.Clone(svg)
		}
	}
	return side
}

// isFinalDrawingState: the side will not change any more. A missing official
// side is final too: the group's official run exists from the start, and a
// group is only published once all its runs have finished.
func isFinalDrawingState(state string) bool {
	switch state {
	case "done", "failed", "not_reached", "none":
		return true
	}
	return false
}

func (dc *drawingCache) missing(ids []string, now time.Time) []string {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	var out []string
	for _, id := range ids {
		if e, ok := dc.entries[id]; !ok || !now.Before(e.expiresAt) {
			out = append(out, id)
		}
	}
	return out
}

func (dc *drawingCache) store(batch []string, items []upstreamDrawingItem, now time.Time) {
	byID := make(map[string]upstreamDrawingItem, len(items))
	for _, item := range items {
		byID[item.TaskID] = item
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()
	for _, id := range batch {
		entry := drawingEntry{expiresAt: now.Add(pendingDrawingTTL)}
		if item, ok := byID[id]; ok {
			d := &Drawing{TaskID: id, Candidate: drawingSideFrom(item.Candidate)}
			final := isFinalDrawingState(d.Candidate.State)
			if item.Official != nil {
				official := drawingSideFrom(*item.Official)
				d.Official = &official
				final = final && isFinalDrawingState(official.State)
				entry.bytes += len(official.SVG)
			}
			entry.bytes += len(d.Candidate.SVG)
			entry.drawing = d
			if final {
				entry.expiresAt = now.Add(finalDrawingTTL)
			}
		}
		if old, ok := dc.entries[id]; ok {
			dc.bytes -= old.bytes
		}
		dc.entries[id] = entry
		dc.bytes += entry.bytes
	}
	dc.evict(now)
}

// evict drops expired entries once the cache outgrows its byte or entry
// budget, then the soonest-expiring ones until it fits again.
func (dc *drawingCache) evict(now time.Time) {
	fits := func() bool {
		return dc.bytes <= maxDrawingCacheBytes && len(dc.entries) <= maxDrawingEntries
	}
	if fits() {
		return
	}
	for id, e := range dc.entries {
		if !now.Before(e.expiresAt) {
			dc.bytes -= e.bytes
			delete(dc.entries, id)
		}
	}
	if fits() {
		return
	}
	ids := make([]string, 0, len(dc.entries))
	for id := range dc.entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return dc.entries[ids[i]].expiresAt.Before(dc.entries[ids[j]].expiresAt)
	})
	for _, id := range ids {
		if fits() {
			break
		}
		dc.bytes -= dc.entries[id].bytes
		delete(dc.entries, id)
	}
}

func (dc *drawingCache) lookup(ids []string, now time.Time) []Drawing {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	out := make([]Drawing, 0, len(ids))
	for _, id := range ids {
		if e, ok := dc.entries[id]; ok && e.drawing != nil {
			out = append(out, *e.drawing)
		}
	}
	return out
}
