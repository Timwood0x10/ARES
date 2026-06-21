# GoAgentX Architecture Deep Dive (VI): Security and Observability — Defense in Depth and Transparent Tracing

> The more powerful an Agent gets, the more damage it can do. You give your Agent a code executor, file read/write, network requests — and then it gets tricked by a prompt injection...
> I kept asking myself one question while designing the tool system: **How do I prevent sensitive information from leaking when the Agent outputs?**
> The answer: Don't wait until the leak happens to deal with it. Start sanitizing from the very first step of log output.

---

## 1. Why Security Can't Be an Afterthought

Giving an Agent tools is like handing a teenager the car keys — they can go anywhere, but you can't guarantee they won't get into trouble.

I've seen too many AI projects crash and burn after going live: an Agent printing the user's API Key in a log, a prompt accidentally carrying a database password, an LLM response leaking a phone number... Once these problems happen, a simple "be more careful next time" won't cut it — compliance audits will be on your tail.

So when I designed GoAgentX's infrastructure, I considered security and observability together. Not "build features first, then add security", but **design them into the system from day one**.

This article covers four modules: **Security (Sanitization)**, **Observability (Tracing)**, **Rate Limiting**, and **Graceful Shutdown**. They don't directly face the user, but without them, putting your Agent into production is like running naked.

Core file list:

| Module | File Path |
|------|----------|
| Security (Sanitizer) | `internal/security/sanitizer.go` |
| Observability (Tracer) | `internal/observability/tracer.go`, `noop.go`, `log.go` |
| Rate Limiting (Limiter) | `internal/ratelimit/` (four source files) |
| Graceful Shutdown | `internal/shutdown/` (four source files) |
| Middleware Pattern | `internal/dashboard/api.go`, `internal/arena/http.go` |
| End-to-End Integration | `internal/workflow/graph/graph.go`, `.../executor.go` |
| API Layer Integration | `api/service/graph/service.go` |

---

## 2. Security Module: Regex-Based Sensitive Information Sanitization

The core design philosophy of the security module is **"field type + regex detection → targeted sanitization strategy"**, rather than simple global string replacement. It provides two layers of defense:

### 2.1 SensitiveFieldType — Using String Constants Instead of iota for Type Identification

Unlike common iota enumerations, GoAgentX uses string constants to define sensitive field types:

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

The advantage of this design: string constants are naturally serializable (can be directly referenced in JSON/YAML configs), and they make it easy to dynamically identify field types at runtime through reflection or string matching.

### 2.2 Two-Layer Detection Mechanism

The Sanitizer workflow operates in two layers:

**Layer One: Match by field name.** For structured JSON input (e.g., LLM requests/responses), the Sanitizer iterates over field names, mapping them to `SensitiveFieldType` via the `getFieldType()` method. For instance, if a field name contains "key" or "token", it's classified as APIKey.

**Layer Two: Match by regex.** For unstructured text or scenarios JSON cannot cover, the Sanitizer maintains a set of precompiled regex patterns that scan the text content comprehensively:

```go
type Sanitizer struct {
    patterns []sanitizePattern
}

type sanitizePattern struct {
    pattern *regexp.Regexp
    mask    func(string) string
}
```

Each sensitive type corresponds to a different masking strategy. Take the critical `maskAPIKey` as an example:

```go
func (s *Sanitizer) maskAPIKey(input string) string {
    if len(input) <= 8 {
        return strings.Repeat("*", len(input))
    }
    return input[:4] + strings.Repeat("*", len(input)-8) + input[len(input)-4:]
}
```

This strategy of "keeping the first and last 4 characters, replacing the middle with `*`" both conceals the sensitive content and preserves the format characteristics of the API Key, making it easier to distinguish different keys during debugging.

Other masking strategies:

- **Password**: Fully replaced with `*`
- **Email**: Preserves the domain (after `@`), with the username part showing 1 character at each end + `***`
- **Phone**: Preserves the first 3 and last 4 digits, replacing the middle with `****`
- **CreditCard**: Preserves the last 4 digits
- **SSN**: Preserves the last 4 digits

### 2.3 SafeLogger — A Safe Log Wrapper

`SafeLogger` is an elegant application-layer wrapper around Sanitizer:

```go
type SafeLogger struct {
    sanitizer *Sanitizer
    logger    func(string)
}
```

It wraps any `func(string)` logging function into an auto-sanitizing version, where all log output is automatically processed by the Sanitizer before being written. This design allows the security module to transparently integrate into existing logging systems without modifying log consumer code.

### 2.4 Package-Level Convenience Function

```go
func SanitizeLog(logger func(string), message string) {
    s := &Sanitizer{}
    s.SafeLogger(logger).Output(message)
}
```

`SanitizeLog()` is an "out-of-the-box" one-click sanitization function, suitable for scripts or simple scenarios where you don't need to construct a Sanitizer instance in advance.

### 2.5 A Lesson That Made My Blood Run Cold

The first version of the Sanitizer wasn't as complete as I'd like to admit.

Initially, I only handled structured JSON input with field name matching — API keys were masked, passwords were masked. But if the LLM response's `content` field happened to contain a phone number or email, it went straight through untouched. I assumed, naively: "LLM-generated text shouldn't contain sensitive data, right?"

Then the ops team came over and told me they'd found unredacted phone numbers in the logging system — not `138****5678`, but plain `13812345678` sitting there in the logs. When I traced it back, the root cause was clear: the first version's regex layer only matched against JSON keys, not against arbitrary text content. The phone number the user had mentioned in conversation passed through the LLM round-trip entirely in plaintext, and the logger dutifully wrote it down without a second thought.

Luckily it was caught early — the logs had only been retained in the test environment for two days. But that experience fundamentally changed how I think about sanitization: **Sensitive information can appear in any field, not just the ones you anticipate.** That's exactly why the final Sanitizer has two detection layers — when field name matching can't catch everything, full-text regex scanning has your back.

---

## 3. Observability Module: Tracer Interface and Two Implementations

GoAgentX's observability follows the classic **Observer Pattern**: defining an abstract `Tracer` interface with two implementations: `NoopTracer` and `LogTracer`.

### 3.1 Tracer Interface Definition

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

This interface covers four key observation points during Agent execution:
1. **LLM Call** (`RecordLLMCall`): Records model, prompt, response, token usage, and duration
2. **Tool Call** (`RecordToolCall`): Records tool name, input, output, and duration
3. **Agent Step** (`RecordAgentStep`): Records the execution phase of each node
4. **Error** (`RecordError`): Records error type and message

### 3.2 NoopTracer — Zero-Overhead Default Implementation

```go
type NoopTracer struct{}

var traceCounter uint64

func (t *NoopTracer) generateTraceID() string {
    id := atomic.AddUint64(&traceCounter, 1)
    return fmt.Sprintf("trace-%d", id)
}
```

`NoopTracer` is the default tracer for the Graph constructor (see `NewGraph()`). Its `Record*` methods are all empty implementations, but `generateTraceID()` uses `atomic.AddUint64` to generate auto-incrementing trace IDs — it's not a complete "zero-allocation" (since `fmt.Sprintf` does some heap allocation). The `WithTrace` method checks whether the context already has a trace ID to avoid generating a duplicate.

### 3.3 LogTracer — Structured Log Implementation Based on slog

```go
type LogTracer struct {
    logger *slog.Logger
}
```

`LogTracer` maps each Tracer event point to a structured log record. For example, `RecordLLMCall`:

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

Key design points:
- **Success/Failure branches**: Uses `ErrorContext` for failures and `InfoContext` for successes
- **Structured attributes**: All fields are passed as key-value pairs, making it easy for log collection systems (e.g., Loki, Datadog) to parse
- **`slog.Logger` injection**: LogTracer doesn't create a logger itself, but accepts one from the outside, adhering to the dependency inversion principle

---

## 4. Rate Limiting Module: Three Algorithms + Factory Pattern

The rate limiting module provides three built-in algorithms and supports extensibility through the factory pattern.

### 4.1 Limiter Interface and Factory

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

`Factory` follows a register-create pattern: use `Register(name, constructor)` to register a limiter constructor, and `Create(name, config)` to create one by name. `DefaultFactory` is a package-level global singleton, pre-registered with three built-in limiters.

### 4.2 TokenBucketLimiter — Token Bucket Algorithm

```go
type TokenBucketLimiter struct {
    rate       float64
    burst      int
    tokens     float64
    lastCheck  time.Time
    mu         sync.Mutex
}
```

The core logic lives in `Allow()`: on each call, it first calculates how many tokens should be replenished via `time.Since(lastCheck).Seconds() * rate`, then determines whether to allow the request. `Wait()` is implemented as a busy-loop + `time.After(waitTime)`, where waitTime = `float64(time.Second) / rate`. Supports `SetRate()` and `SetBurst()` for runtime dynamic parameter adjustment.

### 4.3 SlidingWindowLimiter — Sliding Window Algorithm

```go
type SlidingWindowLimiter struct {
    rate       int
    windowSize time.Duration
    requests   []time.Time
    mu         sync.Mutex
}
```

Implemented with a timestamp array: each request appends the current time to the end of the slice, and `cleanup()` removes expired requests outside the window. `Allow()` checks `len(l.requests) < l.rate`, while `Wait()` calculates the time until the oldest request expires: `waitTime = l.windowSize - time.Since(oldest)`. Supports `ResetAt(t time.Time)` for scheduled resets.

### 4.4 SemaphoreLimiter — Semaphore-Based Rate Limiting

```go
type SemaphoreLimiter struct {
    slots chan struct{}
}
```

A channel-based semaphore implementation: `Acquire()` reads from the channel, `Release()` writes back. `WeightedSemaphoreLimiter` is a weighted version using `sync.Cond` + `context.AfterFunc` for cancellation propagation, supporting weighted quota allocation by different keys (e.g., by API Key).

### 4.5 Comparison of the Three Algorithm Choices

Each of these three rate-limiting algorithms has its own applicable scenarios: the token bucket algorithm is suitable for handling burst traffic, the sliding window algorithm can precisely control the total number of requests within a time window, and the semaphore algorithm excels at limiting concurrency rather than request rate. In real production environments, the token bucket is the most common choice at the entry point of the Graph executor, because Agent workflows inherently exhibit "bursty" characteristics — multiple nodes may arrive at the ready queue simultaneously, requiring short-term burst allowance. A special note: when rate limiting fails, the caller must handle the context cancellation error returned by `Wait()`, otherwise it may lead to request accumulation and goroutine leaks.

---

## 5. Graceful Shutdown Module: Four-Phase State Machine

The graceful shutdown module is the last line of defense for system fault tolerance. Its design ensures that the process can exit cleanly under any circumstances.

### 5.1 Manager — Four-Phase Execution

```go
const (
    PhasePreShutdown Phase = iota // 0
    PhaseGraceful                 // 1
    PhaseForce                    // 2
    PhaseDone                     // 3
)
```

Execution logic for each phase:

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

Each callback executes in its own goroutine with its own timeout context. Panic recovery ensures a single callback crash doesn't affect the overall shutdown flow. The entire phase also has a 5-second hard timeout as a safety net.

### 5.2 PhaseExecutor — Retry State Machine with Exponential Backoff

```go
type PhaseExecutor struct {
    phase    Phase
    state    ExecutorState
    retry    int
    maxRetry int
    rollback func()
}
```

PhaseExecutor's state transitions: `Pending → Running → Completed / Failed`. The retry mechanism uses exponential backoff:

```go
backoff = time.Duration(1 << uint(attempt)) * time.Second
```

Note: when attempt reaches 30, `1 << uint(30)` produces an overflow of approximately 1.07 billion seconds (about 34 years). Therefore, attempt is capped at 29 in the loop (consistent with the `1<<30` division guard).

### 5.3 CallbackRegistry — Priority-Based Callback Registration

Callbacks are organized by phase via `map[Phase][]RegisteredCallback`, and executed in descending priority order using bubble sort. `CallbackChain` supports both serial chaining and parallel batch execution modes.

### 5.4 SignalHandler — Signal Listening

```go
type SignalHandler struct {
    signals []os.Signal
    ch      chan os.Signal
}
```

Integrates with the standard library `os/signal`, listening for `SIGINT`, `SIGTERM`, and `os.Interrupt`. Provides two blocking wait methods: `WaitForSignal()` and `WaitForContextOrSignal()`.

### 5.5 Full Shutdown Flow Timeline

A typical graceful shutdown flow works as follows: The system first receives a SIGINT or SIGTERM signal, and the SignalHandler forwards the signal to the Manager. The Manager enters the PhasePreShutdown phase, executing all pre-shutdown callbacks (e.g., disconnecting database connections, sending heartbeat stop signals). It then enters the PhaseGraceful phase, where each callback has its own timeout context and executes concurrently in goroutines. If all callbacks complete before the timeout, the Manager moves directly to PhaseDone; if some callbacks time out, the Manager enters the PhaseForce phase, forcefully executing remaining callbacks and recording timeout information. Finally, it enters the PhaseDone phase and the system completes its exit. The core idea of this tiered protection mechanism is "ask politely first, then enforce" — ensuring the system can exit in a predictable manner under any circumstances, without orphaned resources caused by a stuck callback.

---

## 6. Middleware Pattern: CORS and Panic Recovery

### 6.1 Dashboard's withCORS Middleware

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

Supports wildcard CORS, returning 200 directly for OPTIONS preflight requests.

### 6.2 Unified withRecovery Middleware

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

The Arena module also has its own `RecoverMiddleware`, which behaves similarly but uses its own independent `slog` instance.

### 6.3 Middleware Chain Composition

```go
func (a *APIv2) Handler() http.Handler {
    mux := http.NewServeMux()
    // ... register all routes ...
    return withRecovery(withCORS(mux))
}
```

This onion-ring style of middleware composition ensures all request paths are protected by both CORS and panic recovery.

---

## 7. End-to-End Integration: From Graph Service to LLM Client

These four modules do not exist in isolation; they work closely together in the Graph execution engine. Below is the complete call chain:

### 7.1 Injection Entry Point at the API Layer

In `api/service/graph/service.go`'s `Service.Execute()`:

```go
func (s *Service) Execute(ctx context.Context, g *wfgraph.Graph, request *ExecuteRequest) (*ExecuteResponse, error) {
    // Inject Tracer and Limiter
    if s.tracer != nil {
        g.SetTracer(s.tracer)
    }
    if s.limiter != nil {
        g.SetLimiter(s.limiter)
    }
    // Create State → Execute Graph
    result, err := g.Execute(ctx, state)
}
```

In the `Service` constructor, if `config.Tracer` is nil, it defaults to `observability.NewNoopTracer()`, ensuring observability is always enabled.

### 7.2 Observation Points in the Graph Executor

In `internal/workflow/graph/executor.go`'s `Graph.Execute()`, observation points span the entire execution flow:

**Step 1: Rate Limit Check**
```go
if g.limiter != nil {
    if err := g.limiter.Wait(ctx); err != nil {
        return nil, errors.Wrap(err, "rate limit")
    }
}
```

**Step 2: Record AgentStep Before and After Each Node Execution**
```go
g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
    TraceID:  g.tracer.GetTraceID(ctx),
    AgentID:  nodeID,
    StepName: "execute",
})
// ... execute node ...
g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
    TraceID:  g.tracer.GetTraceID(ctx),
    AgentID:  nodeID,
    StepName: "execute",
    Duration: time.Since(nodeStart),
})
```

**Step 3: Record Error on Failure**
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

**Step 4: Record ToolCall After Graph Execution Completes**
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

### 7.3 Observation Points in the LLM Client

In `internal/llm/client.go`, `recordLLMCall()` is an internal method called in both `Generate()` and `GenerateStream()`:

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

For streaming calls (`GenerateStream`), `recordLLMCall` is invoked asynchronously after the stream ends, accumulating the full response in a goroutine before recording it.

---

## 8. Architectural Observations and Best Practices

### 8.1 Constructor Panic Pattern — Fail-Fast

The Graph module's constructors directly panic when detecting invalid parameters (e.g., `NewGraph("")`, `NewGraphWithTracer("id", nil)`). The code comments explicitly state:

> "This is intentional as it indicates a programming error in the calling code. These methods are used during workflow graph initialization (startup phase), and invalid parameters represent fatal startup failures."

This reflects the "Fail-Fast" philosophy prevalent in the Go community: programming errors at startup should surface as early as possible, rather than returning an error for the caller to handle.

### 8.2 Layered Defense Design Philosophy

- **Security module** is independent of the entire execution pipeline — any log output is automatically sanitized before being written. This is the last layer of "defense in depth"
- **Observability module** is injected via interfaces into the Graph and LLM Client, serving as the infrastructure for "transparent tracing"
- **Rate limiting module** intercepts at the Graph execution entry point (the first line of the `Execute` method), without doing fine-grained per-node rate limiting
- **Graceful shutdown module** is completely independent, triggered by system signals via `SignalHandler`, decoupled from business logic

### 8.3 Coupling Analysis Between Modules

It's worth noting that although all four modules affect the Graph's execution behavior, their coupling with each other is extremely low. The security module is entirely independent, with zero code-level intrusion into the Graph or LLM Client. The observability module maintains loose coupling with the Graph and LLM Client through the Tracer interface, allowing implementation swaps via `SetTracer()` at any time. The rate limiting module also couples with the Graph through an interface, and has nothing to do with observability — rate limiting success or failure does not produce observation events. The graceful shutdown module is completely detached from the business execution path, only intervening at the end of the process lifecycle. This "each minding its own business" design allows each module to be independently tested, independently replaced, or even independently removed, greatly reducing system maintenance complexity.

### 8.4 Safe by Default vs. Explicit Configuration

- `NewGraph()` defaults to using `NoopTracer` — observability is on by default (even if it's just a noop implementation)
- `NewService()` also defaults to `NoopTracer` if `config.Tracer` is nil
- Rate limiter defaults to nil, meaning no rate limiting — requires explicit configuration
- The security module has no global default instance; the caller must construct it themselves

This "safe by default" design ensures that even without a configured tracer, the Tracer's calling code will never panic (because the default is a non-nil NoopTracer instance). This is a Go idiom worth learning from — for optional interface dependencies, provide a non-nil default implementation (noop), which both avoids nil checks before every use and preserves the ability to swap in a real implementation later. In contrast, the rate limiting and security modules have no default implementation because they are "opt-in": not every deployment environment needs rate limiting, nor does every log entry need sanitization. Letting the caller choose on demand is a better design.

### 8.5 The "Reverse" Application of Security and Observability in Arena

Interestingly, the Arena module (chaos engineering testing framework) simultaneously uses patterns from both the security and observability modules, but for entirely opposite purposes: the Tracer interface from observability is used by Arena to record fault injection events and recovery processes, helping analyze the system's fault tolerance behavior; while the middleware pattern from the security module (RecoverMiddleware) is used by Arena to catch expected panics that may occur during fault injection, ensuring that Arena's core loop is not affected by the crash of a single Agent. All of Arena's 28 route handler functions are protected by RecoverMiddleware, demonstrating a unique synergy between "security" and "observability" in the context of chaos engineering: observability lets you see the failures, security lets you survive them.

---

## 9. Summary

Four modules, each with its own job:

- **Security module**: Dual detection via regex + field names, automatic sanitization before log output
- **Observability module**: Tracer interface decouples "what to record" from "how to record"
- **Rate limiting module**: Factory pattern, three algorithms you can swap at will
- **Graceful shutdown module**: Four-phase state machine + exponential backoff, clean exit under any circumstances

To be honest, these four modules aren't the "coolest" parts — they're the "least cool but absolutely necessary" parts. No sanitization? Compliance will be on your tail. No rate limiting? Burst traffic will take you down. No graceful shutdown? Process exit leaves a trail of orphaned resources.

But when they're in the codebase, you barely notice their existence. That's exactly what infrastructure should be — **there when you need it, out of your way when you don't.**

---

*Next up: Runtime and Lifecycle — How Agents are born, die, and come back. I call it the "Impure World Reincarnation" mechanism.*