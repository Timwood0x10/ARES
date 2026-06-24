# ares for Quant — 量化交易开发指南

> 本文档说明如何在 ares 框架上构建 TradingAgents 级别的量化多 Agent 系统。
> 覆盖架构设计、目录结构、ares 接口使用、代码量预估。

---

## 一、架构总览

```
┌────────────────────────────────────────────────────────────┐
│                   ares api.StartService()               │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              Portfolio Manager (Leader Agent)         │  │
│  │  - 接收 ticker + date 输入                            │  │
│  │  - 编排分析师管线                                     │  │
│  │  - 交易最终批准                                       │  │
│  └────────────┬──────────────────────────┬──────────────┘  │
│               │                          │                  │
│     ┌─────────▼─────────┐      ┌─────────▼─────────┐      │
│     │   Analyst Team     │      │   Researcher Team  │      │
│     │   (并行执行)       │      │   (辩论循环)       │      │
│     ├────────────────────┤      ├───────────────────┤      │
│     │ ① Fundamentals    │      │ ⑤ Bull Researcher  │      │
│     │ ② Sentiment       │ ──►  │ ⑥ Bear Researcher  │      │
│     │ ③ News            │      │  ↕ N 轮辩论        │      │
│     │ ④ Technical       │      └───────────────────┘      │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ ⑦ Trader Agent     │                                 │
│     │  (综合出决策)       │                                 │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ ⑧ Risk Manager     │                                 │
│     │  (风控检查)         │                                 │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ Portfolio Manager  │──► 输出: {signal, qty, price}   │
│     │ (最终批准)          │                                 │
│     └────────────────────┘                                 │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │               MCP Tool 层                             │   │
│  │  financial_data │ news_sentiment │ technical_ind     │   │
│  │  polymarket     │ market_data    │ calculator        │   │
│  └──────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐   │
│  │           基础设施层 (ares 原生)                    │   │
│  │  EventStore │ Arena │ Flight Recorder │ Memory        │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 8 个 Agent 角色映射

| # | 角色 | 映射为 | 数据源 |
|---|------|--------|--------|
| ① | Fundamentals Analyst | Sub Agent | Yahoo Finance 财务数据 |
| ② | Sentiment Analyst | Sub Agent | Polymarket + NewsAPI |
| ③ | News Analyst | Sub Agent | NewsAPI / RSS |
| ④ | Technical Analyst | Sub Agent | 技术指标库 (MACD/RSI) |
| ⑤ | Bull Researcher | Sub Agent | Analyst 报告做多论点 |
| ⑥ | Bear Researcher | Sub Agent | Analyst 报告做空论点 |
| ⑦ | Trader | Sub Agent | Researcher 辩论结果 |
| ⑧ | Risk Manager | Sub Agent | 组合集中度/波动率 |
| — | Portfolio Manager | **Leader Agent** | 全部汇总 + 最终批准 |

---

## 二、目录结构

```
internal/quant/                          ← 纯 Go 基础设施层 (~1,850行)
├── market/                              ← 行情数据接入 (~850行)
│   ├── types.go                        100行  OHLCV, Candle, Quote, TimeSeries
│   ├── yahoo.go                        200行  Yahoo Finance HTTP 客户端
│   ├── polymarket.go                   250行  Polymarket GraphQL 客户端
│   ├── csv.go                          150行  CSV 行情回放
│   └── feed.go                         150行  Subscribe/Poll 统一接口
│
├── indicators/                          ← 技术指标计算 (~350行)
│   ├── macd.go                          80行  MACD 线/信号/柱状图
│   ├── rsi.go                           60行  相对强弱指标
│   ├── sma.go                           50行  简单移动平均
│   ├── bollinger.go                     60行  布林带
│   └── registry.go                     100行  MCP Tool 注册
│
├── store/                               ← 持久化层 (~500行)
│   ├── interface.go                    100行  Store 接口定义
│   ├── sqlite.go                       300行  SQLite 实现
│   └── models.go                       100行  quant_decisions + signals 表结构
│
├── sentiment/                           ← 情绪聚合层 (~150行)
│   └── polymarket.go                   150行  预测市场 → 情绪信号
│
└── memory/                              ← 决策记忆层 (可选, 也可走 ares 原生)
    └── history.go                      200行  跨股票决策记忆注入
                                        200行

examples/quant-trading/                  ← Demo 应用层 (~1,710行)
├── agents/                              ← Agent Prompt 工程 (~900行)
│   ├── prompts.go                      350行  8 个 Agent Prompt (中英双语)
│   ├── analyst.go                      200行  创建 4 个 Analyst Agent
│   ├── researcher.go                   100行  创建 Bull/Bear Agent
│   ├── trader.go                       100行  创建 Trader/Risk/PM Agent
│   └── config.go                       150行  Agent 参数配置
│
├── workflow/                            ← 工作流编排 (~400行)
│   ├── pipeline.go                     200行  DAG 管线构建
│   └── debate.go                       200行  Bull/Bear 辩论循环
│
├── memory/                              ← 记忆工程 (~200行)
│   └── history.go                      200行  跨股票学习 + 事后反思
│
├── chaos/                               ← Arena 混沌场景
│   ├── analyst_crash.yaml                30行  分析途中杀 Agent
│   └── pm_failover.yaml                  30行  PM 挂掉自动选举
│
├── main.go                             300行  主入口
├── config.yaml                          30行
├── run.sh                               80行
└── README.md                           200行
```

### 总计

| 层 | 目录 | 预估行数 | 说明 |
|----|------|---------|------|
| 基础设施 | `internal/quant/` | ~1,850 | 纯 Go，无 LLM 依赖 |
| Demo 应用 | `examples/quant-trading/` | ~1,710 | 含双语 prompt |
| 混沌场景 | `examples/quant-trading/chaos/` | ~60 | YAML 配置 |
| 项目配置 | 根目录配置文件 | ~310 | config.yaml + run.sh + README |
| **合计** | | **~3,560** | **核心逻辑 ~2,300 行** |

---

## 三、ares 接口使用清单

### 3.1 框架启动

```go
// api.StartService 是唯一入口
cfg, _ := api.LoadServiceConfig("examples/quant-trading/config.yaml")
svc, _ := api.StartService(ctx, cfg)
orch := svc.Orchestrator()  // 获取 Agent 管理器
```

### 3.2 Agent 创建

```go
// 每个分析师是一个 Sub Agent
req := dashboard.AgentRequest{
    Name:      "Fundamentals Analyst",
    Target:    "Analyze fundamentals for " + ticker,
    LLMPrompt: fundamentalsPrompt,          // 见 prompts.go
    MCPTool:   "financial_data",            // 绑定数据工具
    MCPArgs:   map[string]any{"ticker": ticker},
}
id, _ := orch.CreateAgent(req)
```

### 3.3 MCP Tool 注册

```go
// 技术指标计算工具
registry.Register(tool.Tool{
    Name:        "technical_indicators",
    Description: "Compute MACD, RSI, SMA for given ticker",
    Execute:     computeIndicators,
    Parameters:  paramSchema,
})

// Polymarket 情绪工具
registry.Register(tool.Tool{
    Name:        "polymarket_sentiment",
    Description: "Fetch prediction market probability for an event",
    Execute:     fetchPolymarketSentiment,
})
```

### 3.4 DAG 工作流编排

```go
// 使用 Graph System 构建管线
g := graph.NewGraph("quant-pipeline")
g.AddNode(graph.Node{ID: "analysts", Agents: analystAgents})
g.AddNode(graph.Node{ID: "researchers", Agent: debateAgent})
g.AddNode(graph.Node{ID: "trader", Agent: traderAgent})
g.AddNode(graph.Node{ID: "risk", Agent: riskAgent})
g.AddNode(graph.Node{ID: "pm", Agent: pmAgent})

g.AddEdge("analysts", "researchers")
g.AddEdge("researchers", "trader")
g.AddEdge("trader", "risk")
g.AddEdge("risk", "pm")
```

### 3.5 EventStore 审计日志

```go
eventStore.Append(ctx, "quant:AAPL:2026-06-15", []*events.Event{
    {Type: "quant.decision", Payload: map[string]any{
        "signal": "buy", "price": 245.0, "reasoning": "...",
    }},
}, -1)
```

### 3.6 Memory Distillation 跨股票记忆

```go
summaries, _ := compactableStore.GetSummariesForStream(ctx, "quant:AAPL:*")
pmPrompt += "\nHistorical decisions:\n"
for _, s := range summaries {
    pmPrompt += "- " + s.SummaryText + "\n"
}
```

### 3.7 Arena 混沌测试

```bash
# 场景：分析过程中杀掉 Sentiment Analyst
ares arena run examples/quant-trading/chaos/analyst_crash.yaml

# 场景：杀 PM，测试自动选举
ares arena run examples/quant-trading/chaos/pm_failover.yaml
```

### 接口汇总

| ares 接口 | 包路径 | 用途 |
|--------------|--------|------|
| `api.StartService()` | `ares/api` | 启动完整运行时 |
| `api.LoadServiceConfig()` | `ares/api` | 加载 YAML 配置 |
| `orch.CreateAgent()` | `ares/internal/dashboard` | 创建子 Agent |
| `orch.ListAgents()` | `ares/internal/dashboard` | 获取 Agent 列表 |
| `orch.CancelAgent()` | `ares/internal/dashboard` | 杀死 Agent |
| `graph.NewGraph()` | `ares/internal/workflow/graph` | 构建 DAG |
| `graph.AddNode()` | `ares/internal/workflow/graph` | 添加工作流节点 |
| `graph.AddEdge()` | `ares/internal/workflow/graph` | 添加依赖边 |
| `graph.Execute()` | `ares/internal/workflow/graph` | 执行工作流 |
| `events.EventStore.Append()` | `ares/internal/events` | 写入事件 |
| `events.EventStore.Read()` | `ares/internal/events` | 读取事件 |
| `CompactableEventStore.GetSummariesForStream()` | `ares/internal/events` | 获取历史摘要 |
| `arena.RunScenario()` | `ares/internal/arena` | 运行混沌场景 |
| `tools/core.Registry.Register()` | `ares/internal/tools/resources/core` | 注册 MCP 工具 |
| `dashboard.AgentRequest` | `ares/internal/dashboard` | Agent 参数结构体 |

---

## 四、数据存储设计

### DB 选型

| 阶段 | 数据库 | 原因 |
|------|--------|------|
| 开发/个人回测 | **SQLite** | 零部署、文件级、够用 |
| 生产/团队 | **PostgreSQL + pgvector** | 并发、向量搜索、可扩展 |

### 核心表

```sql
-- quant_decisions: 交易决策记录 (替代 trading_memory.md)
CREATE TABLE IF NOT EXISTS quant_decisions (
    id              TEXT PRIMARY KEY,
    ticker          TEXT NOT NULL,
    decision_date   TEXT NOT NULL,      -- "2026-06-15"
    signal          TEXT NOT NULL,      -- "buy" / "sell" / "hold"
    confidence      REAL,               -- 0.0 ~ 1.0
    quantity        INTEGER,
    price           REAL,
    reasoning       TEXT,               -- 完整推理过程
    analyst_reports TEXT,               -- JSON: 各分析师报告摘要
    debate_rounds   INTEGER,
    realized_return REAL,               -- 实际收益（事后更新）
    alpha_vs_spy    REAL,               -- 超额收益
    reflection      TEXT,               -- 事后反思
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(ticker, decision_date)
);

-- market_data: 行情数据缓存
CREATE TABLE IF NOT EXISTS market_data (
    ticker      TEXT NOT NULL,
    date        TEXT NOT NULL,
    open        REAL,
    high        REAL,
    low         REAL,
    close       REAL,
    volume      INTEGER,
    source      TEXT DEFAULT 'yahoo',
    PRIMARY KEY (ticker, date)
);

-- signals: 技术指标信号缓存 (避免重复计算)
CREATE TABLE IF NOT EXISTS signals (
    ticker      TEXT NOT NULL,
    date        TEXT NOT NULL,
    indicator   TEXT NOT NULL,          -- "MACD" / "RSI" / "SMA_20"
    value       REAL,
    metadata    TEXT,                   -- JSON 扩展字段
    PRIMARY KEY (ticker, date, indicator)
);
```

---

## 五、Polymarket 集成说明

### 接入方式

```go
// internal/quant/market/polymarket.go

// FetchMarket 查询预测市场的当前价格
func FetchMarket(ctx context.Context, question string) (*Market, error) {
    // Polymarket Gamma API: https://gamma-api.polymarket.com
    // GET /markets?tag=<tag>&limit=1
    // 返回当前 YES/NO token 价格
}

// SentimentSignal 将预测市场概率转化为 Sentiment 信号
// 例如: "Will AAPL close above $250 on 2026-07-01?"
// YES 价格 0.65 → 市场认为 65% 概率 → Sentiment 正向
func SentimentSignal(market *Market) float64 {
    return market.YesPrice  // 0.0 ~ 1.0
}
```

### 在 Sentiment Analyst Prompt 中使用

```
You are a Sentiment Analyst. Consider the following data:

Polymarket Prediction Markets:
- "Will AAPL close above $250 next month?" → YES at $0.65
- "Will Fed cut rates in June?" → YES at $0.72

Interpret these probabilities and incorporate them into your
overall sentiment assessment for AAPL.
```

---

## 六、代码量预估汇总

| 模块 | 文件 | 行数 | 复杂度 |
|------|------|------|--------|
| market/types.go | 类型定义 | 100 | ⭐ |
| market/yahoo.go | Yahoo 行情 | 200 | ⭐⭐ |
| market/polymarket.go | Polymarket | 250 | ⭐⭐ |
| market/csv.go | CSV 回放 | 150 | ⭐ |
| market/feed.go | 统一接口 | 150 | ⭐⭐ |
| indicators/* | 4 个指标 | 250 | ⭐⭐⭐ |
| indicators/registry.go | Tool 注册 | 100 | ⭐ |
| store/interface.go | 接口 | 100 | ⭐ |
| store/sqlite.go | SQLite | 300 | ⭐⭐ |
| store/models.go | 表结构 | 100 | ⭐ |
| sentiment/polymarket.go | 情绪聚合 | 150 | ⭐⭐ |
| **internal/quant 小计** | | **~1,850** | |
| agents/prompts.go | 8 个 Prompt | 350 | ⭐⭐⭐ |
| agents/*.go | Agent 创建 | 550 | ⭐⭐ |
| workflow/pipeline.go | DAG | 200 | ⭐⭐⭐ |
| workflow/debate.go | 辩论循环 | 200 | ⭐⭐⭐ |
| memory/history.go | 跨股票记忆 | 200 | ⭐⭐ |
| main.go | 入口 | 300 | ⭐⭐ |
| run.sh + config.yaml | 配置 | 110 | ⭐ |
| README.md | 文档 | 200 | ⭐ |
| **examples/quant-trading 小计** | | **~1,710** | |
| **总计** | | **~3,560** | **核心 ~2,300** |

---

## 七、开发路线图

```
Week 1: 数据层 (market + indicators + store)         ← 850 + 350 + 500 = 1,700行
Week 2: Agent 层 (prompts + 8 agents)                 ← 900行
Week 3: 编排层 (workflow + memory + main)             ← 700行
Week 4: 打磨 (chaos + docs + run.sh)                  ← 310行
```

---

*本文档对应实施计划详见 `plan/quan/quant-implementation-plan.md`*
