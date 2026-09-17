package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/kernel"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// pluginBusHook adapts runtime.PluginBus to the kernel.QuantumHook
// contract, so the runtime plugin ecosystem (observer/checkpoint/tool/...)
// participates in the Agent OS scheduling loop without the kernel depending on
// the runtime package (the adapter lives in the cmd assembly layer — the only
// place allowed to import both).
//
// Mapping: the bus speaks workflow Step/StepResult; a scheduling quantum is
// projected as a single-step workflow whose ID is the fabric task id.
//
// The hook ALSO drives the kernel loop clock: every LoopRoundQuanta
// quanta closes one loop round through the registered LoopPlugin — the "beat"
// of the evolution clock. Concurrency contract (QuantumHook may be invoked
// from concurrent drains):
//   - the boundary decision uses the atomic.AddInt64 RETURN value, never
//     Add-then-Load — Add-then-Load lets two drains read the same counter
//     value (double-fire) or skip past a multiple (lost round);
//   - the round BUDGET is likewise derived from that return value, not from
//     the stop flag: a flag is read-then-set, so concurrent boundary callers
//     all observe "not stopped" before any of them latches it and each would
//     settle its own over-budget round. Deriving the round number from the
//     caller's own unique count makes the budget order-independent.
//   - the stop flag is its own atomic.Bool, kept purely as a fast path so an
//     exhausted clock stops touching the plugin on every later quantum.
type pluginBusHook struct {
	bus *runtime.PluginBus
	// loop is the registered round-clock plugin; nil disables the beat.
	loop *runtime.LoopPlugin
	// roundQuanta / maxRounds are the loop clock knobs (from kernelLoopConfig).
	roundQuanta int64
	maxRounds   int64
	// quantumCount counts quanta observed by this hook; the AddInt64 return
	// value is the authoritative boundary test input.
	quantumCount atomic.Int64
	// loopStop is a fast path only: once the budget is exhausted later quanta
	// skip the plugin entirely. It is NOT the budget enforcement mechanism —
	// see driveLoopRound (the budget is derived from quantumCount).
	loopStop atomic.Bool
}

// newPluginBusHook wraps a started PluginBus as a scheduler QuantumHook.
// loop may be nil (no loop clock). roundQuanta is normalized HERE (<=1 → 1)
// rather than trusting the caller: the boundary arithmetic divides by it, so
// the invariant belongs to the type, not to every construction site.
func newPluginBusHook(bus *runtime.PluginBus, loop *runtime.LoopPlugin, loopCfg kernelLoopConfig) *pluginBusHook {
	roundQuanta := int64(loopCfg.LoopRoundQuanta)
	if roundQuanta <= 0 {
		roundQuanta = 1
	}
	maxRounds := int64(loopCfg.LoopMaxIterations)
	if maxRounds < 0 {
		maxRounds = 0 // negative is meaningless; treat as unlimited
	}
	return &pluginBusHook{
		bus:         bus,
		loop:        loop,
		roundQuanta: roundQuanta,
		maxRounds:   maxRounds,
	}
}

// BeforeQuantum implements kernel.QuantumHook: projects the quantum
// onto the bus as a before-step hook invocation.
func (h *pluginBusHook) BeforeQuantum(ctx context.Context, taskID, agentID string) error {
	return h.bus.BeforeStep(ctx, taskID, &runtime.Step{
		ID:        taskID,
		Name:      taskID,
		AgentType: agentID,
		Status:    runtime.StepStatusRunning,
		StartedAt: time.Now(),
	})
}

// AfterQuantum implements kernel.QuantumHook: projects the quantum
// outcome onto the bus as an after-step hook invocation, then advances the
// loop clock. Both paths are observational — the hook never blocks or fails
// the scheduler.
func (h *pluginBusHook) AfterQuantum(ctx context.Context, taskID, agentID string, qerr error) {
	res := &runtime.StepResult{
		StepID:   taskID,
		Name:     taskID,
		Duration: 0,
		Metadata: map[string]string{"agent_id": agentID},
	}
	if qerr != nil {
		res.Status = runtime.StepStatusFailed
		res.Error = qerr.Error()
	} else {
		res.Status = runtime.StepStatusCompleted
	}
	_ = h.bus.AfterStep(ctx, taskID, res) // observational; bus already logs hook failures
	h.driveLoopRound(ctx)
}

// driveLoopRound advances the kernel loop clock when this quantum closes a
// round boundary.
//
// Judgment order (settle-then-gate — the gate must come AFTER the settle,
// otherwise a MaxIterations budget would swallow the FINAL round's
// OnRoundEnd: asking ShouldExecuteRound(round+1) before settling round
// `round` returns false at exactly the boundary where the last round needs
// its end-of-round bookkeeping):
//
//  1. settle the finished round: OnRoundEnd(round, executionID)
//  2. gate the next round: ShouldExecuteRound(round+1, vars) — false latches
//     loopStop and stops all further round advancement.
//
// executionID is the ROUND's identity, not the boundary task's taskID: one
// round spans LoopRoundQuanta quanta over multiple different tasks, so the
// task that happens to land on the boundary must not stand in for the round
// as a whole. (C1.3: the per-round identity used to be observable via the
// checkpoint flush it triggered; with capability dispatch retired it is only
// logged at the round boundary.)
//
// Budget enforcement is derived from the caller's own `count`, NOT from
// loopStop. loopStop is read-then-set, so N concurrent boundary callers can
// all observe "not stopped" before any of them latches it, and each would
// then settle a round beyond the budget (observed: max_iterations=1 settling
// 3 rounds under concurrent drains). Because `count` is unique and monotonic
// per quantum, `round = count/roundQuanta` is unique per caller, so testing
// `round > maxRounds` is order-independent and exact.
func (h *pluginBusHook) driveLoopRound(ctx context.Context) {
	if h.loop == nil || h.loopStop.Load() {
		return
	}
	// The AddInt64 return value — not a subsequent Load — is the boundary
	// test: it is unique per quantum even under concurrent drains, so each
	// multiple of roundQuanta maps to exactly one caller.
	count := h.quantumCount.Add(1)
	if h.roundQuanta <= 0 || count%h.roundQuanta != 0 {
		return
	}
	round := count / h.roundQuanta
	// Over-budget rounds are dropped before any settling. `round` comes from
	// this caller's unique count, so this holds regardless of interleaving.
	// Note round == maxRounds still settles: settle-then-gate means the final
	// round keeps its end-of-round bookkeeping.
	if h.maxRounds > 0 && round > h.maxRounds {
		h.loopStop.Store(true)
		return
	}
	executionID := fmt.Sprintf("kernel-round-%d", round)
	// OnRoundEnd is best-effort by contract (each subsystem failure only
	// logs) and guarded internally — a panic here would be a plugin bug, so
	// recover to keep the observational contract ("hook never kills the
	// scheduler") airtight.
	defer func() {
		if r := recover(); r != nil {
			log.Error("kernel loop: round-end processing panicked (recovered)", "panic", r)
		}
	}()
	h.loop.OnRoundEnd(ctx, int(round), executionID)

	vars := map[string]any{"round": int(round)}
	if !h.loop.ShouldExecuteRound(int(round)+1, vars) {
		h.loopStop.Store(true)
		log.Info("kernel loop: round budget exhausted; round clock stopped (scheduler task flow unaffected)", "max_rounds", h.maxRounds, "round", round)
	}
}

// startPluginBus assembles the runtime plugin ecosystem and attaches it to the
// kernel scheduler's quantum boundary (Agent OS closure: the plugins observe
// every Schedule→Acquire→RunQuantum without the kernel importing the runtime).
//
// Registration order is no longer load-bearing: PluginBus.Register hot-plugs
// (a plugin registered after Start is started immediately under the bus
// lifetime ctx and receives its EventBus reference), so the only ordering
// constraint left is that the bus exists before plugins that emit during
// their own Start.
//
// This wires the ROUND CLOCK (LoopPlugin beat). C1.3 (runtime plugin
// half-closed-loop burial) removed the per-round capability dispatch from
// OnRoundEnd — the CapCheckpoint/CapMemory/CapEvolution plugin faces were
// deleted with their zero-production implementations, so OnRoundEnd now only
// records the settled round (Iteration()). The clock itself is alive:
// ShouldExecuteRound/round budget gate the loop, and the falsifiable tests
// (runtime_bridge_loop_test.go) lock the settle-then-gate order and the
// budget's concurrency contract via Iteration() and bus passthrough.
//
// Args:
//
//	ctx     - lifetime of the serve process; cancelling stops the bus.
//	store   - the shared event store the bus mirrors events into (may be nil).
//	sched   - the kernel scheduler to hook; may be nil (no-op).
//	loopCfg - kernel loop knobs (round quanta / max iterations).
//
// Returns:
//
//	*runtime.PluginBus - the started bus (nil when nothing to wire).
func startPluginBus(ctx context.Context, store ares_events.EventStore, sched *kernel.Scheduler, loopCfg kernelLoopConfig) *runtime.PluginBus {
	if sched == nil {
		return nil
	}
	bus := runtime.NewPluginBus()
	_ = store // the bus subscribes via Subscribe(); store passthrough not needed

	// Register BEFORE Start (see doc comment: Register-after-Start is a
	// guaranteed silent no-op).
	loop := runtime.NewLoopPlugin("kernel-loop", runtime.LoopConfig{
		MaxIterations: loopCfg.LoopMaxIterations,
		// UntilCondition stays nil: the kernel round clock does no variable
		// assertion — the round budget is the only stop condition.
	})
	if err := bus.Register(loop); err != nil {
		log.
			// Downgrade to log + continue scheduling: a registration metadata
			// problem must never block the kernel.
			Info("peer mode: loop plugin registration skipped (scheduling continues without the round clock):", "err", err)
		loop = nil
	}
	if err := bus.Start(ctx); err != nil {
		log.Warn("peer mode: plugin bus start failed (scheduling continues without plugins)", "err", err)
		// Stop the bus so any plugins that DID start before the failure
		// are torn down; otherwise they are orphaned with no handle.
		if stopErr := bus.Stop(ctx); stopErr != nil {
			log.Warn("peer mode: plugin bus stop after failed start", "err", stopErr)
		}
		return nil
	}
	sched.WithQuantumHook(newPluginBusHook(bus, loop, loopCfg))
	log.Info("peer mode: plugin bus wired to kernel quantum boundary (loop clock)", "loop_round_quanta", loopCfg.LoopRoundQuanta, "loop_max_iterations", loopCfg.LoopMaxIterations)
	return bus
}
