# ARES Agent Runtime 与调度稳定化开发计划

> 编写日期：2026-11-01。
> 审计基线：`dev` / `580fe71cb271f12d4c3ffe02e573fd2143300e5f`，`VERSION=0.3.6`。
> 测试对象：该提交之上的当前工作区，包含原有未提交改动；不能将结果等同于纯 HEAD 的验证结果。
> 状态：**开发计划与实施记录**。上方日期、基线及第 2 节保留初次审计记录；修补与验收进度见第 3 节。
> 核心约束：**不增加新产品功能，只使现有模块在执行、失败、恢复和关闭路径上闭环。**

---

## 1. 目标与范围

本轮目标不是继续扩展 Agent 能力，而是把已有主链做稳定：

```text
提交 → 会话接纳 → 动态图生成 → 任务投影 → 调度/租约
    → 工具或模型执行 → 检查点/终态提交 → 结果等待
    → 失败处理/恢复 → 会话回收 → Runtime 关闭
```

### 1.1 必须达到的结果

1. 持久模式下，返回成功与可恢复的提交事实一致，存储失败不能只写日志。
2. 编译尚未追上动态图时，不把正常等待误判为会话终态失败。
3. 抢占释放的资源确实能服务对应的高优先级任务，不无谓丢弃无关量子。
4. 超时、取消、关闭具有明确的作用域和时间边界。
5. 任务、会话、租约、订阅、后台循环和恢复投影之间没有未说明的残留。
6. 每项保证都能指向可重复的测试；外部依赖缺失时明确记录“未验证”。

### 1.2 本轮不做

- 不新增 Agent 类型、工具、规划算法、演化算法或第二条执行主线。
- 不新增资源发现、软件安装、通用策略引擎或多租户平台能力。
- 不新增对外任务管理 API、通用成本账本、工具效果账本或展示应用。
- 不以大范围重构替代局部修补，不改变 SDK 默认内存运行模式。
- 不重新实现已有 pump、aging、Peer 自动额度、HTTP 429 或 L1 声明式同步。
- 不承诺任意外部工具的 exactly-once；租约 fencing 不能撤销已经发生的外部副作用。

修复现有契约所必需的内部提交状态、会话级编译进度和错误传播属于本轮范围；涉及公共接口或存储格式变化时，必须先完成兼容性评审，不借机扩展功能。

### 1.3 与现有计划的关系

- [PLAN-0.3.6-scheduling.md](PLAN-0.3.6-scheduling.md)：保留历史调度/L1 事项及原编号，已修项目不重复排期。
- [PLAN-0.3.6-hardening.md](PLAN-0.3.6-hardening.md)：保留跨迭代路线图；本计划只取现有 runtime 闭环所需部分，不自动纳入其新增功能。
- 本计划 RT-01 对应 H01/H02，RT-02 对应 H06，RT-03 对应 H05；接纳与对外契约复核对应 H04/H08。
- 不覆盖旧计划的历史记录，不修改已有 tag；发布版本由实际修补完成后另行决定。

## 2. 已取得的证据与边界

### 2.1 本轮验证记录

| 项目 | 实际结果 | 能证明什么 |
|---|---|---|
| codebase-memory | 26,271 个节点、161,286 条边，索引 ready | 用于定位结构；不证明运行时正确性 |
| CodeScope | 强制索引 1,885 个文件；增强 53,545 个符号、29,965 条调用边 | 新文件已纳入本轮索引范围；统计不等同于缺陷数量 |
| 10 个核心包定向回归 | 使用 Go 1.26.1，race + coverage 全部通过 | 当前测试覆盖的 kernel/fabric/agentruntime/SDK/serve 等路径通过 |
| 全仓短模式 race | 132 个测试包通过，4 个包无测试文件；193 个测试项/子测试跳过 | 已运行路径无测试失败；不代表跳过项通过 |
| `closure` 标签验收 | evolution、ares_evolution、ares_bootstrap 共 15 个包通过 | 对应设计闭环断言通过，不代替故障反例 |
| 临时反例 | 5 个测试各重复 5 次，均按预期暴露缺陷，无 race 警告 | 下表四类问题已复现；属于逻辑缺陷，不是数据竞争报告 |

临时反例使用仓库外 Go overlay，尚未成为仓库回归测试。开发第一步必须将其整理为可提交的测试。

本轮证据文件位于审计机的 OpenCode 临时目录，文件名为：

- `runtime-audit-20261101.log`、`runtime-audit-20261101.cover`。
- `runtime-audit-all-short-20261101.jsonl`。
- `runtime-audit-closure-20261101.log`。
- `runtime-audit-counterexamples-repeated-20261101.log`。

临时目录不是长期交付物：实施时应在对应 PR 或验证记录中保存必要摘要、命令和复现条件，不能只保留本机绝对路径。

### 2.2 尚未验证

- 真实 PostgreSQL 下的事务、断连、提交结果未知和子进程恢复。
- 真实模型与外部 MCP 服务的故障行为。
- 长时间混合负载 soak、进程级故障后的完整 serve 恢复。
- 运行中 scheduler 与提交失败清理之间的竞争；暂停 scheduler 的清理测试不能替代这一验证。

当前不能认定“所有模块已经稳定闭环”。后续需要区分：**已复现、修补后通过、仍未验证、明确不保证**。

## 3. 首批修补清单

| 编号 | 优先级 | 已复现行为 | 主要位置 | 状态 |
|---|---|---|---|---|
| RT-01 | P0 | `task.created`/`task.completed` Append 失败，Create/Complete 仍返回成功；完成未提交已解锁后继；恢复重建与内存不一致 | `internal/fabric/task/fabric_events.go` 的 `flushAppends`，及生命周期调用方 | 已复现，待修 |
| RT-02 | P1 | DAG version=2、投影 version=1 时，waiter 判为 stalled；解除订阅阻塞后答案节点能正常投影 | `internal/agentruntime/session.go` 的 `SessionStalled`/`StallDetector`，`await.go`，编译协调器 | 已复现，待修 |
| RT-03 | P1 | 两类 Agent 全忙，紧急 database 任务首次抢占了低优先级 code 任务，database Agent 仍未释放 | `internal/kernel/scheduler_dispatch.go` 的 `PreemptLowerPriority` | 已复现，待修 |
| RT-04 | P1 | 调用方给 25ms deadline，Shutdown 在 250ms 时仍等待后台循环；人工解除阻塞后返回 nil | `internal/kernel/orchestrator.go` 的 `Shutdown` errgroup 等待段 | 已复现，待修 |

**当前状态（RT-02 更新于 2026-11-05，其余保留各自验证记录）**

| 编号 | 状态 | 依据 |
|---|---|---|
| RT-04 | 已修补，反例已固化 | 关停预算、Stop 有界、Wait 失败传播、依赖链保护、重复调用共享结果、panic 边界、`WaitBackgroundContext`、`finishBackgroundWait`、`runServeTeardown`（signal 与非 signal 共用）；`orchestrator_shutdown_*` / `serve_background_wait_test.go` 覆盖 |
| RT-01 | 已修补，待完整验收 | 三态提交契约（`commit.go`）、Create/Complete/Fail/Delete/Yield/`CompilePlan`/`ExpireDeadlines` 的结局传播、可见性闸门（`CommitPending` + 依赖/waiter 共用闸门）、稳定事件 ID 与核验账本（`reconcile.go`）。**未完成**：账本持久化（当前仅进程内）、真实 PostgreSQL 断连与提交未知、子进程重建验收 |
| RT-02 | 已修补；判定层及共享 await 入口回归成立（2026-11-05） | `SessionTopology` + `SessionStalledWith`；已接线 eval（`e.sessions`）、evolution（kernel 注册表）、SDK（`execCore.Sessions`）、HTTP 读取路径。`await_topology_test.go` 在同一夹具下验证无拓扑返回 `ErrSessionStalled`、带拓扑等待并收到延迟答案；连续 20 次通过，删除 await 内的拓扑传递后两项对照子测试失败。独立调用方接线故障注入、真实订阅补偿与多会话验收仍待补 |

**RT-02 共享等待入口验收记录（2026-11-05，Go 1.26.1 + race）**

入口核查：

- `AwaitSessionResult` 的三个生产直接调用方：`internal/agentruntime/eval_executor.go`、`sdk/l2_submit.go`、`cmd/ares/evolution.go` 的 `executeAskViaSession`，均传入 topology。
- HTTP 等待经 `waitForTaskResult → TaskResolved → StallDetector.Stalled`；HTTP 终态读取经 `terminalViewFields → SessionStalledWith`，两处均接入 topology。
- `ask_agent` 的发送端等待 IPC 回复；evolution 模式下，协作主题的接收端由 `wireEvolutionIPC` 接入 `executeCollabViaKernel → executeAskViaSession → AwaitSessionResult`。发送端文件没有 stall 判断，不能据此排除接收端；它复用上面的 evolution 入口，并非第四个 shared-await 直接调用方。

复现前提与旧夹具的边界：

- 非空 `planTaskID` 必须对应存在且完成已确认的任务；DAG root 与提交产生的 `peer-plan-*` 是两个任务，完成前者不会完成后者。
- 会话前缀下至少存在一个已投影任务，且所有这些任务均已结束；没有匹配任务时 `seen=false`，不会判 stalled。
- `planTaskID=""` 只跳过计划任务检查，不跳过会话任务检查。答案成功、答案失败、计划失败仍保持原有提前返回语义。
- 删除的旧入口夹具无法逐行复核，不能将其 deadline 结果归因于“预算先于五次确认”。新增 `StallPrerequisites` 测试证明：缺失/未完成的计划任务、空会话任务集或未完成的会话 root，均会阻止无拓扑 stall。
- 新夹具使用 `InitSession(..., nil)` 显式关闭自动投影订阅，先投影并完成 root，再完成独立提交任务；答案只加入 DAG。测试等待超过五次轮询后，才用真实 `CompileNode` 和完成协议发布答案。`testing/synctest` 控制顺序与时间，不靠真实 Sleep 猜测。

| 验证 | 结果 | 证据 |
|---|---|---|
| 无拓扑对照 → 带拓扑继续等待 → 释放投影得到答案 | PASS ×20 | `TestAwaitSessionResult_PendingProjection`：非空计划 ID、空计划 ID 两个子场景；同一图与 fabric，仅切换 topology |
| pending 持续时遵守等待预算、调用方 deadline、取消 | PASS ×20 | `TestAwaitSessionResult_PendingProjectionHonorsWaitBoundary`；返回相应 context 错误且 `Stalled=false`，现有调用方 deadline 优先于 `opts.Wait` |
| stall 前提检查 | PASS ×20 | `TestAwaitSessionResult_StallPrerequisites`，5 个子场景 |
| 变异反证 | 预期 FAIL（2 个子场景） | 仓库外 Go overlay 将 `AwaitSessionResult` 内 `.WithTopology(opts.Topology)` 改成 `.WithTopology(nil)`；上述答案测试在释放投影前返回 `Stalled=true` |
| 相关包回归 | PASS | `GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race -count=1 -timeout=300s -p 4 ./internal/agentruntime ./internal/fabric/planprojection ./sdk ./cmd/ares` |

重复验证命令：

```sh
GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race ./internal/agentruntime \
  -run '^TestAwaitSessionResult_(PendingProjection|StallPrerequisites)' -count=20 -timeout=60s
```

本记录证明共享等待入口的回归有区分力；不代替各调用方独立接线的变异测试，也不代表真实订阅延迟/丢事件补偿、编译失败反馈、多 DAG/多轮、释放/重建验收全部完成。

**PR-03 验收记录（2026-11-04，Go 1.26.1 + race）**

| 项 | 结果 | 证据 |
|---|---|---|
| 组合回归：已提交未确认的完成 | PASS | `TestDurability_CommittedButUnacknowledgedCompletionSettles` |
| 组合回归：批量部分提交 | PASS | `TestDurability_PartialBatchKeepsCommittedAndIsolatesTheRest` |
| 组合回归：删除与核验并发 | PASS | `TestDurability_DeleteDuringReconciliationIsNotUndone` |
| 核心反例连续 20 次 | PASS（33 个用例 ×20 = 660，0 失败） | `-run 'TestAudit|TestDurability_|TestReconcile_|TestComplete_|TestFail_|TestYield_|TestDelete_' -count=20` |
| 定向回归（5 个包，race） | PASS | `GOTOOLCHAIN=go1.26.1 go test -race ./internal/ares_events ./internal/fabric/task ./internal/agentruntime ./internal/kernel ./cmd/ares` → 全 ok |
| 间歇用例 `TestScheduledCountsTasksNotQuanta` | 已修（等待计数稳定） | 连跑 20 次通过 |
| PostgreSQL 断连 / 提交未知 | **SKIP** | 无隔离 PG；`TEST_POSTGRES_DSN` 未设置，`TestPostgresEventStore_*` 全部 SKIP |
| 子进程重建（完成不重跑/删除不复活） | **未做** | 需要可用 PG 与进程注入 |
| 后端契约表核对 | **部分** | 代码中只有 Memory 与 PostgreSQL 两种 `EventStore`；无 SQLite 实现，计划中的该行无从核对 |


| RT-03 | 未开始 | — |

RT-01 的恢复实验是拒绝 Append 后使用 `RestoreFromStore` 重建，不是物理断电或真实进程重启。RT-03 证明的是首轮无效抢占与额外扰动，不据此断言高优先级任务永远不能执行。

## 4. 开发阶段与验收

### 阶段 A：固化反例与契约基线

**工作：**

- [ ] 记录实施时的 commit、工作区差异、Go 版本、测试环境和跳过原因。
- [ ] 将以下 5 个临时反例整理到对应包，避免依赖审计机 overlay：
  - `TestAuditCreateMustNotAcknowledgeFailedPersistence`。
  - `TestAuditFailedCompleteMustNotUnlockDependent`。
  - `TestAuditQueuedAnswerProjectionMustNotDeclareStalled`。
  - `TestAuditPreemptionMustFreeAMatchingExecutor`。
  - `TestAuditShutdownMustRespectCallerDeadlineWhileDraining`。
- [ ] 故障夹具以 channel 控制先后顺序；时间窗口只做上限，不依赖固定 Sleep 猜测并发时序。
- [ ] 明确新断言的目标契约与现有行为差异；反例与相应修补成对提交，不让主分支长期处于红灯。

**验收：**修补前各反例可靠失败，修补后保留同一核心断言；连续执行至少 20 次仍稳定。

### 阶段 B：RT-01 持久提交与可见性

这是首要工作，也是接纳、恢复和旁路消费者闭环的前置条件。

**工作：**

- [ ] 为现有 Memory/SQLite/PostgreSQL EventStore 建立契约表：Append 成功含义、单 stream 批量原子性、版本冲突、事件 ID、通知时刻、超时后的提交状态。
- [ ] 区分确定拒绝、版本冲突、提交结果未知；不能把 `context.DeadlineExceeded` 一律当作“未写入”。
- [ ] 优先复用现有 Event.ID 与版本机制；不默认所有后端支持去重、首写 CAS 或跨 stream 事务。
- [ ] 设计最小内部提交边界：同一 task 的变更有序，未确认提交的状态不能作为调度或结果成功依据。
- [ ] 存储 I/O 不长期持有全局 `Fabric.mu`；待提交变更与租约/版本校验相协调。
- [ ] 向 kernel、agentruntime、SDK/HTTP 调用链传播错误；仅让 `flushAppends` 返回 error 不是完整修复。
- [ ] 同时检查 Create、Checkpoint、Complete、Fail、Expired、Delete 和审批状态，以及批量编译/依赖级联路径。
- [ ] 审核事件订阅、演化反馈、记忆蒸馏和回收时机，确保“成功”消费者不提前消费未提交事实。
- [ ] 批次部分写入或提交结果未知时，阻止不安全的后继推进并报告原因；不能仅回滚内存伪装成数据库回滚。
- [ ] 保留无 EventStore 模式的既有行为，并说明其不提供跨进程恢复保证。

**验收：**

- [ ] 确定写入失败时不能返回成功；Create 不留下可执行的幽灵任务，Complete 不解锁后继。
- [ ] Append 阻塞期间，并发 drain/waiter 看不到未确认的成功状态。
- [ ] “写入成功但响应超时”有明确确认/失败关闭路径，不盲目重做终态或工具执行。
- [ ] 单任务与多任务变更分别测试；已提交事实和恢复结果按契约一致。
- [ ] 一条慢 stream 不把无关任务长期堵在全局锁上。
- [ ] 已完成任务不复活、已删除任务不回流、依赖与审批状态可恢复。
- [ ] 内存与持久后端的测试共用语义断言；真实持久后端另做子进程恢复验证。

**停止线：**若现有存储能力不足，先提交最小设计与兼容性评审；不能用默认参数绕过版本冲突，也不自动引入通用事务平台。

### 阶段 C：RT-02 编译进度与会话终态

**工作：**

- [ ] 复用现有 DAG version、`LastChange` 和投影结果，区分“开始编译”“成功投影”“仍有 Skipped/失败”。
- [ ] 编译进度按会话/DAG 归属；共享 CompileCoordinator 的最后一次全局结果不能直接当作每个会话的确认版本。
- [ ] 将待处理图变更纳入 shared await/stall 判断，使 SDK 与 HTTP 使用一致依据。
- [ ] 若缺少必要状态，只补最小内部进度标记，不新增公开编译管理接口。
- [ ] 编译真实失败与临时容量拒绝分别处理；日志不能是唯一反馈渠道。
- [ ] 检查丢事件补偿、Reconcile 失败/Skipped 后的重试，以及重新接纳会话时旧结果清理。
- [ ] 保留空答案、失败答案、计划失败的现有快速失败语义；不靠无限增加 `StallConfirmations` 掩盖问题。

**验收：**

- [ ] 暂停订阅使答案事件等待超过旧的五次 poll 窗口，waiter 不误报 stalled；解除暂停后能正常拿到答案。
- [ ] pending 未收敛时只能继续等待或按 deadline 返回有原因的错误，不能声称确定终态。
- [ ] 真编译失败可定位；丢事件和队列拒绝解除后能够收敛。
- [ ] 多个 DAG 同时变更、同会话多轮、释放/重新接纳都不会串用进度或旧答案。

### 阶段 D：RT-03 能力匹配的有效抢占

**工作：**

- [ ] 对每个待执行高优先级任务匹配可用资源与可抢占资源，而非只比较全局最大优先级和能力集合总数。
- [ ] 受害任务的 owner 必须能服务对应待执行任务，且受害任务优先级确实更低。
- [ ] 处理多能力 Agent、重复需求、静态 executor、动态 fabric Agent 和 recovery-bound executor。
- [ ] 候选选择遵循已有绑定、资格和预算约束；不要将不可用资源算作可兑现容量。
- [ ] 保留确定性排序与“只抢占实际所需数量”；不新增公平策略或调度算法框架。
- [ ] 复核协作式抢占与 in-flight 量子计数：任务状态回 READY 不等于运行中的 goroutine 已退出。

**验收：**

- [ ] 紧急 database 任务不抢占只能执行 code 的 Agent。
- [ ] 无匹配执行者、存在匹配空闲执行者、所有优先级相同的场景均不无谓抢占。
- [ ] 一个多能力 Agent 不能被重复计作多个同时可用资源。
- [ ] 静态与生产 Peer 接线均验收；并发上限不突破，旧 epoch 完成被拒绝。
- [ ] 用受控 cognition 观察真实开始执行的顺序，而不只断言任务状态改成 READY。

### 阶段 E：RT-04 关闭预算与生命周期

**工作：**

- [ ] errgroup 等待同时响应调用方 `ctx.Done()`，使用剩余整体预算，不额外重开完整 30s 等待窗口。
- [ ] deadline/cancel 错误可以通过错误链识别；超过预算不能返回 nil 或声称全部关闭。
- [ ] 统一 Stop/Wait、scheduler in-flight、图订阅、event store 的退出顺序：停止接纳与生产，处理在飞工作，再关闭其依赖。
- [ ] 核对多次/并发 Shutdown、Start/Adopt 与 Shutdown 竞争的行为，保留既有幂等和拒绝晚注册语义。
- [ ] 不在仍有合法提交者时提前关闭存储，不把超时退出当作后台 goroutine 已消失。

**验收：**

- [ ] 忽略 context 的受控后台循环存在时，Shutdown 在调用方预算加合理测试容差内返回可识别错误。
- [ ] 已过期 ctx、无 deadline、正常退出、Stop/Wait 失败、多次并发关闭均有断言。
- [ ] 协作式后台循环关闭后无租约、订阅或 goroutine 残留。
- [ ] 不合作的循环被明确报告；不能承诺 Go goroutine 可以被强杀。

### 阶段 F：现有模块端到端闭环与发布验收

前四项修补后，以真实接线检查模块组合，不只各自跑单元测试。

| 闭环对象 | 必测场景 | 完成依据 |
|---|---|---|
| HTTP/SDK → 接纳 | 新会话、续轮、重复/并发提交、队列满、请求断开 | 已接受与被拒绝的工作边界明确，无错误回收他人会话 |
| 接纳 → scheduler | root 已被领取后 plan 创建失败；清理与 drain 竞争 | 被拒绝提交不留下可继续执行的孤儿工作；合法在飞工作有明确收尾 |
| DAG → task 投影 | 延迟、丢事件、Skipped、编译失败、释放/重建 | 图任务能够收敛，失败不是只写日志 |
| scheduler → 执行 | 抢占、租约过期、Agent 死亡、panic、重试、预算耗尽 | fencing 和容量有效；不可执行原因可解释 |
| 工具 → checkpoint | 超时/取消后晚返回，抢占后旧结果，检查点写入失败 | 外部副作用与可安全重试边界被验证，不声称 exactly-once |
| task → 等待结果 | 成功、失败、空答案、慢投影、wait 超时、主动取消 | HTTP/SDK 按各自既有等待语义返回；等待结束不被混同为执行取消 |
| event → 记忆/演化 | 重复通知、消费者延迟、事件通道关闭、存储失败 | 未提交成功不进入成功反馈，消费者按既有契约处理重复与退出 |
| L1 → planner/router | 空声明、工具增删、enabled/timeout/retry 约束 | 已实现规范不被回退、热变更或恢复路径绕过 |
| 日志/快照 → 恢复 | 部分进度、已完成/已删除任务、依赖、审批、损坏数据 | 已提交事实可恢复；不确定结果与不可恢复数据显式报告 |
| Runtime → 回收/关闭 | 会话释放、reaper、关闭中提交、deadline 到期 | 无静默残留，不在关闭后继续发布成功 |

**工作与验收：**

- [ ] 先为覆盖缺口写失败测试；未成立的静态风险不直接升级为缺陷。
- [ ] 在隔离测试环境运行真实持久后端；核对测试夹具确实读取测试库配置，不仅设置环境变量。
- [ ] 子进程故障注入覆盖持久提交前后、计划生成/投影间隙、工具返回/检查点之间。
- [ ] 真实外部模型/MCP 验证按需 opt-in，不能作为唯一稳定性判据；主要故障回归必须离线可重复。
- [ ] 检查已有预算与指标口径；量子数不得被误称为实际工具调用数，不新增成本账本。
- [ ] 使用现有压测/soak 入口先跑 30min 冒烟，再跑至少 2h 混合负载；记录配置、随机种子和原始数据。
- [ ] 固定活跃会话规模下观察 goroutine、订阅、任务/会话占用、内存与队列趋势；回收后不能持续单调增长。
- [ ] 修正文档中的旧执行路径说明，更新必要验证记录；未实现的公共操作只说明限制，不借验收新增 API。

## 5. 实施顺序、PR 切分与估算

```text
A 固化反例与基线
├─ B 持久契约评审 → 提交/可见性修补 → 恢复与消费者复核 ─┐
├─ C 编译进度与会话终态 ─────────────────────────────┤
├─ D 有效抢占 ───────────────────────────────────────┤
└─ E 关闭预算 ───────────────────────────────────────┤
                                                    ↓
                                      F 端到端、真实存储、soak、发布验收
```

B 优先启动；C/D/E 是相对独立的局部修补，可以先完成，但必须在 B 合入后重跑组合测试。B 内部未完成批次/消费者复核前，不能宣称持久闭环完成。

| PR | 交付内容 | 合入条件 |
|---|---|---|
| PR-01 | 持久提交契约、后端能力矩阵、兼容性决定与反例夹具 | 设计明确，区别确定失败与提交未知 |
| PR-02 | 单 task 提交、错误传播、调度/结果可见性修补 | RT-01 两个反例及并发观察测试通过 |
| PR-03 | 批次/级联/审批/删除、恢复与成功消费者收尾 | 部分失败不发布幽灵成功，真实存储验证通过 |
| PR-04 | 会话级编译进度与 waiter 修补 | RT-02、多 DAG、多轮与丢事件补偿通过 |
| PR-05 | 能力匹配的抢占修补 | RT-03、真实 Peer 开始执行与容量测试通过 |
| PR-06 | Shutdown deadline 与生命周期收尾 | RT-04、退出顺序与并发关闭测试通过 |
| PR-07 | 组合故障验收、soak 与文档收尾 | 发布门槛全部满足；未验证边界如实记录 |

以上是交付边界，不强制一项一个 commit。修补与红灯反例应成对交付；共享代码改动只做一次。

| 阶段 | 粗估人天 |
|---|---:|
| A | 1 |
| B | 6–9 |
| C | 2–3 |
| D | 1–2 |
| E | 1 |
| F | 4–6 |
| 合计 | **15–22** |

估算以一名熟悉项目的开发者、隔离测试环境可用为前提，不是日历承诺；B 完成契约评审后重估。若必须修改存储格式，先重新界定范围，不默认追加到本轮。

## 6. 回归命令与验证记录

使用 `go.mod` 声明的 Go 1.26.1，控制测试并发。以下为开发验收命令，不表示本次写计划时再次执行过。

```sh
# 定向修补回归
GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race -count=1 -timeout=300s -p 4 \
  ./internal/fabric/task ./internal/kernel ./internal/agentruntime \
  ./internal/fabric/planprojection ./internal/ares_events ./sdk ./cmd/ares

# 全仓快速回归；必须记录 skip，不能拿短模式替代全部验收
GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race -short -count=1 -timeout=300s -p 4 ./...

# 已有设计闭环标签
GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race -tags closure -count=1 -timeout=300s -p 4 \
  ./internal/runtime/ares_evolution/... ./internal/runtime/evolution/... ./internal/ares_bootstrap/...

# 非短模式回归；外部依赖仍可能 opt-in 或跳过，须单独检查
GOTOOLCHAIN=go1.26.1 GOMAXPROCS=8 go test -race -count=1 -timeout=300s -p 4 ./...
```

另执行项目现有格式、静态检查、文档锚点与冻结边界检查。数据库、live、soak 测试使用各自真实入口和适当超时，不对生产库执行故障实验。

每个修补的验证记录至少包含：问题 ID、基线 commit/工作区差异、故障时刻、预期/实际行为、命令、pass/fail/skip、修补前后对照、兼容性影响、尚未保证的边界。

## 7. 最终完成标准

- [ ] RT-01 至 RT-04 全部修补；5 个反例进入仓库并连续至少 20 次通过。
- [ ] race、定向、全仓、closure 与现有静态检查通过，没有依靠跳过或放宽断言隐藏问题。
- [ ] 持久成功、编译终态、有效抢占、关闭 deadline 均有真实接线的组合测试。
- [ ] 接纳失败、取消/超时、工具晚返回、恢复、回收路径覆盖，并明确不确定效果的处理边界。
- [ ] 真实持久后端和子进程恢复验证完成；外部依赖未验证项不能被标为通过。
- [ ] soak 原始记录可复现，无已知的持续资源增长、静默卡死或超预算成功关闭。
- [ ] 文档与实现一致；已有未提交用户改动没有被覆盖，未引入计划外功能。

**完成口径：现有模块在已声明部署与故障边界内闭环、可恢复、可解释；不是所有任意故障或外部副作用都得到无限保证。**
