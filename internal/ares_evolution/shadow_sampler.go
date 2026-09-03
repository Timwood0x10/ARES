// shadow_sampler.go provides the ShadowSampler — the P0-9 comparison feeder
// for the G2 shadow verify gate. It is the counterpart of the OBSERVE stage in
// observer.go: where RuntimeObserver samples the ACTIVE strategy's live
// outcomes, this sampler produces the candidate-vs-active comparisons the
// promote decision needs.
package evolution

import (
	"context"
	"sync"
	"time"

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
// WINDOWED REPLAY (C3.2): when the scorer is deterministic, scoring the same
// pair N times yields N IDENTICAL comparisons — the win rate collapses to 0.0
// or 1.0 and MinSamples is satisfied by repetition rather than by evidence. To
// avoid that, Prime hands each comparison a DIFFERENT replay window (see
// replay_scorer.go): comparison i reads the history slice
// [now-(i+1)·span, now-i·span), so the samples are disjoint task sets and the
// win rate is a real distribution over time. A scorer that ignores the window
// (e.g. an LLM scorer, which is non-deterministic anyway) is unaffected.
//
// REMAINING HONEST LIMIT: replay measures each strategy's own history, so a
// never-executed candidate has no records and falls back to the cold-start
// prior in every window. Its verdict is then "current fleet quality vs the
// active strategy's measured history" — a real signal, but not a candidate-
// specific one. Per-task A/B execution (running the candidate on live traffic)
// is what would make it candidate-specific; that path does not exist yet.
type ShadowSampler struct {
	// evaluator is the G2 gate's data source; this sampler only feeds it.
	evaluator *ShadowEvaluator
	// samples is how many comparisons to gather per submitted candidate so
	// the gate crosses its MinSamples threshold. Zero falls back to
	// defaultShadowSamples.
	samples int
	// timeout bounds one Prime batch; zero falls back to shadowPrimeTimeout.
	// Exposed (unexported field, set by tests) so the batch deadline is
	// verifiable without a 60s real-time wait.
	timeout time.Duration
	// windowSpan is the width of one replay window. Zero falls back to
	// replayWindowSpan.
	windowSpan time.Duration
	mu         sync.Mutex // serializes Prime so two submissions cannot interleave StartShadow/Evaluate
}

// defaultShadowSamples is the comparison count used when the caller passes a
// non-positive sample count. It matches DefaultShadowEvaluationConfig's
// MinSamples so the gate can always reach a verdict.
const defaultShadowSamples = 10

// shadowPrimeTimeout bounds one Prime batch. Each comparison may call the
// scorer twice (active + candidate), and the scorer may in turn make LLM
// calls bounded only by the LLM client's own HTTP timeout — without a batch
// deadline the evolution heartbeat could stall for the whole chain. On
// timeout Prime returns with whatever comparisons it recorded; the gate's
// MinSamples check then rejects (fail-closed) rather than judging a short
// window.
const shadowPrimeTimeout = 60 * time.Second

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
	return &ShadowSampler{evaluator: evaluator, samples: samples, timeout: shadowPrimeTimeout, windowSpan: replayWindowSpan}
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
// The batch is bounded by shadowPrimeTimeout so the evolution heartbeat
// cannot be held hostage by a slow scorer.
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

	timeout := s.timeout
	if timeout <= 0 {
		timeout = shadowPrimeTimeout
	}
	primeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	span := s.windowSpan
	if span <= 0 {
		span = replayWindowSpan
	}
	// Anchor all windows to ONE clock reading: deriving each window from a
	// fresh time.Now() would let the batch's own execution time shift the
	// slices and overlap them, reintroducing the duplicate-evidence problem.
	anchor := time.Now()
	s.evaluator.SetActiveStrategy(active)
	s.evaluator.StartShadow(candidate)
	for i := 0; i < s.samples; i++ {
		if primeCtx.Err() != nil {
			return
		}
		// Walk backwards through history, one disjoint window per
		// comparison: [anchor-(i+1)*span, anchor-i*span).
		w := replayWindow{
			Since: anchor.Add(-time.Duration(i+1) * span),
			Until: anchor.Add(-time.Duration(i) * span),
		}
		s.evaluator.Evaluate(withReplayWindow(primeCtx, w))
	}
}

// TODO(tech-debt): replay windows make the comparisons disjoint in TIME, but a
// never-executed candidate still has no records of its own and scores the
// cold-start prior in every window (see the ShadowSampler doc comment).
// Replace with per-task real-execution sampling (one comparison per live task,
// candidate vs active) once a task-level A/B execution path exists.
