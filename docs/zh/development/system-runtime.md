# System Runtime 架构

> 日期：2026-08-04
> 状态：阶段 0-3 已实现

## 概述

System Runtime（`internal/system_runtime/`）是系统级控制面，统一管理组件装配、依赖解析、生命周期编排和关闭协调。它与 `ares_runtime.Manager`（Agent 生命周期子系统）分离。

## 核心概念

### 组件生命周期

每个受管组件实现以下一个或多个接口：

```go
type Component interface {
    Name() string
    Dependencies() []string
}

type Binder interface {
    Bind(ctx context.Context, deps Resolver) error
}

type Starter interface {
    Start(ctx context.Context) error
}

type ReadinessChecker interface {
    Ready(ctx context.Context) error
}

type Stopper interface {
    Stop(ctx context.Context) error
}

type Waiter interface {
    Wait() error
}
```

状态机：
```
Declared → Constructed → Bound → Started → Ready
                         ↘ Failed
Ready → Degraded / Failed
Ready|Degraded → Stopping → Stopped
```

### 组件模式

- **Required**：必须达到 Ready 系统才 Ready。失败 = 系统失败。
- **Optional**：禁用时不构造。启用后表现为 Required。
- **Degraded**：可以降级运行。必须报告缺失能力。

### 配置门控（阶段 2）

以下配置标志现在控制组件是否构造：

| 配置标志 | 默认值 | 组件 | false 时的行为 |
|---|---|---|---|
| `memory.enabled` | false | MemoryManager | 不构造，无 goroutine |
| `evolution.enabled` | false | NewEvolution + GA ticker | 不构造，无 ticker |
| `knowledge.retrieval_enabled` | false | AKG 回路 | 只读或跳过 |
| `embedding.enabled` | false | EmbeddingClient | 不构造 |
| `storage.enabled` | false | PostgreSQL 连接池 | 不构造 |

**重要**：调用 `Bootstrap()` 的测试必须显式设置 `memory.enabled: true` 才能获得 Memory 构造。

### EventStore 装配（阶段 3）

EventStore 现在在 Bootstrap 构造期间注入 MemoryManager，而非在 `serve.go` 中后置装配。消除了 B01 旁路。

### EvidenceStore（始终可用）

`comp.EvidenceStore` 现在始终设置，即使 evolution 被禁用。启用时使用 NewEvolution 的 EvidenceStore；禁用时使用独立 store 供 flight recorder 使用。

## 使用方法

### 基本 Bootstrap（启用 Memory）

```go
cfg := &ares_config.Config{
    LLM:    ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    Memory: ares_config.MemoryConfig{Enabled: true},
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
```

### 完整配置

```go
cfg := &ares_config.Config{
    LLM: ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    Memory: ares_config.MemoryConfig{
        Enabled:            true,
        EnableRAG:          true,
        EnableDistillation: true,
    },
    Evolution: ares_config.EvolutionConfig{Enabled: true},
    Knowledge: ares_config.KnowledgeConfig{RetrievalEnabled: true},
    Storage:   ares_config.StorageConfig{Enabled: true, Type: "postgres", Host: "localhost"},
    Embedding: ares_config.EmbeddingConfig{Enabled: true},
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
```

### 最小配置（无 Memory，无 Evolution）

```go
cfg := &ares_config.Config{
    LLM: ares_config.LLMConfig{Provider: "ollama", Model: "llama3.2"},
    // Memory 和 Evolution 默认禁用
}
comp, err := ares_bootstrap.Bootstrap(ctx, cfg, nil)
// comp.Memory == nil, comp.NewEvolution == nil
// comp.EvidenceStore 仍然设置（独立）
// comp.FlightRecorder 仍然启动
```

## 闭环测试

闭环测试使用 `closure` 构建标签：

```bash
go test -tags closure ./internal/ares_bootstrap/...
```

全部 20 个闭环测试通过（19 PASS，1 SKIP）。
