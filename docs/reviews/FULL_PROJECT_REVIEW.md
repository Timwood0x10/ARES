# ARES 全项目深度 Code Review 报告

> **日期**：2026-09-12
> **基线**：分支 `dev`，工作区未提交变更
> **方法**：多模块并行检索 → 交叉核对 `plan/rules/code_rules_v2.md` → 汇总
> **范围**：38 个包、187K 行 Go 代码（不含测试）

---

## 一、总体评价

ARES 是一个 **Agent 操作系统**，将 Agent 视为被调度的进程（checkpoint + lease + epoch fencing）。0.3.1 完成了从 `agentloop` 到 `agentruntime` 的架构收敛，SDK 与 serve 共用同一条 L2 执行路径。

**优点**：
- 架构不变量通过编译时测试锁定（kernel 不 import runtime、fabric 不 import runtime）
- Epoch fencing + checkpoint v4 设计严密，恢复链完整
- 并发安全意识强，大量 `-race` 测试覆盖
- 技术债务显式标注（`TODO(tech-debt)` 36 处），不留暗坑

**主要风险**：
- 多处技术债务集中在 IPC/消息传递层和多租户隔离
- 部分文件逼近 1000 行上限
- SDK 的 Stream 方法是假实现（拆分已有结果冒充流式）
- LLM streaming 路径零生产调用

---

## 二、分模块审查

### 2.1 `cmd/ares` — 命令行入口（serve 模式）

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-CMD-1 | `serve_peer.go:293` | `buildPeerRegistry` 构建的 peer registry **永远为空**——`sub.Agent` 的 `SendMessage` 方法已被移除，所有非进化 `ask_agent` 发送都会失败。保留仅为发现合约，需重构。 | 中 |
| TD-CMD-2 | `agent_kernel.go:374` | `createPeerSubAgents` 创建的 shell agent 使用 `cognitionExecutor{id: p.ID}` 适配器，该适配器**永远不被驱动**（静态调度池已删，one-shot Execute 无生产调用方）。 | 低 |
| TD-CMD-3 | `kernel_dispatch.go:29` | `agentipc` 无 retry/dead-letter 语义——leader-sub 协议的 DLQProcessor 已删，多 Agent 消息扩展时需补回。 | 中 |
| TD-CMD-4 | `serve_wiring.go:290` | PG 模式下内存模式的 round_N.json archive + compaction/trim 未接——PG 表本身是持久历史，但 archive 的缺失意味着内存→PG 切换时历史不可移植。 | 低 |
| TD-CMD-5 | `db.go:195` | `distilled_memories` schema ghost 的 DDL 仍在 `migrate_storage.go` 中，待旧部署收敛后清理。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 |
|---|------|------|
| BUG-CMD-1 | `db.go:127-136` | `connectAdmin` 在连接失败时 `os.Exit(1)`——违反 rule 0.1（禁止 panic 作为业务路径），但这是初始化不可恢复错误，符合 rule 3.4 的例外。**可接受**。 |

#### 设计不足

- serve 模式的路由注册分散在多个文件（`agent_routes_*.go`），缺少统一路由表文档
- Chaos 路由的 RBAC 检查（`agent_routes_chaos.go:35`）之前未强制 admin 权限，现已修复

---

### 2.2 `internal/agentruntime` — 共享执行核

#### 技术债务
无显著技术债务。

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-RT-1 | `submit.go:171` | `sessionID, _ = payload["session_id"].(string)` — 如果 `session_id` 存在但类型不是 string（如 int），类型断言静默返回 ""，导致 auto-admission 覆盖调用方意图。应做类型检查后拒绝非 string 值。 | 中 |
| BUG-RT-2 | `session.go:131` | `context.WithoutCancel(ctx)` 用于 compile subscription — 正确设计（订阅必须比请求长活），但如果 `ctx` 本身来自一个已 cancel 的 context，`WithoutCancel` 仍会创建一个新 context，可能导致泄漏。**已通过 reaper + idle-TTL 覆盖**。 | 低 |

#### 设计亮点
- `admitLock` 的 per-session 序列化 + refcount 设计精确，防止了"并发 Submit 误返回上一轮 answer"的竞态
- `MaxRestoredSeq` 四族 ID 扫描覆盖了所有计数器派生 ID

---

### 2.3 `internal/fabric` — 任务编排（Agent Fabric + Task Fabric）

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-FAB-1 | `agent/executor_test.go:128` | `SubAgentCognition` 及其 parity test 已删除，仅留 TODO trace。 | 低 |
| TD-FAB-2 | `task/workflow/engine/registry.go` | `AgentRegistry` 是一个通用 agent 工厂注册表，但在生产中仅被 workflow 引擎使用。与 `fabric/agent/Fabric` 的 Spawn 模型有概念重叠。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-FAB-1 | `task/fabric.go:274-290` | `Task()` 方法对 `Checkpoint (any)` 字段**故意保留别名**——注释说"fabric 只替换整个 Checkpoint 指针，从不通过它修改"。如果未来有人在持锁外修改 Checkpoint 内部字段，将是 data race。**当前安全但脆弱**。 | 中 |
| BUG-FAB-2 | `task/workflow/engine/mutable_dag.go` | `Steps()` 和 `StepIndex()` 返回 live `*Step` 指针——在 `mutable_dag_isolation_test.go` 中已发现并修复了 race，但根本问题是 Step 指针的 `DependsOn` 切片可能被 `AddEdge/RemoveEdge` 原地修改。**已通过 `ReadDeps` 方法缓解**。 | 低（已修） |

#### 设计亮点
- Epoch fencing + `ownerLocked` 实现精确
- `flushAppends` 的 seq gate 保证了因果顺序（`TestFabricConcurrentFlushPreservesCausalOrder`）
- Session registry 的 `ReleaseSession` 在锁外执行 `stopSub`，避免阻塞全局读

---

### 2.4 `internal/kernel` — 调度器内核

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-KER-1 | `scheduler_dispatch.go:39` | per-agent 本地 ready-queue 设计（`taskfabric.AgentQueue/Steal`）已作为未使用移除。共享 `ReadyTasks()` 队列由有界 goroutine 并发 drain 是当前唯一的 stealing 基质。如果 profiling 显示竞争，需重新引入。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-KER-1 | `scheduler.go:284` | `go func()` 裸 goroutine — 但这是 `Run` 方法的后台 sweep goroutine，由 `context` 管理。符合 rule 4.1 的"由带停止信号的受管 worker"例外。**可接受**。 | 低 |
| BUG-KER-2 | `fabric_executor.go:52-56` | `Type()` 返回第一个 capability 作为 AgentType — 如果 capability 列表顺序变化，Type 也会变化。影响调度评分但不影响正确性。 | 低 |

#### 设计亮点
- `reconcileFabricDeaths` 在每次 drain 时清理已死亡 agent 的静态注册，防止 zombie executor
- `filterBudgetAffordable` 在 `Schedule` 之前过滤预算耗尽的候选，避免 acquire/release 活锁
- `stale-winner` 路径有完整测试覆盖

---

### 2.5 `internal/runtime` — 生命周期/进化/记忆/Arena

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-RT-1 | `memory/manager_impl.go:820` | `GetLatestSessionForAgent` 返回 `ErrAgentCheckpointNotSupported` — **无实现**。旧实现（ProductionMemoryManager）已删，当前无 in-tree manager 能回答此查询。调用方（ares_runtime cognitive recovery）只能区分"no session"和"backend cannot answer"。 | 中 |
| TD-RT-2 | `memory/manager_impl.go:834` | `SetDefaultTenantID` 的 write tenant pinning 限制 — DB 写 tenant 在 adapter 构造时固定，override 只影响 READ。多租户部署前需修复。 | 中 |
| TD-RT-3 | `arena/scenario.go:339` | `RunScenario` 已 deprecated，无生产调用方，仅测试使用。 | 低 |
| TD-RT-4 | `arena/metrics.go:111` | `RecordConsistency` 已 deprecated，同上。 | 低 |
| TD-RT-5 | `ares_evolution/lifecycle_evidence.go:330` | decision records 与 runtime fitness 共享 `KindFitness`，仅靠 `Source` 区分。0.4.x 应分离为 `KindDecision`。 | 低 |
| TD-RT-6 | `memory/context/task.go:369` | `context.Cache` 类型已作为死代码删除，但留有 TODO trace。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-RT-1 | `runtime_test.go:773` | `TestManager_PanicRecovery` 使用 `time.Sleep(300ms)` 等待 panic 恢复 — 违反 rule 7.3（禁止 `time.Sleep` 做同步）。应改用 channel/WaitGroup。 | 中 |
| BUG-RT-2 | `memory/manager_impl.go:809-822` | `GetLatestSessionForAgent` 无 agent→session 映射，返回错误。这是**诚实的 fail-loud**（符合 rule 0.2），但意味着 agent recovery 路径在内存模式下无法恢复认知状态。 | 中（设计不足） |

#### 设计不足

- `memory/manager_impl.go` 954 行，逼近 1000 行限制（rule 1）
- PluginBus 的热插拔语义复杂，`invokeStart` 失败后自动 `remove` 的竞态窗口需持续关注

---

### 2.6 `internal/knowledge` — 知识系统（BETA）

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-KNOW-1 | `service/adapter.go:87` | `Query` 方法返回 `ErrQueryUnsupported` — **未实现**。Adapter 包装的是 runtime 而非 store，无法回答查询。符合 rule 0.2（禁止假实现）。 | 中 |
| TD-KNOW-2 | `service/adapter.go:91` | `Distill` 方法仅返回单个 KnowledgeObject 包装 raw bytes — 简化实现，生产调用方可能需要更丰富的 compiler。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-KNOW-1 | `provider/postgres/provider.go:130-181` | `Stream` 方法的 errgroup goroutine **不被 Wait** — 注释说"callers observe completion via objCh/errCh being closed"，但 g.Wait() 从未被调用。如果 goroutine 内部 panic（虽然 errgroup 会 recover），外部无法感知。 | 中 |
| BUG-KNOW-2 | `store/sqlite/store.go:36` | `db.SetMaxOpenConns(1)` — SQLite 单写者限制正确，但在高并发读场景下可能成为瓶颈。应考虑 WAL 模式 + 多读连接。 | 低 |

---

### 2.7 `internal/storage/postgres` — 存储层

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-PG-1 | `repositories/experience_repository_test.go:507` | 测试注释标注 "Metadata field has a bug in ExperienceRepository.Create" — **未修复的已知 bug**。 | 高 |
| TD-PG-2 | `embedding_queue.go` | 723 行，文件较大但未超限。embedding queue 是异步管线核心，逻辑复杂。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-PG-1 | `repositories/experience_repository_test.go:507` | Metadata 字段在 `Create` 中有 bug — 测试代码显式注释。需要检查 `Create` 的 SQL 和 metadata 序列化。 | 高 |
| BUG-PG-2 | `pool.go:69` | `waitCount` 只在 `elapsed > time.Second` 时递增 — 如果连接获取时间为 999ms，不计入统计。监控指标可能低估延迟。 | 低 |

---

### 2.8 `internal/ares_events` — 事件存储

#### 技术债务
无显著技术债务。

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-EVT-1 | `compactable_store.go` | 736 行，接近 1000 行限制。CompactableEventStore 混合了 store + compactor + trim 三种职责。 | 低（设计不足） |
| BUG-EVT-2 | `concurrency_test.go:1140` | `TestCompactableEventStore_ConcurrentAppendWithAutoCompact` 使用 `time.Sleep(500ms)` 等待异步 compaction — 违反 rule 7.3。 | 中 |

#### 设计亮点
- `TestMemoryEventStore_CloseUnsubscribeRace` 和 `TestMemoryEventStore_ConcurrentCloseManyLiveSubscribers` 精确覆盖了 double-close panic 的竞态
- `Append_VersionOverflow` 测试覆盖了 int64 溢出

---

### 2.9 `internal/ares_config` — 配置系统

#### 技术债务
无显著技术债务。

#### 设计亮点
- `ConfigStore.Watch` 的 debounce + drain 模式精确处理了编辑器 partial-write 场景
- `TestG2ConfigContract` 是一个**配置消费合约测试**——扫描所有非 test Go 文件确认每个配置字段有消费方，防止死配置
- `allowedConfigDir` 路径遍历防护完善

---

### 2.10 `sdk` — 嵌入式 SDK

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-SDK-1 | `agent.go:98-99` | `Stream` 方法是**假实现**——先完整 `Run`，再把结果按 10 rune 拆分冒充流式。`TODO(tech-debt)` 已标注。违反 rule 0.2 精神（虽然标注了）。 | 高 |
| TD-SDK-2 | `goleak_test.go:20` | 四个手写的 `runtime.NumGoroutine()` 比较测试未迁移到 goleak，对并行测试敏感。 | 低 |

#### 潜在 Bug

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| BUG-SDK-1 | `agent.go:112` | `go func()` 裸 goroutine — 但通过 `ctx.Done()` + channel close 管理生命周期。符合 rule 4.1 例外。**可接受**。 | 低 |

---

### 2.11 `internal/tools` — 工具系统

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-TOOL-1 | `resources/builtin/memory/memory_tools.go:55` | `MemorySearch` 无 per-user/tenant 隔离 — 单租户 v1 可接受，多租户部署前需修复。 | 中 |
| TD-TOOL-2 | `resources/builtin/knowledge/knowledge_base.go:22` | 知识工具的 tenant 由 server 侧固定为 `DefaultTenantID` — 同上。 | 中 |

#### 设计亮点
- `FileTools` 的 `allowedDir` deny-by-default + symlink 解析设计精确
- `WebSearch` 有 SSRF allowlist 防护
- `PDFTool` 同样有 `allowedDir` 防护

---

### 2.12 `internal/llm*` — LLM 栈

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-LLM-1 | `llm/` | `GenerateStream` 零生产调用 — LLM streaming 路径未接生产。 | 中 |
| TD-LLM-2 | `llmcore/` | `LLMRepository` 接口有 `LogGeneration` / `GetGenerationLog` 方法，但 `mockLLMRepository` 的实现是空壳。 | 低 |

#### 潜在 Bug
无明显 bug。

---

### 2.13 `internal/ares_bootstrap` — 装配层

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-BOOT-1 | `provide_llm.go:44` | `compat.RegisterLLM` 已移除（M5 sunset），但 `compat/` 目录本身保留 — 0.4.x 决定是否删。 | 低 |
| TD-BOOT-2 | `provide_new_evolution.go:155` | Scheduler genome 维度已退役 — sdk.Graph 全并行 ready 批次，排序调度器无执行决策。Legacy patch applier 保留供持久化 patch。 | 低 |
| TD-BOOT-3 | `tool_deps.go:29` | `DistilledRepo` 已随 `distilled_memories` schema ghost 删除，TODO trace 保留在 RUNTIME.md #9。 | 低 |

---

### 2.14 `internal/agentipc` — Agent 间通信

#### 技术债务

| # | 位置 | 问题 | 严重度 |
|---|------|------|--------|
| TD-IPC-1 | `primitives.go` | `TODO(tech-debt)` 标注 — IPC 无 retry/dead-letter 语义（DLQProcessor 已随 leader-sub 协议移除）。多 Agent 消息扩展时需补回。 | 中 |

---

### 2.15 `internal/aresrecovery` — 恢复系统

无明显技术债务或 bug。恢复链设计完整：`RequeueExpiredLeases` → `RecoverTaskCheckpoint` → `RevivableSnapshot` → `RestartAgent`/`spawnAgent` → `Acquire(epoch)`。

---

## 三、不符合 `code_rules_v2.md` 之处

### 3.1 规模限制（rule 1）

| 文件 | 行数 | 状态 |
|------|------|------|
| `internal/fabric/task/workflow/engine/mutable_dag.go` | 990 | ⚠️ 逼近上限 |
| `internal/runtime/ares_evolution/genome/population.go` | 977 | ⚠️ 逼近上限 |
| `internal/runtime/ares_evolution/service/service.go` | 972 | ⚠️ 逼近上限 |
| `internal/runtime/memory/manager_impl.go` | 954 | ⚠️ 逼近上限 |
| `internal/fabric/agent/planner_cognition.go` | 905 | ⚠️ 逼近上限 |

**无文件超过 1000 行**，但 5 个文件超过 900 行，应在下次重构中拆分。

### 3.2 测试中使用 `time.Sleep`（rule 7.3）

| 位置 | 问题 |
|------|------|
| `runtime_test.go:773` | `TestManager_PanicRecovery` 用 300ms sleep 等待恢复 |
| `ares_events/compactable_store_test.go:1140` | 用 500ms sleep 等待异步 compaction |
| `ares_config/store_test.go:95` | 用 150ms sleep 等待 watcher 启动 |
| `fabric/agent/agent_medium_test.go:152` | 用 200ms sleep 运行 race 测试 |

### 3.3 假实现（rule 0.2）

| 位置 | 问题 |
|------|------|
| `sdk/agent.go:109-139` | `Stream` 方法拆分已有结果冒充流式，`TODO(tech-debt)` 已标注 |
| `knowledge/service/adapter.go:87` | `Query` 返回 `ErrQueryUnsupported`（诚实失败，符合 rule 0.2） |

### 3.4 裸 goroutine（rule 4.1）

| 位置 | 问题 |
|------|------|
| `sdk/agent.go:112` | `go func()` — 但由 ctx + channel 管理，**可接受** |
| `kernel/scheduler.go:284` | `go func()` — background sweep，由 ctx 管理，**可接受** |
| `ares_shutdown/manager.go:207` | `go func()` — shutdown 序列，由 WaitGroup 管理，**可接受** |
| `ares_shutdown/callbacks.go:243` | `go func()` — callback 派发，由 panic recovery 保护，**可接受** |

### 3.5 库层直接打印（rule 9.1）

| 位置 | 问题 |
|------|------|
| `errors/example_test.go` | `fmt.Println` — 但这是 example 文件，**可接受** |
| `distillation/benchmark_token_comparison_test.go` | `fmt.Println` — benchmark 文件，**可接受** |

**生产代码无 `fmt.Println` 违规**。

---

## 四、问题汇总矩阵

### 按严重度

| 严重度 | 数量 | 关键项 |
|--------|------|--------|
| 🔴 高 | 3 | SDK Stream 假实现；ExperienceRepository.Create metadata bug；IPC 无 retry/DLQ |
| 🟡 中 | 11 | peer registry 永远为空；tenant 隔离未完成；GetLatestSessionForAgent 未实现；LLM streaming 未接生产 |
| 🟢 低 | 15+ | deprecated 方法未清理；文件逼近行数上限；测试 sleep |

### 按类别

| 类别 | 数量 |
|------|------|
| 1. 技术债务 | 36（`TODO(tech-debt)` 标注） |
| 2. 未完成的方法 | 2（`Query`、`GetLatestSessionForAgent`） |
| 3. 潜在的 bug | 6（含 1 个已知 bug） |
| 4. 设计不足之处 | 5（文件逼近上限、多租户未完成、streaming 未接） |
| 5. 逻辑不通顺之处 | 2（peer registry 空转、stale-winner 路径复杂） |
| 6. 不符合 code_rules_v2.md | 4（测试 sleep、假实现） |

---

## 五、当前数据流

### 5.1 主执行流：任务 → 量子 → 终态

```
用户输入 (HTTP POST /api/tasks | SDK Submit)
  → Sessions.Admit (session.go:51)
    → 拒斜杠 ID → 幂等查 → InitSession + SubscribeGraphEvents → 编译 root
  → MutableDAG 接收 graph events
  → planprojection coordinator (coordinator.go:681) 将 graph steps 投影为 fabric tasks
  → Task Fabric: READY 任务
  → kernel drain (scheduler_dispatch.go:43)
    → reconcileFabricDeaths (清理 zombie executor)
    → ResumableTasks (READY + SUSPENDED)
    → PreemptLowerPriority
    → buildCandidates (executor_registry.go:129)
      → peer 模式: fabric 人口唯一候选源
      → hybrid 模式: 静态优先 + fabric 补充
    → filterBudgetAffordable (预算预过滤)
    → 置信重解析 (measured 恒胜先验)
    → fabric.Schedule (fabric.go:556)
      → Pick (capability-aware 打分) → Acquire (lease + epoch)
    → TryBegin (原子占忙位)
    → RunQuantum (quantum.go:58)
      → Start (READY→RUNNING)
      → t.Quantum++
      → runStepRecovered (panic 边界)
        → routerCognition.ExecuteStep (l2graph.go:346)
          → ares/root → rootCognition (只 Yield)
          → ares/plan → plannerCognition (调 LLM, 长 tool/answer 节点, 不执行工具)
          → tool/* → toolCognition (盖章 callerID, 调 ToolBinder, 执行工具)
          → ares/answer → answerCognition (读 content, ReleaseSession)
      → Done → Complete + checkpoint
      → !Done → Yield + checkpoint → 回 READY
      → err → Fail (重试预算 → requeue 或 FAILED)
  → answer 节点完成 → ReleaseSession → 终答返回调用方
```

### 5.2 反馈流：执行结果 → 经验 → 下一轮先验

```
量子完成/失败
  → ares_events.Emit (task.completed / task.failed)
    → observability (延迟 + token 成本惩罚)
    → skill_outcome_writer → Experience.Record
    → flight.Collector (谱系 + 时间线)
    → channel_feedback (工具/协作回执)
  → ExperienceStore (llmexp/types.go:156)
  → 下轮消费:
    → ConfidenceSource (Schedule 内填充)
    → spawn 先验 (planner l1Priors)
    → GA 适应度 (fitness_aggregator)
  → ActiveStrategy
    → prompt 覆盖 → plannerCognition
    → 参数覆盖 → quantum
```

### 5.3 知识流：对话 → AKG → 检索回注

```
会话事件
  → DistillBridge (adapter/distill.go:30)
  → KnowledgePipeline (pipeline.go:77)
    → Normalizer → EntityMatcher → Validator → Summarizer
  → KnowledgeStore (memory / postgres / sqlite)
  → KnowledgeRuntime.Execute (runtime.go:123)
    → link:312 (抽关系) → reduce:351 (按 TokenBudget 裁剪)
  → WorkingGraph
  → Retriever (hybrid.go:72 向量+词面)
  → memory 上下文注入 / planner 先验
```

### 5.4 演化流：候选 → 门禁 → 部署

```
fitness_aggregator (evidence 聚合)
  → Population (Selection → Crossover → Mutator)
  → candidate_pipeline
  → G1 guardrail → G2 shadow → G3 eval (llm_judge)
  → Arena 回归门 (regression.go:209, Welch t-test :628)
  → staging → live deployment
  → ActiveStrategy (下次执行生效)
  → 显著回退 → Rollback
```

---

## 六、工作流程

### 6.1 serve 模式工作流

```
1. 启动: Bootstrap(ctx, cfg, deps) → Components
   - EventStore → Runtime → Memory → MCP → Skills → LLM → Dashboard
   - NewEvolution (Genome+Diff+Coordinator) → Evolution(旧) → GA适配
   - Discovery (opt-in) → SystemRuntime (注册组件图, 拓扑启动)

2. 请求: POST /api/tasks
   - authWrite 鉴权 → handleSubmitTask → submitPeerTask → Submitter.Submit
   - 202 Accepted + task_id (异步)

3. 执行: kernel.Scheduler.Run → drain → execute → RunQuantum
   - plannerCognition 调 LLM → 长 tool/answer 节点
   - toolCognition 执行工具
   - 循环直到 answer 节点

4. 恢复: kernel recovery loop
   - RequeueExpiredLeases → RecoverTaskCheckpoint → RestartAgent/spawnAgent → Acquire(epoch)

5. 演化: DreamCycle (5min ticker)
   - Population.Evolve → candidate_pipeline → 门禁链 → deployment → ActiveStrategy

6. 关闭: Orchestrator.Shutdown
   - 逆序 Stop → Wait → Close
```

### 6.2 SDK 模式工作流

```
1. 构造: New(opts...) → Runtime
   - ensureL2 惰性构造 Execution
   - detector.Detect 零配置探测

2. 调用: Agent.Run(ctx, input)
   - submitThroughL2 → Submitter.Submit → 等待 answer

3. 调度: 同 serve（共享 agentruntime.NewExecution）
   - hybrid 模式 (WithStaticPoolHybrid)
   - 单协程串行执行量子

4. 关闭: Runtime.Close
   - cancel lifecycle ctx → errgroup.Wait → cleanup
```

---

## 七、整体架构

### 七层分层

```
L0 入口面: cmd/ares (serve) | sdk (嵌入) | services/embedding (sidecar)
    ↓
L1 装配层: ares_bootstrap.Bootstrap (唯一装配根)
    ↓
L2 共享执行核: agentruntime (Sessions + Submitter + Execution)
    ↓
L3 调度+编排: kernel (Scheduler) ↔ fabric (Agent Fabric + Task Fabric + MutableDAG)
    ↓
L4 生命周期: runtime (PluginBus + Manager + Memory + Arena + Evolution)
    ↓
L5 Agent通讯: agents (base/sub/peer/lease) + agentipc + agentsyscall + mcpclient
    ↓
L6 工具系统: tools/resources (core/base/builtin) + planner + toolsource + envcap
    ↓
L7 LLM栈: llmcore → llm → llmservice → llmsvcapi → llmexp
    ↓
L8 存储: ares_events (EventStore) + storage/postgres (Pool+7仓储) + evidence
    ↓
L9 知识AKG: knowledge (runtime/planner/provider/linker/compiler/adapter/retriever/store)
    ↓
L10 横切: ares_config + ares_security + ares_ratelimit + ares_shutdown + ares_callbacks + errors + logger
```

### 核心不变量

| 不变量 | 实现 |
|--------|------|
| 一个内核 | kernel 不 import runtime (架构测试锁定) |
| 一张图 | MutableDAG 是全仓唯一任务图载体 |
| 一条主线 | L2 router 是唯一生产执行路径 |
| Epoch fencing | 过期持有者不能驱动已易主任务 |
| Agent 可弃, Task 持久 | checkpoint + lease + recovery |
| 规划者不执行工具 | plannerCognition 只长图 |
| syscall 身份来自 kernel ctx | 不信 LLM 参数 |

### 依赖方向

```
errors/logger (最底层)
  → core/models (112 import, 最多)
    → llmcore → llm → llmservice → llmsvcapi
    → fabric/task → fabric/agent → kernel → agentruntime → sdk/cmd
    → fabric/task → fabric/agent → runtime
feedback (stdlib only, 保持 agentipc↔ares_evolution 无环)
```

---

## 八、优先修复建议

### P0（发布前）

1. **修复 `ExperienceRepository.Create` metadata bug** — 测试代码已标注
2. **SDK `Stream` 方法** — 要么移除接口，要么标注 `Deprecated`，不应以假实现暴露给用户

### P1（下个迭代）

3. **多租户隔离** — `memory_tools.go` 和 `knowledge_base.go` 的 tenant 隔离需在多租户部署前完成
4. **IPC retry/dead-letter** — 多 Agent 消息扩展的必要前提
5. **`GetLatestSessionForAgent` 实现** — agent recovery 路径的认知状态恢复

### P2（技术债务清理）

6. 清理 deprecated 方法（`RunScenario`、`RecordConsistency`）
7. 将逼近 1000 行的文件拆分
8. 测试中的 `time.Sleep` 迁移到 channel/WaitGroup
9. LLM streaming 路径接入生产或移除
10. `compat/` 目录在 0.4.x 决定是否删除

---

*报告完毕。所有结论基于源码核查 + 编译/测试验证 + code_rules_v2.md 交叉核对。*
