# ARES 闭环开发计划 (Development Plan)

> 基于 `code-audit-report.md` 的审计结果，制定修复计划，让项目彻底闭环。
> 目标：消除伪接线空转、补齐空转实现、修复潜在 bug。

---

## 总体原则

1. **要么接线，要么删掉**：每个模块要么真正接入内核，要么标记 deprecated 并删除，不允许"有配置但空转"。
2. **修复顺序**：先修影响数据正确性的 bug → 再接线闭环 → 最后清理死代码。
3. **每项修复都必须有测试**：闭环后新增 e2e/集成测试验证模块真实生效。
4. **以 config 驱动的闭环为主**：新功能通过现有 config 开启，不引入额外开关。

---

## Phase 1: 修复高优先级 Bug (数据正确性 / 崩溃风险)

### P1-1 修复 ares_shutdown 并发与状态机 bug
- **问题**: `Manager.StartShutdown` 中 `PhasePreShutdown == 0` 与零值启动哨兵混淆，并发第二次调用可双重执行; `SignalHandler.SetContext` 写 `h.ctx` 未加锁，与 `handleSignals` 读存在数据竞争。
- **修复**:
  - 引入显式 `notStarted` 哨兵常量（如 `Phase = -1`）或单独的 `started bool` 标志，在 StartShutdown 开头原子判断。
  - `SetContext` 加锁写 `h.ctx`，读也加锁。
- **验证**: 新增并发 `StartShutdown` 测试（用 `-race`）。

### P1-2 修复 agentloop 空指针风险
- **问题**: `Engine.Run` 对 `req`、`e.LLM`、`e.Tools` 无 nil 校验，直接 panic。
- **决策**: `agentloop` 完全未接入内核，见 P3-1。若保留模块，须在 `Run` 开头加 nil 校验并返回可诊断错误; 若删除，此项随模块删除。

### P1-3 修复 ares_flight handleMemoryDistilled 类型断言
- **问题**: `evt.Payload["input_count"].(float64)` 恒失败，发射器写入 Go `int` 类型，计数恒为 0。
- **修复**: 统一 payload 写 int 或用 `fmt.Sprint` + `strconv.Atoi` 容错解析。
- **验证**: 新增 `handleMemoryDistilled` 单测断言 count 正确。

### P1-4 修复 ares_arena runScenarioReport 分数计算
- **问题**: 注释声称用 per-scenario 分数，实际仍用 global `Stats()`。
- **修复**: 从本场景的 `results` 计算 `TotalActions/SuccessfulActions/FailedActions` 传给 `CalculateScoreV1`。
- **验证**: 新增测试：先跑一个场景再跑第二个，断言第二个的 score 与第一个无关。

### P1-5 修复 taskfabric Fabric.Create 指针别名
- **问题**: 直接把调用方 `*Task` 指针存入内部 map，绕过锁并发写。
- **修复**: `Create` 内部对入参做深拷贝再存入。
- **验证**: 新增并发 Create + 读测试（`-race`）。

### P1-6 修复 ares_skills FetchHTTPManifest 远程技能路径
- **问题**: 远程技能 `Path` 未设置，`Load()` 时从 CWD 下错误路径读取。
- **修复**: 为 SourceRegistered 条目设置实际的 manifest/下载路径；或在加载时按 URL/ID 定位。
- **验证**: 新增 FetchHTTPManifest → Load 端到端测试。

### P1-7 修复 llmservice NewService 字段丢失
- **问题**: `MaxTokens` 未复制; `BaseConfig.RequestTimeout/MaxRetries/RetryDelay` 被忽略。
- **决策**: `llmservice` 完全未接入内核，见 P3-1。若保留，补齐字段映射并对齐 `api/service/llm` 的转换逻辑; 若删除，随模块删除。

---

## Phase 2: 修复中低优先级 Bug

| 编号 | 位置 | 修复 |
|------|------|------|
| P2-1 | ares_security/maskString | 用 `utf8.RuneCountInString` + rune 索引切片，保证非 ASCII 不截断 |
| P2-2 | ares_security/sanitizeValue | 删除 json.Number 死分支或改用 `UseNumber` 解码器 |
| P2-3 | ares_arena/metrics.go | 删除 3 个废弃 Record* 方法及其测试，或改由 RecordActionResult 聚合 |
| P2-4 | tools/discovery/discover.go | 用 `exec.Command` + `bytes.Buffer` 限量读取，超限直接 kill |
| P2-5 | tools/planner/extractor.go | 删除 `var _ = math.Round`，直接移除未用的 math import |
| P2-6 | tools/resources/builtin | 修正 text_processor 的 domain 标签为 "text" 或移除错误标签 |
| P2-7 | introspect/dashboard.go spawnAgent | 检查 bus.Register 返回错误，失败时跳过该 agent 并告警 |
| P2-8 | ares_flight/graph.go AddNode | 父节点缺失时建立待挂链表，父节点加入后回补 Child 关系 |
| P2-9 | ares_flight/timeline.go handleAgentEnd | 为 TimelineEvent 设置 ParentID（来自 agent start 的 id） |
| P2-10 | agentloop parseArgs | 解析失败返回错误并由调用方反馈给 LLM |
| P2-11 | agentloop FriendlyErr | 用 errors.New 包裹，保留错误链 |
| P2-12 | kernelscheduler PreemptLowerPriority | 守卫改为同时检查 fabric 可用 agent 数，生产模式也能触发 |
| P2-13 | kernelscheduler executeUnbound / HasCapableExecutor | 清理冗余条件，明确静态 executor 在生产模式的角色（或删除） |
| P2-14 | llmservice Generate / GenerateEmbedding | 修正错误语义与哨兵错误统一 |
| P2-15 | llmservice buildPrompt | 用 strings.Builder，并对角色分隔符做转义/白名单 |
| P2-16 | ares_archive summarizeFileChange | 使用 output 参数或删除参数 |

---

## Phase 3: 消除伪接线 (闭环/删除)

### P3-1 完全未接入内核的模块（13 个）

对这些模块逐一决策：**接线 or 删除**。

| 模块 | 建议 | 理由 |
|------|------|------|
| **agentloop** | 接线 | 它是真正的 LLM 对话引擎，可作为 peer agent 的执行体（sub.Agent 的替换/补充）。将它接入 `createPeerAgents` 的工具调用路径。若架构上已被 `agents/sub` 取代，则删除 |
| **detector** | 部分接线 | SDK `buildOptsFromEnv` 已用 LLM 检测结果; 将 PostgreSQLURL/MCPEndpoints 也接入 SDK 装配（Postgres 存储与 MCP 服务器自动发现） |
| **knowledge/linker** | 接线 | 在 `BuildKnowledgeRuntime` 或 AKG 管线中构造并使用（相似度/时间线/决策/架构连接器） |
| **knowledge/provider/postgres** | 接线 | 当 `cfg.Storage.PGVector.Enabled` 时，注册 PG 知识提供者到 KnowledgeRuntime |
| **knowledge/retriever** | 接线 | 当 `cfg.Knowledge.RetrievalEnabled` 时，构建检索器并注入 memory retriever 路径 |
| **knowledge/service** | 删除或接线 | 若保留 `KnowledgeService` HTTP 端点，则注册到 serve; 否则删除整个包 |
| **knowledge/store/sqlite** | 接线 | 当 `cfg.Storage.Type == "sqlite"` 时构建 SQLite 知识存储 |
| **knowledge/workflow** | 接线 | 若知识工作流未落地，删除或接入 workflow engine |
| **llmservice** | 删除 | 已有 `internal/llm` + `api/service/llm` 两套等价实现，`llmservice` 是第三套且字段丢失。优先删除，避免三套 LLM 客户端并存 |
| **storage/memory** | 接线 | 当 `cfg.Storage.Type == "memory"` 或未配置 PG 时，注册内存向量存储（当前 VectorStore 仅 PG） |
| **storage/postgres/query** | 删除或接线 | 若保留查询去重缓存，接入 retrieval_service; 否则删除 |
| **tools/toolsource** | 接线 | 将 `CapabilitySelector`/`TagSelector` 作为 SDK 可选 `WithToolSelector` 默认值（当前默认 AllSelector 绕过了智能选择） |

### P3-2 已接线但子系统空转的模块

| 子系统 | 接线方案 |
|--------|---------|
| **ares_evolution 候选验证/发布管线** (CandidateVerifier, CandidatePipeline, Diagnoser, GAGenerator, ProfileStore, ProfileExecutor) | 在 `wireGAEvolution` 中构建 CandidateVerifier + CandidatePipeline，把 GA 变异产生的候选 → 验证 → 发布 接入现有 Coordinator 流程 |
| **ares_evolution coordinator 自愈** | 在 Coordinator 收到失败 patch 时自动触发 NotifySelfHealingAttempt/Outcome，闭环"失败→自愈尝试→结果记录" |
| **ares_arena FlightBridge** | 在 `cmd/ares/arena.go` 的 `arena serve` 中调用 `service.SetFlightBridge(flightRecorder)` + `handler.SetFlightRecorder(...)`，让 timeline/diagnostics 端点返回真实数据 |
| **ares_arena 场景并发** | 实现 ParallelActions/MaxConcurrent/DependsOn 的执行（并行执行 + 依赖排序），或从 schema 移除并停止解析 |
| **ares_callbacks** | 在 LLM 调用链中注册 handler（如 metrics 收集、日志、审计），把 Registry 接入 events/observability |
| **ares_eval** | 将 EvaluatorRegistry 接入 GA 进化打分（LLMScoring 路径），让 llm_judge 真正用于策略评分 |
| **ares_experience FeedbackService** | 在 wireGAEvolution 设置 `gaCfg.FeedbackService`，让 RecordSuccess/Failure 闭环 |
| **ares_skills 反馈/工具/置信度** | 1) 在 serve 中启动 SkillOutcomeRecorder(订阅 EventSubTaskResult)；2) 注册 CatalogTools 5 个技能工具到 internalReg；3) 将 ExperienceConfidenceSource 注入 taskfabric 调度器的 ConfidenceSource |
| **ares_observability OTEL/Prometheus/CostDashboard** | 在 serve 中可选构造并挂载：OTEL tracer 接到 llm 客户端，Prometheus 接到 :8080/metrics，CostDashboard 挂到 introspect 路由 |
| **ares_runtime 路由器/插件** | 决策：MemoryRouter/EvolutionRouter/FallbackRouter 是否被 sdk.Graph 或 workflow 使用；若不用则删除，若用则从 sdk 装配 |
| **ares_shutdown 子系统** | SignalHandler/CallbackRegistry/PhaseExecutor/CallbackChain 删除（生产用 Manager.AddCallback + serve 内联信号），或重构 serve 用 SignalHandler 替代内联 |
| **aresrecovery WithCognitionFactory / GlobalTracer.TraceTask/TraceAgent** | 1) 在 kernel 构建 Recovery 时调用 WithCognitionFactory(agentfabric 的执行体工厂)；2) 在 task/agent 生命周期 hook 中调用 TraceTask/TraceAgent |
| **kernelscheduler 抢占/静态 executor** | 见 P2-12/P2-13 |

### P3-3 空转实现补齐

| 位置 | 补齐方案 |
|------|---------|
| knowledge/service ServiceAdapter.Query/Distill | 让 Query 委托 KnowledgeRuntime / 知识存储，Distill 运行 AKF 管线（若包保留） |
| tools/planner executionPlanner.Plan | 用 ToolCandidate 实际 cost/latency 元数据替代硬编码占位值 |
| detector detectPostgreSQL/detectMCP | 实际探测端口/URL 可达性 |

---

## Phase 4: 清理未使用配置与死代码

| 项目 | 处理 |
|------|------|
| `cfg.Tools.Defaults` / `cfg.Tools.Agents` | 接入工具选择（Defaults 作为 agent 默认工具集，Agents 作为按 agent 的 tool 分配）或从 config schema 移除 |
| `cfg.Memory.SessionMemory/UserProfile/TaskDistillation` | 接入 bootstrap wireMemory（映射到 ares_memory 配置）或移除 |
| `cfg.Memory.EnableDistillation/DistillationThreshold` | 接入 bootstrap wireDistillation 门控（当前蒸馏仅由 Storage+Embedding 门控，无视这些字段）或移除 |
| `ares.yaml` 中 `memory.enable_distillation` / `memory.distillation_threshold` | 移除无效字段或接上实际门控逻辑 |
| `ares.yaml` 中 `reflection` | 移除无效字段 |
| `CrossoverGenome` 接口 | 删除 |
| `ares_flight` AutoDiagnose / SuggestFix(Concurrency) 死代码 | 删除或接入 handleTaskEnd |
| `taskfabric.NewLease` 死代码 | 删除或改为使用 f.now() |
| `tools/resources/core.GlobalRegistry` 包装函数 | 删除 |
| `kernelscheduler` 冗余条件 | 清理 |
| `llmservice` 死字段 | 随 P3-1 删除 |

---

## Phase 5: 闭环验证 (端到端测试)

为每个闭环模块添加 e2e 测试，确保"接线后真的生效"：

1. **技能闭环**: 启动 serve → 注册技能工具 → LLM 调用 skill_load → SkillOutcomeRecorder 记录 EventSubTaskResult → Experience 更新 → 调度器置信度变化。
2. **进化候选闭环**: evolution.enabled=true → GA 生成候选 → CandidateVerifier 三关验证 → CandidatePipeline 发布 → ProfileStore 稳定配置生效。
3. **自愈闭环**: 注入失败 → Coordinator 记录 → NotifySelfHealingAttempt → 重试 → 结果回写。
4. **arena 飞行数据闭环**: arena serve → SetFlightRecorder → 执行动作 → timeline/diagnostics 端点返回非 503。
5. **可观测性闭环**: 启动 OTel/Prometheus → 跑任务 → /metrics 出现自定义指标，CostDashboard 有会话成本。
6. **知识服务闭环**: KnowledgeService 端点 → BuildGraph → Query 返回真实对象。

---

## 实施顺序与工作量估算

| 阶段 | 内容 | 预计工作量 |
|------|------|-----------|
| Phase 1 | 7 个高优先级 bug 修复 | 2-3 天 |
| Phase 2 | 16 个中低优先级 bug 修复 | 2-3 天 |
| Phase 3 | 13 个未接入模块决策 + 12 个子系统接线 + 3 个空转补齐 | 5-7 天 |
| Phase 4 | 配置/死代码清理 | 1 天 |
| Phase 5 | 6 个闭环 e2e 测试 | 2-3 天 |

**总预估**: 12-17 人日。

---

## 里程碑

- **M1** (Phase 1 完成): 无崩溃风险、无数据正确性 bug，`go test -race ./...` 全绿。
- **M2** (Phase 3 完成): 无伪接线模块（所有模块要么接线要么删除），`cmd/ares` 传递导入闭包含盖所有保留模块。
- **M3** (Phase 5 完成): 6 个闭环 e2e 测试全部通过，项目彻底闭环。
