# Examples

ARES SDK examples, ordered by complexity (01 → 09).

## Quick Start

```bash
# Ollama (default, no API key needed)
make quickstart

# or directly:
go run examples/01-quickstart/main.go
```

## Official SDK Examples

| # | Example | Run Command | Core Concept | Lines |
|---|---|---|---|---|
| 01 | Quickstart | `go run examples/01-quickstart/main.go` | Minimal agent: Runtime → Agent → Run | ≤20 |
| 02 | Tool Calling | `go run examples/02-tool-calling/main.go` | Multi-tool registration, ReAct loop | ≤60 |
| 03 | DAG Workflow | `go run examples/03-dag-workflow/main.go` | `NewGraph` + `FuncNode` + conditional `Edge` | ≤130 |
| 04 | Multi-Agent | `go run examples/04-multi-agent/main.go` | `NewTeam` leader/member orchestration | ≤60 |
| 05 | Evolution | `go run examples/05-evolution-demo/main.go` | `Evolve()` instruction evolution before/after | ≤96 |
| 06 | Chaos | `go run examples/06-chaos-resilience/main.go` | 9 failure modes: file, timeout, network, MCP, LLM... | ≤179 |
| 07 | Human-in-Loop | `go run examples/07-human-in-loop/main.go` | `WithHumanInput` human approval for tool calls | ≤150 |
| 08 | MCP | `go run examples/08-mcp-integration/main.go` | `WithMCP` connect to MCP server | ≤73 |
| 09 | Full App | `go run examples/09-full-app/main.go` | Web UI + Agent + Tools + Memory + Stats (open :8080) | ≤240 |
| Eval | Evaluation | `go run examples/eval/main.go` | 5 scenarios: chat, tool, multi-agent, resilience, evolution | ≤264 |

## Evaluation Scenarios

Run all 5 capability evaluations:

```bash
go run examples/eval/main.go
```

Measures:
- **basic-chat**: response correctness (contains expected answer)
- **tool-calling**: tool invocation accuracy
- **multi-agent**: team collaboration output
- **resilience**: graceful error handling
- **evolution**: instruction improvement before/after (score delta)

## Other Examples

| Example | Run Command | Description |
|---|---|---|
| [autonomous-evolution](autonomous-evolution/) | `go run examples/autonomous-evolution/` | Self-evolving Dream Cycle demo |
| [quant-trading](quant-trading/) | `go run examples/quant-trading/` | Quantitative trading multi-agent system |
| [graph_demo](graph_demo/) | `go run examples/graph_demo/basic/` | MutableDAG graph orchestration |
| [mcp-server](mcp-server/) | `go run examples/mcp-server/ serve` | MCP server implementation |
| [knowledge-base](knowledge-base/) | `go run examples/knowledge-base/` | Knowledge base + distillation |
| [travel](travel/) | `go run examples/travel/` | Travel planning DAG workflow |

## Build All

```bash
make examples
```
