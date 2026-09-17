package kernel

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// Scheduler is the "no leader" execution engine (ares-runtime.md:
// "Agents are not orchestrated. They are scheduled."). It repeatedly drains
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
// Dynamic executor registration: RegisterExecutor / UnregisterExecutor
// let the recovery loop inject a replacement agent at runtime so a recovered
// task is executed by a real executor, not a phantom agent. execMu guards the
// executor map for concurrent register/unregister/lookup from drain goroutines.
type Scheduler struct {
	fabric    *taskfabric.Fabric
	executors map[string]CapabilityExecutor
	// execMu guards the executors map for dynamic register/unregister.
	// A separate lock avoids reentrancy with the fabric mutex during drain.
	execMu sync.RWMutex
	// tracker supplies real per-agent Load/Confidence to Schedule
	// (real load tracking instead of a static placeholder).
	tracker *LoadTracker
	// PollInterval is how often ReadyTasks is drained (default 500ms).
	PollInterval time.Duration
	// ttl is the lease granted to each winning agent.
	ttl time.Duration
	// eventStore is the shared EventStore the Task Fabric publishes lifecycle
	// events to. When set, the scheduler subscribes to dependency-relevant
	// task events (completed/failed/ready/… ) and drains immediately on each,
	// so a task whose DAG dependencies have just completed runs without
	// waiting for the next poll tick (event-driven DAG completion).
	// Nil keeps pure 500ms polling (backward compatible).
	eventStore ares_events.EventStore
	// maxConcurrent caps how many ready tasks run in parallel during one
	// drain (work stealing: multiple agents pick up tasks concurrently).
	// <= 0 falls back to the auto chain in drainLimit (executor count, then
	// live fabric candidates; bounded by 32).
	maxConcurrent int
	// scheduled counts successfully executed tasks (for observability).
	// atomic: incremented from concurrent drain goroutines (work stealing).
	Scheduled atomic.Int64
	// noCandidateMu guards lastNoCandidateLog for throttling unschedulable-task
	// logs.
	noCandidateMu      sync.Mutex
	lastNoCandidateLog time.Time
	// governance is the cognitive-execution budget provider (agentfabric:
	// token/tool budgets + deadline). Nil skips enforcement (backward
	// compatible). When set, execute() checks the budget at each quantum
	// boundary (before CheckResource / after ConsumeResource+Deadline) so a
	// budget-exhausted agent yields the task back instead of burning tokens it
	// cannot afford (cooperative yield, not hard preempt).
	governance *agentfabric.Fabric
	// boundExecutors maps taskID → executorID for recovery executors. A
	// recovery executor is bound to exactly one task: execute() only offers it
	// as a candidate for that task, never for another READY task, so a
	// replacement spawned for a recovered task cannot hijack new tasks.
	// Guarded by execMu.
	boundExecutors map[string]string
	// hybridStatic keeps statically-registered executors schedulable even
	// when an agent fabric is attached (SDK hybrid mode). Peer mode (cmd/ares
	// serve) assumes every static registration mirrors a managed fabric agent,
	// so reconcileFabricDeaths sweeps registrations with no live fabric agent
	// and buildCandidates offers only the fabric population. The SDK breaks
	// that assumption: RegisterAgent / graph agents / auto-created executors
	// are static capability executors that deliberately live OUTSIDE the
	// fabric, while the L2 router peer lives INSIDE it. With this flag set,
	// static executors
	// stay candidates, fabric-death sweeping is skipped (the SDK has no chaos
	// kills; UnregisterExecutor is its explicit control surface), and a
	// static executor matching the task's capability wins over an overlapping
	// fabric candidate (registered agents keep their pre-L2 behavior).
	hybridStatic bool
	// attribution is the optional execution-outcome source. When wired,
	// execute() records every finalized outcome (agent, capability, success)
	// so the evolution feedback loop can read attribution and push derived
	// confidence into the tracker. Nil skips recording (backward compatible).
	attribution *aresrecovery.ExecutionAttribution
	// agents is the optional agentfabric.Fabric whose live IDLE agents are
	// schedulable candidates (the scheduler's candidate
	// pool comes from the agentfabric dynamic population). Every drain re-queries the fabric, so a spawned
	// agent becomes schedulable immediately and a killed one disappears — no
	// explicit registry sync. Nil keeps the static executor registry only
	// (backward compatible with tests and minimal wiring).
	agents *agentfabric.Fabric
	// decisions records every scheduling decision (candidate pool + scores +
	// winner) for the Scheduling Observatory. It is written
	// in executeWithCandidates and read via DecisionsSnapshot — the panel
	// explains WHY a task went to a particular agent.
	decisions *DecisionRecorder
	// quantumHook is the optional observational extension at the quantum
	// boundary (see quantum_hook.go). Nil = no hook (backward compatible).
	// Guarded by execMu alongside the executor registry so runtime
	// registration races with the drain loop stay safe.
	quantumHook QuantumHook
	// running reports whether the drain loop is actually running (the
	// System Runtime readiness gate must mean "drain loop alive", not
	// "object exists"). Set at Run entry, cleared on exit.
	running atomic.Bool
	// recoveryHint, when wired, is invoked at the stale-winner boundary: the
	// winner died between candidate build and executor lookup and no capable
	// replacement exists yet, so waiting for the lease TTL is the only other
	// way out. The hint asks the recovery loop to sweep NOW.
	//
	// It must be non-blocking — the caller is a drain goroutine on the hot
	// path. The wiring side (cmd/ares) satisfies this with a capacity-1
	// channel and a drop-on-full send, matching the sweep semaphore's own
	// drop semantics. Nil keeps the legacy behavior for the leader/SDK paths
	// that have no recovery loop.
	//
	// Deliberately a callback rather than a recovery dependency: the
	// architecture red line forbids kernelscheduler importing runtime
	// (TestSchedulerMustNotImportRuntime).
	recoveryHint func(taskID string)
}

// Running reports whether the scheduler's drain loop is currently running.
// Readiness semantics: a constructed-but-not-running scheduler must never
// report Ready — the System Runtime gate polls this before adoption.
func (s *Scheduler) Running() bool { return s.running.Load() }

// noCandidateLogInterval throttles "no capable candidate" logs to one per
// window — the condition is a waiting state, not an error worth per-poll noise.
const noCandidateLogInterval = 5 * time.Second

// maxConcurrentPerAgent caps how many quanta one agent may run at the same
// time. It is 1 by architectural definition: an agent is a PROCESS with one
// cognitive state, not a reentrant worker pool, and Score already treats
// load >= 1 as "unschedulable". The constant exists so the admission gate in
// executeWithCandidates and the scoring model state the same rule once instead
// of agreeing by accident.
const maxConcurrentPerAgent = 1

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
	// Copy the initial executor map so the scheduler owns its own
	// map. The caller's map and the scheduler's map are now independent —
	// the caller must use RegisterExecutor/UnregisterExecutor to mutate
	// the live registry. Without this copy, both sides hold the same map
	// reference guarded by DIFFERENT mutexes (caller's lock vs execMu),
	// and concurrent read/write is a fatal race.
	ownExecutors := make(map[string]CapabilityExecutor, len(executors))
	for k, v := range executors {
		ownExecutors[k] = v
	}
	return &Scheduler{
		fabric:         fabric,
		executors:      ownExecutors,
		boundExecutors: make(map[string]string),
		tracker:        tracker,
		PollInterval:   500 * time.Millisecond,
		ttl:            5 * time.Minute,
		decisions:      newDecisionRecorder(),
	}
}

// WithAttribution attaches the execution-outcome source (aresrecovery.
// ExecutionAttribution). When set, execute() records every finalized outcome
// after the quantum. Returns the scheduler for chaining.
func (s *Scheduler) WithAttribution(a *aresrecovery.ExecutionAttribution) *Scheduler {
	s.attribution = a
	return s
}

// WithAgentFabric attaches the agent lifecycle fabric so every live, IDLE,
// executable fabric agent is a schedulable candidate (single scheduling
// loop — the scheduler recognizes only the unified Agent). It is wired by the kernel lifecycle once the
// fabric exists; nil keeps the static executor registry only. Returns the
// scheduler for chaining.
func (s *Scheduler) WithAgentFabric(f *agentfabric.Fabric) *Scheduler {
	s.agents = f
	return s
}

// WithStaticPoolHybrid opts the scheduler into hybrid mode: static executor
// registrations coexist with the agent fabric's live population as schedulable
// candidates, and fabric-death reconciliation is disabled (the embedder
// unregisters explicitly). Used by the SDK, where static capability executors
// (RegisterAgent, graph agents, on-demand auto-creation) intentionally live
// outside the fabric while the L2 router peer lives inside it. A static
// executor whose capability matches the task wins over an overlapping fabric
// candidate, so registered-agent behavior is unchanged. Peer mode (the
// default when a fabric is attached) keeps the fabric as the single candidate
// source. Returns the scheduler for chaining.
func (s *Scheduler) WithStaticPoolHybrid() *Scheduler {
	s.hybridStatic = true
	return s
}

// WithMaxConcurrent caps how many ready tasks run in parallel per drain
// (work stealing). <= 0 keeps the auto fallback chain in drainLimit
// (executor count, then live fabric candidates). Returns the scheduler for
// chaining.
func (s *Scheduler) WithMaxConcurrent(n int) *Scheduler {
	s.maxConcurrent = n
	return s
}

// WithTTL overrides the lease duration granted on Acquire (default 5 minutes).
// The scheduler heartbeats Renew at ttl/3 (minimum 5s) while a quantum runs,
// so steps longer than ttl are not requeued by lease expiry. Returns the
// scheduler for chaining.
func (s *Scheduler) WithTTL(ttl time.Duration) *Scheduler {
	if ttl > 0 {
		s.ttl = ttl
	}
	return s
}

// Run drains ReadyTasks until ctx is cancelled or the fabric becomes nil.
// It runs synchronously; callers start it in a goroutine. Panics from one
// task's execution are recovered so a single bad step cannot kill the loop.
//
// When an event store is wired (WithEventStore), the scheduler also drains
// immediately on dependency-relevant task events (completed / failed /
// ready / created), so a task whose DAG dependencies just finished runs
// without waiting for the next poll tick. The periodic poll remains
// as a safety net for transitions that do not publish events.
//
// Args:
//   - ctx: lifetime of the scheduling loop.
func (s *Scheduler) Run(ctx context.Context) {
	if s.fabric == nil {
		log.Warn("kernel scheduler: fabric nil, scheduler disabled")
		return
	}
	// The readiness flag goes up before the loop blocks so an Adopt-time
	// Ready gate observes "drain loop alive" as soon as Run begins, and goes
	// down when the loop exits so a crashed scheduler never keeps reporting
	// Ready.
	s.running.Store(true)
	defer s.running.Store(false)
	// Guard against zero or negative PollInterval which would panic
	// in time.NewTicker. Fall back to preemptInterval which
	// applies the same safe default.
	pollInterval := s.PollInterval
	if pollInterval <= 0 {
		pollInterval = s.preemptInterval()
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Cooperative-preemption watcher (BUG-KSCHED-001): drain() blocks on
	// wg.Wait() until every dispatched quantum finishes, so preemption
	// checked only at drain entry could never observe a RUNNING task — the
	// branch was unreachable through the production loop. This managed worker
	// (deterministic exit on ctx.Done, per-sweep recover)
	// scans independently of the blocking drain. Preemption stays
	// cooperative: it only mutates durable state; the stale holder's late
	// completion is rejected by the fencing token.
	preemptTicker := time.NewTicker(s.preemptInterval())
	defer preemptTicker.Stop()
	// WaitGroup-managed (N-2): Run must not return while a preemption sweep
	// is still in flight — an unmanaged sweep racing shutdown could mutate
	// durable state after the caller believes the scheduler fully stopped.
	// The sweep is bounded (one ResumableTasks pass), so Wait cannot hang.
	var preemptWG sync.WaitGroup
	preemptWG.Add(1)
	go func() {
		defer preemptWG.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-preemptTicker.C:
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Error("kernel scheduler: panic in preemption sweep, continuing", "panic", r)
						}
					}()
					s.PreemptLowerPriority(s.fabric.ResumableTasks())
				}()
			}
		}
	}()
	defer preemptWG.Wait()

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
			log.Warn("kernel scheduler: event subscribe failed, polling only", "error", err)
		} else {
			events = ch
			log.Info("kernel scheduler: event-driven drain enabled (task lifecycle events)")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.safeDrain(ctx)
		case _, ok := <-events:
			// A dependency-relevant task event arrived: drain now instead of
			// waiting up to one poll interval. When the subscription channel
			// is closed, disable the case (nil channel blocks forever) so the
			// loop falls back to pure polling instead of busy-spinning on a
			// closed channel.
			if !ok {
				log.Info("kernel scheduler: event subscription closed, polling only")
				events = nil
				continue
			}
			s.safeDrain(ctx)
		}
	}
}

// WithEventStore wires the shared EventStore so the scheduler drains on task
// lifecycle events (event-driven DAG completion) instead of waiting
// for the next poll tick. Returns the scheduler for chaining.
func (s *Scheduler) WithEventStore(store ares_events.EventStore) *Scheduler {
	s.eventStore = store
	return s
}

// SchedulerSnapshot is a point-in-time, read-only view of the scheduler's
// observable state — the runtime introspection panel's Domain A read-model
// (queue depth, preemption cadence, executor inventory,
// budget/governance wiring, per-agent load). Every field is a copy taken
// under the appropriate lock; callers never touch scheduling internals.
type SchedulerSnapshot struct {
	// PollInterval is the drain cadence; PreemptInterval the preemption sweep
	// cadence (both as configured / defaulted).
	PollInterval    time.Duration `json:"pollInterval"`
	PreemptInterval time.Duration `json:"preemptInterval"`
	// TTL is the lease granted to each winning agent.
	TTL time.Duration `json:"ttl"`
	// MaxConcurrent is the per-drain parallelism cap (after defaulting).
	MaxConcurrent int `json:"maxConcurrent"`
	// EventDriven reports whether an event store subscription accelerates
	// dependency completion on top of polling.
	EventDriven bool `json:"eventDriven"`
	// Executors is the static + spawned executor count; BoundExecutors the
	// recovery-bound one-task-one-executor subset.
	Executors      int `json:"executors"`
	BoundExecutors int `json:"boundExecutors"`
	// Scheduled is the total successfully executed task count.
	Scheduled int64 `json:"scheduled"`
	// ReadyTasks is the fabric's current resumable (ready/suspended) depth —
	// the queue-depth signal for the panel's queue gauge.
	ReadyTasks int `json:"readyTasks"`
	// GovernanceWired / AgentFabricWired report optional subsystem wiring so
	// the panel can annotate whether budgets and dynamic population are live.
	GovernanceWired  bool `json:"governanceWired"`
	AgentFabricWired bool `json:"agentFabricWired"`
	// Load is the per-agent load/confidence snapshot from the tracker.
	Load LoadTrackerSnapshot `json:"load"`
}

// DecisionsSnapshot returns the recorded scheduling decisions (newest first)
// for the Scheduling Observatory. Purely read-only: the
// recorder's lock is internal; no scheduling write path is touched.
func (s *Scheduler) DecisionsSnapshot() []ScheduleDecision {
	if s.decisions == nil {
		return nil
	}
	return s.decisions.Snapshot()
}

// Snapshot returns the read-only view. It acquires only reader locks
// (execMu.RLock, tracker/fabric internal locks), never the drain write path,
// and is safe to call concurrently with Run (monitoring.md:
// "pure read-only, copy under a read lock, return an immutable copy").
func (s *Scheduler) Snapshot() SchedulerSnapshot {
	s.execMu.RLock()
	execN := len(s.executors)
	boundN := len(s.boundExecutors)
	s.execMu.RUnlock()

	snap := SchedulerSnapshot{
		PollInterval:     s.preemptInterval(), // same guard/default as both tickers
		PreemptInterval:  s.preemptInterval(),
		TTL:              s.ttl,
		MaxConcurrent:    s.maxConcurrent,
		EventDriven:      s.eventStore != nil,
		Executors:        execN,
		BoundExecutors:   boundN,
		Scheduled:        s.Scheduled.Load(),
		GovernanceWired:  s.governance != nil,
		AgentFabricWired: s.agents != nil,
	}
	if snap.MaxConcurrent <= 0 {
		// Mirror drainLimit's auto mode exactly (registry MAX fabric
		// population) — the snapshot must report the parallelism the drain
		// actually runs at, not the registry count alone (peer mode fans
		// out to fabric candidates the registry doesn't know about).
		snap.MaxConcurrent = max(execN, s.fabricCandidateCount())
	}
	if snap.MaxConcurrent <= 0 {
		snap.MaxConcurrent = 1
	}
	if snap.MaxConcurrent > 32 {
		snap.MaxConcurrent = 32 // same sanity cap as drain
	}
	if s.fabric != nil {
		snap.ReadyTasks = len(s.fabric.ResumableTasks())
	}
	if s.tracker != nil {
		snap.Load = s.tracker.Snapshot()
	}
	return snap
}
