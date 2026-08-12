# 模块分析报告：`internal/ares_events`（事件系统）

> 分析范围：`internal/ares_events/`（20 个 Go 文件）

---

## BUG（高置信度）

### 1. `memory_store.go` nil 事件导致流版本产生空洞
- **位置**：`memory_store.go` 71-93 行
- **说明**：`Append` 循环中 `events` 含 `nil` 项时 `continue`，但版本按 `startVersion + int64(i+1)` 用**原始切片下标**赋值。对 `events = [e1, nil, e3]`，`e3` 得到 `Version = start+3`，版本 `start+2` 永久空置，产生流版本空洞。这不同于 `pg_store.go`（101-103 行）**拒绝** nil 事件。空洞会使 `VerifyStreamIntegrity` 和假设版本连续的消费者失败。

---

## LOGIC（逻辑问题）

### 2. `pg_store.go` 订阅游标用 `Timestamp` 而非 `created_at`
- **位置**：`pg_store.go` 380-430、435 行
- **说明**：订阅查询过滤 `created_at > cursor`（DB 列），但游标用 `evt.Timestamp` 推进（421 行）。若事件的 `Timestamp` 与存储的 `created_at` 不同（调用方自定义时间戳），游标与 DB 列漂移，事件可能重复投递或跳漏。

### 3. `compactable_store.go` `Append` 的 teardown 竞争
- **位置**：`compactable_store.go` 174-208 行
- **说明**：`g.Go(...)` goroutine（176 行）在 `s.closed` 检查（190 行）**之前**启动。当 `s.closed` 已为 true 时，`Append` 调 `cancel()` 并返回，但该 worker 未注册到 `wg` 也未 `g.Wait()` 加入。worker 会对可能刚被 `Close()` 关闭的 store 执行 `drainPendingRounds`/`maybeCompact`。错误被记录不崩溃，但 worker 未被 join，可能短暂触碰关闭中的 store。

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
