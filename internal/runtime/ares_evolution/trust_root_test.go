package evolution_test

// Trust-root disambiguation (M-C1, ARCHITECTURE.md high-risk #3): the promote
// trust root is v1 ares_evolution's StrategyLifecycle gate chain. The v2
// engine's CandidatePipeline carries its own SetStable promote path; it is
// examples/fixtures-only and must NOT silently enter the production import
// graph. This gate fails the moment an internal/ or cmd/ file constructs the
// pipeline, forcing the author to either route through v1's gates or amend
// this test with an explicit, reviewed exception.
//
// Detection is constructor-scoped on purpose: importing the v2 package is
// legitimate in production for its non-pipeline contracts (e.g.
// ares_bootstrap's LLMAdapter); only constructing the pipeline crosses the
// trust-root boundary.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// findModuleRoot walks up from the test's working directory (the package
// dir) until it finds go.mod. The filepath.Dir-chain pattern this test
// previously used resolved one level above the repo root and silently
// scanned zero files — walk-up fails loudly instead.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found from working directory upward")
		}
		dir = parent
	}
}

func TestCandidatePipelineNotInProductionImportGraph(t *testing.T) {
	repo := findModuleRoot(t)

	dirs := []string{
		filepath.Join(repo, "internal"),
		filepath.Join(repo, "cmd"),
		filepath.Join(repo, "sdk"),
		filepath.Join(repo, "services"),
	}
	// The pipeline itself and its package-internal tests are excluded; so
	// is this file (its needle string literals would self-match).
	excluded := filepath.Join(repo, "internal", "runtime", "evolution")
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}

	// The v2 pipeline's production entry points. A comment or a doc
	// reference does not count; a constructor call does. The needle starts
	// with "." (not "evolution.") so an aliased import — the repo itself
	// imports this package as "evoparent" — cannot evade the gate.
	needles := []string{
		".NewCandidatePipeline",
		".NewCandidatePipelineWithOptions",
	}

	hits := []string{}
	scanned := 0
	for _, dir := range dirs {
		werr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			if strings.HasPrefix(path, excluded) || path == thisFile {
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			scanned++
			for i, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				for _, needle := range needles {
					if strings.Contains(line, needle) {
						rel, _ := filepath.Rel(repo, path)
						hits = append(hits, rel+":"+itoa(i+1))
						break
					}
				}
			}
			return nil
		})
		if werr != nil {
			t.Fatalf("walk %s: %v", dir, werr)
		}
	}
	// Self-check: a wrong repo root or an emptied tree must fail loudly,
	// not vacuously pass (the pre-fix bug scanned zero files and passed).
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %v — the discovery logic is broken (wrong repo root?); do not let this gate rot", scanned, dirs)
	}
	if len(hits) > 0 {
		t.Fatalf("v2 CandidatePipeline entered the production import graph (%v) — the promote trust root is v1 ares_evolution's lifecycle gates (ARCHITECTURE.md 高风险 #3). Route the promotion through v1's gate chain, or amend this test with a reviewed exception documenting why v2's SetStable path is now production-trusted.",
			hits)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
