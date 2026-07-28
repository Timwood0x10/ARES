# 统一 DAG 管线代码审查

> 日期：2026-07-27  
> 范围：当前工作区未提交的 `internal/workflow` IR/Compiler/Validator/Runner/Scheduler/ExecutionScope，以及 `api/service/workflow`、`api/client/workflow`、`api/graph` 的生产接线。  
> 方式：只读静态审查；未修改项目源码。  
> 动态验证状态：尝试执行 `go test`、`go test -race` 和 `go vet`，但当前执行沙箱未连接，命令未实际启动。因此下列结论均为逐行源码验证，测试结果仍待本地补跑。

## 结论

**当前实现是 P1/P2 原型，不是已经完成的“统一生产管线”。暂不建议启用 `UseRunner`，更不能删除旧运行时。**

最严重的问题不是缺少外围增强，而是 Runner 核心调度在并行 DAG 上会静默漏执行下游节点；条件表达式没有任何公开绑定入口；服务输入没有注入 ExecutionScope。即便简单测试通过，也不能证明生产正确性。

| 级别 | 发现 | 关键证据 |
|---|---|---|
| Critical | 并发批次完成后，后续 ready 节点可能永远不派发 | `internal/workflow/runner.go:217-242` |
| Critical | 条件求值器不可配置，所有非 nil 条件实际恒 false | `runner.go:87,163-169`; `scheduler.go:43-45,223-227` |
| Critical | “单一生产路径”尚未成立，至少四条旧执行路径仍然可达 | `service.go:133-155,280-358`; `api/client/workflow.go:22,36,68`; `api/graph/graph.go:64-71` |
| High | Runner 服务路径没有注入请求 input，且忽略 Step.Input | `service.go:179-190`; `scope.go:175-187` |
| High | 未注册节点 executor 被静默视为成功 | `runner.go:59-68` |
| High | engine/graph 编译器丢失条件、Router、UntilCondition 与真实 Node binding | `compiler.go:111-135,154-163,193-222` |
| High | `MaxParallel` 完全未生效，ready 节点无界并发 | `spec.go:137-141`; `runner.go:248-278` |
| High | Loop 每轮重置全部状态、重复整个工作流，并忽略 LoopNodes/UntilCondition | `runner.go:171-188`; `compiler.go:154-163` |
| High | 节点输出未进入普通 State，节点结果 Output/Error/Attempts 也未填充 | `scope.go:110-114,281-295,332-366` |
| High | SubWorkflow 只编译、不执行 | `compiler.go:80-87`; `runner.go:331-381`; `service.go:174-192` |
| Medium | Merge 语义声明为可重复触发，但 Scheduler 明确阻止重复入队 | `types.go:84-86`; `scheduler.go:190-197,259-298` |
| Medium | BranchOne fallback 依赖 edge 顺序，且不检测多条件同时命中 | `scheduler.go:221-235`; `validate.go:246-290` |
| Medium | ControlFlow-only JoinAll 可能在首条控制边满足时提前执行 | `scheduler.go:273-287` |
| Medium | Timeout、HITL timeout/auto action、Interrupted/Resume 尚未实现 | `spec.go:50-57,117-126`; `runner.go:282-381` |
| Medium | Checkpoint、PluginBus、PatchQueue、Evolution/Knowledge/Memory/Experience/Observability 未接入 Runner | `runner.go:82-88`; `service.go:194-196` |
| Medium | Conformance tests 多处弱断言，无法阻止上述回归 | `runner_test.go:96-104,118-165,313-318,381-390` |

---

## Critical

### C1. [已验证] 并行 DAG 会静默漏执行后续节点

**位置：** `internal/workflow/runner.go:217-242`

主循环每轮先派发所有 ready 节点，然后只等待一个结果：

```go
dispatched := r.dispatchReady(...)
if dispatched == 0 {
    break
}
res := <-resultCh
r.handleResult(res, scope, sched)
```

假设 `A`、`B` 并行，`C` 为 `JoinAll(A,B)`：

1. 首轮派发 A/B，等待 A 完成；
2. A 完成后 C 仍未 ready；
3. 下一轮 `dispatched == 0`，主循环退出；
4. drain 阶段收到 B，`OnNodeCompleted(B)` 把 C 放入 ready queue；
5. drain 阶段不会再次调用 `dispatchReady()`，C 永远不执行，最终被标为 `unreachable`。

**影响：** 常见 fan-out/fan-in、并行链路和多层 DAG 会返回“成功但少跑节点”的错误结果。

**修复：** 显式维护 `running` 数量。终止条件只能是 `ready == 0 && running == 0`；每收到一个 result 后递减 running，并继续调度新 ready 节点。为上述 A/B→C 场景增加确定性测试（使用 channel 控制 A 先完成、B 后完成）。

### C2. [已验证] 条件表达式在 Runner 中实际上不可用

**位置：**

- `internal/workflow/runner.go:87,163-169`
- `internal/workflow/scheduler.go:43-45,223-227`

Runner 有私有字段 `condEvaluator`，但没有任何 `RunnerOption` 或公开方法对其赋值。Scheduler 明确规定 nil evaluator 时所有非 nil 条件不满足。因此从包外调用 `NewRunner`/`RunWorkflow` 时，所有带 `Cond` 的边恒为 false。

现有 BranchOne 测试没有配置 evaluator，却只检查 `pass != not_selected`；`unreachable` 也能通过该断言（`runner_test.go:96-104`）。

**影响：** 条件路由、BranchOne、BranchMany 和由 graph 编译来的条件边均无法按表达式运行。

**修复：** 提供明确的 `ConditionEvaluator` 接口和 `WithConditionEvaluator`；内置并校验支持的表达式类型；缺少 evaluator 时在 Validate/Execute 阶段失败，不能静默按 false 处理。

### C3. [已验证] 尚未形成唯一生产运行时

**生产可达证据：**

- 同步 Service 默认仍走 DynamicExecutor；`UseRunner` 的 Go 零值是 false：`api/service/workflow/service.go:43-47,133-155`。
- `ExecuteStream` 无 Runner 分支，固定走 DynamicExecutor：`service.go:280-358`。
- WorkflowClient 固定持有并调用静态 `engine.Executor`：`api/client/workflow.go:22,36,68`。
- 公共 `api/graph` 仍直接 alias `graph.Graph`，调用者继续使用 `Graph.Execute`：`api/graph/graph.go:58-71`。
- SubGraphNode 内部直接递归调用旧 `Graph.Execute`：`internal/workflow/graph/node.go:326-340`。

**影响：** 相同工作流仍会根据入口得到不同条件、状态、Router、Checkpoint 与插件语义；“统一 IR + 单 Runner”的终态尚未达到。

**修复：** 先把 Runner 做到与旧能力等价，再按入口逐条迁移：同步 Service → Stream → Client/YAML → public Graph/SubGraph；迁移期明确标注实验开关，不能在注释中宣称已替代全部生产路径。

---

## High

### H1. [已验证] Service Runner 路径向所有 Agent 传入 nil

`executeWithRunner` 从 `view.Get("input")` 读取输入（`service.go:184`），但 `NewExecutionScope` 只创建空 state（`scope.go:175-187`），`Runner.Execute` 也没有 initial state/input 参数。`req.Input` 与 workflow variables 从未写入 scope；`NodeSpec.Input` 也没有解析或使用。

**影响：** 要求字符串输入的 Agent 会失败；接受 nil 的 Agent 也会收到错误业务输入。各节点的 Step.Input 模板语义全部丢失。

**修复：** Execute 接口接收不可变 initial state；prepare 阶段写入 request input/variables；每个节点执行前解析 `NodeSpec.Input`，并显式决定前驱输出的映射规则。

### H2. [已验证] 缺失 binding 会“假成功”

`FuncNodeExecutor.ExecuteNode` 对未注册 NodeID 返回空 map 和 nil（`runner.go:59-68`）。IR 没有结构节点的显式类型，因此无法区分“合法 gate”与“漏绑 Agent/Tool/SubWorkflow”。

**影响：** 拼写错误、SubWorkflow、graph node binding 丢失均可能显示 completed，掩盖实际未执行。

**修复：** NodeSpec 增加明确 BindingKind/StructuralKind；普通执行节点缺 binding 必须在 Validate/prepare 阶段报错。结构节点由专用 executor 处理。

### H3. [已验证] 编译器不是语义保持转换

- engine Condition 仅把第一条入边改成 ControlFlow，却没有设置 `Cond` marker（`compiler.go:116-125`）；root Condition 完全丢失，多依赖 Condition 被错误拆分。
- Router 仅 `_ = step.Router`，IR 无任何 router binding（`compiler.go:128-135`）。
- `UntilCondition` 未进入 LoopSpec（`compiler.go:154-163`）。
- graph 条件仅变成不可执行的字符串 marker（`compiler.go:249-259`），且 graph Node 的类型/实例/binding 不进入 IR（`compiler.go:193-207`）。

**影响：** “编译成功”不等于行为等价，迁移旧 workflow 后会静默改变执行路径。

**修复：** Compiler 返回 `CompiledWorkflow{Spec, Bindings}` 或在编译期明确拒绝无法保持的 closure；不能只写 marker 后继续执行。

### H4. [已验证] MaxParallel 是无效配置

IR 声明 `Schedule.MaxParallel`（`spec.go:137-141`），Service 也有 `Config.MaxParallel`，但 Runner 的 `dispatchReady` 会把 ready queue 全部启动为 goroutine（`runner.go:248-278`），没有 semaphore 或运行计数上限。

**影响：** 大图可能瞬间启动大量 Agent/Tool 调用，突破 API 配额、内存和下游限流；编译器设置的 `MaxParallel: 1` 同样不生效。

**修复：** Scheduler/Runner 共享并发预算；每次最多派发 `maxParallel-running` 个节点；校验负值并定义 0 的默认语义。

### H5. [已验证] Loop 实现不符合 LoopSpec

Runner 每轮新建整个 scope 和 scheduler（`runner.go:171-188`）：

- 前一轮 state/output 全部丢失；
- 每轮重复整个工作流，不只运行 `LoopNodes`；
- 最终只返回最后一轮结果；
- 无法执行 UntilCondition；Compiler 也丢弃它。

**影响：** 依赖跨轮状态的迭代、反思、自修正流程失效；非循环节点被重复调用。

**修复：** scope 跨轮持久；为 loop body 建立明确子计划；每轮保存 iteration checkpoint；条件在 safe point 求值；定义 loop 输出合并规则。

### H6. [已验证] State 与 Result 契约未完成

- 节点返回 map 只存入 `nodeOuts`，不会写入普通 state key：`scope.go:110-114,281-285`。
- `NodeStatusValue.Output` 从未赋值，所以 Service 的 StepResult.Output 通常是 `<nil>`：`service.go:213-220`。
- `NodeStatusValue.Error` 从未赋值：`scope.go:287-295`。
- `Attempts` 从未更新。
- `GetNodeOutput`、NodeStates、snapshot 只做浅层/指针返回，与“deep copy/transactional isolation”注释不符。

现有 transactional test 读取 `shared_key` 后不断言值，最后 `_ = observedValue`（`runner_test.go:138-165`），因此没有证明输出传播。

**修复：** 冻结输出到 state 的规范；完成 Output/Error/Attempts 赋值；所有暴露 snapshot 做不可变复制或文档化所有权。

### H7. [已验证] SubWorkflow 只存在于 IR，没有执行语义

Compiler 会递归生成 `NodeSpec.SubWorkflow`，但 Runner 始终只调用通用 `NodeExecutor.ExecuteNode`，没有 SubWorkflow 分支；Service 又只为顶层 `wf.Steps` 注册 closure。

**影响：** SubWorkflow 节点若未注册会触发 H2，直接空输出成功；子流程完全没有执行。

**修复：** Runner 原生识别 SubWorkflow binding，派生 child scope、继承/隔离 state、传播取消与 checkpoint，并定义结果 merge。

---

## Medium

### M1. [已验证] Merge 不可能重复执行

`Merge` 文档要求每次 activation 都触发（`types.go:84-86`），但 `evaluateTarget` 开头若 completed 直接返回，`enqueue` 也拒绝 completed 节点（`scheduler.go:190-197,259-298`）。

### M2. [已验证] BranchOne 语义与声明不一致

Scheduler 取 slice 中第一个满足项并 break；无条件 fallback 若排在前面会抢占条件分支。Validator 无法检测多条件重叠，也不要求 fallback（`scheduler.go:221-235`; `validate.go:246-290`），与 `types.go:62-66` 的契约不一致。

### M3. [已验证] ControlFlow JoinAll 只检查 data predecessor

`evaluateTarget` 的 JoinAll 只遍历 DataDependency（`scheduler.go:273-287`）。一个拥有多个 control-flow 入边的 join，第一条满足时可能因“没有 data predecessor”直接入队，无法表达“所有已激活控制前驱完成”。

### M4. [已验证] 超时/HITL/恢复字段只定义了形状

`NodeSpec.Timeout`、`InterruptSpec.TimeoutSec/AutoAction` 未在 Runner 使用；节点也从未进入 `Interrupted`。HITL rejection 被当作普通 failed，当前没有可恢复的 pause/resume 状态。RecoverySpec 的 Strategy 也不驱动行为，仅依赖外部 handler。

### M5. [已验证] 外围闭环仍停留在旧运行时

Runner 只有 executor/strategy/interrupt/recovery/condition 五类依赖（`runner.go:82-88`）。Service Runner 构造时只传 executor 与 FIFO（`service.go:194-196`）。旧 DynamicExecutor/Graph 中已有的 PluginBus、Checkpoint、PatchRegistry、ExecutionCollector、Memory/Evolution hook 没有迁移。

### M6. [已验证] Validator 仍允许大量运行期歧义

未拒绝空 workflow、重复边、非法 enum、非法 MaxParallel/Retry/Timeout/ConditionExpr；join 只统计 data edges；BranchOne 没有完整性校验。`validateLoop` 的条件实际上只拒绝负数，与文案较绕（`validate.go:324-350`）。

### M7. [已验证] graph 重复 Node ID 仍静默覆盖

`Graph.Node` 直接执行 `g.nodes[id] = node`（`internal/workflow/graph/graph.go:126-140`）。Compiler 的 duplicate 检查无法挽回，因为 map 中旧节点已丢失。

### M8. [已验证] 新增了两套未接通的执行接口

`ares_runtime.Executable` 使用 `ExecutionContext/NodeOutput`，新 Runner 使用独立的 `workflow.NodeExecutor/ExecutionScope`；graph Node 只留下 TODO，没有 adapter（`internal/ares_runtime/executable.go:23-39`; `internal/workflow/graph/node.go:21-31`）。这会继续扩大而不是收敛执行抽象。

---

## 测试质量审查

现有测试主要证明“代码能走完”，没有形成统一语义 gate：

1. BranchOne：条件 evaluator 不存在，断言仍允许错误状态（`runner_test.go:96-104`）。
2. Transactional State：不检查 reader 实际读到任何值（`runner_test.go:159-165`）。
3. Cancellation：无论 err/status 都通过（`runner_test.go:313-318`）。
4. HITL rejection：状态错误只 `Logf`，不失败（`runner_test.go:381-390`）。
5. Loop compiler test 创建了 UntilCondition，但不检查其是否保留（`spec_test.go:185-213`）。
6. 缺少确定性覆盖：并行多层 DAG、MaxParallel、Merge、control-flow join、fallback 顺序、missing binding、initial input、timeout、SubWorkflow、checkpoint/resume。

建议把 `conformance_test.go` 从“记录三套旧行为”拆为一组 table-driven 合同，先让 Runner 严格通过；兼容编译器分别验证 legacy 输入编译后的同一合同。

## 已验证安全/正确的部分

- IR 已把 Node/Edge、Branch/Join、Retry/Recovery/Interrupt/Schedule 基本形状集中到一个包，方向正确。
- Validator 能发现重复 Node ID、悬空边、显式 entry 不存在和普通 cycle。
- ExecutionScope 对顶层 map 访问使用锁；Scheduler 当前由 Runner 主 goroutine调用，没有直接并发写 map 的证据。
- Retry backoff 有最大延迟并响应 context cancellation。
- Service 的 legacy DynamicExecutor 路径仍保留 PluginBus 与 MaxParallel，未被此次改动直接破坏。

## 建议修复顺序

### P0：先把 Runner 做正确

1. 修复 running/ready 终止条件；加入 A/B→C 确定性回归测试。
2. 加 ConditionEvaluator 公共接口，缺失 evaluator 时 fail-fast。
3. 注入 initial input/variables，解析 NodeSpec.Input。
4. missing binding fail-fast；实现结构节点和 SubWorkflow 的显式 binding。
5. 实现 MaxParallel。
6. 完成 Output/Error/Attempts 与 state merge 契约。

### P1：冻结复杂语义

7. 重做 Loop、Merge、BranchOne fallback/冲突、control-flow Join。
8. 实现 node timeout、HITL interrupted/resume、recovery strategy。
9. 编译器改为 `Spec + Bindings`，无法保持的 closure 明确报错。

### P2：迁移生产入口与外围闭环

10. Runner 接入 PluginBus、Checkpoint/Resume、PatchQueue safe point、Collector/Tracing、Knowledge/Memory/Experience/Evolution。
11. 依次迁移同步 Service、Stream、Client/YAML、public Graph/SubGraph。
12. 所有入口通过同一 conformance suite 后，再删除旧执行循环。

## 待补动态验证

当前环境未能启动命令。修复执行环境后至少运行：

```bash
go test ./internal/workflow/...
go test -race ./internal/workflow/...
go test ./api/service/workflow/... ./internal/workflow/graph/... ./internal/workflow/engine/...
go vet ./internal/workflow/... ./api/service/workflow/...
go test ./...
```

在上述命令和新增确定性语义测试通过前，不应把 `UseRunner` 设为默认 true。
