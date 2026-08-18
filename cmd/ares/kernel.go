package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Kernel assembly entry (P4 D4: parallel + feature flag gradual cutover).
//
// wireKernelDispatcher assembles the dual-track dispatch kernel:
//
//   - legacy:   the leader.TaskDispatcher path (real dispatch, unchanged
//     behavior — retained as an explicit opt-out via kernel.policy=legacy).
//   - newPath:  a taskfabric capability-aware scoring dispatcher. It is a
//     pure observer in this stage: it computes "who would the Kernel pick" via
//     taskfabric.Score/Pick without creating, acquiring or executing, so a
//     task is never double-run.
//
// The PolicyFlag starts at PolicyLegacyLeader (safety) with shadow mode ON, so
// the new path begins as an observer: every legacy dispatch also scores the
// same task and counts mismatches (Mismatches()). Production flips to
// PolicyTaskFabric by default — wireKernelPolicy treats any value other than
// explicit "legacy" as taskfabric (D4 gradual cutover completed); the real
// Create→Schedule→Acquire→RunQuantum executor is wired at that point.
//
// Returns:
//   - *agentipc.DualTrackDispatcher: the assembled kernel dispatcher.
//   - *agentipc.PolicyFlag: the feature flag (flip it to cut over).
func wireKernelDispatcher(
	leaderDispatcher leader.TaskDispatcher,
	subAgents []subAgentCapability,
) (*agentipc.DualTrackDispatcher, *agentipc.PolicyFlag) {
	flag := agentipc.NewPolicyFlag(agentipc.PolicyLegacyLeader)
	legacy := &kernelLegacyDispatcher{inner: leaderDispatcher}
	newPath := &kernelFabricDispatcher{candidates: subAgents}
	return agentipc.NewDualTrackDispatcher(flag, legacy, newPath, true), flag
}

// enableKernelExecution switches the kernel's new path from shadow (scoring
// only) to real Task Fabric execution, and turns shadow mode off on the
// dual-track dispatcher so the legacy path is not re-run for every task (which
// would double-execute). Callers invoke this in the same critical section as
// flag.Set(PolicyTaskFabric).
//
// Order matters for a live mid-run flip: shadow is disabled BEFORE the new
// path is swapped in, so a dispatch racing the flip can never run legacy in
// shadow against the executing new path (double execution). In-flight legacy
// dispatches complete synchronously (Dispatch blocks until the legacy path
// returns), so nothing is orphaned.
//
// Args:
//   - kernel: the dual-track dispatcher assembled by wireKernelDispatcher.
//   - fabric: the Task Fabric that executes tasks.
func enableKernelExecution(
	kernel *agentipc.DualTrackDispatcher,
	fabric *taskfabric.Fabric,
) {
	// Turn shadow off first: with the new path about to become live, running
	// legacy in shadow would re-dispatch every task (double execution).
	kernel.SetShadow(false)
	// Replace the shadow-only new path with the submitting one. IMPORTANT: the
	// leader dispatch only SUBMITS the task to the fabric (Create); the
	// kernelScheduler is the single executor (Schedule→Acquire→RunQuantum on
	// every READY task). Keeping the full execution in the dispatch path as
	// well caused a double-path race: both the leader dispatch and the
	// scheduler tried to acquire the same task, surfacing as
	// "task not ready for acquire" in serve logs (GAP #2 fix).
	exec := &kernelFabricDispatcher{
		candidates: kernelNewPathCandidates(kernel),
		executeFn: func(ctx context.Context, task *models.Task) error {
			return submitFabricTask(ctx, fabric, task)
		},
	}
	kernel.SetNewPath(exec)
}

// kernelNewPathCandidates extracts the candidate list from the kernel's new
// path so enableKernelExecution can rebuild it with an executor attached.
func kernelNewPathCandidates(kernel *agentipc.DualTrackDispatcher) []subAgentCapability {
	if fp, ok := kernel.NewPath().(*kernelFabricDispatcher); ok {
		return fp.candidates
	}
	return nil
}

// submitFabricTask SUBMITS a task to the Task Fabric (Create with DAG edges)
// WITHOUT executing it. Execution is the kernelScheduler's sole job: its
// drain runs Schedule→Acquire→RunQuantum on every READY task. The leader
// dispatch path must NOT also schedule the task — doing so created a
// double-path race where both the leader dispatch (executeFabricTask) and the
// kernelScheduler tried to acquire the same task, surfacing as
// "task not ready for acquire" in serve logs (GAP #2 fix).
//
// Args:
//   - ctx: task lifetime (unused; kept for signature symmetry).
//   - fabric: the Task Fabric that owns the task.
//   - task: the task to submit.
//
// Returns:
//   - error: fabric create error (ErrTaskExists is tolerated).
func submitFabricTask(
	ctx context.Context,
	fabric *taskfabric.Fabric,
	task *models.Task,
) error {
	if fabric == nil {
		return taskfabric.ErrTaskNotFound
	}
	var deps []string
	if task.Context != nil {
		deps = append([]string(nil), task.Context.Dependencies...)
	}
	if err := fabric.Create(&taskfabric.Task{
		ID:           task.TaskID,
		Capability:   string(task.AgentType),
		Dependencies: deps,
		Priority:     task.Priority,
		// RetryPolicy.MaxRetries counts TOTAL attempts, not retries-after-the-first
		// (taskfabric.CanRetry: Attempts < MaxRetries). MaxRetries: 1 therefore
		// grants ZERO retries — a transient failure finalizes FAILED immediately
		// (v0.4.0 review Bug 2). 2 = first attempt + one retry.
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		// Carry the submission-time metadata in the Checkpoint slot so the
		// scheduler's toModelTask can restore it for the executor (LLM path
		// needs the profile; the outcome recorder needs UsedExperienceID).
		// The envelope type is opaque to the fabric; a genuine progress
		// checkpoint replaces it once a quantum runs (RunQuantum yield).
		Checkpoint: fabricTaskMeta{
			UserProfile:      task.UserProfile,
			Payload:          task.Payload,
			UsedExperienceID: task.UsedExperienceID,
		},
	}); err != nil && err != taskfabric.ErrTaskExists {
		return fmt.Errorf("kernel fabric create: %w", err)
	}
	return nil
}

// subAgentCapability is the minimal capability surface the new-path scorer
// needs for one agent (its type is the declared capability chain).
type subAgentCapability struct {
	ID   string
	Type string
	Load float64
}

// kernelHandle carries the assembled dual-track kernel from agent construction
// to the serve wiring so the policy can be flipped and the Task Fabric
// scheduler started per configuration. mu guards the live-flip state: a mid-run
// flip must start the scheduler exactly once and must not race a concurrent
// flip or the wiring read.
//
// The three Kernel pillars (ares-runtime.md §13) are assembled here:
//   - fabric:   Scheduler pillar (taskfabric: Create/Schedule/Acquire/RunQuantum)
//   - agents:   Lifecycle pillar (agentfabric: spawn/suspend/resume/retire/kill)
//   - recovery: Lifecycle recovery surface (aresrecovery: lease-expiry requeue /
//     checkpoint resume / agent restart)
//   - dual/flag: IPC pillar (agentipc: dual-track dispatch + live flip)
type kernelHandle struct {
	dual *agentipc.DualTrackDispatcher
	flag *agentipc.PolicyFlag

	mu        sync.Mutex
	fabric    *taskfabric.Fabric
	agents    *agentfabric.Fabric
	recovery  *aresrecovery.Recovery
	executors map[string]sub.Agent
	// taskDispatcher is the batch adapter the leader calls (kernelTaskDispatcher).
	// Retained so the live flip can inject the fabric for result read-back.
	taskDispatcher *kernelTaskDispatcher
	// tracker is the shared per-agent load/confidence/priority source for the
	// scheduler and the fabric dispatch path. It is created by the flip and
	// retained so wireKernelLifecycle can inject agent priorities into it
	// (B2: OS-thread-style thread priority).
	tracker *loadTracker
	flipped bool
}

// flipKernelToTaskFabric performs a live mid-run flip of the dispatch kernel
// from the legacy leader policy to the Task Fabric policy (ares-runtime.md P4
// D4: parallel + feature flag gradual cutover, live variant).
//
// Idempotent: a second call after the flip is a no-op — the scheduler keeps
// draining the same fabric. Safe mid-run: the swap order inside
// enableKernelExecution (shadow off → new path → flag) guarantees a dispatch
// racing the flip never double-executes, and in-flight legacy dispatches
// complete synchronously before this returns, so no task is orphaned.
//
// Args:
//   - ctx: lifetime of the Task Fabric scheduler started by the flip.
//   - kernel: the assembled kernel handle.
//   - subAgents: the executor registry (agentID → sub.Agent) for the fabric
//     path; the same registry the startup wiring uses.
//   - store: the shared EventStore the Task Fabric publishes lifecycle events
//     to. When non-nil the scheduler subscribes and drains immediately on
//     dependency-relevant events (GAP 6: event-driven DAG completion) instead
//     of waiting for the next poll tick.
func flipKernelToTaskFabric(ctx context.Context, kernel *kernelHandle, subAgents []sub.Agent, store ares_events.EventStore) {
	if kernel == nil || kernel.dual == nil || kernel.flag == nil {
		return
	}
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if kernel.flipped {
		return
	}
	kernel.fabric = taskfabric.NewFabric()
	if store != nil {
		// Publish task lifecycle transitions to the shared stream from the
		// flip itself: the kernelTaskDispatcher waits on EventTaskCompleted/
		// Failed to reflux the worker result, so the fabric must emit into
		// the same store the dispatch subscribes to. (wireKernelLifecycle
		// also wires this when it runs; doing it here makes the flip
		// self-sufficient for the result contract.)
		kernel.fabric = kernel.fabric.WithEventStore(store)
	}
	kernel.executors = make(map[string]sub.Agent, len(subAgents))
	for _, a := range subAgents {
		if a != nil {
			kernel.executors[a.ID()] = a
		}
	}
	// The batch dispatcher reads the worker's structured output back from the
	// completed fabric task (result-reflux fix): inject the fabric reference
	// now that it exists. Tasks dispatched through the kernel before the flip
	// (shadow mode) never complete in the fabric, so the injection only
	// affects post-flip dispatches.
	if kernel.taskDispatcher != nil {
		kernel.taskDispatcher.fabric = kernel.fabric
	}
	// One shared load tracker for the scheduler and the fabric dispatch path,
	// so Load/Confidence stay consistent across both entry points (GAP4).
	tracker := newLoadTracker()
	kernel.tracker = tracker
	enableKernelExecution(kernel.dual, kernel.fabric)
	kernel.flag.Set(agentipc.PolicyTaskFabric)
	sched := NewKernelScheduler(kernel.fabric, kernel.executors, tracker)
	if store != nil {
		sched.WithEventStore(store)
	}
	// Work stealing: let as many agents pick up ready tasks concurrently as
	// there are executors (bounded internally at 32). Default 0 = unlimited
	// up to the executor count.
	sched.WithMaxConcurrent(0)
	kernel.flipped = true
	log.Printf("kernel: live flip to policy=taskfabric, Task Fabric scheduler started (%d executors)", len(kernel.executors))
	go sched.Run(ctx)
}

// wireKernelPolicy applies the configured dispatch policy to the assembled
// kernel:
//
//   - default / "taskfabric": flips the flag to PolicyTaskFabric, replaces the
//     shadow scorer with the real Task Fabric executor (Create→Schedule→
//     Acquire→RunQuantum via the sub-agent executors), turns shadow mode off
//     (avoiding double execution) and starts the kernelScheduler to drain
//     ReadyTasks. The flip is done through flipKernelToTaskFabric, the same
//     idempotent path a live mid-run flip uses. The Lifecycle pillar
//     (agentfabric + aresrecovery) is assembled at the same time, so the
//     Kernel exposes a single unified entry coordinating Scheduler + Lifecycle
//   - IPC (ares-runtime.md §13 Kernel pillars).
//   - "legacy" (explicit opt-out): keeps the leader path live; the Task
//     Fabric path stays in shadow (scores only, Mismatches observable).
//
// The config-driven flip runs at startup; flipKernelToTaskFabric can be called
// again at runtime for a live mid-run flip without orphaning in-flight tasks
// (ares-runtime.md P4 D4).
func wireKernelPolicy(
	ctx context.Context,
	cfg *ares_config.Config,
	kernel *kernelHandle,
	subAgents []sub.Agent,
	store ares_events.EventStore,
) {
	if kernel == nil || kernel.dual == nil || kernel.flag == nil {
		return
	}
	if strings.ToLower(strings.TrimSpace(cfg.Kernel.Policy)) == "legacy" {
		log.Printf("kernel: policy=legacy (explicit), Task Fabric path in shadow (Mismatches observable)")
		return
	}
	flipKernelToTaskFabric(ctx, kernel, subAgents, store)

	// Apply the configured dispatch wait timeout to the batch adapter so a
	// non-default kernel.dispatch_timeout takes effect (M3). The adapter
	// falls back to the default when unset.
	if kernel.taskDispatcher != nil {
		kernel.taskDispatcher.eventTimeout = parseKernelLoopConfig(cfg).DispatchTimeout
	}

	// Assemble the Lifecycle pillar (agentfabric + aresrecovery) under the
	// same unified Kernel entry. The resource budget (P5: spawn quota
	// enforcement) is wired from config — without this, spawn claims are
	// carried but never enforced (code-review-2025-01-16 #4).
	wireKernelLifecycle(ctx, cfg, kernel, store)
}

// wireKernelLifecycle assembles the Kernel's Lifecycle pillar (ares-runtime.md
// §13): the Agent Fabric (spawn/suspend/resume/retire/kill) with the P5
// resource budget from config, and the Recovery subsystem (lease expiry →
// requeue → checkpoint resume → agent restart) wired to the same Task Fabric
// that the scheduler drains. This closes the "resource claim has no limit"
// gap (code-review-2025-01-16 #4) and gives Recovery a production owner.
//
// Args:
//   - ctx: lifetime of the event-driven recovery loop.
//   - cfg: kernel configuration (Resources budget, MaxRestarts).
//   - kernel: the assembled kernel handle (must be flipped to taskfabric).
//   - store: the shared EventStore the Task Fabric publishes task.* events to
//     and the recovery loop subscribes from (may be nil; then the loop relies
//     on its periodic sweep).
func wireKernelLifecycle(ctx context.Context, cfg *ares_config.Config, kernel *kernelHandle, store ares_events.EventStore) {
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if kernel.fabric == nil || kernel.agents != nil {
		return // not flipped, or lifecycle already wired
	}
	if store != nil {
		// Publish task lifecycle transitions to the shared stream so the
		// event-driven recovery loop (and any observer) sees them.
		kernel.fabric = kernel.fabric.WithEventStore(store)
	}
	agents := agentfabric.NewFabric()
	if len(cfg.Kernel.Resources) > 0 {
		agents = agents.WithResourceBudget(cfg.Kernel.Resources)
	}
	policy := aresrecovery.DefaultRestartPolicy()
	if cfg.Kernel.MaxRestarts > 0 {
		policy.MaxRestarts = cfg.Kernel.MaxRestarts
	}
	kernel.agents = agents
	kernel.recovery = aresrecovery.New(kernel.fabric, agents, policy)
	// Inject agent priorities into the shared tracker so the scheduler's
	// candidate scoring sees them (B2: OS-thread-style thread priority).
	// The fabric is empty at wiring time (recovery spawns happen later), so
	// the sub-agent config is the authoritative production source: each
	// sub-agent's cfg priority is injected under its ID. Fabric agents that
	// already carry their own priority (recovery spawns) are injected too,
	// overriding the config value for that agent.
	if kernel.tracker != nil {
		for _, sub := range cfg.Agents.Sub {
			if sub.Priority > 0 {
				kernel.tracker.SetPriority(sub.ID, sub.Priority)
			}
		}
		for _, id := range agents.Agents() {
			a, err := agents.Get(id)
			if err == nil && a.Priority > 0 {
				kernel.tracker.SetPriority(id, a.Priority)
			}
		}
	}
	log.Printf("kernel: lifecycle wired (agentfabric budget=%d resources, recovery max_restarts=%d)",
		len(cfg.Kernel.Resources), policy.MaxRestarts)
	// Event-driven recovery loop: consumes task lifecycle events and runs the
	// recovery chain. This is the event-driven Agent loop (code-review-
	// 2025-01-16 #2) at the Kernel level — the runtime reacts to TaskAcquired/
	// Yielded/Expired events instead of a command loop. The loop's interval
	// and per-sweep timeout come from config (M3); absent knobs use defaults.
	go runKernelRecoveryLoop(ctx, store, kernel.recovery, parseKernelLoopConfig(cfg))
}

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
		QuotaApplyInterval:    parse(cfg.Kernel.QuotaApplyInterval, quotaApplyInterval),
		QuotaApplyTimeout:     parse(cfg.Kernel.QuotaApplyTimeout, quotaApplyTimeout),
		RecoverySweepInterval: parse(cfg.Kernel.RecoverySweepInterval, recoverySweepInterval),
		RecoverySweepTimeout:  parse(cfg.Kernel.RecoverySweepTimeout, recoverySweepTimeout),
		DispatchTimeout:       parse(cfg.Kernel.DispatchTimeout, kernelDispatchTimeout),
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

// runKernelTraceLoop feeds the shared GlobalTracer (v0.3.0 M4-1) from the
// Task Fabric's lifecycle events on the shared EventStore: task creation,
// ready, acquired, started, completed, failed. It is the write side of
// /observability/spans — without it the dashboard's span endpoint returns an
// empty list even though the tracer is wired. The task id is the event's
// StreamID (taskfabric.Fabric.record appends with t.ID as the stream).
//
// Args:
//   - ctx: stops the loop.
//   - store: the EventStore to subscribe from (nil disables the loop).
//   - tracer: the shared global tracer (nil disables the loop).
func runKernelTraceLoop(ctx context.Context, store ares_events.EventStore, tracer *aresrecovery.GlobalTracer) {
	if store == nil || tracer == nil {
		return
	}
	ch, err := store.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			ares_events.EventTaskCreated,
			ares_events.EventTaskReady,
			ares_events.EventTaskAcquired,
			ares_events.EventTaskStarted,
			ares_events.EventTaskCompleted,
			ares_events.EventTaskFailed,
		},
	})
	if err != nil {
		log.Printf("kernel trace loop: subscribe failed: %v", err)
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Recover per event so a single malformed event cannot kill the
			// loop and stop span collection (M2: kernel loops must not crash
			// the process).
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("kernel trace loop: panic processing event: %v", r)
					}
				}()
				if ev.StreamID == "" {
					return
				}
				// The executor (agent_id) rides in the fabric event payload
				// (taskfabric.Fabric.record), so the same events that trace the
				// TASK also trace the AGENT that drives it — wiring
				// GlobalTracer.TraceAgent to a real production caller (v0.4.0
				// review: TraceAgent was library-only).
				agentID, _ := ev.Payload["agent_id"].(string)
				switch ev.Type {
				case ares_events.EventTaskCreated:
					tracer.TraceTask(ev.StreamID, "created", ev.Payload)
				case ares_events.EventTaskReady:
					tracer.TraceTask(ev.StreamID, "ready", ev.Payload)
				case ares_events.EventTaskAcquired:
					tracer.TraceTask(ev.StreamID, "acquired", ev.Payload)
					if agentID != "" {
						tracer.TraceAgent(agentID, "acquired", ev.Payload)
					}
				case ares_events.EventTaskStarted:
					tracer.TraceTask(ev.StreamID, "started", ev.Payload)
					if agentID != "" {
						tracer.TraceAgent(agentID, "started", ev.Payload)
					}
				case ares_events.EventTaskCompleted:
					tracer.Close(ev.StreamID, "completed")
					if agentID != "" {
						tracer.Close(agentID, "completed")
					}
				case ares_events.EventTaskFailed:
					tracer.Close(ev.StreamID, "failed")
					if agentID != "" {
						tracer.Close(agentID, "failed")
					}
				}
			}()
		}
	}
}

// runKernelRecoveryLoop is the Kernel-level event-driven recovery loop
// (ares-runtime.md §13 + P5, code-review-2025-01-16 #2). It reacts to task
// lifecycle events (TaskExpired / TaskFailed / TaskAcquired / TaskYielded) on
// the shared EventStore and, on each, runs the full recovery chain
// (RequeueExpiredLeases → checkpoint resume → agent restart). A slow periodic
// sweep complements the event channel because TTL-based lease expiry is only
// observable by sweeping.
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
func runKernelRecoveryLoop(ctx context.Context, store ares_events.EventStore, recovery *aresrecovery.Recovery, cfg kernelLoopConfig) {
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
			// Requeue-only recovery (v0.4.0 review Bug 1): RecoverFromAgentDeath
			// re-acquires each requeued task to a freshly SPAWNED replacement
			// agent. That replacement is an agentfabric.Agent — NOT one of the
			// kernelScheduler's registered sub.Agent executors — so nobody can
			// execute the task; it stalls LEASED until its 1-minute lease
			// expires (the phantom-agent bug). The kernel owns execution via
			// the scheduler, which resumes READY tasks from the preserved
			// checkpoint (toModelTask), so the kernel needs only the lease
			// expiry → READY requeue half of the chain. RecoverFromAgentDeath
			// stays for the chaos/sandbox sims where the agent fabric IS the
			// executor.
			if requeued := recovery.RequeueExpiredLeases(); requeued > 0 {
				log.Printf("kernel recovery loop: requeued %d expired task(s)", requeued)
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

type kernelLegacyDispatcher struct {
	inner leader.TaskDispatcher
}

// D dispatches one task through the legacy leader path.
func (d *kernelLegacyDispatcher) D(ctx context.Context, agentID, taskID string, payload any) error {
	if d.inner == nil {
		return agentipc.ErrDispatcherNotRegistered
	}
	task, err := taskFromPayload(taskID, payload)
	if err != nil {
		return fmt.Errorf("kernel legacy dispatch: %w", err)
	}
	_, dispatchErr := d.inner.Dispatch(ctx, []*models.Task{task})
	return dispatchErr
}

// kernelFabricDispatcher is the new Task Fabric path. Its D() behavior depends
// on whether an executeFn is attached (enableKernelExecution):
//
//   - scoring mode (shadow, default): scores the task against the candidate
//     agents with the Kernel's capability-aware formula (taskfabric.Score/Pick)
//     and reports the would-be outcome. It never creates, acquires or executes
//     — a task is never double-run.
//   - execution mode (flag flipped to PolicyTaskFabric): runs the real Task
//     Fabric path via executeFn (Create → Schedule → Acquire → RunQuantum).
type kernelFabricDispatcher struct {
	candidates []subAgentCapability
	executeFn  func(ctx context.Context, task *models.Task) error // nil = scoring only
}

// D routes the task through the kernel's new path: scoring (shadow) or real
// execution (active), depending on whether an executeFn is attached.
func (d *kernelFabricDispatcher) D(ctx context.Context, agentID, taskID string, payload any) error {
	task, err := taskFromPayload(taskID, payload)
	if err != nil {
		return fmt.Errorf("kernel fabric dispatch: %w", err)
	}
	if d.executeFn != nil {
		return d.executeFn(ctx, task)
	}
	if len(d.candidates) == 0 {
		return nil
	}
	cands := make([]taskfabric.Candidate, 0, len(d.candidates))
	for _, c := range d.candidates {
		cands = append(cands, taskfabric.Candidate{
			AgentID:      c.ID,
			Capabilities: []string{c.Type},
			Load:         c.Load,
			Confidence:   1.0, // shadow: no experience store wired here
		})
	}
	if winner := taskfabric.Pick(string(task.AgentType), cands); winner == nil {
		return taskfabric.ErrNoCapableCandidate
	}
	return nil
}

// kernelTaskDispatcher adapts the agentipc.DualTrackDispatcher (single-task
// surface) back to the leader.TaskDispatcher batch surface. The leader keeps
// calling Dispatch(ctx, tasks) as before; each task is routed through the
// kernel dispatcher, so shadow scoring runs for every task without any change
// to leader behavior.
//
// Result flow (fix for the fake-success bug): Dispatch does NOT return a
// placeholder success after submitting. The kernel submits each task to the
// Task Fabric (asynchronous execution — the kernelScheduler owns
// Schedule→Acquire→RunQuantum) and then BLOCKS until the worker's real
// completion event arrives for every task (or the dispatch timeout elapses),
// reconstructing the leader-expected []*models.TaskResult from the actual
// worker outcome. This restores the event-driven result contract the legacy
// leader dispatcher had (dispatchViaEvents: subscribe → publish → collect)
// that kernelTaskDispatcher previously bypassed, which made every leader
// dispatch a silent no-op (success=true, items=0).
type kernelTaskDispatcher struct {
	kernel *agentipc.DualTrackDispatcher
	// store is the shared EventStore the worker's EventTaskCompleted/Failed
	// events land on (subAgent.Execute emits them under the sub-agent's
	// stream with task_id in the payload). Nil disables event collection: a
	// task whose result cannot be observed is reported as failed rather than
	// silently faked as success (code_rules_v2 §0.2: no fake implementation).
	store ares_events.EventStore
	// eventTimeout bounds how long Dispatch waits for a task's completion
	// event. It mirrors the legacy leader dispatcher's timeout contract
	// (DefaultDispatcherTimeoutSeconds = 300s); <= 0 falls back to the same
	// default.
	eventTimeout time.Duration
	// fabric lets Dispatch read the worker's structured output back from the
	// completed fabric task (the scheduler stored it in the quantum
	// checkpoint). Nil disables the read-back: the result still carries the
	// event's textual output. Injected by the live flip.
	fabric *taskfabric.Fabric
}

// newKernelTaskDispatcher assembles the batch adapter with the shared event
// store wired for result collection.
func newKernelTaskDispatcher(kernel *agentipc.DualTrackDispatcher, store ares_events.EventStore) *kernelTaskDispatcher {
	return &kernelTaskDispatcher{kernel: kernel, store: store}
}

// Dispatch routes every task through the kernel dispatcher and aggregates the
// per-task outcomes into the leader-expected []*models.TaskResult shape.
//
// The submission is asynchronous (fabric Create; the kernelScheduler runs the
// task in the background), but the return is synchronous: Dispatch waits for
// each task's real completion/failure event (broadcast subscription, so it
// never competes with the scheduler/trace/recovery consumers) and reports the
// worker's actual outcome. A task that times out or whose result cannot be
// observed is reported as failed with the reason, never as a fake success.
func (d *kernelTaskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, 0, len(tasks))
	if len(tasks) == 0 {
		return results, nil
	}

	// The wait bound applies to BOTH the subscription and the collection loop
	// (C2): the subscription is scoped to waitCtx, so a finished Dispatch
	// cancels it and the store releases its per-subscriber cleanup goroutine.
	// Subscribing with the raw parent ctx would leave every completed
	// Dispatch's subscription (and its goroutine) alive until the parent
	// context is cancelled — accumulating across dispatches.
	timeout := d.eventTimeout
	if timeout <= 0 {
		timeout = kernelDispatchTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Subscribe to the worker completion events BEFORE submitting so no
	// completion can be missed between submit and the collection loop
	// (mirrors dispatchViaEvents: subscribe-then-publish ordering). The
	// broadcast store delivers every matching event to every subscriber, so
	// the scheduler/trace/recovery consumers are unaffected.
	//
	// Pure legacy path (fabric == nil): the inner dispatcher runs each task
	// synchronously inside kernel.Dispatch, so no async completion event
	// exists to wait for — skip the subscription entirely and report the
	// synchronous success below.
	resultCh, subErr := d.subscribeResults(waitCtx)
	if subErr != nil && !errors.Is(subErr, errNoResultSubscription) {
		return d.failAll(tasks, "kernel dispatch: result collection unavailable: "+subErr.Error()), subErr
	}

	// Resolve the per-task final outcome as events arrive.
	pending := make(map[string]*models.Task, len(tasks))
	taskIndex := make(map[string]int, len(tasks))
	for i, task := range tasks {
		if task == nil {
			continue
		}
		pending[task.TaskID] = task
		taskIndex[task.TaskID] = i
		results = append(results, nil) // placeholder, filled by the collection loop
	}

	// Submit every task through the kernel (async: fabric Create; the
	// scheduler executes in the background). A submit error is a real failure
	// for that task — record it and drop it from the pending set.
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := d.kernel.Dispatch(ctx, "", task.TaskID, dispatchPayload(task)); err != nil {
			idx := taskIndex[task.TaskID]
			res := models.NewTaskResult(task.TaskID, task.AgentType)
			res.SetError(err.Error())
			results[idx] = res
			delete(pending, task.TaskID)
		}
	}
	if len(pending) == 0 {
		return results, nil
	}

	// No event store, or no fabric at all: the results cannot be observed via
	// events. Two distinct cases:
	//
	//   - fabric == nil (pure legacy path): the inner dispatcher ran each task
	//     SYNCHRONOUSLY inside d.kernel.Dispatch above, so the task already
	//     completed. Report real success (the dispatch did happen) with an
	//     empty reason — this is not a fake worker output.
	//   - fabric != nil but no store: the worker runs in the background and
	//     cannot be observed without a store. Fail loudly rather than report
	//     fake success (code_rules_v2 §0.2).
	if resultCh == nil {
		if d.fabric == nil {
			for tid, task := range pending {
				idx := taskIndex[tid]
				res := models.NewTaskResult(tid, task.AgentType)
				res.SetSuccess(nil, "dispatched via kernel (legacy sync)")
				results[idx] = res
			}
			return results, nil
		}
		for tid, task := range pending {
			idx := taskIndex[tid]
			res := models.NewTaskResult(tid, task.AgentType)
			res.SetError("kernel dispatch: no event store, result collection disabled")
			results[idx] = res
		}
		return results, nil
	}

	// Block until every submitted task's final outcome is known or the
	// dispatch timeout elapses. This is the synchronous wait that turns the
	// kernel's async execution into the leader-expected blocking dispatch.
	// waitCtx was created above (before subscribing) and is cancelled on
	// return, which also releases the result subscription.
	for len(pending) > 0 {
		select {
		case ev, ok := <-resultCh:
			if !ok {
				// Stream closed: whatever is still pending can never be
				// observed. Fail them explicitly rather than leave nil
				// placeholders (which would aggregate as fake zeros).
				d.failPending(results, pending, taskIndex, "kernel dispatch: event stream closed before result")
				pending = map[string]*models.Task{}
				continue
			}
			tid, ok := ev.Payload["task_id"].(string)
			if !ok || tid == "" {
				continue
			}
			if _, wanted := pending[tid]; !wanted {
				continue
			}
			if res, done := d.resolveOutcome(ev, tid, pending[tid]); done {
				results[taskIndex[tid]] = res
				delete(pending, tid)
			}
		case <-waitCtx.Done():
			// Timeout / parent cancel: mark every still-pending task failed
			// with the reason so the leader never aggregates a fake success.
			d.failPending(results, pending, taskIndex, "kernel dispatch: timed out waiting for worker result: "+waitCtx.Err().Error())
			pending = map[string]*models.Task{}
		}
	}

	// Any nil placeholder left behind (a task was in tasks but never got a
	// result) must never surface: fail it explicitly.
	for i, task := range tasks {
		if task == nil {
			continue
		}
		if results[i] == nil {
			res := models.NewTaskResult(task.TaskID, task.AgentType)
			res.SetError("kernel dispatch: no result observed for task")
			results[i] = res
		}
	}
	return results, nil
}

// errNoResultSubscription signals that no event subscription is needed (no
// store, or pure legacy path without fabric). It is not an error: Dispatch
// falls back to the synchronous legacy success path.
var errNoResultSubscription = errors.New("kernel dispatch: no result subscription needed")

// subscribeResults opens the broadcast subscription on the shared event store
// for the worker's terminal events. Returns errNoResultSubscription when no
// subscription is needed (no store, or pure legacy path without fabric).
func (d *kernelTaskDispatcher) subscribeResults(ctx context.Context) (<-chan *ares_events.Event, error) {
	if d.store == nil || d.fabric == nil {
		return nil, errNoResultSubscription
	}
	return d.store.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			// The worker's terminal events. EventTaskCompleted fires from
			// both subAgent.Execute (carries EventKeyResult, the worker's
			// textual output) and fabric.record (task_id/agent_id/state
			// only). EventTaskFailed fires from subAgent.Execute (real
			// failure, carries error text) and from fabric.Fail (retry
			// requeue — followed by EventTaskReady — or final FAILED). The
			// collection loop resolves the final outcome per task.
			ares_events.EventTaskCompleted,
			ares_events.EventTaskFailed,
		},
	})
}

// failAll builds a failed TaskResult for every non-nil task in tasks.
func (d *kernelTaskDispatcher) failAll(tasks []*models.Task, reason string) []*models.TaskResult {
	results := make([]*models.TaskResult, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		res := models.NewTaskResult(task.TaskID, task.AgentType)
		res.SetError(reason)
		results = append(results, res)
	}
	return results
}

// failPending marks every task still in pending with a failed result. It
// writes through taskIndex into results and is used when the collection loop
// can no longer observe the tasks (stream closed / timeout / cancel).
func (d *kernelTaskDispatcher) failPending(results []*models.TaskResult, pending map[string]*models.Task, taskIndex map[string]int, reason string) {
	for tid, task := range pending {
		idx := taskIndex[tid]
		res := models.NewTaskResult(tid, task.AgentType)
		res.SetError(reason)
		results[idx] = res
	}
}

// dispatchPayload builds the agentipc dispatch payload for a task: the
// capability (agent_type), the DAG dependencies and any opaque user data. The
// UserProfile rides through as the same-process struct reference (no JSON
// round-trip) so the executor sees the real profile — without this it
// silently degrades to executeByType (empty result), the serve no-op chain.
func dispatchPayload(task *models.Task) map[string]any {
	payload := map[string]any{"agent_type": string(task.AgentType)}
	if task.Context != nil && len(task.Context.Dependencies) > 0 {
		payload["dependencies"] = append([]string(nil), task.Context.Dependencies...)
	}
	if task.Payload != nil {
		maps.Copy(payload, task.Payload)
	}
	if task.UserProfile != nil {
		payload["user_profile"] = task.UserProfile
	}
	return payload
}

// resolveOutcome decides whether ev is the final outcome for tid and, if so,
// builds the leader-visible TaskResult from the worker's real output. task is
// the pending task (non-nil when the caller found it in the pending set).
//
// Retry resolution: fabric.Fail publishes EventTaskFailed BEFORE a retry
// requeues the task (failed→ready→re-execute). A single failed event is
// therefore not proof of a final failure. The loop treats failed as final
// only when the event carries the worker's error text (subAgent.Execute
// emits KeyError; fabric.Fail does not) — a bare fabric failed is a retry in
// flight and stays pending until the retry's terminal event resolves it.
func (d *kernelTaskDispatcher) resolveOutcome(ev *ares_events.Event, tid string, task *models.Task) (*models.TaskResult, bool) {
	if task == nil {
		return nil, false
	}
	res := models.NewTaskResult(tid, task.AgentType)

	switch ev.Type {
	case ares_events.EventTaskCompleted:
		// Terminal success. Prefer the worker's structured output read back
		// from the fabric checkpoint; fall back to the event's text.
		if out := d.outcomeFromFabric(tid); out != nil {
			res.SetSuccess(out.items, out.reason)
			res.Metadata = out.metadata
			return res, true
		}
		if text, ok := ev.Payload[ares_events.EventKeyResult].(string); ok && text != "" {
			res.SetSuccess(nil, text)
			return res, true
		}
		// Neither the fabric checkpoint nor the event carries output: the
		// task genuinely completed with no result (e.g. a pure state-machine
		// transition). Success with an empty reason beats faking output.
		res.SetSuccess(nil, "kernel: task completed")
		return res, true
	case ares_events.EventTaskFailed:
		// A worker failure carries the error text under KeyError (subAgent
		// emits it on real failures and output-guard rejections). A
		// fabric-side failed carries only task_id/agent_id/state and is
		// ambiguous: it fires both when a retry requeues the task
		// (failed→ready→re-execute) and when the retry budget is exhausted
		// (final FAILED). Resolve the ambiguity against the fabric state —
		// the authoritative terminal state — instead of guessing from the
		// event alone:
		//   - fabric StateFailed  → final failure (retries exhausted): fail.
		//   - fabric StateReady   → retry in flight: not final, keep waiting.
		//   - no fabric / other   → fall back to the event's error text.
		if errMsg, ok := ev.Payload["error"].(string); ok && errMsg != "" {
			res.SetError(errMsg)
			return res, true
		}
		if d.fabric != nil {
			if tk, err := d.fabric.Task(tid); err == nil && tk != nil {
				if tk.State == taskfabric.StateFailed {
					res.SetError("kernel: task failed after retries exhausted")
					return res, true
				}
				// READY (or anything else): retry in flight, not final.
				return nil, false
			}
		}
		return nil, false
	}
	return nil, false
}

// outcomeFromFabric reads the worker's structured output back from the
// completed fabric task's checkpoint (the scheduler stored items/reason/
// metadata there via RunQuantum → CompleteWithCheckpoint). Returns nil when
// the fabric is not wired, the task is not yet COMPLETED, or the checkpoint
// carries no output.
func (d *kernelTaskDispatcher) outcomeFromFabric(tid string) *taskOutcome {
	if d.fabric == nil {
		return nil
	}
	tk, err := d.fabric.Task(tid)
	if err != nil || tk == nil {
		return nil
	}
	if tk.State != taskfabric.StateCompleted {
		return nil
	}
	return outcomeFromCheckpoint(tk)
}

// taskOutcome is the worker output read back from a completed fabric task.
type taskOutcome struct {
	items    []*models.RecommendItem
	reason   string
	metadata map[string]any
	err      string
}

// outcomeFromCheckpoint extracts the worker output the scheduler stored in
// the quantum checkpoint (see kernelScheduler.execute: items/reason/metadata
// ride inside a map[string]any). The completed checkpoint is a fabricTaskMeta
// envelope (Bug 3 fix: the meta is re-wrapped around every quantum's output),
// so the step output is read from inside the envelope. A missing or non-map
// checkpoint means the task completed without a payload — still a success,
// just empty.
func outcomeFromCheckpoint(tk *taskfabric.Task) *taskOutcome {
	out := &taskOutcome{}
	var cp map[string]any
	switch c := tk.Checkpoint.(type) {
	case fabricTaskMeta:
		if step, ok := c.StepCheckpoint.(map[string]any); ok {
			cp = step
		}
	case map[string]any:
		cp = c
	}
	if cp == nil {
		return out
	}
	if items, ok := cp["items"]; ok {
		if list, ok := items.([]*models.RecommendItem); ok {
			out.items = list
		}
	}
	if reason, ok := cp["reason"].(string); ok {
		out.reason = reason
	}
	if md, ok := cp["metadata"].(map[string]any); ok {
		out.metadata = md
	}
	if e, ok := cp["error"].(string); ok && e != "" {
		out.err = e
	}
	return out
}

// taskFromPayload builds a models.Task from the agentipc dispatch arguments.
// The payload is a map carrying the task's AgentType (capability), its DAG
// dependencies (Task Fabric gate, ares-runtime.md §9) and any opaque user
// data; absent metadata falls back to a default type.
func taskFromPayload(taskID string, payload any) (*models.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task id required")
	}
	task := models.NewTask(taskID, models.AgentTypeTop, nil)
	if m, ok := payload.(map[string]any); ok {
		if at, ok := m["agent_type"].(string); ok && at != "" {
			task.AgentType = models.AgentType(at)
		}
		// UserProfile arrives as the same-process struct reference (the
		// kernelTaskDispatcher passes it through untouched) — OR as a plain
		// map after a JSON round-trip (web serve → HTTP → decode). Both are
		// restored so the executor never sees profile==nil and degrades to
		// executeByType — the serve no-op chain.
		if up, ok := m["user_profile"].(*models.UserProfile); ok && up != nil {
			task.UserProfile = up
		} else if raw, ok := m["user_profile"].(map[string]any); ok {
			if buf, err := json.Marshal(raw); err == nil {
				var up models.UserProfile
				if err := json.Unmarshal(buf, &up); err == nil {
					task.UserProfile = &up
				}
			}
		}
		// Dependencies arrive as []string when the payload passes through the
		// kernel dispatcher directly (kernelTaskDispatcher.Dispatch) and as
		// []any after a JSON round-trip — accept both so the DAG gate is
		// never silently dropped.
		switch deps := m["dependencies"].(type) {
		case []string:
			task.Context.Dependencies = append(task.Context.Dependencies, deps...)
		case []any:
			for _, dep := range deps {
				if s, ok := dep.(string); ok && s != "" {
					task.Context.Dependencies = append(task.Context.Dependencies, s)
				}
			}
		}
		task.Payload = m
	}
	return task, nil
}
