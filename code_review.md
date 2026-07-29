# goagent (ares) 深度 Code Review — v2（修正版）

**Review Date**: 2026-07-29
**Reviewer**: Kilo
**Scope**: Goagent (goagent v0.5.x, formerly ares) — 1335 Go source files
**Review Dimensions**: 逻辑不通 / 假实现 / 死代码 / 潜在 Bug
**v2 变更**: 根据代码复核，修正 3 处误判（①②④），修正 1 处风险评估过重（⑧），Chaos 空壳数量由 5 改为 4。

---

## 一、假实现 (Fake Implementations)

### 1. Chaos Engineering 4 个方法是空壳 (HIGH)

**文件**: `internal/ares_runtime/manager_chaos.go:84-119`

以下 4 个 `Manager` 方法仅输出 `"SIMULATION: ... (R-02)"` 日志后返回 `nil`，**对 agent 状态无任何实际改变**。调用方无法区分"故障注入成功"与"假注入"。

| 方法 | 行号 | 实际行为 |
|------|------|---------|
| `PartitionNetwork` | 84 | 打印 `SIMULATION` 日志，返回 nil |
| `CorruptMemory` | 104 | 打印 `SIMULATION` 日志，返回 nil |
| `DisconnectMCP` | 110 | 打印 `SIMULATION` 日志，返回 nil |
| `InjectLLMFailure` | 116 | 打印 `SIMULATION` 日志，返回 nil |

```go
// manager_chaos.go:84-87
func (m *Manager) PartitionNetwork(_ context.Context, agentID string) error {
    log.Warn("[arena] PartitionNetwork — SIMULATION: no actual network partition applied (R-02)", "agent", agentID)
    return nil
}
```

**危害**：`api/router/router.go:85` 注册了 `POST /api/v1/arena/faults` HTTP 端点，外部调用会认为故障已注入，但实际上什么都没发生。任何依赖这些 API 做 chaos testing 的下游将产生**虚假的测试通过**。

---

### 2. `PauseAgent` 实为 `StopAgent`，功能名不符实 (MEDIUM)

**文件**: `internal/ares_runtime/manager_chaos.go:57-67`

```go
func (m *Manager) PauseAgent(ctx context.Context, agentID string) error {
    return m.StopAgent(ctx, agentID) // 无任何"暂停"语义
}
func (m *Manager) ResumeAgent(ctx context.Context, agentID string) error {
    return m.RestartAgent(ctx, agentID) // 全量重启，非恢复暂停
}
```

`StopAgent` 会设置 `ma.stopped = true`，导致 `NotifyAgentDead` 忽略该 agent。`ResumeAgent` 则触发完整的 `RestartAgent` 流程（含事件重放、memory 恢复）。系统中**没有任何 "paused" 状态**可供区分，`Stats()` 和 `ListAgents()` 也无法区分"已暂停"与"已停止"。暂停-恢复语义与实际行为**完全不符**。

---

### 3. FitnessGenome 未接线 — 所有进化评分为 0 (HIGH)

**文件**: `internal/ares_evolution/genome_wiring_run.go:404-410`

```go
// NOTE: per-genome fitness is intentionally left at 0 (unknown)
// TODO: wire FitnessGenome once genomes consume live runtime state.
for _, p := range patches {
    a.coordinator.Submit(coordinator.PatchProposal{
        Patch:     p,
        Priority:  6,
        Fitness:   0, // ← 硬编码为 0
        Timestamp: time.Now(),
    })
}
```

所有 GA 进化的 patch 提交时 `Fitness=0`。Coordinator 的 fallback 路径会基于 `MaxPatchesPerMinute` 限速，**完全不考虑 fitness**。进化系统的核心价值（自适应评估）被短路。注释中承认这是假实现。

---

### 4. KnowledgeRuntime LazyLoading 为 TODO (MEDIUM)

**文件**: `internal/knowledge/runtime/runtime.go:138-145`

```go
// TODO: implement lazy graph execution path (expected 2026-08).
if cfg.LazyLoading {
    log.Info("lazy loading requested but not yet implemented; returning full graph")
}
```

用户配置 `LazyLoading: true` 时，静默返回完整图，**无任何提示给调用方**（只有一条 info 日志）。预期返回类型 `LazyGraph` 与当前 `WorkingGraph` 不兼容，意味着 API 契约随时可能断裂。

---

### 5. Dashboard 未接线 (LOW)

**文件**: `api/bootstrap/bootstrap.go:125`

```go
// TODO: wire dashboard with actual MCP/LLM executors (expected by 2026-09-30).
```

Dashboard 启动成功但内部 executors 为假实现，仪表盘数据不可信。

---

## 二、逻辑不通 (Logic Issues)

### 1. `StartAgent` 在 `Start()` 前预注册丢失启动事件 (MEDIUM)

**文件**: `internal/ares_runtime/manager.go:225-243` × `manager_lifecycle.go:53-66`

```go
// manager.go:225-232 — StartAgent before Start()
if !m.isStarted {
    m.mu.Unlock()
    return nil // agent 已存入 m.agents，cancel=nil，但 goroutine 未启动
}
```

`Start()` 后续会处理这些 agent（`manager_lifecycle.go:56-58`），但**不触发 `EventAgentStarted`**。只有通过 `StartAgent()` 路径才 emit 事件（`manager.go:237-240`）。
结果：事件溯源中，由 `Start()` 启动的 agent **丢失了 `EventAgentStarted`** 事件，导致时间线断裂。

---

### 2. `PauseAgent`/`ResumeAgent` 无独立暂停状态 (LOW)

**文件**: `internal/ares_runtime/manager_chaos.go:57-67` × `manager.go:245-283`

`PauseAgent` 调 `StopAgent` → 设置 `ma.stopped=true`，和主动关闭 agent 完全等价。
`ResumeAgent` 调 `RestartAgent` → 做完整的停止+工厂重建+事件重放。
系统中**没有任何 "paused" 状态**可供区分，`Stats()` 和 `ListAgents()` 也无法区分"已暂停"与"已停止"。

**注**: SubAgent 工厂闭包不是问题。`serve.go:194-208` 已在循环内用 `subAgent := sa` 正确捕获每次迭代的独立变量，且 fallback 路径在未匹配时返回 `nil`，由 `recoverAgentState` 报错阻止用死 agent 恢复。此处误判已剔除。

---

### 3. `buildSubscribeQuery` argIdx++ 残留 (LOW)

**文件**: `internal/ares_events/pg_store.go:452-454`

```go
query += fmt.Sprintf(" AND type = ANY($%d)", argIdx)
args = append(args, typeStrs)
argIdx++ //nolint:ineffassign // Reserved for future query parameters.
```

`argIdx++` 后无后续 `$N` 占位符使用。虽然注释说是为未来预留，但当前代码中这是**死代码**。若后续忘记添加新参数，查询将出错。

---

## 三、死代码 (Dead Code)

### 1. Chaos 4 个方法无实际功能

见 **假实现 #1**。这些方法注册了 HTTP 端点（`POST /api/v1/arena/faults`）但永远返回 nil，调用方无法区分成功注入与假注入。应删除标记为 `SIMULATION` 的注释，改为显式返回错误，或将方法移出 Manager 接口。

---

### 2. `PluginError.Recovered` 类型为 `any` 但无结构化处理

**文件**: `internal/ares_runtime/errors.go:18-27`

```go
type PluginError struct {
    PluginName string
    Err        error
    Recovered  any   // 存储 panic 值，类型为 any
}
```

`bus.go:307-310` 设置 `Recovered: r`，然后 `Error()` 通过 `%v` 格式化。但**没有任何代码对 `Recovered` 做类型断言或结构化日志**，相当于空白字段。

---

### 3. 多个事件发射静默丢弃

**文件**: `internal/ares_runtime/bus.go:228-232`, `internal/ares_events/memory_store.go:256-260`

```go
// bus.go — PluginBus.Emit
default:
    // Drop event if buffer full.

// memory_store.go — notifySubscribers
default:
    // Subscriber buffer full, drop event.
```

监控仪表盘依赖事件流，当 subscriber 消费不及时时事件被静默丢弃。没有任何指标（metric）统计丢弃数量，导致**无法调试数据不完整的问题**。

---

## 四、潜在 Bug (Potential Bugs)

### 1. `NotifyAgentDead` 竞争与 `resurrecting` 状态泄漏 (MEDIUM)

**文件**: `internal/ares_runtime/manager.go:512-611`

```go
// notifyAgentDead
if hasAgent && m.config.MaxRestartsPerAgent > 0 && ma.restarts >= m.config.MaxRestartsPerAgent {
    return nil, false  // 不恢复
}
ma.restarts++
ma.resurrecting = true

// scheduleResurrection 成功时 reset
entry.resurrecting = false  // 仅成功时
```

**低风险说明**：`resurrecting` 在 `scheduleResurrection` 的所有退出路径（成功/失败/超时/context cancel）都显式重置为 `false`，`resurrecting` 永久泄漏的风险很低。  
**剩余风险**：`ma.restarts++` 的判断和递增在同一锁内是原子的，但 `scheduleResurrection` 调用后若 `RestoreAgent` 失败，会在 5 次 retry（backoff）全部失败后再放弃，这期间 `NotifyAgentDead` 会跳过检查，不会有超过 `MaxRestartsPerAgent` 的问题。

---

### 2. `store.go` 的 `Emit` 无超时 (MEDIUM)

**文件**: `internal/ares_events/store.go:52-73`

```go
func Emit(ctx context.Context, store EventAppender, streamID string, ...) bool {
    ...
    if err := store.Append(ctx, streamID, []*Event{event}, 0); err != nil {
        ...
        return false
    }
}
```

`Emit` 依赖调用方传入的 `ctx` 取消，但 PostgreSQL 实现 `pg_store.go` 中 `Append` 启动事务后若无数据可写（但连接池满）会阻塞。`PluginBus.Emit` 调用 `Emit` 时持有 `RLock`，阻塞会导致整个 `PluginBus` reader-writer 阻塞。

---

### 3. `serve.go` HTTP Server 错误处理 (LOW)

**文件**: `cmd/ares/serve.go:300-305`

```go
g.Go(func() error {
    if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
        return fmt.Errorf("HTTP server error: %w", err)
    }
    return nil
})
```

非 `ErrServerClosed` 错误由 errgroup 传播并取消全局 `ctx`，但此时 shutdownMgr 的清理 hook 可能仍在等待 HTTP shutdown 完成（`httpSrv.Shutdown` 需要等正在处理的请求完成），造成**死锁/超时**。

---

### 4. `bootstrap.go` EventStore 在 nil deps 时用 MemoryEventStore (LOW)

**文件**: `internal/ares_bootstrap/bootstrap.go:86-90`

```go
if deps.EventStore != nil {
    comp.EventStore = deps.EventStore
} else {
    comp.EventStore = ares_events.NewMemoryEventStore() // 内存实现，非持久化
}
```

`serve.go:110` 确实传 `nil` deps，生产环境默认使用 `MemoryEventStore`（非持久化、设计用于单进程开发）。若有人误以为 `Bootstrap` 生产就绪用于多 agent 集群，将丢失所有事件。

---

## 五、已剔除的误判说明（v1 → v2 修正）

| v1 编号 | v1 结论 | v2 结论 | 理由 |
|---------|---------|---------|------|
| ① SubAgent 闭包捕获 | HIGH 逻辑 bug | **误判，移除** | `serve.go:195` 使用 `subAgent := sa` 正确捕获每次迭代独立变量；`createAgents` 未匹配时返回 `nil`，`recoverAgentState` 会报错阻止复活死 agent |
| ④ Bootstrap CallbackReg 可能 nil panic | MEDIUM 逻辑 bug | **误判，移除** | `provide_evolution.go:38` 有 nil guard：`if eventStore == nil || expRepo == nil || callbackReg == nil` 时返回错误（evolution skipped），不会 panic |
| ⑩ buildMemoryManager 类型伪造 | LOW 潜在 bug | **误判，移除** | `NewMinimalMemoryManager()` 签名返回 `*ProductionMemoryManager`，`buildMemoryManager()` 签名与其完全匹配 |

---

## 六、建议优先级一览（修正后，共 11 项）

| # | Issue | 文件 | Severity | 类型 |
|---|---|---|---|---|
| 1 | FitnessGenome 硬编码 0 | `genome_wiring_run.go:409` | HIGH | 假实现 |
| 2 | Chaos 4 个方法空壳 | `manager_chaos.go:84-119` | HIGH | 假实现/死代码 |
| 3 | Replay/Start 丢失 EventAgentStarted | `manager_lifecycle.go:53-66` | MEDIUM | 逻辑 bug |
| 4 | Emit 无超时 + 静默丢弃 | `bus.go:228-232` | MEDIUM | 潜在 bug |
| 5 | LazyLoading TODO | `runtime.go:141` | MEDIUM | 假实现 |
| 6 | PauseAgent = StopAgent (语义错误) | `manager_chaos.go:58-61` | MEDIUM | 逻辑 bug |
| 7 | NotifyAgentDead resurrecting 状态 (风险已降低) | `manager.go:529-610` | MEDIUM | 潜在 bug |
| 8 | Dashboard 未接线 | `bootstrap.go:125` | LOW | 假实现 |
| 9 | buildSubscribeQuery argIdx++ 残留 | `pg_store.go:452` | LOW | 死代码 |
| 10 | PluginError.Recovered 空白字段 | `errors.go:22` | LOW | 死代码 |
| 11 | bootstrap.go EventStore 默认 MemoryEventStore | `bootstrap.go:86-90` | LOW | 潜在 bug |

---

## 七、修复建议概要

1. **FitnessGenome**：移除 `Fitness: 0` 硬编码，接入 live runtime state scorer。
2. **Chaos 方法**：对 4 个空壳方法（`PartitionNetwork`, `CorruptMemory`, `DisconnectMCP`, `InjectLLMFailure`）返回 `ErrNotImplemented`，或移除对应的 HTTP 端点，避免虚假测试通过。
3. **启动事件缺失**：在 `manager_lifecycle.go:Start()` 的 agent 启动路径（`launchAgentGoroutine` 之后）补充 emit `EventAgentStarted`。
4. **Emit 丢弃检测**：为 `PluginBus.Emit` 和 `MemoryEventStore.notifySubscribers` 的 `default` 分支添加 channel full counter metric，暴露给监控。
5. **LazyLoading**：在 `Execute()` 中当 `cfg.LazyLoading=true` 时返回明确错误，或实现懒加载图路径。
6. **PauseAgent 语义**：增加 `ma.paused` 状态字段，`PauseAgent` 仅 cancel context 但不设置 `stopped`，`ResumeAgent` 恢复 context 而不重建 agent。
7. **notifyAgentDead**：`scheduleResurrection` 成功/失败/超时均已 reset `resurrecting`，当前实现风险低；建议在 `NotifyAgentDead` 入口处加 `if ma.resurrecting { return }` 防御性判断。
8. **Dashboard**：完成 MCP/LLM executor wiring（TODO 中有截止日期 2026-09-30）。

---

*报告 v2 结束。所有引用行号基于 2026-07-29 提交的快照。*
