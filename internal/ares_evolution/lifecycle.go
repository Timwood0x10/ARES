// lifecycle.go provides the StrategyLifecycle — the sole orchestrator that
// can change the active strategy. It implements the candidate state machine:
//
//	CANDIDATE → SHADOW → ACTIVE → DEGRADED → (rollback to previous)
//
// The lifecycle is the single submission entry point: only Submit(candidate)
// can change the active strategy. GA's deployBestStrategy now calls Submit
// instead of Deploy directly (B2 fix). Before promoting, the lifecycle runs
// four serial verify gates (B2/B3 fix). After promotion, a background watch
// loop feeds real runtime samples into RollbackPolicy and triggers
// Rollback when degradation is detected (B1 fix).
//
// The lifecycle is nil-safe: when not wired (legacy mode), the adapter
// falls back to the old unconditional deploy path, preserving prior behavior.
package evolution

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/evidence"
)

// CandidateState identifies where a candidate strategy is in the lifecycle.
type CandidateState int

const (
	// StateCandidate is the initial state: GA produced a candidate but it
	// has not passed any verification gate yet.
	StateCandidate CandidateState = iota
	// StateShadow means the candidate is undergoing shadow evaluation.
	// It is NOT visible to the live agent.
	StateShadow
	// StateActive means the candidate has been promoted to the active
	// strategy. The live agent reads it via GetActiveStrategy.
	StateActive
	// StateDegraded means the active strategy's runtime performance has
	// dropped below the rollback threshold and Rollback is pending.
	StateDegraded
)

// String returns the human-readable name of the candidate state.
func (s CandidateState) String() string {
	switch s {
	case StateCandidate:
		return "candidate"
	case StateShadow:
		return "shadow"
	case StateActive:
		return "active"
	case StateDegraded:
		return "degraded"
	default:
		return "unknown"
	}
}

// VerifyGate is a single verification checkpoint in the promote pipeline.
// Each gate returns whether the candidate passed, a normalized score (when
// applicable), and a human-readable reason for rejection.
type VerifyGate interface {
	// Name identifies the gate (e.g. "guardrail", "shadow", "eval").
	Name() string
	// Check evaluates the candidate against the currently active strategy.
	// Returns pass=true when the candidate may proceed to the next gate.
	Check(ctx context.Context, cand, active *mutation.Strategy) (pass bool, score float64, reason string)
}

// LifecycleConfig groups all StrategyLifecycle settings.
type LifecycleConfig struct {
	// Enabled activates the lifecycle orchestrator. When false, Submit
	// falls back to the legacy direct-deploy path (backward compatible).
	Enabled bool `json:"enabled"`
	// FitnessWindow is the number of runtime samples to keep for rollback
	// evaluation.
	FitnessWindow int `json:"fitness_window"`
	// MinSamplesBeforeJudge is the minimum runtime sample count before
	// promote/rollback decisions are made.
	MinSamplesBeforeJudge int `json:"min_samples_before_judge"`
	// ColdStartScore is the fallback fitness when no evidence exists.
	ColdStartScore float64 `json:"cold_start_score"`
	// Weights controls per-source fitness contribution.
	Weights FitnessWeights `json:"weights"`
	// Penalty configures cost/latency deductions.
	Penalty FitnessPenaltyConfig `json:"penalty"`
	// Shadow configures shadow evaluation thresholds.
	Shadow ShadowEvaluationConfig `json:"shadow"`
	// Rollback configures degradation detection.
	Rollback RollbackPolicyConfig `json:"rollback"`
	// Gates holds verify-gate-specific settings.
	Gates GateConfig `json:"gates"`
}

// GateConfig groups verify-gate thresholds.
type GateConfig struct {
	// EvalMinScore is the minimum G3 (eval suite) score for a candidate to
	// pass. Set to 0 to disable the eval gate.
	EvalMinScore float64 `json:"eval_min_score"`
	// RequireManualApproval, when true, holds candidates in SHADOW until an
	// external API call explicitly approves them (P2-4).
	RequireManualApproval bool `json:"require_manual_approval"`
}

// DefaultLifecycleConfig returns sensible defaults matching the design doc.
func DefaultLifecycleConfig() LifecycleConfig {
	shadowCfg := DefaultShadowEvaluationConfig()
	shadowCfg.Enabled = true
	shadowCfg.MinSamples = 20
	shadowCfg.MinWinRate = 0.55
	return LifecycleConfig{
		Enabled:               true,
		FitnessWindow:         50,
		MinSamplesBeforeJudge: 10,
		ColdStartScore:        0.5,
		Weights:               DefaultFitnessWeights(),
		Shadow:                shadowCfg,
		Rollback: RollbackPolicyConfig{
			Enabled:              true,
			DegradationThreshold: 0.15,
			WindowSize:           5,
			MinSamples:           3,
		},
		Gates: GateConfig{
			EvalMinScore: 0.7,
		},
	}
}

// lifecycleSnapshot is a point-in-time copy of the lifecycle state for
// the HTTP /evolution/lifecycle endpoint (P2-2).
type LifecycleSnapshot struct {
	ActiveID        string  `json:"active_id"`
	PreviousID      string  `json:"previous_id,omitempty"`
	ShadowID        string  `json:"shadow_id,omitempty"`
	State           string  `json:"state"`
	WindowScore     float64 `json:"window_score"`
	WindowCount     int     `json:"window_count"`
	Generation      int     `json:"generation"`
	LastDecision    string  `json:"last_decision,omitempty"`
	PendingApproval bool    `json:"pending_approval,omitempty"`
}

// StrategyLifecycle is the sole orchestrator that can change the active
// strategy. It owns the candidate state machine, the verify gates, and the
// rollback watch loop.
type StrategyLifecycle struct {
	asm     *ActiveStrategyManager
	agg     *RuntimeFitnessAggregator
	shadow  *ShadowEvaluator
	metrics *ares_observability.PrometheusMetrics
	evStore evidence.Store

	cfg LifecycleConfig

	mu sync.Mutex
	// state holds the current candidate's lifecycle state.
	state CandidateState
	// currentCandidate is the strategy currently being evaluated or deployed.
	currentCandidate *mutation.Strategy
	// generation is the GA generation that produced the current candidate.
	generation int
	// blacklist holds strategy IDs that were rolled back and are banned
	// from re-nomination for the current generation window.
	blacklist map[string]int // strategyID → generation when blacklisted
	// cancel stops the watch loop.
	cancel context.CancelFunc
	// lastDecision is the reason for the most recent promote/rollback.
	lastDecision string

	// pendingApproval is set when RequireManualApproval is true and the
	// candidate is held in SHADOW awaiting an external Approve call (P2-4).
	pendingApproval bool
	// approvalCh is the channel that Approve() sends on to unblock a
	// waiting Submit call (P2-4). Allocated on first Submit when
	// RequireManualApproval is true.
	approvalCh chan struct{}

	// gates holds the ordered verify gates.
	gates []VerifyGate
}

// LifecycleOption configures a StrategyLifecycle.
type LifecycleOption func(*StrategyLifecycle)

// WithLifecycleGates sets the ordered verify gates (G1..G4). When set, they
// replace the default gate set. Gates are evaluated in order: the first
// failure short-circuits the pipeline.
func WithLifecycleGates(gates ...VerifyGate) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.gates = append(l.gates, gates...)
	}
}

// WithLifecycleShadowEvaluator attaches a ShadowEvaluator for the G2 gate.
// When set, the lifecycle uses it for shadow evaluation instead of
// DreamCycle (B3 fix).
func WithLifecycleShadowEvaluator(se *ShadowEvaluator) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.shadow = se
	}
}

// WithLifecycleMetrics attaches Prometheus metrics for promote/rollback
// counters (P2-1).
func WithLifecycleMetrics(m *ares_observability.PrometheusMetrics) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.metrics = m
	}
}

// WithLifecycleEvidenceStore attaches the shared evidence store so the
// lifecycle can read runtime fitness evidence and write promote/rollback
// events. It also injects the store into the RuntimeFitnessAggregator so
// its Window queries return real evidence instead of always returning
// ok=false (the aggregator may have been created with a nil store at
// NewWiredEvolutionSystem time because the shared store is not yet known).
func WithLifecycleEvidenceStore(store evidence.Store) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.evStore = store
		if l.agg != nil {
			l.agg.SetStore(store)
		}
	}
}

// NewStrategyLifecycle creates the sole strategy orchestrator. It wraps the
// ActiveStrategyManager (which owns Deploy/Rollback) so the lifecycle is
// the only caller of those methods (B1 fix).
func NewStrategyLifecycle(
	asm *ActiveStrategyManager,
	agg *RuntimeFitnessAggregator,
	cfg LifecycleConfig,
	opts ...LifecycleOption,
) *StrategyLifecycle {
	l := &StrategyLifecycle{
		asm:       asm,
		agg:       agg,
		cfg:       cfg,
		blacklist: make(map[string]int),
		state:     StateActive, // start in active state (no candidate pending)
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Start launches the rollback watch loop. It is idempotent. The loop runs
// until ctx is cancelled or Stop is called.
func (l *StrategyLifecycle) Start(ctx context.Context) {
	if l == nil || !l.cfg.Enabled {
		return
	}
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return
	}
	watchCtx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.mu.Unlock()

	go l.watch(watchCtx)
}

// Stop cancels the watch loop.
func (l *StrategyLifecycle) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Submit is the single entry point for GA to propose a new strategy. It
// replaces the old deployBestStrategy unconditional Deploy call (B2 fix).
// The candidate goes through the verify-gate pipeline before being
// promoted to ACTIVE. If any gate fails, the candidate is discarded and the
// active strategy remains unchanged.
func (l *StrategyLifecycle) Submit(ctx context.Context, candidate *mutation.Strategy, generation int) {
	if l == nil || !l.cfg.Enabled || candidate == nil {
		return
	}

	// Check blacklist: candidates rolled back in this generation window
	// are banned from re-nomination.
	l.mu.Lock()
	if gen, blacklisted := l.blacklist[candidate.ID]; blacklisted && gen >= generation {
		l.mu.Unlock()
		el.Info(ctx, "lifecycle.Submit", "candidate is blacklisted, skipping",
			"strategy_id", candidate.ID,
			"generation", generation,
		)
		return
	}
	l.state = StateCandidate
	l.currentCandidate = candidate
	l.generation = generation
	l.mu.Unlock()

	el.Info(ctx, "lifecycle.Submit", "candidate submitted",
		"strategy_id", candidate.ID,
		"generation", generation,
		"score", candidate.Score,
	)

	// Run the verify-gate pipeline.
	active := l.asm.Current()
	for _, gate := range l.gates {
		pass, score, reason := gate.Check(ctx, candidate, active)
		if !pass {
			el.Info(ctx, "lifecycle.Submit", "gate rejected candidate",
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
		el.Debug(ctx, "lifecycle.Submit", "gate passed",
			"gate", gate.Name(),
			"strategy_id", candidate.ID,
			"score", score,
		)
	}

	// P2-4: when manual approval is required, hold the candidate in
	// SHADOW and block until Approve() is called. The candidate is not
	// promoted until an external caller signals approval.
	if l.cfg.Gates.RequireManualApproval {
		l.mu.Lock()
		l.state = StateShadow
		l.pendingApproval = true
		if l.approvalCh == nil {
			l.approvalCh = make(chan struct{}, 1)
		}
		ch := l.approvalCh
		l.mu.Unlock()
		el.Info(ctx, "lifecycle.Submit", "candidate held for manual approval",
			"strategy_id", candidate.ID,
			"generation", generation,
		)
		select {
		case <-ch:
			l.mu.Lock()
			l.pendingApproval = false
			l.mu.Unlock()
		case <-ctx.Done():
			l.mu.Lock()
			l.pendingApproval = false
			l.state = StateActive
			l.currentCandidate = nil
			l.mu.Unlock()
			return
		}
	}

	// All gates passed (and approval received when required): promote to ACTIVE.
	l.promote(ctx, candidate)
}

// Approve releases a candidate held in SHADOW by RequireManualApproval (P2-4).
// It is a no-op when no candidate is pending approval. The next Submit call
// (if blocked) will unblock and proceed to promote.
func (l *StrategyLifecycle) Approve() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pendingApproval && l.approvalCh != nil {
		select {
		case l.approvalCh <- struct{}{}:
		default:
		}
	}
}

// promote deploys the candidate as the new active strategy and resets the
// rollback window (B1 fix: previous is preserved by ActiveStrategyManager.Deploy).
func (l *StrategyLifecycle) promote(ctx context.Context, candidate *mutation.Strategy) {
	if l == nil || l.asm == nil {
		return
	}
	if err := l.asm.Deploy(ctx, candidate); err != nil {
		el.Warn(ctx, "lifecycle.promote", "deploy failed, keeping current active",
			"strategy_id", candidate.ID,
			"error", err,
		)
		l.mu.Lock()
		l.state = StateActive
		l.currentCandidate = nil
		l.lastDecision = fmt.Sprintf("deploy_failed: %s", err)
		l.mu.Unlock()
		if l.metrics != nil {
			l.metrics.RecordEvolutionPromote("deploy_failed")
		}
		return
	}
	l.mu.Lock()
	l.state = StateActive
	l.currentCandidate = candidate
	l.lastDecision = "promoted"
	l.mu.Unlock()

	if l.metrics != nil {
		l.metrics.RecordEvolutionPromote("success")
		l.metrics.RecordEvolutionDeploy("promoted")
	}
	l.writeDecisionEvidence(ctx, "promote", candidate.ID, candidate.Score, "")
	el.Info(ctx, "lifecycle.promote", "strategy promoted to active",
		"strategy_id", candidate.ID,
		"score", candidate.Score,
	)
}

// watch is the background loop that feeds runtime samples into the
// RollbackPolicy and triggers Rollback when degradation is detected (B1 fix).
// It also resets the rollback policy window after a promote to avoid stale
// scores from the previous strategy influencing the new one.
func (l *StrategyLifecycle) watch(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.evaluateAndMaybeRollback(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// evaluateAndMaybeRollback queries the aggregator for the current window
// fitness, feeds it into RollbackPolicy.RecordScore, and triggers Rollback
// when degradation is detected.
func (l *StrategyLifecycle) evaluateAndMaybeRollback(ctx context.Context) {
	if l.agg == nil || l.asm == nil {
		return
	}

	active := l.asm.Current()
	if active == nil {
		return
	}

	mean, count, ok := l.agg.Window(ctx, active.ID)
	if !ok || count == 0 {
		return
	}

	// Clamp to [0,1] before feeding RollbackPolicy (B1 fix: dimensional
	// consistency — RollbackPolicy threshold is 0.15 on a [0,1] scale).
	score := clamp01(mean)

	l.mu.Lock()
	gen := l.generation
	l.mu.Unlock()

	l.asm.RecordScore(gen, score)

	// Evaluate degradation.
	decision := l.asm.RollbackPolicy().Evaluate()
	if decision == nil || !decision.ShouldRollback {
		return
	}

	// Trigger rollback.
	prev, err := l.asm.Rollback(ctx)
	if err != nil {
		el.Warn(ctx, "lifecycle.watch", "rollback failed",
			"active_id", active.ID,
			"error", err,
		)
		if l.metrics != nil {
			l.metrics.RecordEvolutionRollback("failed")
			l.metrics.RecordEvolutionDeploy("rollback_failed")
		}
		return
	}

	// Blacklist the degraded candidate so it's not re-nominated.
	l.mu.Lock()
	if l.currentCandidate != nil {
		l.blacklist[l.currentCandidate.ID] = gen
	}
	rolledBackID := active.ID
	rolledBackScore := active.Score
	l.state = StateActive
	l.currentCandidate = nil
	l.lastDecision = fmt.Sprintf("rollback: %s", decision.Reason)
	l.mu.Unlock()

	// Reset the rollback window so the new (previous) strategy gets a
	// clean baseline.
	l.asm.RollbackPolicy().Reset()

	if l.metrics != nil {
		l.metrics.RecordEvolutionRollback("degradation")
		l.metrics.RecordEvolutionDeploy("rollback")
	}
	// P2-3: write the rollback decision into the evidence store so the
	// knowledge graph's EvolutionProvider and the GA scorer can consume
	// the decision trail. active.ID is the strategy that was rolled back.
	l.writeDecisionEvidence(ctx, "rollback", rolledBackID, rolledBackScore, decision.Reason)
	el.Info(ctx, "lifecycle.watch", "strategy rolled back",
		"active_id", active.ID,
		"restored_id", prev.ID,
		"reason", decision.Reason,
		"degradation", decision.Degradation,
		"threshold", decision.Threshold,
	)
}

// Snapshot returns a point-in-time copy of the lifecycle state for
// observability (P2-2 HTTP endpoint).
func (l *StrategyLifecycle) Snapshot() LifecycleSnapshot {
	if l == nil {
		return LifecycleSnapshot{State: "disabled"}
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	snap := LifecycleSnapshot{
		State:           l.state.String(),
		Generation:      l.generation,
		LastDecision:    l.lastDecision,
		PendingApproval: l.pendingApproval,
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
	if l.agg != nil {
		mean, count, _ := l.agg.Window(context.Background(), snap.ActiveID)
		snap.WindowScore = mean
		snap.WindowCount = count
	}
	return snap
}

// LifecycleSnapshot returns the lifecycle state as a JSON-friendly map
// for the introspect ControlServer /api/evolution/lifecycle endpoint
// (P2-2). It satisfies the introspect.LifecycleSnapshotProvider interface
// without creating an import cycle (introspect does not import ares_evolution).
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
	}
	return m
}

// recordGateReject increments the gate-reject metric (P2-1).
func (l *StrategyLifecycle) recordGateReject(gateName, reason string) {
	if l.metrics == nil {
		return
	}
	l.metrics.RecordEvolutionGateReject(gateName)
	// Also fire the legacy guardrail counter for backward-compatible
	// dashboards that still watch ARES_evolution_guardrail_total.
	l.metrics.RecordEvolutionGuardrail("gate_reject_" + gateName)
}

// writeDecisionEvidence writes a KindFitness evidence record (source=
// "strategy") for promote/rollback events so the knowledge graph's
// EvolutionProvider (P2-3) and the GA scorer can consume the decision
// trail. The evidence payload includes the action, strategy ID, score,
// and optional reason (for rollbacks).
func (l *StrategyLifecycle) writeDecisionEvidence(ctx context.Context, action, strategyID string, score float64, reason string) {
	if l.evStore == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"action":      action,
		"value":       score,
		"strategy_id": strategyID,
		"reason":      reason,
		"timestamp":   time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return
	}
	_ = l.evStore.Append(ctx, evidence.Evidence{
		ID:        "strategy_decision_" + action + "_" + strategyID + "_" + time.Now().Format("150405.000000"),
		Source:    "strategy",
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
