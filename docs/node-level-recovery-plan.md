# Node-Level Recovery Plan

## Goal

Step failure should not always fail the whole workflow.

The engine should be able to use `MutableDAG` to insert a recovery step and continue execution:

```text
Step failed != Workflow failed
```

This is an engine-level capability. Do not put recovery policy logic into the low-level `workflow/graph` package.

## Scope

- Keep `workflow/graph` as pure DAG, conditional edge, and scheduler logic.
- Add recovery orchestration in `workflow/engine`.
- Preserve failed nodes for audit; do not delete them.
- Use existing `MutableDAG` versioning and `GraphEventHub`.
- Record recovery as events for replay and debugging.

## Plan

### 1. Add Recovery Policy

- Add `RecoveryPolicy` to `workflow/engine`.
- Attach it to `Step`.
- First version only supports:
  - `retry`
  - `replace_node`
  - `fail_fast`

Example:

```go
type RecoveryStrategy string

const (
    RecoveryRetry       RecoveryStrategy = "retry"
    RecoveryReplaceNode RecoveryStrategy = "replace_node"
    RecoveryFailFast    RecoveryStrategy = "fail_fast"
)

type RecoveryPolicy struct {
    Strategy         RecoveryStrategy
    MaxAttempts      int
    ReplacementAgent string
}
```

### 2. Intercept Step Failure

- Change `DynamicExecutor` failure handling.
- Current behavior:
  - step failed -> workflow failed
- New behavior:
  - step failed -> check `Step.RecoveryPolicy`
  - no policy -> keep current fail-fast behavior
  - policy exists -> call recovery handler

### 3. Add Recovery Handler

- Add a small engine-level interface:

```go
type StepRecoveryHandler interface {
    RecoverStep(ctx context.Context, failure StepFailure, dag *MutableDAG) (*RecoveryDecision, error)
}
```

- `StepFailure` should include:
  - `execution_id`
  - `workflow_id`
  - `step_id`
  - `error`
  - `input`
  - upstream outputs if available

- `RecoveryDecision` should support:
  - retry same step
  - insert replacement step
  - fail workflow

### 4. Insert Replacement Node

For `replace_node`:

1. Create replacement step.
2. Add it with `MutableDAG.AddNode`.
3. Connect original upstream nodes to replacement.
4. Connect replacement to original downstream nodes.
5. Keep failed node in graph/result for audit.
6. Recompute execution order through existing `DynamicExecutor.recomputeOrder`.

Target shape:

```text
before:
plan -> tool_a -> analyze

after:
plan -> tool_a_failed
plan -> tool_a_recovery -> analyze
```

### 5. Record Events

Add event types:

```text
step.failed
step.recovery.started
step.recovery.completed
step.recovery.failed
```

Payload should include:

- `execution_id`
- `workflow_id`
- `failed_step_id`
- `replacement_step_id`
- `strategy`
- `graph_version_before`
- `graph_version_after`
- `error`

### 6. First-Version Limits

- Do not auto-select replacement agents with LLM.
- Do not migrate live process state across agents.
- Do not remove failed nodes.
- Do not modify `workflow/graph`.
- Do not add a new runtime manager recovery path yet.

### 7. Tests

Add focused tests:

- A failed step with no policy still fails the workflow.
- A failed step with `replace_node` inserts replacement step.
- Replacement step can continue to downstream steps.
- Failed step remains visible in results.
- `MutableDAG.Version()` increases after recovery mutation.
- Recovery events are emitted in order.

## Implementation Order

1. Add types: `RecoveryPolicy`, `RecoveryStrategy`, `StepFailure`, `RecoveryDecision`.
2. Add `RecoveryPolicy` to `Step`.
3. Add optional `StepRecoveryHandler` to `DynamicExecutor`.
4. Intercept failed `StepResult` before returning workflow failure.
5. Implement `replace_node` mutation with `MutableDAG` (see details below).
6. Add recovery event types and emit events.
7. Add tests.

---

## ReplaceNode Implementation Detail

### ChangeType / GraphChange

File: `graph_events.go`

Add to `ChangeType` iota:
```go
ChangeReplaceNode
```

Add `OldNodeID` field to `GraphChange`:
```go
type GraphChange struct {
    Type      ChangeType
    NodeID    string
    OldNodeID string    // populated for ReplaceNode
    Step      *Step
    Error     string
    Timestamp time.Time
}
```

### MutableDAG.ReplaceNode

File: `mutable_dag.go`

```go
// ReplaceNode atomically replaces the node identified by oldID with newStep,
// migrating all incoming and outgoing edges to the new node.
//
// Behavior depends on whether the ID changes:
//   - Same ID (newStep.ID == oldID): in-place update, new DependsOn edges are added.
//   - Different ID: all incoming edges are redirected to newStep.ID, all outgoing
//     edges are moved from oldID to newStep.ID, new DependsOn edges are added,
//     then the old node is removed.
//
// Cycle detection is performed on a simulated adjacency list of the post-replacement
// graph before any mutation is applied, so the operation is atomic with respect to
// consistency — no rollback logic is needed.
func (m *MutableDAG) ReplaceNode(ctx context.Context, oldID string, newStep *Step) error
```

**Validation order** (same as existing methods):
1. `ctx.Err()` — context cancelled
2. `newStep == nil` → error
3. `newStep.ID == ""` → error
4. `m.dag.Nodes[oldID]` not found → `ErrNodeNotFound`
5. `newStep.ID != oldID` and `m.dag.Nodes[newStep.ID]` exists → `ErrDuplicateID`
6. `newStep.DependsOn` references node not in `m.dag.Nodes` → `ErrInvalidDependency`
7. `dep == newStep.ID` in DependsOn → `ErrCycleDetected` (self-loop)

**Cycle detection approach** — adjacency list simulation:

```go
// 1. Build adjacency list representing the post-replacement graph.
adjList := make(map[string][]string)

// Add all existing nodes, skipping the old node if it will be removed.
for nodeID := range m.dag.Nodes {
    if nodeID == oldID && newStep.ID != oldID {
        continue
    }
    adjList[nodeID] = nil
}
if newStep.ID != oldID {
    adjList[newStep.ID] = nil
}

// Copy existing edges with ID redirection.
for src, targets := range m.dag.Edges {
    effSrc := src
    if src == oldID && newStep.ID != oldID {
        effSrc = newStep.ID
    }
    if _, ok := adjList[effSrc]; !ok {
        continue
    }
    for _, t := range targets {
        effTgt := t
        if t == oldID && newStep.ID != oldID {
            effTgt = newStep.ID
        }
        if _, ok := adjList[effTgt]; !ok {
            continue
        }
        adjList[effSrc] = append(adjList[effSrc], effTgt)
    }
}

// Add new DependsOn edges.
for _, dep := range newStep.DependsOn {
    adjList[dep] = append(adjList[dep], newStep.ID)
}

// 2. Run DFS cycle detection on adjList.
if hasCycleInAdjList(adjList) {
    return ErrCycleDetected
}
```

**Mutation** (after cycle check passes):

- Different ID:
  1. Add `m.dag.Nodes[newStep.ID] = &DAGNode{StepID: newStep.ID}`
  2. Move outgoing edges: `m.dag.Edges[newStep.ID] = m.dag.Edges[oldID]`, delete `m.dag.Edges[oldID]`
  3. Redirect incoming edges: scan all `m.dag.Edges[src]`, replace `oldID` with `newStep.ID`
  4. Delete `m.dag.Nodes[oldID]`, `m.steps[oldID]`
  5. Set `m.steps[newStep.ID] = newStep`

- Same ID:
  1. Set `m.steps[oldID] = newStep`
  2. (DependsOn edges already added to adjList and applied to graph)

Final step: `m.recalculateDegrees()`, `m.version++`, `m.hub.Publish(...)`.

### Helpers

```go
// hasCycleInAdjList returns true if the directed graph represented by
// the adjacency list contains a cycle. Uses DFS with three-color marking.
func hasCycleInAdjList(adjList map[string][]string) bool

// recalculateDegrees recomputes InDegree/OutDegree for all DAGNodes from
// the current Edges map. Called after structural mutations that affect
// multiple edges at once.
func (m *MutableDAG) recalculateDegrees()
```

### Edge Semantics Summary

| Scenario | Old Incoming | Old Outgoing | New DependsOn |
|---|---|---|---|
| Same ID | preserved as-is | preserved as-is | added |
| Different ID | redirected to new ID | migrated to new ID | added |

### Test Plan

All tests in `mutable_dag_test.go`, using the existing `makeStep` helper. Group with `// =====================================================` separators.

| Test | What it verifies |
|---|---|
| `TestReplaceNode_SameID` | In-place update, step reference replaced, DependsOn added |
| `TestReplaceNode_SameID_EdgeCount` | Edges correctly updated after same-ID replacement |
| `TestReplaceNode_DifferentID` | Full migration, node removed, edges redirected |
| `TestReplaceNode_DifferentID_EdgeCount` | Incoming + outgoing edge count correct after migration |
| `TestReplaceNode_NilStep` | `nil` → error |
| `TestReplaceNode_EmptyID` | `""` → error |
| `TestReplaceNode_NodeNotFound` | Bogus oldID → `ErrNodeNotFound` |
| `TestReplaceNode_DuplicateID` | newID conflicts with existing node → `ErrDuplicateID` |
| `TestReplaceNode_InvalidDependency` | DependsOn references nonexistent node → `ErrInvalidDependency` |
| `TestReplaceNode_SelfLoopDep` | DependsOn has `newStep.ID` → `ErrCycleDetected` |
| `TestReplaceNode_CycleViaOutgoing` | Migrated outgoing edge creates indirect cycle → `ErrCycleDetected` |
| `TestReplaceNode_ExecutionOrder` | After replacement, GetExecutionOrder is valid |
| `TestReplaceNode_VersionIncrements` | Version increments once per replace |
| `TestReplaceNode_EventPublished` | Subscribe channel receives `ChangeReplaceNode` with correct `OldNodeID` |
| `TestReplaceNode_CancelledContext` | Cancelled ctx → error, no mutation |
| `TestReplaceNode_Concurrent` | Parallel ReplaceNode + reads, `-race` clean |
