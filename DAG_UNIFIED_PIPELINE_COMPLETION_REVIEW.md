# 统一 DAG 管线完成度复审

> 日期：2026-07-28  
> 基准：`DAG_UNIFIED_PIPELINE.md`、`DAG_UNIFIED_PIPELINE_CODE_REVIEW.md`  
> 范围：当前工作区未提交的统一 IR、Compiler、Validator、Runner、Scheduler、ExecutionScope，以及 Service / Client / Graph 生产入口与外围闭环。  
> 审查方式：只读源码、执行现有测试、使用 Go overlay 注入临时回归测试；未修改项目源码。

## 1. 结论

**当前约完成 31%，定位仍是 P1/P2 原型，不是可启用的统一生产管线。**

本轮相对上次有两项有效进展：

1. 新增 `WithConditionEvaluator`，解决了条件求值器完全不可配置的问题；
2. 节点输出现在会写入 `NodeStatusValue.Output` 和普通 State，State/Result 契约得到部分补全。

但最危险的 Runner 并行调度错误仍未修复，并已通过确定性动态测试复现：`A/B → Join C` 中 C 执行 0 次。`MaxParallel=1` 也实测出现峰值并发 2。当前现有测试、race、vet 和全仓测试虽然通过，但测试集没有覆盖这两个核心合同，因此不能据此判定 Runner 正确。

**上线判断：不通过。暂不应将 `UseRunner` 设为默认 true，不应删除任何旧运行时。**

## 2. 完成度计算

完成度按设计阶段加权，不按文件数或代码量计算：

| 阶段 | 权重 | 阶段完成度 | 加权得分 | 判断 |
|---|---:|---:|---:|---|
| P0 语义合同与金丝雀测试 | 15% | 40% | 6.0% | 类型已定义，但复杂语义和强测试未冻结 |
| P1 IR / Compiler / Validator / Explain | 20% | 55% | 11.0% | IR 骨架可用；Compiler 非语义保持；无 Explain |
| P2 Single Runner | 30% | 35% | 10.5% | linear/retry/基础 HITL 可跑；并行、限流、loop、subflow、checkpoint 未完成 |
| P3 生产入口与外围闭环 | 25% | 12% | 3.0% | Service 仅有 opt-in 支路；其他入口仍走旧运行时；外围未接 Runner |
| P4 删除旧路径 | 10% | 0% | 0% | 旧执行循环全部仍可达 |
| **总计** | **100%** |  | **30.5% ≈ 31%** | **未达到生产验收** |

此百分比表示对 `DAG_UNIFIED_PIPELINE.md` 最终验收标准的完成程度，不表示代码编写进度。

## 3. 上轮 Critical / High 修复矩阵

| 上轮问题 | 当前状态 | 证据 |
|---|---|---|
| C1 并行调度提前退出 | **未修复，动态复现** | `runner.go:234-259`；overlay 测试 C 执行 0 次 |
| C2 Condition evaluator 无公开入口 | **已修复** | `runner.go:138-145,180-185` 新增并接入 `WithConditionEvaluator` |
| C3 单生产运行时未成立 | **未修复** | `service.go:133-155,305-358`; `api/client/workflow.go:24,38,70`; `api/graph/graph.go:65-66`; `graph/node.go:340` |
| H1 Service input 未注入 | **未修复** | `service.go:184` 读取 `input`，但 `runner.go:171-173` 创建空 Scope；无 initial state API |
| H2 missing binding 假成功 | **未修复** | `runner.go:59-68` 未注册 executor 返回空 map、nil |
| H3 Compiler 不保持 Condition/Router/Loop/binding | **未修复** | `compiler.go:117-134,178-186,249-259` |
| H4 MaxParallel 无效 | **未修复，动态复现** | `runner.go:267-295` 派发全部 ready；overlay 实测 1→2 |
| H5 Loop 重置 Scope、忽略 LoopNodes/UntilCondition | **未修复** | `runner.go:188-205` 每轮重建整个 Scope/Scheduler |
| H6 State/Result 契约缺失 | **部分修复** | `scope.go:281-295` 已写 Output/State；Error/Attempts/不可变快照仍缺失 |
| H7 SubWorkflow 只编译不执行 | **未修复** | `compiler.go:81-86` 只写 IR；Runner 无 `SubWorkflow` 分支 |

统计：10 项中 **1 项已修复、1 项部分修复、8 项未修复**。

## 4. Critical 发现

### C1. [已验证 + 已复现] 并行 fan-out/fan-in 静默漏执行

**位置：** `internal/workflow/runner.go:223-263`

```go
dispatched := r.dispatchReady(...)
if dispatched == 0 {
    break
}
res := <-resultCh
r.handleResult(res, scope, sched)
```

Runner 没有维护 `running` 数量。首轮派发 A/B 后只收一个结果；下一轮 ready 为空就退出。drain B 时虽然 C 被加入 ready queue，却不再派发。

临时 overlay 回归测试结果：

```text
TestReviewParallelFanInExecutesJoin
join c executed 0 times
FAIL
```

**影响：** 常见 diamond、fan-in、多层并行 DAG 会“返回成功但少跑节点”。这是生产阻断项。

**必须修复：** 终止条件改为 `ready == 0 && running == 0`；每个 result 将 running 减一，并在处理结果后继续派发新 ready 节点。

### C2. [已验证] 仍存在至少四套生产可达执行路径

1. Service 同步路径：`UseRunner` 零值 false，默认仍走 DynamicExecutor：`api/service/workflow/service.go:43-47,133-155`；
2. Service Stream：固定 `ExecuteDynamic`：`service.go:305-358`；
3. Client/YAML：固定 `engine.Executor`：`api/client/workflow.go:24,38,70`；
4. Public Graph / SubGraph：旧 `Graph.Execute` 仍公开且内部递归：`api/graph/graph.go:65-66`; `internal/workflow/graph/node.go:327-340`。

`api/graph` 增加 Runner re-export、旧类型加 Deprecated 注释，只提高了新 API 的可发现性，不等于切换执行链。

**影响：** 同一个逻辑工作流仍按入口获得不同的 branch、state、checkpoint、plugin 和 recovery 语义。

## 5. High 发现

### H1. [已验证 + 已复现] `MaxParallel` 只是配置形状

**位置：** `internal/workflow/runner.go:267-295`

`dispatchReady` 清空整个 ready queue并逐个启动 goroutine，未读取 `spec.Schedule.MaxParallel`。

临时 overlay 测试结果：

```text
TestReviewMaxParallelOne
MaxParallel=1 but observed peak concurrency 2
FAIL
```

**影响：** Agent/Tool 调用可突破限额、下游配额和资源预算。

### H2. [已验证] Service Runner 向 Agent 传错输入

`executeWithRunner` 在 `service.go:184` 执行 `view.Get("input")`，但 `Runner.Execute` 只创建空 Scope（`runner.go:171-173`）。`req.Input`、variables 和 `NodeSpec.Input` 均未注入或解析。

**影响：** opt-in Runner 路径会向 Agent 传 nil，迁移后业务行为改变。

### H3. [已验证] 缺失 binding 继续假成功

`FuncNodeExecutor.ExecuteNode` 在 `runner.go:63-67` 对未注册节点返回空输出和 nil。`edge_test.go` 还把“无注册函数成功”写成预期，固化了问题。

**影响：** Agent 拼写错误、graph binding 丢失、SubWorkflow 未执行都可显示 completed。

### H4. [已验证] Compiler 仍不是语义保持转换

- engine Condition：只把第一条入边改成 control flow，没有可执行 `Cond`；root/multi-dependency 条件丢失：`compiler.go:113-125`；
- Router：仅 `_ = step.Router`：`compiler.go:128-135`；
- UntilCondition：未进入可执行 loop 语义；
- graph closure：仅写 `graph_closure_ref` 字符串：`compiler.go:249-259`；
- graph Node 类型、实例和 binding 不进入编译结果。

注释声称“调用方通过 Bindings 重连”，但 API 只返回 `*WorkflowSpec`，不存在 bindings 结果。

### H5. [已验证] Loop 仍重复整个工作流并丢失跨轮状态

`runner.go:188-205` 每轮重建 Scope 和 Scheduler：

- 前一轮 state/output 丢失；
- 忽略 `LoopNodes`，每轮运行整个 spec；
- 没有 UntilCondition；
- 只返回最后一轮 Scope。

示例通过闭包变量 `iteration` 计数，不能证明统一 State 的跨轮语义成立。

### H6. [已验证] SubWorkflow 仍不会由 Runner 执行

Compiler 递归填充 `NodeSpec.SubWorkflow`，但 Runner 只调用通用 `NodeExecutor`，没有 child scope、输入继承、结果 merge、取消传播或 checkpoint 语义。missing binding 又会让该节点空成功。

### H7. [已验证] 新条件示例与实际结果矛盾

运行：

```text
go run ./examples/03-dag-workflow
```

输出同时出现：

```text
pass (completed)
fail (completed)
```

但示例声称 `score=85` 应走 pass、否则走 fail。两条 control edge 未设置同一个 `BranchOne` group，无条件 fail 边因此也被激活。

**影响：** 示例会教给调用者错误的 fallback 写法，同时证明 Branch 合同尚未冻结。

## 6. Medium 及契约缺口

1. **State/Result 仅部分补全：** `SetNodeOutput` 已写 `Output` 和普通 State，但平铺输出 key 会产生并发同名覆盖；`NodeStatusValue.Error`、`Attempts` 仍不更新；`GetNodeOutput`/`NodeStates` 返回内部引用；`StateSnapshot` 注释称 deep copy，实际是浅拷贝。
2. **Merge 不可重复触发：** `scheduler.go:260` 和 `enqueue:192` 都拒绝 completed target，与 `types.go:84-86` 冲突。
3. **BranchOne 依赖 edge slice 顺序：** `scheduler.go:229-234` 取第一个满足条件；无条件 fallback 放前面会抢占条件分支，也不检测多条件同时命中。
4. **ControlFlow JoinAll 不完整：** `scheduler.go:274-286` 只检查 data predecessor。
5. **Node timeout 未使用：** `NodeSpec.Timeout`、HITL timeout/auto-action 没有 Runner 逻辑；HITL rejection 仍作为 failed，而非 resumable interrupted。
6. **公共 API 不完整：** `api/graph` 未导出 `NotSelected/Unreachable/Blocked` 和主要 Runner options，且同时暴露新旧两套构建/执行 API。
7. **Validator 覆盖不足：** 未拒绝空 workflow、重复边、非法 enum、非法 `MaxParallel`、invalid condition、missing binding、state 冲突、unsupported capability；无 Explain。
8. **两套新执行接口继续漂移：** `ares_runtime.Executable.Execute(*ExecutionContext)` 与 `workflow.NodeExecutor.ExecuteNode(*ExecutionScope)` 并存；graph Node 只有 TODO adapter。
9. **未跟踪构建产物：** 根目录 `03-dag-workflow` 是 `Mach-O 64-bit executable arm64`，不应进入源码交付。

## 7. 外围闭环验收

Runner 当前依赖只有 executor、schedule strategy、interrupt handler、recovery handler、condition evaluator（`runner.go:82-88`）。以下能力仍在 legacy DynamicExecutor/Graph 中，未进入统一 Runner：

| 能力 | Runner 状态 | 结论 |
|---|---|---|
| PluginBus / BeforeNode / AfterNode | 未接入 | 未完成 |
| Checkpoint / Resume / 原子版本 | 仅注释声称，代码无依赖 | 未完成 |
| PatchQueue / safe point / typed mutation | 未接入 | 未完成 |
| ExecutionCollector / route decision | 未接入 | 未完成 |
| Observability / trace / metrics / events | 未接入 | 未完成 |
| Knowledge prepare / context | 未接入 | 未完成 |
| Memory Before/AfterNode | 未接入 | 未完成 |
| Experience terminal distillation | 未接入 | 未完成 |
| Evolution evaluate / apply | 仍在 DynamicExecutor | 未完成 |
| Recovery | 仅外部 callback，未执行 `RecoverySpec.Strategy` | 部分 |
| HITL | 同步 callback，可批准/拒绝 | 部分；无持久化 resume |

因此 `DAG_UNIFIED_PIPELINE.md:584-602` 的“每次生产执行可关联完整闭环”验收项当前基本为 0。

## 8. 动态验证结果

### 通过

```text
go test ./internal/workflow/...                         PASS
go test -race ./internal/workflow/...                   PASS
go test ./api/service/workflow/... ./api/graph/...      PASS
go vet ./internal/workflow/... ./api/service/workflow/... ./api/graph/...  PASS
go test ./...                                           PASS
go run ./examples/03-dag-workflow                       EXIT 0
```

### 失败（临时 overlay 语义回归，不改仓库）

```text
TestReviewParallelFanInExecutesJoin  FAIL: join c executed 0 times
TestReviewMaxParallelOne             FAIL: MaxParallel=1, peak concurrency 2
```

### Benchmark（Apple M3 Max, darwin/arm64）

| Benchmark | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| Runner Linear3 | 16,526 | 6,806 | 71 |
| Legacy Executor Linear3 | 13,853 | 8,321 | 110 |
| Runner Linear10 | 40,536 | 24,845 | 215 |
| Legacy Executor Linear10 | 47,428 | 27,181 | 388 |

Runner 在线性 10 节点上有较好趋势，但 benchmark 只覆盖 linear，且核心并行语义尚错误，暂不能形成性能结论。

## 9. 上线门槛与修复顺序

### P0：必须先完成

1. 修复 `ready/running` 调度终止条件；把 overlay 的 A/B→C 场景转成仓库确定性测试；
2. 实现并强测 `MaxParallel`；
3. 为 Runner 增加 initial input/variables，并执行 `NodeSpec.Input` 映射；
4. missing binding fail-fast，显式区分 executable node 与 structural node；
5. 完成 `Output/Error/Attempts` 和冲突可控的 state merge；
6. 修正条件示例，冻结 BranchOne fallback、重叠条件和 NotSelected 状态。

### P1：复杂语义

7. 重做 LoopNodes/UntilCondition/跨轮 Scope；
8. 实现 SubWorkflow child scope；
9. 修正 Merge、control-flow Join、timeout、HITL interrupted/resume；
10. Compiler 返回 `Spec + Bindings`，无法保持 closure 时明确失败；补 Explain。

### P2：统一生产与闭环

11. Runner 接入 PluginBus、Checkpoint/Resume、PatchQueue、Collector/Trace、Knowledge/Memory/Experience/Evolution；
12. 依次迁移同步 Service、Stream、Client/YAML、Graph/SubGraph；
13. 所有入口通过同一强 conformance suite 后，再删除旧 ready queue / indegree 循环。

## 10. 最终判断

这次实现已经证明了统一 IR + Runner 的方向可落地，也补出了基本 API 和线性执行骨架；但当前仍未跨过“生产执行器正确性”门槛。特别是并行漏执行和并发预算失效都已动态复现，且输入、binding、compiler、loop、subworkflow 和外围闭环仍有系统性缺口。

**建议把当前版本标记为 experimental prototype。先完成 P0 六项，再做下一轮完成度验收；在此之前不要打开默认 Runner，也不要开始 P4 删除。**
