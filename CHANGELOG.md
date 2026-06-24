# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-06-24

### New Features

- **Genetic Algorithm Evolution System (Beta)**: Full GA genome package with `Population`, `Crossover` (Inherit/HalfSplit/Uniform modes), `TournamentSelection`, and strategy mutation engine. Supports deterministic reproduction via seed control, elite preservation, adaptive survival rates, and diversity tracking with fitness sharing. [(GA Hardening Plan)](plan/GA/README.md)
- **Autonomous Evolution (Dream Mode v1)**: Closed-loop evolution orchestration with Dream Cycle (trigger → mutate → evaluate → adopt → record lineage). Includes arena regression testing with Welch's t-test, bandit feedback loop for experience quality optimization, and full genealogy tracking.
- **Agent Resurrection & Snapshot System**: Pluggable health checking for agent recovery, checkpoint-based resurrection with state restoration from EventStore and MemoryStore.
- **Tiered Scoring System**: Multi-level scoring pipeline with FailoverScorer integration. Includes scoring cache optimization (atomic hit/miss counters), hybrid scoring with prompt crossover modes, and unevaluated score guardrails.
- **JSONL Training Data Pipeline**: End-to-end pipeline for agent strategy evolution and experience distillation data export.
- **Leader Agent Hardening**: Nil validation for all constructor parameters (memory manager, aggregator, parser, planner, dispatcher). Session initialization via `sync.Once`. Comprehensive error collection during `Stop()` with joined errors from distillation/streaming goroutines.
- **Workflow Engine Hardening**: Thread-safe HITL handler/store access via `sync.RWMutex`. Workflow execution timeout (default 30s) to prevent indefinite blocking. Proper `OutputStore.Close()` cleanup.
- **Event Store Hardening**: Errgroup-based compaction with timeout context (30s). Nil compactor/repo guards in all read paths. MemoryEventStore `Close()` returns `ErrEventStoreClosed` on double-close for idempotent shutdown.
- **LLM Client Validation**: Config validation enforces required `Provider` and `BaseURL` fields. `Close()` idempotency via `sync.Once`. OpenAI adapter properly handles `io.ReadAll` errors instead of silently discarding them.
- **MCP Client Hardening**: Nil client guard in tool registration. Godoc-style documentation for all public APIs. SSE transport fixes double-close of `resp.Body` (deferred close only in `receiveLoop`).
- **Memory Manager Config Validation**: Validates `MaxTasks`, `MaxDistilledTasks`, `DistilledTaskTTL`, and `VectorDim` are positive. `Stop()` collects all errors and returns them joined.
- **Crossover & Selection Validation**: `Crossover.Validate()` and `TournamentSelection.Validate()` methods for post-construction config invariance checking. Defensive nil checks and enum validation.

### Improvements

- Renamed project to **ARES** (Adaptive Resilient Evolution System)
- Enhanced scoring cache with atomic counters replacing `sync.RWMutex`
- Improved error visibility with structured error wrapping across all modules
- Added debug logging to TaskDispatcher and planner fallback warnings
- Default `DistilledTaskTTL` set to 30 days in `DefaultMemoryConfig()`
- Guarded all `CompactableEventStore` read paths against nil compactor/repo

### Bug Fixes

- Fixed data race in `DynamicExecutor` recovery path with proper timeout handling
- Fixed `MemoryEventStore.Close()` idempotency — second+ calls return `ErrEventStoreClosed`
- Fixed SSE transport double `resp.Body.Close()` causing panic on shutdown
- Fixed LLM client `Close()` race condition via `sync.Once`
- Fixed OpenAI adapter silently swallowing `io.ReadAll` errors in error paths
- Fixed `Population.ScoreAgents` panic recovery logging with agent context
- Fixed `updateBestEverLocked` concurrency safety with deep copy via `a.Clone()`
- Fixed `NewTaskPlanner`/`NewTaskPlannerWithConfig` silent fallback from invalid `maxTasks`
- Added nil validation in `leader.New`, `NewTaskDispatcher`, and `NewMCPManager`

## [0.2.2] - 2026-06-19

### New Features

- **Embedding Lifecycle Unification**: Unified embedding lifecycle across distillation, storage, and retrieval pipelines. Embedding workflows now share a common lifecycle model, reducing code duplication and ensuring consistent behavior during creation, update, and deletion of embeddings.
- **Context Cleaning**: Automatic context window management with tool call causality preservation. The context cleaner maintains causal ordering of tool calls during cleanup, preventing out-of-order execution after context truncation.
- **Workflow Enhancements**: `MutableDAG.ReplaceNode` for replacing nodes at runtime. Custom `RecoveryHandler` for failure recovery per workflow step. Enhanced event propagation across workflow execution.
- **Portfolio Simulator**: Investment portfolio simulation system with multi-asset backtesting. Includes research memory bridge connecting portfolio simulation results to the research memory system for data-driven investment decisions.
- **Investment Simulator**: Standalone investment simulation module for modeling and analyzing investment strategies.
- **CoinGecko Crypto Feed**: Real-time cryptocurrency price data integration via CoinGecko API, enabling live market data for trading analysis.
- **Public Marketmaking API**: Marketmaking API migrated from internal to public (`api/marketmaking/`), with multi-asset backtesting support. Includes comprehensive paper trading, chaos testing, and backtesting capabilities.
- **Quant Trading Example**: Complete quantitative trading example with SQLite backend, demonstrating end-to-end quant trading workflow.
- **Tool Lifecycle Events**: Emit lifecycle events for tool execution, enabling observability and monitoring of tool calls throughout their lifecycle.
- **Memory Metadata Propagation**: Expanded metadata propagation across memory operations, enriching context with session and agent metadata.
- **Concurrent Distillation Pipeline**: errgroup-based parallel embedding in distiller (concurrency limit 5), and concurrent experience storage in manager_impl.go, reducing end-to-end distillation latency.
- **Content Hash Dedup**: SHA-256 `content_hash` column on `distilled_memeries` with `ON CONFLICT (tenant_id, content_hash) WHERE content_hash IS NOT NULL DO NOTHING` for idempotent memory storage.
- **Idempotent Migrations**: All DDL operations now safe to re-run — `DROP IF EXISTS` + `CREATE` for policies/triggers, `IF NOT EXISTS` for indexes, `ADD COLUMN IF NOT EXISTS` for schema evolution.
- **Chinese Language Support**: Chinese keyword detection in `detector.go` (介绍/是什么/怎么/有哪些/区别/说说/推荐 etc.) and Chinese importance scoring in `scorer.go` (错误/修复/配置/框架/架构/优化 etc.), enabling experience extraction from Chinese Q&A.
- **Knowledge Correction Flow**: End-to-end correction pipeline in knowledge-base example — detects correction intent, calls LLM for structured commands (`UPDATE:`/`DELETE:`/`CREATE:`), executes DB writes for both `distilled_memories` and `knowledge_chunks_1024`. Supports correction via "纠错" keyword.
- **RAG Search Includes Corrected Memories**: `KnowledgeBase.Search()` now queries both `knowledge_chunks_1024` and `distilled_memories`, with corrected memories boosted in ranking.
- **Restart Script Import**: `scripts/docker/restart.sh --save <path>` option to import a document immediately after DB migration.

### Refactors

- Enhanced configuration safety with improved validation and error handling in the API layer (`api/config.go`, `api/service.go`).
- Renamed `quant-demo` to `quant-trading` with updated configuration and documentation.
- Enforced snapshot-only data constraint in analyst prompts to ensure data consistency.
- Replaced `WriteString(fmt.Sprintf(...))` with `fmt.Fprintf` across correction flow.
- Simplified loop with `append(..., distilledResults...)`.

### Bug Fixes

- Resolved data race and timing issues in `DynamicExecutor` recovery path.
- Fixed documentation file naming inconsistencies.
- Fixed all errcheck issues (unchecked `Close()` calls) in `cmd/` migration tools.
- Fixed De Morgan's law simplification in UUID validation.

## [0.2.1] - 2026-06-16

### New Features

- **MCP Client**: Model Context Protocol client implementation with JSON-RPC 2.0 messaging, stdio and SSE transport support, tool schema management, and connection lifecycle management.
- **Web Dashboard**: Real-time monitoring dashboard with WebSocket hub, REST API v2, orchestrator for multi-agent coordination, event bridge for system state streaming, and static asset serving.
- **Flight Recorder**: Multi-agent runtime intelligence recording with timeline tracking, decision logging, diagnostics engine, agent genealogy graph, DOT/JSON export, and replay pipeline.
- **Chaos Engineering Arena**: Fault injection framework with injector supporting process_kill, network_partition, latency_spike, and kill_orchestrator fault types; resilience scoring with configurable metrics; survival mode for continuous chaos testing; HTTP API and YAML scenario configuration.
- **Callbacks System**: Event-driven callback mechanism with typed event contexts, handler registry, and lifecycle hooks for agent/tool/runtime events.
- **LLM Output Parsing**: Multi-provider output adapters (OpenAI, Ollama, OpenRouter), prompt template engine with Go template syntax, function calling extraction and validation, schema-based parameter validation, and streaming output parser.
- **Function Calling**: LLM function calling support with tool schema generation, argument extraction, and result formatting.
- **Agent Genealogy**: Agent lineage tracking with parent-child relationships, birth/death event recording, and genealogy graph export.
- **Event Auto-Compaction**: Configurable event store compaction with retention policies, snapshot-based trimming, and automatic execution.
- **Tool Lifecycle Hooks**: Pre/post execution hooks for tools with context injection and error handling.
- **Quant Demo**: Quantitative analysis example with CSV data processing.
- **DevAgent Example**: Development agent example with workflow configuration.
- **MCP Dashboard Example**: Dashboard integration example with MCP transport.
- **Capability Demo**: Tool capability demonstration example.

### Improvements

- Pruned unused components and deduplicated code across runtime resurrection module
- Improved error visibility with structured error messages
- Extracted restore logic into reusable functions
- Exposed migration DDL for external tooling
- Cleaned up validators and reduced code duplication
- Streamlined dashboard frontend assets
- Generalized domain models for broader use cases beyond original fashion domain

### Bug Fixes

- Fixed various lint issues identified by golangci-lint
- Added `GetAgent` method to Runtime interface
- Wired `verifyRestoredState` in example code
- Corrected semaphore available count calculation
- Escaped password in DSN connections
- Added ILIKE pattern escaping for PostgreSQL queries

## v0.2.0 (2026-06-11)

### New Features

- **Leader Failover**: Checkpoint-based recovery with `LeaderSupervisor` detecting leader failure, recovering stale tasks from last checkpoint, and reassigning work to available sub-agents. `ColdRestartStrategy` for deterministic recovery.
- **Runtime Dynamic Graph**: `MutableDAG` with thread-safe mutation (add/remove nodes and edges at runtime). `DynamicExecutor` with `ApplyMode` for hot-reload without stopping execution. Incremental cycle detection on edge insertion.
- **Human-in-the-Loop**: `InterruptConfig` on workflow steps for human approval gates. `InterruptHandler` blocks execution until approved. `InterruptStore` provides crash recovery of pending approvals.
- **Agent Resurrection Plugin**: Pluggable `HealthChecker` interface for custom health detection. `HeartbeatAdapter` for heartbeat-based liveness. `Supervisor` for automatic agent restart on failure.
- **Event Sourcing**: `EventStore` interface with optimistic concurrency control. `MemoryEventStore` for dev/test, `PostgresEventStore` for production. 17 event types covering agent lifecycle, tasks, sessions, workflows, and failover. Pub/sub via `Subscribe` with filtered event channels. DLQ auto-retry with configurable retry budgets.
- **Pluggable Vector Store**: `VectorStore` interface replacing concrete `*VectorSearcher` in Repository. PostgreSQL + pgvector for production, in-memory for dev/test. Drop-in replacement support for Qdrant, Milvus, SQLite, or custom backends.
- **WorkflowService API**: High-level workflow orchestration abstraction over the DAG engine.

### Bug Fixes (46 fixes)

**Storage (12 fixes)**
- C1: Embedding queue dedup key mismatch causing duplicate embeddings
- C2: Write buffer data loss on `Stop()` before flush completes
- C3: Embedding enqueue outside transaction leading to orphaned records
- C4: `FetchPendingTasks` lock ineffective with `FOR UPDATE SKIP LOCKED`
- C5: Reconcile threshold time arithmetic off by orders of magnitude
- M1: `ManagedRow` connection leak on error paths
- M2: Missing migration tables in `migrate.go`
- M3: Circuit breaker `halfOpenInflight` counter leak
- M4: `VectorSearcher` missing dimension validation
- M6: FileWatcher TOCTOU race in `scanAndLoad`
- M7: Map reference shared unsafely in callbacks
- M8: Graph `Edge()` no validation of node endpoints

**Workflow (8 fixes)**
- C6: Panic recovery ordering in `executor.go` (recovery after cleanup)
- C7: Graph executor in-degree tracking incorrect after node removal
- H1: Deadlock false positive in `executor.go` (errgroup misuse)
- H2: `DynamicExecutor` hang on node removal during execution
- H3: `stepEg.Wait()` concurrent with `Go()` causing race
- H4: `NewDAG` silently dropping duplicate step IDs
- M5: `MaxAttempts=0` skips execution entirely
- M9: `recomputeOrder` version-check race on concurrent access

**AHP Protocol (7 fixes)**
- C8: Queue `send on closed channel` panic during shutdown
- C9: `HeartbeatSender` Start/Stop race condition
- H5: `getRandomSuffix` nil dereference on empty slice
- H6: `SendMessage` swallows all errors silently
- H7: `Protocol` has no `Close()` method (resource leak)
- M10: `Peek()` non-atomic read (race under concurrent access)
- M12: `DLQ.Remove` leaks trailing pointer after deletion

## [0.1.0] - 2026-04-19

### Added

- Initial multi-agent collaboration framework
- Memory management with distillation and retrieval
- Tool calling with ACE (Agent Capability Engine)
- Workflow engine with DAG-based orchestration
- PostgreSQL + pgvector integration
- Support for multiple LLM providers (OpenAI, Ollama, OpenRouter)
