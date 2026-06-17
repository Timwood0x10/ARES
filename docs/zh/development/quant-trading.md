# GoAgentX for Quant — 量化交易开发指南

> 本文档说明如何在 GoAgentX 框架上构建 TradingAgents 级别的量化多 Agent 系统。
> 覆盖架构设计、目录结构、GoAgentX 接口使用、代码量预估。

---

## 一、架构总览

```
┌────────────────────────────────────────────────────────────┐
│                   goagentx api.StartService()               │
│  ┌──────────────────────────────────────────────────────┐  │
│  │              Portfolio Manager (Leader Agent)         │  │
│  │  - 接收 ticker + date 输入                            │  │
│  │  - 编排分析师管线                                     │  │
│  │  - 交易最终批准                                       │  │
│  └────────────┬──────────────────────────┬──────────────┘  │
│               │                          │                  │
│     ┌─────────▼─────────┐      ┌─────────▼─────────┐      │
│     │   分析师团队        │      │   研究员团队       │      │
│     │   (并行执行)       │      │   (辩论循环)       │      │
│     ├────────────────────┤      ├───────────────────┤      │
│     │ ① 基本面分析师    │      │ ⑤ 看多研究员      │      │
│     │ ② 情绪分析师      │ ──►  │ ⑥ 看空研究员      │      │
│     │ ③ 新闻分析师      │      │  ↕ N 轮辩论        │      │
│     │ ④ 技术分析师      │      └───────────────────┘      │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ ⑦ 交易员 Agent     │                                 │
│     │  (综合出决策)       │                                 │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ ⑧ 风控经理         │                                 │
│     │  (风险检查)         │                                 │
│     └─────────┬──────────┘                                 │
│               │                                            │
│     ┌─────────▼──────────┐                                 │
│     │ Portfolio Manager  │──► 输出: {信号, 数量, 价格}     │
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
│  │           基础设施层 (GoAgentX 原生)                    │   │
│  │  EventStore │ Arena │ Flight Recorder │ Memory        │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

### 8 个 Agent 角色映射

| # | 角色 | 映射为 | 数据源 |
|---|------|--------|--------|
| ① | 基本面分析师 | Sub Agent | Yahoo Finance 财务数据 |
| ② | 情绪分析师 | Sub Agent | Polymarket + NewsAPI |
| ③ | 新闻分析师 | Sub Agent | NewsAPI / RSS |
| ④ | 技术分析师 | Sub Agent | 技术指标库 (MACD/RSI) |
| ⑤ | 看多研究员 | Sub Agent | Analyst 报告做多论点 |
| ⑥ | 看空研究员 | Sub Agent | Analyst 报告做空论点 |
| ⑦ | 交易员 | Sub Agent | Researcher 辩论结果 |
| ⑧ | 风控经理 | Sub Agent | 组合集中度/波动率 |
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
└── memory/                              ← 决策记忆层
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

## 三、GoAgentX 接口使用清单

| GoAgentX 接口 | 包路径 | 用途 |
|--------------|--------|------|
| `api.StartService()` | `goagentx/api` | 启动完整运行时 |
| `api.LoadServiceConfig()` | `goagentx/api` | 加载 YAML 配置 |
| `orch.CreateAgent()` | `goagentx/internal/dashboard` | 创建子 Agent |
| `orch.ListAgents()` | `goagentx/internal/dashboard` | 获取 Agent 列表 |
| `orch.CancelAgent()` | `goagentx/internal/dashboard` | 杀死 Agent |
| `graph.NewGraph()` | `goagentx/internal/workflow/graph` | 构建 DAG |
| `graph.AddNode()` | `goagentx/internal/workflow/graph` | 添加工作流节点 |
| `graph.AddEdge()` | `goagentx/internal/workflow/graph` | 添加依赖边 |
| `graph.Execute()` | `goagentx/internal/workflow/graph` | 执行工作流 |
| `events.EventStore.Append()` | `goagentx/internal/events` | 写入事件 |
| `events.EventStore.Read()` | `goagentx/internal/events` | 读取事件 |
| `CompactableEventStore.GetSummariesForStream()` | `goagentx/internal/events` | 获取历史摘要 |
| `arena.RunScenario()` | `goagentx/internal/arena` | 运行混沌场景 |
| `tools/core.Registry.Register()` | `goagentx/internal/tools/resources/core` | 注册 MCP 工具 |
| `dashboard.AgentRequest` | `goagentx/internal/dashboard` | Agent 参数结构体 |

---

## 四、数据存储设计

### DB 选型

| 阶段 | 数据库 | 原因 |
|------|--------|------|
| 开发/个人回测 | **SQLite** | 零部署、文件级、够用 |
| 生产/团队 | **PostgreSQL + pgvector** | 并发、向量搜索、可扩展 |

### 核心表

```sql
-- quant_decisions: 交易决策记录
CREATE TABLE IF NOT EXISTS quant_decisions (
    id              TEXT PRIMARY KEY,
    ticker          TEXT NOT NULL,
    decision_date   TEXT NOT NULL,
    signal          TEXT NOT NULL,       -- "buy" / "sell" / "hold"
    confidence      REAL,
    quantity        INTEGER,
    price           REAL,
    reasoning       TEXT,
    analyst_reports TEXT,                -- JSON
    debate_rounds   INTEGER,
    realized_return REAL,
    alpha_vs_spy    REAL,
    reflection      TEXT,
    created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(ticker, decision_date)
);

-- market_data: 行情数据缓存
CREATE TABLE IF NOT EXISTS market_data (
    ticker      TEXT NOT NULL,
    date        TEXT NOT NULL,
    open        REAL, high REAL, low REAL, close REAL,
    volume      INTEGER,
    source      TEXT DEFAULT 'yahoo',
    PRIMARY KEY (ticker, date)
);

-- signals: 技术指标信号缓存
CREATE TABLE IF NOT EXISTS signals (
    ticker      TEXT NOT NULL,
    date        TEXT NOT NULL,
    indicator   TEXT NOT NULL,
    value       REAL,
    metadata    TEXT,
    PRIMARY KEY (ticker, date, indicator)
);
```

---

## 五、Polymarket 集成

```go
// internal/quant/market/polymarket.go

// FetchMarket 查询预测市场的当前价格
func FetchMarket(ctx context.Context, question string) (*Market, error) {
    // Polymarket Gamma API: https://gamma-api.polymarket.com
}

// SentimentSignal 将预测市场概率转化为 Sentiment 信号
// YES 价格 0.65 → 市场认为 65% 概率 → 情绪正向
func SentimentSignal(market *Market) float64 {
    return market.YesPrice
}
```

在 Sentiment Analyst Prompt 中使用：

```
Polymarket Prediction Markets:
- "Will AAPL close above $250 next month?" → YES at $0.65
- "Will Fed cut rates in June?" → YES at $0.72

Interpret these probabilities and incorporate them into
your overall sentiment assessment for AAPL.
```

---

## 六、代码量预估汇总

| 模块 | 文件数 | 行数 | 核心逻辑 |
|------|--------|------|---------|
| market (行情接入) | 5 | ~850 | ~550 |
| indicators (技术指标) | 5 | ~350 | ~250 |
| store (持久化) | 3 | ~500 | ~300 |
| sentiment (情绪聚合) | 1 | ~150 | ~100 |
| agents (Agent 工程) | 5 | ~900 | ~500 |
| workflow (编排) | 2 | ~400 | ~300 |
| memory (记忆) | 1 | ~200 | ~150 |
| main + config | 4 | ~610 | ~200 |
| **合计** | **26** | **~3,560** | **~2,300** |

---

## 七、开发路线图

```
Week 1: 数据层 market + indicators + store        ← ~1,700行
Week 2: Agent 层 8 个 Agent Prompt 工程             ← ~900行
Week 3: 编排层 workflow + memory + main             ← ~700行
Week 4: 打磨 chaos + docs + run.sh                  ← ~310行
```

---

*本文档对应实施计划详见 `plan/quant-implementation-plan.md`*
