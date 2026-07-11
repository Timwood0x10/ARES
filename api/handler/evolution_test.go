// Package handler — tests for the runtime evolution HTTP handlers.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// ---- Mocks ----

type mockRuntimeEvolution struct {
	cycleResult     *core.RuntimeCycleResult
	cycleErr        error
	statusResult    *core.RuntimeEvolutionStatus
	statusErr       error
	proposeErr      error
	evidenceResults []core.Evidence
	evidenceErr     error
	registerErr     error
	registeredComps []string
}

func (m *mockRuntimeEvolution) RunCycle(_ context.Context) (*core.RuntimeCycleResult, error) {
	return m.cycleResult, m.cycleErr
}

func (m *mockRuntimeEvolution) Status() (*core.RuntimeEvolutionStatus, error) {
	return m.statusResult, m.statusErr
}

func (m *mockRuntimeEvolution) Propose(_ context.Context, _ core.RuntimeProposal) error {
	return m.proposeErr
}

func (m *mockRuntimeEvolution) QueryEvidence(_ context.Context, _ core.EvidenceFilter) ([]core.Evidence, error) {
	return m.evidenceResults, m.evidenceErr
}

func (m *mockRuntimeEvolution) RegisterComponent(_ context.Context, comp core.RuntimeComponent) error {
	m.registeredComps = append(m.registeredComps, comp.Name())
	return m.registerErr
}

// ---- Helpers ----

func newRuntimeEvolutionHandler(svc core.RuntimeEvolution) (*RuntimeEvolutionHandler, *httptest.Server) {
	h := NewRuntimeEvolutionHandler(svc)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/evolution/runtime/cycle", h.HandleCycle)
	mux.HandleFunc("GET /api/v1/evolution/runtime/status", h.HandleRuntimeStatus)
	mux.HandleFunc("POST /api/v1/evolution/runtime/propose", h.HandlePropose)
	mux.HandleFunc("GET /api/v1/evolution/runtime/evidence", h.HandleEvidenceQuery)
	mux.HandleFunc("POST /api/v1/evolution/runtime/components", h.HandleRegisterComponent)
	return h, httptest.NewServer(mux)
}

// ---- Tests ----

func TestRuntimeEvolutionHandler_Cycle_Success(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{
		cycleResult: &core.RuntimeCycleResult{
			GenomesEvaluated: 3,
			PatchesApplied:   2,
		},
	}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/cycle", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cycle: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result core.RuntimeCycleResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.GenomesEvaluated != 3 || result.PatchesApplied != 2 {
		t.Fatalf("unexpected cycle result: %+v", result)
	}
}

func TestRuntimeEvolutionHandler_Cycle_Failure(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{cycleErr: context.DeadlineExceeded}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/cycle", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cycle: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestRuntimeEvolutionHandler_Status_Success(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{
		statusResult: &core.RuntimeEvolutionStatus{
			PatchesApplied:  5,
			EvidenceEntries: 10,
		},
	}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/evolution/runtime/status")
	if err != nil {
		t.Fatalf("GET status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var status core.RuntimeEvolutionStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if status.PatchesApplied != 5 || status.EvidenceEntries != 10 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestRuntimeEvolutionHandler_Propose_Success(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	body := strings.NewReader(`{"source":"human","text":"increase budget","priority":7}`)
	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/propose", "application/json", body)
	if err != nil {
		t.Fatalf("POST propose: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["status"] != "submitted" {
		t.Fatalf("expected status=submitted, got %q", result["status"])
	}
}

func TestRuntimeEvolutionHandler_Propose_DefaultsApplied(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	// Empty source and priority should be defaulted.
	body := strings.NewReader(`{"text":"my proposal"}`)
	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/propose", "application/json", body)
	if err != nil {
		t.Fatalf("POST propose: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRuntimeEvolutionHandler_Propose_Failure(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{proposeErr: context.Canceled}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	body := strings.NewReader(`{"text":"fail this"}`)
	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/propose", "application/json", body)
	if err != nil {
		t.Fatalf("POST propose: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRuntimeEvolutionHandler_EvidenceQuery_Success(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{
		evidenceResults: []core.Evidence{
			{ID: "ev-1", Source: "arena", Kind: core.EvidenceFailure, Timestamp: time.Now()},
			{ID: "ev-2", Source: "flight", Kind: core.EvidenceExecutionTrace, Timestamp: time.Now()},
		},
	}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/evolution/runtime/evidence?source=arena&kind=failure&limit=10")
	if err != nil {
		t.Fatalf("GET evidence: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var results []core.Evidence
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 evidence entries, got %d", len(results))
	}
}

func TestRuntimeEvolutionHandler_EvidenceQuery_Empty(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{evidenceResults: []core.Evidence{}}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/evolution/runtime/evidence")
	if err != nil {
		t.Fatalf("GET evidence: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var results []core.Evidence
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 evidence entries, got %d", len(results))
	}
}

func TestRuntimeEvolutionHandler_EvidenceQuery_Failure(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{evidenceErr: context.Canceled}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/v1/evolution/runtime/evidence")
	if err != nil {
		t.Fatalf("GET evidence: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestRuntimeEvolutionHandler_RegisterComponent_RequiresName(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	// No name query parameter → 400.
	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/components", "application/json", nil)
	if err != nil {
		t.Fatalf("POST components: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestRuntimeEvolutionHandler_RegisterComponent_PlaceholderResponse(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	// With name → placeholder 200 (REST registration not yet supported).
	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/components?name=my-component", "application/json", nil)
	if err != nil {
		t.Fatalf("POST components: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(result["status"], "SDK") {
		t.Fatalf("expected placeholder status mentioning SDK, got %q", result["status"])
	}
}

func TestRuntimeEvolutionHandler_ServiceUnavailable(t *testing.T) {
	t.Parallel()

	h := NewRuntimeEvolutionHandler(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/evolution/runtime/cycle", h.HandleCycle)
	mux.HandleFunc("GET /api/v1/evolution/runtime/status", h.HandleRuntimeStatus)
	mux.HandleFunc("POST /api/v1/evolution/runtime/propose", h.HandlePropose)
	mux.HandleFunc("GET /api/v1/evolution/runtime/evidence", h.HandleEvidenceQuery)
	mux.HandleFunc("POST /api/v1/evolution/runtime/components", h.HandleRegisterComponent)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tests := []struct {
		name string
		fn   func() (*http.Response, error)
	}{
		{
			name: "cycle",
			fn: func() (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/evolution/runtime/cycle", "application/json", nil)
			},
		},
		{
			name: "status",
			fn:   func() (*http.Response, error) { return http.Get(srv.URL + "/api/v1/evolution/runtime/status") },
		},
		{
			name: "propose",
			fn: func() (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/evolution/runtime/propose", "application/json", strings.NewReader(`{}`))
			},
		},
		{
			name: "evidence",
			fn:   func() (*http.Response, error) { return http.Get(srv.URL + "/api/v1/evolution/runtime/evidence") },
		},
		{
			name: "components",
			fn: func() (*http.Response, error) {
				return http.Post(srv.URL+"/api/v1/evolution/runtime/components?name=x", "application/json", nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := tt.fn()
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("expected 503, got %d", resp.StatusCode)
			}
		})
	}
}

func TestRuntimeEvolutionHandler_Propose_InvalidJSON(t *testing.T) {
	t.Parallel()

	mock := &mockRuntimeEvolution{}
	_, srv := newRuntimeEvolutionHandler(mock)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/v1/evolution/runtime/propose", "application/json", strBody("{not json"))
	if err != nil {
		t.Fatalf("POST propose: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}
