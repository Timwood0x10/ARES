# Examples

ARES examples, organized as a LEARNING PATH across four layers. Every example
runs on the single kernel execution path (taskfabric + kernelscheduler) —
there is no second engine.

| Layer | What you learn |
|---|---|
| **Basics** | SDK four verbs: NewRuntime → NewAgent/RegisterAgent → Run/Submit |
| **Orchestration** | `sdk.Graph`: conditions, router loops, fan-out+join, subgraphs; HTTP graph submission |
| **Kernel internals** | Watch the scheduler work; LLM-decided spawn via kernel syscalls; deterministic AgentOS baseline |
| **Evolution** | GA strategy evolution and genome patching |

Quick start (no API key needed):

```bash
make quickstart        # = go run examples/01-quickstart/main.go with Ollama
```

Legend: ★ flagship · LLM = needs a configured provider · dry = runs without an LLM

## Basics

| Example | Concept | Needs LLM |
|---|---|---|
| [01-quickstart](01-quickstart/) | Runtime → Agent → Run, minimal surface | yes |
| [02-tool-calling](02-tool-calling/) | Tool registry + ReAct loop | yes |
| [04-multi-agent](04-multi-agent/) | RegisterAgent by capability + Submit dispatch | yes |
| [07-human-in-loop](07-human-in-loop/) | Human approval gates inside agent loops | yes |
| [12-yaml-driven-flags](12-yaml-driven-flags/) | Config-driven setup (`ares.yaml`) | no |

## Orchestration

| Example | Concept | Needs LLM |
|---|---|---|
| [03-dag-workflow](03-dag-workflow/) | sdk.Graph core shapes + the three collaboration modes (delegate / pipeline / orchestrate) | dry |
| [28-collab-graphs](28-collab-graphs/) | Submit explicit DAGs over HTTP (`POST /api/graphs`); ops surface of C4 | yes (serve) |
| [29-akf-graph-node](29-akf-graph-node/) | AKF knowledge-fabric step as a `sdk.Graph` node (BETA adapter) | no |
| [09-full-app](09-full-app/) | Composing tools + memory + agents into a small app | yes |
| [21-ai-assistant-integration](21-ai-assistant-integration/) | Embedding ARES into an existing assistant stack | yes |

## Kernel internals

| Example | Concept | Needs LLM |
|---|---|---|
| [26-runtime-scheduling-demo](26-runtime-scheduling-demo/) ★ | Watch the kernelscheduler drive a capability agent | yes |
| [27-peer-spawn-demo](27-peer-spawn-demo/) ★★ | REAL LLM autonomously decomposes: spawn_agent ×N + create_task ×N through kernel syscalls; captured evidence in `evidence/` | yes |
| [aresos-demo](aresos-demo/) | Deterministic 7-step AgentOS baseline (spawn → parallel → death → IPC → revival → synthesis), zero deps | **no** |
| [06-chaos-resilience](06-chaos-resilience/) | Failure injection & recovery semantics | partial |

## Evolution

| Example | Concept | Needs LLM |
|---|---|---|
| [05-evolution-demo](05-evolution-demo/) | Strategy evolution intro (`rt.Evolve`) | yes |
| [10-ga-full-evolution](10-ga-full-evolution/) | Full GA pipeline on public api/evolution blocks | no |
| [19-ga-candidate-e2e](19-ga-candidate-e2e/) | Multi-generation GA → champion → CandidateVerifier gates | no |
| [22-evolution-blocks](22-evolution-blocks/) | Zero-internal composition path for external embedders | no |
| [runtime_evolution/](runtime_evolution/) | Genome patching over engine DAGs (workflow/knowledge/recovery) | no |

> The scheduler genome dimension was RETIRED (fusion plan §B1): sdk.Graph runs
> fully-parallel ready batches. A future concurrency dimension may evolve
> `sdk.Graph.MaxRoundConcurrency`.

## Advanced / integrations

Unnumbered utility examples, each demonstrating one integration surface:

| Directory | Surface |
|---|---|
| [08-mcp-integration](08-mcp-integration/) · [mcp-registry](mcp-registry/) | MCP tool discovery & servers |
| [11-knowledge-import](11-knowledge-import/) · [knowledge-fabric](knowledge-fabric/) | AKF/AKG knowledge pipeline & tools |
| [13-archive-akg-chain](13-archive-akg-chain/) | Archive → AKG distillation chain |
| [14-tool-discovery](14-tool-discovery/) · [external-tools](external-tools/) | Tool discovery sources |
| [15-llm-evolution-suite](15-llm-evolution-suite/) · [25-dual-endpoint-fallback](25-dual-endpoint-fallback/) | LLM-driven evolution suite · endpoint failover |
| [arena](arena/) · [eval](eval/) | Chaos arena CLI · evaluation harness |
| [custom-store](custom-store/) | Pluggable knowledge store backend |
| [discovery](discovery/) | Legacy service discovery (deprecated) |
