# ARES v2 — 开发文档

> 融合 `next_step.md`（产品战略）与 `improve.md`（架构评审）的实操路线图。
>
> 核心哲学：**ARES is an Evidence-Driven Autonomous Runtime where every optimization, recovery, adaptation, and evolution is represented as a Runtime Patch.**

---

## 一、定位

```
                 LangChain                     Focus on Components
                 LangGraph                     Focus on Workflow
                 CrewAI                        Focus on Collaboration
                 ARES                          Focus on Autonomous Runtime Evolution
```

ARES 不跟 LangChain 拼生态。ARES 的核心竞争力是**开放的扩展协议 + 会进化的运行时**。

```
ARES is not batteries included. ARES is evolution included.
```

---

## 二、两条线

```
ARES
├── Compatibility（兼容层）— 解决"我能不能接入你的框架？"
│
└── Innovation（创新层）  — 解决"为什么我要选你的框架？"
```

### 兼容层原则

官方只维护 **80% 用户需要的 20% 组件**，剩下全部 Plugin：

| 类别 | 官方维护 | 社区 Plugin |
|------|----------|-------------|
| LLM | OpenAI, Ollama | Anthropic, Gemini, DeepSeek... |
| Vector | pgvector | Chroma, Qdrant, Milvus... |
| Loader | Markdown, HTML, PDF | Office, CAD, LaTeX... |
| Protocol | OpenAI API, MCP | gRPC, TRPC... |

### 创新层排名

| 优先级 | 方向 | 价值 |
|--------|------|------|
| ★★★★★ | **Graph Structure Evolution** — Workflow 拓扑进化 | 论文级创新，核心方向 |
| ★★★★★ | **Self-Healing Runtime** — Chaos → Patch → Apply | 真正的 Adaptive Runtime |
| ★★★★☆ | **Cognitive Knowledge Runtime** — AKF 升级版 | 差异化能力 |
| ★★★★☆ | **Execution River** — Pull → Push | v2 再考虑 |

---

## 三、核心架构

### 数据流闭环

```
Execution
    │
    ▼
Evidence Collection    ← Flight / Chaos / Memory / Tool / Metrics
    │
    ▼
Knowledge Runtime     ← AKF: Evidence → KnowledgeObject → Graph
    │
    ▼
Evolution Coordinator ← 汇聚 GA / Chaos / LLM / Human 信号
    │
    ▼
Runtime Patch         ← 唯一修改 Runtime 的方式
    │
    ▼
Mutable Runtime       ← RuntimeComponent.Apply(Patch)
    │
    ▼
Execution
```

### 两个核心协议

```
输入协议：Evidence     — 所有子系统输出 Evidence
输出协议：RuntimePatch — 所有修改必须通过 Patch
```

**铁律：ARES 内部所有能够改变 Runtime 的行为，都必须产出 RuntimePatch；所有能够驱动 Evolution 的输入，都必须归一为 Evidence。**

---

## 四、架构改进（来自 improve.md 的三刀）

### 刀1：Genome 不应该直接 Encode Patch

```
当前：  Genome → Encode() → RuntimePatch
改进：  Genome A → Diff Engine → RuntimePatch
        Genome B →              (Diff 负责生成 Patch)
```

Genome 的职责只有：Mutation、Fitness、Crossover。Patch 由 Diff Engine 比较 Genome A ↔ B 生成。

### 刀2：Coordinator 不应该自己 Mutation

```
当前：  Coordinator → Genome.Mutate() → Fitness → Patch
改进：  GA / Chaos / LLM / Human 各自跑
        Coordinator 只负责：Apply？Reject？Delay？Canary？
```

Coordinator 不知道 Patch 是谁生成的，只知道 Source（GA/Chaos/LLM/Human）。

### 刀3：Evidence 不应该只有 Metric

```
type Evidence struct {
    Source    string        // flight, chaos, memory, akf, genome, llm
    Kind      EvidenceKind  // ExecutionTrace, Failure, Knowledge, Insight, Fitness, Critique
    Payload   any           // 任意结构化数据
    Metadata  map[string]string
    Timestamp time.Time
    TTL       time.Duration
}
```

---

## 五、Phase 路线图

### Phase 0 ✅ 已完成 — Runtime Compatibility Layer

**目录：** `compat/`

| 组件 | 状态 |
|------|------|
| `compat/llm/openai` | ✅ 真实实现 |
| `compat/llm/ollama` | ✅ 真实实现 |
| `compat/vector/pgvector` | ✅ 真实实现 |
| `compat/loader/markdown` | ✅ 可用 |
| `compat/loader/html` | ✅ 可用 |
| `compat/loader/pdf` | ✅ 绑 ledongthuc/pdf |
| `compat/protocol/mcp` | ✅ 绑 ares_mcp.MCPClient |
| `compat/protocol/openai_api` | ✅ v1+v2/v3 完整端点 |
| `compat/tool/builtin` | ✅ 骨架 |

**注册入口：**
```go
compat.RegisterLLM(name, factory)
compat.RegisterVector(name, factory)
compat.RegisterLoader(name, factory)
compat.RegisterProtocol(name, factory)
```

---

### Phase 1 — Mutable Runtime（统一 RuntimeComponent 接口）

**目标：** 所有可变的子系统实现统一的 `RuntimeComponent` 接口

**接口：**
```go
type RuntimeComponent interface {
    Name() string
    Snapshot(ctx context.Context) (any, error)
    Apply(ctx context.Context, patch RuntimePatch) (*RuntimePatch, error)
    CanApply(ctx context.Context, patch RuntimePatch) error
}
```

**需要接入的子系统：**

| 子系统 | 当前状态 | 接入进展 |
|--------|----------|----------|
| MutableDAG | `internal/workflow/graph/` | ✅ GraphPatchExecutor 已实现 |
| Scheduler | `internal/workflow/graph/` | ✅ 已通过 DAG 接入 |
| Planner | `internal/knowledge/runtime/` | ✅ KnowledgePatchExecutor 已实现 |
| Knowledge Runtime | `internal/knowledge/runtime/` | ✅ 已接入 |
| Recovery | `internal/workflow/engine/` | ✅ RecoveryPatchExecutor 已实现 |
| Memory | `internal/ares_memory/` | ⬜ 待接入 |

**完成标准：**
- [ ] 所有子系统实现 `RuntimeComponent`
- [ ] `Registry.RegisterComponent()` 统一注册
- [ ] Coordinator 通过 `RuntimeComponent` 接口调度
- [ ] 旧 `Register()` 路径废弃

---

### Phase 2 — Evidence Runtime（统一证据协议）

**目标：** 所有子系统输出统一 `Evidence`，Knowledge Runtime 消费 Evidence 产出 Knowledge

**接口：**
```go
type Evidence struct {
    ID        string
    Source    string        // "flight", "chaos", "memory", "akf", "genome", "llm"
    Kind      EvidenceKind  // "execution_trace", "failure", "knowledge", "insight", "fitness", "critique"
    Payload   json.RawMessage
    Metadata  map[string]string
    Timestamp time.Time
    TTL       time.Duration
}

type Store interface {
    Append(ctx context.Context, e Evidence) error
    Query(ctx context.Context, filter Filter) ([]Evidence, error)
    Aggregate(ctx context.Context, filter Filter, fn AggregateFn) (float64, error)
}
```

**需要接入的子系统：**

| 子系统 | 当前状态 | 接入进展 |
|--------|----------|----------|
| Flight Recorder | `internal/ares_flight/` | ⬜ 待接入 |
| Chaos Arena | `internal/ares_arena/` | ⬜ 待接入 |
| Memory Distillation | `internal/ares_memory/` | ⬜ 待接入 |
| AKF | `internal/knowledge/` | ⬜ 待接入 |
| GA | `internal/ares_evolution/` | ⬜ 待接入 |

**完成标准：**
- [ ] Evidence 统一接口已实现（`internal/evidence/` ✅ 已有，但需确认所有子系统接入）
- [ ] Flight → `KindExecutionTrace`
- [ ] Chaos → `KindFailure`
- [ ] Memory → `KindKnowledge`
- [ ] AKF → `KindInsight`
- [ ] GA → `KindFitness`
- [ ] LLM → `KindCritique`

---

### Phase 3 — Runtime Genome（可进化组件协议）

**目标：** Genome 接口化，GA 不知道具体 Genome 类型

**接口：**
```go
type Genome interface {
    ID() string
    Mutate() Genome
    Crossover(other Genome) Genome
    Fitness(evidence []Evidence) float64
}
```

**注册式 Genome：**

| Genome | 负责 | 当前状态 |
|--------|------|----------|
| WorkflowGenome | DAG 拓扑变异 | ✅ 已实现 |
| KnowledgeGenome | Knowledge 配置变异 | ✅ 已实现 |
| SchedulerGenome | 调度策略变异 | ✅ 已实现 |
| RecoveryGenome | 恢复策略变异 | ✅ 已实现 |
| PlannerGenome | Planner 策略变异 | ⬜ 待实现 |
| MemoryGenome | Memory 参数变异 | ⬜ 待实现 |
| PromptGenome | Prompt 模板变异 | ⬜ 待实现 |

---

### Phase 4 — Evolution Coordinator（演化大脑）

**目标：** 汇聚所有信号，决策 Apply/Reject/Delay/Canary

```
            Runtime
               │
               ▼
      Evolution Coordinator
   ┌──────┼───────┐
   ▼      ▼       ▼
 Chaos   Genome   AKF
   ▼      ▼       ▼
        Decision
           ▼
      Runtime Patch
```

**Coordinator 职责：**
- 这个 Patch 能不能应用？
- 风险多大？
- 现在负载高吗？
- 需不需要灰度？
- 是不是先 Canary？

**完成标准：**
- [ ] Coordinator 接口定义
- [ ] 支持 Apply / Reject / Delay / Canary 四种决策
- [ ] 接入 GA / Chaos / LLM / Human 四个 Source
- [ ] 决策日志可追溯

---

### Phase 5 — Runtime Patch 统一（唯一修改语言）

**目标：** 所有修改必须通过 Patch，禁止直接修改 Runtime

**接口：**
```go
type RuntimePatch struct {
    Component string      // 目标组件名
    Action    PatchType   // InsertNode, RemoveNode, ReplaceNode, ChangeScheduler, ChangePlanner...
    Payload   any         // 具体参数
    Source    string      // GA, Chaos, LLM, Human
    Reason    string      // 修改原因
    Rollback  *RuntimePatch // 回滚 Patch（可选）
}

type PatchType int
const (
    PatchInsertNode      PatchType = iota  // 插入节点
    PatchRemoveNode                         // 删除节点
    PatchReplaceNode                        // 替换节点
    PatchChangeScheduler                    // 修改调度器
    PatchChangePlanner                      // 修改 Planner
    PatchChangeReducer                      // 修改 Reducer
    PatchChangeBudget                       // 修改预算
    PatchChangeRecovery                     // 修改恢复策略
)
```

**完成标准：**
- [ ] 所有 PatchType 定义完整
- [ ] 每个 RuntimeComponent 支持 CanApply 预检
- [ ] 自动回滚（Rollback 链）
- [ ] Patch 历史可追溯

---

### Phase 6 — Graph Structure Evolution（论文级创新）

**目标：** Workflow 拓扑进化，非 Prompt 进化

**Mutation 操作：**
```
InsertNode    — 插入新节点
DeleteNode    — 删除节点
ReplaceNode   — 替换节点
Swap          — 交换节点顺序
Parallelize   — 串行 → 并行
Serialize     — 并行 → 串行
Split         — 拆分节点
Merge         — 合并节点
```

**Fitness 来源：** Evidence（执行时间、成功率、延迟、Token 消耗...）

---

### Phase 7 — Self-Healing Runtime（自适应运行时）

**目标：** Runtime 自己修，不用重跑、不用 Resume

```
Chaos → 发现 Node Crash
    → Coordinator → 生成 ReplaceNode Patch
    → MutableDAG → Apply
    → 继续执行
```

---

### Phase 8 — Execution River（Reactive Runtime）

**目标：** Pull → Push，事件驱动

```
Event → Subscription → Execution → Event → Execution → ...
```

---

## 六、目录结构（建议）

```
ares/
├── compat/            # 兼容层（生态入口）
│   ├── llm/           # LLM provider 适配器
│   ├── vector/        # Vector store 适配器
│   ├── loader/        # Document loader
│   ├── protocol/      # 协议适配器（OpenAI API, MCP）
│   └── tool/          # 工具注册
├── runtime/           # Mutable Runtime
│   ├── mutable/       # MutableDAG
│   ├── planner/       # Planner
│   ├── scheduler/     # 调度器
│   └── recovery/      # 恢复策略
├── evidence/          # Evidence Runtime
│   ├── collector/     # 证据收集
│   ├── store/         # 证据存储
│   └── aggregator/    # 证据聚合
├── knowledge/         # AKF（Knowledge Runtime）
├── genome/            # 可进化组件协议
├── evolution/         # GA / Mutation / Selection
├── coordinator/       # Evolution Coordinator
├── patch/             # Runtime Patch
├── chaos/             # Chaos Arena
├── scheduler/         # 调度器
├── workflow/          # Mutable DAG
├── compiler/          # Context Compiler
└── api/               # 对外 API
```

---

## 七、优先级建议

### 第一梯队（Phase 1-2，当前最值得投入）

```
Phase 1: Mutable Runtime 统一  →  已有 70% 代码，收尾即可
Phase 2: Evidence Runtime      →  internal/evidence/ 已有，接子系统
```

### 第二梯队（Phase 3-4，核心竞争力）

```
Phase 3: Runtime Genome 接口化  →  GA 已经能用，接口化后更好扩展
Phase 4: Evolution Coordinator  →  新模块，从简单策略开始
```

### 第三梯队（Phase 5-6，论文级创新）

```
Phase 5: Runtime Patch 统一     →  已有基础，加强 CanApply + Rollback
Phase 6: Graph Evolution        →  真正的差异化
```

### 第四梯队（Phase 7-8，锦上添花）

```
Phase 7: Self-Healing           →  依赖 Phase 4+5 完成
Phase 8: Execution River        →  v2 再考虑
```

---

## 八、当前代码库健康度

| 指标 | 数值 |
|------|------|
| `make check` | ✅ 零 issues |
| bugs 修复率 | 20/22 ✅（91%） |
| 超 1000 行文件 | 0 |
| 适配器实现 | 8/8 官方组件 ✅ |
| 未修复 bug | M4 SSE scanner 缓冲丢失、M8 EvaluatorRegistry nil 判断 |

---

*ARES is an Evidence-Driven Autonomous Runtime where every optimization, recovery, adaptation, and evolution is represented as a Runtime Patch.*