# ares 架构深度解析（六）：安全与可观测性 — 纵深防御与透明追踪

> Agent 越强大，搞破坏的能力也越强。你给 Agent 装了代码执行器、文件读写、网络请求——然后它被 prompt injection 骗了……
> 我在设计工具系统的时候就一直在想一个问题：**怎么让 Agent 输出敏感信息时不翻车？**
> 答案是：别等到翻车再处理。从日志输出那一步就开始脱敏。

---

## 一、为什么安全不能是事后加的

给 Agent 装工具就像给 teenager 车钥匙——他能去的地方很多，但你不能保证他不闯祸。

我见过太多 AI 项目上线后翻车的案例：Agent 在日志里把用户的 API Key 打印出来了、prompt 里不小心带上了数据库密码、LLM 响应里泄露了手机号……这些问题一旦发生，不是"下次注意"就能解决的——合规审计追着你跑。

所以我在设计 ares 的基础设施时，把安全和可观测性放在了一起考虑。不是"先做功能再做安全"，而是**从第一天就把它们设计进系统里**。

这篇文章聊四个模块：**安全（脱敏）**、**可观测性（追踪）**、**限流**和**优雅关闭**。它们不直接面向用户，但没有它们，你把 Agent 放到生产环境就是裸奔。

核心文件清单：

| 模块 | 文件路径 |
|------|----------|
| 安全（脱敏） | `internal/security/sanitizer.go` |
| 可观测性（Tracer） | `internal/observability/tracer.go`、`noop.go`、`log.go` |
| 限流（Limiter） | `internal/ratelimit/` 目录下四个源文件 |
| 优雅关闭 | `internal/ares_shutdown/` 目录下四个源文件 |
| 中间件模式 | `internal/dashboard/api.go`、`internal/arena/http.go` |
| 端到端集成 | `internal/workflow/graph/graph.go`、`.../executor.go` |
| API 层集成 | `api/service/graph/service.go` |

---

## 二、安全模块：基于正则的敏感信息脱敏

安全模块的核心设计思路是 **"字段类型 + 正则检测 → 针对性脱敏策略"**，而非简单的全局字符串替换。它提供了两重防御：

### 2.1 SensitiveFieldType — 用字符串常量而非 iota 做类型标识

与常见的 iota 枚举不同，ares 使用字符串常量来定义敏感字段类型：

```go
const (
    SensitiveFieldTypeAPIKey       SensitiveFieldType = "api_key"
    SensitiveFieldTypePassword     SensitiveFieldType = "password"
    SensitiveFieldTypeEmail        SensitiveFieldType = "email"
    SensitiveFieldTypePhone        SensitiveFieldType = "phone"
    SensitiveFieldTypeCreditCard   SensitiveFieldType = "credit_card"
    SensitiveFieldTypeSSN          SensitiveFieldType = "ssn"
)
```

这种设计的优势在于：字符串常量天然支持序列化（JSON/YAML 配置可直接引用），且便于在运行时通过反射或字符串匹配动态识别字段类型。

### 2.2 双层检测机制

Sanitizer 的工作流程分为两层：

**第一层：按字段名匹配。** 对于结构化的 JSON 输入（如 LLM 请求/响应），Sanitizer 遍历字段名，通过 `getFieldType()` 方法将字段名映射到 `SensitiveFieldType`。例如字段名包含 "key" 或 "token" 就归类为 APIKey。

**第二层：按正则匹配。** 对于非结构化的文本或 JSON 无法覆盖的场景，Sanitizer 维护了一组预编译的正则模式，对文本内容进行全面扫描：

```go
type Sanitizer struct {
    patterns []sanitizePattern
}

type sanitizePattern struct {
    pattern *regexp.Regexp
    mask    func(string) string
}
```

每种敏感类型对应不同的掩码策略。以关键的 `maskAPIKey` 为例：

```go
func (s *Sanitizer) maskAPIKey(input string) string {
    if len(input) <= 8 {
        return strings.Repeat("*", len(input))
    }
    return input[:4] + strings.Repeat("*", len(input)-8) + input[len(input)-4:]
}
```

这种"保留首尾各 4 字符，中间用 `*` 替换"的策略，既掩盖了敏感内容，又保留了 API Key 的格式特征，便于调试时区分不同的 Key。

其他掩码策略：

- **密码（Password）**：完全用 `*` 替换
- **邮箱（Email）**：保留域名（`@` 之后），用户名部分首尾各 1 字符 + `***`
- **手机号（Phone）**：保留前 3 位和后 4 位，中间用 `****` 替换
- **信用卡号（CreditCard）**：保留末 4 位
- **SSN**：保留末 4 位

### 2.3 SafeLogger — 安全的日志包装器

`SafeLogger` 是 Sanitizer 的一个优雅的应用层包装：

```go
type SafeLogger struct {
    sanitizer *Sanitizer
    logger    func(string)
}
```

它将任何 `func(string)` 类型的日志函数包装为自动脱敏的版本，所有输出日志在写出前自动经过 Sanitizer 处理。这种设计使得安全模块可以透明地嵌入已有的日志体系，无需修改日志消费者的代码。

### 2.4 包级便捷函数

```go
func SanitizeLog(logger func(string), message string) {
    s := &Sanitizer{}
    s.SafeLogger(logger).Output(message)
}
```

`SanitizeLog()` 是一个"开箱即用"的一键脱敏函数，适用于脚本或简单场景，无需提前构造 Sanitizer 实例。

### 2.5 一个让我后背发凉的教训

说实话，第一版 Sanitizer 并不完善。

最初我只对结构化 JSON 输入做了字段名匹配——API Key 脱了、密码脱了，但 LLM 响应的 `content` 字段里如果包含了手机号或邮箱，直接原样输出。我想当然地认为："LLM 返回的内容，应该不会有敏感信息吧？"

直到运维同事跑来找我，说日志系统里搜到了完整的手机号——不是脱敏后的 `138****5678`，是明文的 `13812345678`。排查下来发现：第一版的正则检测层只配置了对 JSON key 的匹配规则，没有针对文本内容的全量扫描。`content` 字段里用户说的手机号，从 LLM 响应的往返过程中一路都是明文，日志里也照写不误。

幸好发现得早，日志只在测试环境保留了两天。但这件事让我彻底改了对"脱敏"的看法：**敏感信息可能出现在任何字段里，不限于你预判的那些。** 这也是为什么后来的 Sanitizer 一定要有两层检测——字段名匹配兜不住的时候，正则全量扫描兜底。

---

## 三、可观测性模块：Tracer 接口与两套实现

ares 的可观测性采用经典的**观察者模式**：定义抽象的 `Tracer` 接口，提供 `NoopTracer` 和 `LogTracer` 两种实现。

### 3.1 Tracer 接口定义

```go
type Tracer interface {
    RecordLLMCall(ctx context.Context, call *LLMCall)
    RecordToolCall(ctx context.Context, call *ToolCall)
    RecordAgentStep(ctx context.Context, step *AgentStep)
    RecordError(ctx context.Context, err *AgentError)
    GetTraceID(ctx context.Context) string
    WithTrace(ctx context.Context) context.Context
}
```

这个接口覆盖了 Agent 执行过程中的四个关键观测点：
1. **LLM 调用**（`RecordLLMCall`）：记录模型、prompt、response、token 用量和耗时
2. **工具调用**（`RecordToolCall`）：记录工具名、输入、输出和耗时
3. **Agent 步骤**（`RecordAgentStep`）：记录每个节点的执行阶段
4. **错误**（`RecordError`）：记录错误类型和消息

### 3.2 NoopTracer — 零开销的默认实现

```go
type NoopTracer struct{}

var traceCounter uint64

func (t *NoopTracer) generateTraceID() string {
    id := atomic.AddUint64(&traceCounter, 1)
    return fmt.Sprintf("trace-%d", id)
}
```

`NoopTracer` 是 Graph 构造函数的默认 tracer（见 `NewGraph()`）。它的 `Record*` 方法均为空实现，但 `generateTraceID()` 使用 `atomic.AddUint64` 生成自增 trace ID，并非完全的"零分配"（`fmt.Sprintf` 有少量堆分配）。`WithTrace` 方法会检查上下文中是否已有 trace ID，避免重复生成。

### 3.3 LogTracer — 基于 slog 的结构化日志实现

```go
type LogTracer struct {
    logger *slog.Logger
}
```

`LogTracer` 将 Tracer 的每个事件点映射为一条结构化日志记录。例如 `RecordLLMCall`：

```go
func (t *LogTracer) RecordLLMCall(ctx context.Context, call *LLMCall) {
    if call.Error != nil {
        t.logger.ErrorContext(ctx, "llm call failed",
            "trace_id", call.TraceID,
            "model", call.Model,
            "prompt_len", len(call.Prompt),
            "response_len", len(call.Response),
            "tokens", call.TokensUsed,
            "duration_ms", call.Duration.Milliseconds(),
            "error", call.Error,
        )
    } else {
        t.logger.InfoContext(ctx, "llm call completed",
            "trace_id", call.TraceID,
            // ... same fields ...
        )
    }
}
```

关键设计要点：
- **成功/失败分支**：失败时使用 `ErrorContext` 记录，成功时使用 `InfoContext`
- **结构化属性**：所有字段以 key-value 对形式传入，方便日志收集系统（如 Loki、Datadog）解析
- **`slog.Logger` 注入**：LogTracer 不负责创建 logger，而是接收外部注入，符合依赖反转原则

---

## 四、限流模块：三种算法 + 工厂模式

限流模块提供了三个内置算法，并通过工厂模式支持扩展。

### 4.1 Limiter 接口与 Factory

```go
type Limiter interface {
    Allow() bool
    Wait(ctx context.Context) error
    Reset()
    Rate() float64
}

type Factory struct {
    constructors map[string]func(config map[string]any) (Limiter, error)
}
```

`Factory` 采用注册-创建模式，通过 `Register(name, constructor)` 注册限流器构造器，通过 `Create(name, config)` 按名创建。`DefaultFactory` 是包级全局单例，预注册了三种内置限流器。

### 4.2 TokenBucketLimiter — 令牌桶算法

```go
type TokenBucketLimiter struct {
    rate       float64
    burst      int
    tokens     float64
    lastCheck  time.Time
    mu         sync.Mutex
}
```

核心逻辑在 `Allow()` 中：每次调用时先根据 `time.Since(lastCheck).Seconds() * rate` 计算应补充的令牌数，再判断是否放行。`Wait()` 实现为 busy-loop + `time.After(waitTime)`，其中 waitTime = `float64(time.Second) / rate`。支持 `SetRate()` 和 `SetBurst()` 运行时动态调整参数。

### 4.3 SlidingWindowLimiter — 滑动窗口算法

```go
type SlidingWindowLimiter struct {
    rate       int
    windowSize time.Duration
    requests   []time.Time
    mu         sync.Mutex
}
```

基于时间戳数组实现：每次请求将当前时间追加到切片尾部，`cleanup()` 移除窗口外的过期请求。`Allow()` 判断 `len(l.requests) < l.rate`，`Wait()` 则计算到最早请求过期的时间：`waitTime = l.windowSize - time.Since(oldest)`。支持 `ResetAt(t time.Time)` 实现定时重置。

### 4.4 SemaphoreLimiter — 信号量限流

```go
type SemaphoreLimiter struct {
    slots chan struct{}
}
```

channel 信号量实现：`Acquire()` 从 channel 读取，`Release()` 写回。`WeightedSemaphoreLimiter` 是加权版本，使用 `sync.Cond` + `context.AfterFunc` 实现取消传播，支持按不同 key 分配权重配额（如按 API Key 分配）。

### 4.5 三种算法的选型对比

这三种限流算法各有适用场景：令牌桶算法适合应对突发流量，滑动窗口算法能够精确控制时间窗口内的请求总数，信号量算法则擅长限制并发数而非请求速率。在实际生产环境中，Graph 执行器的入口使用令牌桶最为常见，因为 Agent 的工作流天然存在"突发式"特征——多个节点可能同时到达就绪队列，需要短时间内的突发放行能力。需要特别注意的是，限流失败时调用方必须处理 `Wait()` 返回的 context 取消错误，否则可能导致请求堆积和 goroutine 泄露。

---

## 五、优雅关闭模块：四阶段状态机

优雅关闭模块是系统容错的最后一道防线，其设计确保进程在任何情况下都能有序退出。

### 5.1 Manager — 四阶段执行

```go
const (
    PhasePreShutdown Phase = iota // 0
    PhaseGraceful                 // 1
    PhaseForce                    // 2
    PhaseDone                     // 3
)
```

每个阶段的执行逻辑：

```go
func (m *Manager) executePhase(ctx context.Context, phase Phase) []CallbackResult {
    var wg sync.WaitGroup
    results := make([]CallbackResult, 0, len(callbacks))
    resultsMu := sync.Mutex{}

    for _, cb := range callbacks {
        wg.Add(1)
        go func(cb RegisteredCallback) {
            defer wg.Done()
            defer func() { // panic recovery
                if rec := recover(); rec != nil { ... }
            }()
            // Execute with per-callback timeout
            cbCtx, cancel := context.WithTimeout(ctx, cb.timeout)
            defer cancel()
            err := cb.fn(cbCtx)
            // ...
        }(cb)
    }
    wg.Wait()
    return results
}
```

每个回调在独立的 goroutine 中执行，拥有自己的超时上下文。panic recovery 确保单个回调崩溃不会影响整体关闭流程。整个阶段还有一个 5 秒的硬超时兜底。

### 5.2 PhaseExecutor — 带指数退避的重试状态机

```go
type PhaseExecutor struct {
    phase    Phase
    state    ExecutorState
    retry    int
    maxRetry int
    rollback func()
}
```

PhaseExecutor 的状态转换：`Pending → Running → Completed / Failed`。重试机制使用指数退避：

```go
backoff = time.Duration(1 << uint(attempt)) * time.Second
```

注意：当 attempt 达到 30 时，`1 << uint(30)` 会产生约 10.7 亿秒的溢出（约 34 年），因此在循环中 attempt 会被 cap 到 29（与 `1<<30` 相除保护一致）。

### 5.3 CallbackRegistry — 带优先级的回调注册

回调通过 `map[Phase][]RegisteredCallback` 按阶段组织，使用冒泡排序按优先级降序执行。`CallbackChain` 支持串行链式和并行批处理两种执行模式。

### 5.4 SignalHandler — 信号监听

```go
type SignalHandler struct {
    signals []os.Signal
    ch      chan os.Signal
}
```

集成标准库 `os/signal`，监听 `SIGINT`、`SIGTERM`、`os.Interrupt`。提供 `WaitForSignal()` 和 `WaitForContextOrSignal()` 两种阻塞等待方式。

### 5.5 关闭流程的完整时序

一个典型的优雅关闭流程如下：首先系统接收到 SIGINT 或 SIGTERM 信号，SignalHandler 将信号转发到 Manager。Manager 进入 PhasePreShutdown 阶段，执行所有预关闭回调（如断开数据库连接、发送心跳停止信号）。随后进入 PhaseGraceful 阶段，每个回调拥有独立的超时上下文，并以 goroutine 并发执行。如果所有回调在超时前完成，Manager 直接进入 PhaseDone；如果某些回调超时，Manager 进入 PhaseForce 阶段，强制执行剩余回调并记录超时信息。最终进入 PhaseDone 阶段，系统完成退出。这种分级保护机制的核心思想是"先礼貌请求、再强制要求"，确保在任何情况下系统都能以可预测的方式退出，不会因为某个回调卡死而导致进程残留。

---

## 六、中间件模式：CORS 与 panic 恢复

### 6.1 Dashboard 的 withCORS 中间件

```go
func withCORS(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
        if r.Method == http.MethodOptions {
            w.WriteHeader(http.StatusOK)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

支持通配符 CORS，对 OPTIONS 预检请求直接返回 200。

### 6.2 统一的 withRecovery 中间件

```go
func withRecovery(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if rec := recover(); rec != nil {
                slog.Error("api: panic recovered", "path", r.URL.Path, "recover", rec)
                writeJSON(w, http.StatusInternalServerError, errResp("internal server error"))
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

Arena 模块也有其独立的 `RecoverMiddleware`，行为类似但使用独立的 `slog` 实例。

### 6.3 中间件链式组合

```go
func (a *APIv2) Handler() http.Handler {
    mux := http.NewServeMux()
    // ... 注册所有路由 ...
    return withRecovery(withCORS(mux))
}
```

这种洋葱圈风格的中间件组合确保了所有请求路径都经过 CORS 和 panic 恢复的保护。

---

## 七、端到端集成：从 Graph Service 到 LLM Client

这四个模块并非孤立存在，它们在 Graph 执行引擎中紧密协作。以下是完整的调用链：

### 7.1 API 层的注入入口

在 `api/service/graph/service.go` 的 `Service.Execute()` 中：

```go
func (s *Service) Execute(ctx context.Context, g *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error) {
    // 注入 Tracer 和 Limiter
    if s.tracer != nil {
        g.SetTracer(s.tracer)
    }
    if s.limiter != nil {
        g.SetLimiter(s.limiter)
    }
    // 创建 State → 执行 Graph
    result, err := g.Execute(ctx, state)
}
```

`Service` 的构造函数中，如果 `config.Tracer` 为 nil，默认使用 `observability.NewNoopTracer()`，确保可观测性永远是启用状态。

### 7.2 Graph 执行器中的观测点

在 `internal/workflow/graph/executor.go` 的 `Graph.Execute()` 中，观测点贯穿整个执行流程：

**步骤 1：限流检查**
```go
if g.limiter != nil {
    if err := g.limiter.Wait(ctx); err != nil {
        return nil, errors.Wrap(err, "rate limit")
    }
}
```

**步骤 2：每个节点执行前后记录 AgentStep**
```go
g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
    TraceID:  g.tracer.GetTraceID(ctx),
    AgentID:  nodeID,
    StepName: "execute",
})
// ... 执行节点 ...
g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
    TraceID:  g.tracer.GetTraceID(ctx),
    AgentID:  nodeID,
    StepName: "execute",
    Duration: time.Since(nodeStart),
})
```

**步骤 3：失败时记录 Error**
```go
if err != nil {
    g.tracer.RecordError(ctx, &observability.AgentError{
        TraceID:   g.tracer.GetTraceID(ctx),
        AgentID:   nodeID,
        ErrorType: "execution_error",
        Message:   err.Error(),
    })
}
```

**步骤 4：Graph 执行完毕后记录 ToolCall**
```go
if g.tracer != nil {
    g.tracer.RecordToolCall(ctx, &observability.ToolCall{
        TraceID:  g.tracer.GetTraceID(ctx),
        ToolName: g.id,
        Input:    state.ToParams(),
        Output:   state.ToParams(),
        Duration: time.Since(startTime),
    })
}
```

### 7.3 LLM Client 中的观测点

`internal/llm/client.go` 中，`recordLLMCall()` 是一个内部方法，在 `Generate()` 和 `GenerateStream()` 中均被调用：

```go
func (c *Client) recordLLMCall(ctx context.Context, prompt, response string, tokens int, start time.Time, err error) {
    if c.tracer == nil {
        return
    }
    c.tracer.RecordLLMCall(ctx, &observability.LLMCall{
        TraceID:    c.tracer.GetTraceID(ctx),
        Model:      c.config.Model,
        Prompt:     prompt,
        Response:   response,
        TokensUsed: tokens,
        Duration:   time.Since(start),
        Error:      err,
    })
}
```

对于流式调用（`GenerateStream`），`recordLLMCall` 在流结束后异步调用，goroutine 中累积完整 response 后统一记录。

---

## 八、架构观察与最佳实践

### 8.1 构造函数 panic 模式 — 失败即崩溃

Graph 模块的构造函数在检测到非法参数时直接 panic（如 `NewGraph("")`、`NewGraphWithTracer("id", nil)`）。代码注释明确说明：

> "This is intentional as it indicates a programming error in the calling code. These methods are used during workflow graph initialization (startup phase), and invalid parameters represent fatal startup failures."

这是 Go 社区中"失败即崩溃"（Fail-Fast）理念的体现：启动时的编程错误越早暴露越好，不应返回 error 让调用方自行处理。

### 8.2 分层防御的设计哲学

- **安全模块**独立于整个执行管线，任何日志输出前自动脱敏，属于"纵深防御"的最后一层
- **可观测性模块**以接口方式注入 Graph 和 LLM Client，属于"透明追踪"的基础设施
- **限流模块**在 Graph 执行入口处拦截（`Execute` 方法第一行），不做精细化的每个节点限流
- **优雅关闭模块**完全独立，通过 `SignalHandler` 监听系统信号触发，不与业务逻辑耦合

### 8.3 模块间的耦合度分析

值得关注的是，这四个模块虽然都影响 Graph 的执行行为，但它们之间的耦合度极低。安全模块完全独立，不对 Graph 或 LLM Client 产生任何代码级的侵入；可观测性模块通过 Tracer 接口与 Graph 和 LLM Client 保持松耦合，可以通过 `SetTracer()` 随时替换实现；限流模块同样通过接口与 Graph 耦合，且与可观测性毫无关系——限流成功与否不产生观测事件；优雅关闭模块则完全游离于业务执行路径之外，只在进程生命周期结束时介入。这种"各自为政"的设计使得每个模块都可以独立测试、独立替换甚至独立移除，极大地降低了系统的维护复杂度。

### 8.4 默认安全 vs 显式配置

- `NewGraph()` 默认使用 `NoopTracer`，可观测性默认开启（即使只是空实现）
- `NewService()` 中如果 `config.Tracer` 为 nil，也默认使用 `NoopTracer`
- 限流器默认 nil，不做限流 —— 需要显式配置
- 安全模块没有全局默认实例，需要调用方自行构造

这种"默认安全"的设计确保了即使在没有配追踪器的情况下，Tracer 的调用代码永远不会 panic（因为默认是 NoopTracer 的非 nil 实例）。这是一种值得借鉴的 Go 语言惯用法——对于可选的接口依赖，提供非 nil 的默认实现（noop），既避免了调用方每次使用前都判 nil，又保留了未来切换为真实实现的能力。相反，限流和安全模块没有默认实现，是因为它们是"按需启用"的：不是每个部署环境都需要限流，也不是每段日志都需要脱敏，让调用方按需选择才是更好的设计。

### 8.5 从 Arena 看安全与可观测性的"反向"应用

有意思的是，Arena 模块（混沌工程测试框架）同时使用了安全模块和可观测性模块提供的模式，但目的完全相反：可观测性的 Tracer 接口被 Arena 用来记录故障注入事件和恢复过程，帮助分析系统的容错行为；而安全模块的中间件模式（RecoverMiddleware）被 Arena 用来捕获故障注入过程中可能发生的预期内 panic，确保 Arena 的核心循环不会被单个 Agent 的崩溃所影响。Arena 的 28 个路由处理函数全部通过 RecoverMiddleware 保护，这体现了"安全"与"可观测性"在混沌工程场景下的独特协同：可观测性让人看到故障，安全让人承受故障。

---

## 九、总结

四个模块，各管一摊：
- **安全模块**：正则+字段名双重检测，日志输出前自动脱敏
- **可观测性模块**：Tracer 接口解耦了"记什么"和"怎么记"
- **限流模块**：工厂模式，三种算法随便换
- **优雅关闭模块**：四阶段状态机 + 指数退避，任何场景都能有序退出

说实话，这四个模块不是最"酷"的部分——它们是最"不酷但必须有"的部分。没有脱敏，被合规追着跑；没有限流，被突发流量冲垮；没有优雅关闭，进程退出留下一堆孤儿资源。

但它们在代码里待着，你基本感觉不到它们的存在。这才是基础设施该有的样子——**在你需要的时候它就在那里，不需要的时候它也不烦你。**

---

*下一篇预告：运行时与生命周期——Agent 怎么出生、怎么死、怎么复活。我管这个叫"秽土转生"机制*