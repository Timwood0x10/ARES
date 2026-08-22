# ARES API 迁移指南（v0.3.0）

> **状态：v0.3.0 生效**
> 本文档记录 ARES 公共 API 的「旧 → 新」替代关系。v0.3.0 起，旧 `api/*` 用户面包全部标记为 `Deprecated`，
> **唯一受支持的公共 API 是 `github.com/Timwood0x10/ares/sdk`**。旧包保留一个迁移窗口期，之后会移除。

---

## 1. 为什么标记废弃

v0.2.x 时代存在**两套公共 API 面**并存：

| 层面 | 旧 API（已废弃） | 新 API（唯一入口） |
|------|-----------------|--------------------|
| 入口 | `api/client.NewClient`（客户端门面） | `sdk.NewRuntime` / `sdk.New` |
| Agent | `api/agent.Agent` 接口 + 类型别名 | `sdk.Agent`（`Run` / `Stream`） |
| 编排 | `api/graph`、`api/workflow`、`api/service/*` | `sdk.Task` + `RegisterAgent` + `Submit` |
| 记忆 | `api/memory.Manager`、`api/experience` | `sdk.WithDefaultMemory` 等选项自动装配 |
| 知识 | `api/knowledge`、`api/service/knowledge` | `rt.KnowledgeStore()` + `sdk.WithKnowledge` |
| 进化 | `api/evolution`、`api/service/evolution` | `rt.Evolve(ctx, agent, task)` + `sdk.WithEvolution` |
| 其他 | `api/bootstrap`、`api/handler`、`api/router`、`api/discovery`、`api/flight`、`api/integration` | 由 sdk 内部装配，无公共替代 |

旧层存在**重复类型**（`TokenUsage`×2、`TaskResult`×5、`AgentStatus`×3、`LLMMessage`×2）、**类型别名泄漏 internal 结构**
（`api/agent` 钉死 `models.AgentType` 等），且 CRUD 风格厚重（约 101 个导出符号），与 Agent OS
「thread 调度」的极简模型不符。新 `sdk/` 仅约 23 个导出符号，一次 `sdk.New` 完成 LLM / 记忆 / 工具 / 进化全装配。

---

## 2. 保留不废弃的包（sdk 的支撑层）

以下包**未**标记废弃，它们是 `sdk` 内部依赖的共享类型/工具层，仍可正常引用：

| 包 | 作用 |
|----|------|
| `api/core` | 共享类型与接口（`LLMConfig`、`LLMMessage`、`Tool`、`Message` 等） |
| `api/tools` | 工具注册表与工具接口（`tools.Tool`、`tools.Registry`） |
| `api/mcp` | MCP 客户端（`WithMCP` 使用） |
| `api/embedding` | Embedding 服务客户端 |
| `api/service/llm` | LLM 服务实现（fallback 链） |

> 注意：`sdk.New` 返回的 `*Runtime` 暴露了 `ToolRegistry()`、`KnowledgeStore()` 等访问器，
> 其返回类型正是 `api/tools`、`api/knowledge` 等支撑层类型——这部分类型未来会收敛到 `sdk` 内部，
> 但 v0.3.0 不破坏兼容。

---

## 3. 逐包映射表（旧 → 新）

### 3.1 `api/client` → `sdk`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `client.NewClient(&client.Config{...})` | `sdk.New(opts...)`（返回 `(*Runtime, error)`） |
| `client.NewClientFromConfigPath(path)` | `sdk.New(sdk.WithConfig(path))` |
| `client.NewClientFromDefaultPath()` | `sdk.MustNew()` / `sdk.New(sdk.WithConfigFromEnv())` |
| `client.NewSimpleClient(path)` | `sdk.New(sdk.WithConfig(path))` + `agent.Run` |
| `cl.LLM()` → `svc.Generate(...)` | `agent.Run(ctx, input)`（LLM 细节被封装） |
| `cl.Memory()` / `cl.Runtime(cfg, store)` | `sdk.New(sdk.WithDefaultMemory())`（自动装配） |
| `cl.Workflow()` → `WorkflowClient` | `rt.RegisterAgent(...)` + `rt.Submit(ctx, sdk.Task{...})` |
| `client.ConfigFile` / `LoadConfigFile` | `sdk.ConfigFile` / `sdk.LoadConfigFile` |
| `client.HealthReport` / `ServiceStatus` | 无（健康检查由运行态内部处理） |

### 3.2 `api/agent` → `sdk.Agent`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `agent.Agent` 接口 | `sdk.Agent`（`rt.NewAgent(name, opts...)`） |
| `agent.NewAgent(...)` 流程 | `rt.NewAgent(name, sdk.WithInstruction(...), sdk.WithTools(...))` |
| `Agent.Run` / `Stream` | `sdk.Agent.Run` / `sdk.Agent.Stream`（语义一致） |
| `AgentType` / `AgentStatus` / `AgentEvent` 别名 | 移除（内部 `models`/`base` 类型不再暴露） |

### 3.3 `api/bootstrap` → `sdk`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `bootstrap.New(ctx, &bootstrap.Config{...})` | `sdk.New(sdk.WithOpenAI(...), sdk.WithDefaultMemory(), ...)` |
| `bootstrap.DefaultConfig()` | 不需要（sdk 选项驱动） |
| `bootstrap.ARES` 门面 | `*sdk.Runtime` |

### 3.4 `api/memory` → sdk 选项

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `memory.NewManager(cfg)` / `NewProductionManager` | `sdk.WithDefaultMemory()` / `sdk.WithMemoryConfig(maxHistory, maxSessions)` |
| `memory.Manager` 接口 | 不需要（sdk 自动装配进 Runtime） |
| `memory.Message` / `ToolCall` / `ToolCallFunction` | `sdk.Result`（消息内部化） |
| `ToCoreMessage` / `FromCoreMessage` / `ToLLMMessage` | 移除（转换内部化） |

### 3.5 `api/experience` → sdk 选项

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `ExperienceRepository` / `ExperienceStore` 接口 | `sdk.WithRAG(topK, minScore)` / `sdk.WithDistillation(threshold)` |
| `Experience` / `StoredExperience` / `Memory` 类型 | 内部化（蒸馏/RAG 由 sdk 接线） |

### 3.6 `api/knowledge` → `rt.KnowledgeStore()`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `knowledge.KnowledgeStore` / `KnowledgePipeline` | `rt.KnowledgeStore()`（`sdk.WithKnowledge()` 启用） |
| `KnowledgeObject` / `WorkingGraph` / `Representation` / `Relation` / `Evidence` | 同上（类型由 `rt.KnowledgeStore()` 返回） |
| `Normalizer` / `EntityMatcher` / `Validator` / `Summarizer` | 同上（AKG 流水线由 sdk 装配） |
| `Query` / `Intent` / `Scope` / `Constraint` / `TokenBudget` | 同上（查询参数） |
| `ResolveResult` / `ValidationResult` / `Conflict` | 同上（结果类型） |

### 3.7 `api/graph` → sdk Task 编排

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `graph.NewGraph` / `graph.NewNode` / `graph.NewEdge` | `rt.RegisterAgent(capability, opts...)` + `rt.Submit(ctx, sdk.Task{Capability, Input})` |
| `graph.Scheduler` / `DefaultScheduler` / `PriorityScheduler` / `RoundRobinScheduler` / `WeightedFairScheduler` / `ShortJobScheduler` | 内核调度器（`internal/kernelscheduler`）自动接管 |
| `graph.State` / `Condition` / `NodeRouter` / `Result` | 内部化（DAG 由内核编排） |

### 3.8 `api/workflow` → sdk Task 编排

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `workflow.Workflow` / `workflow.Step` / `workflow.WorkflowResult` | `rt.Submit(ctx, sdk.Task{...})`（内核 fabric 调度） |
| `workflow.RetryPolicy` / `RecoveryPolicy` / `RecoveryStrategy` / `InterruptConfig` / `LoopConfig` | 内部化（checkpoint 恢复由内核负责） |
| `workflow.ConditionFunc` / `NodeRouter` / `StepStatus` / `WorkflowStatus` | 内部化 |

### 3.9 `api/evolution` → `rt.Evolve`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `evolution.NewDreamCycle(...)` / `DreamCycle` 接口 | `rt.Evolve(ctx, agent, task)` |
| `evolution.NewPopulation(...)` / `Population` 接口 | `sdk.WithEvolution()`（后台进化回路） |
| `evolution.Strategy` / `Lineage` / `PopulationConfig` / `DreamCycleConfig` / `CallbackData` | 内部化（sdk 管理进化状态） |
| `evolution.Agent` | `*sdk.Agent` |

### 3.10 `api/discovery` → `sdk.WithToolDiscovery`

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `discovery.NewEngine(cfg)` / `discovery.Engine` | `sdk.WithToolDiscovery()`（Agent 选项） |
| `discovery.RegisterRequest` / `UpdateTagsRequest` / `NewMemoryStore` | 内部化（sdk 自动装配发现注册表） |

### 3.11 `api/service/*`（全部） → sdk 内部装配

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `service/agent.New(memoryMgr)` | `rt.NewAgent(...)` / `rt.RegisterAgent(...)` |
| `service/runtime.NewService(cfg, store)` | `sdk.New(opts...)` |
| `service/workflow.NewService(cfg)` | `rt.Submit(ctx, sdk.Task{...})` |
| `service/evolution.New(cfg)` | `sdk.WithEvolution()` / `rt.Evolve(...)` |
| `service/knowledge.New(rt, comp, ret)` | `sdk.WithKnowledge()` / `rt.KnowledgeStore()` |
| `service/memory.New(cfg)` | `sdk.WithDefaultMemory()` / `sdk.WithMemoryConfig(...)` |
| `service/events.NewInMemory()` | 不需要（sdk 内部接线事件总线） |
| `service/callbacks.New()` | 不需要（sdk 内部回调注册） |
| `service/arena.New(...)` | 不需要（混沌注入由内部治理） |
| `service/dashboard.New(...)` | 不需要（观测由内部监控） |
| `service/eval.NewExactMatch()` / `NewLLMJudge` / `NewRegistry` | 不需要（质量门禁内部化） |
| `service/flight.New(...)` | 不需要（轨迹记录内部化） |

### 3.12 其余包

| 旧（Deprecated） | 新（sdk） |
|------------------|-----------|
| `api/handler.*`（HTTP handlers） | 无公共替代（HTTP 层由 sdk 运行态接管） |
| `api/router.Router` | 无公共替代（路由内部化） |
| `api/flight.FlightRecorder` / `Timeline` / `Graph` / `Genealogy` | 无公共替代（轨迹/谱系内部化） |
| `api/integration.*` | 无公共替代（仅测试辅助） |

---

## 4. 迁移示例（旧 → 新对照）

### 4.1 最小 Agent（从 NewClient 迁移）

```go
// ── 旧（Deprecated）──────────────────────────────────────
package main

import (
    "github.com/Timwood0x10/ares/api/client"
    "github.com/Timwood0x10/ares/api/core"
)

func main() {
    cl, _ := client.NewClient(&client.Config{
        BaseConfig: &core.BaseConfig{RequestTimeout: 30},
    })
    svc, _ := cl.LLM()
    resp, _ := svc.Generate(ctx, &core.GenerateRequest{Messages: msg})
    // ...
}

// ── 新（sdk 唯一入口）────────────────────────────────────
package main

import "github.com/Timwood0x10/ares/sdk"

func main() {
    rt, _ := sdk.New(
        sdk.WithOpenAI("gpt-4o-mini"),
        sdk.WithDefaultMemory(),
    )
    agent := rt.NewAgent("assistant", sdk.WithInstruction("You are helpful."))
    result, _ := agent.Run(ctx, "Hello!")
    fmt.Println(result.Content)
}
```

### 4.2 工具 + 多 Agent 协作

```go
// ── 旧（Deprecated）：api/graph + api/agent + api/service/workflow
graph := graph.NewGraph()
nodeA := graph.NewNode("researcher", researcherAgent.Run)
nodeB := graph.NewNode("writer", writerAgent.Run)
graph.AddEdge(nodeA, nodeB)
scheduler := graph.NewDefaultScheduler(graph)
result, _ := scheduler.Run(ctx)

// ── 新（sdk）：RegisterAgent + Submit
rt.RegisterAgent("researcher", sdk.WithInstruction("You research."))
rt.RegisterAgent("writer",    sdk.WithInstruction("You write."))
resultA, _ := rt.Submit(ctx, sdk.Task{Capability: "researcher", Input: "Find sources on Go."})
resultB, _ := rt.Submit(ctx, sdk.Task{Capability: "writer", Input: "Write a summary."})
```

### 4.3 进化（DreamCycle → Evolve）

```go
// ── 旧（Deprecated）
cycle, _ := evolution.NewDreamCycle(scheduler, mutator, opts...)
result, _ := cycle.Evolve(ctx, agent, goal)

// ── 新（sdk）
evolvedID, _ := rt.Evolve(ctx, agent, "Improve the response quality")
```

### 4.4 知识图谱（AKG）

```go
// ── 旧（Deprecated）
store := knowledge.NewStore(...)
pipeline := knowledge.NewPipeline(store, normalizer, matcher)

// ── 新（sdk）
rt, _ := sdk.New(sdk.WithKnowledge())
ks := rt.KnowledgeStore()
graph, _ := ks.BuildWorkingGraph(ctx, "research topic")
```

### 4.5 配置加载

```go
// ── 旧（Deprecated）
cfgFile, _ := client.LoadConfigFile("ares.yaml")
cl, _ := client.NewClientFromConfigPath("ares.yaml")

// ── 新（sdk）
cfgFile, _ := sdk.LoadConfigFile("ares.yaml")
rt, _ := sdk.New(sdk.WithConfig("ares.yaml"))
// 或一行完成
rt, _ := sdk.New(sdk.WithConfig("ares.yaml"))
```

---

## 5. 迁移步骤建议

| 步骤 | 动作 | 风险 |
|------|------|------|
| 1 | 将 `api/client.NewClient` 调用替换为 `sdk.New` + 选项 | 需理解选项语义 |
| 2 | 将 `client.LLM().Generate(...)` 替换为 `agent.Run(ctx, input)` | 返回值从 `*GenerateResponse` 变为 `*Result` |
| 3 | 将 `api/agent.Agent` 接口实现替换为 `sdk.NewAgent` | `sdk.Agent` 是具体类型（非接口） |
| 4 | 将 `api/graph` / `api/workflow` 编排替换为 `RegisterAgent` + `Submit` | Task 运行时由内核调度 |
| 5 | 移除 `api/memory.Manager` 直接调用，用 `sdk.WithDefaultMemory` / `sdk.WithMemoryConfig` | 无直接 Manager 句柄 |
| 6 | 将 `api/evolution.DreamCycle` 替换为 `rt.Evolve` | 参数更简洁 |
| 7 | 将 `api/knowledge` 直接使用替换为 `rt.KnowledgeStore()` | 需先 `sdk.WithKnowledge()` 启用 |
| 8 | 移除 `api/bootstrap.New`，用 `sdk.New` + 选项 | 配置结构不同 |

> 兼容性承诺：v0.3.0 是最后一个两套 API 面并存的版本。v0.4.0 稳定前旧包**不会物理删除**，
> 但不再获得新功能，仅接收安全修复。强烈建议在 v0.4.0 之前完成迁移。

---

## 6. 与 Agent OS 对标

新 `sdk` API 的设计对标 Agent OS 模型：

| Agent OS 概念 | sdk 对应 |
|--------------|----------|
| 线程（Agent） | `sdk.Agent`（`rt.NewAgent` / `rt.RegisterAgent`） |
| 调度（Scheduler） | `rt.Submit`（内核执行 `drain` → `Schedule` → `Acquire` → `RunQuantum` → `Yield` → `Re-Schedule`） |
| 消息（Message） | `agent.Run(ctx, input)` → `*Result` |
| Checkpoint | 自动（每个量子后 yield 保存 PCB） |
| 恢复（Recovery） | 自动（lease 过期 → 替换 agent → 从 checkpoint 继续） |
| 进化（Evolution） | `rt.Evolve(ctx, agent, task)` |

---

> 本文档随 v0.3.0 发布，后续如有变更会在 `CHANGELOG.md` 中记录。
