# goagent / ARES — Deep Code Review Report

**Date:** 2026-07-08
**Method:** 11 parallel review agents, each reading every file in its assigned modules end-to-end.
**Scope:** ~1,169 Go files + the Python embedding service, across `internal/`, `api/`, `cmd/`, `sdk/`, `examples/`, `evaluation/`, `services/`.

---

## Executive Summary

| Severity | Count |
|----------|------:|
| **CRITICAL** | 37 |
| **HIGH** | 116 |
| **MEDIUM** | 283 |
| **LOW** | 224 |
| **INFO** | 104 |
| **Total** | **764** |

The project is a large, ambitious multi-agent framework. It compiles and runs, but it carries **systemic security and correctness gaps** that make it unsafe to deploy as-is against untrusted input or in production. The single most important theme:

> **The tool-execution layer (file, network, code-runner, MCP) ships with default-permissive settings, bypassable sandboxing, and no SSRF protection. Combined with no authentication on any HTTP endpoint, any LLM agent (or any network-reachable client) can achieve full host compromise.**

Secondary themes:

- **Concurrency bugs are pervasive** — data races, goroutine leaks, send-on-closed-channel, broken mutual exclusion, and `context.Background()` misuse that defeats cancellation.
- **Path-traversal protection is broken in 5+ places** — all use `strings.HasPrefix(absPath, absDir)`, which a sibling directory like `/allowed-evil/` bypasses.
- **Duplicate error sentinels** between `internal/errors` and `internal/core/errors` break `errors.Is` across the codebase.
- **Monitoring data is silently wrong** — stale pointers lose span durations, `percentile` returns max instead of p99, latency units (ns vs ms) are confused, and currency is mixed in cost totals.
- **SQL is built with `fmt.Sprintf`** in multiple repositories, and tenant isolation has holes (`SET` vs `SET LOCAL`, missing tenant scoping on `GetByID`/`DeleteByID`).
- **Examples ship broken/misleading tools** (hardcoded "445" calculator, always-failing parser, auto-approving file deletion) that will be copied as anti-patterns.

---

## CRITICAL Findings (37)

These are bugs that cause data corruption, security compromise, panics in normal operation, or fundamentally broken features. Address before any production use.

### Security — Tool Execution (RCE / SSRF / Path Traversal)

1. **`internal/tools/resources/builtin/execution/code_runner.go:224-259`** — Pattern-based denylist (`strings.Contains` for `import os`, `eval(`) is trivially bypassable; no process isolation (no chroot/seccomp/cgroups). **Remote code execution as the service user.**
2. **`internal/tools/resources/builtin/file/file_tools.go:183,266,348`** — Path traversal check uses `strings.HasPrefix(absPath, absDir)`. `/allowed-backdoor/secret` passes when `allowedDir = "/allowed"`. Symlinks not resolved.
3. **`internal/tools/resources/builtin/network/http_request.go:64-165`** — No SSRF protection. LLM can hit `http://169.254.169.254/latest/meta-data/`, `file:///etc/passwd`, internal services. No redirect policy, no response size limit.
4. **`internal/tools/resources/builtin/builtin.go:43,58,128`** — `FileTools` registered with no `WithAllowedDir`; `HTTPRequest` with no URL filter; `CodeRunner` with Python enabled by default. All globally registered → every agent has full host access.
5. **`internal/tools/resources/builtin/network/web_scraper.go:96`** and **`web_search.go:79-112`** — `searxng_base_url` is user-overridable → SSRF vector.
6. **`api/tools/builtin.go`** — Same issues as `internal` builtin registration: arbitrary file read/write/delete, SSRF-vulnerable web search, no input limits on regex.
7. **`internal/discovery/health.go:52-60`** — `probeMCP` connects to any URL / runs any binary from discovered service records with no allowlist. Combined with filesystem provider scanning world-writable dirs → SSRF + arbitrary execution.
8. **`internal/discovery/providers/binary.go`** — `probeBinary` runs discovered binaries with `--help`/`--version` using the process's privileges; no whitelist.

### Security — API Surface

9. **`internal/monitoring/http_api.go:97-103`** — No auth on `kill`/`resume`/`retry`/`/mcp/tools/:name/call`. Any network-reachable client can kill agents or invoke MCP tools.
10. **`internal/dashboard/api.go`** — No auth on chaos/arena endpoints (`kill_orchestrator`, `network_partition`, `memory_corrupt`, agent create/destroy).
11. **`internal/dashboard/api.go` (WS `CheckOrigin`)** — `strings.Contains(origin, host)` allows `https://evil.com/?target=realhost`; `strings.HasPrefix(origin, "http://localhost")` opens WS to any localhost page.
12. **`internal/ares_arena/http.go:35-61`** — All 23 routes unauthenticated, including destructive chaos actions.
13. **`api/handler/stream.go:82`** — SSE `Access-Control-Allow-Origin: *` with no auth → any website can stream agent data.

### Security — Storage / Secrets

14. **`internal/storage/postgres/config.go`** — `DefaultConfig()` hardcodes `Password: "postgres"` and `sslmode=disable`. Production inherits insecure defaults.
15. **`internal/storage/postgres/base_repository.go`** — `GetByID`/`DeleteByID`/`CountByTenant` build SQL with `fmt.Sprintf("SELECT ... FROM %s ...", tableName)`. SQL injection idiom normalized in the base layer.
16. **`internal/storage/postgres/tenant_guard.go`** — `ClearTenantContext` uses `SET` (session-level) not `SET LOCAL`. Cleared tenant context persists on pooled connections → RLS isolation breaks across requests.
17. **`internal/knowledge/provider/postgres/provider.go:119-143`** — SQL injection: column/table names interpolated via `fmt.Sprintf`; worse, `FROM %s` uses the ID column name as the table name (clear bug).

### Correctness — Data Corruption / Silent Wrongness

18. **`internal/monitoring/data/trace_linker.go:124,170,240`** — Stores `&spans[traceID][len-1]` (pointer into slice backing array). Later `append` reallocates → stale pointer. `handleAgentStopped` writes through stale pointer → span durations silently lost. Same in `handleToolCallStarted` and `handleTaskCreated`.
19. **`internal/monitoring/intelligence.go:454-471`** — `percentile` is documented as "P99" but ignores `p` and returns `max`. Latency anomaly threshold fires on worst-ever sample, not 99th percentile.
20. **`internal/ares_quant/portfolio/simulator.go:544-555`** — Multi-asset simulation routes ALL signals to `symbols[0]`, ignoring `signal.Symbol`. Every multi-symbol backtest is silently wrong.
21. **`internal/ares_quant/research/evaluation.go:190-193`** — `drawdownAwareReturn` ADDS the variance penalty instead of subtracting. Drawdown-adjusted returns are inverted — bad sequences score higher.
22. **`internal/ares_quant/marketmaking_api/backtest.go:169`** — `mergeResponses` averages `TotalReturn` instead of recomputing from PnL + capital. Mathematically wrong for multi-symbol.
23. **`internal/ares_evolution/mutation/adaptive_distribution.go:405-415`** — Normalizes probabilities to sum=1.0, then re-clamps to bounds, breaking the invariant. Mutation selection is biased.
24. **`internal/workflow/graph/node.go:267-271`** — `hashInput` uses `fmt.Sprintf("%v", map)` — Go randomizes map iteration → non-deterministic `tool_call_id`. Breaks caching/idempotency.
25. **`internal/knowledge/runtime/lazy_graph.go:77-95`** — `NewLazyGraph` sets `expanded: true` on all nodes, so `Expand()` returns `ErrNodeAlreadyExpanded` without ever calling `expandFn`. Lazy loading is completely broken.
26. **`internal/storage/postgres/write_buffer.go`** — On retry, failed embedding batches are DROPPED (discarded), not re-queued. Silent data loss.
27. **`internal/storage/postgres/vector.go`** — Hardcoded `dim = 1536` while migrations create `VECTOR(1024)` tables. Dimension mismatch → every vector insert/search fails at runtime.
28. **`internal/ares_memory/context/rag.go:73`** — `Add` writes to pgvector BEFORE acquiring the in-memory lock. Concurrent adds with same ID corrupt state.
29. **`internal/storage/postgres/embedding_queue.go`** — `MarkFailed` does read-then-write (SELECT then UPDATE) with no transaction/`FOR UPDATE`. Concurrent workers race on pending→failed.
30. **`internal/core/errors/errors.go:11-104`** vs **`internal/errors/wrap.go:13-131`** — Duplicate sentinel errors (`ErrAgentNotFound`, etc.) as DIFFERENT values. `errors.Is(core_errors.X, internal_errors.X)` is always false. Breaks error checking across packages.
31. **`internal/core/errors/handler.go:40`** — `appErr.Code.Code` panics when `Code == nil` (which `AppError` explicitly permits).
32. **`internal/agents/service_impl.go:170`** — `updates["status"].(core.AgentStatus)` always fails for JSON-sourced maps (which produce `string`). Status updates silently dropped.
33. **`internal/dashboard/orchestrator.go:673`** — `failAgent` does `err.Error()` without nil check. Any caller passing nil error panics the process.
34. **`internal/ares_evolution/feedback_recorder.go:133-139`** — Circuit breaker atomicity broken by unlock/service-call/relock. Data race on `circuitBreakerConsecutiveErrors`; breaker may never trip or trip spuriously.

### Concurrency — Panics / Deadlocks

35. **`internal/ares_mcp/client.go:341-351`** — `handleNotification` runs synchronously in `receiveLoop`; on `tools/list_changed` it calls `ListTools` which blocks on a response only `receiveLoop` can deliver → **deadlock** for 30s.
36. **`internal/ares_mcp/transport_sse.go:173-177`** — SSRF: SSE server can send `endpoint` event redirecting POSTs (with auth headers) to arbitrary URL.
37. **`internal/ares_shutdown/manager.go:221-236`** — After phase timeout, `close(errChan)`/`close(panicChan)` races with still-running callback goroutines → **panic: send on closed channel**.

---

## Cross-Cutting Themes

### 1. Path traversal protection is broken everywhere (5+ sites)
All use `strings.HasPrefix(absPath, absDir)`. A sibling dir `/allowed-evil/` bypasses it.
- `internal/ares_config/config.go:254`
- `internal/core/errors/strategy_config.go:107`
- `internal/workflow/engine/loader.go:91`
- `api/client/config.go`
- `internal/tools/resources/builtin/file/file_tools.go:183,266,348`
- `internal/ares_eval/loader.go:25`

**Fix pattern:** `rel, err := filepath.Rel(absDir, absPath); if err != nil || strings.HasPrefix(rel, "..") { deny }`, plus `filepath.EvalSymlinks` on both paths.

### 2. `context.Background()` misuse (defeats cancellation)
Found in 15+ locations. Background work, I/O, and event emission use `context.Background()` instead of the caller's context, leaking goroutines and preventing shutdown.
- `internal/ares_quant/marketmaking_api/client.go:196`, `market/polymarket.go:47`, `market/yahoo.go:60`, `market/coingecko.go:77`
- `internal/ares_evolution/scoring/memory_aware_scorer.go:502`
- `internal/ares_callbacks/callback_bridge.go:55`
- `internal/monitoring/http_api.go:381`, `internal/dashboard/orchestrator.go:705`
- `internal/workflow/engine/hitl_plugin.go:73`
- `internal/storage/postgres/pool.go` (QueryRow hack)

**Rule to adopt:** every I/O function must accept and propagate `context.Context`.

### 3. Goroutine leaks / lifecycle not managed
`Stop()` methods that don't wait for their loop goroutines, unbuffered channels that block forever, errgroups never `Wait()`ed.
- `internal/monitoring/pruner.go`, `collector.go`
- `internal/dashboard/ws_hub.go` (`Register`/`Unregister` block on unbuffered channels; `Stop()` doesn't wait for `Run`)
- `internal/ares_bootstrap/provide_dashboard.go:45-49` (hub goroutine never waited)
- `internal/ares_runtime/bus.go:299` (goroutine leaks on timeout if `fn` ignores ctx)
- `internal/ares_runtime/router_memory.go:78` (uncancellable prefetch goroutine)
- `api/mcp/stdio.go:80-91` (goroutine leaks on timeout; `close()` kills process but never `cmd.Wait()` → zombie)

### 4. Regex compiled inside functions/loops (perf + DoS)
- `internal/ares_security/sanitizer.go` (9 sites)
- `internal/tools/planner/extractor.go:53,63,69,75,81`
- `internal/tools/resources/builtin/text/log_analyzer.go:229` (compiled inside per-line loop → 300k+ compilations for 100k-line log)
- `internal/tools/resources/builtin/text/data_validation.go:111,172`
- `internal/tools/resources/builtin/network/web_scraper.go:174,185-215`
- `internal/workflow/engine/definition.go:153,184,215`

**Fix:** move all to package-level `var` blocks.

### 5. `io.ReadAll` without `LimitReader` (OOM DoS)
- `internal/llm/output/openai.go:409`, `internal/tools/resources/builtin/network/http_request.go:137`, `http_client.go:95`, `embedding.go:121`, `api/handler/workflow.go`, `api/handler/llm.go`, `ares_eval/service/handler.go:38,382,453,474`, `cmd/embedding-mcp/main.go:112,170`

### 6. SQL built with `fmt.Sprintf` / `err == sql.ErrNoRows`
- `internal/storage/postgres/base_repository.go`, `distilled_memory_repository.go`, `repository_test_helper.go:224`
- `err == sql.ErrNoRows` (not `errors.Is`) in `session.go`, `strategy_repository.go`, `production_manager.go`, `repository.go:150`

### 7. Hardcoded DB credentials `postgres/postgres`
- `cmd/check_rls/`, `cmd/create_distilled_table/`, `cmd/migrate_db/`, `cmd/setup_test_db/`, `cmd/ares/db_check_rls.go`, `internal/storage/postgres/config.go`, `repository_test_helper.go:29`

### 8. Massive code duplication
- `cmd/ares/` vs `cmd/monitor-live/` — nearly identical `agents.go`, `actions.go`, `bridge.go`, `mcp_adapter.go`, `tools.go`, `main/serve` (agents.go is identical including the `log.Fatalf` bug).
- `cmd/flight/main.go` vs `cmd/ares/flight.go` — entire file duplicated.
- `cmd/mcp-null/` vs `cmd/ares/mcp_null.go`.
- `internal/storage/postgres/migrate.go` vs `migrate_storage.go` — duplicate table definitions with divergent RLS/indexes.
- Duplicated `getString`/`getFloat64` helpers across `monitoring/tabs/tab.go`, `eventutil`, and 5+ tool packages.

---

## Per-Module Detailed Findings

The full per-file findings from each agent are below. Severity tags: **[CRITICAL] / [HIGH] / [MEDIUM] / [LOW] / [INFO]**.

---

### Module Group 1: Foundation & Core
**Scope:** `internal/core`, `internal/errors`, `internal/logger`, `internal/truncate`, `internal/ares_config`, `internal/ares_ctxutil`, `internal/ares_shutdown`, `internal/ares_callbacks`
**Files reviewed:** 38
**Counts:** Critical 4 · High 9 · Medium 28 · Low 30

#### `internal/core/errors/errors.go`
- **[CRITICAL]** Duplicate sentinel errors vs `internal/errors/wrap.go:13-131` — two packages export same-named sentinels as different values; `errors.Is` across packages fails. Consolidate to one source of truth.
- **[LOW]** `fmt.Errorf` at init — sentinels built at init; tests can't swap `apperrors.ErrNotFound`.

#### `internal/core/errors/handler.go`
- **[CRITICAL]** `HandleError` `code.go:40` — `appErr.Code.Code` panics if `Code == nil` (which `AppError` permits). Add nil check.
- **[HIGH]** `RetryWithBackoff:63-91` does NOT retry — calls `fn()` once; caller must loop. Rename or implement.
- **[MEDIUM]** `FormatError`/`IsRetryable` use type assertion not `errors.As` — fails on wrapped errors.
- **[MEDIUM]** Backoff shift `1<<(attempt-1)` overflows for `attempt > 64`.

#### `internal/core/errors/strategy_config.go`
- **[HIGH]** Path traversal check `:107-109` — `strings.HasPrefix(absPath, absDir)` allows `/etc/allowed_dir_evil/`. Use `filepath.Rel`.
- **[MEDIUM]** `SetAllowedDir` not thread-safe — writes `allowedDir` without lock; `LoadStrategiesFromConfig` reads under lock.
- **[MEDIUM]** `ExportStrategiesToConfig` has no path traversal check (asymmetric with Load).
- **[LOW]** `validateStrategy` doesn't cap `Backoff` (could be `24h`).

#### `internal/core/errors/code.go`
- **[HIGH]** `AppError.WithContext` not concurrency-safe — mutates `Context` map without sync.
- **[MEDIUM]** `Context` is an exported mutable map.
- **[MEDIUM]** `Err error` field marshals poorly.

#### `internal/core/models/user.go`
- **[HIGH]** `Validate`/`HasStyle`/`HasOccasion`/`SetRating` panic on nil receiver (inconsistent with `IsValid` which handles nil).

#### `internal/core/models/recommend.go`
- **[MEDIUM]** `AddItem` doesn't deduplicate.
- **[MEDIUM]** `CalculateScore` mutates state (named "Calculate" implies pure).

#### `internal/core/models/{session,task}.go`
- **[MEDIUM]** `Progress` can exceed 1.0 (results without matching tasks).
- **[MEDIUM]** `Task` has redundant `TaskType` and `AgentType` (both `AgentType`).
- **[MEDIUM]** `NewTask` doesn't init `TaskContext` sub-maps → nil-panic on write.
- **[LOW]** `IsExpired` returns false for zero time (masks uninitialized fields).

#### `internal/errors/wrap.go`
- **[CRITICAL]** Duplicate sentinels (see above).
- **[HIGH]** `WrapError:171-182` stringifies base error (loses unwrap chain); `errors.Is(result, base)` is false.
- **[MEDIUM]** `Wrap(err, "")` silently returns original.
- **[MEDIUM]** `FormatError` name collides with `core/errors.FormatError`.

#### `internal/logger/logger.go`
- **[MEDIUM]** `Error` signature breaks slog symmetry (`err error` param inserted).
- **[MEDIUM]** `attrs` allocates per call — hot-path overhead for per-token logging.
- **[LOW]** Package doc example uses `slog.Info` instead of `log.Info`.

#### `internal/truncate/truncate.go`
- **[HIGH]** `WithEllipsis` returns `maxLen + 3` chars but doc promises "at most maxLen runes". Callers relying on a hard ceiling overflow.
- **[MEDIUM]** `WithEllipsis("AB", 1)` returns `"A..."` (4 chars) — truncation expands.
- **[LOW]** Unnecessary `[]rune` allocation when input fits.

#### `internal/ares_config/config.go`
- **[HIGH]** Path traversal check `:254` — `strings.HasPrefix`.
- **[HIGH]** `StorageConfig.Password` plaintext — risks accidental logging/serialization.
- **[MEDIUM]** `allowedConfigDir` package-level mutable var without sync.
- **[MEDIUM]** `LoadFromEnv` uses `fmt.Sscanf` (permissive: `"123abc"`→`123`); silently ignores invalid port.
- **[MEDIUM]** `LLMConfig.Fallbacks` recursive with no depth limit.
- **[LOW]** `setDefaults` is 120 lines of `if x == 0`.

#### `internal/ares_ctxutil/ctxutil.go`
- **[MEDIUM]** `seq` field unused (dead code).
- **[MEDIUM]** `trackBackground` requires manual `DoneBackground` — forgotten on early return → unbounded growth.
- **[LOW]** `BackgroundStats` snapshot may be stale.

#### `internal/ares_shutdown/manager.go`
- **[CRITICAL]** `executePhase:221-236` — after timeout, `close(errChan)`/`close(panicChan)` races with callback goroutines → send-on-closed-channel panic.
- **[HIGH]** `executePhase:150,160` reads `handler.callbacks` without lock — races with `AddCallback`.
- **[MEDIUM]** `StartShutdown` doesn't reset `currentPhase` (one-shot only, undocumented).

#### `internal/ares_shutdown/phase.go`
- **[HIGH]** `Execute` holds `RLock` while calling `onFailure`/`onComplete` — deadlock if callback re-enters write lock.
- **[MEDIUM]** Backoff cap `1<<30` seconds ≈ 394 years (meaningless).
- **[MEDIUM]** `endTime` not set on failure → `Duration()` grows forever.
- **[MEDIUM]** `Rollback` not called automatically on failure.
- **[LOW]** `retries` written without lock.

#### `internal/ares_shutdown/signal.go`
- **[HIGH]** `AddSignal:122-126` doesn't update `signal.Notify` — new signal never delivered.
- **[MEDIUM]** `SetContext` orphans old cancel func.
- **[MEDIUM]** `handleSignal` panics if `manager` nil.
- **[MEDIUM]** `Stop` doesn't wait for `handleSignals` goroutine.

#### `internal/ares_shutdown/callbacks.go`
- **[MEDIUM]** `Register` O(n²) bubble sort on every registration.
- **[MEDIUM]** `GetCallbacks` discards `Timeout`/`OnError` (dead fields).
- **[MEDIUM]** `Register` doesn't check duplicate IDs.
- **[MEDIUM]** `ExecuteParallel` can hang on context cancellation if callback ignores ctx.

#### `internal/ares_callbacks/callbacks.go`
- **[MEDIUM]** `Emit` allocates handler slice copy per call — hot-path overhead.
- **[MEDIUM]** `Context.Extra` map shared across handlers — mutation affects subsequent handlers.

#### `internal/ares_callbacks/callback_bridge.go`
- **[MEDIUM]** `Emit` uses `context.Background()` — detaches from request context.
- **[MEDIUM]** `mapEventType` collapses `EventLLMStart`/`End`/`Error` into `EventLLMCall` — consumers can't distinguish.
- **[LOW]** `Extra` keys can overwrite explicit payload keys.

---

### Module Group 2: Agents & Runtime
**Scope:** `internal/agents`, `internal/ares_runtime`
**Files reviewed:** 76
**Counts:** Critical 1 · High 4 · Medium 16 · Low 18

#### `internal/agents/service_impl.go`
- **[CRITICAL]** `:170` — `updates["status"].(core.AgentStatus)` always fails for JSON-sourced maps; status updates silently dropped. Assert to `string` then convert.
- **[MEDIUM]** `:22` — `taskResults` map unbounded (memory leak).
- **[MEDIUM]** `:160-190` — status reset not deferred; stuck at Running on panic.

#### `internal/agents/memory_repository_impl.go`
- **[MEDIUM]** `Get`/`List` shallow-copy `Config` map — callers mutate stored data.
- **[LOW]** `Update`/`Delete` return `fmt.Errorf` for not-found; `Get` returns sentinel — inconsistent.

#### `internal/agents/leader/agent.go`
- **[HIGH]** `ProcessStream:608-799` — mutual exclusion broken: `defer processingMu.Unlock()` releases at return, but streaming goroutine outlives the lock → concurrent data races on `status`/memory/feedback.
- **[HIGH]** `Start:105-138` — race: lock released before `stopCh` created; concurrent `Stop` closes nil channel → panic. (Contrast `sub/agent.go` which creates `stopCh` inside lock.)
- **[LOW]** Misplaced/orphaned doc comments; naming collision `emitCallback`/`ares_callbacks`.

#### `internal/agents/leader/agent_memory.go`
- **[MEDIUM]** `sessionInitOnce:24` consumed even on failure — `CreateSession` failure means no retry ever.
- **[MEDIUM]** `:113` distillation uses request context — aborted when HTTP request finishes.

#### `internal/agents/leader/dispatcher.go`
- **[MEDIUM]** `:175-186` returns `Success=true` for "dispatched via message queue" — masks non-execution.
- **[LOW]** `getAgentID()` hardcoded `"leader"`.

#### `internal/agents/leader/event_recovery.go`
- **[MEDIUM]** `:85-90` no dedup on `EventTaskCreated` replay — duplicate pending tasks.

#### `internal/agents/leader/profile.go`
- **[MEDIUM]** `Parse:64-83` never returns an error — silently degrades to default.

#### `internal/agents/leader/supervisor.go`
- **[MEDIUM]** `HandleFailover:385-389` mutates private field via type assertion — leaky abstraction.

#### `internal/agents/sub/agent.go`
- **[MEDIUM]** `Process:219-246` TOCTOU — status check and transition split across two locks; two concurrent calls both claim `Busy`.
- **[MEDIUM]** `ProcessStream:308-396` same race.
- **[LOW]** `Stop:200` correctly nil-guards `stopCh` (good pattern).

#### `internal/agents/sub/handler.go`
- **[MEDIUM]** `handleTaskMessage`/`handleAckMessage` are silent no-op stubs.

#### `internal/agents/sub/tools.go`
- **[MEDIUM]** `GetTool:162-179` TOCTOU — releases lock then calls `registry.Get` on possibly-nil registry → nil panic.
- **[LOW]** `BridgeFromRegistry` holds write lock for full loop.

#### `internal/ares_runtime/manager.go`
- **[HIGH]** `:162-173` — context cancel leak: when `chaos.toolTimeout > 0`, `agentCancel` (from `WithCancel`) overwritten by `WithTimeout`'s cancel; parent cancel never invoked → leaked.
- **[HIGH]** `:599-608` — write under read lock: `ma.cancel = agentCancel` while holding `RLock` → data race.
- **[MEDIUM]** `Start()` replaces errgroup; prior goroutines with detached context continue.
- **[MEDIUM]** `:997-1028` chaos mutations only affect new launches, not running agents.
- **[MEDIUM]** `:657-680` `store.Save` (Postgres I/O) under write lock — blocks all lookups.
- **[LOW]** `:1011-1046` chaos stubs return nil (misleading).

#### `internal/ares_runtime/bus.go`
- **[MEDIUM]** `invokeWithTimeout:299-323` goroutine leaks if `fn` ignores ctx.

#### `internal/ares_runtime/checkpoint.go`
- **[MEDIUM]** `:247-256` double-lock TOCTOU on flush decision.
- **[LOW]** `:400` `context.Background()` for bus emit.

#### `internal/ares_runtime/loop.go`
- **[MEDIUM]** `:126` leaky type assertion `p.bus.(*PluginBus)`.
- **[MEDIUM]** `:150` API misuse: `AdviseRoute` for round completion.
- **[LOW]** `:159` dead variable `_ = state`.

#### `internal/ares_runtime/outcome_recorder.go`
- **[MEDIUM]** `:116-118` score not clamped to [0,1] — can go negative; bandit logic misbehaves.

#### `internal/ares_runtime/router_memory.go`
- **[MEDIUM]** `:78-84` prefetch goroutine has no cancellation/timeout — leaks if `queryMemory` blocks.

#### `internal/ares_runtime/{router_fallback,router_evolution,interrupt,recovery,tool}.go`
- **[LOW]** `Stop` keeps only first error (use `errors.Join`); magic `0.3` threshold; brittle interrupt heuristic; silently dropped `store.Load` error; tool input always `""`.

---

### Module Group 3: LLM & Tools
**Scope:** `internal/llm`, `internal/llmservice`, `internal/tools`
**Files reviewed:** 64
**Counts:** Critical 9 · High 17 · Medium 33 · Low 22

#### `internal/tools/resources/builtin/execution/code_runner.go`
- **[CRITICAL]** `:224-259` denylist bypassable; `:264,328` no process isolation (no chroot/seccomp/cgroups). RCE as service user.
- **[HIGH]** Mutator methods (`EnablePython`, `SetTimeout`, etc.) not thread-safe.
- **[HIGH]** No output size limit — `print("x"*10**9)` OOMs (truncation happens after `cmd.Run`).

#### `internal/tools/resources/builtin/file/file_tools.go`
- **[CRITICAL]** `:183,266,348` `strings.HasPrefix` path traversal.
- **[HIGH]** No restriction when `allowedDir == ""` (default) — entire FS readable/writable.
- **[HIGH]** Symlink escape — `filepath.Abs`/`Clean` don't resolve symlinks.
- **[MEDIUM]** `os.Stat` before security check leaks file existence.
- **[MEDIUM]** No file size limit on read.

#### `internal/tools/resources/builtin/network/http_request.go`
- **[CRITICAL]** `:64-165` no SSRF protection (cloud metadata, internal services, `file://`).
- **[HIGH]** `io.ReadAll` no `LimitReader`.
- **[HIGH]** No redirect policy — public URL → internal IP.

#### `internal/tools/resources/builtin/network/web_scraper.go`
- **[HIGH]** No SSRF protection (`isValidURL` only checks `http(s)://`).
- **[MEDIUM]** Regex compiled per call.

#### `internal/tools/resources/builtin/network/web_search.go`
- **[MEDIUM]** `searxng_base_url` user-controllable → SSRF.

#### `internal/tools/resources/builtin/network/http_client.go`
- **[MEDIUM]** `io.ReadAll` no `LimitReader`.

#### `internal/tools/resources/builtin/pdf/pdf.go`
- **[HIGH]** No path traversal protection.
- **[MEDIUM]** No file size limit; O(n²) string concat.

#### `internal/tools/resources/builtin/math/calculator.go`
- **[HIGH]** `compiled` map not thread-safe — races on concurrent `Execute`.
- **[MEDIUM]** `factorial` no upper bound (loops 1B times).

#### `internal/tools/resources/builtin/text/regex_tool.go`
- **[HIGH]** User-supplied regex with no timeout → ReDoS (catastrophic backtracking).

#### `internal/tools/resources/builtin/text/log_analyzer.go`
- **[HIGH]** Regex compiled inside per-line loop → 300k+ compilations for 100k-line log.

#### `internal/tools/resources/builtin/text/data_validation.go`
- **[MEDIUM]** Regex compiled per call; `float64` with fractional part accepted as `integer`.

#### `internal/tools/resources/builtin/text/json_tools.go`
- **[MEDIUM]** `deepMerge` no depth limit (stack overflow with nested arrays, since `ValidateJSONDepth` doesn't count `[]`).

#### `internal/tools/resources/builtin/builtin.go`
- **[CRITICAL]** `FileTools` no `WithAllowedDir`; `HTTPRequest` no SSRF filter; `CodeRunner` Python enabled by default.
- **[HIGH]** Knowledge/Memory tools registered with `nil` service → nil-panic on invoke.
- **[MEDIUM]** `TaskPlanner` registered with `nil` LLM.

#### `internal/tools/resources/core/registry.go`
- **[HIGH]** `Filter` returns registry with broken `schemaDirty`/`schemaCache` invariant.
- **[HIGH]** `GetSchemas` double-checked locking is fragile/non-obvious.
- **[MEDIUM]** `ValidateParams` doesn't recurse into array elements/object properties.
- **[MEDIUM]** `checkType` accepts `1.5` as `integer`.

#### `internal/tools/planner/bridge.go`
- **[HIGH]** `logger.Module` called per invocation (allocates logger each call).
- **[MEDIUM]** `executeMultiStep` sequential — no parallelism for independent DAG branches.
- **[MEDIUM]** Fallback failures not saved as negative evidence.

#### `internal/tools/planner/extractor.go`
- **[HIGH]** Regex compiled on every call.
- **[LOW]** Dead `var _ = math.Round`.

#### `internal/tools/planner/resolver.go`
- **[MEDIUM]** O(n²) capability lookup.

#### `internal/tools/planner/analyzer.go`
- **[MEDIUM]** `extractConstraints:227` is a no-op (`_ = request`).

#### `internal/llm/client.go`
- **[HIGH]** `streamClient` uses `http.DefaultTransport` — no conn limits; FD/goroutine exhaustion under load.
- **[MEDIUM]** `isRateLimitError` falls back to substring matching (fragile).

#### `internal/llm/failover.go`
- **[HIGH]** `GenerateStream` no per-call timeout — hung stream blocks failover loop.

#### `internal/llm/output/openai.go`
- **[HIGH]** `sendToolRequest:409` `io.ReadAll` no `LimitReader`.

#### `internal/llm/output/ollama.go`
- **[MEDIUM]** Schema appended to prompt via string concat — prompt-injection vector.
- **[INFO]** Correctly uses `/api/generate` for non-tool, `/api/chat` for tools (meets project constraint).

#### `internal/llm/output/validator_ext.go`
- **[MEDIUM]** `ValidateJSONDepth:111` only counts `{`/`}` — deeply nested arrays bypass.

#### `internal/llm/output/openrouter.go`
- **[MEDIUM]** Read error silently ignored.

#### `internal/llmservice/service.go`
- **[HIGH]** `CallbackRegistry` only applied to single client, not `FailoverClient` — observability dropped for failover paths.
- **[MEDIUM]** `embeddingClient` typed as `any` with runtime assertions.
- **[MEDIUM]** `buildPrompt` O(n²) string concat.
- **[INFO]** Correctly routes to Chat API when tools present (meets constraint).

#### `internal/tools/resources/formatter/result_formatter.go`
- **[HIGH]** `formatCalculator:161` uses `%.2f` on `interface{}` — malformed output for non-floats.
- **[MEDIUM]** Mixed Chinese/English in user-facing output (violates English convention).

#### `internal/tools/resources/builtin/memory/memory_tools.go`
- **[MEDIUM]** Hardcoded Chinese keywords for preference extraction (`"精通"`, `"喜欢"`) — won't work for English users.
- **[MEDIUM]** Brittle string parsing for preferences.

#### `internal/tools/resources/builtin/embedding/embedding.go`
- **[MEDIUM]** No response size limit.
- **[LOW]** Batch response doesn't return embeddings (likely a bug).

---

### Module Group 4: Storage & Memory
**Scope:** `internal/storage`, `internal/ares_memory`, `internal/memoryservice`, `internal/retrievalservice`
**Files reviewed:** 167
**Counts:** Critical 5 · High 21 · Medium 56 · Low 28

#### `internal/storage/postgres/config.go`
- **[CRITICAL]** `DefaultConfig()` hardcodes `Password: "postgres"`, `sslmode=disable`.
- **[MEDIUM]** `Validate()` silently mutates receiver to apply defaults.
- **[LOW]** Dead `var _ = strconv.IntSize`.

#### `internal/storage/postgres/pool.go`
- **[HIGH]** Package-level `// nolint:errcheck` suppresses ALL errors in file.
- **[MEDIUM]** `QueryRow` uses canceled background context hack.
- **[MEDIUM]** `Stats()` double-counts `WaitCount`/`WaitDuration`.
- **[LOW]** `runtime.SetFinalizer` on rows unreliable.

#### `internal/storage/postgres/base_repository.go`
- **[CRITICAL]** `fmt.Sprintf("SELECT ... FROM %s ...", tableName)` — SQL injection idiom.
- **[HIGH]** No tenant scoping on `GetByID`/`DeleteByID` — any tenant reads/deletes another's row.

#### `internal/storage/postgres/security.go`
- **[HIGH]** `containsSQLInjectionPatterns` naive substring — flags `"1=1"`, misses encoded/Unicode attacks. Security theater.

#### `internal/storage/postgres/tenant_guard.go`
- **[HIGH]** `ClearTenantContext` uses `SET` (session) not `SET LOCAL` — cleared context persists on pooled connections → RLS breaks.
- **[MEDIUM]** Manual SQL string escaping instead of `set_config('app.tenant_id', $1, true)`.

#### `internal/storage/postgres/vector.go`
- **[HIGH]** `validateSQLIdentifier` rejects hyphens → UUIDs can't be used as identifiers.
- **[HIGH]** Hardcoded `dim = 1536` vs migrations `VECTOR(1024)` — runtime failure.

#### `internal/storage/postgres/vector_utils.go`
- **[HIGH]** `FormatVector` `%.6f` loses precision for normalized embeddings — changes similarity scores.
- **[MEDIUM]** `NormalizeVector` returns original slice if `norm == 0`.

#### `internal/storage/postgres/write_buffer.go`
- **[CRITICAL]** On retry, failed batch DROPPED — silent data loss.
- **[HIGH]** `safeSend` uses `recover()` for control flow (anti-pattern).
- **[MEDIUM]** Zero-vector placeholders on embedding failure pollute index.

#### `internal/storage/postgres/embedding_queue.go`
- **[HIGH]** Package-level `// nolint:errcheck`.
- **[HIGH]** `MarkFailed` read-then-write with no transaction → race.
- **[MEDIUM]** Dead-letter move non-atomic across two tables.

#### `internal/storage/postgres/embedding/client.go`
- **[HIGH]** Package-level `// nolint:errcheck`.
- **[HIGH]** `EmbedBatch` prefix `"query"` vs `Embed` prefix `"query:"` — cache never hits across batch/single.

#### `internal/storage/postgres/embedding/cache.go`
- **[HIGH]** Uses Redis `KEYS` (O(N) over keyspace) — blocks Redis in production. Use `SCAN`.

#### `internal/storage/postgres/embedding/fallback.go`
- **[HIGH]** `SetStrategy` mutates without mutex — data race.

#### `internal/storage/postgres/recommend.go`
- **[HIGH]** Package-level `// nolint:errcheck`.
- **[MEDIUM]** Double error handling (logged then returned).

#### `internal/storage/postgres/services/retrieval_service.go`
- **[HIGH]** `:316` `_ = eg.Wait()` discards ALL errors from parallel search — silent partial/empty results.
- **[HIGH]** `allowedSynonymDir` package-level mutable, security-sensitive, raced.
- **[MEDIUM]** Logs every result before filtering (hot path).

#### `internal/storage/postgres/services/simple_retrieval_service.go`
- **[HIGH]** `SetEmbeddingPipeline` not mutex-protected — data race.
- **[MEDIUM]** `searchPrecision` returns nil error on vector search failure.

#### `internal/storage/postgres/services/retrieval_helpers.go`
- **[MEDIUM]** LRU eviction removes one item per miss (can exceed target under burst).
- **[MEDIUM]** `isWordChar` ASCII-only (not Unicode-safe for CJK).
- **[MEDIUM]** `replaceAllIgnoreCase:1525` O(n²) string concat.

#### `internal/storage/postgres/adapters/secret_adapter.go`
- **[HIGH]** `parseYAML:78` hand-rolled parser — silently misparses arrays/nested maps/quoted strings. Use `yaml.v3`.
- **[MEDIUM]** `convertToYAML` doesn't escape special chars.

#### `internal/storage/postgres/repositories/secret_repository.go`
- **[HIGH]** `RotateKey` doesn't update in-memory key → decrypts fail after rotation.
- **[HIGH]** Logs secret KEY NAMES at Info level.
- **[MEDIUM]** `decrypt`/`decryptSecret` duplicated.

#### `internal/storage/postgres/migrate.go` vs `migrate_storage.go`
- **[HIGH]** Duplicate table definitions with divergent RLS/indexes.

#### `internal/storage/postgres/repositories/*` (conversation, knowledge, experience, task_result, tool, strategy, distilled_memory)
- **[MEDIUM]** Pattern across all: silent `continue` on scan errors; 4-branch `Create` duplication; `err == sql.ErrNoRows` not `errors.Is`; `DeleteBatch` builds `IN (...)` via string concat.

#### `internal/storage/postgres/comprehensive_test.go`
- **[HIGH]** 5 core repository tests `t.Skip("requires real database")` → zero unit coverage of SQL layer.

#### `internal/retrievalservice/memory_repository.go`
- **[HIGH]** O(n²) bubble sort for ranking.
- **[MEDIUM]** `DeleteKnowledge`/`UpdateKnowledge` don't enforce tenant isolation.

#### `internal/retrievalservice/service.go`
- **[MEDIUM]** `ListKnowledge` `Total` = page size, not true total (misleading pagination).

#### `internal/memoryservice/memory_repository.go`
- **[MEDIUM]** `SearchSimilarTasks` matches ANY task with non-empty Input (hardcoded `matched = true`, `Score: 0.8`) — not similarity at all.

#### `internal/memoryservice/service.go`
- **[MEDIUM]** `SearchSimilarTasks` mutates caller's `query.Limit`.

#### `internal/ares_memory/context/rag.go`
- **[CRITICAL]** `:73` `Add` writes to pgvector BEFORE in-memory lock → concurrent adds corrupt state.
- **[MEDIUM]** `Delete` rebuilds entire index O(n).
- **[MEDIUM]** `evictOldest` removes first slice entry, not oldest by access time.

#### `internal/ares_memory/manager_impl.go`
- **[HIGH]** `StoreDistilledTask:513` errgroup goroutines return nil on error — caller sees success.
- **[MEDIUM]** `cosineSimilarity:591` dead code (duplicated in 2 other files).
- **[MEDIUM]** Session ID via `fmt.Sprintf("session_%s_%d", ...)` — not crypto-random.

#### `internal/ares_memory/production_manager.go`
- **[MEDIUM]** `sessionCache` LRU is O(n) scan.
- **[MEDIUM]** `AddMessage` falls back to `"anonymous"` userID silently.
- **[MEDIUM]** `BuildContext` O(n²) string concat.
- **[MEDIUM]** `err == sql.ErrNoRows` not `errors.Is`.

#### `internal/ares_memory/distillation/distiller.go`
- **[HIGH]** `topNBeforeConflictPhase:379` — `memories = memories[:len(filtered)]` then overwrites; stale data in tail if `filtered` shorter.
- **[HIGH]** `enforceSolutionCap:669,714` uses `Problem` as ID for deletion — not unique; deletes wrong solutions.
- **[MEDIUM]** `distillEg` never `Wait()`ed → goroutine leak.

#### `internal/ares_memory/service/distillation_service.go`
- **[HIGH]** `experienceRepositoryAdapter.SearchByVector` drops `Vector` field → conflict detection gets nothing.
- **[HIGH]** `UpdateConfig:268-287` doesn't propagate to internal distiller — silent no-op masquerading as success.
- **[MEDIUM]** `metrics` field read/written without mutex.
- **[MEDIUM]** `SearchMemories` creates memories with `ID: ""` "will be populated" but never is.

#### `internal/ares_memory/context/cleaner.go`
- **[MEDIUM]** `codePattern` regex doesn't match multi-line fenced blocks.
- **[MEDIUM]** `extractGist` splits mid-sentence for Chinese (no full-width punctuation).

#### `internal/ares_memory/distillation/{extractor,scorer,filter,classifier}.go`
- **[MEDIUM]** Byte-level truncation (`solution[:500]`, `msg.Content[:120]`) splits UTF-8.
- **[MEDIUM]** `SecurityFilter` misses encoded/Base64 secrets.
- **[MEDIUM]** `CodeBlockFilter`/`StacktraceFilter` high false-positive rate.
- **[MEDIUM]** Classification purely keyword-based.

---

### Module Group 5: Evolution & Quant
**Scope:** `internal/ares_evolution`, `internal/ares_quant`
**Files reviewed:** 204
**Counts:** Critical 2 · High 4 · Medium 15 · Low 6

#### `internal/ares_quant/portfolio/simulator.go`
- **[CRITICAL]** `:544-555` multi-asset routing ignores `signal.Symbol` — all signals applied to `symbols[0]`. Every multi-symbol backtest wrong.
- **[MEDIUM]** `:518-534` O(symbols × dates × bars) linear price lookup (~85M comparisons for 5yr/10-symbol).

#### `internal/ares_evolution/feedback_recorder.go`
- **[CRITICAL]** `:133-139` circuit breaker atomicity broken by unlock/service-call/relock → data race on `circuitBreakerConsecutiveErrors`.

#### `internal/ares_quant/research/evaluation.go`
- **[HIGH]** `:190-193` `drawdownAwareReturn` ADDS penalty instead of subtracting — inverted metric.

#### `internal/ares_quant/marketmaking_api/backtest.go`
- **[HIGH]** `:169` `mergeResponses` averages `TotalReturn` (mathematically wrong).
- **[MEDIUM]** `generateDefaultSignals:146-153` creates BUY for every day → nonsensical default.

#### `internal/ares_quant/marketmaking_api/client.go`
- **[HIGH]** `:196` quote loop context from `context.Background()` — not cancelled on caller cancel; goroutine leak.

#### `internal/ares_evolution/mutation/adaptive_distribution.go`
- **[HIGH]** `:405-415` normalize then re-clamp breaks sum-to-1.0 invariant.

#### `internal/ares_evolution/regression_tester.go`
- **[MEDIUM]** `:88-89` `rng` not thread-safe — data race under concurrent `Run`.

#### `internal/ares_quant/market/{polymarket,yahoo,coingecko}.go`
- **[MEDIUM]** HTTP requests without context (`context.Background()` or `http.NewRequest`).

#### `internal/ares_quant/marketmaking_api/paper.go`
- **[MEDIUM]** `:140,186` session fields accessed after lock release → race on `Positions` map.

#### `internal/ares_quant/research/memory_log.go`
- **[MEDIUM]** `:248-249` reentrant `RLock` deadlock risk; cross-ticker lessons queried then discarded (`_ = crossLessons`).

#### `internal/ares_evolution/scoring/memory_aware_scorer.go`
- **[MEDIUM]** `:502` `ScoreAsScorerFunc` uses `context.Background()` — LLM calls can't be cancelled.

#### `internal/ares_evolution/genome/selection.go`
- **[MEDIUM]** `:748` `PickParent` panics if `rng` nil.

#### `internal/ares_evolution/experience/aggregator.go`
- **[MEDIUM]** Cache grows unbounded (no eviction).

#### `internal/ares_evolution/experience/normalizer.go`
- **[MEDIUM]** `:211-251` `normalizeScore` divides by 100 if >1.0 — can't distinguish scales.

#### `internal/ares_evolution/pg_strategy_store.go`
- **[MEDIUM]** `:117` `GetHistory` ignores `id` parameter.

#### `internal/ares_quant/research/agents/prompt_builder.go`
- **[MEDIUM]** `:408` Chinese text in prompt (violates English convention).

#### `internal/ares_quant/marketmaking/chaos.go`
- **[MEDIUM]** `:347` `time.Sleep` holds mutex — blocks entire system during disconnect simulation.

#### `internal/ares_evolution/genome/multi_objective.go`
- **[MEDIUM]** `:38-39` `ParetoDominance` skips missing dimensions — inflates Pareto front.

#### `internal/ares_evolution/shadow_evaluator.go`
- **[MEDIUM]** `:310` `Reset` clears `shadowStrategy` but not `activeStrategy` — stale references.

#### `internal/ares_evolution/service/service_bridge.go`
- **[LOW]** `:105` `toAPILineage` always returns empty (stub).

#### `internal/ares_evolution/genome/hypothesis.go` / `meta_evolution.go`
- **[LOW]** `context.Background()` for debug logging.

---

### Module Group 6: Events, Eval, Arena & Experience
**Scope:** `internal/ares_events`, `internal/ares_eval`, `internal/ares_arena`, `internal/ares_experience`
**Files reviewed:** 82
**Counts:** Critical 0 · High 7 · Medium 18 · Low 22

#### `internal/ares_events/pg_store.go`
- **[HIGH]** `:199` `Subscribe` channel buffer size 1 — slow consumer deadlocks polling goroutine. (Memory store uses 64.)
- **[MEDIUM]** `:376-399` 1s polling latency (consider `LISTEN/NOTIFY`).

#### `internal/ares_events/memory_store.go`
- **[MEDIUM]** `:88-93` stores same `*Event` pointer in global + per-stream slices — callers mutate shared state.
- **[LOW]** `notifySubscribers` under write lock.

#### `internal/ares_events/compactable_store.go`
- **[MEDIUM]** `:90-103` compaction goroutine on `context.Background()`, no tracking, no graceful shutdown.

#### `internal/ares_events/compactor.go`
- **[MEDIUM]** `:188-196` `buildSummary` sets `AgentID="unknown"` on first event lacking ID; subsequent real IDs never replace it.
- **[LOW]** `:103-108` reads ALL events into memory (no batching).

#### `internal/ares_eval/loader.go`
- **[HIGH]** `:25` path traversal check only blocks `/etc/` — `../../../` passes. `// #nosec G304` unjustified.

#### `internal/ares_eval/service/handler.go`
- **[HIGH]** `:38,382,453,474` no body size limit — DoS.
- **[MEDIUM]** `:57-69` async eval on `context.Background()` with 30min timeout, no tracking/cancel.

#### `internal/ares_eval/service/service.go`
- **[HIGH]** `:387-392` `serviceAgentExecutor.Execute` is a stub returning `"executed: " + input` — eval service doesn't test agents.
- **[MEDIUM]** `:454-462` `isHardError` fragile substring matching.

#### `internal/ares_eval/llm_judge.go`
- **[MEDIUM]** `WithPrompt` silently falls back to default on parse failure.

#### `internal/ares_eval/dimension_judge.go`
- **[MEDIUM]** `:121-134` uses `strings.ReplaceAll` not `text/template` — input containing `{{.ExpectedOutput}}` breaks.

#### `internal/ares_eval/comparison.go`
- **[MEDIUM]** `NewComparisonRunner` doesn't validate nil `suite`/`runnerFactory`.
- **[MEDIUM]** `repository.go:297-300` `GetComparison` returns rows in random map order despite SQL `ORDER BY`.

#### `internal/ares_arena/service.go`
- **[HIGH]** `:272-274` `parseDuration` `case float64: return time.Duration(v)` — 5.0 (seconds) becomes 5 nanoseconds.
- **[MEDIUM]** `SetFlightBridge` writes `s.bridge` without lock.

#### `internal/ares_arena/http.go`
- **[MEDIUM]** `:380-410` `handleSurvivalStart` returns 202 even when no-op (already running).
- **[MEDIUM]** `:451-469` `handleScenarioRun` synchronous — blocks HTTP for minutes.
- **[MEDIUM]** `:35-61` all 23 routes unauthenticated.

#### `internal/ares_arena/regression.go`
- **[HIGH]** `:255` `batchTestCases := testCases[offset:offset+thisBatch]` panics if `cfg.TestCases` shorter than `maxRuns`.
- **[MEDIUM]** `:340-341` semaphore acquire not context-aware.

#### `internal/ares_arena/scenario.go`
- **[HIGH]** `:288-306` `checkVerified` only verifies `ExpectSuccess=true` actions — actions expecting failure are never checked.
- **[MEDIUM]** `:199,262` `calculateAvgRecoveryTime(nil)` always 0 → score artificially inflated.
- **[MEDIUM]** `:145-167` `ParallelActions`/`MaxConcurrent`/`DependsOn` not implemented (only warnings).

#### `internal/ares_arena/survival.go`
- **[MEDIUM]** `:81-82` `context.WithCancel` cancel never deferred — leaks.

#### `internal/ares_arena/scorer.go`
- **[MEDIUM]** `:69-76` `EnsembleScorer` clamps to [0,1] but arena uses 0-100 elsewhere — scale inconsistency.

#### `internal/ares_experience/distillation_service.go`
- **[HIGH]** `:85-88` dead code: `ShouldDistill` returns false for `!task.Success`, so `ExperienceTypeFailure` branch never reached — failure experiences never created.
- **[MEDIUM]** `:162` hardcoded 30s timeout.
- **[MEDIUM]** `:205-260` rigid line-by-line LLM response parsing.

#### `internal/ares_experience/{conflict_resolver,ranking_service}.go`
- **[MEDIUM]** `Configure` not thread-safe (races with `Rank`/`DetectConflictGroups`).
- **[MEDIUM]** `Rank` returns empty slice (not error) on length mismatch.

---

### Module Group 7: Infrastructure
**Scope:** `internal/ares_bootstrap`, `ares_integration`, `ares_mcp`, `ares_flight`, `ares_protocol`, `ares_ratelimit`, `ares_security`, `ares_observability`
**Files reviewed:** 96
**Counts:** Critical 3 · High 9 · Medium 24 · Low 25

#### `internal/ares_mcp/client.go`
- **[CRITICAL]** `:341-351` `handleNotification` deadlock — receive loop blocks on `ListTools` response only it can deliver (30s hang).
- **[HIGH]** `:317-338` `dispatchResponse` send-on-closed-channel race with `Close()`.
- **[MEDIUM]** `Connect:100` uses caller's ctx for `ListTools` instead of `c.ctx`.

#### `internal/ares_mcp/transport_sse.go`
- **[CRITICAL]** `:173-177` SSRF — SSE `endpoint` event redirects POSTs (with auth headers) to arbitrary URL.
- **[HIGH]** `:59-61` `http.Client.Timeout` kills SSE connections every 30s.
- **[MEDIUM]** `postURL` defaults to SSE URL.

#### `internal/ares_mcp/manager.go`
- **[HIGH]** `:130-134` `onChange` captures caller's ctx — fails after caller's request ends.
- **[MEDIUM]** `:267-272` direct access to `MCPClient` internals (encapsulation break).
- **[MEDIUM]** `:261-272` TOCTOU in `registerTools`.
- **[MEDIUM]** `:234-236` `Version = "connected"` misuses field.

#### `internal/ares_mcp/server.go`
- **[MEDIUM]** `:475` internal error details leaked to clients.
- **[LOW]** no input size limit on params.

#### `internal/ares_mcp/transport_server.go`
- **[HIGH]** `:335,443` send-on-closed-`requestCh` panic.
- **[MEDIUM]** `:157-173` goroutine leak in `StdioServerTransport.Close`.
- **[MEDIUM]** `sseClients` channel dead code.

#### `internal/ares_mcp/factory.go`
- **[HIGH]** `:94-124` client connection leaked — never stored/returned.
- **[MEDIUM]** non-deterministic tool selection (iterates map).

#### `internal/ares_mcp/config_watcher.go`
- **[MEDIUM]** `:124-129` blocking reload in event loop.

#### `internal/ares_security/sanitizer.go`
- **[HIGH]** `SanitizeOptions` (`KeepLength`, `MaskChar`, `PreserveLengthFor`) are dead code — `Sanitize` ignores them.
- **[HIGH]** `SanitizeJSON:149-157` calls `Sanitize` on raw JSON string — masking a value with `"`/`\` breaks JSON.
- **[MEDIUM]** regex compiled inside mask functions (9 sites).
- **[MEDIUM]** credit card regex high false-positive rate.
- **[MEDIUM]** entire security module is just a sanitizer — no auth/authz, no tool sandboxing, no SSRF protection, no audit logging for a system executing external tools.

#### `internal/ares_observability/otel_tracer.go`
- **[HIGH]** `WithTrace:266-269` leaks span (never `End()`ed).
- **[MEDIUM]** `:214` magic `1` instead of `codes.Error`.
- **[MEDIUM]** `Shutdown:290-292` `%v` not `%w`/`errors.Join`.

#### `internal/ares_observability/cost.go`
- **[MEDIUM]** `:497-498` brittle path parsing for session ID (use `r.PathValue`).
- **[LOW]** `Reset` doesn't update `createdAt`.

#### `internal/ares_observability/prometheus.go`
- **[MEDIUM]** `:86` `LLMTokensTotal` is a `GaugeVec` named `..._total` (counter convention).
- **[MEDIUM]** high-cardinality `session`/`model` labels → unbounded metric memory.

#### `internal/ares_observability/log.go`
- **[LOW]** logs raw input/output — may contain sensitive data.

#### `internal/ares_bootstrap/bootstrap.go`
- **[HIGH]** `:58-107` no cleanup on partial failure — started MCP manager, runtime, memory leak.
- **[MEDIUM]** `:65` `ProvideMemory(nil)` ignores memory config.

#### `internal/ares_bootstrap/provide_dashboard.go`
- **[HIGH]** `:45-49` `hubGrp` never `Wait()`ed — hub goroutine leaks.
- **[HIGH]** `:50-53` HTTP server has no `Addr` → binds `:http` (port 80).

#### `internal/ares_bootstrap/provide_evolution.go`
- **[MEDIUM]** `:65` `dreamCycle` always nil (dead code returned).
- **[MEDIUM]** `:93-104` evaluator registry and feedback service discarded.

#### `internal/ares_flight/collector.go`
- **[HIGH]** `:182-186` graph node lookup by `agentID` fails because start adds node with `ID: evt.ID` (event ID) — node status never updated, all agents show "running".
- **[MEDIUM]** `:151-167` timeline `Duration`/`EndAt` never set → `Summary()` always 0.
- **[LOW]** brittle `s[:5] == "tool."` instead of `strings.HasPrefix`.

#### `internal/ares_flight/genealogy.go`
- **[MEDIUM]** `:123-124` resurrection doesn't update children's `ParentID` → `Ancestors()` traverses to dead node.

#### `internal/ares_flight/replay.go`
- **[MEDIUM]** `:46-47` silent truncation at 10000 events.
- **[LOW]** not safe for concurrent use.

#### `internal/ares_protocol/ahp/queue.go`
- **[MEDIUM]** `Enqueue:55-75` ignores ctx (non-blocking send returns immediately).

#### `internal/ares_protocol/ahp/heartbeat.go`
- **[MEDIUM]** `:251-260` sends heartbeat after ctx cancelled.

#### `internal/ares_protocol/ahp/dlq.go`
- **[MEDIUM]** `RemoveBySession:141` no nil-check on `Message`.
- **[LOW]** `StartAutoRetry` blocks caller (misleading async name).

#### `internal/ares_ratelimit/token_bucket.go`
- **[MEDIUM]** `Wait:52-82` thundering herd + imprecise timing (waits full token period even if 0.9 available).

#### `internal/ares_ratelimit/sliding_window.go`
- **[MEDIUM]** `:24` ignores `Burst` config.

#### `internal/ares_ratelimit/semaphore.go`
- **[MEDIUM]** `Allow:60-67` doesn't acquire — check-then-act inconsistency.
- **[MEDIUM]** `Reset:75-90` races with `Acquire`.

---

### Module Group 8: Monitoring, Dashboard & Discovery
**Scope:** `internal/monitoring`, `internal/dashboard`, `internal/discovery`
**Files reviewed:** 93
**Counts:** Critical 6 · High 14 · Medium 22 · Low 18

#### `internal/monitoring/data/trace_linker.go`
- **[CRITICAL]** `:124,170,240` stale pointer into slice backing array — span durations silently lost (see CRITICAL #18).

#### `internal/monitoring/http_api.go`
- **[CRITICAL]** `:97-103` no auth on kill/resume/retry/MCP-call.
- **[HIGH]** `:381` SSE uses `context.Background()` — snapshot continues after client disconnect.
- **[MEDIUM]** `:383` SSE error payload not JSON-escaped.
- **[MEDIUM]** `:301` `err.Error() != "EOF"` instead of `errors.Is`.

#### `internal/monitoring/cost_bar.go`
- **[HIGH]** `:57` currency mixing — `Currency` set to last-seen; `EstimatedCost` sums across currencies.

#### `internal/monitoring/pruner.go`
- **[MEDIUM]** `MaxTracesPerAgent` documented but never enforced.
- **[MEDIUM]** `Stop()` doesn't wait for loop goroutine.

#### `internal/monitoring/publisher.go`
- **[HIGH]** `:31-38` `WSHub` interface incompatible with `dashboard.WSHub` (no adapter exists).
- **[MEDIUM]** `extractPathID:396` `strings.Trim` (cutset) not prefix/suffix.

#### `internal/monitoring/dag/engine.go`
- **[HIGH]** `Children()`/`Parents()` return raw pointers into node map — concurrent mutation unsafe.

#### `internal/monitoring/tabs/llm_tab.go`
- **[MEDIUM]** field name inconsistency (`cost`/`model` vs `estimated_cost`/`model_name`) → LLM tab records zero cost/model.

#### `internal/monitoring/test_constants.go`
- **[MEDIUM]** test constants in non-`_test.go` file ship in production binary.

#### `internal/dashboard/api.go`
- **[CRITICAL]** no auth on chaos/arena endpoints.
- **[HIGH]** weak WS `CheckOrigin` (substring match).
- **[MEDIUM]** CORS `*`.

#### `internal/dashboard/orchestrator.go`
- **[CRITICAL]** `:673` `failAgent` panics on nil error.
- **[HIGH]** `:705` `emitEvent` uses `context.Background()`.
- **[MEDIUM]** `:319` auto-resurrection recursion with no backoff.

#### `internal/dashboard/intelligence.go`
- **[CRITICAL]** `:454-471` `percentile` returns max, not p99.
- **[HIGH]** `:168,172,176` unbounded `restarts`/`errors`/`latencies` slice growth (memory leak + O(n) per observation).

#### `internal/dashboard/event_bridge.go`
- **[HIGH]** `:140-150` latency unit confusion (ns vs ms) → 5ms trips 5000ms threshold instantly.

#### `internal/dashboard/ws_hub.go`
- **[HIGH]** `Register`/`Unregister` block on unbuffered channels.
- **[HIGH]** `Stop()` doesn't wait for `Run`.
- **[MEDIUM]** silent message drops (3 sites, no metric).

#### `internal/dashboard/api_handlers.go`
- **[HIGH]** `handleArenaSurvivalStop` swallows error.
- **[HIGH]** `handleArenaAgentFault` swallows JSON decode error.

#### `internal/dashboard/service.go`
- **[MEDIUM]** `EnableAuth` field never read (dead config).

#### `internal/discovery/health.go`
- **[CRITICAL]** `:52-60` SSRF + arbitrary binary execution (no allowlist).

#### `internal/discovery/engine.go`
- **[MEDIUM]** `CheckHealth:169-199` sequential (N×5s worst case).
- **[MEDIUM]** `StartAutoDiscovery` no `Stop` method.

#### `internal/discovery/providers/binary.go`
- **[HIGH]** runs arbitrary binaries without whitelist.
- **[MEDIUM]** no global timeout.

#### `internal/discovery/providers/filesystem.go`
- **[HIGH]** errors silently swallowed.
- **[MEDIUM]** no file size limit.

---

### Module Group 9: Knowledge, Workflow & Plugins
**Scope:** `internal/knowledge`, `internal/workflow`, `internal/plugins`
**Files reviewed:** 89
**Counts:** Critical 3 · High 9 · Medium 22 · Low 14

#### `internal/knowledge/provider/postgres/provider.go`
- **[CRITICAL]** `:119-143` SQL injection — column/table names via `fmt.Sprintf`; `FROM %s` uses ID column as table name (bug).

#### `internal/knowledge/runtime/lazy_graph.go`
- **[CRITICAL]** `:77-95` `expanded: true` in constructor makes `expandFn` unreachable — lazy loading completely broken.

#### `internal/workflow/graph/node.go`
- **[CRITICAL]** `:267-271` non-deterministic `hashInput` (`fmt.Sprintf("%v", map)`) → breaks `tool_call_id`/caching.

#### `internal/knowledge/pipeline.go`
- **[HIGH]** `:96-102` nil `obj` propagated to next normalizer on error → nil-panic.
- **[MEDIUM]** `:124` `break` unconditionally after first matcher (only first matcher ever consulted).
- **[MEDIUM]** `:140` `ProcessStream` ignores `ctx.Done()`.

#### `internal/knowledge/runtime/runtime.go`
- **[HIGH]** `:108` `errCh` never written to (dead code) — all errors lost.
- **[HIGH]** `:131-160` goroutine leak on early return — producers block forever.

#### `internal/knowledge/provider/code/provider.go`
- **[HIGH]** `:166-198` only first `TypeSpec` in grouped `type ( A int; B string )` emitted — incomplete graph.

#### `internal/knowledge/store/memory/store.go`
- **[HIGH]** contract violation: `Get` returns `ErrObjectNotFound` but interface doc says "nil, nil if not found".
- **[MEDIUM]** `Query` ignores `Offset`; `Search` ignores `model`; `Delete` prefix-matches `id+":"`.

#### `internal/knowledge/compiler/compiler.go`
- **[MEDIUM]** `formatJSON:165-200` `fmt.Fprintf %q` not valid JSON for control chars.

#### `internal/knowledge/mcp/mcp.go`
- **[MEDIUM]** `:159-164` `nodeIDs` iterates map → non-deterministic order.

#### `internal/workflow/graph/executor.go`
- **[MEDIUM]** `:419-430` O(N×M) per completed node (`hasAnySatisfiedEdge` scans all edges).
- **[MEDIUM]** `:53` holds `RLock` for entire execution duration.
- **[LOW]** `:200` silent break on empty scheduler.

#### `internal/workflow/graphservice/service.go`
- **[HIGH]** `:120-129` `SetTracer`/`SetLimiter` called per `Execute` — races with read lock.

#### `internal/workflow/engine/executor.go`
- **[HIGH]** `:318-324` cancellation not honored — blocked on `sem <- struct{}{}` never notices ctx.
- **[MEDIUM]** `:172` `collectStepResults` length mismatch if routing adds steps.

#### `internal/workflow/engine/registry.go`
- **[HIGH]** `:112-118` hardcodes `*models.RecommendResult` assertion — registry unusable for other result types.

#### `internal/workflow/engine/loader.go`
- **[HIGH]** `:91-93` path traversal `strings.HasPrefix`.
- **[LOW]** `dir + "/" + name` instead of `filepath.Join`; `getFileExt` reinvents `filepath.Ext`.

#### `internal/workflow/engine/dynamic_executor.go`
- **[HIGH]** `:276-563` `execLoop` ~290 lines, very high complexity.

#### `internal/workflow/engine/reloader.go`
- **[HIGH]** `:130,196` `FileWatcher.Close` closes watcher twice.

#### `internal/workflow/engine/definition.go`
- **[MEDIUM]** regex compiled inside loops; `ParseBytes` ignores ctx.

#### `internal/workflow/engine/executor_options.go`
- **[MEDIUM]** `DynamicExecutor` setters not mutex-protected; `maxParallel: 1` (vs `NewExecutor`'s 10).

#### `internal/plugins/resurrection/resurrection.go`
- **[HIGH]** `:470-592` `resurrect` reads `s.agents[agentID]` twice under RLock — old agent reference may change between reads → double-stop/resource leak.
- **[MEDIUM]** `:598-623` `verifyResurrection` recursive without backoff.
- **[MEDIUM]** `:227-239` `CheckHealth` return value discarded.

#### `internal/plugins/resurrection/snapshot_store.go`
- **[MEDIUM]** `Save`/`Load` shallow-copy maps — nested mutation corrupts stored data.

---

### Module Group 10: API, CMD & SDK
**Scope:** `api/`, `cmd/`, `sdk/`
**Files reviewed:** 108
**Counts:** Critical 4 · High 16 · Medium 36 · Low 16

#### `api/mcp/mcp.go`
- **[CRITICAL]** `ListTools` writes `c.tools` without mutex — data race.
- **[HIGH]** `sendNotification` uses `context.Background()`.
- **[MEDIUM]** `CallTool` returns both error-result AND Go error.

#### `api/mcp/stdio.go`
- **[CRITICAL]** `:80-91` goroutine leaks on timeout (blocked on `stdout.Scan()`).
- **[HIGH]** `:101-105` `close()` kills process but never `cmd.Wait()` → zombie.
- **[MEDIUM]** hardcoded 30s timeout.

#### `api/tools/builtin.go`
- **[CRITICAL]** `file_tools` arbitrary FS access (no sandbox).
- **[HIGH]** `web_search` SSRF; `regex` no input limits.
- **[MEDIUM]** `calculator` tokenizer doesn't handle floats.

#### `api/handler/evolution.go`
- **[HIGH]** `:125-127` `writeJSON` calls `http.Error` after `WriteHeader` — corrupts response across all handlers.

#### `api/handler/stream.go`
- **[HIGH]** `:82` CORS `*` with no auth.

#### `api/bootstrap/bootstrap.go`
- **[HIGH]** `Stop()` doesn't stop Evolution or Dashboard.
- **[MEDIUM]** swallows all errors; raw seconds instead of `time.Duration`.

#### `api/client/client.go` / `config.go`
- **[HIGH]** `Close()` only sets flag — child services leak.
- **[HIGH]** path traversal `strings.HasPrefix`.

#### `api/service/agent/service.go`
- **[HIGH]** `ExecuteTask:133` stores results in unbounded map (OOM).
- **[HIGH]** `UpdateAgent:71-85` ignores `updates` param (silent no-op).
- **[MEDIUM]** `ListAgents` returns nil (JSON `null` not `[]`).

#### `api/service/workflow/service.go`
- **[MEDIUM]** `buildEngineSteps` called twice per execution.
- **[MEDIUM]** `ExecuteStream` event channel can block.

#### `sdk/sdk.go`
- **[HIGH]** `:487` hardcoded `maxIter=10` not configurable.
- **[MEDIUM]** `Stream:192-216` simulates streaming (chunks final output, not real streaming).
- **[MEDIUM]** `:573` tool result `%v` not JSON.
- **[MEDIUM]** `parseArgs` swallows JSON errors.
- **[MEDIUM]** `toCoreTools` generates empty parameter schemas.

#### `sdk/config.go`
- **[HIGH]** API key plaintext in YAML.

#### `sdk/team.go`
- **[HIGH]** `:83-108` member agents' tools never passed to LLM — tool-armed members useless.
- **[MEDIUM]** sequential, not parallel; errors collected as strings.

#### `cmd/ares/serve.go` / `cmd/monitor-live/main.go`
- **[HIGH]** `createLLMAdapterWithFallback` uses `log.Fatalf` (kills process).
- **[HIGH]** no graceful HTTP shutdown.
- **[MEDIUM]** errgroup `g` never `Wait()`ed.
- **[CRITICAL]** (monitor-live) massive code duplication with `cmd/ares/serve.go`.

#### `cmd/ares/dev.go`
- **[HIGH]** `doctor` `ok` variable never set to false — always reports "Environment looks good".
- **[MEDIUM]** `--config=value` not handled; `fmt.Scanln` stops at space.

#### `cmd/ares/agents.go`
- **[HIGH]** `createAgents` uses `log.Fatalf`.

#### `cmd/ares/arena.go`
- **[HIGH]** `http.Get` without context (noctx).

#### `cmd/ares/db_check_rls.go` / `cmd/check_rls/` / `cmd/create_distilled_table/` / `cmd/migrate_db/` / `cmd/setup_test_db/`
- **[HIGH]** hardcoded `postgres/postgres` credentials.
- **[MEDIUM]** `connectAdmin`/`ensureDatabase` call `os.Exit(1)` — prevents cleanup/testing.

#### `cmd/embedding-mcp/main.go`
- **[HIGH]** HTTP requests without context.
- **[MEDIUM]** no body size limit; `embeddingURL` not validated (SSRF).

#### `cmd/monitor-live/actions.go`
- **[MEDIUM]** no auth on chaos endpoints; `rand.Intn` without seed.

#### `cmd/flight/main.go` / `cmd/mcp-null/main.go`
- **[MEDIUM]** duplicated with `cmd/ares/flight.go` / `cmd/ares/mcp_null.go`.

---

### Module Group 11: Examples, Evaluation & Services
**Scope:** `examples/`, `evaluation/`, `services/embedding/`
**Files reviewed:** ~30
**Counts:** Critical 0 · High 6 · Medium 13 · Low 25

#### `services/embedding/app.py`
- **[HIGH]** `:391,56,51` cache key uses `MODEL_NAME` instead of `OLLAMA_MODEL` — stale embeddings when Ollama model changes.
- **[HIGH]** `:73-90` sync `requests.post` in async endpoints — blocks event loop.
- **[HIGH]** `:304-321` `embed_batch` generates ALL then checks cache — cache never read for hits.
- **[MEDIUM]** `:38-44` CORS `*` with `allow_credentials=True` (invalid per spec).
- **[MEDIUM]** no batch size limit (DoS).
- **[MEDIUM]** inconsistent vector normalization between backends.
- **[LOW]** `EMBEDDING_DIM`/`BATCH_SIZE` dead vars; sequential `embed_batch`.

#### `services/embedding/config.py`
- **[MEDIUM]** entire module dead code (never imported by `app.py`).

#### `services/embedding/test_service.py`
- **[MEDIUM]** `test_normalization:98-125` logic broken — collects booleans into set, `sum` at most 1.

#### `examples/01-quickstart/main.go`
- **[MEDIUM]** `:87` calculator returns hardcoded `"445"` regardless of expression.

#### `examples/02-tool-calling/main.go`
- **[HIGH]** `:181-184` `parseMulDiv` always errors — calculator never works.
- **[MEDIUM]** dead parser functions with misleading comments.

#### `examples/07-human-in-loop/main.go`
- **[HIGH]** `:119-130` `read_file` no path validation — path traversal.
- **[MEDIUM]** `:132-142` `delete_file` no path validation — destructive.

#### `examples/09-full-app/main.go`
- **[MEDIUM]** `:114-116` `app.history` unbounded (memory leak).
- **[MEDIUM]** `:101` `http.Error(w, err.Error(), 500)` leaks internal errors.

#### `examples/eval/main.go`
- **[HIGH]** `:79` accepts `"345"` as correct for `15*23+100` (=445) — wrong answers scored correct.
- **[MEDIUM]** `Latency` only measures "before" run; swallowed errors lose context.

#### `examples/custom-store/main.go`
- **[MEDIUM]** `JSONFileStore` no concurrency protection — data loss on concurrent saves.
- **[MEDIUM]** `load()` returns `nil, nil` on read error.

#### `examples/external-tools/main.go`
- **[MEDIUM]** `:95` `defer client.Close()` inside loop accumulates — all connections open until `main` returns.

#### `examples/knowledge-fabric/main.go`
- **[MEDIUM]** `IntentMatch:197-206` dead code — provider names don't match switch cases.

#### `evaluation/evaluation.go`
- **[MEDIUM]** `:36` `Register` panics on nil `*Scenario`.
- **[LOW]** `RunAll` discards partial results on first error.

#### `evaluation/runner.go`
- **[MEDIUM]** `:32-38` goroutine leak when runner ignores ctx.

---

## Prioritized Recommendations

### P0 — Block production deployment (fix immediately)

1. **Sandbox the tool-execution layer.** `CodeRunner` needs real isolation (nsjail/gVisor/seccomp/cgroups), `FileTools` needs `filepath.EvalSymlinks` + `filepath.Rel` path validation + mandatory `allowedDir`, `HTTPRequest`/`WebScraper`/`WebSearch` need SSRF protection (reject private/loopback/link-local IPs, redirect policy, `io.LimitReader`). Stop registering them with default-permissive settings in `builtin.go`.
2. **Add authentication to every HTTP endpoint.** Both monitoring and dashboard APIs expose kill/chaos/MCP-call operations with no auth, weak WS origin checks, and `CORS: *`. Gate state-changing routes behind token/session middleware.
3. **Fix the duplicate sentinel errors** — pick one package (`internal/errors` or `internal/core/errors`) as the source of truth; re-export from the other. `errors.Is` across packages is currently broken.
4. **Fix the data-corruption bugs:** trace_linker stale pointer (lost span durations), `percentile` returns max not p99, `drawdownAwareReturn` sign error, multi-asset signal routing, `write_buffer` data loss on retry, `vector.go` dimension mismatch (1536 vs 1024).
5. **Fix tenant isolation holes:** `ClearTenantContext` must use `SET LOCAL`; `base_repository` `GetByID`/`DeleteByID` must scope by `tenantID`; `retrievalservice` `Update`/`Delete` must verify ownership.
6. **Remove hardcoded DB credentials** from all `cmd/*` and `config.go` defaults. Require env vars with no insecure defaults.
7. **Fix the panics in normal code paths:** `HandleError` nil `Code`, `failAgent` nil `err`, `service_impl.go` type assertion, `regression.go` slice out-of-bounds, `PickParent` nil rng, `bootstrap.go` partial-failure leaks.

### P1 — Correctness & concurrency (fix soon)

8. **Fix concurrency bugs:** MCP client deadlock (`handleNotification`), `executePhase` send-on-closed-channel, leader `ProcessStream` lock scope, leader `Start`/`Stop` race, `manager.go` cancel leak + write-under-RLock, `feedback_recorder` circuit breaker atomicity, calculator `compiled` map, `SimpleRetrievalService.SetEmbeddingPipeline`, `DistillationServiceImpl.metrics`.
9. **Eliminate `context.Background()` misuse** — establish a project rule that all I/O functions accept and propagate `context.Context`. Audit the 15+ sites.
10. **Fix path traversal** in all 5+ sites with the `filepath.Rel` pattern.
11. **Fix goroutine leaks:** make `Stop()` wait for loop goroutines (`pruner`, `collector`, `ws_hub.Run`, `provide_dashboard` hub, `compactable_store`), buffer `ws_hub` `Register`/`Unregister` channels, `cmd.Wait()` after `Kill()` in `api/mcp/stdio.go`.
12. **Fix `WrapError`** to preserve the unwrap chain (`errors.Is(result, base)` must work).
13. **Fix the broken features:** lazy_graph (`expandFn` unreachable), distillation failure experiences (dead code), `checkVerified` (only checks `ExpectSuccess=true`), `UpdateConfig` no-op, `experienceRepositoryAdapter` vector loss.

### P2 — Hardening & cleanup

14. **Precompile all regex** at package level (20+ sites compile per call/per loop).
15. **Add `io.LimitReader`** to all `io.ReadAll` on external responses (10+ sites).
16. **Remove all package-level `// nolint:errcheck`** — audit every suppressed error.
17. **Replace `err == sql.ErrNoRows`** with `errors.Is` (8+ sites).
18. **Parameterize SQL** — eliminate `fmt.Sprintf` table/column interpolation; use `pq.QuoteIdentifier` + whitelist.
19. **Consolidate code duplication:** `cmd/ares` ↔ `cmd/monitor-live`, `cmd/flight` ↔ `cmd/ares/flight`, `migrate.go` ↔ `migrate_storage.go`, duplicated helpers.
20. **Replace `log.Fatalf`/`os.Exit(1)`** in constructors with error returns.
21. **Make string truncation Unicode-safe** (replace `s[:N]` byte-slicing with rune-aware truncation in `extractor.go`, `distiller_admin.go`, `cleaner.go`, `memory_tools.go`).
22. **Fix examples** that ship broken/misleading tools (hardcoded calculator, always-failing parser, path-traversing file tools, wrong eval acceptance).
23. **Add real unit test coverage** for postgres repositories (currently all `t.Skip` without a live DB).
24. **Translate Chinese** in prompts/user-facing strings to English (project convention).

### P3 — Design improvements

25. **Split the 23-method `ConsoleAPI`** into role interfaces.
26. **Add bounded eviction** to all caches (`experience/aggregator`, `monitoring` slices, `distiller` metrics).
27. **Make `maxIter`, timeouts, backoff caps, scoring weights configurable** (currently hardcoded throughout).
28. **Implement the security module gap** — auth/authz framework, tool sandboxing, SSRF protection, audit logging. The current `ares_security` package is only a sanitizer.

---

## Appendix: Agent Coverage

| # | Module Group | Files | Crit | High | Med | Low | Info |
|---|---|---:|---:|---:|---:|---:|---:|
| 1 | Foundation & Core | 38 | 4 | 9 | 28 | 30 | 0 |
| 2 | Agents & Runtime | 76 | 1 | 4 | 16 | 18 | 1 |
| 3 | LLM & Tools | 64 | 9 | 17 | 33 | 22 | 11 |
| 4 | Storage & Memory | 167 | 5 | 21 | 56 | 28 | 22 |
| 5 | Evolution & Quant | 204 | 2 | 4 | 15 | 6 | 0 |
| 6 | Events/Eval/Arena/Experience | 82 | 0 | 7 | 18 | 22 | 9 |
| 7 | Infrastructure | 96 | 3 | 9 | 24 | 25 | 11 |
| 8 | Monitoring/Dashboard/Discovery | 93 | 6 | 14 | 22 | 18 | 9 |
| 9 | Knowledge/Workflow/Plugins | 89 | 3 | 9 | 22 | 14 | 16 |
| 10 | API/CMD/SDK | 108 | 4 | 16 | 36 | 16 | 15 |
| 11 | Examples/Evaluation/Services | ~30 | 0 | 6 | 13 | 25 | 10 |
| **Total** | | **~1047** | **37** | **116** | **283** | **224** | **104** |

*Every Go file in the project (excluding `vendor/`, `.venv/`, generated code) and the Python embedding service was read end-to-end by a dedicated review agent. Findings reference `file:line` where possible; line numbers are as of the review date.*
