# Code Review: 2026-06-14

## Summary
- Files audited: 62 (non-test .go files across all specified packages)
- Dead code: 28 items
- Potential bugs: 16 items
- Technical debt: 18 items

---

## Dead Code

| File | Line | Type | Description |
|------|------|------|-------------|
| `internal/mcp/jsonrpc.go` | 91 | Unused function | `NewResponse` is defined but never called anywhere in the codebase |
| `internal/mcp/jsonrpc.go` | 105 | Unused function | `NewErrorResponse` is defined but never called anywhere in the codebase |
| `internal/mcp/jsonrpc.go` | 151 | Unused function | `IsRequest` is defined but never called anywhere in the codebase |
| `internal/mcp/jsonrpc.go` | 188 | Unused function | `DecodeParams` is defined but never called anywhere in the codebase |
| `internal/mcp/factory.go` | 10 | Unused type | `MCPToolFactory` is defined with constructor but never instantiated in any non-test code |
| `internal/flight/timeline.go` | 95 | Unused function | `Timeline.FilterByType` is exported but never called outside tests |
| `internal/flight/decision.go` | 74 | Unused function | `DecisionLog.FilterByType` is exported but never called outside tests |
| `internal/flight/diagnostics.go` | 85 | Unused function | `DiagnosticsEngine.FilterByCategory` is exported but never called outside tests |
| `internal/flight/genealogy.go` | 174 | Unused function | `Genealogy.Descendants` is exported but never called outside tests |
| `internal/flight/genealogy.go` | 196 | Unused function | `Genealogy.Ancestors` is exported but never called outside tests |
| `internal/flight/genealogy.go` | 276 | Unused function | `Genealogy.AllNodes` is exported but never called outside tests |
| `internal/flight/genealogy.go` | 269 | Unused function | `Genealogy.ExportJSON` is exported but never called outside tests |
| `internal/flight/graph.go` | 225 | Unused function | `Graph.ExportJSON` is exported but never called outside tests |
| `internal/arena/http.go` | 224 | Unused function | `RecoverMiddleware` is exported but never called (dashboard uses its own `withRecovery`) |
| `internal/arena/http.go` | 263 | Unused function | `RoutePath` is exported but never called anywhere |
| `internal/arena/http.go` | 279 | Unused function | `ParseActionType` is exported but never called anywhere |
| `internal/arena/scenario.go` | 22 | Unused function | `RunScenario` is exported but never called anywhere |
| `internal/arena/verifier.go` | 307 | Unused function | `PrintReport` is exported but never called anywhere |
| `internal/dashboard/types.go` | 9 | Unused type | `SystemOverview` struct is defined but never used (dashboard uses `map[string]any` for overview) |
| `internal/dashboard/types.go` | 19 | Unused type | `RuntimeStatsView` struct is defined but never used |
| `internal/dashboard/types.go` | 27 | Unused type | `MemoryStats` struct is defined but never used |
| `internal/dashboard/types.go` | 34 | Unused type | `MCPOverview` struct is defined but never used |
| `internal/llm/output/template.go` | 272 | Unused function | `RenderWithDefault` is exported but never called |
| `internal/llm/output/template.go` | 277 | Unused function | `RenderRecommendationWithDefault` is exported but never called |
| `internal/llm/output/template.go` | 282 | Unused function | `RenderProfileExtractionWithDefault` is exported but never called |
| `internal/llm/output/template.go` | 287 | Unused function | `RenderStyleAnalysisWithDefault` is exported but never called |
| `internal/llm/output/validator.go` | 511 | Unused variable | `ErrValidationFailed` is declared but never used |
| `api/errors.go` | 10 | Unused variable | `ErrInitializationFailed` is declared but never used |

---

## Potential Bugs

| File | Line | Severity | Description | Suggested Fix |
|------|------|----------|-------------|---------------|
| `internal/dashboard/orchestrator.go` | 265 | Critical | **Race condition on `result.Status` in resurrection check**: After `o.runAgent` returns, the goroutine reads `result.Status` without holding a lock, but `runAgent` modifies `result` fields under lock. The `result` pointer is shared between the goroutine and `runAgent`, creating a data race. | Read `result.Status` under `o.mu.RLock()` before the resurrection check. |
| `internal/mcp/client.go` | 200 | High | **Nil pointer dereference in `MCPClient.Close()`**: If `Connect` was never called (or failed before setting `c.cancel`), calling `Close()` will panic on `c.cancel()` because `c.ctx` and `c.cancel` are nil. | Add a nil check: `if c.cancel != nil { c.cancel() }`. |
| `internal/dashboard/orchestrator.go` | 818 | High | **`truncateStr` may split UTF-8 runes**: The function truncates by byte length (`len(s)`), not rune count. For multi-byte UTF-8 characters (e.g., Chinese, emoji), truncation at a byte boundary can produce an invalid rune. | Use `[]rune(s)` or check `utf8.ValidString(s[:n])` and back up to a valid rune boundary. |
| `internal/flight/diagnostics.go` | 221 | Medium | **`contains` function is case-sensitive despite comment**: The comment on line 221 says "case-insensitive via lowercase" but the implementation does exact substring matching, not case-insensitive. Error messages with uppercase keywords (e.g., "Timeout" vs "timeout") will not match. | Either convert both `s` and `substr` to lowercase before comparison, or fix the comment. |
| `internal/dashboard/orchestrator.go` | 246 | Medium | **Agent context leak on creation failure**: `context.WithCancel(context.Background())` is called at line 246, but if `o.CreateAgent` fails later (e.g., in resurrection), the cancel function stored in `o.cancels[id]` may leak if the goroutine never runs. | Defer `agentCancel()` in the error path or only create the context inside the goroutine. |
| `internal/mcp/transport_stdio.go` | 150 | Medium | **Goroutine leak in `Receive` on context cancellation**: When the context is cancelled at line 161, the code waits for `scanWg` but the underlying `t.stdout.Scan()` may block indefinitely if the subprocess is hung. The scanner has no timeout mechanism. | Use `cmd.Process.Kill()` or set a read deadline on the stdout pipe (not possible with `bufio.Scanner`). Consider using `bufio.Reader` with a deadline-aware read. |
| `internal/mcp/transport_sse.go` | 246 | Medium | **`msgCh` closed while `receiveLoop` may still write**: `Close()` calls `t.cancel()` then `t.eg.Wait()` then `close(t.msgCh)`. However, `handleSSEEvent` may be in the middle of sending to `t.msgCh` when the channel is closed. The `handleSSEEvent` selects on `ctx.Done()`, but there is a narrow window between context cancellation and channel close. | Use a separate `done` channel in `handleSSEEvent` instead of relying on `ctx.Done()` timing, or close `msgCh` inside the errgroup goroutine. |
| `internal/arena/http.go` | 99 | Medium | **`ArenaAdapter.Execute` always returns `Success: true`**: The `Execute` method in `api/adapters.go` line 99 always sets `Success: true` regardless of whether `CancelAgent` actually found and cancelled an agent. A failed cancel (agent not found) is reported as success. | Check the return value of `CancelAgent` and set `Success` accordingly. |
| `internal/arena/http.go` | 162 | Medium | **`calculateAvgRecoveryTime(nil)` passes nil slice**: In `handleScore`, `h.service.calculateAvgRecoveryTime(nil)` is called. The method iterates over the slice with `range`, which is safe for nil slices, but the intent is unclear -- it always returns 0 for the average recovery time when called from the score endpoint, making the speed score always 0. | Either pass the actual survival timeline events, or compute from action results. |
| `internal/api/service.go` | 57 | Medium | **Hardcoded MCP server index `[0]`**: `cfg.MCP.Servers[0]` will panic with index out of range if the config has no MCP servers. | Add a bounds check: `if len(cfg.MCP.Servers) == 0 { return nil, fmt.Errorf("no mcp servers configured") }`. |
| `internal/api/service.go` | 78 | Low | **Ignored error from `bridge.Start(ctx)`**: The error from `EventBridge.Start` is discarded with `_ =`. If the event store subscription fails, the bridge silently does nothing. | Propagate the error or log it as a warning. |
| `internal/api/service.go` | 93 | Low | **Ignored error from `fr.Start(ctx)`**: The error from `FlightRecorder.Start` is discarded with `_ =`. If the collector fails to start, flight recording silently does nothing. | Propagate the error or log it as a warning. |
| `internal/flight/genealogy_collector.go` | 109 | High | **Direct access to genealogy internals without lock**: `handleAgentStarted` directly accesses `c.genealogy.nodes` and `c.genealogy.roots` while holding `c.genealogy.mu`, but the lock is acquired manually (lines 109-128) instead of using the `Genealogy` methods. This bypasses the encapsulation and is fragile if `Genealogy` internals change. | Use `Genealogy.RecordSpawn` or a new `Genealogy.RecordRoot` method instead of directly manipulating internal state. |
| `internal/arena/survival.go` | 142 | Low | **`GetSurvivalStatus` takes write lock for read-only operation**: The method uses `s.survival.mu.Lock()` (line 143) instead of `s.survival.mu.RLock()` even though it only reads fields. | Change to `s.survival.mu.RLock()` / `s.survival.mu.RUnlock()`. |
| `internal/dashboard/orchestrator.go` | 365 | Low | **Off-by-one in progress calculation**: `progress := 20 + (i * 25 / len(req.Steps))` will never reach 45 (the max is `20 + ((n-1)*25/n)`) which is fine, but the range 20-45 is narrow for MCP gathering. When combined with LLM at 50, the jump from 45 to 50 may look abrupt. | Use `(i+1) * 25 / len(req.Steps)` for a smoother progress curve. |
| `internal/dashboard/ws_hub.go` | 187 | Low | **Lock ordering inconsistency in `Subscribe`/`Unsubscribe`**: `Subscribe` locks `c.mu` then `c.hub.mu` (lines 183-193). `removeClient` (called from the hub's main loop) holds `h.mu` and then calls `close(client.send)` which could race with `WritePump`. The lock ordering is consistent but the comment should document the ordering requirement. | Add a comment documenting the lock ordering: always `c.mu` before `h.mu`. |

---

## Technical Debt

| File | Line | Type | Description |
|------|------|------|-------------|
| `internal/llm/output/template.go` | 265 | TODO | `toYAML` function has TODO comment: "implement proper YAML serialization (expected by 2026-07-01)". Currently returns `fmt.Sprintf("%v", v)` which is not valid YAML. |
| `api/errors.go` | 7 | Duplication | `ErrInvalidConfig` is defined in 7+ places across the codebase (`api/errors.go`, `api/errors/common.go`, `api/client/errors.go`, `api/service/agent/errors.go`, `api/service/llm/errors.go`, `api/service/memory/errors.go`, `api/service/retrieval/errors.go`, `api/service/graph/service.go`, `api/service/workflow/service.go`). Should be consolidated. |
| `api/service.go` | 57 | Hardcoded | MCP server is hardcoded to `cfg.MCP.Servers[0]` -- only supports a single MCP server. The config struct supports multiple servers but only the first is used. |
| `api/service.go` | 50 | Fragile | LLM connectivity check sends "Reply OK" as a test prompt. This is wasteful and may fail for some LLM providers that reject trivial prompts. |
| `internal/dashboard/orchestrator.go` | 1 | Complexity | File is 823 lines. The `runAgent` method alone is 200+ lines with deeply nested conditionals for MCP data gathering (single tool, multi-step, resume). Should be decomposed. |
| `internal/dashboard/orchestrator.go` | 246 | Design | Agent lifecycle uses `context.Background()` instead of the service context. Agents are not cancelled when the service shuts down, leading to goroutine leaks on shutdown. |
| `internal/dashboard/api.go` | 466 | Stub | `handleArenaSurvival` returns a hardcoded "started" response without actually starting a survival run. The survival provider is only used for status queries. |
| `internal/api/adapters.go` | 102 | Stub | `ArenaAdapter.Stats()` always returns zeros. `ArenaAdapter.History()` always returns nil. These are placeholder implementations. |
| `internal/flight/diagnostics.go` | 222 | Naming | The `contains` function name shadows `strings.Contains` and the `searchString` helper reimplements `strings.Contains` manually. Should use `strings.Contains` with `strings.ToLower` for case-insensitive matching. |
| `internal/arena/scenario.go` | 1 | Orphan | The entire `scenario.go` file defines `RunScenario` which is never called. Either wire it up or remove it. |
| `internal/arena/verifier.go` | 1 | Orphan | The entire `verifier.go` file defines `Verifier` and `PrintReport` which are never called from production code. Either wire it up or move to a test helper. |
| `internal/dashboard/types.go` | 9 | Orphan | `SystemOverview`, `RuntimeStatsView`, `MemoryStats`, `MCPOverview` structs are defined but the dashboard API uses `map[string]any` for the overview response instead. |
| `internal/mcp/transport_stdio.go` | 150 | Design | `Receive` spawns a new goroutine for every call to make `bufio.Scanner.Scan()` interruptible. This is expensive for high-throughput scenarios. Consider using `bufio.Reader` with `ReadBytes('\n')` which can be interrupted via pipe close. |
| `internal/dashboard/orchestrator.go` | 442 | Data leak | `emitEvent` stores the full `rawData` (MCP tool output) in the event store payload. For large tool outputs, this can cause excessive memory usage in `MemoryEventStore`. |
| `internal/arena/survival.go` | 190 | Security | `randomID()` uses `math/rand` for generating IDs. While acceptable for chaos testing (acknowledged with nolint), the IDs are predictable. If these IDs are exposed via API, consider using `crypto/rand`. |
| `internal/mcp/manager.go` | 246 | Encapsulation | `registerTools` directly accesses `mc.client.mu` and `mc.client.tools` (internal fields of `MCPClient`). This breaks encapsulation. `MCPClient` should expose a `ToolDefs()` method. |
| `internal/dashboard/ws_hub.go` | 151 | Cleanup | `removeClient` closes `client.send` channel but does not close `client.conn`. The WebSocket connection is closed in `ReadPump`/`WritePump` defer blocks, but if those goroutines are stuck, the connection leaks. |
| `internal/api/service.go` | 100 | Graceful shutdown | The HTTP server goroutine uses `go func() { _ = s.httpServer.ListenAndServe() }()` but `Wait()` only calls `httpServer.Shutdown`. The `hub.Run()` goroutine is never stopped, and MCP client is never closed. Missing `Stop()` method for clean teardown. |

---

## Severity Distribution

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High | 3 |
| Medium | 7 |
| Low | 5 |
| **Total bugs** | **16** |

## Package-by-Package Notes

### `internal/mcp/`
Well-structured JSON-RPC and transport layer. Main issues: several exported JSON-RPC helper functions (`NewResponse`, `NewErrorResponse`, `IsRequest`, `DecodeParams`) are dead code -- likely written speculatively for a server-side implementation that was never built. The `MCPClient.Close()` nil-pointer on `c.cancel` is the most serious bug. The `MCPToolFactory` is defined but never wired into any plugin registry.

### `internal/dashboard/`
The orchestrator is the largest and most complex file. The race condition on `result.Status` during agent resurrection is the critical finding. The `truncateStr` UTF-8 issue can produce garbled output for non-ASCII tool results. Several view types in `types.go` are unused artifacts. The `ArenaAdapter` and `SurvivalProvider` implementations are stubs.

### `internal/flight/`
Clean, well-protected data structures with proper mutex usage. The main issues are dead code (many exported filter/query methods are never called) and the genealogy collector directly manipulating genealogy internals. The `contains` function's case-sensitivity mismatch with its comment is a latent bug.

### `internal/arena/`
Good chaos engineering design. The `scenario.go` and `verifier.go` files are completely orphaned -- never called from production code. The `RecoverMiddleware`, `RoutePath`, and `ParseActionType` exports are also unused. The `calculateAvgRecoveryTime(nil)` call in the score endpoint always returns 0 for speed score.

### `internal/callbacks/`
Clean, minimal lifecycle hook registry. No issues found.

### `internal/llm/output/`
Template engine and parser are solid. The `toYAML` TODO is the main debt. Many template convenience functions (`RenderWithDefault`, `RenderProfileExtractionWithDefault`, etc.) are dead code. The `ErrValidationFailed` sentinel is declared but unused.

### `api/`
The `StartService` function is fragile: hardcoded single MCP server, ignored errors, no graceful shutdown, and a wasteful LLM connectivity check. The error package has excessive duplication of `ErrInvalidConfig` across 7+ files.

### `examples/mcp-dashboard/`
Minimal entry point, no issues found.

### `cmd/flight/`
Well-structured CLI. The `separateArgs` helper is a good pattern for subcommand flag parsing. No significant issues.
