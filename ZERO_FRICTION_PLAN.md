# ARES Zero-Friction Agent Plan

> 目标：用户只需要几行 Go 代码 + 一份 YAML 配置，就能启动一个全能 agent。
> 蒸馏、Tools、MCP、AKG、压缩、进化全部"无感"自动运作，
> agent 能自己进化、自己操作，用户无需过多干涉。

---

## 一、愿景

### 用户最终体验

```yaml
# ares.yaml — 唯一配置
llm:
  provider: openai
  model: gpt-4o
memory:
  enabled: true
  enable_distillation: true
  distillation_threshold: 3
knowledge:
  enabled: true
evolution:
  enabled: true
tools:
  builtin: true
mcp:
  config: .mcp.json
```

```go
// main.go
package main

import "github.com/Timwood0x10/ares/sdk"

func main() {
    rt := sdk.MustNew(sdk.WithConfig("ares.yaml"))
    defer rt.Close()

    agent := rt.NewAgent("assistant")
    result, _ := agent.Run(ctx, "帮我分析这份文档")
}
```

### 启动后自动激活（用户无感）

| 能力 | 触发条件 | 当前状态 |
|---|---|---|
| **Memory Distill** | config 启用 | ⚠️ 蒸馏存储已就绪，事件驱动触发缺失 |
| **Context 压缩** | 自动 | ✅ 已注入 BuildContext |
| **AKG RAG** | knowledge 启用 | ✅ MemoryRetriever + KnowledgeRetriever 已 wiring |
| **AKG 知识上下文注入** | knowledge 启用 | ✅ buildMessages 自动执行 Execute + Compile |
| **Evolution** | config 启用 | ❌ 未联动 |
| **MCP Tools** | `.mcp.json` 存在 | ✅ `WithMCP()` 支持 |
| **AKF Tools** | knowledge 启用 | ❌ 未注册到 tool registry |
| **自定义 Tools** | 无 | ✅ 用户需手动 `Register()` |
| **YAML 驱动** | 用户传入 | ✅ `sdk.WithConfig()` + `sdk.WithConfigFromEnv()` |

---

## 二、各子系统运作机制

### 2.1 AKG（ARES Knowledge Fabric）

```
用户输入 / 文档 / 代码
  │
  ├─→ planner.KnowledgePlanner        ← 解析意图，规划知识图谱
  ├─→ provider.ProviderRegistry       ← 多数据源（memory / evolution / code / mysql / postgres）
  ├─→ runtime.KnowledgeRuntime        ← 构建知识图谱（图结构）
  ├─→ compiler.Compiler.Compile()     ← 编译为 LLM 格式（prompt / markdown / json）
  │
  ▼
两种消费路径：
  ├─ AKF MCP Tools  → agent 主动调用（知识问答、检索）  [仅 serve.go，SDK 中缺失]
  └─ KnowledgeRetrieverAdapter → ContextRetriever → BuildContext → prompt（RAG 被动注入）[SDK ✅]
```

**本质**：知识图谱的 ETL + 检索管道。原始信息自动构建图谱，编译成 LLM 可用格式，在对话中通过 RAG 或工具调用注入上下文。

### 2.2 Memory Distill（蒸馏双路）

```
对话完成 → EventStore 发出 TaskCompleted 事件         [SDK 未监听]
  │
  ▼
handleTaskCompletedForDistillation                     [SDK 中缺失]
  │
  ▼
DistillationService（调用 LLM 压缩对话为结构化 Experience）  [wireMemory 中已构建]
  │
  ▼
ExperienceRepository 存储（带向量嵌入）                  [已就绪]
  │
  ▼
下次对话 → MemoryRetriever.Retrieve() → BuildContext → prompt  [wireSDKRetrievers ✅]
```

**本质**：agent 的"长期记忆"。将历史对话精华提炼为 Experience，下次遇到相似问题自动调取。

### 2.3 Context 压缩

```
GetMessages(sessionID) → 原始对话历史
  │
  ▼
ContextCleaner.Clean()                                 [✅ 在 BuildContext 中自动执行]
  ├─ compressCodeBlocks()   ← 长代码块 → 摘要
  ├─ tool_call/tool_result  ← 压缩到首句，保留因果链
  └─ assistant reasoning    ← 截断冗余
  │
  ▼
压缩后的历史 + RAG 上下文 → prompt → LLM
```

**本质**：token 预算管理器。在不丢失关键信息的前提下，消除 tool 噪音和重复内容。

### 2.4 Tools / MCP 系统

```
工具来源：
  ├ 用户代码 Register(customTool)     ← 自定义 ✅
  ├ sdk.WithMCP() → MCP Client        ← 外部 MCP 服务器 ✅
  └ AKF MCP Tools（akf_mcp）           ← 知识工具 ❌ SDK 缺失
      │
      ▼
tools.Registry（全局注册表）
      │
      ▼
agent ReAct 循环 → LLM 选择 → 执行 → 结果回喂
```

---

## 三、当前现状 vs 目标差距（基于实际代码审计）

### SDK 当前实力（比想象中强）

经过实际代码审计，`sdk/sdk.go` + `sdk/memory_wiring.go` 中已实现：

| 功能 | 文件/位置 | 状态 |
|---|---|---|
| `sdk.WithConfig("ares.yaml")` | `sdk/options.go:28` | ✅ |
| `sdk.WithConfigFromEnv()` | `sdk/options.go:50` | ✅ |
| `sdk.LoadConfigFile()` + `ToOptions()` | `sdk/config.go:127` | ✅ |
| `wireMemory()` → `NewMemoryManagerWithDistiller` | `sdk/memory_wiring.go:402` 行 429 | ✅ |
| `wireSDKRetrievers()` → MemoryRetriever + KnowledgeRetriever | `sdk/memory_wiring.go:549` | ✅ |
| `SetRetrievers()` → 注入 MemoryManager | `sdk/memory_wiring.go:592` | ✅ |
| `buildMessages` → `memMgr.BuildContext()`（RAG + 压缩） | `sdk/sdk.go:917` | ✅ |
| `buildMessages` → `knowledgeRT.Execute()` + `Compile()` | `sdk/sdk.go:927-940` | ✅ |
| AKG context 注入到 prompt | `sdk/sdk.go:926` | ✅ |
| EventStore 创建、事件写入 | `sdk/sdk.go:482`, 843, 870 | ✅ |
| 蒸馏依赖自动 fallback | `sdk/memory_wiring.go:415-426` | ✅ |

### 差距总表

| 维度 | 当前状态 | 目标状态 | 缺口 |
|---|---|---|---|
| **YAML 驱动** | `WithConfig()` ✅ `LoadConfigFile()` ✅ | 已是默认入口 | **无** ✅ |
| **蒸馏 RAG 注入** | `wireSDKRetrievers` ✅ MemoryRetriever ✅ | 已注入 BuildContext | **无** ✅ |
| **ContextCleaner 压缩** | `BuildContext` 内部调用 ✅ | 已随 ProductionMemoryManager 启用 | **无** ✅ |
| **ProductionMemoryManager** | `wireMemory` 自动选择 ✅ | 蒸馏时用 `NewMemoryManagerWithDistiller` | **无** ✅ |
| **PostgreSQL/持久化** | `buildKnowledgeStore` 自动选择 ✅ | 按配置选 SQLite → PG → 内存 | **无** ✅ |
| **AKF Tools 注册** | 知识上下文注入 ✅ 但 tools 未注册 ❌ | SDK 自动注册 AKF 工具 | **小** |
| **Evolution 联动** | `knowledgeEnabled` 可注入 ✅ | `UpdateLiveKnowledgeRuntime` 自动调用 | **中** |
| **事件驱动蒸馏** | EventStore 已创建 ✅ 但未订阅事件 ❌ | 对话完成自动触发蒸馏 | **中** |
| **AKF Tools 注册** | 仅 serve.go 中 `akf_mcp.NewAKFService` | SDK New() 内自动检测注册 | 0.5 天 |
| **Evolution 热更新** | 仅 serve.go 中 `UpdateLiveKnowledgeRuntime` | SDK 自动 wiring | 1-2 天 |
| **事件驱动蒸馏** | EventStore 已有，但无 Subscriber | 注册 `handleTaskCompletedForDistillation` | 2-3 天 |

### 整体完成度（修订版）

| 阶段 | 完成度 | 说明 |
|---|---|---|
| 各子系统功能实现 | **95%** | AKG / Distill / 压缩 / Tools / MCP / Evolution 全部实现 |
| SDK 路径功能完整性 | **85%** | 蒸馏 RAG ✅ 压缩 ✅ AKG 上下文注入 ✅ YAML 驱动 ✅ |
| 极简用户入口（3 行 Go + yaml） | **80%** | `WithConfig()` 已实现，示例跑通即可 |
| YAML 声明式驱动 | **90%** | config 结构完善 + SDK 已消费 |
| **整体** | **85%** | 核心管道通了，剩 3 个小缺口 |

---

## 四、剩下的三个缺口（按优先级）

### ✅ 已完成的（6 个 Phase 中的前 3 个）

- **Phase 1（YAML 驱动）** — `sdk.WithConfig()` 已完成，`sdk.WithConfigFromEnv()` 也完成了
- **Phase 2（蒸馏+压缩注入 SDK）** — `wireMemory` + `wireSDKRetrievers` + `BuildContext` 全部 wiring 完毕
- **Phase 3（ProductionMemoryManager）** — SDK 已按 config 自动选择 `NewMemoryManagerWithDistiller` 或基础版

### ❌ 仍需要做的

#### 缺口 1：AKF Tools 自动注册到 SDK 工具注册表（0.5 天）

**当前状态**：AKG 知识通过 `knowledgeRT.Execute` + `Compiler.Compile` 注入到 prompt，但 AKF 知识工具（MCP 风格的工具）未注册到 tool registry。

**改动范围**：
```go
// sdk/sdk.go — New() 中，AKF 知识工具注册
if cfg.knlCfg.Enabled {
    akfSvc := akf_mcp.NewAKFService(kw.rt, &compiler.DefaultCompiler{})
    for _, akfTool := range akfSvc.Tools() {
        toolReg.Register(&akfToolAdapter{name: akfTool.Name, desc: akfTool.Description, fn: akfTool.Execute})
    }
}
```

**依赖**：`internal/knowledge/mcp`（仅 serve.go 引用，SDK 未引用）

---

#### 缺口 2：Evolution 热更新自动 wiring（1-2 天）

**当前状态**：SDK 中 `knowledge.enabled` 和 `evolution.enabled` 各自工作，但缺少 `serve.go:387` 的 `comp.NewEvolution.UpdateLiveKnowledgeRuntime(comp.KnowledgeRuntime)` 调用，进化系统的知识补丁无法影响运行中的知识引擎。

**改动范围**：
```go
// sdk/sdk.go — New() 中，evolution 完成时
if cfg.evoCfg.Enabled && kw.rt != nil {
    // 共享 knowledge runtime 给进化系统
    evoExecutor := knowledgeruntime.NewKnowledgePatchExecutor(kw.rt)
    // ... 注入到进化管线
}
```

---

#### 缺口 3：事件驱动蒸馏自动化（2-3 天）

**当前状态**：
- EventStore 已创建（`sdk/sdk.go:482`）
- Agent 每次 tool call 已写入事件（`sdk/sdk.go:843, 870`）
- 蒸馏依赖已构建（`wireMemory` 中创建了 DistillationService）
- **但**：没有注册 `TaskCompleted` 事件监听器来自动触发蒸馏

**改动范围**：
```go
// sdk/memory_wiring.go — wireMemory 末尾
if distillSvc != nil && eventStore != nil {
    // 订阅事件 → 自动蒸馏
    go distillLoop(ctx, eventStore, distillSvc, tenantID)
}
```

让 agent `Run()` 完成后自动 emit `TaskCompleted` 事件，蒸馏循环自动消费。

---

### 剩余工作量汇总

| 缺口 | 工作量 | 依赖 |
|---|---|---|
| AKF Tools 自动注册 | 0.5 天 | 无（纯新增代码） |
| Evolution 热更新 | 1-2 天 | 需理解 evolution/knowledge 接口 |
| 事件驱动蒸馏 | 2-3 天 | 需设计事件消费循环 |
| **合计** | **4-6 天** | |

---

## 五、架构原则

1. **YAML 唯一真相源**：`ares.yaml` 决定所有行为，Go 代码只做 `sdk.WithConfig()`
2. **渐进式复杂**：缺省 = 智能默认，启用 = 自动 wiring，无需用户显式配置管道
3. **向后兼容**：SDK 路径变动不影响现有 `cmd/ares/serve.go` 生产部署
4. **可观测但不可见**：用户可以通过 API 查询蒸馏结果、知识图谱、进化状态，但日常无需关心
5. **公共 API 接口保留**：`api/` 下的接口仍可被高级用户直接调用自定义实现

---

## 六、附录：关键文件索引

| 路径 | 作用 |
|---|---|
| `sdk/sdk.go` | **SDK 入口**，所有 wiring 已内聚于此（"一切从这里开始"） |
| `sdk/memory_wiring.go` | 记忆 + 蒸馏 + RAG wiring，缺口 2/3 的改动位置 |
| `sdk/options.go` | SDK Option 定义，`WithConfig()` 在此 |
| `sdk/config.go` | YAML 配置加载 + 校验，`LoadConfigFile()` + `ToOptions()` |
| `internal/ares_bootstrap/bootstrap.go` | 生产级组件组合，SDK 的灵感来源 |
| `internal/ares_memory/manager_impl.go` | 记忆管理器实现（含 BuildContext） |
| `internal/ares_memory/production_manager.go` | 生产级记忆管理器（含压缩） |
| `internal/ares_memory/context/cleaner.go` | ContextCleaner 实现（已通过 BuildContext 启用） |
| `internal/ares_memory/context/memory_retriever.go` | MemoryRetriever（蒸馏经验检索，已 wiring） |
| `internal/knowledge/adapter/context_retriever.go` | KnowledgeRetrieverAdapter（AKG 检索，已 wiring） |
| `internal/knowledge/mcp/mcp.go` | AKF MCP 工具服务（缺口 1 的依赖） |
| `internal/ares_memory/distillation/distiller.go` | 蒸馏引擎 |
| `internal/ares_experience/` | 蒸馏 DB 路 |
| `internal/knowledge/` | AKG 原始知识引擎 |
| `cmd/ares/serve.go` | 生产服务器（缺口 1/2 的"参考实现"） |
| `internal/ares_bootstrap/provide_new_evolution.go` | 进化系统构造器（缺口 2 的依赖） |

---

*文档版本：2026-07-25（修订版） | 对应 dev 分支 HEAD d2bce720*
