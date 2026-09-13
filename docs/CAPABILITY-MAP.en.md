# ARES Capability–Module Map

> Start from "I want to use capability X" and locate the corresponding code module in one step, without first understanding the directory layering.
> Verified against the code: 2026-09-13 (dev branch, v0.3.1).

Conventions: Paths are relative to the repo root. `★` = the capability has a dedicated CLI subcommand.

---

## The Map

| Capability | Entry module | What it does | CLI / SDK |
|---|---|---|---|
| **Agent execution** | `internal/agents/base` | Single-agent Run / Stream | `sdk.NewAgent` |
| **Shared L2 execution core** | `internal/agentruntime` | Session registry + plan-projection compile + router cognition + submitter (shared by serve and SDK since v0.3.1 — the only execution engine) | — |
| **Multi-agent dispatch** | `internal/agents/sub`, `internal/fabric/task`, `internal/agentipc` | Capability-based task fabric (state machine / lease / epoch / checkpoint), peer IPC | `rt.Submit` |
| **Runtime wiring** | `internal/ares_bootstrap` | Component-graph assembly, dependency injection, evolution/memory/knowledge wiring | `ares serve` |
| **Kernel scheduler** | `internal/kernel` | Drain loop, capability scoring, lease heartbeat, priority preemption, recovery nomination | — |
| **Agent lifecycle** | `internal/fabric/agent` | Spawn/Kill/Suspend, session registry, L2 graphs | — |
| **Crash recovery** | `internal/aresrecovery` | Lease expiry → requeue → replacement agent → checkpoint resume; cross-restart task restore (`RestoreFromStore`, v0.3.1) | — |
| ★ **Strategy Evolution GA** | `internal/runtime/ares_evolution` | Population evolution, gated promotion (shadow A/B, regression gate, manual approval), lifecycle, dream cycle | `ares evolution run/status` |
| ★ **Runtime Patch Engine** | `internal/runtime/evolution` | Genome/Diff/Patch: hot-patch DAG/recovery/knowledge parameters | `ares evolution run` (under the hood) |
| **DAG engine** | `internal/fabric/task/workflow/engine` | MutableDAG, DAG/recovery patch executors, HITL plugin (HITL not production-wired) | — |
| **Plan round loops** | `internal/fabric/task` | PlanLoop: re-execute a plan DAG for N rounds + UntilCondition + Replan hook (v0.3.1) | `create_plan loop` argument |
| **Memory & Distillation** | `internal/runtime/memory` | Session context, distillation, vector embedding, experience/feedback | `sdk.WithDefaultMemory` |
| **Serve memory enrichment** | `cmd/ares/memory_enricher.go` | Cross-turn session memory on the serve submission path (PromptEnricher, v0.3.1) | — |
| **Event storage** | `internal/ares_events` | Event persistence (memory/PG), compaction, trimming, retention cleanup | — |
| ★ **Knowledge system** | `internal/knowledge` | Knowledge planning, compilation, linking, retrieval, storage (AKF) | `ares knowledge build` |
| **LLM clients** | `internal/llm` | OpenAI / OpenRouter / Ollama / Anthropic adapters + failover | `sdk.WithOpenAI` etc. |
| **Tool system** | `internal/tools`, `internal/apitools` | Built-in tools (web_search/calculator/regex/json/file etc.), registry | `sdk.WithTools` |
| ★ **MCP integration** | `internal/mcpclient` | MCP server discovery and connection | `sdk.WithMCP` |
| ★ **Chaos Arena** | `internal/runtime/arena` | Fault injection (Kill/Partition/Pause/Slow/LLMFailure etc.), survival/scenario modes | `ares arena run/validate/…` |
| ★ **Flight Recorder** | `internal/runtime/observability/flight` | Task recording & replay | `ares flight inspect/replay` |
| **Security & auth** | `internal/ares_security` | JWT / API key / introspect token, audit | — |
| **Rate limiting** | `internal/ares_ratelimit` | Request rate limiting | — |
| **Graceful shutdown** | `internal/ares_shutdown` | Coordinated multi-component shutdown | — |
| ★ **Evaluation framework** | `internal/runtime/eval` | Evaluation runner, dimension scoring, comparison, reports | `ares bench` |
| **Observability** | `internal/runtime/observability`, `internal/introspect` | OTel traces / Prometheus / read-only introspect panel | — |
| **Callback injection** | `internal/ares_callbacks` | Callback bridging | — |
| **Discovery & registration** | `internal/discovery` | Provider discovery and registration | — |
| ★ **HTTP API service** | `cmd/ares/agent.go` + `agent_routes_*.go` | REST: `GET /api/tools`, `POST /api/tools/call`, `POST /api/tasks`, `POST /api/graphs`, `/api/v1/introspect/*` | `ares serve` |
| ★ **Status at a glance** | `cmd/ares/status.go` | Runtime health/config/capability overview | `ares status` |
| **SDK entry** | `sdk/` | `sdk.New` / `sdk.MustNew` — one-stop initialization | `sdk.MustNew` |
| ★ **CLI entry** | `cmd/ares/` | Entry point for all subcommands | `ares …` |

> Deleted capabilities (don't look for them): Leader-Sub architecture (removed in v0.3.x); the quant-trading module (`ares_quant` no longer exists); the standalone plugin package (`internal/plugins` is gone — the plugin surface converged into `internal/runtime`'s PluginBus); `api/*` public user-facing packages (forwarding layer since v0.3.0; the only supported public API is `sdk`).

---

## Quick Navigation

### Two most common confusions

**Q: What's the difference between `internal/runtime/ares_evolution` and `internal/runtime/evolution`?**

| Directory | Responsibility |
|---|---|
| `internal/runtime/ares_evolution` | **GA population evolution** — population, crossover/mutation, scoring, gated promotion, lifecycle, dream cycle |
| `internal/runtime/evolution` | **Runtime Patch Engine** — Genome/Diff/Patch, hot-patching DAG/recovery/knowledge parameters at deploy time |

**Q: Are serve and the SDK two separate execution engines?**

No. Since v0.3.1 (the "convergence") both build the same L2 execution core via `internal/agentruntime.NewExecution` (session registry + plan projection + router cognition + submitter); the SDK no longer has its own built-in ReAct engine. Construction points are locked by `sdk/arch_test.go` (only `sdk/l2.go`, `cmd/ares/agent_kernel.go`, `cmd/ares/peer_assembly.go` may construct it).

### Three-step navigation

1. Find your capability in the table above; note the **entry module** path
2. Entry in `internal/` → concrete implementation lives there; entry in `sdk/` → the public interface is defined there
3. The HTTP surface is exposed by `ares serve` (`/api/tools`, `/api/tasks`, `/api/graphs`, `/api/v1/introspect/*`)
