# GoAgentX Quantitative Trading Demo

A self-healing multi-agent trading system supporting **two execution modes**:

1. **Legacy Mode (default)**: 8 agents via Orchestrator, YAML-driven pipeline.
2. **Research Layer Mode**: Structured 12-node research graph with typed schemas,
   data validation, anti-hallucination guards, and markdown reporting.

## Two Execution Modes

```
┌─────────────────────────────────────────────────────────────┐
│                      main.go entry                          │
├──────────────────────────┬──────────────────────────────────┤
│  Legacy Mode (default)   │  Research Layer Mode             │
│  --use-research-layer    │  --use-research-layer=true       │
│                          │                                  │
│  StartService            │  ResearchConfig                  │
│  RegisterTools           │  VendorRouter + Yahoo vendor     │
│  Load agents.yaml        │  Validator (stale guard)         │
│  Orchestrator(8 agents)  │  SnapshotBuilder                 │
│  YAML pipeline           │  ResearchGraph (12 nodes)        │
│  JSON output             │  Mock/Real LLM execution         │
│                          │  Markdown report + JSON output   │
└──────────────────────────┴──────────────────────────────────┘
```

## Quick Start

### Mode 1: Legacy (default)

```bash
cd examples/quant-trading
./run.sh
```

Open http://localhost:8092 → Arena tab to see agents and kill them.

### Mode 2: Research Layer

```bash
cd examples/quant-trading
go run . -ticker AAPL --use-research-layer
```

### Mode 3: Offline Demo (no LLM required)

```bash
cd examples/quant-trading
go run -tags=researchdemo research_demo.go -ticker AAPL
go run -tags=researchdemo research_demo.go -ticker TSLA -output ./demo_output.json
```

## `--use-research-layer` Flag

| Flag | Default | Description |
|------|---------|-------------|
| `--use-research-layer` | `false` | Enable new research layer (12-node structured graph) |

When enabled, the program:

1. Creates `ResearchConfig` from defaults or config.yaml `research` section.
2. Sets up data flow: `VendorRouter` (Yahoo), `Validator` (stale guard), `SnapshotBuilder`.
3. Builds and executes a 12-node `ResearchGraph`.
4. Outputs structured results: `ResearchPlan` → `TraderProposal` → `PortfolioDecision`.
5. Renders a markdown report to console.
6. Saves full state as JSON.

## Architecture (Research Layer)

```
Research Graph (12 Nodes)
├── Phase 1: Analysts (parallel-ready)
│   ├── Market Analyst      ← VerifiedMarketSnapshot
│   ├── Sentiment Analyst   ← sentiment signals
│   ├── News Analyst        ← news headlines
│   └── Fundamentals Analyst← financial metrics
├── Phase 2: Debate
│   ├── Bull Researcher     ← analyst reports
│   └── Bear Researcher     ← analyst reports
├── Phase 3: Convergence
│   └── Research Manager    ← bull/bear arguments
├── Phase 4: Trading
│   └── Trader              ← ResearchPlan
├── Phase 5: Risk Assessment
│   ├── Aggressive Risk     ← TraderProposal
│   ├── Conservative Risk   ← TraderProposal
│   └── Neutral Risk        ← TraderProposal
└── Phase 6: Final Decision
    └── Portfolio Manager   ← risk views + memory context
```

## Research Layer Advantages

- **Structured Output**: Typed schemas (`ResearchPlan`, `TraderProposal`, `PortfolioDecision`)
  prevent free-form LLM hallucinations.
- **Anti-Hallucination**: Data flow layer validates market data freshness, normalizes symbols,
  and rejects stale/lookahead data.
- **Memory Loop**: `MemoryLog` stores decisions for post-trade reflection; past context is
  injected into subsequent PM prompts.
- **Checkpoint Resume**: Research state can be serialized/deserialized for interrupted runs.
- **Markdown Reporting**: Consistent render functions produce stable, parseable output.

## Configuration

```bash
# first time: copy example config
cp config.example.yml config.yml
# edit config.yml to set your API key and choose LLM provider
# 
```

## Switch Model

Edit `config.yml`, uncomment the corresponding LLM config block:

```yaml
# Ollama (local, default)
llm:
  provider: "ollama"
  model: "llama3.2"
```

## Config: Research Layer

The `research` section in `config.yml` controls the new mode:

```yaml
research:
  enabled: true
  selected_analysts: [market, sentiment, news, fundamentals]
  max_debate_rounds: 2
  max_risk_rounds: 1
  quick_model: "gpt-4o-mini"    # For analyst nodes
  deep_model: "gpt-4o"          # For manager/trader nodes
  output_language: "english"
  data_vendors: [yahoo]
  checkpoint_enabled: true
  memory_enabled: true

dataflow:
  max_stale_duration: "120h"
  holiday_grace_period: "72h"
```

## Requirements

- Go 1.22+
- [Ollama](https://ollama.ai/) with default model (for legacy mode)

## Files

```
├── main.go              Entry point (dual-mode: legacy / research layer)
├── research_demo.go     Standalone offline demo (build tag: researchdemo)
├── agents/
│   ├── prompts.go       8 bilingual prompts (legacy)
│   └── agents.go        Agent factory and YAML parser (legacy)
├── workflow/            DAG pipeline (legacy)
├── memory/              Cross-stock learning (legacy)
├── chaos/               Arena YAML scenarios (legacy)
├── config.example.yml   Service config template (复制为 config.yml 使用)
├── config/
│   ├── agents.yaml      Agent definitions (legacy)
│   └── workflow.yaml    DAG workflow (legacy)
└── run.sh               One-click runner (legacy)
```
