package aresrecovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/task"
)

// RecoveryObserver receives recovery timing events. It is the minimal surface
// the recovery path needs to feed timing into the arena metrics without
// aresrecovery反向依赖 arena: the arena side adapts its MetricsCollector to
// this interface at wiring time.
//
// The observer is called SYNCHRONOUSLY from the recovery path — it must be
// cheap (a map append + arithmetic, not a network call). A panic inside the
// observer is recovered so it can never kill the recovery loop.
type RecoveryObserver interface {
	// OnRecovery records one successful recovery. duration is the wall-clock
	// time from death detection to revival completion (zero when the start
	// time was unavailable — see RecoverFromAgentDeath).
	OnRecovery(duration time.Duration)
}

// Recovery orchestrates the Kernel's failure-recovery paths. It is a
// SEPARATE responsibility from Chaos (which injects failures):
// Recovery proves the Runtime survives them. The subsystem wires the Task
// Fabric (durable tasks + lease expiry + checkpoints) to the Agent Fabric
// (disposable agents + cognitive state) so that an agent death is followed
// by task requeue + checkpoint resume + agent replacement.
type Recovery struct {
	tasks  *taskfabric.Fabric
	agents *agentfabric.Fabric
	// observer receives recovery timing events. Nil = no observation
	// (the default; production wiring injects the arena adapter).
	observer RecoveryObserver
	// spawner is the optional evolution-aware spawn gate. When
	// set, every replacement spawn goes through it so the evolution policy
	// (Enabled / MaxConcurrent / PreferredCapabilities) shapes restarts and
	// checkpoint recovery too — "Evolution decides; Kernel enforces". Nil
	// keeps the plain fabric spawn.
	spawner *EvolutionAwareSpawner
	// cogFactory is the A1 execution-body factory injected into every
	// replacement spawn (the replacement goes through the A1 factory,
	// is executable — no phantom). When set, spawnAgent fills
	// spec.CognitionFactory before spawning so the replacement agent is a
	// REAL cognitive process that the scheduler can execute, not an empty
	// shell. Nil keeps the caller's spec untouched (the production peer path
	// passes its own factory through runKernelRecoveryLoop's executorFactory).
	cogFactory agentfabric.CognitionFactory
	// policy is the restart policy (max attempts, backoff).
	policy RestartPolicy
	// restarts records WHEN each identity was restarted, so the budget can be counted
	// inside the policy's RestartWindow. It is never reset by a successful revival: the
	// budget exists to stop a broken agent from cycling, and successful revivals are
	// exactly the cycles it must count.
	//
	// Stamps older than the window are pruned as the gate reads them (see
	// pruneRestarts), so the slice stays proportional to the budget, not to the agent's
	// lifetime. With a zero window nothing is pruned — the lifetime-cumulative semantics
	// the field had before P2-6①.
	mu       sync.Mutex
	restarts map[string][]time.Time
	now      func() time.Time
	// sleep suspends the calling goroutine for d, returning ctx.Err() when
	// ctx is cancelled first. It exists as a field so tests can replace the
	// real time.Sleep (same determinism pattern as the now clock above).
	sleep func(ctx context.Context, d time.Duration) error
}

// realSleeper sleeps for d unless ctx is cancelled first. The production
// default injected into Recovery.sleep.
func realSleeper(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// RestartPolicy bounds agent restart attempts after a crash.
type RestartPolicy struct {
	// MaxRestarts is the restart budget allowed per identity (0 = no restart).
	MaxRestarts int
	// RestartWindow bounds how far back the budget counts (defect P2-6①).
	//
	// Zero keeps the LIFETIME-CUMULATIVE behaviour: every death the identity ever had
	// counts, so an agent that crashes once a month is refused for good after MaxRestarts
	// months. That conflates two different things — "this agent is cycling right now" and
	// "this agent has died before" — and the budget exists for the first. With a window,
	// only deaths inside it count, so a long-lived healthy agent earns its budget back.
	//
	// The default (DefaultRestartPolicy) sets one hour: MaxRestarts=5 within an hour is a
	// crash-loop budget, and an agent that has been up for longer than that is not looping.
	// The zero value deliberately stays the old semantics, so a zero-value policy is
	// unchanged.
	RestartWindow time.Duration
	// Backoff is the initial delay before a restart attempt (doubles each
	// attempt, capped at MaxBackoff).
	Backoff time.Duration
	// MaxBackoff caps the backoff growth.
	MaxBackoff time.Duration
	// IdleAgentTTL is how long an agent may go without starting a quantum before
	// idle reclamation retires it and gives its resource claim back to the pool
	// (defect A-factory3 / P1-6). Zero DISABLES reclamation and keeps every agent
	// for the process's lifetime — which is what the pool did before this knob
	// existed, so the zero value stays the documented pre-fix behaviour.
	//
	// It lives on the restart policy because both answer "what happens to an
	// agent that is not working": restart handles one that died, reclamation
	// handles one that is simply not needed any more.
	IdleAgentTTL time.Duration
	// SuspendedAgentTTL is the same window for a SUSPENDED agent, which the same
	// sweep retires. It is a separate knob and a LONGER default because a paused
	// agent is not the same fact as an unused one: it had work, was deliberately
	// paused with its checkpoint preserved, and its task still holds its lease.
	// Zero DISABLES that arm, which is the pre-fix behaviour (a suspended agent
	// was unreclaimable) and the safe direction to fail: keeping one costs its
	// resources, retiring one prematurely costs the paused work.
	SuspendedAgentTTL time.Duration
	// RetiredAgentRetention is how long a retired agent's record stays queryable
	// before the sweep reaps it. Reclamation retires; it can never remove, so
	// without this the registry keeps one record per spawn for the process's
	// lifetime. Zero DISABLES reaping (records stay) — the zero value is the
	// pre-fix behaviour, and it is the safe direction to fail: a kept record
	// costs bytes, a removed one costs the lookup.
	RetiredAgentRetention time.Duration
}

// DefaultRestartPolicy is a sane production default: 5 attempts, 1s backoff
// capped at 30s.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		MaxRestarts: 5,
		// Five restarts inside an hour is the crash-loop budget (P2-6①): an agent that
		// dies five times within an hour is cycling, while one that has been up for longer
		// than the window has earned its budget back. Zero would keep the old
		// lifetime-cumulative semantics, which refused a healthy long-lived agent for good
		// after its fifth death ever — a refusal that looks like governance and is really a
		// bookkeeping artefact.
		RestartWindow: time.Hour,
		// Reclamation is ON by default (ten minutes of inactivity, one holder per
		// capability kept): the pool leak it fixes is silent and unbounded, so a
		// default that kept every agent forever would keep leaking in every
		// deployment that never heard of the knob.
		IdleAgentTTL: agentfabric.DefaultAgentReclaimPolicy().IdleTTL,
		// Suspended agents are reclaimed too (a paused agent is otherwise a
		// permanent leak: nothing schedules it, nothing resumes it, and the reaper
		// only removes RETIRED records). ON by default for the same reason as the
		// idle window, with the longer window the policy documents.
		SuspendedAgentTTL: agentfabric.DefaultAgentReclaimPolicy().SuspendedTTL,
		// Reaping is ON by default for the same reason, one step later: retirements
		// happen continuously in peer mode, and each one leaves a record.
		RetiredAgentRetention: agentfabric.DefaultRetiredAgentRetention,
		Backoff:               1 * time.Second,
		MaxBackoff:            30 * time.Second,
	}
}

// ErrRecoveryExhausted is returned when the restart budget is exhausted.
var ErrRecoveryExhausted = errors.New("aresrecovery: restart budget exhausted")

// pruneRestarts drops the stamps that fall outside the window and returns the rest.
//
// A zero (or negative) window returns the slice untouched: that is the LIFETIME-CUMULATIVE
// semantics the budget had before P2-6①, and a zero-value policy must keep it.
//
// It compacts in place (stamps[:0]) — the caller replaces its stored slice with the result,
// so the stale tail is never read again.
func pruneRestarts(stamps []time.Time, now time.Time, window time.Duration) []time.Time {
	if window <= 0 {
		return stamps
	}
	cutoff := now.Add(-window)
	kept := stamps[:0]
	for _, at := range stamps {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	return kept
}

// New wires the Recovery subsystem to the Task and Agent Fabrics.
func New(tasks *taskfabric.Fabric, agents *agentfabric.Fabric, policy RestartPolicy) *Recovery {
	if policy.MaxRestarts == 0 {
		policy = DefaultRestartPolicy()
	}
	if policy.Backoff == 0 {
		policy.Backoff = time.Second
	}
	if policy.MaxBackoff == 0 {
		policy.MaxBackoff = 30 * time.Second
	}
	return &Recovery{
		tasks:    tasks,
		agents:   agents,
		policy:   policy,
		restarts: make(map[string][]time.Time),
		now:      time.Now,
		sleep:    realSleeper,
	}
}

// WithClock injects a controllable clock for deterministic tests.
func (r *Recovery) WithClock(now func() time.Time) *Recovery {
	r.now = now
	return r
}

// WithSleeper injects a controllable sleeper for deterministic tests: the
// restart backoff is observable through the durations handed to it, without
// real time.Sleep. Returns the Recovery for chaining.
func (r *Recovery) WithSleeper(sleep func(ctx context.Context, d time.Duration) error) *Recovery {
	r.sleep = sleep
	return r
}

// WithRecoveryObserver injects a recovery timing observer. When set, each
// successful recovery in RecoverFromAgentDeath calls OnRecovery with the
// wall-clock duration from the sweep start to the recovery completion. Nil
// (the default) leaves observation off — the recovery path is unchanged.
func (r *Recovery) WithRecoveryObserver(obs RecoveryObserver) *Recovery {
	r.observer = obs
	return r
}

// WithSpawner injects the evolution-aware spawn gate. When set,
// every replacement spawn in RecoverTaskCheckpoint / RestartAgent is routed
// through it, so the evolution policy shapes restart and recovery spawns.
// Returns the Recovery for chaining.
func (r *Recovery) WithSpawner(s *EvolutionAwareSpawner) *Recovery {
	r.spawner = s
	return r
}

// WithCognitionFactory injects the A1 execution-body factory.
// Every replacement spawn produced by RecoverTaskCheckpoint /
// RestartAgent then carries the factory, so the replacement agent is
// executable (non-phantom). Returns the Recovery for chaining.
func (r *Recovery) WithCognitionFactory(f agentfabric.CognitionFactory) *Recovery {
	r.cogFactory = f
	return r
}

// spawnAgent creates a replacement agent, routing through the evolution
// spawner when wired, otherwise spawning directly on the fabric. Recovery
// spawns ALWAYS use the recovery path (SpawnForRecovery): they replace a
// dead/expired agent and must not be blocked by the population cap — a
// self-healing spawn rejected by MaxConcurrent would strand the task forever
// (recovery bypasses quota, not the Enabled gate).
func (r *Recovery) spawnAgent(ctx context.Context, spec agentfabric.SpawnSpec) (*agentfabric.Agent, error) {
	// Inject the A1 execution body so a replacement is a REAL cognitive
	// process (the scheduler can execute it), never a phantom shell.
	if r.cogFactory != nil && spec.CognitionFactory == nil {
		spec.CognitionFactory = r.cogFactory
	}
	if r.spawner != nil {
		return r.spawner.SpawnForRecovery(ctx, spec)
	}
	return r.agents.Spawn(ctx, spec)
}

// RequeueExpiredLeases sweeps the Task Fabric for expired leases and returns
// the ids of every task requeued to READY (lease expiry → requeue).
// A dead agent's lease expires; the task becomes acquirable again. This is
// the first recovery path. The returned ids are the ONLY tasks that were
// expired — the caller must NOT iterate all READY tasks (which would hijack
// brand-new tasks unrelated to any crash).
//
// Returns:
//   - []string: the ids of every requeued task (empty when none).
func (r *Recovery) RequeueExpiredLeases() []string {
	return r.tasks.CheckExpiredLeases()
}

// ExpireTaskDeadlines fails every task whose absolute deadline has passed and
// returns their ids. The kernel recovery sweep calls it BEFORE
// RequeueExpiredLeases: a task past its deadline must stop (Agent death is a
// recoverable execution fault, a passed deadline is a task-level stop), so the
// lease path must not hand it to a replacement executor first.
func (r *Recovery) ExpireTaskDeadlines() []string {
	return r.tasks.ExpireDeadlines()
}

// ExpireApprovals auto-rejects every pending approval gate whose
// ApprovalTimeout has passed. The kernel recovery sweep calls it AFTER
// ExpireTaskDeadlines and BEFORE RequeueExpiredLeases: an expired approval
// gate is a task-level stop (the human never responded), so the task must be
// driven to FAILED before the lease path tries to hand it to a replacement
// executor — a pending approval task has no lease, so the lease path would
// otherwise skip it, but calling it here keeps the sweep log honest (R2:
// recovery sweep does not "recover" a task that is actually waiting for a
// human, it auto-rejects it when the SLA fires).
func (r *Recovery) ExpireApprovals() []string {
	return r.tasks.ExpireApprovals()
}

// RecoverTaskCheckpoint resumes a task's preserved checkpoint with a new
// agent (checkpoint recovery). The task must be in a state where its
// checkpoint is preserved (SUSPENDED or READY after lease expiry). The
// Recovery subsystem:
//  1. Finds a replacement agent (spawns one if the original is gone).
//  2. Acquires the task for the new agent.
//  3. The new agent resumes from the checkpoint (its cognitive state is
//     installed from the task's preserved checkpoint).
//
// TEST/CHAOS-ONLY (this entry point installs checkpoints via
// agents.SetCognitiveState and acquires tasks itself, which is a separate
// mechanism from the production scheduler path (scheduler.executeWith-
// Candidates → taskfabric.DecodeCheckpoint → ToModelTask). Production
// recovery uses runKernelRecoveryLoop in cmd/ares. Callers: chaos
// simulations, sandbox tests, and recovery tests only.
//
// Args:
//   - ctx: for event sinks.
//   - taskID: the task to recover.
//   - replacementID: the new agent's id ("" = auto-spawn one).
//
// Returns:
//   - string: the replacement agent id.
//   - uint64: the new lease epoch (fencing token).
//   - error: taskfabric.ErrTaskNotFound / ErrRecoveryExhausted.
func (r *Recovery) RecoverTaskCheckpoint(ctx context.Context, taskID, replacementID string) (string, uint64, error) {
	t, err := r.tasks.Task(taskID)
	if err != nil {
		return "", 0, err
	}
	// The task must be acquirable (READY after lease expiry, or SUSPENDED with
	// a preserved checkpoint).
	if t.State != taskfabric.StateReady && t.State != taskfabric.StateSuspended {
		return "", 0, fmt.Errorf("aresrecovery: task %s not recoverable in state %s", taskID, t.State)
	}
	// Spawn or reuse the replacement agent.
	agentID := replacementID
	if agentID == "" {
		spawned, err := r.spawnAgent(ctx, agentfabric.SpawnSpec{
			Capabilities: []string{t.Capability},
		})
		if err != nil {
			return "", 0, fmt.Errorf("aresrecovery: spawn replacement: %w", err)
		}
		agentID = spawned.Identity
	}
	// Acquire the task for the replacement.
	epoch, err := r.tasks.Acquire(taskID, agentID, time.Minute)
	if err != nil {
		return "", 0, fmt.Errorf("aresrecovery: acquire %s for %s: %w", taskID, agentID, err)
	}
	// Install the preserved checkpoint as the new agent's cognitive state so
	// it resumes from where the dead agent left off. A failure to install the
	// checkpoint must not be silent: the task is acquired by a replacement
	// that cannot resume, so surface it for the recovery loop instead of
	// pretending recovery succeeded.
	if t.Checkpoint != nil {
		if err := r.agents.SetCognitiveState(agentID, agentfabric.CognitiveState{
			Checkpoint: t.Checkpoint,
			Context:    t.Checkpoint,
		}); err != nil {
			return "", 0, fmt.Errorf("aresrecovery: install checkpoint for %s: %w", agentID, err)
		}
	}
	return agentID, epoch, nil
}

// RestartAgent replaces a crashed agent with a new one that picks up the
// dead agent's cognitive checkpoint (agent restart). The original
// agent must be gone (killed). The new agent is spawned with the original's
// capabilities and cognitive state. The restart budget is checked and its
// slot reserved ATOMICALLY (check+increment under one lock hold); if
// exhausted, ErrRecoveryExhausted is returned, and concurrent calls for the
// same corpse cannot collectively exceed the budget. A failed
// spawn/recover rolls the reservation back, so only successful restarts
// consume budget. A backoff delay (Backoff
// doubled per prior attempt, capped at MaxBackoff) is slept before the
// replacement spawn, so consecutive crash-restart cycles cannot hammer the
// fabric; a cancelled ctx aborts the wait.
//
// Args:
//   - ctx: for event sinks.
//   - deadAgentID: the crashed agent's id.
//   - cognitive: the dead agent's preserved cognitive state.
//   - capabilities: the dead agent's declared capabilities.
//
// Returns:
//   - *agentfabric.Agent: the replacement agent.
//   - error: ErrRecoveryExhausted, or the ctx error when the ctx is
//     cancelled during the backoff sleep (no spawn happens).
func (r *Recovery) RestartAgent(ctx context.Context, deadAgentID string, cognitive agentfabric.CognitiveState, capabilities []string) (*agentfabric.Agent, error) {
	now := r.now()
	r.mu.Lock()
	// Count only the restarts inside the policy window (P2-6①). Successful revivals do
	// NOT reset the stamps — a revival IS the cycle the budget must count — but stamps
	// older than the window stop counting, so an agent that died MaxRestarts times over
	// months is not mistaken for a crash loop and refused forever. Pruning happens here,
	// at the gate, so the stored slice stays proportional to the budget rather than to the
	// agent's lifetime.
	kept := pruneRestarts(r.restarts[deadAgentID], now, r.policy.RestartWindow)
	attempts := len(kept)
	if attempts >= r.policy.MaxRestarts {
		r.restarts[deadAgentID] = kept // keep the pruned view so the map shrinks with it
		r.mu.Unlock()
		return nil, ErrRecoveryExhausted
	}
	// Reserve the slot NOW, atomically with the check: the backoff sleep
	// and spawn below happen outside the lock, so a check-then-charge-later
	// scheme let N concurrent RestartAgent calls for the same corpse each
	// pass the gate and collectively blow past MaxRestarts (TOCTOU). If the
	// spawn or recover below fails, the reservation is rolled back so a
	// failed attempt still does not consume the budget — the same
	// "only successful restarts count" semantics as before, just with an
	// atomic gate.
	r.restarts[deadAgentID] = append(kept, now)
	r.mu.Unlock()
	// rollbackBudget releases THIS caller's reservation. Each caller owns exactly one
	// stamp, so exactly one is removed: a value-match guard (only undo when the count is
	// still attempts+1) leaked under concurrent failures — with two failing callers the
	// first rollback saw the second's stamp and skipped, leaving a phantom count and
	// burning budget with zero successful restarts.
	rollbackBudget := func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		list := r.restarts[deadAgentID]
		if len(list) <= 1 {
			delete(r.restarts, deadAgentID)
			return
		}
		// Which stamp is dropped does not matter: only the count feeds the gate, and every
		// stamp present was added inside the window (an expired one was pruned above).
		r.restarts[deadAgentID] = list[:len(list)-1]
	}
	// Crash-restart storm prevention: consecutive crash-restart cycles used
	// to hammer the fabric instantly (sleep-less restarts, the exact storm
	// the policy's Backoff was written to prevent). Sleep backoff doubled
	// per prior attempt, capped at MaxBackoff — the 0-th restart pays the
	// plain Backoff. The sleeper honors ctx, so a shutdown aborts the wait
	// instead of spawning a replacement into a dying process. Sleep BEFORE
	// spawning, and never under r.mu (holding the lock while sleeping would
	// freeze RestartCount/budget checks).
	delay := r.policy.Backoff << attempts
	if delay > r.policy.MaxBackoff || delay <= 0 {
		// Cap at MaxBackoff; the <=0 guard also catches the (unreachable
		// for sane policies) shift-overflow into non-positive values.
		delay = r.policy.MaxBackoff
	}
	if err := r.sleep(ctx, delay); err != nil {
		rollbackBudget()
		return nil, fmt.Errorf("aresrecovery: restart backoff for %s: %w", deadAgentID, err)
	}
	// Arbitration: when a death snapshot exists for THIS
	// identity, revive IN PLACE under the same id — provenance and the audit
	// trail stay continuous ("stateful cognition revival"). Without a snapshot, fall back
	// to a freshly generated identity (plain replacement).
	spec := agentfabric.SpawnSpec{Capabilities: capabilities}
	if _, ok := r.agents.LastSnapshot(deadAgentID); ok {
		spec.Identity = deadAgentID
	}
	a, err := r.spawnAgent(ctx, spec)
	if err != nil {
		rollbackBudget()
		return nil, fmt.Errorf("aresrecovery: restart spawn for %s: %w", deadAgentID, err)
	}
	// Install the preserved cognitive state.
	if err := r.agents.Recover(ctx, a.Identity, cognitive); err != nil {
		rollbackBudget()
		return nil, fmt.Errorf("aresrecovery: restart recover for %s: %w", deadAgentID, err)
	}
	// The budget was already charged atomically at the gate above (the
	// reservation); a successful spawn+recover simply keeps it.
	// The snapshot is now CONSUMED by this revival; keeping it would let a
	// much later death of the revived body restore stale cognition.
	r.agents.ClearSnapshot(deadAgentID)
	return a, nil
}

// RestartCount returns how many restarts of this identity count against the budget.
//
// That is the IN-WINDOW count, not the lifetime total: it is the number the gate compares
// with MaxRestarts, and a caller reading "3 of 5 used" must see the same 3 the gate sees.
// With a zero RestartWindow the two are the same thing (lifetime-cumulative semantics).
func (r *Recovery) RestartCount(agentID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(pruneRestarts(r.restarts[agentID], r.now(), r.policy.RestartWindow))
}

// ReclaimIdleAgents retires the agents that have been idle longer than the
// policy's window, releasing their resource claims back to the pool, and returns
// them in id order. A zero IdleAgentTTL disables it and returns nil.
//
// It is the pool's only reclamation path in production: without it, every
// spawned agent holds its claim for the life of the process, so a configured
// budget fills up and stays full (ErrResourceQuotaExceeded on all later spawns)
// and the agents themselves accumulate. See agentfabric.AgentReclaimPolicy.
func (r *Recovery) ReclaimIdleAgents(ctx context.Context) []agentfabric.IdleAgent {
	if r.policy.IdleAgentTTL <= 0 && r.policy.SuspendedAgentTTL <= 0 {
		return nil
	}
	// "Nobody is using it" is decided on TASK OWNERSHIP, not on the activity
	// clock: an agent whose task was dispatched but whose quantum has not started
	// yet holds a lease while still looking idle. Only this layer can make that
	// call — it is the one that can see both fabrics — and LeaseSnapshot reports
	// live leases only (never terminal), so an empty set is the normal case for a
	// quiet pool.
	inUse := make(map[string]struct{})
	for _, lease := range r.tasks.LeaseSnapshot() {
		if lease.Owner != "" {
			inUse[lease.Owner] = struct{}{}
		}
	}
	return r.agents.ReclaimIdle(ctx, agentfabric.AgentReclaimPolicy{
		IdleTTL:      r.policy.IdleAgentTTL,
		SuspendedTTL: r.policy.SuspendedAgentTTL,
		// One holder per capability is an invariant, not a knob: retiring the
		// last agent able to serve a capability would turn a quota leak into
		// tasks that can never be scheduled at all.
		KeepPerCapability: 1,
	}, inUse)
}

// AuditOrphans reports the live agents whose spawning parent is gone (P2-7's
// reconciliation half — see agentfabric.OrphanAudit for why this is an audit and not a
// cascade).
//
// It lives here, not only on the fabric, because this layer already runs the reclaim sweep
// and therefore already pays for a walk of the live agents: the audit is what turns "the
// sweep reclaimed 3 agents" into "and 11 agents nothing owns are still holding 4 units".
//
// sample bounds the returned id list (<= 0 means none).
func (r *Recovery) AuditOrphans(sample int) agentfabric.OrphanAudit {
	return r.agents.AuditOrphans(sample)
}

// ReapRetiredAgents removes the registry records of agents retired longer ago
// than the policy's retention window, and returns their ids in order. A zero
// retention disables it and returns nil.
//
// It runs AFTER ReclaimIdleAgents in the sweep, and the order matters for the
// unit of work rather than for safety: a sweep that reclaimed an agent this tick
// should not also delete the record it just created (retention is far longer
// than a tick, so this only ever reaps earlier sweeps' tombstones).
func (r *Recovery) ReapRetiredAgents(ctx context.Context) []string {
	if r.policy.RetiredAgentRetention <= 0 {
		return nil
	}
	return r.agents.ReapRetired(ctx, r.policy.RetiredAgentRetention)
}

// RevivableSnapshot exposes the agent fabric's death-snapshot matching the
// given capability (arbitration input). Found does not imply
// allowed — RestartAgent still enforces the restart budget.
func (r *Recovery) RevivableSnapshot(capability string) (string, agentfabric.AgentSnapshot, bool) {
	return r.agents.FindRevivableSnapshot(capability)
}

// RecoverFromAgentDeath is the full recovery chain (the acceptance path:
// "inject failure → kill agent → lease expires → Task READY → B acquire →
// checkpoint resume"). It sweeps expired leases, requeues tasks, and resumes
// each requeued task's checkpoint with a fresh replacement agent.
//
// TEST/CHAOS-ONLY (this entry point installs checkpoints via
// agents.SetCognitiveState and acquires tasks itself, which is a separate
// mechanism from the production path (runKernelRecoveryLoop → taskfabric
// DecodeCheckpoint → scheduler Schedule/Acquire). It must not be wired into
// the production serve path — production recovery uses runKernelRecoveryLoop
// in cmd/ares. Callers: chaos simulations, sandbox tests, and recovery
// tests only.
//
// Args:
//   - ctx: for event sinks.
//
// Returns:
//   - int: the number of tasks fully recovered (requeued + checkpoint resumed).
func (r *Recovery) RecoverFromAgentDeath(ctx context.Context) int {
	// Record the sweep start so each successful recovery can report its
	// wall-clock duration. The start point is the sweep itself (not the
	// agent's death timestamp, which is not reliably available here — the
	// lease expiry that triggers this sweep is the earliest signal).
	sweepStart := r.now()
	requeued := r.RequeueExpiredLeases()
	if len(requeued) == 0 {
		return 0
	}
	// Resume the checkpoint of exactly the tasks whose lease expired — not
	// every READY task. A brand-new or released task is not a crash-recovery
	// candidate and must not be grabbed by a replacement agent.
	recovered := 0
	for _, taskID := range requeued {
		if _, _, err := r.RecoverTaskCheckpoint(ctx, taskID, ""); err != nil {
			// A failure here means the task was requeued to READY but the
			// checkpoint resume failed (spawn/acquire/SetCognitiveState).
			// The task is still READY and will be picked up by the scheduler
			// on the next drain, but surface the failure instead of
			// silently dropping it.
			slog.Error("aresrecovery: recover task failed",
				slog.String("task_id", taskID),
				slog.Any("error", err))
			continue
		}
		recovered++
		// Notify the observer (if wired) with the per-task recovery
		// duration. The panic guard ensures an observer bug can never
		// kill the recovery loop.
		r.notifyObserver(sweepStart)
	}
	return recovered
}

// notifyObserver calls the recovery observer with the elapsed time since
// start. It is panic-safe: an observer bug is logged and swallowed so the
// recovery loop never crashes through the observer path.
func (r *Recovery) notifyObserver(start time.Time) {
	if r.observer == nil {
		return
	}
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("aresrecovery: recovery observer panicked",
				slog.Any("panic", rec))
		}
	}()
	r.observer.OnRecovery(r.now().Sub(start))
}

// RecoveryTask is a snapshot of a task that was requeued from an expired
// lease. It carries the minimal information the recovery loop needs to
// spawn a replacement executor: the task ID and its required capability.
type RecoveryTask struct {
	// ID is the task identifier.
	ID string
	// Capability is the task's required capability (used to match a
	// replacement executor).
	Capability string
	// Checkpoint is the preserved checkpoint from the dead agent (nil when
	// the task had no checkpoint — a fresh start).
	Checkpoint any
	// Attempts and MaxRetries are the task's OWN retry budget, carried so that a
	// caller which also holds a revival budget can state both in one place
	// (defect P2-5: the two budgets did not know about each other).
	//
	// Neither policy can see the other by construction — the task fabric knows
	// nothing about agents, and the recovery policy is handed only a dead agent id
	// and its capability — so the ONLY place a joint statement can be made is a
	// caller holding both. This snapshot is that caller's input; the numbers are
	// copied verbatim (no clamping, no inference) because a wrong number here would
	// be worse than a missing one.
	Attempts   int
	MaxRetries int
}

// RecoveryTasksFor returns a RecoveryTask snapshot for each requeued task id
// from RequeueExpiredLeases. Only the requeued ids are accepted — the
// caller must pass exactly the ids returned by RequeueExpiredLeases, never
// ReadyTasks, so a brand-new READY task is never mistaken for a crash-
// recovery candidate.
//
// Args:
//   - taskIDs: the ids returned by RequeueExpiredLeases.
//
// Returns:
//   - []RecoveryTask: the tasks ready for recovery (empty when none).
func (r *Recovery) RecoveryTasksFor(taskIDs []string) []RecoveryTask {
	out := make([]RecoveryTask, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		t, err := r.tasks.Task(taskID)
		if err != nil {
			continue
		}
		out = append(out, RecoveryTask{
			ID:         t.ID,
			Capability: t.Capability,
			Checkpoint: t.Checkpoint,
			Attempts:   t.RetryPolicy.Attempts,
			MaxRetries: t.RetryPolicy.MaxRetries,
		})
	}
	return out
}
