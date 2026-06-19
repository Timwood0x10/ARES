# Code Review Report: goagent — 问题确认审计

> 本报告基于自动化扫描生成，已逐行人工确认，删除误报和已修复项，修正严重度描述。
> 最终确认：**3 个 CRITICAL**、**4 个 HIGH**、**4 个 MEDIUM**、**4 个 LOW** 级别问题（共 15 个）。

---

## CRITICAL（必须立即修复）

### 1. SQL 注入 — `validateUserInput` 在线上代码中从未调用
- **文件:** `internal/storage/postgres/tenant_guard.go:41-45`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `SetTenantContext` 用 `fmt.Sprintf` 拼接用户可控的 `tenantID`：
```go
quotedID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
query := fmt.Sprintf("SET LOCAL app.tenant_id TO %s", quotedID)
```
`strings.ReplaceAll` 手动转义单引号对于 PostgreSQL 而言不足（Unicode/E-string 注入不受限）。
文件内 `validateUserInput` 已定义且有测试，但线上路径完全不调用。
- **修复建议:** 在 `SetTenantContext` 入口处调用 `validateUserInput(tenantID, 255)`。

### 2. MCP `dispatchResponse` 丢弃响应，`call()` 最终超时
- **文件:** `internal/mcp/client.go:318-323`
- **状态:** ✅ 代码存在，但原报告"永久阻塞"描述不准确
- **描述:** channel 缓冲区为 1，满时走 `default` 静默丢弃响应消息：
```go
case ch <- msg:
default:
```
但 `call()` 方法有 `context.WithTimeout` 保护（line 270），实际上调用方会**超时返回**而非永久阻塞。后果是**数据丢失 + 不必要的延迟**。
- **修复建议:** 改为 context 感知的阻塞发送，或增大 channel buffer。

### 3. `DLQ.GetByAgent` / `GetBySession` 在 nil Message 上解引用
- **文件:** `internal/protocol/ahp/dlq.go:82,96`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `DLQEntry.Message` 可为 nil，直接访问 `.AgentID` 引发 nil pointer panic：
```go
if entry.Message.AgentID == agentID {  // panic if Message == nil
```
- **修复建议:** 加 `entry.Message != nil && ...` 判断。

---

## HIGH（重要功能故障风险）

### 4. `RestoreAgent` 可向 map 存储 nil agent 导致 `Start()` panic
- **文件:** `internal/runtime/manager.go:303-318`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `recoverAgentState` 返回 `(nil, nil)` 时，`RestoreAgent` 未检查 nil 即存入 map 并调用 `agent.Start()`：
```go
newAgent, err := m.recoverAgentState(ctx, agentID, factory)
if err != nil {
    return err
}
// 未检查 newAgent == nil
m.agents[agentID] = &managedAgent{agent: newAgent, ...}
m.launchAgentGoroutine(agentCtx, agentID, newAgent)  // newAgent.Start() → panic
```
- **修复建议:** 在 `recoverAgentState` 返回后加 `if newAgent == nil` guard。

### 5. `ProcessStream` 中 `processingMu` 无 defer Unlock，panic 路径泄漏
- **文件:** `internal/agents/leader/agent.go:829-845`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `processingMu.Lock()` 后所有解锁路径都是显式 `Unlock()` 而非 `defer`。如果 `a.Start(ctx)` panic（line 833），mutex 永久锁定：
```go
a.processingMu.Lock()
// ...
if err := a.Start(ctx); err != nil {  // 若 panic 则泄漏
    a.processingMu.Unlock()
    return nil, err
}
// ...
a.processingMu.Unlock()
```
- **修复建议:** 入口处改 `a.processingMu.Lock()` 为 `defer a.processingMu.Unlock()`。

### 6. `StdioTransport.Receive` 先抢锁再检查 ctx，可能与 Close 死锁
- **文件:** `internal/mcp/transport_stdio.go:135-138`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `receiveMu.Lock()` 在检查 ctx 之前，若 `Close()` 也要获取 `receiveMu` 则可能死锁：
```go
func (t *StdioTransport) Receive(ctx context.Context) (*JSONRPCMessage, error) {
    t.receiveMu.Lock()  // 先抢锁
    defer t.receiveMu.Unlock()
    // ctx 检查在后面的 select 中
```
- **修复建议:** 在 `Lock()` 前检查 `ctx.Err()` 和 `started` 状态。

### 7. `NotifyAgentDead` 恢复持续失败时缺少指数退避
- **文件:** `internal/runtime/manager.go:432-484`
- **状态:** ✅ 问题存在（但原报告"goroutine 泄漏"不准确，已修正）
- **描述:** `resurrecting` 标志位模式**正确**（同步置 true，goroutine defer 清除），不会 goroutine 泄漏。但每次失败后 `resurrecting` 被清回 false，下次 `NotifyAgentDead` 立即重试，**缺少指数退避**：
```go
ma.resurrecting = true   // 同步设置
m.g.Go(func() error {
    defer func() {        // goroutine 结束时清除
        if entry, exists := m.agents[agentID]; exists {
            entry.resurrecting = false
        }
    }()
    if err := m.RestoreAgent(restoreCtx, agentID, factory); err != nil { ... }
})
```
- **修复建议:** 加指数退避（如 1s, 2s, 4s...），或限制重试频率。

---

## MEDIUM（代码异味与潜在问题）

### 8. `SSETransport.receiveLoop` 阻塞读不可中断，context 取消不生效
- **文件:** `internal/mcp/transport_sse.go:113-144`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `reader.ReadString('\n')` 是阻塞系统调用，context 取消无法打断。若 SSE 服务器连接空闲，goroutine 泄漏：
```go
for {
    line, err := reader.ReadString('\n')  // 阻塞，不响应 ctx
    if err != nil {
        if ctx.Err() != nil {     // 只在读错误后检查 ctx
            return nil
        }
```
- **修复建议:** `Close()` 中关闭 `resp.Body` 以中断阻塞读，或改用 io.Ctx 感知的读取方式。

### 9. `maskString` 短字符串只保留前缀，丢失后缀
- **文件:** `internal/security/sanitizer.go:295-297`
- **状态:** ✅ 代码存在，问题真实
- **描述:** 当 `length <= preserveLength*2` 时，只保留前缀，丢失后缀。例：`maskString("1234567890", 4)` 返回 `"1234******"` 而非 `"1234****90"`：
```go
if length <= preserveLength*2 {
    return s[:preserveLength] + strings.Repeat("*", length-preserveLength)  // 丢后缀
}
```
- **修复建议:**
```go
prefix := s[:preserveLength]
suffix := s[length-preserveLength:]
return prefix + strings.Repeat("*", length-preserveLength*2) + suffix
```

### 10. `maskSSN` 忽略输入，两个分支返回相同字面量
- **文件:** `internal/security/sanitizer.go:279-284`
- **状态:** ✅ 代码存在，问题真实
- **描述:** `cleaned` 计算后完全未用到返回值中，两个分支都返回 `"***-**-****"`：
```go
func maskSSN(match string) string {
    cleaned := regexp.MustCompile(`[^\d]`).ReplaceAllString(match, "")  // 未使用
    if len(cleaned) != 9 {
        return "***-**-****"
    }
    return "***-**-****"  // 相同字面量
}
```
- **修复建议:** 按原始输入分隔符保留输出格式（`***-**-****` 或 `***.***.****`）。

### 11. `validateUserInput` 是死代码（线上无人调用）
- **文件:** `internal/storage/postgres/security.go:81-105`
- **状态:** ✅ 代码存在，问题真实
- **描述:** 函数已定义、有测试，但与 `SetTenantContext` 一样，零个生产调用者。
- **修复建议:** 要么接入生产路径（见 #1），要么删除避免虚假安全感。

---

## LOW（建议改进）

### 12. `buildSummary` 找不到 agent 时用 `streamID` 回退充作 `AgentID`
- **文件:** `internal/events/compactor.go:187-195`
- **状态:** ✅ 代码存在，问题真实
- **描述:** 当 metadata 和 payload 均无 `agent_id` 时，用 `streamID` 回退，数据语义混淆（streamID 不是 agentID）。建议留空或使用 `"unknown"`。

### 13. `DefaultConfig` 用 `time.Now().UnixNano()` 做 ID，高并发同 ns 下会重复
- **文件:** `internal/agents/base/agent.go:106-114`
- **状态:** ✅ 代码存在，问题真实
- **描述:** 同纳秒多次调用 `DefaultConfig` 会生成相同 ID，可能导致 agent map 覆盖。建议改用 `uuid.New().String()` 或 sync/atomic 计数器。

### 14. `maskEmail` 对单字符 username（如 `"a@e.co"`）保留 2 字符会不当行为
- **文件:** `internal/security/sanitizer.go:228-242`
- **状态:** ✅ 代码存在，问题真实（轻微）
- **描述:** username 长度 1（小于 preserveLength 2）时 `maskString("a", 2)` 返回 `"*"`，整个 username 被全 mask。应 clamp 到 `min(2, len(username))`。

### 15. `maskAPIKey` / `maskToken` 最长匹配回退可能在 value 很短的场景误匹配
- **文件:** `internal/security/sanitizer.go:169-224`
- **状态:** ✅ 代码存在，问题真实（边缘场景）
- **描述:** 回退策略选最长的匹配作为 API key，若 value 很短而上下文中有更长的无关字符串，可能误匹配。建议优先使用紧邻关键词的匹配。

---

## 已排除项（误报或已修复，从原始报告删除）

| 原编号 | 问题 | 原因 |
|--------|------|------|
| CRITICAL #3 | `TaskMemory.Get` RLock + delete 竞争 | 当前代码 `task.go:132` 用的是 `m.mu.Lock()`（完整锁），误报 |
| MEDIUM #9 | `notifySubscribers` 阻塞 | 当前代码 `memory_store.go:262-271` 已用 `select { default: }` 非阻塞发送，已修复 |
| MEDIUM #14 | `GraphEventHub.nextID` 裸 int 溢出 | 当前代码 `graph_events.go:62-64` 有 `h.mu.Lock()` 保护，线程安全，误报 |
| LOW #18 | `truncate` 字节与 rune 双重检查 | 当前 `manager_impl.go:522-528` 只有 rune 检查，已简化，无双检查，误报 |
| LOW #19 | MCP `receiveLoop` 重试无退避 | `client.go:289-299` 的 receiveLoop 遇到错误即 return 无重试，不存在"重试逻辑"，误报 |

---

## 推荐修复优先级

1. **CRITICAL #1** — SQL 注入，最严重安全风险
2. **CRITICAL #3 (#4)** — DLQ nil 解引用，可导致线上 panic
3. **HIGH #4 (#5)** — RestoreAgent nil agent → Start panic
4. **HIGH #5 (#6)** — ProcessStream panic 路径 mutex 泄漏
5. **HIGH #6 (#7)** — StdioTransport 锁序死锁
6. **CRITICAL #2 (#2)** — MCP dispatchResponse 数据丢失
7. **MEDIUM #8 (#10)** — SSE receiveLoop goroutine 泄漏
8. **MEDIUM #9-11** — sanitizer bug（低风险）
9. **HIGH #7 (#8)** — 加指数退避（优化项）