# 模块分析报告：`internal/ares_memory`（记忆系统）

> 分析范围：`internal/ares_memory/`（68 个 Go 文件），含 distillation/、context/ 等子包

---

## BUG（高置信度）

### 1. 蒸馏 `KeepBoth` 冲突分支丢失 `problem`/`confidence` 元数据（数据丢失）
- **位置**：`distillation/distiller.go` 584-604 行
- **说明**：重建的 `oldMemory` 只带 `Metadata: {"solution": conflict.Solution}`，省略 `"problem"`、`"confidence"`（及 `"extraction_method"`）。后续 `memoryManager.StoreDistilledTask`（manager_impl.go 567-597）**只从 Metadata 读取** `problem`/`solution`/`confidence`（不从 `Importance`/`Content`），因此持久化的经验得到空 `Problem` 和 `Confidence==0`。重嵌入（597 行 `embedOneMemory`）也读 Metadata，嵌入的是部分/空文本。**KeepBoth 策略的真实数据丢失 bug。**

### 2. 无冲突正常路径也会误报 "Failed to detect conflicts"
- **位置**：`distillation/distiller.go` 563-567 行
- **说明**：`d.resolver.DetectConflict` 在**无冲突**时（repo nil、空向量、无相似结果、低于阈值）返回哨兵错误 `ErrNoConflict`（非 nil）。代码只要 `err != nil` 就打 `"Failed to detect conflicts"` 警告，导致**每个无冲突的正常记忆**都触发此警告。应排除哨兵错误再记录。

---

## LOGIC（逻辑问题）

### 3. `SearchSimilarTasks` 永远返回空结果（experience 仓库未接线）
- **位置**：`production_manager.go` 139-155 + `production_manager_tasks.go` 271-324
- **说明**：`NewProductionMemoryManager` 用 `nil` 构造 `retrievalService` 的 `expRepo`。`SearchSimilarTasks` 设置 `Plan.SearchExperience=true`，但 `searchExperienceVector`/`bm25SearchExperience`（retrieval_search.go）都 guard `s.expRepo == nil` 并返回空。**"搜索相似任务"路径在生产中总是返回零结果**，功能静默失效。

### 4. `enforceSolutionCap` 可能让租户仍超上限
- **位置**：`distillation/distiller.go` 779-815 行
- **说明**：`GetByMemoryType`（Postgres 适配器封顶 `DefaultListLimit=1000`）。若 `count` 超过上限多于返回列表大小，`deleteCount` 被 clamp 到 `len(solutions)`，只删除一部分超额项，租户仍超 `MaxSolutionsPerTenant`。

### 5. 生命周期标志不对称
- **位置**：`manager_impl.go` 202 行 vs `production_manager.go`
- **说明**：`memoryManager.Stop()` 后 `started` 仍为 true，后续 `Start()` 是静默 no-op（无法重启）；`ProductionMemoryManager.Stop()` 正确重置 `started=false`。两管理器重启行为不一致。

---

## DEAD_CODE

### 6. `manager_impl.go` `cosineSimilarity` 生产未用
- **位置**：`manager_impl.go` 708 行
- **说明**：包级 `cosineSimilarity` 仅 `manager_impl_cosine_test.go` 引用。

### 7. `manager.go` `ToBuildContextFormat` 生产未用
- **位置**：`manager.go` 305 行
- **说明**：仅 `manager_test.go` 引用。

### 8. `FromCoreMessage` 未使用参数 `sessionID`
- **位置**：`manager.go` 219/244 行
- **说明**：`_ = sessionID` 丢弃，函数体从不使用。

### 9. `context/cleaner.go` `summarizeSearchResult` 计数偏差
- **位置**：`context/cleaner.go` 428-432 行
- **说明**：`count = len(strings.Split(content, "\n"))`，内容以换行结尾时多计一个"result"。轻微 off-by-one。

### 10. `context/cache.go` maxSize=0 时无界增长
- **位置**：`context/cache.go` 55-71 行
- **说明**：`Set` 仅在 `len(c.items) >= c.maxSize` 时驱逐；`maxSize==0` 时 `evictOldest` 对空 map 无操作，缓存永不驱逐、无界增长。

### 11. `distillation/detector.go` 未读的 `sensitivity` 字段
- **位置**：`distillation/detector.go` 136-146 行
- **说明**：`sensitivity` 在 `NewQuestionDetector` 赋值，但 `Detect` 委托给 `IsProblem`，从不读该字段。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `distiller.go` 584-604 | KeepBoth 分支丢 problem/confidence，持久化数据丢失 |
| **高** | `distiller.go` 563 | 无冲突误报 "Failed to detect conflicts" |
| 中 | `production_manager.go` 139 | SearchSimilarTasks 永远空（expRepo 未接线） |
| 中 | `distiller.go` 779 | enforceSolutionCap 可能未恢复上限 |
| 低 | 多处 | 生命周期不对称 + 大量死代码 |
