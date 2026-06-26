# Dashboard Module -- Performance & Code Quality Analysis

## 1. Module Overview

The dashboard module provides a real-time web UI for observing, creating, and
interacting with agents. It exposes an HTTP/JSON API, WebSocket-based live
updates, SSE streams, arena chaos-injection endpoints, and flight-recorder
telemetry.

### Key Files

| File | Purpose |
|------|---------|
| `internal/dashboard/api.go` | HTTP router, all REST/WS/SSE handlers |
| `internal/dashboard/ws_hub.go` | WebSocket hub: client lifecycle, pub/sub channels |
| `internal/dashboard/orchestrator.go` | Agent creation, execution, LLM integration, resurrection |
| `internal/dashboard/event_bridge.go` | Forwards events from EventStore to WS hub |

---

## 2. Performance Bottlenecks

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `ws_hub.go:172-183` | `sendToClient` calls `json.Marshal(msg)` once per client per broadcast. For N clients receiving the same message, this is O(N) redundant serializations. | Marshal once in the broadcast loop before iterating clients, pass `[]byte` to each send. |
| 2 | **HIGH** | `orchestrator.go:455` | `rawData += fmt.Sprintf(...)` accumulates multi-step MCP results via repeated string concatenation. Each step allocates a new string copy. | Use `strings.Builder` and pre-estimate capacity from step count. |
| 3 | **HIGH** | `orchestrator.go:495` | `emitEvent(id, "mcp.data.gathered", map[string]any{"bytes": len(rawData), "data": rawData})` stores the **entire raw MCP payload** into the event store. With large tool outputs (100 KB+), this creates unbounded memory pressure and inflates the event store. | Store only the byte length and a hash; load raw data from the tool result directly when needed. |
| 4 | **MEDIUM** | `api.go:309-316` | `handleWS` creates a `newUpgrader()` on every WebSocket connection. Each call allocates a fresh `websocket.Upgrader` and its `CheckOrigin` closure. | Create the upgrader once as a field on `APIv2`; reuse across requests. |
| 5 | **MEDIUM** | `api.go:296-305` | `handleMCPByName` calls `a.mcp.ListServers()` and performs a linear scan to find a server by name. O(N) per lookup. | Build a `map[string]MCPToolInfo` index once when servers are loaded, or provide a `GetServer(name)` method on the provider. |
| 6 | **MEDIUM** | `api.go:676-693` | `handleArenaStream` sends history events with `time.Sleep(100 * time.Millisecond)` between each. This artificially rate-limits the SSE stream and keeps the HTTP handler goroutine alive for `N * 100ms`. | Remove the sleep; send events as fast as the client can consume. If throttling is desired, use a configurable ticker. |
| 7 | **MEDIUM** | `orchestrator.go:780-826` | `loadResumeProgress` reads up to 10,000 events from the event store to count `mcp.step.completed` events. This scans the entire event stream for a single agent on every resurrection. | Store step completion count in a dedicated state field or use a targeted query with `event_type` filter. |
| 8 | **MEDIUM** | `orchestrator.go:830-857` | `loadPreviousData` similarly scans up to 10,000 events to find the largest `mcp.data.gathered` payload. Redundant with the event stored in #3. | Store the raw data pointer/hash at task creation; load it directly. |
| 9 | **MEDIUM** | `ws_hub.go:64-79` | The broadcast case in `Run()` holds `h.mu.RLock()` while iterating all clients and calling `sendToClient`. While `sendToClient` is non-blocking (buffered channel), the lock is held for the entire iteration, blocking `Register`/`Unregister` writes. | Snapshot the client list under RLock, release the lock, then iterate the snapshot. |
| 10 | **LOW** | `api.go:628-635` | `handleArenaMetrics` iterates the full arena history to compute `failoverCount` and `recoveryTimes`. O(H) per metrics request. | Maintain running counters (total, successful, sum of durations) in the arena provider; expose them via `Stats()`. |
| 11 | **LOW** | `orchestrator.go:356-365` | `ListAgents` returns all agents without pagination. Unbounded response size. | Add `limit` and `offset` query parameters; return total count in a wrapper. |
| 12 | **LOW** | `event_bridge.go:56-68` | `forwardLoop` is a tight select loop with no batching. Under high event throughput, each event triggers a separate WS broadcast. | Batch events (e.g., flush every 50ms or every 50 events) into a single broadcast. |

---

## 3. Code Quality Issues

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `api.go:909-914` | `CheckOrigin` always returns `true`. Any website can open a WebSocket connection to the dashboard, enabling cross-site WebSocket hijacking. | Validate the `Origin` header against an allowlist or the server's own host. |
| 2 | **MEDIUM** | `orchestrator.go:540-548` | Result mutation under `o.mu.Lock()` directly writes to `result.Status`, `result.Progress`, etc. The `result` pointer is shared between the goroutine and the `agents` map. If `GetAgent` reads concurrently, it may see a partially-updated struct. | Build a new `AgentResult` value, write all fields, then assign atomically via `o.agents[id] = &newResult`. |
| 3 | **MEDIUM** | `orchestrator.go:216-329` | `CreateAgent` acquires `o.mu.RLock` (line 219), releases it, then acquires `o.mu.Lock` (line 266). Between these two critical sections, another goroutine could modify `o.agents` or `o.cancels`, causing the resurrection counter read to be stale. | Hold a single lock across template resolution and agent registration. |
| 4 | **MEDIUM** | `ws_hub.go:139-166` | `removeClient` acquires `client.mu` while already holding `h.mu`. The comment says lock ordering is `client.mu` then `h.mu`, but this code does `h.mu` then `client.mu` -- the opposite order. This violates the documented invariant and risks deadlock if `Subscribe`/`Unsubscribe` races with `removeClient`. | Remove the `client.mu` acquisition in `removeClient`; the channel copy can be done safely because `h.mu` (write) prevents concurrent `Subscribe`/`Unsubscribe`. |
| 5 | **MEDIUM** | `orchestrator.go:690-698` | `emitEvent` uses `context.Background()` instead of the request context. If the event store is slow, this blocks the caller indefinitely with no cancellation path. | Pass the request `ctx` through or use a short timeout context. |
| 6 | **LOW** | `api.go:197-206` | `listAgents` filters by status after retrieving the full list. If there are thousands of agents, this allocates a full slice then a filtered slice. | Push the filter into `Orchestrator.ListAgents(status string)` for server-side filtering. |
| 7 | **LOW** | `orchestrator.go:872-878` | `truncateStr` converts the string to `[]rune` to check length, which allocates a full copy even when truncation is not needed. | Use `utf8.RuneCountInString(s)` to check length without allocation; only convert to runes if truncation is needed. |
| 8 | **LOW** | `event_bridge.go:109-120` | `extractExecutionID` tries three hardcoded payload keys. Fragile and not extensible. | Define a constant slice or use a struct tag on the event payload. |

---

## 4. Code Snippets: Problems and Fixes

### 4.1 Redundant JSON Marshal per Client (ws_hub.go:172-183)

**Problem:**
```go
func (h *WSHub) sendToClient(client *WSClient, msg *WSMessage) {
    data, err := json.Marshal(msg)  // called once PER CLIENT
    if err != nil {
        return
    }
    select {
    case client.send <- data:
    default:
    }
}
```

For a broadcast to 100 clients, this serializes the same message 100 times.

**Fix:**
```go
func (h *WSHub) broadcastMessage(clients []*WSClient, msg *WSMessage) {
    data, err := json.Marshal(msg)
    if err != nil {
        return
    }
    for _, client := range clients {
        select {
        case client.send <- data:
        default: // drop if full
        }
    }
}
```

### 4.2 String Concatenation in Multi-Step Loop (orchestrator.go:414-457)

**Problem:**
```go
for i, step := range req.Steps {
    // ...
    res, err := o.mcp.CallTool(ctx, toolName, step.Args)
    // ...
    for _, b := range res.Content {
        rawData += fmt.Sprintf("\n--- Step %d: %s ---\n%s\n", i+1, toolName, b.Text)
    }
}
```

Each `+=` allocates a new string and copies all previous content. For K steps with average output size S, this is O(K^2 * S) total bytes copied.

**Fix:**
```go
var builder strings.Builder
builder.Grow(len(req.Steps) * 4096) // rough estimate
for i, step := range req.Steps {
    // ...
    for _, b := range res.Content {
        fmt.Fprintf(&builder, "\n--- Step %d: %s ---\n%s\n", i+1, toolName, b.Text)
    }
}
rawData := builder.String()
```

### 4.3 WebSocket Upgrader Allocated Per Request (api.go:309-316)

**Problem:**
```go
func (a *APIv2) handleWS(w http.ResponseWriter, r *http.Request) {
    // ...
    upgrader := newUpgrader()  // allocates every time
    conn, err := upgrader.Upgrade(w, r, nil)
    // ...
}
```

**Fix:**
```go
// In APIv2 struct:
type APIv2 struct {
    // ... existing fields ...
    upgrader *websocket.Upgrader
}

// In NewAPIv2:
func NewAPIv2(...) *APIv2 {
    return &APIv2{
        // ...
        upgrader: &websocket.Upgrader{
            ReadBufferSize:  1024,
            WriteBufferSize: 1024,
            CheckOrigin:     func(r *http.Request) bool { return true },
        },
    }
}

// In handleWS:
func (a *APIv2) handleWS(w http.ResponseWriter, r *http.Request) {
    conn, err := a.upgrader.Upgrade(w, r, nil)
    // ...
}
```

### 4.4 SSE Stream Artificial Delay (api.go:676-693)

**Problem:**
```go
for i, h := range history {
    if i >= 20 {
        break
    }
    data, _ := json.Marshal(h)
    fmt.Fprintf(w, "event: arena_action\ndata: %s\n\n", data)
    flusher.Flush()
    time.Sleep(100 * time.Millisecond)  // 2 seconds for 20 events
}
```

**Fix:**
```go
for i, h := range history {
    if i >= 20 {
        break
    }
    data, _ := json.Marshal(h)
    if _, err := fmt.Fprintf(w, "event: arena_action\ndata: %s\n\n", data); err != nil {
        break // client disconnected
    }
    flusher.Flush()
    // No sleep -- send as fast as possible
}
```

### 4.5 Event Stores Entire Raw Data Payload (orchestrator.go:495)

**Problem:**
```go
o.emitEvent(id, "mcp.data.gathered", map[string]any{
    "bytes": len(rawData),
    "data": rawData,  // could be 100KB+
})
```

**Fix:**
```go
o.emitEvent(id, "mcp.data.gathered", map[string]any{
    "bytes": len(rawData),
    // Do not store raw data in events; it inflates the event store.
    // Resume support reads from tool results directly.
})
```

---

## 5. Priority Action Items

### P0 -- Must Fix

1. **Marshal once, broadcast many.** Refactor `sendToClient` / broadcast loop in `ws_hub.go` to serialize once per message, not per client. This is the single highest-impact change for WebSocket-heavy deployments.

2. **Stop storing raw MCP data in events.** The `mcp.data.gathered` event at `orchestrator.go:495` should store only metadata (byte count, hash). Remove `"data": rawData` from the payload.

3. **Fix CheckOrigin.** The permissive `CheckOrigin` at `api.go:913` is a security vulnerability. At minimum, validate against the request's `Host` header.

### P1 -- Should Fix

4. **Use `strings.Builder` for multi-step data accumulation** in `orchestrator.go:414-457`.

5. **Reuse the WebSocket upgrader** -- make it a field on `APIv2` instead of allocating per request.

6. **Remove `time.Sleep(100ms)` in SSE stream** at `api.go:688`.

7. **Fix lock ordering violation** in `ws_hub.go:148` -- `removeClient` acquires `client.mu` under `h.mu`, violating the documented ordering.

### P2 -- Nice to Have

8. **Add pagination** to `ListAgents` and `handleArenaHistory`.

9. **Build MCP server name index** for O(1) lookup in `handleMCPByName`.

10. **Batch event bridge forwarding** to reduce per-event WS broadcast overhead.

11. **Push status filter into orchestrator** for `listAgents` to avoid full-list allocation.

12. **Use `utf8.RuneCountInString`** instead of `[]rune` conversion in `truncateStr`.
