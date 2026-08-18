# ares_runtime 插件/路由子系统：能力储备清单与启用路径

> 状态：**能力储备**（完整实现 + 有测试，生产暂未启用，非死代码）
> 分类标准（用户定义）：复用价值 = "未来功能能用到，有了是锦上添花，而不是滥竽充数"。

---

## 一、为什么保留而不是删除

`internal/ares_runtime` 的插件框架是**生产活跃的**，不只是死代码：

| 证据 | 位置 |
|------|------|
| `PluginBus` 在 prod 构造并注册 `MonitorPlugin` | `cmd/ares/serve.go` |
| workflow Runner **生产消费** `CapCheckpoint`/`CapEvolution`/`Flusher`/`EvolutionPlugin` 接口 | `internal/workflow/runner_plugins.go:70,83` |
| `LoopPlugin`/`CheckpointPlugin` 作为 graph executor 的插件集成**测试夹具** | `internal/workflow/graph/executor_test.go` |
| `ExpressionRouter`/`RouteRule` 被 graph executor 测试使用 | `internal/workflow/graph/executor_test.go:738` |
| `ExecutionOutcome` 被 workflow 生产引用 | `internal/workflow/` |
| `manager_chaos.go` 的 `chaosWrappedAgent` 被 Manager **生产使用**（故障注入边界） | `internal/ares_runtime/manager.go:211` |

所以：**插件框架（总线 + 能力发现 + 接口）是 workflow 生产路径的组成部分**。下面列出的"能力储备"是完整、正确、有测试的**插件实现**，只是运行时暂未注册（因为 workflow 用原生 loop/checkpoint/routing）。

---

## 二、能力储备清单（生产零引用，但完整可用）

| 能力 | 文件 | 功能 | 未来启用场景 |
|------|------|------|-------------|
| `ArenaPlugin` | `arena.go` | 故障注入（panic/timeout/error/bus_stop），通过 `chaosWrappedAgent` 边界生效 | 混沌工程/鲁棒性演练；与 `internal/ares_arena` 故障场景对接 |
| `ObserverPlugin` | `observer.go` | 订阅 workflow 事件写 `ares_events.EventStore` | 事件可观测性（区别于 MonitorPlugin 的实时监控） |
| `ToolPlugin` | `tool.go` | 工具插件化接入 runtime（注册/收集器/查询） | 工具以插件形式接入的扩展点 |
| `MemoryRouter` | `router_memory.go` | 基于历史经验的路由决策（消费 `MemoryPlugin.AdviseRoute`） | 记忆驱动路由 |
| `EvolutionRouter` | `router_evolution.go` | 基于 GA 推荐的路由（消费 `EvolutionPlugin.Recommend`） | 进化驱动路由 |
| `FallbackRouter` | `router_fallback.go` | 多 router 降级链 | 路由鲁棒性兜底 |
| `NewEvolutionPlugin` | `evolution_plugin.go` | 进化推荐/反馈/缓存（`StrategyProvider`/`OutcomeRecorder`） | 进化闭环 |
| `NewBasicRecoveryPlugin` | `recovery.go` | 步骤失败恢复决策 | 步骤级自动恢复 |
| `NewInterruptPlugin` | `interrupt.go` | HITL 中断事件观察（`CapInterrupt`） | 人机协同中断上报 |

> 注：`manager_chaos.go`（`chaosWrappedAgent`）**不是**能力储备，是生产活跃（见上表证据）。

---

## 三、为什么暂不注册（方向决策，铁律 #4）

workflow 引擎已提供**原生**对应能力，注册这些插件会造成功能重复/冲突：

| 能力储备插件 | workflow 原生替代 |
|-------------|------------------|
| `LoopPlugin` | `LoopSpec.MaxIterations`（`internal/workflow`） |
| `CheckpointPlugin` | `WithCheckpointStore` + `flushLifecycleCheckpoints`（`runner_plugins.go:69`） |
| `ExpressionRouter`/`MemoryRouter`/`EvolutionRouter`/`FallbackRouter` | `NodeRouter` + `liveDAGRouter`（serve.go） |

因此是否注册/启用这些插件是**产品方向决策**，非 bug 修复。当前决策：**保留为能力储备，不注册**。

---

## 四、启用路径（当需要某能力时）

1. **确认原生替代不足**：优先使用 workflow 原生 loop/checkpoint/router。
2. **启用单个插件**：在 `cmd/ares/serve.go` 的 PluginBus 上 `bus.Register(plugin)`，并在 `api/service/workflow.Service` 通过 `WithPluginBus` 注入（`service.go:169`）。
3. **启用 router**：`bus.Register(router)`，router 会经 `PluginsByCap(CapRouter)` 被发现。
4. **验证**：参考 `internal/ares_runtime/*_test.go` 和 `internal/workflow/graph/executor_test.go` 的既有测试覆盖。

---

## 五、与"死代码"的边界

**不是死代码**（保留）：
- 有明确功能语义 + 完整实现 + 测试覆盖
- 与活跃框架（PluginBus/接口/Manager）协同
- 未来能力点明确（见上表）

**才是死代码/滥竽充数**（可清理）：
- 空壳/假实现（如已删除的 ares_eval `placeholderRunner`，铁律 #2）
- 无功能语义、无测试、被误挂载的残留
- 经核实 `ares_runtime` 目前**无此类**（本清单全部为能力储备）

---

## 六、相关修复记录

- `GraphPatchExecutor.SetGraph` + `UpdateLiveDAG` 就地更新（修复"每次注册必失败"）——见分析报告第三轮。
- 本清单对应的代码**未删除**，已还原所有破坏性改动。
