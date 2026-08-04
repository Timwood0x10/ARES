# Runtime Closure Stage 0 — Gap Report

> Date: 2026-08-04
> Stage: 0 (Baseline)
> Status: Complete — gaps documented, tests in place, bugs fixed
>
> **Update (2026-08-04)**: F01/F02 (config gate bypass) and the nil-interface-trap
> bug have been fixed in Stage 2; the fail-first contract tests now PASS. See
> `outputs/RUNTIME_CLOSURE_PROGRESS_REPORT_2026-08-04.md` for the current state.

---

## 1. Deliverables Summary

| # | Deliverable | Status | Location |
|---|-------------|--------|----------|
| D01 | Runtime component inventory & dependency DAG | ✅ Complete | `outputs/RUNTIME_COMPONENT_INVENTORY_2026-08-04.md` |
| D02 | Closure contract tests (4 tests) | ✅ 2 FAIL (exposing gaps), 2 PASS | `internal/ares_bootstrap/closure_contract_test.go` |
| D03 | Shared instance consistency tests (6 tests) | ✅ All PASS | `internal/ares_bootstrap/closure_shared_instance_test.go` |
| D04 | Lifecycle start/stop assertion tests (8 tests) | ✅ All PASS (1 SKIP) | `internal/ares_bootstrap/closure_lifecycle_test.go` |
| D05 | Bug fix: nil-interface-trap in BuildKnowledgeRuntime | ✅ Fixed | `bootstrap.go:264`, `retriever_wiring.go:124` |
| D06 | Gap report (this document) | ✅ Complete | `outputs/RUNTIME_CLOSURE_STAGE0_GAP_REPORT_2026-08-04.md` |

---

## 2. Test Results

### 2.1 Contract Tests (build tag: `closure`)

```
=== RUN   TestClosure_MemoryDisabled_NotConstructed
--- FAIL: F01 — Memory constructed even when cfg.Memory.Enabled=false
=== RUN   TestClosure_EvolutionDisabled_NoGATicker
--- FAIL: F02 — GA ticker runs even when cfg.Evolution.Enabled=false
=== RUN   TestClosure_KnowledgeRetrievalEnabled_MissingWriteDeps_NotReady
--- PASS: F03 documented via t.Logf (silent degradation)
=== RUN   TestClosure_Ready_AllExecutorsBoundToLiveTargets
--- PASS: F04 documented via t.Logf (synthetic DAG at Ready)
```

### 2.2 Shared Instance Tests

```
All 6 tests PASS — shared instance identity verified for:
- EventStore (BootstrapDeps injection)
- EvidenceStore (NewEvolution internal)
- KnowledgeRuntime (single instance)
- StrategyStore (GA + Agent)
- EmbeddingClient (distillation + retrievers)
- PatchRegistry (executor identity)
```

### 2.3 Lifecycle Tests

```
All 8 tests PASS (1 SKIP):
- Complete start/stop (goroutine leak check)
- Runtime start/stop
- MCP stop (B03 documented)
- Dashboard stop
- Concurrent stop (race-safe)
- Repeated start/stop (10 iterations)
- Context cancellation
- Bootstrap cleanup (SKIP — requires Stage 1)
```

### 2.4 Regular Test Suite (no build tag)

```
make check = lint + test — 0 errors expected
```

---

## 3. Gap Classification

### 3.1 Config Gate Bypass (F01, F02)

**F01: Memory.Enabled=false but Memory constructed**

- **Location**: `bootstrap.go:106-121`
- **Root cause**: `Bootstrap()` always calls `ProvideMemory(memCfg)` regardless of `cfg.Memory.Enabled`
- **Impact**: Memory is constructed, configured, and wired even when the operator explicitly disabled it
- **Fix target**: Stage 2 — check `cfg.Memory.Enabled` before constructing Memory

**F02: Evolution.Enabled=false but GA ticker runs**

- **Location**: `bootstrap.go:182-265` (ProvideNewEvolution), `bootstrap_steps.go:201-218` (ticker)
- **Root cause**: `Bootstrap()` always creates `NewEvolution` and `wireGAEvolution()` always starts the background ticker
- **Impact**: GA ticker goroutine runs, consuming CPU and writing evidence even when evolution is disabled
- **Fix target**: Stage 2 — check `cfg.Evolution.Enabled` before constructing NewEvolution and starting tickers

### 3.2 Silent Degradation (F03)

**F03: Knowledge.RetrievalEnabled=true but no write deps → silent**

- **Location**: `knowledge_akg.go:151-185` (wireAKGLoop), `retriever_wiring.go:120-143`
- **Root cause**: When AKG retrieval is enabled but embedding/experience repo are unavailable, the system silently degrades to read-only without reporting Degraded status
- **Impact**: Operator cannot distinguish "AKG working" from "AKG silently inert"
- **Fix target**: Stage 2 — add Degraded status reporting

### 3.3 Live Binding Bypass (F04)

**F04: GA executors bound to synthetic DAG at Ready**

- **Location**: `bootstrap.go:235-264` (synthetic DAG), `serve.go:299` (wireEvolutionLiveDAGs post-Start)
- **Root cause**: Bootstrap creates a 3-step synthetic DAG. The live agent DAG is only wired after `mgr.Start()` in serve.go
- **Impact**: Workflow/scheduler/recovery patches hit a toy graph, not real runtime state, until the post-Start bypass runs
- **Fix target**: Stage 3 — move live DAG binding before Ready

### 3.4 Construct Side Effects (B03, F05)

**B03/F05: MCP starts during construction**

- **Location**: `provide_mcp.go:68` — `mcpMgr.Start(ctx)` inside `ProvideMCP()`
- **Root cause**: Provider function violates "construct has no side effects" principle
- **Impact**: Cannot roll back MCP construction failure cleanly; MCP is already started when later construction steps fail
- **Fix target**: Stage 1 — separate Construct from Start

### 3.5 Structured Concurrency Violation (F06)

**F06: Bootstrap naked goroutines + private WaitGroup**

- **Location**: `bootstrap_steps.go:69` (distillation subscriber), `bootstrap_steps.go:201-218` (GA ticker), `bootstrap_steps.go:225-262` (LLM suggestion ticker)
- **Root cause**: `go func()` with `comp.wg` instead of errgroup
- **Impact**: Violates `plan/rules/code_rules.md` §4.5: "禁止使用裸 go 关键字"
- **Fix target**: Stage 1 — move to Runtime-managed errgroup

### 3.6 Post-Bootstrap Bypass (B01, F07)

**B01: Memory EventStore set post-Bootstrap**

- **Location**: `serve.go:142` — `memMgr.SetEventStore(store, "memory")`
- **Root cause**: Runtime constructor passes nil for Memory; EventStore wired after Bootstrap
- **Impact**: Runtime should own this wiring, not the serve command
- **Fix target**: Stage 3 — wire EventStore during construction

**F07: Live DAG bound post-Start**

- **Location**: `serve.go:299` — `wireEvolutionLiveDAGs(comp, mgr, leaderID)`
- **Root cause**: Live DAG binding happens after `mgr.Start()`, not before Ready
- **Impact**: Between Start and wireEvolutionLiveDAGs, the system may use synthetic executors
- **Fix target**: Stage 3 — move live binding before Ready

### 3.7 Bug Fix (NEW)

**BUG: Nil-interface-trap in BuildKnowledgeRuntime**

- **Location**: `bootstrap.go:264` (call site), `retriever_wiring.go:125` (call site)
- **Root cause**: A nil `*embedding.EmbeddingClient` passed as `apiembedding.EmbeddingService` interface is non-nil (has type) but wraps a nil pointer. The `if emb == nil` check passes, but `emb.GetModel()` panics.
- **Impact**: Panics when `Knowledge.RetrievalEnabled=true` but `Embedding.Enabled=false`
- **Fix applied**: ✅ Converted nil `*EmbeddingClient` to nil `EmbeddingService` interface before passing to `BuildKnowledgeRuntime` and `akgModelName`

---

## 4. Closure Acceptance Matrix (Initial)

| Component | Runtime Input | Output/Effect | Ready Assert | Closure Assert | Status |
|-----------|--------------|---------------|---------------|-----------------|--------|
| Config | YAML/env | Component declarations | Validation passes | Enabled state matches component state | ⚠️ F01/F02 |
| EventStore | Agent/Runtime events | Subscribe/archive | Append/subscribe available | Flight/Distill/Monitor read same instance | ✅ |
| MemoryManager | EventStore, retrievers | Context/prompt, fitness | Start + retriever status matches config | Next prompt contains real memory | ⚠️ F01 |
| Embedding | Config/network | Vectors | Probe succeeds | Write/query sides use same model/dim | ✅ |
| KnowledgeRuntime | Providers, store, evidence | Graph/context, fitness | Provider set complete | AKF tools + patch executor share instance | ✅ |
| FlightRecorder | EventStore, EvidenceStore | 3-source fitness | Subscribed | Events produce corresponding evidence | ✅ |
| GA | 5-source evidence, patch targets | Proposal/decision/strategy | 5 genomes + live targets Ready | Strategy consumed by live Agent | ⚠️ F02/F04 |
| Deployment | Snapshot/eval/live target | Promote/rollback | Staging non-nominal | Bad patch rejected, rollback works | ⚠️ F08 |
| MCPManager | Server configs | Tools | Required server connected | Agent/Dashboard call real tools | ⚠️ B03 |
| ToolRegistry | Real deps | Tool results | No known-unusable tools | Tool results enter Agent execution | ⚠️ B10 |
| Agent Runtime | Agents, factories, memory, DAG | Events/tasks | Agent healthy | Produces events + consumes strategy/RAG/tools | ✅ |
| Monitoring | Component states/events | Health | Can read Runtime state | No duplicate data source | ✅ |
| Shutdown | Dependency graph | Stop/Wait/Close | Repeatable | Resources zero, in-flight work bounded | ✅ |

---

## 5. Stage 1 Readiness

Stage 0 is complete. The following are prerequisites for Stage 1:

1. ✅ Component inventory and DAG documented
2. ✅ Contract tests in place (2 failing, exposing F01/F02)
3. ✅ Shared instance tests in place (6 passing)
4. ✅ Lifecycle tests in place (8 passing)
5. ✅ Nil-interface-trap bug fixed
6. ✅ Existing bypasses recorded (B01-B10)
7. ✅ Failure classification established (F01-F08)

**Stage 1 should implement:**
- System Runtime control plane (component registry, lifecycle orchestration)
- Move bootstrap `wg` and naked goroutines to Runtime-managed errgroup
- Separate Construct from Start (fix B03/F05)
- Add component status snapshot API (internal)
- Ensure Provider construction has no side effects

**Authorization required from user before proceeding to Stage 1.**
