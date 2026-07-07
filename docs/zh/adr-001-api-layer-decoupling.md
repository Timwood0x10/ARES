# ADR-001: API 层架构解耦

## 状态

✅ 已实施（2026-07）

## 背景

ARES 项目最初只有一个 `internal/` 层，所有组件都在内部包中。随着功能增长，`internal/` 下的包之间形成复杂的循环依赖。为了对外提供稳定的编程接口，引入了 `api/` 层，但在实际使用中 `api/client` 仍然直接 import `internal/` 包，API 层形同虚设。

## 决策

将 API 层分为三层，依赖方向严格单向：

```
api/client (外部入口, 零 internal import)
  → api/core (接口定义)
    → api/service (包装实现)
      → internal/* (内部实现)
```

关键规则：
- `api/client` 不得 import 任何 `internal/` 包
- `api/client.Config` 的字段类型必须是 `api/core` 接口，而非 `internal/*` 的具体类型
- `api/service/*` 负责包装 `internal/*` 的实现，在包内部完成类型转换
- 访问器方法（`Client.LLM()`、`Client.Memory()` 等）返回 `api/core` 接口

## 影响

- 正面：外部项目可通过 `api/client` 嵌入 ARES，无需 import `internal/`
- 正面：`api/core` 接口可以作为 mock 标准，测试不再依赖具体实现
- 负面：`api/service/*` 需要额外的适配层代码（约 50 行/包）

## 实现文件

- `api/client/client.go` — 主入口，零 internal import
- `api/core/*.go` — 23 个接口定义
- `api/service/arena/`、`api/service/events/` 等 — 包装实现
