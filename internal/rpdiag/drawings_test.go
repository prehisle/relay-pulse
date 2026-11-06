package rpdiag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// drawingStub serves one export board whose single row lists `runs` as its
// recent runs, and a /tasks/drawings endpoint answering from `items`.
type drawingStub struct {
	runs        []string
	items       map[string]upstreamDrawingItem
	failDrawing atomic.Bool
	calls       atomic.Int32
	mu          sync.Mutex
	batches     [][]string
}

func (s *drawingStub) lastBatch() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.batches[len(s.batches)-1]
}

func (s *drawingStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ranking/export":
			var items []map[string]any
			if r.URL.Query().Get("test_case") == "" {
				runs := make([]map[string]string, 0, len(s.runs))
				for _, id := range s.runs {
					runs = append(runs, map[string]string{"task_id": id, "run_at": "2026-11-06T01:00:00Z"})
				}
				items = append(items, map[string]any{
					"channel_name": "O-Max", "provider_name": "P", "service_cli_command": "claude",
					"model":       "claude-opus-5-5",
					"score_trend": map[string]any{"recent_runs": runs},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": "ranking-export.v5.18", "items": items})
		case "/api/v1/tasks/drawings":
			s.calls.Add(1)
			ids := r.URL.Query()["task_ids"]
			s.mu.Lock()
			s.batches = append(s.batches, ids)
			s.mu.Unlock()
			if s.failDrawing.Load() {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			out := []upstreamDrawingItem{}
			for _, id := range ids {
				if item, ok := s.items[id]; ok {
					out = append(out, item)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": out})
		default:
			http.NotFound(w, r)
		}
	}))
}

func preview(s string) *string { return &s }

func doneItem(id, candidate, official string) upstreamDrawingItem {
	return upstreamDrawingItem{
		TaskID:    id,
		Candidate: upstreamDrawingSide{State: "done", ResponsePreview: preview(candidate)},
		Official:  &upstreamDrawingSide{State: "done", ResponsePreview: preview(official)},
	}
}

func newDrawingClient(t *testing.T, stub *drawingStub) (*Client, *time.Time) {
	t.Helper()
	srv := stub.server(t)
	t.Cleanup(srv.Close)
	c := NewClient(srv.Client(), srv.URL+"/api/v1/ranking/export?scoring_version=current", time.Hour, true)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	c.nowFn = func() time.Time { return now }
	return c, &now
}

func TestDrawings_OnlyKnownRunsAndExtractsSVG(t *testing.T) {
	stub := &drawingStub{
		runs: []string{"r1"},
		items: map[string]upstreamDrawingItem{
			"r1":    doneItem("r1", "Here you go:\n```svg\n<SVG viewBox=\"0 0 1 1\"><g/></svg>\n```", "<svg>o</svg>"),
			"other": doneItem("other", "<svg>x</svg>", "<svg>y</svg>"),
		},
	}
	c, _ := newDrawingClient(t, stub)

	got, complete, err := c.Drawings(context.Background(), []string{"other", "r1", "r1"})
	if err != nil || !complete {
		t.Fatalf("err=%v complete=%v", err, complete)
	}
	if len(got) != 1 || got[0].TaskID != "r1" {
		t.Fatalf("want only the known run r1, got %+v", got)
	}
	if got[0].Candidate.SVG != `<SVG viewBox="0 0 1 1"><g/></svg>` {
		t.Fatalf("candidate svg = %q", got[0].Candidate.SVG)
	}
	if got[0].Official == nil || got[0].Official.SVG != "<svg>o</svg>" {
		t.Fatalf("official = %+v", got[0].Official)
	}
	for _, batch := range stub.batches {
		for _, id := range batch {
			if id == "other" {
				t.Fatal("an id outside the snapshot reached rpdiag")
			}
		}
	}
}

func TestDrawings_NoSVGAndOversizedSVGKeepStateWithoutMarkup(t *testing.T) {
	huge := "<svg>" + strings.Repeat("a", maxSVGBytes) + "</svg>"
	stub := &drawingStub{
		runs: []string{"refusal", "huge", "failed"},
		items: map[string]upstreamDrawingItem{
			"refusal": doneItem("refusal", "I can't draw that.", "<svg/>"),
			"huge":    doneItem("huge", huge, "<svg></svg>"),
			"failed":  {TaskID: "failed", Candidate: upstreamDrawingSide{State: "failed"}},
		},
	}
	c, _ := newDrawingClient(t, stub)

	got, _, err := c.Drawings(context.Background(), []string{"refusal", "huge", "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 items, got %d", len(got))
	}
	for _, d := range got {
		if d.Candidate.SVG != "" {
			t.Fatalf("%s: candidate svg should be empty, got %d bytes", d.TaskID, len(d.Candidate.SVG))
		}
	}
	if got[2].Candidate.State != "failed" || got[2].Official != nil {
		t.Fatalf("failed item = %+v", got[2])
	}
}

func TestDrawings_FinalEntriesAreCachedPendingOnesRetried(t *testing.T) {
	stub := &drawingStub{
		runs: []string{"done", "pending", "omitted"},
		items: map[string]upstreamDrawingItem{
			"done": doneItem("done", "<svg>a</svg>", "<svg>b</svg>"),
			"pending": {
				TaskID:    "pending",
				Candidate: upstreamDrawingSide{State: "done", ResponsePreview: preview("<svg>c</svg>")},
				Official:  &upstreamDrawingSide{State: "pending"},
			},
		},
	}
	c, now := newDrawingClient(t, stub)
	ids := []string{"done", "pending", "omitted"}

	if _, _, err := c.Drawings(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Drawings(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	if n := stub.calls.Load(); n != 1 {
		t.Fatalf("second call within TTL should be served from cache, upstream calls = %d", n)
	}

	*now = now.Add(pendingDrawingTTL + time.Second)
	if _, _, err := c.Drawings(context.Background(), ids); err != nil {
		t.Fatal(err)
	}
	last := stub.lastBatch()
	if strings.Join(last, ",") != "omitted,pending" {
		t.Fatalf("after the pending TTL only the non-final runs are asked again, got %v", last)
	}
}

func TestDrawings_SplitsIntoUpstreamBatches(t *testing.T) {
	stub := &drawingStub{items: map[string]upstreamDrawingItem{}}
	for i := range MaxDrawingIDs {
		id := fmt.Sprintf("r%02d", i)
		stub.runs = append(stub.runs, id)
		stub.items[id] = doneItem(id, "<svg/>", "<svg/>")
	}
	c, _ := newDrawingClient(t, stub)

	got, _, err := c.Drawings(context.Background(), stub.runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxDrawingIDs {
		t.Fatalf("want %d items, got %d", MaxDrawingIDs, len(got))
	}
	if len(stub.batches) != 2 || len(stub.batches[0]) != upstreamDrawingBatch {
		t.Fatalf("want batches of %d then the rest, got sizes %d", upstreamDrawingBatch, len(stub.batches))
	}
	if got[0].TaskID != "r00" || got[MaxDrawingIDs-1].TaskID != stub.runs[MaxDrawingIDs-1] {
		t.Fatal("items must keep request order")
	}

	if _, _, err := c.Drawings(context.Background(), append(stub.runs, "one-too-many")); err == nil {
		t.Fatal("more than MaxDrawingIDs ids must be rejected")
	}
}

func TestDrawings_UpstreamFailureServesWhatIsCached(t *testing.T) {
	stub := &drawingStub{
		runs: []string{"a", "b"},
		items: map[string]upstreamDrawingItem{
			"a": doneItem("a", "<svg>a</svg>", "<svg/>"),
			"b": doneItem("b", "<svg>b</svg>", "<svg/>"),
		},
	}
	c, _ := newDrawingClient(t, stub)
	if _, _, err := c.Drawings(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}

	stub.failDrawing.Store(true)
	got, complete, err := c.Drawings(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("an upstream failure must not fail the call: %v", err)
	}
	if complete {
		t.Fatal("a partial answer must report complete=false")
	}
	if len(got) != 1 || got[0].TaskID != "a" {
		t.Fatalf("want the cached run only, got %+v", got)
	}
}

func TestDrawingCache_EvictsDownToBudget(t *testing.T) {
	var dc drawingCache
	dc.init()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	big := "<svg>" + strings.Repeat("a", maxSVGBytes-20) + "</svg>"
	n := maxDrawingCacheBytes/len(big) + 10
	for i := range n {
		id := fmt.Sprintf("r%04d", i)
		dc.store([]string{id}, []upstreamDrawingItem{
			{TaskID: id, Candidate: upstreamDrawingSide{State: "done", ResponsePreview: preview(big)}},
		}, now.Add(time.Duration(i)*time.Second))
	}
	if dc.bytes > maxDrawingCacheBytes {
		t.Fatalf("cache holds %d bytes, budget %d", dc.bytes, maxDrawingCacheBytes)
	}
	if _, ok := dc.entries[fmt.Sprintf("r%04d", n-1)]; !ok {
		t.Fatal("the newest entry should survive eviction")
	}
}

func TestDrawingCache_CapsEntryCount(t *testing.T) {
	var dc drawingCache
	dc.init()
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for i := range maxDrawingEntries + 50 {
		dc.store([]string{fmt.Sprintf("r%05d", i)}, nil, now.Add(time.Duration(i)*time.Second))
	}
	if len(dc.entries) > maxDrawingEntries {
		t.Fatalf("cache holds %d entries, cap %d", len(dc.entries), maxDrawingEntries)
	}
}
