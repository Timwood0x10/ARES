# 模块分析报告：`internal/storage`（存储层）

> 分析范围：`internal/storage/`（98 个 Go 文件），含 postgres/、mysql/、sqlite/ 等子包

---

## BUG（高置信度）

---

## LOGIC（逻辑问题）

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
