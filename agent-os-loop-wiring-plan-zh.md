# Agent OS 控制闭环接线开发计划（0.3.x）

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

2. **注册**：`startPluginBus` 内在 `bus.Start(ctx)` 之后、`WithQuantumHook` 之前注册 loop 插件：

   ```go
   loop := ares_runtime.NewLoopPlugin("loop", ares_runtime.LoopConfig{
       MaxIterations: cfg.LoopMaxIterations, // UntilCondition 留 nil：内核轮次不做变量断言
   })
   if err := bus.Register(loop); err != nil { /* 降级：日志 + 继续调度 */ }
   ```

   > `startPluginBus` 需增一个 `cfg kernelLoopConfig` 形参；调用点 `peer_mode.go:280` 已有 `parseKernelLoopConfig(cfg)` 结果可复用（提取为局部变量，避免二次解析）。

3. **驱动**：轮次由 `AfterQuantum` 推进——**quantum 是 Kernel 唯一的执行节拍，不新造循环 goroutine**。
   在 `newPluginBusHook` 的适配器里加一个 `int64` 计数器（`atomic`）：

   - `AfterQuantum` 每次自增；
   - 当 `count % LoopRoundQuanta == 0` 时：`round := count / LoopRoundQuanta`
     - 先问 `loop.ShouldExecuteRound(round+1, vars)`；返回 false 则**只记一条 "loop: budget exhausted" 日志并停止后续轮次推进**（不停止调度器——Kernel 的任务流不受演化时钟约束）；
     - 再调 `loop.OnRoundEnd(ctx, round, executionID)`。
   - `executionID` 取该 quantum 的 `taskID`（`AfterQuantum` 已入参），`vars` 传 `map[string]any{"round": round}`。

4. **纪律**：`OnRoundEnd` 内部已全程 best-effort（每个子系统失败只 `log.Warn`），hook 契约本身也是"错误只记不阻塞"（`quantum_hook.go:52-59`），因此该接线**不会引入新的调度阻塞路径**。计数器自增必须 `atomic`——`drain`（`scheduler.go:366`）是并发的。

**验收标准**
- `grep -rn "NewLoopPlugin" --include=*.go cmd/` 有生产命中；`LoopPlugin` 不再是 test-only。
- 新增 `cmd/ares/runtime_bridge_loop_test.go`：`LoopRoundQuanta=2` 时，4 次 `AfterQuantum` 触发 2 次 `OnRoundEnd`，`round` 依次为 1、2。
- 新增测试：`LoopMaxIterations=1` 时第 2 轮不再触发 `OnRoundEnd`，且**调度器仍能继续 drain 任务**（证明轮次耗尽不杀调度）。
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

## 6. 防回归门禁

1. **test-only 插件检测**：CI 增一条脚本——遍历 `ares_runtime` 中所有实现 `RuntimePlugin` 的类型，若其构造器仅被 `_test.go` 引用则失败。这是本次问题（`LoopPlugin` 死了却无人发现）的根因防线。
2. **死声明检测**：对 `internal/workflow/engine/types.go` 中的导出类型跑引用计数，0 生产引用即失败（`RecoveryPolicy` 之类有引用的自然通过）。
3. **架构红线检测**：禁止 `internal/kernelscheduler` 出现 `internal/ares_runtime` import（依赖方向必须是 `cmd/ares` 适配器单向注入）。

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
