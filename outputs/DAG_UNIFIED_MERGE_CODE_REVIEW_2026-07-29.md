# DAG 统一管线合并前 Code Review

**结论：暂不建议合并。** 已验证 2 个 High 级正确性缺陷；均位于本轮新增的 Mutation / Resume 生命周期闭环，和本次目标直接相关。

## 发现摘要

| 严重度 | 位置 | 结论 |
|---|---|---|
| High | `internal/workflow/patch_queue.go:90-100` | 已提交 mutation ID 会从全局去重集合移除，可被同一执行再次投递并重复应用。 |
| High | `internal/workflow/runner_checkpoint.go:70-78` | Resume 没有恢复或校验调用方注入的 ExecutionCollector，恢复执行的生命周期采集链断裂。 |
| Low | `internal/workflow/graph/node.go:24-26` | 已实现单一 Runner 后仍保留“once the single Runner is built”的过期 TODO。 |

## High — 已提交 mutation 可被重复投递并二次执行 [已验证]

**位置：** `internal/workflow/patch_queue.go:39-43`, `internal/workflow/patch_queue.go:90-100`; `internal/workflow/runner_mutation.go:17-36`; `internal/workflow/runner_checkpoint.go:271-295`。

`Enqueue` 仅以 `q.known[executionID]` 去重；而 `Acknowledge` 在 durable commit 后执行：

```go
delete(q.known[executionID], id)
```

随后同一 ID 可以再次 `Enqueue`。Runner safe point 只校验当前 pending batch 内的重复 ID：

```go
if seen[mutation.ID] { ... }
```

它不检查 `scope.MutationIDs()`（已持久化的已应用 ID）。因此消息重投、恢复后的重复 patch 或上游至少一次投递都会再次进入 `applyMutations`，再次发出 `mutation.applied` 并修改 effective spec。以同 ID 的 `MutationRemoveEdge` 为例：首次成功后重复投递会在第二次找不到边并中断工作流；其他 mutation 则可能静默重复应用。

**建议修复：** PatchQueue 需要区分 `pending` 与 `applied` ID，确认后保留 applied tombstone；Resume 时将 `CheckpointSnapshot.MutationIDs` 恢复到该集合。`applyQueuedMutations` 也应拒绝任何已在 `scope.MutationIDs()` 中存在的 ID。补充“ack 后重复 enqueue”和“resume 后重复 enqueue”合同测试。

## High — Resume 未接入调用方 Collector，统一生命周期在恢复路径断裂 [已验证]

**位置：** `internal/workflow/runner.go:208-214`; `internal/workflow/runner_checkpoint.go:70-78`, `124-151`。

首次执行会校验并挂载调用方传入的 Collector：

```go
if r.collector != nil {
    if r.collector.ExecutionID() != scope.ExecutionID { ... }
    scope.SetCollector(r.collector)
}
```

但 `ResumeExecution` 创建 scope 后仅恢复 state、node states、interrupt、mutation ID 与 event sequence，未执行等价的 collector 身份校验/挂载：

```go
scope := NewExecutionScope(snapshot.ExecutionID, snapshot.EffectiveSpec)
...
scope.RestoreEventSequence(snapshot.EventSequence)
```

结果是带 `WithExecutionCollector(...)` 的 Runner 在恢复时静默使用新的默认 Collector：恢复节点产生的 route/error/HITL 记录及最终 Evolution Outcome 不会写入调用方的 execution-scoped collector。这与“统一执行生命周期”合同不一致。

**建议修复：** 抽取 `attachExecutionCollector(scope)`，由 `Execute` 与 `ResumeExecution` 共同调用；Resume 要求 collector ID 与 checkpoint execution ID 一致。新增带 Collector 的 Resume 合同测试，断言 route/interrupt/error 与 Outcome 均在同一 Collector 可见。

## Low — Graph Node 接口 TODO 已过期 [已验证]

**位置：** `internal/workflow/graph/node.go:24-26`。

注释仍声明“once the single Runner is built”，但 graph 已通过 `CompileBound` 和 `Runner.ExecuteBound` 进入统一 Runner。它不会改变运行时行为，但会误导后续维护。

**建议修复：** 更新为当前 adapter 的技术债描述，或删除该 TODO。

## 已验证通过 / 非问题

- `api/service/workflow/service.go:133-142,178-190`：同步和流式 Service 均经 `buildBoundRunner` 后调用 `Runner.ExecuteBound`，没有 legacy 执行分支。
- `internal/workflow/graph/executor.go:19-40` 与 `internal/workflow/graph/runner.go:20-64`：Graph 编译为 `BoundWorkflow` 并由统一 Runner 运行；未见独立 graph 执行循环。
- 全仓源码扫描未发现 `NewExecutor(`、`NewDynamicExecutor(`、`type Executor struct`、`type DynamicExecutor struct`、`ExecutionModeLegacy` 或 `ExecuteFromCheckpoint(`。
- `internal/workflow/runner_checkpoint.go:53-69`：Resume 同时校验 base Spec hash 与 effective Spec hash；Mutation 后拓扑不会按旧 Spec 恢复。
- `internal/workflow/scope.go:473-510`：checkpoint / mutation 的持久化先于序列推进与事件发布，序列协议具备明确的 durable-before-publish 边界。
- 已完成质量门：`golangci-lint run ./...`、`go test ./...`、`go test -race ./...`、legacy bypass 扫描均通过。最终一体化复跑仍在执行中。

## 合并建议

先修复上述两个 High，并补齐对应合同测试；完成后复跑 `go test ./...`、`go test -race ./...`、`staticcheck ./...`、`go vet ./...`、`golangci-lint run ./...` 与 `git diff --check`，再合并。
