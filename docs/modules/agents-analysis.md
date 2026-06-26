# Agents Module -- Performance & Code Quality Analysis

## 1. Module Overview

The agents module implements a leader/sub-agent architecture for task
orchestration. The leader agent parses user profiles, plans tasks, dispatches
them to sub-agents, aggregates results, and manages memory (sessions, messages,
distillation). Sub-agents execute individual tasks. Both support stateful
resurrection via snapshot + event replay.

### Key Files

| File | Purpose |
|------|---------|
| `internal/agents/leader/agent.go` | Leader agent: lifecycle, Process/ProcessStream, memory management |
| `internal/agents/leader/planner.go` | Task planner: trigger matching, task creation, replanning |
| `internal/agents/leader/aggregator.go` | Result aggregation: deduplication, sorting, scoring |
| `internal/agents/leader/evaluator.go` | Quality evaluator for iterative refinement |
| `internal/agents/leader/checkpoint.go` | PostgreSQL-backed checkpoint persistence |
| `internal/agents/sub/agent.go` | Sub-agent: task execution, streaming, tool binding |
| `internal/agents/base/agent.go` | Base interfaces: Agent, StatefulAgent, Messenger, Heartbeater |

---

## 2. Performance Bottlenecks

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `leader/agent.go:424-539` | `initMemoryContext` makes 4-5 sequential DB/network calls: `GetLatest` (checkpoint), `CreateSession`, `AddMessage`, `BuildContext`, `SearchSimilarTasks`, `CreateTask`. Each call has network latency. Total latency = sum of all call latencies (could be 200-500ms). | Parallelize independent calls: `BuildContext` and `SearchSimilarTasks` can run concurrently after `CreateSession` completes. Use `errgroup` for the independent subset. |
| 2 | **HIGH** | `leader/agent.go:505-518` | `SearchSimilarTasks` runs a vector similarity search on **every request** with no caching. For repeated or similar inputs, this is redundant expensive work. | Cache recent similarity results with a short TTL (e.g., 30s) keyed by input hash. Or use a write-through cache that invalidates on new task creation. |
| 3 | **HIGH** | `leader/agent.go:718-940` | `Process` acquires `processingMu.Lock()` which serializes **all** Process and ProcessStream calls. Only one request can be processed at a time per agent. This is a hard concurrency bottleneck. | If the intent is to prevent concurrent state mutation, use a more granular lock (e.g., lock only the state-transition sections, not the entire pipeline). Or use a channel-based work queue with a single worker goroutine. |
| 4 | **MEDIUM** | `leader/planner.go:28-34` | `getRandomSuffix` calls `crypto/rand.Int(rand.Reader, big.NewInt(100000000))` for every task ID. `crypto/rand` is cryptographically secure but slow (~1us per call). Task IDs don't need cryptographic security. | Use `math/rand` (seeded once) or an atomic counter + timestamp combination. |
| 5 | **MEDIUM** | `leader/planner.go:37-41` | `generateTaskID` combines `time.Now().Format()`, an atomic counter, **and** a crypto/rand suffix. The crypto/rand part is redundant given the atomic counter already guarantees uniqueness within a process. | Remove `getRandomSuffix()` call; `taskIDCounter` + timestamp is sufficient. |
| 6 | **MEDIUM** | `leader/planner.go:250-276` | `matchWordBoundary` converts both `text` and `keyword` to `[]rune` on every call. For the `Plan` method, `text` is the full input string, and this is called once per trigger per sub-agent. With 10 sub-agents and 5 triggers each, that's 50 `[]rune` conversions of the same input. | Convert `text` to runes once outside the loop; pass `[]rune` to `matchWordBoundary`. |
| 7 | **MEDIUM** | `leader/agent.go:630-672` | `finalizeMemory` creates a nested `errgroup.Group` inside the distillation goroutine for a single `DistillTask` + `StoreDistilledTask` call. The errgroup adds goroutine overhead for what is effectively a sequential two-step operation. | Call `DistillTask` then `StoreDistilledTask` sequentially without errgroup. The outer goroutine already provides async execution. |
| 8 | **MEDIUM** | `leader/agent.go:636` | `context.WithTimeout(context.Background(), DefaultDistillTimeout)` creates a detached context. If `DefaultDistillTimeout` is large (e.g., 5 minutes), distillation goroutines can accumulate and hold resources long after the request completes. | Use a shorter default (e.g., 30s) or make it configurable. Add a gauge metric for active distillation goroutines. |
| 9 | **MEDIUM** | `leader/checkpoint.go:60-68` | `Save` uses `INSERT ... ON CONFLICT (leader_id) DO UPDATE`. This is correct but runs on every `initMemoryContext` call when a session is created. The upsert acquires a row-level lock. | Only save checkpoints when state actually changes (e.g., new session ID), not on every call. |
| 10 | **MEDIUM** | `sub/agent.go:205-210` | `Process` calls `a.Status()` twice without holding the lock between checks. Between the two calls, another goroutine could change the status, causing a TOCTOU race. | Read status once under `a.mu.RLock()` and use the cached value for both checks. |
| 11 | **LOW** | `leader/planner.go:143-217` | `Plan` calls `strings.ToLower(inputText)` and then `strings.ToLower(trigger)` for every trigger comparison. The lowercased input is reused (good), but each trigger is lowered per comparison. | Pre-lowercase all triggers at planner construction time. |
| 12 | **LOW** | `leader/aggregator.go:57-90` | `Aggregate` builds `priorityMap` by iterating tasks, then iterates results. For each item, it does a map lookup. This is O(T + R*I) which is fine, but the `slog.Warn` on line 80 fires for every item whose task is not in the map, creating log noise. | Remove the warn or downgrade to debug; missing priorities are expected when `sortBy != "priority"`. |
| 13 | **LOW** | `leader/agent.go:800-803` | `emitEvent(ctx, events.EventTaskCreated, map[string]any{"step": "parse"})` emits a `TaskCreated` event for the parse step. This is semantically incorrect -- parsing is not task creation. | Use a more appropriate event type or a generic `EventStepStarted` type. |

---

## 3. Code Quality Issues

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `sub/agent.go:205-210` | TOCTOU race: `Process` reads status twice via `a.Status()` (which acquires RLock each time). Between the two reads, status could change. If status transitions from `Offline` to `Ready` between the two calls, the agent starts twice. | Read status once: `status := a.Status(); if status == ...`. |
| 2 | **HIGH** | `leader/agent.go:718-737` | `Process` auto-starts the agent if offline (`a.Start(ctx)`) but does not check for concurrent auto-starts. Two concurrent `Process` calls could both see `Offline`, both call `Start`, and the second would fail with `ErrAgentAlreadyStarted`. | Use `sync.Once` for auto-start, or check status atomically with a CAS loop. |
| 3 | **MEDIUM** | `leader/agent.go:542-546` | `emitEvent` silently drops events if `eventStore` is nil. This is intentional but makes debugging difficult when events are expected but not appearing. | Log at debug level when event store is nil. |
| 4 | **MEDIUM** | `leader/agent.go:619-629` | The `distillMu` + `stopCh` check + `distillWg.Add(1)` sequence is correct but complex. The comment explains the reasoning well, but the pattern is error-prone for future maintainers. | Extract into a helper: `func (a *leaderAgent) tryAddDistill() bool`. |
| 5 | **MEDIUM** | `leader/planner.go:135-218` | `Plan` has three code paths: (1) sub-agents with triggers, (2) sub-agents without triggers, (3) no sub-agents. Paths 1 and 2 share logic but are duplicated. | Extract trigger matching into a helper; unify the task creation loop. |
| 6 | **MEDIUM** | `leader/agent.go:990-1026` | `RestoreState` silently skips invalid fields with no logging. If a field has an unexpected type, the agent silently operates with stale state. | Log warnings for unexpected types or missing fields. |
| 7 | **MEDIUM** | `leader/agent.go:1045-1095` | `ReplayEvents` does not validate event ordering. If events arrive out of order (e.g., `TaskCompleted` before `TaskCreated`), the state reconstruction will be incorrect. | Add a monotonic sequence number check or document the ordering requirement. |
| 8 | **MEDIUM** | `sub/agent.go:381-416` | `ReplayEvents` for sub-agents only logs task completions but does not update any state. The implementation is a no-op beyond logging. | Either implement meaningful state reconstruction or document that sub-agents are stateless by design. |
| 9 | **LOW** | `leader/agent.go:409-420` | `parseInput` accepts `string`, `[]byte`, and `fmt.Stringer`. The `[]byte` case converts to string via `string(v)`, which copies. For large inputs, this is an unnecessary allocation. | Use `unsafe.String` (Go 1.20+) for zero-copy conversion, or document the copy as intentional for safety. |
| 10 | **LOW** | `leader/evaluator.go:32-61` | `DefaultEvaluator.Evaluate` uses hardcoded magic numbers (0.3, 0.4, 0.5) for scoring. No way to tune thresholds without code changes. | Make thresholds configurable via `EvaluatorConfig`. |
| 11 | **LOW** | `leader/checkpoint.go:84-88` | `GetLatest` queries with `ORDER BY updated_at DESC LIMIT 1` but the table has a unique constraint on `leader_id` (from the upsert in `Save`). The `ORDER BY` and `LIMIT` are unnecessary. | Simplify to `SELECT ... WHERE leader_id = $1`. |
| 12 | **LOW** | `base/agent.go:124-132` | `DefaultConfig` uses a global `agentIDSeq` atomic counter. Agent IDs are `agent-1`, `agent-2`, etc. These are not unique across process restarts. | Include a process-unique identifier (e.g., hostname + PID, or UUID prefix). |

---

## 4. Code Snippets: Problems and Fixes

### 4.1 Sequential DB Calls in initMemoryContext (leader/agent.go:424-539)

**Problem:**
```go
func (a *leaderAgent) initMemoryContext(ctx context.Context, strInput string) (...) {
    // 1. Checkpoint lookup (DB call)
    cp, err := checkpoint.GetLatest(ctx, leaderID)
    // 2. Create session (DB call)
    newSessionID, err := a.memoryManager.CreateSession(ctx, a.getUserID())
    // 3. Save checkpoint (DB call)
    checkpoint.Save(ctx, &LeaderCheckpoint{...})
    // 4. Add message (DB call)
    a.memoryManager.AddMessage(ctx, sessionID, "user", strInput)
    // 5. Build context (DB/network call)
    inputWithContext, err := a.memoryManager.BuildContext(ctx, strInput, sessionID)
    // 6. Search similar tasks (DB/vector call)
    similarTasks, err := a.memoryManager.SearchSimilarTasks(ctx, enrichedInput, ...)
    // 7. Create task (DB call)
    taskID, err := a.memoryManager.CreateTask(ctx, sessionID, ...)
}
```

Steps 1-3 are sequential dependencies. Steps 5 and 6 are independent of each other (both depend on sessionID from step 2). Step 7 depends on step 5's enriched input.

**Fix:**
```go
func (a *leaderAgent) initMemoryContext(ctx context.Context, strInput string) (...) {
    // Sequential: checkpoint recovery + session creation
    sessionID = a.ensureSession(ctx, leaderID, checkpoint)

    // Parallel: add message + build context + search similar tasks
    var enrichedInput string
    var similarTasks []events.Event
    g, gCtx := errgroup.WithContext(ctx)

    g.Go(func() error {
        return a.memoryManager.AddMessage(gCtx, sessionID, "user", strInput)
    })

    g.Go(func() error {
        input, err := a.memoryManager.BuildContext(gCtx, strInput, sessionID)
        if err == nil {
            enrichedInput = input
        }
        return err
    })

    g.Go(func() error {
        tasks, err := a.memoryManager.SearchSimilarTasks(gCtx, strInput, DefaultSimilarTasksLimit)
        if err == nil {
            similarTasks = tasks
        }
        return err // non-fatal: log and continue
    })

    _ = g.Wait() // collect errors, log non-fatal ones

    // Sequential: create task (depends on enrichedInput)
    if enrichedInput == "" {
        enrichedInput = strInput
    }
    taskID, _ := a.memoryManager.CreateTask(ctx, sessionID, a.getUserID(), enrichedInput)
    // ...
}
```

### 4.2 TOCTOU Race in subAgent.Process (sub/agent.go:205-210)

**Problem:**
```go
func (a *subAgent) Process(ctx context.Context, input any) (any, error) {
    if a.Status() != models.AgentStatusReady && a.Status() != models.AgentStatusOffline {
        return nil, errors.ErrAgentNotReady
    }

    if a.Status() == models.AgentStatusOffline {  // second read -- could be different!
        if err := a.Start(ctx); err != nil {
            return nil, err
        }
    }
    // ...
}
```

Between the two `a.Status()` calls, another goroutine could call `Start()`, changing status from `Offline` to `Ready`. The second `Status()` then returns `Ready`, skipping the auto-start. This is benign in this case, but the pattern is dangerous.

**Fix:**
```go
func (a *subAgent) Process(ctx context.Context, input any) (any, error) {
    a.mu.RLock()
    status := a.status
    a.mu.RUnlock()

    if status != models.AgentStatusReady && status != models.AgentStatusOffline {
        return nil, errors.ErrAgentNotReady
    }

    if status == models.AgentStatusOffline {
        if err := a.Start(ctx); err != nil {
            return nil, err
        }
    }
    // ...
}
```

### 4.3 Redundant []rune Conversion in matchWordBoundary (leader/planner.go:250-276)

**Problem:**
```go
func matchWordBoundary(text, keyword string) bool {
    runes := []rune(text)       // allocates every call
    kwRunes := []rune(keyword)  // allocates every call
    // ...
}
```

Called from `Plan` in a loop:
```go
for _, trigger := range sa.Triggers {
    if matchWordBoundary(lowerInput, strings.ToLower(trigger)) {  // lowerInput re-converted each time
        // ...
    }
}
```

**Fix:**
```go
// In Plan, convert once:
textRunes := []rune(lowerInput)

// Updated signature:
func matchWordBoundary(textRunes []rune, keyword string) bool {
    if keyword == "" {
        return false
    }
    kwRunes := []rune(keyword)
    kwLen := len(kwRunes)
    for i := 0; i <= len(textRunes)-kwLen; i++ {
        match := true
        for j := 0; j < kwLen; j++ {
            if textRunes[i+j] != kwRunes[j] {
                match = false
                break
            }
        }
        if !match {
            continue
        }
        before := i == 0 || !isAlphaNum(textRunes[i-1])
        after := i+kwLen >= len(textRunes) || !isAlphaNum(textRunes[i+kwLen])
        if before && after {
            return true
        }
    }
    return false
}
```

### 4.4 Unnecessary errgroup for Single Task (leader/agent.go:637-648)

**Problem:**
```go
g, gCtx := errgroup.WithContext(distillCtx)
g.Go(func() error {
    distilled, err := a.memoryManager.DistillTask(gCtx, taskID)
    if err != nil {
        return err
    }
    return a.memoryManager.StoreDistilledTask(gCtx, taskID, distilled)
})
if err := g.Wait(); err != nil {
    // ...
}
```

This creates an errgroup for a single sequential operation. The errgroup adds goroutine scheduling overhead with no concurrency benefit.

**Fix:**
```go
distilled, err := a.memoryManager.DistillTask(distillCtx, taskID)
if err != nil {
    slog.Warn("Failed to distill task", "error", err, "task_id", taskID)
    return nil
}
if err := a.memoryManager.StoreDistilledTask(distillCtx, taskID, distilled); err != nil {
    slog.Error("Failed to store distilled task", "error", err, "task_id", taskID)
    return nil
}
```

### 4.5 Serialized Process Calls (leader/agent.go:718-722)

**Problem:**
```go
func (a *leaderAgent) Process(ctx context.Context, input any) (any, error) {
    a.processingMu.Lock()
    defer a.processingMu.Unlock()
    // ... entire pipeline runs under this lock
}
```

If `Process` takes 5 seconds (LLM calls, DB calls), a second request arriving 1 second later will block for 4 seconds before even starting.

**Fix (if concurrent processing is desired):**
```go
// Option A: Channel-based worker
type leaderAgent struct {
    // ...
    workCh chan processRequest  // buffered channel
}

func (a *leaderAgent) Start(ctx context.Context) error {
    // ...
    a.workCh = make(chan processRequest, 10)
    go a.processLoop() // single worker goroutine
    return nil
}

// Option B: Keep serialization but add queue depth metric
func (a *leaderAgent) Process(ctx context.Context, input any) (any, error) {
    // ... existing code ...
    // Add metric: processing_queue_depth
}
```

### 4.6 Unnecessary ORDER BY in GetLatest (leader/checkpoint.go:84-88)

**Problem:**
```go
query := `SELECT leader_id, session_id, status, metadata, updated_at
    FROM leader_checkpoints
    WHERE leader_id = $1
    ORDER BY updated_at DESC
    LIMIT 1`
```

The `INSERT ... ON CONFLICT (leader_id) DO UPDATE` in `Save` ensures at most one row per `leader_id`. The `ORDER BY` and `LIMIT` are unnecessary overhead.

**Fix:**
```go
query := `SELECT leader_id, session_id, status, metadata, updated_at
    FROM leader_checkpoints
    WHERE leader_id = $1`
```

---

## 5. Priority Action Items

### P0 -- Must Fix

1. **Fix TOCTOU race in `subAgent.Process`** (`sub/agent.go:205-210`). Read status once under lock. This is a correctness bug, not just a performance issue.

2. **Parallelize `initMemoryContext` DB calls** (`leader/agent.go:424-539`). Use `errgroup` for the independent subset (AddMessage, BuildContext, SearchSimilarTasks). This can cut per-request latency by 200-400ms.

3. **Fix double auto-start race in `leader.Process`** (`leader/agent.go:718-737`). Two concurrent calls could both see `Offline` and both call `Start`. Use `sync.Once` or atomic CAS.

### P1 -- Should Fix

4. **Cache `SearchSimilarTasks` results** (`leader/agent.go:505`). A short-TTL cache (30s) keyed by input hash eliminates redundant vector searches for repeated inputs.

5. **Remove redundant `errgroup` in distillation** (`leader/agent.go:637-648`). Replace with direct sequential calls.

6. **Pre-convert `text` to runes once** in `Plan` (`leader/planner.go:143-217`) and pass `[]rune` to `matchWordBoundary`. Eliminates N redundant allocations per plan call.

7. **Remove `crypto/rand` from task ID generation** (`leader/planner.go:28-34`). An atomic counter + timestamp is sufficient and ~10x faster.

8. **Simplify `GetLatest` query** (`leader/checkpoint.go:84-88`). Remove unnecessary `ORDER BY` and `LIMIT`.

### P2 -- Nice to Have

9. **Add concurrency control** to `leader.Process` beyond full serialization. Consider a work queue with bounded concurrency.

10. **Pre-lowercase triggers** at planner construction time instead of per-comparison.

11. **Log warnings in `RestoreState`** for unexpected field types.

12. **Make evaluator thresholds configurable** in `DefaultEvaluator`.

13. **Add meaningful `ReplayEvents` implementation** for sub-agents or document them as stateless.

14. **Use event type `EventStepStarted`** instead of `EventTaskCreated` for parse/plan/dispatch steps in `leader.Process`.

15. **Include process-unique prefix in agent IDs** (`base/agent.go:124-132`) to avoid ID collisions across restarts.
