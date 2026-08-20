package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// kernelScheduler is the "no leader" execution engine (ares-runtime.md
// Agents are not orchestrated. They are scheduled). It repeatedly drains
// the fabric's ReadyTasks — the work source — and for each ready task:
//
//	Schedule (capability-aware) → Acquire (lease + fencing) → RunQuantum (one
//	agent step) → finalize (COMPLETED / FAILED / SUSPENDED).
//
// No leader decides "B is done, now run C"; the fabric's dependency-completed
// states make C ready. The scheduler is only a consumer of ReadyTasks.
//
// Failure policy: a scheduling or execution failure for one task is logged and
// the loop continues — one bad task must never take down the scheduler.
type kernelScheduler struct {
	fabric    *taskfabric.Fabric
	executors map[string]sub.Agent
	// tracker supplies real per-agent Load/Confidence to Schedule
	// (v0.3.0 GAP4: real load tracking instead of a static placeholder).
	tracker *loadTracker
	// pollInterval is how often ReadyTasks is drained (default 500ms).
	pollInterval time.Duration
	// ttl is the lease granted to each winning agent.
	ttl time.Duration
	// eventStore is the shared EventStore the Task Fabric publishes lifecycle
	// events to. When set, the scheduler subscribes to dependency-relevant
	// task events (completed/failed/ready/… ) and drains immediately on each,
	// so a task whose DAG dependencies have just completed runs without
	// waiting for the next poll tick (GAP 6: event-driven DAG completion).
	// Nil keeps pure 500ms polling (backward compatible).
	eventStore ares_events.EventStore
	// maxConcurrent caps how many ready tasks run in parallel during one
	// drain (work stealing: multiple agents pick up tasks concurrently).
	// <= 0 falls back to the executor count (bounded by 32).
	maxConcurrent int
	// scheduled counts successfully executed tasks (for observability).
	// atomic: incremented from concurrent drain goroutines (work stealing).
	scheduled atomic.Int64
	// noCandidateMu guards lastNoCandidateLog for throttling unschedulable-task
	// logs.
	noCandidateMu      sync.Mutex
	lastNoCandidateLog time.Time
	// governance is the P3 cognitive-execution budget provider (agentfabric:
	// token/tool budgets + deadline). Nil skips enforcement (backward
	// compatible). When set, execute() checks the budget at each quantum
	// boundary (before CheckResource / after ConsumeResource+Deadline) so a
	// budget-exhausted agent yields the task back instead of burning tokens it
	// cannot afford (aresos-plan.md P3: cooperative yield, not hard preempt).
	governance *agentfabric.Fabric
}

// noCandidateLogInterval throttles "no capable candidate" logs to one per
// window — the condition is a waiting state, not an error worth per-poll noise.
const noCandidateLogInterval = 5 * time.Second

// WithGovernance attaches the P3 budget provider (agentfabric.Fabric). It is
// wired by the kernel lifecycle once the agent fabric exists; without it the
// scheduler enforces nothing (backward compatible with tests and minimal
// wiring). The provider is read-only here — the scheduler checks and consumes,
// it never mutates budgets.
func (s *kernelScheduler) WithGovernance(g *agentfabric.Fabric) *kernelScheduler {
	s.governance = g
	return s
}

// budgetOK reports whether the winning agent may start a new quantum. It is
// the P3 pre-quantum gate: deadline first (a deadline-expired agent is dead
// weight), then the tool budget for this quantum's expected 1 tool round. A
// denial is a cooperative yield — the scheduler returns the task to READY
// instead of burning a quantum the agent cannot afford.
func (s *kernelScheduler) budgetOK(winner string) bool {
	if s.governance == nil {
		return true
	}
	if over, err := s.governance.DeadlineExceeded(winner); err == nil && over {
		return false
	}
	ok, err := s.governance.CheckResource(winner, 0, 1)
	if err != nil {
		return true // unknown agent (not spawned via fabric) → don't block
	}
	return ok
}

// consumeBudget records the winning agent's quantum consumption (1 tool round)
// after a completed quantum. Errors (budget exceeded mid-quantum) are logged,
// not fatal — the task already ran; the next quantum's gate stops further work.
func (s *kernelScheduler) consumeBudget(winner string) {
	if s.governance == nil {
		return
	}
	if err := s.governance.ConsumeResource(winner, 0, 1); err != nil {
		log.Printf("kernel scheduler: agent %s budget consumption: %v", winner, err)
	}
}

// loadTracker records per-agent execution statistics so scheduling decisions
// use real load and confidence instead of static placeholders (v0.3.0 GAP4:
// "线程"的负载真实跟踪). It is shared by the kernel scheduler and the fabric
// dispatch path (executeFabricTask) so both see the same live numbers. mu
// guards all fields; every method is safe for concurrent use.
type loadTracker struct {
	mu       sync.Mutex
	inflight map[string]int // agentID → tasks currently acquired (not finalized)
	done     map[string]int // agentID → tasks finalized
	ok       map[string]int // agentID → tasks that succeeded
	// priorities maps agentID → OS-thread-style scheduling priority (>= 0; 0 =
	// normal). Injected once from the agent fabric at kernel wiring time
	// (B2: thread priority), read by every Schedule candidate build.
	priorities map[string]float64
}

// newLoadTracker creates an empty tracker.
func newLoadTracker() *loadTracker {
	return &loadTracker{
		inflight:   make(map[string]int),
		done:       make(map[string]int),
		ok:         make(map[string]int),
		priorities: make(map[string]float64),
	}
}

// SetPriority records an agent's scheduling priority (B2: thread priority).
// It is set once at wiring time from the agent fabric; 0/absent = normal.
func (t *loadTracker) SetPriority(agentID string, priority float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.priorities[agentID] = priority
}

// Priority returns an agent's scheduling priority (0 = normal when unset).
func (t *loadTracker) Priority(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.priorities[agentID]
}

// begin records that agentID acquired a task.
func (t *loadTracker) begin(agentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight[agentID]++
}

// end records a finalized task for agentID; success reports the outcome.
func (t *loadTracker) end(agentID string, success bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inflight[agentID]--
	t.done[agentID]++
	if success {
		t.ok[agentID]++
	}
}

// Load returns agentID's current utilization in [0,1]. Sub-agents are
// single-slot executors (one quantum at a time), so load is the fraction of
// the slot currently busy.
func (t *loadTracker) Load(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight[agentID] > 0 {
		return 1.0
	}
	return 0.0
}

// Confidence returns agentID's historical success rate in [0,1], defaulting
// to 1.0 (neutral prior) before any history exists.
func (t *loadTracker) Confidence(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.done[agentID] == 0 {
		return 1.0
	}
	return float64(t.ok[agentID]) / float64(t.done[agentID])
}

// NewKernelScheduler creates a scheduler over a fabric with the given
// executors (agentID → sub.Agent). A nil tracker allocates a private one; pass
// a shared tracker to keep Load/Confidence consistent with the fabric dispatch
// path (executeFabricTask).
//
// Args:
//   - fabric: the Task Fabric backing this scheduler.
//   - executors: the agent registry (agentID → sub.Agent).
//   - tracker: per-agent load/confidence source; nil creates a private one.
//
// Returns:
//   - *kernelScheduler: ready to Run.
func NewKernelScheduler(fabric *taskfabric.Fabric, executors map[string]sub.Agent, tracker *loadTracker) *kernelScheduler {
	if tracker == nil {
		tracker = newLoadTracker()
	}
	return &kernelScheduler{
		fabric:       fabric,
		executors:    executors,
		tracker:      tracker,
		pollInterval: 500 * time.Millisecond,
		ttl:          5 * time.Minute,
	}
}

// WithMaxConcurrent caps how many ready tasks run in parallel per drain
// (work stealing). <= 0 falls back to the executor count. Returns the
// scheduler for chaining.
func (s *kernelScheduler) WithMaxConcurrent(n int) *kernelScheduler {
	s.maxConcurrent = n
	return s
}

// Run drains ReadyTasks until ctx is cancelled or the fabric becomes nil.
// It runs synchronously; callers start it in a goroutine. Panics from one
// task's execution are recovered so a single bad step cannot kill the loop.
//
// When an event store is wired (WithEventStore), the scheduler also drains
// immediately on dependency-relevant task events (completed / failed /
// ready / created), so a task whose DAG dependencies just finished runs
// without waiting for the next poll tick (GAP 6). The periodic poll remains
// as a safety net for transitions that do not publish events.
//
// Args:
//   - ctx: lifetime of the scheduling loop.
func (s *kernelScheduler) Run(ctx context.Context) {
	if s.fabric == nil {
		log.Printf("kernel scheduler: fabric nil, scheduler disabled")
		return
	}
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Subscribe to dependency-relevant task events when a store is wired.
	// The channel is nil (and the select case inert) when eventStore is nil,
	// preserving the pure-polling path.
	var events <-chan *ares_events.Event
	if s.eventStore != nil {
		ch, err := s.eventStore.Subscribe(ctx, ares_events.EventFilter{
			Types: []ares_events.EventType{
				ares_events.EventTaskCreated,
				ares_events.EventTaskReady,
				ares_events.EventTaskCompleted,
				ares_events.EventTaskFailed,
				// A yielded task (SUSPENDED) resumes on the next drain; draining
				// on the yield event skips the poll interval between quanta.
				ares_events.EventTaskYielded,
			},
		})
		if err != nil {
			log.Printf("kernel scheduler: event subscribe failed, polling only: %v", err)
		} else {
			events = ch
			log.Printf("kernel scheduler: event-driven drain enabled (task lifecycle events)")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.safeDrain(ctx)
		case <-events:
			// A dependency-relevant task event arrived: drain now instead of
			// waiting up to one poll interval.
			s.safeDrain(ctx)
		}
	}
}

// safeDrain recovers a panic from one drain so the scheduling loop survives a
// single bad drain (M2: kernel loops must not crash the process). Per-task
// panics are already recovered inside drain; this guards the drain itself
// (e.g. a panic inside ReadyTasks).
func (s *kernelScheduler) safeDrain(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("kernel scheduler: panic in drain, continuing: %v", r)
		}
	}()
	s.drain(ctx)
}

// WithEventStore wires the shared EventStore so the scheduler drains on task
// lifecycle events (GAP 6: event-driven DAG completion) instead of waiting
// for the next poll tick. Returns the scheduler for chaining.
func (s *kernelScheduler) WithEventStore(store ares_events.EventStore) *kernelScheduler {
	s.eventStore = store
	return s
}

// drain executes every currently ready task. When the scheduler is configured
// for concurrency (WithMaxConcurrent), ready tasks run in parallel (bounded by
// maxConcurrent) so multiple agents pick up work at the same time — the
// work-stealing substrate at the scheduler side. Panics from one task's
// execution are recovered so a single bad step cannot kill the loop.
// TODO(tech-debt): the per-agent local ready-queue design
// (taskfabric.AgentQueue/Steal, ares-runtime.md §5) was removed as unused
// (v0.3.0 review P1: Steal 空转 — 要么接线要么删除); the shared ReadyTasks()
// queue drained concurrently by bounded goroutines IS the stealing substrate.
// Re-introduce per-agent queues only if profiling shows contention.
func (s *kernelScheduler) drain(ctx context.Context) {
	// Work source: READY tasks (new work) plus SUSPENDED tasks (a yielded
	// quantum the scheduler continues via re-acquire — the SUSPENDED
	// semantics lock: "Continue is the Scheduler's decision via re-acquire").
	tasks := s.fabric.ResumableTasks()
	if len(tasks) == 0 {
		return
	}
	// Priority preemption (v0.3.0 review: fabric.Preempt was production-
	// unused): if a READY task outranks a task that is RUNNING from a
	// previous drain, cooperatively preempt the lower one so a capable
	// executor can pick up the higher-priority work. Preempt hands the task
	// back to READY with its checkpoint preserved (it resumes later), and the
	// fencing token guarantees only the current holder is affected. This runs
	// BEFORE this drain spawns its own goroutines — between quanta — so a
	// quantum is never interrupted mid-step.
	s.preemptLowerPriority(tasks)
	limit := s.maxConcurrent
	if limit <= 0 {
		limit = len(s.executors)
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > 32 {
		limit = 32 // sanity cap: a drain never spawns unbounded goroutines
	}

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for _, taskID := range tasks {
		select {
		case <-ctx.Done():
			return
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if recover() != nil {
					log.Printf("kernel scheduler: panic executing task %q, continuing", id)
				}
			}()
			if err := s.execute(ctx, id); err != nil {
				s.logFailure(id, err)
			}
		}(taskID)
	}
	wg.Wait()
}

// preemptLowerPriority cooperatively preempts any RUNNING task whose priority
// is lower than the highest-priority READY task in this drain, so the next
// drain can hand the executor to the higher-priority work. No-op when no
// priority information exists (all zeros) — the scheduler never churns a
// running task on a tie or on unset priorities. The preempted task keeps its
// checkpoint and returns to READY for a later quantum.
func (s *kernelScheduler) preemptLowerPriority(ready []string) {
	if len(s.executors) == 0 || len(ready) == 0 {
		return
	}
	maxReady := 0
	for _, id := range ready {
		if tk, err := s.fabric.Task(id); err == nil && tk.Priority > maxReady {
			maxReady = tk.Priority
		}
	}
	if maxReady <= 0 {
		return
	}
	for _, rt := range s.fabric.RunningTasks() {
		if rt.Priority >= maxReady {
			continue
		}
		if err := s.fabric.Preempt(rt.ID, rt.Owner, rt.Epoch, "higher-priority task arrived"); err != nil {
			// A concurrently-finalized task (already COMPLETED/FAILED) or a
			// stale epoch is a benign race, not worth log spam.
			continue
		}
		log.Printf("kernel scheduler: preempted %q (priority %d) for higher-priority work (max ready %d)", rt.ID, rt.Priority, maxReady)
	}
}

// logFailure logs a task failure, throttling ErrNoCapableCandidate: an
// unschedulable task is a legitimate "waiting for a capable agent" state that
// the scheduler re-polls every interval, so it must not spam the log. Other
// errors are logged every time (they are transient and need attention).
func (s *kernelScheduler) logFailure(taskID string, err error) {
	if err == taskfabric.ErrNoCapableCandidate {
		now := time.Now()
		s.noCandidateMu.Lock()
		defer s.noCandidateMu.Unlock()
		if now.Sub(s.lastNoCandidateLog) < noCandidateLogInterval {
			return
		}
		s.lastNoCandidateLog = now
	}
	log.Printf("kernel scheduler: execute task %q failed: %v", taskID, err)
}

// fabricTaskMeta is the submission-time metadata envelope stored in a fabric
// task's Checkpoint slot before execution (submitFabricTask) and restored by
// toModelTask when the scheduler builds the models.Task for the executor.
// Without this the executor saw profile==nil and degraded to an empty
// executeByType fallback — a silent no-op that still reported success (the
// serve result-reflux bug chain). The type is declared in the consumer
// package (code_rules_v2: interfaces/contracts live at the consumer); the
// fabric treats Checkpoint as opaque any.
type fabricTaskMeta struct {
	// UserProfile is the profile the leader attached to the task.
	UserProfile *models.UserProfile
	// Payload carries the task's opaque user data (incl. task_desc).
	Payload map[string]any
	// UsedExperienceID is the experience consumed by this task (bandit
	// feedback linkage), preserved for the outcome recorder.
	UsedExperienceID string
	// StepCheckpoint is the quantum's durable progress/output. The scheduler
	// wraps EVERY quantum's returned checkpoint (yield AND done) back into a
	// fabricTaskMeta envelope, so the submission metadata survives a yield:
	// RunQuantum overwrites the task Checkpoint with the step's checkpoint,
	// and re-wrapping it inside the meta envelope means the next quantum's
	// toModelTask can still restore UserProfile/Payload (v0.3.0 review
	// Bug 3: yield→resume otherwise lost the profile and degraded to
	// executeByType). nil before the first quantum runs.
	StepCheckpoint any
}

// execute runs the full fabric path for one task: Schedule → Acquire →
// RunQuantum (delegating the actual work to the winning sub-agent) →
// finalize. Errors are returned to the caller for logging; the fabric
// state machine (RetryPolicy) decides requeue vs. final failure.
func (s *kernelScheduler) execute(ctx context.Context, taskID string) error {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return err
	}
	// Build the candidate list from the registered executors so scheduling is
	// always consistent with what can actually run. Each candidate declares its
	// OWN capabilities (from the agent's Type), NOT the task's — the scorer
	// compares the task's required capability against what the agent can do.
	// Load/Confidence come from the live tracker (v0.3.0 GAP4): real busy
	// fraction and historical success rate, not static placeholders.
	cands := make([]taskfabric.Candidate, 0, len(s.executors))
	for agentID, agent := range s.executors {
		if agent == nil {
			continue
		}
		cands = append(cands, taskfabric.Candidate{
			AgentID:      agentID,
			Capabilities: []string{string(agent.Type())},
			Load:         s.tracker.Load(agentID),
			Confidence:   s.tracker.Confidence(agentID),
			Priority:     s.tracker.Priority(agentID),
		})
	}
	if len(cands) == 0 {
		return taskfabric.ErrNoCapableCandidate
	}
	winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)
	if err != nil {
		return err
	}
	executor, ok := s.executors[winner]
	if !ok || executor == nil {
		// Release so the task can be retried by another agent. A failed
		// release leaves the task leased — surface it instead of dropping
		// the error (code_rules_v2 §3.1).
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Printf("kernel scheduler: release %q for missing executor %q failed: %v", taskID, winner, releaseErr)
		}
		return nil
	}
	// Track the busy slot while the quantum runs so the next Schedule sees the
	// real load (v0.3.0 GAP4); end records the outcome for confidence.
	// Preserve the submission metadata across the quantum: the task's current
	// checkpoint is the meta envelope written by submitFabricTask or by a
	// previous quantum (yield/done re-wraps below). Capturing it here — before
	// RunQuantum overwrites the task Checkpoint — is what keeps UserProfile
	// alive through an arbitrary number of yield→resume cycles.
	meta := fabricTaskMeta{}
	if m, ok := tk.Checkpoint.(fabricTaskMeta); ok {
		meta = m
	}
	// P3 pre-quantum gate: if the winner's budget/deadline is exhausted, yield
	// the task back (release the lease) so another capable agent (or a later
	// quantum after ResetResource) can pick it up. This closes the P3 loop at
	// the scheduler boundary — the fabric's state machine (Release→READY)
	// drives the requeue, matching the plan's "budget.exceeded → yield()".
	if !s.budgetOK(winner) {
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Printf("kernel scheduler: release %q for budget-exhausted %q failed: %v", taskID, winner, releaseErr)
		}
		return nil
	}
	s.tracker.begin(winner)
	err = s.fabric.RunQuantum(taskID, winner, epoch, func() (any, bool, error) {
		out, stepErr := executor.ExecuteStep(ctx, s.toModelTask(tk))
		if stepErr != nil {
			// A step error flows to fabric.Fail, which requeues (retry budget)
			// or finalizes FAILED — the fabric owns the retry policy.
			return nil, false, stepErr
		}
		if out == nil {
			return nil, false, fmt.Errorf("executor returned a nil step outcome")
		}
		if out.Result != nil && out.Result.Error != "" {
			return nil, false, fmt.Errorf("%s", out.Result.Error)
		}
		if !out.Done {
			// Yield (P1.1 Execution Quantum): the quantum made progress but the
			// task is not complete. RunQuantum's not-done branch SUSPENDEDs the
			// task with this checkpoint preserved; the next drain re-acquires
			// it and the next quantum resumes from this PCB. Re-wrapping the
			// submission metadata keeps UserProfile/Payload/UsedExperienceID
			// alive across yield→resume cycles (Bug 3).
			return fabricTaskMeta{
				UserProfile:      meta.UserProfile,
				Payload:          meta.Payload,
				UsedExperienceID: meta.UsedExperienceID,
				StepCheckpoint:   out.Checkpoint,
			}, false, nil
		}
		// Done: carry the worker's real output back through the fabric so the
		// leader dispatch (which waits on task completion) can surface the
		// actual items instead of the old "ok" placeholder — the last link
		// in the no-op chain. The result rides in the quantum checkpoint:
		// RunQuantum's done branch stores it via CompleteWithCheckpoint, and
		// the dispatcher reads it back after polling the task to COMPLETED.
		outMap := map[string]any{"result": "ok"}
		if res := out.Result; res != nil {
			if items := res.Items; len(items) > 0 {
				outMap["items"] = items
			}
			if res.Reason != "" {
				outMap["reason"] = res.Reason
			}
			if len(res.Metadata) > 0 {
				outMap["metadata"] = res.Metadata
			}
		}
		// Re-wrap the step output in the metadata envelope so the dispatcher's
		// outcomeFromFabric unwraps it on COMPLETED (same as pre-quantum).
		return fabricTaskMeta{
			UserProfile:      meta.UserProfile,
			Payload:          meta.Payload,
			UsedExperienceID: meta.UsedExperienceID,
			StepCheckpoint:   outMap,
		}, true, nil
	})
	s.tracker.end(winner, err == nil)
	// P3 post-quantum bookkeeping: record the quantum's consumption (1 tool
	// round) so the next gate sees the new balance. Runs even on step errors —
	// the quantum did execute (or partially execute) and spent budget.
	if s.governance != nil {
		s.consumeBudget(winner)
	}
	if err == nil {
		s.scheduled.Add(1)
	}
	return err
}

// toModelTask maps a fabric Task back to the models.Task shape the sub-agent
// executor expects. The submission-time metadata (UserProfile + Payload +
// UsedExperienceID) rides in the fabric Checkpoint slot inside a tagged
// fabricTaskMeta envelope; restoring it here is what lets the executor take
// the real LLM path instead of degrading to an empty fallback result
// (profile==nil → executeByType). A genuine progress checkpoint (plain map,
// written by RunQuantum) is preserved in the payload so a resumed quantum can
// observe where the previous step left off.
func (s *kernelScheduler) toModelTask(tk *taskfabric.Task) *models.Task {
	t := models.NewTask(tk.ID, models.AgentType(tk.Capability), nil)
	if tk.Checkpoint != nil {
		if meta, ok := tk.Checkpoint.(fabricTaskMeta); ok {
			t.UserProfile = meta.UserProfile
			t.Payload = meta.Payload
			t.UsedExperienceID = meta.UsedExperienceID
			// A resumed quantum observes where the previous step left off
			// (Bug 3): the step checkpoint rides inside the meta envelope,
			// surfaced to the executor as payload["checkpoint"] exactly like
			// the legacy plain-map case below.
			if meta.StepCheckpoint != nil {
				if t.Payload == nil {
					t.Payload = make(map[string]any)
				}
				t.Payload["checkpoint"] = meta.StepCheckpoint
			}
			return t
		}
		t.Payload = map[string]any{"checkpoint": tk.Checkpoint}
	}
	return t
}
