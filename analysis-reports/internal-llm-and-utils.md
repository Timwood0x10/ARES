# 模块分析报告：`internal/llm` 与内部工具包

---

# `internal/llm` / `internal/llmservice`

### 3. `resilience.go` 重试抖动注释正确
- **位置**：`resilience.go`
- **说明**：`retryJitterRatio` 为 0.4，`delay *= 1 - 0.4/2 + 0.4*rand` = `[0.8, 1.2)`，即 ±20%，与注释一致。**（无问题，确认。）**

---

# 内部工具包

## `retrievalservice`

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
