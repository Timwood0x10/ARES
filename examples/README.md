# Examples

ARES SDK examples, ordered by complexity (01 → 09).

## Quick Start

```bash
make quickstart
# or directly:
go run examples/01-quickstart/main.go
```

---

## Official SDK Examples

| # | Example | Core Concept | Lines |
|---|---|---|---|
| 01 | **[01-quickstart](01-quickstart/main.go)** | Minimal agent: Runtime → Agent → Run | ≤20 |
| 02 | **[02-tool-calling](02-tool-calling/main.go)** | Multi-tool registration, ReAct loop | ≤60 |
| 03 | **[03-dag-workflow](03-dag-workflow/main.go)** | `NewGraph` + `FuncNode` + conditional `Edge` | ≤130 |
| 04 | **[04-multi-agent](04-multi-agent/main.go)** | `NewTeam` leader/member orchestration | ≤60 |
| 05 | **[05-evolution-demo](05-evolution-demo/main.go)** | `Evolve()` instruction evolution before/after | ≤96 |
| 06 | **[06-chaos-resilience](06-chaos-resilience/main.go)** | Real filesystem fault tolerance + self-healing | ≤114 |
| 07 | **[07-human-in-loop](07-human-in-loop/main.go)** | `WithHumanInput` human approval for tool calls | ≤150 |
| 08 | **[08-mcp-integration](08-mcp-integration/main.go)** | `WithMCP` connect to MCP server | ≤73 |
| 09 | **[09-full-app](09-full-app/main.go)** | Web UI + Agent + Tools + Memory + Stats | ≤240 |

## Other Examples

| Example | Description |
|---|---|
| [autonomous-evolution](autonomous-evolution/) | Self-evolving Dream Cycle demo |
| [quant-trading](quant-trading/) | Quantitative trading multi-agent system |
| [graph_demo](graph_demo/) | MutableDAG graph orchestration scenarios |
| [knowledge-base](knowledge-base/) | Knowledge base + distillation |
| [mcp-server](mcp-server/) | MCP server implementation |
| [mcp-dashboard](mcp-dashboard/) | MCP + Dashboard monitoring |
| [travel](travel/) | Travel planning DAG workflow |
| [end-to-end](end-to-end/) | End-to-end workflow |
| [tool-intelligence](tool-intelligence/) | Tool intelligence orchestration |

---

## Build All

```bash
make examples
```
