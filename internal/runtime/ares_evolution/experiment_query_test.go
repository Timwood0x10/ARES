package evolution

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExperimentFile writes records as JSONL without fsync, which is what makes the truncation
// test affordable: the production writer fsyncs every record because losing one to a crash is
// the failure the log exists to explain, and that does not need to be paid per test record.
func writeExperimentFile(t *testing.T, recs []ExperimentRecord) string {
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

// TestExperimentQuery_FilterAndLimit pins both halves of the contract: the filter selects what it
// says it selects, and Limit caps the response without changing what Matched reports (a caller
// must be able to tell "2 of 3" from "2").
func TestExperimentQuery_FilterAndLimit(t *testing.T) {
	gen0, gen1 := 0, 1
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	path := writeExperimentFile(t, []ExperimentRecord{
		{Generation: gen0, CandidateID: "a", Outcome: ExperimentOutcomePending, At: at},
		{Generation: gen1, CandidateID: "b", Outcome: ExperimentOutcomeAccepted, At: at.Add(time.Hour)},
		{Generation: gen1, CandidateID: "c", Outcome: ExperimentOutcomeAccepted, At: at.Add(2 * time.Hour)},
		{Generation: gen1, CandidateID: "d", Outcome: ExperimentOutcomeRejected, At: at.Add(3 * time.Hour)},
	})
	q := NewExperimentQuery(path)

	all, err := q.ListFiltered(context.Background(), ExperimentFilter{})
	require.NoError(t, err)
	assert.Len(t, all.Records, 4)
	assert.Equal(t, 4, all.Matched)
	assert.False(t, all.Truncated)

	accepted, err := q.ListFiltered(context.Background(), ExperimentFilter{Outcome: ExperimentOutcomeAccepted})
	require.NoError(t, err)
	assert.Equal(t, 2, accepted.Matched)
	assert.Len(t, accepted.Records, 2)

	limited, err := q.ListFiltered(context.Background(), ExperimentFilter{
		Outcome: ExperimentOutcomeAccepted, Limit: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, limited.Matched, "Matched is the count BEFORE the limit")
	assert.Len(t, limited.Records, 1)

	// Generation 0 is a real generation, which is why the filter takes a pointer: an int would
	// make "generation 0" indistinguishable from "any generation".
	gen0Result, err := q.ListFiltered(context.Background(), ExperimentFilter{Generation: &gen0})
	require.NoError(t, err)
	assert.Equal(t, 1, gen0Result.Matched)

	// Since excludes earlier records; the boundary itself is included.
	sinceResult, err := q.ListFiltered(context.Background(), ExperimentFilter{Since: at.Add(2 * time.Hour)})
	require.NoError(t, err)
	assert.Equal(t, 2, sinceResult.Matched)
}

// TestExperimentQuery_CorruptLineIsReported: filtering must never be the reason a broken record
// disappears. A line that cannot be parsed names its number and fails the query, matching the
// read policy of ExperimentLog.List.
func TestExperimentQuery_CorruptLineIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	content := `{"generation":0,"candidate_id":"ok","outcome":"pending"}` + "\n" + `{not json` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	_, err := NewExperimentQuery(path).ListFiltered(context.Background(), ExperimentFilter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
}

// TestExperimentQuery_Truncated: the scan cap bounds the cost of a query on a log that grows
// forever. Hitting it must be reported, not silently rendered as "that is all there is".
func TestExperimentQuery_Truncated(t *testing.T) {
	recs := make([]ExperimentRecord, 0, 6)
	for i := 0; i < 6; i++ {
		recs = append(recs, ExperimentRecord{Generation: i, CandidateID: "c"})
	}
	path := writeExperimentFile(t, recs)

	q := NewExperimentQuery(path)
	q.maxScan = 3 // production value is 10000; injected so the test stays fast

	res, err := q.ListFiltered(context.Background(), ExperimentFilter{})
	require.NoError(t, err)
	assert.True(t, res.Truncated, "stopping at the cap is a prefix, not the whole log")
	assert.Equal(t, 3, res.Scanned)
	assert.Len(t, res.Records, 3)
}

// TestExperimentQuery_LimitIsClamped: asking for more than the API will send returns the largest
// answer it will send rather than an error — the caller still gets data, and the cap is what
// protects the process from a request that would load the whole file into memory.
func TestExperimentQuery_LimitIsClamped(t *testing.T) {
	path := writeExperimentFile(t, []ExperimentRecord{{CandidateID: "a"}})
	res, err := NewExperimentQuery(path).ListFiltered(context.Background(), ExperimentFilter{
		Limit: MaxExperimentQueryLimit + 500,
	})
	require.NoError(t, err)
	assert.Len(t, res.Records, 1, "clamping changes the ceiling, not the result set")
}
