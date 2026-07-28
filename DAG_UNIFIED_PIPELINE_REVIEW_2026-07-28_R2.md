# 统一 DAG 管线第三轮验收（R2）

> 日期：2026-07-28  
> 基准：`DAG_UNIFIED_PIPELINE.md`、`DAG_UNIFIED_PIPELINE_COMPLETION_REVIEW.md`  
> 范围：当前 `internal/workflow`、Service / Client / Graph 生产入口、Runner 外围接线。  
> 方式：只读源码、现有测试、Go overlay 临时语义测试与 race 测试；未修改项目源码。

## 1. 结论

**并非“都解决了”。当前完成度约 39%，较上轮 31% 有明确进展，但仍不能切换为统一生产运行时。**

本轮确认修复：

1. 并行 fan-out/fan-in 提前退出已修复；
2. `MaxParallel` 对单次执行已生效；
3. missing binding 已改为明确失败；
4. SubWorkflow 已有最小递归执行；
5. Loop 开始保存跨轮 State；
6. PluginBus Before/AfterStep 与基础 checkpoint save 已加入 Runner；
7. HITL timeout/auto-action、部分 RecoverySpec 分支已加入。

但定向验收仍发现：

- `LoopNodes` 被忽略；
- `NodeSpec.Timeout` 不生效；
- `NodeStatusValue.Error/Attempts` 不填充；
- SubWorkflow 不继承父 State；
- 同一个 Runner 并发执行发生 data race；
- Compiler 仍不保持 Condition/Router/UntilCondition/graph binding；
- Service 输入仍未注入；
- Service Stream、Client、Graph/SubGraph 仍走旧运行时；
- Checkpoint 只是节点元数据，不是可恢复的原子 ExecutionScope；无 Resume；
- Knowledge / Memory / Experience / Evolution / PatchQueue / Collector / Trace 仍未进入 Runner。

**上线判断：不通过。**

## 2. 最新完成度

| 阶段 | 权重 | 当前完成度 | 加权得分 | 判断 |
|---|---:|---:|---:|---|
| P0 语义合同与金丝雀测试 | 15% | 50% | 7.5% | diamond/限流有强测试；复杂 Branch/Join/Loop 仍未冻结 |
| P1 IR / Compiler / Validator / Explain | 20% | 55% | 11.0% | IR 骨架存在；Compiler 非语义保持；无 Explain |
| P2 Single Runner | 30% | 52% | 15.6% | 核心并行已修；timeout/loop/result/subflow/concurrency 仍失败 |
| P3 生产入口与外围闭环 | 25% | 20% | 5.0% | Plugin/基础 save 有进展；生产入口与完整闭环未统一 |
| P4 删除旧路径 | 10% | 0% | 0% | 旧执行循环全部仍可达 |
| **总计** | **100%** |  | **39.1% ≈ 39%** | **仍是实验实现** |

## 3. 上轮 10 项 Critical / High 修复矩阵

| 问题 | 当前状态 | 证据 |
|---|---|---|
| C1 并行调度提前退出 | **已修复** | `runner.go:253-317`；diamond 100 次通过；旧 overlay 10 次通过 |
| C2 Condition evaluator 无入口 | **已修复** | `runner.go:148-163,221-227` |
| C3 单生产运行时未成立 | **未修复** | `service.go:133-155,305-358`; `api/client/workflow.go:24,38,70`; `graph/node.go:340` |
| H1 Service input 未注入 | **未修复** | `service.go:184`; `runner.go:206-208` 仍创建空 Scope |
| H2 missing binding 假成功 | **已修复** | `runner.go:59-71`; `edge_test.go:62-81` |
| H3 Compiler 不保持语义 | **未修复** | `compiler.go:20-27,111-143,157-225,241-262` |
| H4 MaxParallel 无效 | **单次执行已修复；并发复用有竞态** | `runner.go:210-214,253-381`；限流测试通过；race 失败 |
| H5 Loop 重置/忽略 LoopNodes | **部分修复** | `runner.go:229-257` 保存 State，但仍每轮执行整个 Spec |
| H6 State/Result 契约 | **部分修复** | Output 已有；`scope.go:298-307` 不写 node Error；Attempts 仍为 0 |
| H7 SubWorkflow 不执行 | **部分修复** | `runner.go:410-419` 可递归执行；不继承父 State、无 child scope/checkpoint 语义 |

统计：**3 项已修复，3 项部分修复，4 项未修复。**

## 4. 已确认修复

### 4.1 并行 diamond / fan-in

当前 Runner 使用 `running` 计数，并只在 `dispatched == 0 && running == 0` 时终止：

```go
if dispatched == 0 && atomic.LoadInt32(&running) == 0 {
    break
}
```

动态验证：

```text
go test ./internal/workflow -run TestRunner_DiamondFanIn_JoinAll -count=100
PASS

旧 overlay：fan-in + MaxParallel，各 count=10
PASS
```

### 4.2 MaxParallel 单次执行

`dispatchReady` 加入 semaphore，`MaxParallel=1` 的旧失败复现已连续 10 次通过。

### 4.3 missing binding

`FuncNodeExecutor.ExecuteNode` 现在返回：

```go
return nil, fmt.Errorf("node %q: no executor registered — missing binding", spec.ID)
```

结果状态为 failed，不再空输出 completed。

### 4.4 SubWorkflow 最小执行

无父状态依赖的 child workflow 可以递归执行，父 SubWorkflow 节点可得到 child 结果。这只证明“可调用”，尚不等于完整 child scope 语义。

## 5. 新增 Critical / High 发现

### C1. [已复现] 同一个 Runner 并发复用发生 data race

**位置：** `internal/workflow/runner.go:85,210-214,253-256`

`Execute` 把本次 Spec 的 `MaxParallel` 写回共享 Runner：

```go
r.maxParallel = spec.Schedule.MaxParallel
```

随后 `execLoop` 又读取该共享字段建立 semaphore。同一个 Runner 并发执行两个不同 Spec 时产生读写/写写竞态，并可能使用另一个 execution 的并发预算。

动态结果：

```text
go test -race -overlay=... -run TestReviewSharedRunnerConcurrentSpecs
WARNING: DATA RACE
runner.go:189/190（当前行号约 211-213）
FAIL
```

**修复建议：** `maxParallel` 必须是 `Execute` 局部变量，并作为参数传入 `execLoop/dispatchReady`；不要在执行阶段修改 Runner 配置。

### H1. [已复现] LoopNodes 仍被忽略

测试 Spec：setup → body，`LoopNodes=[body]`，`MaxIterations=3`。

```text
实测 setup=3 body=3
期望 setup=1 body=3
```

`runner.go:229-257` 每轮仍对整个 Spec 重建 Scheduler，未构建 loop body 子计划。跨轮 State 保存只是部分修复。

### H2. [已复现] NodeSpec.Timeout 未执行

配置节点 `Timeout=10ms`，executor sleep 100ms：

```text
实测约 101ms 后 status=completed
```

`executeSingle` 直接把原 ctx 传入 executor（`runner.go:488-525`），没有 `context.WithTimeout(spec.Timeout)`。

### H3. [已复现] NodeStatusValue.Error / Attempts 仍未填充

两次失败重试后：

```text
Status=failed
Error=""
Attempts=0
```

`scope.SetNodeError` 只设置全局 `s.err`，未写当前 node 的 `Error`；Retry loop 未更新 `Attempts`。

### H4. [已复现] SubWorkflow 不继承父 State

父节点写入 `token=parent-value`，child 读取 `token`：

```text
child output: seen=<nil>
```

原因：`runner.go:410-419` 直接 `r.Execute(ctx, spec.SubWorkflow)`，创建全新空 Scope。当前没有输入映射、父 State 只读继承、结果 merge policy 或 child execution ID。

### H5. [已验证] HITL timeout 分支可能吞掉其他节点结果

**位置：** `runner.go:466-485`

```go
select {
case resultCh <- nodeResult{...}:
case <-resultCh:
}
```

第二个 case 会从共享 `resultCh` 接收并丢弃任意其他节点的完成结果。若被选中：

- 其他节点结果消失；
- 当前 interrupt 节点也可能没有向调度主循环发送结果；
- 节点可能从 running 最终被错误改为 cancelled/unreachable。

**修复建议：** 绝不能从 resultCh 反向消费。只发送一次当前节点结果；channel 容量/生命周期应由调度核心保证。

## 6. 仍未修复的架构问题

### 6.1 Compiler 仍不保持语义

- engine Condition 只改第一条入边为 control flow，没有可执行条件 binding；
- root/multi-dependency Condition 仍丢失；
- Router 只写 metadata marker；
- UntilCondition 在 `compiler.go:120-143` 先创建后被 LoopSpec 重写，且 `LoopSpec` 本身没有 UntilCondition 字段；
- graph condition 仍只是 `graph_closure_ref` 字符串；
- graph Node 类型/实例/binding 不进入编译结果；
- 注释要求调用方提供 Bindings，但 API 仍只返回 `*WorkflowSpec`。

### 6.2 生产入口仍未统一

- Service 同步：`UseRunner` 零值 false，默认 DynamicExecutor；
- Service Stream：固定 DynamicExecutor；
- WorkflowClient/YAML：固定静态 `engine.Executor`；
- public Graph 仍公开旧 `NewGraph/Graph.Execute`；
- SubGraphNode 仍直接调用旧 `n.graph.Execute`。

因此仍存在多套 ready queue / indegree / status / checkpoint 语义。

### 6.3 Service initial input 仍缺失

Service executor closure读取 `view.Get("input")`，但 Runner 没有 initial state/input 参数，也不解析 `NodeSpec.Input`。启用 `UseRunner` 后 Agent 仍可能收到 nil。

## 7. 外围闭环复核

### 已有进展

- `WithPluginBus` 已存在，并调用 BeforeStep / AfterStep；
- `WithCheckpointStore` 已存在，每个 node 后尝试 Save；
- RecoverySpec 的 `replace_node/fail_fast` 有基础分支；
- HITL 有 timeout/auto-action 代码。

### 仍不构成闭环

当前 checkpoint（`runner.go:772-788`）只保存：

```text
saved_at / node_id / status / error / duration
```

缺少：

- execution ID；
- Workflow IR ID/version；
- ExecutionScope State；
- 所有 NodeStatus；
- ready/running/pending 集合；
- patch queue/version；
- route decision；
- Resume API。

而且 key 仅为 `nodeID`，不同 workflow/execution 的同名节点可能覆盖。因此它是“节点审计记录”，不是设计要求的原子恢复点。

Runner 仍未接入：

- PatchQueue / typed mutation / safe point；
- ExecutionCollector；
- trace / metrics / canonical event；
- Knowledge prepare；
- Memory Before/AfterNode；
- Experience terminal distillation；
- Evolution proposal/apply。

## 8. 示例仍错误

`go run ./examples/03-dag-workflow` 仍同时输出：

```text
pass (completed)
fail (completed)
```

但文案声称 score=85 只走 pass。示例两条 control edge没有组成同一个 BranchOne group，fallback 仍是无条件 BranchMany 分支。

## 9. 验证汇总

### 通过

```text
go test ./internal/workflow/...
go test -race ./internal/workflow/...
go test ./api/service/workflow/... ./api/client/... ./api/graph/...
go vet ./internal/workflow/... ./api/service/workflow/... ./api/client/... ./api/graph/...
go test ./...
diamond -count=100
旧 fan-in / MaxParallel overlay -count=10
SubWorkflow 最小递归执行
```

### 失败

```text
LoopNodes：setup=3 body=3，期望 1/3
Node Timeout：10ms 配置，约 101ms 后 completed
Node Error/Attempts：Error="" Attempts=0
SubWorkflow parent state：child 读取 token 得到 nil
shared Runner race：go test -race 明确报 DATA RACE
示例条件分支：pass/fail 同时 completed
```

## 10. 下一步修复顺序

### P0：本轮新增阻断项

1. 把 `maxParallel` 改为 execution-local，修复 shared Runner data race；
2. 删除 HITL timeout 中的 `<-resultCh`，保证每节点只产生一个结果；
3. 实现 `NodeSpec.Timeout`；
4. 写入 node Error/Attempts，并加强断言；
5. 实现真正的 LoopNodes 子计划；
6. SubWorkflow 使用 child scope，继承映射后的父 State。

### P1：迁移前必须完成

7. Runner 支持 initial input/variables 与 NodeSpec.Input；
8. Compiler 改为 `Spec + Bindings`，无法语义保持时 fail-fast；
9. 修正 BranchOne/fallback 示例和合同测试；
10. checkpoint 保存完整 ExecutionScope 并提供 Resume。

### P2：形成唯一生产闭环

11. 接入 PatchQueue、Collector/Trace、Knowledge/Memory/Experience/Evolution；
12. 迁移 Service sync/stream、Client/YAML、Graph/SubGraph；
13. 全入口通过统一 conformance suite 后，才开始删除旧运行时。

## 11. 最终判断

本轮修复是实质性的，特别是并行 fan-in、单次限流和 missing binding 已跨过上一轮的硬阻断。但“都解决了”的判断不成立：当前仍有可复现竞态、未实现的 timeout/loop/result 契约、残缺 SubWorkflow、非语义保持 Compiler、多生产运行时和未闭环外围系统。

**建议继续标记为 experimental，不要默认打开 `UseRunner`，不要删除 legacy engine/graph。**
