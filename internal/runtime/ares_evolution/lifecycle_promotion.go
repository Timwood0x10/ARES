package evolution

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// Submit is the single entry point for GA to propose a new strategy. It
// replaces the old deployBestStrategy unconditional Deploy call.
// The candidate goes through the verify-gate pipeline before being
// promoted to ACTIVE. If any gate fails, the candidate is discarded and the
// active strategy remains unchanged.
//
// Two special cases:
//
//   - Seed deploy: when NO strategy is active yet there is nothing to
//     shadow-compare against, so the first candidate is promoted without
//     gates (it becomes the "previous" baseline that rollback relies on).
//     Every subsequent candidate must earn promotion through the gates.
//   - Manual approval: when RequireManualApproval is set, the
//     candidate is HELD in SHADOW and Submit RETURNS immediately — the
//     candidate waits, never the caller's goroutine (the ticker/adapter
//     path must not block on human latency).
func (l *StrategyLifecycle) Submit(ctx context.Context, candidate *mutation.Strategy, generation int) {
	if l == nil || !l.cfg.Enabled || candidate == nil {
		return
	}

	// Seed deploy: no active strategy → nothing to verify against. Promote
	// unconditionally so the baseline exists (the seed
	// baseline is always available as `previous`).
	//
	// The exemption is a ONE-SHOT flag, not derived from asm.Current()==nil:
	// if the ASM were ever reset (or its store emptied) mid-flight, a
	// Current()==nil test would let the next candidate skip ALL gates again.
	// Once seeded, every candidate must earn promotion.
	// Note: an ASM that ALREADY holds an externally deployed strategy is
	// "born seeded" — the first Submit runs the gates against it.
	hasActive := l.asm != nil && l.asm.Current() != nil

	l.mu.Lock()
	if !l.seeded {
		l.seeded = true
		if !hasActive {
			l.mu.Unlock()
			log.InfoContext(ctx, "no active strategy, promoting seed baseline without gates",
				"method", "lifecycle.Submit",
				"strategy_id", candidate.ID,
				"generation", generation,
			)
			l.promote(ctx, candidate, false)
			return
		}
	}

	// Promote throttle: a promoted strategy must stay active for
	// MinActiveDuration before another candidate may replace it. Without it
	// the GA ticker rotates strategies faster than the rollback window
	// accumulates evidence — degradation becomes undetectable in principle.
	// Checked BEFORE the gate chain so a throttled candidate never burns gate
	// evaluations; the rejection is observable on the shared gate-reject
	// counter under the min_active_duration gate name.
	if wait := l.residencyRemainingLocked(time.Now()); wait > 0 {
		l.mu.Unlock()
		reason := fmt.Sprintf("min active duration not elapsed (%s remaining)", wait.Truncate(time.Second))
		log.InfoContext(ctx, "candidate rejected: promote throttle", "method", "lifecycle.Submit",
			"strategy_id", candidate.ID,
			"generation", generation,
			"reason", reason,
		)
		l.recordGateReject(gateMinActiveDuration, reason)
		return
	}

	// A candidate is already awaiting manual approval: reject new
	// submissions until an operator decides (replacing the held candidate
	// silently would defeat the gate).
	if l.pendingApproval {
		heldID := l.heldCandidateIDLocked()
		l.mu.Unlock()
		log.InfoContext(ctx, "manual approval pending, rejecting new candidate", "method", "lifecycle.Submit",
			"strategy_id", candidate.ID,
			"generation", generation,
			"held_id", heldID,
		)
		return
	}

	// Check blacklist: candidates rolled back within the ban window
	// (rollBackGen + N generations) are banned from re-nomination
	// (rollback-oscillation damping). Entries are pruned once the submitted
	// generation reaches the ban-lift generation.
	for id, banUntil := range l.blacklist {
		if banUntil <= generation {
			delete(l.blacklist, id)
		}
	}
	if banUntil, blacklisted := l.blacklist[candidate.ID]; blacklisted && generation < banUntil {
		l.mu.Unlock()
		log.InfoContext(ctx, "candidate is blacklisted, skipping", "method", "lifecycle.Submit",
			"strategy_id", candidate.ID,
			"generation", generation,
			"ban_until_generation", banUntil,
		)
		return
	}
	l.state = StateCandidate
	l.currentCandidate = candidate
	l.generation = generation
	l.mu.Unlock()

	log.InfoContext(ctx, "candidate submitted", "method", "lifecycle.Submit",
		"strategy_id", candidate.ID,
		"generation", generation,
		"score", candidate.Score,
	)

	active := l.asm.Current()

	// Prime the task-level shadow feeder (when wired) so the shadow gate
	// has candidate-vs-active comparison evidence to judge. Must run AFTER
	// the candidate record is set and BEFORE the gates. No-op when no
	// sampler is wired or no independent scorer exists (stays fail-closed).
	if l.sampler != nil {
		l.sampler.Prime(ctx, candidate, active)
	}

	// Run the verify-gate pipeline.
	for _, gate := range l.gates {
		pass, score, reason := gate.Check(ctx, candidate, active)
		if !pass {
			log.InfoContext(ctx, "gate rejected candidate", "method", "lifecycle.Submit",
				"gate", gate.Name(),
				"strategy_id", candidate.ID,
				"score", score,
				"reason", reason,
			)
			l.recordGateReject(gate.Name(), reason)
			l.mu.Lock()
			l.state = StateActive
			l.currentCandidate = nil
			l.mu.Unlock()
			return
		}
		log.DebugContext(ctx, "gate passed", "method", "lifecycle.Submit",
			"gate", gate.Name(),
			"strategy_id", candidate.ID,
			"score", score,
		)
	}

	// When manual approval is required, HOLD the candidate in SHADOW
	// and return immediately. The candidate sits pending until Approve()
	// promotes it (or a later Submit replaces it after approval/rejection).
	// Blocking here would stall the whole evolution heartbeat: the call
	// chain is bootstrap ticker → scheduler.Tick → adapter.Run → Submit,
	// and a human response can take hours.
	if l.cfg.Gates.RequireManualApproval {
		l.mu.Lock()
		l.state = StateShadow
		l.pendingApproval = true
		l.heldCandidate = candidate
		l.heldGeneration = generation
		l.mu.Unlock()
		log.InfoContext(ctx, "candidate held for manual approval", "method", "lifecycle.Submit",
			"strategy_id", candidate.ID,
			"generation", generation,
		)
		return
	}

	// All gates passed (and no hold requested): promote to ACTIVE.
	l.promote(ctx, candidate, true)
}

// residencyRemainingLocked reports how much of the current strategy's minimum
// active duration is still outstanding (0 = the next candidate may be judged).
// Caller holds l.mu. A zero activeSince (externally deployed or seed baseline)
// never throttles: there is no judged promote to protect yet.
func (l *StrategyLifecycle) residencyRemainingLocked(now time.Time) time.Duration {
	if l.activeSince.IsZero() {
		return 0
	}
	d := l.cfg.minActiveDuration(l.cfg.WatchInterval)
	if d <= 0 {
		return 0
	}
	if elapsed := now.Sub(l.activeSince); elapsed >= d {
		return 0
	} else {
		return d - elapsed
	}
}

// heldCandidateIDLocked returns the held candidate's ID; caller holds l.mu.
func (l *StrategyLifecycle) heldCandidateIDLocked() string {
	if l.heldCandidate == nil {
		return ""
	}
	return l.heldCandidate.ID
}

// Approve promotes the candidate held in SHADOW by RequireManualApproval.
// It is a no-op when no candidate is pending.
//
// Concurrency: "take and clear" happen in ONE critical
// section, so exactly one caller of N concurrent approvals receives the
// candidate and promotes it — the losers return with cand == nil. The
// previous two-phase (read → unlock → promote) let two concurrent
// POST /api/evolution/approve calls both promote the same strategy, which
// made ActiveStrategyManager set previous = current = that strategy:
// subsequent rollbacks would "succeed" while restoring the strategy to
// itself, and degradation could never be undone. The HTTP handler's 409
// pre-check stays purely as a friendlier early error, not a correctness
// device. Approve carries no request context, so the promote runs under a
// bounded background context.
func (l *StrategyLifecycle) Approve() {
	if l == nil {
		return
	}
	l.mu.Lock()
	cand := l.heldCandidate
	l.heldCandidate = nil
	l.pendingApproval = false
	l.mu.Unlock()
	if cand == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l.promote(ctx, cand, true)
}

// promote deploys the candidate as the new active strategy and resets the
// rollback window (previous is preserved by ActiveStrategyManager.Deploy).
// startResidency controls whether this promote starts the MinActiveDuration
// clock: gated Submit/Approve promotions do, the one-shot seed deploy does not
// (it is the seed baseline, not a judged promote).
func (l *StrategyLifecycle) promote(ctx context.Context, candidate *mutation.Strategy, startResidency bool) {
	if l == nil || l.asm == nil {
		return
	}
	if err := l.asm.Deploy(ctx, candidate); err != nil {
		log.WarnContext(ctx, "deploy failed, keeping current active", "method", "lifecycle.promote",
			"strategy_id", candidate.ID,
			"error", err,
		)
		l.mu.Lock()
		l.state = StateActive
		l.currentCandidate = nil
		l.lastDecision = fmt.Sprintf("deploy_failed: %s", err)
		l.lastWindowAt = time.Time{}
		l.mu.Unlock()
		// Deploy can internally roll the ASM back to `previous` when the
		// post-evolve guardrail stops it — the ACTIVE strategy may have
		// changed even though this promote failed. Reset the rollback
		// window so the (possibly new) active strategy is not judged on the
		// previous strategy's stale scores (same reasoning as the promote
		// path above; conservative direction).
		l.asm.RollbackPolicy().Reset()
		if l.metrics != nil {
			l.metrics.RecordEvolutionPromote("deploy_failed")
		}
		return
	}
	l.mu.Lock()
	l.state = StateActive
	l.currentCandidate = candidate
	l.lastDecision = "promoted"
	// Reset the rollback window on EVERY promote, not
	// only on rollback. The old strategy's low scores are still in
	// scoreHistory right after a promote; without the reset the new strategy
	// could be judged as a sudden drop on its very first watch tick using
	// the PREVIOUS strategy's evidence. The decorrelation timestamp resets
	// with it so the new strategy records on its first tick.
	l.lastWindowAt = time.Time{}
	if startResidency {
		l.activeSince = time.Now()
	}
	l.mu.Unlock()

	l.asm.RollbackPolicy().Reset()

	if l.metrics != nil {
		l.metrics.RecordEvolutionPromote("success")
		l.metrics.RecordEvolutionDeploy("promoted")
	}
	l.writeDecisionEvidence(ctx, "promote", candidate.ID, candidate.Score, "")
	log.InfoContext(ctx, "strategy promoted to active", "method", "lifecycle.promote",
		"strategy_id", candidate.ID,
		"score", candidate.Score,
	)
}

// watch is the background loop that feeds runtime samples into the
// RollbackPolicy and triggers Rollback when degradation is detected.
// The rollback window itself is reset on every promote (see promote) so the
// new strategy is judged from a clean baseline.
func (l *StrategyLifecycle) watch(ctx context.Context) {
	interval := l.cfg.WatchInterval
	if interval <= 0 {
		interval = defaultWatchInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.recordWatchGauges()
			l.evaluateAndMaybeRollback(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// recordWatchGauges publishes the per-tick observability gauges: how
// long the current strategy has been active. Purely observational — it must
// never influence the rollback decision.
func (l *StrategyLifecycle) recordWatchGauges() {
	if l.metrics == nil {
		return
	}
	l.mu.Lock()
	since := l.activeSince
	l.mu.Unlock()
	if since.IsZero() {
		return
	}
	l.metrics.SetEvolutionActiveDuration(time.Since(since).Seconds())
}
