# 模块分析报告：`internal/knowledge`（知识库）

> 分析范围：`internal/knowledge/`（108 个 Go 文件），含 store/、provider/、linker/、workflow/ 等子包

---

## BUG（高置信度）

### 1. PostgreSQL `HybridSearch` 表示查询参数与占位符不匹配，向量召回必然失败
- **位置**：`store/postgres/store.go` 391-400 行
- **说明**：查询只声明两个占位符：
  ```sql
  SELECT ... FROM akf_representations WHERE object_id = ANY($1) AND model = $2
  ```
  但参数切片构造为 `len(ids) + 1` 个元素（先 append N 个 id，再 append model），且只把 `repArgs[0]` 覆盖为 ID 数组：
  ```go
  repArgs := make([]interface{}, 0, len(ids)+1)
  for _, id := range ids { repArgs = append(repArgs, id) }  // N 个
  repArgs = append(repArgs, req.Model)                      // N+1 个
  repArgs[0] = pqStringArray(ids)                           // 只替换索引 0
  ```
  后果：(a) `$2`（model）绑到 `repArgs[1]`——即第二个对象 ID 字符串，模型过滤把 `model` 列与对象 ID 比较，永远不匹配；(b) 多余 `N-1` 个参数被 lib/pq/pgx 拒绝。**只要 `len(ids)>0 && req.Model!=""`，每次 `HybridSearch` 都失败**。MySQL/SQLite/内存 store 都正确，仅 Postgres 错。
- **状态**：✅ 已核实修复（2026-08-14）——现构造 `repArgs := []interface{}{pqStringArray(ids), req.Model}`（恰两参数），注释明确说明此前错误（N 个 id + model、只覆盖索引 0）；报告条目过时。

---

## LOGIC（逻辑问题）

### 2. `store/memory/store.go` `Query` 先 Limit 后 Offset，破坏分页
- **位置**：`store/memory/store.go` 103-116 行
- **说明**：
  ```go
  if q.Limit > 0 && len(result) > q.Limit { result = result[:q.Limit] }  // 先 Limit
  if q.Offset > 0 { ... result[q.Offset:] }
  ```
  先 Limit 再 Offset，任何带 Offset+Limit 的请求最多只返回第 1 页（Offset ≥ Limit 时返回空）。SQL 版本正确使用 `LIMIT ? OFFSET ?`。
- **状态**：✅ 已核实修复（2026-08-14）——改为 offset-first + limit（`q.Offset` 先切片、`q.Limit` 后截断），注释明确说明"previous order (limit-then-offset) produced wrong pagination"；报告条目过时。

### 3. `pipeline.go` `mergeConfidence` 可返回 >1.0 的置信度
- **位置**：`pipeline.go` 252-257 行
- **说明**：
  ```go
  if a > b { return a + (b * 0.1) }
  return b + (a * 0.1)
  ```
  a=1.0,b=1.0 时返回 1.1，超出文档声明的 `[0,1]` 范围。结果直接赋给 `obj.Confidence`，下游排序可能收到越界值。
- **状态**：✅ 已核实修复（2026-08-14）——`mergeConfidence` 结果 clamp 到 [0,1]（`>1` 归 1、`<0` 归 0），注释说明此前公式可超 1.0；报告条目过时。

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
