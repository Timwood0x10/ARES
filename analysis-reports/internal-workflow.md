# 模块分析报告：`internal/workflow`（工作流引擎）

> 分析范围：`internal/workflow/`（65 个 Go 文件），含 graph/、engine/ 等子包

---

## BUG（高置信度）

### 5. `scope.go` `ToResult()` 正常完成后状态可为 `Interrupted`
- **位置**：`scope.go` 570-575、619-634 行
- **说明**：`r.Status = overallNodeStatus(r.NodeStates)`，而 `overallNodeStatus` 只要任一节点为 Pending/Ready/Running/Interrupted 就返回 `NodeStatusInterrupted`。若工作流完成但某节点停留在 Pending（如循环体执行期间 loop 外节点未被 `finaliseUnprocessed` 终结），整体 Result 状态退化为 Interrupted 而非 Completed。依赖 `finaliseUnprocessed` 是否正确覆盖所有节点。

### 6. `scheduler.go` `arrive()` 的 repeatable 只针对 Merge *源*
- **位置**：`scheduler.go` 284 行
- **说明**：`repeatable := s.joinKind(edge.From) == Merge`，repeatable 取决于边的**源**节点的 join kind，而非目标。Merge 节点作为**目标**时（这是 merge 模式的真实用法，见 `enqueueMerge`），该标记从错误节点计算。结合 `arrive` 把 Merge 目标路由到 `enqueueMerge`（290 行），`edgeArrived && !repeatable` guard 可能阻塞 Merge 重复触发时的合法重新到达。语义错配。

### 7. `graph/executor.go` 失败时也合并部分结果状态
- **位置**：`graph/executor.go` 23、39 行
- **说明**：`executeCompiledGraph` 中 `mergeUnifiedResultState(state, result)` 在 `err != nil` 时也无条件执行。部分失败时，构建到一半的 `result.State` 仍被并入 `state` 并返回。只检查 `execErr` 的调用方会看到尽管出错仍被变更的 state。

---

## DEAD_CODE

### 8. `types.go` `NodeStatusReady` / `NodeStatusCancelled` 未产生
- **位置**：`types.go` 15、25 行
- **说明**：两个常量声明但生产代码从不赋值给任何节点状态（`NodeStatusReady` 在 `overallNodeStatus` 被提及，但从不设置；`NodeStatusCancelled` 被多个 switch 处理但从不产生）。

### 9. `runner_execution.go` 未使用的 `maxParallel` 参数
- **位置**：`runner_execution.go` 199、215、422 行
- **说明**：`maxParallel` 被 `executeNode` 接收并转发给 `executeChildScope`，但 `executeChildScope`（422 行）在函数体内**从不使用**它——子工作流通过 `executeWorkflow`/`executeIteration` 读 `spec.Schedule.MaxParallel`。参数贯穿两个函数而无效果。

### 10. `mutation.go` 移除有边的节点直接中止整个批次
- **位置**：`mutation.go` 112-115 行
- **说明**：`removeMutationNode` 在节点仍有边时返回错误，`applyMutations` 捕获并中止，导致整个 mutation 批次失败，而非部分应用。严格性权衡，**（标注：非明确 bug。）**

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `runner_checkpoint.go` 312-314 | Acknowledge 失败不回滚，mutation 可能重复应用 |
| **高** | `engine/recovery_patcher.go` 38 | Snapshot 返回活 DAG 指针（无锁） |
| 中 | `graph/graph.go` 179 | 静默丢弃不同条件边 |
| 中 | `compiler.go` 64 | compileUntilCondition 的 LoopSpec 总被覆盖 |
| 中 | `scope.go` 570 | 正常完成状态可退化为 Interrupted |
| 中 | `scheduler.go` 284 | repeatable 从错误的源节点计算 |
| 低 | `types.go` 15/25 | 两个状态常量从不产生 |
| 低 | `runner_execution.go` 422 | maxParallel 参数无效 |
