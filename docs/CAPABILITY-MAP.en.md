# ARES Capability–Module Map

> Start from "I want to use capability X" and locate the corresponding code module in one step, without first understanding the directory layering.

Conventions: Paths are relative to the repo root. `★` = the capability has a dedicated CLI subcommand.

---

## The Map

| Capability | Entry module | What it does | CLI / SDK |
|---|---|---|---|
| **Agent execution** | `internal/agents/base` | Single-agent Run / Stream | `sdk.NewAgent` |
| **Multi-agent orchestration** | `internal/agents/leader`, `internal/agents/sub` | Leader/Sub dispatch, aggregation, heartbeat, checkpoint recovery | `rt.NewTeam` |
| **Runtime wiring** | `internal/ares_bootstrap` | Dependency injection, config loading, service wiring | `ares serve` |
| ★ **Strategy Evolution GA** | `internal/ares_evolution` | Population evolution, crossover/mutation, scoring/promotion, dream cycle | `ares evolution run/status` |
| ★ **Runtime Patch Engine** | `internal/evolution` | Hot-patch DAG/scheduler/recovery strategies at deploy time | `ares evolution deploy` |
| ★ **DAG Workflow** | `internal/workflow` | Directed acyclic graph orchestration, conditional branching, auto-recovery | `ares workflow run` |
| **Memory & Distillation** | `internal/ares_memory` | Session context, task distillation, vector embedding | `sdk.WithDefaultMemory` |
| **Long-term memory store** | `internal/memoryservice` | Memory persistence read/write service | — |
| **Vector retrieval** | `internal/retrievalservice` | Unified retrieval interface (vector + knowledge) | — |
| **Event storage** | `internal/ares_events` | Event persistence, compaction, trimming | — |
| ★ **Knowledge Graph** | `internal/knowledge` | Knowledge planning, compilation, linking, retrieval, storage | `ares knowledge build` |
| **LLM clients** | `internal/llm` | OpenAI / Ollama / Anthropic adapters | `sdk.WithOpenAI` etc. |
| **Tool system** | `internal/tools` | Built-in tools, formatting, planner, resource management | `sdk.WithTools` |
| ★ **MCP integration** | `internal/ares_mcp` | SSE / stdio protocol, connect any MCP server | `sdk.WithMCP` |
| ★ **Chaos Arena** | `internal/ares_arena` | Fault injection, load testing, scenario orchestration, survival testing | `ares arena run/validate/…` |
| ★ **Flight Recorder** | `internal/ares_flight` | Task recording & replay | `ares flight inspect/replay` |
| **Security & auth** | `internal/ares_security` | Security policies, AHP protocol | — |
| **Rate limiting** | `internal/ares_ratelimit` | Request rate limiting | — |
| **Graceful shutdown** | `internal/ares_shutdown` | Coordinated multi-component shutdown | — |
| ★ **Evaluation framework** | `internal/ares_eval` | Evaluation runner, LLM judge, dimension scoring, comparison, reports | `ares bench` |
| **Observability** | `internal/ares_observability`, `internal/monitoring` | Traces / Metrics / Logs | — |
| **Callback injection** | `internal/ares_callbacks` | Callback bridging | — |
| ★ **HTTP API service** | `api/handler`, `api/router`, `api/service` | External REST interface | `ares serve` |
| **API client** | `api/client` | Unified client, config, health check | `ares` CLI |
| **SDK entry** | `sdk/` | `sdk.MustNew` — one-stop initialization | `sdk.MustNew` |
| **Quantitative trading** | `internal/ares_quant` | Market making, indicators, portfolio management, research | — |
| ★ **CLI entry** | `cmd/ares/` | Entry point for all subcommands | `ares …` |
| **Plugin system** | `internal/plugins` | Resurrection and other plugins | — |
| **Discovery & registration** | `internal/discovery` | Provider discovery and registration | — |

---

## Quick Navigation

### Two most common confusions

**Q: What's the difference between `internal/ares_evolution` and `internal/evolution`?**

| Directory | Responsibility | Package name |
|---|---|---|
| `internal/ares_evolution` | **Strategy Evolution GA** — population, crossover, mutation, scoring, promotion | `evolution` |
| `internal/evolution` | **Runtime Patch Engine** — hot-patch DAG/scheduler/recovery at deploy time | `coordinator`/`diff`/`patch`/`genome` |

**Q: What's the difference between `internal/ares_memory`, `internal/memoryservice`, and `api/memory`?**

| Path | Responsibility |
|---|---|
| `internal/ares_memory` | Primary memory module: session context, distillation, vector push |
| `internal/memoryservice` | Long-term memory database read/write service |
| `api/memory` | HTTP-layer memory interface |

### Three-step navigation

1. Find your capability in the table above; note the **entry module** path
2. Entry in `internal/` → concrete implementation lives there; entry in `api/` → interface definition lives there
3. Need the bridge from interface to implementation → `internal/api_impl/`

---

**Code snapshot**: `dev` branch, 1295 `.go` files, 41 top-level packages under `internal/`, 19 sub-packages under `api/`.