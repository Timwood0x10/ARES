# Runtime Component Inventory & Dependency DAG

> Date: 2026-08-04
> Stage: 0 (Baseline)
> Source: `go list ./...`, production entry call graph, config schema

---

## 1. Component Inventory

### 1.1 Core Infrastructure

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C01 | Config | Bootstrap | Required | — | LoadYAML + setDefaults + Validate | — | — | Entry-level; no goroutine |
| C02 | EventStore | Bootstrap | Required | always | `NewMemoryEventStore` or deps.EventStore | — | — | Shared instance; archive-enabled in `serve` |
| C03 | Storage (PostgreSQL pool) | Bootstrap | Optional | `storage.enabled` | `provideDistillation` | — | `pool.Close()` | Conditional on PG + embedding |
| C04 | EmbeddingClient | Bootstrap | Optional | `embedding.enabled` | `provideDistillation` | — | — | Reused by retrievers |
| C05 | LLM Client | Bootstrap | Required | `llm.*` | `ProvideLLM` or deps.LLMClient | — | — | Interface{} in Components |

### 1.2 Agent Subsystem

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C06 | Runtime Manager | Bootstrap | Required | always | `ProvideRuntime(eventStore)` — **nil memory** | `mgr.Start(ctx)` | `mgr.Stop()` | Agent lifecycle only; not system-level |
| C07 | MemoryManager | Bootstrap | Required | **always** (ignores `memory.enabled`) | `ProvideMemory(memCfg)` | `SetEventStore` (post-Bootstrap in serve) | — | Config gate not respected |
| C08 | Leader Agent | serve/api_impl | Required | `agents.leader` | `createLeaderAgent` | `mgr.StartAgent` | `mgr.StopAgent` | Factory-registered for resurrection |
| C09 | Sub Agents | serve/api_impl | Required | `agents.sub[]` | `createAgents` | `mgr.RegisterAgent` + `Start` | — | Factory-registered |

### 1.3 Evolution System

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C10 | NewEvolution (Genome/Diff/Patch/Coordinator) | Bootstrap | Required | **always** (ignores `evolution.enabled`) | `ProvideNewEvolution(dag, knowRt, liveMemoryStore)` | — | — | Synthetic 3-step DAG |
| C11 | EvidenceStore | Bootstrap (inside NewEvolution) | Required | always | `evidence.NewMemoryStore()` | — | — | Shared by 5 genomes |
| C12 | PatchRegistry | Bootstrap (inside NewEvolution) | Required | always | `patch.NewRegistry()` | — | — | Executors: memory, knowledge, recovery |
| C13 | GA Population Adapter | Bootstrap (`wireGAEvolution`) | Required | **always** (ignores `evolution.enabled`) | `evolution.NewWiredEvolutionSystem` | — | — | **Starts background ticker** |
| C14 | GA Evolution Ticker | Bootstrap (`wireGAEvolution`) | Required | **always** | `go func() { ticker }` | **naked goroutine + wg** | `ctx.Done()` | **Violates structured concurrency rule** |
| C15 | LLM Suggestion Ticker | Bootstrap (`wireGAEvolution`) | Optional | LLM client available | `go func() { ticker }` | **naked goroutine + wg** | `ctx.Done()` | **Violates structured concurrency rule** |
| C16 | Distillation Subscriber | Bootstrap (`subscribeDistillationEvents`) | Optional | distillation wired | `go func() { subscribe }` | **naked goroutine + wg** | `ctx.Done()` | **Violates structured concurrency rule** |
| C17 | Old Evolution (deprecated) | Bootstrap | Optional | `deps.ExpRepo != nil` | `ProvideEvolution` | — | — | Legacy; conditional |
| C18 | Deployment Pipeline | Bootstrap | Optional | `evolution.deployment.enabled` | `deployment.NewDeploymentPipeline` | — | — | **Staging always passes (nominal 1.0)** |
| C19 | StrategyStore | Bootstrap (`wireGAEvolution`) | Required | always | PG or in-memory | — | — | Shared by GA + Agent |

### 1.4 Knowledge System

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C20 | KnowledgeRuntime | Bootstrap | Required | always | `BuildKnowledgeRuntime()` (no deps) | — | — | **No vector/store provider at construct time** |
| C21 | KnowledgeStore | Bootstrap | Optional | `knowledge.retrieval_enabled` | in-memory or PG | — | — | AKG read/write |
| C22 | AKG DistillBridge | Bootstrap | Optional | distillation + AKG | `adapter.NewDistillBridge` | — | — | Write side of AKG loop |
| C23 | MemoryRetriever | Bootstrap (`wireRetrievers`) | Optional | `memory.enable_rag` | `NewMemoryRetriever` | — | — | Read side of experience loop |
| C24 | KnowledgeRetriever | Bootstrap (`wireRetrievers`) | Optional | `knowledge.retrieval_enabled` | `NewKnowledgeRetriever` | — | — | Read side of AKG loop |

### 1.5 External Interfaces

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C25 | MCPManager | Bootstrap | Required | always | `ProvideMCP` | **Start inside Construct!** | `mcp.Stop(ctx)` | **Construct has side effects** |
| C26 | Dashboard | Bootstrap | Optional | `dashboard.addr` | `ProvideDashboard` | — | `dash.Stop(ctx)` | — |
| C27 | FlightRecorder | Bootstrap | Optional | eventStore available | `flight.NewFlightRecorder` | `Start(ctx)` | — | Subscribes to EventStore |
| C28 | Discovery | Bootstrap | Optional | `discovery.enabled` | `ProvideDiscovery` | — | — | — |
| C29 | HTTP Server | serve | Optional | `server.port` | `http.Server` | `ListenAndServe` | `Shutdown(ctx)` | serve-only |
| C30 | Monitoring Plugin | serve | Optional | always | `monitoring.NewConsole` | `plugin.Start` | — | Dashboard bridge |
| C31 | ToolRegistry | serve | Required | always | `newToolRegistry` | — | — | Public API tools |
| C32 | InternalToolRegistry | serve | Required | always | `setupMCP` | — | — | MCP + AKF tools |

### 1.6 Shutdown

| # | Component | Owner | Mode | Config Gate | Construct | Start | Stop | Notes |
|---|-----------|-------|------|-------------|-----------|-------|------|-------|
| C33 | ShutdownManager | serve | Required | always | `ares_shutdown.NewManager` | `RegisterPhase` | `StartShutdown` | Phase-based |

---

## 2. Dependency DAG (Startup Topology)

```text
Config (C01)
  ├─→ EventStore (C02)
  │     ├─→ Runtime Manager (C06)
  │     ├─→ FlightRecorder (C27)
  │     ├─→ Distillation Subscriber (C16)
  │     └─→ Monitoring Plugin (C30)
  ├─→ Storage Pool (C03) [optional]
  │     ├─→ EmbeddingClient (C04)
  │     ├─→ KnowledgeStore (C21) [PG backend]
  │     └─→ StrategyStore (C19) [PG backend]
  ├─→ LLM Client (C05)
  │     ├─→ Distillation (C22)
  │     └─→ LLM Suggestion Ticker (C15)
  ├─→ MemoryManager (C07)
  │     └─→ MemoryRetriever (C23) [optional]
  ├─→ MCPManager (C25)
  │     └─→ InternalToolRegistry (C32)
  ├─→ KnowledgeRuntime (C20)
  │     └─→ KnowledgeRetriever (C24) [optional]
  ├─→ NewEvolution (C10)
  │     ├─→ EvidenceStore (C11)
  │     ├─→ PatchRegistry (C12)
  │     ├─→ GA Ticker (C14)
  │     └─→ Deployment Pipeline (C18) [optional]
  ├─→ Agents (C08, C09)
  │     └─→ live DAG → PatchRegistry (C12) [post-Start bypass]
  └─→ HTTP Server (C29) [serve-only]
```

### Critical Path Issues

1. **C07 Memory constructed before C06 Runtime** — Runtime gets nil memory
2. **C25 MCP starts during construction** — violates "construct has no side effects"
3. **C14/C15/C16 naked goroutines** — violates structured concurrency rule
4. **C10 NewEvolution always constructed** — ignores `evolution.enabled`
5. **C07 Memory always constructed** — ignores `memory.enabled`
6. **Live DAG binding post-Start** — `wireEvolutionLiveDAGs` called after `mgr.Start()`

---

## 3. Shared Instance Constraints

| Instance | Producers | Consumers | Current Status |
|----------|-----------|-----------|----------------|
| EventStore | Bootstrap (C02) | Runtime, Memory, Flight, Distill, Monitor | ✅ Shared via `comp.EventStore` |
| EvidenceStore | NewEvolution (C11) | 5 Genomes, Flight | ✅ Shared via `NewEvolution.EvidenceStore` |
| KnowledgeRuntime | Bootstrap (C20) | PatchExecutor, AKF tools | ✅ Shared via `comp.KnowledgeRuntime` |
| StrategyStore | wireGAEvolution (C19) | GA, Agent StrategySource | ✅ Shared via `NewEvolution.StrategyStore` |
| KnowledgeStore | Bootstrap (C21) | DistillBridge, KnowledgeRetriever | ⚠️ Best-effort; may be nil |
| EmbeddingClient | provideDistillation (C04) | Distillation, Retrievers | ✅ Shared via `embClient` variable |
| PatchRegistry | NewEvolution (C12) | All executors, live DAG | ⚠️ Live DAG registered post-Start |

---

## 4. Existing Bypass List

| # | Bypass | Location | Impact |
|---|--------|----------|--------|
| B01 | `memMgr.SetEventStore(store, "memory")` | `serve.go:142` | Post-Bootstrap patch; Runtime should own this |
| B02 | `wireEvolutionLiveDAGs(comp, mgr, leaderID)` | `serve.go:299` | Post-Start live binding; should be in Bind phase |
| B03 | `ProvideMCP` starts during construction | `provide_mcp.go:68` | Construct has side effects |
| B04 | Bootstrap always creates Memory | `bootstrap.go:106-121` | Ignores `cfg.Memory.Enabled` |
| B05 | Bootstrap always creates NewEvolution | `bootstrap.go:182-232` | Ignores `cfg.Evolution.Enabled` |
| B06 | Naked goroutines in bootstrap | `bootstrap_steps.go:69,201,225` | Violates structured concurrency |
| B07 | Deployment staging always 1.0 | `deployment_wiring.go:10-32` | No real shadow evaluation |
| B08 | `RecordStrategyOutcome` is no-op | `provide_distillation.go:67-81` | Track A write side missing |
| B09 | AKG/RAG best-effort silent | `knowledge_akg.go:151-185` | Enabled but silently degraded |
| B10 | Tools register with nil deps | `builtin.go:121-175` | Register success, call failure |

---

## 5. Failure Classification

| # | Failure | Category | Test Exposure |
|---|---------|----------|---------------|
| F01 | Memory.Enabled=false but Memory constructed | Config gate bypass | Contract test 1 |
| F02 | Evolution.Enabled=false but GA ticker runs | Config gate bypass | Contract test 2 |
| F03 | Knowledge.RetrievalEnabled=true but no write deps | Silent degradation | Contract test 3 |
| F04 | GA executors bound to synthetic DAG at Ready | Live binding bypass | Contract test 4 |
| F05 | MCP starts during Construct | Side effect in Construct | Lifecycle test |
| F06 | Bootstrap naked goroutines | Concurrency violation | Lifecycle test |
| F07 | EventStore set post-Bootstrap | Bypass | Shared instance test |
| F08 | Live DAG bound post-Start | Bypass | Shared instance test |
