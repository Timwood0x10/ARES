package evolution

// lineage_significance_test.go pins the UNIT of the significance threshold and the one
// rule that consumes it.
//
// The delta being judged is CandidateScore - BaselineScore from the regression test, i.e.
// a difference of two scorer scores on [0, evolve.JudgeScaleCeiling] (0-100 in
// production). The threshold used to be the literal 0.01, written as if the delta were a
// [0,1] ratio — a hundredth of a point on a 0-100 scale, so the "significant improvement"
// flag fired on scorer noise and the flag meant nothing.

import (
	"testing"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

func TestDefaultMinLineageImprovement_IsOnePercentOfTheJudgeScale(t *testing.T) {
	t.Parallel()
	if want := 0.01 * evolution.JudgeScaleCeiling; defaultMinLineageImprovement != want {
		t.Fatalf("defaultMinLineageImprovement = %v, want %v", defaultMinLineageImprovement, want)
	}
	if defaultMinLineageImprovement <= 0.01 {
		t.Fatalf("defaultMinLineageImprovement = %v: anything at or below 0.01 on a 0-100 "+
			"scale is below scorer resolution, which is the bug this constant fixes",
			defaultMinLineageImprovement)
	}
}

func TestImprovementIsSignificant_UsesTheDeltasOwnUnit(t *testing.T) {
	t.Parallel()
	const threshold = defaultMinLineageImprovement // 1 point

	cases := []struct {
		name  string
		delta float64
		want  bool
	}{
		{name: "no change", delta: 0, want: false},
		{name: "a hundredth of a point is noise", delta: 0.01, want: false},
		{name: "half a point is still noise", delta: 0.5, want: false},
		{name: "one point meets the threshold", delta: 1, want: true},
		{name: "a real jump is significant", delta: 7.5, want: true},
		{name: "a regression is never significant", delta: -3, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := improvementIsSignificant(tc.delta, threshold); got != tc.want {
				t.Fatalf("improvementIsSignificant(%v, %v) = %v, want %v",
					tc.delta, threshold, got, tc.want)
			}
		})
	}
}

// TestIsSuspiciousLegacyThreshold pins the migration heuristic: a value below a tenth of a
// point cannot be a real noise floor on a 0-100 scale, so it is read as a leftover from the
// old [0,1] ratio reading. Pure and deterministic, so it needs no logger.
func TestIsSuspiciousLegacyThreshold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		configured float64
		want       bool
	}{
		{name: "unset", configured: 0, want: false},
		{name: "negative", configured: -1, want: false},
		{name: "the historical 0.01", configured: 0.01, want: true},
		{name: "a plausible old ratio", configured: 0.05, want: true},
		{name: "at the boundary", configured: suspiciouslySmallLineageImprovement, want: false},
		{name: "the new default", configured: defaultMinLineageImprovement, want: false},
		{name: "a deliberate small value", configured: 0.5, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSuspiciousLegacyThreshold(tc.configured); got != tc.want {
				t.Fatalf("isSuspiciousLegacyThreshold(%v) = %v, want %v",
					tc.configured, got, tc.want)
			}
		})
	}
}

// TestWarnSuspiciousLegacyThreshold_WarnsOnlyOnce keeps the notice from becoming a log
// sink: the threshold is read on every report, so a legacy value would otherwise be
// reported per call. The FIRST call's return value is deliberately not asserted here — the
// limiter is process-global, and another test may have raised the notice already.
func TestWarnSuspiciousLegacyThreshold_WarnsOnlyOnce(t *testing.T) {
	t.Parallel()

	_ = warnSuspiciousLegacyThreshold(0.01) // may or may not be the first call: see above
	if warnSuspiciousLegacyThreshold(0.01) {
		t.Fatal("a second legacy value must not warn again: the notice is process-wide")
	}
	if warnSuspiciousLegacyThreshold(5) {
		t.Fatal("a value that is not suspicious must never warn")
	}
}

func TestLineageImprovementThreshold_UnsetFallsBackToTheDerivedDefault(t *testing.T) {
	t.Parallel()

	unset := &Service{config: DefaultConfig()}
	unset.config.MinLineageImprovement = 0
	if got := unset.lineageImprovementThreshold(); got != defaultMinLineageImprovement {
		t.Fatalf("unset threshold = %v, want the derived default %v",
			got, defaultMinLineageImprovement)
	}

	configured := &Service{config: DefaultConfig()}
	configured.config.MinLineageImprovement = 5 // points, verbatim
	if got := configured.lineageImprovementThreshold(); got != 5 {
		t.Fatalf("configured threshold = %v, want 5 (taken as points, not rescaled)", got)
	}
}
