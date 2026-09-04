# ARES 全仓深度 Code Review —— 薄弱点 / 未闭环 / 潜在 Bug / 测试敷衍 / 规范违规

> 审查范围：`internal/`（49+ 包）与 `cmd/` 全部模块
> 审查时间：2026-09-03 · 基准规范：`plan/rules/code_rules_v2.md`
> 方法：全局静态扫描（AST 函数规模、模式 grep）+ 4 路并行 subagent（装配/内核/存储/可观测）+ 逐项人工核实高危点
> **声明**：以下每条标注「已核实」或「需确认」；凡经我打开源文件确认的标 ✅，仅凭模式扫描/子代理的标 ⚠️。**不编造行号不存在的伪 bug。**

***

## 0. 执行摘要

这是 36 万行 Go 单体。**代码质量整体高**：SQL 参数化、pgx 租户隔离、embedding 异步链路、一致性/恢复协议在多数核心路径实现严谨且有自诊断注释。但存在**三类系统性问题**：

1. **规范执行不彻底**：单文件/单函数超限成规模（6 个 1000+ 行文件、156 个 100+ 行函数）；测试用 `time.Sleep` 251 处；库层 `fmt.Println/log.Print` 65 处；注释掉代码 142 处；裸 goroutine 38 处（多数受管，个别无 recover）。
2. **"测试覆盖率高"有虚高**：4 个 `*_coverage_test.go` 巨型表驱动硬凑覆盖，忽略错误路径/反向用例，掩盖了真断言不足。
3. **若干真 bug / 未闭环**：散布在错误处理、并发边界、超长函数内。

***

## 1. 真 Bug 与未闭环（P0/P1）

### P1-1 候选无自身证据（进化 G2 判定非候选特异）— 已核实 / ✅ 已闭环（2026-09-03，Step 4）

- `internal/ares_evolution/replay_scorer.go` + `shadow_sampler.go:45-50`：G2 判定靠 replay 读策略自身历史，候选从未被执行 → 语义是"机队 vs 线上历史"，不是"候选更好"。

- **已闭环**：`evolution.shadow_execution.enabled=true` 时候选与活跃双臂在隔离 runner 实跑配对（见 `AGENT_OS_CLOSURE_DEV_PLAN.md` N-1）。默认关闭时仍是 replay 语义。

### P1-1b（2026-09-04 新增）协作与工具通道：进化看得见但改不动

- **已闭环的部分**：协作回执（`agentipc/primitives.go` 的 `Request` + `Send` 双出口）与工具成败（`sub/tool_observer.go` binder 装饰器）现已写入 `collaboration` / `tool_call` 独立 source 的 fitness 证据，按活跃策略归因，`RuntimeFitnessAggregator` 按策略 scope 加权读取。默认关闭。

- **未闭环的部分（`AGENT_OS_CLOSURE_DEV_PLAN.md`** **N-11 修订 / N-12）**：工具维度有**旋钮但没接线**——`Params["tools"]`（`string` 逗号分隔）已存在且 `Mutator.mutateTool` 会变异它，但两条执行体的 `GetToolSchemas()`（`sub/executor.go:860`、`agentfabric/chat_cognition.go:304`）无条件全量投给 LLM、不读取过滤，且 `ComputeEvidenceKey` 只含数值参数、工具字段不进归因 key；协作维度 agent 没有"问某个 agent"的 syscall。所以判决分会因这两个通道的真实成败而分化，但**下一代在这两个维度上的行为不会因此改变**——开环反馈。

- **性质**：从"最大未闭环"降级为"度量已闭环、作动未闭环"。最小突破口是给两条执行体**接线** **`Params["tools"]`** **过滤**并把工具字段**并入** **`ComputeEvidenceKey`**（详见计划 Y.3-ACT），非新增字段。

### P1-2 error 被吞且无注释 — 已核实 ✅

- `internal/agentloop/engine.go:325,332`：`_ = sm.AddStructuredMessage(...)` / `_ = e.Memory.AddMessage(...)` 吞写 memory 错误，无 `// best-effort` 注释。规范(§3.1)要求每个 error 显式处理或注明忽略原因。

- `internal/agentipc/primitives.go:118,129`：`_ = b.deliverReply(...)` 吞投递错误——reply 投递失败会静默，调用方可能永久等待。

### P1-3 裸 goroutine 无 recover — 已核实 ✅ / `agentipc` 部分已修 ✅（2026-09-04）

- `internal/agentipc/primitives.go:112`：`go func(){ reply, err := h(...); ... }()` 由 deadline select 管理，但 **handler 无 recover 边界**——若 handler panic（如第三方插件/注册 handler），整进程崩溃而非仅该请求失败。规范(§4.2)要求 goroutine 内 recover。

  **已修（2026-09-04）**：抽为 `Bus.invokeHandler` 并加 recover 边界。三处设计取舍：①panic 转 `ErrHandlerPanic` 走与普通 handler error **相同的 sentinel-nil-reply 唤醒协议**——只 log 不唤醒会让调用方白等满 timeout（panic 的 handler 永远不会 Reply），有专门测试断言不烧 timeout；②panic 值**不进 error**（可能含内部路径/请求数据，§3.5），只进注入的 logger（`Bus.WithLogger`，`cmd/ares/evolution_ipc.go` 与 `introspect/dashboard.go` 两个生产构造点已接）；③`Send` 的同步 panic **刻意不 recover**——它跑在调用方自己的 goroutine 上，调用方可自行 recover，吞掉才是隐藏错误。测试：`agentipc/collaboration_observer_test.go` 断言进程存活、调用方拿到 `ErrHandlerPanic`、其他 agent 事后仍可用、不烧 timeout、日志含 from/to/topic 上下文键。

- ⚠️ 待确认：`internal/ares_runtime/manager_chaos.go:286`、`internal/planprojection/coordinator.go:158` 等裸 goroutine，需逐一点检 recover/生命周期（仅子代理面点）。`internal/ares_memory/context/session.go:93` 已于 §6.5 确认受管。

### P1-4 超长生产函数隐藏浅析风险 — 已核实 ✅（非漏洞但属高风险）

- `internal/workflow/engine/mutable_dag.go:659` `ReplaceNode` **154 行**（自环/重复/不存在/循环检测全在一个函数）— 功能正确但状态机巨复杂，维护高风险。

- `internal/storage/postgres/write_buffer.go:285` `flushBatch` **127 行**。

- `internal/llm/output/validator.go:79` `validateValue` **115 行**（递归 schema 校验，超长）。

### P1-5 异步嵌入链路缓解但死信无人强制回收 — 需确认 ⚠️

- `internal/storage/postgres/embedding_queue.go`：`FOR UPDATE SKIP LOCKED` + `ON CONFLICT` revive 均严谨。但 `embedding_dead_letter` 的**存量清理/重试策略**未见犯罪循环 —— 死信累积不回收，nor 的写入可能卡 NULL vector（`Query` 过滤 `embedding IS NOT NULL`，死信永不入车间）。需确认是否存在死信重投（子代理面点，未定位到犯罪循环）。

***

## 2. 测试敷衍（P3）

### T-1 `*_coverage_test.go` 巨型硬凑 — 已核实 ✅（4 个文件）

- `internal/workflow/engine/coverage_test.go`（608 行，`TestWorkflowTypesCoverage` 299 行表驱动）

- `internal/storage/postgres/coverage_test.go`（`TestVectorSearcher_Coverage` 155 行）

- `internal/ares_memory/manager_coverage_test.go`、`internal/ares_events/offline_coverage_test.go`

- **问题**：这类文件多为"调用所有方法断言不 panic/NoError"，少反向用例、少错误路径、少语义断言 → **虚增覆盖率**，掩盖真实断言不足（N-3 覆盖率 59.2% 但"有效"覆盖率存疑）。

### T-2 测试用 `time.Sleep` 同步 — 已核实 ✅（251 处）

规范(§7.3)明令禁止。重灾文件：

- `internal/ares_runtime/runtime_test.go` **45 处**

- `internal/ares_shutdown/shutdown_comprehensive_test.go` **19 处**

- `cmd/ares/scheduler_test.go` **13 处**、`internal/ares_integration/runtime_resurrection_test.go` **11 处**

- 其余 12+ 文件各 4-8 处

- **风险**：flaky 测试、CI 慢、掩盖真实时序竞争。

### T-3 空壳/无断言测试 — 需确认 ⚠️

- ⚠️ 存在大量"仅 require.NoError + 简单调用"的高覆盖文件，但具体到函数级哪些是空壳需逐文件确认。建议后续用"断言密度 gocritic/自检"排查。

***

## 3. 规范违规（P2）— 全部已量化核实

### V-1 单文件 >1000 行（规范 §1）— ✅ 6 个

| 文件                                                | 行数   | 函数数 |
| ------------------------------------------------- | ---- | --- |
| `internal/ares_evolution/lifecycle.go`            | 1287 | 31  |
| `internal/kernelscheduler/scheduler.go`           | 1185 | 31  |
| `internal/ares_evolution/dream_cycle.go`          | 1081 | 25  |
| `internal/ares_evolution/genome_wiring_system.go` | 1059 | 23  |
| `internal/taskfabric/fabric.go`                   | 1048 | 31  |
| `internal/agents/sub/executor.go`                 | 1044 | 34  |

### V-2 单函数 >100 行（规范 §1）— ✅ **156 个**

生产代码代表：`mutable_dag.go:659 ReplaceNode 154`、`write_buffer.go:285 flushBatch 127`、`validator.go:79 validateValue 115`、`knowledge_repository.go:43 Create 123 / :178 CreateBatch 130 / :446 SearchByVector 103`、`secret_repository.go:356 RotateKey 118 / :522 Import 138` 等。

> **注意**：大函数多为表驱动/多分支，功能正确，但违反"单函数<100"硬上限，需拆。

### V-3 库层 `fmt.Println/log.Print`（规范 §9.1，禁止库层打印）— ✅ **65 处**

- `internal/ares_protocol`、若干 load/init 路径直接打印而非注入 logger。

### V-4 注释掉的代码块（规范 §8.2，禁止提交注释代码）— ✅ **142 处**

- 抽查确认存在多处被注释的 `if/for/return` 块（集中在事件/工具/工作流模块）。需 grep 清理。

### V-5 panic 作为业务错误路径（规范 §3.4）

- `internal/ares_runtime/arena.go:127` `panic("arena: injected panic ...")` — 这是**故意的混沌注入**（合法，非业务错误）。✅ 非违规。

- `internal/tools/resources/builtin/pdf/testdata/gen_pdf.go:34` `panic(err)` — 测试生成器，可接受。✅

### V-6 字符串硬编码 goconst 候选 — ✅ **47 处抽样**

- `30 * time.Second` 等超时魔数多处重复，未提常量。

### V-7 错误被吞总量 — ✅ 各包合计（HTTP 编码 best-effort 居多，但需补注释）

- `internal/introspect/control.go` 43 处 `_ = json.NewEncoder(w).Encode(...)` — HTTP 响应编码 best-effort，**可接受但建议补注释**（已核实语义合理）。

- `internal/knowledge` 21 处 — ⚠️ 部分为 `rows.Close()` 之外的库写，需确认。

### V-8 模板规范：导出符号无注释 / import 乱序 — 需确认 ⚠️

- 全仓抽查未见大范围，但 156 个超大函数文件中个导出方法注释可能缺失，建议 `golint` 全量跑。

***

## 4. 各模块专项发现汇总

### 装配与运行时（bootstrap/runtime/system\_runtime/agentfabric/agentipc/agentsyscall）

- ✅ `ares_shutdown`（`manager.go:174-235`）：回调 goroutine 用 `WaitGroup`+`recover`+错误 channel 传回，**合规优秀**。

- ✅ `kernelctx.go:480` / `:823`：量子执行用 `sem`+`defer wg.Done()`+recover，受管。

- ✅ `runtime/bus.go:264`：订阅清理 goroutine 由 `ctx.Done()` 管理，持锁清理，无泄漏。

- ⚠️ `agentipc/primitives.go:112` 裸 goroutine 无 recover（见 P1-3）。

- ⚠️ `agentfabric`（11 文件 4842 行）未被 subagent 深查，需补。

### 内核与进化（evolution/kernelscheduler/taskfabric/evidence/aresrecovery）

- ✅ `deployment.go`（Step 1-3 已落地）：增量判据、双值 Evaluate、Snapshot/Restore 正确。

- ✅ `replay_scorer.go`：冷启动长期均值回退正确防平局。

- ⚠️ `evolution/scheduler.go:197` 调度入口需确认并发（subagent 面点未深挖）。

### 存储/知识/LLM/MCP（storage/knowledge/memory/skills/mcp/llm）

- ✅ `pool.go:176-217`：tenant `set_config` 事务/连接级 + 释放时 `clearTenantContext`，防 RLS 泄漏，**实现严谨**。

- ✅ `distillation_service.go:155-179`：异步嵌入失败 fallback 同步 embed，避免卡死。

- ✅ `llm/client.go:393 onward`：流转用 ctx 退出 + 阻塞 send，消费者 cancel 即释放，**管理正确**。

- ✅ `knowledge/store/postgres/store.go`：`ANY($1)`/`OFFSET $N` 参数化已修，SQL 安全。

- ⚠️ `embedding_dead_letter` 死信回收策略未确认（见 P1-5）。

- ⚠️ `knowledge_repository.go` 多个 100+ 行 SQL 函数（见 V-2）。

### 可观测/安全/竞技场/事件（events/arena/security/ratelimit/flight/observability/introspect/config）

- ✅ `ares_events/memory_store.go:128-158`：`Read` 用新切片 append，**不覆盖先前返回**（早前 bug 已修）。

- ✅ `ares_security/middleware.go:91-132`：JWT+role 校验，未授权返回 401/403，无鉴权空隙。

- ✅ `ares_arena/http.go:60-85`：API key 默认 deny。

- ⚠️ `introspect/control.go` 43 处吞 HTTP 编码错误（可接受需注释）。

***

## 5. 整改建议（按优先级）

| 优先级      | 动作                                                                               | 对应问题        |
| -------- | -------------------------------------------------------------------------------- | ----------- |
| ~~W1~~ ✅ | ~~`agentipc/primitives.go`~~ ~~的 handler goroutine 加 recover 边界~~ 已修（2026-09-04） | P1-3        |
| W1       | `embedding_dead_letter` 补死信重投/过期清理，防 NULL vector 永久卡死                            | P1-5        |
| W1       | `agentloop engine.go:325/332` 吞错补注释或改返回                                          | P1-2        |
| W1       | 两条执行体接线 `Params["tools"]` 过滤工具集 + 工具字段并入 `ComputeEvidenceKey`（让工具通道反馈闭环成作动）      | P1-1b       |
| W2       | 拆 6 个 1000+ 行文件 + 156 个 100+ 行函数（按职责拆）                                           | V-1/V-2     |
| W2       | 消灭 251 处 test `time.Sleep`（改用 channel/WaitGroup/轮询）                              | T-2         |
| W2       | 清理 142 处注释代码 + 65 处库层打印 + 47 处硬编码魔数                                              | V-3/V-4/V-6 |
| W3       | 重构 4 个 `*_coverage_test.go` 为真语义断言，补错误/反向用例                                      | T-1         |
| W3       | `agentfabric` 未覆盖模块补深查                                                           | 各模块         |

***

## 6.5 补扫（2026-09-03 二轮：补齐上轮未看模块）

> 上轮明确声明未看的模块，本轮逐包补查（AST 函数规模 + goroutine/panic/sleep/swallow 扫描 + 核心文件人工核实）。**结论：这些模块生产代码比预想干净——goroutine 全受管、无 panic、超长函数集中在测试文件。**

| 模块                                                                                                                                      | 补查结论                                                                                                                                                                                                   |
| --------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `agentfabric`(2141行)                                                                                                                    | ✅ 文件规模合理（最大 chat\_cognition.go 564行）；goroutine=0；e2e 测试巨型函数 `TestE2E_GrandLoop_CompleteAgentOS` 133行 / `TestP3_4_EndToEndSpawnSynthesis` 172行（P3 用着事）；swallow=1                                        |
| `ares_arena`                                                                                                                            | ✅ `regression.go:481` 并发用 sem+wg+runCtx 全管理，**合规优秀**；`http.go:450` survival 后台用 `WithTimeout(Background,Dur*2)` 有界+H 日志，可接受（但用 Background 忽略请求 ctx，注释已声明）。service.go 仅312行                             |
| `ares_mcp`(4055行)                                                                                                                       | ✅ stderr drain goroutine(`transport_stdio.go:122`) 由 `stderrWg` 管理+错误日志，受管；吞错==`_ = Close()`(config\_watcher/stdio) best-effort 需补注释。"微弱点":Scanner 无超时、依赖 pipe 关闭退出（Close 会关，可接受）。server.go 789行 <1000 |
| `ares_memory`                                                                                                                           | ✅ `context/session.go:93` cleanup goroutine 由 `stopCleanup` channel + `wg` 管理，合规                                                                                                                       |
| `discovery`(1766行)                                                                                                                      | ✅ `engine.go:210` 周期 goroutine 由 ctx.Done + ticker defer stop + 错误 log.Warn，**受管**                                                                                                                     |
| `ares_skills`(5014行)                                                                                                                    | ✅ 无裸 goroutine/panic；swallow=5 需逐定点                                                                                                                                                                    |
| `ares_ratelimit`(1312行)                                                                                                                 | ✅ 无生产超长函数；test `TestTokenBucketLimiter` 180行(P3)                                                                                                                                                       |
| `ares_flight`/`ares_observability`/`ares_callbacks`/`ares_config`/`ares_archive`/`ares_ctxutil`/`detector`/`evidence`/`ares_experience` | ✅ 扫描干净：goroutine=0/panic=0；超长函数仅出现在 `*_test.go`（如 `contract_test.go:61 TestG2ConfigContract` 113行、`identifiers_test.go:151 TestProtectIdentifiers`）                                                    |
| `ares_protocol`                                                                                                                         | ✅ 生产代码无 goroutine/超长；**测试 10 处 time.Sleep**（ahp/dlq/heartbeat，P3 高频）                                                                                                                                   |
| `agentsyscall`(1506行)                                                                                                                   | ✅ 生产代码 `plan.go:246` goroutine 需确认→ 见下                                                                                                                                                                 |

**补查新增的发现**（并入主报告）：

- **N-6（P3）**：`ares_protocol/ahp` 测试 10 处 `time.Sleep`（dlq\_test/heartbeat\_callback\_test）—— protocol 握手时序用 Sleep 同步，未用 channel。

- **N-7（P3）**：`agentfabric` e2e 测试巨型函数（133/172 行）为硬凑覆盖，断言密度低。

- **N-8（✅ 已确收）**：`agentsyscall/plan.go:246` 的 goroutine 已人工核实——由 `<-loop.Done()` 通道管理、调 `releasePlanLoop` 释放、`loop.Err()` 错误上报，**受管且不泄漏**。此黑点已关闭。

- **N-9（P2）**：`ares_mcp`/`agentfabric`/`discovery` 的 `_ = .Close()` / swallow 无 `// best-effort` 注释，规范(§3.1)不达标（资源释放，功能无害）。

**修正上轮覆盖盲区声明**：`agentfabric`/`ares_arena`/`ares_mcp` 等**已补查**，`agentsyscall/plan.go:246` 已确收受管；现仅剩 `taskfabric`/`workflow engine`/`ares_eval` 的**逐函数深度**未机器式扫尽（AST 规模扫描已覆盖，但逻辑逐行仍余）。

## 6. 方法与局限

- **已核实**＝我打开源文件确认；**需确认**＝模式扫描/子代理面点，需二次人工确认。

- 已消除首轮"agentfabric/araena/ara\_mcp 未看"盲区（见 §6.5）；**剩余盲区**：`agentsyscall/plan.go:246` 单点 goroutine、`taskfabric`/`workflow engine` 的深度逐函数、`ares_eval`/`eval` 内部。

- 本 review 未运行 `golangci-lint` 全量（V-8 后续）。建议合入前跑全量 `make check` 印证。

***

## 附录：验证记录

```
AST 扫描: 156 个函数 >100 行 / 6 个文件 >1000 行
grep: 251 time.Sleep(测试) / 65 库层打印 / 142 注释代码 / 38 裸goroutine / 4 coverage_test
已读核实: pacing_pool/agentipc/llm.client/memory_store/security/arena/store/embedding_queue
          /knowledge_repository/replay_scorer/deployment_wiring/gate_eval/patch(registry snapshot/restore)
已隔离确认: pgx tenant 泄漏→已防 / events 切片覆盖→已修 / LLM 流转→受管 / bus 订阅→受管
```

