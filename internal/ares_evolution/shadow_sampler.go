// shadow_sampler.go provides the ShadowSampler — the P0-9 comparison feeder
// for the G2 shadow verify gate. It is the counterpart of the OBSERVE stage in
// observer.go: where RuntimeObserver samples the ACTIVE strategy's live
// outcomes, this sampler produces the candidate-vs-active comparisons the
// promote decision needs.
package evolution

import (
	"context"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// ShadowSampler is the P0-9 task-level feeder for the G2 shadow gate. It owns
// StartShadow/RecordResult on a ShadowEvaluator so the gate itself stays
// read-only: when a candidate is submitted, the lifecycle calls Prime, which
// (a) points the evaluator at the candidate-and-active pair and (b) gathers the
// comparison samples the gate needs to judge.
//
// The sampler reuses the ShadowEvaluator's independent scorer (wired by
// buildShadowEvaluator from the GA scorer). When no independent scorer is set
// the evaluator cannot produce samples, so Prime is a no-op and the candidate
// stays fail-closed in SHADOW — the intended safe default (§4④) until a real
// evidence source (LLM/heuristic scorer or a task execution sampler) is wired.
//
// It is deliberately SYNCHRONOUS: Submit runs the G2 gate inline, so a feeder
// that accumulates async comparisons could never be seen by the very gate that
// drops the candidate. Prime fills the gap before the pipeline runs.
//
// EXACTLY ONE FEEDER: the sampler must not be wired alongside DreamCycle's
// shadow flow — both call StartShadow, which resets accumulated comparisons.
// The wiring picks one (see NewWiredEvolutionSystem).
//
// HONEST LIMIT (do not mistake this for statistical power): the sampler scores
// the same candidate/active pair `samples` times. That only yields INDEPENDENT
// comparisons when the scorer is non-deterministic (the LLM scorer, which
// samples at temperature > 0). With a deterministic scorer every comparison is
// identical, so the win rate collapses to 0.0 or 1.0 and MinSamples is
// satisfied by repetition rather than by evidence — the verdict is a single
// score comparison wearing MinSamples' clothes. Per-task real-execution
// sampling (each comparison from a different live task) is the follow-up that
// makes the threshold meaningful; it needs a task-level A/B execution path that
// does not exist yet.
type ShadowSampler struct {
	// evaluator is the G2 gate's data source; this sampler only feeds it.
	evaluator *ShadowEvaluator
	// samples is how many comparisons to gather per submitted candidate so
	// the gate crosses its MinSamples threshold. Zero falls back to
	// defaultShadowSamples.
	samples int
	mu      sync.Mutex // serializes Prime so two submissions cannot interleave StartShadow/Evaluate
}

// defaultShadowSamples is the comparison count used when the caller passes a
// non-positive sample count. It matches DefaultShadowEvaluationConfig's
// MinSamples so the gate can always reach a verdict.
const defaultShadowSamples = 10

// NewShadowSampler creates a task-level shadow comparison feeder.
//
// Args:
//
//	evaluator - the ShadowEvaluator the G2 gate reads (must be non-nil).
//	samples   - comparison count to gather per submitted candidate;
//	            non-positive falls back to defaultShadowSamples.
//
// Returns:
//
//	*ShadowSampler - the configured feeder.
func NewShadowSampler(evaluator *ShadowEvaluator, samples int) *ShadowSampler {
	if samples <= 0 {
		samples = defaultShadowSamples
	}
	return &ShadowSampler{evaluator: evaluator, samples: samples}
}

// Prime prepares the evaluator for one candidate-and-active pair and gathers
// the shadow-comparison samples the G2 gate judges. Callers invoke it once per
// submitted candidate, between recording the candidate and running the gates.
//
// Each call RESTARTS the sample window via StartShadow (which drops prior
// comparisons). That is required, not incidental: every submission introduces a
// different candidate, and the gate must judge only THIS candidate's evidence
// rather than a batch accumulated for an already-rejected one.
//
// Prime respects ctx cancellation between comparisons: a shutdown mid-batch
// leaves the partial samples it already recorded, and the gate's fail-closed
// MinSamples check rejects the candidate rather than judging on a short window.
//
// Args:
//
//	ctx       - operation context for cancellation.
//	candidate - the strategy being shadow-evaluated.
//	active    - the currently active strategy to compare against.
func (s *ShadowSampler) Prime(ctx context.Context, candidate, active *mutation.Strategy) {
	if s == nil || s.evaluator == nil || candidate == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.evaluator.HasIndependentScorer() {
		// No independent evidence source: leave the evaluator without
		// comparisons so the G2 gate stays fail-closed. Fabricating scores
		// here would make the gate a rubber stamp, which §4④ rejects.
		return
	}
	s.evaluator.SetActiveStrategy(active)
	s.evaluator.StartShadow(candidate)
	for i := 0; i < s.samples; i++ {
		if ctx.Err() != nil {
			return
		}
		s.evaluator.Evaluate(ctx)
	}
}

// TODO(tech-debt): comparisons are repeated scores of the SAME pair, so they are
// only independent under a non-deterministic (LLM) scorer — see the
// ShadowSampler doc comment. Replace with per-task real-execution sampling
// (one comparison per live task, candidate vs active) once a task-level A/B
// execution path exists; until then MinSamples counts repetitions, not
// independent evidence.
