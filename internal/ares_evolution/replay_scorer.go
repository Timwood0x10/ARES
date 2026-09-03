// replay_scorer.go provides the zero-LLM ReplayScorer — the G2 shadow gate's
// independent evidence source in the default (LLM scoring off) configuration.
//
// WHY THIS EXISTS (C3.2, depends on C2.6): bootstrap declares
// DeterministicScorerEnabled so shadowGateMode registers the G2 gate as
// "independent scorer wired". That promise is only real if a scorer actually
// DISCRIMINATES between the candidate and the active strategy. A scorer that
// returns one global number for every strategy makes every comparison an exact
// tie, ShadowWon is never true, the win rate collapses to 0.0 and G2 rejects
// every candidate forever — a gate that claims evidence while gathering none.
// That failure mode is the very thing C3.2 forbids ("需确认 MinSamples 是被独立
// 证据满足而非同分重复").
//
// The honest evidence source under a zero-token budget is REPLAY: the runtime
// already writes one KindFitness evidence record per finished task
// (RuntimeObserver.writeEvidence, source="strategy", payload carries
// strategy_id). Scoring a strategy by the mean of ITS OWN historical records,
// over a bounded time window, is a real per-strategy measurement that costs no
// LLM call. Slicing the history into disjoint windows turns MinSamples into
// independent evidence — each comparison reads a DIFFERENT task set — instead
// of counting repetitions of one verdict.
package evolution

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// observerEvidenceSource is the evidence source RuntimeObserver stamps on the
// per-task fitness records the replay scorer reads back. Shared as a constant
// so the writer and the reader can never drift apart — a mismatch would make
// every query return nothing and silently reduce the scorer to its prior.
const observerEvidenceSource = "strategy"

// replayWindowSpan is the duration of ONE replay window. Each shadow
// comparison replays a distinct span of history, so the sampler's MinSamples
// comparisons read disjoint task sets rather than re-reading one set.
//
// 10 minutes is chosen against the observer's write rate: one record per
// finished task, so a 10-minute span holds several tasks under normal load
// while staying short enough that MinSamples (10 by default) windows cover a
// ~100-minute horizon — recent enough to reflect current behaviour.
const replayWindowSpan = 10 * time.Minute

// replayWindow bounds one comparison's evidence read. A zero value means
// "no bound" (query the whole retained history).
type replayWindow struct {
	Since time.Time
	Until time.Time
}

// replayWindowCtxKey is the private context key carrying the current replay
// window. The window travels through the context because the ShadowEvaluator
// owns the scorer call sites (it calls scorer(ctx, active) and
// scorer(ctx, shadow) per comparison) and must not need to know that some
// scorers are window-aware.
type replayWindowCtxKey struct{}

// withReplayWindow returns a context carrying the given replay window.
func withReplayWindow(ctx context.Context, w replayWindow) context.Context {
	return context.WithValue(ctx, replayWindowCtxKey{}, w)
}

// replayWindowFrom extracts the replay window from ctx. The zero window
// (unbounded) is returned when none is set — a non-windowed caller then reads
// the full history, which is the correct degradation, not an error.
func replayWindowFrom(ctx context.Context) replayWindow {
	if ctx == nil {
		return replayWindow{}
	}
	w, _ := ctx.Value(replayWindowCtxKey{}).(replayWindow)
	return w
}

// ReplayScorer scores a strategy by replaying its own historical execution
// evidence. It performs NO LLM call: the score is the mean of the strategy's
// KindFitness records inside the requested window.
//
// COLD START is the delicate part. A freshly generated candidate has no
// history, so its own evidence set is empty. Returning a fixed constant would
// resurrect the tie problem for the candidate side, and inventing a favourable
// number would make G2 a rubber stamp. Instead the scorer falls back to the
// caller-supplied prior — in production the attribution-derived deterministic
// score (C2.2), i.e. the CURRENT fleet-wide execution quality. The resulting
// verdict has a defensible reading: an untried candidate is promoted only when
// the fleet is currently performing better than the active strategy's own
// measured history, that is, when the active strategy is the thing holding
// quality back. Absent a live A/B execution path, that is the strongest honest
// statement available at zero token cost.
//
// Thread-safe: all fields are read-only after construction and evidence.Store
// implementations are safe for concurrent use.
type ReplayScorer struct {
	// store is the shared evidence store the RuntimeObserver writes to.
	// A nil store makes every score fall back to the prior.
	store evidence.Store
	// prior supplies the cold-start score for a strategy with no history in
	// the requested window. Nil prior falls back to neutralPriorScore.
	prior func() float64
	// limit caps the records read per window query.
	limit int
}

// neutralPriorScore is used when no prior func is wired. It matches the
// deterministic scorer's neutral prior so the two agree on "no information".
const neutralPriorScore = 0.5

// replayQueryLimit caps one window's evidence read. Windows are short
// (replayWindowSpan), so this bounds a pathological burst rather than normal
// traffic.
const replayQueryLimit = 200

// NewReplayScorer creates a zero-LLM per-strategy replay scorer.
//
// Args:
//
//	store - the shared evidence store (may be nil: every score is the prior).
//	prior - cold-start score source for strategies with no history in the
//	        window; nil uses the neutral 0.5.
//
// Returns:
//
//	*ReplayScorer - the configured scorer.
func NewReplayScorer(store evidence.Store, prior func() float64) *ReplayScorer {
	return &ReplayScorer{store: store, prior: prior, limit: replayQueryLimit}
}

// Score returns the strategy's mean historical fitness in [0,1] for the
// replay window carried by ctx, or the cold-start prior when the strategy has
// no records in that window.
//
// Args:
//
//	ctx - carries the replay window (see withReplayWindow) and cancellation.
//	s   - the strategy to score; nil returns the prior.
//
// Returns:
//
//	float64 - the score in [0,1].
func (r *ReplayScorer) Score(ctx context.Context, s *mutation.Strategy) float64 {
	if r == nil {
		return neutralPriorScore
	}
	if s == nil || s.ID == "" || r.store == nil {
		return r.coldStart()
	}
	w := replayWindowFrom(ctx)
	evs, err := r.store.Query(ctx, evidence.Filter{
		Source: observerEvidenceSource,
		Kind:   evidence.KindFitness,
		Since:  w.Since,
		Until:  w.Until,
		Limit:  r.limit,
	})
	if err != nil {
		// A store error is missing information, not evidence of quality:
		// fall back to the prior so both sides of the comparison degrade
		// identically instead of penalizing whichever side failed.
		return r.coldStart()
	}
	sum, count := 0.0, 0
	for _, ev := range evs {
		if len(ev.Payload) == 0 {
			continue
		}
		var fe struct {
			Value      float64 `json:"value"`
			StrategyID string  `json:"strategy_id"`
		}
		if err := json.Unmarshal(ev.Payload, &fe); err != nil {
			continue
		}
		if fe.StrategyID != s.ID {
			continue
		}
		if fe.Value < 0 || fe.Value > 1 {
			continue
		}
		sum += fe.Value
		count++
	}
	if count == 0 {
		return r.coldStart()
	}
	return sum / float64(count)
}

// HasStore reports whether an evidence store is wired. Callers use it to
// decide whether replay is possible at all before advertising the scorer as
// an independent evidence source — a nil store would make every comparison a
// prior-vs-prior tie, i.e. the tie deadlock this scorer exists to remove.
func (r *ReplayScorer) HasStore() bool {
	return r != nil && r.store != nil
}

// coldStart returns the configured prior clamped to [0,1].
func (r *ReplayScorer) coldStart() float64 {
	if r == nil || r.prior == nil {
		return neutralPriorScore
	}
	v := r.prior()
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
