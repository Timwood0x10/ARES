
```shell
           _____  ______  _____ 
     /\   |  __ \|  ____|/ ____|
    /  \  | |__) | |__  | (___  
   / /\ \ |  _  /|  __|  \___ \ 
  / ____ \| | \ \| |____ ____) |
 /_/    \_\_|  \_\______|_____/ 

```

**ARES** — Agent Runtime & Evolution System.

Build resilient, self-evolving AI agents in Go. Unified SDK, DAG workflow, chaos engineering, MCP support.

**Runtime Evolution**: ARES continuously evolves its DAG topology, scheduler, knowledge planner, and recovery strategies — all in production, without restarts. LLM is a participant in evolution, not the leader.

## Quick Start

```go
package main

import "github.com/Timwood0x10/ares/sdk"

func main() {
    rt := sdk.MustNew(sdk.WithOllama("llama3.2"))
    defer rt.Close()

    agent := rt.NewAgent("assistant")
    result, _ := agent.Run(ctx, "Say hello")
    println(result.Output)
}
```

Install the CLI:

```bash
go install github.com/Timwood0x10/ares/cmd/ares@latest
ares doctor
ares run -c ares.yaml "What is Go?"
```

Or run examples directly:

```bash
git clone https://github.com/Timwood0x10/ares
cd ares
make quickstart        # go run examples/quickstart
make examples          # build all 24 examples
```

## Features

| Feature | Description |
|---|---|
| **Unified SDK** | Single `sdk.MustNew()` API for LLM, tools, memory, evolution |
| **Runtime Evolution** | Genome + Diff Engine + Coordinator evolve DAG, scheduler, planner, recovery in production |
| **Strategy GA** | Population-based strategy optimization — NSGA-II multi-objective, steady-state, uniform/two-point/segment crossover, 6 mutation types |
| **Evidence-Driven** | Every runtime event (flight, chaos, fitness) feeds into evolution decisions |
| **DAG Workflow** | Dynamic graphs with conditional branching and recovery |
| **Chaos Resilient** | Fault injection, failover, survival testing, self-healing |
| **Memory** | Session context, task distillation, vector similarity search |
| **MCP Ready** | Connect any Model Context Protocol server for tools and data |
| **Multi-Agent** | Leader/sub orchestration with automatic failover |
| **Observability** | OpenTelemetry traces, structured logs, Prometheus metrics |

## CLI

```bash
ares serve              # Start full agent monitoring (LLM + MCP + dashboard)
ares agent list         # List all registered agents
ares arena run/validate/list/serve/survival/inspect  # Chaos engineering scenarios
ares evolution run/status         # Runtime evolution
ares flight inspect/replay        # Inspect and replay task recordings
ares workflow run <id> <input>    # Execute a workflow
ares knowledge build <goal>       # Build a knowledge graph (via HTTP API)
ares mcp-null serve     # Start minimal MCP null server (stdio)
ares db migrate/setup-test/create-table/check-rls  # Database management
ares init               # Scaffold a new project (main.go + ares.yaml)
ares run                # Run agent from config file
ares bench              # Quick performance benchmark
ares doctor             # Diagnose environment (LLM key, Ollama, Git)
ares version            # Show version
```

## SDK

```go
rt := sdk.MustNew(
    sdk.WithOpenAI("gpt-4o-mini"),          // or WithOllama, WithAnthropic
    sdk.WithDefaultMemory(),                 // session history
    sdk.WithEvolution(),                     // strategy evolution
    sdk.WithMCP(sdk.MCPConn{                 // MCP server tools
        Name: "my-server", Command: "/path/to/server", Args: []string{"serve"},
    }),
)
defer rt.Close()

// Agent with tools and human-in-the-loop.
agent := rt.NewAgent("assistant",
    sdk.WithInstruction("You are helpful."),
    sdk.WithTools(calculatorTool, weatherTool),
    sdk.WithHumanInput(approveFn),
)
result, _ := agent.Run(ctx, "Calculate 15*23")

// Streaming response.
ch, _ := agent.Stream(ctx, "Tell me a story")
for chunk := range ch { fmt.Print(chunk.Content) }

// Multi-agent team.
team := rt.NewTeam("project", leaderAgent, []*Agent{memberAgent})
teamResult, _ := team.Run(ctx, "Research and write")
```

See [examples/README.md](examples/README.md) for 9 hands-on examples.

## Articles

Deep dives into ARES internals:

| English | 中文 |
|---|---|
| [Architecture](docs/articles/en/architecture-overview-deep-dive.md) | [架构](docs/articles/zh/architecture-overview-deep-dive.md) |
| [Evolution](docs/articles/en/autonomous-evolution-deep-dive.md) | [进化](docs/articles/zh/autonomous-evolution-deep-dive.md) |
| [MCP Integration](docs/articles/en/mcp-integration-deep-dive.md) | [MCP 集成](docs/articles/zh/mcp-integration-deep-dive.md) |
| [Workflow Engine](docs/articles/en/workflow-engine-deep-dive.md) | [工作流引擎](docs/articles/zh/workflow-engine-deep-dive.md) |
| [Memory & Distillation](docs/articles/en/memory-distillation-deep-dive.md) | [记忆与蒸馏](docs/articles/zh/memory-distillation-deep-dive.md) |
| [Chaos Arena](docs/articles/en/arena-fault-injection-deep-dive.md) | [混沌测试](docs/articles/zh/arena-fault-injection-deep-dive.md) |

## Architecture

```mermaid
graph TB
    User["User / CLI"] --> SDK

    subgraph SDK ["SDK Layer (sdk/)"]
        RT["Runtime<br/>MustNew / New"]
        A["Agent<br/>Run / Stream"]
        T["Team<br/>Multi-Agent"]
        CFG["Config<br/>YAML + Options"]
        EV["Evolve()<br/>GA Strategy Evolution"]
    end

    SDK --> LLM
    SDK --> Tools
    SDK --> Memory
    SDK --> Evo

    subgraph LLM ["LLM Providers"]
        OAI["OpenAI"]
        OLL["Ollama"]
        ANTH["Anthropic"]
        OR["OpenRouter"]
    end

    subgraph Tools ["Tool System"]
        BT["Built-in<br/>calculator, search..."]
        MCP["MCP Servers<br/>Stdio / SSE"]
        CT["Custom Tools<br/>ToolFunc"]
    end

    subgraph Memory ["Memory System"]
        SES["Session Context"]
        DIST["Task Distillation"]
        VEC["Vector Search"]
        CONF["Config<br/>max_history, session_ttl..."]
        MP["Memory Patch Executor<br/>Runtime Evolution"]
    end

    subgraph Evo ["GA Evolution Engine"]
        direction TB
        POP["Population<br/>N individuals"]
        SEL["7 Selection Operators<br/>tournament/rank/nsga2..."]
        CROSS["3 Crossover Types<br/>uniform/two_point/segment"]
        MUT["6 Mutation Types<br/>param/swap/inversion/scramble..."]
        SCORE["Experience-Guided Scoring<br/>multi-objective"]
        SS["Steady-State GA<br/>online learning mode"]
        SHARE["Fitness Sharing<br/>SelectionScore preservation"]
    end

    POP --> SEL --> CROSS --> MUT --> SCORE
    SCORE --> POP
    SS -.-> POP

    subgraph RuntimeEvo ["Runtime Evolution Pipeline"]
        direction TB
        TICKER["Background Ticker<br/>5min interval"]
        SCHED["Scheduler<br/>OnAgentEnd callback"]
        ADAPTER["GenomePopulationAdapter<br/>Run()"]
        GENOME["Genomes<br/>Workflow / Scheduler / Knowledge<br/>Recovery / Planner / Memory"]
        DIFF["Diff Engine<br/>4 Differs"]
        COORD["Coordinator<br/>Apply / Reject / Delay"]
        EXEC["Executors<br/>Graph / Recovery / Knowledge / Memory"]
        STORE["Strategy Store<br/>Active Strategy"]
        AGENT["Live Agent<br/>consume evolved params"]
    end

    TICKER --> ADAPTER
    SCHED --> ADAPTER
    ADAPTER --> GENOME
    GENOME --> DIFF
    DIFF --> COORD
    COORD --> EXEC
    ADAPTER --> STORE
    STORE --> AGENT

    Evo --> ADAPTER
    AGENT --> LLM
    AGENT --> Tools
    AGENT --> Memory

    subgraph CLI ["CLI (cmd/ares/)"]
        INIT["ares init"]
        RUN["ares run"]
        BENCH["ares bench"]
        DOCTOR["ares doctor"]
        EVO["ares evolution"]
        ARENA["ares arena"]
    end

    subgraph EX ["Examples"]
        QS["01 Quickstart"]
        TC["02 Tool Calling"]
        DAG["03 DAG Workflow"]
        MA["04 Multi-Agent"]
        EVO_DEMO["05 Evolution Demo"]
        CHAOS["06 Chaos Resilience"]
        HIL["07 Human-in-Loop"]
        GA_FULL["10 GA Full Evolution"]
    end

    style SDK fill:#1e3a5f,stroke:#3b82f6,color:#fff
    style LLM fill:#1a2332,stroke:#64748b
    style Tools fill:#1a2332,stroke:#64748b
    style Memory fill:#1a2332,stroke:#64748b
    style Evo fill:#1a2332,stroke:#64748b
    style RuntimeEvo fill:#2d1b69,stroke:#8b5cf6,color:#fff
    style CLI fill:#2d1b69,stroke:#8b5cf6,color:#fff
    style EX fill:#1a3a2a,stroke:#22c55e
```

## Data Flow

```mermaid
sequenceDiagram
    participant U as User
    participant S as SDK
    participant A as Agent
    participant GA as GA Engine
    participant C as Coordinator
    participant E as Executors
    participant M as Memory

    U->>S: rt.Evolve(agent, task)
    S->>GA: Create Population(10)
    loop 3 generations
        GA->>GA: ScoreAgents(execution results)
        GA->>GA: Evolve(selection → crossover → mutation)
    end
    GA->>S: BestStrategy params
    S->>A: applyEvolvedParams(tool_selector, search_depth, scheduler...)

    Note over S,A: Strategy params applied to live agent

    U->>A: agent.Run(task)
    A->>M: Read strategy, load tools
    A->>A: Execute with evolved params
    A->>C: Submit evidence
    C->>E: Apply patches if needed

    Note over GA,C: Background: ticker + scheduler trigger evolution
    loop Every 5min
        GA->>GA: Run evolution cycle
        GA->>C: submitToCoordinator(patches)
        C->>E: Evaluate & Apply
    end
```

## Cookbook

| Recipe | Code |
|---|---|
| [Chat Agent](docs/cookbook/chat.md) | 20-line conversational agent |
| [Tool Calling](docs/cookbook/tool.md) | Custom tools for LLM function calling |
| [Multi-Agent](docs/cookbook/multi-agent.md) | Leader/member team orchestration |
| [Memory](docs/cookbook/memory.md) | Persistent conversation context |
| [Coding Agent](docs/cookbook/coding.md) | Code generation with specialized instructions |
| [Code Review](docs/cookbook/review.md) | Automated PR review |
| [GitHub Agent](docs/cookbook/github.md) | Issue and PR automation |

## Project Structure

```
├── sdk/           # Unified SDK (package sdk)
├── cmd/ares/      # CLI entry point (evolution status/run)
├── examples/      # 24+ runnable examples
│   └── runtime_evolution/  # Evolution demos (basic / knowledge / full)
├── docs/          # Documentation and articles
├── api/           # Public API interfaces
└── internal/
    ├── evolution/         # Runtime evolution system
    │   ├── genome/        # 5 Genome implementations (Workflow/Scheduler/Knowledge/Recovery/Prompt)
    │   ├── diff/          # Diff Engine (4 Differ implementations)
    │   ├── coordinator/   # Evolution Coordinator (7 PatchSources, PolicyGenome)
    │   ├── patch/         # RuntimePatch type + Registry + Apply/ApplySet
    │   └── llm_adapter.go # LLM participant adapter
    ├── ares_evolution/    # Strategy-level GA (population, NSGA-II, crossover, mutation, experience)
    ├── evidence/          # Evidence data primitive + MemoryStore
    ├── workflow/
    │   ├── graph/         # GraphPatchExecutor (7 patch types)
    │   └── engine/        # RecoveryPatchExecutor
    ├── knowledge/
    │   └── runtime/       # KnowledgePatchExecutor
    └── ares_bootstrap/    # Assembly wiring (ProvideNewEvolution)
```

## Runtime Evolution

ARES's runtime evolution system is **evidence-driven**: every execution, fault, and insight produces `Evidence`, which feeds into the evolution cycle. The system evolves DAG topology, scheduler selection, knowledge planner parameters, and recovery strategies — all in production, without restarts.

### Architecture

```
Execution → Evidence → Genome → Candidate → Diff Engine → RuntimePatch → Coordinator → Apply
```

| Component | Role | Sources |
|-----------|------|---------|
| **5 Genomes** | Generate candidate configurations via mutation + crossover | workflow, scheduler, knowledge, recovery, prompt |
| **4 Differs** | Compare old vs new snapshots → produce RuntimePatches | workflow, knowledge, scheduler, recovery |
| **Coordinator** | Decides Apply/Reject/Delay for each PatchProposal | GA, Chaos, AKF, LLM, Human, K8s, Rule |
| **3 Executors** | Apply patches to live runtime | Graph, Knowledge, Recovery |
| **LLM Adapter** | Converts natural-language suggestions into PatchProposals | parsed format → Coordinator |

**Key design**: LLM is a **participant**, not the leader. The Coordinator treats all 7 `PatchSource` values equally. No source has privileged access.

### Benchmarks (Apple M3 Max)

```
BenchmarkWorkflowGenome_Mutate     309k   7.1µs  11.4KB  155 allocs
BenchmarkSchedulerGenome_Mutate    3.3M   0.4µs   719B    15 allocs
BenchmarkKnowledgeGenome_Mutate    2.8M   0.4µs   960B    11 allocs
BenchmarkRecoveryGenome_Mutate     2.2M   0.5µs  1.1KB    21 allocs
BenchmarkDiffEngine_Workflow       2.9M   0.4µs   256B     3 allocs
BenchmarkCoordinator_Evaluate      217M   5.4ns     0B      0 allocs
BenchmarkFullEvolutionCycle        206k   5.3µs  8.0KB   109 allocs
```

### CLI

```bash
ares evolution status   # Show genomes, differs, coordinator state
ares evolution run      # Run one evolution cycle
```

### Examples

```bash
go run examples/runtime_evolution/basic/      # Full end-to-end evolution demo
go run examples/runtime_evolution/knowledge/  # Knowledge parameter evolution
go run examples/runtime_evolution/full/       # All 4 genomes + real executors
```

## Strategy Evolution (GA)

Beyond runtime-level evolution, ARES includes a **strategy-level Genetic Algorithm** that optimizes agent inference parameters (temperature, top_k, prompt templates, tool configs) through population-based search. The system evolves a population of strategies across generations using selection, crossover, and mutation, with zero-cost background evolution cycles.

### Key Features

| Feature | Description |
|---|---|
| **NSGA-II Multi-Objective** | 4 default dimensions (success_rate 0.40, quality 0.25, cost 0.20, latency 0.15) with direction-aware Pareto dominance |
| **Steady-State GA** | Configurable replace rate (0.1–0.5, default 0.3) — replaces only the worst individuals each generation |
| **Score / SelectionScore** | Canonical score preserved; selection score adjusted by fitness sharing for diversity |
| **Fitness Sharing** | 3 strategies — full O(n²), reservoir sampling, spatial grid index (for >500 individuals) |
| **3 Crossover Types** | Uniform (per-gene), Two-Point (swap segment), Segment (contiguous block) |
| **6 Mutation Types** | Parameter, Prompt, Tool, Swap, Inversion, Scramble |
| **Evolution Callbacks** | OnGeneration / OnFitness / OnMutation / OnCrossover |
| **Termination** | MaxGenerations + TargetFitness (stops when BestEverScore ≥ target) |
| **Generation History** | Per-generation snapshots with metadata |
| **Experience System** | 3-tier pipeline: ToolCallRecord → RawExperience → NormalizedExperience → EvolutionHint → GuidanceProvider |

### Benchmarks (Apple M3 Max)

```
BenchmarkPopulation_Init-10           100   11.7ms    2.5MB   32 allocs
BenchmarkPopulation_Select-10         300    4.1ms    1.1MB   12 allocs
BenchmarkPopulation_Mutate-10         500    2.5ms    708KB   10 allocs
BenchmarkDreamCycle_FullCycle-10       50   24.3ms    5.8MB   55 allocs
BenchmarkNondominatedSort-10         1000    1.8ms    256KB    8 allocs
```

### Examples

```bash
go run examples/10-ga-full-evolution/main.go   # Full GA evolution demo
go run examples/05-evolution-demo/main.go       # Pre-NSGA-II evolution demo
```

## License

Apache 2.0

## Acknowledgments

ARES's genetic algorithm implementation was inspired by the design and features of **[PyGAD](https://github.com/ahmedfgad/GeneticAlgorithmPython)** — the Python genetic algorithm library by [Ahmed F. Gad](https://github.com/ahmedfgad). PyGAD's architecture, operator design, and multi-objective optimization capabilities served as a valuable reference for building the GA engine in this project.

We recommend PyGAD for anyone looking for a mature, well-documented GA library in Python:
- GitHub: [github.com/ahmedfgad/GeneticAlgorithmPython](https://github.com/ahmedfgad/GeneticAlgorithmPython)
- Documentation: [pygad.readthedocs.io](https://pygad.readthedocs.io/)

Additional GA concepts and terminology follow the standard definitions from the [Genetic Algorithm](https://en.wikipedia.org/wiki/Genetic_algorithm) article on Wikipedia.
