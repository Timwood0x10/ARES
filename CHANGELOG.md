# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

> Security & hardening (v0.3.0 review round): JWT auth + RBAC + modular audit, runtime config hot-reload, chaos-recovery e2e and agent-pool benchmarks, versioning, and quality-gate compliance.

### Added

- **JWT authentication + RBAC** (`internal/ares_security/`): stdlib HS256 token
  sign/verify, role hierarchy (`admin`⊃`operator`⊃`agent`) with a read/write/admin
  permission matrix, and net/http + gin middleware (default deny). Destructive
  console endpoints accept either the legacy API key **or** a valid JWT
  (dual-credential, backward compatible). `ares auth token` CLI mints tokens.
  Configure via `security.jwt_secret` / `ARES_JWT_SECRET` / `ARES_AUTH_ENABLED`.
- **Modular audit logging** (`internal/ares_security/audit.go`): structured
  `AuditLogger` records auth decisions and destructive actions (kill/resume/
  retry, MCP tool calls); tokens are never logged.
- **Runtime config hot-reload** (`internal/ares_config/store.go`): `ConfigStore`
  with fsnotify watcher (200ms debounce); failed reloads keep the last-good
  config and are recorded in history. `/runtime/config` endpoint serves the
  redacted snapshot + history (`Config.Redacted()` masks API keys, DB passwords,
  JWT secret).
- **Chaos-recovery e2e** (`internal/ares_arena/e2e_chaos_recovery_test.go`):
  crash-detection → resurrection through the real arena→runtime wiring, at
  8/16/64/128 agent pool scales.
- **Agent-pool benchmarks** (`internal/ares_runtime/benchmark_agent_pool_test.go`):
  concurrent register/start/stop lifecycle and resurrection throughput.
- **CI**: `.github/workflows/agentos_ci.yml` (chaos e2e + benchmark sanity),
  codecov upload in `ci.yml`, coverage/CI badges in README.
- **Versioning** (`VERSION` file + Makefile injection): `make build` embeds
  `0.3.0-dev` into `main.version`; `ares version` prefers the injected version,
  falling back to build-info pseudo-version. Deprecation policy documented in
  `docs/design/versioning.md`.
- **P0-P2 plan items** (AGENTOS_DEVELOPMENT_PLAN.md §6): security layer, config
  hot-reload, and fault-injection e2e all marked implemented.

### Changed

- **P3 resource governance** (`internal/agentfabric/governance.go`):
  `SpawnSpec.Governance{TokenBudget, ToolBudget, Deadline}` + `CheckResource` /
  `ConsumeResource` / `DeadlineExceeded` / `ResetResource` / `BudgetUsage`.
  Zero-value = unlimited (legacy agents unchanged); exceeding a budget returns
  `ErrResourceExceeded` — the cooperative yield signal (not cgroups). Demo
  `examples/aresos-demo` updated with a governed-agent step.
- **Agent-OS Grand-Loop e2e + demo** (`aresos-plan.md` 附件 E): a single
  continuous scenario (large task → A spawns B/C/D as peers → parallel work →
  A dies → task survives → peer IPC collaboration → replacement resumes →
  synthesis) as `internal/agentfabric/e2e_grand_loop_test.go` and a zero-dep
  runnable demo at `examples/aresos-demo/`. Uses only public agentfabric /
  agentipc APIs; no library code extended.
- **M1 collaboration wired into production IPC** (`cmd/ares/evolution_ipc.go`):
  the `wireEvolutionIPC` bus handler now dispatches by topic —
  `delegate-task`/`pipeline-stage`/`orchestrate-worker` messages reaching a
  sub agent run through its `Execute` capability and reply with the result,
  closing the "library-ready, not wired" gap for `DelegateToSpecialist` /
  `Pipeline` / `Orchestrate`. Peer messages (the production channel) are
  unaffected. Tests cover execution, rejection paths, and peer/collaboration
  non-interference.
- **`cmd/ares/serve.go` split**: runtime helpers moved to
  `cmd/ares/serve_routine.go` (~900 lines) to keep the serve entry readable.
- **`createLLMAdapterWithFallback`**: returns `ErrNoLLMAdapter` sentinel,
  detectable with `errors.Is` while retaining the underlying error.
- **`internal/llm/output/parser.go`**: `extractJSON` now keeps whole arrays
  (`[{...},{...}]`) instead of truncating to the first object (bug fix).
- **`cmd/ares/dev.go`**: `ares version` prefers the ldflags-injected version
  over the module pseudo-version.

### Fixed

- **`MemoryProvider.Stream` nil-searcher panic** (found via run log
  `scheduler_trace_with_logs.log`): a `MemoryProvider` wired without a backing
  searcher nil-deref'd in `Stream` and crashed `ares serve` (SIGSEGV). It now
  degrades to an empty stream. Regression test:
  `TestStream_NilSearcherDoesNotPanic`.
- **OTel resource merge** on SDK upgrade: `ares_observability` aligns the schema
  URL with `resource.Default()` to avoid "conflicting Schema URL" failures.
- **`ConfigStore` watcher race** in tests: settle window before rewrite.
- **Health-check test gap**: `Manager.healthCheck()` now covered for dead
  (resurrect) and alive (no-op) heartbeaters.
- **`actionHandler` auth/audit bypass** (code review): `POST /api/agents/:id/{kill,
  resume,retry}`, `/api/chaos/*` and `/api/tools/call` are intercepted by
  `actionHandler` before the gin router — the JWT middleware and destructive-
  action audit never ran there. The handler now accepts API key **or** JWT and
  records every destructive action on the audit sink.
- **Dead code removal** (code review): removed zero-caller `WithAuditLogger`,
  `WithClock`, `WithGinMode` options.

> Capability Fabric (SkillCatalog) release: declarative skill sources, zero-scan progressive disclosure, lazy MCP activation, and a closed agent loop from skill discovery to execution feedback.

### Capability Fabric (`internal/ares_skills/`)

- **SkillCatalog** (`catalog.go`): facade over declarative skill sources — project `.ares/skills`, user `~/.ares/skills`, and `[[skill_sources]]` from `~/.ares/config.toml` (directory / git / http-oci). Indexes metadata only (zero disk scanning beyond declared roots) with content-hash change detection.
- **Progressive disclosure** (`indexer.go`, `loader.go`, `discovery.go`): Level-0 resident metadata (name + description in the memory manager), Level-1 SKILL.md body on demand, Level-2 resolved tools. FTS5 full-text search with a keyword fallback.
- **Lazy MCP activation** (`resolver.go`): MCP servers are connected only when a skill declaring them is activated (via `skill_activate`); `ares_mcp.MCPManager` satisfies the `MCPConnector` interface. Executable / builtin carriers resolve through a unified trust-gated resolver.
- **Experience prior** (`experience.go`, `experience_store.go`): learned-source relevance ranking with JSON persistence; task outcomes feed back through `SkillOutcomeRecorder` (subscribing to `EventSubTaskResult`), with keyword-overlap scoring and truncated task patterns.
- **Agent-facing tools** (`tools.go`): `skill_search` / `skill_load` / `skill_activate` / `skill_list` / `skill_experience` registered into the serve tool registry — closing the Discover → Load → Execute loop in the LLM main loop.
- **Memory bridge** (`catalog.go` → `skills.Registry`): resident skill block in the memory manager with on-demand `LoadDetail` via `SetDetailLoader` (Level-1 no longer returns empty bodies).
- **Runtime robustness**: `Refresh` re-syncs git sources and re-fetches http manifests outside the index write lock; MCP `listChanged` notifications are debounced; git sync is bounded by a 2-minute timeout so an unreachable host degrades to local-checkout indexing.

### Wired into `cmd/ares/serve.go`

- `wireSkillCatalog` seeds the memory registry, registers the five skill tools, and starts the `SkillOutcomeRecorder` against the serve event store.
- `createAgents`/`createLeaderAgent` accept an optional `leader.ExperienceLocator` that pre-fills `task.UsedExperienceID` from the catalog's best matching skill — the record-side attribution for the feedback loop.

### CLI (`cmd/ares`)

- **`ares status`** (`status.go`): one-command runtime overview. It probes the dashboard API (system health + live agent fleet) when `ares serve` is up, resolves the effective configuration (config file vs minimal assembly, LLM endpoint, kernel policy, memory, agent team, storage) and reports the Capability Fabric assets (indexed skills + accumulated experience records). Text and `--json` output; exit code 0 healthy / 1 on warnings (e.g. memory disabled or runtime unreachable).
- **`ares serve --autopilot`** (`serve.go`): opt-in demo task injector (default off). Submits the autopilot demo tasks through the leader at startup so `serve` can be observed end-to-end without manual task submission; the E2E suite (`serve_e2e_test.go`) and the `26-runtime-scheduling-demo` logs run with it enabled.
- `ares_config.DefaultLeaderID` exported so CLI tooling can report the assembled default agent team without duplicating the literal.

### Fixed

- **Kernel dispatch fake success** (`cmd/ares/kernel.go`): `kernelTaskDispatcher.Dispatch` unconditionally reported `SetSuccess(nil, "dispatched via kernel")` for every task, so the leader aggregated empty results (`items=0`) while the scheduler actually executed the work — the `EventSubTaskResult` reflux was bypassed and no producer existed in production. The dispatcher is now event-driven: it subscribes to `EventTaskCompleted/Failed` (broadcast), submits tasks through the fabric, waits for the real terminal event (with the same 300s timeout contract as the leader dispatcher), and rebuilds the `TaskResult` from the fabric checkpoint (`items`/`reason`/`metadata`) plus the original task's `UsedExperienceID`. Legacy sync path (no fabric) keeps immediate success; fabric present but no event store fails explicitly. `flipKernelToTaskFabric` injects the fabric reference and wires the event store so `fabric.record` emits externally. `user_profile` is now passed through to the executor (struct reference), so the LLM path no longer degrades to the empty `executeByType` fallback. Contract tests cover result reflux + `UserProfile` passthrough, timeout, worker failure, and the legacy/batch adapters.
- **Kernel DAG wiring** (`cmd/ares/kernel.go`): `taskFromPayload` now accepts `dependencies` as both `[]string` (in-memory hop via `kernelTaskDispatcher.Dispatch`) and `[]any` (JSON round-trip). Previously the `[]any`-only assertion silently dropped every DAG edge on the Task Fabric path, defeating the `IsReady` gate (ares-runtime.md §9). Test extended to cover both shapes.
- **Planner DAG truncation** (`internal/agents/leader/planner.go`): dependency resolution now runs *after* the `maxTasks` truncation. Previously a retained task could depend on a truncated task — a dangling reference that permanently blocked the Task Fabric's `IsReady` gate (deadlock). Regression test `TestPlan_DependenciesAfterTruncation` covers the truncation-dependency interplay.

## [0.3.0] - 2026-08-11

> Candidate release closed-loop release: a stateful candidate pipeline with three-gate verification, a release-time LLM-driven regression gate, batch request merging, and multi-provider LLM support.

### Candidate Pipeline (`internal/evolution/`)

Evolved strategies now ship through a layered, verifiable, releasable **candidate** lifecycle instead of a one-shot Arena pass:

- **`Candidate` + `CandidateStore`** (`candidate.go`): a stateful candidate object with a lifecycle (`candidate → verified → promoted/rejected`); `CandidateStore` is concurrency-safe (`sync.RWMutex` on Submit/Get/List) and assigns unique sequential IDs.
- **`CandidateVerifier`** (`candidate.go`): three-gate verification — Gate 1 `staticCheck` (structural integrity + dangerous-pattern rejection), Gate 2 `replayFailureCases` (verifies referenced failure evidence exists and is `KindDimensionEval` via an injected `evidence.Store`), Gate 3 `checkRegression` (preserved-case regression, injectable).
- **`CandidatePipeline`** (`candidate_pipeline.go`): `Release` accepts only `StatusVerified` candidates, runs the coordinator decision (Apply/Reject/Delay) → canary → `SetStable` → `Promote`.
- **Release-time gate-3** (`WithReleaseRegressionCheck` + `NewCandidatePipelineWithOptions`): the regression check runs **before any patch is built/applied**; on failure the candidate is `Reject("release regression gate: ...")` and neither runtime nor stable is touched. Backward compatible when not wired.
- **Dead-code removal**: removed the legacy `CandidateStore.promoteToStable`/`applyDiff` (superseded by `CandidatePipeline.Release`), eliminating the dual promotion path.

### Gate-3 LLM Regression

- **`CandidateRegressionChecker`** (`candidate_regression.go`): runs the `ares_arena.RegressionTester` over preserved cases comparing `stable.Instructions` vs `candidate.Diff`; rejects on a statistically significant drop (`Confident && NewAvg < OldAvg`). Configurable runs / min win rate / timeout.
- **`LLMArenaScorer`** (`internal/ares_evolution/service/llm_arena_scorer.go`): implements `ares_arena.Scorer`, driving a real LLM in two steps (execute instructions×case → grade output on [0,1]). Scoring prompt stays open-ended (anchored rubrics measurably weaken significance).
- **`BuildRegressionGate3` / `LoadRegressionGate3`** (`gate3_orchestrator.go`): top-level assembly — `LLMClient → LLMArenaScorer → CandidateRegressionChecker`; `LoadRegressionGate3` reads `llm.Client` from a YAML config and supports ollama (keyless) / openai (keyed). The same check is injectable into both verify and release.

### Batch Request Merging

- **`ares_arena.BatchScorer`** (`regression.go`): optional interface (`ScoreBatch(ctx, strategy, count, testCases)`); `RegressionTester.runStrategy` collapses all runs of a strategy into one batch call, falling back to per-run concurrent `Score` for non-batch scorers (backward compatible).
- **`LLMArenaScorer.ScoreBatch`**: collapses count executions + gradings into exactly 2 LLM calls (one batch execute + one batch grade). One regression drops from `2×runs` calls to 2 — critical for low-rpm providers.

### Reliability & Hardening

- **`CandidateStore` concurrency safety**: full `sync.RWMutex`; `TestCandidateStore_ConcurrentAccess` (32 goroutines × 50 submits, unique IDs) passes `go test -race`.
- **Gate-2 evidence verification**: `replayFailureCases` queries the evidence store to distinguish missing vs wrong-kind evidence (no fabricated IDs).
- **Verifier state machine**: `Verify()` advances the state — all gates pass → `StatusVerified`; any failure → `StatusRejected` + `RejectionReason`; nil-candidate guard.

### Documentation & Examples

- **Articles (ZH/EN)**: `docs/articles/zh/11-autonomous-evolution-deep-dive.md` + `docs/articles/en/11-autonomous-evolution-deep-dive.md` gained "0.3.0: From Strategy Evolution to Candidate Release Closed-Loop"; `docs/articles/zh/24.7-autonomous-evolution-overview.md` gained a 0.3.0 section.
- **README / README_CN**: added "Candidate Release Closed-Loop (0.3.0)" section and a feature-table row.
- **Examples with full logs**: `examples/16-llm-regression-demo` (real regression comparison), `examples/17-gate3-e2e-demo` (verify e2e), `examples/18-release-closed-loop` (full release closed loop); each writes a timestamped transcript to its own `logs/run-<ts>.log`.
- **Live verification (agnes-2.5-flash, batch)**: bad candidate rejected at verify and at the release gate (`avg dropped 1.000 -> 0.000, p=0.0000`); good candidate `verified → promoted`.

## [0.2.9] - 2026-08-05

> Runtime closure & persistence release: System Runtime lifecycle kernel, PostgreSQL evidence persistence, SDK/Bootstrap unification, closed-loop GA feedback, and config-driven assembly.
> This release closes the Runtime Component Closure plan (stages 0–9): one lifecycle kernel (Orchestrator) shared by serve/start/SDK, real feedback loops (Event → Evidence → GA → Strategy → Agent → experience), persistent evidence across restarts, and a single config-driven entry path (`sdk.NewRuntime(sdk.WithYAMLFile("ares.yaml"))`).

### System Runtime Lifecycle Kernel

A unified component lifecycle kernel replaces per-entry startup/shutdown wiring:

- **Orchestrator** (`internal/system_runtime/orchestrator.go`): Reverse-topological Start/Stop across the registered component graph with state transitions (Constructed → Bound → Started → Ready → Stopping → Stopped) and error aggregation.
- **Registry + Snapshot** (`internal/system_runtime/`): `Registry.Register`/`Names`/`GetMode`, `Snapshot()` returns per-component status JSON (`ComponentStatus`/`IsSystemReady`), wired into Bootstrap as `comp.SystemRuntime`.
- **Degraded state (F03)**: Adapters implement `ReadinessChecker`; a component with missing write deps (e.g. AKG retrieval enabled without DistillBridge) reports **Degraded** with a reason instead of silently claiming Ready. Closure test is a hard assertion.
- **Entry unification**: serve, start (monitor-live), and SDK all assemble through `ares_bootstrap.Bootstrap`; component graph equivalence across entries is locked by contract tests (`closure_entry_equivalence_test.go`).

### Evidence Persistence (PostgreSQL)

GA feedback and runtime evidence survive restarts instead of resetting to baseline:

- **`evidence.PostgresStore`** (`internal/evidence/postgres_store.go`): `Append`/`Query`/`Aggregate` over a `evidence_records` table (source/kind/payload/metadata/ts), mirroring MemoryStore semantics with time-window filtering.
- **Interface-ized store**: `EvidenceStore` fields changed from `*evidence.MemoryStore` to `evidence.Store` across Bootstrap, evolution genomes, and the deployment staging runtime; `ProvideNewEvolution` accepts an optional persistent store (nil → in-memory default).
- **Entry opt-in, fail-loud**: Bootstrap wires PostgresStore when `storage` is configured; SDK wires it when `database.host` is set and the SDK-owned evolution path is active. Connection failure blocks startup (no silent fallback).

### GA Feedback Loop (closed)

- **Track A closed**: strategy outcomes are written back to the experience store (`recordStrategyOutcome` in `provide_distillation.go`) so the next mutation round reads real hints — previously a silent no-op.
- **Six genomes** evolve runtime strategies (workflow topology, scheduler, knowledge retrieval, recovery, memory, prompt) with a real `Event → Evidence(fitness) → GA → StrategyStore → Agent` loop; feedback-loop tests assert real data flow (fitness 1.0/0.0 observed in the shared store).

### SDK / Bootstrap Unification

- SDK `New()` assembles the core component graph through `ares_bootstrap.Bootstrap`; EventStore / NewEvolution / System Runtime instances are shared, not duplicated (`bootstrap_runtime.go`, `bootstrap_shared_instance_test.go`).
- `Runtime.Close()` drains Bootstrap background goroutines via `WaitBackground()` (same lifecycle kernel as serve); PostgreSQL evidence pool is closed on Close (leak fixed).
- **F04 live DAG**: `buildLeaderLiveDAG` (serve) registers the leader's real workflow DAG pre-Start; `wireEvolutionLiveDAGs` binds executors to it instead of the synthetic placeholder. Synthetic DAGs are isolated to the `"evolution"` key (hard assertion).

### Reliability & Hardening

- **Chaos fault injection** (`b5a8ae2a`): injected failures, failover, survival tests, self-healing.
- **SSRF defense** (`102fb75b`): hardened network transport with import allowlist; sandbox/streaming paths repaired (`bd7d3816`).
- **MCP-style tool discovery** (`9a8c7f1d`): `tool_discovery` + tool source registration.
- **AKG closed loop** (`fb4e06e0`, `7d430b5f`, `147d8c8b`): full AKG loop with relevance scoring + LLM-free knowledge graph implementations.
- **State-aware LLM evolution suggestions** (`ac69f066`) + graceful shutdown fixes; flight fitness write loop closed across all deployment paths (`dcad4236`).
- **Config gating / shutdown timeout / null-pointer fixes** in bootstrap (`95e7c1d9`); memory system cleanup and auth/tooling hardening (`d5f3aed3`).

### Documentation & Examples

- **New `config.yaml` guide** (EN/ZH): `docs/articles/en/25-config-yaml-guide.en.md` / `docs/articles/zh/25-config-yaml-guide.zh.md` — LLM, distillation, GA evolution, knowledge, tools, and chaos-related switches, verified against actual `yaml:` tags.
- **README / README_CN / framework comparisons updated** to v0.2.9 capabilities (System Runtime, evidence persistence, six-genome loop).
- **Examples**: `08-mcp-integration` demonstrates the System Runtime `Snapshot()` API; live-DAG entry tests (`cmd/ares/serve_live_dag_test.go`).
- Cleanup: removed stale review artifacts (`outputs/`), obsolete plans (`AKG_DEV_PLAN_029`, `MASTER_DEV_PLAN`, `EXTERNAL_API_GUIDE`), and unreferenced top-level docs.

## [0.2.8] - 2026-07-29

> Public API layer + Unified DAG Runner + Context Compression Archive + Logic closure fixes.
> Eight new public API packages expose agents, workflows, evolution, knowledge, embedding, experience, and graph — all without importing `internal/`. Plus self-healing evolution, YAML-driven distillation gating, brand assets, five new GA technical articles, the unified DAG workflow runner, the context compression archive module, and comprehensive reliability closure fixes across mutation idempotency, checkpoint/resume, and observability collectors.

### Unified DAG Runner (`internal/workflow/`)

The legacy `graph.Graph` + `engine.Workflow` dual runtime architecture has been unified into a single IR-based pipeline:

- **WorkflowSpec IR** (`internal/workflow/spec.go`): Single intermediate representation with `NodeSpec`, `EdgeSpec`, `ConditionExpr`, `NodeID`, `LoopSpec`, `ScheduleSpec`, `RetrySpec`, `RecoverySpec`, `InterruptSpec`. Both `engine.Workflow` and `graph.Graph` compile to this IR.
- **Unified Runner** (`internal/workflow/runner.go`): Single `Runner.Execute(ctx, spec)` — FIFO scheduler, condition evaluation, interrupt handling (HITL), recovery policies, loop support, runtime mutations via `PatchQueue`. `RunningWorkflow(ctx, spec, functions)` convenience entry point.
- **Atomic Checkpoint/Resume** (`internal/workflow/runner_checkpoint.go`): `ResumeExecution()` with spec hash verification, scheduler state restoration, pending mutation re-queuing, pending interrupt restoration. Schema v3 with durable event sequences.
- **BoundWorkflow Compiler** (`internal/workflow/compiler.go`, `binding.go`): `CompileFromEngine()` / `CompileFromEngineWithBindings()` / `CompileBound()` convert legacy `engine.Workflow` and `graph.Graph` to executable `BoundWorkflow` with predicate and router closures.
- **Edge-Activation Scheduler** (`internal/workflow/scheduler.go`): Incremental topological scheduler with conditional edge evaluation, branch-skipping, JoinAll/JoinAny/Merge join policies, ready-queue with configurable selectors.
- **ExecutionScope** (`internal/workflow/scope.go`): Transactional state (pending→committed), per-node status tracking, loop history, pending interrupts, event sequencing, `ExecutionScoped` collector.
- **Typed Mutation / PatchQueue** (`internal/workflow/mutation.go`, `patch_queue.go`): Six typed mutations (add/remove/replace node, add/remove edge, update policy). `PatchQueue.Enqueue` with dedup, `Acknowledge` prefix commitment, `Restore` from checkpoint. Safe-point atomic application pipeline.
- **Native Runner Events** (`internal/workflow/runner_events.go`): 12 typed event types (started/resumed/started/completed/failed/skipped/interrupt pending/resolved/checkpoint saved/mutation applied/completed/failed) with ordered sequencing via `RunnerEventSink`.
- **Legacy Cleanup**: `CLOSURE_PLAN.md`, `ORPHAN_MODULES.md`, `ZERO_FRICTION_PLAN.md`, `outputs/DAG_UNIFIED_MERGE_*` plan documents deleted. `engine.NewDAG`/`NewExecutor`/`DynamicExecutor`/`Graph.execute` production paths fully replaced.
- **466 files changed**, +37077 insertions, -17976 deletions across the DAG unification branch.

### Context Compression Archive (`internal/ares_archive/`)

A new archive module that preserves structured per-round records before compaction discards raw conversation events:

- **RoundRecord** (`record.go`): Per-round structured entry with `Round`, `Action`, `Summary`, `Files` (P1 file-change list), `Verdict` (P2 pass/fail), `Decisions` (P0 architecture decisions), `Refs` (P3 identifier protection). JSON-serialized as `round_N.json`.
- **File Archive Writer** (`writer.go`): Atomic write (temp file + rename), round rotation (configurable `maxRounds`), concurrent-safe. `NewFileArchiveWriter(dir, maxRounds)` creates the directory on demand.
- **File Archive Reader** (`reader.go`): `Read(n)`, `List()`, `Search(query)` (case-insensitive substring across Summary/Decisions/Files/Refs), `Recall(query)` (human-readable multi-round output). Missing/empty directory handled gracefully.
- **Identifiers Protection** (`identifiers.go`): Compiled regexes for P3-level protection — commit hashes (7+ hex), PR/issue numbers (`#\d+`), IP:port, owner/repo paths. These identifiers are preserved verbatim during extraction.
- **Event Archive Sink** (`sink.go`): Bridges `ares_events.CompactableEventStore` to the archive writer via `ArchiveSink` interface. `BuildRoundRecord()` extracts RoundRecord from raw events.
- **Compactable Store Integration** (`store.go`): `NewCompactableStoreWithArchive()` creates an archive-enabled event store. When enabled, each round's record is flushed before compaction discards the raw events.
- **17 files**, +2896 lines new code.

### SDK Layer (`sdk/`)

- **Zero-Friction Agent SDK** (`sdk/sdk.go`, `options.go`, `config.go`, +450 lines net): `MustNew`/`New` runtime builder with functional options (`WithAgent`, `WithLLM`, `WithMCP`, `WithMemory`, `WithRAG`, `WithWorkflow`, etc.). Three execution modes: `RunAgent` (blocking), `RunStream` (streaming events), `RunTeam` (multi-agent orchestration).
- **SDK RAG Support** (`sdk/rag.go`, `sdk/memory_wiring_test.go`, +350 lines): YAML-driven RAG configuration with `enable_rag`, `rag_top_k`, `rag_min_score`. Wiring tests verify the end-to-end configuration→runtime path.
- **Runtime Evolution Config** (`sdk/evolution.go`): Strategy configuration for GA evolution within the SDK.

### Logic Closure Fixes

Comprehensive reliability closure across the runtime and evolution systems:

- **Mutation Idempotency** (`internal/evolution/patch/patch.go`): `RuntimePatch` gains `ID string` field. `Registry.applied map[string]bool` tracks already-applied patches. `Apply()`/`ApplySet()` silently skip duplicate IDs — prevents re-delivery attacks.
- **Resume Collector Persistence** (`internal/workflow/runner_checkpoint.go`): `CheckpointSnapshot.CollectorData` preserves route/tool/memory/interrupt/error history across crashes. `ExecutionCollector.Import()` restores data on resume. `ResumeExecution()` reuses the caller's `ExecutionCollector`.
- **Sub-Workflow Collector Merge** (`internal/workflow/runner_execution.go`): Child workflow collector data merged back into parent scope via `parent.Collector().Import(child.Collector().Export())`.
- **Graph.Node() Duplicate Detection** (`internal/workflow/graph/graph.go`): `Graph.Node()` now returns error on duplicate node IDs (previously silent overwrite). `Graph.Edge()` deduplicates (from, to, condition) pairs.
- **WorkflowSpec Validator** (`internal/workflow/validate.go`): `validateDuplicateEdges()` checks for duplicate (From, To, Kind) edge triples.
- **Normalization** (`internal/workflow/engine/types.go`, `mutable_dag.go`): `strings.TrimSpace` applied to step IDs in `NewDAG()` and `AddNode()`. `DependsOn` arrays deduplicated.
- **Chaos Methods Return Error** (`internal/ares_runtime/manager_chaos.go`): 4 empty chaos stubs (`PartitionNetwork`, `CorruptMemory`, `DisconnectMCP`, `InjectLLMFailure`) now return `ErrNotImplemented` instead of silent nil.
- **StartAgent Event Emission** (`internal/ares_runtime/manager_lifecycle.go`): `Start()` now emits `EventAgentStarted` for agents registered before `Start()`, closing the event-sourcing gap.
- **Event Drop Counter** (`internal/ares_runtime/bus.go`, `internal/ares_events/memory_store.go`): `droppedEvents` atomic counters added to both `PluginBus` and `MemoryEventStore`. Drops are logged with event type and stream ID.
- **PauseAgent State** (`internal/ares_runtime/manager_chaos.go`, `manager.go`): Independent `paused` flag added to `managedAgent`. `AgentInfo.Paused` exposed to callers. `NotifyAgentDead` and `healthCheck` skip paused agents.
- **Patch Executor Locking** (`internal/workflow/graph/patcher.go`): `applyInsertNode`, `applyRemoveNode`, `applyReplaceNode` all now hold `graph.mu` while reading/writing `nodes` map — closing data race windows.
- **argIdx++ Dead Code Removed** (`internal/ares_events/pg_store.go`): Removed the unused `argIdx++` after the last query parameter.
- **Plugin Panic Structured Logging** (`internal/ares_runtime/bus.go`): Recovered panic values logged with `slog.Default().Error()`, including `panic_type` and `panic_value` fields.
- **Runner Validate at Entry** (`internal/workflow/runner.go`): `validateExecutionInput()` calls `Validate(spec)` on every `Execute()` — catches duplicate nodes/edges before execution.
- **FitnessGenome Wiring** (`internal/ares_evolution/genome_wiring_run.go`): `submitToCoordinator()` now queries registered genomes for `FitnessGenome` scores instead of hardcoding `Fitness: 0`. Falls back to 0.5 baseline.
- **Dashboard LLM/MCP Wiring** (`api/bootstrap/bootstrap.go`): `dashboardMCPAdapter` and `dashboardLLMAdapter` bridge `*ares_mcp.MCPManager` and `*llm.Client` to the dashboard `MCPExecutor`/`LLMExecutor` interfaces. Previous TODO (expected 2026-09-30) resolved.
- **LazyLoading Budget Clamping** (`internal/knowledge/runtime/runtime.go`): `cfg.LazyLoading=true` now clamps `budget.ForGraph` to 2000 tokens before the reduce step, producing a genuinely smaller graph.

### Context Compression Archive (cont.)

- **BuildRoundRecord** (`extract.go`): 578 lines — extracts round summary, action categorization, file changes, decisions, Refs from raw conversation events. Action inference handles Chinese keywords (修复/审查/设计/实现).
- **In-Memory Demo** (`internal/ares_archive/`, `examples/13-archive-akg-chain/`): Full pipeline demo reading `.workbuddy/memory/` → processing through AKG knowledge pipeline → structured knowledge objects. Example README documents real capabilities and limitations.

### Public API Layer (`api/`)

Eight new public API packages, all re-exporting internal types via type aliases so external callers never import `internal/`. The public surface is now stable and documented in `api/README.md`.

- **`api/agent`** (`agent.go`): `Agent` interface for creating, running, and streaming from agents. Re-exports `AgentType`, `AgentStatus`, `EventType`, `AgentEvent` from `internal/agents/base`. Built-in agent type constants (Leader, Top, Bottom, Destination, Food, Hotel, Itinerary).
- **`api/workflow`** (`workflow.go`): Public workflow API re-exporting `Workflow`, `Step`, `NodeRouter`, `RetryPolicy`, `RecoveryPolicy`, `LoopConfig`, `InterruptConfig`, `ConditionFunc`, `AgentFactory`, `WorkflowResult`, `StepResult` from `internal/workflow/engine`.
- **`api/evolution`** (`evolution.go`): Public strategy evolution API — `Strategy`, `Lineage`, `Population`, `DreamCycle` orchestrator, GA `Population`, mutation (`pubmutation`), and promotion subsystems. External modules can evolve strategies without coupling to `internal/ares_evolution`.
- **`api/knowledge`** (`knowledge.go`, `service.go`): Public Knowledge Fabric API with `KnowledgeObject`, `KnowledgeLink`, `KnowledgeGraph`, `Provider` interface, and `Service` facade. Storage-agnostic: back it with PostgreSQL, SQLite, memory, or any custom provider.
- **`api/graph`** (`graph.go`): Public DAG API re-exporting `Graph`, `Node`, `Edge`, `State`, `Result`, `Condition`, `NodeRouter`, and five scheduler types (`Default`, `Priority`, `ShortJob`, `RoundRobin`, `WeightedFair`).
- **`api/embedding`** (`service.go`): `EmbeddingService` interface for vector embedding operations. Storage-agnostic — callers may back it with PostgreSQL, SQLite-vec, pgvector, or any vector database. `Embed()`, `EmbedWithPrefix()`, `BatchEmbed()` methods.
- **`api/experience`** (`types.go`, `repository.go`): Public experience storage and memory distillation DTOs. `ExperienceRepository` interface lets external modules implement experience persistence with any vector database. Four `MemoryType` constants: `knowledge`, `preference`, `interaction`, `profile`.
- **`api/service/workflow`** (`service.go`): Workflow service bridge updated to work with the new public workflow API.

### Self-Healing Evolution System

- **Self-Healing Coordinator** (`internal/evolution/coordinator/coordinator.go`): `Coordinator` now orchestrates self-healing evolution — detecting runtime regressions and automatically proposing corrective patches. +108 lines of coordinator logic.
- **DAG Runtime Registration** (`internal/ares_runtime/manager.go`): Runtime manager gains +29 lines for DAG runtime registration, enabling evolution patches to target the DAG topology. `internal/ares_bootstrap/bootstrap.go` wires the new registration (+7 lines).
- **Deployment Pipeline** (`internal/evolution/deployment/deployment.go`, +237 lines): Canary deployment strategy with automatic rollback on regression. Pipeline: `Coordinator.Apply(patch)` → `StagingRuntime.Apply(patch)` → `StagingRuntime.Evaluate()` → if pass: `LiveRuntime.Apply(patch)`; if fail: `StagingRuntime.Rollback()`. Default `Enabled=false`. Includes `deployment_test.go` (+161 lines).
- **Diff Patch Generation Test** (`internal/ares_evolution/generate_diff_patches_test.go`, +235 lines): New test verifying the end-to-end diff patch generation pipeline.

### YAML-Driven Distillation & Config Options

- **Distillation Threshold** (`api/memory/distillation/distillation.go`, `internal/ares_memory/distillation/distiller.go`, `distiller_admin.go`): New YAML-driven `distillation_threshold` config. Semantics: `0` = ungated (fire every event), `N` = fire every N conversation rounds. Mirrors the v0.2.4 `examples/knowledge-base/config.yaml` convention. The `classifier.go` lost 16 lines (consolidated into distiller).
- **New Config Options** (`internal/ares_config/config.go` +10 lines, `sdk/config.go` +165 lines, `sdk/options.go` +163 lines): New SDK config options for `max_history`, `max_sessions`, `enable_distillation`, `distillation_threshold`. All default to zero/false, falling back to component defaults. `sdk/config_test.go` (+275 lines) and `internal/ares_memory/distillation/distiller_test.go` (+157 lines) verify the new options.
- **YAML-Driven Flags Example** (`examples/12-yaml-driven-flags/`): New example demonstrating all new YAML-driven config flags. `ares.yaml` (+19 lines) and `main.go` (+83 lines).

### Brand Assets

- **Logo Assets** (`assets/logo/`): Three new SVG logo assets — `ares-lockup.svg` (+23 lines), `ares-logo-board.svg` (+49 lines), `ares-mark.svg` (+19 lines).

### Documentation

- **Five New GA Technical Articles** (Chinese + English, +5087 lines total):
  - `docs/articles/{en,zh}/ga-deep-dive.md` (+649/+647 lines): Deep-dive into GA internals.
  - `docs/articles/{en,zh}/ga-genealogy.md` (+605/+599 lines): GA genealogy and lineage tracking.
  - `docs/articles/{en,zh}/ga-promoter.md` (+451/+451 lines): GA promoter and promotion logic.
  - `docs/articles/{en,zh}/ga-selection-benchmark.md` (+352/+350 lines): GA selection strategy benchmarks.
  - `docs/articles/{en,zh}/ga-tiered-scorer.md` (+407/+405 lines): Tiered scorer architecture.
- **`examples/10-ga-full-evolution/main.go`**: Refactored — 528 lines changed (simplification, -357 net lines after the article rewrite).

### Examples

- **`examples/21-ai-assistant-integration/main.go`** (+100 lines): New AI assistant integration example demonstrating the public `api/agent` API. Originally +91 lines in `ed62bae`, then +18 lines in `2225940` for self-healing wiring, then +3 lines in `4fa46d7` for cancel-on-error.
- **`examples/22-evolution-blocks/main.go`** (+148 lines): New evolution blocks example demonstrating the public `api/evolution` API.
- **`examples/README.md`** (+2 lines): Updated to list the two new examples.
- **Memory Config Comments** (`examples/01-10/ares.yaml`, `cmd/monitor-live/config.yaml`): All 11 example `ares.yaml` files and the monitor-live config now include commented-out memory subsystem tuning fields (`max_history`, `max_sessions`, `enable_distillation`, `distillation_threshold`) pointing to `examples/12-yaml-driven-flags` for semantics. (+61 lines across 11 files.)

### Refactor

- **Embedding & Experience API Extraction** (`1d14107`): Extracted `api/embedding/service.go` (+77 lines) and `api/experience/{types.go,repository.go}` (+252 lines) to public packages. `internal/storage/postgres/embedding/service.go` simplified (-76 lines net). `internal/ares_memory/distillation/memory.go` refactored (-155 lines, +155 lines — moved logic to public API layer). `internal/ares_memory/embedding/pipeline.go` updated to use new public embedding API.
- **Knowledge Service Adapter** (`internal/knowledge/service/adapter.go` +126 lines, `adapter_test.go` +90 lines): New adapter bridging the public `api/knowledge` API to the internal Knowledge Fabric runtime.
- **Memory Patcher & Production Manager** (`internal/ares_memory/memory_patcher.go` +73 lines, `production_manager.go` +22 lines, `manager_impl.go` +22 lines): Memory patcher and production manager enhanced to support the new deployment pipeline.

### Documentation Completion

Closed the gap between code modules and article coverage. Seven new articles (Chinese + English) cover the previously undocumented modules:

- **SDK Layer** (`docs/articles/{en,zh}/00-sdk-layer.md`): The `sdk/` package — `MustNew`/`New`, functional options, Agent/Team/Stream, config-driven setup. The user-facing main entry point, now documented.
- **Knowledge Graph Build** (`docs/articles/{en,zh}/00-knowledge-graph-build.md`): The AKF Knowledge Fabric construction side — `Plan → Load → Link → Reduce → Graph` pipeline, four Linkers (Decision, Architecture, Similarity, Timeline), three Stores, lazy subgraphs. Article X only covered retrieval; this covers construction.
- **Storage Layer** (`docs/articles/{en,zh}/00-storage-layer.md`): `internal/storage/postgres/` — Pool, CircuitBreaker, WriteBuffer, Timeout. 14,112 lines of foundational infrastructure, now documented as a coherent layer.
- **LLM Client Layer** (`docs/articles/{en,zh}/00-llm-client-layer.md`): `internal/llm/` and `internal/llmservice/` — FailoverClient with rate-limit-aware cooldown, DeepSeek ReasoningContent support, multi-provider output adapters.
- **Evaluation Framework** (`docs/articles/{en,zh}/00-evaluation-framework.md`): `internal/ares_eval/` — LLMJudgeEvaluator (1-10/1-5/pass-fail scales), DimensionJudgeEvaluator, Runner/Comparison/ConcurrentRunner. The fitness function for the GA engine.
- **Config System** (`docs/articles/{en,zh}/00-config-system.md`): `internal/ares_config/config.go` and `sdk/config.go` — one YAML driving twelve modules, typed validation, path traversal protection, zero-value philosophy, v0.2.8 distillation threshold.
- **Quant Trading Module** (`docs/articles/{en,zh}/00-quant-trading.md`): `internal/ares_quant/` — the honest assessment of the 9,768-line experiment. Market data sources, market making engine, portfolio metrics, research agents. Labeled as experiment; extraction to separate repo deferred.

### Documentation Fixes

- **XIII Numbering Conflict Resolved**: `flight-recorder-deep-dive` was renumbered from (XIII) to (XVI) to resolve the conflict with `bootstrap-api-deep-dive`. Both English and Chinese versions updated.
- **Architecture Overview Series List Updated** (`docs/articles/{en,zh}/architecture-overview-deep-dive.md`): Series list extended from XII to include XIII (Bootstrap), XIV (Plugin), XV (MCP), XVI (Flight Recorder), plus the seven new `00-*` articles.
- **README Article Index Updated** (`README.md`, `README_CN.md`): Added the seven new articles to the Articles section.

### Stats

- **8 commits** since v0.2.7 (code), plus 7 new documentation articles.
- **62 code files changed**, +8710 insertions, -849 deletions.
- **14 documentation files** added/updated (7 new articles × 2 languages, plus series list and README updates).
- **0 build warnings, 0 vet warnings, 0 test failures.**



## [0.2.7] - 2026-07-13

> This is a **major milestone release** — 270 commits, 99 features, 27 fixes, 74 refactors since v0.2.5.
> Four big themes: **all pipelines connected, all modules closed-loop, GA evolved again, dynamic workflow.**

### Theme 1: All Pipelines Connected

- **Phase 3-6 WiredEvolutionSystem Integration**: `genome_wiring_system.go` unifies all evolution phases into a single `WiredEvolutionSystem` with `Reflector`, `HypothesisGen`, `MetaCtrl` (Phase 3-5), `DiffReg`, `Coordinator`, `GenomeReg` (Phase 6). `RunIdleEvolution()` Phase 6 generates diff patches as `PatchProposal` with `SourceGA` and `Priority 6`. Full reflection loop and diff engine integration.
- **Service Bridge**: `service_bridge.go` provides bidirectional conversion between API and internal strategy representations: `toAPIStrategy()`, `toInternalStrategy()`, `cloneParams()`, `cloneDimensionScores()`. Enables the evolution system to integrate with the HTTP API layer without exposing internal types.
- **Memory Pipeline Complete**: End-to-end memory pipeline with `ReportGenerator`, `PushService`, and report formatting for human-readable evolution summaries. Full cycle: evaluation → distillation → report → push.
- **Internal Evolution Module** (`internal/evolution/`): New standalone evolution runtime with `coordinator`, `diff`, `genome`, `patch` sub-packages. 4 Differs (Workflow, Scheduler, Knowledge, Recovery), 5 Executors (Graph, Recovery, Knowledge, Memory + StrategyStore), 6 Genomes (Workflow, Scheduler, Knowledge, Recovery, Planner, Memory).
- **Internal Evidence Module** (`internal/evidence/`): Evidence data primitives + MemoryStore. Feeds evolution decisions with structured execution evidence.
- **Internal Knowledge Module** (`internal/knowledge/`): Full AKF Knowledge Fabric with linker, compiler, pipeline, retriever, runtime, provider (code, evolution, memory, mysql, vector), store (memory, postgres, sqlite), MCP integration, and workflow orchestration.

### Theme 2: All Modules Closed-Loop

- **Memory Evolution Genome**: `MemoryGenomeConfig` with configurable parameters: `MaxHistory` [3–50], `MaxSessions` [20–500], `MaxDistilledTasks` [500–20000], `UseStructuredCleaning`. Implements `Mutate()`, `Crossover()`, `Fitness()` with heuristic fitness based on evidence quality. Works alongside the strategy genome in the evolution pipeline.
- **Planner Evolution Genome**: `PlannerGenomeConfig` with strategy selection: `balanced`, `architecture-first`, `memory-first`. Configurable `MaxSources` [3–30] and `MinRelevance` [0.1–0.9]. Heuristic fitness assessment based on evidence coverage and consistency. Evolves planning behavior alongside strategy parameters.
- **Memory Patcher**: `RuntimeComponent` implementation with `Snapshot()`, `Apply()`, `CanApply()` lifecycle. Supports `PatchChangePlanner`, `PatchChangeBudget`, `PatchChangeReducer` for controlled memory system changes. Enables the evolution system to propose and apply memory configuration patches.
- **Agent Age Eviction**: `AgentMaxAge` config limits strategy lifespan; `GenerationCreated` tracking ensures agents survive exactly `AgentMaxAge` generations. Legacy strategies (GenerationCreated==0) exempted.
- **Confidence Calculation**: Added sample-based confidence to `AggregateEvidenceCrossTask`, enabling evidence quality scoring in cross-task aggregation.
- **Truncate Utility Consolidation**: Unified `internal/ares_memory/internal/truncate` package for reusable truncation logic across memory and LLM modules.

### Theme 3: GA Evolution v2

- **NSGA-II Multi-Objective Selection**: Pareto-based multi-objective optimization for strategy evolution. `NondominatedSortingSelection` with non-dominated sorting, crowding distance computation, and Pareto front ranking. Four default optimization dimensions: `success_rate` (maximize, 0.40 weight), `quality` (maximize, 0.25), `cost` (minimize, 0.20), `latency` (minimize, 0.15). Direction-aware Pareto dominance ensures proper handling of minimize vs maximize objectives. Configurable via `WithSelectionStrategy("nsga2")` or `WithSelectionStrategy("nondominated")`.
- **Split Canonical/Selection Score**: `Score` field represents canonical fitness (never modified by GA internals), `SelectionScore` field is adjusted by fitness sharing per epoch. `effectiveScore()` falls back to `Score` when `SelectionScore` is zero, enabling backward compatibility with existing scoring pipelines.
- **Fitness Sharing with 3 Strategies**: Diversity-preserving fitness sharing with three automatic scaling strategies: full O(n²) pairwise for small populations (< 100), reservoir sampling for medium populations, spatial grid index for large populations (> 500). `shareSigma = 0.3`, `FitnessNicheRadius = 0.15`. Elites are exempt from sharing penalty. Configurable via `WithFitnessSharing(true)`.
- **Steady-State GA**: `EvolveSteadyState()` method replaces only `max(1, int(float64(p.Size) * replaceRate))` worst individuals per generation (default 30%). Enables online learning — population persists across generations, only bottom performers are replaced by new candidates. Ideal for production deployments where the system learns continuously without full generation resets. Configurable via `WithSteadyState(true)` and `WithReplaceRate(rate)`.
- **Experience-Guided Mutation System**: Three-tier evolution experience pipeline: `ToolCallRecord → RawExperience → NormalizedExperience → EvolutionHint`. `GuidanceProvider` interface provides directional hints for mutation. `ToolCallExperienceCollector` captures tool call outcomes. `MemoryExperienceStore` with dictionary-based indexing stores and retrieves evolution hints. `AggregateEvidence` computes success rate, p50/p95 latency, and confidence scores for cross-task evidence aggregation.

### Theme 4: Dynamic Workflow Engine

- **MutableDAG**: Thread-safe mutation (add/remove nodes and edges at runtime). Incremental cycle detection on edge insertion.
- **DynamicExecutor**: `ApplyMode` for hot-reload without stopping execution.
- **GraphPatchExecutor**: Insert, remove, or replace nodes at runtime — DAG topology evolution.
- **ExecuteFromCheckpoint**: Lightweight workflow resume from checkpoint via `Graph.ExecuteFromCheckpoint()`. Checkpoint integration via PluginBus hooks.
- **LoopPlugin**: Controlled execution loops with configurable iteration limits.
- **RouterPlugin Auto-Wiring**: Automatic plugin registration based on declared capabilities.

### Documentation

- **Architecture Diagram Overhaul**: Updated README architecture diagram to 6-layer model (added Evolution Engine layer), with GA engine details (7 selectors, 3 crossover, 6 mutation, 6 genomes), runtime evolution pipeline, and data flow sequence diagram.
- **GA Deep-Dive Articles**: Updated `docs/articles/en/autonomous-evolution-deep-dive.md` and `docs/articles/zh/autonomous-evolution-deep-dive.md` with 6 new subsections (9.11-9.16) covering NSGA-II, steady-state GA, split score, experience system, memory evolution, and Phase 3-6 integration.
- **GA-in-the-Trenches**: Updated `docs/articles/en/ga-in-the-trenches.md` and `docs/articles/zh/ga-in-the-trenches.md` with steady-state GA, NSGA-II, split score lessons, and new Lesson 6 on experience systems.
- **Overview Update**: Updated `docs/articles/zh/autonomous-evolution-overview.md` with service bridge, memory evolution, and experience hints coverage.
- **Feature Doc Update**: Updated `docs/en/features/autonomous-evolution.md` and `docs/zh/features/autonomous-evolution.md` with all new GA features.
- **Analysis Plan Sync**: Updated `GA_ANALYSIS.md` and `GA_DEVELOPMENT_PLAN.md` to reflect completed implementation status.

### Integrated Examples & Infrastructure Fixes

- **Knowledge Base Example** (`examples/11-knowledge-import/`): Complete structure-aware markdown knowledge base with CLI import/query, multi-agent team import, and dialog-based chat. Integrates parser (6 BlockTypes), section-first chunker, PostgreSQL + pgvector embedding, batch transactions, and retry with exponential backoff.
- **AKG Knowledge Graph Builder** (`examples/11-knowledge-import/akg/`): Builds working knowledge graphs from the knowledge base via `KnowledgeRuntime.Execute()`. 147 nodes, 27K edges, 73ms build. Uses the existing PGProvider (tag column bug fixed), planner, linkers (DecisionLinker, ArchitectureLinker, TimelineLinker, SimilarityLinker), and reducer — zero custom infrastructure.
- **LLM Failover**: `FailoverClient` wired through SDK's `WithFallbackLLM()` option. Automatic 30s timeout → cooldown → fallback. Verified with ollama chain.
- **GA Evolution Integration**: `--evolve` CLI command calls `Runtime.Evolve()` with population (10 agents × 3 generations). `executeAndScore` bug fixed (nil pointer on `runtime` field). Best strategy scored 99.5/100.
- **Event Store Tool Chain Recording**: `Agent.Run()` now emits `EventToolCallStarted`/`EventToolCallCompleted` events to `ares_events.EventStore` for every tool call, capturing tool name, arguments, result, and success status.
- **Chaos Engineering + Resurrection**: `ToolWrapper` with fault injection (failure rate, latency, kill-after-N-calls) and `AgentSupervisor` for health monitoring. `--chaos-fail/--chaos-latency/--chaos-kill` flags.
- **SDK AKG Context Injection**: `buildMessages()` queries `KnowledgeRuntime` before each agent run and injects compiled knowledge graph context into the system prompt. Enabled via `WithEvolution()` + `WithKnowledge()`.
- **DeepSeek ReasoningContent Support**: Added `ReasoningContent` field to `Message` and `AssistantMsg` structs, wired through `toMap()` for proper round-trip serialization of DeepSeek thinking mode responses.
- **PGProvider Bug Fix**: `scanRow()` scanned the tag column via SQL but never assigned it to `obj.Tags`. Fixed — tag column data now properly populates `KnowledgeObject.Tags`.
- **14 Lint Fixes**: errcheck, noctx, gosec G114, goconst, staticcheck SA9003/QF1012, unused dead code — all resolved across 8 files. Zero warnings on `go build` + `go vet`.



## [0.2.6] - 2026-07-07

### New Features

- **Unified SDK Package** (`sdk/`): New top-level API `sdk.MustNew()` / `sdk.New()` with functional options (`WithOpenAI`, `WithOllama`, `WithAnthropic`, `WithDefaultMemory`, `WithEvolution`, `WithMCP`, `WithHumanInput`, etc.). Single entry point for LLM, tools, memory, evolution, and MCP.
- **Agent Runtime**: `agent.Run(ctx, input)` ReAct loop with tool calling, memory context injection, token tracking, and result metadata.
- **Streaming Support**: `agent.Stream(ctx, input)` returns `<-chan StreamChunk` for async response streaming.
- **Multi-Agent Teams**: `rt.NewTeam(name, leader, members)` with `team.Run()` for leader/member orchestration.
- **Human-in-the-Loop**: `WithHumanInput()` callback for tool call approval before execution.
- **MCP Integration**: `WithMCP()` connects to MCP servers via stdio, auto-registers their tools.
- **Strategy Evolution**: `rt.Evolve(ctx, agent, task)` evolves agent instructions via LLM. `WithEvolution()` enables the evolution system.
- **CLI Tools** (`cmd/ares/`): `ares init` (scaffold project), `ares run` (run agent from config, auto-detects `ares.yaml`), `ares bench` (benchmark with JSON/Markdown output), `ares doctor` (diagnose environment), `ares version`.
- **Config-Driven Setup**: `sdk.LoadConfigFile(path)` reads YAML config, `cfg.ToOptions()` converts to SDK options. `ares run` auto-discovers `ares.yaml` or `config/ares.yaml`.
- **Evaluation Framework** (`evaluation/`): `evaluation.New()`, `Register()`, `RunScenario()`, `RunAll()` with structured `Metrics`, `Report`, `Aggregate`. Report output via `ToMarkdown()` / `ToJSON()`. Built-in scenarios: basic-chat, tool-calling, multi-agent, resilience, evolution.

### Examples

- **9 New SDK Examples**: Numbered `01-quickstart` through `09-full-app`, each with `ares.yaml` config.
  - `01-quickstart`: Minimal agent in 20 lines
  - `02-tool-calling`: Multi-tool registration
  - `03-dag-workflow`: MutableDAG + conditional branching
  - `04-multi-agent`: Leader/member team orchestration
  - `05-evolution-demo`: Instruction evolution before/after comparison
  - `06-chaos-resilience`: 9 failure modes (file, timeout, network, MCP, LLM, memory, graceful degradation)
  - `07-human-in-loop`: Tool call approval with `WithHumanInput`
  - `08-mcp-integration`: MCP server connection via `WithMCP`
  - `09-full-app`: Web UI + Agent + Tools + Memory + Stats dashboard
- **Evaluation Example** (`examples/eval/`): Runs all 5 capability scenarios with scoring.

### Documentation

- **README Rewrite**: Reduced from 774 to 214 lines. SDK Quick Start at the top. English (`README.md`) and Chinese (`README_CN.md`) versions.
- **GitHub Pages Website**: `docs/index.html` with dark theme, marked.js inline Markdown rendering, all articles browsable.
- **Architecture Diagram**: Mermaid diagram covering SDK, LLM providers, Tools, Memory, Evolution, CLI, Examples.
- **7 Cookbook Recipes**: `docs/cookbook/` with Chat, Tool Calling, Multi-Agent, Memory, Coding Agent, Code Review, GitHub Agent.
- **CI Docs Deployment**: GitHub Actions workflow (`docs.yml`) auto-deploys `docs/` to Pages.

### Code Quality

- **SDK Test Coverage**: 54%+ with 20+ tests covering Runtime, Agent, Team, Config, Evolution, Streaming, MCP, HumanInput, Benchmarks. All pass with `go test -short ./...`.
- **Lint Clean**: `golangci-lint` 0 issues across SDK, CLI, evaluation, and examples.
- **English Comments**: All code comments in English per `code_rules.md`.
- **Binary Rename**: CLI binary `ARES` → `ares` (lowercase).

### Infrastructure

- **Docker Compose**: `docker-compose.yml` + `Dockerfile.demo` for one-command demo deployment (Ollama + full-app).
- **Makefile**: Added `quickstart`, `examples`, `install-cli`, `test-eval` targets.
- **Example Cleanup**: Removed 20+ stale/duplicate examples; kept 9 curated SDK examples + advanced ones in git history.
- **Chaos Arena YAML**: Restored `examples/arena/leader_assassination.yaml` and `cascading_storm.yaml` with all built-in action types.

### Performance

- **GA Diversity Sampling**: Added `DiversitySampleSize` config (default 200) to estimate numeric diversity via random neighbor sampling instead of O(n²) exact computation. Stats(pop=1000) latency dropped **38%** (69.5ms → 43.3ms). Configurable per `PopulationConfig.DiversitySampleSize`.
- **Fitness Sharing Optimization**: Replaced per-agent Fisher-Yates full permutation with Reservoir Sampling in `applyFitnessSharingSampled`. Allocation reduced **44%** for all population sizes (pop=100: 185→106 allocs, pop=500: 905→506 allocs). GC pressure halved in large evolution runs.
- **Subscribe Allocation Reduction**: Replaced UUID subscription IDs with `atomic.Int64` counter and removed `*sync.Once` per subscriber. Allocs reduced **33%** (900→600 per 100 subscribers). Channel buffer increased from 1→64 to reduce burst drops.
- **Benchmark Report**: Comprehensive benchmark report across all modules (events, GA genome/evaluation, memory distillation, tools core, handlers, errors) with full platform config (Apple M3 Max, Go 1.26, 3-run average).

### New Features

- **Memory Pipeline Complete**: End-to-end memory pipeline with `ReportGenerator`, `PushService`, and report formatting for human-readable evolution summaries. Full cycle: evaluation → distillation → report → push.
- **Agent Age Eviction**: `AgentMaxAge` config limits strategy lifespan; `GenerationCreated` tracking ensures agents survive exactly `AgentMaxAge` generations. Legacy strategies (GenerationCreated==0) exempted.
- **Confidence Calculation**: Added sample-based confidence to `AggregateEvidenceCrossTask`, enabling evidence quality scoring in cross-task aggregation.

### Refactors

- **Truncate Utility Consolidation**: Unified `internal/ares_memory/internal/truncate` package for reusable truncation logic across memory and LLM modules.
- **Evidence Logic Cleanup**: `AggregateEvidence` refactored for clarity; cross-task evidence aggregation now filters mixed-task noise with `AggregateEvidenceCrossTask`.
- **FIXME Cleanup (22 files)**: Removed stale FIXME comments in `internal/ares_quant/`, `internal/api_impl/`, `api/client/`, `internal/ares_events/`, `internal/storage/postgres/services/`. All had already been implemented but comments were not updated.
- **Promotion Logic**: Tightened statistical bands (5-20x → 6-18x) in `selection_extra_test.go` and reduced low-scorer threshold (5% → 0.2%) for more deterministic selection verification.

### Bug Fixes

- **Ignored json.Marshal Errors**: Fixed 4 ignored `json.Marshal` calls in `internal/ares_events/summary_repository.go` — previously would silently produce `null` DB values on serialization failure. Now errors propagate with `fmt.Errorf("marshal %s: %w", ...)`.
- **Errgroup Context Propagation**: In `internal/api_impl/service.go`, `errgroup.WithContext(ctx)` returned a derived context cancelled on sibling errors — but it was discarded with `_`. Fix: `s.g, s.ctx = errgroup.WithContext(ctx)` to enable proper error propagation.
- **SSE Health Probe**: Implemented real SSE health check via `ConnectSSE` instead of hardcoded assumed healthy.
- **Generation Logging**: Fixed generation=0 in logs by using absolute `Population.Generation` in callback\_gen.
- **GenerationCreated Off-by-One**: Use Generation+1 so agents survive exactly `AgentMaxAge` generations.
- **Guardrail Config Default**: Inverted `PromptDiversityGuardEnabled` → `DisablePromptDiversityGuard` (default enabled).

### Code Quality

- **Unit Test Coverage (service.go)**: Added `internal/ares_evolution/service/service_test.go` (383 lines, 23 test cases). Coverage of `service.go` increased **16.3% → 47.2%**. Key functions: `NewService` 93.3%, `Evolve` 80.4%, `toAPIStrategy`/`toInternalStrategy`/`cloneDimensionScores` all 100%.
- **LLM Scorer Tests**: Added pure-logic tests for `extractScoreFromText`, `fallbackScore`, `buildPrompt`, `parseScore` (35 table-driven test cases). No LLM required.
- **Task Planner Tests**: Consolidated 10 repetitive test functions into 2 table-driven tests with meaningful `result.Error` content assertions. `TestFormatToolsList` converted to table-driven.
- **Test Weakness Assessment**: Sampled 20+ non-testify test files — confirmed they have meaningful multi-field assertions (not perfunctory). Postgres integration tests properly isolated behind `//go:build integration`.
- **Docker Compose Update**: Added Ollama service for local LLM fallback in development stack. Updated benchmark links in both EN and CN README.


## [0.2.5] - 2026-07-02

### Performance

- **GA Diversity Sampling**: Added `DiversitySampleSize` config (default 200) to estimate numeric diversity via random neighbor sampling instead of O(n²) exact computation. Stats(pop=1000) latency dropped **38%** (69.5ms → 43.3ms). Configurable per `PopulationConfig.DiversitySampleSize`.
- **Fitness Sharing Optimization**: Replaced per-agent Fisher-Yates full permutation with Reservoir Sampling in `applyFitnessSharingSampled`. Allocation reduced **44%** for all population sizes (pop=100: 185→106 allocs, pop=500: 905→506 allocs). GC pressure halved in large evolution runs.
- **Subscribe Allocation Reduction**: Replaced UUID subscription IDs with `atomic.Int64` counter and removed `*sync.Once` per subscriber. Allocs reduced **33%** (900→600 per 100 subscribers). Channel buffer increased from 1→64 to reduce burst drops.
- **Benchmark Report**: Comprehensive benchmark report across all modules (events, GA genome/evaluation, memory distillation, tools core, handlers, errors) with full platform config (Apple M3 Max, Go 1.26, 3-run average).

### New Features

- **Memory Pipeline Complete**: End-to-end memory pipeline with `ReportGenerator`, `PushService`, and report formatting for human-readable evolution summaries. Full cycle: evaluation → distillation → report → push.
- **Agent Age Eviction**: `AgentMaxAge` config limits strategy lifespan; `GenerationCreated` tracking ensures agents survive exactly `AgentMaxAge` generations. Legacy strategies (GenerationCreated==0) exempted.
- **Confidence Calculation**: Added sample-based confidence to `AggregateEvidenceCrossTask`, enabling evidence quality scoring in cross-task aggregation.

### Refactors

- **Truncate Utility Consolidation**: Unified `internal/ares_memory/internal/truncate` package for reusable truncation logic across memory and LLM modules.
- **Evidence Logic Cleanup**: `AggregateEvidence` refactored for clarity; cross-task evidence aggregation now filters mixed-task noise with `AggregateEvidenceCrossTask`.
- **FIXME Cleanup (22 files)**: Removed stale FIXME comments in `internal/ares_quant/`, `internal/api_impl/`, `api/client/`, `internal/ares_events/`, `internal/storage/postgres/services/`. All had already been implemented but comments were not updated.
- **Promotion Logic**: Tightened statistical bands (5-20x → 6-18x) in `selection_extra_test.go` and reduced low-scorer threshold (5% → 0.2%) for more deterministic selection verification.

### Bug Fixes

- **Ignored json.Marshal Errors**: Fixed 4 ignored `json.Marshal` calls in `internal/ares_events/summary_repository.go` — previously would silently produce `null` DB values on serialization failure. Now errors propagate with `fmt.Errorf("marshal %s: %w", ...)`.
- **Errgroup Context Propagation**: In `internal/api_impl/service.go`, `errgroup.WithContext(ctx)` returned a derived context cancelled on sibling errors — but it was discarded with `_`. Fix: `s.g, s.ctx = errgroup.WithContext(ctx)` to enable proper error propagation.
- **SSE Health Probe**: Implemented real SSE health check via `ConnectSSE` instead of hardcoded assumed healthy.
- **Generation Logging**: Fixed generation=0 in logs by using absolute `Population.Generation` in callback_gen.
- **GenerationCreated Off-by-One**: Use Generation+1 so agents survive exactly `AgentMaxAge` generations.
- **Guardrail Config Default**: Inverted `PromptDiversityGuardEnabled` → `DisablePromptDiversityGuard` (default enabled).

### Code Quality

- **Unit Test Coverage (service.go)**: Added `internal/ares_evolution/service/service_test.go` (383 lines, 23 test cases). Coverage of `service.go` increased **16.3% → 47.2%**. Key functions: `NewService` 93.3%, `Evolve` 80.4%, `toAPIStrategy`/`toInternalStrategy`/`cloneDimensionScores` all 100%.
- **LLM Scorer Tests**: Added pure-logic tests for `extractScoreFromText`, `fallbackScore`, `buildPrompt`, `parseScore` (35 table-driven test cases). No LLM required.
- **Task Planner Tests**: Consolidated 10 repetitive test functions into 2 table-driven tests with meaningful `result.Error` content assertions. `TestFormatToolsList` converted to table-driven.
- **Test Weakness Assessment**: Sampled 20+ non-testify test files — confirmed they have meaningful multi-field assertions (not perfunctory). Postgres integration tests properly isolated behind `//go:build integration`.
- **Docker Compose Update**: Added Ollama service for local LLM fallback in development stack. Updated benchmark links in both EN and CN README.

## \[0.2.4] - 2026-06-28

### New Features

- **Plugin System Architecture**: Full plugin system with `PluginBus`, `RuntimePlugin` interface, `WorkflowHook` interface, and capability-based plugin discovery. 10 built-in plugins: ObserverPlugin, CheckpointPlugin, ToolPlugin, ExpressionRouter, MemoryRouter, EvolutionRouter, LoopPlugin, RecoveryPlugin, InterruptPlugin, ArenaPlugin.
- **Genetic Algorithm Evolution System (Beta)**: Complete GA package with `Population`, `Crossover` (Inherit/HalfSplit/Uniform), `TournamentSelection`, strategy mutation engine, diversity tracking with fitness sharing, adaptive survival rates, and deterministic reproduction via seed control.
- **Autonomous Evolution (Dream Mode v1)**: Closed-loop evolution orchestration with Dream Cycle (trigger → mutate → evaluate → adopt → record lineage). Arena regression testing with Welch's t-test, bandit feedback loop for experience quality optimization, and full genealogy tracking.
- **Batch LLM Scorer**: Concurrent LLM scoring with failover resilience for evolution pipeline.
- **ExecuteFromCheckpoint**: Lightweight workflow resume from checkpoint via `Graph.ExecuteFromCheckpoint()`. Checkpoint integration via PluginBus hooks.
- **LoopPlugin**: Controlled execution loops with configurable iteration limits.
- **RouterPlugin Auto-Wiring**: Automatic plugin registration based on declared capabilities.
- **Execution Collector**: Thread-safe runtime data aggregation for route recording and tool invocation tracking.
- **Module-Scoped Structured Logging**: Each core module emits logs with `module` field for traceability. Added `logger.Module()` helper in `internal/logger/`. 12 core packages converted.
- **Event ModuleName Field**: Added `ModuleName` field to `Event` struct. `Emit()` and `PluginBus.Emit()` now accept `moduleName` parameter for full traceability of which module emitted each event.
- **Abstract API Layer**: Added interfaces in `api/core/` for all major modules: `AgentService`, `Runtime`, `WorkflowService`, `MemoryService`, `LLMService`, `RetrievalService`, `Evolution`, `DreamCycle`, `Arena`, `ContextCleaner`.
- **Bootstrap Factory**: Added `api/bootstrap/` package that wires all ARES modules (Runtime, Memory, Evolution, Arena, EventStore) into a single `ARES` container with `New()`, `Start()`, `Stop()`, `RunEvolution()`, and `ExecuteArenaAction()`.
- **Interview Demo**: Complete interview demo stack with web search tool and prompt length validation.
- **JSONL Training Data Pipeline**: End-to-end pipeline for agent strategy evolution and experience distillation data export.

### Refactors

- **Unified Package Naming**: Renamed 15 internal packages to `ares_` prefix: `bootstrap`, `callbacks`, `ctxutil`, `shutdown`, `ratelimit`, `security`, `config`, `eval`, `observability`, `integration`, `events`, `mcp`, `protocol`, `quant`, `runtime`.
- **API Layer Thinning**: Moved all independent service implementations from `api/` to `internal/`. The `api/` layer now only contains interface definitions, error types, HTTP handlers, router, and client SDK. Moved packages:
  - `api/service/agent` → `internal/agents/`
  - `api/service/graph` → `internal/workflow/graphservice/`
  - `api/service/llm` → `internal/llmservice/`
  - `api/service/memory` → `internal/memoryservice/`
  - `api/service/retrieval` → `internal/retrievalservice/`
  - `api/ares_evolution` → `internal/ares_evolution/service/`
  - `api/ares_memory` → `internal/ares_memory/service/`
  - `api/ares_retrieval` → `internal/ares_memory/retrieval_api/`
  - `api/ares_experience` → `internal/ares_experience/service/`
  - `api/eval` → `internal/ares_eval/service/`
  - `api/marketmaking` → `internal/ares_quant/marketmaking_api/`
- **Evolution Genome Wiring**: Split genome\_wiring into separate module, fix guardrails, wire dream cycle.
- **HITL Feedback Plugin**: Moved from standalone to workflow engine integration.
- **Graph Builder APIs**: Migrated all graph builder APIs to return errors instead of panicking.
- **Scoring Cache**: Replaced `sync.RWMutex` with atomic counters for hit/miss tracking.
- **Evolution Mutation**: Restructured mutation logic with experience-guided evolution system.
- **Performance**: Increased concurrent LLM scoring limits and optimized sampling. Completed P0/P1/P2 performance improvements.

### Bug Fixes

- Replaced `time.Sleep` with channel-based event test pattern in graph executor tests.
- Fixed indentation in executor\_test.go.
- Fixed data race in `DynamicExecutor` recovery path with proper timeout handling.
- Fixed `MemoryEventStore.Close()` idempotency — second+ calls return `ErrEventStoreClosed`.
- Fixed SSE transport double `resp.Body.Close()` causing panic on shutdown.
- Fixed LLM client `Close()` race condition via `sync.Once`.
- Fixed OpenAI adapter silently swallowing `io.ReadAll` errors in error paths.
- Fixed `Population.ScoreAgents` panic recovery logging with agent context.
- Fixed `updateBestEverLocked` concurrency safety with deep copy via `a.Clone()`.
- Fixed `NewTaskPlanner`/`NewTaskPlannerWithConfig` silent fallback from invalid `maxTasks`.
- Added nil validation in `leader.New`, `NewTaskDispatcher`, and `NewMCPManager`.

## \[0.2.3] - 2026-06-24

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

## \[0.2.2] - 2026-06-19

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
- **Concurrent Distillation Pipeline**: errgroup-based parallel embedding in distiller (concurrency limit 5), and concurrent experience storage in manager\_impl.go, reducing end-to-end distillation latency.
- **Content Hash Dedup**: SHA-256 `content_hash` column on `distilled_memeries` with `ON CONFLICT (tenant_id, content_hash) WHERE content_hash IS NOT NULL DO NOTHING` for idempotent memory storage.
- **Idempotent Migrations**: All DDL operations now safe to re-run — `DROP IF EXISTS` + `CREATE` for policies/triggers, `IF NOT EXISTS` for indexes, `ADD COLUMN IF NOT EXISTS` for schema evolution.
- **Chinese Language Support**: Chinese keyword detection in `detector.go` (介绍/是什么/怎么/有哪些/区别/说说/推荐 etc.) and Chinese importance scoring in `scorer.go` (错误/修复/配置/框架/架构/优化 etc.), enabling experience extraction from Chinese Q\&A.
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

## \[0.2.1] - 2026-06-16

### New Features

- **MCP Client**: Model Context Protocol client implementation with JSON-RPC 2.0 messaging, stdio and SSE transport support, tool schema management, and connection lifecycle management.
- **Web Dashboard**: Real-time monitoring dashboard with WebSocket hub, REST API v2, orchestrator for multi-agent coordination, event bridge for system state streaming, and static asset serving.
- **Flight Recorder**: Multi-agent runtime intelligence recording with timeline tracking, decision logging, diagnostics engine, agent genealogy graph, DOT/JSON export, and replay pipeline.
- **Chaos Engineering Arena**: Fault injection framework with injector supporting process\_kill, network\_partition, latency\_spike, and kill\_orchestrator fault types; resilience scoring with configurable metrics; survival mode for continuous chaos testing; HTTP API and YAML scenario configuration.
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

## \[0.1.0] - 2026-04-19

### Added

- Initial multi-agent collaboration framework
- Memory management with distillation and retrieval
- Tool calling with ACE (Agent Capability Engine)
- Workflow engine with DAG-based orchestration
- PostgreSQL + pgvector integration
- Support for multiple LLM providers (OpenAI, Ollama, OpenRouter)

