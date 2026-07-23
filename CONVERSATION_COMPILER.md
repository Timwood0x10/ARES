# Unified Knowledge Compiler — ARES 认知内核

> **核心思想：** 所有原始输入经过同一条管线：Raw Input → Compiler → Knowledge Model → Selector → Consumer。
>
> AKG 负责 Compiler 的 Extract 阶段（零 Token 成本），Distiller 负责 KM 的裁剪（蒸馏即裁剪）。
>
> 与 CodeScope 对偶：CodeScope 编译源码，ARES 编译对话。

---

## 一、统一架构

```
Raw Input
    │
    ▼
Compiler (Extract → Normalize → Compile)
    │
    ▼
Knowledge Model (KM) — Graph (Node + Edge)
    │
    ▼
Selector
    │
    ▼
Consumer
```

### CodeScope 管线

```
Code → Parser → Resolver → Modeler → Code Semantic Model → Inspector/Verifier/Analyzer
```

### ARES Conversation 管线

```
Conversation → Compiler → KM → Selector → Consumer
```

### ARES Runtime 管线

```
Runtime Events → Compiler → Runtime Model → Selector → Evolution/Recovery/Scheduling
```

**三个系统，同一条数据流。**

---

## 二、Compiler 三层

### 为什么 AKG 在 Extract 层？

AKG 已有的能力：

| AKG 能力 | Compiler 阶段 | 是否需要 LLM |
|:---------|:-------------|:------------|
| EntityExtractor | Extract | ❌ 规则 + NER |
| RelationBuilder | Compile | ❌ 依赖解析 |
| FactExtractor | Extract | ❌ 规则 |

**AKG 完全是规则 + NER，不需要 LLM 调用。** 所以 Compiler 的 Extract + Normalize + Compile 三个阶段都可以做到零额外 LLM Token 消耗。

```
Conversation
      │
      ▼
┌─────────────────────────────────────┐
│          1. Extract                  │
│  ┌────────────────────────────────┐  │
│  │ AKG EntityExtractor (零 LLM)   │  │
│  │ AKG FactExtractor  (零 LLM)   │  │
│  │ 原始事件提取                   │  │
│  └────────────────────────────────┘  │
      │
      ▼
┌─────────────────────────────────────┐
│          2. Normalize                │
│  ┌────────────────────────────────┐  │
│  │ 统一：Rust / rust / Rust语言   │  │
│  │ 别名解析 / 指代消解            │  │
│  └────────────────────────────────┘  │
      │
      ▼
┌─────────────────────────────────────┐
│          3. Compile                  │
│  ┌────────────────────────────────┐  │
│  │ AKG RelationBuilder (零 LLM)  │  │
│  │ 建立 Graph                     │  │
│  │ 去重 / 合并 / 链接 / 解析     │  │
│  │ 输出：Knowledge Model (Graph)  │  │
│  └────────────────────────────────┘  │
      │
      ▼
    Knowledge Model
```

**Compiler 不做推理。** 推理（"为什么采用 Patch？"）是 Inference Engine 的事。

---

## 三、Knowledge Model（KM）— Graph 结构

### 3.1 核心数据结构

```go
// Node — 知识模型中的节点
type Node struct {
    ID         string            `json:"id"`
    Type       NodeType           `json:"type"` // entity | fact | decision | constraint | ...
    Attributes map[string]any     `json:"attributes"`
    Confidence float64            `json:"confidence"`
    AccessCount int64             `json:"access_count"`
    CreatedAt  time.Time          `json:"created_at"`
    Version    int                `json:"version"`
}

// Edge — 节点间的关系
type Edge struct {
    ID        string    `json:"id"`
    Type      EdgeType  `json:"type"` // depends_on | supports | contradicts | implements | ...
    Source    string    `json:"source"`
    Target    string    `json:"target"`
    Weight    float64   `json:"weight"`
}

// KnowledgeModel — 整个知识模型
type KnowledgeModel struct {
    Nodes    map[string]*Node `json:"nodes"`
    Edges    []Edge           `json:"edges"`
    Metadata ModelMeta        `json:"metadata"`
}
```

### 3.2 Node 类型

```
entity      — 实体（Rust, ARES, RuntimePatch）
fact        — 事实（三元组：ARES → uses → Patch）
decision    — 决策（采用 Patch，拒绝热更新）
goal        — 目标（实现 Context Lifecycle）
constraint  — 约束（SaaS 成本必须可控）
tradeoff    — 权衡（成本↓ 准确率↓）
question    — 未解决的问题
task        — 待办项
evidence    — 证据
reference   — 外部引用
memory      — 蒸馏后的记忆节点（由 Distiller 产出）
```

### 3.3 示例

```
实体: ARES
  └── type: entity
  └── attributes: { name: "ARES", type: "system" }

决策: 采用 Patch 机制
  └── type: decision
  └── attributes: { choice: "Patch", rejection: "热更新", reason: "实现复杂度 > 收益" }

边: 采用 Patch 机制
  └── supported_by → 热更新需要停机
  └── creates → 实现 Runtime Patch Module
```

---

## 四、蒸馏即裁剪（Distillation is Pruning）

### 4.1 问题

KM 会无限增长。每次 Compile 新增节点，如果不裁剪，Selector 的遍历成本、Prompt Builder 的渲染成本都会线性增长。

### 4.2 方案

**蒸馏不是 Compile 之后的事，蒸馏就是裁剪。**

```
KM accumulates
    │
    ▼
Selector picks subgraph（当前活跃的子图）
    │
    ▼
Distiller compresses subgraph → Memory Node
    │                          │
    ▼                          ▼
原节点删除（裁剪）            Memory Node 保留在 KM 中
```

### 4.3 裁剪策略

**策略 1：按 Confidence + AccessCount 排序**

```
定期（每 10 次 Compile）：
  1. 对每个 Node 计算 Score = Confidence × AccessCount
  2. 按 Score 排序
  3. 保留 Top K（例如 500 个）
  4. 其余：
     ├── Score > 阈值 → 蒸馏为 Memory Node 后保留
     └── Score < 阈值 → 直接删除
```

**策略 2：蒸馏即裁剪**

```
KM 中的一段子图
  │
  ▼
Distiller（现有的 Distillation Pipeline，不变）
  │
  ▼
Memory Node（一个节点代表一段蒸馏后的知识）
  │
  ▼
原节点删除，Memory Node 保留在 KM 中
```

**策略 3：按时间窗口裁剪**

```
保留最近 N 轮的全部节点
超过 N 轮的节点：
  ├── Confidence > 0.8 → 保留，标记为 archive
  └── Confidence < 0.8 → 删除
```

### 4.4 数据流

```
                    Conversation
                          │
                          ▼
                    Compiler
                          │
                          ▼
                  Knowledge Model (KM)
                          │
          ┌───────────────┴───────────────┐
          │                               │
          ▼                               ▼
    Prompt Consumer                Distiller
          │                               │
          ▼                               ▼
    LLM Prompt                    Memory Node → 写回 KM
                                           │
                                           ▼
                                    原节点被替换（裁剪）
```

**图的规模通过蒸馏自动控制，不会无限增长。**

---

## 五、Pipeline 分层

### 5.1 完整 Pipeline

```
Conversation
    │
    ▼
┌──────────────┐
│   Compiler   │  ← Extract (AKG) → Normalize → Compile (AKG)
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Knowledge   │  ← Graph (Node + Edge)
│    Model     │
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Selector    │  ← 选哪些子图进入下游
└──────┬───────┘
       │
       ▼
┌──────────────┐
│  Consumer    │  ← PromptBuilder / MemoryEmitter / AKG Builder / Analytics
└──────────────┘
```

### 5.2 Selector

```
KM → Selector → SubGraph

Selector 类型：
  PromptSelector    — 选哪些节点进入 Prompt
  MemorySelector    — 选哪些节点作为 Memory Candidate
  AKGSelector       — 选哪些节点进入知识图谱
  AnalyticsSelector — 选哪些节点进入 Timeline
```

### 5.3 Consumer

**MemoryEmitter** — 只写数据库，不选、不评分：

```
SubGraph → MemoryEmitter → MemoryStore (trait)
  ↑                       ↑
 从 Selector 拿到          LanceDB / SQLite / Postgres / Redis
```

**PromptBuilder** — 拆为 Renderer + Formatter：

```
SubGraph → Renderer → ContextBlock → Formatter → Prompt
  ↑                       ↑              ↑
 从 Selector 拿到         中间表示        Markdown / XML / JSON
```

**AKG Builder** — 直接消费 SubGraph：

```
SubGraph → AKG Builder → Knowledge Graph
```

---

## 六、Working Context 不存数据

```
Compiler → KM → PromptSelector → Renderer → Formatter → Prompt
                                                    ↑
                                           每次实时渲染，不存数据
```

**Working Context 根本不用存。** 每次 Prompt 时，从 KM 实时渲染。

---

## 七、触发机制

### 7.1 Token Budget 触发

```
Window: 128K
当前:   95K  (74%)
超过 70% → 触发 Compile
```

### 7.2 增量编译

```
Round 1: Conversation 100轮 → Compile → KM v1 + 保留最近 10 轮
Round 2: KM v1 + 新 50 轮 → Compile → KM v2 + 保留最近 10 轮
```

### 7.3 Overlay（不替换）

```
System
KM Context (从 KM 实时渲染)
Recent Conversation (保留最近 N 轮)
Retrieved Memory
```

---

## 八、Benchmark

### 8.1 核心指标：Token Utility

```
Token Utility = 有用信息量 / Token 数
```

### 8.2 完整指标

| 指标 | 定义 | 目标 |
|:-----|:-----|:----:|
| Token Utility | 有用 Node 数 / Token 数 | > 0.01 |
| Fidelity Score | KM 渲染后 QA 正确率 / 全文 QA 正确率 | > 0.9 |
| Decision Recall | 决策节点保留率 | > 95% |
| Constraint Recall | 约束节点保留率 | > 90% |
| Compile Latency | 单次 Compile 耗时（不含 LLM） | < 100ms |
| Distillation Pruning Ratio | 每次裁剪比例 | > 30% |

---

## 九、实现路线图

### Phase 1：KM 定义 + AKG Compiler

- 定义 KM Graph 数据结构（Node + Edge）
- 集成 AKG 到 Compiler 的 Extract 阶段
- 实现 Compile：提取 → 归一 → 建图
- 验证：技术文章 → KM → Fidelity Score

### Phase 2：Selector + Consumer

- PromptSelector + Renderer + Formatter
- MemorySelector + MemoryEmitter + MemoryStore Trait
- 验证：Token Utility

### Phase 3：蒸馏即裁剪

- Distiller 消费 KM 子图
- 裁剪策略实现
- 验证：Distillation Pruning Ratio

### Phase 4：Context Lifecycle 上线

- Token Budget 触发
- 增量编译
- 替换 `BuildPromptMessages`
- 完整的 Cognitive Pipeline