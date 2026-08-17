package main

import (
	"context"
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
//   - executors: agentID → sub.Agent that runs the acquired task.
//   - tracker: shared per-agent load/confidence source (must be non-nil).
func enableKernelExecution(
	kernel *agentipc.DualTrackDispatcher,
	fabric *taskfabric.Fabric,
	executors map[string]sub.Agent,
	tracker *loadTracker,
) {
	// Turn shadow off first: with the new path about to become live, running
	// legacy in shadow would re-dispatch every task (double execution).
	kernel.SetShadow(false)
	// Replace the shadow-only new path with the executing one.
	exec := &kernelFabricDispatcher{
		candidates: kernelNewPathCandidates(kernel),
		executeFn: func(ctx context.Context, task *models.Task) error {
			return executeFabricTask(ctx, fabric, executors, tracker, task)
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

// executeFabricTask runs the full Task Fabric path for one task: Create →
// Schedule (capability-aware) → Acquire (lease + fencing) → RunQuantum (one
// agent step) → finalize. It is the real (non-shadow) new-path body.
//
// DAG gate: a task whose dependencies are not all COMPLETED is created (so it
// becomes visible to ReadyTasks) but not scheduled here — the kernelScheduler
// drains ReadyTasks and picks it up once its dependencies complete
// (ares-runtime.md §9: DAG as scheduling source). This keeps planner-declared
// dependencies ordering the execution without a leader deciding "B now".
//
// Args:
//   - ctx: task lifetime.
//   - fabric: the Task Fabric that owns the task.
//   - executors: agentID → sub.Agent that runs the acquired task.
//   - tracker: shared per-agent load/confidence source (must be non-nil).
//   - task: the task to run through the fabric path.
func executeFabricTask(
	ctx context.Context,
	fabric *taskfabric.Fabric,
	executors map[string]sub.Agent,
	tracker *loadTracker,
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
		RetryPolicy:  taskfabric.RetryPolicy{MaxRetries: 1},
		Checkpoint:   nil,
	}); err != nil && err != taskfabric.ErrTaskExists {
		return fmt.Errorf("kernel fabric create: %w", err)
	}
	ready, err := fabric.IsReady(task.TaskID)
	if err != nil {
		return fmt.Errorf("kernel fabric isready: %w", err)
	}
	if !ready {
		// Dependencies not satisfied: leave the task READY in the fabric for
		// the kernelScheduler to execute when its dependencies complete.
		return nil
	}
	cands := make([]taskfabric.Candidate, 0, len(executors))
	for agentID, agent := range executors {
		if agent == nil {
			continue
		}
		cands = append(cands, taskfabric.Candidate{
			AgentID:      agentID,
			Capabilities: []string{string(agent.Type())},
			Load:         tracker.Load(agentID),
			Confidence:   tracker.Confidence(agentID),
			Priority:     tracker.Priority(agentID),
		})
	}
	winner, epoch, err := fabric.Schedule(task.TaskID, cands, 5*time.Minute)
	if err != nil {
		// ErrNoCapableCandidate is NOT a dispatch failure: the task is already
		// READY in the fabric (Create above) and the kernelScheduler drains it
		// as soon as a capable executor frees up (real-load scoring can reject a
		// busy single-slot executor here). Returning an error would make the
		// leader adapter count the task as failed even though it is queued —
		// a silent drop (v0.4.0 GAP4 regression fix).
		if errors.Is(err, taskfabric.ErrNoCapableCandidate) {
			return nil
		}
		return fmt.Errorf("kernel fabric schedule: %w", err)
	}
	executor, ok := executors[winner]
	if !ok {
		if releaseErr := fabric.Release(task.TaskID, winner, epoch); releaseErr != nil {
			// The task stays leased to an unknown executor; surface it so the
			// recovery loop can requeue it rather than dropping the error
			// silently (code_rules_v2 §3.1).
			log.Printf("kernel fabric: executor %q not registered and release failed: %v", winner, releaseErr)
		}
		return fmt.Errorf("kernel fabric: executor %q not registered", winner)
	}
	// Track the busy slot while the quantum runs so the next Schedule sees the
	// real load; end records the outcome for confidence (v0.4.0 GAP4).
	tracker.begin(winner)
	err = fabric.RunQuantum(task.TaskID, winner, epoch, func() (any, bool, error) {
		res, execErr := executor.Execute(ctx, task)
		if execErr != nil {
			return nil, false, execErr
		}
		if res != nil && res.Error != "" {
			return nil, false, fmt.Errorf("%s", res.Error)
		}
		return map[string]any{"result": "ok"}, true, nil
	})
	tracker.end(winner, err == nil)
	return err
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
func flipKernelToTaskFabric(ctx context.Context, kernel *kernelHandle, subAgents []sub.Agent) {
	if kernel == nil || kernel.dual == nil || kernel.flag == nil {
		return
	}
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	if kernel.flipped {
		return
	}
	kernel.fabric = taskfabric.NewFabric()
	kernel.executors = make(map[string]sub.Agent, len(subAgents))
	for _, a := range subAgents {
		if a != nil {
			kernel.executors[a.ID()] = a
		}
	}
	// One shared load tracker for the scheduler and the fabric dispatch path,
	// so Load/Confidence stay consistent across both entry points (GAP4).
	tracker := newLoadTracker()
	kernel.tracker = tracker
	enableKernelExecution(kernel.dual, kernel.fabric, kernel.executors, tracker)
	kernel.flag.Set(agentipc.PolicyTaskFabric)
	sched := NewKernelScheduler(kernel.fabric, kernel.executors, tracker)
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
	flipKernelToTaskFabric(ctx, kernel, subAgents)

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
	// Yielded/Expired events instead of a command loop.
	go runKernelRecoveryLoop(ctx, store, kernel.recovery)
}

// recoverySweepInterval is how often the event-driven recovery loop also
// sweeps TTL-based lease expiry (lease expiry is detected by a sweep, not by
// an event, so a periodic safety net is required alongside the event channel).
const recoverySweepInterval = time.Second

// quotaApplyInterval is how often the evolution-aware quota manager pushes
// the current evolution resource budget into the Agent Fabric (v0.4.0 M2-2).
// The GA evolution ticker runs on a 5-minute cadence, so a 1-minute apply
// loop keeps a deployed budget effective within a reasonable window without
// burning CPU.
const quotaApplyInterval = time.Minute

// runKernelQuotaLoop periodically applies the evolution resource policy to
// the Agent Fabric's budget (v0.4.0 M2-2). It applies once at startup so an
// already-deployed policy is effective immediately, then re-applies on a
// fixed interval — Apply is idempotent (replaces the budget in place), so a
// nil/no-op policy leaves the configured kernel resources untouched.
//
// Args:
//   - ctx: stops the loop.
//   - mgr: the quota manager (nil disables the loop).
func runKernelQuotaLoop(ctx context.Context, mgr *aresrecovery.EvolutionAwareQuotaManager) {
	if mgr == nil {
		return
	}
	apply := func(phase string) {
		if err := mgr.Apply(ctx); err != nil {
			log.Printf("kernel: quota apply (%s): %v", phase, err)
		}
	}
	apply("startup")
	ticker := time.NewTicker(quotaApplyInterval)
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

// runKernelTraceLoop feeds the shared GlobalTracer (v0.4.0 M4-1) from the
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
			if ev.StreamID == "" {
				continue
			}
			switch ev.Type {
			case ares_events.EventTaskCreated:
				tracer.TraceTask(ev.StreamID, "created", ev.Payload)
			case ares_events.EventTaskReady:
				tracer.TraceTask(ev.StreamID, "ready", ev.Payload)
			case ares_events.EventTaskAcquired:
				tracer.TraceTask(ev.StreamID, "acquired", ev.Payload)
			case ares_events.EventTaskStarted:
				tracer.TraceTask(ev.StreamID, "started", ev.Payload)
			case ares_events.EventTaskCompleted:
				tracer.Close(ev.StreamID, "completed")
			case ares_events.EventTaskFailed:
				tracer.Close(ev.StreamID, "failed")
			}
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
// Args:
//   - ctx: stops the loop.
//   - store: the EventStore to subscribe from (nil disables the event channel;
//     the periodic sweep still runs).
//   - recovery: the Recovery subsystem (nil disables the loop).
func runKernelRecoveryLoop(ctx context.Context, store ares_events.EventStore, recovery *aresrecovery.Recovery) {
	if recovery == nil {
		return
	}
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
	ticker := time.NewTicker(recoverySweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recovery.RecoverFromAgentDeath(ctx)
		case _, ok := <-events:
			if !ok {
				return
			}
			recovery.RecoverFromAgentDeath(ctx)
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
type kernelTaskDispatcher struct {
	kernel *agentipc.DualTrackDispatcher
}

// Dispatch routes every task through the kernel dispatcher and aggregates the
// per-task outcomes into the leader-expected []*models.TaskResult shape.
func (d *kernelTaskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		payload := map[string]any{"agent_type": string(task.AgentType)}
		if task.Context != nil && len(task.Context.Dependencies) > 0 {
			payload["dependencies"] = append([]string(nil), task.Context.Dependencies...)
		}
		if task.Payload != nil {
			maps.Copy(payload, task.Payload)
		}
		err := d.kernel.Dispatch(ctx, "", task.TaskID, payload)
		res := models.NewTaskResult(task.TaskID, task.AgentType)
		if err != nil {
			res.SetError(err.Error())
		} else {
			res.SetSuccess(nil, "dispatched via kernel")
		}
		results = append(results, res)
	}
	return results, nil
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
