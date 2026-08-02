# Deep Code Review — `internal/ares_memory`

范围：memory 管理、对话蒸馏（distillation）、上下文构建（context）、生产级 `ProductionMemoryManager`。
方法：全量阅读所列文件，对 High/Medium 逐项 re-read 真实代码行确认；用 `grep` 核对调用点以确定 REACHABLE / DEAD。
说明：仅审查、未修改任何代码。

---

## F1 [Medium] `cosineSimilarity` 缺少 NaN/Inf 防护（resolver.go / rag.go）
**可达性：REACHABLE** — 这是生产路径实际的相似度函数（冲突检测 `DetectConflict`、RAG `Search` 均调用）。

`internal/ares_memory/distillation/resolver.go:150-172`
```go
func (r *ConflictResolver) cosineSimilarity(v1, v2 []float64) float64 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0.0
	}
	...
	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}
	return dotProduct / math.Sqrt(norm1*norm2)   // 无 NaN/Inf 检查
}
```
`internal/ares_memory/context/rag.go:240-259` 的包级 `cosineSimilarity` 同样无防护。

**失效模式**：若任一向量含 `NaN`/`Inf`（ embedding 模型异常、维度错位、零向量绕过 `norm==0` 之外的古怪值），结果为 `NaN`。`NaN > threshold` 恒为 `false` → `DetectConflict` 永不报冲突 → 重复记忆被各自存储；RAG `Search` 的排序因 `NaN` 比较未定义而错乱。**对比**：`manager_impl.go:706` 已有 `math.IsNaN/IsInf` 防护，证明此防护是本应存在的。
**修复**：在两个函数末尾 `return` 前加入 `if math.IsNaN(result) || math.IsInf(result, 0) { return 0.0 }`。

---

## F2 [Medium] 会话缓存 "LRU" 实为 FIFO，且 `SessionData` 永不更新 → 多用户归属丢失
**可达性：REACHABLE** — `CreateSession`/`AddMessage` 是热路径。

`internal/ares_memory/production_manager.go:347-360`
```go
if len(m.sessionCache) > m.maxCacheSize {
	var oldestKey string
	var oldestTime time.Time
	for k, v := range m.sessionCache {
		if oldestKey == "" || v.CreatedAt.Before(oldestTime) {
			oldestKey = k
			oldestTime = v.CreatedAt
		}
	}
	if oldestKey != "" {
		delete(m.sessionCache, oldestKey)
	}
}
```
`SessionData`（结构定义见 `production_manager.go:84-90`）的 `UpdatedAt`/`MessageCount` 在 `CreateSession` 后**再未被任何代码更新**。因此：
1. 注释声称的 "simple LRU" 实际是按 `CreatedAt` 淘汰的 FIFO，活跃会话与冷会话一视同仁。
2. `AddMessage`（`production_manager.go:403-407`）从缓存读 `userID`；若会话被淘汰或未经 `CreateSession` 建立，则 `userID==""` 落到 `"anonymous"`（`production_manager.go:411-415`）。

**失效模式**：缓存压力下，消息被归属到 `anonymous`，按用户维度的历史/检索失真，多租户用户隔离被削弱。
**修复**：淘汰应基于 `UpdatedAt`（并在 `AddMessage`/`GetMessages` 命中时更新 `UpdatedAt`/`MessageCount`）；或改为真 LRU（链表/access 时间戳）。

---

## F3 [Medium] `RAG.Delete` 与 `Add` 锁顺序不对称 + O(n) 全量索引重建
**可达性：REACHABLE** — `RAG` 在 `usePersistent` 模式下被检索/维护调用。

`internal/ares_memory/context/rag.go:78-98`（`Add`）先 `Lock()` 再做 pgvector I/O；`rag.go:183-202`（`Delete`）**先**做 pgvector I/O **后** `Lock()`：
```go
func (r *RAG) Delete(ctx context.Context, id string) error {
	if r.usePersistent && r.vectorSearch != nil {
		if err := r.vectorSearch.DeleteEmbedding(ctx, r.tableName, id); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
	r.index.entries = make([]*KnowledgeEntry, 0)   // 每次重建全部索引
	for _, entry := range r.entries {
		r.index.entries = append(r.index.entries, entry)
	}
	return nil
}
```
`Add` 的注释明确"持锁以保持内存与 pgvector 一致"，而 `Delete` 违反该约定：在 `Delete` 的 pgvector 删除 与 取锁之间，`Add` 可能持锁写入 pgvector+内存，造成两端顺序交错。
**失效模式**：并发增删场景下内存索引与持久化向量状态偶发不一致；`Delete` 每次 O(n) 重建索引，规模大时放大锁持有时间。
**修复**：`Delete` 改为与 `Add` 一致的"先 `Lock()` 再 I/O"；`evictOldestLocked` 已用切片移位，索引重建应改为同样增量维护而非全量重扫。

---

## F4 [Low/Medium] 分类器将 "rule" 内容误标为 `MemoryProfile`
**可达性：REACHABLE** — `classifyAndScorePhase` 调用 `ClassifyMemory`。

`internal/ares_memory/distillation/classifier.go:49-54`
```go
if c.isRule(content) {
	return MemoryProfile
}
return MemoryKnowledge
```
`GetMemoryTypeFromString`（`classifier.go:123-124`）同样把 `"rule" → MemoryProfile`。`MemoryProfile` 语义上是"用户画像/自我介绍"，把包含 "config/policy/rule/constraint" 的内容存为 `MemoryProfile`，在 `convertMemoryToExperience`、检索与呈现时会被当作用户画像处理。
**失效模式**：规则类记忆类型错乱，检索召回与展示语义错误；且 `MemoryProfile` 与 `isUserProfile` 最高优先级路径共用类型，易混淆。
**修复**：为规则新增独立类型（或归入 `MemoryKnowledge`），并在 `api/experience/types.go` 的 `String()`/`GetMemoryTypeFromString` 中保持一致。

---

## F5 [Low] `ResolveConflict` 对近似重复记忆执行 `KeepBoth`
**可达性：REACHABLE** — 冲突阶段调用。

`internal/ares_memory/distillation/resolver.go:54-68`
```go
if newMemory.Confidence > oldMemory.Confidence {
	return ReplaceOld
}
return KeepBoth
```
只要旧记忆置信度 ≥ 新记忆（即便两者相似度 > `conflictThreshold`，几乎相同内容），就保留两份。
**失效模式**：近似重复记忆累积，检索噪音与存储膨胀（与 F1 的 NaN 缺失叠加会进一步放任重复）。
**修复**：当 `DetectConflict` 已判定相似度超阈值时，`ResolveConflict` 应优先按置信度覆盖（ReplaceOld/Merge），而非默认 KeepBoth。

---

## F6 [Low] `GetLatestSessionForLeader` 脆弱的错误字符串匹配
**可达性：REACHABLE** — leader agent 调用以恢复会话。

`internal/ares_memory/production_manager.go:773-779`
```go
if err := row.Scan(&sessionID); err != nil {
	if err == sql.ErrNoRows || strings.Contains(err.Error(), "no rows in result set") {
		return "", nil
	}
	return "", errors.Wrap(err, "get latest session for leader")
}
```
**失效模式**：依赖驱动错误字符串 `"no rows in result set"`。若驱动版本/封装改变措辞，本应返回 `("","nil")` 的"无检查点"会被误报为错误，中断 leader 恢复流程。
**修复**：统一使用 `errors.Is(err, sql.ErrNoRows)` / pgx 的 `pgx.ErrNoRows`，移除字符串匹配。

---

## F7 [Low] `NewMinimalMemoryManager` 返回带 nil 存储字段的 `*ProductionMemoryManager`
**可达性：REACHABLE**（bootstrap/evolution 路径）。

`internal/ares_memory/memory_patcher.go:63-67`
```go
func NewMinimalMemoryManager() *ProductionMemoryManager {
	return &ProductionMemoryManager{
		config: DefaultMemoryConfig(),
	}
}
```
`dbPool`/`tenantGuard`/`embeddingClient`/`ctxCleaner`/`conversationRepository` 全为 nil。注释声明"仅用于读取 config"，故当前安全；但返回的是**完整类型** `*ProductionMemoryManager`，任何对其余方法（如 `BuildPromptMessages`、`AddMessage`）的调用都会 nil-panic。
**修复**：返回窄接口或显式标记"config-only"子类型，避免将不满的 `*ProductionMemoryManager` 暴露给调用方。

---

## F8 [Low] `SetDefaultTenantID` 疑似死代码
**可达性：DEAD**（仅定义，无调用方）。

`internal/ares_memory/manager_impl.go:673-675` 定义 `func (m *memoryManager) SetDefaultTenantID(...)`，但全仓 `grep` 无调用点；该方法属于已被 `ProductionMemoryManager` 大量取代的 `memoryManager`。
**失效模式**：维护负担/误导；若未来被接上但 `defaultTenantID` 与 `getCurrentTenantID` 的读取路径不一致，会引入租户错乱。建议确认后保留（补测试）或删除。

---

## 验证安全（Verified Safe）
- **S1 `PushService.Start/Stop` 生命周期**：`runMu` 保护 `isRunning`/`cancelFn`/`doneCh`，`Stop` 在 `doneCh` 上等待避免提前关闭 channel；`scheduledLoop`/`eventLoop` 的 `defer` 关闭 `doneCh` 且仅在持 `runMu` 时操作 → 无重复 close、无竞态（`push/service.go:278-374`）。
- **S2 内存 map 均经 RWMutex 保护并副本返回**：`UserMemory`/`SessionMemory`/`TaskMemory` 的读写均在锁内，且读取返回 `copy(...)` 副本（`context/user.go`、`context/session.go`、`context/task.go`）→ 无数据竞争。
- **S3 `manager_impl.go:685` 的 `cosineSimilarity` 具备 NaN/Inf 防护**（`math.IsNaN/IsInf`）→ 对比 F1 的正确实现。
- **S4 `sessionCache` 访问一致加锁**：`AddMessage` 读用 `RLock`（`production_manager.go:403-407`）、`DeleteSession` 删除用 `Lock`（`:674-676`）、`CreateSession` 写入用 `Lock`（`:334-335`）→ 无对 `sessionCache` 的裸访问竞态。

## 总结
最应优先处理的是 **F1**（缺 NaN/Inf 防护，直接影响去重与检索质量，且已有正确对照实现）与 **F2**（缓存语义错误导致多用户归属丢失）。F3 为并发一致性隐患，F4-F8 为中等/低优先级逻辑与健壮性问题。无发现 Critical 级崩溃或数据损坏主路径漏洞。
