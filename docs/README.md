# ARES Documentation Center

Welcome to the ARES framework documentation center.

## Release Notes / 发布说明

| Version | 中文 | English |
|---------|------|---------|
| v0.3.1 (dev) | [CHANGELOG](../CHANGELOG.md) | [CHANGELOG](../CHANGELOG.md) |
| v0.3.0 | [发布说明](../CHANGELOG.md) | [Release Notes](../CHANGELOG.md) |

> 注：`docs/zh/features/` 中的部分特性文档写于 v1/v2 架构时期（2026-06 前后），涉及 Leader/Sub 模型的内容已过时——v0.3.x 起 Leader-Sub 架构已删除，现行架构见 [framework-comparison](./framework-comparison-langchain-crewai-agentscope-goagent-zh.md) §3 与 [CAPABILITY-MAP](./CAPABILITY-MAP.md)。这些旧文档保留作历史参考，待逐步重写。

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
| Framework Comparison | [框架对比](./framework-comparison-langchain-crewai-agentscope-goagent-zh.md) | [Framework Comparison](./framework-comparison-langchain-crewai-agentscope-goagent-en.md) |
| Capability Map | [能力地图](./CAPABILITY-MAP.md) | [Capability Map](./CAPABILITY-MAP.en.md) |
| Integration | [集成指南](./zh/development/integration-guide.md) | [Integration Guide](./en/development/integration-guide.md) |
| Testing | [测试指南](./zh/development/testing-guide.md) | [Testing Guide](./en/development/testing-guide.md) |
| config.yaml Guide | [config.yaml 配置指南](./articles/zh/25-config-yaml-guide.zh.md) | [config.yaml Guide](./articles/en/25-config-yaml-guide.en.md) |
| API Reference | — | [API Reference](./en/api-reference.md) |

## Coding Standards / 编码规范

| Document | 说明 | 强制 |
|----------|------|------|
| [Code Rules](../plan/rules/code_rules.md) | Go 编码规范：命名、格式、错误处理、并发、禁止模式 | ✅ CI 拦截 |
| [Skills](../plan/rules/skills.md) | 开发技能要求与最佳实践 | 推荐 |
| [Uber Go Style](../plan/rules/uber_go_style.md) | Uber Go 风格指南参考 | 推荐 |

---

## Core Features（现行架构，v0.3.x）

| Feature | 中文 | English |
|---------|------|---------|
| Kernel Scheduler (task fabric) | [框架对比 §3](./framework-comparison-langchain-crewai-agentscope-goagent-zh.md) | [Comparison §3](./framework-comparison-langchain-crewai-agentscope-goagent-en.md) |
| Agent Recovery | [Agent 恢复](./zh/features/agent-recovery.md) | [Agent Recovery](./en/features/agent-recovery.md) |
| Event Sourcing | [事件溯源](./zh/features/event-sourcing.md) | [Event Sourcing](./en/features/event-sourcing.md) |
| Memory Distillation | [记忆蒸馏](./zh/features/memory-distillation.md) | [Memory Distillation](./en/features/memory-distillation.md) |
| Autonomous Evolution | [自主进化](./zh/features/autonomous-evolution.md) | [Autonomous Evolution](./en/features/autonomous-evolution.md) |
| MCP & Dashboard | [MCP 与控制面板](./zh/features/mcp-and-dashboard.md) | [MCP & Dashboard](./en/features/mcp-and-dashboard.md) |

> 已删除的特性（不要查找）：Leader Failover / Dynamic Graph（旧 leader 模型，v0.3.x 删除）；Agent Resurrection 独立特性（能力并入 aresrecovery / runtime manager 的恢复链路）。HITL（人机协作）在 `internal/fabric/task/workflow/engine` 有实现但未接入生产装配。

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

> 注：articles 深度文章写于不同版本时点，个别模块路径可能已迁移（如 workflow engine 现位于 `internal/fabric/task/workflow/engine`），以 [CAPABILITY-MAP](./CAPABILITY-MAP.md) 为准。

---

## Directory Structure

```
docs/
├── README.md          # This file
├── articles/          # Deep-dive articles (en + zh)
├── en/                # English documentation
│   ├── architecture/
│   ├── development/
│   ├── features/
│   └── guides/
├── zh/                # Chinese documentation
│   ├── architecture/
│   ├── development/
│   ├── features/
│   └── guides/
├── cookbook/          # Runnable recipe agents
├── operator/          # Operator runbook
├── reviews/           # Architecture review reports
├── bug@ques/          # Bug post-mortems (append-only, see plan/rules)
├── convergence/       # Package fan-in audit
└── (top-level comparison & map docs)
```

---

**Last Updated**: 2026-09-13
