# 模块分析报告：`internal/ares_observability`、`internal/ares_protocol`、`internal/ares_experience`

---

# `internal/ares_observability`

### 1. `otel_tracer.go` 用裸数字 `1` 代替 `codes.Error`
- **位置**：`otel_tracer.go` 214 行
- **说明**：`span.SetStatus(1, step.Error.Error())` 用裸数值字面量 `1`，其它调用点（157、185、241 行）都用命名常量 `codes.Error`。现在碰巧因 `codes.Error == 1` 工作，若常量值改变会静默失效。应改 `codes.Error`。
- **状态**：✅ 已修复（2026-08-14）——改为 `codes.Error`。

### 2. `prometheus.go` 重复初始化时返回未注册的采集器
- **位置**：`prometheus.go` 144-151 行
- **说明**：`AlreadyRegisteredError` 时静默返回新创建的 `PrometheusMetrics`（其采集器未注册，registry 仍持早期实例）。调用方记录到返回的 `m` 实为未注册向量，指标不出现在 `/metrics`。
- **状态**：✅ 已修复（2026-08-14）——包级 `cachedMetrics` 缓存首个成功注册实例；重复初始化返回缓存实例（幂等，`TestNewPrometheusMetrics_Idempotent` 通过），无缓存时显式报错。

### 3. `cost.go` 整个 CostDashboard 生产未用
- **位置**：`cost.go`
- **说明**：`CostDashboard` 全部表面（`NewCostDashboard`、`GetSessionCost`、`GetAllSessions`、`GenerateDashboardHTML`、`RegisterCostRoutes` 等）及 `CostTracker` 方法仅测试引用。
- **状态**：⚠️ 保留为能力储备（2026-08-14）——与 monitoring 4 桩同类别（本会话处置决策：保留，不接线），非本次修复范围。

---

# `internal/ares_protocol`（ahp 协议）

### 4. `ahp/dlq.go` `Add` 从不设置 `MaxRetries`，重试上限失效（BUG）
- **位置**：`ahp/dlq.go` 43-62 行
- **说明**：`Add` 从不设 `DLQEntry.MaxRetries`，恒为 `0`（代码视为"无限"`MaxRetriesUnlimited`）。因此 `Process`（183 行 `entry.MaxRetries > 0 && ...`）的重试预算检查在生产路径实际是死的——每个条目被无限重试。`MaxRetries` 字段、常量、`json:"max_retries"` tag 仅测试（dlq_test.go）使用。
- **状态**：✅ 已核实修复（2026-08-14）——`Add` 现委托 `AddWithMaxRetries(..., MaxRetriesUnlimited)`，`AddWithMaxRetries` 设置 `MaxRetries` 并支持有界预算（注释明确说明此前 MaxRetries 逻辑是死的）；报告条目过时。

### 5. `ahp/dlq.go` `Process` 中 `entry.Retries++` 数据竞争（BUG）
- **位置**：`ahp/dlq.go` 178-204 行
- **说明**：`entry.Retries++`（187 行）无锁/无原子地修改共享 `DLQEntry`。若 `Process` 并发调用（手动 `Process` 与 `StartAutoRetry` ticker 竞争），`entry.Retries` 数据竞争，且两 goroutine 可能并发处理并 `Remove` 同一条目。
- **状态**：✅ 已核实修复（2026-08-14）——`Process` 用 `processMu`（`sync.Mutex`）串行化全部处理（"processMu serializes Process calls so concurrent Process invocations never race"），`Retries++` 与 `Remove` 均在锁内；报告条目过时。

### 6. `ahp/queue.go` `Enqueue` 忽略 ctx（LOGIC）
- **位置**：`ahp/queue.go` 55-75 行
- **说明**：`ctx` 参数声明但完全未用，`select`（69-74）无 `<-ctx.Done()` case。上下文取消被忽略：队列有空间时即使 ctx 已取消也入队；满时即使 ctx 已 done 也返回 `ErrQueueFull`。签名承诺的上下文感知取消未被兑现（内联注释 67-68 已承认）。

### 7. `ahp/queue.go` `IsFull`/`Available` 不一致，`Available` 可负
- **位置**：`ahp/queue.go` 189-200 行
- **说明**：`IsFull` 只看 `len(q.messages) >= q.opts.MaxSize`，忽略 `backupBuffer`；`Available`/`Size` 会计 backup buffer。两者可矛盾（IsFull false 但实际已满）。`Available` 在 `Peek()` 把条目存入 backup buffer 而 channel 满时可为负。目前仅测试使用。

### 8. `ahp/heartbeat.go` Start/Stop 并发时 WaitGroup 可能死锁
- **位置**：`ahp/heartbeat.go` 219-274 行
- **说明**：若 `Start` 在 `Stop` 进行中（`Stop` 释放 mutex 于 271 行与 `s.wg.Wait()` 之间）并发调用，新 `Start` 做 `s.wg.Add(1)`（226 行）到同一 WaitGroup，`Stop` 的 `wg.Wait()` 会阻塞直到新 sender 也停止，死锁 `Stop`。文档（179-180）声称多 goroutine 安全仅当严格串行成立。

### 9. `ahp/dlq.go` `StartAutoRetry` / `retryInterval` 生产未用（DEAD_CODE）
- **位置**：`ahp/dlq.go` 209-235、155、210 行
- **说明**：`retryInterval` 被写从不读；整个自动重试机制生产未引用。

---

# `internal/ares_experience`

### 10. `conflict_resolver.go` 空输入返回一个虚假空组
- **位置**：`conflict_resolver.go` 35-38 行
- **说明**：空输入返回 `[][]*Experience{{}}`（一个虚假空组）。`Resolve` 经 `len(group)==0` continue（110-112）不崩溃，但空组是浪费工作。

### 11. `task_result.go` 注释与实际行为矛盾
- **位置**：`task_result.go` 17 行
- **说明**：注释 "Only successful tasks are distilled into experiences" 与 `DistillationService.ShouldDistill`/`Distill` 实际行为（显式蒸馏失败任务）矛盾。误导性文档。

### 12. 整个蒸馏/排序/冲突解析管线生产未用（DEAD_CODE）
- **位置**：包内多处
- **说明**：生产 `api_impl/service.go`（379-380）构造 `RankingService`、`ConflictResolver` 但从不调 `Rank`、`Resolve`、`DetectConflictGroups`、`Configure`（字段注释 "exposed for later wiring"）。`DistillationService`、`FeedbackService` 生产从不引用。整个管线仅测试使用。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `ares_protocol/dlq.go` 43 | MaxRetries 从不设置，重试上限失效，条目无限重试 |
| **高** | `ares_protocol/dlq.go` 187 | entry.Retries++ 数据竞争 |
| 中 | `ares_protocol/queue.go` 55 | Enqueue 忽略 ctx 取消 |
| 中 | `ares_observability/otel_tracer.go` 214 | 裸数字 1 代替 codes.Error |
| 中 | `ares_protocol/heartbeat.go` 219 | Start/Stop 并发可能死锁 |
| 低 | 多处 | CostDashboard / StartAutoRetry / experience 管线生产未用 |
