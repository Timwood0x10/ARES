# Ares Runtime Module Performance Analysis

## 1. Module Overview

The ares_runtime module provides lifecycle management for agents -- registration, start, stop, restart, and resurrection. It implements a health-check monitoring loop with exponential backoff retry for agent recovery, event-sourced state restoration, and cognitive recovery via memory manager integration.

### Key Files

| File | Purpose |
|------|---------|
| `internal/ares_runtime/runtime.go` | Runtime interface, Config, AgentFactory, error sentinels |
| `internal/ares_runtime/manager.go` | Full Manager implementation: lifecycle, health checks, recovery, event replay |
| `internal/ares_runtime/recovery.go` | Snapshot-first recovery utility with event fallback |

### Architecture

The `Manager` owns the full agent lifecycle:

1. **Registration**: Agents and their factories are registered via `RegisterAgent`.
2. **Start**: `Start()` launches all registered agents in managed goroutines via `errgroup`, starts a health check ticker.
3. **Monitoring**: `healthCheck()` runs periodically, checking agent liveness via `Heartbeater` interface or status-based fallback.
4. **Resurrection**: `NotifyAgentDead` triggers async restoration with exponential backoff (1s, 2s, 4s, capped at 30s, max 5 attempts).
5. **Recovery**: `recoverAgentState` attempts snapshot-first recovery, falls back to event replay, and enriches with cognitive state from memory manager.

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Fix |
|----------|----------|---------|-----|
| HIGH | `manager.go:440-527` | `NotifyAgentDead` holds `m.mu` (write lock) across the entire resurrection decision logic (lines 442-516), including the goroutine launch at line 475. This blocks ALL agent operations (Start, Stop, Restart, Stats, ListAgents, GetAgent) during the resurrection setup | Release the lock before launching the goroutine; capture needed values under lock, then launch outside |
| HIGH | `manager.go:530-589` | `Start()` holds `m.mu.Lock()` from line 536 to line 550 (14 lines), then immediately re-acquires `m.mu.RLock()` at line 558. The gap between unlock and re-lock creates a window where `RegisterAgent` or `StopAgent` could modify `m.agents` | Hold a single lock for the entire start sequence or use a state machine |
| HIGH | `manager.go:558-571` | Agent launch loop iterates `m.agents` under `RLock` and calls `launchAgentGoroutine` which calls `m.g.Go()`. If `m.g.Go()` blocks (e.g., errgroup limit reached), the read lock is held indefinitely, blocking all writes | Collect agent info under lock, release lock, then launch goroutines |
| MEDIUM | `manager.go:703-756` | `replayEvents` loads up to `MaxReplayEvents` (default 10000) events into memory, then runs `VerifyStreamIntegrity` which iterates all events. For agents with long histories, this is a significant memory and CPU spike during resurrection | Stream events instead of loading all at once; verify incrementally |
| MEDIUM | `manager.go:845-893` | `healthCheck` creates a `[]agentCheck` slice on every tick by iterating all agents under RLock. For systems with many agents, this creates GC pressure from the repeated allocations | Reuse a pre-allocated slice or use a persistent agent registry |
| MEDIUM | `manager.go:232-295` | `RestartAgent` performs stop and start sequentially. The stop operation (line 262-266) blocks on `AgentStopTimeout` (default 10s) before the new agent can start | Pipeline stop and start: cancel old context, start new agent, then wait for old agent to drain |
| LOW | `manager.go:486-509` | Exponential backoff in resurrection uses `time.After` which creates a new timer on each iteration. Under high churn, this creates many short-lived timer goroutines | Use `time.NewTimer` with `Reset()` for reuse |
| LOW | `manager.go:674-697` | `Stats()` iterates all agents under RLock to count active ones. This is called frequently by dashboard/monitoring | Maintain an atomic counter for active agents, updated on start/stop |

---

## 3. Code Quality Issues

| Severity | Location | Problem | Recommendation |
|----------|----------|---------|----------------|
| HIGH | `manager.go:440-527` | `NotifyAgentDead` is a complex method that mixes lock management, resurrection logic, goroutine launch, and event emission. The lock is acquired at line 442, held through the goroutine closure definition, and released at line 516 -- making it very difficult to reason about lock ordering | Split into: `shouldResurrect()` (under lock), `scheduleResurrection()` (no lock), `emitDeathEvent()` (no lock) |
| MEDIUM | `manager.go:413-436` | `launchAgentGoroutine` calls `m.NotifyAgentDead` from within a goroutine launched by `m.g.Go()`. Since `NotifyAgentDead` acquires `m.mu` and may call `m.g.Go()`, this creates a potential deadlock if the errgroup is full | Use a separate goroutine for resurrection scheduling, not the errgroup |
| MEDIUM | `manager.go:592-671` | `Stop()` creates a new `errgroup` at line 642 for concurrent agent stopping, separate from `m.g`. This means the runtime has two errgroup systems that can interfere | Unify on a single errgroup or document the separation |
| MEDIUM | `runtime.go:32-42` | `RuntimeStats.BackgroundTasks` exposes internal `ctxutil` implementation details. Consumers of the API should not need to know about background task labels | Abstract into a simpler health indicator |
| LOW | `manager.go:760-774` | `buildStateFromEvents` only extracts `session_id` from `EventSessionCreated` events. All other event types are ignored, making the function name misleading | Either extract more state or rename to `extractSessionID` |
| LOW | `recovery.go:25-42` | `RecoverSnapshotOrEvents` is a standalone function that takes a `SnapshotStore` and an `eventFn` callback. The pattern is useful but the function is not testable in isolation since it directly calls `store.Load` | Accept an interface rather than the concrete store |

---

## 4. Code Snippets: Problems and Proposed Fixes

### Problem 1: NotifyAgentDead holds write lock during goroutine launch

**`manager.go:440-527`**
```go
func (m *Manager) NotifyAgentDead(agentID string, reason string) {
    m.mu.Lock()
    // ... 75 lines of logic under lock ...
    m.g.Go(func() error {  // Line 475: goroutine launched under lock
        // ... resurrection logic ...
    })
    m.mu.Unlock()  // Line 516: lock released after goroutine launch
}
```

**Proposed fix:** Separate lock-held decision from lock-free action.
```go
func (m *Manager) NotifyAgentDead(agentID string, reason string) {
    var shouldRestore bool
    var factory AgentFactory

    m.mu.Lock()
    // Decision logic only
    factory, shouldRestore = m.prepareResurrection(agentID, reason)
    m.mu.Unlock()

    if !shouldRestore {
        return
    }

    // Lock-free goroutine launch
    m.scheduleResurrection(agentID, factory)
    m.emitDeathEvent(agentID, reason)
}
```

### Problem 2: Agent launch holds RLock during goroutine creation

**`manager.go:558-571`**
```go
m.mu.RLock()
for id, ma := range m.agents {
    if ma.agent != nil {
        agentCtx, agentCancel := context.WithCancel(m.gctx)
        ma.cancel = agentCancel
        // ... launchAgentGoroutine may block on errgroup ...
        m.launchAgentGoroutine(agentCtx, currentID, currentAgent)
    }
}
m.mu.RUnlock()
```

**Proposed fix:** Snapshot agents under lock, launch outside lock.
```go
type launchInfo struct {
    id    string
    agent base.Agent
}

m.mu.RLock()
toLaunch := make([]launchInfo, 0, len(m.agents))
for id, ma := range m.agents {
    if ma.agent != nil {
        agentCtx, agentCancel := context.WithCancel(m.gctx)
        ma.cancel = agentCancel
        toLaunch = append(toLaunch, launchInfo{id, ma.agent})
    }
}
m.mu.RUnlock()

for _, info := range toLaunch {
    m.launchAgentGoroutine(agentCtx, info.id, info.agent)
}
```

### Problem 3: Timer leak in backoff loop

**`manager.go:486-509`**
```go
for attempt := 1; attempt <= maxAttempts; attempt++ {
    // ...
    select {
    case <-m.gctx.Done():
        return nil
    case <-time.After(backoff):  // Creates new timer each iteration
    }
    backoff *= 2
}
```

**Proposed fix:** Reuse a single timer.
```go
timer := time.NewTimer(backoff)
defer timer.Stop()
for attempt := 1; attempt <= maxAttempts; attempt++ {
    // ...
    timer.Reset(backoff)
    select {
    case <-m.gctx.Done():
        return nil
    case <-timer.C:
    }
    backoff *= 2
}
```

---

## 5. Priority Action Items

1. **[✓] [P0 - Correctness/Performance]** Refactor `NotifyAgentDead` to release the write lock before launching the resurrection goroutine. Decision (send on `resurrectCh`) is made under lock; goroutine launch happens outside.

2. **[P0 - Performance]** Fix the agent launch loop in `Start()` to release the read lock before calling `launchAgentGoroutine`. Holding RLock during goroutine creation blocks concurrent agent registration.

3. **[P1 - Performance]** Stream event replay instead of loading all events into memory. For agents with 10K+ events, the current approach causes significant memory spikes during resurrection.

4. **[P1 - Code Quality]** Split `NotifyAgentDead` into smaller, focused methods (`prepareResurrection`, `scheduleResurrection`, `emitDeathEvent`) for testability and clarity.

5. **[✓] [P2 - Performance]** Fix timer leak in resurrection backoff loop by using `time.NewTimer` with `Reset()` instead of `time.After`.

6. **[P2 - Performance]** Maintain an atomic active-agent counter to avoid iterating all agents in `Stats()`.

7. **[P3 - Code Quality]** Rename `buildStateFromEvents` to `extractSessionID` to accurately reflect its behavior.
