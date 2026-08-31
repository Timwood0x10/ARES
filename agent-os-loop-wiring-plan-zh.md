# Agent OS 控制闭环接线开发计划（0.3.x）

> **执行状态（2026-08-30）：W-L1 + K1–K3 已落地。** 评审修正已全部并入实现：
> ① 注册时序改为 **Register 先于 `bus.Start`**（`Register` 在 Start 后返回
> `ErrBusAlreadyStarted`，且插件仅在 `Start` 里拿到 bus 引用——原顺序双重失效）；
> ② 边界判定顺序改为**先 `OnRoundEnd(round)` 结算、再 `ShouldExecuteRound(round+1)`
> 门控**（否则 MaxIterations 吞掉最后一轮的结算）；
> ③ 边界判定用 `atomic.AddInt64` 返回值取模；**轮次预算从 `count` 推导**
> （`round > maxRounds` 直接丢弃），`atomic.Bool` 停止标志退化为快速路径——
> 标志是 read-then-set，并发边界调用会全部先读到"未停止"再各自结算，
> 实测 `max_iterations=1` 超发到 3 轮；
> ④ `executionID` 用轮次自身标识 `kernel-round-<N>`（一轮跨多个任务，
> 边界任务的 taskID 只 flush 偶然撞线的那个执行体）；
> ⑤ W-L1 本期只建立节拍，验收改为可证伪形式（注册 fake `CapCheckpoint`
> Flusher → 断言 `Flush` 被调用，见 `cmd/ares/runtime_bridge_loop_test.go`；
> 下游 Checkpoint/Memory/Evolution 插件注册另立一项）；
> ⑥ 删除集扩展：K2 并入 `NodeRouter` + `Step.Router`，K3 并入 `StepFailure`
> （`types.go` 的 `context` import 随之删除）；**`Step.SubWorkflow` 一并删除**
> ——零执行器消费、graph 侧无任何子图实现，与 `LoopConfig` 同性质死声明；
> ⑦ 门禁 6.1/6.2 已另立落地（2026-08-31，白名单起步）：
> 6.1 见 `internal/ares_runtime/architecture_test.go`（7 个 test-only 插件构造器入白名单，
> `NewLoopPlugin` 已接线不入）；6.2 见 `internal/workflow/engine/architecture_test.go`
> （死类型白名单：`InterruptConfig`/`WorkflowExecution`/`StepState`/`WorkflowStatus`/
> `WorkflowResult`——实测 `DAGNode` 是活的，`mutable_dag.go` 在用）。两个门禁都有
> STALE 反向检测：白名单条目一旦获得生产引用即红，逼迫消化；目标态是白名单清空。
> 门禁 6.3（kernelscheduler 禁 import runtime 插件包）已落地，见
> `internal/kernelscheduler/architecture_test.go`；
> ⑧ examples/docs 改写按"先改 `accuracy_test.go` 关键词表再改文案"执行，
> 实际涉及 main.go:107/137、benchmark_test.go 26/31/35/37/203/227/239、
> large_scale_test.go 84-86、docs zh/en 04 恢复章节、zh/en 14 §10.8。
>
> 原文各节保留如下（行号为成稿时快照，与当前工作区有偏移，以实现为准）。

> 目标版本：`VERSION = 0.3.1`（Agent OS / no-leader Kernel 调度）
> 范围：只处理"轮次闭环（loop）与步骤恢复（step recovery）"这一簇**建而未接**的接口。
> 原则：每项二选一——**接到 Kernel**（wire）或**删除**（kill）。不允许"挂着但没用"。
> 红线：不得引入"某 agent 决定另一 agent 做什么"的派发控制流（leader-sub 复辟）。一切执行经由
> `kernelscheduler.Scheduler` 的 `Schedule → Acquire → RunQuantum → finalize`。

---

## 0. 只有三个合法接入点

| 接入点 | 文件:行 | 用途 |
|--------|---------|------|
| `Scheduler.WithQuantumHook(QuantumHook)` | `internal/kernelscheduler/quantum_hook.go:22` / `:47` | 观测/推进 quantum 边界（`BeforeQuantum`/`AfterQuantum`）|
| `startPluginBus` → `sched.WithQuantumHook(newPluginBusHook(bus))` | `cmd/ares/runtime_bridge.go:73` / `:83` | runtime 插件生态唯一入内核通道 |
| `runKernelRecoveryLoop` | `cmd/ares/kernel_loop.go:227`（装配于 `cmd/ares/peer_mode.go:283`）| 事件驱动 + 周期 sweep 的恢复闭环 |

装配主线：`createPeerAgents`（`peer_mode.go:70`）→ `NewKernelScheduler`（`:151`）→ `startPluginBus`（`:280`）→ `go sched.Run`（`:282`）→ `go runKernelRecoveryLoop`（`:283`）。
**新接线只能挂在这条链上**，不得改接 `ares_bootstrap.Bootstrap` 非 kernel 主线。

---

## 1. 现状实测（findReferences 结论）

| 符号 | 定义 | 生产引用 | 判定 |
|------|------|---------|------|
| `ares_runtime.LoopPlugin` | `internal/ares_runtime/loop.go:26` | **0**（仅 `bus_test.go:862-904`、`loop_test.go:20-39`）| 建而未接 |
| `ares_runtime.CapLoop` | `internal/ares_runtime/plugin.go:19` | 仅 `loop.go:51` 自报 | 无消费者 |
| `engine.LoopConfig` / `Workflow.LoopConfig` | `internal/workflow/engine/types.go:145` / `:166` | **0** | 死声明 |
| `engine.ConditionFunc` / `Step.Condition` | `types.go:44` / `:75` | **0** | 死声明 |
| `engine.StepRecoveryHandler.RecoverStep` | `types.go:124` / `:125` | **0**（无实现体）| 死接口 |
| `engine.RecoveryDecision` | `types.go:116` | 仅被上面死接口引用 | 随之处理 |
| `engine.RecoveryPolicy` | `types.go:96` | ✅ `recovery_patcher.go:184-274`、`genome/recovery_genome.go` | **保留，勿动** |
| `runKernelRecoveryLoop` | `kernel_loop.go:227` | ✅ `peer_mode.go:283`（真实 `newPeerExecutor` 工厂）| 已闭环 |

关键事实：`internal/workflow/engine` **没有 executor**（包内仅 `mutable_dag.go` / `recovery_patcher.go` / `hitl.go` / `loader.go` / `registry.go` / `reloader.go`）。
`engine` 在 0.3.x 的真实角色是**演化基因的可变 DAG 载体**——消费者是 `evolution/genome`、`evolution/diff`、`cmd/ares/serve_live_dag.go`、`bootstrap/provide_new_evolution.go`。
因此 `Workflow` 上那些"等一个工作流执行器来读"的字段，在 0.3.x 里永远等不到执行器：**它们属于被 Kernel 取代的老编排模型。**

---

## 2. 决策登记

| 编号 | 项 | 决策 | 理由 |
|------|-----|------|------|
| D1 | `LoopPlugin` 轮次闭环 | **接线** | 轮次边界是 Agent OS 的"演化时钟"（checkpoint flush + memory advise + evolution record），能力真实存在，只差一个内核触发源 |
| D2 | `engine.LoopConfig` + `Workflow.LoopConfig` | **删除** | 轮次语义已由 D1 在 Kernel 侧承担；DAG 层再挂一份 = 双轨 |
| D3 | `engine.ConditionFunc` + `Step.Condition` | **删除** | 步骤级条件门控属于 leader-sub 编排语义；0.3.x 的准入决策在 `Schedule` 候选评分与 `budgetOK`（`scheduler.go:125`）|
| D4 | `engine.StepRecoveryHandler` + `RecoveryDecision` | **删除** | 恢复闭环已由 `runKernelRecoveryLoop` + `RecoveryPolicy` 补丁器实现，且是**真实执行器**恢复，不是回调式改图 |

删除项合计减少 4 个类型 + 2 个字段，无生产引用，不需迁移期。

---

## 3. W-L1：LoopPlugin 接入 Kernel 轮次边界（唯一接线项）

**问题**
`LoopPlugin.ShouldExecuteRound`（`loop.go:92`）与 `OnRoundEnd`（`loop.go:117`）实现完整——后者会：
1. 对 `CapCheckpoint` 插件调 `Flush(ctx, executionID)`；
2. 对 `CapMemory` 插件调 `AdviseRoute`；
3. 向 `CapEvolution` 记录轮次结果。

但**没有任何生产代码调用它们**，`NewLoopPlugin` 也从未被注册进 `PluginBus`。整套"轮次演化时钟"是死的。

**文件:方法**
- `internal/ares_runtime/loop.go:36` `NewLoopPlugin`
- `cmd/ares/runtime_bridge.go:73` `startPluginBus`
- `cmd/ares/runtime_bridge.go` `newPluginBusHook`（`QuantumHook` 适配器）
- `cmd/ares/kernel_loop.go:64` `kernelLoopConfig` / `:138` `parseKernelLoopConfig`
- `internal/ares_config/config.go` `KernelConfig`

**修复方案**

1. **配置**：在 `KernelConfig` 增 `loop_max_iterations`（int，0 = 不限）与 `loop_round_quanta`（int，默认 `1`，表示多少个 quantum 记为一轮）。
   `parseKernelLoopConfig` 把它们填进 `kernelLoopConfig` 新增的 `LoopMaxIterations` / `LoopRoundQuanta` 两个字段，走既有 `withDefaults()`（`kernel_loop.go:83`）零值兜底纪律。

2. **注册**：`startPluginBus` 内在 `bus.Start(ctx)` **之前**注册 loop 插件（成稿原文误写为"之后"，已按实现纠正）：

   ```go
   loop := ares_runtime.NewLoopPlugin("loop", ares_runtime.LoopConfig{
       MaxIterations: cfg.LoopMaxIterations, // UntilCondition 留 nil：内核轮次不做变量断言
   })
   if err := bus.Register(loop); err != nil { /* 降级：日志 + 继续调度 */ }
   // ...其余插件注册完毕后才 bus.Start(ctx)
   ```

   > **为什么必须先注册**：`Register` 在 `Start` 之后返回 `ErrBusAlreadyStarted`，
   > 且插件只在 `Start` 里才拿到 `EventBus` 引用。Start 后注册是**双重失效**——
   > 要么直接报错，要么插件的服务发现（`PluginsByCap`）永远为空。

   > `startPluginBus` 需增一个 `cfg kernelLoopConfig` 形参；调用点 `peer_mode.go:280` 已有 `parseKernelLoopConfig(cfg)` 结果可复用（提取为局部变量，避免二次解析）。

3. **驱动**：轮次由 `AfterQuantum` 推进——**quantum 是 Kernel 唯一的执行节拍，不新造循环 goroutine**。
   在 `newPluginBusHook` 的适配器里加一个 `int64` 计数器（`atomic`）：

   - `AfterQuantum` 每次自增，**用 `Add` 的返回值**做边界判定（不得 Add-then-Load：
     两个并发 drain 会读到同一个值 → 重复触发，或跨过倍数 → 丢轮次）；
   - 当 `count % LoopRoundQuanta == 0` 时：`round := count / LoopRoundQuanta`
     - **先** `loop.OnRoundEnd(ctx, round, executionID)` 结算本轮；
     - **再** `loop.ShouldExecuteRound(round+1, vars)` 门控下一轮；返回 false 则
       **只记一条 "loop: budget exhausted" 日志并停止后续轮次推进**（不停止调度器——
       Kernel 的任务流不受演化时钟约束）。
       成稿原文把两步写反了，会让 `MaxIterations` 吞掉最后一轮的结算，已按实现纠正。
   - **轮次预算必须从 `count` 推导**（`round > maxRounds` 则直接丢弃，不结算），
     **不能只靠停止标志**：标志是 read-then-set，N 个并发边界调用会全部先读到
     "未停止"再各自结算一轮，实测 `max_iterations=1` 结算出 3 轮。`count` 每 quantum
     唯一且单调，故 `round` 判定与交错顺序无关。
   - `executionID` 取**轮次自身标识** `kernel-round-<N>`，不取该 quantum 的 `taskID`：
     一轮跨多个任务，用边界任务的 taskID 只会 flush 偶然撞线的那一个执行体。
     `vars` 传 `map[string]any{"round": round}`。
   - `LoopRoundQuanta` 的 `<=0 → 1` 归一化放在 `newPluginBusHook` **内部**（边界算术要除它，
     不变量属于类型而非每个构造点）。

4. **纪律**：`OnRoundEnd` 内部已全程 best-effort（每个子系统失败只 `log.Warn`），hook 契约本身也是"错误只记不阻塞"（`quantum_hook.go:52-59`），因此该接线**不会引入新的调度阻塞路径**。计数器自增必须 `atomic`——`drain`（`scheduler.go:366`）是并发的。

**验收标准**
- `grep -rn "NewLoopPlugin" --include=*.go cmd/` 有生产命中；`LoopPlugin` 不再是 test-only。
- 新增 `cmd/ares/runtime_bridge_loop_test.go`：`LoopRoundQuanta=2` 时，4 次 `AfterQuantum` 触发 2 次 `OnRoundEnd`，`round` 依次为 1、2。
- 新增测试：`LoopMaxIterations=1` 时第 2 轮不再触发 `OnRoundEnd`，且**调度器仍能继续 drain 任务**（证明轮次耗尽不杀调度）。
- 新增测试（预算 × 并发）：`LoopMaxIterations=N` + 并发 `AfterQuantum`，结算轮次必须**恰好** N 个且为 `kernel-round-1..N`。仅"串行预算"或仅"并发无预算"两个用例会漏掉超发缺陷。
- 竞态门禁：`go test -race ./cmd/ares/ ./internal/ares_runtime/` 通过。
- 配置回归：`loop_round_quanta` 未配置时默认 1；`loop_max_iterations` 未配置时不限轮次。

---

## 4. W-K1..K3：删除项（必须与 W-L1 同一 PR，避免中间态歧义）

| 编号 | 删除内容 | 文件:行 | 连带处理 |
|------|---------|---------|---------|
| K1 | `LoopConfig` 类型 + `Workflow.LoopConfig` 字段 | `internal/workflow/engine/types.go:142-157`、`:166` | 无生产引用；检查 `engine_test.go` / `coverage_test.go` / `loader_test.go` 是否构造该字段 |
| K2 | `ConditionFunc` 类型 + `Step.Condition` 字段 | `types.go:41-44`、`:75` | 同上；`graph` 包的 `Condition`（`graph/graph.go:129`）**是另一个类型，不要误删** |
| K3 | `StepRecoveryHandler` 接口 + `RecoveryDecision` 类型 | `types.go:116-126` | `RecoveryStrategy` 常量（含 `RecoveryReplaceNode`，`types.go:91`）与 `RecoveryPolicy`（`:96`）**保留**——`recovery_patcher.go` 与 `recovery_genome.go` 在用 |

**删除纪律**
- `RecoveryDecision.NewStep` 是 `replace_node` 的唯一载体，但真实的节点替换在 0.3.x 走 `recovery_patcher.go:190-193`（直接改 `step.RecoveryPolicy.Strategy`）+ Kernel 侧 `newPeerExecutor` 替身，**不经过 `RecoveryDecision`**。删除后恢复能力不减。
- `examples/knowledge-fabric/main.go:137` 的中文说明串里提到 "Workflow Executor 的 StepRecoveryHandler"——那是**文案**，需同步改写为"Kernel 恢复循环查询经验库"，否则文档与代码再次背离。

**验收标准**
- `go build ./... && go vet ./...` 通过。
- `grep -rn "StepRecoveryHandler\|RecoveryDecision\|ConditionFunc\|engine.LoopConfig" --include=*.go .` 仅剩 0 命中（examples 文案已改写）。
- `go test ./internal/workflow/... ./internal/evolution/...` 全绿——证明 `RecoveryPolicy` 侧未被误伤。

---

## 5. 已落地的前置项（本计划的依赖，勿重复做）

`memory.enable_distillation` 三态门控（C1 / P0-3）已在工作区改完：

- `MemoryConfig.EnableDistillation` 改为 `*bool`，新增 `DistillationEnabled()`（`internal/ares_config/config.go:444` / `:478`）；
- `setDefaults` 中 `nil → true`，仅显式 YAML `false` 可关（`config_defaults.go:198+`）；
- `wireDistillation` 改用 `cfg.Memory.DistillationEnabled()`（`internal/ares_bootstrap/bootstrap_steps.go:37`）；
- `configs/ares.yaml` 移除硬写的 `enable_distillation: false`。

语义：**默认开启蒸馏**——只依赖 `Storage + Embedding` 的旧部署在 C1 门控落地后不会静默失去经验蒸馏。
W-L1 的 `OnRoundEnd → CapMemory.AdviseRoute` 正是这条链的下游消费者，二者合起来才构成"轮次 → 记忆 → 演化"闭环。

---

## 6. 防回归门禁（✅ 2026-08-31 全部落地）

1. **test-only 插件检测**（已落地：`internal/ares_runtime/architecture_test.go`
   `TestPluginsMustBeWiredInProduction`）：AST 枚举 `New*Plugin` 构造器，
   全仓非测试 `.go` 引用计数为零即失败（剔除 `func` 定义行与行注释）。
   白名单起步 7 个，带 STALE 反向检测（条目获得引用即红）。
2. **死声明检测**（已落地：`internal/workflow/engine/architecture_test.go`
   `TestExportedTypesMustHaveProductionReferences`）：对 `types.go` 导出类型
   跑"声明文件之外"引用计数，零即失败。白名单起步 5 个，同样带 STALE 检测。
3. **架构红线检测**（已落地：`internal/kernelscheduler/architecture_test.go`
   `TestSchedulerMustNotImportRuntime`）：禁止 `internal/kernelscheduler`
   出现 `internal/ares_runtime` import（依赖方向必须是 `cmd/ares` 适配器单向注入）。

---

## 7. 交付顺序

```
① K1/K2/K3 删除（编译面收窄，暴露隐藏引用）
② W-L1 配置 + 注册 + AfterQuantum 驱动
③ W-L1 测试（含 -race）
④ examples 文案同步
⑤ 门禁脚本
```

单 PR 交付。改动预估：删除 ~40 行、新增 ~90 行（含测试）、修改 4 个文件 + 1 个 examples 文案。
