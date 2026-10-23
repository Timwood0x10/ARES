// Package main: read-only audit surface for the evolution experiment log.
//
// The endpoints answer the question the GA could not answer about itself: "this generation
// proposed something — what did the gates decide, and what happened after promotion?" The log is
// append-only, so the answers are read-only by construction; there is nothing here that could
// rewrite an experiment's history.
package main

import (
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	ares_security "github.com/Timwood0x10/ares/internal/ares_security"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// experimentsAvailable gates the audit routes.
//
// Off is a configuration state, not a failure: with evolution.experiment_log_path unset the
// routes do not participate in dispatch, so a caller sees 404 (unknown endpoint) rather than 503
// (broken endpoint). That distinction matters — an operator reading dashboards should not be
// paged for a feature they never turned on.
func experimentsAvailable(h *actionHandler) bool { return h.experiments != nil }

// evolutionQueryFor returns a reader for the configured log, or nil when the feature is off.
func evolutionQueryFor(path string) *evolution.ExperimentQuery {
	if path == "" {
		return nil
	}
	return evolution.NewExperimentQuery(path)
}

// routeExperiments serves GET /api/evolution/experiments.
func (h *actionHandler) routeExperiments(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	h.handleExperiments(w, r)
}

// routeExperimentChain serves GET /api/evolution/experiments/{proposal_key}.
func (h *actionHandler) routeExperimentChain(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	key := strings.TrimPrefix(r.URL.Path, "/api/evolution/experiments/")
	key = strings.TrimSuffix(key, "/")
	h.handleExperimentChain(w, r, key)
}

// handleExperiments parses the filters and returns matching records.
//
// Every malformed parameter is a 400 naming the parameter: silently ignoring it would answer a
// different question than the one asked, which is worse than refusing to answer.
func (h *actionHandler) handleExperiments(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if h.experiments == nil {
		writeJSONError(w, http.StatusNotFound, "experiment log not configured")
		return
	}

	filter, errMsg := parseExperimentFilter(r)
	if errMsg != "" {
		writeJSONError(w, http.StatusBadRequest, errMsg)
		return
	}

	result, err := h.experiments.ListFiltered(r.Context(), filter)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Configured but nothing sealed yet: a fresh deployment running its first
			// generation. "No records" is the honest answer; a 500 would suggest breakage.
			writeJSONError(w, http.StatusNotFound, "experiment log has no records yet")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"status":      "ok",
		"experiments": result.Records,
		"count":       len(result.Records),
		"matched":     result.Matched,
		"scanned":     result.Scanned,
		// Truncated is surfaced on purpose: an empty-looking page and "the scan cap stopped
		// before the end" must not look the same to a caller.
		"truncated": result.Truncated,
	})
}

// handleExperimentChain returns one proposal and the decision records that decided it.
//
// The join is ProposalKey, not ReplayKey: the decision is sealed by the lifecycle, which does not
// have the fitness weights that ReplayKey folds in, so it carries the proposal's key explicitly.
// Recomputing would name a different experiment the moment the defaults change.
func (h *actionHandler) handleExperimentChain(w http.ResponseWriter, r *http.Request, key string) {
	w.Header().Set("Content-Type", "application/json")
	if h.experiments == nil {
		writeJSONError(w, http.StatusNotFound, "experiment log not configured")
		return
	}
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "proposal_key is required")
		return
	}

	// Two targeted queries rather than one page plus in-memory filtering: on a long-lived log the
	// proposal can sit outside any page limit, and "not in the first 1000 records" would then be
	// reported as "no such experiment".
	proposalRes, err := h.experiments.ListFiltered(r.Context(), evolution.ExperimentFilter{
		ReplayKey: key, Limit: 1,
	})
	if err != nil {
		h.writeExperimentQueryError(w, err)
		return
	}
	decisionRes, err := h.experiments.ListFiltered(r.Context(), evolution.ExperimentFilter{
		ProposalKey: key, Limit: evolution.MaxExperimentQueryLimit,
	})
	if err != nil {
		h.writeExperimentQueryError(w, err)
		return
	}

	var proposal *evolution.ExperimentRecord
	if len(proposalRes.Records) > 0 {
		proposal = &proposalRes.Records[0]
	}
	decisions := decisionRes.Records
	if proposal == nil && len(decisions) == 0 {
		writeJSONError(w, http.StatusNotFound, "no experiment matches that proposal key")
		return
	}

	// PostDeploy lives on the decision record (it is observed after the verdict), so it is
	// reported there rather than duplicated onto the proposal.
	var postDeploy *evolution.StrategyOutcome
	for _, d := range decisions {
		if d.PostDeploy != nil {
			postDeploy = d.PostDeploy
			break
		}
	}

	writeJSON(w, map[string]any{
		"status":       "ok",
		"proposal_key": key,
		"proposal":     proposal,
		"decisions":    decisions,
		"post_deploy":  postDeploy,
		"truncated":    proposalRes.Truncated || decisionRes.Truncated,
	})
}

// writeExperimentQueryError maps a read failure onto the honest status: a configured log with no
// records yet is a 404, anything else is a 500 that says what broke.
func (h *actionHandler) writeExperimentQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, fs.ErrNotExist) {
		writeJSONError(w, http.StatusNotFound, "experiment log has no records yet")
		return
	}
	writeJSONError(w, http.StatusInternalServerError, err.Error())
}

// parseExperimentFilter validates the query string. It returns an error message rather than an
// error so the handler can answer 400 without wrapping: every branch here is a caller mistake.
func parseExperimentFilter(r *http.Request) (evolution.ExperimentFilter, string) {
	var filter evolution.ExperimentFilter
	q := r.URL.Query()

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return filter, "limit must be a non-negative integer"
		}
		filter.Limit = n
	}
	if v := q.Get("generation"); v != "" {
		// Pointer so generation 0 is expressible; a plain int would make "generation 0" and
		// "any generation" the same request.
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return filter, "generation must be a non-negative integer"
		}
		filter.Generation = &n
	}
	if v := q.Get("outcome"); v != "" {
		switch evolution.ExperimentOutcome(v) {
		case evolution.ExperimentOutcomePending, evolution.ExperimentOutcomeAccepted,
			evolution.ExperimentOutcomeRejected:
			filter.Outcome = evolution.ExperimentOutcome(v)
		default:
			// The vocabulary is exactly what ExperimentRecord seals. Listing a state the
			// writer never produces would let a caller filter for "rolled_back" and get an
			// empty page that looks like "none happened".
			return filter, "outcome must be one of pending|accepted|rejected"
		}
	}
	filter.CandidateID = q.Get("candidate")
	filter.ProposalKey = q.Get("proposal_key")
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return filter, "since must be an RFC3339 timestamp"
		}
		filter.Since = t
	}
	return filter, ""
}

// writeJSONError is the shared error shape for this surface: a status field keeps it identical to
// the success responses, so a client parses one envelope rather than two.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.WriteHeader(status)
	writeJSON(w, map[string]any{"status": "error", "error": msg})
}
