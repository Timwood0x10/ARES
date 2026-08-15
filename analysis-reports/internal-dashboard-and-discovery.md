# 模块分析报告：`internal/dashboard` 与 `internal/discovery`

> 分析范围：`internal/dashboard/`（22 个文件）、`internal/discovery/`（18 个文件）

---

# `internal/dashboard`

## BUG（高置信度）

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | dashboard `api_handlers.go` 182 | handleWS 未 nil-check hub |
| 中 | discovery `binary.go` 44 | symlink 绕过 allowlist（安全） |
| 中 | dashboard `api_handlers.go` 494 | failover 计数把任意成功当 failover |
| 低 | monitoring nil deref / discovery goroutine 生命周期 / identity 平局非确定 | 若干低优先级项 |
