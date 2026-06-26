# Code Review: Runtime Plugin System Commits

> 审查范围: commit c43959d + 791e62c
> 审查日期: 2026-06-26
> 审查方法: 2 个并行 agent 深度审查，覆盖所有新增/修改文件

---

## 总览

| 维度 | c43959d | 791e62c | 合计 |
|------|---------|---------|------|
| Critical | 2 | 0 | **2** |
| High | 4 | 3 | **7** |
| Medium | 6 | 6 | **12** |
| Low | 7 | 6 | **13** |
| Test Gaps | 6 | 7 | **13** |

---

## 🔴 Critical（必须立即修复）

### C1. Send-on-closed-channel 竞态

**文件**: `internal/runtime/bus.go:182,213`

```go
// Emit 中（持 RLock，但已复制 subscriber slice 后释放锁）
for _, s := range subs {
    select {
    case s.ch <- evt:  // ← 可能 send 到已关闭的 channel
    default:
    }
}

// Subscribe cleanup goroutine 中（持 Lock）
close(sub.ch)  // ← 可能和上面的 send 竞态
```

**问题**: `Emit` 复制 subscriber slice 后释放锁，然后遍历发送。cleanup goroutine 可能在这期间获取锁、移除 subscriber、close channel。结果是 send-on-closed-channel panic。

**修复**: 在 send 前检查 `ctx.Err()`，或用 `sync.Once` 保护 close。

---

### C2. Resume 时 resultChan 可能死锁

**文件**: `internal/workflow/engine/dynamic_executor.go:255`

```go
resultChan := make(chan *StepResult, len(executionOrder))
```

**问题**: 从 checkpoint 恢复时，如果 recovery 添加了新步骤到 `currentOrder`，但 resultChan 的 buffer 大小仍是原始 `executionOrder` 的长度。新步骤的 goroutine 可能阻塞在 send 上，导致 `stepEg.Wait()` 永远等不到。

**修复**: 动态调整 resultChan 大小，或用无缓冲 channel + select。

---

## 🟡 High（应该尽快修复）

### H1. BeforeStep/AfterStep 错误被静默丢弃

**文件**: `internal/workflow/engine/dynamic_executor.go:640,673`

```go
_ = e.pluginBus.BeforeStep(ctx, execution.ID, step)   // 错误被丢弃
_ = e.pluginBus.AfterStep(ctx, execution.ID, result)   // 错误被丢弃
```

**问题**: 插件接口文档说 "Returning an error aborts the step"，但执行器完全忽略错误。这违反了插件契约。

**修复**: 检查错误，对于 required hook 失败应中止 step。

---

### H2. Resume 不恢复 checkpoint 中的 Variables

**文件**: `internal/workflow/engine/dynamic_executor.go:221-223`

```go
execution.Variables = make(map[string]any, len(workflow.Variables))
for k, v := range workflow.Variables {
    execution.Variables[k] = v  // ← 只复制 workflow 的初始变量
}
// ckpt.Variables 被忽略！
```

**问题**: 恢复时只用 workflow 的初始 Variables，丢弃了 checkpoint 中保存的执行过程中修改过的 Variables。

**修复**: 优先用 `ckpt.Variables`，fallback 到 `workflow.Variables`。

---

### H3. CheckpointPlugin.Snapshot 返回浅拷贝

**文件**: `internal/runtime/checkpoint.go:264-266`

```go
func (p *CheckpointPlugin) Snapshot(executionID string) (*ExperienceCheckpoint, bool) {
    cp := *ckpt  // ← 浅拷贝！Slice/Map 仍指向原始数据
    return &cp, true
}
```

**问题**: 调用者修改返回的 Snapshot 会破坏插件内部状态。

**修复**: 深拷贝所有 slice 和 map 字段。

---

### H4. Checkpoint 无限增长（MergeInto 重复追加）

**文件**: `internal/runtime/checkpoint.go:269-273`

```go
func (p *CheckpointPlugin) saveLocked(...) error {
    if p.collector != nil {
        p.collector.MergeInto(ckpt)  // ← 每次 save 都追加全量数据
    }
}
```

**问题**: `MergeInto` 每次调用都把 collector 的全部历史追加到 checkpoint。K 步后 checkpoint 包含 K 份重复数据。**内存泄露**。

**修复**: 要么 merge 后清空 collector，要么每次重建 checkpoint 而非追加。

---

### H5. Route Decision 计算了但从未应用

**文件**: `internal/workflow/engine/dynamic_executor.go:379-391`

```go
decision := e.handleStepRouting(ctx, execution, result, mutableDAG, currentOrder)
if decision != nil {
    slog.Debug("route decision", ...)  // ← 只打日志，decision 被丢弃
}
```

**问题**: 整个路由系统（RouterPlugin、ExpressionRouter、RouteState、RouteDecision）已接线但**不产生任何效果**。路由决策只用于可观测性。

**修复**: 如果是有意的（observability-only），需要在文档中明确说明。如果不是，这是功能 bug。

---

### H6. Export() 返回内部 slice 引用（数据竞态）

**文件**: `internal/runtime/collector.go:188-199`

```go
func (c *ExecutionCollector) Export() map[string]any {
    c.mu.Lock()
    defer c.mu.Unlock()
    return map[string]any{
        "route_history": c.routeHistory,  // ← 返回内部 slice 引用
        ...
    }
}
```

**问题**: 锁释放后，调用者持有的 slice 引用和并发写入者之间存在数据竞态。

**修复**: 返回 slice 的副本。

---

### H7. ExpressionRouter 不是线程安全的

**文件**: `internal/runtime/router.go:95-97`

```go
func (r *ExpressionRouter) AddRule(rule RouteRule) {
    r.rules = append(r.rules, rule)  // ← 无锁
}
```

**问题**: `AddRule` 无锁写入，`Route` 并发读取。数据竞态。

**修复**: 加 `sync.RWMutex`。

---

## 🟢 Medium（计划内修复）

### M1. ErrBusNotStarted 语义反转

**文件**: `internal/runtime/bus.go:62-64`

`Register` 在 bus **已启动**时返回 `ErrBusNotStarted`。命名和语义相反。

**修复**: 改名为 `ErrBusAlreadyStarted`。

---

### M2. Start/Stop 只返回最后一个错误

**文件**: `internal/runtime/bus.go:89-95,104-117`

多个插件失败时，只保留最后一个错误，其余静默丢失。

**修复**: 用 `errors.Join` 合并所有错误。

---

### M3. invokeWithTimeout PluginError 没有 PluginName

**文件**: `internal/runtime/bus.go:252-254`

panic recovery 时创建的 `PluginError` 的 `PluginName` 为空字符串。

**修复**: 传入 plugin name。

---

### M4. PluginsByCap 返回可变内部 slice

**文件**: `internal/runtime/bus.go:223-227`

返回原始 slice，调用者可修改内部状态。

**修复**: 返回副本。

---

### M5. CreatedAt 每次 save 都被覆盖

**文件**: `internal/runtime/checkpoint.go:270`

```go
ckpt.CreatedAt = time.Now()  // ← 每次都更新
```

**修复**: 只在首次创建时设置，新增 `UpdatedAt` 字段。

---

### M6. EventCheckpointSaved 声明但从未 emit

**文件**: `internal/runtime/events.go:16`

Observer 订阅了此事件但永远收不到。

**修复**: 在 `saveLocked` 中 emit，或从 observer 订阅列表移除。

---

### M7. AfterStep 在事件 emit 之后调用（顺序错误）

**文件**: `internal/workflow/engine/dynamic_executor.go:656-673`

先 emit `EventStepCompleted`，再调用 `AfterStep`。如果 AfterStep 修改了结果，事件已经发出去了。

**修复**: 先 AfterStep，再 emit 事件。

---

### M8. LoopPlugin 死代码 + iteration 无保护

**文件**: `internal/runtime/loop.go:62-79`

```go
reason := "max_iterations_reached"  // 计算了但
_ = reason                          // 立即丢弃
```

`iteration` 字段无 mutex 保护。

---

### M9. LoopPlugin 未被 engine 消费

**文件**: `internal/runtime/loop.go`

engine 从未查找 `CapLoop` 插件，从未调用 `ShouldContinue`。插件是惰性的。

---

### M10. Duration JSON tag 误导

**文件**: `internal/runtime/collector.go:36`

```go
Duration time.Duration `json:"duration_ms"`  // ← time.Duration 序列化为纳秒，不是毫秒
```

**修复**: 改为 `int64` 存毫秒，或改 tag 为 `duration_ns`。

---

### M11. Interrupt 靠字符串匹配检测 reject

**文件**: `internal/runtime/interrupt.go:58`

```go
if result.Error == "rejected by human" {  // ← 脆弱的字符串匹配
```

**修复**: 用结构化信号（metadata key）。

---

### M12. WithCollector 无线程安全保护

**文件**: `internal/runtime/checkpoint.go:146-149`

`WithCollector` 设置 `p.collector` 时未持锁。

---

## 🔵 Low（后续改进）

| # | 文件 | 问题 |
|---|------|------|
| L1 | `bus.go:104` | Stop 只返回最后一个错误 |
| L2 | `checkpoint.go:106` | 冗余的 `checkpointKey` wrapper |
| L3 | `checkpoint.go:119` | snapshots map 无清理（内存泄露） |
| L4 | `observer.go:88` | 用 `slog.Warn` 而非可配置 logger |
| L5 | `dynamic_executor.go:416` | 取消执行不 emit 事件 |
| L6 | `genome_wiring.go:1466` | Marshal/Unmarshal 转换比直接 copy 慢 |
| L7 | `plugin.go:48` | BeforeStep 文档提到 "required/optional" 但未实现 |
| L8 | `collector.go:215` | MergeInto 丢失 Input/Output/Duration 等字段 |
| L9 | `router.go:78` | nil Condition 无条件匹配，需文档说明 |
| L10 | `events.go:31` | EventInterruptResolved 声明但从未 emit |
| L11 | `plugin.go:60` | MemoryPlugin/EvolutionPlugin 接口无实现 |
| L12 | `types.go:43` | ConditionFunc 不可序列化（json:"-"） |
| L13 | `executor_helpers.go:122` | NextStepID 未校验是否存在于 DAG |

---

## 测试缺口

| # | 缺失测试 | 风险 |
|---|----------|------|
| T1 | Emit + context cancellation 并发竞态 | Critical bug 未覆盖 |
| T2 | Checkpoint resume + failed steps | 恢复路径不完整 |
| T3 | Resume + DAG/workflow 不匹配 | 静默失败 |
| T4 | invokeWithTimeout 实际超时行为 | 超时路径未验证 |
| T5 | ObserverPlugin store 失败 | 降级路径未测试 |
| T6 | BeforeStep 错误传播到 executor | 契约违反未检测 |
| T7 | Collector Export 并发安全 | 数据竞态未覆盖 |
| T8 | LoopPlugin 并发 iteration | 竞态未覆盖 |
| T9 | Router concurrent AddRule + Route | 竞态未覆盖 |
| T10 | Checkpoint MergeInto 重复追加 | 内存泄露未检测 |
| T11 | ConditionFunc panic | scheduler goroutine 无 panic recovery |
| T12 | handleStepRouting 边界情况 | 缺少单元测试 |
| T13 | time.Sleep-based observer tests | flaky test 模式 |

---

## 修复优先级

### P0 — 立即修复（阻塞性问题）

1. **C1**: bus.go Emit/Subscribe 竞态 → 加 ctx.Err() 检查或 sync.Once
2. **C2**: resultChan 死锁 → 动态调整 buffer 大小
3. **H1**: BeforeStep/AfterStep 错误处理 → 检查并传播错误
4. **H4**: Checkpoint 无限增长 → merge 后清空 collector 或重建 checkpoint

### P1 — 本周修复

5. **H2**: Resume 恢复 Variables
6. **H3**: Snapshot 深拷贝
7. **H6**: Export 返回副本
8. **H7**: ExpressionRouter 加锁
9. **M1**: ErrBusNotStarted 改名
10. **M2**: errors.Join 合并错误
11. **M7**: AfterStep 调用顺序

### P2 — 下周修复

12. **M3-M6, M8-M12**: 其余 Medium 问题
13. **H5**: 明确 Route Decision 是 observability-only 还是功能 bug
14. **L1-L13**: Low 问题
15. **T1-T13**: 补充测试覆盖
