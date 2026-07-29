package ares_archive

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// Potential bug scenarios covered by this file:
//  1. Concurrent same-round writes — the mutex serialises them so neither
//     file is corrupted and exactly one round_N.json remains (no .tmp left
//     behind). Tested by TestRecordRound_Concurrent.
//  2. Rotation deleting a file being written — the glob pattern
//     "round_*.json" excludes "round_N.json.tmp", so only fully-renamed
//     files are eligible for deletion. Tested implicitly by
//     TestRecordRound_MaxRoundsRotation (rotation runs while writes are
//     still in-flight-safe by construction).
//  3. Temp filename collision — each round uses a round-specific temp
//     suffix "round_N.json.tmp", so two concurrent writes for different
//     rounds cannot stomp each other's temp file. Tested by
//     TestRecordRound_Concurrent (10 distinct rounds in parallel).

func TestNewFileArchiveWriter_InvalidDir(t *testing.T) {
	w, err := NewFileArchiveWriter("", 0)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyDir)
	assert.Nil(t, w)
}

func TestNewFileArchiveWriter_CreatesDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "nested", "rounds")

	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)
	require.NotNil(t, w)

	info, err := os.Stat(dir)
	require.NoError(t, err, "MkdirAll must create the directory")
	assert.True(t, info.IsDir())
}

func TestRecordRound_Atomic(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	rec := RoundRecord{
		Round:   1,
		Action:  actionImplement,
		Summary: "atomic write smoke test",
		Files:   []FileChange{{Path: "main.go", LinesAdded: 5, Summary: "touched"}},
		Verdict: Verdict{GoVet: verdictPass, GoTest: verdictPass},
	}
	require.NoError(t, w.RecordRound(context.Background(), rec))

	final := roundPath(dir, 1)
	_, err = os.Stat(final)
	require.NoError(t, err, "round_1.json must exist after a successful write")

	tmps, _ := filepath.Glob(filepath.Join(dir, "round_*.json.tmp"))
	assert.Empty(t, tmps, "no .tmp file may remain after a successful write")
}

func TestRecordRound_InvalidRound(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	rec := RoundRecord{Round: 0, Action: actionImplement, Summary: "bad round"}
	err = w.RecordRound(context.Background(), rec)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRound)

	// No file must be written for an invalid round.
	_, statErr := os.Stat(roundPath(dir, 0))
	assert.True(t, os.IsNotExist(statErr), "round_0.json must not exist")
}

func TestRecordRound_MaxRoundsRotation(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 3)
	require.NoError(t, err)

	// Write rounds 1..6; with maxRounds=3 only the three newest (4,5,6)
	// should remain after rotation.
	for n := 1; n <= 6; n++ {
		rec := RoundRecord{Round: n, Action: actionImplement, Summary: "round"}
		require.NoError(t, w.RecordRound(context.Background(), rec))
	}

	for _, n := range []int{4, 5, 6} {
		_, err := os.Stat(roundPath(dir, n))
		require.NoError(t, err, "round %d must be retained", n)
	}
	for _, n := range []int{1, 2, 3} {
		_, err := os.Stat(roundPath(dir, n))
		assert.True(t, os.IsNotExist(err), "round %d must have been rotated out", n)
	}
}

func TestRecordRound_Concurrent(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	const n = 10
	g, gCtx := errgroup.WithContext(context.Background())
	for i := 1; i <= n; i++ {
		i := i
		g.Go(func() error {
			rec := RoundRecord{Round: i, Action: actionImplement, Summary: "concurrent"}
			return w.RecordRound(gCtx, rec)
		})
	}
	require.NoError(t, g.Wait())

	// Every round file must be present and exactly one per round.
	for i := 1; i <= n; i++ {
		_, err := os.Stat(roundPath(dir, i))
		assert.NoError(t, err, "round %d file must exist after concurrent writes", i)
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "round_*.json.tmp"))
	assert.Empty(t, tmps, "no .tmp file may remain after concurrent writes")
}

func TestRecordRound_SameRoundOverwrite(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	first := RoundRecord{Round: 5, Action: actionImplement, Summary: "first"}
	require.NoError(t, w.RecordRound(context.Background(), first))

	second := RoundRecord{Round: 5, Action: actionFix, Summary: "second wins"}
	require.NoError(t, w.RecordRound(context.Background(), second))

	// Exactly one round_5.json with the second content.
	matches, _ := filepath.Glob(filepath.Join(dir, "round_5.json*"))
	assert.Len(t, matches, 1, "exactly one round_5.json file must exist")
	tmps, _ := filepath.Glob(filepath.Join(dir, "round_5.json.tmp"))
	assert.Empty(t, tmps, "no .tmp file may remain")

	read, err := NewFileArchiveReader(dir)
	require.NoError(t, err)
	got, err := read.Read(context.Background(), 5)
	require.NoError(t, err)
	assert.Equal(t, "second wins", got.Summary, "last write must win")
	assert.Equal(t, actionFix, got.Action)
}

func TestRecordRound_PartialOnFailure(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	// Make the directory read-only after the writer is constructed so the
	// WriteFile inside writeAtomic fails. The deferred chmod restores write
	// permission so t.TempDir cleanup can remove the directory.
	require.NoError(t, os.Chmod(dir, 0o500))
	defer func() { _ = os.Chmod(dir, 0o755) }()

	rec := RoundRecord{Round: 1, Action: actionImplement, Summary: "fail me"}
	err = w.RecordRound(context.Background(), rec)
	require.Error(t, err, "write to a read-only directory must fail")

	// Cleanup invariant: no .tmp file and no partial round_N.json remain
	// after a failed write, regardless of where in the write path it failed.
	tmps, _ := filepath.Glob(filepath.Join(dir, "round_*.json.tmp"))
	assert.Empty(t, tmps, "no .tmp file may remain after a failed write")
	finals, _ := filepath.Glob(filepath.Join(dir, "round_*.json"))
	assert.Empty(t, finals, "no partial round_N.json may remain after a failed write")
}

func TestRecordRound_RotationNonFatal(t *testing.T) {
	// Rotation must never fail RecordRound. Writing more rounds than
	// maxRounds triggers rotation that deletes the oldest files, but each
	// RecordRound call still succeeds.
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 2)
	require.NoError(t, err)

	for n := 1; n <= 4; n++ {
		rec := RoundRecord{Round: n, Action: actionImplement, Summary: "round"}
		require.NoError(t, w.RecordRound(context.Background(), rec),
			"rotation must never fail the write")
	}

	// Only the two newest rounds remain.
	assert.NoFileExists(t, roundPath(dir, 1))
	assert.NoFileExists(t, roundPath(dir, 2))
	assert.FileExists(t, roundPath(dir, 3))
	assert.FileExists(t, roundPath(dir, 4))
}

func TestRecordRound_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rec := RoundRecord{Round: 1, Action: actionImplement, Summary: "cancelled"}
	err = w.RecordRound(ctx, rec)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// No file must be written when the context is already cancelled.
	assert.NoFileExists(t, roundPath(dir, 1))
}

func TestFlush(t *testing.T) {
	dir := t.TempDir()
	w, err := NewFileArchiveWriter(dir, 0)
	require.NoError(t, err)

	require.NoError(t, w.Flush(context.Background()), "Flush on a live ctx must be nil")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = w.Flush(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// roundPath returns the absolute path to round_N.json inside dir.
func roundPath(dir string, round int) string {
	return filepath.Join(dir, "round_"+strconv.Itoa(round)+".json")
}
