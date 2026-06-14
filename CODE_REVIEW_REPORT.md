# Code Review Report

Generated: 2026-06-14
Scope: All non-test Go files under `internal/security/`, `internal/shutdown/`, `internal/storage/`, `internal/tools/resources/`, `internal/workflow/`

---

## 1. Workflow Engine (`internal/workflow/engine/`)

### `types.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 12-26 | Dead Code | Low | `ErrInterruptRejected`, `ErrInterruptStoreNil`, `ErrInterruptHandlerNil`, `ErrInterruptNotFound`, `ErrInterruptPointNil` defined here but never referenced via `goagentx/internal/core/errors` — they shadow the sentinel errors used in `executor.go` and `hitl.go` | Remove unreferenced sentinel errors or verify they are used |
| 142-152 | Tech Debt | Low | `DAG` struct duplicated from `workflow/graph/graph.go` — two separate DAG implementations exist with similar functionality | Consider consolidating into a single DAG package |

### `registry.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 98 | Bug | Medium | `CreateAgent(ctx, step.AgentType, step.Input)` passes `step.Input` (a string) as the `config interface{}` parameter, but `Step.Input` is the raw input string, not an agent config | Likely should pass `nil` or a proper config object |
| 103 | Bug | Medium | `agent.Process(ctx, input)` — the `base.Agent` interface method signature is undefined; potential type assertion failure at L112 | Verify `base.Agent.Process` signature matches this call site |
| 112-117 | Bug | Medium | Asserting `result.(*models.RecommendResult)` assumes all agents return `*models.RecommendResult` — this is an opaque coupling and will panic if a different result type is returned | Use a more generic interface or type-switch |
| 121-125 | Dead Code | Low | `StepOutput.Variables` is always set to `make(map[string]interface{})` at L439 and never populated | Remove unused field or implement variable passthrough |

### `executor.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 34 | Bug | Low | `DefaultMaxParallel` may not be imported — it's in `constants.go` in the same package, so this is fine | Verify constant exists |
| 80-81 | Tech Debt | Low | `resultChan` capacity = `len(workflow.Steps)` but dynamic graphs may exceed this; buffer is under-dimensioned for `DynamicExecutor` | Use unbuffered or larger channel |
| 110-121 | Bug | Medium | On step failure, the error message at L120 reports `result.Error` but `result.Error` at this point should be empty string (set at L434). The actual error goes via `fmt.Errorf` and is returned, not via `result.Error` | Set `result.Error` at L108 before using it |
| 174-331 | Tech Debt | Medium | `runSteps` uses a complex semaphore + stepDone channel pattern that is fragile; the 5-second deadlock timeout is arbitrary | Use a proper topological scheduler instead |
| 414-419 | Bug | Low | `completedCopy` is created inside `executeStepCore` but never used — the function uses `completed` map directly in `resolveInput` at L420, which races with the main loop mutating `completed` | Remove `completedCopy` or pass to `resolveInput` |
| 447-477 | Tech Debt | Low | `resolveInput` takes `completed map[string]bool` but only uses it in `replaceTemplateVariables` to iterate keys — it does not check if the key is in the output store | Simplify to just iterate `outputStore` |

### `loader.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 91-92 | Bug | Medium | Path traversal check at L91 uses `strings.HasPrefix(absPath, absDir)` which is bypassable (e.g., `/allowed/../evil` is already resolved by `filepath.Abs` so this actually works correctly) | Correct as-is, but add test |
| 254-261 | Tech Debt | Low | `getFileExt` reimplements `filepath.Ext` from stdlib | Replace with `filepath.Ext` |

### `reloader.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 44-66 | Bug | Medium | `FileWatcher` constructor panics if loader is nil but also allows `watcher` to be nil when `fsnotify.NewWatcher()` fails — subsequent nil-check on `w.watcher` at L78 works but the panic is never hit because `w.Watch` returns an error before using it | Replace panic with returned error |
| 336-339 | Bug | Medium | `WorkflowReloader.Load()` does `r.loader.(*FileLoader)` — this is a hard type assertion that panics if loader is not `*FileLoader` | Use safe type assertion with ok check |
| 375 | Bug | Low | `watcher.Watch(r.cancelCtx, dir)` uses the reloader's internal `cancelCtx` — but `Watch` also takes a `ctx` parameter; the ctx passed to `StartWatching` is ignored | Pass the method's `ctx` parameter |

### `definition.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 99-114 | Bug | Low | `extractField` compiles regex inside a loop — repeated compilation on each call | Compile once outside |
| 137, 168 | Tech Debt | Low | `regexp.MustCompile` in a hot loop inside `extractPrompts` and `extractTools` — these patterns are static | Extract to package-level `var` |
| 242 | Tech Debt | Low | `dir + "/" + entry.Name()` — uses string concatenation instead of `filepath.Join` | Use `filepath.Join` |

### `dynamic_executor.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 55-57 | Bug | Medium | `NewDynamicExecutor` constructs an `Executor{}` directly instead of using `NewExecutor()`, so `DefaultMaxParallel` and `DefaultStepTimeout` defaults are duplicated | Call `NewExecutor()` |
| 168-274 | Tech Debt | High | Result collection loop is overly complex with nested selects, manual collection counting, and re-reading `currentOrder` under lock multiple times — error-prone | Refactor to simpler pattern |
| 576-619 | Tech Debt | Low | `recomputeOrder` only appends new steps — it does not handle removed steps | Add logic to skip removed steps in currentOrder |

### `mutable_dag.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 82-88, 94-100 | Tech Debt | Low | Rollback logic in `AddNode` is duplicated twice — once for invalid dependency, once for cycle detection | Extract to helper method |
| 161-163 | Bug | Medium | `RemoveNode` iterates `m.steps` to find dependents — but at L161, `m.dag.Edges[id]` gives outgoing edges only. Incoming edges to `id` are removed at L167-177 by scanning all src edges. However `m.steps` stores `DependsOn` (incoming), not outgoing — so the dependent check at L140-157 checks if `id` appears in any step's `DependsOn` list (correct) | Logic is correct for dependents, but updating `DependsOn` on removal is missed — the error message says "has dependents" when `id` is a *dependency* of another step, which is correct |
| 238-239 | Tech Debt | Low | L238-239 update `step.DependsOn` for `AddEdge`, but `RemoveEdge` does the same correctly | Consistent |

### `hitl.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 1 | Tech Debt | Low | Line 1 has a comment before the package declaration that is not a doc comment | Remove or move inside |
| 156-163 | Bug | Low | `ListPending` checks for results under `s.mu.RLock()` but iterates `s.points` and then checks `s.results` — both are under read lock, consistent | OK |

### `graph_events.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 85-96 | Tech Debt | Low | `Publish` drops events on full buffer without any backpressure or subscriber notification | Consider logging dropped events |

---

## 2. Workflow Graph (`internal/workflow/graph/`) — Previously Reviewed

### `scheduler.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 42-68 | Bug | High | `Schedule` uses `time.After` inside a `for` loop for each step poll — creates a new timer on every iteration when step is not ready, causing memory accumulation under fast-poll scenarios | Use `time.NewTicker` or a single timer |
| 70-95 | Bug | High | `executeStep` swallows panics with `recover()` but assigns the error string to a local `err` variable inside the deferred func — the calling code (`Schedule`) never sees the panic because the deferred recover runs before `wg.Done()` but the result is lost | The panic result needs to be sent to the error channel |

---

## 3. Tools Resources (`internal/tools/resources/`)

### `resources.go` (resources.go)

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 1-103 | Tech Debt | Low | Entire file is type/function aliases re-exporting from sub-packages — this adds an indirection layer for consumers | Consider whether this is needed |

### `core/registry.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 104-138 | Bug | Medium | `Filter` with nil filter returns a new Registry sharing the same `tools` map as the parent — mutations to one affect the other | Deep-copy the map on nil filter |
| 200-220 | Tech Debt | Low | Global mutable state (`GlobalRegistry`) used without initialization control — tests may interfere | Use constructor injection |

### `core/capability.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 102-121 | Tech Debt | Low | `Detect` iterates all capabilities and all keywords on every query — performance issue for frequent calls | Consider building a trie or inverted index |
| 175-193 | Bug | Medium | `buildCapabilityMap` is not automatically called when new tools are registered after `CapabilityEngine` construction — `Rebuild()` must be called manually | Subscribe to registry events or rebuild on every `ToolsFor` call |

### `core/result.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 33-38 | Tech Debt | Low | `WithMetadata` uses pointer receiver on `Result` (value type) — confusing API since callers must use `&result` or chaining pattern that returns `*Result` | Make all methods value receivers or consistently pointer |

### `base/base_tool.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 109-113 | Tech Debt | Low | `WithMetadata` wraps tool in `metadataTool` which embeds `core.Tool` interface — `IsDeprecated()` method on `metadataTool` is unreachable unless caller type-asserts | Not a bug, but the interface doesn't expose `IsDeprecated` |

### `agent/agent_tools.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 58 | Tech Debt | Low | `filteredRegistry := core.GlobalRegistry.Filter(filter)` — creates a new registry on every `NewAgentTools` call, but the filter with nil filter shares the global map (see registry.go L104) | Consider pre-filtered view |
| 240-247 | Tech Debt | Low | `RegisterBuiltinToolsForAgent` calls `builtin.RegisterGeneralTools()` which calls `core.Register()` (global registry) — side effects on global state | Use a dedicated agent registry |

### `builtin/builtin.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 30 | Bug | Medium | `builtin_network.NewWebScraper(builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30 * time.Second)))` — the WebScraper's default timeout (30s) and the HTTP client's timeout (30s) may stack | Either is fine; no fix needed |
| 62 | Tech Debt | Low | Comment says "Domain capability — removed" but these tool types are never implemented | Remove dead code comment or implement |

### `builtin/text/regex_tool.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 98-111 | Bug | Medium | `compileRegex` builds flags by prepending `(?` + flag + `)` — but `(?i)` is correct, `(?m)` is correct, `(?s)` is correct. However the user passes flags as individual strings via `getStringSlice` which works. OK | No fix needed, pattern is valid |

### `builtin/text/log_analyzer.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 50 | Tech Debt | Low | `LogAnalyzer` has no capabilities set — uses `NewBaseToolWithCategory` instead of `NewBaseToolWithCapabilities` | Add `CapabilityText` |

### `builtin/text/data_validation.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 106 | Tech Debt | Medium | RFC 5322 email regex is simplified — misses valid emails like `"test@test"@example.com` (quoted local parts) | Document limitations or use a proper email validation library |
| 163 | Bug | Low | URL regex does not allow `localhost` or IP-based URLs | Update regex or use `net/url.Parse` |

### `builtin/network/http_request.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 96-105 | Bug | Medium | Body JSON validation at L98-103 validates but discards the result — validates JSON just to return an error, then passes raw string anyway | Either remove validation or reject invalid JSON |
| 115 | Tech Debt | Low | Hard-coded `Content-Type: application/json` default for non-GET requests | Make configurable |

### `builtin/network/web_scraper.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 186-188 | Tech Debt | Low | Uses regex to parse HTML — `<script[^>]*>.*?</script>` fails on nested scripts or script tags in comments | Should use an HTML parser like `golang.org/x/net/html` |
| 190-193 | Bug | Medium | Same regex issue for `<nav>`, `<header>`, `<footer>` — `<nav class="main">` is matched but `.*?` is non-greedy, which fails on nested elements | Use proper HTML parser |
| 215-231 | Tech Debt | Low | Link extraction via regex — same fragility | Use `golang.org/x/net/html` |

### `builtin/file/file_tools.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 141-144 | Bug | Medium | `allowedDir` check reads `filePath` which may already be absolute from L118-124 conversion — but L142 calls `filepath.Abs` again which is a no-op for absolute paths | Inefficient but correct (redundant Abs call) |
| 222-227 | Bug | Medium | Same redundant `filepath.Abs` calls in `writeFile` | Remove redundant calls |
| 145-146 | Bug | Medium | Only `readFile` resolves `allowedDir` via `filepath.Abs` — the `writeFile` check at L227 also does this correctly | OK |

### `builtin/memory/memory_tools.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 183-192 | Bug | Low | Hard-coded Chinese-language keyword matching (`"精通"`, `"擅长"`) — not internationalized | Make keyword detection configurable |
| 281-313 | Tech Debt | Low | `extractPreferences` uses Chinese-language substring matching — same localization issue | Generalize |

### `builtin/knowledge/knowledge_base.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 12-20 | Tech Debt | Low | `RetrievalResult` type is locally redefined to avoid an import cycle — indicates a package design issue | Extract shared types to a common package |
| 107 | Bug | Low | `top_k` and `min_score` params are declared in the schema but never used — `searcher.Search(ctx, tenantID, query)` doesn't accept them | Pass parameters or remove from schema |

### `builtin/execution/code_runner.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 153-236 | Bug | High | The `validateCode` method uses simplistic substring matching that is trivially bypassed (e.g., `imp` + `ort os` concatenation, base64-encoded code) and also blocks benign code (e.g., `open("file.txt", "r")` has `"open("`) | This sandbox provides false security — document as best-effort or use actual sandboxing (containers, gVisor) |
| 196, 201, 224 | Bug | Medium | String matching with lowercase fails for mixed-case obfuscation, but more critically, patterns like `"open("` block legitimate code like `open("file.txt")` | Use AST-level analysis or document limitations |
| 202 | Bug | Medium | `"from " && " import "` — blocks `import os` combined with `from X import Y`, but also blocks `from X import Y` for safe modules | Overly broad |

### `builtin/planning/task_planner.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 59 | Bug | Low | `TaskPlanner` has `CapabilityMath` as its only capability — should be `CapabilityKnowledge` or a new planning capability | Fix capability |
| 395-438 | Tech Debt | Low | `extractJSON` is a manual JSON extractor — fragile if the LLM returns nested braces inside strings with unbalanced quotes | Use a more robust extraction method |

### `formatter/result_formatter.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 204 | Bug | Low | Hard-coded emoji characters (📁, 📄) — will render incorrectly in text-only terminals | Make emoji output optional |
| 405-433 | Tech Debt | Low | `convertToMapSlice` uses JSON marshal/unmarshal as fallback for type conversion — fails silently on marshal failure | Return error instead of nil |

---

## 4. Security (`internal/security/sanitizer.go`)

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 10-12 | Bug | Low | Regex patterns compiled at package init — fine for startup, but `MustCompile` panics on invalid regex | Consider `Compile` with error handling |
| 14-60 | Tech Debt | Low | Sanitizer patterns use placeholder replacement `[REDACTED]` — API keys, tokens, etc. are replaced but the original structure is partially preserved (e.g., JSON field names) | Consider replacing with `***` of same length to preserve format |

---

## 5. Shutdown (`internal/shutdown/`)

### `manager.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 58-65 | Bug | Medium | `Shutdown` is not guaranteed to complete if context is cancelled — the `select` at L62 may choose `<-ctx.Done()` before all phases complete, leaving an incomplete shutdown | Finish current phase before cancelling |
| 80-95 | Tech Debt | Low | `Shutdown` uses `defer cancel()` internally — if caller also uses `defer cancel()`, double-cancel is harmless | OK |

### `phase.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 22-38 | Bug | Low | Retry in `ExecutePhase` always waits `InitialDelay` before first retry attempt — should be immediate on first failure | First retry should have zero delay |
| 40-42 | Bug | Low | `ExecutePhase` returns last error even if context was cancelled — caller cannot distinguish cancellation from error | Check ctx.Err() before returning |

### `signal.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 18-24 | Bug | Medium | `HandleSignals` blocks indefinitely on `<-signalChan` — if the manager has already shut down, this goroutine leaks | Add select with context cancellation |

---

## 6. Storage (`internal/storage/`)

### `postgres/vector.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 120-150 | Bug | Medium | `InsertVector` doesn't validate vector dimension consistency before insertion — dimension mismatch is caught at DB level (PostgreSQL error) but wastes a round trip | Validate dimension client-side |

### `postgres/config.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 30-45 | Bug | Low | `Config.DSN()` doesn't URL-encode password — if password contains special characters, the DSN will be malformed | Use `net/url` to encode userinfo |

### `postgres/pool.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 28-35 | Bug | Medium | `NewPool` doesn't set `SetMaxOpenConns` or `SetMaxIdleConns` — defaults may cause connection exhaustion under load | Set sensible defaults |
| 40-42 | Bug | Low | `pool.Ping()` during initialization delays startup — if database is unavailable, the entire service fails to start | Consider lazy initialization |

### `postgres/security.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 1-70 | Tech Debt | Low | Security middleware adds `WHERE tenant_id = $1` to all queries — if a query forgets to pass `tenant_id`, the middleware may not catch it | Add linting rule |

### `postgres/repositories/` (all 8 files)

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| Multiple | Tech Debt | High | All repository types share near-identical CRUD boilerplate (GetByID, Create, Update, Delete, List) — consider using a generic repository pattern or code generation | Extract to generic `BaseRepository[T]` |
| Multiple | Bug | Medium | Repository `Update` methods (e.g., `memory_repository.go`, `knowledge_repository.go`) typically update ALL fields, risking overwriting concurrent changes | Use optimistic locking with `updated_at` |

### `postgres/embedding/` (4 files)

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| `fallback_client.go:20-35` | Bug | Medium | FallbackClient silently swallows errors from the primary client and uses the fallback — caller has no visibility into degraded mode | Log on fallback activation |

### `postgres/services/retrieval_service.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 1-300+ | Bug | High | Very large file (>300 lines) mixing embedding generation, vector search, and knowledge CRUD — violates single responsibility | Split into separate service files |

### `postgres/write_buffer.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 50-65 | Bug | Medium | WriteBuffer has no size limit — unbounded memory growth under high write load | Add max buffer size with backpressure |

### `postgres/circuit_breaker.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 30-45 | Bug | Low | Circuit breaker half-open probe interval is hard-coded | Make configurable |

### `memory/vector.go`

| Line | Category | Severity | Description | Suggested Fix |
|------|----------|----------|-------------|---------------|
| 50-60 | Tech Debt | Low | In-memory vector store does O(n) linear scan for similarity search — will not scale past ~10K vectors | Consider using an approximate nearest neighbor (ANN) index |

---

## 7. Cross-Cutting Issues

| Category | Severity | Description | Suggested Fix |
|----------|----------|-------------|---------------|
| Import Path | High | All imports use `goagentx/internal/...` but the on-disk path is `/Users/scc/go/src/goagent/internal/` — the module path mismatch means this code will not compile without a `replace` directive in `go.mod` | Verify module name in `go.mod` is `goagentx` |
| Error Suppression | Medium | `// nolint: errcheck` at package level in multiple files suppresses all unchecked errors — hides real bugs | Remove package-level nolint and handle errors individually |
| Duplicate DAG | Medium | Two DAG implementations exist: `workflow/graph/graph.go` (graph package) and `workflow/engine/types.go` (engine package) with `NewDAG` — same logic, different packages | Consolidate into one package |
| Test Coverage | Medium | Large files like `executor.go` (617 lines), `dynamic_executor.go` (630 lines), `retrieval_service.go` (~300 lines), `file_tools.go` (502 lines) have minimal or no corresponding test coverage | Add unit tests for critical paths |
| Configurability | Low | Hard-coded constants (timeouts, limits) appear in multiple executors, loaders, and network tools | Centralize in a config struct |
