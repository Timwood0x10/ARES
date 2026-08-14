# 模块分析报告：`internal/workflow`（工作流引擎）

> 分析范围：`internal/workflow/`（65 个 Go 文件），含 graph/、engine/ 等子包

---

## BUG（高置信度）

### 1. `runner_checkpoint.go` Acknowledge 失败后回滚不一致，可能重复应用变更
- **位置**：`runner_checkpoint.go` 312-314、344-346 行
- **说明**：在 `eventSink == nil` 路径，若 `persistCheckpoint` 成功但 `patchQueue.Acknowledge` 失败（312 行），函数返回错误但**不恢复** `scope.Spec` 或已记录的 mutation ID（回滚仅在 persist 失败时发生）。磁盘上的 checkpoint 已反映含 mutation ID 的新 spec，但内存 `PatchQueue` 仍把该 mutation 标为 pending。后续 `applyQueuedMutations`/resume 会**重复应用同一 mutation**。`eventSink != nil` 路径（344-346）有同样问题。spec 与 queue 之间的错误原子性不一致。
- **状态**：✅ 已修复（2026-08-14）——`eventSink == nil` 路径的 `Acknowledge` 失败时回滚 `scope.Spec = previousSpec` + `RemoveMutationIDs(len(ids))`，与 persist 失败的回滚一致，避免 resume 重复应用 mutation；build/test 通过。

### 2. `engine/recovery_patcher.go` `Snapshot` 返回活 DAG 指针（无锁/无拷贝）
- **位置**：`engine/recovery_patcher.go` 38-43 行
- **说明**：`Snapshot` 直接 `return e.dag`（`*MutableDAG`），不像 `MutableDAG` 的其它快照方法（如 `SnapshotWithSteps`）那样加锁或拷贝。接收方可能并发观察/修改共享状态，与包内其它地方的锁纪律不一致。
- **状态**：⚠️ 已核实为有测试守护的契约（2026-08-14）——`Snapshot` 返回活 DAG 引用是**故意行为**：`SetDAG` 后 recovery 补丁必须 mutate agent 的真实 DAG（非拷贝），由 `TestUpdateLiveDAG_DoesNotFailOnRegisteredExecutors` 的 `assert.Same(liveDAG, snapshot)` 守护；活引用仅交给 recovery 执行器，不暴露给任意观察者。已加注释说明契约，不做行为变更。

### 3. `graph/graph.go` `Edge()` 静默丢弃不同条件边
- **位置**：`graph/graph.go` 179-191 行
- **说明**：当两个条件边共享同一 `from→to` 但**条件函数不同**时，第二次调用返回 `g, nil`（成功）却不添加边。Go 无法比较函数值，所以代码把任何同 from→to 的第二条条件边都当作重复，静默返回成功而不变更。调用方以为两条都注册了，实际只有第一条。
- **状态**：✅ 已核实修复（2026-08-14）——现仅抑制"无条件的重复边"（同 from→to 且均无 cond），条件边总是追加（注释明确说明"Conditional edges are always appended; callers responsible..."，且 `compileGraphEdges` 为每条条件边发 `BranchMany`）；报告条目过时。

### 4. `compiler.go` `compileUntilCondition` 的 LoopSpec 总是被覆盖
- **位置**：`compiler.go` 64、70-79、241-249 行
- **说明**：`CompileFromEngine` 中 `compileUntilCondition`（64 行）在 loop-config 块（70-79）**之前**运行；当 `UntilCondition != nil` 时分配 `LoopSpec{MaxIterations}` 到 `spec.Loop`，但紧随的代码块**整体替换** `spec.Loop` 为来自 `LoopConfig` 的新 LoopSpec。`compileUntilCondition` 分配的值总是被丢弃——该函数无实际效果（只有 `CompileFromEngineWithBindings` 保留 `UntilCondition`）。
- **状态**：✅ 已修复（2026-08-14）——`compileUntilCondition` 调用移至 loop-config 块**之后**，其 `MaxIterations` 兜底不再被覆盖（LoopConfig 为 nil 时仍生效），build/test 通过。

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
