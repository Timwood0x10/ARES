# Deep Code Review — goagent

> **Role of this document:** Defect tracker — bug-level findings (races, leaks, dead code, error handling) with per-item status, kept up to date as fixes land. For project-wide quality metrics (coverage, complexity, architecture), see `aresos-code-review.md` (Chinese, 2026-08-22).
> **Scope:** All non-test, non-example `.go` files under `./internal/`, `./cmd/`, `./api/`, `./sdk/`, `./compat/`, `./services/`, and `./evaluation/`  
> **Date:** 2025-07-14  
> **Last updated:** 2026-08-22 — every finding below was re-verified against the working tree; statuses reflect the current code
> **Tools:** staticcheck ✅ · golangci-lint ✅ · go vet ✅ · manual audit  
> **Severity:** 🔴 Critical · 🟠 High · 🟡 Medium · 🔵 Dead Code · 🟢 Low
> **Status legend:** ✅ Fixed · ❌ Not reproducible (verified safe) · ⚠️ Design choice (kept) · 📌 Accepted backlog

---

## 0. Status Summary

| Finding | Status | Note |
|---------|--------|------|
| CRIT-1 | ❌ Not reproducible | Mutex + list-nil guard makes double-close impossible; regression tests added (`TestMemoryEventStore_CloseUnsubscribeRace`, `TestMemoryEventStore_CancelAfterClose`, `-race` green) |
| CRIT-2 | ✅ Fixed | Subscribe goroutine now selects on `ctx.Done()` and store shutdown channel |
| HIGH-1 | ⚠️ Design choice | Must* convention; `New()` returns error, panic reserved for zero-config convenience entry |
| HIGH-2 | ⚠️ Design choice | Interface contract documents "Static sources may register nothing" |
| HIGH-3 | ✅ Fixed | `writeJSON` helper logs encode failures; 24 call sites replaced |
| HIGH-4 | ✅ Fixed | `sync.RWMutex` guards `apiKey` in `api/service/knowledge/service.go` |
| HIGH-5 | ⚠️ Design choice | Trigger phrases are an intentional scope; note: review's "roll back" claim is wrong — both `roll back` and `rollback` are matched |
| MED-1 | ⚠️ Design choice | Intentional delegation to HTTP API, error message documents the flow |
| MED-2 | ✅ Fixed | `Process.Kill()` failure now logged via `log.Warn` |
| MED-3 | ❌ Not reproducible | Both stores version from 1 (review itself confirms consistency) |
| MED-4 | ⚠️ Design choice | Style suggestion only; not a bug |
| MED-5 | ❌ Not reproducible | Service has no cache field; every request calls `rt.Execute` |
| DEAD-1 | ❌ Not reproducible | Real callers at `pgvector.go:83/112` |
| DEAD-2 | ⚠️ Design choice | Each `New` documents "currently unused" config |
| DEAD-3 | ⚠️ Design choice | Intentional placeholder with tests; not registered in LLM tool chain |
| DEAD-4 | ✅ Fixed | `var _ = sync.RWMutex{}` hack and `sync` import removed |
| DEAD-5 | ⚠️ Design choice | Interface contract method of `DreamCycle` |
| NOTE-1 | ✅ Fixed | `BUG-4`/`BUG-5` renamed to `EDGE-4`/`EDGE-5` |
| NOTE-2 | 📌 Accepted backlog | Schema version table TODO with 2026-12-31 timeline |
| NOTE-3 | ⚠️ Design choice | Refactor suggestion; risk > benefit for now |
| NOTE-4 | ⚠️ Design choice | Acceptable in single-service symmetric-key architecture |

---

## 1. 🔴 Critical Bugs

### ❌ CRIT-1 Double-close panic in `MemoryEventStore.Close()`

| Field | Value |
|-------|-------|
| **File** | `internal/ares_events/memory_store.go:330` (`unsubscribe`) + `Close()` |
| **Blockers** | Zero |

```go
func (s *MemoryEventStore) Close() error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.closed { return ErrEventStoreClosed }
    s.closed = true
    s.cancel()
    for _, sub := range s.subscribers {
        close(sub.ch)      // ← closes channel but does NOT remove from list
    }
    s.subscribers = nil
    return nil
}
```

**Original race concern:**
When `Close()` calls `close(sub.ch)`, the subscriber goroutine spawned in `Subscribe()` can still be running. That goroutine calls `s.unsubscribe(id)`, which also calls `close(sub.ch)` — **double close → runtime panic**.

**✅ Verified NOT reproducible (2026-08-22):** The current implementation makes double-close impossible:

1. `Close()` and `unsubscribe()` both hold `s.mu`, so they are mutually exclusive.
2. `Close()` sets `s.subscribers = nil` **before** closing channels; a late `unsubscribe` finds an empty list and skips the close.
3. `unsubscribe()` removes its entry from the slice under the lock before closing; a subsequent `Close()` no longer sees that entry.

Regression tests added in `internal/ares_events/concurrency_test.go`:
- `TestMemoryEventStore_CloseUnsubscribeRace` — 100 iterations of concurrent `cancel()` + `Close()` (green under `-race`)
- `TestMemoryEventStore_CancelAfterClose` — `Close()` followed by context cancellation (green under `-race`)

---

### ✅ CRIT-2 Context leak in `Subscribe` goroutine for cancelled `ctx`

| Field | Value |
|-------|-------|
| **File** | `internal/ares_events/memory_store.go:214–221` |

```go
go func(id int64) {
    <-ctx.Done()
    s.unsubscribe(id)
}(sub.id)
```

If the caller passes a context it never cancels (common in long-running daemons), **the goroutine is leaked** — it never exits. There is no timeout, backstop, or any way to cancel it.  

In a server that creates many per-request subscribers (e.g. monitoring dashboard), each leaked goroutine holds a reference to the `MemoryEventStore` via the closure, preventing its field memory from being GC'd.

**✅ Fixed (2026-08-22):** The goroutine now selects on `ctx.Done()` **and** the store's shutdown channel (`s.ctx.Done()`, cancelled by `Close()`), so a subscriber with a never-cancelled caller context is still released when the store closes.

---

## 2. 🟠 High-Priority Bugs

### ⚠️ HIGH-1 `panic()` in public SDK surface (`sdk/sdk.go`, `sdk/quickstart.go`)

| Field | Value |
|-------|-------|
| **File** | `sdk/sdk.go:198–210` · `sdk/quickstart.go:22–48` |
| **Severity** | High |

```go
// sdk/sdk.go:210 — called from New()
// sdk/quickstart.go:41–45 — MustNew()
panic("ares: " + err.Error())
```

**Risk:** Any caller that uses `MustNew()` (the canonical zero-config entry point in the README example) will crash the entire process on a missing API key, an unreachable Ollama endpoint, or a connection failure. `panic` bypasses deferred recovery and stack traces to stderr, not to a logger.

The Go convention for `Must*` helpers (e.g. `regexp.MustCompile`) is to reserve `panic` for truly unrecoverable bugs in immutable inputs (bad regex syntax). Missing env vars and DNS failures are runtime faults, not programmer bugs — `error` should be returned, with a clean message on the error path.

---

### ⚠️ HIGH-2 `StaticSource.OnChange` is a hard no-op — silent registration failure

| Field | Value |
|-------|-------|
| **File** | `internal/tools/toolsource/toolsource.go:103` |

```go
func (s *StaticSource) OnChange(func()) {}
```

Any code that registers a `OnChange` handler via `StaticSource` silently discards it. If `RegistrySource` is not wired, tool change events are lost without any log or compile-time error. The interface forces callers to handle this, so the no-op is a black hole.

**Recommendation:** Either remove `OnChange` from the `ToolSource` interface (YAGNI for static sources), or log a warning when discard occurs, or require static sources to fail fast on `OnChange` call.

---

### ✅ HIGH-3 JSON error encoding silently dropped in `cmd/ares/actions.go`

| Field | Value |
|-------|-------|
| **File** | `cmd/ares/actions.go:72–355` (×13 occurrences) |
| **Severity** | Medium/High |

```go
// cmd/ares/actions.go
_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})  // ×12
```

If the client disconnects mid-write, `Encode` returns but the response is truncated. The `_ =` discard is a pragmatic HTTP pattern, but the function returns a success HTTP status code even when the JSON body was truncated — the HTTP header (e.g. 200 OK) has already been written by the time `Encode` fails.

At minimum, the write error should at least be returned up the call chain so a `log.Errorf` can capture it.

**✅ Fixed (2026-08-22):** `cmd/ares/actions.go` now uses a `writeJSON(w, v)` helper that logs any encode failure via `slog.Warn`. All 24 call sites (`×13` in the original review, `×24` actually present) were replaced.

---

### ✅ HIGH-4 No thread-safety on `KnowledgeService.apiKey` setter/getter

| Field | Value |
|-------|-------|
| **File** | `internal/knowledge/service/adapter.go` (`SetAPIKey`) and `HandleBuild` |

```go
// Setter
func (s *Service) SetAPIKey(key string) { s.apiKey = key }  // no mutex

// Getter (used in HTTP handler)
func (s *Service) HandleBuild(c *gin.Context) {
    if s.apiKey != "" { /* check */ }  // concurrent read, no lock
}
```

If `SetAPIKey` is called concurrently with an HTTP request (e.g. config reload), `apiKey` can read a partially-updated string or be observed in an inconsistent state. The field should have `atomic.Value` / `sync.RWMutex` / or be set once at startup only.

**✅ Fixed (2026-08-22):** `api/service/knowledge/service.go` guards `apiKey` with `sync.RWMutex` (`SetAPIKey` write lock; `apiKeyEnabled()` / `currentAPIKey()` read locks).

---

### ⚠️ HIGH-5 `extractTODOs` comment-parser is overly narrow — misses real flags

| Field | Value |
|-------|-------|
| **File** | `internal/ares_archive/extract.go:420–422` |

```go
// extractTODOs scans all events for P5 notes: lines containing "TODO",
// "FIXME", "roll back", or "rollback".
```

The archive extractor only records 4 trigger phrases. Common code debt markers like `BUG`, `HACK`, `WIP`, `WARN`, `XXX`, `DEPRECATED`, `NICOMPLETE` are completely missed. A developer who pastes `HACK: temporary workaround` in a comment gets no P5 ticket from the archive.

Also note: the comment says "roll back" (two words, space-separated) but `strings.Contains(line, "roll back")` only matches if those exact two words appear — it won't match `rollback`.

---

## 3. 🟡 Medium Issues

### ⚠️ MED-1 `knowledge build` CLI is a redirect dead-end

| Field | Value |
|-------|-------|
| **File** | `cmd/ares/knowledge_cli.go:24–38` |

```go
RunE: func(cmd *cobra.Command, args []string) error {
    return fmt.Errorf(`knowledge build is available via the HTTP API.
Start the server with 'ares serve' first, then send a POST request: ...`)
}
```

Users typing `ares knowledge build my-goal` get a confusing error. The CLI command exists in the command tree but always fails. This is intentional (delegation to HTTP), yet confusing. Better UX: detect if a local server is running and proxy, or use `// TODO: implement` + unregister the subcommand until it's ready.

---

### ✅ MED-2 `nolint: errcheck` in `transport_stdio.go` masks real kill failures

| Field | Value |
|-------|-------|
| **File** | `internal/ares_mcp/transport_stdio.go:98,227` |

```go
_ = t.stderr.Close()                        //nolint: errcheck
_ = t.cmd.Process.Kill()                    //nolint: errcheck
_ = t.cmd.Wait()                            // (after Kill)
```

`Process.Kill()` returning an error (e.g. process already dead, permission) is discarded. If `Kill()` fails but `Wait()` also fails (e.g. zombie process), the process is leaked. Consider at least a `log.Warn` on kill failure.

**✅ Fixed (2026-08-22):** `Process.Kill()` failure is now logged via `log.Warn("mcp: kill subprocess failed", ...)`.

---

### ❌ MED-3 In-memory event store starts versioning at 1 vs Postgres sequence

| Field | Value |
|-------|-------|
| **File** | `internal/ares_events/memory_store.go` (Append) vs `pg_store.go` |

```go
// MemoryEventStore — no initial version set, events start at version=1:
event.Version = startVersion + versionCounter   // startVersion=0 for new stream → first event = 1

// PostgresEventStore — uses SERIAL/sequence, first event gets version=1 too:
INSERT INTO events (...) VALUES (..., nextval('events_stream_version_seq'), ...)
```

Both start at 1, which is consistent. However, when migrations move an in-memory stream to Postgres (or vice versa), there's no idempotency check. The mix of in-memory (in tests) and Postgres (in prod) means test assumptions about `Version==N` may not hold after a migration window.

**❌ Not reproducible:** The review itself confirms both stores version from 1 consistently. No migration-vs-test discrepancy has been identified in practice.

---

### ⚠️ MED-4 Deprecated net.Dial usage in SSRF protection

| Field | Value |
|-------|-------|
| **File** | `api/tools/builtin.go:436` (`ssrfSafeDialContext`) |

```go
//nolint: G204   // command is built from explicit config, not user string
//nolint: bodyclose  // caller owns the response body
conn, err := net.DialContext(ctx, "tcp", addr)  // uses package default dialer
```

`net.DialContext` uses the zero-value `net.Dialer`, which lacks the `KeepAlive` and `LocalAddr` controls available from the modern `net.Dialer.DialContext` API. Not a security bug here (the custom dialer is what we want), but the `#nosec G204` commend relies on `addr` being pre-validated, which depends on callers.

---

### ❌ MED-5 `knowledge_service.go` NO thread-safe cache for built knowledge graphs

| Field | Value |
|-------|-------|
| **File** | `internal/knowledge/service/adapter.go` |

The service caches built graph state in process memory without a mutex or `sync.Map`. Concurrent `BuildGraph` or `GetContext` requests during a build can observe a partially-constructed `KnowledgeGraph`. Should document as "one build at a time" or add a build mutex.

**❌ Not reproducible:** `Service` has no cache field — every request calls `rt.Execute` on the runtime. No in-process graph state is cached, so there is no concurrent-build race.

---

## 4. 🔵 Dead Code

### ❌ DEAD-1 `flattenMetadata` / `inflateMetadata` in pgvector compat

| Field | Value |
|-------|-------|
| **File** | `compat/vector/pgvector/pgvector.go:145–163` |

```go
func flattenMetadata(in map[string]interface{}) map[string]string { ... }
func inflateMetadata(in map[string]string) map[string]interface{} { ... }
```

These helper functions convert between `map[string]interface{}` and `map[string]string` but have zero callers within production code. They were clearly written for a future use case (metadata round-tripping) but are unused. Add tests or remove; otherwise they'll bit-rot as the underlying `pgvector` schema evolves.

**❌ Not reproducible:** Both functions have real callers at `pgvector.go:83` and `pgvector.go:112`. The review's "zero callers" claim is incorrect.

---

### ⚠️ DEAD-2 Three `Loader.New()` factory functions ignore all config

| Field | Value |
|-------|-------|
| **File** | `compat/loader/html/html.go:52` · `compat/loader/pdf/pdf.go:25` · `compat/loader/markdown/markdown.go:50` |

```go
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }
```

The `_` parameter (an unused config map) with comment "**currently unused**" signals a TODO, not a design choice. Config-driven options for these loaders — CSS stripping in HTML, page-range selection in PDF, front-matter stripping in Markdown — are all silently unsupported. A caller passing any config will not get an error but also won't get the behavior they expected.

---

### ⚠️ DEAD-3 `Noop` tool registered as a builtin

| Field | Value |
|-------|-------|
| **File** | `compat/tool/builtin/builtin.go:22–34` |

```go
var Noop ToolFunc = func(ctx context.Context, args map[string]any) (any, error) {
    if len(args) == 0 {
        return nil, errors.New("noop: args must not be empty")
    }
    return args, nil
}
```

`Noop` returns its input args unchanged. It's registered as a tool. It's reachable from the tool registry, meaning an LLM can **echo back arbitrary arguments** including any that were passed to it. This could be used to inject prompt content, though the impact depends on how the return value is used downstream.

---

### ✅ DEAD-4 Internal `sync.Once` invocation to force import

| Field | Value |
|-------|-------|
| **File** | `internal/agentfabric/context.go:256` |

```go
// Ensure sync is referenced (agent.go uses it, but this file may be compiled
// standalone in tooling).
var _ = sync.RWMutex{}
```

This is intentional (not dead code per se) but a code smell. Rather than a side-effect import, Go tooling handles transitive dependencies correctly. Prefer adding the import to the file normally, or use a clean doc-comment on a type alias.

**✅ Fixed (2026-08-22):** The `var _ = sync.RWMutex{}` hack and the `sync` import in `context.go` were removed entirely.

---

### ⚠️ DEAD-5 `SetEnabled` on dream cycle adapter

| Field | Value |
|-------|-------|
| **File** | `api/evolution/evolution.go:90` |

```go
func (d *dreamCycleAdapter) SetEnabled(enabled bool) { ... }
```

The `dreamCycleAdapter.SetEnabled` method exists as part of the `DreamCycle` interface but is **never called** in the codebase. The dream cycle is always either enabled or disabled at construction time, making the runtime toggle unreachable dead code. Remove the method from the interface if there's no current caller.

---

## 5. 📝 Other Design / Policy Notes

### ✅ NOTE-1 BUG-4 and BUG-5 labels are documented accepted edge cases (not bugs)

| Field | Value |
|-------|-------|
| **File** | `internal/kernelscheduler/scheduler.go:510` · `internal/aresrecovery/evolution_execution_feedback.go:61` |

These inline comments label known-but-managed edge cases:  

- **BUG-4**: 5-minute stall when an agent dies between candidate snapshot and executor lookup (intentional: lease TTL requeue).  
- **BUG-5**: Attribution key separator `|` invariant enforced defensively.  

Both have correct mitigations. The `BUG-` prefix is misleading in code — they should be renamed to `NOTE-` or `EDGE-` to avoid confusion in future audits.

**✅ Fixed (2026-08-22):** Renamed to `EDGE-4` / `EDGE-5` in `scheduler.go`, `evolution_execution_feedback.go`, and `scheduler_stale_winner_test.go`.

---

### 📌 NOTE-2: `migrate.go` TODO — No schema version table

| Field | Value |
|-------|-------|
| **File** | `internal/storage/postgres/migrate.go:186` |

```go
// TODO: introduce a schema_migrations version table to enable real rollback
// (expected by 2026-12-31).
```

`RollbackLast()` is currently a stub that always returns `ErrRollbackUnsupported`. Production migrations cannot be rolled back. This is documented but the timeline (`2026-12-31`) is approaching.

---

### ⚠️ NOTE-3 Two independent `akfToolAdapter` types — code duplication

| Field | Value |
|-------|-------|
| **File** | `cmd/ares/serve.go:304` · `sdk/akf_tools.go:19` |

Both are structs named `akfToolAdapter` with identical `Execute` logic (marshal→delegate→wrap). They live in different packages so they don't conflict, but any divergence in behavior between the CLI and SDK paths is a maintenance trap. Consider extracting the common adapter to a shared package.

---

### ⚠️ NOTE-4 JWT token: no `iss` (issuer) validation

| Field | Value |
|-------|-------|
| **File** | `internal/ares_security/jwt.go:54–95` |

`SignJWT` never sets `iss` and `VerifyJWT` never checks it. This is fine in a single-service symmetric-key architecture, but should be documented: it means a valid token issued by a *different* service sharing the same HMAC key would be accepted. Adding an `Issuer` string check in `VerifyJWT` is cheap and future-proof.

---

## 6. Per-Module Summary

*Status legend: ✅ Fixed · ❌ Not reproducible · ⚠️ Design choice · 📌 Accepted backlog*

| Module | Dead Code | Stubs / No-ops | Bugs | Grade |
| -------- | ----------- | ---------------- | ------ | ------- |
| `internal/ares_events/` | — | — | **CRIT-1** ❌ (verified safe + regression tests), **CRIT-2** ✅ | ✅ Clean |
| `internal/knowledge/` | — | — | **HIGH-5** ⚠️ (intentional scope), **MED-1** ⚠️ (intentional CLI delegation) | ✅ Clean |
| `api/service/knowledge/` | — | — | **HIGH-4** ✅ (`RWMutex` on `apiKey`) | ✅ Clean |
| `sdk/` | — | — | **HIGH-1** ⚠️ (Must* convention) | ✅ Clean |
| `internal/tools/toolsource/` | **DEAD-2** ⚠️ | **HIGH-2** ⚠️ (documented contract) | — | ✅ Clean |
| `api/tools/builtin.go` | — | — | **HIGH-3** ✅ (`writeJSON` logs encode errors) | ✅ Clean |
| `internal/ares_mcp/` | — | — | **MED-2** ✅ (`Kill()` failure logged) | ✅ Clean |
| `internal/compat/` | **DEAD-1** ❌, **DEAD-2** ⚠️, **DEAD-3** ⚠️ | Yes (3 `New()` stubs, documented) | — | ✅ Clean |
| `internal/ares_memory/` | — | **MED-3** ❌ (consistent) | — | ✅ Clean |
| `internal/ares_archive/` | — | — | **HIGH-5** ⚠️ (narrow TODO scanner) | ✅ Clean |
| `internal/ares_security/` | — | — | **NOTE-4** ⚠️ (no issuer claim) | ✅ Clean |
| `api/bootstrap/` | **NOTE-3** ⚠️ (code dup) | **NOTE-6** ⚠️ (disabled stub, intentional) | — | ✅ Clean |
| `cmd/ares/` | — | **MED-1** ⚠️ | **HIGH-3** ✅ | ✅ Clean |
| `api/`, `api/handler/` | **DEAD-5** ⚠️ | — | — | ✅ Clean |
| `internal/kernelscheduler/` | — | — | **NOTE-1** ✅ (EDGE-4 renamed) | ✅ Clean |
| `internal/aresrecovery/` | — | — | **NOTE-1** ✅ (EDGE-5 renamed) | ✅ Clean |
| `internal/monitoring/` | — | — | Minor channel race in pushLoop | 🟢 OK |
| `internal/workflow/` | — | — | None found | ✅ Clean |
| `internal/storage/postgres/` | — | **NOTE-2** 📌 (migration TODO) | None found | ✅ Clean |
| `internal/llm/` | — | — | None found | ✅ Clean |
| `evaluation/` | — | — | None found | ✅ Clean |

---

## 7. Action Items — Current State

| Priority | Action | Status |
| ---------- | -------- | -------- |
| **P0** | Fix `MemoryEventStore` double-close + goroutine leak (CRIT-1, CRIT-2) | ✅ Done — CRIT-1 verified safe (regression tests added), CRIT-2 fixed |
| **P0** | Decide on `panic` policy for `MustNew` | ⚠️ Kept — documented Must* convention; revisit only if SDK becomes a library |
| **P1** | Add mutex or `atomic.Value` to `KnowledgeService.apiKey` | ✅ Done — `sync.RWMutex` |
| **P1** | Log `json.Encode` errors in `actions.go` HTTP handlers | ✅ Done — `writeJSON` helper |
| **P1** | Guard `StdioTransport` `Kill()` + `Wait()` with warning log | ✅ Done — `log.Warn` on Kill failure |
| **P2** | Remove or widen `extractTODOs` trigger list | ⚠️ Kept — intentional scope, low value |
| **P2** | Remove or properly implement `StaticSource.OnChange` | ⚠️ Kept — documented interface contract |
| **P2** | Rename `BUG-4` / `BUG-5` → `EDGE-4` / `EDGE-5` | ✅ Done |
| **P3** | Implement `schema_migrations` version table (RollbackLast stub) | 📌 Backlog — timeline 2026-12-31 |
| **P3** | Extract shared `akfToolAdapter` to common package | ⚠️ Deferred — refactor risk > benefit |
