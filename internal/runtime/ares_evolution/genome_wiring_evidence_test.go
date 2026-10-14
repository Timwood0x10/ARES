package evolution

// genome_wiring_evidence_test.go covers P0-1(ii)'s second hop: the runtime's
// measured fitness reaching SELECTION.
//
// Before it, the accuracy/outcome/cost evidence only gated deployment (staging and
// rollback), so a strategy could be bred on the judge's opinion of its text while
// the running system measured the same strategy and could disagree. The blend is
// asserted against its formula rather than against "it changed something", and the
// wiring is asserted to be the SAME aggregator the deployment gate uses — a second
// one would drift by construction.

import (
	"context"
	"math"
	"testing"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// fakeFitnessWindows is a StrategyFitnessReader over fixed windows, keyed by
// strategy id. An absent id reads as "not measured" (zero Count), which is what an
// aggregator with no evidence returns.
type fakeFitnessWindows map[string]WindowResult

func (f fakeFitnessWindows) Window(_ context.Context, strategyID string) WindowResult {
	return f[strategyID]
}

func judgeOf(score float64) genome.ScorerFunc {
	return func(*mutation.Strategy) float64 { return score }
}

// TestRunScorer_BlendsMeasuredFitnessIntoSelection is the rule itself, in the
// units the production judge actually uses:
//
//	score = (n·(measured·judgeScaleCeiling) + k·judge) / (n + k)
//
// The judge in these cases is 0–100 (the scale of every scorer wired in production)
// and the measured fitness is the aggregator's [0,1] Mean. An earlier version of
// this test used a 0–1 judge, which made the blend look correct while production's
// 0–100 judge drowned the measured side entirely — the units are part of the
// contract here, not a detail of the fixture.
func TestRunScorer_BlendsMeasuredFitnessIntoSelection(t *testing.T) {
	const judge = 90.0
	cases := []struct {
		name     string
		measured float64
		count    int
		prior    float64
		want     float64
	}{
		{name: "no evidence keeps the judge's score exactly", measured: 0.2, count: 0, want: judge},
		{
			name: "one sample barely moves it (the prior holds)", measured: 0.2, count: 1,
			want: (1*20 + 3*judge) / 4,
		},
		{name: "many samples decide it", measured: 0.2, count: 9, want: (9*20 + 3*judge) / 12},
		{
			name: "evidence above the judge pulls the score up", measured: 0.95, count: 9,
			want: (9*95 + 3*judge) / 12,
		},
		{name: "agreement does not move it", measured: 0.9, count: 5, want: judge},
		{
			name: "a weaker prior lets evidence decide sooner", measured: 0.2, count: 1, prior: 1,
			want: (1*20 + 1*judge) / 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &GenomePopulationAdapter{
				scorer:   judgeOf(judge),
				evidence: fakeFitnessWindows{"s1": {Mean: tc.measured, Count: tc.count}},
				// Zero means "use the default prior" — set it explicitly only when
				// the row is about the knob.
				evidencePrior: tc.prior,
			}
			got := a.buildRunScorer(context.Background())(&mutation.Strategy{ID: "s1"})
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("score = %v, want %v (measured=%v n=%d judge=%v)",
					got, tc.want, tc.measured, tc.count, judge)
			}
		})
	}
}

// TestRunScorer_MeasuredEvidenceIsNotDrowned is the scale regression, stated as the
// property the bug violated: on a 0–100 judge, two very different measurements must
// land very differently, and MORE evidence must move the score TOWARDS the measured
// value, not away from it.
//
// With the [0,1]-into-[0,100] mix this test cannot pass: a 0.95 and a 0.2
// measurement both land near 13 (the judge's 150 dominates the sum), so "excellent"
// and "bad" are indistinguishable, and each extra sample dilutes the result further
// towards zero.
func TestRunScorer_MeasuredEvidenceIsNotDrowned(t *testing.T) {
	const judge = 50.0 // the no-scorer fallback and the deterministic base
	scoreFor := func(measured float64, count int) float64 {
		a := &GenomePopulationAdapter{
			scorer:   judgeOf(judge),
			evidence: fakeFitnessWindows{"s1": {Mean: measured, Count: count}},
		}
		return a.buildRunScorer(context.Background())(&mutation.Strategy{ID: "s1"})
	}

	bad := scoreFor(0.2, 9)
	good := scoreFor(0.95, 9)
	if good-bad < 50 {
		t.Fatalf("excellent (%v) and bad (%v) measurements are indistinguishable: "+
			"the measured side is not on the judge's scale", good, bad)
	}
	if bad >= judge {
		t.Fatalf("a bad record (%v) must score below the judge's %v", bad, judge)
	}

	// More evidence moves the score towards the record, monotonically.
	prev := scoreFor(0.2, 0)
	for _, n := range []int{1, 3, 9, 30} {
		got := scoreFor(0.2, n)
		if got > prev {
			t.Fatalf("n=%d scored %v, above n-1's %v: more evidence must pull towards "+
				"the measured value", n, got, prev)
		}
		prev = got
	}
	if prev > 25 {
		t.Fatalf("30 bad samples still score %v: the record should dominate by now", prev)
	}
}

// TestRunScorer_WithoutEvidenceStaysPureJudge keeps the fallback: with no reader
// wired, selection must behave exactly as it did before this hop existed.
func TestRunScorer_WithoutEvidenceStaysPureJudge(t *testing.T) {
	a := &GenomePopulationAdapter{scorer: judgeOf(42)}
	got := a.buildRunScorer(context.Background())(&mutation.Strategy{ID: "s1"})
	if got != 42 {
		t.Fatalf("score = %v, want the judge's 42 unchanged", got)
	}
}

// TestRunScorer_UnlookupableStrategyKeepsTheJudge covers the two shapes with no id
// to look up: a strategy that has never been recorded (empty id) and a nil
// strategy. Both must fall back instead of inventing evidence — the scorer runs
// inside the evolution loop, where a panic would abort a generation.
func TestRunScorer_UnlookupableStrategyKeepsTheJudge(t *testing.T) {
	a := &GenomePopulationAdapter{
		scorer:        judgeOf(70),
		evidence:      fakeFitnessWindows{"": {Mean: 0.1, Count: 100}},
		evidencePrior: 3,
	}
	scorer := a.buildRunScorer(context.Background())

	if got := scorer(&mutation.Strategy{}); got != 70 {
		t.Fatalf("a strategy with no id scored %v, want the judge's 70", got)
	}
	if got := scorer(nil); got != 70 {
		t.Fatalf("a nil strategy scored %v, want the judge's 70", got)
	}
}

// TestWiring_SelectionReadsTheDeploymentAggregator locks the wiring against the
// shape bootstrap uses, and against the specific mistake this defect class makes:
// building a second reader that looks right and drifts. The adapter must hold the
// very aggregator the lifecycle gates deployments with.
func TestWiring_SelectionReadsTheDeploymentAggregator(t *testing.T) {
	defer discardLogs()()
	cfg := DefaultSystemConfig()
	cfg.PopulationSize = 4
	cfg.EnableDreamCycle = false
	cfg.EnableScheduler = true
	cfg.EventStore = newMockCallbackRegistrarForTest()
	cfg.StrategyStore = newMockStrategyStore()
	cfg.RollbackPolicyConfig = RollbackPolicyConfig{Enabled: true}

	system, err := NewWiredEvolutionSystem(&mutation.Strategy{ID: "root"}, cfg)
	if err != nil {
		t.Fatalf("NewWiredEvolutionSystem failed: %v", err)
	}
	defer Shutdown(system)

	if system.Lifecycle == nil || system.Lifecycle.agg == nil {
		t.Fatal("the deployment gate's aggregator must be wired (test precondition)")
	}
	if system.PopAdapter == nil {
		t.Fatal("expected a population adapter")
	}
	if got, want := system.PopAdapter.evidence, StrategyFitnessReader(system.Lifecycle.agg); got != want {
		t.Fatalf("selection reads %T, deployment reads the lifecycle's aggregator: "+
			"one fitness signal must serve both, never two", got)
	}
}
