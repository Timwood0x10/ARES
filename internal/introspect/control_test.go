package introspect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAgentSource is a fixed AgentSource for handler tests.
type fakeAgentSource struct{ views []AgentView }

func (f *fakeAgentSource) ListAgents() []AgentView { return f.views }

// fakeTrajectory is a fixed EvolutionTrajectoryProvider.
type fakeTrajectory struct{ views []map[string]any }

func (f *fakeTrajectory) EvolutionTrajectory() []map[string]any { return f.views }

// fakeFeedback is a recording EvolutionFeedbackSink.
type fakeFeedback struct {
	last *EvolutionFeedback
}

func (f *fakeFeedback) SubmitFeedback(fb EvolutionFeedback) error {
	f.last = &fb
	return nil
}

// fakeSpans is a fixed ObservabilitySpansProvider.
type fakeSpans struct{ views []map[string]any }

func (f *fakeSpans) Spans() []map[string]any { return f.views }

// doGet performs a GET against the server and returns the recorder.
func doGet(t *testing.T, s *ControlServer, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestControlServer_ListAgents(t *testing.T) {
	s := NewControlServer(&fakeAgentSource{views: []AgentView{
		{ID: "a1", Name: "a1", Status: "ready"},
		{ID: "a2", Name: "a2", Role: "coder", Status: "working"},
	}})
	rec := doGet(t, s, "/api/agents")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out []AgentView
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 || out[1].Role != "coder" {
		t.Fatalf("unexpected agents %+v", out)
	}
}

func TestControlServer_GetAgent(t *testing.T) {
	s := NewControlServer(&fakeAgentSource{views: []AgentView{
		{ID: "a1", Status: "ready"},
	}})
	if rec := doGet(t, s, "/api/agents/a1"); rec.Code != http.StatusOK {
		t.Fatalf("existing agent: status %d", rec.Code)
	}
	if rec := doGet(t, s, "/api/agents/missing"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing agent: status %d", rec.Code)
	}
}

func TestControlServer_Health(t *testing.T) {
	// No intel configured → level unknown, no panic.
	s := NewControlServer(nil)
	rec := doGet(t, s, "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown") {
		t.Fatalf("expected unknown level, got %s", rec.Body.String())
	}

	// With intel fed by errors → degraded/unhealthy + anomaly count.
	intel := NewEngine(nil)
	for i := 0; i < 30; i++ {
		intel.ObserveAgentEvent("a1", "error", 0, true)
	}
	s2 := NewControlServer(nil, WithIntel(intel))
	rec = doGet(t, s2, "/api/health")
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	agents, _ := body["agents"].(float64)
	if agents < 1 {
		t.Fatalf("expected anomaly count ≥1, got %v", body["agents"])
	}
}

func TestControlServer_Unconfigured(t *testing.T) {
	// Agent source nil → 503 for agent endpoints; unknown config → 404.
	s := NewControlServer(nil)
	if rec := doGet(t, s, "/api/agents"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil agent source: status %d", rec.Code)
	}
	if rec := doGet(t, s, "/api/runtime/config"); rec.Code != http.StatusNotFound {
		t.Fatalf("nil config source: status %d", rec.Code)
	}
	if rec := doGet(t, s, "/api/unknown"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: status %d", rec.Code)
	}
}

func TestControlServer_RuntimeConfig(t *testing.T) {
	s := NewControlServer(nil, WithRuntimeConfig(func() (any, []map[string]any) {
		return map[string]any{"model": "m"}, []map[string]any{{"message": "ok"}}
	}))
	rec := doGet(t, s, "/api/runtime/config")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"model":"m"`) {
		t.Fatalf("config payload missing, got %s", rec.Body.String())
	}
}

func TestControlServer_Observability(t *testing.T) {
	s := NewControlServer(nil,
		WithEvolution(&fakeTrajectory{views: []map[string]any{{"generation": 1}}}, &fakeFeedback{}),
		WithObservability(&fakeSpans{views: []map[string]any{{"id": "t1"}}}),
	)
	rec := doGet(t, s, "/api/evolution/trajectory")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"generation":1`) {
		t.Fatalf("trajectory: %d %s", rec.Code, rec.Body.String())
	}
	rec = doGet(t, s, "/api/observability/spans")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"t1"`) {
		t.Fatalf("spans: %d %s", rec.Code, rec.Body.String())
	}

	// POST feedback records into the sink.
	req := httptest.NewRequest(http.MethodPost, "/api/evolution/feedback",
		strings.NewReader(`{"candidate_id":"c1","rating":4,"approved":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("feedback should return 405 on read-only plane, got %d", rec2.Code)
	}

	// Unconfigured observability → 404.
	s3 := NewControlServer(nil)
	if rec := doGet(t, s3, "/api/observability/spans"); rec.Code != http.StatusNotFound {
		t.Fatalf("unconfigured spans: status %d", rec.Code)
	}
}
