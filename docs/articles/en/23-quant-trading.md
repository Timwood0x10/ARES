# ares Architecture Deep Dive (XXIII): Quant Trading Module — The Experiment We Keep Honest About

Every project has that one module. The one that started as "just a quick experiment" and grew into 9,768 lines of code. For ares, that module is `internal/ares_quant/` — the quantitative trading system.

This article is different from the others. It's not "look at this great architecture." It's "here's what we built, why we built it, and why it probably shouldn't be here."

---

## The Origin Story

The quant module started in v0.2.4 as a side experiment: "Can we use the agent framework to do quantitative trading research?" The answer was yes — and that's the problem.

Three sub-systems grew in parallel:

| Sub-system | Purpose | Size |
|------------|---------|------|
| `market/` | Data sources (Yahoo, CoinGecko, Polymarket) | 582 lines |
| `marketmaking/` | Quote engine, inventory, chaos testing | 1,328 lines |
| `portfolio/` | Position tracking, risk metrics | 1,242 lines |
| `research/` | Backtesting, evaluation, research agents | 3,562 lines |
| `indicators/` | Technical indicators (RSI, MACD, etc.) | 171 lines |
| `dataflow/` | Event streaming, pipeline orchestration | 585 lines |
| `store/` | Persistence | 382 lines |
| `marketmaking_api/` | HTTP API for market making | 1,569 lines |

Total: **9,768 lines**, or about 11% of the entire ares codebase.

---

## What It Does

### Market Data (`market/`)

Three data source adapters:

```go
// internal/ares_quant/market/yahoo.go
type YahooSource struct {
    client *http.Client
}

func (y *YahooSource) Fetch(ctx context.Context, symbol string, range_ string) (*MarketData, error)
```

- **Yahoo Finance** — historical OHLCV data
- **CoinGecko** — cryptocurrency prices
- **Polymarket** — prediction market odds

Each implements a common `DataSource` interface.

### Market Making (`marketmaking/`)

A complete market making engine:

```go
// internal/ares_quant/marketmaking/
type QuoteEngine struct {
    config      QuoteEngineConfig
    inventory   *Inventory
    riskLimit   float64
    // ...
}

type Inventory struct {
    cash        float64
    position    float64
    // ...
}
```

The engine:
1. Receives `MarketDataEvent`s
2. Computes bid/ask quotes based on inventory and risk
3. Adjusts spread based on volatility
4. Includes a `ChaosExecutor` for fault injection testing

**Honest reflection**: The market making engine is genuinely useful — it tests the agent framework under high-frequency, stateful conditions. But it's also 1,328 lines of domain-specific code that 99% of ares users will never touch.

### Portfolio (`portfolio/`)

Position tracking and risk metrics:

```go
// internal/ares_quant/portfolio/
type Portfolio struct {
    positions map[string]*Position
    cash      float64
}

func (p *Portfolio) SharpeRatio() float64
func (p *Portfolio) MaxDrawdown() float64
func (p *Portfolio) VaR(confidence float64) float64
```

Standard quant finance metrics. The implementation is correct but unremarkable.

### Research (`research/`)

The largest sub-package (3,562 lines):

```
research/
├── agents/           # Research agents (base, interface, mock)
├── evaluation.go     # Evaluator for historical decisions
├── backtest.go       # Backtesting framework
└── ...
```

Research agents use the ares LLM client to analyze market data and produce trading recommendations. The `Evaluator` scores these recommendations against historical outcomes.

**Honest reflection**: The research module is where "experiment" most clearly shows. The agents are powerful but the evaluation is basic — we compare predicted ratings against future returns, but we don't account for market regimes, transaction costs, or selection bias. This is prototype-quality research tooling.

---

## The MCP Integration

The quant module exposes its tools via MCP (Model Context Protocol):

```go
// internal/ares_quant/tools.go
// Data sources (Yahoo Finance, Polymarket) and computations (technical indicators)
// are wrapped as MCP Tool instances registered in the global tool Registry.
```

This means an ares agent can call quant tools the same way it calls any other tool:

```go
req := dashboard.AgentRequest{
    MCPTool: "financial_data",
    MCPArgs: map[string]any{"ticker": "AAPL"},
}
```

The agent doesn't know it's calling a quant tool — it just sees an MCP tool with a schema and a handler.

**Honest reflection**: The MCP integration is the one part of the quant module that's genuinely well-architected. By exposing quant tools through MCP, we get:
- Schema validation for free
- Tool discovery for free
- Composability with non-quant tools for free

If we ever extract the quant module into a separate repo, the MCP interface is the clean boundary.

---

## The Honest Assessment

In `01-architecture-overview-deep-dive.md`, I wrote:

> **坦诚反思**：代码库比需要的大。量化交易模块、面试 demo、MCP dashboard——这些是实验，应该放在独立仓库。核心（Runtime + Workflow + Memory + Events）是扎实的。外围还在找自己的形状。

This is still true in v0.2.8. The quant module is:

1. **Useful for testing** — high-frequency, stateful, error-prone operations stress the agent framework
2. **Useful for demos** — "watch an agent trade" is compelling
3. **Not useful for most users** — 99% of ares users don't need a market making engine
4. **A maintenance burden** — 9,768 lines that need to be kept up to date with Go versions, dependencies, and ares API changes

### The Decision We Keep Deferring

v0.2.5: "We should extract this."
v0.2.6: "After the SDK refactor, we'll extract this."
v0.2.7: "We'll extract this in the next release."
v0.2.8: "We'll extract this in the next release."

**The honest truth**: We keep not extracting it because:
- The MCP integration makes it genuinely useful for agent testing
- Extracting it means breaking the MCP tool registration
- It's not hurting anything sitting where it is

But it's wrong that a 9,768-line trading module lives in a general agent framework. It should be a separate `ares-quant` repo that depends on `ares`, not a sub-package of `ares` itself.

---

## Lessons

The quant module teaches three lessons:

1. **Experiments should be labeled as experiments.** The quant module grew because we treated it as production code. If we'd labeled it `experimental/` from day one, we'd have been more ruthless about extracting or deleting it.

2. **Domain-specific code leaks.** A market making engine has different requirements than an agent framework (latency, statefulness, error recovery). Mixing them means compromising both.

3. **Honesty is a feature.** The architecture overview calls out the quant module as an experiment. This article does the same. Users deserve to know what's production-grade and what's not.

**The best codebase is the one that knows what it is and what it isn't.** ares is an agent framework. It's not a quant trading system. The quant module is useful, but it's in the wrong place.
