# 模块分析报告：`internal/ares_events`（事件系统）

> 分析范围：`internal/ares_events/`（20 个 Go 文件）

---

## BUG（高置信度）

---

## DEAD_CODE

### 4. `summary.go` `EventSummary.Duration()`
- **位置**：`summary.go` 61 行
- **说明**：唯一调用方是 `compactor_test.go:26`，生产无调用。

### 5. `compactable_store.go` `archivePendingRounds`
- **位置**：`compactable_store.go` 380 行
- **说明**：此包装器仅测试引用（`compactable_store_archive_test.go`），生产都直接调 `archivePendingRoundsOnce`/`drainPendingRounds`。注释已说明"保留用于直接单元测试"——有意的生产死代码。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `memory_store.go` 71-93 | nil 事件产生版本空洞（与 pg_store 行为不一致） |
| 中 | `pg_store.go` 421 | 游标用 Timestamp 而非 created_at，可能重复/漏投 |
| 中 | `compactable_store.go` 174 | Append teardown 竞争，worker 未 join |
| 低 | `summary.go` 61 | Duration() 仅测试使用 |
