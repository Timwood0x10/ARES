# 统一 DAG 管线最终验收（R3）

> 日期：2026-07-28  
> 基准：`DAG_UNIFIED_PIPELINE.md`、`DAG_UNIFIED_PIPELINE_REVIEW_2026-07-28_R2.md`  
> 方式：只读源码、现有测试、Go overlay 定向语义测试、race/vet/全仓测试；未修改项目源码。

## 1. 最终结论

**还不能判定为 OK。**

本轮确实修复了上一轮的主要 Runner 阻断项：并行 fan-in、单次并发限流、shared Runner data race、`LoopNodes`、节点 Error/Attempts、SubWorkflow 父状态继承、HITL 结果通道和协作式节点 Timeout 均已通过定向验证。

但最终验收又稳定复现 5 个执行合同缺陷，并确认生产入口、Compiler binding 和外围闭环仍未统一。因此：

- **Runner 核心：可继续作为 experimental / opt-in 使用；**
- **默认生产切换：不通过；**
- **删除 legacy engine/graph：不通过；**
- **宣称“Single Runner 唯一生产管线”：目前不成立。**

综合完成度从 R2 的约 39% 提升到 **约 55%**。

## 2. 上轮阻断项复验矩阵

| 项目 | 本轮结果 | 证据 |
|---|---|---|
| fan-out / fan-in 提前退出 | 已修复 | overlay `-count=20` 通过；diamond `-count=100` 通过 |
| `MaxParallel` 单次限流 | 已修复 | overlay `-count=20` 通过 |
| shared Runner data race | 已修复 | `go test -race` 定向并发复用通过 |
| `LoopNodes` body-only | 已修复 | setup=1、body=3 定向测试通过 |
| Node Error / Attempts | 已修复 | 两次失败后 Error 非空、Attempts=2 |
| SubWorkflow 父 State | 已修复 | child 读取父 `token` 成功，`-count=20` 通过 |
| SubWorkflow 基础输出 | 已修复 | child 输出合入父 sub 节点输出 |
| Node Timeout | 基础能力已修复 | 响应 `ctx.Done()` 的 executor 在 10ms 左右失败 |
| HITL timeout 吞结果 | 代码已修复 | 不再从共享 `resultCh` 反向读取 |
| 条件分支示例 | 已修复 | 示例输出 pass=completed、fail=not_selected |

## 3. 本轮新增阻断发现

### H1. Loop 路径丢失 initial input / variables

**位置：** `internal/workflow/runner.go:226-230,250-289`

`Execute` 首先对初始 scope 调用 `SetInitialState`，但进入 Loop 分支后，第 0 轮立即重新创建 scope：

```go
scope = NewExecutionScope("", iterSpec)
scope.InitNodeStates()
```

初始 input/variables 没有复制进新 scope。定向结果：setup 与两轮 body 全部从 `StateView.Get("input")` 读到 `nil`。

**影响：** 只要 workflow 配置 Loop，Service/SDK 传入的初始请求数据即使接入 Runner，也会在执行前丢失。

### H2. Loop 最终 Result 丢失父 workflow 身份与 setup 节点状态

**位置：** `internal/workflow/runner.go:261-295,902-935`

最后一轮使用 `bodySpec`，并把 `scope` 替换为 body scope；最终直接 `scope.ToResult()`。

定向结果：

```text
SpecID="loop-identity.loop-body"，期望 "loop-identity"
最终 NodeStates 中不存在 setup
```

**影响：** API 响应、审计、checkpoint、指标会把执行错误归属到内部 loop-body spec，并丢失非 loop 节点的终态。

### H3. Checkpoint 保存发生在节点提交之前

**位置：** `internal/workflow/runner.go:438-458,661-674,826-899`

`emitAfterStep(... saveRunnerCheckpoint ...)` 在 goroutine 内发送 `nodeResult` 之前执行；真正的 `SetNodeOutput + CommitState + Scheduler.OnNodeCompleted` 在调度主循环收到结果后才发生。

定向快照只包含：

```text
state = {input:""}
node a = running
```

而不是本节点已提交的 output 和 completed 状态。

**影响：** 注释声称“after each node commit”，实际却是 pre-commit 非原子快照；崩溃恢复会重复或错误跳过节点。

### H4. `Merge` 没有实现“每次到达触发一次”

**位置：** `internal/workflow/scheduler.go:193-200,268-307`

`Merge` 调用 `enqueue(target)`，但 `enqueue` 对 `completed[target]` 直接返回；首次执行后后续 arrival 无法再次触发。

定向结果：两个 control-flow 前驱到达，merge 只执行 1 次，合同期望 2 次。

### H5. `JoinAll` 不等待 control-flow 前驱

**位置：** `internal/workflow/scheduler.go:282-296`

`JoinAll` 只遍历 `EdgeDataDependency` 前驱；control-flow 前驱完全不参与 allDone 判断。

定向结果：a 完成、b 被阻塞时，join 已提前执行。

**影响：** 混合条件分支和汇合的工作流会在所需分支尚未终止时提前运行。

## 4. Compiler 仍不是可执行的语义保持编译

### 已有进展

- 新增 `CompiledWorkflow{Spec, ConditionFuncs, RouterFuncs, UntilCondition}`；
- engine Condition/Router/UntilCondition closure 可以被捕获；
- Retry、Recovery、Interrupt、Timeout、SubWorkflow 等字段有更多映射。

### 仍未闭环

1. `CompileFromEngineWithBindings` 在全仓没有调用点；
2. Service 仍调用基础 `CompileFromEngine`，closure 实际被丢弃；
3. Runner 没有消费 `RouterFuncs` 和 `UntilCondition` 的 adapter；
4. `LoopSpec` 没有 UntilCondition 字段，Runner 只按 MaxIterations；
5. engine Condition 仍只把第一条入边改成 control-flow：`compiler.go:217-225`；
6. graph condition 仍只是 `graph_closure_ref` marker，graph Node 实例/binding 未编入结果。

因此当前 Compiler 更接近“结构迁移器 + 未使用的 closure 容器”，还不是生产可执行的语义保持编译器。

## 5. 生产入口仍未统一

### Service sync / stream

`api/service/workflow/service.go:133-155,362-380` 仍以 `UseRunner bool` 分流；Go 零值为 false，所以所谓“Runner 默认”并未实现。

Runner 路径还有三项未接线：

- 未传 `workflow.WithInitialInput(req.Input)`；
- 未传 `workflow.WithInitialVariables(req.Variables)`；
- 未传 `workflow.WithPluginBus(s.config.PluginBus)`，也没有 CheckpointStore 配置。

所以 `executeWithRunner` / `executeStreamWithRunner` 中 executor 读取 `view.Get("input")` 时仍得到空值。

### Client / YAML

`api/client/workflow.go:24,38,63-70` 仍固定持有并调用 `engine.Executor`。

### public Graph / SubGraph

`api/graph/graph.go:16-84` 仍公开 legacy `Graph/NewGraph`；`internal/workflow/graph/executor.go:21` 仍有独立执行循环；`internal/workflow/graph/node.go:340` 的 SubGraphNode 仍直接调用 `n.graph.Execute`。

因此当前仍有至少三套可达执行语义，尚未满足“所有公共入口最终进入同一个 Runner”。

## 6. Checkpoint / Resume 与外围闭环

Runner checkpoint 现在包含 execution ID、spec ID、State 和 NodeStates，比 R2 有明显进步；但由于保存时机在 commit 前，仍不是原子恢复点，而且 Runner 没有 Load/Resume API。

旧 `DynamicExecutor` 仍独占真正的恢复入口：`internal/workflow/engine/dynamic_executor.go:177-251`。

Runner 仍未接入：

- PatchQueue / typed mutation / safe point；
- ExecutionCollector；
- canonical trace / metrics；
- Knowledge prepare；
- Memory route/context hooks；
- Experience terminal checkpoint/distillation；
- Evolution proposal/apply/outcome。

`PluginBus` 目前仅调用 BeforeStep / AfterStep 和基础事件，不能代表上述闭环已经迁移。

## 7. 测试与静态检查

### 通过

```text
go test ./internal/workflow/...
go test -race ./internal/workflow/...
go test ./api/service/workflow/... ./api/client/... ./api/graph/...
go vet ./internal/workflow/... ./api/service/workflow/... ./api/client/... ./api/graph/...
go test ./...
go run ./examples/03-dag-workflow
```

全仓测试通过；示例条件分支已正确只执行 pass。

### 定向合同失败

```text
Loop initial input: 全部读到 nil
Loop result identity: SpecID 变为 *.loop-body，setup 状态丢失
Checkpoint commit boundary: state 无节点输出，node 仍为 running
Merge: 两次 arrival 只执行一次
JoinAll(control-flow): 第二前驱未完成时提前执行
```

这些场景没有被现有仓库测试覆盖，因此“全绿”不能等同于设计验收通过。

## 8. 最新完成度

| 阶段 | 权重 | 当前完成度 | 加权得分 | 判断 |
|---|---:|---:|---:|---|
| P0 语义合同与金丝雀测试 | 15% | 65% | 9.75% | 核心 fan-in/限流已稳；Merge/control Join/Loop 结果合同仍缺 |
| P1 IR / Compiler / Validator / Explain | 20% | 60% | 12.00% | IR/字段映射较完整；Bindings 无消费，仍无 Explain |
| P2 Single Runner | 30% | 72% | 21.60% | 主循环明显成熟；Loop、checkpoint、复杂 Join/Merge 仍失败 |
| P3 生产入口与外围闭环 | 25% | 28% | 7.00% | Service 有 opt-in sync/stream；输入、Plugin、Resume 与外围未闭环 |
| P4 删除旧路径 | 10% | 0% | 0.00% | legacy 执行循环全部仍可达 |
| **总计** | **100%** |  | **50.35%** | **按严格设计约 50%；按 Runner 核心约 70%** |

考虑本轮 Runner 修复的实质进展，可把工程总体口径描述为 **约 50%–55%**；不能用“测试全绿”把入口迁移和外围闭环计为完成。

## 9. 上线前最小修复顺序

### P0：Runner 正确性

1. Loop 必须保留同一个父 ExecutionScope，或建立明确的 parent/iteration scope 聚合；初始 input/variables、SpecID、所有 NodeStates 不能丢；
2. 把 checkpoint 移到 `handleResult` 的 commit + scheduler transition 之后，并提供一致性快照；
3. 明确并实现 `Merge` 重入模型（不能复用 completed 去重）；
4. `JoinAll` 基于“所有被激活前驱”而不是只看 data edge；
5. 增加上述 5 个强断言回归测试。

### P1：迁移合同

6. Service Runner 路径传入 input/variables/PluginBus/CheckpointStore；
7. 把 `UseRunner` 改成可表达三态的配置，或真正默认 Runner；
8. Compiler 统一返回并消费 `Spec + Bindings`，无法保持语义时 fail-fast；
9. 实现 UntilCondition / Router binding 的 Runner adapter；
10. 实现 Runner Resume。

### P2：唯一生产闭环

11. 迁移 Client/YAML、public Graph/SubGraph；
12. 接入 PatchQueue、Collector/Trace、Knowledge/Memory/Experience/Evolution；
13. 所有入口通过同一 conformance suite 后，才删除 legacy runtime。

## 10. 最终判定

**不是最终 OK，但已经从“Runner 骨架”进入“可运行实验实现”。**

建议当前状态：

- 保持 Runner opt-in / experimental；
- 不要默认切换生产流量；
- 不要删除 legacy engine/graph；
- 先修 Loop scope、checkpoint commit boundary、Merge/JoinAll 四个核心合同，再做入口迁移。
