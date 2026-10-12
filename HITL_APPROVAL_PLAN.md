# ARES HITL 审批门接线计划（Human-in-the-Loop Approval Gate）· rev1

> **状态：方案待拍板（2026-10-11 新建）**。本文档是把现存但未接线的 HITL 子系统落成"任务执行前的人工审批门"的详细改动计划。遵循 `plan/rules/code_rules_v2.md` 与 `plan/0.3.3_task.md` 的既有红线（两个闸门、持久化可恢复、缺省逐字节兼容、不新增状态机迁移）。
>
> **一句话**：某个 step 被显式标成"需人工批准"时，它对应的任务进入 READY 但**被一道审批闸门挡住、不得被 `Acquire` 执行**；人类经 HTTP 端点 approve → 闸门放开、正常调度；reject → 走既有 `READY→FAILED` 终态路径 + 级联。
>
> **关键设计判定（与初次口头讨论的修正）**：不走 `SUSPENDED` 中途挂起，改走 **READY 预执行闸门**。理由见 §2.1——`state.go:48` 只允许 `SUSPENDED→LEASED/READY`，不允许 `SUSPENDED→FAILED`，而 0.3.3 §2.2 红线"FAILED 是终态、不新增迁移"禁止加边；审批是**执行前**的事，用预执行闸门语义更准、零新增迁移、复用 0.3.3 的"两个闸门"模式。

---

## 0. 现状核对（逐文件 grep 确认，附 file:line，不是架构图推断）

| 机制 | 现状 | 证据 |
|---|---|---|
| 任务状态机 | `READY/LEASED/RUNNING/SUSPENDED/COMPLETED/FAILED`；`canTransition` 中 `READY→FAILED` **合法**、`SUSPENDED→FAILED` **非法** | `internal/fabric/task/state.go:8-19`、`canTransition` `state.go:38-51`（`READY: to==LEASED||to==FAILED` `:40`；`SUSPENDED: to==LEASED||to==READY` `:48`） |
| 两个闸门（调度 + 授权） | 调度侧 `ReadyTasks`/`ResumableTasks`，授权侧 `Acquire`；0.3.3 的退避/截止都落在这两处 | `dag.go:39`、`dag.go:70`、`fabric_lifecycle.go:87`（deadline 守护 `:102-108`、backoff 守护 `:109-115`） |
| 终态入口（不依赖租约） | `ExpireDeadlines` 经 `failTerminalLocked` 把 live 态直接推 FAILED + 级联，**无需 ownership** | `fabric_lifecycle.go:436`、`failTerminalLocked` `:454`、`cascadeFailureLocked`（`dag.go`） |
| PlanStep → Task | `CompilePlan` 从 `PlanStep` 建 `Task{}`，`f.now()` 解析相对 Deadline | `workflow_plan.go:104`、`Task{}` 建构 `:160-170`、Deadline `:175-177` |
| **ProjectStep 丢弃 Interrupt** | `engine.Step.Interrupt` 过桥**被显式丢弃**，docstring 明列 | `projection.go:58`、丢弃声明 docstring `projection.go:55-57`；`engine.Step.Interrupt` 字段 `types.go:71` |
| 事件持久化 + restore 折叠 | 调度态按"有信息量才写"进 payload，`restoreKey*` 常量 + `fold*` 在 restore 折回 | 写 `fabric_events.go:151-196`（`next_attempt_at` `:166-168`、`allow_partial` `:178-180`）；键 `restore.go:36-100`；折叠 `foldRetryScheduling` `restore.go:371`、`foldDegradation` `:389`、调用点 `:255`/`:302` |
| 恢复环 tick | `runKernelRecoveryLoop` 的 sweep 调 `ExpireTaskDeadlines`/`RequeueExpiredLeases`；ticker + 事件触发 | `cmd/ares/kernel_loop.go:275`、sweep 闭包 `:363-403`、`:389`、事件触发 `:447-451` |
| HTTP 路由 | `actionRoutes []routeSpec` 注册表；handler 签名统一；`{param}` 段匹配 | `cmd/ares/agent.go:419`、task 路由 `:462-470`、`routeSpec` `:378-402`、`match` `:480`；handler 样板 `agent_routes_tasks.go:117,163,163`、`k.fabric.Create` 直建样例 `:613` |
| **现存 HITL 构件（已写、未接线）** | `InterruptConfig{Message,Payload}`、`InterruptPoint/Result/Handler/Store`、`MemoryInterruptStore`、`HITLFeedbackPlugin`（仅观测）、`InterruptPlugin`（runtime 观测）；两事件常量 | `types.go:101-104`、`engine/hitl.go:10,17,25,28,47,54`、`engine/hitl_plugin.go:12,22,55,73`、`runtime/interrupt.go:15,23`、`runtime/events.go:28,31` |
| **两道架构门禁把 HITL 标为"未来之家"** | `NewInterruptPlugin` 与 `InterruptConfig` 在白名单里，一旦获得生产引用则门禁转红（must-shrink） | `runtime/architecture_test.go:54`（`testOnlyPluginAllowlist`，gate `TestPluginsMustBeWiredInProduction` `:60`）、`engine/architecture_test.go:41`（`deadTypeAllowlist`，gate `TestExportedTypesMustHaveProductionReferences` `:48`） |
| **分层门禁：fabric 核心禁依赖 runtime** | `internal/fabric/task`（及 `agent`/`planprojection`）**不得 import `internal/runtime`**——源码扫描，命中 banned 前缀即红 | `internal/fabric/task/architecture_test.go:29` `TestFabricCoreMustNotImportRuntime`，banned 前缀 `:45`。**这决定了 HITL 事件常量不能留在 `internal/runtime`**（见 §2.5 落点订正） |

**三条必须记住的约束**：

1. **审批是执行前的闸门，不是中途挂起**：任务在被 `Acquire` 之前就要挡住；用 `SUSPENDED` 反而受 `state.go:48` 的出边限制（见 §2.1）。
2. **reject 必须走合法迁移**：`READY→FAILED` 合法（`state.go:40`），直接复用 `failTerminalLocked`（无需 ownership，同 `ExpireDeadlines`）+ `cascadeFailureLocked`。**不加任何新迁移边**。
3. **两个闸门同改**：调度侧（`ReadyTasks`+`ResumableTasks` 含 SUSPENDED 分支）与授权侧（`Acquire`）必须同时挡——否则会"既不执行也查不到"或"绕过审批被提前拿走执行权"（0.3.3 §7 风险1 的教训）。

---

## 1. 目标（只有 4 条）

| # | 目标 | 验收 |
|---|---|---|
| G1 | step 可声明"需人工批准"，并真正贯穿到任务层 | 作者在 `engine.Step.Interrupt` 设 `Message` → `ProjectStep` 不再丢弃 → `Task` 带审批策略；缺省（未声明）逐字节兼容 0.3.2。**YAML 作者面见 §2.6 末尾**：`StepFile` 当前无 `interrupt` 字段，必须补，否则"作者声明"只在 Go 面可达 |
| G2 | 未批准的任务**不得被调度、不得被 `Acquire`** | 单测：pending 任务不出现在 `ReadyTasks`/`ResumableTasks`；`Acquire` 返回 `ErrTaskAwaitingApproval`；重启回放后仍被挡 |
| G3 | approve → 放行并正常调度；reject → 终态失败 + 级联 | 单测：approve 后任务进入可调度集合并可 `Acquire`；reject 后任务 FAILED、下游按既有级联（`AllowPartial` 下游不被级联） |
| G4 | 人类经 HTTP 看到待批、回传决定；鉴权受控 | e2e：`GET /api/interrupts` 列待批；`POST /api/interrupts/{id}/resolve` 走 `authWrite`、approve/reject 生效；崩溃重启后待批仍在 |

**非目标**（见 §6）：approver 角色/RBAC、按 capability 危险度自动标、`SUSPENDED` 中途审批、超时自动批/驳、engine 侧 `MemoryInterruptStore`（任务账本即存储）。

---

## 2. 设计

### 2.1 为什么用"READY 预执行闸门"而非 `SUSPENDED`

初次讨论提过 (a) 阻塞式 / (b) `SUSPENDED` 挂起式。核实状态机后两者都不最优：

- **`SUSPENDED` 的出边受限**：`state.go:48` 只允许 `SUSPENDED→LEASED/READY`。人类 **reject** 想让任务失败时，`SUSPENDED→FAILED` **非法**（`canTransition` default 分支 `state.go:50` 返回 false）。
  - **精确机制（订正）**：`ExpireDeadlines` 的状态白名单其实**包含 `StateSuspended`**（`fabric_lifecycle.go:439-444`），不是在循环里被跳过；真正的拦截发生在 `failTerminalLocked` 内部——它调 `t.transition(StateFailed)`（`fabric_lifecycle.go:309-310`），`canTransition(SUSPENDED, FAILED)` 为 false → 返回错误 → 循环 `continue` 跳过该任务。**净结论不变**：不加迁移边，SUSPENDED 任务到不了 FAILED。要支持 reject 就得**新增迁移边**，直接违反 0.3.3 §2.2 红线"FAILED 是终态、不新增迁移"。
- **语义错位**：`SUSPENDED` 是**运行中** `Yield` 的产物（`fabric_lifecycle.go:156`、`state.go:46` 仅 `RUNNING→SUSPENDED`）。审批发生在**执行前**，任务此刻还是 READY，没有也不该有 RUNNING 过。

**采用的模型**：任务照常 `createLocked` 进 READY；给它挂一道**审批闸门**，和既有的 backoff 闸门（`NextAttemptAt`）同构——pending 时 `ReadyTasks`/`ResumableTasks` 不收它、`Acquire` 拒绝它。approve 翻开闸门（仍是 READY，调度器下一轮自然取走）；reject 走合法的 `READY→FAILED`。**零新增迁移、复用 0.3.3 两个闸门骨架、从人类视角任务确实"被暂停到批准为止"**。

> 结论：满足"需高级权限 → 暂停让人类审核"的诉求，且不碰状态机不变量。

### 2.2 数据模型（`Task` + `PlanStep` 各加策略/运行态）

```go
// internal/fabric/task/task.go — Task 新增（紧挨 AllowPartial/BackoffBase 等 0.3.3 字段）
RequiresApproval bool           // 策略：创建时确定，true=执行前必须人工批准
ApprovalMessage  string         // 策略：给审批人看的提示（来自 InterruptConfig.Message）
ApprovalPayload  map[string]any // 策略：附带上下文（来自 InterruptConfig.Payload），可空
ApprovalDecision string         // 运行态："" 未决 | "approved" | "rejected"
ApprovalFeedback string         // 运行态：人类反馈（reject 原因 / approve 备注）
ApprovalBy       string         // 运行态：决定人标识（principal），审计用
ApprovalAt       time.Time      // 运行态：决定时间
```

```go
// internal/fabric/task/workflow_plan.go — PlanStep 新增（紧挨 AllowPartial :39）
RequiresApproval bool
ApprovalMessage  string
ApprovalPayload  map[string]any
```

- **可调度判据**（新内部谓词，供两个闸门复用）：
  ```go
  // dag.go，紧挨 retryDueLocked :95
  func approvalCleared(t *Task) bool {
      return !t.RequiresApproval || t.ApprovalDecision == "approved"
  }
  ```
  pending（`""`）与 rejected 都返回 false → 两种都不可调度；rejected 另由 resolve 路径同步推 FAILED（见 2.4），不会一直占着。

### 2.3 闸门落三处（调度两处 + 授权一处）

1. **`ReadyTasks`**（`dag.go:39`）：现有过滤 `State==READY && retryDueLocked && depsSatisfiedLocked`（`:44-50`）后，**追加** `&& approvalCleared(t)`。
2. **`ResumableTasks`**（`dag.go:70`）：**两个分支都要加**——READY 分支（`:77-80`）和 SUSPENDED 分支（`:83-86`）各追加 `approvalCleared(t)`。（SUSPENDED 分支当前无 deps/backoff 闸门，但一个 approval=required 的任务在本设计里不会处于 SUSPENDED；加上是为契约一致、防御未来路径。）
3. **`Acquire`**（`fabric_lifecycle.go:87`）：在 deadline 守护（`:102-108`）与 backoff 守护（`:109-115`）**之间或之后**插一道：
   ```go
   if t.RequiresApproval && t.ApprovalDecision != "approved" {
       return 0, ErrTaskAwaitingApproval
   }
   ```
   新增 sentinel `ErrTaskAwaitingApproval`（**`fabric.go` 的 error 区**，紧挨 `ErrTaskNotReady`（`fabric.go:26`）；这些 sentinel 实际都定义在 `fabric.go`，**不在 `task.go`**）。

> 理由同 0.3.3 §3.2②：`Acquire` 是公开 Fabric 方法，只过滤查询等于把约束交给调用者自觉。

### 2.4 approve / reject 入口（无需 ownership，同 `ExpireDeadlines`）

```go
// fabric_lifecycle.go，紧挨 ExpireDeadlines :436（failTerminalLocked 本体在 :309，不是 :454）
// 人类带外决定，不走 ownerLocked（与 ExpireDeadlines 一致：f.mu 下直接操作）
func (f *Fabric) ResolveApproval(id, decision, feedback, by string) error
// 锁内：
//   取任务；校验 t.RequiresApproval（否则 ErrApprovalNotRequired）
//   校验 decision ∈ {"approved","rejected"}（否则 ErrInvalidApprovalDecision）
//   幂等：若 ApprovalDecision 已非空且相同 → no-op 返回；不同 → ErrApprovalAlreadyResolved
//   写 ApprovalDecision/Feedback/By/At
//   approved: 不改 State（仍 READY），recordLocked(t, EventInterruptResolved)（payload: approved=true）
//   rejected: failTerminalLocked(t, ErrInterruptRejected)（READY→FAILED 合法）→ 内部已发 task.failed + cascadeFailureLocked；
//             另 recordLocked(t, EventInterruptResolved)（payload: approved=false, feedback）
```

- **approve 后谁重新调度**：无需额外 sweep。任务仍是 READY，调度器下一轮 `ResumableTasks`/`ReadyTasks` 轮询自然取走。为降低延迟，`EventInterruptResolved` 可挂进 `kernel_loop.go:447-451` 的事件触发集合唤醒 sweep（见 §3 M3 可选项）。
- **reject 的级联**：`failTerminalLocked` 已复用 `cascadeFailureLocked`，`AllowPartial` 下游不被级联（0.3.3 §3.3 既有语义），严格下游照常级联 FAILED。**无需为 HITL 另写级联**。
- 新增 sentinel：`ErrApprovalNotRequired`、`ErrInvalidApprovalDecision`、`ErrApprovalAlreadyResolved`、`ErrInterruptRejected`（**`fabric.go` error 区**，与既有 `ErrTaskNotReady` 等同处；非 `task.go`）。

### 2.5 创建时发事件（可观测）

`createLocked`（`workflow_plan.go:191` 一带）在 `t.RequiresApproval == true` 时 `recordLocked(t, EventInterruptCreated)`（payload: `execution_id`/`step_id`/`message`）。缺省（false）不发 → 非 HITL 路径事件字节不变（红线）。

> ⚠️ **落点订正（阻断级）**：`EventInterruptCreated` / `EventInterruptResolved` 目前定义在 **`internal/runtime/events.go`**（package `runtime`），而本节与 §2.4 要在 **`internal/fabric/task`** 里引用它们——这会直接撞 `internal/fabric/task/architecture_test.go:29` 的 `TestFabricCoreMustNotImportRuntime`（源码扫描禁止 `task`/`agent`/`planprojection` 出现 `internal/runtime` 前缀，banned 字符串 `:45`）。
>
> **修法**：把两个常量**下沉到 `internal/ares_events/types.go`**（fabric 已依赖该包，且它们本来就是 `ares_events.EventType`），`internal/runtime/events.go` 保留同名单向别名（`const EventInterruptCreated = ares_events.EventInterruptCreated`）以不破坏 runtime 侧引用（现仅 `bus_test.go` 用到）。已核实：两常量当前**无任何生产 emitter/subscriber**，下沉不动行为；`ares_events` 的事件契约门禁只校验 **subscribed** 事件，新增未订阅常量不触发它。§9 改动清单已补这两行。

### 2.6 字段贯穿（修复 ProjectStep 有损桥）

```go
// internal/fabric/planprojection/projection.go — ProjectStep :58
// 删除对 Interrupt 的"丢弃"，改为映射：
if s.Interrupt != nil {
    ps.RequiresApproval = true
    ps.ApprovalMessage  = s.Interrupt.Message
    ps.ApprovalPayload  = s.Interrupt.Payload
}
// 同步更新 docstring :55-57：把 Interrupt 从"Explicitly discarded"移出，改列入映射字段。
```

```go
// internal/fabric/task/workflow_plan.go — CompilePlan 的 Task{} 建构 :160-170 追加：
RequiresApproval: s.RequiresApproval,
ApprovalMessage:  s.ApprovalMessage,
ApprovalPayload:  s.ApprovalPayload,
```

> **这会反转 `engine/architecture_test.go:41` 的 `InterruptConfig` 白名单项**——因为 `ProjectStep`（非测试）现在真的读了 `s.Interrupt`。必须**删除该白名单行**，否则 `TestExportedTypesMustHaveProductionReferences` 的 stale 分支（`:67-68`/`:75-76`）转红。这正是"能力宣称 = 真实能力"的正向触发。
>
> **同时订正"未来之家"口径（F8）**：两条 allowlist 目前把 HITL 的归宿记为插件——`engine/architecture_test.go:41` 的理由串是 `"...engine has no interrupt executor — NewInterruptPlugin is the future home (ares_runtime)"`，`runtime/architecture_test.go:54` 是 `"HITL interrupt plugin; no human-approval transport exists yet"`。本方案把**权威**放在 Fabric 层、插件只做观测（M4）——这是更对的（任务账本 + 事件流 = 单一真相源，见 §6）。删条目时理由串随之消失，但本计划在此明确记录：**该选择取代了 allowlist 的原意图**，插件不是被漏掉、而是被有意降级为观测面。

**YAML 作者面（F2，阻断级补充）**

字段贯穿只做了 `engine.Step → PlanStep → Task`，但**作者真正写的 YAML 面还没有入口**：`StepFile`（`internal/fabric/task/workflow/engine/loader.go`）当前**没有任何 `interrupt` 字段**（与 `allow_partial` 的 §C.8 遗留同款），所以"作者声明需审批"只在 **Go 侧**可达。

```go
// internal/fabric/task/workflow/engine/loader.go — StepFile 新增（YAML 作者面）
Interrupt *InterruptConfigFile `json:"interrupt,omitempty" yaml:"interrupt,omitempty"`

// InterruptConfigFile 是 InterruptConfig 的文件面镜像（字段同名：Message / Payload）。
// 在 loader 的 StepFile → engine.Step 装配处直拷转换，使 YAML：
//   steps:
//     - id: deploy
//       agent_type: tool/shell
//       interrupt: { message: "批准后才会执行部署" }
// 真正落到 engine.Step.Interrupt。
```

> **为什么必须补**：本方案 §5(ADR-H1) 与 §6 / G3 红线 3 自己的原则是"**闭环到 ares.yaml，别重蹈'代码里有、运维开不了'**"。不补 `StepFile.Interrupt` 就恰好重蹈了那个错——运维无法用 YAML 触发审批，功能对使用者不可达。**备选**（不推荐）：按 `allow_partial` 口径只做 Go 面、把 YAML 面记为后续遗留；但那与本方案自己的红线冲突。

### 2.7 持久化 + restore 折叠（崩溃可恢复）

**写入**（`fabric_events.go:151-196` must-persist 块，沿用"有信息量才写"）：
```go
if t.RequiresApproval {
    payload[restoreKeyRequiresApproval] = true
    if t.ApprovalMessage != "" { payload[restoreKeyApprovalMessage] = t.ApprovalMessage }
    if t.ApprovalDecision != "" { payload[restoreKeyApprovalDecision] = t.ApprovalDecision }
    if t.ApprovalFeedback != "" { payload[restoreKeyApprovalFeedback] = t.ApprovalFeedback }
    // ApprovalPayload 视需要序列化；首版可只持久化决定态，Payload 作为展示态可不恢复（标注）
}
```

**键常量**（`restore.go:36-100` 区）：`restoreKeyRequiresApproval = "requires_approval"`、`restoreKeyApprovalMessage = "approval_message"`、`restoreKeyApprovalDecision = "approval_decision"`、`restoreKeyApprovalFeedback = "approval_feedback"`。

**折叠**（新 helper，从两个分支调用）：
```go
// restore.go，紧挨 foldDegradation :389
func foldApproval(p map[string]any, t *Task) { /* 读上述键回填 Task 字段 */ }
// 在 foldRestoreEvent 的 task.created 分支（:255 一带）与 post-create 分支（:302 一带）各加一行 foldApproval(p, t)
```

> 红线（同 0.3.3 §2.5）：审批决定是**运行态**，必须进事件流而非只活内存；否则崩溃重启后 pending 退化为"可立即跑"（绕过审批）或 rejected 丢失。

### 2.8 HTTP 传输层

新建 `cmd/ares/agent_routes_interrupts.go`，两个 handler 仿 `agent_routes_tasks.go`（nil-kernel 503 守护、`json` 解码、`writeJSON`、经 `h.kernel.fabric` 操作）：

```
GET  /api/interrupts
  → 列出所有 RequiresApproval && ApprovalDecision=="" 的任务
  → resp: [{task_id, capability, message, payload, created_at}]
  → 新增 Fabric 查询 PendingApprovals() []ApprovalView（dag.go 一带，锁内快照）

POST /api/interrupts/{id}/resolve
  → body: {approved: bool, feedback?: string}
  → 取 principal.ID 作为 ApprovalBy
  → 调 h.kernel.fabric.ResolveApproval(id, decision, feedback, principal.ID)
  → resp: {task_id, decision, state}
  → {id} 段提取仿 routeGetTask 的前缀裁剪（agent_routes_task_read.go:185）
```

路由注册（`cmd/ares/agent.go` 的 `actionRoutes`，`:470` 之后）：
```go
{Method: "GET",  Path: "/api/interrupts", Auth: authRead,
    Desc: "pending HITL approval gates", Handler: (*actionHandler).routeListInterrupts},
{Method: "POST", Path: "/api/interrupts/{id}/resolve", Auth: authWrite,
    Desc: "resolve a HITL approval (approve/reject)", Handler: (*actionHandler).routeResolveInterrupt},
```

> **安全红线**：`resolve` 必须 `authWrite`（已验证 principal），**不得**新增匿名入口；`ApprovalBy` 记 principal 以留审计。`list` 用 `authRead`。这是一个会改变执行决策的对外写端点，鉴权缺失 = 任何人可放行高权限任务。

---

## 3. 里程碑（每个可独立发布）

| 里程碑 | 内容 | 落点 | 验收 |
|---|---|---|---|
| **M1**（~1.5 天）字段 + 闸门 + approve/reject 核心 | §2.2/§2.3/§2.4/§2.5/§2.6 | `task.go`、`fabric.go`（sentinel）、`dag.go`、`fabric_lifecycle.go`、`workflow_plan.go`、`projection.go`、`engine/loader.go`（YAML 作者面）、`engine/architecture_test.go`（删白名单行）、`ares_events/types.go`+`runtime/events.go`（事件常量下沉） | pending 不可调度 + `Acquire` 拒绝；approve 放行；reject 终态+级联；缺省逐字节兼容；**可纯 fabric API 驱动，无 HTTP** |
| **M2**（~0.5 天）持久化 + restore | §2.7 | `fabric_events.go`、`restore.go` | 注入事件流回放后 pending 仍被挡、approved 仍放行、rejected 仍 FAILED；默认路径红线测试加新键断言 |
| **M3**（~1 天）HTTP 传输层 | §2.8 | `cmd/ares/agent_routes_interrupts.go`（新）、`agent.go`（路由+Desc）、`fabric` 查询 `PendingApprovals` | e2e：GET 列待批、POST approve/reject 鉴权生效；（可选）`EventInterruptResolved` 挂进 `kernel_loop.go:447-451` 事件触发以降低 approve→调度延迟 |
| **M4**（可选 ~0.5 天）观测接线 | 接 `runtime.InterruptPlugin` 订阅 `EventInterruptCreated/Resolved` 入 collector；emit `EventInterruptResolved` | `cmd/ares` 插件注册点（`kernel_pluginbus.go`）、`runtime/architecture_test.go`（删 `NewInterruptPlugin` 白名单行） | 面板/蒸馏能看到审批生命周期；**反转第二道门禁** |

M1+M2+M3 = 功能完整可用（CLI/HTTP 驱动审批）。M4 只加可观测，可晚做。

---

## 4. 不变量（红线）

1. **缺省兼容**：`RequiresApproval=false` 的任务——闸门、事件、调度——与 0.3.2/0.3.3 **逐字节一致**（conditional write 只在 true 时落键，沿用 `if t.AllowPartial` 风格）。默认路径红线测试（`TestDefaultPathPayloadHasNoNewKeys` 一类）须加全部新键到 `newKeys`。
2. **不新增状态机迁移**：只用既有 `READY→FAILED`（reject）与 READY 停留（pending/approve）；**不碰 `canTransition`**。
3. **两个闸门同改**：§2.3 的调度两处 + 授权一处缺一不可。
4. **approve/reject 不走 ownership**：同 `ExpireDeadlines`，是带外终态/放行入口，不经 `ownerLocked`，不污染 fencing 契约。
5. **可恢复**：审批策略 + 决定态进事件 payload + restore 折叠；崩溃重启后 pending 不退化为可跑。
6. **传输层鉴权**：`resolve` 走 `authWrite`，记 `ApprovalBy`；无匿名放行入口。
7. **每次迁移恰一条事件、经 `transition()`**（reject 的 FAILED 迁移复用 `failTerminalLocked`，天然满足）；approve 不是迁移，用 `EventInterruptResolved` 记一条 payload 事件。
8. **分层不越界**：HITL 的两个事件常量必须落在 **`internal/ares_events`**；`internal/fabric/task` / `agent` / `planprojection` **不得** import `internal/runtime`（`TestFabricCoreMustNotImportRuntime` 硬门禁，见 §2.5）。

---

## 5. 需要拍板的两条 ADR

**ADR-H1：审批的"触发"由谁标？**
- **(a) 显式标（首版推荐）**：作者在 workflow YAML / `engine.Step.Interrupt` 显式设 `Message` → 该 step 需审批。零魔法、可测、最小面。**注**：YAML 侧需先补 `StepFile.Interrupt`（§2.6 末尾）——`engine.Step` 的 Go 面已存在，YAML 面当前**没有** `interrupt` 键。
- (b) 按 capability 危险度自动标：如 `file_tools` 写 / `code_runner` / 对外 HTTP 自动进审批。**属新策略引擎，留后续增量**（见 §6）。

> **默认采 (a)**。

**ADR-H2：谁有资格批？**
- **(a) 任意已鉴权 principal（首版推荐）**：`authWrite` 即可 approve/reject，记 `ApprovalBy` 审计。
- (b) approver 角色/权限等级（RBAC）：`InterruptConfig` 增 `RequiredRole`，`resolve` 校验 principal 角色。**属 RBAC 扩展，留后续增量**。

> **默认采 (a)**。

---

## 6. 刻意不做（防过度设计）

| 砍掉的 | 为什么 |
|---|---|
| `SUSPENDED` 中途审批 | 受 `state.go:48` 出边限制、语义错位（审批是执行前），且要新增迁移边违反 0.3.3 §2.2 |
| approver 角色 / RBAC（`RequiredRole`） | 首版任意 `authWrite` 够用；RBAC 是独立扩展（ADR-H2(b)） |
| 按 capability 危险度自动标 | 属策略搜索/新引擎，无当前需求方（ADR-H1(b)） |
| 审批超时自动批/驳 | 引入计时器 + 新失败路径；首版"无限等到人决定"，超时策略后置 |
| 复用 engine `MemoryInterruptStore` / `InterruptStore` 接口 | 任务账本（事件流 + restore）**本身就是存储**，再引入内存 store 是第二份真相、两条漂移路径（同 0.3.3 对 `degraded_inputs` 的"单一真相源"原则）。engine 侧那套保持现状/后续按需清理 |
| `engine.HITLFeedbackPlugin` 的 approval 决策逻辑 | 它是 `CapObserver`，只发事件；审批决策放 Fabric 层（权威），插件只订阅观测（M4） |

---

## 7. Definition of Done

- [ ] M1：pending 不可调度（`ReadyTasks`/`ResumableTasks` 不含）+ `Acquire` 返回 `ErrTaskAwaitingApproval`；approve 后可调度可 Acquire；reject 后 FAILED + 级联（严格下游级联、`AllowPartial` 下游不级联）；`ProjectStep` 映射 `Interrupt`；**删除 `engine/architecture_test.go:41` 白名单行**且该门禁绿；**事件常量已下沉 `ares_events`，`TestFabricCoreMustNotImportRuntime` 保持绿**；**`StepFile.Interrupt` 可用**（YAML `interrupt: {message: ...}` 能落到 `engine.Step`，配一条 loader 解析测试）
- [ ] M2：事件回放后 pending/approved/rejected 三态一致；默认路径红线测试含 4 个新键断言
- [ ] M3：`GET /api/interrupts` 列待批、`POST /api/interrupts/{id}/resolve` 走 `authWrite` 且 approve/reject 生效；崩溃重启后待批仍在；（可选）resolve 事件唤醒调度
- [ ] M4（可选）：`InterruptPlugin` 接线观测 + emit `EventInterruptResolved`；**删除 `runtime/architecture_test.go:54` 白名单行**且门禁绿
- [ ] 缺省（未声明审批）行为与现状逐字节一致，有回归断言
- [ ] ADR-H1 / ADR-H2 定稿（默认各 (a)）
- [ ] `docs/system-internals.{en,zh}.md` 补 HITL 审批门语义；`CHANGELOG.md` 记一条
- [ ] `make check` 全绿

---

## 8. 验收测试清单（具体用例）

**M1（fabric 层）**
- `TestApprovalGate_PendingNotSchedulable`：`RequiresApproval=true`、`ApprovalDecision=""` → 不在 `ReadyTasks`/`ResumableTasks`
- `TestApprovalGate_AcquireRefusesPending`：`Acquire` 返回 `ErrTaskAwaitingApproval`
- `TestApprovalGate_ApproveThenSchedulable`：`ResolveApproval(approved)` → 进可调度集合 + 可 `Acquire`
- `TestApprovalGate_RejectFailsAndCascades`：`ResolveApproval(rejected)` → 任务 FAILED；严格下游级联 FAILED；`AllowPartial` 下游保持 READY + `DegradedInputs` 记录
- `TestApprovalGate_Idempotent`：重复相同决定 no-op；相反决定 `ErrApprovalAlreadyResolved`
- `TestProjectStep_MapsInterrupt`：`engine.Step.Interrupt` → `PlanStep.RequiresApproval/Message/Payload`
- `TestProjectStep_NoInterruptStaysStrict`：未设 → 三字段零值
- `TestDefaultPath_NoApprovalKeys`：缺省任务事件 payload 不含 4 个新键

**M2（持久化）**
- `TestRestore_PendingStaysBlocked`：回放 `task.created(requires_approval=true)` → 恢复后仍被闸门挡
- `TestRestore_ApprovedStaysCleared` / `TestRestore_RejectedStaysFailed`

**M3（HTTP）**
- `TestRouteListInterrupts_ReturnsPending`
- `TestRouteResolveInterrupt_RequiresAuthWrite`（匿名/只读 principal → 401/403）
- `TestRouteResolveInterrupt_ApproveSchedules` / `_RejectFails`
- e2e：提交带审批 step 的 graph → 任务卡住 → GET 看到 → POST approve → 任务完成

---

## 9. 改动文件清单（总览）

| 文件 | 改动 | 里程碑 |
|---|---|---|
| `internal/fabric/task/task.go` | `Task` 加 7 字段（**4 个 sentinel 不在这个文件**，见下一行） | M1 |
| `internal/fabric/task/fabric.go` | 4 个新 sentinel（与既有 `ErrTaskNotReady` `:26` 同处） | M1 |
| `internal/fabric/task/dag.go` | `approvalCleared` + `ReadyTasks`/`ResumableTasks`(两分支) 加闸门 + `PendingApprovals()` 查询 | M1/M3 |
| `internal/fabric/task/fabric_lifecycle.go` | `Acquire` 加审批守护 + `ResolveApproval` 入口 | M1 |
| `internal/fabric/task/workflow_plan.go` | `PlanStep` 加 3 字段 + `CompilePlan`/`createLocked` 透传 + 创建发 `EventInterruptCreated` | M1 |
| `internal/fabric/planprojection/projection.go` | `ProjectStep` 映射 `Interrupt`（删丢弃）+ docstring | M1 |
| `internal/fabric/task/workflow/engine/architecture_test.go` | 删 `InterruptConfig` 白名单行(`:41`) | M1 |
| `internal/fabric/task/workflow/engine/loader.go` | `StepFile` 加 `Interrupt`（YAML 作者面）+ 装配转换（**F2**） | M1 |
| `internal/ares_events/types.go` | 新增 `EventInterruptCreated` / `EventInterruptResolved`（**下沉**：fabric 要引用它们，不能留在 `internal/runtime`，否则撞分层门禁）（**F1**） | M1 |
| `internal/runtime/events.go` | 两常量改为指向 `ares_events` 的别名（不破坏 runtime 侧引用） | M1 |
| `internal/fabric/task/fabric_events.go` | must-persist 块条件写 4 新键 | M2 |
| `internal/fabric/task/restore.go` | 4 个 `restoreKey*` + `foldApproval` + 两分支调用 | M2 |
| `cmd/ares/agent_routes_interrupts.go`（新） | 两个 handler | M3 |
| `cmd/ares/agent.go` | `actionRoutes` 加两条 + Desc | M3 |
| `internal/runtime/interrupt.go` + 插件注册 + `runtime/architecture_test.go` | 观测接线 + 删 `NewInterruptPlugin` 白名单行(`:54`) | M4（可选） |
| `docs/system-internals.{en,zh}.md` / `CHANGELOG.md` | 文档同步 | 收尾 |

---

## 10. 风险

1. **两个闸门漏改一处** → "既不执行也查不到" 或 "绕过审批被 Acquire"。调度两处 + 授权一处必须一起加测（0.3.3 §7 风险1 的原教训）。
2. **持久化遗漏** → 崩溃后 pending 退化为可跑（静默绕过审批，安全问题）。M2 的回放测试是硬门。
3. **reject 级联语义** → 必须复用 `failTerminalLocked`+`cascadeFailureLocked`，不要另写；否则与 `AllowPartial` 语义漂移。
4. **鉴权缺失** → `resolve` 对外写端点若非 `authWrite`，任何人可放行高权限任务。安全红线，e2e 必测拒绝路径。
5. **门禁反转遗漏** → 加了生产引用却没删对应 `architecture_test` 白名单行 → 门禁转红（这是**预期信号**，按 §9 同步删除即可）。
6. **分层门禁撞线（阻断）** → 若把 HITL 事件常量留在 `internal/runtime` 而从 `internal/fabric/task` 引用，`TestFabricCoreMustNotImportRuntime`（`internal/fabric/task/architecture_test.go:29`）直接红。修法见 §2.5「落点订正」（常量下沉到 `ares_events`）。
7. **YAML 作者面缺失（阻断）** → `StepFile` 无 `interrupt` 字段时，"作者声明"只在 Go 面可达、运维开不了——违反 §5(ADR-H1) 与本方案 G3 红线 3 的闭环原则。修法见 §2.6 末尾（补 `StepFile.Interrupt`，或明确记为后续遗留）。

---

## 11. 待拍板（评审输入，2026-10-11）

以下三条是方案评审提出的**设计取舍**，不属于阻断修复，但需在 M1 开工前定。**当前正文未按任一选项改写**，拍板后再落。

**D1（payload 持久化）· `ApprovalPayload` 要不要持久化？**

§2.7 现写"payload 作为展示态可不恢复"，但 §2.8 的 `GET /api/interrupts` 要把 `payload` 返给审批人 → **重启后待批任务的上下文丢失**，人只能在信息不全时决定；而 `ApprovalMessage` 是持久化的，两者语义不一致。
- **(a) payload 一并持久化（倾向）**：小 JSON，与"任务账本 = 单一真相源"一致；
- (b) 不持久化，但在 DoD 明写"重启后 list 的 payload 降级/为空"并接受。

**D2（resolve 的前提）· `resolve` 只在"仍未决"时有效？**

approve 后任务会被调度、可能已 COMPLETED；此时再 reject 会走到 `failTerminalLocked → transition(FAILED)`，而 `canTransition(COMPLETED, FAILED)=false` → 返回错误（**行为是正确的**）。但应显式规定并写进 §2.4 与 DoD：**`resolve` 仅在任务仍未决（`State==READY` 且 `ApprovalDecision==""`）时有效**；已终态 → `ErrApprovalAlreadyResolved`（或另立 `ErrApprovalNotPending`）。
- 影响：DoD 的 `TestApprovalGate_Idempotent` 需覆盖"已终态再 resolve"的断言口径。

**D3（守护位置）· `Acquire` 里审批守护插在哪？**

§2.3 现写"在 deadline 守护与 backoff 守护**之间或之后**"（未定）。任务同时"待批 + 已过 deadline"时报哪个错？
- **(a) 放最后（倾向）**：过期仍优先报 `ErrTaskDeadlineExceeded`（更可操作），且不改动既有两条守护的相对优先级；
- (b) 放最前：审批优先，报 `ErrTaskAwaitingApproval`。
- 影响：决定 §8 用例 `TestApprovalGate_AcquireRefusesPending` 与 deadlin/backoff 用例的相对断言。

---

## 附录 F：rev2 落地增量（2026-10-11，已实现）

> 在 rev1 的审批门之上落了三块增量 + 一次去重，均已带测试、`make check` 绿。按 `code_rules_v2.md` 实现。

### F.1 已实现

1. **审批 SLA（R1）**：`PlanStep.ApprovalTimeout`(相对) → `CompilePlan` 解析为绝对 `Task.ApprovalTimeout`；`Fabric.ExpireApprovals` 在恢复 sweep 中（deadline 之后、lease requeue 之前，`runSweepChain`）自动 reject 超时的待批门，走与手动 reject 同一条 `failTerminalLocked`+级联;SLA 进事件 payload + restore 折叠(`restoreKeyApprovalTimeout`)。只处理 `StateReady`（审批门挡 Acquire,待批任务不可能到 LEASED/RUNNING/SUSPENDED;限定 READY 规避 `SUSPENDED→FAILED` 非法迁移的僵尸风险)。
   - **兜底（rev3 补）**：SLA 原先只在 `CompilePlan` 路径设,`fabric.Create` 直建的 `RequiresApproval` 任务 timeout=0 → 永不过期 → 无限滞留。现加 `Fabric.WithDefaultApprovalSLA(d)`,在**唯一创建瓶颈** `createLocked`(Create 与 CompilePlan 都经此)对无显式 timeout 的待批门套用默认 SLA;生产经 `kernel.approval_sla`(`cmd/ares/peer_assembly.go`)注入。语义:默认 0 = 关(全局"等到人决定");设正值 → "忘了设"的门也会过期。per-task timeout 永远优先。这把"故意无限等"与"忘了设"从**不可分**变成**operator 可选的安全阀**。
2. **拒绝影响面预览(R3)+ 原子化(消 TOCTOU)**：`BlastRadius{cascaded_fail, allow_partial_degraded}`;`ResolveApprovalWithBlastRadius` 在同一把锁内先算 blast 再 resolve,HTTP handler 用它,返回数即实际级联数。
3. **匿名拒绝(R6)**:`POST /api/interrupts/{id}/resolve` 对 nil principal 返回 401,`ApprovalBy` 记 `princ.Subject`。
4. **approve 唤醒(闭环)**:approve 后发 `EventTaskReady`,`scheduler.go` 的触发集含 `EventTaskReady` → 立即 drain,无 sweep 延迟。
5. **agent `StatePaused` 改名(R5)**:`StateSuspended`→`StatePaused`,wire 值仍 `"SUSPENDED"`;并补 `busy` 执行态桥(`SetBusy`/`IsBusy`,`IsIdle` 读之,`scheduler_execute` 跑 quantum 前置、defer 清除)。
6. **去重(§5.1)**:`ResolveApproval` 与 `ResolveApprovalWithBlastRadius` 抽出共享 `resolveApprovalPreflightLocked` + `resolveApprovalLocked` 单一真相核心,防漂移。

### F.2 测试(§7)

- `internal/fabric/task/approval_sla_test.go`:SLA 自动 reject + 级联(严格/AllowPartial) + 零 timeout 不过期 + 重启后仍可过期;`CompilePlan` 相对→绝对;blast 预览计数(含传递);`ResolveApprovalWithBlastRadius` blast==实际级联/approve 零 blast/幂等;approve 发 `task.ready`。
- `internal/fabric/agent/busy_test.go`:busy 桥排除调度 + `-race` 并发安全。

### F.3 M4 决定:**暂不接 `runtime.InterruptPlugin`,显式搁置**

计划 rev1 的 M4 原拟"接 `InterruptPlugin` 观测 + 反转 `runtime/architecture_test.go:54` 守护"。实现时核实后**撤回**:
- `InterruptPlugin` 是 **WorkflowHook 平面**的观测器(`AfterStep` 回调,runtime engine 执行路径),而本审批门的 `interrupt.created`/`interrupt.resolved` 走的是 **taskfabric 事件存储平面**。注册它**观测不到**审批事件 → 属"空转接线",反转守护等于让守护说谎(违反 §0.2 禁止假实现)。
- HITL 已有真实消费端:`GET /api/interrupts`(待批查询)+ 事件存储(既有 flight/event 面可读)。`InterruptPlugin` 冗余。
- 真正的审批可观测闭环 = 订阅事件存储的 `interrupt.*` 并上面板,属 0.3.3 计划显式搁置的"观测轮"(C1),需独立设计。
- **结论**:M4 搁置,`runtime/architecture_test.go:54` 的 `NewInterruptPlugin` 白名单保留(它仍是真未接线的 capability reserve)。要做观测闭环请走观测轮,别用假接线凑数。
