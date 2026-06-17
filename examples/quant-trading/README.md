# GoAgentX Quantitative Trading Demo

A self-healing multi-agent trading system. 8 specialized LLM agents analyze stocks, debate bull vs bear cases, and produce trading decisions — while Arena chaos engineering continuously tests resilience.

## Architecture

```
Portfolio Manager (Leader Agent)
  ├── Fundamentals Analyst    ← financial_data MCP tool
  ├── Sentiment Analyst       ← polymarket_sentiment MCP tool
  ├── News Analyst            ← general knowledge
  ├── Technical Analyst       ← technical_indicators MCP tool
  ├── Bull Researcher         ← debate (bull case)
  ├── Bear Researcher         ← debate (bear case)
  ├── Trader                  ← trading decision
  └── Risk Manager            ← risk assessment
```

## Quick Start

```bash
cd examples/quant-trading
./run.sh
```

Open http://localhost:8092 → Arena tab to see agents and kill them.

### Switch Model

编辑 `config.yaml`，取消注释对应的 LLM 配置块即可切换模型：

```yaml
# Ollama (本地, 默认)
llm:
  provider: "ollama"
  model: "llama3.2"

# DeepSeek v4 Flash
# llm:
#   provider: "openai"
#   base_url: "https://token.sensenova.cn/v1"
#   model: "deepseek-v4-flash"
#   api_key: "sk-xxx"

# GPT-5.5
# llm:
#   provider: "openai"
#   base_url: "https://vip-sg.freemodel.dev"
#   model: "gpt-5.5"
#   api_key: "fe_xxx"
```

## Requirements

- Go 1.22+
- [Ollama](https://ollama.ai/) with default model (llama3.2 or your choice)

## Files

```
├── main.go            Entry point
├── agents/            Agent prompts and creation
│   ├── prompts.go     8 bilingual prompts
│   └── agents.go      Agent factory
├── workflow/          DAG pipeline
├── memory/            Cross-stock learning
├── chaos/             Arena YAML scenarios
├── config.yaml        Service config
└── run.sh             One-click runner
```
