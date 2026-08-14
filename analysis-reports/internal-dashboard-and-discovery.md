# 模块分析报告：`internal/dashboard` 与 `internal/discovery`

> 分析范围：`internal/dashboard/`（22 个文件）、`internal/discovery/`（18 个文件）

---

# `internal/dashboard`

## BUG（高置信度）

### 2. `api_handlers.go` `handleWS` 未 nil-check `a.hub`
- **位置**：`api_handlers.go` 182-186 行
- **说明**：`NewWSClient(a.hub, conn)` 和 `a.hub.Register(client)` 在 `APIv2` 未配 hub（构造参数可传 nil）时会 panic。其它 handler 都对可选 provider 做 nil check，唯独这个没有。

---

## LOGIC（逻辑问题）

### 3. `api_handlers.go` `handleArenaMetrics` failover 计数错误
- **位置**：`api_handlers.go` 494-499 行
- **说明**：`failoverCount` 通过统计 `history` 中**成功**条目计算。但 `history`（`a.arena.History()`）包含任意 arena 动作（kill、pause、slow 等），并非只有 failover。把所有成功都算作 failover 会过度上报。指标名与实际计算不符。

### 4. `intelligence.go` `Health()` 双重锁
- **位置**：`intelligence.go` 217-239 行
- **说明**：`Health()` 先取 `e.mu.RLock()` 释放后再重取。`agentState` 指针稳定（创建后不替换），功能正确，但双锁不必要且若 `agentState` 未来被替换则脆弱。

---

## DEAD_CODE

### 5. `api_handlers.go` kill 动作走 catch-all 分发
- **位置**：`api_handlers.go` 407-411 行
- **说明**：`handleArenaAgentFault` 中的 "kill" 回退与 `ArenaActionKillAgent` 路径重复，但 `ArenaActionKillAgent` 常量只在 handleArenaAgentFault 内处理——无独立 kill 路由。功能正常，但 kill 处理与其它 fault 类型（走 `actionMap`）分发不一致。

---

# `internal/discovery`

## BUG（高置信度）

### 6. `providers/binary.go` allowlist 仅校验 basename，symlink 可绕过
- **位置**：`providers/binary.go` 44-46、184 行
- **说明**：`isAllowedBinary(path)` 用 `knownMCPBinariesSet[filepath.Base(path)]`，`runCommand`（184 行）也校验 basename。恶意 symlink 指向 basename 相同但路径不同的二进制（如 `codegraph` → `/evil/codegraph`）会通过检查。`health.go` 用 `EvalSymlinks` 处理此问题，但 `binary.go` provider 不解析 symlink，可被绕过。**安全相关不一致。**

### 7. `engine.go` `StartAutoDiscovery` goroutine 无法停止/join
- **位置**：`engine.go` 210-231 行
- **说明**：启动无 guard 的 goroutine，无 `WaitGroup`、无 `done` channel，只尊重 `ctx.Done()`。调用方无法阻塞等待完成或检测泄漏（ctx 存活期间非泄漏，但无法 await）。

---

## LOGIC（逻辑问题）

### 8. `identity.go` `mergeRecords` 平局时非确定性
- **位置**：`identity.go` 46-54 行
- **说明**：元数据合并条件 `if _, ok := mergedMeta[k]; !ok || r.Confidence >= best.Confidence` 依赖 `best` 是最高置信度记录。map 迭代顺序非确定，若两记录并列最高置信度，后处理者获胜，非确定性。

### 9. `discovery.go` `HealthMsg`/`Latency` 字段部分死
- **位置**：`discovery.go` 71-78、`health.go` 57 行
- **说明**：`DiscoveredService.CheckedAt`、`HealthMsg`、`Healthy` 在 `CheckHealth` 设置，但 `HealthMsg` 从不出现在任何输出/API；`Latency` 在 `HealthStatus` 计算但从不存到 `DiscoveredService`。部分死字段。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | dashboard `api_handlers.go` 182 | handleWS 未 nil-check hub |
| 中 | discovery `binary.go` 44 | symlink 绕过 allowlist（安全） |
| 中 | dashboard `api_handlers.go` 494 | failover 计数把任意成功当 failover |
| 低 | monitoring nil deref / discovery goroutine 生命周期 / identity 平局非确定 | 若干低优先级项 |
