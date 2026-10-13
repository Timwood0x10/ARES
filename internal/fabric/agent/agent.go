package agentfabric

import (
	"errors"
	"sync"
	"time"
)

// AgentState is the lifecycle state of a managed Agent (ares-runtime: disposable execution).
//
// State transfer table:
//
//	State       → allowed actions / exits
//	────────  ─────────────────────────────────────────
//	IDLE        → Suspend (→PAUSED), Retire (→RETIRED), Kill (→gone)
//	PAUSED      → Resume (→IDLE), Retire (→RETIRED), Kill (→gone)
//	RETIRED     → (terminal, no exits)
//
// R5: PAUSED replaces the old SUSPENDED name to avoid collision with the
// task-layer StateSuspended (which means a RUNNING task yielded its
// quantum). The agent PAUSED is a lifecycle-level pause — the agent is
// alive but excluded from scheduling. The string value stays "SUSPENDED"
// for wire compatibility (events, snapshots).
type AgentState string

const (
	// StateIdle: the agent is alive and available for assignment.
	StateIdle AgentState = "IDLE"
	// StatePaused: the agent is paused (Lifecycle, not Task); its state
	// is preserved and it can be resumed. Renamed from StateSuspended to
	// avoid collision with task-layer StateSuspended (R5). The wire value
	// stays "SUSPENDED" for event/snapshot compatibility.
	StatePaused AgentState = "SUSPENDED"
	// StateRetired: the agent is permanently decommissioned (retire); it
	// cannot be resumed. Its in-flight tasks are reclaimed by the Runtime.
	StateRetired AgentState = "RETIRED"
)

// Agent is a disposable, peer-equivalent cognitive process . Agents are NOT orchestrated — they are scheduled (by taskfabric) and
// managed (by this Fabric). An Agent independently holds its own Cognitive
// State; the Runtime never depends on hidden CoT, only on checkpointable
// state.
type Agent struct {
	// Identity is the stable agent identifier.
	Identity string
	// Capabilities are declared capabilities (used by the capability-aware
	// scheduler in taskfabric).
	Capabilities []string
	// State is the current lifecycle state.
	State AgentState
	// Load is the current utilization (0 = idle; scheduler hint).
	Load float64
	// Confidence is the experience-derived prior (ares_skills source).
	Confidence float64
	// Parent is the spawning agent's identity ("" for a root agent). This
	// is PROVENANCE ONLY — parent/child does NOT form a permission
	// hierarchy (invariant #1: A ≡ B ≡ C).
	Parent string
	// Priority is the scheduling priority (>= 0; 0 = normal). It mirrors
	// OS-thread priority: the taskfabric scheduler boosts higher-priority
	// agents when choosing among capable candidates.
	Priority float64
	// SpawnedAt is when the agent was created via spawn.
	SpawnedAt time.Time

	// resources is the agent's resource claim (name → amount), recorded at
	// spawn for quota accounting and released on kill/retire. Guarded by
	// Fabric.mu (the registry lock), not a.mu: it is only read/written under
	// the fabric lock during spawn/kill/retire.
	resources map[string]float64

	mu             sync.RWMutex // guards cognitive, privateContext, taskContext, state, load, cognition, busy, revived, clock, lastActiveAt, retiredAt
	cognitive      CognitiveState
	privateContext map[string]any
	taskContext    map[string]any // shared task state (read-only view for the agent)
	// cognition is the agent's execution body (execution capability injected into the unified Agent).
	// Nil means the agent has no execution capability yet — it can be managed
	// (spawn/kill/recover) but cannot run a quantum until injected via
	// SpawnSpec.CognitionFactory. Guarded by mu.
	cognition Cognition
	// revived reports that this agent's cognitive state was installed from a DEAD
	// agent's preserved snapshot (Fabric.Recover) instead of being accumulated by
	// living through its own quanta. It is the one bit that makes the two
	// continuation paths distinguishable from the outside (defect A6): work
	// finished by a revived agent continued WITH the dead agent's brain, while the
	// same work finished by a fresh agent continued on a blank slate. Set only by
	// Recover; never cleared, because a revived agent stays revived for its whole
	// life. Guarded by mu.
	revived bool
	// clock is the fabric's clock captured at spawn, so every timestamp this
	// agent derives agrees with the ones the fabric reasons about (a WithClock
	// fabric in tests, the wall clock in production). Nil on an agent built
	// outside Spawn — see now(). Guarded by mu.
	clock func() time.Time
	// lastActiveAt is when this agent last started a quantum. Idle reclamation
	// uses it: an agent may be retired only when it is IDLE, not busy, and
	// inactive longer than the policy window. SpawnedAt deliberately does NOT
	// serve here — being spawned long ago says nothing about being idle now, and
	// retiring a working agent is the one outcome the sweep must never produce.
	// Guarded by mu.
	lastActiveAt time.Time
	// busy is the R4 bridge between subAgent execution state and fabric
	// scheduling state. When true the agent is executing a quantum and must
	// NOT be offered as a scheduling candidate, even though its lifecycle
	// state is still StateIdle (the lifecycle state does not have a RUNNING
	// value — it was removed as unused). The scheduler's IsIdle check
	// therefore also reads this flag. Guarded by mu.
	busy bool
	// retiredAt is when Retire decommissioned this agent; zero on a live agent.
	// It is the age basis for reaping the tombstone (Fabric.ReapRetired): a
	// retired record is kept queryable on purpose, and without an age on the
	// record that retention could not be bounded at all. Guarded by mu.
	retiredAt time.Time
	// governance is the budget state (see governance.go). Nil when the
	// agent was spawned without budgets.
	governance *governanceState
	// toolAllowlist is the per-agent tool restriction set. Nil = inherit all
	// registered tools (backward compatible). Non-nil = only tools whose
	// names are in this set are allowed. Guarded by mu.
	toolAllowlist map[string]bool
}

// CognitiveStateSchemaVersion is the current CognitiveState schema version.
// Bump when CognitiveState fields change; DecodeCognitiveState handles
// migration from prior versions. (A versioned structure.)
const CognitiveStateSchemaVersion = 1

// CognitiveState is the agent's independent cognitive content (Context / Observation / Working Memory / Decision / Tool State / Checkpoint).
// It is independently checkpointable — the Runtime does NOT depend on hidden
// chain-of-thought, only on this durable state.
//
// SchemaVersion is the version of this struct. DecodeCognitiveState rejects a
// future version instead of silently misinterpreting it. Callers that
// construct a zero-value CognitiveState (e.g. in tests) produce SchemaVersion=0
// (legacy), which DecodeCognitiveState accepts as compatible.
type CognitiveState struct {
	// SchemaVersion is the state's schema version. Set by SetCognitiveState
	// and Recover; zero means legacy and is accepted by
	// DecodeCognitiveState.
	SchemaVersion int
	// Context is the active reasoning context (task goal + constraints).
	Context any
	// Observation is the latest observation from the environment/tools.
	Observation any
	// WorkingMemory is the agent's scratchpad for intermediate reasoning.
	WorkingMemory any
	// Decision is the current decision/hypothesis.
	Decision any
	// ToolState is the state of active tools (open files, connections…).
	ToolState any
	// Checkpoint is the durable progress pointer (links to taskfabric
	// Checkpoint when the agent is executing a Task).
	Checkpoint any
}

// ErrAgentNotFound is returned when an agent id is unknown to the Fabric.
var ErrAgentNotFound = errors.New("agentfabric: agent not found")

// ErrAgentExists is returned when an agent with the given id already exists.
var ErrAgentExists = errors.New("agentfabric: agent already exists")

// ErrAgentRetired is returned when an operation targets a retired agent.
var ErrAgentRetired = errors.New("agentfabric: agent retired")

// ErrAgentNotSuspended is returned when an operation (e.g. resume) targets an
// agent that is not in the SUSPENDED state.
var ErrAgentNotSuspended = errors.New("agentfabric: agent not suspended")

// ErrAgentNotExecutable is returned when ExecuteStep is called on an agent
// that was spawned without a CognitionFactory (execution capability not injected).
var ErrAgentNotExecutable = errors.New("agentfabric: agent not executable")

// IsToolAllowed reports whether the given tool is permitted for this agent.
// It returns true (permissive) when no allowlist is set — the agent inherits
// every registered tool. When a non-nil allowlist is set, only tools whose
// names are in the set pass. Thread-safe; reads the allowlist under the
// agent lock.
func (a *Agent) IsToolAllowed(toolName string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.toolAllowlist == nil {
		return true
	}
	return a.toolAllowlist[toolName]
}

// Executable reports whether the agent has an execution body injected
// (execution capability injected). An agent spawned without a CognitionFactory is managed
// (spawn/kill/recover) but NOT schedulable — the scheduler must not offer it
// as a candidate. Thread-safe; reads the cognition under the agent lock.
func (a *Agent) Executable() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cognition != nil
}

// SetBusy sets the agent's busy flag — the R4 bridge between subAgent
// execution state and fabric scheduling state. When true, IsSchedulable
// returns false so the scheduler does not offer the agent as a candidate
// while it is executing a quantum. The lifecycle state stays StateIdle
// (there is no RUNNING lifecycle state); busy is the execution-layer signal
// that complements it. Thread-safe; writes under the agent lock.
//
// The dispatch path uses TryBeginQuantum instead — it needs the claim, not a
// bare write. SetBusy remains for clearing the flag and for callers that hold
// the flag by construction (tests, and the deferred release).
func (a *Agent) SetBusy(busy bool) {
	a.mu.Lock()
	a.busy = busy
	a.mu.Unlock()
}

// TryBeginQuantum claims this agent for one quantum, setting the busy flag and
// reporting true ONLY IF the agent is IDLE and not already busy. It is the
// guarded form of SetBusy(true) and the dispatch path must use it.
//
// Why the guard rather than IsIdle followed by SetBusy: those two are not atomic
// together, so the agent can leave IDLE in between — in production idle
// reclamation retires it. The quantum would then run against an agent whose
// execution body Retire has already released, failing a task for a reason that
// has nothing to do with the task. Contending for this lock is what makes the
// decision single-winner: the sweep's retire and this claim cannot both succeed,
// and the loser acts on the truth (the sweep skips a busy agent; this reports
// false for one that just retired).
//
// A caller that gets false must release the task lease and let the next drain
// re-schedule — never fall back to SetBusy, which is exactly the unguarded write
// this replaces.
func (a *Agent) TryBeginQuantum() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.State != StateIdle || a.busy {
		return false
	}
	a.busy = true
	return true
}

// IsBusy reports whether the agent is currently executing a quantum (R4
// bridge). Thread-safe; reads under the agent lock.
func (a *Agent) IsBusy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.busy
}

// ErrInvalidSpawnSpec is returned when a SpawnSpec is invalid.
var ErrInvalidSpawnSpec = errors.New("agentfabric: invalid spawn spec")
