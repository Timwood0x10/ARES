package eval

import (
	"path/filepath"
	"testing"
)

// minAnchoredCorpusCases is the floor the corpus must keep of cases carrying a
// deterministic check.
//
// It is a deliberate policy number, not a count of what happens to be there. The
// accuracy channel the GA selects on is only as verifiable as this number: every
// case without a declared check is scored by the LLM judge alone, so a corpus
// that shrinks below a usable floor silently turns the signal back into opinion
// — the state P0-1(ii) was opened for. Raising it is the intended direction.
const minAnchoredCorpusCases = 20

// TestCorpus_DeclaredChecksAreResolvable lints every suite under test/eval.
//
// The corpus is data, so nothing else would notice a case that declares a check
// it cannot run: at runtime that resolves to "missing" (indeterminate), the case
// quietly loses its anchor, and the only trace is a counter nobody reads. This
// test is the offline half of the trigger design — the corpus's own health check
// — and it also keeps the anchored-case floor honest.
func TestCorpus_DeclaredChecksAreResolvable(t *testing.T) {
	suites, err := NewLoader().LoadDir(filepath.Join("..", "..", "..", "test", "eval"))
	if err != nil {
		t.Fatalf("load test/eval: %v", err)
	}
	if len(suites) == 0 {
		t.Fatal("no suites found under test/eval — the corpus path or the loader is broken")
	}

	anchored := 0
	for _, suite := range suites {
		for _, tc := range suite.TestCases {
			for _, kind := range tc.Checks {
				if !KnownCheckKind(kind) {
					t.Errorf("case %s (suite %s) declares unknown check kind %q", tc.ID, suite.Name, kind)
				}
			}
			if len(tc.Checks) == 0 {
				continue
			}
			anchored++
			// Every declared check must be resolvable against an empty result:
			// what is missing here is the case's own input (an expectation, an
			// expected tool), which is exactly the corpus bug this catches.
			for _, check := range groundTruthChecks(tc, TestResult{}) {
				if check.Status == StatusMissing {
					t.Errorf("case %s (suite %s): declared check %q is unresolvable — %s", tc.ID, suite.Name, check.Name, check.Detail)
				}
			}
		}
	}

	if anchored < minAnchoredCorpusCases {
		t.Errorf("corpus has %d case(s) with a declared deterministic check, want >= %d: "+
			"the rest of the accuracy signal is LLM opinion, and too little anchored signal means the GA "+
			"selects on a number nobody can verify", anchored, minAnchoredCorpusCases)
	}
	t.Logf("corpus: %d suites, %d anchored case(s)", len(suites), anchored)
}
