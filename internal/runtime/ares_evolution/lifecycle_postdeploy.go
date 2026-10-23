package evolution

import (
	"context"
	"strconv"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

const (
	// postDeployCycles is how many watch ticks the observation window stays open. It bounds the
	// latency of the answer: waiting for the full age would leave "did the promotion help"
	// unanswered for a day on a quiet system.
	postDeployCycles = 3
	// postDeployMaxAge bounds the same window in wall-clock terms: a busy system reaches
	// postDeployCycles quickly, a quiet one must not wait for ticks that never come.
	postDeployMaxAge = 24 * time.Hour
)

// postDeployWatch is one open observation window over a just-promoted strategy.
//
// The candidate is COPIED rather than referenced: the record sealed at the end must describe the
// strategy that was promoted, and a pointer would let a later mutation rewrite history in a log
// whose entire purpose is that history cannot be rewritten. (Maps inside the copy are shared —
// they are not mutated after promotion.)
type postDeployWatch struct {
	candidate  mutation.Strategy
	startedAt  time.Time
	deadline   time.Time
	cyclesLeft int
}

// openPostDeployWatch starts observing candidate after acceptance.
//
// It does nothing when no experiment log is configured: the observation exists to be recorded, and
// holding a window open for a sink that cannot receive it would just be state nobody reads.
func (l *StrategyLifecycle) openPostDeployWatch(candidate *mutation.Strategy) {
	if l == nil || candidate == nil {
		return
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.experimentLog == nil || l.proposalKey == nil {
		// Without a proposal key the observation cannot be joined to the verdict it belongs to,
		// and an unjoinable record is indistinguishable from a fresh proposal. Hold no window
		// for something that could not be recorded honestly.
		return
	}
	// A new promotion SUPERSEDES an open window: the strategy it was observing is no longer
	// active, so its post-deploy evidence describes a period that has ended. Dropping it is
	// deliberate — sealing it here would attribute the new strategy's first cycles to the old one.
	l.postDeploy = &postDeployWatch{
		candidate:  *candidate,
		startedAt:  now,
		deadline:   now.Add(postDeployMaxAge),
		cyclesLeft: postDeployCycles,
	}
}

// tickPostDeploy advances the open window and seals its outcome when the window closes.
//
// It runs on the existing watch tick rather than a timer of its own: the rollback policy already
// samples on this cadence, and a second clock would make the two disagree about what "one cycle"
// means.
func (l *StrategyLifecycle) tickPostDeploy(ctx context.Context) {
	if l == nil {
		return
	}
	l.mu.Lock()
	watch := l.postDeploy
	if watch == nil {
		l.mu.Unlock()
		return
	}
	watch.cyclesLeft--
	due := watch.cyclesLeft <= 0 || time.Now().After(watch.deadline)
	if due {
		l.postDeploy = nil
	}
	candidate := watch.candidate
	l.mu.Unlock()
	if !due {
		return
	}

	if l.agg == nil {
		return
	}
	win := l.agg.Window(ctx, candidate.ID)
	outcome := postDeployOutcome(win, candidate, time.Now())
	if outcome == nil {
		// No samples: close the window WITHOUT a record. Writing a zero-valued outcome would
		// claim "measured, and it did nothing" about a period that was never measured — the
		// same distinction Score and Count already keep elsewhere.
		return
	}

	verdict := GateVerdict{
		Gate:   "post_deploy",
		Passed: outcome.Success,
		Reason: "observed after promotion: " + strconv.Itoa(win.Count) + " fitness samples",
	}
	l.sealRecord(ctx, &candidate, ExperimentOutcomeAccepted, []GateVerdict{verdict}, outcome)
}

// postDeployOutcome turns the window into the record's PostDeploy field.
//
// Returns nil when the window holds no samples at all — neither fitness evidence nor a latency or
// cost metric. The rule is "absence stays absent": a non-nil outcome with Success=false means the
// strategy was observed and did not hold up, which is a different, much stronger claim.
func postDeployOutcome(win WindowResult, candidate mutation.Strategy, at time.Time) *StrategyOutcome {
	latency, hasLatency := win.Metrics[latencyMetricSource]
	cost, hasCost := win.Metrics[costMetricSource]
	hasLatency = hasLatency && latency.Count > 0
	hasCost = hasCost && cost.Count > 0
	if win.Count == 0 && !hasLatency && !hasCost {
		return nil
	}

	out := &StrategyOutcome{
		StrategyID: candidate.ID,
		// Success requires fitness evidence: Ok is false when the window never had enough of it,
		// and reporting that as "the promotion failed" would blame the strategy for a quiet
		// system.
		Success:      win.Count > 0 && win.Ok,
		Score:        win.Mean,
		MutationType: strconv.Itoa(int(candidate.StrategyMutationType)),
		Timestamp:    at,
	}
	if hasLatency {
		// Normalised [0,1], NOT milliseconds: the metrics channel records a cost, and the
		// scale that would turn it into wall-clock time is not part of the evidence. Copying it
		// into LatencyMs would invent a number the system never measured.
		out.LatencyCost = latency.Mean
	}
	if hasCost {
		out.SpendCost = cost.Mean
	}
	return out
}
