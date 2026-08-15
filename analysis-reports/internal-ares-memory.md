# 模块分析报告：`internal/ares_memory`（记忆系统）

> 分析范围：`internal/ares_memory/`（68 个 Go 文件），含 distillation/、context/ 等子包

---

## BUG（高置信度）

---

## LOGIC（逻辑问题）

### 4. `enforceSolutionCap` 可能让租户仍超上限
- **位置**：`distillation/distiller.go` 779-815 行
- **说明**：`GetByMemoryType`（Postgres 适配器封顶 `DefaultListLimit=1000`）。若 `count` 超过上限多于返回列表大小，`deleteCount` 被 clamp 到 `len(solutions)`，只删除一部分超额项，租户仍超 `MaxSolutionsPerTenant`。
- **状态**：⚠️ 已核实（2026-08-14）——distill 层 `ExperienceRepository.GetByMemoryType` 为 3 参（无 limit），`enforceSolutionCap` 以 `count`（`CountByMemoryType`）计算 `deleteCount`，仅当 `len(solutions) < deleteCount` 时 clamp；若 distill 实现全量加载则 clamp 不触发（正确）。Postgres 适配器的 `DefaultListLimit=1000` 是另一条路径，仅当 distill 实现经该适配器且租户超 1000+ 上限时才生效——低风险边界，暂不修改（需确认 distill repo 的具体实现绑定）。

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
