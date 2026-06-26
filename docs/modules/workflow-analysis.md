# Workflow Module Performance Analysis

## 1. Module Overview

The workflow module implements a DAG-based workflow execution engine with two execution strategies: a static `Executor` and a `DynamicExecutor` that supports mid-execution graph mutations. It includes human-in-the-loop (HITL) support, retry policies, step recovery, and a pub/sub event system for graph changes.

### Key Files

| File | Purpose |
|------|---------|
| `internal/workflow/engine/executor.go` | Static workflow executor with parallel step scheduling |
| `internal/workflow/engine/dynamic_executor.go` | Dynamic executor with mid-execution graph mutation support |
| `internal/workflow/engine/types.go` | Core types, DAG construction, topological sort |
| `internal/workflow/engine/hitl.go` | Human-in-the-loop interrupt handling and in-memory store |
| `internal/workflow/engine/mutable_dag.go` | Thread-safe mutable DAG with add/remove/replace operations |
| `internal/workflow/engine/graph_events.go` | Pub/sub event hub for graph change notifications |
| `internal/workflow/graph/graph.go` | Higher-level graph with conditional edges and rate limiting |
| `internal/workflow/graph/executor.go` | Graph executor with BFS scheduling |
| `internal/workflow/graph/scheduler.go` | Pluggable scheduler implementations (FIFO, priority, shortest-job) |
| `internal/workflow/graph/node.go` | Node types (AgentNode, ToolNode, FuncNode) |
| `internal/workflow/graph/state.go` | Shared runtime state for graph execution |

### Architecture

The module operates at two abstraction levels:

1. **Engine layer** (`engine/`): Manages step-based workflows with DAG ordering, HITL interrupts, and recovery. The `DynamicExecutor` extends the base `Executor` with mid-flight graph mutation via `MutableDAG`.

2. **Graph layer** (`graph/`): Provides a higher-level abstraction with conditional edge traversal, pluggable schedulers, and rate limiting. Nodes are executed sequentially via BFS.

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Fix |
|----------|----------|---------|-----|
| HIGH | `executor.go:114-176` | Busy-wait loop collects results by counting `len(workflow.Steps)` steps, but `runSteps` may skip or duplicate steps on failure, causing channel reads to block until the safety timeout fires at `DefaultWorkflowTimeout` | Track expected results explicitly or close `resultChan` immediately on first failure |
| HIGH | `executor.go:233-288` | Linear scan of `executionOrder` on every iteration; when a step cannot execute, the scheduler spins through all already-processed steps before finding the blocked one, causing O(n^2) scheduling in worst case | Use a ready-queue (set of steps whose dependencies are met) instead of linear scan |
| HIGH | `executor.go:415-422` | `findStep` performs O(n) linear scan through `workflow.Steps` for every step execution and dependency check. Called multiple times per step from goroutines | Pre-build a `map[string]*Step` index during DAG construction |
| HIGH | `dynamic_executor.go:676-684` | `findStepInDAG` performs O(n) linear scan through `mutableDAG.Steps()` for every step lookup. Called at least once per step in the dispatch loop | Cache the steps map from `mutableDAG.Steps()` or add a `FindStep(id)` method to `MutableDAG` |
| MEDIUM | `executor.go:482-487` | `completed` map is copied under lock on every step execution (`completedCopy`). For large workflows this creates unnecessary allocations per goroutine | Use a concurrent map or snapshot only when template variables reference completed steps |
| MEDIUM | `dynamic_executor.go:143` | `stepEg, _ := errgroup.WithContext(ctx)` discards the derived context. The `errgroup` context is never used for cancellation propagation, meaning step goroutines cannot be cancelled via errgroup | Use `stepEgCtx` for step goroutine cancellation |
| MEDIUM | `dynamic_executor.go:298` | Recovery loop is hard-capped at 5 rounds (`recoveryRound < 5`). If recovery keeps adding steps, the 6th+ recovery is silently dropped | Replace hard cap with a configurable max or loop until no pending recovery |
| MEDIUM | `types.go:267-299` | `GetExecutionOrder` creates a fresh `inDegree` map and re-runs Kahn's algorithm every time. For `DynamicExecutor` with `ApplyImmediate` mode, this runs before every step | Cache the execution order and invalidate on version change (already done in `recomputeOrder`, but the static path re-computes) |
| LOW | `mutable_dag.go:407-432` | `wouldCreateCycle` allocates a new `visited` map on every call. For graphs with many edges, this is called for each dependency during `AddNode` | Reuse a pre-allocated visited map or use a generation counter |
| LOW | `graph/executor.go:68-157` | Graph executor holds `g.mu.RLock()` for the entire execution duration, blocking all mutations for the lifetime of the workflow | Consider a copy-on-read pattern where the graph snapshot is taken once at start |
| LOW | `graph/executor.go:76-81` | Ready queue removal uses linear scan `append(readyQueue[:i], readyQueue[i+1:]...)` for every node selection | Use a proper queue data structure (ring buffer or list) |
| LOW | `graph/executor.go:182-194` | `hasAnySatisfiedEdge` iterates ALL edges in the graph (not just incoming edges to `targetID`) to find incoming edges. O(E) per node | Maintain a reverse edge index `map[string][]*Edge` for incoming edges |

---

## 3. Code Quality Issues

| Severity | Location | Problem | Recommendation |
|----------|----------|---------|----------------|
| HIGH | `executor.go:130-149` | Duplicated error return blocks for `StepStatusFailed` handling. Lines 130-149 and 155-169 contain nearly identical `WorkflowResult` construction | Extract into a helper function `buildFailedResult(execution, workflow, stepResults, err)` |
| HIGH | `dynamic_executor.go:140-141` | `completed` and `processed` maps are created at the top of `ExecuteDynamic` but shared between the collection loop and `runDynamicSteps` via closure, with synchronization only via `mu`. The collection loop (line 218-224) accesses `execution.StepStates` without the lock while `runDynamicSteps` may also read it | Protect `execution.StepStates` with the same mutex or use a separate channel for state updates |
| MEDIUM | `types.go:52-68` | `Step` struct mixes definition metadata (`Name`, `AgentType`, `DependsOn`) with runtime state (`Status`, `Output`, `Error`, `StartedAt`). This blurs the line between workflow definition and execution state | Split into `StepDefinition` and `StepRuntime` |
| MEDIUM | `hitl.go:47-59` | `MemoryInterruptStore` stores nested maps `map[string]map[string]*InterruptPoint` but never bounds the outer map size. Long-running systems accumulate stale execution entries | Add TTL-based cleanup or periodic pruning of old executions |
| MEDIUM | `mutable_dag.go:280-289` | `RemoveEdge` updates `step.DependsOn` by creating a new slice but does not check for duplicate dependencies. If a step has duplicate deps, only one copy is removed | Use a set-based approach for `DependsOn` or deduplicate on construction |
| MEDIUM | `graph_events.go:44` | `graphEventBufferSize = 64` is a magic constant with no documentation on why 64 was chosen or how to tune it | Make configurable or document the rationale |
| LOW | `executor.go:86-88` | `resultChan` is buffered to `len(workflow.Steps)` but `runSteps` may send more results than steps (e.g., recovery replacement steps) causing a channel-full deadlock in edge cases | Use an unbuffered channel with a dedicated collector goroutine |
| LOW | `graph/scheduler.go:108-119` | `ShortJobScheduler.getEstimate` returns 1000ms for unknown nodes. The comment says "reasonable default" but this biases scheduling toward unknown nodes over known long ones | Document the rationale or return `math.MaxInt` to deprioritize unknowns |

---

## 4. Code Snippets: Problems and Proposed Fixes

### Problem 1: O(n) step lookup called in hot path

**`executor.go:415-422`**
```go
func (e *Executor) findStep(steps []*Step, stepID string) *Step {
    for _, step := range steps {
        if step.ID == stepID {
            return step
        }
    }
    return nil
}
```

**Proposed fix:** Build a map index at workflow construction time.
```go
type stepIndex map[string]*Step

func buildStepIndex(steps []*Step) stepIndex {
    idx := make(stepIndex, len(steps))
    for _, s := range steps {
        idx[s.ID] = s
    }
    return idx
}
```

### Problem 2: Linear scan scheduling loop

**`executor.go:233-288`** - The scheduler iterates `executionOrder` linearly, skipping already-processed steps.

**Proposed fix:** Maintain a ready set that is updated when dependencies complete.
```go
ready := make(map[string]bool)
for _, sid := range executionOrder {
    if e.canExecute(stepIndex[sid], completed) {
        ready[sid] = true
    }
}
// When a step completes, check its dependents and add to ready
for _, dependent := range dag.Edges[completedStepID] {
    if e.canExecute(stepIndex[dependent], completed) {
        ready[dependent] = true
    }
}
```

### Problem 3: Discarded errgroup context

**`dynamic_executor.go:143`**
```go
stepEg, _ := errgroup.WithContext(ctx)
```

**Proposed fix:** Use the derived context for step cancellation.
```go
stepEg, stepEgCtx := errgroup.WithContext(ctx)
// Pass stepEgCtx to executeStepCore instead of ctx
```

### Problem 4: hasAnySatisfiedEdge scans all edges

**`graph/executor.go:182-194`**
```go
func hasAnySatisfiedEdge(g *Graph, targetID string, state *State) bool {
    for _, edges := range g.edges {
        for _, edge := range edges {
            if edge.to == targetID {
                if edge.cond == nil || edge.cond(state) {
                    return true
                }
            }
        }
    }
    return false
}
```

**Proposed fix:** Maintain a reverse edge index.
```go
// Build during graph construction
incomingEdges := make(map[string][]*Edge)
for _, edges := range g.edges {
    for _, edge := range edges {
        incomingEdges[edge.to] = append(incomingEdges[edge.to], edge)
    }
}
```

---

## 5. Priority Action Items

1. **[✓] [P0 - Correctness]** Fix `resultChan` sizing — doubled buffer to `len(workflow.Steps)*2` to prevent deadlock when recovery adds replacement nodes.

2. **[✓] [P0 - Performance]** Replace `findStep` linear scan with pre-built `stepsByID` map index. `runSteps` builds the map once and passes `*Step` directly to `executeStep`, eliminating O(n^2) step resolution.

3. **[P1 - Performance]** Refactor the scheduler loop in `runSteps` to use a dependency-ready set instead of linear scanning `executionOrder`. The current approach is O(n^2) in the worst case for workflows with many parallel branches.

4. **[P1 - Performance]** Fix `hasAnySatisfiedEdge` in `graph/executor.go` to use a reverse edge index. Currently O(E) per node evaluation; should be O(in_degree).

5. **[P2 - Code Quality]** Extract duplicated error handling blocks in `executor.go:130-169` into a shared helper.

6. **[P2 - Code Quality]** Split `Step` struct into definition and runtime types to prevent accidental mutation of workflow definitions during execution.

7. **[P3 - Performance]** Make `graphEventBufferSize` configurable and document the tuning rationale.

8. **[P3 - Robustness]** Replace the hard-coded recovery round cap of 5 in `dynamic_executor.go:298` with a configurable limit.
