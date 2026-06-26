# ARES Documentation Center

Welcome to the ARES framework documentation center.

## Release Notes / 发布说明

| Version | 中文 | English |
|---------|------|---------|
| v0.2.0 | [发布说明](./zh/releases/v0.2.0.md) | [Release Notes](./en/releases/v0.2.0.md) |

## Documentation Languages / 文档语言

- **[中文文档](./zh/)** — Chinese documentation
- **[English Docs](./en/)** — English documentation

---

## Quick Links

| Topic | 中文 | English |
|-------|------|---------|
| Quick Start | [快速开始](./zh/guides/quick-start.md) | [Quick Start](./en/guides/quick-start.md) |
| FAQ | [常见问题](./zh/guides/faq.md) | [FAQ](./en/guides/faq.md) |
| Architecture | [架构设计](./zh/architecture/arch.md) | [Architecture](./en/architecture/arch.md) |
| Integration | [集成指南](./zh/development/integration-guide.md) | [Integration Guide](./en/development/integration-guide.md) |
| Testing | [测试指南](./zh/development/testing-guide.md) | [Testing Guide](./en/development/testing-guide.md) |
| Integration Testing | [集成测试](./zh/development/integration-testing.md) | [Integration Testing](./en/development/integration-testing.md) |
| CI/CD | [CI/CD 管线](./zh/development/ci-cd.md) | [CI/CD Pipeline](./en/development/ci-cd.md) |
| Performance | [性能调优](./zh/development/performance-tuning.md) | [Performance Tuning](./en/development/performance-tuning.md) |
| API Reference | — | [API Reference](./en/api-reference.md) |
| Examples | [示例](./zh/development/examples.md) | [Examples](./en/development/examples.md) |

---

## v2 Features

| Feature | 中文 | English |
|---------|------|---------|
| Leader Failover | [Leader 故障转移](./zh/features/leader-failover.md) | [Leader Failover](./en/features/leader-failover.md) |
| Runtime Dynamic Graph | [运行时动态图](./zh/features/dynamic-graph.md) | [Dynamic Graph](./en/features/dynamic-graph.md) |
| Runtime Layer | [Runtime 层](./zh/architecture/runtime.md) | [Runtime Layer](./en/architecture/runtime.md) |
| Human-in-the-Loop | [人机协作](./zh/features/hitl.md) | [Human-in-the-Loop](./en/features/hitl.md) |
| Agent Resurrection | [Agent 复活](./zh/features/resurrection.md) | [Agent Resurrection](./en/features/resurrection.md) |
| v2 Architecture | [v2 架构](./zh/architecture/v2-architecture.md) | [v2 Architecture](./en/architecture/v2-architecture.md) |
| Event Sourcing | [事件溯源](./zh/features/event-sourcing.md) | [Event Sourcing](./en/features/event-sourcing.md) |
| Framework Comparison | — | [Framework Comparison](./en/framework-comparison.md) |

---

## Features

| Feature | 中文 | English |
|---------|------|---------|
| Experience System | [经验系统](./zh/features/experience-system.md) | [Experience System](./en/features/experience-system.md) |
| Memory Distillation | [记忆蒸馏](./zh/features/memory-distillation.md) | [Memory Distillation](./en/features/memory-distillation.md) |
| LLM Query Rewrite | [LLM 查询重写](./zh/features/llm-query-rewrite.md) | — |

---

## Components

| Component | 中文 | English |
|-----------|------|---------|
| Agents | [Agent 定义](./zh/components/agents-definition.md) | [Agent Definition](./en/components/agents-definition.md) |
| Leader Agent | [Leader Agent](./zh/components/agents-leader.md) | [Leader Agent](./en/components/agents-leader.md) |
| Sub Agent | [Sub Agent](./zh/components/agents-sub.md) | [Sub Agent](./en/components/agents-sub.md) |
| Runtime | [Runtime 层](./zh/architecture/runtime.md) | [Runtime Layer](./en/architecture/runtime.md) |
| Memory | [记忆组件](./zh/components/memory.md) | [Memory](./en/components/memory.md) |
| LLM | [LLM 组件](./zh/components/llm.md) | [LLM](./en/components/llm.md) |
| Storage | [存储组件](./zh/components/storage.md) | [Storage](./en/components/storage.md) |
| Protocol | [协议组件](./zh/components/protocol.md) | [Protocol](./en/components/protocol.md) |
| Engine | [引擎组件](./zh/components/engine.md) | [Engine](./en/components/engine.md) |
| Tools | [工具组件](./zh/components/tools.md) | [Tools](./en/components/tools.md) |
| Rate Limit | [限流组件](./zh/components/ratelimit.md) | [Rate Limit](./en/components/ratelimit.md) |
| Shutdown | [优雅关闭](./zh/components/shutdown.md) | [Shutdown](./en/components/shutdown.md) |

---

## Directory Structure

```
docs/
├── README.md          # This file
├── en/                # English documentation
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   ├── guides/
│   └── api-reference.md
├── zh/                # Chinese documentation
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   └── guides/
├── benchmarks/        # Benchmark results
└── plan/              # Development plans
```

---

**Last Updated**: 2026-06-12
