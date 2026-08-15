# 模块分析报告：`internal/storage`（存储层）

> 分析范围：`internal/storage/`（98 个 Go 文件），含 postgres/、mysql/、sqlite/ 等子包

---

## BUG（高置信度）

### 1. `postgres/pool.go` `QueryRow` 吞掉真实连接错误并伪装成"No rows"
- **位置**：`postgres/pool.go` 313-321 行
- **说明**：当 `p.Get(ctx)` 获取连接失败时，函数不返回该错误，而是打日志并返回一个包装 `SELECT 1 WHERE 1=0` 的 `ManagedRow`。所有调用方随后 `.Scan(...)` 得到 `sql.ErrNoRows` 而非真实的连接/池错误。**瞬时数据库故障被静默报告为"记录未找到"**，是真实的错误处理 bug。
- **状态**：✅ 已核实修复（2026-08-14）——获取连接失败时返回 `&ManagedRow{err: err}`（真实错误经 `Scan` 透出），注释明确说明"old behaviour masked a real pool error as sql.ErrNoRows"；报告条目过时。

### 2. `postgres/tenant_guard.go` `set_config(..., true)`（SET LOCAL）在无事务时无效
- **位置**：`postgres/tenant_guard.go` 34-48、88-95 行
- **说明**：`set_config('app.tenant_id', $1, true)` 实现 `SET LOCAL` 语义，PostgreSQL 只在**事务块内**生效。`Pool.Exec` 单连接执行语句后释放，从未在事务内运行。因此租户上下文从未真正设置到池连接上，RLS 未通过此路径生效。代码注释（24-28 行）已承认此 RLS 方案无效，但代码仍具误导性。
- **状态**：✅ 已修复（2026-08-15）——`Pool.QueryWithTenant` 改为连接级 `set_config('app.tenant_id', $1, false)`（is_local=false，autocommit 下对后续查询生效），`ManagedRows.Close` 归还连接前清除租户上下文（`set_config('app.tenant_id', '', false)`）防池化泄漏；`ExecWithTenant` 本就事务包裹（BeginTx→set_config→Commit）无需改动。新增 `TestQueryWithTenantRejectsEmptyTenant` 守护；完整 RLS 隔离语义（真实 Postgres 环境）验证需真实 DB（mock pool db=nil），已如实标注。

---

## LOGIC（逻辑问题）

### 3. `postgres/embedding_queue.go` 回退去重键忽略 `task.Table`
- **位置**：`postgres/embedding_queue.go` 31、144-147 行
- **说明**：`generateDedupeKey` 的回退路径使用 `content|model|version`，忽略 `task.Table`。不同表的相同内容会碰撞到同一 `dedupe_key`，而队列是跨表通用的（有 `table_name` 列），可能导致跨表错误去重。
- **状态**：✅ 已修复（2026-08-14）——`generateDedupeKey` 回退键加入 `task.Table`（`table|content|model|version`），跨表同内容不再碰撞，build/test 通过。

### 4. `postgres/write_buffer.go` 上下文取消时丢弃缓冲项
- **位置**：`postgres/write_buffer.go` 112-125、402-433 行
- **说明**：在 `ctx.Done()` 分支只刷新当前累积的 `batch`，`b.buffer` 通道中尚未拉入 batch 的项被静默丢弃。`Stop()` 通过 `!ok` 分支正确排空，但父上下文被外部取消时（`errgroup.WithContext`）会丢失排队项。

### 5. `postgres/vector.go` 缺少 `::vector` 类型转换
- **位置**：`postgres/vector.go` 76-88、153-158 行
- **说明**：`VectorSearcher` 把 embedding 作为原始 JSON 字节传入 `embedding <=> $1` 和 `INSERT ... $2`，**没有**像其它仓库（如 `knowledge_repository.go` 123 行 `$3::vector`）那样加 `::vector` 转换。pgvector 需要类型转换才能正确操作。

---

## DEAD_CODE

### 6. `postgres/pool.go` `var _ = errors.ErrDBConnectionFailed` 哑引用
- **位置**：`postgres/pool.go` 373 行
- **说明**：该哑引用只为保留 import，`ErrDBConnectionFailed` 未真正使用。import 被"糊弄"而非实际使用。

### 7. `repository.go` `GetSessionWithResult` 未找到时返回 nil 而非错误
- **位置**：`repository.go` 174-177 行
- **说明**：`ErrRecordNotFound` 被显式忽略，`result` 为 nil 直接返回。注释表明是故意的，但调用方需自行判 nil，容易遗漏。**（标注为设计取舍。）**

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `postgres/pool.go` 313-321 | QueryRow 吞真实错误，伪装成 NoRows |
| **高** | `postgres/tenant_guard.go` 34-48 | SET LOCAL 无事务无效，RLS 未生效 |
| 中 | `postgres/embedding_queue.go` 31 | 去重键忽略 table |
| 中 | `postgres/write_buffer.go` 112 | 取消时丢缓冲项 |
| 中 | `postgres/vector.go` 76 | 缺 `::vector` 转换 |
| 低 | `postgres/pool.go` 373 | 哑 import 引用 |
