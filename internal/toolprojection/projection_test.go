package toolprojection

import (
	"context"
	"testing"
	"time"

	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
)

// completed builds an EventToolCallCompleted event with the C1 unified contract.
func completed(stream, tool, shape string, round, seq int, ok bool) *ares_events.Event {
	return &ares_events.Event{
		Type:     ares_events.EventToolCallCompleted,
		StreamID: stream,
		Payload: map[string]any{
			ares_events.EventKeyToolName:   tool,
			ares_events.EventKeyArgShape:   shape,
			ares_events.EventKeyRound:      round,
			ares_events.EventKeySeq:        seq,
			ares_events.EventKeySuccess:    ok,
			ares_events.EventKeyError:      "",
			ares_events.EventKeyToolCallID: "c-" + tool,
		},
	}
}

// TestProject_AggregatesSameToolStepID is the C2 core aggregation invariant:
// the projected node count equals the number of distinct toolStepID values, and
// repeated invocations of the same toolStepID collapse into one node with the
// correct count/success_rate.
func TestProject_AggregatesSameToolStepID(t *testing.T) {
	events := []*ares_events.Event{
		completed("s1", "search", "q", 0, 0, true),
		completed("s1", "search", "q", 1, 0, true),
		completed("s1", "search", "q", 2, 0, false), // 2/3 success
		completed("s1", "calc", "expr", 0, 1, true),
	}
	p := Project(events, Options{})

	if p.TotalEvents != 4 {
		t.Fatalf("TotalEvents = %d, want 4", p.TotalEvents)
	}
	// 2 distinct toolStepIDs: search#q and calc#expr.
	if len(p.Steps) != 2 {
		t.Fatalf("distinct steps = %d, want 2; got %+v", len(p.Steps), p.Steps)
	}
	var search *ToolStep
	for _, s := range p.Steps {
		if s.ToolStepID == ToolStepID("search", "q") {
			search = s
		}
	}
	if search == nil {
		t.Fatalf("search#q step missing; got %+v", p.Steps)
	}
	if search.Count != 3 {
		t.Fatalf("search count = %d, want 3", search.Count)
	}
	if search.SuccessCount != 2 {
		t.Fatalf("search success = %d, want 2", search.SuccessCount)
	}
	if search.SuccessRate != 2.0/3.0 {
		t.Fatalf("search success_rate = %f, want %f", search.SuccessRate, 2.0/3.0)
	}
}

// TestProject_MinSamplesThreshold filters out steps below the pattern-aggregation
// threshold (plan §7), preventing the projection from fragmenting on rare calls.
func TestProject_MinSamplesThreshold(t *testing.T) {
	events := []*ares_events.Event{
		completed("s1", "search", "q", 0, 0, true),
		completed("s1", "search", "q", 1, 0, true),
		completed("s1", "calc", "expr", 0, 1, true),
	}
	p := Project(events, Options{MinSamples: 2})

	if len(p.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (calc dropped below MinSamples=2); got %+v", len(p.Steps), p.Steps)
	}
	if p.Steps[0].ToolStepID != ToolStepID("search", "q") {
		t.Fatalf("surviving step = %s, want search#q", p.Steps[0].ToolStepID)
	}
}

// TestProject_EdgesRespectSessionOrder asserts the execution-order edges chain
// same-session calls in (round, seq) order and never across sessions.
func TestProject_EdgesRespectSessionOrder(t *testing.T) {
	events := []*ares_events.Event{
		completed("s1", "search", "q", 0, 0, true),
		completed("s1", "calc", "expr", 0, 1, true),
		completed("s2", "read", "path", 0, 0, true),
	}
	p := Project(events, Options{})

	haveEdge := func(from, to string) bool {
		for _, e := range p.Edges {
			if e.From == from && e.To == to {
				return true
			}
		}
		return false
	}
	if !haveEdge(ToolStepID("search", "q"), ToolStepID("calc", "expr")) {
		t.Fatalf("missing same-session search->calc edge; edges=%+v", p.Edges)
	}
	// s2 is a separate session: no cross-session edge into/out of it.
	if haveEdge(ToolStepID("calc", "expr"), ToolStepID("read", "path")) ||
		haveEdge(ToolStepID("read", "path"), ToolStepID("search", "q")) {
		t.Fatalf("cross-session edge must not exist; edges=%+v", p.Edges)
	}
}

// TestToolStepIDCollapsesValuesOnSameShape guards the §11.0 invariant at the
// projection entry: same tool + same (already-normalized) arg shape yields the
// same toolStepID, so repeated calls aggregate. The arg-shape NORMALIZATION —
// "q,k" === "k,q" — is done by ares_events.ToolArgShape at emit time (covered by
// tool_events_test.go); this projection aggregates whatever shape it is handed.
func TestToolStepIDCollapsesValuesOnSameShape(t *testing.T) {
	a := completed("s1", "search", "q,k", 0, 0, true)
	b := completed("s1", "search", "q,k", 0, 1, true)
	p := Project([]*ares_events.Event{a, b}, Options{})
	if len(p.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 for identical shapes; got %+v", len(p.Steps), p.Steps)
	}
	if p.Steps[0].Count != 2 {
		t.Fatalf("search count = %d, want 2", p.Steps[0].Count)
	}
}

// recordingSource captures the ReadOptions the projection passed down.
type recordingSource struct {
	got    ares_events.ReadOptions
	events []*ares_events.Event
}

func (r *recordingSource) ReadAll(_ context.Context, opts ares_events.ReadOptions) ([]*ares_events.Event, error) {
	r.got = opts
	return r.events, nil
}

// TestProjectFromSource_ForwardsSinceWindow guards the incremental-read contract
// the periodic production trigger depends on: without Since reaching the store,
// every run would re-read the entire event log and re-emit fitness records for
// tool calls already projected, letting stale behavior keep voting forever.
func TestProjectFromSource_ForwardsSinceWindow(t *testing.T) {
	since := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	src := &recordingSource{events: []*ares_events.Event{
		completed("s1", "search", "q", 0, 0, true),
	}}

	p, err := ProjectFromSource(context.Background(), src, Options{Since: since})
	if err != nil {
		t.Fatalf("ProjectFromSource: %v", err)
	}
	if !src.got.Since.Equal(since) {
		t.Errorf("ReadOptions.Since = %s, want %s — the window never reached the event store",
			src.got.Since, since)
	}
	if p.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1", p.TotalEvents)
	}
}

// TestProjectFromSource_ZeroSinceReadsWholeLog keeps the on-demand audit read
// working: an unset window must not be turned into a bogus filter.
func TestProjectFromSource_ZeroSinceReadsWholeLog(t *testing.T) {
	src := &recordingSource{}
	if _, err := ProjectFromSource(context.Background(), src, Options{}); err != nil {
		t.Fatalf("ProjectFromSource: %v", err)
	}
	if !src.got.Since.IsZero() {
		t.Errorf("ReadOptions.Since = %s, want zero (read everything)", src.got.Since)
	}
}
