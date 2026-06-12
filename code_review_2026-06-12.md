## Local Review for **uncommitted changes against master**

### Summary

This diff introduces two substantial new modules: a **Dynamic Workflow engine** (`internal/workflow/engine/`, `internal/workflow/graph/`) with runtime DAG reordering, human-in-the-loop (HITL), and graph events; and the **Runtime / Agent Resurrection** layer (`internal/runtime/`, `internal/events/`, `internal/agents/leader/`) managing agent lifecycle, event-sourced recovery, and a new `StatefulAgent` interface. The implementations are largely complete and compile cleanly, but there are high-confidence architecture-level risks that should be resolved before merging, particularly around competing failover owners, duplicated recovery logic, and race conditions in concurrent shutdown/reordering paths.

---

### Issues Found

| Severity | File:Line | Issue |
|----------|-----------|-------|
| CRITICAL | `manager.go:740-788` + `supervisor.go:116-171` | Runtime `healthCheck` and `LeaderSupervisor` both detect and resurrect the same leader with no single owner |
| WARNING | `resurrection.go:92-148, 296-444` | `plugins/resurrection/` is a parallel, competing mechanism; it only calls `RestoreState`, never `ReplayEvents` |
| WARNING | `event_recovery.go:54-115`, `manager.go:661-675`, `resurrection.go:326-344` | Event-recovery state extraction is implemented three times with divergent semantics |
| WARNING | `supervisor.go:116` vs `supervisor.go:135-151` | `LeaderSupervisor.Start()` registers AHP callback but `Stop()` never unregisters; long-running systems accumulate stale callbacks |
| WARNING | `pg_store.go:367-456` | `Subscribe` polls with hard `LIMIT 100`, silently dropping events under burst load |
| WARNING | `dynamic_executor.go:320-392, 452-455` | `ApplyAtCheckpoint` reorder may permanently stall waiters after reordering |
| WARNING | `dynamic_executor.go:168-226` | Result-collector loop splits into "expected-met" vs "drain" phases; can permanently leak a goroutine |
| WARNING | `mutable_dag.go:321-335` | `SnapshotWithSteps` returns aliased mutable `*Step` pointers; executor can observe torn `DependsOn` |
| WARNING | `graph_events.go:85-96` | `GraphEventHub.Publish` uses `select default` and silently drops events on full subscriber buffers |
| WARNING | `hitl.go:76-79` | `MemoryInterruptStore.Save` overwrites prior point for same `stepID`; prior pending point becomes untrackable |
| SUGGESTION | `events/store.go:10-30` | `Store` interface exposes `StreamVersion` and `expectedVersion`; leaks storage implementation details into runtime |
| SUGGESTION | `dynamic_executor.go:476-493` | `runDynamicSteps` sends `ErrWorkflowIncomplete` after all goroutines exited, producing spurious workflow failures |
| SUGGESTION | `dynamic_executor.go:410-462` | `NotifyAgentDead` high-frequency crash fan-out has no per-agent dedup; can spawn unbounded `RestoreAgent` goroutines |
| SUGGESTION | `runtime_test.go:247` | `RestartAgent` no longer increments `totalRestarts`; manual restarts are invisible to `Stats()` |
| SUGGESTION | `integration/runtime_test.go:124` | Integration tests pre-populate events under `agent:<id>` but `replayEvents` reads `<id>`; silently skipping replay |

---

## Detailed Findings

### CRITICAL

**File:** `internal/runtime/manager.go:740-788` / `internal/agents/leader/supervisor.go:116-171`  
**Confidence:** High

`LeaderSupervisor.Start()` registers `handleFailover` as an AHP heartbeat callback. Independently, Runtime's `healthCheck` also calls `NotifyAgentDead` for any offline or heartbeat-failed agent. Both paths then race to `Stop()` the dead leader and create a replacement — the Runtime via `RestoreAgent`, the Supervisor via `doFailover → HandleFailover`. There is zero coordination, so under a single leader crash both systems may create competing new instances.

**Suggestion:** Establish a single owner for leader recovery. Either the Runtime should skip leaders when `LeaderSupervisor` is active, or the Supervisor should mediate through a shared `Runtime` instance rather than maintaining its own factory.

---

### WARNING

**File:** `internal/plugins/resurrection/resurrection.go:92-148, 296-444`  
**Confidence:** High

The standalone `Supervisor` maintains its own agent map, its own heartbeat probe loop (`sendHeartbeats` + `health.CheckHealth`), its own `replayEvents`, and its own factory-driven replacement (`resurrect`). It shares the same `HeartbeatMonitor` as the Runtime. If both are wired for the same agent, they independently detect failure and each calls the factory to build a replacement — duplicating work, splitting event-replay side effects, and racing on ownership.

**Suggestion:** Either deprecate `plugins/resurrection/` in favor of the Runtime, or make it a thin adapter that delegates entirely to the Runtime rather than re-implementing lifecycle management.

---

**File:** `internal/agents/leader/event_recovery.go:54-115`, `internal/runtime/manager.go:661-675`, `internal/plugins/resurrection/resurrection.go:326-344`  
**Confidence:** High

`buildStateFromEvents` (runtime) handles one event type (`EventSessionCreated`). `EventRecovery.RecoverFromEvents` (leader) handles four event types and computes `PendingTasks` and `LastFailover`. The resurrection plugin handles only `EventSessionCreated`. If a failure triggers event-based recovery via the resurrection plugin instead of `EventRecovery`, state like pending tasks and last-failover timestamp is silently lost because none of the other implementations would populate it.

**Suggestion:** Introduce a single package-level function (e.g. `events.ExtractRecoveryState`) shared by all three call sites so state-extraction semantics are unified.

---

**File:** `internal/agents/leader/supervisor.go:116` vs `internal/agents/leader/supervisor.go:135-151`  
**Confidence:** High

`Start()` calls `s.heartbeatMon.RegisterCallback(s.handleFailover)` every time. `Stop()` cancels its own context and waits for goroutines, but does not call a counterpart `UnregisterCallback`. In a long-running system that cycles supervisors, the shared `HeartbeatMonitor` retains stale callbacks referencing dead supervisors; `CheckTimeouts` (heartbeat.go) will then fan out into no-ops or panics when the callback reads from a cancelled `gctx`.

**Suggestion:** Add an `UnregisterCallback` method to `HeartbeatMonitor`, and call it from `LeaderSupervisor.Stop()` before the context is cancelled.

---

**File:** `internal/events/pg_store.go:367-456`  
**Confidence:** Medium

Each poll runs every 1 second and caps the result set at 100 rows. If event throughput exceeds 100 events/second for a given subscriber filter, events between the cursor and the 100-row ceiling are never delivered. The subscriber has no signal that it has fallen behind, so state reconstruction (e.g. `ReplayEvents`) starts from an arbitrarily-truncated slice, skipping late events entirely.

**Suggestion:** Make the limit configurable via `ReadOptions`, surface a "truncated" warning in the polling loop, and document throughput requirements so callers can set an appropriate limit.

---

**File:** `internal/workflow/engine/dynamic_executor.go:320-392, 452-455`  
**Confidence:** High

In `ApplyAtCheckpoint` mode, reordering happens under the big `mu` lock, but waiters blocked on `<-stepDone` are only woken by other step goroutines via `stepDone`. After a checkpoint makes a new step runnable, nothing specifically signals the scheduler, so the loop can sit blocked waiting for an unrelated event that may never come.

**Suggestion:** When a checkpoint completes, signal a dedicated condition/channel (or reorder under `mu` then broadcast) so waiters can re-evaluate dependencies immediately instead of being tied to another goroutine's completion.

---

**File:** `internal/workflow/engine/dynamic_executor.go:168-226`  
**Confidence:** High

Once `collected >= expectedResults` the loop tries a non-blocking `default:` drain that bails out rather than reading from `resultChan`. If the context is already done, a goroutine that later sends a `StepResultSkipped` for a phantom node blocks forever on that send, leaking the goroutine and its resources and preventing `close(resultChan)` from being reached. The parent `runDynamicSteps` has the same core pattern.

**Suggestion:** Do not split the collection loop into "expected-met" vs "drain" phases. Drain the channel until the producer `done` channel is closed, and use a single `errgroup` so `resultChan` consumers cannot outlive producers.

---

**File:** `internal/workflow/engine/mutable_dag.go:321-335`  
**Confidence:** High

The steps returned by `SnapshotWithSteps` share the same `*Step` pointers as the live map. `MutableDAG.AddEdge` and `RemoveEdge` mutate `step.DependsOn` in-place. If the executor keeps a step pointer from an earlier snapshot (e.g. via `findStepInDAG`), it can observe a torn `DependsOn` slice or see a step disappear entirely without the executor knowing.

**Suggestion:** Copy the `Step` struct value (not pointer) when returning a `Steps()` result, or change `Step.DependsOn` to be append-only and stored at the `MutableDAG` level.

---

**File:** `internal/workflow/engine/graph_events.go:85-96`  
**Confidence:** High

The publish loop uses `select { case ch <- event: default: }`, silently dropping the event for any subscriber whose buffer is full. Any crash-recovery or event-store consumer wired to `MutableDAG.Subscribe()` can therefore miss node additions/removals that happened mid-execution, corrupting rehydration or audit replay without any error.

**Suggestion:** Replace the drop policy with a blocking send (with timeout or via `context.Context`), or expose `Publish` options so durability-sensitive callers can opt into back-pressure instead of silent loss.

---

**File:** `internal/workflow/engine/hitl.go:76-79`  
**Confidence:** High

`MemoryInterruptStore.Save` does `s.points[executionID][point.StepID] = point` unconditionally. If two concurrent executions (or a handler that re-triggers) save the same `stepID`, the second silently overwrites the first because the map entry is replaced by a new `*InterruptPoint`. That makes the prior pending point untrackable via `ListPending`.

**Suggestion:** Make the send idempotent (`if _, exists := s.points[executionID][point.StepID]; exists { return nil }`) or store entries as append-only with a higher-level dedup key.

---

### SUGGESTION

**File:** `internal/events/store.go:10-30` / `internal/runtime/manager.go:36, 635-675`  
**Confidence:** High

The interface exposes `StreamVersion` and an optimistic-concurrency `expectedVersion` parameter. `Manager` never uses `StreamVersion`; it only `Read`s. For anything other than an event-sourced database with external optimistic concurrency, that field is both unnecessary and traps implementers. It also means any new `EventStore` implementation must carry the stream version externally rather than internally, leaking persistence mechanics into the client.

**Suggestion:** Remove `StreamVersion` from the interface (the runtime can statefully track its own offset) and remove the `expectedVersion` parameter if the store is responsible for its own concurrency. Clients should call `Read` and deduplicate, not hand-roll OCC.

---

**File:** `internal/workflow/engine/dynamic_executor.go:476-493`  
**Confidence:** High

The `pending` check runs after `stepEg.Wait()`. If all goroutines exited but `completed` is missing some steps (e.g. because they were rejected HITL and marked `processed=true` but not `completed=true`), the function sends `ErrWorkflowIncomplete` to `errChan` and returns, even though every producer has already exited. A caller that waits on `errChan` sees an error that can't correspond to any further result on `resultChan`, producing a spurious workflow-failure status.

**Suggestion:** Treat the post-wait `pending` check as informational and drop it into logs rather than an error channel. HITL rejections are already expressed by a `StepStatusSkipped` result; do not also promote them to an execution-level error after the fact.

---

**File:** `internal/runtime/manager.go:410-462`  
**Confidence:** Medium

The write lock gates the check and increment (`ma.restarts++`) only. The lock is released before `m.g.Go(...)` is called. When `MaxRestartsPerAgent` is high (or 0 = unlimited), each call to `NotifyAgentDead` for the same agentID passes the check and dispatches a new `RestoreAgent` goroutine. Concurrent invocations for the same agent therefore fan out N redundant `RestoreAgent` goroutines that all serialize on `m.mu` inside `RestoreAgent`.

**Suggestion:** Guard goroutine dispatch with per-agent dedup — e.g. maintain a `restoring map[string]bool` under the write lock; only launch if not already restoring.

---

**File:** `internal/runtime/runtime_test.go:247`  
**Confidence:** High

`RestartAgent` no longer increments `m.totalRestarts`. As a result, `Stats().TotalRestarts` only counts "NotifyAgentDead-triggered" restarts and silently omits any manual `RestartAgent` calls. If a production operator calls `RestartAgent` directly (e.g. for a rolling deploy), restart stats will under-report.

**Suggestion:** If `RestartAgent` is a first-class public API, it should either increment `totalRestarts` (and accept the restart-limit check) or be explicitly marked as out-of-band in the interface docs and in `Stats()`.

---

**File:** `internal/integration/runtime_test.go:124`  
**Confidence:** High

Tests pre-populate events under `streamID = "agent:<id>"` (e.g., `"agent:leader-1"`), but `replayEvents` queries with the raw agent ID (`"leader-1"`). The current integration tests therefore silently skip replay verification.

**Suggestion:** Align the integration test stream IDs with `replayEvents` (use `"leader-1"` and `"sub-1"`), or make `replayEvents` try both formats if a migration is in progress.

---

## Cross-Module Risks

1. **EventStore 版本号泄漏**：同上述 SUGGESTION。如果后续 workflow 的 graph event 和 runtime 的事件恢复都接入同一个 `events.Store`，接口中不必要的版本号会让所有实现者背上负担。

2. **`StatefulAgent` 契约缺口**：Runtime 链路调用 `RestoreState → ReplayEvents`，Resurrection Plugin 只调用 `RestoreState`。如果生产部署中某个 agent 同时被两个路径复活（插件用于轻量重启，Runtime 用于 crash recovery），ReplayEvents 中的增量状态会被静默丢失。

3. **`conversation_history` 静默丢弃**：`buildCognitiveState` 会从 MemoryManager 拉取对话历史塞进 state map，但 `leaderAgent` 和 `subAgent` 的 `RestoreState` 都不读这个 key，等于白拉一次数据库。如果不打算消费，应该从 state map 中移除该 key，避免泄露敏感对话内容到序列化路径。

---

## Recommendation

**NEEDS CHANGES** — The two modules are functionally complete, but the cross-module owner-race (Runtime vs LeaderSupervisor) and the competing resurrection plugin are production-blocking. Additionally, the workflow module's `ApplyAtCheckpoint` stall risk and `MutableDAG` aliased-pointer semantics should be addressed before shipping. The `Store` interface should also be simplified to avoid trapping implementers with unnecessary versioning mechanics.
