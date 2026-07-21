# goagent 闭环改造计划

> **目标**：将 goagent 从"静态代码库"改造为"运行时自我进化、稳定闭环的 Agent 系统"
>
> **核心原则**：每个模块必须有明确的入边（被谁调用）和出边（调用谁），不允许存在"写了但不接"的孤立代码。`ares_quant`（量化模块）除外，它是独立子系统，允许作为孤岛存在。

---

## 现状总览

| 指标 | 数值 |
|------|------|
| 总文件 | 1,254 |
| 总节点（函数/方法/类型） | 18,798 |
| 总调用边 | 4,475 |
| 孤立节点（无入边+无出边） | 11,637 |
| 调用图连通分量 | 159 个 |
| 最大连通分量 | 380 个实体 |

**核心问题**：72% 的节点是孤立的。项目写了大量代码，但调用链是断裂的。

---

## P0 — 修复 Agent 核心调用链（最高优先级）

`internal/agents/leader/` 是 Agent 的大脑，但核心函数链全部断裂。

### 问题清单

#### agent.go（LeaderAgent）
```
setStatus            → 无调用者，无被调用者  ❌
ensureInitialized    → 无调用者，无被调用者  ❌
getUserID            → 无调用者，无被调用者  ❌
fail                 → 无调用者，无被调用者  ❌
checkStepLimit       → 无调用者，无被调用者  ❌
checkAgentRunning    → 无调用者，无被调用者  ❌
emitEvent            → 无调用者，无被调用者  ❌
emitCallback         → 无调用者，无被调用者  ❌
```

#### agent_memory.go
```
initMemoryContext           → 无调用者，无被调用者  ❌
finalizeMemory              → 无调用者，无被调用者  ❌
recordExperienceFeedback    → 无调用者，无被调用者  ❌
```

#### dispatcher.go
```
executeTask  → 无调用者，无被调用者  ❌
getAgentID   → 无调用者，无被调用者  ❌
```

#### planner.go
```
generateTaskID  → 无调用者，无被调用者  ❌
createTask      → 无调用者，无被调用者  ❌
```

#### profile.go
```
toFloat64          → 无调用者，无被调用者  ❌
emitEvent          → 无调用者，无被调用者  ❌
getDefaultProfile  → 无调用者，无被调用者  ❌
parseOnce          → 无调用者，无被调用者  ❌
parseResponse      → 无调用者，无被调用者  ❌
validateProfile    → 无调用者，无被调用者  ❌
```

#### supervisor.go
```
handleFailover  → 无调用者，无被调用者  ❌
doFailover      → 无调用者，无被调用者  ❌
```

### 修复方案

```
期望闭环：

  StartService (api_impl/service.go)
      │
      ▼
  Bootstrap (ares_bootstrap/bootstrap.go)
      │
      ├──► ProvideLLM
      │       │
      │       ▼
      │   New (leader/agent.go)  ←── 目前只有 ProvideLLM 调用了 New
      │       │
      │       ├──► setStatus ────────── 应该在 Run 循环中调用
      │       ├──► ensureInitialized ── 应该在 New 末尾调用
      │       ├──► checkStepRunning ─── 应该在 Run 循环中周期调用
      │       ├──► emitEvent ────────── 应该在每个关键步骤调用
      │       ├──► emitCallback ─────── 应该在任务完成时调用
      │       ├──► executeTask ──────── 应该由 NewLeaderSupervisor 触发
      │       └──► fail ────────────── 应该在 handleFailover 中调用
      │
      ├──► ProvideEvolution
      │       │
      │       ▼
      │   NewEvolutionScheduler
      │
      └──► ProvideRuntime
              │
              ▼
          NewManager (ares_runtime/manager.go)
```

**具体操作**：

1. `New()` 末尾调用 `ensureInitialized()`
2. `NewLeaderSupervisor()` 的 `Run` 循环中，周期调用 `checkStepRunning()` + `checkAgentRunning()` + `getUserID()`
3. `Run` 循环中的任务执行路径调用 `executeTask()` → `createTask()` → `generateTaskID()`
4. `handleFailover()` 中调用 `doFailover()` + `fail()`
5. 所有关键步骤（创建任务、完成任务、失败恢复）调用 `emitEvent()` + `emitCallback()`
6. Agent 启动时调用 `initMemoryContext()`，关闭时调用 `finalizeMemory()`

---

## P1 — 打通进化闭环

`ares_evolution`（1,319 个孤立节点，全系统最多）是整个系统的进化引擎，但它和运行时、评估、记忆之间的连接是断裂的。

### 期望闭环

```
Bootstrap (ares_bootstrap/bootstrap.go)
    │
    ▼
ProvideEvolution
    │
    ├──► NewEvolutionScheduler
    │       │
    │       ├──► 触发进化周期
    │       │       │
    │       │       ▼
    │       │   NewWiredEvolutionSystem
    │       │       │
    │       │       ├──► genome_wiring.go 的 AdapterCoordinator
    │       │       │       │
    │       │       │       ├──► mutation/   ← 变异策略（当前孤立）
    │       │       │       ├──► promotion/  ← 晋升策略（当前孤立）
    │       │       │       ├──► scoring/    ← 评分策略（当前孤立）
    │       │       │       └──► selection/  ← 选择策略（当前孤立）
    │       │       │
    │       │       └──► genome_wiring_system.go 的 DefaultSystemConfig
    │       │
    │       ├──► ares_eval/  ← 评估反馈
    │       │       │
    │       │       ├──► LLMJudgeEvaluator  → 评分结果
    │       │       └──► EvaluatorRegistry   → 注册的评估器列表
    │       │
    │       └──► ares_flight/  ← 飞行记录器
    │               │
    │               └──► FlightRecorder → 记录进化历史
    │
    ├──► ProvideMemory
    │       │
    │       ├──► ares_memory/  ← 记忆存储
    │       │       ├──► context/  ← 上下文管理（当前孤立）
    │       │       └──► distillation/  ← 记忆蒸馏（当前孤立）
    │       │
    │       └──► ares_memory/strategies/  ← 记忆策略（当前孤立）
    │
    └──► ProvideRuntime
            │
            └──► ares_runtime/  ← 运行时
                    ├──► manager.go: RegisterAgentDAG → 注册 Agent 工作流
                    ├──► bus.go: PluginsByCap → 按能力查询插件
                    └──► collector.go: RouteHistory / ToolHistory / MemoryHits → 收集反馈
```

### 修复方案

#### 1.1 进化调度器 → 实际执行

```
NewEvolutionScheduler
    → NewWiredEvolutionSystem
        → AdapterCoordinator.Add()
            → mutation/ 的变异策略  ←─ 当前未接入
            → promotion/ 的晋升策略  ←─ 当前未接入
            → scoring/ 的评分策略    ←─ 当前未接入
            → genome/ 的种群管理     ←─ 当前部分接入
```

- 检查 `NewEvolutionScheduler` 的 `Run()` 是否真正调用了进化周期
- 确保 `AdapterCoordinator` 的 `Add()` 注册了所有进化策略
- 在每次进化周期结束时，调用 `FlightRecorder` 记录

#### 1.2 进化 → 评估 → 反馈

```
进化周期完成
    │
    ▼
ares_eval/EvaluatorRegistry  ←─ 当前未从进化周期触发
    │
    ├──► LLMJudgeEvaluator.Evaluate()  → 评分
    └──► FeedbackService.Record()      → 记录反馈
            │
            ▼
        ares_memory/  → 存储经验，供下次进化参考
```

- 在进化周期末尾，调用 `EvaluatorRegistry` 执行评估
- 评估结果通过 `FeedbackService` 写入 `ares_memory`
- 下次进化时，从 `ares_memory` 读取历史经验

#### 1.3 记忆系统打通

```
ares_memory/  ←─ 当前大量孤立
    ├── context/  → 会话上下文管理
    ├── distillation/  → 记忆蒸馏
    ├── strategies/  → 记忆策略
    └── report/  → 记忆报告
```

- `initMemoryContext()` → 在 Agent 启动时调用
- `finalizeMemory()` → 在 Agent 关闭时调用
- `recordExperienceFeedback()` → 在进化评估完成后调用
- 记忆蒸馏 → 定期将短期记忆压缩为长期记忆

---

## P2 — 消除孤岛模块

除 `ares_quant` 外，所有模块必须闭环。

### 模块闭环状态

| 模块 | 孤立节点 | 必须闭环 | 状态 |
|------|---------|---------|------|
| `ares_evolution` | 1,319 | ✅ | 见 P1 |
| `tools` | 1,048 | ✅ | 工具注册链检查 |
| `storage` | 970 | ✅ | 多 store 实现需全部接入 |
| **`ares_quant`** | **847** | **❌ 允许孤岛** | **独立量化子系统** |
| `monitoring` | 713 | ✅ | 监控指标需被 dashboard 消费 |
| `ares_memory` | 631 | ✅ | 见 P1 |
| `workflow` | 615 | ✅ | 工作流引擎需被 Agent 执行链调用 |
| `knowledge` | 555 | ✅ | 知识库需被 tools 调用 |
| `ares_runtime` | 555 | ✅ | 见 P1 |
| `agents` | 443 | ✅ | 见 P0 |
| `ares_arena` | 401 | ✅ | 竞技场测试框架需被 eval 调用 |
| `ares_mcp` | 293 | ✅ | MCP 协议层需被 tools 调用 |
| `llm` | 285 | ✅ | LLM 客户端需被 agents 调用 |
| `ares_events` | 256 | ✅ | 事件系统需被所有模块共享 |
| `dashboard` | 237 | ✅ | 仪表盘需消费监控数据 |
| `ares_eval` | 223 | ✅ | 见 P1 |
| `ares_flight` | 204 | ✅ | 见 P1 |
| `ares_observability` | 176 | ✅ | 可观测性需被全局接入 |
| `plugins` | 173 | ✅ | 插件系统需被 runtime 加载 |
| `api_impl` | 154 | ✅ | API 实现层需被 cmd 调用 |
| `ares_shutdown` | 126 | ✅ | 关闭钩子需被全局注册 |
| `ares_protocol` | 125 | ✅ | 协议层需被 MCP 调用 |
| `core` | 104 | ✅ | 核心模型需被所有模块使用 |
| `discovery` | 101 | ✅ | 服务发现需被 MCP 调用 |
| `ares_bootstrap` | 60 | ✅ | 启动引导（已部分接入） |
| 其余小模块 | ~300 | ✅ | 全部需接入调用链 |

### 2.1 tools 层修复

`tools`（1,048 孤立节点）是 Agent 的工具库，所有工具必须通过 `RegisterGeneralTools` → `Register` 注册到 `Registry`。

**检查点**：
- `RegisterGeneralTools` 是否调用了所有工具的 `New*` 函数
- 每个工具的 `New*` 函数是否调用 `NewBaseTool` 或 `NewBaseToolWithCapabilities`
- `Register` 是否将工具添加到 `Registry` 的 `toolGroups` 中
- 工具链末端：`FindCapability` → `BuiltinCapabilities` 是否包含了所有注册的工具

### 2.2 storage 层修复

`storage`（970 孤立节点）有多个存储实现，需要确认：

- `memory/store.go`、`sqlite/store.go`、`postgres/store.go` 是否都通过 `Store` interface 接入
- 在 `Bootstrap` 中根据配置选择正确的 store 实现
- 未使用的 store 实现可标记为 `// TODO: 按编译标签启用`

### 2.3 monitoring 层修复

`monitoring`（713 孤立节点）的监控指标需要被 `dashboard` 消费：

- `monitoring/http_api.go` 的 `NewHTTPServer` + `WithDashboardAPI` → 需在 `StartService` 中启动
- `monitoring/main_page.go` 的 `Tab` 定义 → 需在 dashboard 中注册
- 监控指标 → 需通过 `ares_observability` 暴露给 Prometheus

---

## P3 — 稳定性加固

### 3.1 补全知识图谱

当前 `CODESCOPE_SKIP_ASYNC=1` 跳过了知识图谱构建，导致：

```
modules 表为空           → get_module_tree 不工作
architecture_edge 表为空  → detect_architecture_drift 不工作
capability 表为空        → detect_capability_drift 不工作
module_summary 表为空    → 看不到模块健康度
code_fts 表为空          → search 不可用
```

**修复**：运行 `enhance_project` 补全所有知识图谱数据。

### 3.2 添加 CI 检查

```yaml
# .github/workflows/closure-check.yml
# 每次 PR 检查：
#   1. 是否有新的死函数产生（verify_integrity）
#   2. 调用图连通分量是否增加
#   3. 是否有新的孤立模块
```

### 3.3 错误处理链

- `internal/errors/wrap.go` 的 `Wrap`/`Wrapf` 已被广泛使用 ✅
- 检查所有返回 `error` 的函数，确保调用方检查了返回值
- 使用 `Wrap` 而不是裸 `return err`，保持错误追踪链完整

### 3.4 启动/关闭顺序

```
启动顺序（Bootstrap 中已部分实现，需确认完整）：
  1. Logger
  2. Storage
  3. Events
  4. Memory
  5. LLM
  6. MCP
  7. Runtime
  8. Evolution
  9. Dashboard
  10. API Server

关闭顺序（ares_shutdown 需接入）：
  1. API Server 停止接受新请求
  2. Evolution 停止进化周期
  3. Runtime 保存状态
  4. Memory 持久化
  5. Storage 关闭
```

---

## 执行计划

### 阶段 1: Agent 核心闭环（P0）

| # | 任务 | 文件 | 预估 |
|---|------|------|------|
| 1 | `New()` 末尾调用 `ensureInitialized()` | `internal/agents/leader/agent.go` | 0.5d |
| 2 | Supervisor Run 循环中调用 checkStepRunning/checkAgentRunning/getUserID | `internal/agents/leader/supervisor.go` | 1d |
| 3 | 任务执行路径调用 executeTask → createTask → generateTaskID | `internal/agents/leader/dispatcher.go` + `planner.go` | 1d |
| 4 | handleFailover 中调用 doFailover + fail | `internal/agents/leader/supervisor.go` | 0.5d |
| 5 | 关键步骤调用 emitEvent + emitCallback | `internal/agents/leader/agent.go` + `profile.go` | 1d |
| 6 | Agent 启动/关闭时调用 initMemoryContext/finalizeMemory | `internal/agents/leader/agent_memory.go` | 0.5d |
| 7 | SubAgent 同步修复 | `internal/agents/sub/agent.go` + `executor.go` | 1d |

### 阶段 2: 进化闭环（P1）

| # | 任务 | 文件 | 预估 |
|---|------|------|------|
| 1 | 进化周期末尾调用 EvaluatorRegistry | `internal/ares_evolution/scheduler.go` | 1d |
| 2 | 评估结果通过 FeedbackService 写入 ares_memory | `internal/ares_evolution/` + `ares_experience/` | 1d |
| 3 | 进化策略通过 AdapterCoordinator 注册 | `internal/ares_evolution/genome_wiring.go` | 1d |
| 4 | 进化历史通过 FlightRecorder 记录 | `internal/ares_evolution/` + `ares_flight/` | 0.5d |
| 5 | 记忆系统打通：context/distillation/strategies | `internal/ares_memory/` | 1.5d |

### 阶段 3: 孤岛消除（P2）

| # | 任务 | 文件 | 预估 |
|---|------|------|------|
| 1 | 工具注册链完整性检查 | `internal/tools/` | 1d |
| 2 | 存储实现按配置选择 | `internal/storage/` | 1d |
| 3 | 监控数据接入 dashboard | `internal/monitoring/` + `dashboard/` | 1d |
| 4 | 工作流引擎被 Agent 执行链调用 | `internal/workflow/` + `agents/` | 1d |
| 5 | 知识库被 tools 正确调用 | `internal/knowledge/` + `tools/` | 0.5d |
| 6 | 竞技场被 eval 调用 | `internal/ares_arena/` + `ares_eval/` | 0.5d |
| 7 | MCP 协议层被 tools 调用 | `internal/ares_mcp/` + `tools/` | 0.5d |
| 8 | LLM 客户端被 agents 调用 | `internal/llm/` + `agents/` | 0.5d |
| 9 | 事件系统全局接入 | `internal/ares_events/` | 0.5d |
| 10 | 关闭钩子全局注册 | `internal/ares_shutdown/` | 0.5d |

### 阶段 4: 稳定性加固（P3）

| # | 任务 | 文件 | 预估 |
|---|------|------|------|
| 1 | 运行 enhance_project 补全知识图谱 | 运行命令 | 0.5d |
| 2 | 添加 CI 死函数检查 | `.github/workflows/` | 1d |
| 3 | 错误处理链审计 | 全局 | 1d |
| 4 | 启动/关闭顺序确认 | `internal/ares_bootstrap/` | 0.5d |

---

**总计**：约 20 人天

**关键里程碑**：
- 第 1 周：Agent 核心闭环（P0）→ `internal/agents/leader/` 零孤立函数
- 第 2 周：进化闭环（P1）→ `ares_evolution` ←→ `ares_runtime` ←→ `ares_eval` 完整回路
- 第 3 周：孤岛消除（P2）→ 除 `ares_quant` 外所有模块有入边有出边
- 第 4 周：稳定性加固（P3）→ CI 集成 + 知识图谱完整

---

## 验收标准

- [ ] `internal/agents/leader/` 所有函数有调用者
- [ ] 进化周期完整执行：scheduler → evolution → eval → memory → scheduler
- [ ] 除 `ares_quant` 外，所有模块的孤立节点数 ≤ 总节点数的 10%
- [ ] 调用图连通分量从 159 个减少到 30 个以内
- [ ] `verify_integrity` 报告 0 个 DeadFunction 发现
- [ ] `enhance_project` 后知识图谱完整
- [ ] CI 中每次 PR 自动检查调用链完整性