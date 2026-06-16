# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

## v2.0.0 (2026-06-11)

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

## v2.0.0 (2026-06-11)

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

**Agent System (8 fixes)**
- L6: `Start` partial validation cleanup (inconsistent state on error)
- L7: SubAgent `ProcessStream` goroutine leak on context cancellation
- L8: `doFailover` uses cancelled ctx for Stop (fails to clean up)
- L9: `Dispatcher` partial results misleading (reports success on partial failure)
- L10: Dynamic executor uses bare `go` instead of `stepEg`
- L11: Missing `SnapshotWithSteps()` on `MutableDAG`
- M11: `NewTaskMessage` allows nil payload
- BUG-5: Dead verification in `pg_store_test` `ConcurrentAppend`

**Event Sourcing (5 fixes)**
- BUG-1: `FromVersion` inclusive/exclusive boundary wrong in `memory_store.go`
- BUG-2: `Since` filter inclusive boundary wrong in `pg_store.go`
- BUG-3: `ReadAll` incorrectly applies `FromVersion` filter in `memory_store`
- BUG-4: `Append` does not return `ErrVersionConflict` on unique violation in `pg_store`
- STYLE-1: Bare `go` keyword without context cancellation in event store

**Other (6 fixes)**
- L1: `safeFormatTable` returning empty string on valid input
- L2: Missing immediate retry after flush failure in write buffer
- F1: `workflow_test.go` config mismatch with actual types
- Executor nil pointer check on context cancellation (commit 9565f79)
- STYLE-2: Channel buffer size 64 changed to 1 (backpressure)
- STYLE-3: Empty slice literals replaced with nil returns

### Infrastructure

- CI/CD pipeline via GitHub Actions (lint, test, race detection, build)
- Integration tests for workflow engine (5 test cases)
- Benchmark suite (32 benchmarks across 8 categories)
- Bilingual documentation (English + Chinese) in `docs/en/` and `docs/zh/`
- Reorganized documentation into language-specific directories
- `.golangci.yml` configuration for consistent linting

### Breaking Changes

- `NewLeaderSupervisor` signature changed: added `eventStore` parameter
- `NewColdRestartStrategy` signature changed: added `checkpoint` parameter
- `MemoryManager` interface added `GetLatestSessionForLeader` method
- `VectorStore` interface replaces concrete `*VectorSearcher` in `Repository`
- `NewResultAggregator` signature changed: added `sortBy string` parameter
- `TaskPlanner.Plan` signature changed: added `inputText string` parameter
- `ResultAggregator.Aggregate` signature changed: added `tasks []*models.Task` parameter
- Domain types renamed: `FashionFilters` -> `ResourceFilters`, `FashionItem` -> `ResourceItem`, `AgentProfile` -> `AgentUserProfile`, `AgentRecommendation` -> `TaskRecommendation`, `OutfitSuggestion` -> `Suggestion`, `AgentTrend` -> `Trend`

## [0.1.0] - 2026-04-19

### Added

- Initial multi-agent collaboration framework
- Memory management with distillation and retrieval
- Tool calling with ACE (Agent Capability Engine)
- Workflow engine with DAG-based orchestration
- PostgreSQL + pgvector integration
- Support for multiple LLM providers (OpenAI, Ollama, OpenRouter)
