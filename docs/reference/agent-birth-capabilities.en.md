# Agent Birth Capabilities (Capability Inventory)

> Updated: 2026-09-13 (v0.3.1)
> Scope: capabilities **auto-injected** when `ares serve` assembles the peer agents
> (`cmd/ares/peer_assembly.go` + `cmd/ares/serve_wiring.go` + `internal/agentruntime` + `internal/runtime/memory`) — available
> without any extra configuration. Runtime on-demand capabilities (MCP connection after skill
> activation, lazily fetched SKILL.md bodies) are excluded.
> Chinese version: [agent-birth-capabilities.md](agent-birth-capabilities.md).

---

## 1. Overview (four layers)

| Layer | Responsibility | Representative capabilities |
|-------|----------------|-----------------------------|
| Architecture | Execution skeleton | Shared L2 execution core / kernel scheduler / task-fabric state machine / output guard |
| Capability | Knowledge & skills | SkillCatalog family (multi-source index / FTS5 / Experience / lazy MCP) |
| Primitive | Agent OS primitives | Peer registry (discovery contract) / session lease / context cleaning |
| Control | Runtime control | Budgets / GA strategy source / evolution feedback / sandboxed tools |

> v0.3.x change: the Leader/Sub execution model was deleted. Birth capabilities that went with it — the leader task planner, AHP message queues, and the ActionLog audit path — are gone (`internal/agents/sub/agent.go` documents these removals: the peer direct channel was always a nil queue in production, and ActionLog had zero production constructors).

---

## 2. Execution & Validation (shared L2 core)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Shared execution core | `agentruntime.NewExecution` (assembled in `peer_assembly.go`) | Shared by serve and SDK: session registry + plan-projection compile + router cognition + submitter (v0.3.1 convergence) |
| Task state machine | `internal/fabric/task` | Create/Acquire/Yield/Complete/Checkpoint + DAG dependencies + epoch fencing + leases |
| Kernel scheduler | `internal/kernel` | Capability scoring, drain loop, lease heartbeat, priority preemption, recovery nomination |
| Output guard | `outputguard.NewGuard().ValidateResult` (sub event path) | Rejects structurally inconsistent agent results |
| Prompt memory enrichment | `resolveServePromptEnricher` → `Submitter` (v0.3.1) | Cross-turn session memory injected on the serve submission path (fail-open) |
| Cross-restart recovery | `fabric.RestoreFromStore` (PG mode, v0.3.1) | Tasks fold back with checkpoints after a process crash |

---

## 3. Knowledge / Skills (Capability Fabric)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Resident skill block | `skills_wiring` → `SetSkillsRegistry` → `BuildContext` | Ships with Level-0 metadata (name + one-liner) at birth; SKILL.md fetched on demand (progressive disclosure) |
| Multi-source skill index | config `[[skill_sources]]`: project / user / registered / git / http / oci | Only declared sources are scanned — zero full-disk scanning |
| FTS5 full-text search | `FTS5Index` (Discovery prefers FTS5, falls back to keyword matching) | Ranked retrieval |
| Skill catalog tools | `ares_skills.CatalogTools` (serve_wiring) | skill_search / skill_load etc. registered as first-class tools; the LLM drives progressive disclosure itself |
| Experience persistence | Experience store (skill outcome writer on the terminal-event stream, closed in v0.3.1) | Task terminal states → {capability → success rate} priors written back |
| Lazy MCP connection | `SetMCPConnector(comp.MCP)` | Declared MCP servers connect only when a skill is activated (`Catalog.Activate`) |
| listChanged incremental re-index | `MCPManager.SetToolChangeHandler` → `Catalog.Refresh` | MCP tools/listChanged triggers hash-based incremental re-indexing |
| Change detection | `DetectIndexChanges` + `Catalog.Refresh` | Classifies Added / Modified / Removed by ID+Source+Hash |

---

## 4. Tooling

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Builtin tools | `api_tools.RegisterBuiltinTools(WithFileSandboxDir)` | web_search / calculator / regex / json / file etc. confined to a sandbox directory |
| Native commands | `registerNativeTools` (`ARES_NATIVE_TOOLS` allowlist) | Only allowlisted commands are probed (`command -v` + `--help`) and exposed |
| MCP tools | `setupMCP` → `internalReg` | tools/list of connected servers registered into the tool registry |
| Capability search tool | `registerCapabilitySearch` | Unified search over tools / skills / commands (envcap) |
| Planner bridge | `newPlannerBridge(internalReg)` | Agent tool fallback resolution |

---

## 5. Communication / Collaboration (Agent OS primitives)

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Evolution IPC bridge | `wireEvolutionIPC` (serve_peer) | Peer message bus + dead-letter observability (30s periodic count/reason snapshot; observe-only, no auto-redelivery) |
| Collaboration feedback channel | `bridge.ipc.Bus().WithCollaborationObserver` (when armed) | ask_agent / collaboration receipts feed the evolution feedback source |
| Peer registry | `buildPeerRegistry` (discovery contract) | No production agent currently exposes the SendMessage surface — the registry is always empty; non-evolution ask_agent sends fail loud with "not registered" (contract kept, see TODO) |

---

## 6. Persistence / Audit / State

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Event store | `WithEventStore` (sub) + `fabric.WithEventStore` | Full event sourcing (`internal/ares_events`, memory/PG) |
| Feedback recording | `FeedbackRecorder` (ares_evolution) | Strategy outcomes → experience feedback system (success/failure counts) |
| Session lease | `memoryManager.AcquireSessionLease` | Concurrent session access control (TTL lease, owner-checked) |
| Context cleaning | `ContextCleaner` (memory/context) | Turn-grouped, tool-semantic-summary differential compaction |
| Session storage bound | `SessionMemory.WithMaxMessages` (default 500) | Long-lived session message slices stay bounded |

---

## 7. Resource Control / Evolution Wiring

| Capability | Wiring point | Description |
|------------|--------------|-------------|
| Run budgets | `sdk.WithMaxTokens` / `sdk.WithTimeout` | Token cap + wall-clock timeout |
| Governance budget | `agentfabric.Governance` (TokenBudget/ToolBudget/Deadline, peer_assembly) | Per-peer cognitive execution budget, bounded from birth |
| GA strategy source | `strategySrc.GetActiveStrategy` (fabric strategy stamp) | Tasks are stamped with the active strategy ID at submission; fitness attribution stays consistent across promotes |
| Strategy stamp | `fabric.WithStrategyStamp` | One 2s-bounded store read per Create |
| Evolution feedback loop | `RunScoredFeedbackLoop` (10s period) | Execution attribution → confidence re-injected into the tracker + zero-LLM score written back to the strategy store |

---

## 8. Wiring index (source locations)

- Assembly entry: `cmd/ares/peer_assembly.go` (fabric/scheduler/L2 core/syscalls), `cmd/ares/serve_wiring.go` (toolchain/control plane), `cmd/ares/serve_peer.go` (evolution wiring/panel)
- Execution core: `internal/agentruntime/` (execution/session/submit), `internal/kernel/` (scheduler split files), `internal/fabric/task/` (task fabric)
- Primitive implementations: `internal/agents/{peer,lease,outputguard}/`, `internal/agentipc/`
- Capability implementations: `internal/runtime/protocol/skills/` (Catalog / Discovery / Loader / Experience / FTS5 / git-http sources / changes)
- Memory wiring: `internal/runtime/memory/` (manager_impl + context/cleaner + context/session)

---

## 9. Notes

- This inventory covers capabilities injected **at serve startup**; post-`Catalog.Activate` MCP connections and on-demand SKILL.md / references loading are runtime on-demand capabilities and are excluded.
- Birth capabilities that are **gone** (don't look for them): the leader task planner, AHP message queues (`ahp.NewMessageQueue`), ActionLog audit (`WithActionLog`), the `internal/agents/actionlog` package, and the experience locator (`WithExperienceLocator`) — all removed with Leader-Sub or as zero-production-call sites.
