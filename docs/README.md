# ARES Documentation Center

Welcome to the ARES framework documentation center.

## Release Notes / 发布说明

| Version | 中文 | English |
|---------|------|---------|
| v0.2.4 | [发布说明](../CHANGELOG.md) | [Release Notes](../CHANGELOG.md) |
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
| API Reference | — | [API Reference](./en/api-reference.md) |

## Coding Standards / 编码规范

| Document | 说明 | 强制 |
|----------|------|------|
| [Code Rules](../plan/rules/code_rules.md) | Go 编码规范：命名、格式、错误处理、并发、禁止模式 | ✅ CI 拦截 |
| [Skills](../plan/rules/skills.md) | 开发技能要求与最佳实践 | 推荐 |
| [Uber Go Style](../plan/rules/uber_go_style.md) | Uber Go 风格指南参考 | 推荐 |

---

## Core Features

| Feature | 中文 | English |
|---------|------|---------|
| Leader Failover | [Leader 故障转移](./zh/features/leader-failover.md) | [Leader Failover](./en/features/leader-failover.md) |
| Dynamic Graph | [运行时动态图](./zh/features/dynamic-graph.md) | [Dynamic Graph](./en/features/dynamic-graph.md) |
| Event Sourcing | [事件溯源](./zh/features/event-sourcing.md) | [Event Sourcing](./en/features/event-sourcing.md) |
| Memory Distillation | [记忆蒸馏](./zh/features/memory-distillation.md) | [Memory Distillation](./en/features/memory-distillation.md) |
| Human-in-the-Loop | [人机协作](./zh/features/hitl.md) | [Human-in-the-Loop](./en/features/hitl.md) |
| Agent Resurrection | [Agent 复活](./zh/features/resurrection.md) | [Agent Resurrection](./en/features/resurrection.md) |
| Autonomous Evolution | [自主进化](./zh/features/autonomous-evolution.md) | [Autonomous Evolution](./en/features/autonomous-evolution.md) |
| MCP & Dashboard | [MCP 与控制面板](./zh/features/mcp-and-dashboard.md) | [MCP & Dashboard](./en/features/mcp-and-dashboard.md) |

---

## Deep Dives (Articles)

| Topic | 中文 | English |
|-------|------|---------|
| Runtime Lifecycle | [运行时生命周期](./articles/zh/07-runtime-lifecycle-deep-dive.md) | [Runtime Lifecycle](./articles/en/07-runtime-lifecycle-deep-dive.md) |
| Workflow Engine | [工作流引擎](./articles/zh/04-workflow-engine-deep-dive.md) | [Workflow Engine](./articles/en/04-workflow-engine-deep-dive.md) |
| Memory Distillation | [记忆蒸馏](./articles/zh/03-memory-distillation-deep-dive.md) | [Memory Distillation](./articles/en/03-memory-distillation-deep-dive.md) |
| Event System | [事件系统](./articles/zh/08-event-system-deep-dive.md) | [Event System](./articles/en/08-event-system-deep-dive.md) |
| Tool System | [工具系统](./articles/zh/05-tool-system-deep-dive.md) | [Tool System](./articles/en/05-tool-system-deep-dive.md) |
| Autonomous Evolution | [自主进化](./articles/zh/11-autonomous-evolution-deep-dive.md) | [Autonomous Evolution](./articles/en/11-autonomous-evolution-deep-dive.md) |
| Arena Fault Injection | [混沌工程](./articles/zh/09-arena-fault-injection-deep-dive.md) | [Arena Fault Injection](./articles/en/09-arena-fault-injection-deep-dive.md) |

---

## Directory Structure

```
docs/
├── README.md          # This file
├── articles/          # Deep-dive articles (en + zh)
├── en/                # English documentation
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   └── guides/
├── zh/                # Chinese documentation
│   ├── architecture/
│   ├── components/
│   ├── development/
│   ├── features/
│   └── guides/
├── modules/           # Module analysis reports
├── code-review/       # Code review reports
└── development/       # Development guides
```

---

**Last Updated**: 2026-06-27
