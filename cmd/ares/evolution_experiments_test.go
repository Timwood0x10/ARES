package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// writeExperimentLog seeds a log file for the handler tests.
func writeExperimentLog(t *testing.T, recs []evolution.ExperimentRecord) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	var sb strings.Builder
	for _, rec := range recs {
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(path, []byte(sb.String()), 0o600))
	return path
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &out), "body: %s", rr.Body.String())
	return out
}

// TestExperimentAPI_ListFilters: the query parameters reach the filter. limit=1 with two matches
// must still report matched=2 — the caller has to be able to tell "one of two" from "one".
func TestExperimentAPI_ListFilters(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	path := writeExperimentLog(t, []evolution.ExperimentRecord{
		{Generation: 3, CandidateID: "a", Outcome: evolution.ExperimentOutcomeAccepted, At: at},
		{Generation: 3, CandidateID: "b", Outcome: evolution.ExperimentOutcomeAccepted, At: at.Add(time.Minute)},
		{Generation: 4, CandidateID: "c", Outcome: evolution.ExperimentOutcomeRejected, At: at.Add(2 * time.Minute)},
	})
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	h.handleExperiments(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments?outcome=accepted&limit=1", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	body := decodeBody(t, rr)
	assert.Equal(t, "ok", body["status"])
	assert.Equal(t, float64(1), body["count"])
	assert.Equal(t, float64(2), body["matched"], "matched is the count before the limit")
	assert.Equal(t, false, body["truncated"])
}

// TestExperimentAPI_GenerationZeroIsARealFilter: ?generation=0 must select generation 0, not be
// read as "no filter". This is why the filter carries a pointer.
func TestExperimentAPI_GenerationZeroIsARealFilter(t *testing.T) {
	path := writeExperimentLog(t, []evolution.ExperimentRecord{
		{Generation: 0, CandidateID: "gen0"},
		{Generation: 1, CandidateID: "gen1"},
	})
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	h.handleExperiments(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments?generation=0", nil))

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, float64(1), decodeBody(t, rr)["matched"])
}

// TestExperimentAPI_RejectsUnknownOutcome: an unknown value is a 400 naming the parameter.
// Returning an empty page would look like "that never happened".
func TestExperimentAPI_RejectsUnknownOutcome(t *testing.T) {
	path := writeExperimentLog(t, []evolution.ExperimentRecord{{CandidateID: "a"}})
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	h.handleExperiments(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments?outcome=rolled_back", nil))

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, decodeBody(t, rr)["error"], "pending|accepted|rejected")
}

// TestExperimentAPI_ProposalDecisionChain is the audit question the release promises: given a
// proposal, what did the gates decide, and what happened after? The join is ProposalKey because
// ReplayKey cannot serve as one (the lifecycle does not hold the fitness weights).
func TestExperimentAPI_ProposalDecisionChain(t *testing.T) {
	proposal := evolution.ExperimentRecord{
		Generation: 2, CandidateID: "cand-1", Outcome: evolution.ExperimentOutcomePending,
		At: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
	}
	proposalKey := proposal.ReplayKey()
	postDeploy := &evolution.StrategyOutcome{StrategyID: "cand-1", Success: true}

	path := writeExperimentLog(t, []evolution.ExperimentRecord{
		proposal,
		{
			Generation: 2, CandidateID: "cand-1", Outcome: evolution.ExperimentOutcomeAccepted,
			ProposalKey: proposalKey, PostDeploy: postDeploy,
			At: time.Date(2026, 10, 20, 12, 5, 0, 0, time.UTC),
		},
	})
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/evolution/experiments/"+proposalKey, nil)
	h.routeExperimentChain(rr, req, nil)

	require.Equal(t, http.StatusOK, rr.Code)
	body := decodeBody(t, rr)
	require.NotNil(t, body["proposal"], "the proposal record must be found by ReplayKey")
	decisions, ok := body["decisions"].([]any)
	require.True(t, ok)
	assert.Len(t, decisions, 1)
	assert.NotNil(t, body["post_deploy"], "post-deploy is reported from the decision record")
}

// TestExperimentAPI_UnknownKeyIs404: a key that matches nothing is a missing resource, not an
// empty success.
func TestExperimentAPI_UnknownKeyIs404(t *testing.T) {
	path := writeExperimentLog(t, []evolution.ExperimentRecord{{CandidateID: "a"}})
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	h.routeExperimentChain(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments/deadbeef", nil), nil)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// TestExperimentAPI_NotConfiguredIs404: with no log path the surface is absent, which is a 404 —
// not a 503 that would page an operator about a feature they never enabled.
func TestExperimentAPI_NotConfiguredIs404(t *testing.T) {
	h := &actionHandler{}

	rr := httptest.NewRecorder()
	h.handleExperiments(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments", nil))

	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Nil(t, evolutionQueryFor(""), "empty path means no reader at all")
}

// TestExperimentAPI_RoutesAreReadOnly pins the auth level in the registry: these endpoints expose
// experiment history, and a future edit that drops them to authNone would be invisible in the
// handlers themselves.
func TestExperimentAPI_RoutesAreReadOnly(t *testing.T) {
	want := map[string]bool{
		"/api/evolution/experiments":                false,
		"/api/evolution/experiments/{proposal_key}": false,
	}
	for _, spec := range actionRoutes {
		if _, ok := want[spec.Path]; !ok {
			continue
		}
		want[spec.Path] = true
		assert.Equal(t, authRead, spec.Auth, "route %s must require read auth", spec.Path)
	}
	for path, found := range want {
		assert.True(t, found, "route %s is missing from the registry", path)
	}
}

// TestExperimentAPI_ChainFindsProposalBeyondTheFirstPage guards the reason the chain uses two
// targeted queries: the first implementation loaded one page (limit 1000) and filtered in memory,
// so a proposal older than the page was reported as "no such experiment". Here the proposal sits
// past the default page size and must still be found.
func TestExperimentAPI_ChainFindsProposalBeyondTheFirstPage(t *testing.T) {
	var recs []evolution.ExperimentRecord
	for i := 0; i < 150; i++ {
		recs = append(recs, evolution.ExperimentRecord{Generation: i, CandidateID: fmt.Sprintf("c%d", i)})
	}
	target := evolution.ExperimentRecord{Generation: 999, CandidateID: "target"}
	recs = append(recs, target)
	recs = append(recs, evolution.ExperimentRecord{
		Generation: 999, CandidateID: "target",
		ProposalKey: target.ReplayKey(),
		Outcome:     evolution.ExperimentOutcomeAccepted,
	})

	path := writeExperimentLog(t, recs)
	h := &actionHandler{experiments: evolution.NewExperimentQuery(path)}

	rr := httptest.NewRecorder()
	h.routeExperimentChain(rr, httptest.NewRequest(http.MethodGet, "/api/evolution/experiments/"+target.ReplayKey(), nil), nil)

	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())
	body := decodeBody(t, rr)
	assert.NotNil(t, body["proposal"])
	assert.Len(t, body["decisions"], 1)
}
