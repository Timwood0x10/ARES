# Agents 模块 Bug 分析报告（修订版）

> **模块**: `internal/agents/leader`
> **分析时间**: 2026-06-24
> **分析范围**: Dead Code、Technical Debt、Potential Bugs
> **说明**: 基于源码逐项核实的修订版本，删除已修复/不准确的结论，补充遗漏问题。

---

## 📊 概览

| 类型 | 数量 | 严重程度 |
|------|------|----------|
| Dead Code / 废弃代码 | 1 | 🟡 中等 |
| Technical Debt | 3 | 🟠 较高 |
| Potential Bugs | 4 | 🔴 高 |

---

## 🚫 Dead Code（死代码 / 废弃代码）

### 1. `LeaderSupervisor` 已废弃但未删除

**位置**: `internal/agents/leader/supervisor.go:46-98`

**状态**: ✅ 确认

- `supervisor.go:46`：类型级 `// Deprecated:` 注释
- `supervisor.go:68`：构造函数级 `// Deprecated:` 注释
- `supervisor.go:78`：运行时 `slog.Warn("LeaderSupervisor is deprecated, ...")`
- 完整实现（`LeaderSupervisor` 结构体、`NewLeaderSupervisor`、`RegisterLeader`、`Start`、`Stop`、`handleFailover`、`doFailover`）仍保留在主包中

**影响**: 误导开发者使用已废弃 API，增加维护成本。

**建议**:
```go
// 方案 1（推荐）: 删除废弃代码（需确认测试已迁移）
// 方案 2: 移至 internal/agents/leader/supervisor_legacy.go，添加 build tag 限制
// 方案 3: 在 NewLeaderSupervisor 构造处保留运行时警告，文档标注废弃
```

---

## 🏗️ Technical Debt（技术债务）

### 1. 缺少 `sender` nil 校验

**位置**: `internal/agents/leader/dispatcher.go:50-57`

**状态**: ✅ 确认（报告中 4 项校验里唯一准确的一项）

`NewTaskDispatcher` 已校验 `agentRegistry`（`:41`）、`maxParallel`（`:44`）、`timeout`（`:47`），但 `sender` 未校验：

```go
func NewTaskDispatcher(agentRegistry map[models.AgentType]string, maxParallel int, timeout int, sender MessageSender) (TaskDispatcher, error) {
    if agentRegistry == nil { ... }
    if maxParallel <= 0 { ... }
    if timeout <= 0 { ... }
    // sender == nil 未检查，直接存入 d.messageSender
    d := &taskDispatcher{..., messageSender: sender, ...}
    return d, nil
}
```

**影响**: 传入 nil `sender` 时，`d.messageSender` 为 nil，后续调用发送方法时 panic。

**建议**:
```go
if sender == nil {
    return nil, errors.New("task dispatcher: message sender cannot be nil")
}
```

---

### 2. `New()` 缺少 `memMgr` nil 校验

**位置**: `internal/agents/leader/agent.go:181-220`

**状态**: ✅ 确认（报告中 5 项校验里唯一遗漏的一项）

`New()` 已校验 `id`（`:182`）、`parser`（`:184`）、`planner`（`:187`）、`dispatcher`（`:190`）、`aggregator`（`:193`），但 `memMgr` 无校验：

```go
func New(..., memMgr memory.MemoryManager, cfg *LeaderAgentConfig, opts ...LeaderOption) (Agent, error) {
    if id == "" { return nil, errors.New(...) }
    if parser == nil { return nil, errors.New(...) }
    // ... parser/planner/dispatcher/aggregator 均已校验
    // memMgr: 无校验，直接存入 a.memoryManager = memMgr
}
```

`memMgr` 的 nil 检查仅在 `initMemoryContext()` 内懒加载（`agent.go:404`），若该路径从未触发则错误延迟暴露。

**影响**: 传入 nil `memMgr` 时，构造不报错，首次使用内存功能时才 panic。

**建议**:
```go
if memMgr == nil {
    return nil, errors.New("leader agent: memory manager cannot be nil")
}
```

---

### 3. `NewTaskPlanner` / `NewTaskPlannerWithConfig` 静默 fallback，调用方无法区分

**位置**: `internal/agents/leader/planner.go:78-105`

**状态**: ✅ 确认（报告原 "错误处理不一致" 项的实质问题）

```go
func NewTaskPlanner(maxTasks int, opts ...PlannerOption) TaskPlanner {
    if maxTasks <= 0 {
        maxTasks = DefaultMaxTasks  // 静默替换为 5
    }
    // ... 返回 planner，无 error
}
```

`maxTasks <= 0` 时静默替换为 `DefaultMaxTasks(5)`，调用方无法区分"用户传了 0"和"用户传了 5"。与 `NewTaskDispatcher` 的 fallback 行为一致，但 API 签名为 `(TaskPlanner, error)` → 实际上永远不返回 error，签名具有误导性。

**影响**: 配置错误被静默吞掉，排查困难。

**建议**:
```go
// 方案 1: 返回 error，由调用方决定 fallback 行为
func NewTaskPlanner(maxTasks int, opts ...PlannerOption) (TaskPlanner, error) {
    if maxTasks <= 0 {
        return nil, errors.New("maxTasks must be positive, got %d", maxTasks)
    }
    ...
}

// 方案 2: 保留 fallback，文档明确标注，并在日志中记录
func NewTaskPlanner(maxTasks int, opts ...PlannerOption) TaskPlanner {
    if maxTasks <= 0 {
        slog.Warn("maxTasks <= 0, falling back to DefaultMaxTasks", "value", maxTasks)
        maxTasks = DefaultMaxTasks
    }
    ...
}
```

---

## 🐛 Potential Bugs（潜在 Bug）

### 1. `Stop()` 吞掉 goroutine 错误，始终返回 nil

**位置**: `internal/agents/leader/agent.go:342-376`

**状态**: ✅ 确认

```go
a.cleanupOnce.Do(func() {
    // ...
    if a.distillEg != nil {
        if err := a.distillEg.Wait(); err != nil {
            slog.Warn("Errors from distillation goroutines during shutdown", "error", err)  // ← 仅 warn
        }
    }
    if a.streamEg != nil {
        if err := a.streamEg.Wait(); err != nil {
            slog.Warn("Errors from streaming goroutines during shutdown", "error", err)  // ← 仅 warn
        }
    }
})
// ...
return nil  // agent.go:376 — 始终返回 nil
```

`distillEg` / `streamEg` 的错误以 `slog.Warn` 记录后丢弃，`Stop()` 签名 `(context.Context) error` 永远返回 nil。调用方无法区分"干净停止"和"后台 goroutine 失败"。

**影响**: 资源泄漏、goroutine 泄漏、内存状态不一致（distillation 失败时 memory 状态陈旧）。

**建议**:
```go
func (a *leaderAgent) Stop(ctx context.Context) error {
    // ...
    var shutdownErr error
    a.cleanupOnce.Do(func() {
        // ...
        if a.distillEg != nil {
            if err := a.distillEg.Wait(); err != nil {
                shutdownErr = errors.Join(shutdownErr, fmt.Errorf("distillation goroutines error: %w", err))
            }
        }
        if a.streamEg != nil {
            if err := a.streamEg.Wait(); err != nil {
                shutdownErr = errors.Join(shutdownErr, fmt.Errorf("streaming goroutines error: %w", err))
            }
        }
    })
    a.setStatus(models.AgentStatusOffline)
    return shutdownErr
}
```

---

### 2. `sessionID` 竞态：`CreateSession` 期间释放锁，并发 `RestoreState`/`ReplayEvents` 可覆盖

**位置**: `internal/agents/leader/agent.go:410-440`

**状态**: ✅ 确认（报告原结论正确，但建议修复方案有误）

```go
a.mu.RLock()
sessionID = a.sessionID     // :411
checkpoint := a.checkpoint  // :412
leaderID := a.id            // :413
a.mu.RUnlock()              // :414  ← 锁在此释放

if sessionID == "" {
    // ...
    newSessionID, err := a.memoryManager.CreateSession(ctx, a.getUserID())  // :429 — 耗时 DB 调用
    // ...
    a.mu.Lock()            // :438
    a.sessionID = sessionID // :439
    a.mu.Unlock()          // :440
}
```

在 `:414`（RUnlock）到 `:438`（Lock）之间，`RestoreState()`（`:964-969`）或 `ReplayEvents()`（`:1023-1068`）可并发获取 `a.mu.Lock` 并写入 `a.sessionID`。两个 goroutine 可能同时看到空 `sessionID`，同时调用 `CreateSession`，后写入者覆盖前者。

**报告建议的修复（在锁内调用 `CreateSession`）有误**：会序列化所有 `Process()`/`ProcessStream()` 调用，放大超时风险。

**建议**（使用 `sync.Once` 或 compare-and-swap 保证单次创建）:
```go
type leaderAgent struct {
    // ...
    sessionInitOnce sync.Once
    sessionInitErr  error
}

// 在 initMemoryContext 内:
if sessionID == "" {
    a.sessionInitOnce.Do(func() {
        newSessionID, err := a.memoryManager.CreateSession(ctx, a.getUserID())
        a.sessionInitErr = err
        if err == nil {
            a.mu.Lock()
            a.sessionID = newSessionID
            a.mu.Unlock()
        }
    })
    if a.sessionInitErr != nil {
        return "", "", ""
    }
    sessionID = a.sessionID
}
```

---

### 3. 空 `sessionID` 泄漏到 `finalizeMemory` → `AddMessage` 无保护调用

**位置**: `internal/agents/leader/agent.go:460, 563-564`

**状态**: ✅ 确认（报告遗漏的新问题）

`initMemoryContext()` 在 `CreateSession` 失败或返回空串时，返回 `sessionID=""`（`:512`）。`Process()` 直接将该空值传入 `finalizeMemory()`（`:906`），后者调用:

```go
// finalizeMemory 内:
a.memoryManager.AddMessage(ctx, sessionID, "assistant", resultStr)  // :564 — sessionID 可能为 ""
```

`AddMessage` 未对 `sessionID == ""` 做 guard，每次任务完成都会产生一次无效内存写入调用。

**建议**:
```go
// 在 finalizeMemory 入口处:
if sessionID == "" {
    slog.Warn("finalizeMemory skipped: empty sessionID")
    return
}
a.memoryManager.AddMessage(ctx, sessionID, "assistant", resultStr)
```

---

### 4. `distillMu` 过度保护（设计异味，非数据竞争）

**位置**: `internal/agents/leader/agent.go:118, 344-346`

**状态**: ✅ 确认（报告将"过度保护"误判为"竞态条件"）

`distillMu` 注释写 "Protects stopCh-close vs distillWg.Add ordering"（`:118`），但 Go 内存模型保证 channel close 与所有 receiver 同步，`close(stopCh)` 本身不需要 mutex。`distillMu` 实际用途只是保证 `distillWg.Add(1)` 在 `distillWg.Wait()` 之前调用。

所有 `<-a.stopCh` 读取点（`:596,764,802,840,883,1173,1191,1221,1243,1257,1268,1296`）均未加锁，但由于 channel close 的同步保证，不存在数据竞争。

**影响**: 锁语义与实际保护目标不一致，增加代码理解成本。

**建议**: 将注释改为描述真实保护目标，或移除 `distillMu` 对 `close(stopCh)` 的包裹（保留其对 `distillWg` 顺序保证）。

---

## 📋 原始报告已修复 / 不准确的结论（供参考，不计入有效项）

| 原始结论 | 实际状态 |
|---------|---------|
| `taskIDCounter` 死代码 | 在 `generateTaskID` 内被 `atomic.AddUint64` 读取并拼入返回字符串（`planner.go:38`），冗余但非死代码 |
| 魔法数字 5/10/300/20 未提取 | `constants.go` 已存在，均已用 `DefaultMaxTasks`/`DefaultMaxParallel`/`DefaultDispatcherTimeoutSeconds`/`DefaultMaxItems` 引用 |
| `NewTaskDispatcher` 缺 `agentRegistry`/`maxParallel`/`timeout` 校验 | 均已校验（`:41,:44,:47`），仅 `sender` 缺失 |
| `New()` 缺 `parser`/`planner`/`dispatcher`/`aggregator` 校验 | 均已校验（`:184,:187,:190,:193`），仅 `memMgr` 缺失 |
| `stopCh` 竞态 | 误判：channel close 本身保证同步，`distillMu` 是过度保护而非保护不足 |
| `distillMu`/`processingMu` 死锁风险 | 无代码路径同时获取两个锁，推断错误 |
| `aggregator.Aggregate` 错误未处理 | `agent.go:895-903` 及 `ProcessStream`（`:1272-1284`）均已正确处理 |

---

## 📋 优先级建议

### 🔴 高优先级（立即修复）
1. **`Stop()` 吞掉 goroutine 错误**（`agent.go:342-376`）
2. **空 `sessionID` 泄漏到 `finalizeMemory`**（`agent.go:460,564`）
3. **`sessionID` 竞态：CreateSession 期间释放锁**（`agent.go:410-440`）

### 🟠 中优先级（近期修复）
4. **`sender` nil 校验缺失**（`dispatcher.go:50-57`）
5. **`memMgr` nil 校验缺失**（`agent.go:213`）
6. **`NewTaskPlanner` 静默 fallback**（`planner.go:78-105`）

### 🟡 低优先级（技术债务）
7. **`LeaderSupervisor` 废弃代码未删除**（`supervisor.go:46-98`）
8. **`distillMu` 注释与实际保护目标不符**（`agent.go:118`）

---

*报告修订于 2026-06-24*
*验证方法: 源码逐项核对 + grep 全局搜索*
