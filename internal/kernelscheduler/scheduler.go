package kernelscheduler

import (
	"context"
	"encoding/json"
	"errors"
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

// Scheduler is the "no leader" execution engine (ares-runtime.md
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
type Scheduler struct {
	fabric    *taskfabric.Fabric
	executors map[string]CapabilityExecutor
	// execMu guards the executors map for dynamic register/unregister (W1).
	// A separate lock avoids reentrancy with the fabric mutex during drain.
	execMu sync.RWMutex
	// tracker supplies real per-agent Load/Confidence to Schedule
	// (v0.3.0 GAP4: real load tracking instead of a static placeholder).
	tracker *LoadTracker
	// PollInterval is how often ReadyTasks is drained (default 500ms).
	PollInterval time.Duration
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
	Scheduled atomic.Int64
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
	// agents is the optional agentfabric.Fabric whose live IDLE agents are
	// schedulable candidates (aresos-agentos-plan B1: scheduler 候选来自
	// agentfabric 动态群体). Every drain re-queries the fabric, so a spawned
	// agent becomes schedulable immediately and a killed one disappears — no
	// explicit registry sync. Nil keeps the static executor registry only
	// (backward compatible with tests and minimal wiring).
	agents *agentfabric.Fabric
}

// noCandidateLogInterval throttles "no capable candidate" logs to one per
// window — the condition is a waiting state, not an error worth per-poll noise.
const noCandidateLogInterval = 5 * time.Second

// WithGovernance attaches the P3 budget provider (agentfabric.Fabric). It is
// wired by the kernel lifecycle once the agent fabric exists; without it the
// scheduler enforces nothing (backward compatible with tests and minimal
// wiring). The provider is read-only here — the scheduler checks and consumes,
// it never mutates budgets.
func (s *Scheduler) WithGovernance(g *agentfabric.Fabric) *Scheduler {
	s.governance = g
	return s
}

// budgetOK reports whether the winning agent may start a new quantum. It is
// the P3 pre-quantum gate: deadline first (a deadline-expired agent is dead
// weight), then the tool budget for this quantum's expected 1 tool round. A
// denial is a cooperative yield — the scheduler returns the task to READY
// instead of burning a quantum the agent cannot afford.
func (s *Scheduler) budgetOK(winner string) bool {
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
func (s *Scheduler) consumeBudget(winner string) {
	if s.governance == nil {
		return
	}
	if err := s.governance.ConsumeResource(winner, 0, 1); err != nil {
		log.Printf("kernel scheduler: agent %s budget consumption: %v", winner, err)
	}
}

// New creates a scheduler over a fabric with the given
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
//   - *Scheduler: ready to Run.
func New(fabric *taskfabric.Fabric, executors map[string]CapabilityExecutor, tracker *LoadTracker) *Scheduler {
	if tracker == nil {
		tracker = NewLoadTracker()
	}
	return &Scheduler{
		fabric:         fabric,
		executors:      executors,
		boundExecutors: make(map[string]string),
		tracker:        tracker,
		PollInterval:   500 * time.Millisecond,
		ttl:            5 * time.Minute,
	}
}

// WithAttribution attaches the W4 execution-outcome source (aresrecovery.
// ExecutionAttribution). When set, execute() records every finalized outcome
// after the quantum. Returns the scheduler for chaining.
func (s *Scheduler) WithAttribution(a *aresrecovery.ExecutionAttribution) *Scheduler {
	s.attribution = a
	return s
}

// WithAgentFabric attaches the agent lifecycle fabric so every live, IDLE,
// executable fabric agent is a schedulable candidate (B1: 单一调度回路 —
// scheduler 只认统一 Agent). It is wired by the kernel lifecycle once the
// fabric exists; nil keeps the static executor registry only. Returns the
// scheduler for chaining.
func (s *Scheduler) WithAgentFabric(f *agentfabric.Fabric) *Scheduler {
	s.agents = f
	return s
}

// WithMaxConcurrent caps how many ready tasks run in parallel per drain
// (work stealing). <= 0 falls back to the executor count. Returns the
// scheduler for chaining.
func (s *Scheduler) WithMaxConcurrent(n int) *Scheduler {
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
func (s *Scheduler) Run(ctx context.Context) {
	if s.fabric == nil {
		log.Printf("kernel scheduler: fabric nil, scheduler disabled")
		return
	}
	ticker := time.NewTicker(s.PollInterval)
	defer ticker.Stop()

	// Cooperative-preemption watcher (BUG-KSCHED-001): drain() blocks on
	// wg.Wait() until every dispatched quantum finishes, so preemption
	// checked only at drain entry could never observe a RUNNING task — the
	// branch was unreachable through the production loop. This managed worker
	// (deterministic exit on ctx.Done, per-sweep recover per code_rules_v2
	// §4.1/§4.2) scans independently of the blocking drain. Preemption stays
	// cooperative: it only mutates durable state; the stale holder's late
	// completion is rejected by the fencing token.
	preemptTicker := time.NewTicker(s.preemptInterval())
	defer preemptTicker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-preemptTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("kernel scheduler: panic in preemption sweep, continuing: %v", r)
						}
					}()
					s.PreemptLowerPriority(s.fabric.ResumableTasks())
				}()
			}
		}
	}()

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

// preemptInterval returns the preemption sweep period, guarding against a
// zero/negative PollInterval (time.NewTicker panics on a non-positive tick).
func (s *Scheduler) preemptInterval() time.Duration {
	if s.PollInterval > 0 {
		return s.PollInterval
	}
	return 500 * time.Millisecond
}

// safeDrain recovers a panic from one drain so the scheduling loop survives a
// single bad drain (M2: kernel loops must not crash the process). Per-task
// panics are already recovered inside drain; this guards the drain itself
// (e.g. a panic inside ReadyTasks).
func (s *Scheduler) safeDrain(ctx context.Context) {
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
func (s *Scheduler) WithEventStore(store ares_events.EventStore) *Scheduler {
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
func (s *Scheduler) drain(ctx context.Context) {
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
	s.PreemptLowerPriority(tasks)
	limit := s.maxConcurrent
	if limit <= 0 {
		limit = s.ExecutorCount()
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
func (s *Scheduler) PreemptLowerPriority(ready []string) {
	if s.ExecutorCount() == 0 || len(ready) == 0 {
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
func (s *Scheduler) logFailure(taskID string, err error) {
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
func (s *Scheduler) execute(ctx context.Context, taskID string) error {
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
// overlaps the task, plus every live IDLE fabric agent (B1).
func (s *Scheduler) executeUnbound(ctx context.Context, taskID string) error {
	execs := s.allExecutors()
	cands := make([]taskfabric.Candidate, 0, len(execs))
	for agentID, agent := range execs {
		if agent == nil {
			continue
		}
		if s.isBoundToAnyTask(agentID) {
			continue
		}
		// C1: when the fabric is wired (peer mode), the fabric's live
		// population is the SINGLE candidate source — the static registrations
		// of the configured sub-agents have a managed copy in the fabric, so a
		// chaos kill takes effect on the next drain. Only recovery-bound
		// executors (reserved for their task) stay in the static pool.
		if s.agents != nil && !s.isBoundToAnyTask(agentID) {
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
	// B1: live fabric agents are candidates too. Every drain re-queries the
	// fabric, so a freshly spawned IDLE agent becomes schedulable immediately
	// and a killed one disappears (spawn/kill 即时反映到候选集).
	cands = s.appendFabricCandidates(cands, execs)
	return s.executeWithCandidates(ctx, taskID, cands)
}

// executeWithCandidates runs the shared Schedule → Acquire → RunQuantum →
// finalize path for a prebuilt candidate list. The task capability is read
// for W4 attribution at the outcome boundary.
func (s *Scheduler) executeWithCandidates(ctx context.Context, taskID string, cands []taskfabric.Candidate) error {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		return taskfabric.ErrNoCapableCandidate
	}
	// W4: capability-specific confidence. The candidate builders only know
	// agentID; the task capability is available here, so re-resolve each
	// candidate's confidence against (agentID, task capability) before
	// Schedule scores them. Without a capability override this falls back to
	// the agent-level value (design-fix: per-capability feedback is consumed).
	for i := range cands {
		cands[i].Confidence = s.tracker.ConfidenceFor(cands[i].AgentID, tk.Capability)
	}
	winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)
	if err != nil {
		return err
	}
	// C1: when the fabric is wired, resolve the winner through the fabric
	// FIRST — the fabric copy is the live, lifecycle-managed agent (kill/
	// recovery affect it), so a same-id static registration must not shadow
	// it. Only when the fabric has no live agent for the winner (legacy
	// mode, or a recovery-bound static executor) fall back to the registry.
	var executor CapabilityExecutor
	var ok bool
	if s.agents != nil {
		executor = s.fabricExecutor(winner)
	}
	if executor == nil {
		executor, ok = s.LookupExecutor(winner)
		if !ok || executor == nil {
			// The candidate snapshot is stale: the winner died (or became
			// non-executable) between candidate build and executor lookup.
			// When another capable executor exists, release the task so the
			// next drain re-schedules it within one poll interval instead of
			// stalling for the full lease TTL (EDGE-4: 5-minute stall).
			// Only when NO capable executor is left do we keep the lease:
			// Release would leave the task READY-without-candidates, a state
			// the recovery loop's lease path never visits — the dead agent's
			// task would strand until another agent appears. Keeping the
			// lease lets it expire (TTL) so the event-driven recovery
			// requeues it and the W1 replacement executor resumes the
			// preserved checkpoint (aresos-agentos-plan E1: death → lease
			// expiry → replacement resumes). A task still held by other live
			// agents is unaffected.
			if s.HasCapableExecutor(taskID) {
				if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
					log.Printf("kernel scheduler: release %q for stale winner %q failed: %v", taskID, winner, releaseErr)
				}
				return nil
			}
			log.Printf("kernel scheduler: winner %q for task %q is no longer executable and no capable replacement exists; task stays leased for recovery", winner, taskID)
			return nil
		}
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
	s.tracker.Begin(winner)
	err = s.fabric.RunQuantum(taskID, winner, epoch, func() (any, bool, error) {
		out, stepErr := executor.ExecuteStep(ctx, s.ToModelTask(tk))
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
	// Release the busy slot and attribute the outcome (see endQuantumOutcome).
	s.endQuantumOutcome(winner, tk.Capability, taskID, err)
	// P3 post-quantum bookkeeping: record the quantum's consumption (1 tool
	// round) so the next gate sees the new balance. Runs even on step errors —
	// the quantum did execute (or partially execute) and spent budget.
	if s.governance != nil {
		s.consumeBudget(winner)
	}
	if err == nil {
		s.Scheduled.Add(1)
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

// endQuantumOutcome releases the winner's busy slot and attributes the
// quantum outcome to W4 feedback.
//
// A benign fencing rejection (cooperative preemption handed the task back
// while the stale holder was still mid-step) is NOT the executor's failure:
// recording it as one would poison the agent's success rate toward 0, and
// Score's confidence factor would make the preempted task permanently
// unschedulable. Such rejections end NEUTRAL — load is released but no
// success/failure enters the history, and W4 attribution is skipped.
func (s *Scheduler) endQuantumOutcome(winner, capability, taskID string, err error) {
	if errors.Is(err, taskfabric.ErrNotOwner) || errors.Is(err, taskfabric.ErrEpochMismatch) {
		s.tracker.EndNeutral(winner)
		log.Printf("kernel scheduler: quantum for task %q ended by preemption fencing (benign); outcome not attributed", taskID)
		return
	}
	s.tracker.End(winner, err == nil)
	// W4 evolution feedback: record the outcome for the feedback loop. The
	// attribution is read by the EvolutionFeedbackAdapter and pushed back into
	// the tracker's confidence override (SetAgentConfidence) so the next
	// Schedule sees the evolution-derived confidence.
	if s.attribution != nil {
		s.attribution.Record(winner, capability, err == nil)
	}
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
func (s *Scheduler) ToModelTask(tk *taskfabric.Task) *models.Task {
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
