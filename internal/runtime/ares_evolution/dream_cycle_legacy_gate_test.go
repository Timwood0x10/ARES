package evolution_test

// the v1 DreamCycle orchestrator is dead on the
// production path — nothing invokes DreamCycle.Run. Production drives evolution
// through GenomePopulationAdapter.Run (see the DreamCycle type doc, and
// ares_bootstrap/bootstrap_evolution.go which hard-codes EnableDreamCycle=false).
//
// This gate fails if someone wires Run() back into a production call site,
// forcing a reviewed decision: the v1 orchestrator bypasses the
// StrategyLifecycle gate chain, so re-enabling it silently would reopen the
// A1 trust-root gap. It mirrors the source-scan style of
// TestCandidatePipelineNotInProductionImportGraph.

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// dreamRunRe matches any production call site that reaches DreamCycle.Run.
//
// A regex, not a needle list: the system holds the orchestrator as the field
// `DreamCycle *DreamCycle` (genome_wiring_system.go), so the most natural
// spellings are `s.DreamCycle.Run(...)`, the scheduler accessor
// `s.DreamCycle().Run(...)`, chained/atomic forms such as
// `dreamCycle.Load().Run(...)`, and a bare method value `_ = s.DreamCycle.Run`.
// The original four-needle list missed `s.DreamCycle.Run(` — the commonest form
// — which made the gate a false guarantee (REVIEW-2026-10-07 C1). The pattern
// therefore matches `DreamCycle` followed by zero or more `.Ident()` hops and
// then `.Run` as a whole word (with or without a call's `(`). It deliberately
// does NOT match the definition `func (dc *DreamCycle) Run(` — there the token
// after `DreamCycle` is `)`, not `.Run`.
var dreamRunRe = regexp.MustCompile(
	`[Dd]reamCycle(\(\))?(\s*\.\s*[A-Za-z_][A-Za-z0-9_]*\(\))*\s*\.\s*Run\b`)

func TestDreamCycleRunNotInvokedInProduction(t *testing.T) {
	repo := findModuleRoot(t)

	dirs := []string{
		filepath.Join(repo, "internal"),
		filepath.Join(repo, "cmd"),
		filepath.Join(repo, "sdk"),
		filepath.Join(repo, "services"),
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}

	// Self-check the rule itself before trusting it: it MUST match every shape
	// it exists to catch (a needle list that missed `s.DreamCycle.Run(` is why
	// this gate was rebuilt), and MUST NOT match the type declaration. This
	// guards against the regex silently rotting — same spirit as the
	// `scanned < 100` check below.
	for _, sample := range []string{
		"s.DreamCycle.Run(ctx)",
		"_ = s.DreamCycle.Run",
		"s.DreamCycle().Run(ctx)",
		"dreamCycle.Load().Run(ctx)",
		"dreamCycle.Run(ctx)",
		"s.DreamCycle.Get().Load().Run(ctx)",
	} {
		if !dreamRunRe.MatchString(sample) {
			t.Fatalf("legacy-gate regex fails to match %q — the rule rotted and the boundary is NOT enforced", sample)
		}
	}
	for _, sample := range []string{
		"func (dc *DreamCycle) Run(ctx context.Context)",
		"type DreamCycle struct",
	} {
		if dreamRunRe.MatchString(sample) {
			t.Fatalf("legacy-gate regex wrongly matches %q — it would flag the DreamCycle declaration itself", sample)
		}
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
			// Tests legitimately call Run (unit/benchmark coverage); this gate
			// is about PRODUCTION code only.
			if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
				return nil
			}
			if path == thisFile {
				return nil
			}
			raw, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			scanned++
			for i, line := range strings.Split(string(raw), "\n") {
				// Skip comments: the DreamCycle type doc mentions
				// `.DreamCycle.Run(` when explaining the boundary, and doc
				// prose must not self-trigger the gate.
				if strings.HasPrefix(strings.TrimSpace(line), "//") {
					continue
				}
				if dreamRunRe.MatchString(line) {
					rel, _ := filepath.Rel(repo, path)
					hits = append(hits, rel+":"+itoa(i+1))
				}
			}
			return nil
		})
		if werr != nil {
			t.Fatalf("walk %s: %v", dir, werr)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d Go files under %v — discovery logic is broken (wrong repo root?), do not let this gate rot", scanned, dirs)
	}
	if len(hits) > 0 {
		t.Fatalf("DreamCycle.Run wired into the production path at %v — the v1 orchestrator bypasses the StrategyLifecycle gate chain (A2/A1). Route evolution through GenomePopulationAdapter, or amend this test with a reviewed exception documenting why v1 is now production-trusted.", hits)
	}

	// Lock the production-config premise this gate rests on: bootstrap must
	// keep the v1 orchestrator off. If this flips, the DreamCycle type doc and
	// this gate both need a reviewed update.
	bootstrap := filepath.Join(repo, "internal", "ares_bootstrap", "bootstrap_evolution.go")
	raw, err := os.ReadFile(bootstrap)
	if err != nil {
		t.Fatalf("read bootstrap: %v", err)
	}
	if !strings.Contains(string(raw), "EnableDreamCycle = false") {
		t.Fatalf("%s no longer hard-codes EnableDreamCycle=false — the A2 legacy premise changed; update the DreamCycle type doc and this gate accordingly", bootstrap)
	}
}
