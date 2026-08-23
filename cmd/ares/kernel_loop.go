package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
)

// recoverySweepInterval is how often the event-driven recovery loop also
// sweeps TTL-based lease expiry (lease expiry is detected by a sweep, not by
// an event, so a periodic safety net is required alongside the event channel).
// It is the default when kernel.recovery_sweep_interval is not configured.
const recoverySweepInterval = time.Second

// recoverySweepTimeout bounds one recovery sweep. A hung store must not block
// the recovery loop's event consumption nor pile up sweeps (C3); the sweep
// runs async with this timeout so a slow store at worst drops triggers.
const recoverySweepTimeout = 30 * time.Second

// quotaApplyInterval is how often the evolution-aware quota manager pushes
// the current evolution resource budget into the Agent Fabric (v0.3.0 M2-2).
// The GA evolution ticker runs on a 5-minute cadence, so a 1-minute apply
// loop keeps a deployed budget effective within a reasonable window without
// burning CPU. It is the default when kernel.quota_apply_interval is unset.
const quotaApplyInterval = time.Minute

// evolutionApplyInterval is how often the evolution population adapter
// applies the agent population policy (P6: Runtime Adaptation). Mirrors
// the quota cadence — 1 minute keeps a deployed policy effective within a
// reasonable window. It is the default when
// kernel.evolution_apply_interval is unset.
const evolutionApplyInterval = time.Minute

// evolutionApplyTimeout bounds one population policy application. A hung
// policy store must not stall the loop, so every Apply runs under this
// timeout.
const evolutionApplyTimeout = 30 * time.Second

// quotaApplyTimeout bounds one quota policy application. A hung policy store
// must not stall the quota loop (C1), so every Apply runs under this timeout.
const quotaApplyTimeout = 30 * time.Second

// kernelDispatchTimeout bounds how long kernelTaskDispatcher.Dispatch waits
// for a submitted task's completion event before reporting it failed. It
// mirrors the legacy leader dispatcher's timeout contract
// (DefaultDispatcherTimeoutSeconds = 300) so the kernel path does not time
// out sooner than the path it replaces. It is the default when
// kernel.dispatch_timeout is not configured.
const kernelDispatchTimeout = 300 * time.Second

// kernelLoopConfig carries the tunable intervals/timeouts for the kernel
// background loops (quota, recovery, dispatch). Zero durations fall back to
// the package defaults, so an absent kernel loop config section keeps prior
// behavior (zero-value usable, code_rules_v2 §5.4).
type kernelLoopConfig struct {
	// QuotaApplyInterval is how often the quota loop re-applies the budget.
	QuotaApplyInterval time.Duration
	// QuotaApplyTimeout bounds each quota Apply call.
	QuotaApplyTimeout time.Duration
	// EvolutionApplyInterval is how often the evolution population loop runs.
	EvolutionApplyInterval time.Duration
	// EvolutionApplyTimeout bounds each evolution Apply call.
	EvolutionApplyTimeout time.Duration
	// RecoverySweepInterval is how often the recovery loop sweeps leases.
	RecoverySweepInterval time.Duration
	// RecoverySweepTimeout bounds each recovery sweep.
	RecoverySweepTimeout time.Duration
	// DispatchTimeout bounds Dispatch's wait for a worker completion event.
	DispatchTimeout time.Duration
}

// withDefaults fills any zero-valued knob with the package default so a
// partially-configured (or zero) kernelLoopConfig never drives a loop with a
// zero ticker or a zero timeout.
func (c kernelLoopConfig) withDefaults() kernelLoopConfig {
	if c.QuotaApplyInterval <= 0 {
		c.QuotaApplyInterval = quotaApplyInterval
	}
	if c.QuotaApplyTimeout <= 0 {
		c.QuotaApplyTimeout = quotaApplyTimeout
	}
	if c.EvolutionApplyInterval <= 0 {
		c.EvolutionApplyInterval = evolutionApplyInterval
	}
	if c.EvolutionApplyTimeout <= 0 {
		c.EvolutionApplyTimeout = evolutionApplyTimeout
	}
	if c.RecoverySweepInterval <= 0 {
		c.RecoverySweepInterval = recoverySweepInterval
	}
	if c.RecoverySweepTimeout <= 0 {
		c.RecoverySweepTimeout = recoverySweepTimeout
	}
	if c.DispatchTimeout <= 0 {
		c.DispatchTimeout = kernelDispatchTimeout
	}
	return c
}

// parseKernelLoopConfig reads the kernel loop knobs from the serve config.
// Empty or invalid duration strings log a warning and fall back to the
// package default, so a bad config never disables a safety timeout.
func parseKernelLoopConfig(cfg *ares_config.Config) kernelLoopConfig {
	parse := func(raw string, fallback time.Duration) time.Duration {
		if raw == "" {
			return fallback
		}
		d, err := time.ParseDuration(raw)
		if err != nil {
			log.Printf("kernel: invalid duration %q, using default %s: %v", raw, fallback, err)
			return fallback
		}
		return d
	}
	return kernelLoopConfig{
		QuotaApplyInterval:     parse(cfg.Kernel.QuotaApplyInterval, quotaApplyInterval),
		QuotaApplyTimeout:      parse(cfg.Kernel.QuotaApplyTimeout, quotaApplyTimeout),
		EvolutionApplyInterval: parse(cfg.Kernel.EvolutionApplyInterval, evolutionApplyInterval),
		EvolutionApplyTimeout:  parse(cfg.Kernel.EvolutionApplyTimeout, evolutionApplyTimeout),
		RecoverySweepInterval:  parse(cfg.Kernel.RecoverySweepInterval, recoverySweepInterval),
		RecoverySweepTimeout:   parse(cfg.Kernel.RecoverySweepTimeout, recoverySweepTimeout),
		DispatchTimeout:        parse(cfg.Kernel.DispatchTimeout, kernelDispatchTimeout),
	}.withDefaults()
}

// runKernelQuotaLoop periodically applies the evolution resource policy to
// the Agent Fabric's budget (v0.3.0 M2-2). It applies once at startup so an
// already-deployed policy is effective immediately, then re-applies on a
// fixed interval — Apply is idempotent (replaces the budget in place), so a
// nil/no-op policy leaves the configured kernel resources untouched.
//
// Args:
//   - ctx: stops the loop.
//   - mgr: the quota manager (nil disables the loop).
//   - cfg: loop knobs; zero values fall back to the package defaults.
func runKernelQuotaLoop(ctx context.Context, mgr *aresrecovery.EvolutionAwareQuotaManager, cfg kernelLoopConfig) {
	if mgr == nil {
		return
	}
	cfg = cfg.withDefaults()
	apply := func(phase string) {
		// A hung policy store must not stall the loop (C1): bound every Apply
		// with a timeout and recover from any panic so the ticker keeps
		// running (M2).
		defer func() {
			if r := recover(); r != nil {
				log.Printf("kernel: quota apply (%s) panic: %v", phase, r)
			}
		}()
		applyCtx, cancel := context.WithTimeout(ctx, cfg.QuotaApplyTimeout)
		defer cancel()
		if err := mgr.Apply(applyCtx); err != nil {
			log.Printf("kernel: quota apply (%s): %v", phase, err)
		}
	}
	apply("startup")
	ticker := time.NewTicker(cfg.QuotaApplyInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			apply("tick")
		case <-ctx.Done():
			return
		}
	}
}

// runKernelRecoveryLoop is the Kernel-level event-driven recovery loop
// (ares-runtime.md §13 + P5, code-review-2025-01-16 #2). It reacts to task
// lifecycle events (TaskExpired / TaskFailed / TaskAcquired / TaskYielded) on
// the shared EventStore and, on each, runs the recovery chain
// (RequeueExpiredLeases → checkpoint resume → agent restart). A slow periodic
// sweep complements the event channel because TTL-based lease expiry is only
// observable by sweeping.
//
// W1 recovery闭环: when a factory + registerExecutor + hasCapableExecutor are
// wired (peer mode), the sweep goes beyond requeue-only. For each task that
// actually expired this sweep, if no registered executor can resume it, a
// replacement executor is created and bound to exactly that task
// (RegisterExecutorForTask), so the recovered task is driven by a real
// executor — not a phantom, and never a hijacker of other tasks. Bound
// executors are unregistered by the scheduler once the task reaches a terminal
// state. When the factory is nil (leader path, chaos/sandbox), the loop is
// requeue-only: existing registered executors resume the READY tasks from
// their preserved checkpoints via toModelTask.
//
// Each sweep runs ASYNC with a per-sweep timeout (C3): a slow or hung store
// must neither block the loop's event consumption nor pile up sweeps. A
// buffered semaphore (capacity 1) drops a sweep trigger while the previous
// one is still running. The sweep goroutine is bounded by sweepCtx (derived
// from the loop ctx, so a shutdown cancels it) and releases the semaphore on
// exit (code_rules_v2 §4.1: managed worker with a stop signal).
//
// Args:
//   - ctx: stops the loop.
//   - store: the EventStore to subscribe from (nil disables the event channel;
//     the periodic sweep still runs).
//   - recovery: the Recovery subsystem (nil disables the loop).
//   - cfg: loop knobs; zero values fall back to the package defaults.
//   - registerExecutor: registers a replacement executor bound to a specific
//     recovered task (W1). nil = requeue-only mode.
//   - executorFactory: creates a CapabilityExecutor for a replacement agentID
//     and capability (W1). nil = requeue-only mode.
//   - hasCapableExecutor: reports whether a registered executor can already
//     resume the given task, in which case no replacement is spawned.
func runKernelRecoveryLoop(
	ctx context.Context,
	store ares_events.EventStore,
	recovery *aresrecovery.Recovery,
	cfg kernelLoopConfig,
	registerExecutor func(taskID, agentID string, executor CapabilityExecutor),
	executorFactory func(agentID, capability string) CapabilityExecutor,
	hasCapableExecutor func(taskID string) bool,
) {
	if recovery == nil {
		return
	}
	cfg = cfg.withDefaults()
	var events <-chan *ares_events.Event
	if store != nil {
		ch, err := store.Subscribe(ctx, ares_events.EventFilter{
			Types: []ares_events.EventType{
				ares_events.EventTaskExpired,
				ares_events.EventTaskFailed,
				ares_events.EventTaskAcquired,
				ares_events.EventTaskYielded,
			},
		})
		if err == nil {
			events = ch
		} else {
			log.Printf("kernel recovery loop: subscribe failed, periodic sweep only: %v", err)
		}
	}
	ticker := time.NewTicker(cfg.RecoverySweepInterval)
	defer ticker.Stop()
	// sem (capacity 1) guards against overlapping sweeps: a sweep that is
	// still running (e.g. a stalled store) holds the single slot, so further
	// triggers are dropped until it finishes. Bounded — at most one sweep
	// goroutine exists beyond a hung one.
	sem := make(chan struct{}, 1)
	sweep := func() {
		select {
		case sem <- struct{}{}:
		default:
			return // previous sweep still running; drop this trigger (C3)
		}
		go func() {
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("kernel recovery loop: panic in recovery sweep: %v", r)
				}
			}()
			sweepCtx, cancel := context.WithTimeout(ctx, cfg.RecoverySweepTimeout)
			defer cancel()
			// Honor the sweep timeout even though the requeue scan is
			// in-memory: a cancelled/past-deadline sweep runs no scan at all
			// (the next trigger retries).
			select {
			case <-sweepCtx.Done():
				return
			default:
			}
			// W1 recovery闭环: requeue the tasks whose lease expired THIS
			// sweep (not all READY tasks — a brand-new task is never a
			// recovery candidate). For each requeued task, if no registered
			// executor can already resume it, spawn a replacement executor
			// and bind it to exactly that task. The scheduler unregisters the
			// bound executor once the task reaches a terminal state.
			//
			// When executorFactory / registerExecutor are nil (leader path,
			// tests, chaos/sandbox), the loop is requeue-only: the scheduler
			// picks up the READY task with an existing executor and resumes
			// from the preserved checkpoint via toModelTask.
			requeued := recovery.RequeueExpiredLeases()
			if len(requeued) == 0 {
				return
			}
			log.Printf("kernel recovery loop: requeued %d expired task(s)", len(requeued))
			if registerExecutor == nil || executorFactory == nil || hasCapableExecutor == nil {
				return // requeue-only mode
			}
			for _, taskID := range requeued {
				if hasCapableExecutor(taskID) {
					continue // an existing executor resumes this task
				}
				tasks := recovery.RecoveryTasksFor([]string{taskID})
				if len(tasks) == 0 {
					continue
				}
				rt := tasks[0]

				// Fusion-plan A2 arbitration (priority 1): if a dead agent
				// with matching capability left a cognitive snapshot, revive
				// THAT identity in place — same id, restored cognition,
				// continuous provenance — instead of spawning a generic
				// replacement. RestartAgent enforces the maxRestarts budget
				// and returns ErrRecoveryExhausted past it, in which case we
				// fall through to the generic replacement below.
				if snapID, snap, found := recovery.RevivableSnapshot(rt.Capability); found {
					if revived, err := recovery.RestartAgent(ctx, snapID, snap.Cognitive, snap.Capabilities); err == nil {
						exec := executorFactory(revived.Identity, rt.Capability)
						if exec != nil {
							registerExecutor(taskID, revived.Identity, exec)
							log.Printf("kernel recovery loop: revived %q in place (cognition restored) for task %q", revived.Identity, taskID)
							continue
						}
					} else {
						log.Printf("kernel recovery loop: in-place revival of %q unavailable (%v); using replacement", snapID, err)
					}
				}

				replacementID := fmt.Sprintf("recovery-%s-%d", taskID, time.Now().UnixNano())
				executor := executorFactory(replacementID, rt.Capability)
				if executor == nil {
					log.Printf("kernel recovery loop: executor factory returned nil for %s (%s)", replacementID, rt.Capability)
					continue
				}
				registerExecutor(taskID, replacementID, executor)
				log.Printf("kernel recovery loop: replacement executor %q bound to task %q", replacementID, taskID)
			}
		}()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sweep()
		case _, ok := <-events:
			if !ok {
				return
			}
			sweep()
		}
	}
}
