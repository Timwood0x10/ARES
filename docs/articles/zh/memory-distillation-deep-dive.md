# GoAgentX 架构深度解析（三）：记忆蒸馏 — 当 Agent 学会遗忘与提炼

> **系列目录**
>
> 第一篇：[待整理] 多 Agent 通信协议 AHP
> 第二篇：[待整理] Leader Agent 工作管线
> 第三篇：记忆蒸馏 — 当 Agent 学会遗忘与提炼（本文）

---

## 一、问题：Agent 的记忆困境

任何一个经历过生产环境的 Agent 开发者都会告诉你：**上下文窗口是最贵的资源**。

一个 Agent 运行若干小时后，会话历史可能积累数千条消息。直接全部塞进 LLM 的 context window 既不经济也不现实——GPT-4 的 128K 上下文不是用来装聊天记录的。

但更本质的问题是：**Agent 需要记住的不是对话本身，而是从对话中提炼出的可复用经验。**

如果你做过 Agent 开发，你一定遇到过以下场景：

1. **上下文膨胀**：Agent 与用户来回对话 50 轮后，prompt 中塞满了历史消息，Token 消耗暴涨，响应变慢
2. **经验无法复用**：Agent 刚解决完一个复杂的 bug，下一个用户问类似问题时又要重新推理一遍
3. **崩溃失忆**：Agent 进程挂了自动重启，结果用户问"刚才那个方案你分析到哪了？"，Agent 一脸茫然
4. **检索噪声**：把所有历史对话一股脑向量化存入数据库，结果检索时召回了一堆不相关的旧对话

GoAgentX 的记忆蒸馏（Memory Distillation）系统，正是为了解决这些根本矛盾而设计。

---

## 二、三层记忆架构

整个记忆系统的核心是一套三层架构，每一层都比上一层更精炼、更持久、更昂贵：

```
Session Memory          Task Memory             Distilled Memory
┌──────────────┐       ┌──────────────┐        ┌──────────────┐
│ 会话级       │       │ 任务级       │        │ 经验级       │
│ 滑动窗口     │  →    │ 执行记录     │   →    │ 结构化知识   │
│ TTL 过期     │       │ TTL 过期     │        │ 向量嵌入     │
│ 不持久化     │       │ 可持久化     │        │ LRU 淘汰     │
│ 最多100条    │       │ 最多1000条   │        │ 最多5000条   │
│ 纯内存       │       │ 内存/DB      │        │ pgvector     │
└──────────────┘       └──────────────┘        └──────────────┘
```

**设计哲学**：不是所有数据都需要蒸馏，也不是所有经验都需要向量化。

这三层解决了不同的问题：
- **Session Memory**：当前对话上下文。等价于人的"短期工作记忆"，用完即弃
- **Task Memory**：单次执行记录。记住 Agent 执行了哪些任务、输入输出是什么
- **Distilled Memory**：可复用的经验。从任务中提炼出"问题→解决方案"的结构化知识

每一层都在做「变少、变精、变持久」的转换。

---

## 三、MemoryManager 接口：统一抽象

三层记忆功能被抽象为 `MemoryManager` 接口，定义在 `internal/memory/manager.go`：

```go
type MemoryManager interface {
    // ── 会话层 ──
    CreateSession(ctx context.Context, userID string) (string, error)
    AddMessage(ctx context.Context, sessionID, role, content string) error
    GetMessages(ctx context.Context, sessionID string) ([]Message, error)
    DeleteSession(ctx context.Context, sessionID string) error
    BuildContext(ctx context.Context, input, sessionID string) (string, error)

    // ── 任务层 ──
    CreateTask(ctx context.Context, sessionID, userID, input string) (string, error)
    UpdateTaskOutput(ctx context.Context, taskID, output string) error
    DistillTask(ctx context.Context, taskID string) (*models.Task, error)

    // ── 经验层 ──
    StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error
    SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error)

    // ── 生命周期 ──
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    SetEventStore(store events.EventStore, streamID string)
}
```

这个接口的设计有几个值得注意的点：

**分层清晰**：CreateSession/AddMessage/GetMessages 操作会话，CreateTask/UpdateTaskOutput 操作任务，DistillTask/SearchSimilarTasks 操作经验。上层代码只需要关心自己需要的功能。

**蒸馏是显式的**：`DistillTask` 和 `StoreDistilledTask` 是独立的两个步骤。调用方可以按需选择做轻量提取还是完整蒸馏。

**事件溯源集成**：`SetEventStore` 让记忆系统接入事件总线，所有关键操作都会发出事件。

该接口有两个实现：

| 特性 | memoryManager | ProductionMemoryManager |
|------|---------------|------------------------|
| 适用场景 | 开发/测试/单机 | 生产/多租户/高可用 |
| Session 存储 | 内存 map | 内存 cache + PostgreSQL conversations |
| Task 存储 | 内存 map | PostgreSQL task_results |
| 蒸馏引擎 | 可选注入 | WriteBuffer + 异步 embedding |
| 向量检索 | 余弦相似度计算 | pgvector 混合搜索 (向量 + BM25) |
| 多租户 | 不支持 | TenantGuard |
| Session ID | 时间戳 + userID | crypto/rand 12 bytes → hex |

---

## 四、Session Memory：短期工作记忆

Session Memory 是最薄的一层。当 Agent 开始一次对话，它创建一个 Session，然后逐条追加消息。

在 `internal/memory/context` 包下的实现：

```go
type SessionMemory struct {
    sessions    map[string]*Session
    mu          sync.RWMutex
    maxSessions int
    sessionTTL  time.Duration
}

type Session struct {
    ID        string
    UserID    string
    Messages  []Message
    CreatedAt time.Time
    TTL       time.Duration
}
```

核心行为非常直接：

- **消息追加**：`AddMessage` 追加到 Messages slice
- **滑动窗口**：`BuildContext` 只返回最后 N 条（`MaxHistory`，默认 10）
- **TTL 过期**：后台 goroutine 定期扫描，删除超过 `SessionTTL`（默认 24h）的 session
- **纯内存存储**：不持久化，系统重启后丢失

这里有个有趣的细节：`BuildContext` 拼接上下文时使用滑动窗口，而不是简单截断。这意味着即使 session 中有 100 条消息，传给 LLM 的永远是最新的 10 条。老的上下文会自然"遗忘"。

```go
func (sm *SessionMemory) BuildContext(ctx context.Context, input, sessionID string) (string, error) {
    session, ok := sm.sessions[sessionID]
    if !ok {
        return input, nil
    }

    session.mu.RLock()
    defer session.mu.RUnlock()

    // 滑动窗口：只取最后 maxHistory 条
    start := 0
    if len(session.Messages) > sm.maxHistory {
        start = len(session.Messages) - sm.maxHistory
    }
    relevant := session.Messages[start:]

    // 拼接成 context string
    var sb strings.Builder
    for _, msg := range relevant {
        sb.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
    }
    sb.WriteString(fmt.Sprintf("user: %s\n", input))

    return sb.String(), nil
}
```

---

## 五、Task Memory：执行日志

Task Memory 记录 Agent 单次执行的完整信息：用户输入了什么、Agent 输出了什么。

```go
type TaskEntry struct {
    TaskID    string
    SessionID string
    UserID    string
    Input     string
    Output    string
    CreatedAt time.Time
    TTL       time.Duration
}
```

任务层的关键方法是 `Distill()`——它是整个蒸馏管线的入口：

```go
func (tm *TaskMemory) Distill(ctx context.Context, taskID string) (*models.Task, error) {
    entry, exists := tm.tasks[taskID]
    if !exists {
        return nil, ErrTaskNotFound
    }

    task := &models.Task{
        TaskID: entry.TaskID,
        Payload: map[string]any{
            "input":   entry.Input,
            "output":  entry.Output,
            "user_id": entry.UserID,
        },
        CreatedAt: entry.CreatedAt,
    }
    return task, nil
}
```

这个 `Distill()` 本身很轻量——只是把原始数据打包成 `models.Task`。真正的"蒸馏"（提炼、结构化、向量化）发生在上层调用中。

在 `memoryManager`（内存版）中，`DistillTask` 的完整流程是：

1. 调用 `taskMemory.Distill()` 获取原始数据
2. 发出 `EventMemoryDistilled` 事件
3. 返回 `*models.Task`

```go
func (m *memoryManager) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
    m.mu.RLock()
    defer m.mu.RUnlock()

    if m.stopped {
        return nil, ErrMemoryStopped
    }

    task, err := m.taskMemory.Distill(ctx, taskID)
    if err != nil {
        return nil, err
    }

    m.emitEvent(ctx, events.EventMemoryDistilled, map[string]any{
        "task_id":      taskID,
        "session_id":   task.Metadata["session_id"],
        "input_count":  len(task.Payload["input"].(string)),
        "output_count": len(task.Payload["output"].(string)),
    })

    return task, nil
}
```

---

## 六、Distilled Memory：经验提炼

这是整个记忆系统最具价值的部分。蒸馏后的数据不再是原始消息，而是结构化的 `Experience`。

定义在 `internal/memory/distillation/service.go`：

```go
type Experience struct {
    ID               string
    Problem          string           // 用户的问题/需求
    Solution         string           // Agent 的解决方案
    Confidence       float64          // 置信度 [0, 1]
    ExtractionMethod ExtractionMethod // 提取方式: direct / summary / pattern
    TenantID         string
    UserID           string
    Vector           []float64        // 向量嵌入
    Metadata         map[string]any
    CreatedAt        time.Time
}

type ExtractionMethod string

const (
    ExtractionDirect  ExtractionMethod = "direct"   // 直接从任务提取
    ExtractionSummary ExtractionMethod = "summary"  // LLM 总结提炼
    ExtractionPattern ExtractionMethod = "pattern"  // 模式匹配
)
```

`Experience` 的结构设计体现了三个重要的设计决策：

1. **Problem + Solution 分离**：这是最有价值的知识形式。不是"用户说了什么"，而是"用户遇到了什么问题，Agent 怎么解决的"
2. **Confidence 置信度**：让检索系统可以根据质量过滤结果。LLM 直接提取的 confidence 越高，模式匹配的较低
3. **ExtractionMethod 可溯源**：知道一条经验是怎么来的，方便后续评估和改进

`Distiller` 引擎接收一组对话消息，输出一组 `Experience`：

```go
type Distiller struct {
    config   DistillationConfig
    embedder EmbeddingService
    expRepo  ExperienceRepository
}

func (d *Distiller) DistillConversation(
    ctx context.Context,
    taskID string,
    messages []Message,
    tenantID, userID string,
) ([]*Memory, error)
```

每条 `Memory` 被转换为 `Experience` 后持久化到经验库，同时生成向量嵌入。

---

## 七、两条蒸馏路径

GoAgentX 中并存两条蒸馏路径，这不是设计妥协，而是分层策略。

**路径一：Legacy DistillTask（轻量提取）**

```
DistillTask(taskID)
  → taskMemory.Distill(taskID)    // 打包原始数据
  → emit EventMemoryDistilled      // 通知下游
  → return *models.Task
```

这条路径是 O(1) 的内存操作，不涉及 LLM 调用和向量生成。适合高频、低价值的任务——比如"查询天气"这种不需要长期记忆的交互。

**路径二：Distiller 引擎（完整蒸馏）**

```
StoreDistilledTask(taskID, distilled)
  → 从 distilled.Payload 提取 input/output/user_id
  → 构造 []distillation.Message
  → distiller.DistillConversation(...)
      → 调用 LLM 总结（可选）
      → 调用 embedder 生成向量
      → 返回 []*Memory
  → 遍历 Memory → 创建 Experience
  → expRepo.Create(experience)
```

这条路径涉及 LLM 调用 + 向量生成，成本较高。适合低频、高价值的任务——比如"帮我重构整个模块"这种值得记住的经验。

调用方可以按需选择：高价值任务走完整蒸馏，普通任务只做轻量提取。这种设计避免了对每一条消息都做昂贵的 embedding 调用。

---

## 八、ProductionMemoryManager：生产级实现

`ProductionMemoryManager` 是生产环境的默认实现。它不只是一个 CRUD 封装，而是一个带异步 embedding 管线的数据引擎。

```go
type ProductionMemoryManager struct {
    dbPool            *postgres.Pool
    tenantGuard       *TenantGuard
    retrievalService  *RetrievalService
    embeddingClient   *EmbeddingClient
    writeBuffer       *WriteBuffer
    conversationRepo  *ConversationRepository
    taskResultRepo    *TaskResultRepository
    sessionCache      *SessionCache   // 内存 LRU cache
    config            MemoryConfig
}
```

各个组件各司其职：

| 组件 | 职责 |
|------|------|
| dbPool | PostgreSQL 连接池 |
| tenantGuard | 多租户隔离，确保一个 tenant 看不到另一个 tenant 的数据 |
| retrievalService | 混合搜索引擎（向量 + BM25） |
| embeddingClient | 调用外部 embedding API 生成向量 |
| writeBuffer | 写入缓冲 + 异步 embedding 调度 |
| conversationRepo | 操作 conversations 表（无向量，仅历史追踪） |
| taskResultRepo | 操作 task_results 表（无向量） |
| sessionCache | 内存 LRU 缓存，减少热点 session 的 DB 查询 |

**Session ID 生成策略**：生产版使用 `crypto/rand` 而非时间戳，避免时序攻击和 ID 预测：

```go
func generateSessionID() string {
    b := make([]byte, 12)
    if _, err := rand.Read(b); err != nil {
        return fmt.Sprintf("session_%d", time.Now().UnixNano())
    }
    return "sess_" + hex.EncodeToString(b)
}
```

**Conversation 不嵌入向量**——这是最重要的一条设计原则：

```go
// Note: This stores conversations WITHOUT vector embedding (per design standard).
// conversations table is for history tracking only, retrieval uses knowledge/experience tables.
```

对话历史只服务于"当前 session 的上下文构建"，经验检索走独立的 experience 表。把两者混在一个向量空间里只会降低检索精度。

---

## 九、异步 Embedding 管线

这是整个蒸馏系统的性能心脏。先看数据流：

```
1. Write to DB with embedding_status = 'pending'
2. Write to embedding_queue with dedupe_key
3. Background Worker processes embedding tasks
4. Worker updates DB with embedding and status = 'completed'
```

在 `StoreDistilledTask` 中，数据流经 WriteBuffer：

```go
writeItem := &postgres.WriteItem{
    TenantID: tenantID,
    Table:    "experiences_1024",
    Content:  fmt.Sprintf("%v", distilled.Payload),
    Metadata: map[string]interface{}{
        "output":   "",
        "type":     "solution",
        "agent_id": "style-agent",
    },
}
if err := m.writeBuffer.Write(ctx, writeItem); err != nil {
    return errors.Wrap(err, "write to buffer")
}
```

为什么选择异步？因为 embedding 调用（尤其是通过 HTTP 调用外部 API）的延迟通常在 100ms-500ms。如果 `StoreDistilledTask` 同步等待 embedding 完成，整个 Agent 的任务管线都会被阻塞。

通过 **WriteBuffer + EmbeddingQueue + Background Worker** 的解耦：

- **业务写入 O(1)**：写入 buffer 后立即返回，不阻塞
- **异步处理**：embedding 在后台批量消化
- **失败重试**：embedding 服务暂时不可用时，数据不丢失
- **去重保护**：`dedupe_key` 防止同一条数据被多次 embedding

这是经典的 CQRS 变体——写操作不等待读模型的就绪。对记忆系统来说特别合适：用户不需要在调用 `StoreDistilledTask` 后立刻能搜索到结果，短暂的最终一致性是可接受的。

---

## 十、向量检索：相似经验召回

当 Agent 需要参考过去的经验时，`SearchSimilarTasks` 执行混合搜索。

```go
func (m *ProductionMemoryManager) SearchSimilarTasks(
    ctx context.Context,
    query string,
    limit int,
) ([]*models.Task, error) {
    // 1. 生成查询向量
    queryVector, err := m.embedder.EmbedWithPrefix(ctx, query, "query:")
    if err != nil {
        return nil, errors.Wrap(err, "embed query")
    }

    // 2. 配置检索计划——只搜 experience
    searchRequest.Plan.SearchExperience = true
    searchRequest.Plan.SearchKnowledge = false
    searchRequest.Plan.SearchTools = false
    searchRequest.Plan.ExperienceWeight = 1.0

    // 3. 执行混合搜索
    results, err := m.retrievalService.Search(ctx, searchRequest)
    if err != nil {
        return nil, errors.Wrap(err, "search experiences")
    }

    // 4. 转换回 models.Task 格式
    var tasks []*models.Task
    for _, result := range results {
        if result.Source == "experience" {
            task := &models.Task{
                TaskID:   result.ID,
                Payload:  map[string]any{
                    "input":  result.Content,
                    "output": result.Metadata["output"],
                    "score":  result.Score,
                },
                Priority: int(result.Score * 100),
            }
            tasks = append(tasks, task)
        }
    }
    return tasks, nil
}
```

关键的 `RetrievalPlan` 结构体使搜索策略高度可配置：

```go
type RetrievalPlan struct {
    SearchExperience bool
    SearchKnowledge  bool
    SearchTools      bool
    ExperienceWeight float64
    KnowledgeWeight  float64
    ToolsWeight      float64
}
```

`RetrievalService.Search()` 内部实现 hybrid search——同时跑 **pgvector 余弦距离** 和 **BM25 全文检索**，加权合并结果。这是生产环境的最佳实践：纯向量检索对新术语和冷门词召回效果不好，结合 BM25 可以互补。

底层的 `VectorStore` 接口极其精简：

```go
type VectorStore interface {
    Search(ctx context.Context, table string, embedding []float64, limit int) ([]*SearchResult, error)
    AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error
    CreateCollection(ctx context.Context, name string, dimension int) error
}
```

`table` 参数的存在意味着一个 VectorStore 实例可以管理多个集合——这为多租户隔离提供了基础：每个 tenant 可以有自己的 experience/knowledge 表，共享同一个 pgvector 实例。

---

## 十一、事件溯源与记忆

MemoryManager 通过 `SetEventStore` 接入事件溯源系统。关键事件类型：

```go
const (
    EventMemoryDistilled EventType = "memory.distilled"
    EventSessionCreated  EventType = "session.created"
    EventMessageAdded    EventType = "message.added"
)
```

`emitEvent()` 在所有关键操作点被调用：

```go
// session 创建
m.emitEvent(ctx, events.EventSessionCreated, map[string]any{
    "session_id": sessionID,
    "user_id":    userID,
})

// 消息添加
m.emitEvent(ctx, events.EventMessageAdded, map[string]any{
    "session_id": sessionID,
    "role":       role,
})

// 蒸馏完成
m.emitEvent(ctx, events.EventMemoryDistilled, map[string]any{
    "task_id":      taskID,
    "input_count":  len(inputStr),
    "output_count": len(memories),
})
```

事件的用途不仅是审计。在 GoAgentX 中，它们构成了 Runtime Manager **认知恢复**的基础（见下一节）。

---

## 十二、Runtime 的认知恢复

这是记忆系统最有价值的联动场景。当 Agent 崩溃后，Runtime Manager 执行 `RestoreAgent`，分两步恢复记忆。

**第一步：操作恢复**

`recoverAgentState` 从 EventStore 读取事件，调用 `StatefulAgent.RestoreState` 重建 session_id 等关键状态：

```go
func (m *Manager) recoverAgentState(
    ctx context.Context,
    agentID string,
    factory AgentFactory,
) (base.Agent, error) {
    newAgent := factory()
    if newAgent == nil {
        return nil, fmt.Errorf("runtime: factory returned nil for agent %s", agentID)
    }

    // 重放事件
    evts := m.replayEvents(ctx, agentID)

    if sa, ok := newAgent.(base.StatefulAgent); ok {
        // 从事件重建状态
        state := buildStateFromEvents(evts)

        // 认知恢复：加载对话历史
        if m.memManager != nil {
            cognitiveState := m.buildCognitiveState(ctx, agentID, state)
            for k, v := range cognitiveState {
                state[k] = v
            }
        }

        // 恢复状态
        if len(state) > 0 {
            if err := sa.RestoreState(state); err != nil {
                slog.Warn("runtime: RestoreState failed",
                    "agent_id", agentID, "error", err)
            }
        }

        // 重放事件到 agent
        if len(evts) > 0 {
            if err := sa.ReplayEvents(evts); err != nil {
                slog.Warn("runtime: ReplayEvents failed",
                    "agent_id", agentID, "error", err)
            }
        }
    }
    return newAgent, nil
}
```

**第二步：认知恢复**

`buildCognitiveState` 通过 MemoryManager 获取最近的 session 和对话历史：

```go
func (m *Manager) buildCognitiveState(
    ctx context.Context,
    agentID string,
    operationalState map[string]any,
) map[string]any {
    state := make(map[string]any)

    // 从操作状态或 checkpoint 获取 session_id
    sessionID, _ := operationalState["session_id"].(string)
    if sessionID == "" {
        sessionCtx, sessionCancel := context.WithTimeout(ctx, 5*time.Second)
        sid, err := m.memManager.GetLatestSessionForLeader(sessionCtx, agentID)
        sessionCancel()
        if err != nil {
            return state
        }
        sessionID = sid
    }

    if sessionID == "" {
        return state
    }

    // 加载对话历史（5 秒超时保护）
    msgCtx, msgCancel := context.WithTimeout(ctx, 5*time.Second)
    defer msgCancel()
    messages, err := m.memManager.GetMessages(msgCtx, sessionID)
    if err != nil {
        return state
    }

    if len(messages) > 0 {
        state["session_id"] = sessionID
        state["conversation_history"] = messages
    }
    return state
}
```

这个机制意味着：Agent 崩溃后重新拉起，不只是重建连接池和定时器——它连"刚才聊到什么了"都知道。这是 GoAgentX 自愈能力的认知层面。用 5 秒超时兜底，防止慢 DB 阻塞恢复流程。

---

## 十三、配置体系

记忆系统的配置暴露在 YAML 层，同时运行时参数由代码常量控制：

```yaml
memory:
  enabled: true
  session:
    enabled: true
    max_history: 50
  user_profile:
    enabled: true
    storage: postgres
    vector_db: true
  task_distillation:
    enabled: true
    storage: postgres
    vector_store: true
    prompt: "请总结用户需求和技术方案，提取可复用的模式"
```

运行时配置：

```go
type MemoryConfig struct {
    Enabled          bool          `yaml:"enabled"`
    Storage          string        `yaml:"storage"`       // memory / postgres
    MaxHistory       int           `yaml:"max_history"`   // 默认 10
    MaxSessions      int                                 // 默认 100
    MaxTasks         int                                 // 默认 1000
    MaxDistilledTasks int                                // 默认 5000
    SessionTTL       time.Duration                       // 默认 24h
    TaskTTL          time.Duration                       // 默认 7d
    VectorDim        int                                 // 默认 128
}
```

YAML 暴露业务语义的开关（session/profile/distill），代码常量设性能相关的阈值（TTL/Max），环境变量覆写敏感信息（DB 密码等）。三层配置互不冲突。

---

## 十四、设计权衡与进化

在梳理完整个记忆系统后，有几个值得深入讨论的设计决策：

### 1. Conversation 不嵌入向量

这是反复确认过的设计点。`ProductionMemoryManager.AddMessage` 的注释明确说 conversations 表只做历史追踪，检索走独立的 experience 表。

决策依据：**对话历史是线性叙事，经验是网状知识**。前者只需要按时间倒排，后者需要语义检索。把两者混在一个向量空间里只会降低检索精度。

### 2. 两套蒸馏路径并存

Legacy `DistillTask` 和新的 `Distiller` 引擎并存不是设计妥协，而是分层策略。

- `DistillTask`：O(1) 内存操作，不涉及 LLM 调用和向量生成
- `StoreDistilledTask + Distiller`：涉及 LLM 调用 + 向量生成

业务方可以根据任务价值决定走哪条路——高频低价值任务走轻量级，低频高价值任务走完整蒸馏。

### 3. Session ID 的生成策略进化

- 开发版：`session_{userID}_{timestamp}`——简单可读
- 生产版：`sess_{12 bytes crypto/rand hex}`——不可预测

这反映了安全意识的进化。可预测的 session ID 在多租户环境下是信息泄露的潜在入口。

### 4. WriteBuffer 异步策略

写入先落 buffer → 批量 flush 到 DB → 异步 embedding → 失败重试。这是经典的 CQRS 变体。

用户不需要在调用 `StoreDistilledTask` 后立刻能搜索到结果，短暂的最终一致性（秒级）是可接受的。但如果同步等待 embedding，用户需要等几百毫秒才能继续。

### 5. VectorStore 的简洁设计

三个方法、一个 `table` 参数。这让一个 VectorStore 实例可以管理多个集合——多租户只需共用同一个 pgvector 实例，用表名隔离。

### 6. 三层 TTL 策略

```
Session 内存：24h → 纯内存，重启丢失
Task 内存/DB：7d → 可持久化，定期清理
Distilled Memory：LRU 淘汰（Max=5000）→ 持久化，按容量淘汰
```

TTL 逐层递增、价值密度逐层提高。Session 数据量大但生命周期短，Expirience 数据量小但需要长期保留。

---

## 总结

GoAgentX 的记忆系统不是简单地把数据塞进 PostgreSQL，而是构建了一条完整的数据提纯管线：

```
原始消息 → 滑动窗口(Session) → 执行记录(Task) → 结构化经验(Experience) → 向量索引(pgvector)
    ↑            ↑                    ↑                    ↑                        ↑
  用户输入     TTL 过期            Distill API          LLM 提炼 +            写缓冲异步
                                                        Embedding              embedding
```

每一层都在做「变少、变精、变持久」的转换：

- **Session**：记住当前会话的上下文 → 滑动窗口只保留最新 N 条
- **Task**：记录单次任务执行 → **Distill** 提取关键信息
- **Experience**：从任务中提炼可复用经验 → **LLM** 总结 + **向量化** 存储
- **Dashboard**：Flight Recorder 可视化管理记忆状态

而 Runtime 的认知恢复机制，把这条管线的价值闭环了——蒸馏出的经验不仅能被未来的任务检索复用，还能在 Agent 崩溃后用于恢复对话上下文。

---

**下一篇预告**：Workflow Engine—一个用 MutableDAG 实现的线程安全有向图引擎，支持运行时动态增删节点、增量环检测、信号量并发调度和 5 秒死锁超时。还有可插拔的 Human-in-the-Loop。
