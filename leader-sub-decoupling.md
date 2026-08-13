# Leader & Sub Agent 解耦计划（A+B+C 渐进式）

> 目标：解决"leader 太重"，实现 leader/sub 安全隔离（谁挂不影响谁），加强 agent 间通讯，项目彻底改为 **event 驱动**。
> 原则：**渐进式**（每步可独立验证、可回滚）、**复用现有基础设施**（`ares_events` EventStore 已成熟，非从零造轮子）、遵循 `code_rules_v2.md`。

---

## 〇、根因（历史分析，已被 A/B/C 解耦消除）

> **注意**：本表记录的是解耦**之前**代码状态下的根因（含当时的行号）。A/B/C 阶段完成后，表中的问题均已消除——当前代码已是事件驱动 + 独立包 + panic 隔离。行号已失效，仅作历史留痕。

| # | 问题（历史） | 当时的证据（已过时） | 当前状态 |
|---|------|------|------|
| 1 | **dispatcher 进程内同步执行所有 sub 任务** | `cmd/ares/agents.go:94` `RegisterExecutor`（已移除） | ✅ `dispatcher.dispatchViaEvents` 事件驱动，`cmd/ares/agents.go:82` 用 `WithDispatcherEventStore` |
| 2 | **双重执行路径** | 同一 executor 注册进 dispatcher 又传给 sub.New | ✅ 单一事件驱动路径，`cmd/ares/serve.go` 启动 sub 事件监听器 |
| 3 | **横切职责堆积（God Object）** | leaderAgent 聚合 supervisor/evaluator/checkpoint 等 | ✅ supervisor/evaluator 已移除；checkpoint/recovery/event_recovery 收敛 `leader/state/`，aggregator 收敛 `leader/aggregate/` |
| 4 | **sub 已具备独立执行能力但 leader 不用** | sub 有 ProcessStream/messageQueue/eventStore 但 leader 绕过 | ✅ sub 通过 `StartEventListener`（serve.go:336）事件驱动消费 |
| 5 | **无 panic 隔离** | `sub/agent.go:358` 无 recover | ✅ `sub/agent.go:494` `ProcessStream` goroutine 有 `recover()`（§4.2） |
| 6 | **事件基础设施已就绪但未接入** | leader-sub 执行路径未用 ares_events | ✅ `EventSubTaskScheduled/Started/Result/Failed` 全接入，C 阶段新增 `EventMemoryFinalize` |

**核心矛盾（历史）**：sub 已具备独立执行能力，但 leader dispatcher 在本地模式绕过它同步执行。**该矛盾已被 B 阶段（事件驱动执行模型）消除。**

---

## 一、阶段 A：横切职责剥离（低风险，先减负）

> 目标：移除死代码 + 把"非 leader 编排本质"的关注点下沉为独立包。**不改变执行模型**（暂保留同步执行）。

### A1. 移除死代码
| 动作 | 文件 | 依据 |
|------|------|------|
| 删除 `supervisor.go` | `leader/supervisor.go` | 已标 `Deprecated`，无生产调用 |
| 删除 `evaluator.go` | `leader/evaluator.go` | `DefaultEvaluator` 仅被 `evaluator_test.go` 引用，生产 `Process`/`ProcessStream` 不调用 |

**步骤**：
1. grep 确认 `supervisor.go`/`evaluator.go` 的所有符号零生产引用（含 `internal/agents` 外部）。
2. 删除两个文件 + 对应 `_test.go`。
3. `go build ./...` + 相关测试验证。

### A2. 持久化关注点收敛为独立包
| 动作 | 文件 |
|------|------|
| 新建 `internal/agents/leader/state` 包，收编 checkpoint/recovery/event_recovery | `leader/checkpoint.go`、`leader/recovery.go`、`leader/event_recovery.go` |

**步骤**：
1. 新建 `internal/agents/leader/state/` 包。
2. 把 `checkpoint.go`/`recovery.go`/`event_recovery.go` 的类型与方法**纯搬移**（不改逻辑），只改包名与导出符号。
3. `leaderAgent` 只持 `state.*` 接口引用，不再直接管理持久化细节。
4. `go build` + 相关测试（`checkpoint_test`/`recovery_test` 移到 state 包）。

### A3. aggregator 独立
| 动作 | 文件 |
|------|------|
| 新建 `internal/agents/leader/aggregate` 包 | `leader/aggregator.go` |

**步骤**：同 A2 纯搬移，`leaderAgent` 持接口引用。

### A4. 阶段 A 验证
- `go build ./...`、`go test ./internal/agents/...`、`go vet`、`gofmt`。
- **验收**：`leaderAgent` 结构体字段数显著减少，`leader/` 包内文件按职责归类。

---

## 二、阶段 B：执行模型解耦 + event 驱动（治本）

> 目标：leader 只做**编排**（发任务事件、收结果事件），**不再进程内同步执行** sub。sub 通过 `ProcessStream` 独立执行并回传结果。**彻底 event 驱动**。

### B1. 统一事件契约
在 `internal/ares_events` 增加 leader-sub 协作事件类型（复用现有 `EventStore`）：
| 事件 | 语义 | 方向 |
|------|------|------|
| `EventSubTaskScheduled` | leader 派发任务 | leader→event store |
| `EventSubTaskStarted` | sub 开始执行 | sub→event store |
| `EventSubTaskResult`（成功/失败） | sub 回传结果 | sub→event store |
| `EventSubAgentFailed` | sub 故障（含 panic 捕获） | sub→event store |

**步骤**：在 `internal/ares_events` 定义上述事件常量 + payload 结构，复用 `EventStore.Append`。

### B2. dispatcher 改事件驱动（核心）
改造 `leader/dispatcher.go`：
- **移除**本地 `RegisterExecutor` 同步执行路径（`dispatcher.go:200` `fn(ctx, task)`）。
- **改为**：leader dispatcher 派发任务时 `eventStore.Append(EventSubTaskScheduled)`，sub 通过 `Subscribe` 消费。
- sub 执行完 `eventStore.Append(EventSubTaskResult)` 回传。
- leader `ProcessStream` 订阅 `EventSubTaskResult` 收集结果。
- `cmd/ares/agents.go` 不再 `RegisterExecutor`（移除双重执行路径）。

**步骤**：
1. 先写**契约测试**（事件派发→sub 消费→结果回传的闭环），失败验证当前同步模式不满足。
2. 改造 dispatcher：派发事件、订阅结果。
3. 改造 sub：`ProcessStream` 改为消费 `EventSubTaskScheduled`、产出 `EventSubTaskResult`。
4. `cmd/ares/agents.go` 移除 `RegisterExecutor`，sub 通过 `sub.New` 自带 executor。
5. `go build` + 契约测试通过。

### B3. 安全隔离（谁挂不影响谁）
在每个 sub 的 `ProcessStream` goroutine 加 **panic recovery**：
```go
go func() {
    defer func() {
        if r := recover(); r != nil {
            // 捕获 panic → Append(EventSubAgentFailed) + 设置错误结果
        }
    }()
    ...
}()
```
**步骤**：
1. `sub/agent.go:358` 的 `go func()` 加 `recover()`。
2. panic 时 `eventStore.Append(EventSubAgentFailed)`，错误转为结果而非崩溃进程。
3. 新增测试：executor 内部 panic 时，进程不崩、发出 `EventSubAgentFailed`。

> 说明：Go 单进程内无法真正"进程隔离"，此步实现**故障隔离**（panic 不跨 agent 传播）。真正的进程隔离（daemon/worker）是阶段 D 的方向，见下。

### B4. 阶段 B 验证
- 契约测试全绿、`go build ./...`、`go test ./internal/agents/... ./internal/ares_events/...`、`go test -race`。
- **验收**：leader `ProcessStream`/dispatcher 不再持有 sub executor；sub 独立执行并事件回传；sub panic 不崩进程。

---

## 三、阶段 C：记忆旁路（event 驱动写入）

> 目标：`agent_memory`（写会话/蒸馏/反馈）从 leader 主循环解耦为**事件驱动旁路**，leader 主循环只读上下文。

### C1. 事件触发记忆写入
- leader/sub 的 `ProcessStream` 不再同步调用 `agent_memory` 的写路径。
- 改为：执行完 `Append(EventSubTaskResult)` 后，由**独立消费者**（记忆 worker）订阅结果事件，异步执行蒸馏/会话写入/反馈记录。

### C2. 步骤
1. 抽象 `agent_memory` 为"订阅 `EventSubTaskResult` 的消费 worker"。
2. leader 主循环移除直接写记忆的调用，只保留读取。
3. 新增测试：结果事件产生后，记忆 worker 异步完成蒸馏。

---

## 四、阶段 D（方向，非本次）：进程级安全隔离

> 单进程内只能做到"故障隔离"（panic 不跨 agent）。真正"谁挂不影响谁"的**进程隔离**需要 daemon/worker 架构（参考 prime-agent 的 daemon/worker/kernel）。此为后续演进方向，本次不做，仅在计划中留痕。

---

## 五、实施顺序与风险

| 阶段 | 内容 | 风险 | 前置 |
|------|------|------|------|
| A | 死代码移除 + 持久化/聚合独立包 | 低 | 无 |
| B | 执行模型事件驱动 + 故障隔离 | 中 | A（先减负再改执行） |
| C | 记忆旁路事件驱动 | 中 | B（结果事件已建立） |
| D | 进程级隔离 | 高 | 方向决策 |

**关键依赖**：B 依赖 A 完成（先移除死代码、独立持久化，避免在上帝对象上直接改执行模型）。C 依赖 B（记忆旁路消费 B 建立的结果事件）。

---

## 六、验收标准（最终）

1. **leader 减负**：`leaderAgent` 不再持有 sub executor；`leader/` 包按职责归组（编排/state/aggregate）。
2. **安全隔离**：任一 sub 内部 panic 不崩溃进程，发出 `EventSubAgentFailed`。
3. **event 驱动**：任务派发/执行/结果/记忆写入全部经 `ares_events.EventStore`。
4. **agent 通讯加强**：sub 之间可通过事件/消息直接协作，不强制经 leader。
5. 全部改动通过 `go build ./...`、`go test ./...`、`go vet`、`gofmt`、`go test -race`。

---

## 七、Code Review 与修复记录

> 对已完成解耦代码做 review，确认 A/B/C 均已在生产接通（含暂存区工作），并修复发现的问题。

### 7.1 Review 确认（均已完成，生产接通）

| 项 | 证据 | 状态 |
|----|------|------|
| A 死代码移除 | `internal/agents` 无 `Supervisor`/`Evaluator` | ✅ |
| A 独立包 | `leader/state/`（checkpoint/recovery/event_recovery）+ `leader/aggregate/`（aggregator） | ✅ |
| B 事件驱动 | `dispatcher.dispatchViaEvents` 发布/订阅；`cmd/ares/serve.go:336`、`cmd/monitor-live/main.go:230` 启动 `sub.StartEventListener` | ✅ |
| B 安全隔离 | `sub/agent.go:494` `ProcessStream` goroutine `recover()`（§4.2） | ✅ |
| C 记忆旁路 | 新增 `EventMemoryFinalize` + 独立 `memoryConsumer`（订阅事件异步写入），leader 主循环改为发事件 | ✅（本轮补全） |

### 7.2 修复：`dispatchViaEvents` 结果收集去重缺陷（BUG）

**问题**：`dispatcher.go` 的 `for collected < published` 结果收集循环，`collected++` 对**每个**收到的 `EventSubTaskResult` 计数。若同一 taskID 收到重复结果（sub 重试/事件重放），多 task 场景下重复结果会把 `collected` 推到 `published`，**提前终止循环**，导致其他 task 的结果仍是 nil（返回 nil 结果 → 调用方 panic）。

**修复**：新增 `collectedIDs` 集合，每个已发布 taskID **恰好收集一次**；重复 taskID 跳过且不 `collected++`。

**回归测试**：`TestDispatcher_EventDriven_DuplicateResultNotDoubleCounted`——2 个 task，对 task A 发布重复结果、task B 一次。已验证**修复前该测试 panic**（task B 结果被跳过 → nil deref），修复后通过。

### 7.3 Review 确认非缺陷（不改）

| 项 | 结论 |
|----|------|
| `dispatchViaSender` 遗留路径 | 配置驱动的回退路径（`eventStore==nil` 时才走，用于无 EventStore 的分布式部署）。`SetSuccess("dispatched")` 符合消息队列异步语义（结果经消息回传，非本函数等待）。非 bug，保留。 |
| `Subscribe` 返回 channel 生命周期 | `MemoryEventStore.Subscribe`（memory_store.go:214）注册 ctx.Done→unsubscribe 自动关闭；Postgres 同。**无泄漏**，由 ctx 管理。 |
| sub 双路径（`ProcessStream`/`processScheduledEvent`） | 生产走事件驱动（`StartEventListener`），`ProcessStream` 为主动/兼容路径。均执行同一 executor，非 §5.1 违例（生产单路径）。 |

### 7.4 C 阶段补全：事件驱动的记忆独立消费者（本轮完成）

**此前状态**：C 的"异步化 + 发事件"完成，但 `finalizeMemory` 仍由 leader 主循环**直接调用**（异步 goroutine），未实现文档 C1/C2 的"独立消费者订阅事件"。

**本轮改动**：
1. **新增事件** `EventMemoryFinalize`（`internal/ares_events/types.go`），payload: `session_id`/`task_id`/`result_summary`。
2. **新增独立组件** `memoryConsumer`（`leader/memory_consumer.go`）：
   - `Start` 同步建立 `EventMemoryFinalize` 订阅（订阅就绪后才返回，避免 Emit 事件丢失），返回 error。
   - 消费 goroutine：`UpdateTaskOutput` → `AddMessage`(assistant) → 发 `EventMessageAdded`/`EventTaskCompleted` → `DistillTask`，全部 detached ctx + 2min 超时 + panic recovery（§4.2/§4.3）。
   - `Stop`：取消订阅 + drain 缓冲（不丢 in-flight 写入）+ `wg.Wait`。
3. **leader 主循环解耦**：`agent_memory.go:finalizeMemory` 改为仅发 `EventMemoryFinalize` 事件；`Start` 创建并启动 `memoryConsumer`，`Stop` 停止。
4. **死代码清理**：`distillEg`/`distillWg`（原 leader 内异步蒸馏管理）已无调用方（蒸馏迁至 consumer），移除字段/初始化/Wait。

**新测试**（`leader/memory_consumer_test.go`）：
- `TestMemoryConsumer_EventDrivenClosedLoop`：发事件 → consumer 完成三类写入（UpdateTaskOutput/AddMessage/DistillTask）。
- `TestMemoryConsumer_StopDrainsInFlight`：Emit 后立即 Stop，in-flight 写入不丢。
- `TestMemoryConsumer_StartIdempotent`：Start 幂等。

**修复中发现的根因 bug**：`Start` 原在 goroutine 内异步 `Subscribe`，`Start` 返回后立即 `Emit` 可能订阅未建立 → 事件被静默丢弃（`StopDrainsInFlight` 100% 失败）。已改为 `Start` 同步建立订阅。

### 7.5 验证

`go build ./...`、`go test ./internal/agents/...`、`go test -race ./internal/agents/leader ./internal/agents/sub`、`go vet`、`gofmt` 全部通过（C 阶段测试 `-count=3` 稳定）。
