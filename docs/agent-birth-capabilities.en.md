# Agent Birth Capabilities (Capability Inventory)

> Date: 2026-08-15
> Scope: capabilities **auto-injected** when `serve` creates the leader / sub agents
> (`cmd/ares/{serve,agents,tools}.go` + `internal/agents/sub` + `internal/ares_memory`) — available
> without any extra configuration. Runtime on-demand capabilities (MCP connection after skill
> activation, lazily fetched SKILL.md bodies) are excluded.
> Chinese version: [agent-birth-capabilities.md](agent-birth-capabilities.md).

---

## 1. Overview (four layers)

| Layer | Responsibility | Representative capabilities |
|-------|----------------|-----------------------------|
| Architecture | Execution skeleton | Task planning / dispatch / queues / output validation |
| Capability | Knowledge & skills | SkillCatalog family (multi-source index / FTS5 / Experience / lazy MCP) |
| Primitive | Agent OS primitives | Peer messaging / action log / lease / snapshot / output guard |
| Control | Runtime control | Budgets / GA strategy source / evolution feedback / sandboxed tools |

---

## 2. Execution & Validation (TaskExecutor core)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Output schema validation | `output.NewValidator(WithSchemaType)` (leader + sub) | Results must match the configured schema; structural inconsistencies are rejected at the boundary |
| Output guard | `outputguard.NewGuard().ValidateResult` (sub event path `processScheduledEvent`) | Rejects structurally inconsistent agent results |
| Task planning | `leader.NewTaskPlannerWithConfig` + `plannerOpts` | Plans task dispatch from the sub-agent configuration |
| Dispatcher | `leader.WithDispatcherAgentID` / `WithDispatcherEventStore` | Event-driven single execution path (§5.1) |
| Message queue | `ahp.NewMessageQueue` (leader/sub, MaxSize 500) | Async message buffering |
| Validated executor | `sub.NewTaskExecutorWithValidation` | Every sub-agent task runs through validation |

---

## 3. Knowledge / Skills (Capability Fabric)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Resident skill block | `wireSkillCatalog` → `SetSkillsRegistry` → `BuildContext` | Ships with Level-0 metadata (name + one-liner) at birth; SKILL.md fetched on demand (progressive disclosure) |
| Multi-source skill index | config.toml `[[skill_sources]]`: project / user / registered / **git / http / oci** | Only declared sources are scanned — zero full-disk scanning |
| FTS5 full-text search | `FTS5Index` (Discovery prefers FTS5, falls back to keyword matching) | Ranked retrieval |
| Experience locator | `leader.WithExperienceLocator(skillLocator)` | Task-to-skill matching + Experience relevance priors |
| Experience persistence | `~/.ares/experience.json` (atomic tmp→rename write) | Learned task→skill priors survive restarts |
| Lazy MCP connection | `SetMCPConnector(comp.MCP)` | Declared MCP servers connect only when a skill is activated (`Catalog.Activate`) |
| listChanged incremental re-index | `MCPManager.SetToolChangeHandler` → `Catalog.Refresh` | MCP tools/listChanged triggers hash-based incremental re-indexing |
| Change detection | `DetectIndexChanges` + `Catalog.Refresh` | Classifies Added / Modified / Removed by ID+Source+Hash |

---

## 4. Tooling

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Builtin tools | `api_tools.RegisterBuiltinTools(WithFileSandboxDir)` | Builtin tools (e.g. filesystem) confined to a sandbox directory |
| Native commands | `registerNativeTools` (`ARES_NATIVE_TOOLS` allowlist) | Only allowlisted commands are probed (`command -v` + `--help`) and exposed |
| MCP tools | `setupMCP` → `internalReg` | tools/list of connected servers registered into the tool registry |
| Unified environment search | envcap bridge (`SeedRegistry` → `envcap.Searcher`) | Unified search over tools / skills / commands (kindRank ordering) |

---

## 5. Communication / Collaboration (Agent OS primitives)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Peer messaging | `buildPeerRegistry` → `SetPeerRegistry` / `NotifyPeer` (leader) | Direct agent-to-agent messages without routing through the leader (supplementary notification channel) |
| Message queue | `ahp.NewMessageQueue` | See Architecture layer |

---

## 6. Persistence / Audit / State

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Event store | `WithEventStore(comp.EventStore)` (leader + sub) | Full event sourcing (`internal/ares_events`) |
| Action log | `WithActionLog` (three result exits of sub tasks) | Task audit + replay (idempotent Append, Replay from startID) |
| Feedback recording | `WithFeedbackService` + `FeedbackRecorder.WithRefiner` | Result feedback → refine small-step evolution (`strategy:<id>` key) |
| State snapshot | runner_checkpoint persist / load (`state-snapshot/<execID>`) | Runtime state recovery across restarts (schema-version guarded) |
| Session lease | memoryManager session lease | Concurrent session access control (TTL lease) |
| Context cleaning | memoryManager `ContextCleaner` | Turn-grouped, tool-semantic-summary differential compaction |

---

## 7. Resource Control / Evolution Wiring

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Run budgets | `sdk.WithMaxTokens` / `sdk.WithTimeout` (agentloop.Request passthrough) | Token cap + wall-clock timeout |
| GA strategy source | `WithStrategySource(comp.NewEvolution)` | Live agents read the deployed prompt / params at runtime |
| Regression validation | arena regression (fingerprint cache) | Skips re-running when the environment is unchanged |
| LLM fallback sampling | sampling params via requestOverrides passthrough | Temperature / TopP / Penalty |

---

## 8. Wiring index (source locations)

- Assembly entry: `cmd/ares/serve.go` (`createAndRegisterServeAgents`, `wireSkillCatalog`, `buildPeerRegistry`, `registerNativeTools`, `setupMCP`)
- Agent construction: `cmd/ares/agents.go` (`createLeaderAgent`, `createAgents`, `createSubAgents`)
- Primitive implementations: `internal/agents/{peer,actionlog,lease,outputguard}/`, `internal/ares_runtime/state_snapshot.go`, `internal/ares_evolution/refine/`
- Capability implementations: `internal/ares_skills/` (Catalog / SourceManager / Indexer / Discovery / Loader / Resolver / Experience / FTS5 / git-http sources / changes)
- Memory wiring: `internal/ares_memory/manager_impl.go` (resident skills block + lease + ContextCleaner)
- Design document: `docs/analysis-reports/ares-capability-fabric-design.md`

---

## 9. Notes

- This inventory covers capabilities injected **at serve startup**; post-`Catalog.Activate` MCP connections and on-demand SKILL.md / references loading are runtime on-demand capabilities and are excluded.
- The Capability Fabric shipped in four batches (core → config/MCP/Experience/envcap/hash → git/http/oci/FTS5/listChanged → code-review concurrency & consistency fixes); see the design document status section for details.
