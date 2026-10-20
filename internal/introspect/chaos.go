// Package introspect — chaos read-model.
//
// The chaos subsystem (cmd/ares/serve_chaos.go) runs independently of the
// panel: a shadow sandbox loop verifies recovery capability against scratch
// fabrics, and (when armed) a live loop injects real failures. This file
// defines the read-model the panel renders — a small, lock-guarded snapshot
// produced by the chaos loops themselves, so operators can see shadow-sandbox
// health and live-injection state on the same panel without the loops ever
// touching the panel's write paths.
package introspect

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/task"
)

// ChaosStatus is one frame of chaos observability: how the chaos subsystem is
// configured, what the last shadow sandbox verification concluded, and (when
// live mode is armed) what the injection loop is doing. It is produced by the
// chaos loops (cmd/ares) and consumed by the panel collector as a Source.
type ChaosStatus struct {
	// Enabled reports whether the chaos subsystem is on at all
	// (cfg.Kernel.Chaos.Enabled).
	Enabled bool `json:"enabled"`
	// Mode is "shadow", "live" or "off" (the effective mode after all the
	// arming guards: allow_live, eligible whitelist, stop token).
	Mode string `json:"mode"`

	// Shadow holds the latest shadow sandbox verification result. Zero-valued
	// until the first cycle completes.
	Shadow ShadowResult `json:"shadow"`

	// Live holds the live-injection loop state. Zero-valued in shadow mode.
	Live LiveChaosState `json:"live"`
}

// ShadowResult is the outcome of the most recent shadow sandbox replay: did
// the agent-kill → lease-expire → recovery chain bring the task back to READY?
type ShadowResult struct {
	// LastRun is when the most recent shadow cycle finished.
	LastRun time.Time `json:"last_run"`
	// Events is how many sandbox events the cycle replayed.
	Events int `json:"events"`
	// Recovered reports whether the task returned to READY (healthy chain).
	Recovered bool `json:"recovered"`
	// Errored reports whether the replay itself failed (verification
	// inconclusive rather than a degraded chain).
	Errored bool `json:"errored"`
}

// LiveChaosState is the live-injection loop's operational state.
type LiveChaosState struct {
	// Active is true while a live loop is running.
	Active bool `json:"active"`
	// Injections is how many failures have been injected this process run.
	Injections uint64 `json:"injections"`
	// FailSafeTripped is the fail-safe latch: recovery verification failed,
	// so all further injections were permanently stopped.
	FailSafeTripped bool `json:"fail_safe_tripped"`
	// StoppedByControl is the emergency-stop flag: POST /api/chaos/stop
	// tripped the process-level kill switch.
	StoppedByControl bool `json:"stopped_by_control"`
	// PausedForGA is the GA quiet window: injections are being deferred while
	// a generation is mid-flight.
	PausedForGA bool `json:"paused_for_ga"`
	// LastInjection is when the most recent injection cycle ran.
	LastInjection time.Time `json:"last_injection"`

	// RecoveryMetrics carries the measured recovery timing and checkpoint
	// consistency window from the arena metrics collector. It is written
	// through SetRecoveryMetrics (its own atomic) and merged into the frame by
	// Snapshot — never through SetLive, which would clobber it on the next
	// flag-only frame. Zero-valued until the first sample.
	RecoveryMetrics *RecoveryMetrics `json:"recovery_metrics,omitempty"`
}

// RecoveryMetrics is a snapshot of the arena metrics collector, rendered on
// the chaos panel. It carries recovery timing plus the checkpoint
// data-consistency window: one producer's payload, one panel frame.
//
// ConsistencyMeasured is false while the window is below the collector's
// publication threshold. DataConsistencyRate can then be 0 for two different
// reasons ("no samples yet" and "every decode failed"), so consumers must read
// this flag rather than treating zero as measured. The panel should render
// "not measured" (or omit the number) while this is false.
type RecoveryMetrics struct {
	TotalRecoveries  int           `json:"total_recoveries"`
	AvgRecoveryTime  time.Duration `json:"avg_recovery_time"`
	MinRecoveryTime  time.Duration `json:"min_recovery_time"`
	MaxRecoveryTime  time.Duration `json:"max_recovery_time"`
	LastRecoveryTime time.Duration `json:"last_recovery_time"`

	DataConsistencyRate float64 `json:"data_consistency_rate"`
	ConsistencySamples  int     `json:"consistency_samples"`
	ConsistencyMeasured bool    `json:"consistency_measured"`
}

// chaosReporter is the concrete source the chaos loops update. A single
// instance is created per process in cmd/ares and handed to both the chaos
// loop (to record results) and the introspect collector (to read them), so
// the panel never observes an inconsistent half-write.
//
// The flag state (active / fail-safe / GA-pause / emergency-stop), the
// cumulative injection counter and the last-injection timestamp are tracked
// independently so a high-frequency AddInjection never clobbers the flags.
type chaosReporter struct {
	enabled atomic.Bool
	mode    atomic.Value // string

	shadow atomic.Value // ShadowResult

	liveFlags     atomic.Value // LiveChaosState flags only
	injections    atomic.Uint64
	lastInjection atomic.Value // time.Time

	// recoveryMetrics is deliberately its own atomic rather than a field on
	// LiveChaosState: SetLive replaces that whole struct, so a metric written
	// through it would be wiped by the next flag-only frame (GA-pause,
	// fail-safe, loop start/stop). Snapshot merges it back in exactly like
	// Injections/LastInjection. atomic.Pointer is nil-safe, so an unwritten
	// metric reads as nil instead of panicking.
	recoveryMetrics atomic.Pointer[RecoveryMetrics]
}

// NewChaosReporter builds a chaos status reporter (cmd/ares uses it to bridge
// the chaos loops into the panel). Enabled/mode are set once at wiring time.
func NewChaosReporter() *ChaosReporter { return &ChaosReporter{r: &chaosReporter{}} }

// ChaosReporter is the exported handle the serve wiring hands to the chaos
// loops and the introspect collector.
type ChaosReporter struct {
	r *chaosReporter
}

// SetConfig records the effective chaos configuration (enabled + resolved
// mode) at wiring time.
func (c *ChaosReporter) SetConfig(enabled bool, mode string) {
	c.r.enabled.Store(enabled)
	c.r.mode.Store(mode)
}

// RecordShadow stores the latest shadow sandbox verification outcome.
func (c *ChaosReporter) RecordShadow(res ShadowResult) { c.r.shadow.Store(res) }

// SetLive stores the live-injection loop's flag state (active / fail-safe /
// GA-pause / emergency-stop). Callers set only the flag fields; the injection
// counter, last-injection time and recovery metrics are maintained by
// AddInjection / SetRecoveryMetrics and merged by Snapshot (this call replaces
// the whole flag struct, so it must not carry them).
func (c *ChaosReporter) SetLive(st LiveChaosState) { c.r.liveFlags.Store(st) }

// AddInjection increments the process-lifetime injection counter by one and
// records the injection timestamp (add-only — never touches the flags).
func (c *ChaosReporter) AddInjection(t time.Time) {
	c.r.injections.Add(1)
	c.r.lastInjection.Store(t)
}

// SetRecoveryMetrics stores the latest measured recovery timing for the panel.
// It is add-only against the live flags: unlike SetLive (which replaces the
// whole flag struct), it targets its own atomic, so a later flag-only frame
// cannot wipe it. Snapshot merges it back in.
func (c *ChaosReporter) SetRecoveryMetrics(m *RecoveryMetrics) { c.r.recoveryMetrics.Store(m) }

// Snapshot assembles the current chaos status frame. Implements the panel's
// chaos Source contract (func() introspect.ChaosStatus).
func (c *ChaosReporter) Snapshot() ChaosStatus {
	status := ChaosStatus{Enabled: c.r.enabled.Load()}
	if m, ok := c.r.mode.Load().(string); ok {
		status.Mode = m
	}
	if s, ok := c.r.shadow.Load().(ShadowResult); ok {
		status.Shadow = s
	}
	if f, ok := c.r.liveFlags.Load().(LiveChaosState); ok {
		status.Live = f
	}
	status.Live.Injections = c.r.injections.Load()
	if t, ok := c.r.lastInjection.Load().(time.Time); ok && !t.IsZero() {
		status.Live.LastInjection = t
	}
	if m := c.r.recoveryMetrics.Load(); m != nil {
		status.Live.RecoveryMetrics = m
	}
	return status
}

// Shadow-sandbox script identities (goconst: reused across the replay events).
const (
	shadowAgentID = "shadow-agent-1"
	shadowTaskID  = "shadow-task-1"
)

// runShadowSandbox builds a scratch Task+Agent fabric, replays the canonical
// agent-kill → lease-expire → recovery scenario and returns the outcome.
// RecoverFromAgentDeath re-acquires the requeued task for a replacement agent,
// so the reliable recovered signal is the recover.all outcome's count, not the
// task state. (Moved from the retired dashboard.go: the Dashboard self-contained
// runtime was example-only; the sandbox verifier is shared chaos machinery.)
func runShadowSandbox(ctx context.Context) ShadowResult {
	scratchTasks := taskfabric.NewFabric()
	scratchAgents := agentfabric.NewFabric()
	recovery := aresrecovery.New(scratchTasks, scratchAgents, aresrecovery.DefaultRestartPolicy())
	sandbox := aresrecovery.NewSandbox(scratchTasks, scratchAgents, recovery)

	events := []aresrecovery.SandboxEvent{
		{Type: aresrecovery.SandboxEventAgentSpawn, AgentID: shadowAgentID},
		{Type: aresrecovery.SandboxEventTaskCreate, TaskID: shadowTaskID},
		{Type: aresrecovery.SandboxEventTaskAcquire, TaskID: shadowTaskID, AgentID: shadowAgentID},
		{Type: aresrecovery.SandboxEventAgentKill, AgentID: shadowAgentID},
		{Type: aresrecovery.SandboxEventLeaseExpire, TaskID: shadowTaskID},
		{Type: aresrecovery.SandboxEventRecoverAll},
	}

	outcomes, err := sandbox.Replay(ctx, events)
	if err != nil {
		return ShadowResult{LastRun: time.Now(), Events: len(events), Errored: true}
	}
	if len(outcomes) == 0 {
		return ShadowResult{LastRun: time.Now(), Events: len(events), Errored: true}
	}
	last := outcomes[len(outcomes)-1]
	recovered, _ := last.Detail["recovered"].(int)
	return ShadowResult{
		LastRun:   time.Now(),
		Events:    len(outcomes),
		Recovered: recovered > 0,
	}
}
