package ares_bootstrap

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// CorpusHealth is what a corpus probe can know WITHOUT spending a model call.
//
// The eval corpus is a repository FILE with no version stamp, so its decay is silent:
// a case can lose its checks, cases can be deleted, and the only symptom is that later
// scores are judge opinion. The numbers here are the difference between "the corpus is
// anchored" and "the corpus stopped being anchored and nobody said so".
type CorpusHealth struct {
	// Path is the suite file the numbers came from.
	Path string
	// Cases is the usable case count (non-empty inputs, the same filter the gate applies).
	Cases int
	// Anchored is how many of those declare at least one deterministic check.
	Anchored int
	// Fingerprint identifies the file's content, so a probe-to-probe change is visible
	// even when the counts stay the same (one case edited, another removed).
	Fingerprint string
}

// InspectCorpus loads the suite at path and reports its anchors and fingerprint.
//
// It reads the file twice (hash + parse) on purpose: the hash covers exactly the bytes a
// human edits, while the parse gives the counts. Hashing the parsed structure instead would
// hide a whitespace-only edit, and a whitespace-only edit is still an edit to a file whose
// review is the only thing keeping the anchors honest.
func InspectCorpus(path string) (CorpusHealth, error) {
	if path == "" {
		return CorpusHealth{}, fmt.Errorf("bootstrap: corpus probe needs a suite path")
	}
	// The path is the operator-configured eval suite (or the repository default resolved by
	// resolveEvalSuite), never request input, and it is the same file eval.NewLoader parses
	// on the next line — this read adds no file-inclusion surface that Load does not already
	// have. Reading the raw bytes is what makes the fingerprint cover exactly what a human
	// edits.
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured suite path; same file eval.Load reads
	if err != nil {
		return CorpusHealth{}, fmt.Errorf("bootstrap: read corpus %q: %w", path, err)
	}
	suite, err := eval.NewLoader().Load(path)
	if err != nil {
		return CorpusHealth{}, fmt.Errorf("bootstrap: parse corpus %q: %w", path, err)
	}
	// Same filter the gate applies (empty inputs are skipped), so the numbers describe the
	// corpus the gate will actually score rather than the file's raw case count.
	cases := 0
	for _, tc := range suite.TestCases {
		if tc.Input != "" {
			cases++
		}
	}
	sum := sha256.Sum256(raw)
	return CorpusHealth{
		Path:        path,
		Cases:       cases,
		Anchored:    eval.CountAnchoredCases(*suite),
		Fingerprint: fmt.Sprintf("%x", sum[:8]),
	}, nil
}

// runCorpusProbe re-inspects the corpus on a timer and reports changes.
//
// What it does NOT do: run the corpus. That costs a model call per case and would make a
// background job the deployment's biggest bill — so the probe deliberately answers "did the
// corpus change, and is it still anchored", which needs no model at all.
//
// A change is reported at Info (the corpus is expected to evolve); losing every anchor is
// reported at Warn, because from then on every score is judge opinion and nothing else in
// the pipeline says so. Both are reported relative to the PREVIOUS probe, so a steady state
// stays quiet.
func runCorpusProbe(ctx context.Context, path string, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 || path == "" {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var last CorpusHealth
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			health, err := InspectCorpus(path)
			if err != nil {
				// A corpus that cannot be read is exactly the failure this probe exists
				// for: the G3 gate would degrade (or fail) at promote time, long after
				// the file broke.
				logger.Warn("bootstrap: corpus probe could not read the eval suite",
					"path", path, "error", err)
				continue
			}
			if health.Fingerprint != last.Fingerprint {
				logger.Info("bootstrap: eval corpus changed since the last probe",
					"path", health.Path, "cases", health.Cases,
					"anchored_cases", health.Anchored, "fingerprint", health.Fingerprint)
			}
			if health.Anchored == 0 {
				logger.Warn("bootstrap: eval corpus carries NO deterministic anchors — every score from it is judge opinion",
					"path", health.Path, "cases", health.Cases)
			}
			last = health
		}
	}
}
