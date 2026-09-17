package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/evidence"
)

// evaluateAndMaybeRollback queries the aggregator for the current window
// fitness, feeds it into RollbackPolicy.RecordScore, and triggers Rollback
// when degradation is detected.
func (l *StrategyLifecycle) evaluateAndMaybeRollback(ctx context.Context) {
	if l.agg == nil || l.asm == nil {
		return
	}
	// Rollback disarm: the YAML rollback.enabled=false path removes the
	// post-deployment safety net by explicit operator decision. The watch
	// loop then records nothing and never triggers — promotion risk was
	// accepted up front (and the shadow-gate invariant re-arms the shadow
	// gate fail-closed for exactly this configuration).
	if !l.cfg.RollbackArmed {
		return
	}

	active := l.asm.Current()
	if active == nil {
		return
	}

	res := l.agg.Window(ctx, active.ID)
	if !res.Ok || res.Count == 0 {
		return
	}
	// Window-sample gauge split by strategy — attribution health is
	// visible at a glance. If the evidence stamping ever breaks, all
	// samples pile up under one strategy_id label value instead of
	// distributing.
	if l.metrics != nil {
		l.metrics.SetEvolutionWindowSamples(active.ID, "strategy", res.Count)
	}
	l.mu.Lock()
	// Decorrelation: record a score ONLY when the
	// evidence window advanced since the previous tick. Re-averaging the
	// same batch of evidence every tick would make the RollbackPolicy
	// window a set of highly self-correlated copies of one snapshot —
	// sudden drops get smoothed away and the gradual-decline detector
	// fires on noise.
	//
	// The advance signal is the window's NEWEST evidence timestamp, NOT
	// the record count: each source's count saturates at WindowSize and
	// then stays flat under steady-state churn ("one in, one out") — a
	// count-based check silently stops feeding RollbackPolicy forever
	// once every source fills up (no error, no warning; /api/evolution/
	// lifecycle would even show a healthy window_count). The timestamp is
	// reset to zero on promote so the new strategy records on its first
	// tick.
	if !res.LastAt.After(l.lastWindowAt) {
		l.mu.Unlock()
		return
	}
	l.lastWindowAt = res.LastAt
	gen := l.generation
	l.mu.Unlock()

	// Clamp to [0,1] before feeding RollbackPolicy (dimensional
	// consistency — RollbackPolicy threshold is 0.15 on a [0,1] scale).
	score := clamp01(res.Mean)

	l.asm.RecordScore(gen, score)

	// Evaluate degradation.
	decision := l.asm.RollbackPolicy().Evaluate()
	if decision == nil || !decision.ShouldRollback {
		return
	}

	// Trigger rollback.
	prev, err := l.asm.Rollback(ctx)
	if err != nil {
		if errors.Is(err, ErrNoPreviousStrategy) {
			// Expected in the fail-closed default config: only the seed
			// deploy has happened, so previous is still nil. Log at Info
			// (with the expectation stated) instead of Warn — a long soak
			// would otherwise flood the log with a non-malfunction.
			log.InfoContext(ctx, "rollback unavailable: no previous strategy yet (expected before the second promote)",
				"method", "lifecycle.watch",
				"active_id", active.ID,
			)
			if l.metrics != nil {
				l.metrics.RecordEvolutionRollback("no_previous")
			}
			return
		}
		log.WarnContext(ctx, "rollback failed", "method", "lifecycle.watch",
			"active_id", active.ID,
			"error", err,
		)
		if l.metrics != nil {
			l.metrics.RecordEvolutionRollback("failed")
			l.metrics.RecordEvolutionDeploy("rollback_failed")
		}
		return
	}

	// Blacklist the degraded candidate for N generations (oscillation
	// damping): banUntil = current generation + N. The next Submit prunes
	// the entry once its generation reaches banUntil.
	l.mu.Lock()
	if l.currentCandidate != nil {
		l.blacklist[l.currentCandidate.ID] = gen + l.cfg.blacklistGenerations()
	}
	rolledBackID := active.ID
	rolledBackScore := active.Score
	l.state = StateActive
	l.currentCandidate = nil
	l.lastDecision = fmt.Sprintf("rollback: %s", decision.Reason)
	// The restored strategy becomes active again: restart its residency clock
	// so the promote throttle protects its fresh evidence window too.
	l.activeSince = time.Now()
	l.mu.Unlock()

	// Reset the rollback window so the new (previous) strategy gets a
	// clean baseline.
	l.asm.RollbackPolicy().Reset()

	if l.metrics != nil {
		l.metrics.RecordEvolutionRollback("degradation")
		l.metrics.RecordEvolutionDeploy("rollback")
	}
	// Write the rollback decision into the evidence
	// store. The consumer is the knowledge graph's EvolutionProvider
	// (internal/knowledge/provider/evolution/provider.go, decision-trail
	// segment) via adapter.FromDecisionEvidence. active.ID is the strategy
	// that was rolled back.
	l.writeDecisionEvidence(ctx, "rollback", rolledBackID, rolledBackScore, decision.Reason)
	log.InfoContext(ctx, "strategy rolled back", "method", "lifecycle.watch",
		"active_id", active.ID,
		"restored_id", prev.ID,
		"reason", decision.Reason,
		"degradation", decision.Degradation,
		"threshold", decision.Threshold,
	)
}

// Snapshot returns a point-in-time copy of the lifecycle state for
// observability (HTTP endpoint).
//
// The aggregator Window query (evidence-store I/O) runs OUTSIDE l.mu: the
// mutex protects the state machine fields, and holding it across store I/O
// would stall Submit/Approve/Stop while the HTTP endpoint reads. The agg
// pointer itself is immutable after construction, so reading it without the
// lock is safe.
func (l *StrategyLifecycle) Snapshot() LifecycleState {
	if l == nil {
		return LifecycleState{State: "disabled"}
	}
	l.mu.Lock()
	snap := LifecycleState{
		State:           l.state.String(),
		Generation:      l.generation,
		LastDecision:    l.lastDecision,
		PendingApproval: l.pendingApproval,
		// Gate configuration + promote-throttle posture, so an
		// operator can tell from ONE endpoint call which verification mode
		// is live and whether the rollback net is armed.
		Gates:                l.gateNamesLocked(),
		ShadowGateSkipReason: l.shadowGateSkipReason,
		ActiveSince:          l.activeSince,
		MinActiveDuration:    l.cfg.minActiveDuration(l.cfg.WatchInterval),
		RollbackArmed:        l.cfg.RollbackArmed,
	}
	if l.asm != nil {
		if cur := l.asm.Current(); cur != nil {
			snap.ActiveID = cur.ID
		}
		if prev := l.asm.Previous(); prev != nil {
			snap.PreviousID = prev.ID
		}
	}
	if l.currentCandidate != nil {
		snap.ShadowID = l.currentCandidate.ID
	}
	if l.heldCandidate != nil {
		snap.HeldID = l.heldCandidate.ID
		snap.HeldGeneration = l.heldGeneration
	}
	// Capture compileInfo under the lock for consistent reads. The
	// provider itself is thread-safe, so its methods can be called outside
	// the lock — but the field reference must be read consistently.
	compileInfo := l.compileInfo
	l.mu.Unlock()

	if l.agg != nil {
		// The Window query is evidence-store I/O on the
		// HTTP snapshot path — always bounded so a slow store cannot hang
		// the endpoint. On timeout the fields stay zero (no fabricated
		// score).
		wctx, wcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer wcancel()
		res := l.agg.Window(wctx, snap.ActiveID)
		snap.WindowScore = res.Mean
		snap.WindowCount = res.Count
	}
	// Compile provenance for the attribution chain. When not wired,
	// the fields stay zero-valued.
	if compileInfo != nil {
		snap.CompileID = compileInfo.CompileID()
		snap.DAGVersion = compileInfo.DAGVersion()
		snap.CompileCount = compileInfo.CompileCount()
	}
	return snap
}

// LifecycleSnapshot returns the lifecycle state as a JSON-friendly map
// for the introspect ControlServer /api/evolution/lifecycle endpoint.
// It satisfies the introspect.LifecycleSnapshotProvider interface
// without creating an import cycle (introspect does not import ares_evolution).
//
// Naming note: the METHOD keeps the introspect interface's name; the state
// struct it returns was renamed LifecycleSnapshot → LifecycleState so the
// type and the method no longer share one identifier.
func (l *StrategyLifecycle) LifecycleSnapshot() map[string]any {
	snap := l.Snapshot()
	m := map[string]any{
		"active_id":    snap.ActiveID,
		"state":        snap.State,
		"window_score": snap.WindowScore,
		"window_count": snap.WindowCount,
		"generation":   snap.Generation,
	}
	if snap.PreviousID != "" {
		m["previous_id"] = snap.PreviousID
	}
	if snap.ShadowID != "" {
		m["shadow_id"] = snap.ShadowID
	}
	if snap.LastDecision != "" {
		m["last_decision"] = snap.LastDecision
	}
	if snap.PendingApproval {
		m["pending_approval"] = true
		m["held_id"] = snap.HeldID
		m["held_generation"] = snap.HeldGeneration
	}
	// Gate pipeline visibility + promote-throttle posture. Rendered
	// as seconds so the JSON stays human-readable across language bindings.
	if len(snap.Gates) > 0 {
		m["gates"] = snap.Gates
	}
	if snap.ShadowGateSkipReason != "" {
		m["shadow_gate_skipped_reason"] = snap.ShadowGateSkipReason
	}
	if !snap.ActiveSince.IsZero() {
		m["active_since"] = snap.ActiveSince.Format(time.RFC3339)
	}
	if snap.MinActiveDuration > 0 {
		m["min_active_duration"] = snap.MinActiveDuration.Seconds()
	}
	m["rollback_armed"] = snap.RollbackArmed
	// Compile provenance for the attribution chain. The triplet
	// (generation, gates, compile_id) answers "which generation, which
	// gate, which compile" in a single endpoint call.
	m["dag_version"] = snap.DAGVersion
	m["compile_count"] = snap.CompileCount
	if snap.CompileID != "" {
		m["compile_id"] = snap.CompileID
	}
	return m
}

// gateNamesLocked returns the registered gate names in pipeline order.
// Caller holds l.mu.
func (l *StrategyLifecycle) gateNamesLocked() []string {
	if len(l.gates) == 0 {
		return nil
	}
	names := make([]string, 0, len(l.gates))
	for _, g := range l.gates {
		names = append(names, g.Name())
	}
	return names
}

// recordGateReject increments the gate-reject metric and records
// the decision trail (every promote/reject must leave a trace with
// {generation, gate, reason, win_rate}).
func (l *StrategyLifecycle) recordGateReject(gateName, reason string) {
	// Record the rejection in the decision trail. The generation and
	// win_rate are best-effort: the lifecycle may not know the gate's score
	// at this call site (the gate's Check already returned), so we record
	// what we have.
	l.mu.Lock()
	gen := l.generation
	l.mu.Unlock()

	l.writeDecisionEvidence(context.Background(), "reject",
		"", 0, fmt.Sprintf("gate=%s gen=%d reason=%s", gateName, gen, reason))

	if l.metrics == nil {
		return
	}
	l.metrics.RecordEvolutionGateReject(gateName)
	// Also fire the legacy guardrail counter for backward-compatible
	// dashboards that still watch ARES_evolution_guardrail_total. The code
	// label is a FIXED constant: interpolating the gate name would give the
	// legacy counter unbounded label cardinality (gate names are
	// caller-supplied via VerifyGate.Name()).
	l.metrics.RecordEvolutionGuardrail("gate_reject")
}

// writeDecisionEvidence records promote/rollback decision events with
// source="lifecycle" so the knowledge graph's EvolutionProvider can consume
// the decision trail: the provider's Stream emits
// them as ObjectDecision objects via adapter.FromDecisionEvidence, filtered
// by Source=="lifecycle" plus the payload "action" field.
//
// The source is deliberately NOT "strategy" and the score is deliberately
// NOT normalized: GA scores live on a 0–100 scale, while every fitness
// consumer (RuntimeFitnessAggregator, recentFitnessSummary) filters
// KindFitness values to [0,1]. Writing a 0–100 GA score under the
// "strategy" fitness source would (a) be silently dropped by that filter
// and (b) mix one-off decision events into the runtime fitness window
// semantics. A dedicated source keeps the decision trail queryable without
// polluting either dimension.
//
// TODO(tech-debt): decision records share KindFitness with runtime fitness
// samples, distinguished only by Source. They should separate into a
// dedicated KindDecision in 0.4.x — AFTER confirming Window's sources table
// (fitness_aggregator.go: only strategy/workflow/scheduler/recovery) is
// unaffected. Today "lifecycle" is not in that table, so decision records
// never enter rollback math either way; the separation is a semantic
// cleanup, not a correctness fix.
func (l *StrategyLifecycle) writeDecisionEvidence(ctx context.Context, action, strategyID string, score float64, reason string) {
	if l.evStore == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"action":              action,
		"value":               score,
		evidenceKeyStrategyID: strategyID,
		"reason":              reason,
		"timestamp":           time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = l.evStore.Append(ctx, evidence.Evidence{
		// Full-date format: the PG store uses ON CONFLICT (id) DO NOTHING,
		// so a time-only suffix would silently drop decision events from
		// different days colliding on the same clock reading.
		ID:        "strategy_decision_" + action + "_" + strategyID + "_" + time.Now().Format("20060102150405.000000"),
		Source:    "lifecycle",
		Kind:      evidence.KindFitness,
		Payload:   payload,
		Timestamp: time.Now(),
	})
}

// clamp01 clamps a float64 to the [0,1] range.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
