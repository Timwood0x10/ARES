# ares 架构拆解 (XXIII)：量化交易模块——我们坦诚面对的实验

每个项目都有那么一个模块。那个"只是快速实验一下"开始，最后长到 9,768 行代码的模块。对 ares 来说，那就是 `internal/ares_quant/`——量化交易系统。

这篇文章和其他文章不同。它不是"看这个伟大的架构"。它是"这是我们建的东西，为什么建的，以及为什么它可能不应该在这里"。

---

## 起源故事

量化模块在 v0.2.4 作为副业实验开始："我们能不能用 Agent 框架做量化交易研究？"答案是可以——这正是问题所在。

三个子系统并行生长：

| 子系统 | 用途 | 代码量 |
|--------|------|--------|
| `market/` | 数据源（Yahoo、CoinGecko、Polymarket） | 582 行 |
| `marketmaking/` | 报价引擎、库存、混沌测试 | 1,328 行 |
| `portfolio/` | 持仓追踪、风险指标 | 1,242 行 |
| `research/` | 回测、评估、研究 Agent | 3,562 行 |
| `indicators/` | 技术指标（RSI、MACD 等） | 171 行 |
| `dataflow/` | 事件流、pipeline 编排 | 585 行 |
| `store/` | 持久化 | 382 行 |
| `marketmaking_api/` | 做市 HTTP API | 1,569 行 |

总计：**9,768 行**，约占整个 ares 代码库的 11%。

---

## 它做什么

### 行情数据（`market/`）

三个数据源适配器：

```go
// internal/ares_quant/market/yahoo.go
type YahooSource struct {
    client *http.Client
}

func (y *YahooSource) Fetch(ctx context.Context, symbol string, range_ string) (*MarketData, error)
```

- **Yahoo Finance** —历史 OHLCV 数据
- **CoinGecko** —加密货币价格
- **Polymarket** —预测市场赔率

每个都实现统一的 `DataSource` 接口。

### 做市（`marketmaking/`）

完整的做市引擎：

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

引擎：
1. 接收 `MarketDataEvent`
2. 基于库存和风险计算买卖报价
3. 基于波动率调整价差
4. 包含 `ChaosExecutor` 用于故障注入测试

**坦诚反思**：做市引擎确实有用——它在高频、有状态条件下测试 Agent 框架。但它也是 1,328 行 99% 的 ares 用户永远不会碰的领域专属代码。

### 投资组合（`portfolio/`）

持仓追踪和风险指标：

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

标准的量化金融指标。实现正确但不出彩。

### 研究（`research/`）

最大的子包（3,562 行）：

```
research/
├── agents/           # 研究 Agent（base、interface、mock）
├── evaluation.go     # 历史决策评估器
├── backtest.go       # 回测框架
└── ...
```

研究 Agent 用 ares LLM 客户端分析行情数据并产出交易建议。`Evaluator` 按历史结果给这些建议打分。

**坦诚反思**：研究模块是"实验"最明显的地方。Agent 很强大，但评估很基础——我们对比预测评级和未来收益，但没考虑市场状态、交易成本或选择偏差。这是原型质量的研究工具。

---

## MCP 集成

量化模块通过 MCP（Model Context Protocol）暴露它的工具：

```go
// internal/ares_quant/tools.go
// 数据源（Yahoo Finance、Polymarket）和计算（技术指标）
// 被包装为注册在全局工具 Registry 里的 MCP Tool 实例。
```

这意味着 ares Agent 可以像调用其他工具一样调用量化工具：

```go
req := dashboard.AgentRequest{
    MCPTool: "financial_data",
    MCPArgs: map[string]any{"ticker": "AAPL"},
}
```

Agent 不知道它在调用量化工具——它只看到一个带 schema 和 handler 的 MCP 工具。

**坦诚反思**：MCP 集成是量化模块里唯一真正架构良好的部分。通过 MCP 暴露量化工具，我们免费得到：
- Schema 校验
- 工具发现
- 与非量化工具的组合性

如果我们把量化模块抽到独立仓库，MCP 接口就是干净的边界。

---

## 诚实的评估

在 `01-architecture-overview-deep-dive.md` 里，我写了：

> **坦诚反思**：代码库比需要的大。量化交易模块、面试 demo、MCP dashboard——这些是实验，应该放在独立仓库。核心（Runtime + Workflow + Memory + Events）是扎实的。外围还在找自己的形状。

这在 v0.2.8 仍然成立。量化模块：

1. **对测试有用** —高频、有状态、易出错操作给 Agent 框架施压
2. **对 demo 有用** —"看 Agent 交易"很吸引人
3. **对大多数用户没用** —99% 的 ares 用户不需要做市引擎
4. **维护负担** —9,768 行需要随 Go 版本、依赖和 ares API 变更保持更新

### 我们一直推迟的决定

v0.2.5："我们应该把这个抽出来。"
v0.2.6："SDK 重构之后，我们会抽出来。"
v0.2.7："我们下个版本抽出来。"
v0.2.8："我们下个版本抽出来。"

**诚实的真相**：我们一直不抽出来是因为：
- MCP 集成让它对 Agent 测试确实有用
- 抽出来意味着破坏 MCP 工具注册
- 它放在那里也没伤害谁

但一个 9,768 行的交易模块住在通用 Agent 框架里是错的。它应该是独立的 `ares-quant` 仓库，依赖 `ares`，而不是 `ares` 的子包。

---

## 教训

量化模块教了三个教训：

1. **实验应该被标为实验。** 量化模块之所以生长，是因为我们把它当生产代码对待。如果我们从第一天就标成 `experimental/`，我们会更无情地抽出来或删掉它。

2. **领域专属代码会泄漏。** 做市引擎和 Agent 框架有不同的需求（延迟、有状态性、错误恢复）。把它们混在一起意味着两者都要妥协。

3. **诚实是一个功能。** 架构总览把量化模块标为实验。这篇文章也这样做。用户值得知道什么是生产级的，什么不是。

**最好的代码库是知道自己是什​​么、不是什么的那个。** ares 是一个 Agent 框架。它不是一个量化交易系统。量化模块有用，但它待错了地方。
