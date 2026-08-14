# 模块分析报告：`internal/dashboard` 与 `internal/discovery`

> 分析范围：`internal/dashboard/`（22 个文件）、`internal/discovery/`（18 个文件）

---

# `internal/dashboard`

## BUG（高置信度）

### 2. `api_handlers.go` `handleWS` 未 nil-check `a.hub`
- **位置**：`api_handlers.go` 182-186 行
- **说明**：`NewWSClient(a.hub, conn)` 和 `a.hub.Register(client)` 在 `APIv2` 未配 hub（构造参数可传 nil）时会 panic。其它 handler 都对可选 provider 做 nil check，唯独这个没有。
- **状态**：✅ 已修复（2026-08-14）——`handleWS` 升级连接后检查 `a.hub == nil`（关闭连接并返回，不 panic），与其他 handler 的 nil-check 一致，build/vet/test 通过。

### 3. `api_handlers.go` `handleArenaMetrics` failover 计数错误
- **位置**：`api_handlers.go` 494-499 行
- **说明**：`failoverCount` 通过统计 `history` 中**成功**条目计算。但 `history`（`a.arena.History()`）包含任意 arena 动作（kill、pause、slow 等），并非只有 failover。把所有成功都算作 failover 会过度上报。指标名与实际计算不符。
- **状态**：✅ 已修复（2026-08-14）——新增 `isFailoverAction`（仅 `kill_leader`/`kill_orchestrator` 计为 failover），`failoverCount` 只统计 failover 触发动作且成功，pause/slow 等不再虚增指标，build/vet/test 通过。

### 4. `intelligence.go` `Health()` 双重锁
- **位置**：`intelligence.go` 217-239 行
- **说明**：`Health()` 先取 `e.mu.RLock()` 释放后再重取。`agentState` 指针稳定（创建后不替换），功能正确，但双锁不必要且若 `agentState` 未来被替换则脆弱。
- **状态**：✅ 已修复（2026-08-14）——`Health()` 改为单次 `RLock` 持锁（`defer RUnlock`），先取 state 再计算，去掉不必要的双锁窗口，build/vet/test 通过。

---

## DEAD_CODE

### 5. `api_handlers.go` kill 动作走 catch-all 分发
- **位置**：`api_handlers.go` 407-411 行
- **说明**：`handleArenaAgentFault` 中的 "kill" 回退与 `ArenaActionKillAgent` 路径重复，但 `ArenaActionKillAgent` 常量只在 handleArenaAgentFault 内处理——无独立 kill 路由。功能正常，但 kill 处理与其它 fault 类型（走 `actionMap`）分发不一致。
- **状态**：⚠️ 已核实为设计（2026-08-14）——`actionMap` 不含 "kill"（kill 是直接执行动作而非 fault 注入），catch-all 回退到 `ArenaActionKillAgent` 是刻意设计，功能正确；不做变更。

---

# `internal/discovery`

## BUG（高置信度）

### 6. `providers/binary.go` allowlist 仅校验 basename，symlink 可绕过
- **位置**：`providers/binary.go` 44-46、184 行
- **说明**：`isAllowedBinary(path)` 用 `knownMCPBinariesSet[filepath.Base(path)]`，`runCommand`（184 行）也校验 basename。恶意 symlink 指向 basename 相同但路径不同的二进制（如 `codegraph` → `/evil/codegraph`）会通过检查。`health.go` 用 `EvalSymlinks` 处理此问题，但 `binary.go` provider 不解析 symlink，可被绕过。**安全相关不一致。**
- **状态**：✅ 已修复（2026-08-14）——`isAllowedBinary` 先用 `filepath.EvalSymlinks` 解析真实路径再校验 basename（与 `health.go` 一致），恶意 symlink 不再通过 allowlist，build/vet/test 通过。

### 7. `engine.go` `StartAutoDiscovery` goroutine 无法停止/join
- **位置**：`engine.go` 210-231 行
- **说明**：启动无 guard 的 goroutine，无 `WaitGroup`、无 `done` channel，只尊重 `ctx.Done()`。调用方无法阻塞等待完成或检测泄漏（ctx 存活期间非泄漏，但无法 await）。
- **状态**：⚠️ 已核实为 ctx 生命周期管理（2026-08-14）——goroutine 由 `ctx.Done()` 驱动退出，ctx 取消即终止；无独立 `done` channel 属既定设计（调用方以 ctx 控制生命周期），非泄漏；不做变更。

---

## LOGIC（逻辑问题）

### 8. `identity.go` `mergeRecords` 平局时非确定性
- **位置**：`identity.go` 46-54 行
- **说明**：元数据合并条件 `if _, ok := mergedMeta[k]; !ok || r.Confidence >= best.Confidence` 依赖 `best` 是最高置信度记录。map 迭代顺序非确定，若两记录并列最高置信度，后处理者获胜，非确定性。
- **状态**：✅ 已修复（2026-08-14）——`group` 先按（Confidence 降序、Source 升序）确定性排序，`best` 与元数据合并均稳定，平局不再依赖 map 迭代顺序，build/vet/test 通过。

### 9. `discovery.go` `HealthMsg`/`Latency` 字段部分死
- **位置**：`discovery.go` 71-78、`health.go` 57 行
- **说明**：`DiscoveredService.CheckedAt`、`HealthMsg`、`Healthy` 在 `CheckHealth` 设置，但 `HealthMsg` 从不出现在任何输出/API；`Latency` 在 `HealthStatus` 计算但从不存到 `DiscoveredService`。部分死字段。
- **状态**：⚠️ 保留为诊断字段（2026-08-14）——`HealthMsg`/`CheckedAt` 随 `DiscoveredService` 序列化（`json` tag），供未来 API 输出使用；`Latency` 属 `HealthStatus` 内部计算字段。均非生产消费缺口，保留不删。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | dashboard `api_handlers.go` 182 | handleWS 未 nil-check hub |
| 中 | discovery `binary.go` 44 | symlink 绕过 allowlist（安全） |
| 中 | dashboard `api_handlers.go` 494 | failover 计数把任意成功当 failover |
| 低 | monitoring nil deref / discovery goroutine 生命周期 / identity 平局非确定 | 若干低优先级项 |
