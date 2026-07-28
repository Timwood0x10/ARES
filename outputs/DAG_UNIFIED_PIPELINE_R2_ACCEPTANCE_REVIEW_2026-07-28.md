# 统一 DAG 管线 R2 修复验收

> 日期：2026-07-28  
> 审查对象：`DAG_UNIFIED_PIPELINE_REVIEW_2026-07-28_R2.md` 附录所声明的完成项  
> 方式：只读源码、git diff、现有测试、race/vet/lint、Go overlay 定向合同测试；未修改项目源码。

## 1. 结论

**R2 要求没有全部完成，附录中的“全部 ✅”不能通过验收。**

本轮确实有实质进展：shared Runner 竞态、`LoopNodes`、Error/Attempts、SubWorkflow 父状态、HITL 通道、BranchOne 示例等已修复；新加入的 `UntilCondition` 在单进程运行测试中也能提前终止循环。

但存在两个直接阻断上线的问题：

1. `ResumeExecution` 名为恢复执行，实际只反序列化并立即返回，pending 节点一次都不执行，却可能返回 `completed`；
2. 统一生产入口仍未成立，Service 默认、Client/YAML、Graph/SubGraph 仍可达 legacy runtime。

同时，Loop scope、checkpoint commit 边界、Merge/JoinAll、Service 输入和 Compiler bindings 仍未闭环。

**验收判定：不通过。Runner 可继续 experimental/opt-in；不可默认上线，不可删除 legacy runtime。**

## 2. R2 附录逐项验收矩阵

| R2 声明项 | 验收状态 | 结论 |
|---|---|---|
| P0-1 并行调度提前退出 | **通过** | fan-in/diamond 回归稳定通过 |
| P0-2 HITL 反向消费 | **通过** | 已删除共享 `resultCh` 反向读取 |
| P0-3 `NodeSpec.Timeout` | **部分通过** | 对响应 `ctx.Done()` 的 executor 生效；无法强制终止忽略 context 的函数 |
| P0-4 Error/Attempts | **通过** | 两次失败后 Error 非空、Attempts=2 |
| P0-5 `LoopNodes` 子计划 | **部分通过** | setup=1/body=3；但 Loop 重建 scope 导致输入和父 Result 身份丢失 |
| P0-6 SubWorkflow 父 State | **通过** | child 可读取父 `token`，连续 20 次通过 |
| P1-1 initial input/variables | **部分通过** | Runner option 存在；Loop 中丢失，Service 也未传入 |
| P1-2 Compiler Spec+Bindings | **部分通过** | 类型和捕获函数存在，但全仓无消费调用点，Service 仍用基础 compiler |
| P1-3 BranchOne/NotSelected | **通过** | 示例 pass=completed、fail=not_selected |
| P1-4 完整 checkpoint + Resume | **不通过** | checkpoint 是 pre-commit；Resume 不执行 pending 节点，也不校验 spec |
| Code Review 合规 | **通过** | `runner.go` 984 行；diff check/vet/lint 均通过 |
| `LoopSpec.UntilCondition` | **部分通过** | 运行时提前退出通过；closure 放回 IR 且 `json:"-"`，无法跨进程恢复 |
| ExecuteStream Runner 路径 | **部分通过** | 有 opt-in 分支；仍未注入 input/variables/PluginBus |
| api/graph 状态常量 | **通过** | 三个状态均已导出 |

统计：**7 项通过，5 项部分通过，1 项不通过；Timeout 需明确为协作式合同。** 更重要的是，R2 正文要求的生产入口和外围闭环仍未完成。

## 3. Critical / High 发现

### C1. [已动态复现] `ResumeExecution` 不会恢复执行

**位置：** `internal/workflow/runner.go:854-906`

实现恢复 State 和部分 NodeStatus 后直接：

```go
scope.MarkFinished()
return scope.ToResult(), nil
```

没有创建 Scheduler，没有恢复 ready/pending 集合，也没有调用 `execLoop`。定向 checkpoint 中 a=completed、b=pending；调用 Resume 后：

```text
b executed 0 times，期望 1
```

而 `ToResult` 只看 `scope.err`，因此 pending 节点仍存在时也可能把整个 workflow 标成 completed。

此外：

- 不比较 checkpoint `spec_id` 与传入 Spec；错误 Spec 被静默接受；
- 不恢复 node output、Attempts、StartedAt 等字段；
- 注释“scheduler skips already-completed nodes”与实现不符。

**影响：** 对外提供了虚假的恢复成功，可能造成任务永久漏执行，是生产数据一致性阻断项。

### C2. [已验证] 唯一生产运行时仍未成立

- Service：`api/service/workflow/service.go:132-155,362-380` 仍以 `UseRunner` 分流；bool 零值 false，默认进入 DynamicExecutor；
- Client/YAML：`api/client/workflow.go:22-38,63-70` 固定调用 `engine.Executor`；
- Graph：`api/graph/graph.go:60,66` 仍导出 legacy Graph/NewGraph；
- SubGraph：`internal/workflow/graph/node.go:326-340` 仍直接调用 `n.graph.Execute`。

因此三套 ready queue / indegree / checkpoint / status 语义仍可达，不能宣称 Single Runner。

### H1. [已动态复现] Checkpoint 仍在 node commit 前保存

**位置：** `internal/workflow/runner.go:820-851,914-947`；真正 commit 在 `runner.go:672-685` 附近的 `handleResult`。

`emitAfterStep` 在 goroutine 发送 `nodeResult` 之前调用 `saveRunnerCheckpoint`；此时调度主循环尚未执行 `SetNodeOutput + CommitState`。

动态快照：

```text
state = {input:""}
node a = running
```

缺少当前节点 output/completed。它不是 R2 声称的“完整 ExecutionScope 原子恢复点”。

### H2. [已动态复现] Loop scope 合同仍不完整

**位置：** `internal/workflow/runner.go:250-306`

每轮都以 `NewExecutionScope` 替换 scope，导致：

1. 首轮也丢失 `WithInitialInput/WithInitialVariables`；
2. 最后一轮使用 bodySpec 后，Result.SpecID 变成 `*.loop-body`；
3. 最终 NodeStates 丢失 setup 等非 body 节点。

动态结果：

```text
所有节点 input=<nil>
SpecID="loop-identity.loop-body"
setup 不在最终 NodeStates
```

所以 `LoopNodes` 的执行次数虽修复，完整 ExecutionScope 语义仍未修复。

### H3. [已验证] Service Runner 路径仍未注入请求上下文

**位置：** `api/service/workflow/service.go:167-196,208-245`

sync 和 stream 两处都只创建：

```go
workflow.NewRunner(exec, workflow.WithScheduleStrategy(workflow.ScheduleFIFO))
```

均未传：

- `WithInitialInput(req.Input)`；
- `WithInitialVariables(req.Variables)`；
- `WithPluginBus(s.config.PluginBus)`；
- CheckpointStore。

但 executor 又在 `service.go:184,223` 读取 `view.Get("input")`。因此启用 Runner 后 Agent 仍收到空字符串，而非请求输入。

### H4. [已验证] Compiler bindings 仍不可执行

**位置：** `internal/workflow/compiler.go:17-34,87-125,216-228`

`CompiledWorkflow` 和 `CompileFromEngineWithBindings` 已存在，但全仓只有定义，没有调用点。Service 仍调用 `CompileFromEngine`。

进一步问题：

- Condition 仍只把第一条入边改为 control-flow；
- RouterFuncs 没有 Runner adapter；
- graph condition 仍只是 `graph_closure_ref` marker；
- graph node 实例/binding 未进入编译结果。

因此“Spec+Bindings 完成”只能算数据结构完成，不能算生产语义保持完成。

### H5. [已验证] UntilCondition 修复重新破坏可序列化 IR

**位置：** `internal/workflow/spec.go:129-139`；`compiler.go:70-80`

`UntilCondition` 被直接放入 `LoopSpec`：

```go
UntilCondition func(...) bool `json:"-"`
```

这与统一 IR 的可序列化目标冲突；JSON/checkpoint/restart 后条件消失。与此同时 `CompiledWorkflow.UntilCondition` 又保存一份 closure，形成两个来源。

单进程测试中 iteration=2 可正确退出，但跨进程恢复不成立。正确方向应是 serializable expression 或严格位于 Bindings，不能塞回 IR。

### H6. [已动态复现] Merge / control-flow JoinAll 合同未完成

**位置：** `internal/workflow/scheduler.go:193-200,264-307`

- Merge：注释称每次 arrival 都触发，但 `enqueue` 对 completed 直接拒绝；两个 arrival 实测只执行一次；
- JoinAll：只检查 data-dependency 前驱，control-flow 前驱未完成时 join 已提前执行。

这两项不在 R2 附录中，却属于 `DAG_UNIFIED_PIPELINE.md` 的核心统一语义，仍阻断复杂 DAG 上线。

## 4. Timeout 合同说明

`executeSingle` 使用 `context.WithTimeout` 的实现是标准 Go 协作式取消：executor 必须监听 `ctx.Done()` 或把 ctx 传给下游调用。

动态结果：

- 响应 ctx 的 executor：约 10ms 后 failed，**通过**；
- 直接 `time.Sleep(100ms)` 且忽略 ctx 的 executor：约 101ms 后 completed，**失败**。

Runner 无法安全强杀任意 Go goroutine。因此应把合同明确写成“协作式 timeout”，并在 NodeExecutor/Agent/Tool binding 层保证 context 传播；不能宣称对任意 executor 强制生效。

## 5. 验证结果

### 现有仓库检查通过

```text
go test ./internal/workflow/...                       PASS
go test -race ./internal/workflow/...                 PASS
go test ./api/service/workflow/... ./api/client/...   PASS
go vet ./internal/workflow/... ...                    PASS
golangci-lint run ./internal/workflow/...             0 issues
go run ./examples/03-dag-workflow                     4/4 demos correct
git diff --check                                      PASS
```

### 定向合同通过

```text
LoopNodes setup=1/body=3
Error/Attempts
SubWorkflow 输出及父 State
shared Runner race
协作式 Node Timeout
UntilCondition iteration=2 提前退出
```

### 定向合同失败

```text
Resume pending node：执行 0 次
Resume mismatched spec：未拒绝
Loop initial input：nil
Loop Result SpecID：*.loop-body，setup 状态丢失
Checkpoint：当前节点仍 running、无 output
Merge：两个 arrival 只执行一次
JoinAll(control-flow)：前驱未完成即执行
非协作 executor Timeout：约 101ms 后 completed
```

## 6. 完成度与上线判断

按 R2 附录狭义清单，约一半可判定完全通过；按 `DAG_UNIFIED_PIPELINE.md` 的严格总体目标，当前仍约 **50%–55%**，Runner 核心约 **70%**。

当前可以：

- 继续完善 Runner；
- 在无 Loop/Resume/复杂 Merge-Join、且 executor 正确传播 context 的受控场景 opt-in 试用。

当前不可以：

- 默认打开 Runner；
- 把 `ResumeExecution` 暴露为可靠恢复能力；
- 删除 engine/graph；
- 宣称 Compiler 已语义保持；
- 宣称外围系统已统一闭环。

## 7. 最小修复顺序

### P0

1. 重写 Resume：校验 spec ID/version，恢复 completed/output/state，重建 Scheduler，只执行 pending/ready 节点；
2. checkpoint 移至 node commit + scheduler transition 之后；
3. 修复 Loop parent scope：保留 initial state、父 SpecID 和全量 NodeStates；
4. 修复 Merge 重入与“所有被激活前驱”的 JoinAll；
5. 将本轮失败 overlay 场景转为仓库强断言测试。

### P1

6. Service sync/stream 传 input/variables/PluginBus/CheckpointStore；
7. `CompiledWorkflow` 提供唯一可执行 adapter，无法保持 closure 时 fail-fast；
8. UntilCondition 改为 serializable expression 或只存在于 Bindings；
9. 明确 timeout 为协作式取消，并保证所有 production binding 传播 ctx。

### P2

10. 迁移 Client/YAML、Graph/SubGraph；
11. 接入 PatchQueue、ExecutionCollector、Trace/Metrics、Knowledge/Memory/Experience/Evolution；
12. 全入口通过同一 conformance suite 后再删除 legacy runtime。
