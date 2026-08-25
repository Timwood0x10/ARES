# ares 框架深度 Review — 最终摘要报告

> **日期**: 2026-08-25  
> **范围**: `github.com/Timwood0x10/ares`，目录 `/Users/scc/go/src/goagent`  
> **方法**: 逐模块深读代码 + 生产接线 grep 验证 + 调用链追踪  
> **前置文档**: `REVIEW_PROGRESS.md`（248 行，已读并用为基线）  
> **前置工具**: `make check`（golangci-lint v2, 0 错误）、`go test -short ./...`（143 包全绿）

---

## 一、执行摘要

**本次 review 基于 REVIEW_PROGRESS.md 的 14 个已知缺陷清单，进行二次核实 + 全模块补读。**

- **10 项已修复**（#1–#6, #8, #9, #10, #11）— 二次核实确认接线存在。
- **2 项部分修复**（#7, #12）— 1/5 清理器注册 / Chaos 未接。
- **2 项未修复**（#13, #14）— 嵌入队列子系统 / DAG 修剪器未接线。
- **新发现 4 项**（#15–#18），其中 #15 为中等严重性（planner 证据反馈回路断裂 + 内存证据无界增长）。

---

## 二、REVIEW_PROGRESS.md 已知缺陷 — 二次核实状态

### ✅ 已修复（10 项）

| # | 缺陷 | 严重 | 验证 |
|---|------|------|------|
| 1 | `kernelscheduler` 缺 `waitFor` helper + 竞态 | 中 | 已补 helper + 轮询同步 |
| 2 | `taskfabric.record()` 持锁做 I/O | 中 | `recordLocked`/`flushAppends` 拆分 |
| 3 | `EvolutionScheduler` 订阅错配 | 中 | 改订阅 `EventAgentStopped` |
| 4 | `recordGenealogy` append 无界增长 | 低 | 套用 `maxLineages` cap |
| 5 | README 使用不存在的 `WithYAMLFile` | 中 | 全仓改 `WithConfig` |
| 6 | README benchmark 数字过期 | 低 | 重跑同步 |
| 8 | `RecordScore` 生产无调用方 | 低 | **内核实**：`provide_evolution.go:79` 注册，`scheduler.go:366-369` 订阅事件 |
| 9 | 服务端知识图缺演化上下文 | 中 | **内核实**：`bootstrap_steps.go:164` 注册进 `KnowledgeRuntime` |
| 10 | 服务发现引擎零消费 | 低 | **内核实（commit a4e4c147）**：`provide_discovery.go:66-70` 转发至 EventStore |
| 11 | SKILLS 渐进披露 + 工具能力搜索 | 中 | **内核实**：`bootstrap.go:265` 接技能，`tools.go:107` 注册搜索工具 |

### 🟡 部分修复（2 项）

| # | 缺陷 | 严重 | 状态 | 验证细节 |
|---|------|------|------|---------|
| 7 | 存储过期/衰减清理未接线 | 中 | **部分** | ✅ `maintenance_worker.go:44-68`（每小时）在 `bootstrap.go:325` 启动；`bootstrap_steps.go:53` 注册了 `experiences_1024` 清理器。**仅此一个**：`ConversationRepository`、`KnowledgeRepository`、`SecretRepository`、`SessionRepository` 的 `CleanupExpired`（5 处实现中仅 1 处注册）未接线。 |
| 12 | 演化内核适配器 serve 装配 | 中 | **部分** | ✅ `serve_agents.go:97/114/131` 分别接入了 quota/spawn-gate/population 三阶段（注释标注 REVIEW #12 stage-1/2/3 closure）。**仅 `Chaos`（+ 对应的 `Sandbox`）未接线** — 属危险项（`Chaos.InjectFailure` 真实 Kill/Suspend 活跃 agent），规划走 Shadow Sandbox + Live Chaos 可选。 |

### ⚠️ 未修复（2 项）

| # | 缺陷 | 严重 | 状态 | 验证细节 |
|---|------|------|------|---------|
| 13 | 异步 embedding 队列子系统全链路无消费者 | 中 | **未修** | `EmbeddingQueue.FetchPendingTasks`（`embedding_queue.go:151`）、`MarkCompleted`/`MarkFailed`（`embedding_queue.go:249`）、`Reconcile`（`embedding_queue.go:354`）**零生产调用**（仅测试 `storage_test.go` 使用）。唯一生产者 `ProductionMemoryManager`（`production_manager.go:28`）+ `WriteBuffer`（`write_buffer.go:19`）**仅测试构造**（`memory_test.go:97`、`failover_test.go:74`）。serve 走 `ProvideMemory` → `memoryManager`（`manager_impl.go`），同步 `expRepo.Create` 写入。整套 async 子系统（idempotency/lost-update/DLQ/Reconcile 正确性设计）从未被触达。 |

---

## 三、新发现缺陷（本轮 Review）

### 🔴 #15: Planner 证据反馈回路断裂 + 内存证据无界增长

**严重性**: 中  
**位置**:
- `internal/tools/planner/bridge.go:84-89`（直接路径保存证据时缺 CapabilityName）
- `internal/tools/planner/planner.go:118`（查询按 CapabilityName 过滤）
- `internal/tools/planner/evidence.go:166`（证据聚合键为 `ToolName:CapabilityName`）
- `internal/tools/planner/executor.go:137-145`（`memoryEvidenceStore.Save` 追加无界）
- `cmd/ares/tools.go:128`（serve 生产使用 `NewMemoryEvidenceStore`）
- `cmd/ares/serve.go:243-244`（bridge 接线到 toolBinder）

**问题描述（两部分）**:

**(a) 证据反馈回路断裂**:  
`ToolExecutionBridge.Execute` 的直接路径（`bridge.go:84-89`，当 LLM 直接命名一个已知工具时）保存证据时**未设置 `CapabilityName`**：

```go
// bridge.go:84-89
saveErr := b.evidence.Save(ctx, &ToolEvidence{
    ToolName:  toolName,
    Success:   err == nil && result.Success,
    // CapabilityName 未设置 → 空字符串
    Latency:   latency,
    Timestamp: time.Now(),
})
```

但 `Planner.Plan` 的证据查询（`planner.go:118`）按能力名过滤：

```go
evidence, qErr := p.evidence.Query(ctx, "", req.Name, 50)
```

且 `evidenceScorer` 的聚合键为 `ToolName + ":" + CapabilityName`（`evidence.go:166`）。直接路径证据的键为 `"toolName:"`（CapabilityName 为空），永远不会匹配能力查询 → **直接路径证据从未反馈到评分器**。

"基于证据的适应性工具选择"特性在**最常见路径**（LLM 直接指定已知工具）下无效；仅 multi-step/fallback 路径（`executeStep`/`executeStepWithFallback`，`bridge.go:324-330`/`381-387` 正确设置了 CapabilityName）能产生有效证据。

**(b) 内存证据无界增长**:  
`memoryEvidenceStore` 是生产使用的证据存储（`tools.go:128`）。`Save`（`executor.go:137-145`）仅追加到 `s.evidence` 切片，无容量限制、无 TTL、无淘汰策略。长期运行 serve 下，每次 bridge 间接调用（unknown tool 回退）都追加一条记录，永不释放。

**建议**:
- 修复 (a)：`bridge.go` 直接路径应填充 `CapabilityName`（可通过 `b.registry.Get(toolName)` 获取工具的原生能力，或从 `Execute` 签名传入）。
- 修复 (b)：为 `memoryEvidenceStore` 添加 cap（保留最近 N 条）或 TTL 过期。

---

### 🟡 #16: `stringutils.parseInt` 整数溢出（低严重性）

**位置**: `internal/tools/resources/builtin/stringutils/stringutils.go:177-183`

```go
func parseInt(s string) (int, error) {
    // ...
    n = n*10 + int(r-'0')
    // 无溢出保护
}
```

对超长数字字符串（如 `"99999999999999999999"`，超过 `int` 范围），`n*10` 静默环绕。这是工具参数的辅助函数，低严重性，但可能导致非预期的截断行为。

---

### 🟡 #17: `KnowledgeUpdate`/`KnowledgeCreate` 标签静默空字符串（低严重性）

**位置**: `internal/tools/resources/builtin/knowledge/knowledge_base.go:244-251`

```go
if tags, ok := params["tags"].([]interface{}); ok {
    tagStrings := make([]string, len(tags))
    for i, tag := range tags {
        if s, ok := tag.(string); ok {
            tagStrings[i] = s
        } // 非 string 类型 → tagStrings[i] 保持 ""
    }
    existing.Tags = tagStrings
}
```

当 `tags` 参数中包含非字符串元素时，该索引位置静默变为空字符串。更理想的处理是跳过非字符串元素（`continue` with `append` pattern）或返回错误。同样影响 `knowledge_base.go:347-354`（KnowledgeCreate 路径）。

---

### 🟡 #18: `KnowledgeUpdate` 获取后不检查 nil（防御性建议，非确认 bug）

**位置**: `internal/tools/resources/builtin/knowledge/knowledge_base.go:231`

```go
existing, err := t.service.GetKnowledge(ctx, tenantID, itemID)
if err != nil {
    return core.NewErrorResult(...), nil
}
existing.Content = content   // existing 若为 nil 则 panic
```

**核实结论**: 生产使用的 `StoreAdapter.GetKnowledge`（`store_adapter.go:65-77`）在条目不存在时返回 `errObjectNotFound`（非 nil error），因此**生产路径无 panic 风险**。但 `KnowledgeService` 接口契约未明确约定"缺失条目返回 error 而非 `(nil, nil)`"，第三方/未来实现若返回 `(nil, nil)` 将在 `existing.Content = content` 处 nil 指针 panic。建议加一行 `if existing == nil` 防御性检查（~3 行），或将该契约写入接口注释。


---

## 四、未接线/未连接模块清单

| 模块/子系统 | 状态 | 说明 |
|------------|------|------|
| **WriteBuffer + EmbeddingQueue + ProductionMemoryManager** | ❌ 未接线 | 整套异步嵌入子系统（写入缓冲 + 嵌入队列 + 死信 + Reconcile）仅测试构造。serve 使用同步 `memoryManager` + `expRepo.Create`。`#13`。 |
| **DAG Pruner（monitoring）** | ❌ 未接线 | `Pruner` 仅在 `WithPruneConfig` 时创建，serve/demo 均不传。`#14`。 |
| **Chaos / Sandbox 演化内核适配器** | ❌ 未接线 | 规划走 Shadow Sandbox 默认 + Live Chaos 可选。`#12` 剩余部分。 |
| **ConversationRepository.CleanupExpired** | ❌ 未接线 | 有实现（`conversation_repository.go:355`），未注册进 maintenance worker。`#7`。 |
| **KnowledgeRepository.CleanupExpired** | ❌ 未接线 | 有实现（`knowledge_repository.go:800`），未注册。`#7`。 |
| **SecretRepository.CleanupExpired** | ❌ 未接线 | 有实现（`secret_repository.go:274`），未注册。`#7`。 |
| **SessionRepository.CleanupExpired** | ❌ 未接线 | 有实现（`session.go:213`），未注册。`#7`。 |
| **Planner 证据反馈闭环（直接路径）** | ⚠️ 半断裂 | 回路已接线但直接路径证据不携带 CapabilityName → 评分器无法消费。`#15`。 |
| **Discovery 事件业务消费** | ⚠️ 半接线 | 事件已写入 EventStore（`#10` 修复），但无业务侧订阅者做"发现→MCP 自动注册"。 |
| **监控插件能力储备** | 🟢 有意未接线 | LoopPlugin、CheckpointPlugin、ArenaPlugin、ObserverPlugin、ToolPlugin、MemoryRouter、EvolutionRouter、FallbackRouter、NewEvolutionPlugin — 完整、测试过的能力储备，`serve_routine.go` 注释明确说明有意未注册。非缺陷。 |
| **memoryEvidenceStore 淘汰策略** | ⚠️ 无 | 生产使用的证据存储无容量限制/TTL。`#15b`。 |

---

## 五、配置层级风险

### `.golangci.yml` 混合 v1/v2 格式

**位置**: `/Users/scc/go/src/goagent/.golangci.yml`  
**状态**: ⚠️ 潜在风险（当前工作正常，但结构非标准）

当前文件是 v1/v2 混合格式：
- 使用 v2 的 `version: "2"` 和 `formatters` 键
- 但 `linters-settings`（v1 专用）仍然存在且被用于设置 `goconst` 排除

`golangci-lint v2` 静默忽略 `linters-settings`，但当前排除仍有效（因为默认值已足够）。若未来版本改变行为，或维护者误以为 `linters-settings` 生效，可能导致回归。建议迁移到纯 v2 格式。

---

## 六、干净模块确认（高置信度）

以下模块经深读确认无 bug、闭环正确：

| 模块 | 结论 |
|------|------|
| `ares_runtime`（manager/lifecycle） | ✅ 干净 — errgroup 收口，ctx 传参全覆盖 |
| `ares_skills/registry` | ✅ 干净 |
| `ares_flight`（flight/recorder） | ✅ 干净 |
| `system_runtime/orchestrator` | ✅ 干净 |
| `ares_protocol/ahp`（queue/heartbeat） | ✅ 干净 |
| `ares_bootstrap`（bootstrap/skills_wiring） | ✅ 干净 |
| `cmd/ares/tools.go` | ✅ 干净 |
| `tools/envcap`（envcap/search_tool） | ✅ 干净 |
| `tools/toolsource/capability_selector` | ✅ 干净 |
| `tools/planner`（bridge/planner） | ⚠️ 见 #15；其余干净 |
| `network/web_search` | ✅ 干净 — SSRF 防御正确 |
| `sdk` | ✅ 干净 — panic 是有意 fail-fast |
| `network/http_request` | ✅ 干净 — SSRF + 大小限制 + 超时 |
| `workflow/graph/patcher.go` | ✅ 干净 — 回滚补丁正确 |
| `math/calculator.go`（DateTime） | ✅ 干净 |
| `text/data_validation.go` | ✅ 干净 |
| `pdf/pdf.go` | ✅ 干净 |
| `text/json_tools.go` | ✅ 干净 |
| `ares_skills/outcome_recorder` | ✅ 干净 |
| `ares_skills/catalog`/`changes` | ✅ 干净 |
| `ares_memory`（memoryManager / context） | ✅ 干净 — 锁使用正确，MemoryPatchExecutor 写锁保护 |
| `ares_evolution`（main loop） | ✅ 闭环 — 5min ticker 驱动 GA 主回路 |
| `storage/postgres`（repositories） | ✅ 代码干净 — CleanupExpired 实现正确，仅接线不全 |
| `monitoring/dashboard` | ✅ 逻辑干净 — tabs 自封顶、publisher/collector ctx 受管 |
| `detector` | ✅ 干净 — 只读探测，ctx + per-call timeout，never-panic |
| `llmservice` | ✅ 干净 — 非死包，经 sdk 被消费 |
| `agentipc`/`agentloop`/`agentsyscall`/`kernelctx` | ✅ 干净 — goroutine ctx 受管，defer 清理 |


---

## 七、贯穿性结论

此代码库在**生命周期/循环/goroutine/事件消费**层**极其规整** — 几乎全部用 errgroup / WaitGroup / ctx.Done / ticker.Stop / panic-recover 正确收口，肉眼未见裸 goroutine 泄漏或未受管的无限循环。

**残留缺陷的共性模式**: 组件被实现 → 被测试 → 甚至被引用，但缺少一个逻辑上的**生产消费方/构造方**。具体表现为：

1. **register-but-never-consume**: 4 个 CleanupExpired 实现未被注册进 maintenance worker（#7）
2. **start-but-never-read**: DAG Pruner 从未被构造启动（#14）
3. **adapter-never-constructed**: ProductionMemoryManager 仅测试构造（#13）
4. **data-flow-incomplete**: 直接路径证据不携带 CapabilityName，导致证据回馈断裂（#15）

**新发现 #15 是唯一一个"纯数据流"型缺陷** — 不涉及 goroutine/生命周期，而是调用链上的数据字段缺失导致下游消费者无法匹配。这比装配层问题更难通过测试发现（所有单元测试都绿，因为测试直接构造证据时总是设置 CapabilityName）。

---

## 八、行动建议优先级

| 优先级 | 项 | 估计工作量 | 影响 |
|--------|----|-----------|------|
| **P0** | 修复 #15a：直接路径证据填充 CapabilityName | 小（~5 行） | 恢复证据自适应评分 |
| **P0** | 修复 #15b：memoryEvidenceStore 加 cap | 小（~10 行） | 消除长期运行时内存泄漏 |
| **P1** | 登记 #7 剩余 4 个 Cleaner（Conversation/Knowledge/Secret/Session） | 小（~15 行） | 全表 TTL/decay 清理 |
| **P1** | 修复 #17：KnowledgeUpdate/Create 标签静默空字符串 | 小（~5 行） | 避免静默数据损坏 |
| **P2** | 加 #18 防御性检查：KnowledgeUpdate 检查 `existing == nil` | 小（~3 行） | 接口契约健壮性（生产路径已安全） |
| **P2** | 修复 #16：parseInt 溢出保护 | 小（~5 行） | 边界情况健壮性 |
| **P3** | 接线 #13：异步嵌入队列（WriteBuffer + EmbeddingQueue + ProductionMemoryManager） | 大（跨多文件） | 异步嵌入/解耦写入 |
| **P3** | 接线 #14：DAG Pruner（`WithPruneConfig` 传给 `NewConsole`） | 小（~3 行） | 消除 dashboard DAG 内存泄漏 |
| **P3** | 迁移 `.golangci.yml` 到纯 v2 格式 | 中（清理所有 `linters-settings`） | CI 配置健壮性 |
| **P4** | 接线 Chaos/Sandbox | 大（需设计决策） | 演化混沌测试 |

---

*报告完毕。详细信息见 `REVIEW_PROGRESS.md`（14 项基线）和本文档。*

| 14 | 监控 DAG 节点图在 serve 下无界增长 | 中 | **未修** | `Pruner`（`pruner.go:46`）仅在 `WithPruneConfig` 选项（`plugin.go:126-130`）传入时于 `NewConsole`（`plugin.go:202-205`）创建。`serve_routine.go:78-84` 的 `NewConsole` 调用**未传 `WithPruneConfig`**。`dag.Engine.nodes` map 每 agent/task 一节点，`AddNode` 无 cap，仅 Pruner 会 `RemoveNode`。无修剪器 → 长期运行内存泄漏。 |