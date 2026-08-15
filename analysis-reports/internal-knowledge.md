# 模块分析报告：`internal/knowledge`（知识库）

> 分析范围：`internal/knowledge/`（108 个 Go 文件），含 store/、provider/、linker/、workflow/ 等子包

---

## BUG（高置信度）

### 4. `workflow/workflow.go` 配置的 `ForGraph` 在 `MaxTokens` 未设置时被丢弃
- **位置**：`workflow/workflow.go` 89-96 行
- **说明**：
  ```go
  budget := knowledge.TokenBudget{MaxTokens: cfg.MaxTokens, ForGraph: cfg.ForGraph}
  if cfg.MaxTokens <= 0 {
      budget = knowledge.TokenBudget{MaxTokens: 5000, ForGraph: 3000, Reserved: 2000}
  }
  budget.Reserved = budget.MaxTokens - budget.ForGraph
  ```
  - 若调用方只设 `for_graph`（如 5000）而 `max_tokens=0`，整个 budget 被覆盖为 5000/3000 默认值，`ForGraph` 被忽略。
  - 若 `MaxTokens>0` 但 `ForGraph=0`，`Reserved = MaxTokens - 0 = MaxTokens`，图预算为 0。
  - 若 `MaxTokens < ForGraph`，`Reserved` 变负数。
  **`ForGraph` 字段的可靠性取决于 `MaxTokens`，且负 `Reserved` 无保护。**

---

## CONCURRENCY

### 5. `provider/mysql/provider.go` 错误通道死锁风险
- **位置**：`provider/mysql/provider.go` 112-149 行
- **说明**：`errCh` 缓冲容量为 1，但 `defer rows.Close()` 中又发送一次错误。若 scan 错误与 close 错误同时发生，缓冲已满，发送会**永久阻塞**，导致 provider goroutine 泄漏且 `objCh`/`errCh` 永不关闭。其它 provider 也有此模式，但 MySQL 的 defer-close 发送使其可达。

---

## DEAD_CODE

### 6. `relation.go` 未使用的关系常量
- **位置**：`relation.go` 32-44 行
- **说明**：`RelUses`、`RelImplements`、`RelLearnsFrom`、`RelCauses` 在包内无生产者（`RelGeneratedBy`/`RelDecidedBy` 由 linker 产生）。因是导出常量且经 API 再导出，属边界情况。

### 7. `pipeline.go` 无法到达的 nil 检查
- **位置**：`pipeline.go` 140-142 行
- **说明**：`if obj == nil` 返回 "all normalizers returned nil"——循环内 `obj` 只在 `normalized != nil` 时赋值，永不 nil。纯防御性死代码。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `store/postgres/store.go` 391-400 | HybridSearch 参数与占位符不匹配，Postgres 向量召回必失败 |
| 中 | `store/memory/store.go` 103-116 | 先 Limit 后 Offset，破坏分页 |
| 中 | `pipeline.go` 252-257 | mergeConfidence 可返回 >1.0 |
| 中 | `workflow/workflow.go` 89-96 | ForGraph 被丢弃 / Reserved 可为负 |
| 中 | `provider/mysql/provider.go` | errCh 缓冲 1 + defer 二次发送可死锁 |
| 低 | `relation.go` | 4 个关系常量无生产者 |
| 低 | `pipeline.go` 140 | 不可达的 nil 检查 |
