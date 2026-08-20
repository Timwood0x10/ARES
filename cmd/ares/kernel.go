package main

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

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
	executors map[string]CapabilityExecutor
	// taskDispatcher is the batch adapter the leader calls (kernelTaskDispatcher).
	// Retained so the live flip can inject the fabric for result read-back.
	taskDispatcher *kernelTaskDispatcher
	// scheduler is the running kernelScheduler (set at flip time). Retained so
	// wireKernelLifecycle can attach the P3 governance provider once the agent
	// fabric exists (the flip may run before the lifecycle wiring).
	scheduler *kernelScheduler
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
	kernel.executors = make(map[string]CapabilityExecutor, len(subAgents))
	for _, a := range subAgents {
		if a != nil {
			kernel.executors[a.ID()] = a // sub.Agent satisfies CapabilityExecutor
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
	// P3 governance: the scheduler enforces agent budgets (token/tool/deadline)
	// at each quantum boundary. The agent fabric may not exist yet at flip time
	// (wireKernelLifecycle assembles it later), so the scheduler self-heals: a
	// nil governance provider skips enforcement, and wireKernelLifecycle wires
	// it in once the fabric is up.
	if kernel.agents != nil {
		sched.WithGovernance(kernel.agents)
	}
	kernel.scheduler = sched
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
	wireKernelLifecycle(ctx, cfg, kernel, store, subAgents)
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
func wireKernelLifecycle(ctx context.Context, cfg *ares_config.Config, kernel *kernelHandle, store ares_events.EventStore, subAgents []sub.Agent) {
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
	// C1: the configured sub-agents ARE the fabric's dynamic population — each
	// is spawned WITH its execution body (SubAgentCognition), so the scheduler
	// candidate source is the fabric (B1) and a chaos kill takes effect on the
	// next drain. Same single-source rule as createPeerAgents; spawn failures
	// are logged and skipped (the fabric still serves recovery).
	for _, sa := range subAgents {
		if sa == nil {
			continue
		}
		sa := sa // capture for the CognitionFactory closure
		if _, err := agents.Spawn(ctx, agentfabric.SpawnSpec{
			Identity:     sa.ID(),
			Capabilities: []string{string(sa.Type())},
			CognitionFactory: func([]string) agentfabric.Cognition {
				return agentfabric.NewSubAgentCognition(sa)
			},
		}); err != nil {
			log.Printf("kernel: lifecycle spawn of sub-agent %q skipped: %v", sa.ID(), err)
		}
	}
	kernel.recovery = aresrecovery.New(kernel.fabric, agents, policy)
	// P3 governance: the scheduler may have started before this lifecycle
	// wiring (flip runs first); attach the budget provider now so quantum
	// boundaries enforce token/tool/deadline budgets.
	if kernel.scheduler != nil {
		kernel.scheduler.WithGovernance(agents)
		// B1: the scheduler candidate pool now includes every live, IDLE,
		// executable fabric agent (spawn/kill take effect on the next drain).
		kernel.scheduler.WithAgentFabric(agents)
	}
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
	//
	// The leader path is REQUeue-only: the configured sub-agents are already
	// registered as scheduler executors, so an expired lease simply returns the
	// task to READY and an existing capable executor resumes it from its
	// preserved checkpoint (via toModelTask). No replacement executor is
	// injected here — spawning a synthetic "recovery" executor that claims
	// success without running the LLM would hijack the task (W1 review). The
	// peer path (createPeerAgents) wires a real factory + bound registration.
	go runKernelRecoveryLoop(ctx, store, kernel.recovery, parseKernelLoopConfig(cfg), nil, nil, nil)
}
