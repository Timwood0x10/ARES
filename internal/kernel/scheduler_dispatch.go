package kernel

import (
	"context"
	"sort"
	"sync"
	"time"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// preemptInterval returns the preemption sweep period, guarding against a
// zero/negative PollInterval (time.NewTicker panics on a non-positive tick).
func (s *Scheduler) preemptInterval() time.Duration {
	if s.PollInterval > 0 {
		return s.PollInterval
	}
	return 500 * time.Millisecond
}

// safeDrain recovers a panic from one drain so the scheduling loop survives a
// single bad drain (kernel loops must not crash the process). Per-task
// panics are already recovered inside drain; this guards the drain itself
// (e.g. a panic inside ReadyTasks).
func (s *Scheduler) safeDrain(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("kernel scheduler: panic in drain, continuing", "panic", r)
		}
	}()
	s.drain(ctx)
}

// drain executes every currently ready task. When the scheduler is configured
// for concurrency (WithMaxConcurrent), ready tasks run in parallel (bounded by
// maxConcurrent) so multiple agents pick up work at the same time — the
// work-stealing substrate at the scheduler side. Panics from one task's
// execution are recovered so a single bad step cannot kill the loop.
// TODO(tech-debt): the per-agent local ready-queue design
// (taskfabric.AgentQueue/Steal) was removed as unused; the shared ReadyTasks()
// queue drained concurrently by bounded goroutines IS the stealing substrate.
// Re-introduce per-agent queues only if profiling shows contention.
func (s *Scheduler) drain(ctx context.Context) {
	// Zombie-executor reconciliation: a fabric agent killed via chaos/
	// governance disappears from the fabric, but its STATIC registration (the
	// configured peers' executors and every spawned agent's executor) stayed
	// in the map forever — the stale-winner lookup could then execute a task
	// on a dead agent's registration, and the registry grew unboundedly with
	// each spawn. Every drain drops registrations whose fabric entry is gone,
	// EXCEPT recovery-bound replacements (they are deliberately outside the
	// fabric; they are unregistered at terminal state).
	s.reconcileFabricDeaths()

	// Work source: READY tasks (new work) plus SUSPENDED tasks (a yielded
	// quantum the scheduler continues via re-acquire — the SUSPENDED
	// semantics lock: "Continue is the Scheduler's decision via re-acquire").
	tasks := s.fabric.ResumableTasks()
	if len(tasks) == 0 {
		return
	}
	// Priority preemption (fabric.Preempt was production-
	// unused): if a READY task outranks a task that is RUNNING from a
	// previous drain, cooperatively preempt the lower one so a capable
	// executor can pick up the higher-priority work. Preempt hands the task
	// back to READY with its checkpoint preserved (it resumes later), and the
	// fencing token guarantees only the current holder is affected. This
	// drain-site call runs BEFORE this drain spawns its own goroutines —
	// between quanta. (The background sweeper in Run may additionally preempt
	// mid-quantum; that is safe by fencing — see PreemptLowerPriority.)
	s.PreemptLowerPriority(tasks)
	sem := make(chan struct{}, s.drainLimit())
	var wg sync.WaitGroup
drainLoop:
	for _, taskID := range tasks {
		select {
		case <-ctx.Done():
			// Stop spawning new quanta, but never abandon the ones already in
			// flight: Run() clears s.running as soon as drain returns, so a bare
			// return here would let shutdown complete while goroutines still hold
			// leases and mutate fabric state. The wg.Wait() below is what makes
			// shutdown honest. (break alone would only exit the select.)
			break drainLoop
		default:
		}
		// The semaphore send must also select on ctx cancellation: with every
		// slot held by a stuck quantum, a bare send parks this loop forever
		// even though shutdown was requested — wg.Wait would then block
		// shutdown on executors that never return.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break drainLoop
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()
			defer func() {
				if recover() != nil {
					log.Error("kernel scheduler: panic executing task, continuing", "task_id", id)
				}
			}()
			if err := s.execute(ctx, id); err != nil {
				s.logFailure(id, err)
			}
		}(taskID)
	}
	wg.Wait()
}

// drainLimit computes how many ready tasks one drain may run in parallel.
// Fallback chain: an explicit WithMaxConcurrent value wins; otherwise the
// static executor registry size; when that is empty AND an agent fabric is
// wired, the count of live IDLE executable fabric agents. That third step is
// the production (peer-mode) default: the static registry is empty BY DESIGN
// there — the fabric's live population is the single candidate source — so
// without it the chain collapsed to 1 and every drain ran ONE quantum at a
// time while capable peers sat idle. The floor of 1 keeps the drain alive
// before any agent exists; 32 caps goroutine fan-out per drain.
func (s *Scheduler) drainLimit() int {
	limit := s.maxConcurrent
	if limit <= 0 {
		// Auto mode: the STATIC registry and the fabric population are two
		// different candidate sources, not a fallback chain. A recovery-bound
		// replacement (RegisterExecutorForTask) temporarily enters the static
		// registry while fabric agents remain idle — taking the registry count
		// alone would collapse parallelism to 1 for the binding's lifetime,
		// so auto mode is the MAX of both.
		limit = max(s.ExecutorCount(), s.fabricCandidateCount())
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > 32 {
		limit = 32 // sanity cap: a drain never spawns unbounded goroutines
	}
	return limit
}

// fabricCandidateCount returns how many live fabric agents are IDLE and
// executable — the same schedulability predicate PreemptLowerPriority uses
// to decide whether any candidate exists. O(n) scan of the live population
// per drain; n is small. Returns 0 when no agent fabric is wired (static
// registry only, e.g. tests and the SDK path).
func (s *Scheduler) fabricCandidateCount() int {
	if s.agents == nil {
		return 0
	}
	count := 0
	for _, id := range s.agents.Agents() {
		if !s.agents.IsIdle(id) {
			continue
		}
		if a, err := s.agents.Get(id); err == nil && a != nil && a.Executable() {
			count++
		}
	}
	return count
}

// preemptLowerPriority cooperatively preempts low-priority RUNNING tasks —
// but only as MANY as the higher-priority READY work actually needs, and only
// when no free capable agent can absorb that work without evicting anyone.
// The preempted tasks keep their checkpoints and return to READY for a later
// quantum. No-op when no priority information exists (all zeros) — the
// scheduler never churns a running task on a tie or on unset priorities.
//
// Need computation: for every READY task that carries a priority, the free
// pool is the set of agents that (a) could run at least one of those tasks
// (capability overlap) and (b) are not currently holding a RUNNING task. When
// the prioritized ready work exceeds that pool, the deficit is the number of
// preemptions; the LOWEST-priority running tasks are preempted first so the
// churn is minimal. Preempting a task whose owner cannot run the ready work is
// still useful: the next sweep re-evaluates against the reduced running set
// and preempts further only while the deficit persists.
//
// Boundary semantics: the drain() call site runs between quanta (before its
// own goroutines spawn); the background sweeper in Run() deliberately calls
// this MID-quantum (that is its reason to exist — drain blocks on wg.Wait, so
// drain-entry checks alone could never observe a RUNNING task). Mid-quantum
// preemption stays cooperative: only durable state moves (task → READY,
// checkpoint preserved) and the stale holder's late completion is rejected by
// the fencing token, so no quantum is ever double-applied — its in-flight
// work is simply discarded and resumed later.
func (s *Scheduler) PreemptLowerPriority(ready []string) {
	// The guard must also check fabric agents, not just static executors.
	// In production mode (agent fabric wired), the static executor count may
	// be 0 while fabric agents are the real candidate source.
	hasCandidates := s.ExecutorCount() > 0 || s.fabricCandidateCount() > 0
	if !hasCandidates || len(ready) == 0 {
		return
	}
	maxReady := 0
	// prioritized carries the capability of every ready task that actually
	// competes for preemption (priority > 0).
	var prioritizedCaps []string
	prioritized := 0
	for _, id := range ready {
		tk, err := s.fabric.Task(id)
		if err != nil || tk.Priority <= 0 {
			continue
		}
		prioritized++
		prioritizedCaps = append(prioritizedCaps, tk.Capability)
		if tk.Priority > maxReady {
			maxReady = tk.Priority
		}
	}
	if maxReady <= 0 {
		return
	}
	running := s.fabric.RunningTasks()
	if len(running) == 0 {
		return
	}
	// Free capable pool: distinct agents that could run at least one
	// prioritized ready task and are not currently holding a RUNNING task.
	busy := make(map[string]struct{}, len(running))
	for _, rt := range running {
		busy[rt.Owner] = struct{}{}
	}
	freeCapable := s.freeCapableAgents(prioritizedCaps, busy)
	need := prioritized - freeCapable
	if need <= 0 {
		// Every prioritized ready task has a free capable agent to land on:
		// preempting anyone here would throw away in-flight quantum work for
		// no benefit.
		return
	}
	// Preempt at most `need` tasks, lowest priority first (ties broken by
	// task id for determinism). A running task whose priority is at or above
	// maxReady is never a candidate — only strictly outranked work moves.
	sort.Slice(running, func(i, j int) bool {
		if running[i].Priority != running[j].Priority {
			return running[i].Priority < running[j].Priority
		}
		return running[i].ID < running[j].ID
	})
	preempted := 0
	for _, rt := range running {
		if preempted >= need {
			break
		}
		if rt.Priority >= maxReady {
			continue
		}
		if err := s.fabric.Preempt(rt.ID, rt.Owner, rt.Epoch, "higher-priority task arrived"); err != nil {
			// A concurrently-finalized task (already COMPLETED/FAILED) or a
			// stale epoch is a benign race, not worth log spam.
			continue
		}
		preempted++
		log.Info("kernel scheduler: preempted for higher-priority work", "task_id", rt.ID, "priority", rt.Priority, "max_ready", maxReady)
	}
}

// freeCapableAgents counts the distinct agents that (a) could run at least
// one of the required capabilities (CapabilityOverlap: an empty required
// capability is unconstrained and matches everything) and (b) are not
// currently holding a RUNNING task. Sources mirror buildCandidates: the
// static registry (minus recovery-bound reservations) plus live IDLE fabric
// agents, deduplicated by agent id.
func (s *Scheduler) freeCapableAgents(required []string, busy map[string]struct{}) int {
	freeCapable := 0
	seen := make(map[string]struct{})
	for agentID, agent := range s.allExecutors() {
		if agent == nil || s.isBoundToAnyTask(agentID) {
			continue
		}
		if _, isBusy := busy[agentID]; isBusy {
			continue
		}
		if !capableForAny(required, []string{string(agent.Type())}) {
			continue
		}
		seen[agentID] = struct{}{}
		freeCapable++
	}
	if s.agents == nil {
		return freeCapable
	}
	for _, id := range s.agents.Agents() {
		if _, dup := seen[id]; dup {
			continue
		}
		if !s.agents.IsIdle(id) {
			continue
		}
		a, err := s.agents.Get(id)
		if err != nil || a == nil || !a.Executable() {
			continue
		}
		if _, isBusy := busy[id]; isBusy {
			continue
		}
		if !capableForAny(required, a.Capabilities) {
			continue
		}
		seen[id] = struct{}{}
		freeCapable++
	}
	return freeCapable
}

// capableForAny reports whether an agent's declared capabilities overlap any
// of the required capabilities (taskfabric.CapabilityOverlap: empty required
// means unconstrained and matches everything).
func capableForAny(required []string, have []string) bool {
	for _, r := range required {
		if taskfabric.CapabilityOverlap(r, have) > 0 {
			return true
		}
	}
	return false
}
