package aresrecovery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Recovery orchestrates the Kernel's failure-recovery paths (design §13 +
// P5). It is a SEPARATE responsibility from Chaos (which injects failures):
// Recovery proves the Runtime survives them. The subsystem wires the Task
// Fabric (durable tasks + lease expiry + checkpoints) to the Agent Fabric
// (disposable agents + cognitive state) so that an agent death is followed
// by task requeue + checkpoint resume + agent replacement.
type Recovery struct {
	tasks  *taskfabric.Fabric
	agents *agentfabric.Fabric
	// spawner is the optional evolution-aware spawn gate (v0.3.0 M2-1). When
	// set, every replacement spawn goes through it so the evolution policy
	// (Enabled / MaxConcurrent / PreferredCapabilities) shapes restarts and
	// checkpoint recovery too — "Evolution decides; Kernel enforces". Nil
	// keeps the plain fabric spawn.
	spawner *EvolutionAwareSpawner
	// policy is the restart policy (max attempts, backoff).
	policy RestartPolicy
	// restarts tracks how many times each agent id has been restarted.
	mu       sync.Mutex
	restarts map[string]int
	now      func() time.Time
}

// RestartPolicy bounds agent restart attempts after a crash.
type RestartPolicy struct {
	// MaxRestarts is the total restart attempts allowed (0 = no restart).
	MaxRestarts int
	// Backoff is the initial delay before a restart attempt (doubles each
	// attempt, capped at MaxBackoff).
	Backoff time.Duration
	// MaxBackoff caps the backoff growth.
	MaxBackoff time.Duration
}

// DefaultRestartPolicy is a sane production default: 5 attempts, 1s backoff
// capped at 30s.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		MaxRestarts: 5,
		Backoff:     1 * time.Second,
		MaxBackoff:  30 * time.Second,
	}
}

// ErrRecoveryExhausted is returned when the restart budget is exhausted.
var ErrRecoveryExhausted = errors.New("aresrecovery: restart budget exhausted")

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
		restarts: make(map[string]int),
		now:      time.Now,
	}
}

// WithClock injects a controllable clock for deterministic tests.
func (r *Recovery) WithClock(now func() time.Time) *Recovery {
	r.now = now
	return r
}

// WithSpawner injects the evolution-aware spawn gate (v0.3.0 M2-1). When set,
// every replacement spawn in RecoverTaskCheckpoint / RestartAgent is routed
// through it, so the evolution policy shapes restart and recovery spawns.
// Returns the Recovery for chaining.
func (r *Recovery) WithSpawner(s *EvolutionAwareSpawner) *Recovery {
	r.spawner = s
	return r
}

// spawnAgent creates a replacement agent, routing through the evolution
// spawner when wired, otherwise spawning directly on the fabric. Recovery
// spawns ALWAYS use the recovery path (SpawnForRecovery): they replace a
// dead/expired agent and must not be blocked by the population cap — a
// self-healing spawn rejected by MaxConcurrent would strand the task forever
// (v0.3.0 M2-1; recovery bypasses quota, not the Enabled gate).
func (r *Recovery) spawnAgent(ctx context.Context, spec agentfabric.SpawnSpec) (*agentfabric.Agent, error) {
	if r.spawner != nil {
		return r.spawner.SpawnForRecovery(ctx, spec)
	}
	return r.agents.Spawn(ctx, spec)
}

// RequeueExpiredLeases sweeps the Task Fabric for expired leases and returns
// the ids of every task requeued to READY (design: lease expiry → requeue).
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

// RecoverTaskCheckpoint resumes a task's preserved checkpoint with a new
// agent (design: checkpoint recovery). The task must be in a state where its
// checkpoint is preserved (SUSPENDED or READY after lease expiry). The
// Recovery subsystem:
//  1. Finds a replacement agent (spawns one if the original is gone).
//  2. Acquires the task for the new agent.
//  3. The new agent resumes from the checkpoint (its cognitive state is
//     installed from the task's preserved checkpoint).
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
	// pretending recovery succeeded (code_rules_v2 §3.1).
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
// dead agent's cognitive checkpoint (design: agent restart). The original
// agent must be gone (killed). The new agent is spawned with the original's
// capabilities and cognitive state. The restart budget is checked; if
// exhausted, ErrRecoveryExhausted is returned.
//
// Args:
//   - ctx: for event sinks.
//   - deadAgentID: the crashed agent's id.
//   - cognitive: the dead agent's preserved cognitive state.
//   - capabilities: the dead agent's declared capabilities.
//
// Returns:
//   - *agentfabric.Agent: the replacement agent.
//   - error: ErrRecoveryExhausted.
func (r *Recovery) RestartAgent(ctx context.Context, deadAgentID string, cognitive agentfabric.CognitiveState, capabilities []string) (*agentfabric.Agent, error) {
	r.mu.Lock()
	attempts := r.restarts[deadAgentID]
	if attempts >= r.policy.MaxRestarts {
		r.mu.Unlock()
		return nil, ErrRecoveryExhausted
	}
	r.restarts[deadAgentID] = attempts + 1
	r.mu.Unlock()
	// Spawn the replacement with the dead agent's capabilities.
	a, err := r.spawnAgent(ctx, agentfabric.SpawnSpec{
		Capabilities: capabilities,
	})
	if err != nil {
		return nil, fmt.Errorf("aresrecovery: restart spawn for %s: %w", deadAgentID, err)
	}
	// Install the preserved cognitive state.
	if err := r.agents.Recover(ctx, a.Identity, cognitive); err != nil {
		return nil, fmt.Errorf("aresrecovery: restart recover for %s: %w", deadAgentID, err)
	}
	return a, nil
}

// RestartCount returns how many times an agent has been restarted.
func (r *Recovery) RestartCount(agentID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restarts[agentID]
}

// RecoverFromAgentDeath is the full recovery chain (design P5 acceptance:
// "inject failure → kill agent → lease expires → Task READY → B acquire →
// checkpoint resume"). It sweeps expired leases, requeues tasks, and resumes
// each requeued task's checkpoint with a fresh replacement agent.
//
// Args:
//   - ctx: for event sinks.
//
// Returns:
//   - int: the number of tasks fully recovered (requeued + checkpoint resumed).
func (r *Recovery) RecoverFromAgentDeath(ctx context.Context) int {
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
			// silently dropping it (code_rules_v2 §3.1).
			log.Printf("aresrecovery: recover task %s: %v", taskID, err)
			continue
		}
		recovered++
	}
	return recovered
}

// RecoveryTask is a snapshot of a task that was requeued from an expired
// lease (W1). It carries the minimal information the recovery loop needs to
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
}

// RecoveryTasksFor returns a RecoveryTask snapshot for each requeued task id
// from RequeueExpiredLeases (W1). Only the requeued ids are accepted — the
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
		})
	}
	return out
}
