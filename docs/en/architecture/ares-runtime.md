# ARES Runtime Design (0.3.0 Kernel)

> Status: design document (frozen). This is the authoritative model for Task
> Fabric / Agent Fabric / Scheduler.
> Positioning: ARES evolves from an "Agent Orchestration Framework" (leader+sub)
> into a **dynamic compute runtime for agents** — Agents are not orchestrated.
> They are scheduled.

## 1. Core Theses

| Object | One-line definition | Durability |
|--------|---------------------|------------|
| **Task** | durable intent (holds capability/state/lease/checkpoint) | durable |
| **Agent** | disposable execution (holds context/tools/skills/experience) | disposable |
| **Checkpoint** | durable progress (resume point) | durable |
| **Experience** | durable learning (skill-relevance prior) | durable |
| **Event Stream** | durable history (full state rebuildable) | durable |
| **Runtime** | the life system organizing the objects above | — |

**Agent death ≠ Task death.** Agents are disposable; Tasks/State/Evidence are durable.

## 2. Three Responsibility Separations (replacing the leader's mixed WHAT+WHEN+WHO+HOW)

```
Planner / Cognitive Compiler   = WHAT (produces a Task Graph)
Runtime Scheduler              = WHEN / WHO (who runs when, under what resource constraints)
Agent                          = HOW (executes)
Evolution                      = BETTER (improves graph / scheduling policy / agent population)
```

The leader is no longer a role, only one **Execution Strategy / Policy** among:
`Peer Scheduling / Work Stealing / Priority / Capability / Cooperative Preemption / DAG Scheduling`
— all of these are Scheduler policies, not architecture.

## 3. Core Object Model

### Task (durable intent)

```go
type Task struct {
    ID           string            // stable ID
    Capability   string            // required capability (e.g. "rust/unsafe-analysis")
    State        TaskState         // READY / LEASED / RUNNING / SUSPENDED / COMPLETED / FAILED
    Priority     int               // preemption decision basis
    Owner        string            // current logical executor ("" = none) — Lease is proof of holding, not the holding itself
    Lease        *Lease            // TaskLease (TTL lease = validity proof of ownership; Epoch is the fencing token)
    Checkpoint   *Checkpoint       // durable progress
    Dependencies []string          // DAG prerequisites (is_ready check)
    Resources    map[string]any    // resource requirements
    Deadline     time.Time
    RetryPolicy  RetryPolicy       // retry / invalidation policy
    // Executions is a reserved boundary for the long-term model: Task → Executions[]
    // (Execution #1/#2/#3, each bound to one Agent). retry / handoff / stealing /
    // preemption / crash recovery / perf metrics hang on the Execution layer.
    Executions []any               // reserved: []*Execution
}
```

**Owner ≠ Lease (revision 1)**: Owner is the "current logical executor"; Lease is
the "proof that the Owner temporarily holds this Task". Lease expiry only
invalidates the ownership proof (the Task returns to READY and can be
re-acquired), it does not change who is logically executing. Every
ownership-carrying operation must carry a **leaseEpoch (fencing token)** check,
preventing the classic "A's lease expired → B acquires → A late Release/Complete"
from wrongly releasing B's task.

### Agent (disposable execution)

```go
type Agent struct {
    Identity     string            // stable identifier
    Capabilities []string          // declared capabilities
    State        AgentState        // IDLE / RUNNING / SUSPENDED / RETIRED
    Load         float64           // current load (scheduler input)
    Confidence   float64           // experience confidence (from Experience)
    Context      any               // conversation context
    Tools        []string          // registered tools
    Skills       []string          // activated skills
}
```

### Resource (first-class capability / lease / context)

TaskLease / ResourceLease / CapabilityLease — all abstracted from the existing
`SessionLease` (internal/agents/lease): `Acquire(id, owner, ttl) / Renew /
Release`, auto-invalidated on TTL expiry.

## 4. Task State Machine (Cooperative, not hard preemption)

```
READY ──acquire()──▶ LEASED ──start()──▶ RUNNING
                                          │
                    ┌─────────────────────┤
                    ▼                     ▼
                SUSPENDED ◀──yield/checkpoint── RUNNING
                    │                     │
                    │ resume/preempt      ├─complete()──▶ COMPLETED
                    ▼                     ├─fail()──────▶ FAILED
                 LEASED                   └─preempt()──▶ READY (lease released, checkpoint kept)
```

Key decision: **Cooperative Preemption**, not OS hard preemption. An LLM Agent
cannot be interrupted at an arbitrary instruction (inference/tool call/shell
cannot stop mid-flight) — switching happens only at **Quantum boundaries**
(yield/checkpoint). This matches the work-stealing reality of actor runtimes.

**Yield is an execution boundary, not a state transition (revision 2)**:
`yield()` merely hands execution back to the Runtime; the actual next state is
decided by the Scheduler:

```
RUNNING ──yield()──▶ [Scheduler decision]
                        ├─ continue ──▶ RUNNING
                        ├─ suspend  ──▶ SUSPENDED (checkpoint kept)
                        ├─ preempt  ──▶ READY (lease released, checkpoint kept, re-acquirable)
                        ├─ handoff  ──▶ READY (same as preempt, handoff semantics)
                        └─ complete ──▶ COMPLETED
```

Otherwise every quantum would cause a bizarre RUNNING→SUSPENDED→LEASED→RUNNING cycle.

## 5. Execution Quantum (don't run Task→Agent in one shot)

```
Task ──▶ Execution Quantum ──▶ Agent Step (reasoning → tool call → observation)
                                    │
                              checkpoint (durable)
                                    │
                              yield() ──▶ Runtime Scheduler
                                    │
                              continue / handoff / suspend / split / cancel
```

At the end of every quantum the Agent `yield()`s to the Runtime, which decides
the next step.

## 6. Four Core Primitives (Runtime foundation)

```go
Acquire(taskID, agentID, ttl) (epoch, error) // CAS owner=agentID, READY→LEASED; returns fencing token (epoch)
Release(taskID, agentID, epoch) error         // return, LEASED/RUNNING→READY; epoch check prevents "A expired→B acquire→A late Release" killing B
Yield(taskID, agentID, epoch, checkpoint)     // Quantum boundary: hands back execution; state decided by Scheduler (continue/suspend/preempt/handoff/complete)
Checkpoint(taskID, state) error               // durable progress persist/event
// Extended primitives:
Steal(taskID, fromAgent) error                // capability-aware work stealing
Preempt(taskID, reason) error                 // high-priority preemption (cooperative)
```

Every ownership-carrying operation (Release/Yield/Complete/Fail/Preempt) must
carry a **leaseEpoch**: the operation takes effect only when
`task.Lease.Epoch == passed epoch` and the owner matches — the epoch is the
ownership fencing token, preventing stale holders' late operations.

Agents only express: `I have capability X / I am idle / I apply for Y / I run Y /
I release Y`. They don't know "who the leader is".

## 7. Event Upgrade (full state rebuildable)

The existing `EventSubTaskScheduled / EventSubTaskResult` upgrade to full
lifecycle events:

```
TaskCreated / TaskReady / TaskAcquired / TaskStarted / TaskYielded /
TaskCheckpointed / TaskPreempted / TaskReleased / TaskCompleted /
TaskFailed / TaskExpired / TaskStolen
```

The Event Stream is the single source (SEDA) to rebuild: Scheduler State /
Task State / Agent State / Lease State / Action Audit — continuing the
Evidence-Driven route.

## 8. Capability-aware Work Stealing (wired with Skill-First)

Traditional work stealing: `who is idle?` → steal.
ARES: `who is the best executor for this task?` → steal + capability-matching score:

```
score = capability_overlap × (1 - load) × confidence
Agent C (rust+llvm, load 0.4, conf 0.97) → steal Task(capability=rust) → 0.96
```

The Capability Fabric (SkillCatalog / Experience / skill_activate) directly
becomes the capability/experience source for scoring — the Skill-first design
is actually used.

## 9. DAG as the Scheduling Source (no leader dispatch needed)

```
Task A completed ──▶ B ready / C ready ──▶ Scheduler
                                              ├── Agent X acquires B
                                              └── Agent Y acquires C
B ─┐
   ├─▶ D READY
C ─┘
```

The Scheduler only asks `is_ready(task)` (dependencies satisfied + resources
available); the topology is driven by MutableDAG / Evolution, with no
master/slave dispatch.

## 10. Mapping to Existing Building Blocks (don't rebuild from scratch)

| Existing asset | Runtime role |
|----------------|--------------|
| Event Stream (ares_events) | durable history + state rebuild (event type upgrade) |
| MutableDAG / live DAG | Task Graph (dependency topology is the scheduling source) |
| Agent Registry (peer) | Agent Fabric discovery (spawn/suspend/resume/retire/clone) |
| SessionLease (agents/lease) | abstract to generic Leases (TaskLease/ResourceLease/CapabilityLease) |
| Capability Fabric / Experience | capability-aware scheduling score source |
| skill_activate / trust gating | Agent capability assembly (bind skills at spawn) |
| ActionLog | execution fact audit (Task execution records) |
| Chaos (ares_runtime arena) | **Failure Injection + Recovery Verification** (deliberately kill, verify the Runtime survives); lease expiry / requeue / checkpoint recovery / agent restart are **Runtime Recovery** (independent responsibility, not Chaos) |
| Evolution | modify Task Graph / scheduling policy / Agent population (spawn/retire/clone) |

## 11. Core Model Revision (Kernel Model)

> Revision: the Planner is no longer a central component of the Runtime. The
> Runtime converges to the **ARES Kernel** — it does not think for Agents; it
> only lets Agents safely think, communicate, create child processes, compete
> for resources, be scheduled, die, and recover. Agents are **peer cognitive
> processes** (A ≡ B ≡ C); parent/child is only spawn provenance, not a
> privilege hierarchy.
>
> **Agent decides; Kernel enforces.**

### The Kernel's Three Pillars

```
              ARES KERNEL
                    │
    ┌───────────────┼───────────────┐
    │               │               │
Scheduler          IPC          Lifecycle
    │               │               │
Acquire Steal  Send Request   Spawn Suspend
Lease  Preempt  Reply Handoff  Resume Kill
                 Delegate Subscribe    Recover
```

The Kernel handles "can it, how does it live, how does it run"; the Agent
handles "what do I do, do I spawn children, how do I collaborate". The Runtime
(Kernel) only schedules and manages lifecycle; Agents do the task processing.

### Agent = Peer Cognitive Process

- Each Agent independently holds its **Cognitive State** (Context / Observation /
  Working Memory / Decision / Tool State / Checkpoint) — the Runtime never
  depends on hidden chain-of-thought, only on checkpointable state.
- **spawn establishes provenance, not hierarchy**: A creating B only records
  lifecycle provenance; after creation A ≡ B (peers), free to communicate and
  compete for tasks. The parent/child relation is semantic, not a life
  dependency (Parent dies, Child lives; the Task is reclaimed and re-dispatched
  by the Runtime).
- **Process Tree ≠ Scheduling Graph**: the spawn causality tree (Lifecycle's
  view) and the Task dependency graph (Scheduler's view) coexist without
  merging — parent/child never forms a new leader/sub power structure.

### Three-Layer Context (no shared brain)

| Layer | Content |
|-------|---------|
| Task Shared State | task goal / constraints / artifacts / decisions / dependencies / checkpoints (objective, must be commonly known) |
| Agent Private State | working context / observations / hypotheses / tool history / scratchpad (per-Agent independent) |
| IPC Messages | "I found X" / "help me verify Y" / "your conclusion conflicts with mine" (Send / Request / Reply / Delegate / Handoff) |

### spawn is a syscall, not an orchestration API

```go
type SpawnSpec struct {
    Task         TaskSpec
    Capabilities []string
    Context      ContextSpec // parent's snapshot / selected projection
    Resources    ResourceSpec
}
Agent.Spawn(spec) // Kernel validates quota/capability/resource/policy, then creates Agent+Task+parent-child relation
```

The Planner degrades to an **Agent cognitive capability** (optional), no longer
a central Runtime component — consistent with Skill-first / Capability Fabric.

### Revised Core Definition

> ARES is a runtime where autonomous agents independently maintain cognition,
> communicate as peers, and cooperatively execute durable tasks under
> kernel-level scheduling and recovery.

### Architecture Invariants (must not be broken casually)

1. Agents are peer cognitive processes — A ≡ B ≡ C; parent/child have only
   **spawn provenance**, no permission hierarchy.
2. Tasks are durable, Agents are disposable — **Agent death ≠ Task death**.
3. The Kernel does not think — **Agent decides; Kernel enforces**.
4. The Scheduler decides "who / when / under what constraints"; the Agent
   decides "what to do / whether to spawn / how to collaborate".
5. Every Agent has independent Cognitive State — no shared "brain".
6. Three-layer Context separation — Task Shared State / Agent Private State /
   IPC Messages.
7. **Process Tree ≠ Scheduling Graph** — the spawn causality tree and the Task
   dependency graph coexist without merging.
8. spawn is a syscall, not an orchestration API.
9. Preemption is cooperative — no fake OS hard preemption.
10. No premature design — Auction / distributed Scheduler / full Actor /
    Execution entity remain deferred.
    **v0.4.0 revision (2026-08-17)**: the following items move from "deferred"
    to "scheduled": multi-agent collaboration patterns (delegation / pipeline /
    orchestration), evolution-driven spawn decisions (auto spawn/clone policy),
    evolution-driven resource allocation (complex resource scheduling), and
    evolution-driven IPC protocol (message format / compression). See
    "12. v0.4.0 Advanced Feature Roadmap" below.

### Task decomposition = Agent cognition

> Task decomposition is an Agent responsibility, not a Runtime responsibility.
> Agent may decide that a Task exceeds its effective execution scope and invoke
> spawn to create additional Tasks/Agents. The Kernel does not plan, decompose,
> or coordinate semantic work; it only validates and schedules the resulting
> execution entities.

Task decomposition belongs to the Agent's cognitive responsibility, not the
Runtime's scheduling responsibility. The Agent decides whether to split a task
based on its complexity, its own capability, and the current context, using
spawn to create new Tasks/Agents. The Kernel does not understand task
semantics or decompose tasks — it only validates spawn requests, schedules the
resulting Tasks, and provides IPC and lifecycle management. **The Runtime does
not split tasks; Agents split tasks.**

Agents must not directly manipulate the Scheduler (no `agent.scheduler.
Schedule(...)` / `agent.scheduler.Preempt(...)`); they only express intent
(`agent.Spawn / Send / Request / Yield`) and the Kernel decides execution —
**Agent decides; Kernel enforces**.

**Core theorem: Agents decide the work. Kernel schedules the work.**
(Agents decide what work should exist and how it should be solved; the Kernel
decides when, where, and under what constraints that work executes.)

### SUSPENDED Semantics Locked (avoid concept confusion)

`RunQuantum`'s SUSPENDED is explicitly understood as: **this Agent's execution
quantum has ended, but the Task's durable intent is not yet complete** — not
"the Agent was paused".

Three concepts do not mix:
- **Task suspended**: durable intent incomplete, Task state SUSPENDED
  (checkpoint kept, re-acquirable by others)
- **Agent suspended**: Agent lifecycle state (Lifecycle pillar)
- **Execution yielded**: this quantum ended and handed execution back
  (execution boundary; the Scheduler decides the next state)

## 12. v0.4.0 Advanced Feature Roadmap (decided 2026-08-17)

> The core Runtime (P0-P5 + production wiring) is complete. v0.4.0 focuses on
> advanced features built on the three pillars (Scheduler / IPC / Lifecycle)
> without changing the core invariants (§11). The full roadmap and landing
> plan live in `analysis-reports/v0.4.0-feature-suggestions-corrected.md`.

### Priority matrix

| Track | Difficulty | Value | Priority | Status |
|-------|-----------|-------|----------|--------|
| M1 Multi-agent collaboration | Medium | High | ⭐⭐⭐ P2 required | ✅ implemented (agentipc/collaboration.go) |
| M2 Evolution-Runtime deep integration | Medium-high | High | ⭐⭐⭐ P2 required | 🔄 in progress (M2-1 implemented) |
| M3 Explainability & human feedback | Medium | High | ⭐⭐⭐ P2 recommended | ⏳ pending |
| M4 Global observability & debugging | Low | Medium | ⭐⭐ P3 optional | ⏳ pending |

### M1 Multi-agent collaboration (P2 required) — realizing the "peer cognitive process" vision

A **composition layer** over the IPC primitives (Send/Request/Reply/Delegate/
Handoff/Subscribe); no central orchestration is introduced (the Coordinator is
an Agent-level coordinator, not a Kernel scheduler):
- **Delegation**: Leader → Specialist task handoff with result return
  (`DelegateToSpecialist`)
- **Pipeline**: A → B → C ordered execution, data flows via IPC (`Pipeline`)
- **Orchestration**: a Coordinator fans work out to multiple Workers in
  parallel with failure retry (`Orchestrate`)

### M2 Evolution-Runtime deep integration (P2 required)

Evolution expands from "influencing strategy parameters only" to runtime
decision dimensions (**Evolution decides; Kernel enforces**):
- **M2-1 spawn decisions**: spawn timing / quantity (population cap) /
  capability-type preference
  (`aresrecovery.EvolutionAwareSpawner` + `SpawnPolicySource` consumer interface)
- **M2-2 resource allocation**: CPU/memory quota weight dynamic adjustment
  (quota derived from the active strategy parameters)
- **M2-3 IPC protocol**: message format / compression-rate optimization
  (strategy-driven encoding choice)

### M3 Explainability & human feedback (P2 recommended)

- Evolution trajectory visualization (Dashboard: best-strategy path /
  breakthrough changes / regressions)
- Human feedback API (`POST /api/evolution/feedback`: rating + approval +
  attribution)
- Change attribution analysis (impact estimate per change)

### M4 Global observability & debugging (P3 optional)

- Cross-Fabric tracing (Task / Agent / Message spans)
- Simulation sandbox (Replay historical events to verify recovery logic +
  Simulate future scenarios)
- Performance benchmarks (collaboration / tracing / sandbox)

### Relation to the "no premature design" list (§11 invariant #10 revision)

Promoted from deferred to scheduled (2026-08-17): multi-agent collaboration,
auto spawn/clone policy, complex resource allocation (quota weights), new
message format (IPC compression). Still deferred: Auction/bidding, Agent
migration, distributed/multi-level Scheduler, full Actor model, Execution
entity, new database.
