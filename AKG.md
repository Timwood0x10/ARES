# ARES Knowledge Fabric (AKF) — 设计规格 v1

> 单一事实来源（SSOT）。代码实现位于 `internal/knowledge`。
> 配套：`plan/akf_remediation_plan.md`（21 bug 修缮）、`plan/akg_improvement_plan.md`（本重构计划）。
> 阅读顺序：§1 为什么 → §2 模型 → §3 架构 → §4 决策(ADR) → §5 质量/安全/验收 → §6 边界与路线图。完整 Go 签名见末尾 **附录 A**。

---

## 1. 为什么是 Knowledge Fabric

AKF 不是传统知识图谱，而是**把任意数据源实时编织（Fabric）成 AI 可消费的认知图（Cognitive Graph）**。

```
Data ──▶ Knowledge Object ──▶ Intent Planner ──▶ Dynamic Cognitive Graph ──▶ Context Compiler ──▶ LLM
```

图不是数据库，而是 Agent 针对**当前任务**即时生成的认知结构。换一个任务，可用同一批 `KnowledgeObject` 生成完全不同的图。这是 AKF 相对传统 KG（Data→Graph→Query）的本质区别，也是最值得投入的创新点。

**三条基石原则**

1. **Source of Truth** — 外部数据源（MySQL/PG/Git/Memory/Code…）是事实来源，ARES 只存 `KnowledgeObject`、索引、蒸馏结果与证据引用，不复制业务数据。
2. **Graph is Ephemeral** — 图是运行时对象，按任务构建/裁剪/销毁，永不持久化。
3. **Knowledge is Pluggable** — 任何数据源实现 `GraphProvider` 即可纳入体系。

**与 Memory Distillation 的关系（平级）**：Memory Distillation 把经验沉淀为 `KnowledgeObject`；AKF 把它们按任务编织成图；Context Compiler 把图压成模型上下文。三者构成 AI 知识流水线。

---

## 2. 领域模型

所有知识统一为 `KnowledgeObject`，以**三层结构**承载不同消费阶段：

| 层 | 字段 | 用途 |
|----|------|------|
| Raw | `Raw []byte` | 原始字节，保留以便重新蒸馏 |
| Normalized | `Normalized string` | 清洗后文本，供 embedding / matching |
| Summary | `Summary string` | LLM 友好的压缩摘要，供 token 高效检索 |

Embedding 不外置在对象内，而是经 `Representation{Model, Dimension, Vector}` 独立存储，支持多模型（OpenAI/BGE/Jina）无迁移成本。

其余关键类型：

- **`ObjectType`**（12 种）：`memory / user / project / code / issue / commit / decision / document / tool_result / workflow / runtime / architecture`，按业务语义而非存储区分。
- **`Evidence`**：`Source / Ref / Weight / Timestamp`，每条知识可追溯回来源（Commit/Issue/Conversation/Runtime/Review/Benchmark）。
- **`Relation`**：`From / To / Name / Properties / Score / Evidence`。`Name` 为字符串（内置 12 个常量 + 用户可注册自定义），`Properties` 承载关系元数据。
- **`WorkingGraph`**：`Nodes map + Edges []Relation`，生命周期 Build→Consume→Destroy，永不持久化。

> 设计取舍：三层结构使存储略增，但换来可重新蒸馏/重新 embedding；Relation 用字符串而非 const 枚举，换来可注册自定义关系（代价是 Linker 须负责填充 `Properties`）。完整字段见附录 A.1。

---

## 3. 架构：五层稳定流水线

```
Task
  │
  ▼
① Provider        (provider/)        数据接入，流式吐出 KnowledgeObject
  │
  ▼
② Knowledge Pipeline (knowledge/)    标准化 + 解析(Resolver) + 蒸馏
  │
  ▼
③ Knowledge Planner (planner/)       规划"需要什么知识"（与 Provider 解耦）
  │
  ▼
④ Graph Runtime   (runtime/)         Plan→Load→Link→Reduce→WorkingGraph（动态、不持久化）
  │
  ▼
⑤ Context Compiler (compiler/)       编译为 Prompt/Markdown/JSON/XML/ToolSchema
  │
  ▼
LLM
```
`KnowledgeStore` 为**可选旁路**（缓存/持久化/历史），不在主链路上。

| 层 | 职责 | 关键类型 | 说明 |
|----|------|----------|------|
| ① Provider | 接入数据源 | `GraphProvider`（流式 `Stream`） | 实现 `Name / IntentMatch / Stream` 即可接入；当前有 MySQL/PG/SQLite/Code/Memory/Evolution |
| ② Pipeline | 标准化 + 解析 | `KnowledgePipeline`（`Normalizer→EntityMatcher→Validator→Summarizer`） | Resolver 负责别名匹配、去重、冲突检测 |
| ③ Planner | 规划需求 | `KnowledgePlanner` / `SourceDiscovery` / `QueryPlanner` | Planner 只产出"需要什么"；Discovery 选源；QueryPlanner 翻译为 SQL/Cypher/Vector/Memory/Keyword |
| ④ Runtime | 建图 | `KnowledgeRuntime.Execute` | 编排 Plan→Load→Link→Reduce；`errgroup` 管理并发；`Linker` 生边、`Reducer` 按预算裁剪 |
| ⑤ Compiler | 编译输出 | `Compiler.Compile` | 统一多格式输出，供 LLM / Tool Calling / MCP / API |

---

## 4. 决策记录（ADR）

每条决策记录**选择 + 放弃的代价**。所有决策均已在 `internal/knowledge` 落地（状态：已落地），唯一未闭环的是 ADR-010。

| # | 议题 | 决策 | 代价 |
|---|------|------|------|
| 001 | Store 是否必经 | 否——可选旁路，Provider 可直接喂 Runtime | 无统一查询层，跨源聚合需自实现 |
| 002 | Provider 批量 vs 流式 | 流式 channel（`Stream`），强制分页上限 | Provider 须感知 ctx 取消，实现更复杂 |
| 003 | Intent 结构 | `Goal / Scope / Constraints / Budget` | 结构体膨胀（必要） |
| 004 | KO 三层模型 | `Raw/Normalized/Summary` + 外部 `Representation` | 存储略增 |
| 005 | Relation 类型 | **struct**：`Name`(字符串) + `Properties` | Linker 须填充 `Properties` |
| 006 | Builder 拆分 | Planner/Loader/Linker/Reducer + runtime facade | 接口数增多 |
| 007 | Resolver | 流水线 `Normalizer→Matcher→Validator→Summarizer` | matcher 阈值需调参（默认 0.6） |
| 008 | Compiler 输出 | prompt/markdown/json/xml/tool_schema | 多格式模板维护 |
| 009 | Planner 解耦 | Planner 只产出需求，选源/翻译下放 | 链路更长 |
| 010 | Distillation vs Resolver | Resolver=单对象别名去重；Distillation=跨源 Merge/Conflict/Evolution 产出新 KO | 蒸馏闭环未闭环（推迟 v1.1） |

---

## 5. 质量属性 / 安全 / 验收

**质量属性**
- **Token 预算**：`TokenBudget{MaxTokens, Reserved, ForGraph}`；`ForGraph` 决定 Reducer 节点上限（≈ `ForGraph/50`），`Reserved` 留给 LLM 推理。示例 `{MaxTokens:2000, ForGraph:1000, Reserved:1000}`。
- **可扩展性**：流式 + 强制分页（`LIMIT 10000`）→ 10M 行不 OOM；并发度由 `Config.MaxConcurrentProviders`（默认 5）控制。
- **可靠性**：`errgroup` 管并发；非致命阶段失败仅 warn+跳过该对象，单源失败→图降级而非整体失败；错误以 `%w` 包装上抛。
- **可观测性**：各阶段打日志；`CompileMetrics{InputNodes, InputEdges, OutputTokens, CompressionRatio}` 供压缩比监控。

**安全**
- SQL 注入：标识符经 `validateIdentifier`+`quoteIdentifier` 处理，参数走占位符（修复 B3）。
- 源鉴权：凭据经 `ProviderConfig` 注入，不硬编码；跨命名空间受 `Scope.Namespaces` 约束。
- PII：Embedding 外置不随对象扩散；`Raw` 含原始数据，存储层应按合规加密/脱敏。

**错误模型**
- `ErrObjectNotFound` 为 Store 缺失语义标准错误（`Get` 未找到必须返回它，而非 `nil,nil`，修复 B7）。
- `Execute` 在 Plan/Discover 无产出时返回明确错误，避免空图静默成功。

**验收**
- 每 Provider/Planner/Linker/Reducer/Compiler 单测；并发路径 CI 必须 `go test -race`（B22 教训）。
- 覆盖现状：mysql 41.6%、sqlite 74.8%、postgres 20.2%、compiler 92.4%；缺口：`provider/memory`、`provider/postgres` 仍无单测。

---

## 6. 边界与路线图（v1.1+）

**v1 范围**：本文 §1–§5 全部覆盖（领域模型、五层架构、接口契约、流式 Provider、Resolver、Planner 三件套、Linker/Reducer、多格式 Compiler、可选 Store、MCP/HTTP API 表面）。

**有意推迟到 v1.1+**
1. **Lazy Graph**：`Config.LazyLoading` 已预留但未实现（TODO 2026-08），需返回 `LazyGraph` 而非即时图。
2. **语义 Search**：`KnowledgeStore.Search` 当前仅关键词（B12 TODO），待接 `Representation` 向量检索。
3. **蒸馏闭环**：跨源 Distillation 的 Merge/Conflict/Evolution 未闭环，复用 `plan/distill_imporve.md` 既有 distiller，AKF 仅做 `KnowledgeObject ↔ experience` 桥接。
4. **行为派生边**：`TimelineLinker` 仅同类型生成 `supersedes`（B19）；应与 `plan/event-sourcing-plan.md` 对齐埋点，避免两套事件模型。
5. **更多 Provider/Store**：Git/GitHub/Mongo/Redis/Qdrant/Milvus/Neo4j。
6. **Relation 富化**：推动 Linker 普遍填充 `Relation.Properties`。

**对外 API（表面）**
- MCP：`build_graph` / `compile_context` / `query_knowledge` / `distill_memory`
- HTTP：`POST /kg/build` `/kg/context` `/kg/query` `/kg/distill`

---

## 附录 A — 接口参考（完整签名）

### A.1 领域类型

```go
type KnowledgeObject struct {
    ID, Namespace       string
    Type                ObjectType
    Raw                 []byte
    Normalized, Summary string
    Metadata            map[string]any
    Tags                []string
    Confidence          float64
    Version             int64
    CreatedAt, UpdatedAt time.Time
    Evidence            []Evidence
    Representations     map[string]string // model → representationID
}

type Evidence struct { Source string; Ref string; Weight float64; Timestamp time.Time }

type Relation struct {
    From, To, Name, Evidence string
    Properties                map[string]any
    Score                     float64
}

type WorkingGraph struct {
    Nodes map[string]*KnowledgeObject
    Edges []Relation
}

// ObjectType 常量：memory/user/project/code/issue/commit/decision/document/
//   tool_result/workflow/runtime/architecture
// Relation 内置名：depends_on/calls/causes/fixes/belongs_to/uses/implements/
//   similar_to/generated_by/decided_by/supersedes/learns_from
```

### A.2 Provider 层

```go
type GraphProvider interface {
    Name() string
    IntentMatch(intent knowledge.Intent) float64
    Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error)
}

type ProviderConfig struct {
    Name       string
    Namespace  string
    IntentTags []string
    Mapping    ColumnMapping
    Table      string
}
```

### A.3 Pipeline 层（Resolver）

```go
type Normalizer     interface { Name() string; Normalize(ctx, *KnowledgeObject) (*KnowledgeObject, error) }
type EntityMatcher  interface { Name() string; Match(ctx, *KnowledgeObject, candidates []*KnowledgeObject) (*ResolveResult, error) }
type Validator      interface { Name() string; Validate(ctx, merged *KnowledgeObject, sources []*KnowledgeObject) (*ValidationResult, error) }
type Summarizer     interface { Name() string; Summarize(ctx, *KnowledgeObject) (*KnowledgeObject, error) }

func (p *KnowledgePipeline) Process(ctx, obj *KnowledgeObject) (*KnowledgeObject, error)
func (p *KnowledgePipeline) ProcessStream(ctx, in <-chan *KnowledgeObject) <-chan *KnowledgeObject

// ResolveResult{MatchedObjectID, Confidence, IsNew}
// ValidationResult{Confidence, Conflicts []Conflict}
// Conflict{Field, ValueA, ValueB, Strategy}  // take_newer / take_higher_confidence / manual
```

### A.4 Planner 层

```go
type KnowledgePlanner interface { Plan(ctx, goal string, budget knowledge.TokenBudget) (*KnowledgePlan, error) }
type SourceDiscovery  interface { Discover(ctx, reqs []KnowledgeRequirement, budget knowledge.TokenBudget) ([]PlannedSource, error) }
type QueryPlanner     interface { PlanQuery(ctx, req KnowledgeRequirement, providerName, providerType string) (*QueryPlan, error) }

type KnowledgeRequirement struct { Need NeedType; Description string; Priority, MaxResults int }
type KnowledgePlan        struct { Requirements []KnowledgeRequirement; TokenBudget knowledge.TokenBudget }
type PlannedSource        struct { ProviderName string; Requirement KnowledgeRequirement; Query *QueryPlan; Priority, MaxResults int }
type QueryPlan            struct { Query string; QueryType QueryType; Parameters map[string]any; MaxResults int }
// QueryType: sql | cypher | vector | memory | keyword
// NeedType:  architecture | decision | history | code | issue | performance
```

### A.5 Graph Runtime 层

```go
type Linker interface { Name() string; Link(ctx, objects []*KnowledgeObject) ([]knowledge.Relation, error) }
type Reducer interface { Name() string; Reduce(ctx, graph *knowledge.WorkingGraph, budget knowledge.TokenBudget) (*knowledge.WorkingGraph, error) }

type KnowledgeRuntime struct { /* planner, discovery, registry, pipeline, linkers, reducers */ }
func New(p planner.KnowledgePlanner, d planner.SourceDiscovery, reg *provider.ProviderRegistry,
         pipe *knowledge.KnowledgePipeline, linkers []Linker, reducers []Reducer) *KnowledgeRuntime
func (r *KnowledgeRuntime) Execute(ctx, goal string, budget knowledge.TokenBudget, cfg *Config) (*knowledge.WorkingGraph, error)

type Config struct { MaxConcurrentProviders int; LazyLoading bool } // 默认并发 5；LazyLoading 未实现
```

### A.6 Compiler 层

```go
type Format string // prompt | markdown | json | xml | tool_schema
type CompileConfig struct { Formats []Format; MaxTokens, MaxNodes, MaxEdges int; IncludeRaw bool }
type CompiledContext struct { Intent string; Formats map[Format]string; Metrics CompileMetrics }
type Compiler interface { Compile(ctx, graph *knowledge.WorkingGraph, cfg CompileConfig) (*CompiledContext, error) }
```

### A.7 KnowledgeStore（可选）

```go
type KnowledgeStore interface {
    Save(ctx, objects ...*KnowledgeObject) error
    Get(ctx, id string) (*KnowledgeObject, error)             // 未找到返回 ErrObjectNotFound
    Query(ctx, q Query) ([]*KnowledgeObject, error)
    Delete(ctx, id string) error
    Search(ctx, text, model string, limit int) ([]*KnowledgeObject, error) // 当前仅关键词（B12）
    SaveRepresentation(ctx, rep *Representation) error
    GetRepresentation(ctx, objectID, model string) (*Representation, error)
}
// 实现：memory / sqlite / postgres。MySQL 为路线图。
```
