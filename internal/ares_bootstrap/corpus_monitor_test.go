package ares_bootstrap

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const corpusWithOneAnchor = `
name: anchored
description: one anchored, one judged by opinion
test_cases:
  - id: c1
    name: anchored case
    input: "What is 2+2?"
    checks: [exact]
    expected_output: "4"
  - id: c2
    name: opinion case
    input: "Summarize the quarterly report."
`

const corpusWithoutAnchors = `
name: unanchored
test_cases:
  - id: c1
    name: opinion only
    input: "Summarize the quarterly report."
`

// TestInspectCorpus_ReportsAnchorsAndFingerprint is the P0-1 probe's contract: the corpus
// is a repository file with no version stamp, so "how many cases are actually anchored" and
// "did the file change" are the two facts that were invisible.
func TestInspectCorpus_ReportsAnchorsAndFingerprint(t *testing.T) {
	path := writeSuiteFile(t, corpusWithOneAnchor)

	health, err := InspectCorpus(path)
	require.NoError(t, err)
	assert.Equal(t, 2, health.Cases, "usable cases (non-empty inputs)")
	assert.Equal(t, 1, health.Anchored, "only the case that declares a check is anchored")
	assert.NotEmpty(t, health.Fingerprint)
	assert.Equal(t, path, health.Path)

	// Same bytes, same fingerprint.
	again, err := InspectCorpus(path)
	require.NoError(t, err)
	assert.Equal(t, health.Fingerprint, again.Fingerprint)

	// An EDIT changes it — including one that keeps the counts identical (a case's checks
	// removed and another's left alone would move the numbers, but a reworded case would
	// not; the fingerprint is what catches that).
	edited := writeSuiteFile(t, corpusWithOneAnchor+"\n# touched\n")
	after, err := InspectCorpus(edited)
	require.NoError(t, err)
	assert.NotEqual(t, health.Fingerprint, after.Fingerprint,
		"any edit must be visible, not only the ones that change the counts")
}

// TestInspectCorpus_UnanchoredCorpusIsReported: zero anchors is the state that turns every
// later score into judge opinion — the probe must be able to see it (its caller warns).
func TestInspectCorpus_UnanchoredCorpusIsReported(t *testing.T) {
	path := writeSuiteFile(t, corpusWithoutAnchors)
	health, err := InspectCorpus(path)
	require.NoError(t, err)
	assert.Zero(t, health.Anchored)
	assert.Equal(t, 1, health.Cases, "the case is usable, it just carries no proof")
}

// TestInspectCorpus_RefusesWhatItCannotRead: a broken corpus is exactly the failure the
// probe exists for, so it must surface as an error rather than a zero report.
func TestInspectCorpus_RefusesWhatItCannotRead(t *testing.T) {
	_, err := InspectCorpus("")
	require.Error(t, err)

	_, err = InspectCorpus(t.TempDir() + "/missing.yaml")
	require.Error(t, err)
}

// TestRunCorpusProbe_StopsWithItsContext: the probe is a background loop, so it must exit
// when the deployment shuts down instead of holding the process open.
func TestRunCorpusProbe_StopsWithItsContext(t *testing.T) {
	path := writeSuiteFile(t, corpusWithOneAnchor)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		runCorpusProbe(ctx, path, time.Hour, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the corpus probe ignored its context: a shutdown would hang on it")
	}

	// A zero interval is the documented "off" switch and must not start a loop at all.
	runCorpusProbe(context.Background(), path, 0, nil)
}

// captureLogs returns a logger writing into the returned buffer.
func captureLogs() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// TestRunCorpusProbe_ReportsOnEachTick exercises the LOOP BODY: the first tick reports the
// corpus (it differs from the zero-value "last"), and a corpus with no anchors warns on
// every tick — that warning is the whole reason this probe exists, so it cannot be the one
// line that never runs in a test.
func TestRunCorpusProbe_ReportsOnEachTick(t *testing.T) {
	anchored := writeSuiteFile(t, corpusWithOneAnchor)
	logger, buf := captureLogs()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	runCorpusProbe(ctx, anchored, 10*time.Millisecond, logger)

	out := buf.String()
	assert.Contains(t, out, "eval corpus changed since the last probe",
		"the first tick must report the corpus it found: %s", out)
	assert.NotContains(t, out, "NO deterministic anchors",
		"an anchored corpus must not be reported as unanchored")

	// An unanchored corpus: the warning fires, because from then on every score is opinion.
	unanchored := writeSuiteFile(t, corpusWithoutAnchors)
	logger2, buf2 := captureLogs()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel2()
	runCorpusProbe(ctx2, unanchored, 10*time.Millisecond, logger2)
	assert.Contains(t, buf2.String(), "NO deterministic anchors")
}

// TestRunCorpusProbe_ReportsAnUnreadableCorpus: a corpus that breaks AFTER startup is the
// case the probe exists for (the gate would only notice at promote time), so it must warn
// instead of exiting the loop or panicking.
func TestRunCorpusProbe_ReportsAnUnreadableCorpus(t *testing.T) {
	path := writeSuiteFile(t, corpusWithOneAnchor)
	require.NoError(t, os.Remove(path))

	logger, buf := captureLogs()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	runCorpusProbe(ctx, path, 10*time.Millisecond, logger)

	assert.Contains(t, buf.String(), "could not read the eval suite",
		"a broken corpus must be reported, not silently skipped: %s", buf.String())
}
