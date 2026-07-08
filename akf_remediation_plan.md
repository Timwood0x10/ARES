# AKF 修复计划

> 基于 `AKF_BUGS_REPORT.md` 21 个问题，按优先级逐批修复。
> 编码规范：`plan/rules/code_rules.md`

## 批次与优先级

| Batch | Bugs | 难度 | 主要影响 |
|-------|------|------|---------|
| **P0-安全** | B3 (MySQL注入), B4 (MySQL无LIMIT+ID碰撞), B6 (nil panic) | 小 | 安全+崩溃 |
| **P0-核心逻辑损坏** | B1 (Resolver失效), B2 (QueryPlan丢弃), B8 (Pipeline不接线) | 中 | 核心功能损坏 |
| **P1-功能修复** | B5 (全连接图), B7 (Store契约), B9 (Postgres空列), B10 (MemoryProvider), B14 (Planner空壳) | 中 | 功能缺陷 |
| **P2-死代码激活** | B11 (Compiler), B12 (Embedding), B13 (LazyLoading), B21 (MCP残缺) | 中 | 死代码 |
| **P3-质量收尾** | B15 (ObjectType), B16 (Reducer), B17 (Offset), B18 (Intent), B19 (Timeline), B20 (Confidence) | 小 | 质量 |
