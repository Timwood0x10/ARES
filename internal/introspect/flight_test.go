package introspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	flight "github.com/Timwood0x10/ares/internal/runtime/observability/flight"
)

// fakeFlight is a fixed FlightProvider for handler tests.
type fakeFlight struct {
	timeline   []flight.TimelineEvent
	summary    flight.TimelineSummary
	graph      string
	decisions  []flight.Decision
	diagnostic []flight.DiagnosticRecord
	dist       flight.CategoryDistribution
	genealogy  string
}

func (f *fakeFlight) TimelineEvents(agentID string) []flight.TimelineEvent {
	if agentID != "" {
		var out []flight.TimelineEvent
		for _, e := range f.timeline {
			if e.AgentID == agentID {
				out = append(out, e)
			}
		}
		return out
	}
	return f.timeline
}

func (f *fakeFlight) TimelineSummary(agentID string) flight.TimelineSummary { return f.summary }
func (f *fakeFlight) GraphMermaid() string                                  { return f.graph }
func (f *fakeFlight) Decisions(agentID string) []flight.Decision            { return f.decisions }
func (f *fakeFlight) Diagnostics(agentID string) ([]flight.DiagnosticRecord, flight.CategoryDistribution) {
	return f.diagnostic, f.dist
}
func (f *fakeFlight) GenealogyMermaid() string { return f.genealogy }

func TestControlServer_Flight(t *testing.T) {
	now := time.Now()
	s := NewControlServer(nil, WithFlight(&fakeFlight{
		timeline: []flight.TimelineEvent{
			{ID: "e1", AgentID: "a1", Type: flight.EventToolCall, StartAt: now},
			{ID: "e2", AgentID: "a2", Type: flight.EventLLMCall, StartAt: now},
		},
		summary:    flight.TimelineSummary{EventCount: 2},
		graph:      "graph LR\n    a[ok]",
		decisions:  []flight.Decision{{ID: "d1", AgentID: "a1", Selected: "t1"}},
		diagnostic: []flight.DiagnosticRecord{{ID: "r1", AgentID: "a1", Category: flight.DiagToolTimeout}},
		dist:       flight.CategoryDistribution{Total: 1},
		genealogy:  "graph LR\n    empty[No agents]",
	}))

	// timeline
	rec := doGet(t, s, "/api/flight/timeline")
	if rec.Code != http.StatusOK {
		t.Fatalf("timeline status %d", rec.Code)
	}
	var evts []flight.TimelineEvent
	if err := json.Unmarshal(rec.Body.Bytes(), &evts); err != nil {
		t.Fatalf("timeline decode: %v", err)
	}
	if len(evts) != 2 {
		t.Fatalf("timeline length %d, want 2", len(evts))
	}

	// timeline filtered by agent
	rec = doGet(t, s, "/api/flight/timeline?agent_id=a1")
	_ = json.Unmarshal(rec.Body.Bytes(), &evts)
	if len(evts) != 1 || evts[0].AgentID != "a1" {
		t.Fatalf("filtered timeline %+v", evts)
	}

	// summary
	rec = doGet(t, s, "/api/flight/summary")
	if !strings.Contains(rec.Body.String(), `"event_count":2`) {
		t.Fatalf("summary: %s", rec.Body.String())
	}

	// graph
	rec = doGet(t, s, "/api/flight/graph")
	if !strings.Contains(rec.Body.String(), "graph LR") {
		t.Fatalf("graph: %s", rec.Body.String())
	}

	// decisions
	rec = doGet(t, s, "/api/flight/decisions")
	if !strings.Contains(rec.Body.String(), `"id":"d1"`) {
		t.Fatalf("decisions: %s", rec.Body.String())
	}

	// diagnostics (records + distribution)
	rec = doGet(t, s, "/api/flight/diagnostics")
	var diag map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &diag); err != nil {
		t.Fatalf("diagnostics decode: %v", err)
	}
	if recs, ok := diag["records"].([]any); !ok || len(recs) != 1 {
		t.Fatalf("diagnostics records: %v", diag["records"])
	}
	if dist, ok := diag["distribution"].(map[string]any); !ok || dist["total"].(float64) != 1 {
		t.Fatalf("diagnostics distribution: %v", diag["distribution"])
	}

	// genealogy
	rec = doGet(t, s, "/api/flight/genealogy")
	if !strings.Contains(rec.Body.String(), "graph LR") {
		t.Fatalf("genealogy: %s", rec.Body.String())
	}
}

func TestControlServer_FlightUnconfigured(t *testing.T) {
	// No flight provider → endpoints return empty data, not an error.
	s := NewControlServer(nil)
	if rec := doGet(t, s, "/api/flight/timeline"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("unconfigured timeline: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doGet(t, s, "/api/flight/summary"); rec.Code != http.StatusOK {
		t.Fatalf("unconfigured summary: %d", rec.Code)
	}
	if rec := doGet(t, s, "/api/flight/graph"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No data") {
		t.Fatalf("unconfigured graph: %d %s", rec.Code, rec.Body.String())
	}
}

func TestFlightRecorderAdapter_Nil(t *testing.T) {
	if got := NewFlightRecorderAdapter(nil); got != nil {
		t.Fatalf("nil recorder should yield nil provider, got %+v", got)
	}
}

// TestFlightRecorderAdapter_TimelineSummaryFiltersByAgent is the #54
// regression: flightRecorderAdapter.TimelineSummary discarded its agentID
// parameter and always returned the global summary, so the per-agent filter
// on /api/flight/summary never worked. The adapter must route a non-empty
// agentID to SummaryByAgent.
func TestFlightRecorderAdapter_TimelineSummaryFiltersByAgent(t *testing.T) {
	fr := flight.NewFlightRecorder(flight.FlightRecorderConfig{})
	tl := fr.Timeline()

	base := time.Now()
	// Agent "slow" has a 2s tool call; agent "fast" a 1ms one.
	tl.Add(flight.TimelineEvent{
		ID: "s1", AgentID: "slow", Type: flight.EventToolCall, Name: "tool",
		StartAt: base,
	})
	tl.Add(flight.TimelineEvent{
		ID: "s1", ParentID: "s1", AgentID: "slow", Type: flight.EventToolResult, Name: "tool",
		StartAt: base.Add(2 * time.Second),
	})
	tl.Add(flight.TimelineEvent{
		ID: "f1", AgentID: "fast", Type: flight.EventToolCall, Name: "tool",
		StartAt: base,
	})
	tl.Add(flight.TimelineEvent{
		ID: "f1", ParentID: "f1", AgentID: "fast", Type: flight.EventToolResult, Name: "tool",
		StartAt: base.Add(time.Millisecond),
	})

	provider := NewFlightRecorderAdapter(fr)

	all := provider.TimelineSummary("")
	if all.EventCount != 4 {
		t.Fatalf("global summary EventCount = %d, want 4", all.EventCount)
	}

	fast := provider.TimelineSummary("fast")
	if fast.EventCount != 2 {
		t.Fatalf("per-agent summary EventCount = %d, want 2 (filter must be applied)", fast.EventCount)
	}
	if fast.ToolDuration >= 2*time.Second {
		t.Fatalf("per-agent summary ToolDuration = %v, must reflect only fast's 1ms call — the agent filter was dropped", fast.ToolDuration)
	}
	if fast.ToolDuration < time.Millisecond {
		t.Fatalf("per-agent summary ToolDuration = %v, want fast's 1ms", fast.ToolDuration)
	}

	slow := provider.TimelineSummary("slow")
	if slow.ToolDuration < 2*time.Second {
		t.Fatalf("slow summary ToolDuration = %v, want ≥2s", slow.ToolDuration)
	}
}

// TestFlightSummaryEndpointPassesAgentID verifies the HTTP layer threads the
// agent_id query parameter into the provider.
func TestFlightSummaryEndpointPassesAgentID(t *testing.T) {
	var gotAgentID string
	ff := &fakeFlight{summary: flight.TimelineSummary{EventCount: 2}}
	captured := &capturingFlight{inner: ff, onSummary: func(id string) { gotAgentID = id }}
	s := NewControlServer(nil, WithFlight(captured))

	rec := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/flight/summary?agent_id=agent-42", nil)
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if gotAgentID != "agent-42" {
		t.Fatalf("provider received agent_id %q, want agent-42", gotAgentID)
	}
}

// capturingFlight wraps a FlightProvider and records the agentID handed to
// TimelineSummary.
type capturingFlight struct {
	inner     *fakeFlight
	onSummary func(agentID string)
}

func (c *capturingFlight) TimelineEvents(agentID string) []flight.TimelineEvent {
	return c.inner.TimelineEvents(agentID)
}
func (c *capturingFlight) TimelineSummary(agentID string) flight.TimelineSummary {
	c.onSummary(agentID)
	return c.inner.TimelineSummary(agentID)
}
func (c *capturingFlight) GraphMermaid() string { return c.inner.GraphMermaid() }
func (c *capturingFlight) Decisions(agentID string) []flight.Decision {
	return c.inner.Decisions(agentID)
}
func (c *capturingFlight) Diagnostics(agentID string) ([]flight.DiagnosticRecord, flight.CategoryDistribution) {
	return c.inner.Diagnostics(agentID)
}
func (c *capturingFlight) GenealogyMermaid() string { return c.inner.GenealogyMermaid() }
