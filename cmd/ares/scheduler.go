package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
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
//
// Dynamic executor registration (W1): RegisterExecutor / UnregisterExecutor
// let the recovery loop inject a replacement agent at runtime so a recovered
// task is executed by a real executor, not a phantom agent. execMu guards the
// executor map for concurrent register/unregister/lookup from drain goroutines.
type kernelScheduler struct {
	fabric    *taskfabric.Fabric
	executors map[string]CapabilityExecutor
	// execMu guards the executors map for dynamic register/unregister (W1).
	// A separate lock avoids reentrancy with the fabric mutex during drain.
	execMu sync.RWMutex
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
	// boundExecutors maps taskID → executorID for W1 recovery executors. A
	// recovery executor is bound to exactly one task: execute() only offers it
	// as a candidate for that task, never for another READY task, so a
	// replacement spawned for a recovered task cannot hijack new tasks.
	// Guarded by execMu.
	boundExecutors map[string]string
	// attribution is the optional W4 execution-outcome source. When wired,
	// execute() records every finalized outcome (agent, capability, success)
	// so the evolution feedback loop can read attribution and push derived
	// confidence into the tracker. Nil skips recording (backward compatible).
	attribution *aresrecovery.ExecutionAttribution
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
//
// W4 Evolution feedback: SetAgentConfidence lets the evolution feedback
// adapter override the computed confidence with an evolution-derived value.
// When a confidenceOverride is set (> 0), Confidence returns it instead of
// the raw success rate. This is the write path for the W4 feedback loop:
// execution results → evolution → SetAgentConfidence → next Schedule.
type loadTracker struct {
	mu       sync.Mutex
	inflight map[string]int // agentID → tasks currently acquired (not finalized)
	done     map[string]int // agentID → tasks finalized
	ok       map[string]int // agentID → tasks that succeeded
	// priorities maps agentID → OS-thread-style scheduling priority (>= 0; 0 =
	// normal). Injected once from the agent fabric at kernel wiring time
	// (B2: thread priority), read by every Schedule candidate build.
	priorities map[string]float64
	// confidenceOverride maps agentID → evolution-derived confidence [0,1].
	// When >= 0, Confidence returns this instead of the raw success rate.
	// Set by the W4 evolution feedback adapter (SetAgentConfidence).
	// A negative value means "no override" (use the computed success rate).
	// The map uses ok=false to mean "no override set"; a value of 0.0 is a
	// valid confidence (total failure) and must not be treated as unset.
	confidenceOverride map[string]float64
}

// newLoadTracker creates an empty tracker.
func newLoadTracker() *loadTracker {
	return &loadTracker{
		inflight:           make(map[string]int),
		done:               make(map[string]int),
		ok:                 make(map[string]int),
		priorities:         make(map[string]float64),
		confidenceOverride: make(map[string]float64),
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
// to 1.0 (neutral prior) before any history exists. When the W4 evolution
// feedback adapter has set a confidence override (>= 0), that value is
// returned instead — the evolution system's derived confidence takes
// priority over the raw success rate.
func (t *loadTracker) Confidence(agentID string) float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if override, ok := t.confidenceOverride[agentID]; ok && override >= 0 {
		return override
	}
	if t.done[agentID] == 0 {
		return 1.0
	}
	return float64(t.ok[agentID]) / float64(t.done[agentID])
}

// SetAgentConfidence updates the evolution-derived confidence for agentID
// (W4: 回写 scheduler scoring). A value in [0,1] overrides the computed
// success rate; a negative value (< 0) clears the override (revert to raw
// success rate). This method implements the aresrecovery.ConfidenceInjector
// interface so the EvolutionFeedbackAdapter can push confidence updates
// without importing the scheduler package.
func (t *loadTracker) SetAgentConfidence(agentID string, confidence float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if confidence < 0 {
		delete(t.confidenceOverride, agentID)
		return
	}
	t.confidenceOverride[agentID] = confidence
}

// NewKernelScheduler creates a scheduler over a fabric with the given
// executors (agentID → CapabilityExecutor). A nil tracker allocates a private
// one; pass a shared tracker to keep Load/Confidence consistent with the fabric
// dispatch path (executeFabricTask).
//
// Args:
//   - fabric: the Task Fabric backing this scheduler.
//   - executors: the agent registry (agentID → CapabilityExecutor).
//   - tracker: per-agent load/confidence source; nil creates a private one.
//
// Returns:
//   - *kernelScheduler: ready to Run.
func NewKernelScheduler(fabric *taskfabric.Fabric, executors map[string]CapabilityExecutor, tracker *loadTracker) *kernelScheduler {
	if tracker == nil {
		tracker = newLoadTracker()
	}
	return &kernelScheduler{
		fabric:         fabric,
		executors:      executors,
		boundExecutors: make(map[string]string),
		tracker:        tracker,
		pollInterval:   500 * time.Millisecond,
		ttl:            5 * time.Minute,
	}
}

// WithAttribution attaches the W4 execution-outcome source (aresrecovery.
// ExecutionAttribution). When set, execute() records every finalized outcome
// after the quantum. Returns the scheduler for chaining.
func (s *kernelScheduler) WithAttribution(a *aresrecovery.ExecutionAttribution) *kernelScheduler {
	s.attribution = a
	return s
}

// WithMaxConcurrent caps how many ready tasks run in parallel per drain
// (work stealing). <= 0 falls back to the executor count. Returns the
// scheduler for chaining.
func (s *kernelScheduler) WithMaxConcurrent(n int) *kernelScheduler {
	s.maxConcurrent = n
	return s
}

// RegisterExecutor dynamically registers an executor under agentID so the
// scheduler can execute tasks assigned to it (W1: production-grade recovery
// 闭环). The recovery loop calls this after spawning a replacement agent so
// the new agent is a real executor, not a phantom. Safe for concurrent use
// with drain goroutines: execMu guards the map.
//
// Args:
//   - agentID: the replacement agent's identity.
//   - executor: the CapabilityExecutor (must be non-nil).
func (s *kernelScheduler) RegisterExecutor(agentID string, executor CapabilityExecutor) {
	if agentID == "" || executor == nil {
		return
	}
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.executors[agentID] = executor
	log.Printf("kernel scheduler: registered replacement executor %q", agentID)
}

// RegisterExecutorForTask registers an executor bound to exactly one task
// (W1 recovery). The executor is only ever offered as a candidate for taskID
// — execute() filters it out for every other READY task, so a replacement
// spawned for a recovered task can never hijack a brand-new task. When the
// task reaches a terminal state (COMPLETED / FAILED) execute() unregisters
// the bound executor automatically.
//
// Args:
//   - taskID: the recovered task the executor is bound to.
//   - agentID: the replacement executor's identity.
//   - executor: the CapabilityExecutor (must be non-nil).
func (s *kernelScheduler) RegisterExecutorForTask(taskID, agentID string, executor CapabilityExecutor) {
	if taskID == "" || agentID == "" || executor == nil {
		return
	}
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.executors[agentID] = executor
	s.boundExecutors[taskID] = agentID
	log.Printf("kernel scheduler: registered recovery executor %q bound to task %q", agentID, taskID)
}

// boundFor returns the executor id bound to taskID, if any. Safe for
// concurrent use.
func (s *kernelScheduler) boundFor(taskID string) (string, bool) {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	id, ok := s.boundExecutors[taskID]
	return id, ok
}

// unbindFor removes the executor binding for taskID and returns the bound
// executor id ("" when none). Callers use it to unregister a recovery
// executor once its task reaches a terminal state.
func (s *kernelScheduler) unbindFor(taskID string) string {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	id := s.boundExecutors[taskID]
	delete(s.boundExecutors, taskID)
	return id
}

// UnregisterExecutor removes an executor from the registry and clears any
// task binding it had. The recovery loop calls this when a replacement agent
// itself fails, so stale executors are not selected for scheduling. Safe for
// concurrent use.
//
// Args:
//   - agentID: the agent to remove.
func (s *kernelScheduler) UnregisterExecutor(agentID string) {
	if agentID == "" {
		return
	}
	s.execMu.Lock()
	defer s.execMu.Unlock()
	delete(s.executors, agentID)
	for taskID, boundID := range s.boundExecutors {
		if boundID == agentID {
			delete(s.boundExecutors, taskID)
		}
	}
}

// HasCapableExecutor reports whether a registered, unbound executor can
// execute taskID (capability overlap > 0). The recovery loop calls this for
// each requeued task to decide whether a replacement executor is needed:
// when an existing executor can already resume the task, no spawn is
// required and the task simply returns to READY.
func (s *kernelScheduler) HasCapableExecutor(taskID string) bool {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return false
	}
	if _, ok := s.boundFor(taskID); ok {
		return true
	}
	execs := s.allExecutors()
	for agentID, agent := range execs {
		if agent == nil {
			continue
		}
		if s.isBoundToOtherTask(agentID) {
			continue
		}
		cand := taskfabric.Candidate{Capabilities: []string{string(agent.Type())}}
		if taskfabric.Score(tk.Capability, cand) > 0 {
			return true
		}
	}
	return false
}

// isBoundToOtherTask reports whether agentID is a recovery executor bound to
// some task (so it must not be offered for any other task).
func (s *kernelScheduler) isBoundToOtherTask(agentID string) bool {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	for _, boundID := range s.boundExecutors {
		if boundID == agentID {
			return true
		}
	}
	return false
}

// executorCount returns the current number of registered executors under a
// read lock. Used by drain to compute the concurrency limit.
func (s *kernelScheduler) executorCount() int {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	return len(s.executors)
}

// lookupExecutor safely retrieves an executor under a read lock.
func (s *kernelScheduler) lookupExecutor(agentID string) (CapabilityExecutor, bool) {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	e, ok := s.executors[agentID]
	return e, ok && e != nil
}

// allExecutors returns a snapshot of the executor map under a read lock. The
// drain goroutines iterate this snapshot to build the candidate list.
func (s *kernelScheduler) allExecutors() map[string]CapabilityExecutor {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	out := make(map[string]CapabilityExecutor, len(s.executors))
	for k, v := range s.executors {
		out[k] = v
	}
	return out
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
		limit = s.executorCount()
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
	if s.executorCount() == 0 || len(ready) == 0 {
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

// Submission-time metadata (UserProfile + Payload + UsedExperienceID) rides in
// the task's Checkpoint slot inside a *taskfabric.CheckpointEnvelope (W3
// schema, unversioned-v0 → versioned-v1 migration). Without the envelope the
// executor saw profile==nil and degraded to an empty executeByType fallback —
// a silent no-op that still reported success (the serve result-reflux bug
// chain). The scheduler re-wraps EVERY quantum's returned checkpoint (yield
// AND done) back into an envelope (EncodeCheckpoint), so the submission
// metadata survives a yield: RunQuantum overwrites the task Checkpoint with
// the step's checkpoint, and re-wrapping it inside the envelope means the next
// quantum's toModelTask can still restore UserProfile/Payload (v0.3.0 review
// Bug 3: yield→resume otherwise lost the profile and degraded to
// executeByType). nil before the first quantum runs.

// execute runs the full fabric path for one task: Schedule → Acquire →
// RunQuantum (delegating the actual work to the winning sub-agent) →
// finalize. Errors are returned to the caller for logging; the fabric
// state machine (RetryPolicy) decides requeue vs. final failure.
func (s *kernelScheduler) execute(ctx context.Context, taskID string) error {
	// Build the candidate list from the registered executors so scheduling is
	// always consistent with what can actually run. Each candidate declares its
	// OWN capabilities (from the agent's Type), NOT the task's — the scorer
	// compares the task's required capability against what the agent can do.
	// Load/Confidence come from the live tracker (v0.3.0 GAP4): real busy
	// fraction and historical success rate, not static placeholders.
	//
	// W1 recovery binding: a recovery executor bound to THIS task is the only
	// candidate (the replacement must run the task it was spawned for). Bound
	// executors of OTHER tasks are excluded so a replacement can never hijack
	// a different READY task.
	execs := s.allExecutors()
	if boundID, bound := s.boundFor(taskID); bound {
		cands := make([]taskfabric.Candidate, 0, 1)
		if agent, ok := execs[boundID]; ok && agent != nil {
			cands = append(cands, taskfabric.Candidate{
				AgentID:      boundID,
				Capabilities: []string{string(agent.Type())},
				Load:         s.tracker.Load(boundID),
				Confidence:   s.tracker.Confidence(boundID),
				Priority:     s.tracker.Priority(boundID),
			})
		}
		if len(cands) == 0 {
			// The bound executor is gone (already unregistered) — fall through
			// to the normal pool so the task is not stranded.
			return s.executeUnbound(ctx, taskID)
		}
		return s.executeWithCandidates(ctx, taskID, cands)
	}
	return s.executeUnbound(ctx, taskID)
}

// executeUnbound runs the fabric path for a task with no recovery binding:
// the candidate pool is every registered, unbound executor whose capability
// overlaps the task.
func (s *kernelScheduler) executeUnbound(ctx context.Context, taskID string) error {
	execs := s.allExecutors()
	cands := make([]taskfabric.Candidate, 0, len(execs))
	for agentID, agent := range execs {
		if agent == nil {
			continue
		}
		if s.isBoundToOtherTask(agentID) {
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
	return s.executeWithCandidates(ctx, taskID, cands)
}

// executeWithCandidates runs the shared Schedule → Acquire → RunQuantum →
// finalize path for a prebuilt candidate list. The task capability is read
// for W4 attribution at the outcome boundary.
func (s *kernelScheduler) executeWithCandidates(ctx context.Context, taskID string, cands []taskfabric.Candidate) error {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		return taskfabric.ErrNoCapableCandidate
	}
	winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)
	if err != nil {
		return err
	}
	executor, ok := s.lookupExecutor(winner)
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
	meta, decodeErr := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if decodeErr != nil {
		log.Printf("kernel scheduler: decode checkpoint for task %q: %v", taskID, decodeErr)
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
			return taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
				UserProfile:      meta.UserProfile,
				Payload:          meta.Payload,
				UsedExperienceID: meta.UsedExperienceID,
				StepCheckpoint:   out.Checkpoint,
			}), false, nil
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
		return taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
			UserProfile:      meta.UserProfile,
			Payload:          meta.Payload,
			UsedExperienceID: meta.UsedExperienceID,
			StepCheckpoint:   outMap,
		}), true, nil
	})
	s.tracker.end(winner, err == nil)
	// W4 evolution feedback: record the outcome for the feedback loop. The
	// attribution is read by the EvolutionFeedbackAdapter and pushed back into
	// the tracker's confidence override (SetAgentConfidence) so the next
	// Schedule sees the evolution-derived confidence.
	if s.attribution != nil {
		s.attribution.Record(winner, tk.Capability, err == nil)
	}
	// P3 post-quantum bookkeeping: record the quantum's consumption (1 tool
	// round) so the next gate sees the new balance. Runs even on step errors —
	// the quantum did execute (or partially execute) and spent budget.
	if s.governance != nil {
		s.consumeBudget(winner)
	}
	if err == nil {
		s.scheduled.Add(1)
	}
	// W1 recovery binding: when the task reaches a terminal state, unregister
	// the bound recovery executor so the executor map does not grow unboundedly
	// and the executor is not offered as a candidate for other tasks.
	tk2, tkErr := s.fabric.Task(taskID)
	if tkErr == nil && (tk2.State == taskfabric.StateCompleted || tk2.State == taskfabric.StateFailed) {
		if boundID := s.unbindFor(taskID); boundID != "" {
			s.UnregisterExecutor(boundID)
			log.Printf("kernel scheduler: unregistered recovery executor %q after task %q reached %s", boundID, taskID, tk2.State)
		}
	}
	return err
}

// toModelTask maps a fabric Task back to the models.Task shape the sub-agent
// executor expects. The submission-time metadata (UserProfile + Payload +
// UsedExperienceID) rides in the fabric Checkpoint slot inside a
// *taskfabric.CheckpointEnvelope (W3 schema); restoring it here is what lets
// the executor take the real LLM path instead of degrading to an empty
// fallback result (profile==nil → executeByType). A genuine progress
// checkpoint (plain map, written by RunQuantum) is preserved in the payload so
// a resumed quantum can observe where the previous step left off. Decode goes
// through the single shared protocol (taskfabric.DecodeCheckpoint) — the same
// path recovery and every other consumer use.
func (s *kernelScheduler) toModelTask(tk *taskfabric.Task) *models.Task {
	t := models.NewTask(tk.ID, models.AgentType(tk.Capability), nil)
	if tk.Checkpoint == nil {
		return t
	}
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		log.Printf("kernel scheduler: toModelTask decode checkpoint for task %q: %v", tk.ID, err)
		t.Payload = map[string]any{"checkpoint": tk.Checkpoint}
		return t
	}
	t.UserProfile = reifyUserProfile(dc.UserProfile)
	t.Payload = dc.Payload
	t.UsedExperienceID = dc.UsedExperienceID
	// A resumed quantum observes where the previous step left off (Bug 3):
	// the step checkpoint is surfaced to the executor as payload["checkpoint"].
	if dc.StepCheckpoint != nil {
		if t.Payload == nil {
			t.Payload = make(map[string]any)
		}
		t.Payload["checkpoint"] = dc.StepCheckpoint
	}
	return t
}

// reifyUserProfile converts a decoded envelope UserProfile (typed pointer, or
// a raw map after a JSON round-trip) back into the *models.UserProfile the
// executor expects. A value that cannot be reified yields nil (the executor
// then degrades exactly as before).
func reifyUserProfile(v any) *models.UserProfile {
	switch up := v.(type) {
	case *models.UserProfile:
		return up
	case nil:
		return nil
	default:
		if buf, err := json.Marshal(up); err == nil {
			var p models.UserProfile
			if err := json.Unmarshal(buf, &p); err == nil {
				return &p
			}
		}
		return nil
	}
}
