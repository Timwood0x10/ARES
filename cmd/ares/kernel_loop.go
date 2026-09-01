package main

// TODO(tech-debt): agentipc has no retry/dead-letter semantics (the legacy ahp
// DLQProcessor was removed with the leader-sub protocol). Wire IPC retry or a
// dead-letter path when multi-agent messaging scales (repair plan GAP-3).

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
// behavior (zero-value usable, code_rules).
type kernelLoopConfig struct {
	// LeaseTTL is the scheduler task-lease duration (0 = scheduler default).
	LeaseTTL time.Duration
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
	// LoopMaxIterations caps the kernel loop clock's round count (0 =
	// unlimited). Past the budget the round clock stops advancing; the
	// scheduler's task flow is never gated by it.
	LoopMaxIterations int
	// LoopRoundQuanta is how many quanta constitute one loop round (>=1;
	// 0/absent falls back to 1).
	LoopRoundQuanta int
	// RecoveryKick carries task IDs the scheduler released at the stale-winner
	// boundary (B1): the winner died with no capable replacement, so the task
	// is back in READY but has no execution body. The recovery loop binds a
	// replacement for each nominated task.
	//
	// A nominated task cannot be found by the expired-lease sweep — Release
	// clears the lease, and CheckExpiredLeases only requeues tasks that still
	// hold an expired one. That is exactly why the ID travels with the signal
	// instead of being a bare wake-up.
	//
	// Nil (the zero value) makes the select case inert, preserving the pre-B1
	// behavior for every call site that does not wire it.
	RecoveryKick <-chan string
}

// recoveryKickBuffer bounds the stale-winner nomination channel (B1). Each
// entry is a distinct task needing a replacement body, so the buffer is sized
// for a burst of concurrent deaths (one drain runs at most 32 quanta, see
// Scheduler.drain's sanity cap) rather than the single slot a bare wake-up
// signal would need. The producer drops on full: a dropped nomination degrades
// to the pre-B1 behavior for that task (it waits in READY for an executor),
// never to a blocked drain goroutine.
const recoveryKickBuffer = 32

// newRecoveryKick builds the stale-winner nomination pair for B1: a bounded
// channel to hand to kernelLoopConfig.RecoveryKick, and a non-blocking hint
// function to hand to Scheduler.WithRecoveryHint.
//
// The hint is called from a drain goroutine on the scheduling hot path, so it
// must never block.
//
// Returns:
//   - <-chan string: the receive side for kernelLoopConfig.RecoveryKick.
//   - func(string): the non-blocking hint for Scheduler.WithRecoveryHint.
func newRecoveryKick() (<-chan string, func(taskID string)) {
	ch := make(chan string, recoveryKickBuffer)
	return ch, func(taskID string) {
		if taskID == "" {
			return
		}
		select {
		case ch <- taskID:
		default:
			// Buffer full: drop rather than block the drain. The task stays
			// READY and is picked up as soon as any capable executor appears.
			log.Printf("kernel recovery loop: nomination buffer full, dropping %q", taskID)
		}
	}
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
	if c.LoopRoundQuanta <= 0 {
		c.LoopRoundQuanta = 1
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
	leaseTTL := time.Duration(0)
	if raw := cfg.Kernel.LeaseTTL; raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			leaseTTL = d
		} else {
			log.Printf("kernel: invalid lease_ttl %q, using scheduler default", raw)
		}
	}
	return kernelLoopConfig{
		LeaseTTL:               leaseTTL,
		QuotaApplyInterval:     parse(cfg.Kernel.QuotaApplyInterval, quotaApplyInterval),
		QuotaApplyTimeout:      parse(cfg.Kernel.QuotaApplyTimeout, quotaApplyTimeout),
		EvolutionApplyInterval: parse(cfg.Kernel.EvolutionApplyInterval, evolutionApplyInterval),
		EvolutionApplyTimeout:  parse(cfg.Kernel.EvolutionApplyTimeout, evolutionApplyTimeout),
		RecoverySweepInterval:  parse(cfg.Kernel.RecoverySweepInterval, recoverySweepInterval),
		RecoverySweepTimeout:   parse(cfg.Kernel.RecoverySweepTimeout, recoverySweepTimeout),
		DispatchTimeout:        parse(cfg.Kernel.DispatchTimeout, kernelDispatchTimeout),
		LoopMaxIterations:      cfg.Kernel.LoopMaxIterations,
		LoopRoundQuanta:        cfg.Kernel.LoopRoundQuanta,
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

// runKernelRecoveryLoop is the Kernel-level event-driven recovery loop. It
// reacts to task
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
// exit (code_rules: managed worker with a stop signal).
//
// B1: cfg.RecoveryKick is the scheduler's stale-winner trigger. The scheduler
// signals it when a leased task's winner died with no capable replacement —
// the task is released to READY and this loop spawns the replacement body
// immediately, instead of the task waiting out a full lease TTL. A nil channel
// (the zero value) makes the select case inert, exactly like a nil event
// channel, so every existing call site keeps its previous behavior.
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
	// bindReplacements gives each task in ids an execution body when no
	// registered executor can already resume it. Shared by the expired-lease
	// sweep and the B1 stale-winner nomination path, which differ only in how
	// the task list is obtained: the sweep discovers tasks whose lease just
	// expired, the nomination path is told a specific task by the scheduler.
	//
	// No-op in requeue-only mode (leader path, chaos/sandbox, tests that pass
	// nil callbacks): the scheduler resumes the READY task with an existing
	// executor from its preserved checkpoint via toModelTask.
	bindReplacements := func(ids []string) {
		if registerExecutor == nil || executorFactory == nil || hasCapableExecutor == nil {
			return
		}
		for _, taskID := range ids {
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
	}
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
			// recovery candidate), then give each one an execution body.
			requeued := recovery.RequeueExpiredLeases()
			if len(requeued) == 0 {
				return
			}
			log.Printf("kernel recovery loop: requeued %d expired task(s)", len(requeued))
			bindReplacements(requeued)
		}()
	}
	// bindNominated handles one B1 stale-winner nomination. It shares the
	// sweep's semaphore so a nomination can never run concurrently with a
	// sweep — both mutate the executor registry for the same task set, and
	// RestartAgent's restart budget must not be spent twice for one death.
	//
	// Unlike sweep it does NOT requeue: the scheduler already released the
	// task to READY, and Release cleared the lease, so CheckExpiredLeases
	// would never find it. The nomination carries the task ID for exactly this
	// reason.
	//
	// It WAITS for the semaphore instead of dropping on contention. Dropping
	// looked symmetric with sweep's drop-on-full, but the two are not
	// symmetric: a dropped sweep is retried by the next tick, whereas a
	// dropped nomination is lost forever — the released task holds no lease,
	// so no later sweep will rediscover it, and it sits in READY with no
	// execution body. Measured as a 1-in-30 residual failure of
	// TestE2E_GrandLoop_RealSchedulerChaosRecovery.
	//
	// Waiting is bounded: RecoveryKick is a capacity-32 channel and the loop
	// consumes one entry at a time, so at most a handful of these goroutines
	// exist, each parked on a semaphore released by an in-memory scan.
	bindNominated := func(taskID string) {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("kernel recovery loop: panic binding nominated task %q: %v", taskID, r)
				}
			}()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			bindReplacements([]string{taskID})
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
		case taskID, ok := <-cfg.RecoveryKick:
			// B1: the scheduler released a leased task whose winner died with
			// no capable replacement. Bind a replacement body now so the task
			// resumes within one drain instead of stalling in READY.
			if !ok {
				return
			}
			bindNominated(taskID)
		}
	}
}

// parseKernelPollInterval parses the YAML kernel.poll_interval duration,
// returning 0 when unset/invalid so the scheduler keeps its 500ms default.
//
// Args:
//
//	raw - the raw YAML duration string (may be empty).
//
// Returns:
//
//	time.Duration - the parsed interval, or 0 when empty/invalid.
func parseKernelPollInterval(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("kernel: invalid poll_interval %q, using scheduler default", raw)
		return 0
	}
	return d
}
