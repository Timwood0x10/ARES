# 模块分析报告：`internal/llm` 与内部工具包

---

# `internal/llm` / `internal/llmservice`

### 1. `llm/` 中多个配置常量死代码
- **位置**：`internal/llm/`（32 个文件）内 `keyStyle`、`keyAge` 等
- **说明**：`keyStyle`、`keyAge` 定义但从未引用（`keyMatchReason`、`keyReason`、`keyBrand`、`keyItemID` 等是活跃的）。死常量。
- **状态**：✅ 已核实删除（2026-08-14）——全仓 grep 无 `keyStyle`/`keyAge` 命中，死常量已清理，报告条目过时。

### 2. `llm/validator.go` `toInt64` 边界检查错误（潜在）
- **位置**：`validator.go` 约 479 行
- **说明**：`if val <= float64(^uint64(0)>>1) && val >= float64(^int64(0))`。`^uint64(0)>>1` = MaxInt64（正确上界）；但 `^int64(0)` = **-1**（不是 MinInt64！MinInt64 应为 `-1<<63`）。所以下界检查实为 `val >= float64(-1)`，允许 -1 到 MaxInt64 之间的大数，范围判断与实际 MinInt64 不符。**（标注：需确认调用方预期，潜在边界 bug。）**
- **状态**：✅ 已修复（2026-08-14）——`internal/llm/output/validator.go` 下界改为 `float64(-1<<63)`（真 MinInt64），极大负浮点数不再越界通过，build 通过。

### 3. `resilience.go` 重试抖动注释正确
- **位置**：`resilience.go`
- **说明**：`retryJitterRatio` 为 0.4，`delay *= 1 - 0.4/2 + 0.4*rand` = `[0.8, 1.2)`，即 ±20%，与注释一致。**（无问题，确认。）**

---

# 内部工具包

## `retrievalservice`

### 4. `service.go` `ListKnowledge` 分页元数据用已分页结果计算（LOGIC）
- **位置**：`retrievalservice/service.go`
- **说明**：`total := int64(len(items))`，而 `items` 是 repo.ListKnowledge **已分页**的结果（repo 内部应用了 `filter.Pagination`）。因此 `total` 是当前页元素数而非库中总数，`totalPages`/`hasMore` 基于已截断页计算，分页元数据全部错误。与 `internal/agents/service_impl.go ListAgents` 同类问题。
- **状态**：✅ 已修复（2026-08-14）——`core.RetrievalRepository` 新增 `CountKnowledge(ctx, tenantID, filter)`（分页前计数）；`MemoryRepository`/mock 实现；`ListKnowledge` 改用 `repo.CountKnowledge` 计算 total，与 agents 修复同模式，build/vet/test 通过。

## `evidence`

### 5. `collector.go` `Emit` 中的 `WithID("")` 是 no-op（DEAD_CODE）
- **位置**：`evidence/collector.go` 93 行
- **说明**：`opts = append(opts, WithID(""))`——`WithID` 检查 `if id != ""` 所以 `""` 不做任何事。死代码行。

## `ares_ratelimit`

### 6. `sliding_window.go` `Burst` 字段被忽略（LOGIC）
- **位置**：`sliding_window.go`
- **说明**：`maxRequests := int(math.Ceil(config.Rate))`，`config.Burst` 从不使用。`SlidingWindowLimiter` 只用 Rate 作 maxRequests，而 `DefaultConfig` 里 Rate=10、Burst=20。Burst 字段被忽略（可能是有意的，但值得确认）。

### 7. `semaphore.go` `Release` 不清理 key（LOGIC）
- **位置**：`semaphore.go`
- **说明**：`Allow` 用 key "default"，`Wait` 调 `Acquire(ctx, "default")`；`Release` 递减但不删 key（计数到 0 仍留 `acquired["default"]=0`）。轻微状态残留。

## `ares_shutdown`

### 8. `manager.go` WaitGroup 复用潜在问题（LOGIC，低置信）
- **位置**：`ares_shutdown/manager.go`
- **说明**：`StartShutdown`/`executePhase` 用 WaitGroup，若 phase 超时但有 goroutine 未结束，WaitGroup 可能被复用计数（go vet 的 `WaitGroup.Add` 正值检查在并发下不总是捕获）。需阶段内无遗留 goroutine。**（标注：低置信度，需结合实际用法。）**

## 其它已确认

- `cmdutil` 目录为空。
- `memoryservice/service.go` 的 `expired` 变量名易误导但非 bug。
- `system_runtime/orchestrator.go` 关闭时 `waitCh` 为缓冲 1，goroutine 阻塞发送不泄漏。
- `scoreutil`、`logger`、`errors`、`truncate`、`cmdutil` 等未发现可验证的真 bug。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `llm/validator.go` ~479 | toInt64 下界用 `^int64(0)`(-1) 而非 MinInt64 |
| 中 | `retrievalservice/service.go` | ListKnowledge 分页元数据用已分页结果 |
| 低 | `llm/` 常量 | keyStyle/keyAge 死常量 |
| 低 | `evidence/collector.go` 93 | WithID("") 死代码 |
| 低 | `ares_ratelimit/sliding_window.go` | Burst 字段被忽略 |
| 低 | `ares_shutdown/manager.go` | WaitGroup 复用潜在问题（低置信） |
