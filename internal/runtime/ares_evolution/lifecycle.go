// lifecycle.go provides the StrategyLifecycle — the sole orchestrator that
// can change the active strategy. It implements the candidate state machine:
//
//	CANDIDATE → SHADOW → ACTIVE → DEGRADED → (rollback to previous)
//
// The lifecycle is the single submission entry point: only Submit(candidate)
// can change the active strategy. GA's deployBestStrategy now calls Submit
// instead of Deploy directly. Before promoting, the lifecycle runs four
// serial verify gates. After promotion, a background watch loop feeds real
// runtime samples into RollbackPolicy and triggers Rollback when
// degradation is detected.
//
// NIL-SAFETY / LEGACY PATH: the lifecycle
// itself has NO unconditional deploy fallback — when Enabled is false or the
// lifecycle is not wired, Submit is a no-op and the active strategy is never
// changed through this type. The only legacy path lives in
// GenomePopulationAdapter.Run: when a.lifecycle == nil it falls back to
// deployBestStrategy (the legacy direct Deploy call) for systems built
// without a lifecycle. There is no way to bypass the shadow gate through
// the lifecycle once it IS wired: the gate is registered fail-closed and
// Submit runs every registered gate.
package evolution

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
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
//
// Note on scope: only settings the lifecycle itself consumes live here.
// Rollback thresholds and shadow thresholds are consumed by
// ActiveStrategyManager / ShadowEvaluator respectively, built from
// SystemConfig.RollbackPolicyConfig / SystemConfig.ShadowEvalConfig — they
// are deliberately NOT duplicated in this struct.
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
	// WatchInterval is the rollback watch-loop tick interval. Zero or
	// negative falls back to defaultWatchInterval.
	WatchInterval time.Duration `json:"watch_interval"`
	// BlacklistGenerations is how many generations a rolled-back candidate
	// stays banned from re-nomination (rollback oscillation damping).
	// Zero or negative falls back to defaultBlacklistGenerations.
	BlacklistGenerations int `json:"blacklist_generations"`
	// MinActiveDuration is how long a promoted strategy must stay active
	// before another candidate may replace it (promote throttling). Without
	// it the GA ticker could rotate strategies
	// faster than the rollback window accumulates evidence, making
	// degradation undetectable in principle — this is a CORRECTNESS
	// precondition of opening the promote path, not an optional optimization.
	// Zero falls back to 3 × WatchInterval, so at least three rollback
	// windows are observed between promotes. The residency clock starts at
	// the first GATED promote (the one-shot seed deploy does not start it:
	// the seed is the baseline the rollback logic relies on, and rejecting
	// the first real candidate after it would leave the loop permanently
	// empty).
	MinActiveDuration time.Duration `json:"min_active_duration"`
	// RollbackArmed reports whether the post-deployment rollback watch loop
	// may trigger an automatic Rollback. It is
	// the second half of the shadow-gate safety invariant: skipping
	// PRE-deployment verification is allowed only when POST-deployment
	// verification is armed. When false, evaluateAndMaybeRollback never
	// fires and the wiring layer must keep the shadow gate registered
	// fail-closed.
	RollbackArmed bool `json:"rollback_armed"`
	// DisableShadowGate suppresses the automatic shadow-gate registration —
	// the documented no-scorer-plus-armed-rollback case. The wiring layer
	// sets it via ShadowGateMode's decision; the lifecycle only ever sees
	// the explicit instruction (gate absence is a wiring decision, never an
	// emergent property of nil-checking). ShadowGateSkipReason records why,
	// for the snapshot and startup log — the absence must be visible.
	DisableShadowGate    bool   `json:"disable_shadow_gate"`
	ShadowGateSkipReason string `json:"shadow_gate_skip_reason,omitempty"`
	// Gates holds verify-gate-specific settings.
	Gates GateConfig `json:"gates"`
}

// GateConfig groups verify-gate thresholds.
type GateConfig struct {
	// EvalMinScore is the minimum eval-suite score for a candidate to
	// pass. Set to 0 to disable the eval gate.
	EvalMinScore float64 `json:"eval_min_score"`
	// RequireManualApproval, when true, holds candidates in SHADOW until an
	// external API call explicitly approves them. Submit returns
	// immediately — the CANDIDATE is held, never the caller's goroutine.
	RequireManualApproval bool `json:"require_manual_approval"`
}

// defaultWatchInterval is the rollback watch-loop period when
// LifecycleConfig.WatchInterval is unset.
const defaultWatchInterval = 30 * time.Second

// defaultBlacklistGenerations is the re-nomination ban window (in
// generations) applied to a rolled-back candidate when
// LifecycleConfig.BlacklistGenerations is unset.
const defaultBlacklistGenerations = 3

// defaultResidencyTicks is the default minimum-active duration, expressed in
// watch-loop ticks: a promoted strategy must survive at least three rollback
// windows before it may be replaced.
const defaultResidencyTicks = 3

// gateMinActiveDuration is the promote-throttle's pseudo-gate name, used for
// the gate-reject metric so throttled submissions are observable on the same
// counter as real gate rejections.
const gateMinActiveDuration = "min_active_duration"

// blacklistGenerations returns the effective ban window.
func (c LifecycleConfig) blacklistGenerations() int {
	if c.BlacklistGenerations > 0 {
		return c.BlacklistGenerations
	}
	return defaultBlacklistGenerations
}

// minActiveDuration returns the effective residency period. Zero falls back
// to 3 × watchInterval (the caller passes the already-defaulted interval).
func (c LifecycleConfig) minActiveDuration(watchInterval time.Duration) time.Duration {
	if c.MinActiveDuration > 0 {
		return c.MinActiveDuration
	}
	if watchInterval <= 0 {
		watchInterval = defaultWatchInterval
	}
	return defaultResidencyTicks * watchInterval
}

// DefaultLifecycleConfig returns sensible defaults matching the design doc.
func DefaultLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		Enabled:               true,
		FitnessWindow:         50,
		MinSamplesBeforeJudge: 10,
		ColdStartScore:        0.5,
		Weights:               DefaultFitnessWeights(),
		WatchInterval:         defaultWatchInterval,
		BlacklistGenerations:  defaultBlacklistGenerations,
		Gates: GateConfig{
			EvalMinScore: 0.7,
		},
	}
}

// CompileInfoProvider supplies compile provenance for the introspection
// chain. The wiring layer (cmd/ares) adapts the planprojection.
// CompileCoordinator into this interface so /api/evolution/lifecycle can
// answer "which generation, which gate, which compile" without ares_evolution
// importing planprojection (which would create a circular dependency).
//
// When not wired, the compile fields in LifecycleState stay zero-valued.
type CompileInfoProvider interface {
	// CompileID returns the most recent compile's unique identifier.
	CompileID() string
	// DAGVersion returns the live DAG's mutation counter at the last compile.
	DAGVersion() uint64
	// CompileCount returns the total number of compiles since startup.
	CompileCount() uint64
}

// lifecycleSnapshot was renamed to LifecycleState: the type name clashed with
// the LifecycleSnapshot METHOD (required by introspect.LifecycleSnapshotProvider),
// which read like two different things sharing one name.
// LifecycleState is a point-in-time copy of the lifecycle state for
// the HTTP /evolution/lifecycle endpoint.
type LifecycleState struct {
	ActiveID        string  `json:"active_id"`
	PreviousID      string  `json:"previous_id,omitempty"`
	ShadowID        string  `json:"shadow_id,omitempty"`
	State           string  `json:"state"`
	WindowScore     float64 `json:"window_score"`
	WindowCount     int     `json:"window_count"`
	Generation      int     `json:"generation"`
	LastDecision    string  `json:"last_decision,omitempty"`
	PendingApproval bool    `json:"pending_approval,omitempty"`
	// HeldID / HeldGeneration identify the candidate awaiting manual
	// approval, so an operator sees WHICH generation they are approving
	// before calling /api/evolution/approve. Zero when nothing is held.
	HeldID         string `json:"held_id,omitempty"`
	HeldGeneration int    `json:"held_generation,omitempty"`
	// Gates lists the names of the verify gates actually registered, so an
	// operator sees at a glance which verification pipeline is live.
	Gates []string `json:"gates,omitempty"`
	// ShadowGateSkipReason is non-empty when the shadow gate was
	// deliberately NOT registered (no independent scorer + rollback armed):
	// the absence is a decision and must be visible, not emergent.
	ShadowGateSkipReason string `json:"shadow_gate_skipped_reason,omitempty"`
	// ActiveSince is when the currently active strategy was promoted by this
	// lifecycle (zero for an externally deployed / seed baseline).
	ActiveSince time.Time `json:"active_since,omitempty"`
	// MinActiveDuration is the effective residency period between promotes.
	MinActiveDuration time.Duration `json:"min_active_duration,omitempty"`
	// RollbackArmed reports whether the automatic rollback watch loop may
	// trigger (the post-deployment safety net).
	RollbackArmed bool `json:"rollback_armed"`

	// Compile provenance for the attribution chain. The triplet
	// (Generation, Gates, CompileID) answers "which generation, which gate,
	// which compile" — the introspection acceptance contract. Zero values
	// when no CompileInfoProvider is wired.
	CompileID    string `json:"compile_id,omitempty"`
	DAGVersion   uint64 `json:"dag_version"`
	CompileCount uint64 `json:"compile_count"`
}

// StrategyLifecycle is the sole orchestrator that can change the active
// strategy. It owns the candidate state machine, the verify gates, and the
// rollback watch loop.
type StrategyLifecycle struct {
	asm     *ActiveStrategyManager
	agg     *RuntimeFitnessAggregator
	shadow  *ShadowEvaluator
	sampler *ShadowSampler
	metrics *observability.PrometheusMetrics
	evStore evidence.Store

	cfg LifecycleConfig

	mu sync.Mutex
	// state holds the current candidate's lifecycle state.
	state CandidateState
	// currentCandidate is the strategy currently being evaluated or deployed.
	currentCandidate *mutation.Strategy
	// generation is the GA generation that produced the current candidate.
	generation int
	// blacklist holds strategy IDs that were rolled back, mapped to the
	// generation at which the ban LIFTS (banUntil = rollBackGen + N).
	// Entries are pruned once the submitted generation passes banUntil.
	blacklist map[string]int // strategyID → generation when the ban lifts
	// cancel stops the watch loop.
	cancel context.CancelFunc
	// done is closed when the watch loop exits; Stop waits on it so a
	// shutdown sequence cannot race a late rollback decision (no
	// fire-and-forget goroutines — Start/Stop is a managed pair).
	done chan struct{}
	// lastDecision is the reason for the most recent promote/rollback.
	lastDecision string

	// heldCandidate is the strategy currently held in SHADOW awaiting an
	// external Approve() call (RequireManualApproval=true). Submit
	// stores it and RETURNS immediately — the candidate is held, not the
	// caller's goroutine: the ticker/adapter path must never block on human
	// latency. Approve() promotes it; new Submits are rejected while a hold
	// is pending. Exposed to operators via Snapshot.HeldID/HeldGeneration
	// so an approver can judge the candidate's freshness before deciding.
	heldCandidate *mutation.Strategy
	// heldGeneration is the GA generation that produced heldCandidate.
	heldGeneration int
	// pendingApproval mirrors heldCandidate != nil for cheap Snapshot reads.
	pendingApproval bool
	// lastWindowAt is the newest evidence timestamp seen by the previous
	// watch tick. RecordScore fires only when the window ADVANCES — judged
	// by this TIMESTAMP, not by the record count: each source's count
	// saturates at WindowSize (50), and under steady-state churn the count
	// stays flat forever ("one in, one out"), which would silently kill the
	// rollback feed if judged by count (12h-soak certainty, not an edge
	// case). The timestamp is reset on promote so the new strategy's first
	// window records immediately.
	lastWindowAt time.Time
	// activeSince is when the CURRENT strategy was promoted by a GATED
	// Submit/Approve. It drives the promote throttle (MinActiveDuration) and
	// the active-duration gauge. It is deliberately NOT set by the one-shot
	// seed deploy: the seed is the baseline, not a judged promote, and
	// starting the residency clock there would keep the first real candidate
	// waiting for a window that has no evidence source yet.
	activeSince time.Time
	// shadowGateSkipReason is non-empty when the wiring layer decided (via
	// WithShadowGateDisabled) NOT to register the shadow gate. Surfaced by
	// Snapshot so the absence is visible.
	shadowGateSkipReason string
	// seeded marks that the lifecycle has performed (or observed) its one
	// seed deployment. After it flips, NO candidate may skip the gate
	// pipeline — even if the ASM later reports no active strategy (reset or
	// emptied store), which would otherwise re-open the gate-free path.
	seeded bool

	// compileInfo supplies compile provenance for the attribution chain.
	// When wired, LifecycleSnapshot exposes (compile_id, dag_version,
	// compile_count) so /api/evolution/lifecycle can answer "which compile
	// produced the current task set". Nil when not wired (zero-valued fields).
	compileInfo CompileInfoProvider

	// gates holds the ordered verify gates.
	gates []VerifyGate
}

// LifecycleOption configures a StrategyLifecycle.
type LifecycleOption func(*StrategyLifecycle)

// WithLifecycleGates sets the ordered verify gates. When set, they
// replace the default gate set. Gates are evaluated in order: the first
// failure short-circuits the pipeline.
func WithLifecycleGates(gates ...VerifyGate) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.gates = append(l.gates, gates...)
	}
}

// WithLifecycleShadowEvaluator attaches a ShadowEvaluator for the shadow gate.
// When set, the lifecycle registers a shadow verify gate AHEAD of any
// explicitly supplied gates (shadow runs before eval), and the evaluator's
// accumulated comparisons are enforced fail-closed: enough samples with a
// win rate at or above the configured threshold → pass; below, or no data
// yet → reject (the data feeder — DreamCycle today, a
// task-level sampler — owns StartShadow/RecordResult; the gate is
// read-only).
func WithLifecycleShadowEvaluator(se *ShadowEvaluator) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.shadow = se
	}
}

// WithLifecycleShadowSampler attaches the task-level shadow feeder. When
// set (and an independent scorer is wired on the evaluator), Submit primes the
// sampler before running the gates so the shadow gate has comparison
// evidence to judge in default configs where DreamCycle is disabled.
func WithLifecycleShadowSampler(s *ShadowSampler) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.sampler = s
	}
}

// ShadowSampler returns the wired task-level shadow feeder, or nil. The serve
// layer uses it to attach the real-execution A/B feeder, which needs the
// serve-time cognition stack and is therefore constructed after the
// evolution system.
func (l *StrategyLifecycle) ShadowSampler() *ShadowSampler {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sampler
}

// WithShadowGateDisabled suppresses the automatic shadow-gate registration
// for the documented no-scorer-plus-armed-rollback case.
// It is deliberately explicit: the gate's absence must be a decision at the
// wiring layer (ShadowGateMode's three-branch invariant), never an emergent
// property of nil-checking. The reason is stored and reported by the snapshot
// and the startup log — an absent gate must be visible to be auditable.
func WithShadowGateDisabled(reason string) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.shadowGateSkipReason = reason
	}
}

// WithLifecycleMetrics attaches Prometheus metrics for promote/rollback
// counters.
func WithLifecycleMetrics(m *observability.PrometheusMetrics) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.metrics = m
	}
}

// WithCompileInfoProvider wires the compile provenance source so the
// LifecycleSnapshot map carries (compile_id, dag_version, compile_count)
// alongside the generation and gates. This closes the attribution chain:
// /api/evolution/lifecycle can answer "which generation, which gate, which
// compile" in a single endpoint call.
//
// The provider is typically a *planprojection.CompileCoordinator adapted
// into the CompileInfoProvider interface by the cmd/ares wiring layer (the
// adaptation breaks what would be a circular import).
func WithCompileInfoProvider(provider CompileInfoProvider) LifecycleOption {
	return func(l *StrategyLifecycle) {
		l.compileInfo = provider
	}
}

// SetCompileInfoProvider wires the compile provenance source at runtime.
// This is the post-construction wiring path: the CompileCoordinator is created
// after the lifecycle (it needs the live DAG which is built after bootstrap),
// so the provider must be injected after both are constructed. Called from
// the serve wiring layer (cmd/ares) once the compile coordinator is available.
//
// Thread-safe: the compileInfo field is an interface reference (a pointer-sized
// word) set once after construction and never read concurrently with a write
// (the Snapshot method runs only after wiring is complete).
func (l *StrategyLifecycle) SetCompileInfoProvider(provider CompileInfoProvider) {
	if l == nil || provider == nil {
		return
	}
	l.mu.Lock()
	l.compileInfo = provider
	l.mu.Unlock()
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
// the only caller of those methods.
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
	// When a ShadowEvaluator is wired, register the shadow verify gate
	// ahead of any explicitly supplied gates so the pipeline order is
	// shadow → eval → ... (previously the evaluator was
	// assigned to l.shadow but never read by the promote pipeline).
	// Exception: WithShadowGateDisabled suppresses the registration for
	// the no-scorer-plus-armed-rollback case — the gate would otherwise
	// reject every candidate forever, while canary + automatic rollback
	// carries the promotion risk.
	if l.shadow != nil && l.shadowGateSkipReason == "" {
		l.gates = append([]VerifyGate{shadowVerifyGate{l}}, l.gates...)
	}
	return l
}

// shadowVerifyGate adapts the lifecycle's ShadowEvaluator into the shadow
// verify gate. It is deliberately read-only: ShouldDeploy consults the
// comparisons that the data feeder — DreamCycle's shadow flow, or the
// task-level ShadowSampler when DreamCycle is disabled — recorded via
// StartShadow/RecordResult. The gate never calls StartShadow itself — that
// would reset accumulated comparisons on every Submit and destroy the
// evidence it is supposed to judge.
//
// SEMANTICS (fail-closed): with
// zero comparisons the gate REJECTS ("fewer than
// MinSamples samples → the candidate stays in SHADOW and is NOT deployed").
// Passing candidates without any shadow evidence
// made the whole verify pipeline a no-op in default configs (DreamCycle is
// disabled, so nothing feeds comparisons) — the previous "skip" branch
// silently reduced Submit to unconditional promote. The fail-closed branch is
// still reachable when the task-level sampler is wired but has NO independent
// scorer
// (default bootstrap: LLM scoring off): the sampler deliberately produces zero
// comparisons rather than fabricate evidence.
type shadowVerifyGate struct{ l *StrategyLifecycle }

func (g shadowVerifyGate) Name() string { return "shadow" }

func (g shadowVerifyGate) Check(_ context.Context, _ *mutation.Strategy, _ *mutation.Strategy) (bool, float64, string) {
	se := g.l.shadow
	if se == nil {
		// Unreachable when registered via NewStrategyLifecycle (the gate is
		// only appended when l.shadow != nil); kept nil-safe anyway.
		return true, 0, "shadow evaluator not wired, skipping"
	}
	ok, report := se.ShouldDeploy()
	// Publish the win rate from THIS gate, not only from DreamCycle's
	// shadow flow. Bootstrap runs with EnableDreamCycle=false and the
	// scheduler drives popAdapter.Run → lifecycle.Submit, so the DreamCycle
	// write point never executes in production and the gauge would stay
	// permanently zero. This is the gate the promote decision actually goes
	// through, so it is the authoritative source for the gauge.
	if g.l.metrics != nil && report != nil {
		g.l.metrics.SetEvolutionShadowWinRate(report.WinRate)
	}
	if report == nil {
		// FAIL-CLOSED: no shadow evidence at all — no comparison was ever
		// gathered. The gate cannot vouch for the candidate, so it does not
		// pass. See the type comment — this branch is the difference between a
		// verify pipeline and a rubber stamp.
		return false, 0, "no shadow comparisons recorded — fail-closed (no independent scorer wired)"
	}
	if report.TotalComparisons == 0 {
		// FAIL-CLOSED with a distinction: comparisons WERE gathered
		// but every one was an exact tie (e.g. cold-start prior-vs-prior on an
		// empty/sparse evidence store). Report the tie count so the operator
		// can tell "no evidence" (nil above) from "gathered but uninformative".
		return false, 0, fmt.Sprintf(
			"shadow evidence is all ties (%d comparisons, 0 decisive) — fail-closed: no decisive evidence the candidate is better",
			report.TieCount,
		)
	}
	if ok {
		return true, report.WinRate,
			fmt.Sprintf("shadow win rate %.2f over %d comparisons meets threshold", report.WinRate, report.TotalComparisons)
	}
	return false, report.WinRate,
		fmt.Sprintf("shadow win rate %.2f over %d comparisons below threshold (insufficient samples counts as fail)", report.WinRate, report.TotalComparisons)
}

// Start launches the rollback watch loop. It is idempotent. The loop runs
// until ctx is cancelled or Stop is called; Stop waits for the loop goroutine
// to exit so the lifecycle never leaks or races a late rollback (managed
// goroutine pair, no fire-and-forget).
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
	l.done = make(chan struct{})
	done := l.done
	l.mu.Unlock()

	go func() {
		// Production background goroutines must not die silently or take
		// the process down on a bug — recover, log, and exit cleanly.
		defer func() {
			if r := recover(); r != nil {
				log.ErrorContext(context.Background(), "watch loop panicked",
					"method", "watch", "error", fmt.Errorf("panic: %v", r))
			}
			close(done)
		}()
		l.watch(watchCtx)
	}()
}

// Stop cancels the watch loop and waits for it to exit.
func (l *StrategyLifecycle) Stop() {
	if l == nil {
		return
	}
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.cancel = nil
	l.done = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
