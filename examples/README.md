# Examples

ARES examples live in two areas with different audiences:

| Area | Audience | Rule |
|---|---|---|
| [`_fixtures/`](_fixtures/) | **External users** — copy-paste and build your own agent | **Pure `sdk/` only.** No `internal/` imports anywhere (Go's internal visibility rule means you could not compile them anyway). |
| [`_internal/`](_internal/) | **ARES contributors** — how the machinery works | Deep dives into kernel scheduling, the AKF/AKG knowledge pipeline, GA evolution blocks and service discovery. These drive `internal/` APIs directly and are **not** integration templates. |

Every external example runs on the single kernel execution path
(taskfabric + kernelscheduler) — there is no second engine.

> **2026-09-11 restructure:** the `api/` + `compat/` forwarding layer was
> deleted; `sdk/` is now the only public surface (it re-exports the tool
> types via `sdk.Tool` / `sdk.ToolFunc` / `sdk.ToolResult`). Examples that
> needed the old forwarding layer or drove internal subsystems directly
> moved to `_internal/`.

Quick start (no API key needed):

```bash
make quickstart        # = go run examples/_fixtures/01-quickstart/main.go with Ollama
```

Legend: ★ flagship · LLM = needs a configured provider · dry = runs without an LLM

## External examples (`_fixtures/`, pure `sdk/`)

### Basics

| Example | Concept | Needs LLM |
|---|---|---|
| [01-quickstart](_fixtures/01-quickstart/) | Runtime → Agent → Run, minimal surface | yes |
| [02-tool-calling](_fixtures/02-tool-calling/) | Custom tools (`sdk.ToolFunc`) + ReAct loop | yes |
| [04-multi-agent](_fixtures/04-multi-agent/) | RegisterAgent by capability + Submit dispatch | yes |
| [07-human-in-loop](_fixtures/07-human-in-loop/) | Human approval gates inside agent loops | yes |
| [12-yaml-driven-flags](_fixtures/12-yaml-driven-flags/) | Config-driven setup (`ares.yaml`) | no |

### Orchestration

| Example | Concept | Needs LLM |
|---|---|---|
| [03-dag-workflow](_fixtures/03-dag-workflow/) | sdk.Graph core shapes + the three collaboration modes (delegate / pipeline / orchestrate) | dry |
| [28-collab-graphs](_fixtures/28-collab-graphs/) | Submit explicit DAGs over HTTP (`POST /api/graphs`); ops surface of C4 | yes (serve) |
| [09-full-app](_fixtures/09-full-app/) | Composing tools + memory + agents into a small app | yes |

### Evolution

| Example | Concept | Needs LLM |
|---|---|---|
| [05-evolution-demo](_fixtures/05-evolution-demo/) | Strategy evolution intro (`rt.Evolve`) | yes |

### Kernel & resilience

| Example | Concept | Needs LLM |
|---|---|---|
| [27-peer-spawn-demo](_fixtures/27-peer-spawn-demo/) ★★ | REAL LLM autonomously decomposes: spawn_agent ×N + create_task ×N through sdk syscalls; captured evidence in `evidence/` | yes |
| [06-chaos-resilience](_fixtures/06-chaos-resilience/) | Failure injection & recovery semantics | partial |

### Integrations & evaluation

| Example | Surface |
|---|---|
| [08-mcp-integration](_fixtures/08-mcp-integration/) | MCP tool discovery & servers |
| [25-dual-endpoint-fallback](_fixtures/25-dual-endpoint-fallback/) | Dual-endpoint LLM fallback config template (`ares.yaml`, no Go code) |
| [eval](_fixtures/eval/) · [evaluation](_fixtures/evaluation/) | Evaluation harness + shared assertion library |

> The scheduler genome dimension was RETIRED (fusion plan §B1): sdk.Graph runs
> fully-parallel ready batches. A future concurrency dimension may evolve
> `sdk.Graph.MaxRoundConcurrency`.

## Internal reference (`_internal/`, drives `internal/` directly)

Not for external integration — these exist to show the machinery and to serve
as regression evidence. They will not compile from another module.

| Example | What it demonstrates | internal packages |
|---|---|---|
| [26-runtime-scheduling-demo](_internal/26-runtime-scheduling-demo/) ★ | Watch the kernelscheduler drive a capability agent | kernel scheduler, fabric |
| [aresos-demo](_internal/aresos-demo/) | Deterministic 7-step AgentOS baseline (spawn → parallel → death → IPC → revival → synthesis), zero sdk | agents, fabric, runtime |
| [knowledge-fabric](_internal/knowledge-fabric/) | AKF/AKG pipeline wiring in full | knowledge/* (23 pkgs) |
| [11-knowledge-import](_internal/11-knowledge-import/) | Archive → AKG import chain | knowledge/* |
| [13-archive-akg-chain](_internal/13-archive-akg-chain/) | Archive → AKG distillation chain | knowledge/* |
| [21-ai-assistant-integration](_internal/21-ai-assistant-integration/) | Embedding the AKG runtime into an assistant stack | knowledge/runtime, knowledge/service |
| [29-akf-graph-node](_internal/29-akf-graph-node/) | AKF knowledge-fabric step as a `sdk.Graph` node (BETA adapter) | knowledge/compiler, planner, provider, runtime, workflow |
| [10-ga-full-evolution](_internal/10-ga-full-evolution/) | Full GA pipeline on evolution blocks | evoapi, evoapi/mutation |
| [19-ga-candidate-e2e](_internal/19-ga-candidate-e2e/) | Multi-generation GA → champion → CandidateVerifier gates | ares_evolution, evidence, agents |
| [22-evolution-blocks](_internal/22-evolution-blocks/) | Raw evolution-block composition | evoapi |
| [runtime_evolution/](_internal/runtime_evolution/) | Genome patching over engine DAGs (workflow/knowledge/recovery) | runtime/evolution/* |
| [15-llm-evolution-suite](_internal/15-llm-evolution-suite/) | LLM-driven evolution suite | evolution, llm |
| [14-tool-discovery](_internal/14-tool-discovery/) | Tool discovery sources | tools/toolsource |
| [external-tools](_internal/external-tools/) | Discovered MCP tools wired into a registry | discoveryapi, mcpclient |
| [mcp-registry](_internal/mcp-registry/) | Service registry lifecycle (`make demo-mcp`) | discoveryapi |
| [custom-store](_internal/custom-store/) | Pluggable discovery store backend | discoveryapi |
| [discovery](_internal/discovery/) | Legacy service discovery (**deprecated**) | discoveryapi |

## arena/

YAML chaos/regression scenarios (not Go examples): [`arena/`](arena/).
