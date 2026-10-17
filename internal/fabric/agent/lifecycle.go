package agentfabric

import (
	"context"
	"errors"
	"fmt"
)

// SpawnSpec is the syscall-style spawn request (spawn is a
// syscall, not an orchestration API). The Kernel validates quota / capability
// / resource / policy, then creates the Agent + (optionally) a Task + the
// parent-child provenance link.
//
// ToolAllowlist narrows the spawned agent's tool set. When non-empty, the
// agent can only use tools whose names appear in the list — the planner's
// isToolEnabled checks Agent.IsToolAllowed before growing a tool node, and the
// executor checks it again before calling the tool. When empty or nil, the
// agent inherits every registered tool (backward compatible).
// ValidateToolSet (guardrails.go) is a GA-side candidate validator, NOT a
// runtime per-agent isolation — do not conflate the two.
type SpawnSpec struct {
	// Identity is the requested agent id; "" means the Fabric assigns one.
	Identity string
	// Capabilities are the declared capabilities of the new agent.
	Capabilities []string
	// ParentID is the spawning agent's id ("" for a root agent).
	ParentID string
	// TaskContext is the shared task state passed from the parent (a
	// snapshot or selected projection — never the parent's private state).
	TaskContext map[string]any
	// Resources are resource hints (quota/capability/policy validation
	// surface; stored opaquely here, fully enforced at admission).
	Resources map[string]any
	// Governance is the cognitive-execution budget (token/tool/deadline).
	// Zero values mean unlimited. Set here so the Kernel admits the agent with
	// its budgets from birth.
	Governance Governance
	// Priority is the scheduling priority of the new agent (>= 0; 0 =
	// normal). It mirrors OS-thread priority: the taskfabric scheduler boosts
	// higher-priority agents when choosing among capable candidates.
	Priority float64
	// CognitionFactory produces the agent's execution body (Cognition) from
	// its declared capabilities. When nil, the spawned agent has no execution
	// capability and cannot run a quantum (it can still be managed by
	// lifecycle operations). The factory is called once at spawn time,
	// BEFORE the fabric lock is taken (arbitrary caller code must never run
	// under the fabric mutex), and the result is stored in Agent.cognition.
	// A non-nil factory returning nil is rejected.
	CognitionFactory CognitionFactory
	// ExperiencePrior is the distilled prior experience loaded as the
	// agent's initial cognitive context at spawn time. It is written into
	// CognitiveState.Context so the agent starts with relevant distilled
	// experience instead of a blank slate. Nil = no prior (zero-value usable).
	ExperiencePrior any
	// ToolAllowlist narrows the tools available to the spawned agent. When
	// nil or empty, the agent inherits every registered tool (backward
	// compatible). When non-empty, only listed tool names are permitted —
	// the planner checks Agent.IsToolAllowed before growing a tool node, and
	// the executor checks it again before calling the tool.
	ToolAllowlist []string
}

// Spawn is the Kernel syscall that creates a new Agent (spawn
// establishes provenance, NOT hierarchy). The new agent is a same-level
// cognitive process (A ≡ B ≡ C): it can compete with its parent for tasks,
// communicate as a peer, and survive its parent's death. The parent-child
// link is recorded in the Process Tree for provenance/Lifecycle only — it
// does NOT form a permission hierarchy.
//
// The Kernel validates the spec (non-empty capabilities when a parent is
// present; non-duplicate id) and the resource quota (a spawn whose
// requested resources exceed the remaining budget is rejected with
// ErrResourceQuotaExceeded — the claim is recorded and released on
// kill/retire) before creating the agent. Spawn does NOT schedule the new
// agent — that is the Scheduler's job.
//
// Args:
//   - ctx: for the event sink.
//   - spec: the spawn request.
//
// Returns:
//   - *Agent: the newly created agent in StateIdle.
//   - error: ErrAgentExists / ErrInvalidSpawnSpec / ErrResourceQuotaExceeded.
func (f *Fabric) Spawn(ctx context.Context, spec SpawnSpec) (*Agent, error) {
	if err := validateSpawnSpec(spec); err != nil {
		return nil, err
	}
	// Build the execution body BEFORE taking the fabric lock. The factory
	// is arbitrary caller code (it constructs LLM clients, tool bundles,
	// …): under f.mu it blocked every fabric operation for its duration,
	// deadlocked on any re-entrant fabric call (Agents/Get/…), and a panic
	// left the fabric mutex locked forever. The result is stored in the new
	// agent after the lock is taken; a nil product is a programming error
	// rejected before any fabric state is mutated.
	var cognition Cognition
	if spec.CognitionFactory != nil {
		cognition = spec.CognitionFactory(spec.Capabilities)
		if cognition == nil {
			return nil, fmt.Errorf("%w: CognitionFactory returned nil for agent %q",
				ErrInvalidSpawnSpec, spec.Identity)
		}
	}
	claim := parseResourceClaim(spec.Resources)
	f.mu.Lock()
	id := spec.Identity
	if id == "" {
		id = f.nextIDLocked()
	}
	if _, exists := f.agents[id]; exists {
		f.mu.Unlock()
		return nil, ErrAgentExists
	}
	// Resource admission: reject before mutating any state so a failed
	// spawn leaves the fabric untouched (validate first,
	// then mutate).
	if !f.canAllocateLocked(claim) {
		// The message carries the accounting on purpose (defect P1-6): a bare
		// ErrResourceQuotaExceeded reads as "the pool is broken", while the
		// snapshot shows the steady state that actually caused it — claims held by
		// agents nothing will ever release. Logged once per rejection, this is
		// what turns a silent false-death into a diagnosable one.
		allocated, budget := f.quotaSnapshotLocked()
		f.mu.Unlock()
		return nil, fmt.Errorf("%w: requested %v, allocated %v of budget %v",
			ErrResourceQuotaExceeded, claim, allocated, budget)
	}
	a := &Agent{
		Identity:     id,
		Capabilities: append([]string(nil), spec.Capabilities...),
		State:        StateIdle,
		Parent:       spec.ParentID,
		Priority:     spec.Priority,
		SpawnedAt:    f.now(),
		// The agent inherits the fabric's clock so its activity stamps are
		// comparable with the fabric's own timestamps (WithClock in tests); it
		// starts ACTIVE, because a freshly spawned agent is waiting for work, not
		// idle — reclamation measures inactivity since the last quantum.
		clock:          f.now,
		lastActiveAt:   f.now(),
		resources:      claim,
		taskContext:    cloneMap(spec.TaskContext),
		privateContext: make(map[string]any),
	}
	// Inject the pre-built execution body. A nil factory leaves the agent
	// without execution capability (managed but not schedulable).
	a.cognition = cognition
	// Load the distilled prior experience as the agent's initial cognitive
	// context so a spawned agent starts with reusable experience instead of a
	// blank slate. Nil (zero value) leaves the agent with an empty cognitive
	// state.
	if spec.ExperiencePrior != nil {
		a.cognitive = CognitiveState{
			SchemaVersion: CognitiveStateSchemaVersion,
			Context:       spec.ExperiencePrior,
		}
	}
	// Governance: every agent carries a budget state from birth. A
	// zero-value Governance means "unlimited" for all dimensions (the default
	// for legacy agents), so ConsumeResource never fails with
	// ErrAgentNotGoverned — it only fails when a non-zero budget is exceeded.
	gov := &governanceState{cfg: spec.Governance}
	if spec.Governance.Deadline > 0 {
		gov.deadline = f.now().Add(spec.Governance.Deadline)
	}
	a.governance = gov
	// Install the per-agent tool allowlist. An empty list means the agent
	// inherits every registered tool (backward compatible); a non-empty one
	// permits only the listed tools.
	if len(spec.ToolAllowlist) > 0 {
		a.toolAllowlist = make(map[string]bool, len(spec.ToolAllowlist))
		for _, t := range spec.ToolAllowlist {
			a.toolAllowlist[t] = true
		}
	}
	f.agents[id] = a
	f.allocateLocked(claim)
	if spec.ParentID != "" {
		f.children[spec.ParentID] = append(f.children[spec.ParentID], id)
	}
	f.mu.Unlock()
	f.record(ctx, a, EventAgentSpawned, map[string]any{
		"parent_id":    spec.ParentID,
		"capabilities": spec.Capabilities,
		"resources":    spec.Resources,
	})
	return a, nil
}

// validateSpawnSpec checks the spec is well-formed.
func validateSpawnSpec(spec SpawnSpec) error {
	// ParentID "" is fine (root agent). Identity "" is fine (auto-assigned).
	// A spec with an explicit id must not conflict — checked under mu in Spawn.
	if spec.Identity != "" {
		// reserved/id format check: no spaces.
		for _, r := range spec.Identity {
			if r == ' ' {
				return ErrInvalidSpawnSpec
			}
		}
	}
	return nil
}

// Suspend pauses an IDLE or RUNNING agent (Lifecycle, not Task). The agent's
// in-memory state is preserved; Resume relaunches the SAME instance. A
// retired agent cannot be suspended.
//
// Args:
//   - ctx: for the event sink.
//   - agentID: the agent to suspend.
//
// Returns:
//   - error: ErrAgentNotFound / ErrAgentRetired / ErrAgentNotSuspended.
func (f *Fabric) Suspend(ctx context.Context, agentID string) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	if a.State == StateRetired {
		f.mu.Unlock()
		return ErrAgentRetired
	}
	if a.State == StatePaused {
		f.mu.Unlock()
		return nil // idempotent
	}
	a.mu.Lock()
	a.State = StatePaused
	a.mu.Unlock()
	f.mu.Unlock()
	f.record(ctx, a, EventAgentSuspended, nil)
	return nil
}

// Resume relaunches a previously suspended agent. It is a no-op for agents
// that are not suspended. A retired agent cannot be resumed.
//
// Args:
//   - ctx: for the event sink.
//   - agentID: the agent to resume.
//
// Returns:
//   - error: ErrAgentNotFound / ErrAgentRetired / ErrAgentNotSuspended.
func (f *Fabric) Resume(ctx context.Context, agentID string) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	if a.State == StateRetired {
		f.mu.Unlock()
		return ErrAgentRetired
	}
	if a.State != StatePaused {
		f.mu.Unlock()
		return ErrAgentNotSuspended
	}
	a.mu.Lock()
	a.State = StateIdle
	a.mu.Unlock()
	f.mu.Unlock()
	f.record(ctx, a, EventAgentResumed, nil)
	return nil
}

// Retire permanently decommissions an agent (graceful). The agent must NOT be
// RUNNING — suspend it first. A retired agent cannot be resumed; its in-flight
// tasks (if any) are reclaimed by the Runtime's recovery. Retiring a parent
// does NOT kill its children (invariant #1: parent death ≠ child death).
// The agent's resource claim is released back to the quota.
//
// Args:
//   - ctx: for the event sink.
//   - agentID: the agent to retire.
//
// Returns:
//   - error: ErrAgentNotFound / ErrAgentRetired.
func (f *Fabric) Retire(ctx context.Context, agentID string) error {
	f.mu.Lock()
	a, orphans, err := f.retireLocked(agentID)
	f.mu.Unlock()
	if errors.Is(err, ErrAgentRetired) {
		return nil // already retired: Retire is idempotent by contract
	}
	if err != nil {
		return err
	}
	f.recordAgentRetired(ctx, a, orphans)
	return nil
}

// retireLocked is Retire's lock-held form, shared with idle reclamation so both
// triggers release through ONE path (reclamation adds a trigger, never a second
// bookkeeping scheme).
//
// It returns the agent when THIS call retired it, ErrAgentRetired when a
// previous call had already retired it, and ErrAgentNotFound when the id is
// unknown. Caller must hold f.mu; callers record the event AFTER unlocking,
// because recording does I/O and must not run under the fabric lock.
func (f *Fabric) retireLocked(agentID string) (*Agent, []string, error) {
	a, ok := f.agents[agentID]
	if !ok {
		return nil, nil, ErrAgentNotFound
	}
	if a.State == StateRetired {
		// Reported as a sentinel rather than (nil, nil) so every caller must
		// state what it does with "already retired" — Retire treats it as
		// success, reclamation as "not mine to count".
		return nil, nil, ErrAgentRetired
	}
	// Read BEFORE the state change: the agents this one spawned stay live (Parent is
	// provenance, not a lifecycle relation — see Agent.Parent), so they keep their
	// resource claims and a spawn/retire loop accumulates them (P2-7). The list is
	// RETURNED rather than logged because the callers record after unlocking.
	orphans := f.orphanedSpawnsLocked(agentID)
	// (A RUNNING guard was removed with SetRunning/SetIdle: those transitions
	// had zero production callers, so no agent could ever be RUNNING here.)
	f.releaseLocked(a.resources)
	a.resources = nil
	a.mu.Lock()
	a.State = StateRetired
	a.retiredAt = f.now()
	// A retired agent is a RECORD, not a worker: scheduling candidates must be
	// StateIdle (see Fabric.IsIdle) and Retire is terminal, so this agent can
	// never execute again — its runtime state is dead weight. It is also not
	// small: cognition holds the execution body (LLM client and tool bundle) and
	// cognitive holds the preserved state. Idle reclamation makes retiring the
	// COMMON path, so keeping either would leak exactly what reclamation exists
	// to reclaim (defect A-factory3 / P1-6: the pool's memory grew with every
	// spawn).
	a.cognition = nil
	a.cognitive = CognitiveState{}
	a.mu.Unlock()
	// Retire is terminal — any death snapshot from an earlier kill/revive
	// cycle of this identity must not resurrect later.
	f.snapshots.clear(agentID)
	return a, orphans, nil
}

// orphanedSpawnsLocked lists the still-live agents whose Parent is parentID.
//
// Caller must hold f.mu. Each agent's Parent/State is read under its own lock, the
// f.mu → a.mu order IsIdle already establishes. A root agent has no parent, so an
// empty parentID short-circuits: there is nothing to look for.
func (f *Fabric) orphanedSpawnsLocked(parentID string) []string {
	if parentID == "" {
		return nil
	}
	var out []string
	for id, a := range f.agents {
		if id == parentID {
			continue
		}
		a.mu.RLock()
		parent, state := a.Parent, a.State
		a.mu.RUnlock()
		if parent == parentID && state != StateRetired {
			out = append(out, id)
		}
	}
	return out
}

// recordAgentRetired emits the retirement event, carrying the agents this one left
// live (P2-7). Kept out of the locked section by both callers: recording does I/O.
func (f *Fabric) recordAgentRetired(ctx context.Context, a *Agent, orphans []string) {
	payload := map[string]any{
		PayloadKeyStopReason: StopRetired.String(),
	}
	if len(orphans) > 0 {
		// Only on retirement that actually orphans something: an empty list on every
		// event would be noise, and "this agent left N live children" is the fact an
		// operator acts on.
		payload[PayloadKeyOrphanedSpawns] = orphans
	}
	f.record(ctx, a, EventAgentRetired, payload)
}

// Kill forcefully terminates an agent (non-graceful; e.g. crash). Unlike
// Retire, Kill works on any state and is the crash path. The agent entry is
// removed from the registry, but its children survive (Parent death ≠
// Child death). Children's Parent field is NOT cleared — it stays as
// provenance. Task reclaim is the recovery subsystem's job. The agent's resource claim is
// released back to the quota.
//
// Args:
//   - ctx: for the event sink.
//   - agentID: the agent to kill.
//
// Returns:
//   - error: ErrAgentNotFound.
func (f *Fabric) Kill(ctx context.Context, agentID string) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	// Capture the revival record BEFORE the registry entry disappears —
	// after this delete the agent is unreadable, so the recovery subsystem's
	// in-place-revival decision depends on this snapshot existing.
	//
	// Kill is the non-graceful path (panic, operator kill, chaos), so the
	// death is recorded as StopCrashed. The reason rides the snapshot into the
	// recovery decision and the agent.killed event, which is what separates
	// "died and was revived" from "died and recovery refused" in the log.
	snap := captureFromAgent(a, f.now(), StopCrashed)
	delete(f.agents, agentID)
	f.releaseLocked(a.resources)
	a.resources = nil
	// NOTE: children of a killed agent survive. We do NOT clear their Parent
	// field — it remains as provenance. The Process Tree edge is preserved
	// in f.children so the parent's causal descendants are still discoverable
	// even after the parent is gone (invariant #1 + #7).
	f.mu.Unlock()
	f.snapshots.save(agentID, snap)
	f.record(ctx, a, EventAgentKilled, map[string]any{
		PayloadKeyStopReason: StopCrashed.String(),
	})
	return nil
}

// Recover restores an agent's state from a checkpoint (cognitive
// state can independently survive). The agent must exist and be IDLE or
// SUSPENDED. Recover replaces the agent's cognitive state with the provided
// checkpoint — this is how a new Agent resumes a dead one's cognition
// (invariant #2: Agent disposable, Task durable; a new agent picks up the
// cognitive checkpoint).
//
// Args:
//   - ctx: for the event sink.
//   - agentID: the agent to recover into.
//   - cognitive: the recovered cognitive state.
//
// Returns:
//   - error: ErrAgentNotFound / ErrAgentRetired.
func (f *Fabric) Recover(ctx context.Context, agentID string, cognitive CognitiveState) error {
	f.mu.Lock()
	a, ok := f.agents[agentID]
	if !ok {
		f.mu.Unlock()
		return ErrAgentNotFound
	}
	if a.State == StateRetired {
		f.mu.Unlock()
		return ErrAgentRetired
	}
	a.mu.Lock()
	if cognitive.SchemaVersion == 0 {
		cognitive.SchemaVersion = CognitiveStateSchemaVersion
	}
	a.cognitive = cognitive
	// Recover is the ONLY path that installs a preserved cognitive state, so it is
	// also the only place "this agent is a revived one" becomes true (defect A6).
	// Set under a.mu with the state it describes; the executor reads it when it
	// stamps the quantum payload.
	a.revived = true
	if a.State == StatePaused {
		a.State = StateIdle
	}
	a.mu.Unlock()
	f.mu.Unlock()
	f.record(ctx, a, EventAgentRecovered, map[string]any{
		"has_checkpoint": cognitive.Checkpoint != nil,
		"revived":        true,
	})
	return nil
}

// (SetRunning/SetIdle were removed: the RUNNING/IDLE quantum transitions had
// zero production callers — the scheduler admits candidates via IsIdle and
// the LoadTracker, never via a state flip — so the pair only ever ran in
// tests, and StateRunning/ErrAgentRunning were unreachable from production.)
