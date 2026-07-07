# ARES Knowledge Fabric（AKF）落地计划 v2

> 基于 AKG.md 完整设计，针对 10 个修订点调整架构。
> 核心原则：**Knowledge First, Graph Last**

---

## 修订记录

| 版本 | 基于 | 核心变化 |
|------|------|---------|
| v1 | AKG.md 初始版 | 第一版落地计划 |
| **v2** | **AKG.md 修订版（10 条 critique）** | **架构重写：去中心 Store、Streaming、Planner、Resolver、多格式 Compiler** |
| **v3** | **improve.md（4 条 final critique）** | **Embedding 分离、Resolver 三阶段、Planner-Provider 解耦、Builder→Runtime、QueryPlanner、Lazy Graph** |

---

## 0. 修订点采纳决策

### v2（来自 AKG.md 10 条）

| # | Critique | 采纳 | 方案 |
|---|----------|------|------|
| 1 | KnowledgeStore 不应该成为中心 | ✅ | Store 改为可选管道，Provider→Pipeline 直通 Builder |
| 2 | Provider 不应返回 `[]KnowledgeObject` | ✅ | 改为 `Stream()` 迭代器模式 |
| 3 | Intent 不够 | ✅ | `Intent{Goal, Scope, Constraints, Budget}` |
| 4 | Builder 不应只有一个 | ✅ | 拆为 Planner → Loader → Linker → Reducer → Graph |
| 5 | Relation 不应固定 | ✅ | `Relation{Name, Properties}` 可扩展 |
| 6 | KnowledgeObject 少一层 | ✅ | `Raw → Normalized → Summary` 三层 |
| 7 | Memory Distillation 可以更漂亮 | ✅ | 统一走 Knowledge Pipeline |
| 8 | 缺 Knowledge Resolver | ✅ | 新增 Resolver 模块 |
| 9 | Compiler 不应只是 ToPrompt() | ✅ | 多格式输出 |
| 10 | 缺 Knowledge Planner | ✅ | 新增 Planner 模块 |

### v3（来自 improve.md 4 条 final）

| # | Critique | 采纳 | 方案 |
|---|----------|------|------|
| 11 | **KnowledgeObject 不应带 Embedding** | ✅ | Embedding → 独立 `Representation` 结构，支持多模型共存 |
| 12 | **Resolver 不只是 Alias** | ✅ | 拆为三个阶段：Normalize → Resolve → Validate |
| 13 | **Planner 不应知道 Provider** | ✅ | Planner → KnowledgeRequirement → SourceDiscovery → Provider，三层解耦 |
| 14 | **Builder 应改名** | ✅ | 改为 **Knowledge Runtime**，Builder 只是其内部一个步骤 |

### 新增能力

| 能力 | 来源 | 说明 |
|------|------|------|
| **Query Planner** | improve.md 新增建议 | 在 Planner 与 Provider 之间，负责将 KnowledgeRequirement 翻译为具体查询（SQL/Cypher/Vector/Memory） |
| **Lazy Graph** | improve.md 新增建议 | WorkingGraph 支持懒加载节点——Compiler 需要时才 Expand()，进一步省 Token |

---

## 1. 最终架构（v3 定版）

```
Task
  │
  ▼
Knowledge Planner
  │  输出：KnowledgeRequirement（NeedType，与 Provider 无关）
  ▼
Source Discovery
  │  新增：将 Need 映射到具体 Provider
  ▼
Query Planner
  │  新增：将 Need 翻译为 SQL/Cypher/Vector/Memory 查询
  ▼
Provider（Stream 模式）
  │  输出：KnowledgeObject 流
  ▼
Knowledge Pipeline
  ├── Normalize（Raw → Normalized）
  ├── Resolve（v3：三阶段 Normalize → Resolve → Validate）
  ├── Summarize（Normalized → Summary）
  └── Representation（Embedding 独立存储，支持多模型）
  │
  ▼
Knowledge Runtime（原 Builder）
  ├── Loader（并发加载）
  ├── Linker（Relation 生成，可插拔 Plugin）
  ├── Reducer（Prune + Compress + Rank）
  └── Lazy Graph（v3 新增：懒加载，按需 Expand）
  │
  ▼
Context Compiler
  │  输出：Markdown / JSON / Prompt / XML / ToolSchema
  ▼
LLM
  │
  ▼
Memory Distillation（v3：与 AKF 同层，不是子模块）
  │
  ▼
Knowledge Store（可选：Cache / Persistence / History）
```

---

## 2. 核心数据模型（v2 修订）

### 2.1 KnowledgeObject（三层结构 + 独立 Representation）

```go
type KnowledgeObject struct {
    ID          string            `json:"id"`
    Type        ObjectType        `json:"type"`
    Namespace   string            `json:"namespace"`

    // 三层数据
    Raw         []byte            `json:"raw,omitempty"`         // 原始数据，用于重新蒸馏
    Normalized  string            `json:"normalized,omitempty"`  // 标准化文本，用于 Embedding
    Summary     string            `json:"summary"`               // LLM 压缩摘要，省 token

    Metadata    map[string]any    `json:"metadata,omitempty"`
    Tags        []string          `json:"tags,omitempty"`
    Confidence  float64           `json:"confidence"`
    Version     int64             `json:"version"`
    CreatedAt   time.Time         `json:"created_at"`
    UpdatedAt   time.Time         `json:"updated_at"`
    Evidence    []Evidence        `json:"evidence,omitempty"`

    // v3: Embedding 不再内嵌，通过 Representation 关联
    // representations map[string]string → model_name → representation_id
    Representations map[string]string `json:"representations,omitempty"`
}

// Representation 独立存储，一个对象可对应多个 Embedding 模型。
type Representation struct {
    ID        string            `json:"id"`
    ObjectID  string            `json:"object_id"`
    Model     string            `json:"model"`     // "openai-text-3-large", "bge-m3", "jina-v3"
    Dimension int               `json:"dimension"`
    Vector    []float32         `json:"vector"`
    Metadata  map[string]string `json:"metadata,omitempty"` // model version, hash, etc.
    CreatedAt time.Time         `json:"created_at"`
}
```

**为什么 Embedding 要分离**：

| 场景 | 内嵌 Embedding | 独立 Representation |
|------|---------------|-------------------|
| 换模型 | 迁移全表 | 新增一条记录即可 |
| 多模型共存 | 不可能 | OpenAI + BGE + BM25 并存 |
| 查询 | 固定维度 | 按模型选择匹配维度 |

**三层设计的意义**：

| 层 | 内容 | 用途 | 持久化 |
|----|------|------|--------|
| `Raw` | 原始字节 | 重新蒸馏 / 重新 Embedding | ✅ |
| `Normalized` | 标准化文本 | Embedding / 语义匹配 | ✅ |
| `Summary` | LLM 摘要 | 检索时省 token | ✅ |

### 2.2 Relation（可扩展）

```go
type Relation struct {
    From       string         `json:"from"`       // KnowledgeObject ID
    To         string         `json:"to"`         // KnowledgeObject ID
    Name       string         `json:"name"`       // 关系名称，如 "depends_on"
    Properties map[string]any `json:"properties,omitempty"` // 关系属性
    Score      float64        `json:"score"`      // 关系强度 [0, 1]
    Evidence   string         `json:"evidence,omitempty"`
}

// 内置关系常量（用户可通过字符串自定义扩展）
const (
    RelDependsOn  = "depends_on"
    RelCalls      = "calls"
    RelCauses     = "causes"
    RelFixes      = "fixes"
    RelBelongsTo  = "belongs_to"
    RelUses       = "uses"
    RelImplements = "implements"
    RelSimilarTo  = "similar_to"
    RelGeneratedBy = "generated_by"
    RelDecidedBy  = "decided_by"
    RelSupersedes = "supersedes"
    RelLearnsFrom = "learns_from"
)
```

### 2.3 Intent（可计算）

```go
type Intent struct {
    Goal        string            `json:"goal"`                 // 自然语言目标，"为什么选择Redis?"
    Scope       Scope             `json:"scope"`                // 范围约束
    Constraints []Constraint      `json:"constraints,omitempty"` // 约束条件
    Budget      TokenBudget       `json:"budget"`               // Token 预算
}

type Scope struct {
    Namespaces  []string `json:"namespaces,omitempty"`
    Types       []ObjectType `json:"types,omitempty"`
    TimeRange   *TimeRange `json:"time_range,omitempty"`
    MaxObjects  int      `json:"max_objects"`    // 最大对象数
}

type Constraint struct {
    Key   string `json:"key"`
    Op    string `json:"op"`    // eq / neq / gt / lt / in / contains
    Value any    `json:"value"`
}

type TokenBudget struct {
    MaxTokens  int `json:"max_tokens"`   // 最大 token 数
    Reserved   int `json:"reserved"`     // 保留给 LLM 推理的 token
    ForGraph   int `json:"for_graph"`    // 分配给图上下文的 token
}
```

---

## 3. Phase 1：KnowledgeObject + KnowledgePipeline（数据层）

### 3.1 目录结构

```
internal/
  knowledge/
    object.go             # KnowledgeObject, ObjectType, Evidence
    relation.go           # Relation（可扩展）
    intent.go             # Intent, Scope, TokenBudget
    store.go              # KnowledgeStore 接口（可选）
    pipeline.go           # KnowledgePipeline 编排

    pipeline/
      normalizer.go       # Raw → Normalized
      summarizer.go       # Normalized → Summary
      resolver.go         # 实体解析：Alias / Merge / Conflict（v2 新增）

    provider/
      interface.go        # GraphProvider 接口（Stream 模式）
      registry.go         # ProviderRegistry
      postgres/
        provider.go       # 外部 PostgreSQL Provider
      mysql/
        provider.go       # 外部 MySQL Provider
      memory/
        provider.go       # Memory Distillation Provider
      code/
        provider.go       # 代码分析 Provider（参考 codescope）

    store/
      interface.go        # KnowledgeStore 接口（可选）
      postgres/
        store.go          # PostgreSQL 实现
      sqlite/
        store.go          # SQLite 实现
      memory/
        store.go          # 内存实现（测试用）

  ares_memory/
    distillation/         # 现有代码 + AKF 适配
```

### 3.2 GraphProvider（Stream 模式）

```go
// GraphProvider 将任意外部数据源接入知识管道。
// 使用 Stream 模式避免全量加载 OOM。
type GraphProvider interface {
    Name() string
    IntentMatch(intent Intent) float64                          // [0, 1]

    // Stream 返回 KnowledgeObject 流，Builder 可以边读边建图。
    // ctx 取消时停止生产，调用方负责消费。
    Stream(ctx context.Context, intent Intent) (<-chan *KnowledgeObject, error)
}
```

### 3.3 KnowledgeStore（可选，不再是中心）

```go
// KnowledgeStore 是可选的持久化层，用于 Cache / Persistence / History。
// Provider → Pipeline → Graph 可以完全不经过 Store。
type KnowledgeStore interface {
    Save(ctx context.Context, objects ...*KnowledgeObject) error
    Get(ctx context.Context, id string) (*KnowledgeObject, error)
    Query(ctx context.Context, q Query) ([]*KnowledgeObject, error)
    Delete(ctx context.Context, id string) error
    Search(ctx context.Context, text string, limit int) ([]*KnowledgeObject, error)
}
```

### 3.4 Knowledge Pipeline

```
Provider Stream → Normalizer → Resolver → Summarizer → (Store?) → Graph Runtime
```

```go
// KnowledgePipeline 编排从 Raw 到 Summary 的处理管道。
type KnowledgePipeline struct {
    normalizer Normalizer
    resolver   *KnowledgeResolver
    summarizer Summarizer
    store     KnowledgeStore // 可选
}

func (p *KnowledgePipeline) Process(ctx context.Context, src <-chan *KnowledgeObject) <-chan *KnowledgeObject {
    // Normalizer: Raw → Normalized
    normalized := p.normalizer.Stream(ctx, src)
    // Resolver: 实体别名合并、冲突检测
    resolved := p.resolver.Stream(ctx, normalized)
    // Summarizer: Normalized → Summary
    summarized := p.summarizer.Stream(ctx, resolved)
    // 可选：写入 Store（异步）
    if p.store != nil {
        go func() {
            for obj := range summarized {
                _ = p.store.Save(ctx, obj)
            }
        }()
    }
    return summarized
}
```

### 3.5 KnowledgeResolver（v2 新增，v3 三阶段）

```go
// KnowledgeResolver 负责实体解析：别名识别、冲突检测、对象合并。
// v3: 拆为三个阶段 —— Normalize → Resolve → Validate。
type KnowledgeResolver struct {
    normalizers []Normalizer    // 阶段一：标准化
    matchers    []EntityMatcher // 阶段二：解析匹配
    validators  []Validator     // 阶段三：验证确认
}

// ── 阶段一：Normalize ──
// 将不同来源的同一实体名标准化为统一格式。
// "Redis"、"redis"、"Redis Cache" → "redis"

type Normalizer interface {
    Name() string
    Normalize(ctx context.Context, obj *KnowledgeObject) (*KnowledgeObject, error)
}

// ── 阶段二：Resolve ──
// 将标准化后的对象与已有实体进行匹配。
// 匹配成功则合并，否则新建。

type EntityMatcher interface {
    Name() string
    Match(ctx context.Context, obj *KnowledgeObject, candidates []*KnowledgeObject) (*ResolveResult, error)
}

type ResolveResult struct {
    MatchedObjectID string  `json:"matched_object_id,omitempty"` // 匹配到的已有实体 ID
    Confidence      float64 `json:"confidence"`                  // 匹配置信度
    IsNew           bool    `json:"is_new"`                      // 是否新建实体
}

// ── 阶段三：Validate ──
// 验证合并结果是否合理，检测冲突。
// 例如：同一字段两个不同值 → 标记 Conflict，降低 Confidence。

type Validator interface {
    Name() string
    Validate(ctx context.Context, merged *KnowledgeObject, sources []*KnowledgeObject) (*ValidationResult, error)
}

type ValidationResult struct {
    Confidence float64 `json:"confidence"`
    Conflicts  []Conflict `json:"conflicts,omitempty"`
}

type Conflict struct {
    Field    string `json:"field"`    // 冲突字段
    ValueA   any    `json:"value_a"`  // 来源 A 的值
    ValueB   any    `json:"value_b"`  // 来源 B 的值
    Strategy string `json:"strategy"` // "take_newer" / "take_higher_confidence" / "manual"
}
```

**三阶段的执行流程**：

```
Provider Stream
    │
    ▼
Normalize ── "Redis Cache" → "redis"
    │
    ▼
Resolve ──── 匹配到已有实体 "redis-standalone"
    │
    ▼
Validate ─── 检查：版本号一致？字段冲突？→ 提升 Confidence
    │
    ▼
KnowledgeObject（合并后）
```

---

## 4. Phase 2：Knowledge Planner + Graph Runtime（核心引擎）

### 4.1 Knowledge Planner（v2 新增，v3 解耦）

```
v2（耦合）：
Planner → 直接选择 Provider（memory / code / postgres）

v3（解耦）：
Planner → KnowledgeRequirement → SourceDiscovery → Provider
```

```go
// KnowledgePlanner 根据 Task 生成知识获取需求，不关心具体 Provider。
// v3: Planner 只输出 Need，由 SourceDiscovery 映射到 Provider。
type KnowledgePlanner interface {
    Plan(ctx context.Context, task Task) (*KnowledgePlan, error)
}

// KnowledgePlan 只描述"需要什么知识"，不描述"从哪里获取"。
type KnowledgePlan struct {
    Requirements []KnowledgeRequirement `json:"requirements"`   // 知识需求列表
    TokenBudget  TokenBudget            `json:"token_budget"`
}

type KnowledgeRequirement struct {
    Need        NeedType   `json:"need"`          // architecture / decision / history / code
    Description string     `json:"description"`   // 自然语言描述
    Priority    int        `json:"priority"`      // 优先级
    MaxResults  int        `json:"max_results"`   // 最大返回数
}

type NeedType string

const (
    NeedArchitecture NeedType = "architecture"  // 架构决策
    NeedDecision     NeedType = "decision"      // 技术选型决策
    NeedHistory      NeedType = "history"       // 历史记录
    NeedCode         NeedType = "code"          // 代码实现
    NeedIssue        NeedType = "issue"         // Issue 讨论
    NeedPerformance  NeedType = "performance"   // 性能数据
)

// SourceDiscovery 将 KnowledgeRequirement 映射到具体 Provider。
// v3 新增：Planner 不知道 Provider 存在，新增 Provider 不用改 Planner。
type SourceDiscovery struct {
    registry *ProviderRegistry
}

func (d *SourceDiscovery) Discover(ctx context.Context, reqs []KnowledgeRequirement) ([]PlannedSource, error) {
    // 遍历每个 Need，在 registry 中查找最优 Provider
    // 例如 NeedArchitecture → 同时匹配 CodeProvider + GitProvider
}
```

### 4.2 Query Planner（v3 新增）

```
在 Planner → Provider 之间新增一层：
KnowledgePlanner → QueryPlanner → Provider

QueryPlanner 负责将 KnowledgeRequirement 翻译为具体查询。
```

```go
// QueryPlanner 将知识需求翻译为具体查询语句。
// 避免每个 Provider 自己写查询逻辑。
type QueryPlanner interface {
    PlanQuery(ctx context.Context, req KnowledgeRequirement, provider GraphProvider) (*QueryPlan, error)
}

type QueryPlan struct {
    Query      string            `json:"query"`       // 查询语句
    QueryType  QueryType         `json:"query_type"`  // sql / cypher / vector / memory
    Parameters map[string]any    `json:"parameters"`
    MaxResults int               `json:"max_results"`
}

type QueryType string

const (
    QuerySQL     QueryType = "sql"
    QueryCypher  QueryType = "cypher"
    QueryVector  QueryType = "vector"
    QueryMemory  QueryType = "memory"
    QueryKeyword QueryType = "keyword"
)
```

示例：

```
Requirement: {Need: "architecture", Description: "Redis 的架构设计", MaxResults: 10}

QueryPlanner 根据 Provider 类型生成：
  → PG Provider:  SQL    "SELECT * FROM architecture_docs WHERE title ILIKE '%redis%'"
  → Git Provider: Log    "git log --all --oneline --grep='redis'"
  → Memory:       Vector "embedding(Redis 架构设计)"
```

**Planner 示例**：

```
Task: "为什么选择Redis?"

KnowledgePlanner.Plan() → {
  Intent: {
    Goal: "为什么选择Redis?",
    Scope: {Types: ["decision", "architecture"], MaxObjects: 200},
    Budget: {MaxTokens: 2000, ForGraph: 1000}
  },
  Sources: [
    {Provider: "memory", Priority: 1, MaxObjects: 50},   // 先查记忆中的决策记录
    {Provider: "code",   Priority: 2, MaxObjects: 100},   // 再查代码中的使用场景
    {Provider: "pg",     Priority: 3, MaxObjects: 50},    // 最后查外部 DB
  ],
  Pipeline: {Normalizer: true, Resolver: true, Summarizer: true},
  Graph: {LinkerPlugins: ["decision", "architecture"], MaxNodes: 100},
  Compile: {Formats: ["prompt", "markdown"]},
  TokenBudget: {MaxTokens: 2000, ForGraph: 1000}
}

→ 不加载 Memory、Conversation、Workflow → 真正省 Token
```

### 4.2 Knowledge Runtime（原 Builder，v3 改名）

```
KnowledgeRuntime（Facade）
  │
  ├── Loader  ── 根据 Plan + QueryPlanner 调用 Provider.Stream()，并发加载
  │
  ├── Linker  ── 生成 Relation（可插拔 Plugin）
  │
  ├── Reducer ── 裁剪图：Prune + Compress + Rank
  │
  ├── Lazy Graph ── 懒加载节点，Compiler 需要时才 Expand（v3 新增）
  │
  └── Graph   ── WorkingGraph / LazyGraph
```

```go
// v3: Builder → KnowledgeRuntime，因为它实际执行 Plan→Load→Link→Reduce 整个链路。
type KnowledgeRuntime struct {
    planner      KnowledgePlanner
    discovery    *SourceDiscovery
    queryPlanner QueryPlanner
    registry     *ProviderRegistry
    pipeline     *KnowledgePipeline
    linkers      []Linker
    reducers     []Reducer
}

func (r *KnowledgeRuntime) Execute(ctx context.Context, task Task) (*WorkingGraph, error) {
    // 1. Plan：生成知识需求（不涉及具体 Provider）
    plan, err := r.planner.Plan(ctx, task)
    // 2. Discover：需求 → Provider 映射
    sources, err := r.discovery.Discover(ctx, plan.Requirements)
    // 3. Query Plan：需求 → 具体查询语句
    queries, err := r.queryPlanner.PlanQueries(ctx, plan.Requirements, sources)
    // 4. Load & Pipeline：并发加载 + 标准化 + 解析
    objStream, err := r.load(ctx, queries)
    // 5. Link：基于 KnowledgeObject 生成 Relations
    edges, err := r.link(ctx, plan, objects)
    // 6. Reduce：裁剪到 TokenBudget 可接受范围
    graph := &WorkingGraph{Nodes: objects, Edges: edges}
    graph, err = r.reduce(ctx, graph, plan.TokenBudget)
    return graph, nil
}
```

### 4.3 Lazy Graph（v3 新增）

```go
// LazyGraph 支持懒加载：只加载根节点，当 Compiler 需要时才 Expand 子节点。
// 很多节点 LLM 永远不会看——不加载就省 Token。
type LazyGraph struct {
    Nodes     map[string]*LazyNode
    Edges     []Relation
}

type LazyNode struct {
    Object    *KnowledgeObject
    Expanded  bool
    Children  []string
    expandFn  func(ctx context.Context, id string) (*KnowledgeObject, error)
}

func (g *LazyGraph) Expand(ctx context.Context, nodeID string) error {
    node, ok := g.Nodes[nodeID]
    if !ok || node.Expanded {
        return nil
    }
    obj, err := node.expandFn(ctx, nodeID)
    if err != nil { return err }
    node.Object = obj
    node.Expanded = true
    return nil
}
```

**Lazy Graph 流程**：

```
KnowledgeRuntime.Load
    │
    ├── Redis ──────────────── 只加载根节点
    │
    Compiler 引用 Issue42
    │
    └── KnowledgeRuntime.Expand("Issue42")
            │
            ├── Provider.Load("Issue42")
            │
            └── 图节点展开 → LLM 消费

如果 Compiler 不引用 Issue42，它永远不加载 → 省 Token
```

```go
// 五层职责分离

type Loader interface {
    Load(ctx context.Context, plan *KnowledgePlan, pipeline *KnowledgePipeline) (<-chan *KnowledgeObject, error)
}

type Linker interface {
    Match(intent Intent) bool
    Link(ctx context.Context, objects []*KnowledgeObject) ([]Relation, error)
}

type Reducer interface {
    Reduce(ctx context.Context, graph *WorkingGraph, budget TokenBudget) (*WorkingGraph, error)
}

// Builder 只是 Facade
type Builder struct {
    planner   KnowledgePlanner
    registry  *ProviderRegistry
    pipeline  *KnowledgePipeline
    loaders   []Loader
    linkers   []Linker
    reducers  []Reducer
}

func (b *Builder) Build(ctx context.Context, task Task) (*WorkingGraph, error) {
    // 1. Plan
    plan, err := b.planner.Plan(ctx, task)
    // 2. Load & Pipeline
    objStream, err := b.load(ctx, plan)
    // 3. Link
    edges, err := b.link(ctx, plan, objects)
    // 4. Reduce
    graph := &WorkingGraph{Nodes: objects, Edges: edges}
    graph, err = b.reduce(ctx, graph, plan.TokenBudget)
    return graph, nil
}
```

### 4.3 Linker Plugin（可插拔关系生成器）

```go
type LinkerPlugin interface {
    Name() string
    Match(intent Intent) bool
    Link(ctx context.Context, objects []*KnowledgeObject) ([]Relation, error)
}
```

内置 Linker：

| Plugin | 生成的 Relation | 场景 |
|--------|----------------|------|
| `DecisionLinker` | `decided_by`, `causes` | 决策链路 |
| `ArchitectureLinker` | `depends_on`, `calls` | 架构依赖 |
| `TimelineLinker` | `supersedes`, `generated_by` | 时间线 |
| `SimilarityLinker` | `similar_to` | 语义相似 |

### 4.4 Reducer（省 Token 关键）

```go
// Reducer 负责将 WorkingGraph 压缩到 TokenBudget 可接受的范围。
type Reducer interface {
    Reduce(ctx context.Context, graph *WorkingGraph, budget TokenBudget) (*WorkingGraph, error)
}
```

Reducer 策略：

| 策略 | 操作 | 压缩比 |
|------|------|--------|
| `Prune` | 剪枝低分节点（Score < threshold） | 2-5x |
| `Compress` | 合并相似节点（高 Similarity 合并） | 2-3x |
| `Rank` | 按 Relation 热度 Top-N 保留 | 2-10x |

---

## 5. Phase 3：Context Compiler（多格式输出）

### 5.1 Compiler 接口

```go
// Compiler 将 WorkingGraph 编译为多种格式的 LLM 上下文。
type Compiler interface {
    Compile(ctx context.Context, graph *WorkingGraph, config CompileConfig) (*CompiledContext, error)
}

type CompiledContext struct {
    GraphID     string              `json:"graph_id"`
    Intent      Intent              `json:"intent"`
    Formats     map[string]string   `json:"formats"`      // format → content
    Metrics     CompileMetrics      `json:"metrics"`
}

type CompileMetrics struct {
    InputNodes     int   `json:"input_nodes"`
    OutputTokens   int   `json:"output_tokens"`
    CompressionRatio float64 `json:"compression_ratio"`
}
```

### 5.2 内置格式

| Format | 用途 | 示例 |
|--------|------|------|
| `prompt` | LLM Chat Completion | 结构化 Prompt 文本 |
| `markdown` | 人类可读文档 | # Knowledge Context\n... |
| `json` | 程序消费 | `{"decision": ..., "evidence": [...]}` |
| `xml` | Tool Calling Schema | `<context><decision>...</decision></context>` |
| `tool_schema` | MCP Tool Input | JSON Schema 描述 |

### 5.3 与 Memory Distillation 的关系

```
旧方案：
Memory      → KnowledgeObject → Memory DB

新方案（v2）：
Conversation → Raw Experience → Normalizer → KnowledgeObject
                                                                  ↘
                                                    Knowledge Distillation
                                                      (Merge / Conflict / Confidence)
                                                                  ↗
Git / Code / DB → Provider → Normalizer → KnowledgeObject
                                                                  ↘
                                                    KnowledgeStore（可选）
                                                                  ↗
                                                    Knowledge Planner → Graph Runtime → Compiler → LLM
```

---

## 6. Phase 4：DAG 集成 + MCP API

### 6.1 AKF 作为 DAG Workflow

```
DAG Step: "knowledge_build" (SubWorkflow)
  │
  ├── Step "plan"      ── KnowledgePlanner.Plan(task) → KnowledgePlan
  ├── Step "load"      ── Loader.Load(plan) → KnowledgeObject Stream
  ├── Step "link"      ── Linker.Link(objects) → Relations
  ├── Step "reduce"    ── Reducer.Reduce(graph, budget) → WorkingGraph
  └── Step "compile"   ── Compiler.Compile(graph) → CompiledContext

↓

DAG Step: "llm_reason" (输入 = CompiledContext.Formats["prompt"])
```

### 6.2 MCP Tools

```go
// plan        ── 传入 Task，返回 KnowledgePlan
// build_graph ── 传入 Task，返回 WorkingGraph（含 metrics）
// compile     ── 传入 Task + format，返回 CompiledContext
// query       ── 直接查询 KnowledgeStore
// distill     ── 将对话/经验蒸馏为 KnowledgeObject
```

### 6.3 Agent 调用链路

```go
agent.Process(ctx, task)
  │
  ├── plan := knowledge.Planner.Plan(ctx, task)     // 生成计划
  ├── graph := knowledge.Builder.Build(ctx, plan)   // 构建认知图
  ├── ctx := knowledge.Compiler.Compile(ctx, graph) // 编译上下文
  │
  ├── result := llm.Call(ctx, ctx.Formats["prompt"]) // LLM 推理
  │
  └── knowledge.Distill(task, result)               // 结果蒸馏回 Store
```

---

## 7. 完整目录结构（v2）

```
internal/
  knowledge/                           # AKF 核心
    object.go                          # KnowledgeObject（三层）
    relation.go                        # Relation（可扩展）
    intent.go                          # Intent（可计算）
    pipeline.go                        # KnowledgePipeline 编排

    pipeline/
      normalizer.go                    # Raw → Normalized
      summarizer.go                    # Normalized → Summary
      resolver.go                      # 实体解析（v2 新增）

    planner/
      interface.go                     # KnowledgePlanner 接口
      default.go                       # 默认 Planner 实现

    provider/
      interface.go                     # GraphProvider（Stream 模式）
      registry.go                      # ProviderRegistry
      postgres/
        provider.go                    # 外部 PostgreSQL Provider
      mysql/
        provider.go                    # 外部 MySQL Provider
      memory/
        provider.go                    # Memory Distillation Provider
      code/
        provider.go                    # 代码分析 Provider

    runtime/
      runtime.go                       # KnowledgeRuntime Facade（原 Builder）
      loader.go                        # Loader 接口 + 默认实现
      linker.go                        # Linker 接口
      reducer.go                       # Reducer 接口 + 策略
      working_graph.go                 # WorkingGraph 定义
      lazy_graph.go                    # Lazy Graph（v3 新增）

    linker/
      decision.go                      # DecisionLinker
      architecture.go                  # ArchitectureLinker
      timeline.go                      # TimelineLinker
      similarity.go                    # SimilarityLinker

    compiler/
      interface.go                     # Compiler 接口
      prompt.go                        # Prompt 格式
      markdown.go                      # Markdown 格式
      json.go                          # JSON 格式
      xml.go                           # XML 格式
      tool_schema.go                   # ToolSchema 格式

    store/
      interface.go                     # KnowledgeStore 接口（可选）
      postgres/
        store.go
      sqlite/
        store.go
      memory/
        store.go

  ares_memory/
    distillation/                      # 现有 + AKF 适配层
```

---

## 8. Phase 优先级与工作量

| Phase | 内容 | 工作量 | 依赖 | v3 变化 |
|-------|------|--------|------|---------|
| **P1** | KnowledgeObject（三层 + 独立 Representation）+ KnowledgePipeline（Normalizer/Resolver三阶段/Summarizer） | 中（3 天） | 无 | **Representation 独立、Resolver 三阶段** |
| **P2** | GraphProvider（Stream 模式）+ ProviderRegistry + SourceDiscovery + QueryPlanner | 中（3 天） | P1 | **新增 SourceDiscovery、QueryPlanner** |
| **P3** | KnowledgePlanner（解耦 Need）+ KnowledgeRuntime（Loader/Linker/Reducer/LazyGraph） | 大（5 天） | P2 | **Planner 解耦、Runtime 改名、Lazy Graph** |
| **P4** | Context Compiler（多格式：Prompt先、Markdown/JSON 后补） | 中（2 天） | P3 | **多格式** |
| **P5** | DAG 集成 + MCP API | 中（2 天） | P3-P4 | — |
| **P6** | Memory Distillation 对接 AKF（完成知识闭环） | 中（2 天） | P5 | **与 AKF 同层，不是子模块** |

**总计**：P1-P5 约 15 天

---

## 9. 三条设计原则（v2 确认）

> **① Source of Truth** — 所有外部数据源是事实来源，ARES 不复制业务数据。ARES 保存的是 KnowledgeObject 和证据引用。
>
> **② Graph is Ephemeral** — WorkingGraph 根据任务即时构建、即时裁剪、即时销毁，不作为持久化实体维护。
>
> **③ Knowledge First, Graph Last** — 知识对象是核心资产，图只是当前任务的一种运行时组织方式。换一个任务，用同一批 KnowledgeObject 生成完全不同的图。
