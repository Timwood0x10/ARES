# GoAgent 文档中心

欢迎来到 GoAgent 框架文档中心。

## 文档语言

- **[中文文档](./)** — 中文文档
- **[English Docs](../en/)** — 英文文档

---

## 快速链接

| 主题 | 中文 | English |
|------|------|---------|
| 快速开始 | [快速开始](./guides/quick-start.md) | [Quick Start](../en/guides/quick-start.md) |
| 常见问题 | [常见问题](./guides/faq.md) | [FAQ](../en/guides/faq.md) |
| 架构设计 | [架构设计](./architecture/arch.md) | [Architecture](../en/architecture/arch.md) |
| 集成指南 | [集成指南](./development/integration-guide.md) | [Integration Guide](../en/development/integration-guide.md) |
| 测试指南 | [测试指南](./development/testing-guide.md) | [Testing Guide](../en/development/testing-guide.md) |
| 集成测试 | [集成测试](./development/integration-testing.md) | [Integration Testing](../en/development/integration-testing.md) |
| CI/CD | [CI/CD 管线](./development/ci-cd.md) | [CI/CD Pipeline](../en/development/ci-cd.md) |
| 性能调优 | [性能调优](./development/performance-tuning.md) | [Performance Tuning](../en/development/performance-tuning.md) |
| API 参考 | — | [API Reference](../en/api-reference.md) |
<<<<<<< HEAD
=======
| 示例 | [示例](./development/examples.md) | [Examples](../en/development/examples.md) |
>>>>>>> 3f3093d ( feat(v2): runtime layer, event sourcing, dynamic workflow, HITL, pluggable vector store + 50 bug fixes)

---

## v2 特性

| 特性 | 中文 | English |
|------|------|---------|
| Leader 故障转移 | [Leader 故障转移](./features/leader-failover.md) | [Leader Failover](../en/features/leader-failover.md) |
| 运行时动态图 | [运行时动态图](./features/dynamic-graph.md) | [Dynamic Graph](../en/features/dynamic-graph.md) |
| Runtime 层 | [Runtime 层](./architecture/runtime.md) | [Runtime Layer](../en/architecture/runtime.md) |
| 人机协作 | [人机协作](./features/hitl.md) | [Human-in-the-Loop](../en/features/hitl.md) |
| Agent 复活 | [Agent 复活](./features/resurrection.md) | [Agent Resurrection](../en/features/resurrection.md) |
| v2 架构 | [v2 架构](./architecture/v2-architecture.md) | [v2 Architecture](../en/architecture/v2-architecture.md) |
| 事件溯源 | [事件溯源](./features/event-sourcing.md) | [Event Sourcing](../en/features/event-sourcing.md) |
| 框架对比 | — | [Framework Comparison](../en/framework-comparison.md) |

---

## 功能特性

| 特性 | 中文 | English |
|------|------|---------|
| 经验系统 | [经验系统](./features/experience-system.md) | [Experience System](../en/features/experience-system.md) |
| 记忆蒸馏 | [记忆蒸馏](./features/memory-distillation.md) | [Memory Distillation](../en/features/memory-distillation.md) |
| LLM 查询重写 | [LLM 查询重写](./features/llm-query-rewrite.md) | — |

---

## 组件

| 组件 | 中文 | English |
|------|------|---------|
| Agents | [Agent 定义](./components/agents-definition.md) | [Agent Definition](../en/components/agents-definition.md) |
| Leader Agent | [Leader Agent](./components/agents-leader.md) | [Leader Agent](../en/components/agents-leader.md) |
| Sub Agent | [Sub Agent](./components/agents-sub.md) | [Sub Agent](../en/components/agents-sub.md) |
| Runtime | [Runtime 层](./architecture/runtime.md) | [Runtime Layer](../en/architecture/runtime.md) |
| 记忆 | [记忆组件](./components/memory.md) | [Memory](../en/components/memory.md) |
| LLM | [LLM 组件](./components/llm.md) | [LLM](../en/components/llm.md) |
| 存储 | [存储组件](./components/storage.md) | [Storage](../en/components/storage.md) |
| 协议 | [协议组件](./components/protocol.md) | [Protocol](../en/components/protocol.md) |
| 引擎 | [引擎组件](./components/engine.md) | [Engine](../en/components/engine.md) |
| 工具 | [工具组件](./components/tools.md) | [Tools](../en/components/tools.md) |
| 限流 | [限流组件](./components/ratelimit.md) | [Rate Limit](../en/components/ratelimit.md) |
| 优雅关闭 | [优雅关闭](./components/shutdown.md) | [Shutdown](../en/components/shutdown.md) |

---

## 目录结构

```
docs/
├── README.md          # 文档中心入口（双语）
├── zh/
│   ├── README.md      # 中文文档入口
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   └── guides/
├── en/                # 英文文档
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   ├── guides/
│   └── api-reference.md
├── benchmarks/        # Benchmark 结果
└── plan/              # 开发计划
```

---

**最后更新**: 2026-06-12
