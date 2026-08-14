# 模块分析报告：`internal/agents`（Agent 与多智能体协作）

> 分析范围：`internal/agents/`（49 个 Go 文件），含 leader/ 与 sub/ 子包

---

## BUG（高置信度）

### 1. `sub/agent.go` `ProcessStream` 状态竞争导致并发处理
- **位置**：`sub/agent.go`，约 337-363 行
- **说明**：`ProcessStream` 在 `status` 翻转为 `Ready` 之后才启动 goroutine 再次将其设为 `Busy`。若两个 `ProcessStream` 并发调用，第二个可能在状态仍为 `Ready` 时进入，造成**同一 Agent 被并发处理**。中间状态窗口期缺少原子性保护。
- **状态**：✅ 已核实修复（2026-08-14）——当前实现已在 `a.mu.Lock()` 内检查 `Ready` 并同步置 `Busy`（TOCTOU 注释），`defer setStatus(Ready)` 恢复，报告条目过时。

### 2. `service_impl.go` `ListAgents` 分页 total 错误
- **位置**：`service_impl.go`，约 266 行
- **说明**：`total = len(agents)`，而 `agents` 已经是分页后的切片，因此 `total` 返回的是**当前页的元素数**而非数据库中总记录数，分页元数据（total/totalPages/hasMore）全部错误。
- **状态**：✅ 已修复（2026-08-14）——`AgentRepository` 新增 `Count(ctx, filter)`（过滤后、分页前计数）；`MemoryRepository`/mock 实现；`ListAgents` 改用 `repo.Count` 计算 total。

### 3. `executor.go` `executeWithLLM` 非幂等工具重试的保守中止
- **位置**：`executor.go`，约 336 行
- **说明**：重试时若检测到非幂等工具，会提前中止（`"retry aborted"`）。即便第 1 次重试本可成功也会被中断，属过度保守。且 `errors.Wrap(lastErr, "all retries failed")` 在极端边界（`lastErr` 为 nil）可能包装 nil，但实际 `maxRetries` 恒 ≥3 使循环至少执行一次，风险低。

---

## LOGIC（逻辑问题）

### 4. `checkpoint.go` `GetLatest` 文档与行为不符
- **位置**：`checkpoint.go`
- **说明**：文档注释声称"未找到时返回 nil, nil"，但实际返回 `ErrNotFound`。文档与实现不一致。

### 5. `recovery.go` `RecoverStaleTasks` 的 `RETURNING id` 可能不是真正的任务 ID
- **位置**：`recovery.go`
- **说明**：SQL 为 `RETURNING id, session_id, status, error`，扫描到 `TaskID` 字段。若表的 `id` 是自增主键而非任务逻辑 ID，则 `TaskID` 被填充为数字主键，而非任务 ID。**（需结合表结构确认，标注为潜在问题。）**
- **状态**：⚠️ 待确认（2026-08-14）——查询基于 `TaskResultsTable` 的 `id` 列；当前仓库未检索到该表 DDL，无法确认 `id` 是自增主键还是任务逻辑 ID。属低置信度潜在问题，需结合实际 schema 判定。

### 6. `sub/agent.go` 的 `handleTaskMessage` / `handleAckMessage` 是空实现
- **位置**：`sub/agent.go`
- **说明**：这两个消息处理器 `return nil` 不做任何处理，接收 task/ack 消息不会产生任何效果。功能缺口（可能是 TODO 占位）。

---

## DEAD_CODE

### 7. `Handoff` 相关方法仅测试使用
- **位置**：leader/handoff.go
- **说明**：`HasArtifact`、`ArtifactOfType`、`Size`、`WithMetadata`、`String`（以及 `ArtifactRef.String()`）仅在测试中使用，生产代码未调用。`NewHandoff`、`WithContext`、`WithArtifact` 是活跃使用的。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| 高 | `sub/agent.go` 337-363 | `ProcessStream` 状态竞争，可能并发处理同一 Agent |
| 高 | `service_impl.go` 266 | `ListAgents` total 用分页后的 len，分页元数据错误 |
| 中 | `executor.go` 336 | 非幂等重试过度保守 + 边界 nil wrap |
| 中 | `recovery.go` | `RETURNING id` 可能非任务 ID（需确认表结构） |
| 低 | checkpoint/GetLatest | 文档与行为不符 |
| 低 | sub/handlers | task/ack 处理器为空实现 |
| 低 | handoff.go | 多个方法仅测试使用 |
