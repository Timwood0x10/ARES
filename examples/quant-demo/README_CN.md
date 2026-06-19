# 量化多智能体系统 Demo

基于 GoAgentX 构建的**自愈型量化多智能体系统**。展示多因子选股 + 自动故障恢复能力。

## 架构

```
投资组合经理 (Leader)
  ├── 动量研究员    ← 计算动量因子（3个月 + 6个月收益率）
  ├── 价值研究员    ← 计算价值因子（市盈率 + 市净率）
  └── 风险监控员    ← 检查集中度 & 投资组合风险
```

每个研究员是一个在 Dashboard 上可见的 Agent。Arena 混沌工程层会持续杀死和复活 Agent，证明系统的自愈能力。

## 功能演示

| 能力 | 实现方式 |
|---|---|
| **多因子选股** | 动量因子（40% 3月 + 60% 6月）+ 价值因子（逆 P/E & P/B）→ 复合 z-score → 排序 |
| **自愈运行时** | Arena 杀死分析中的 Agent → 自动复活，上下文完整保留 |
| **组合构建** | 因子加权分配，25% 单仓位上限，归一化到 1.0 |
| **风险监控** | Herfindahl 集中度、重仓检测、加权风险评分 |
| **实时 Dashboard** | DAG 可视化、事件时间线、Arena 控制面板、弹性评分 |

## 快速开始

### 前置条件

- Go 1.22+
- [Ollama](https://ollama.ai/) 以及 `llama3.2` 模型（或任何 OpenAI 兼容的 LLM）

### 运行

```bash
# 启动 Ollama（如果尚未运行）
ollama serve

# 拉取模型
ollama pull llama3.2

# 启动量化 Demo
cd examples/quant-demo
./run.sh

# 或手动启动：
go run . -config ./examples/quant-demo/config.yaml
```

### 观察要点

1. 打开 http://localhost:8091 → Arena 标签页
2. 观察 DAG 中 4 个量化 Agent 的生成
3. 终端日志中实时显示因子评分
4. 点击 ☠Leader 刺杀一个 Agent
5. 观察事件日志：`Agent Failed → Resurrection Started → Agent Resurrected`
6. 最终报告展示：仓位分配 + 风险评分 + 弹性指标

## 流水线流程

```
loadUniverse("universe.csv") → 10 只股票
  ↓
computeMomentum()      → z-score 排名的动量因子
  ├── 第 1 名: AMZN (+1.42σ)  ← 最强动量
  └── 第 10 名: TSLA (-1.65σ) ← 最弱动量
  ↓
computeValue()         → z-score 排名的价值因子
  ├── 第 1 名: JPM (+1.38σ)   ← 最佳价值
  └── 第 10 名: TSLA (-1.92σ) ← 最差价值
  ↓
allocate(动量 + 价值) → 复合评分 → 加权组合（最大 25%/仓位）
  ↓
computeRisk()          → 集中度检查 → 风险评分
  ↓
printReport()          → 完整系统报告 + 自愈统计
```

因子计算完全在 Go 层执行（不调用 LLM）。Agent 运行 LLM 分析生成解读叙事。Arena 混沌测试在后台并行运行。

## 扩展

要添加新因子，按照 `computeMomentum` 模式实现函数即可：

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

然后在 `quantDemo()` 中调用，并在 `createQuantAgents()` 中创建对应的 Agent。

## 配置

参见 `config.yaml`：
- LLM 提供商、模型、端点
- Dashboard 端口（默认 8091）
- 基础 Demo 无需 MCP 服务器

## 文件

```
examples/quant-demo/
├── main.go              # Demo 入口 + 量化流水线
├── data/universe.csv    # 10 只股票的基本面数据
├── config.yaml          # 服务配置
├── run.sh               # 一键启动脚本
├── README.md            # English documentation
└── README_CN.md         # 本文档（中文）
```
