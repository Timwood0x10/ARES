package evolution

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Defaults for ExperimentQuery. They are constants rather than config keys because they bound a
// READ, not a behaviour: an operator who needs a larger window can page by Generation/Since
// instead of raising a number nobody remembers to lower again.
const (
	// DefaultExperimentQueryLimit is how many records a query returns when the caller does not
	// say. Small enough to render, large enough to answer "what happened recently".
	DefaultExperimentQueryLimit = 100
	// MaxExperimentQueryLimit caps a single response. A caller asking for more is asking to
	// page, not to stream the log through an API.
	MaxExperimentQueryLimit = 1000
	// maxExperimentQueryScan bounds how many LINES one query reads. It exists because the file
	// grows forever by design (append-only): without a cap, one query after a long-lived
	// deployment reads the whole history, and the response time depends on uptime.
	maxExperimentQueryScan = 10000
)

// ExperimentFilter selects records from the append-only log.
//
// Zero values mean "no constraint" for every field EXCEPT Generation, which is a pointer: 0 is a
// real generation (the initial population), so an int would make "generation 0" and "any
// generation" indistinguishable.
type ExperimentFilter struct {
	// Limit is the maximum number of records returned. 0 means DefaultExperimentQueryLimit;
	// values above MaxExperimentQueryLimit are clamped, not rejected — the caller still gets an
	// answer, it just gets the largest one the API is willing to send.
	Limit int
	// Generation restricts to one population generation. Nil means any.
	Generation *int
	// Outcome restricts to one decision state (pending/promoted/rejected/rolled_back).
	// Empty means any.
	Outcome ExperimentOutcome
	// CandidateID restricts to one candidate. Empty means any.
	CandidateID string
	// ProposalKey restricts to the records carrying that join key. Decision records point at the
	// proposal they decided; proposal records leave it empty and are therefore NOT selected by
	// this filter — fetch them by CandidateID or Generation instead. Empty means any.
	ProposalKey string
	// Since restricts to records sealed at or after this time. Zero means any.
	Since time.Time
}

// ExperimentQueryResult is what a query returns, including what it did NOT return.
//
// Truncated and Matched exist so an empty-looking answer cannot be misread: "no records" and
// "one record in the window plus 9,999 unread" are different facts, and an API that returns a
// bare slice makes them look identical.
type ExperimentQueryResult struct {
	// Records are the matching records, newest scan order (the file is append-only, so this is
	// oldest-first), capped at the effective limit.
	Records []ExperimentRecord
	// Matched is how many records matched the filter within the scanned window, before Limit.
	Matched int
	// Scanned is how many records were PARSED, which is the denominator for Matched. It is not
	// "lines read": the line that trips the cap is read but deliberately not counted, and a
	// corrupt line fails the query before it can be counted at all.
	Scanned int
	// Truncated is true when the scan stopped at the record cap before reaching EOF: the answer
	// is a prefix of the log, not the whole of it.
	Truncated bool
}

// ExperimentQuery reads the append-only experiment log with filters and a bounded scan.
type ExperimentQuery struct {
	path string
	// maxScan is the line cap. It is a field so a test can prove truncation without writing
	// ten thousand records; production always uses the package default.
	maxScan int
}

// NewExperimentQuery returns a reader for the log at path.
func NewExperimentQuery(path string) *ExperimentQuery {
	return &ExperimentQuery{path: path, maxScan: maxExperimentQueryScan}
}

// ListFiltered returns the records matching filter.
//
// A malformed line is an ERROR naming its line number, matching ExperimentLog.List: a record
// that cannot be parsed is either corruption or a writer bug, and skipping it would let the log
// answer "we never ran that" about an experiment it did run. Filtering must never be the reason a
// broken record disappears.
func (q *ExperimentQuery) ListFiltered(_ context.Context, filter ExperimentFilter) (ExperimentQueryResult, error) {
	var result ExperimentQueryResult
	if q == nil || q.path == "" {
		return result, fmt.Errorf("experiment query: no log path configured")
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultExperimentQueryLimit
	}
	if limit > MaxExperimentQueryLimit {
		limit = MaxExperimentQueryLimit
	}
	maxScan := q.maxScan
	if maxScan <= 0 {
		maxScan = maxExperimentQueryScan
	}

	f, err := os.Open(q.path)
	if err != nil {
		return result, fmt.Errorf("experiment query: open %s: %w", q.path, err)
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush

	scanner := bufio.NewScanner(f)
	// Same buffer as the writer's reader: one record carries a dimension map plus params, and
	// the default 64KB line limit is close enough to a real record to turn a long one into a
	// scanner error.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		lineNo++
		if lineNo > maxScan {
			// The cap is on LINES READ, not matches: a filter that matches nothing must still
			// stop at the same cost as one that matches everything.
			result.Truncated = true
			break
		}
		var rec ExperimentRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return result, fmt.Errorf("experiment query: %s line %d is not a record: %w", q.path, lineNo, err)
		}
		result.Scanned++
		if !matchesExperimentFilter(rec, filter) {
			continue
		}
		result.Matched++
		if len(result.Records) < limit {
			result.Records = append(result.Records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("experiment query: scan %s: %w", q.path, err)
	}
	return result, nil
}

// matchesExperimentFilter applies the filter to one record.
//
// Kept as a free function so the predicate can be read (and tested) without constructing a query
// or touching a file.
func matchesExperimentFilter(rec ExperimentRecord, filter ExperimentFilter) bool {
	if filter.Generation != nil && rec.Generation != *filter.Generation {
		return false
	}
	if filter.Outcome != "" && rec.Outcome != filter.Outcome {
		return false
	}
	if filter.CandidateID != "" && rec.CandidateID != filter.CandidateID {
		return false
	}
	if filter.ProposalKey != "" && rec.ProposalKey != filter.ProposalKey {
		return false
	}
	if !filter.Since.IsZero() && rec.At.Before(filter.Since) {
		return false
	}
	return true
}
