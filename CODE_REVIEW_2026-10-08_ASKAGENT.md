# Code Review — C-1 修复批次（第三轮）

> **审查对象**：`bc2d3c5d` 之后的未提交改动（13 文件修改 + 4 新文件，+414/-193）
> **审查方式**：只读。全部 Critical/High 由我重新读真实代码确认。
> **验证**：`go build ./...` 通过 · `go vet` 干净 · `go test -race`（改动包）全绿

---

## 一、结论

**C-1 的死锁确实解了，但 E2"拿到 answer"的初衷没有达成** —— answer 在生产路径被丢弃，而文档声称的"回写机制"不存在。

同时本轮引入了一个**回归**：为修C-1 而写的裸 goroutine 违反了项目明令。

| 严重度 | 数量 | 概要 |
|---|---|---|
| **Critical** | 2 | answer 被丢弃 + 文档撒谎 · 裸 goroutine 回归（违反 code rules 4.1） |
| **High** | 2 | apply gate 无限重排队 · `tool_allowlist` 无 LLM 入口 |
| Medium | 3 | `shadowEvaluator` 只写 · LISTEN 无 recover · LISTEN 每订阅者独占连接 |
| ✅ 已正确修复 | 4 | C-1 死锁 ·白名单双点检查 · PG DDL 顺序 · 断言未弱化 |

---

## 二、Critical

### [C-1] E2 未达成：answer 被丢弃，文档在撒谎 【净功能倒退】

**位置**：`cmd/ares/serve_peer.go:438`

```go
reply, err := ipc.Request(reqCtx, from, to, topic, payload, askAgentTimeout)
...
log.Info("ask_agent: background reply received", "from", from, "to", to, "topic", topic)
}()      // ← reply 出了作用域就丢弃
return nil, agentsyscall.ErrAskAgentYielding
```

拿到 reply 后**只打一行日志**。`AskAgentResult.Answer` 在生产路径**永远是 nil**。

#### 文档声称的机制不存在

`syscall.go:115-116` 明确写：

> *"The reply arrives asynchronously via Bus.Reply; if an event store is wired, the reply triggers a drain so the asker's next quantum **sees the answer**."*

我核实了：

```bash
grep -rn "\.Reply(" --include="*.go" internal/ cmd/ | grep -v _test.go
# → 零结果
```

**全仓没有任何 `Bus.Reply` 调用者**，也没有任何代码把 reply 写回 asker 的 task payload / cognitive state / 任何 store。`deliverReply` 只投递到 `replyCh`，而该 channel 由那个丢弃 reply 的 goroutine 独占。

**这段注释描述的机制从未实现。**

#### 叠加 `omitempty` 让问题更隐蔽

`syscall.go:494` `Answer any \`json:"answer,omitempty"\``

yielding 时 `Answer = nil` → `omitempty` 把字段**整个删掉** → **LLM 收到 `{"accepted": true}`**。

#### 失败模式

LLM 调 `ask_agent` → 收到 `{"accepted":true}`，而工具描述（`syscall.go:696`）说的是 "Ask a specific target agent a question"，**没有任何"pending/异步"说明**。

agent 会认为协作已完成但对方没回答 → 要么重发（再拿一个 `accepted:true`，goroutine 继续堆积），要么**基于空答案继续推理**。

**这比同步阻塞更糟** —— 同步阻塞至少 LLM 会等待并重试。

#### 建议

二选一，**不能维持现状**（既没拿到答案，又让 LLM 误以为成功了）：

|方案 | 做法 |
|---|---|
| **C-1-a 真正回写** | 把 reply 落到 asker 的下一次 quantum 可见处（cognitive context 或 task payload） |
| **C-1-b 诚实降级** | 改回fire-and-forget，`Accepted` 注释改回"投递回执，非答案"，给 LLM 显式 `status: "pending"` + 工具描述说明需重查 |

### [C-2] 裸 goroutine 回归 —— 违反 code rules 4.1

**位置**：`cmd/ares/serve_peer.go:427`

```go
reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), askAgentTimeout)
go func() {
    defer cancel()
    reply, err := ipc.Request(reqCtx, from, to, topic, payload, askAgentTimeout)
    ...
}()
```

`plan/rules/code_rules.md:324` 明确写着：

> **禁止使用裸 go 关键字：所有 Goroutine 必须通过 `golang.org/x/sync/errgroup` 或自定义的 WorkerPool 进行管理。**

而且 `cmd/ares/` 目录下**此前零裸 `go` 先例** —— 这是第一个。

**被删掉的旧代码明确写着** `runBackground (not a bare goroutine, per code rules 4.1)`。旧代码的注释还指出关键一点：`safeInvokeHandler` 只 recover **handler** panic，所以 `runBackground` 才额外包了一层。

#### 三个实际问题

**① 关机时无人 join**

`context.WithoutCancel` 摘掉了 serve ctx 的取消信号，goroutine 只能靠自己的 30s 超时退出。serve 关闭时不等待它 → handler 仍在跑 `submitPeerTask`，向**已废弃的 fabric** 投递任务。

**② `observeCollaboration` 是真实的 panic 通道**

`invokeHandler` 的 recover 只护 handler goroutine。但 `Request` 的 defer（`primitives.go:161-170`）在**调用方 goroutine**（即这个裸 goroutine）上执行 `b.observeCollaboration(...)` → `obs.OnCollaboration(...)`。

`OnCollaboration` 是导出接口，实现若变化，一次 panic 直接杀进程。`runBackground` 会 recover 并重启。

**③ 30s 内可无上限堆积**

10 个并发 `ask_agent` × 30s = 10 个悬挂 goroutine，每个持有 1 个 pending reply entry + 1 个 L2 session。

#### 建议

改回 `runBackground(reqCtx, comp, "ask-agent-reply", fn)`，`fn` 返回 nil（`GoBackground` 遇 nil 会干净退出）。`runBackground` 在同文件 `:211`、`:247` 仍在用，**13 个调用点**。

---

## 三、High

### [H-1] apply gate 拒绝时无限重排队 —— 绕过重试预算

**位置**：`internal/runtime/evolution/coordinator/coordinator.go:483-496`

```go
if gate != nil {
    ok, gateReason := gate.Check(ctx, proposal.Patch)
    if !ok {
        decision = DecisionDelay
        proposal.RetryCount++          // ← 递增
        ec.proposals = append(ec.proposals, proposal)   // ← 但无预算检查
        continue
    }
}
```

`DecisionDelay` 分支的注释说 *"decide() 已经返回 DecisionDrop 当预算耗尽，所以到这里意味着还有重试次数"* —— **这个不变量被 gate 分支打破了**。

我核实 `decide()`（`:579-580`）：`proposal.Fitness >= ApplyFitnessThreshold` **直接 `return DecisionApply`，不检查 `RetryCount`**。

**失败模式**：高 fitness 提案 + 持续拒绝的 gate → 每轮重排一次，`RetryCount` 无限增长 → **永不 drop**。

**建议**：gate 拒绝时也走预算检查 —— `if proposal.RetryCount >= maxProposalRetries { decision = DecisionDrop } else { ... }`。

### [H-2] `tool_allowlist` 加了字段但 LLM 无法设置

**位置**：`internal/agentsyscall/syscall.go:571-585`

`SpawnAgentArgs` 有 `ToolAllowlist []string`（`:300`），`SpawnAgent` 正确传递（`:366`），`lifecycle.go` 正确消费。

**但 `BindTools` 的解析清单里没有它**：

```go
if v, ok := args[paramCapability].(string); ok { ... }
if v, ok := args["parent_id"].(string); ok { ... }
if v, ok := args["task_context"].(map[string]any); ok { ... }
if v, ok := args["resources"].(map[string]any); ok { ... }
// ← 没有 tool_allowlist
```

**失败模式**：LLM 传 `tool_allowlist` → **被静默丢弃** → 子 agent `toolAllowlist = nil` → `IsToolAllowed` 恒 true → **拿到全权限**。

这正是 E3 想解决的问题在 LLM 侧完全没被触达。

**建议**：接线（加解析分支 + `ToolSchemas()` 的参数表加 `tool_allowlist`），**或**明确声明"E3 当前无 LLM 入口，仅供 Kernel/SDK 内部调用"。

---

## 四、Medium

### [M-1] `shadowEvaluator` 变成只写字段

删除 shadow 分支后，全仓非测试引用只剩 2 处**赋值**（`dream_cycle.go:392`、`genome_wiring_system.go:314`），**0 处读取**。`go vet` 不报，但 `ShouldDeployLoose`/`minWinRate` 等 API 仍挂着，误导 reviewer。

**建议**：删除字段与 setter，或加明确的 deprecated 注释。

### [M-2] `listenEvents` goroutine 仍无 recover

`pg_store.go:571` `go s.listenEvents(ctx, notifyCh)`，全文件 0 个 `recover()`。

我核实实际风险**低于上一轮的判断**：`conn.Raw` 同步返回、断言失败返 error 而非 panic、`Release` 只放回 `sql.DB`。**降级为 Low**，但仍违反 code rules 4.1。

### [M-3] LISTEN goroutine 每订阅者独占一个连接

`pg_store.go:566` `conn, err := s.pool.Get(ctx)` + `defer Release`，而它只在 ctx 取消时返回。

全仓 **23 处 `Subscribe` 调用** → 长跑服务最多同时占 23 个 PG 连接**永不释放**。若 `MaxOpenConns` 被限死，LISTEN 拿不到连接会静默退化为 poll-only。

**建议**：给 LISTEN 独立小连接池，或预留余量。

---

## 五、已验证安全（这部分同样重要）

| 项 | 核实结论 |
|---|---|
| **C-1 死锁真的解了** | `ErrAskAgentYielding` 时返回 `nil` error（`syscall.go:526-529`），量子不阻塞，`drain` 的 `wg.Wait()` 能推进。**方向正确** |
| **白名单双点检查生效** | grow时（`planner_cognition.go:664`）+ 执行时（`l2graph.go:461-475`）。`agentFabric` 两处均已赋值非 nil |
| **PG DDL 顺序已修** | `DROP TRIGGER`(:91) 先于 `DROP FUNCTION`(:92)，用 `IF EXISTS` 幂等 |
| **`ToolAllowlist` 空值语义** | `len(spec.ToolAllowlist) > 0` 才建 map，空/缺省 → nil → 全权限，**向后兼容正确** |
| **既有断言未弱化** | `syscall_test.go` / `syscall_from_provenance_test.go` 的 diff 纯粹是签名适配（`error` → `(any, error)`），断言一字未动 |
| `conn.Raw` 类型断言 | `stdlib.Conn.Conn() *pgx.Conn` 在 pgx v5.10/v5.11 均存在，`go build` 通过 |

---

## 六、测试质量

| 文件 | 覆盖 | 判断 |
|---|---|---|
| `ask_agent_answer_test.go` | 3 个测试全部 `WithAskAgent(func... return "the-answer", nil)` | ⚠️ **仍未覆盖 `ErrAskAgentYielding`** —— 本轮修复的核心生产行为一次都没被测到 |
| `tool_allowlist_test.go` | 只测 `IsToolAllowed` 与 `Spawn` 落地 | ⚠️ 未测 `l2graph` 执行时拦截 |
| `coordinator_apply_gate_test.go` | 3 用例合理 | ✅ 但未覆盖 H-1 的重试耗尽 |
| `benchmark_quantum_latency_test.go` | 真基准 | ✅ |

**"测试全绿但生产路径 answer 恒为 nil"这个 Critical-2 不会被任何测试发现。**

**最小补测建议**：
1. `WithAskAgent` 返回 `ErrAskAgentYielding` → 断言 `Accepted==true && Answer==nil`
2. `toolCognition.ExecuteStep` 带受限 agent → 断言工具被拒

---

## 七、处理建议

```
必须（本批合入前）
  C-1  answer 丢弃 + 文档撒谎 —— 这是净功能倒退
  C-2  改回 runBackground（一行）
  H-1  gate 拒绝补retry 检查

强烈建议
  H-2  tool_allowlist 接线，或明确声明无 LLM 入口

顺手
  M-1 shadowEvaluator 清理
  M-2 listenEvents 加 recover
  M-3 LISTEN 独立连接池
```

**关于 C-1 的一句话**：死锁解了，但**用"拿到答案"这个核心目标换来的只是一个日志行**。要么把答案真正回写，要么诚实降级为 fire-and-forget —— 当前状态（LLM 以为成功、实际拿不到答案）是最坏的。

---

## 八、本次审查未覆盖

- `coordinator.go` apply gate 的完整 gate 实现（只核实了拒绝路径）
- `pg_store.go` LISTEN 的实际 PG 行为（无 PG 环境，只能静态分析）
- `benchmark_quantum_latency_test.go` 的基准设计合理性
- 未跑全量 `go test ./...`