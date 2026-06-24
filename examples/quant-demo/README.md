# Quant Multi-Agent Demo

A self-healing quantitative multi-agent system built on ares. Demonstrates multi-factor stock selection with automatic fault recovery.

## Architecture

```
Portfolio Manager (Leader)
  ├── Momentum Researcher  ← Computes momentum factor (3m+6m returns)
  ├── Value Researcher     ← Computes value factor (P/E + P/B ratios)
  └── Risk Monitor         ← Checks concentration & portfolio risk
```

Each researcher is a dashboard-visible agent. The Arena chaos engineering layer continuously kills and resurrects agents to prove the system is self-healing.

## What It Demonstrates

| Capability | How |
|---|---|
| **Multi-Factor Stock Selection** | Momentum (40% 3m + 60% 6m) + Value (inverted P/E & P/B) → composite z-score → ranked portfolio |
| **Self-Healing Runtime** | Arena kills agents mid-analysis → auto-resurrection with context preserved |
| **Portfolio Construction** | Factor-weighted allocation, 25% position cap, rebalanced to sum 1.0 |
| **Risk Monitoring** | Herfindahl concentration, top-heavy detection, weighted risk score |
| **Real-Time Dashboard** | DAG visualization, event timeline, Arena control panel, resilience score |

## Quick Start

### Prerequisites

- Go 1.22+
- [Ollama](https://ollama.ai/) with `llama3.2` (or any OpenAI-compatible LLM)

### Run

```bash
# Start Ollama (if not running)
ollama serve

# Pull the model
ollama pull llama3.2

# Start the quant demo
cd examples/quant-demo
./run.sh

# Or manually:
go run . -config ./examples/quant-demo/config.yaml
```

### What to Watch

1. Open http://localhost:8091 → Arena tab
2. Watch the DAG populate with 4 quant agents
3. Factor scores appear in the terminal log
4. Click ☠Leader to assassinate an agent
5. Watch the Event Log: `Agent Failed → Resurrection Started → Agent Resurrected`
6. Final report shows allocation + risk score + resilience metrics

## Pipeline Flow

```
loadUniverse("universe.csv") → 10 stocks
  ↓
computeMomentum()      → z-score ranked momentum factor
  ├── Rank 1: AMZN (+1.42σ)  ← strongest momentum
  └── Rank 10: TSLA (-1.65σ) ← weakest momentum
  ↓
computeValue()         → z-score ranked value factor  
  ├── Rank 1: JPM (+1.38σ)   ← best value
  └── Rank 10: TSLA (-1.92σ) ← worst value
  ↓
allocate(momentum + value) → composite → weighted portfolio (max 25%/position)
  ↓
computeRisk()          → concentration check → risk score
  ↓
printReport()          → full system report + self-healing stats
```

The factor computation runs in pure Go (no LLM). Agents run LLM analysis for narrative interpretation. Arena chaos runs simultaneously in the background.

## Extending

To add a new factor, implement a function following the `computeMomentum` pattern:

```go
func computeSize(stocks []Stock) []FactorScore {
    scores := make([]FactorScore, len(stocks))
    for i, s := range stocks {
        scores[i] = FactorScore{Ticker: s.Ticker, Value: -math.Log(s.MarketCapB)}
    }
    normalizeScores(scores)
    return scores
}
```

Then add it to `quantDemo()` and create a corresponding agent via `createQuantAgents()`.

## Config

See `config.yaml`:
- LLM provider, model, endpoint
- Dashboard port (default 8091)
- No MCP servers required for basic demo

## Files

```
examples/quant-demo/
├── main.go              # Demo entry point + quant pipeline
├── data/universe.csv    # 10 stocks with fundamental data
├── config.yaml          # Service configuration
├── run.sh               # One-click start script
├── README.md            # This file (English)
└── README_CN.md         # 中文文档
```
