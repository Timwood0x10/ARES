package kernel

import (
	"context"
	"sort"
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
			log.Error(ctx, "safeDrain", "kernel scheduler: panic in drain, continuing", nil, "panic", r)
		}
	}()
	s.drain(ctx)
}

// drain pumps every currently ready task into execution without waiting
// for the batch to finish (M2 work-conserving). Each quantum runs in its
// own goroutine registered on the inflight tracker; drain returns as soon as
// it has launched all candidates, so the scheduling loop can pump again on
// the next tick or event while the previous batch is still running — an
// idle agent can pick up new READY work within one poll interval instead of
// waiting for the slowest quantum in the previous batch.
//
// The inflight tracker (s.inflight) replaces the per-drain WaitGroup. Run()
// calls inflight.Wait() on shutdown so the honesty contract is preserved:
// the scheduler does not report stopped while goroutines still hold leases.
//
// Concurrency safety: maxConcurrentPerAgent=1 is enforced by the lease/
// tracker.TryBegin admission gate in executeWithCandidates, NOT by drain
// waiting for a batch to finish. A second pump will find the agent's slot
// occupied and skip it, so the pump cannot double-dispatch to one agent.
// TODO(tech-debt): the per-agent local ready-queue design
// (taskfabric.AgentQueue/StealCircuit) was removed as unused; the shared ReadyTasks()
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
	s.reconcileFabricDeaths(ctx)

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
	s.PreemptLowerPriority(ctx, tasks)
	limit := s.drainLimit()
drainLoop:
	for i, taskID := range tasks {
		select {
		case <-ctx.Done():
			// Stop spawning new quanta. The inflight tracker ensures Run()
			// does not report stopped while already-launched goroutines
			// still hold leases — the honesty contract that wg.Wait
			// previously enforced at the batch boundary is now enforced
			// at the shutdown boundary.
			break drainLoop
		default:
		}
		// Non-blocking admission. Parking here until a slot frees would
		// reproduce the batch barrier this pump removed: the scheduling loop
		// would stop reading events for as long as the slowest quantum runs,
		// which is exactly the work-conservation failure M2 is about. The
		// tasks left over stay READY and are picked up by the next tick.
		if !s.admitQuantum(limit) {
			// The task at the head of the remaining queue is the one whose wait this tick is
			// actually extending, so its age is the honest sample. Recording every deferred task
			// would double-count the tail and hide how long the head has been waiting.
			s.observeQueueHead(tasks[i:])
			s.observeWorkConservingGap(len(tasks) - i)
			break drainLoop
		}
		// Register on the inflight tracker under the mu so a concurrent
		// Wait in Run()'s shutdown path cannot return before this Add
		// completes. Once Add returns, the goroutine's deferred Done is
		// guaranteed to release Wait.
		s.inflightMu.Lock()
		s.inflight.Add(1)
		s.inflightMu.Unlock()
		s.observeQueueAge(taskID)
		go func(id string) {
			defer s.releaseQuantum()
			defer s.inflight.Done()
			defer func() {
				if recover() != nil {
					log.Error(ctx, "drain", "kernel scheduler: panic executing task, continuing", nil, "task_id", id)
				}
			}()
			if err := s.execute(ctx, id); err != nil && shouldReportExecutionFailure(ctx) {
				s.logFailure(ctx, id, err)
			}
		}(taskID)
	}
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
	s.execMu.RLock()
	limit := s.maxConcurrent
	s.execMu.RUnlock()
	if limit <= 0 {
		// Auto mode: the STATIC registry and the fabric population are two
		// different candidate sources, not a fallback chain. A recovery-bound
		// replacement (RegisterExecutorForTask) temporarily enters the static
		// registry while fabric agents remain idle — taking the registry count
		// alone would collapse parallelism to 1 for the binding's lifetime,
		// so auto mode is the MAX of both.
		// Capacity, not idleness: this number is compared against inflightQuanta, which counts
		// running quanta. Using idle agents here made the limit shrink as work started, so a
		// partly-busy deployment throttled itself below its own size.
		limit = max(s.ExecutorCount(), s.fabricCapacity())
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
// fabricCapacity is how many agents this deployment can occupy at once: every schedulable agent,
// busy or not. Paused and retired agents are excluded — they are not capacity.
//
// It is not the same number as fabricCandidateCount, which answers "who can take work right now".
// Confusing the two is what S01/S02 in the 0.3.6 plan are about: a limit computed from idle agents
// gets compared against all in-flight quanta, so two busy agents make a three-agent deployment
// refuse work while a third agent sits idle.
func (s *Scheduler) fabricCapacity() int {
	if s.agents == nil {
		return 0
	}
	count := 0
	for _, id := range s.agents.Agents() {
		if a, err := s.agents.Get(id); err == nil && a != nil && a.IsSchedulable() {
			count++
		}
	}
	return count
}

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
// Match prioritized demands to distinct agents, preferring free capacity over
// eviction. Keep the assignment through admission so independent scoring cannot
// spend a flexible agent on work assigned to a rigid one.
//
// Boundary semantics: the drain() call site runs between quanta (before its
// own goroutines spawn); the background sweeper in Run() deliberately calls
// this MID-quantum (that is its reason to exist — drain blocks on wg.Wait, so
// drain-entry checks alone could never observe a RUNNING task). Mid-quantum
// preemption stays cooperative: only durable state moves (task → READY,
// checkpoint preserved) and the stale holder's late completion is rejected by
// the fencing token, so no quantum is ever double-applied — its in-flight
// work is simply discarded and resumed later.
func (s *Scheduler) PreemptLowerPriority(ctx context.Context, ready []string) {
	s.placementMu.Lock()
	defer s.placementMu.Unlock()
	s.pruneDispatchAssignments(ctx)
	// The guard must ask "does this deployment HAVE executors", not "is one free right now":
	// all-busy is precisely when preemption is the only way a high-priority task can start.
	if s.ExecutorCount() == 0 && s.fabricCapacity() == 0 {
		return
	}
	demands := s.preemptionDemands(ready)
	if len(demands) == 0 {
		return
	}
	running := s.fabric.RunningTasks()
	busy := make(map[string]struct{}, len(running))
	for _, rt := range running {
		busy[rt.Owner] = struct{}{}
	}
	// One matching over free agents AND possible victims together. Splitting the phases (free agents
	// first, victims second, each first-fit) let a rigid demand take the flexible agent, leaving the
	// other demand unmatched and a preemption manufactured for work that could have been placed — the
	// same greedy mistake one level up.
	plan := s.planPreemptions(ctx, demands, running, busy)
	s.dispatchAssignments = plan.assignments
	for _, rt := range plan.victims {
		if err := s.fabric.Preempt(rt.ID, rt.Owner, rt.Epoch, "higher-priority task arrived"); err != nil {
			// A concurrently-finalized task or a stale epoch is a benign race.
			for taskID, agentID := range s.dispatchAssignments {
				if agentID == rt.Owner {
					delete(s.dispatchAssignments, taskID)
				}
			}
			continue
		}
		log.Info(ctx, "PreemptLowerPriority",
			"kernel scheduler: preempted for higher-priority work",
			"task_id", rt.ID, "priority", rt.Priority)
	}
}

// preemptionDemand is one prioritized READY task that has no executor yet.
type preemptionDemand struct {
	id         string
	capability string
	priority   int
}

// preemptionDemands collects the ready tasks that compete for preemption, highest priority first.
func (s *Scheduler) preemptionDemands(ready []string) []preemptionDemand {
	demands := make([]preemptionDemand, 0, len(ready))
	resumable := make(map[string]bool)
	for _, id := range s.fabric.ResumableTasks() {
		resumable[id] = true
	}
	for _, id := range ready {
		if !resumable[id] {
			continue
		}
		tk, err := s.fabric.Task(id)
		if err != nil || tk.Priority <= 0 {
			continue
		}
		if s.hasRecoveryCandidate(tk) {
			continue
		}
		demands = append(demands, preemptionDemand{id: id, capability: tk.Capability, priority: tk.Priority})
		delete(resumable, id)
	}
	sort.Slice(demands, func(i, j int) bool {
		if demands[i].priority != demands[j].priority {
			return demands[i].priority > demands[j].priority
		}
		return demands[i].id < demands[j].id
	})
	return demands
}

// preemptionSlot is one agent a demand could be served by: a free one, or one currently running a
// task that would have to be evicted.
type preemptionSlot struct {
	agentID    string
	victim     taskfabric.RunningTask
	free       bool
	waitingFor string // a fenced quantum is still exiting for this assigned task
}

type preemptionPlan struct {
	victims     []taskfabric.RunningTask
	assignments map[string]string
}

// planPreemptions matches in priority order, retaining earlier demands and minimizing the number
// of occupied victim slots. Assignments must survive planning: they are the reason each eviction helps.
func (s *Scheduler) planPreemptions(
	ctx context.Context,
	demands []preemptionDemand,
	running []taskfabric.RunningTask,
	busy map[string]struct{},
) preemptionPlan {
	slots := make([]preemptionSlot, 0, len(demands)+len(running))
	seen := make(map[string]bool)
	for _, id := range s.freeAgentIDs(busy) {
		slots = append(slots, preemptionSlot{agentID: id, free: true})
		seen[id] = true
	}
	// Preempt changes durable state before the old quantum releases its actual busy slot. Keep
	// that already-freed capacity in the plan across sweeps instead of evicting a second owner.
	for taskID, id := range s.dispatchAssignments {
		_, running := busy[id]
		if !seen[id] && !running {
			slots = append(slots, preemptionSlot{agentID: id, free: true, waitingFor: taskID})
			seen[id] = true
		}
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].agentID < slots[j].agentID })
	sort.Slice(running, func(i, j int) bool {
		if running[i].Priority != running[j].Priority {
			return running[i].Priority < running[j].Priority
		}
		return running[i].ID < running[j].ID
	})
	for _, rt := range running {
		if seen[rt.Owner] || !s.preemptionAgentAvailable(rt.Owner) {
			// A bound executor can only serve the task it is bound to: freeing it buys nothing and
			// interrupts recovery work that will simply be resumed.
			continue
		}
		slots = append(slots, preemptionSlot{agentID: rt.Owner, victim: rt})
		seen[rt.Owner] = true
	}

	caps := make(map[string][]string, len(slots))
	for _, sl := range slots {
		if ok, _ := s.admissible(ctx, sl.agentID); ok {
			caps[sl.agentID] = s.agentCapabilities(sl.agentID)
		}
	}
	canServe := func(d preemptionDemand, sl preemptionSlot) bool {
		// Pending release is promised capacity, not idle capacity another demand can consume.
		// In particular, the just-preempted task must not take it back and push the urgent task
		// into a second eviction before the first quantum has even exited.
		if sl.waitingFor != "" && sl.waitingFor != d.id {
			return false
		}
		if _, available := caps[sl.agentID]; !available {
			return false
		}
		if !sl.free && d.priority <= sl.victim.Priority {
			// Only strictly outranked work moves.
			return false
		}
		return capableForAny([]string{d.capability}, caps[sl.agentID])
	}

	assigned := make(map[int]int, len(slots)) // slot index → demand index
	for d := range demands {
		// Find ALL alternating paths before choosing an endpoint. Taking the first DFS path can
		// evict a CPU owner while a GPU-only free slot remains reachable. Internal slots stay
		// occupied when a path flips; only its unmatched endpoint adds a free slot or a victim.
		from := make(map[int]int, len(slots)) // slot -> predecessor demand
		previous := map[int]int{d: -1}        // demand -> predecessor occupied slot
		queue := []int{d}
		for head := 0; head < len(queue); head++ {
			demand := queue[head]
			for i, sl := range slots {
				if _, visited := from[i]; visited || !canServe(demands[demand], sl) {
					continue
				}
				from[i] = demand
				if prev, taken := assigned[i]; taken {
					if _, visited := previous[prev]; !visited {
						previous[prev] = i
						queue = append(queue, prev)
					}
				}
			}
		}
		// Slots are ordered free first, then cheapest victim. Rerouting across either kind is
		// allowed, but an unused victim is selected only if no free endpoint is reachable.
		for i := range slots {
			_, reachable := from[i]
			_, taken := assigned[i]
			if !reachable || taken {
				continue
			}
			for i >= 0 {
				demand := from[i]
				assigned[i] = demand
				i = previous[demand]
			}
			break
		}
	}

	// Collected by slot order, not by iterating the map: map iteration is randomised, and a scheduling
	// decision whose order changes between runs cannot be reasoned about from a log.
	plan := preemptionPlan{
		victims:     make([]taskfabric.RunningTask, 0, len(assigned)),
		assignments: make(map[string]string, len(assigned)),
	}
	for i, sl := range slots {
		demand, matched := assigned[i]
		if !matched {
			continue
		}
		plan.assignments[demands[demand].id] = sl.agentID
		if !sl.free {
			plan.victims = append(plan.victims, sl.victim)
		}
	}
	return plan
}

// freeAgentIDs lists agents that could take work now: not running a task, not bound to one.
func (s *Scheduler) freeAgentIDs(busy map[string]struct{}) []string {
	free := make([]string, 0, len(s.allExecutors()))
	seen := make(map[string]struct{}, len(s.allExecutors()))

	// Peer mode: the fabric population is the single source, and a purely static registration has no
	// live agent behind it — counting it as spare capacity cancels a preemption that was needed.
	staticCounts := s.agents == nil || s.hybridStatic
	if staticCounts {
		for agentID, exec := range s.allExecutors() {
			if exec == nil || !s.preemptionAgentAvailable(agentID) {
				continue
			}
			if s.tracker.Load(agentID) >= 1 {
				continue
			}
			if _, isBusy := busy[agentID]; isBusy {
				continue
			}
			seen[agentID] = struct{}{}
			free = append(free, agentID)
		}
	}

	if s.agents == nil {
		sort.Strings(free)
		return free
	}
	for _, id := range s.agents.Agents() {
		if _, dup := seen[id]; dup {
			continue
		}
		// Idle and executable, the same bar candidate selection applies: a busy agent or one with no
		// cognition injected cannot take the urgent task, so it is not spare capacity.
		if !s.agents.IsIdle(id) {
			continue
		}
		if s.tracker.Load(id) >= 1 {
			continue
		}
		agent, err := s.agents.Get(id)
		if err != nil || agent == nil || !agent.Executable() {
			continue
		}
		if s.isBoundToAnyTask(id) {
			continue
		}
		if _, isBusy := busy[id]; isBusy {
			continue
		}
		seen[id] = struct{}{}
		free = append(free, id)
	}
	sort.Strings(free)
	return free
}

// freeCapableAgents counts the distinct agents that (a) could run at least
// one of the required capabilities (CapabilityOverlap: an empty required
// capability is unconstrained and matches everything) and (b) are not
// currently holding a RUNNING task. Sources mirror buildCandidates: the
// static registry (minus recovery-bound reservations) plus live IDLE fabric
// agents, deduplicated by agent id.

// agentCapabilities returns the capabilities an agent can serve, from the SAME sources candidate
// selection uses — the live Fabric population first, the static pool second — but without the
// availability filters.
//
// Preemption has to judge a busy agent, and a busy agent is precisely what candidate selection drops
// from its list. Using that list here (the first attempt) made every victim look incapable and turned
// preemption off; using only the static pool (the second) missed Fabric-only agents and their
// multi-capability declarations, which is how a batch task got evicted for database work.
func (s *Scheduler) agentCapabilities(agentID string) []string {
	if agentID == "" {
		return nil
	}
	if s.agents != nil {
		if caps, err := s.agents.CapabilitiesOf(agentID); err == nil && len(caps) > 0 {
			return caps
		}
	}
	if exec, ok := s.allExecutors()[agentID]; ok && exec != nil {
		return []string{string(exec.Type())}
	}
	return nil
}

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
		// Executable takes the agent's own lock, so reading it off the
		// pointer Get returns is safe. Capabilities is NOT: it is a plain
		// slice field the SDK's hot tool registration appends to under the
		// fabric lock, so its capabilities come from the locked-copy
		// accessor instead.
		a, err := s.agents.Get(id)
		if err != nil || a == nil || !a.Executable() {
			continue
		}
		caps, err := s.agents.CapabilitiesOf(id)
		if err != nil {
			continue
		}
		if _, isBusy := busy[id]; isBusy {
			continue
		}
		if !capableForAny(required, caps) {
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

// admitQuantum reserves one of the limit process-wide quantum slots.
//
// Returns false when the budget is full; the caller must then leave the remaining tasks READY
// rather than wait, so the scheduling loop keeps serving events and the next tick can admit as
// soon as a slot frees.
func (s *Scheduler) admitQuantum(limit int) bool {
	s.quantumMu.Lock()
	defer s.quantumMu.Unlock()
	if s.inflightQuanta >= limit {
		return false
	}
	s.inflightQuanta++
	if s.inflightQuanta == 1 {
		s.busySince = time.Now()
	}
	return true
}

// releaseQuantum returns a slot reserved by admitQuantum.
func (s *Scheduler) releaseQuantum() {
	s.quantumMu.Lock()
	defer s.quantumMu.Unlock()
	if s.inflightQuanta > 0 {
		s.inflightQuanta--
	}
	if s.inflightQuanta == 0 {
		s.busySince = time.Time{}
	}
}

// observeQueueAge records how long a task had been READY when it left the queue.
//
// This is the queue-age signal: it answers "how long did work sit", independently of whether the
// claim later succeeded. A task that leaves the queue and then fails to find an agent still spent
// that time waiting, and hiding it would make the queue look healthier than it is.
func (s *Scheduler) observeQueueAge(taskID string) {
	s.recordAge(taskID)
}

// observeQueueHead records the age of the task that will be dispatched next, for the case where
// this tick could admit nothing.
//
// Without it, only work that eventually ran contributed to queue age, so a candidate that waits for
// minutes and is then executed reports nothing about the minutes: the histogram would describe the
// winners and stay silent about the backlog. Only the head is sampled — every deferred task would
// multiply the same wait across the histogram and make the tail look like the norm.
func (s *Scheduler) observeQueueHead(remaining []string) {
	if len(remaining) == 0 {
		return
	}
	head := remaining[0]
	// Rate-limited per task: the head is re-observed every drain, and a stalled head would
	// otherwise contribute one sample per poll forever.
	s.headSampleMu.Lock()
	if s.headSampledID == head && time.Since(s.headSampledAt) < headSampleInterval {
		s.headSampleMu.Unlock()
		return
	}
	s.headSampledID = head
	s.headSampledAt = time.Now()
	s.headSampleMu.Unlock()

	s.recordAge(head)
}

// headSampleInterval bounds how often one stalled head contributes to the age histogram.
//
// Five seconds is a compromise: long enough that a 500ms poll does not turn a single wait into
// hundreds of samples, short enough that a queue stuck for minutes keeps showing that it is stuck.
const headSampleInterval = 5 * time.Second

// recordAge is the shared sampling for the two queue-age call sites.
func (s *Scheduler) recordAge(taskID string) {
	if age, ok := s.taskAge(taskID); ok {
		s.metricsOf().RecordReadyAge(age)
	}
}

// taskAge returns how long the task has been READY, if that is knowable.
//
// A zero ReadyAt means the task predates the field (restored from an older store): its age is
// unknown, and reporting 0 would claim it had just arrived.
func (s *Scheduler) taskAge(taskID string) (float64, bool) {
	tk, err := s.fabric.Task(taskID)
	if err != nil || tk.ReadyAt.IsZero() {
		return 0, false
	}
	age := time.Since(tk.ReadyAt).Seconds()
	if age < 0 {
		age = 0
	}
	return age, true
}

// observeDispatchWait records how long a task waited for a real claim, sampled AFTER the scheduler
// has acquired a lease and chosen an agent.
//
// It used to be sampled at admission, before the claim, which reported a start for work that then
// failed to find an agent — the metric counted attempts to run, not runs. It is now recorded only
// once a claim exists, so "dispatched" means dispatched.
func (s *Scheduler) observeDispatchWait(taskID string) {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return
	}
	// THIS attempt's waiting time, not the task's age: a resumed task carries the previous
	// quantum's execution time in ReadyAt, and charging it again made a 0.4ms resume report 150ms.
	since := tk.AttemptReadyAt
	if since.IsZero() {
		since = tk.ReadyAt // tasks created before the field existed, or restored from an old store
	}
	if since.IsZero() {
		return
	}
	wait := time.Since(since).Seconds()
	if wait < 0 {
		wait = 0
	}
	s.metricsOf().RecordReadyToDispatch(wait)
}

// observeWorkConservingGap records that deferred work was blocked behind a running quantum.
//
// remaining is the number of tasks this pump had to leave READY. It is reported only when such
// work exists: a zero would claim "no work waited" and hide the case the metric exists for.
func (s *Scheduler) observeWorkConservingGap(remaining int) {
	if remaining <= 0 {
		return
	}
	s.quantumMu.Lock()
	since := s.busySince
	s.quantumMu.Unlock()
	if since.IsZero() {
		return
	}
	gap := time.Since(since).Seconds()
	if gap < 0 {
		gap = 0
	}
	s.metricsOf().RecordWorkConservingGap(gap)
}

// shouldReportExecutionFailure reports whether an execution error is a task failure rather than the
// scheduler aborting the quantum because it is shutting down.
//
// The distinction matters more since the pump stopped waiting for its batch: cancel() can catch
// many quanta in flight at once, and reporting each of them at ERROR turns every orderly shutdown
// into a burst of apparent failures on the operator's dashboard. A live context means the error
// came from the task's own run, which is a real failure and stays reported.
func shouldReportExecutionFailure(ctx context.Context) bool {
	return ctx.Err() == nil
}
