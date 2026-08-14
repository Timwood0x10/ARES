# 模块分析报告：`internal/ares_flight`（飞行记录 / 追踪）

> 分析范围：`internal/ares_flight/`（13 个 Go 文件）

---

## LOGIC（逻辑问题）

### 1. `timeline.go` `Summary()` 在真实数据下几乎总是全零
- **位置**：`timeline.go` 110-150 行
- **说明**：`Collector` 产生的所有 `TimelineEvent`（collector.go 中 `timeline.Add(...)` 只设 `StartAt`，从不设 `EndAt`/`Duration`），因此 `e.Duration` 恒为 0，`maxEnd` 恒为 0。`Summary()` 计算的 `ToolDuration`、`LLMDuration`、`WaitDuration`、`ErrorDuration`、`TotalDuration` 全为 0，所有百分比恒为 0。**`Summary()` 在被真实 collector 喂数据时实际上不可用。**

---

## DEAD_CODE

### 2. `recorder.go` 未读的 `memManager` 字段
- **位置**：`recorder.go` 17、41 行
- **说明**：`FlightRecorder.memManager` 由 `FlightRecorderConfig.MemManager` 赋值但从未被读取，`FlightRecorder` 从不使用记忆管理器。

---

## LOGIC（低置信度）

### 5. `genealogy.go` `RecordResurrection` 父节点 children 未清理
- **位置**：`genealogy.go` 85-130 行
- **说明**：当 `oldNode.ParentID != ""` 时，`newNode` 作为父节点子节点加入（108-111 行），但 `oldNode` **没有**从 `parent.Children` 移除。父节点同时持有已死的 `oldNode` 和复活的 `newNode`，且 `newNode` 还继承 `oldNode.Children`。取决于意图可能是双表示或有意簿记。**（标注：需确认意图。）**

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 中 | `timeline.go` 110-150 | Summary 全零（collector 不设 EndAt/Duration） |
| 低 | `recorder.go` 17/41 | memManager 字段未读 |
| 低 | `genealogy.go` 108 | 父 children 未清理复活节点 |
