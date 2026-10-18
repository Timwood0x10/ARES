package evolution

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileExperimentLog_SurvivesReopen is the property the in-memory log could not offer:
// a record written before a restart is still there after one. That is the whole point of a
// reproducible experiment log — the crash you want to investigate is the restart you lose.
func TestFileExperimentLog_SurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	ctx := context.Background()

	first, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	rec := experimentFixture(true)
	require.NoError(t, first.Append(ctx, rec))
	require.NoError(t, first.Close())

	// Reopen: the record must still be readable, with its identity intact.
	second, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, second.Close()) }()

	got, err := second.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, rec.ReplayKey(), got[0].ReplayKey(),
		"a record must describe the same experiment after a restart")
	assert.Equal(t, rec.CandidateID, got[0].CandidateID)
}

// TestFileExperimentLog_AppendsInOrder: append order is the record order, across handles —
// two writers in sequence must not interleave or overwrite.
func TestFileExperimentLog_AppendsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	ctx := context.Background()

	log, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, log.Close()) }()

	a := experimentFixture(true)
	b := experimentFixture(true)
	b.CandidateID = "cand-8"
	require.NoError(t, log.Append(ctx, a))
	require.NoError(t, log.Append(ctx, b))

	got, err := log.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, []string{"cand-7", "cand-8"}, []string{got[0].CandidateID, got[1].CandidateID})
}

// TestFileExperimentLog_RefusesAnUnidentifiableRecord: a record with no candidate id cannot be
// replayed, so it must not be written at all.
func TestFileExperimentLog_RefusesAnUnidentifiableRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	log, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, log.Close()) }()

	assert.Error(t, log.Append(context.Background(), ExperimentRecord{}))
	got, err := log.List(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got, "a refused record must not reach the file")
}

// TestFileExperimentLog_ReportsACorruptLine: silently skipping an unparsable line would make
// the log answer "we never ran that" about an experiment it did run, so the line number is
// surfaced instead.
func TestFileExperimentLog_ReportsACorruptLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "experiments.jsonl")
	log, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	require.NoError(t, log.Append(context.Background(), experimentFixture(true)))
	require.NoError(t, log.Close())

	// Simulate a writer bug (or a truncated line from an older build).
	require.NoError(t, os.WriteFile(path, []byte("{not json}\n"), 0o600))

	reopened, err := OpenFileExperimentLog(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()

	_, err = reopened.List(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 1", "the error must say WHICH line is unreadable")
}
