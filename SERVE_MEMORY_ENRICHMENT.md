# Serve Planner Memory 注入 — 设计与核验

> 状态：**已完整落地，质量门全绿**。本文档记录该修复的 as-built 设计、共享/serve 侧分工、
> 完备性核验结论与门禁实据。遵循 `plan/rules/code_rules_v2.md`。

## 1. 缺口定性

深度 review 定位：**serve（L2 提交路径）提交的是裸 input**，prompt 未预注对话历史/知识，
而 SDK 路径在 `Agent.composePrompt`（`sdk/agent.go`）里已把 memory 折进 input 再提交。
结果是同一 planner，SDK 会话带跨轮记忆、serve 会话不带。

### 为何 HITL 不在本次范围

review 里另一可动项是 HITL（`InterruptPoint`）。核实结论：`InterruptPoint` 在 engine 内
**仅 `hitl.go` 自消费，无任何执行循环读取**——所谓"接线"实为"从零建中断感知执行"，属
**新功能**而非修复，故按 `code_rules_v2` 铁律 4（架构方向类改动需先获认可）不在本次处理。

## 2. 设计：钩子落共享核、实现落 serve 侧

关键约束：**同一语义只保留一条生产执行路径**（SDK 与 serve 共用 `agentruntime` 核），
且 **SDK 不能装第二个 enricher**（`composePrompt` 已注入，二次注入会重复折叠 memory）。

分层如下：

| 层 | 位置 | 职责 |
|----|------|------|
| 共享核 | `internal/agentruntime/submit.go` | `PromptEnricher` 类型 + `WithPromptEnricher` 功能选项；`Submit()` 在**解析 sessionID 之后、Admit 之前**应用钩子 |
| 共享核 | `internal/agentruntime/execution.go:51,133` | `ExecutionConfig.PromptEnricher` 字段 + 构造时透传给 `NewSubmitter` |
| serve 侧 | `cmd/ares/memory_enricher.go` | `memoryPromptEnricher`：`BuildContext` 折历史 + `AddMessage` 记本轮 |
| serve 侧 | `cmd/ares/agent_kernel.go:335` | `PromptEnricher: resolveServePromptEnricher(cfg, comp.Memory, logger)` |
| SDK | `sdk/agent.go` | **不装**第二 enricher（`composePrompt` 已注入）；grep 全 sdk/ 零命中确认 |

### 共享核契约（`submit.go`）

- `PromptEnricher = func(ctx, sessionID, prompt) string`：返回 `""` 表示不改 prompt——
  注入是**增量上下文**，永不 reject/清空用户提交。
- 钩子在 sessionID 解析后运行（enricher 按 session 打 key），在 Admit 前完成（enriched
  文本与 planner 所见一致）。
- 零值可用：不装钩子 = 原始 pass-through admission（默认语义）。

### serve enricher（`memory_enricher.go`）

- **会话映射**：memory 会话自铸 ID，故维护 L2-session → memory-session 映射；首轮
  `CreateSession` 建，后续轮复用。
- **并发**：映射表由 `e.mu` 保护，但 `CreateSession`（I/O）在锁外执行，避免跨会话串行化；
  同 L2 会话并发首轮由 `singleflight` 折叠为一次建会话，不同会话并行不互阻。
- **生命周期**：映射条目带 TTL（默认 `memorySessionTTL` 30min），惰性 sweep（读时）为
  主、内联 O(n) sweep 免后台 goroutine 生命周期管理。
- **降级**：任何失败（建会话/构建上下文）一律降级为裸 prompt——绝不阻塞提交。
- **开关**：`resolveServePromptEnricher` 三重门（cfg nil / mgr nil / `!cfg.Memory.IsEnabled()`
  → 返回 nil）。memory **默认启用**（`DefaultMemoryConfig.Enabled: true`，`IsEnabled` 的
  nil 视为 true），故默认就激活。

## 3. 完备性核验结论

端到端链条逐环核实，**全部存在且已接线**：

1. `agentruntime.PromptEnricher` 类型 — 存在（`submit.go`）
2. `WithPromptEnricher` 功能选项（nil 安全、零值可用）— 存在
3. `NewSubmitter(sessions, opts...)` 应用钩子 — 存在
4. `Submit()` 在 Admit 前应用 enriched prompt 并写回 `payload["input"]` — 存在
5. `ExecutionConfig.PromptEnricher` + `execution.go:133` 透传 — 存在
6. `agent_kernel.go:335` 用 `comp.Memory` 填充 — 存在
7. `memoryPromptEnricher.Enrich`（BuildContext + AddMessage）— 存在
8. 合同测试：`submit_enricher_test.go`（共享核）+ `memory_enricher_test.go`（serve）— 存在且过
9. SDK 不装第二 enricher — grep 全 sdk/ 零命中，确认

## 4. 本次改动

调查发现该链**已完整落地**（共享核钩子 + 合同测试 + serve enricher + 接线齐备），
无需新建功能。按 `code_rules_v2` 不做假实现，仅修复核验中发现的**行宽红线违规**：

- `cmd/ares/memory_enricher.go:202` — `resolveServePromptEnricher` 签名 133 字符 → 换行合规
- `internal/agentruntime/submit.go:151` — `Submit` 签名 130 字符 → 换行合规

均为**纯格式化换行**，不动签名语义与行为。

## 5. 门禁实据

| 门 | 结果 |
|----|------|
| `gofmt -l`（涉改文件） | 干净（无输出） |
| `go vet`（agentruntime + cmd/ares） | clean |
| `staticcheck`（同范围） | clean |
| `go test -race`（Enrich/Submit/Memory，双包） | `ok`（agentruntime 1.45s / cmd/ares 1.67s） |
| `go test`（双包） | `ok`（agentruntime 0.43s / cmd/ares 13.68s） |
| 行宽 >120 | 0（修复后复核） |
| 规模 | `memory_enricher.go` 212 行、`submit.go` 219 行，均 <1000 |

## 6. 结论

serve planner memory 注入**已按 `code_rules_v2` 完整实现、接线、测试并过全部质量门**。
本次仅补齐行宽合规。若需处理 review 中其他项（P0–P3，如 JIT 缩窄、COV/DFT 调试工具、
查表驱动的 MARK、MOA、eval 无评分器等），另起任务。
