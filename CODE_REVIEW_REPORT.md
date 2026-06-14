# GoAgentX 代码审查报告

> 生成日期: 2026-06-14（修复审核: 2026-06-14）
> 范围: 全部 497 个 Go 源文件
> 审查类型: Dead Code / 潜在 Bug / 技术债务
> 修复状态: 20 项已修复 / 1 项误报 / 3 项需后续工作 / 3 项未解决

---

## 1. 严重性汇总

| 类别 | High | Medium | Low | 合计 |
|------|------|--------|-----|------|
| **Bug** | 23 | 56 | 22 | 101 |
| **Dead Code** | 1 | 12 | 16 | 29 |
| **Tech Debt** | 3 | 30 | 65 | 98 |
| **合计** | 27 | 98 | 103 | 228 |

---

## 2. High 级别问题

### 2.1 Bug-High

| # | 文件 | 行号 | 状态 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/dashboard/ws_hub.go` | 142 | ✅ 已修复 | **竞态条件**: `removeClient` 读 `client.channels` 持 `h.mu`，`Subscribe`/`Unsubscribe` 写时持 `c.mu`，不同锁导致 data race。 | `removeClient` 中先获取 `client.mu`。 |
| 2 | `internal/dashboard/orchestrator.go` | 253-273 | ✅ 已修复 | **无限复活循环**: 复活 agent 再次被杀时递归 `CreateAgent`，无限制 goroutine 创建。 | 添加复活计数器/最大次数限制。 |
| 3 | `internal/events/memory_store.go` | 256-265, 307-318 | ✅ 已修复 | **双重关闭 channel 导致 panic**: `Close()` 与 `unsubscribe` 可能同时关闭同一 channel。 | 用 `sync.Once` 保护 close。 |
| 4 | `internal/experience/conflict_resolver.go` | 35-81 | ✅ 已修复 | **聚类算法缺陷**: `DetectConflictGroups` 仅将候选与组首元素比较。 | 与组内所有成员比较。 |
| 5 | `internal/flight/collector.go` | 298-301 | ✅ 已修复 | **死代码/逻辑错误**: `handleToolEvent` 内 `isLLMEvent(evt)` 永远 false。 | 移除 `isLLMEvent` 检查。 |
| 6 | `internal/runtime/manager.go` | 515 | ✅ 已修复 | **errgroup 竞态**: `NotifyAgentDead` 释放锁后调用 `m.g.Go()`，`Stop()` 并发调用 `m.g.Wait()`。 | 释锁前调用 `Go()`。 |
| 7 | `internal/tools/resources/builtin/execution/code_runner.go` | 153-236 | ✅ 已修复 | **安全沙箱增强**: 进程组隔离、临时隔离目录、最小环境变量、代码长度限制（10k）、`AddDangerousPattern` 实现。 | 仍需容器级方案作深层防御。 |
| 8 | `internal/mcp/client.go` | 201 | ✅ 已修复 | **nil cancel 导致 panic**: `Close()` 无检查调用 `c.cancel()`。 | `if c.cancel != nil { c.cancel() }`。 |
| 9 | `internal/mcp/transport_stdio.go` | 62-67 | ✅ 已修复 | **子进程丢失环境变量**: `t.cmd.Env` 设为仅含自定义变量的 slice。 | `append(os.Environ(), customEnvVars...)`。 |
| 10 | `internal/memory/production_manager.go` | 777 | ✅ 已修复 | **sql.ErrNoRows 与 pgx 不匹配**: 错误检查用 `database/sql` 的 sentinel 不匹配 pgx 驱动。 | 同时检查两个 sentinel error。 |
| 11 | `internal/workflow/engine/executor.go` | 536 | ⚠️ 误报 | **time.After 在重试循环中**: 每次迭代创建新 timer，但重试次数上限 ≤5，影响极小。 | 无需修改。 |
| 12 | `internal/workflow/engine/executor.go` | 242-271 | ✅ 已存在修复 | **panic 结果丢失**: `recover()` 捕获的 panic 赋值给局部变量。 | 实际代码已正确处理：panic 被 recover 并通过 resultChan 发送。原报告行号有误。 |
| 13 | `api/adapters.go` | 25 | ✅ 已修复 | **nil 指针**: `r.Content` 未判 nil，`ArenaAdapter.Stats/History` 返回真实数据替代 nil。 | 已重写 Stats/History 返回真实统计。 |
| 14 | `api/adapters.go` | 37 | ✅ 已修复 | **nil 指针**: `ListTools` 可能返回 nil slice，改进 success 追踪。 | `success` 变量正确跟踪执行状态。 |
| 15 | `api/service.go` | 70 | ✅ 已修复 | **错误被静默丢弃**: MCP ListTools 错误被 `_` 丢弃，bridge/flight recorder 启动错误也被忽略。 | 检查 error；bridge/fr 启动错误记录日志。 |
| 16 | `api/reviews.go` | 81 | ✅ 已修复 | **索引越界**: `task.Tools[0][0]` 在 Tools 为空时 panic。 | 添加 `len(task.Tools) > 0` 判定。 |
| 17 | `api/client/config.go` | 21 | ✅ 已修复 | **竞态条件**: 包级可变变量 `allowedConfigDir` 无同步访问。 | 添加 `sync.RWMutex` 保护读写。 |
| 18 | `api/memory/distillation_service.go` | 80-196 | ✅ 已修复 | **ctx interface{} 修复**: 内部 `ExperienceRepository` 接口 + adapter + mock 全部改用 `context.Context`。 | 移除 adapter 中 5 处类型断言。 |
| 19 | `api/service/workflow/service.go` | 149 | ✅ 已修复 | **context 误用**: `defer cancel()` 在 goroutine 外，函数返回时提前杀死运行中的工作流。 | 将 timeout context 的 cancel 移到 goroutine 内部。 |
| 20 | `api/service/agent/service.go` | 198 | ✅ 已修复 | **nil 指针**: `DeleteAgent` 中 `s.repo.Get()` 未判 nil。 | 添加 `if s.repo == nil { return error }`。 |
| 21 | `api/service/retrieval/service.go` | 163 | ✅ 已修复 | **安全**: `GetKnowledge` 加 `tenantID` 参数，repo 层直接过滤。 | 删除 service 层多余的 `item.TenantID != tenantID` 检查。 |
| 22 | `internal/callbacks/callbacks.go` | 77, 84 | ✅ 已修复 | **nil 指针 + handler panic 无隔离**: nil ctx 导致 panic；handler panic 传播到调用方。 | 添加 nil 判定 + 每个 handler 用 recover 隔离。 |

### 2.2 Dead Code-High

| # | 文件 | 行号 | 状态 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/flight/collector.go` | 298-301 | ✅ 已修复 | `handleToolEvent` 中 `NodeLLM` 不可达。 | 移除该分支。 |

### 2.3 Tech Debt-High

| # | 文件 | 行号 | 状态 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/storage/postgres/repositories/` | 多处 | ✅ 已修复 | **模板式 CRUD**: 新增 `base_repository.go` 泛型函数 `GetByID[T]`/`DeleteByID`/`CountByTenant`，5 个 repo 的 `Delete` 精简为单行。 | 后续可扩展更多通用方法。 |
| 2 | `internal/flight/genealogy_collector.go` | 109-127 | ✅ 已修复 | **封装破坏**: 直接访问 `c.genealogy.mu` 和 `c.genealogy.nodes`。 | 添加 `Genealogy.RecordRoot()` 方法替代直接访问。 |
| 3 | 跨领域 | 全局 | ❌ 按设计保留 | **模块路径**: `go.mod` 模块名为 `goagentx`，目录名为 `goagent`，此为项目设计选择。 | 不影响编译和运行。 |

---

## 3. Medium 级别问题（按目录分组）

### 3.1 `internal/api/` (12)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `api/adapters.go` | 81 | Bug | `a.Orch` 未判 nil，nil 时 `ListAgents()` panic。 | 判 nil 或文档化。 |
| 2 | `api/adapters.go` | 82 | Bug | `success` 初始化 false 但仅 switch 内更新，无 default case 返回 false。 | 加 default case + logging。 |
| 3 | `api/service.go` | 109 | Tech Debt | `_ = s.httpServer.ListenAndServe()` 错误被丢弃。 | 至少日志记录。 |
| 4 | `api/service.go` | 136 | Bug | `Shutdown(context.Background())` 用 background context 而非 `s.ctx`。 | 用 `s.ctx` 或派生 timeout context。 |
| 5 | `api/config.go` | 41 | Bug | `return &cfg, yaml.Unmarshal(...)` 解析失败仍返回部分配置。 | 解析失败返回 nil。 |
| 6 | `api/handler/stream.go` | 101 | Bug | `for event := range eventCh` 无 `select` + `ctx.Done()`，channel 不关闭时 handler 永久阻塞。 | 加 select 监听 ctx.Done()。 |
| 7 | `api/router/router.go` | 26 | Tech Debt | `AgentProcessorFunc` 用 `ctx any` 而非 `context.Context`。 | 改签名。 |
| 8 | `api/client/config.go` | 301 | Tech Debt | `os.Getenv("LLM_API_KEY")` 无前缀可能读到同名无关变量。 | 添加统一前缀。 |
| 9 | `api/client/client.go` | 44 | Bug | `NewClient` 未验证子 config 是否 nil，`config.LLM.Provider` 在 config.LLM nil 时 panic。 | 判 nil。 |
| 10 | `api/client/client.go` | 193 | Bug | `Ping()` 在 agent/memory/retrieval 为 nil 时返回 false，但这些是可选的。 | 只检查必需服务。 |
| 11 | `api/client/workflow.go` | 95 | Bug | 闭包捕获 range 变量 `agentConfig` 引用，所有闭包共享最终值。 | 循环内拷贝 `ac := agentConfig`。 |
| 12 | `api/client/workflow.go` | 228 | Bug | `cancel()` 仅在 `e.timeout > 0` 时赋值，timeout=0 时调用 nil 函数 panic。 | `if cancel != nil { cancel() }`。 |

### 3.2 `internal/agents/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/agents/leader/dispatcher.go` | 112 | Bug | tasks slice 中 nil 元素导致 `models.NewTaskResult(...)` nil 指针 panic。 | `if task == nil { continue }`。 |
| 2 | `internal/agents/leader/profile.go` | 194,199 | Tech Debt | `toFloat64()` 未处理 int32/int16/int8/uint/uint64 类型。 | 添加缺失 case。 |

### 3.3 `internal/arena/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/arena/service.go` | 155-194 | Bug | `emitEvent` 在 mutex 外获取 stream version，并发 Execute 导致事件丢失。 | version 读取+Append 期间持锁。 |
| 2 | `internal/arena/service.go` | 79-88 | Bug | `s.actions` 和 `s.stats` 无限制增长，内存耗尽。 | 保留最近 1000 条。 |

### 3.4 `internal/bootstrap/` (1)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/bootstrap/bootstrap.go` | 114 | Bug | `dashboard.NewAPIv2(nil,...)` 第一参数硬编码 nil，dashboard 无法管理 agent。 | 传入 runtime 引用。 |

### 3.5 `internal/callbacks/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/callbacks/callbacks.go` | 84 | Bug | handler panic 时后续 handler 被跳过且 panic 传播到调用方。 | 用 `recover()` 隔离每个 handler。 |
| 2 | `internal/callbacks/callbacks_test.go` | 140-166 | Tech Debt | `TestConcurrentOnAndEmit` 只断言 "no crash"，无顺序保证，部分 Emit 可能看到 0 handler。 | 同步 writer 与 reader。 |

### 3.6 `internal/core/` (1)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/core/errors/handler.go` | 64-90 | Bug | `strategy.Backoff` 为 0 时 `RetryWithBackoff` 无延迟忙等。 | 强制最小 backoff（100ms）。 |

### 3.7 `internal/dashboard/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/dashboard/orchestrator.go` | 265 | Bug | 自动复活仅检查 `result.Status != "completed"`，非 arena 杀的取消也触发。 | 明确跟踪复活原因。 |
| 2 | `internal/dashboard/orchestrator.go` | 749-752 | Dead Code | `case int:` 不可达，JSON 反序列化产生 float64 非 int。 | 移除该 case。 |

### 3.8 `internal/eval/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/eval/loader.go` | 22-27 | Bug | 路径遍历保护仅阻止 `/etc/`，`../../../var/log` 可通过。 | 用 `filepath.Clean` 验证。 |
| 2 | `internal/eval/agent_runner.go` | 26-37 | Bug | `RunSuite` 首个错误立即返回，跳过剩余用例。 | 收集所有结果不 abort。 |

### 3.9 `internal/events/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/events/store.go` | 14 | Tech Debt | 文档与实现不一致：`expectedVersion==0` 含义矛盾。 | 统一文档与实现。 |
| 2 | `internal/events/pg_store.go` | 139-144 | Tech Debt | 用 PostgreSQL 错误码 23505 检查版本冲突，schema 变更脆弱。 | 用 `SELECT...FOR UPDATE`。 |

### 3.10 `internal/experience/` (4)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/experience/ranking_service.go` | 103 | Bug | 长度不匹配返回空 slice 无 error，调用者无法区分"无匹配"和"出错"。 | 返回 error。 |
| 2 | `internal/experience/distillation_service.go` | 67-69 | Tech Debt | `Distill` 返回 `(nil, nil)` 易误用为 nil 解引用。 | 返回 `ErrSkipDistillation`。 |
| 3 | `internal/experience/distillation_service.go` | 155-171 | Tech Debt | 硬编码 30 秒超时。 | 使可配置。 |
| 4 | `internal/experience/distillation_service.go` | 205-258 | Tech Debt | 字符串前缀匹配解析 LLM 输出，格式偏离时静默空结果。 | 用 JSON mode 或全 section 验证。 |

### 3.11 `internal/flight/` (3)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/flight/collector.go` | 149-168 | Bug | `handleAgentStart` 用事件 ID 添加节点，`handleAgentEnd` 用 agent ID 查找，ID 不匹配。 | 统一用 AgentID。 |
| 2 | `internal/flight/graph.go` | 58-72 | Bug | `AddNode` 静默替换根节点，多根场景被覆盖。 | 支持多根或返回 error。 |
| 3 | `internal/flight/recorder.go` | 17 | Dead Code | `memManager` 字段存而不用。 | 移除。 |

### 3.12 `internal/mcp/` (5)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/mcp/factory.go` | 88-124 | Bug | `Create` 返回首个 tool，其余工具被丢弃。 | 返回工具集合或关闭不用 client。 |
| 2 | `internal/mcp/transport_sse.go` | 150-152 | Bug | 未发 `endpoint` 事件时 `postURL` 保持 GET 端点。 | 首个事件超时视为错误。 |
| 3 | `internal/mcp/client.go` | 105-111 | Tech Debt | factory/manager 直接访问 `client.mu`、`client.tools` 等私有字段。 | 添加公共访问方法。 |
| 4 | `internal/mcp/schema.go` | 11-53 | Tech Debt | `ConvertJSONSchema` 仅支持极小 JSON Schema 子集。 | 逐步补充或用完整库。 |
| 5 | `internal/mcp/factory.go` | 99 | Tech Debt | 用 `context.Background()`。 | 添加 ctx 参数。 |

### 3.13 `internal/memory/` (4)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/memory/manager_impl.go` | 129-174 | Bug | `Stop()` 后无法 `Start()`，`started` 标志未重置。 | `Stop()` 中设 `started=false`。 |
| 2 | `internal/memory/production_manager.go` | 664 | Bug | `fmt.Sprintf("%v", payload)` 产生 Go map 格式而非 JSON。 | 用 `json.Marshal`。 |
| 3 | `internal/memory/production_manager.go` | 772-783 | Tech Debt | 原始 SQL 不用 repository 层。 | 创建 `LeaderCheckpointRepository`。 |
| 4 | `internal/memory/manager_impl.go` | 569-596 | Tech Debt | 注释说 "LRU" 但实为 FIFO（按创建时间驱逐，永不更新）。 | 实现真 LRU 或修正注释。 |

### 3.14 `internal/plugins/resurrection/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/plugins/resurrection/resurrection.go` | 179-180 | Bug | `onFailure()` 调用 `s.g.Go()` 但 `s.g` 仅在 `Start()` 初始化。 | 加 nil 保护或移到 `New()`。 |
| 2 | `internal/plugins/resurrection/resurrection.go` | 412-424 | Bug | `Unwatch()` 和 `resurrect()` 竞态可撤销 unwatch。 | stop+replace 序列中持写锁。 |

### 3.15 `internal/protocol/ahp/` (3)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/protocol/ahp/queue.go` | 55 | Tech Debt | `Enqueue` 接受 `ctx` 但从不检查取消。 | select 中含 `<-ctx.Done()`。 |
| 2 | `internal/protocol/ahp/dlq.go` | 251-259 | Tech Debt | `defaultHandler` 对未知错误返回 nil，静默移除 DLQ 条目。 | 返回哨兵错误阻止移除。 |
| 3 | `internal/protocol/ahp/protocol.go` | 106-115 | Tech Debt | `SendTask/SendResult` 硬编码 `"leader"` 为源/目标 ID。 | 接受参数或从 context 派生。 |

### 3.16 `internal/ratelimit/` (1)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/ratelimit/backpressure.go` | 140-150 | Bug | `active` 可超 `maxActive`，多 worker 同时处理时无上限。 | `processLoop()` 加限制。 |

### 3.17 `internal/runtime/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/runtime/manager.go` | 247-331 | Bug | `RestartAgent()` 和 `NotifyAgentDead()` 竞态导致 agent 泄漏。 | stop+replace 序列中持写锁。 |
| 2 | `internal/runtime/manager.go` | 341-459 | Tech Debt | `RestoreAgent()` 118 行违反单一职责。 | 拆分为多个方法。 |

### 3.18 `internal/shutdown/` (2)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/shutdown/manager.go` | 58-65 | Bug | context 取消时 shutdown 不保证完成，select 提前选 `<-ctx.Done()`。 | 完成当前 phase 再取消。 |
| 2 | `internal/shutdown/signal.go` | 18-24 | Tech Debt | `HandleSignals` 无限阻塞，manager 关闭后泄漏 goroutine。 | 加 select 监听 ctx 取消。 |

### 3.19 `internal/storage/` (5)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/storage/postgres/pool.go` | 28-35 | Bug | `NewPool` 未设 `MaxOpenConns/MaxIdleConns`，连接耗尽。 | 设合理默认值。 |
| 2 | `internal/storage/postgres/vector.go` | 120-150 | Tech Debt | 未验证向量维度，DB 捕获但浪费一次往返。 | 客户端侧验证。 |
| 3 | `internal/storage/postgres/embedding/fallback_client.go` | 20-35 | Tech Debt | FallbackClient 静默吞主 client 错误。 | fallback 时记录日志。 |
| 4 | `internal/storage/postgres/write_buffer.go` | 50-65 | Tech Debt | WriteBuffer 无大小上限。 | 加最大 buffer 和背压。 |
| 5 | `internal/storage/postgres/services/retrieval_service.go` | 1-300+ | Tech Debt | 300+ 行混合 embedding/搜索/CRUD，违反单一职责。 | 拆分为多文件。 |

### 3.20 `internal/tools/` (4)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/tools/resources/core/registry.go` | 104-138 | Bug | nil filter 时返回共享 map 的 Registry，变更互相影响。 | nil filter 时深拷贝。 |
| 2 | `internal/tools/resources/core/capability.go` | 175-193 | Bug | 注册新 tool 后 `buildCapabilityMap` 不自动重建。 | 订阅 registry 事件。 |
| 3 | `internal/tools/resources/builtin/network/http_request.go` | 96-105 | Bug | Body JSON 验证后丢弃结果，仅用于返回错误信息。 | 移除验证或拒绝无效 JSON。 |
| 4 | `internal/tools/resources/builtin/network/web_scraper.go` | 190-193 | Bug | 正则 `<nav.*?>.*?</nav>` 非贪婪，嵌套元素失败。 | 用 `golang.org/x/net/html`。 |

### 3.21 `internal/workflow/` (5)

| # | 文件 | 行号 | 类别 | 问题 | 建议修复 |
|---|------|------|------|------|----------|
| 1 | `internal/workflow/engine/registry.go` | 98 | Bug | `CreateAgent(ctx, type, step.Input)` 把输入字符串当配置传入。 | 传 nil 或正确配置对象。 |
| 2 | `internal/workflow/engine/registry.go` | 112-117 | Bug | `result.(*models.RecommendResult)` 硬断言，不同结果类型 panic。 | 类型 switch 或通用接口。 |
| 3 | `internal/workflow/engine/executor.go` | 110-121 | Bug | 错误消息用 `result.Error` 但此时为空。 | 在 L108 设值。 |
| 4 | `internal/workflow/engine/reloader.go` | 44-66 | Bug | 构造函数 loader nil 时 panic；`w.watcher` 在 `NewWatcher` 失败时也 nil。 | 返回 error 替代 panic。 |
| 5 | `internal/workflow/engine/reloader.go` | 336-339 | Bug | `r.loader.(*FileLoader)` 硬断言 panic。 | 安全类型断言。 |

### 3.22 Dead Code-Medium (7)

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/dashboard/types.go` | 9-149 | 大量未使用的 view struct（`SystemOverview`、`RuntimeStatsView`、`MemoryStats` 等 17+ 个）。 | 移除未使用的类型。 |
| 2 | `internal/dashboard/service.go` | 8-45 | 未使用的 `AgentInfo`、`AgentProvider`、`DashboardConfig`。 | 移除。 |
| 3 | `internal/dashboard/orchestrator.go` | 105-107 | `EventBroadcaster` 接口从未使用。 | 移除。 |
| 4 | `internal/agents/sub/handler.go` | 40-48 | `handleTaskMessage`、`handleAckMessage` 无条件返回 nil，忽略消息。 | 实现或移除。 |
| 5 | `internal/experience/ranking_service.go` | 48-54 | `DefaultRankingWeights()` 定义但未调用。 | 移除。 |
| 6 | `internal/experience/ranked_experience.go` | 99-103 | `GetUsageCount()` 是导出字段的 getter。 | 移除。 |
| 7 | `internal/ratelimit/backpressure.go` | 17 | `queueSize` 存而不用。 | 移除字段。 |

### 3.23 Tech Debt-Medium (20)

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/config/config.go` | 16-22 | `allowedConfigDir` 可变包级全局，非线程安全。 | 移到 config loader struct。 |
| 2 | `internal/eval/agent_runner.go` | 41-71 | `RunSingle` 手动构造 `TestResult` 而非用 `NewTestResult()`。 | 用 `NewTestResult`。 |
| 3 | `internal/flight/recorder.go` | 17 | `FlightRecorderConfig.MemManager` 存而不用。 | 移除配置字段。 |
| 4 | `internal/mcp/manager.go` | 218 | Version 始终为 "connected"。 | 暴露实际协议版本。 |
| 5 | `internal/memory/production_manager.go` | 666-667 | 注释说提取但硬编码 `"output":""`。 | 实际提取 output。 |
| 6 | `internal/runtime/manager.go` | 752-810 | `buildCognitiveState()` 硬编码 5 秒超时。 | 添加可配置字段。 |
| 7 | `internal/storage/postgres/config.go` | 30-45 | `DSN()` 未 URL-encode 密码。 | 用 `net/url` 编码。 |
| 8 | `internal/storage/postgres/circuit_breaker.go` | 30-45 | 断路器半开探测间隔硬编码。 | 使其可配置。 |
| 9 | `internal/storage/postgres/security.go` | 1-70 | 中间件可能不捕获遗漏 tenant_id 的查询。 | 添加 lint 规则。 |
| 10 | `internal/tools/resources/formatter/result_formatter.go` | 405-433 | JSON fallback 失败时静默返回 nil。 | 返回 error。 |
| 11 | `internal/tools/resources/builtin/text/data_validation.go` | 106 | RFC 5322 email 正则过于简化。 | 文档限制或用库。 |
| 12 | `internal/workflow/engine/dynamic_executor.go` | 55-57 | 直接构造 `Executor{}` 而非用 `NewExecutor()`，重复默认常量。 | 调用 `NewExecutor()`。 |
| 13 | `internal/workflow/engine/dynamic_executor.go` | 576-619 | `recomputeOrder` 仅追加新 step，未处理移除。 | 跳过已移除 step。 |
| 14 | `internal/workflow/engine/definition.go` | 242 | `dir + "/" + entry.Name()` 不用 `filepath.Join`。 | 用 `filepath.Join`。 |
| 15 | `internal/callbacks/callbacks.go` | 58-69 | 无 `Remove` 方法取消单个 handler。 | 添加 `Remove(event, handler)`。 |
| 16 | `internal/callbacks/callbacks.go` | 40 | `Handler` 无返回值，handler 无法报告错误。 | 考虑 `Handler func(ctx *Context) error`。 |
| 17 | `api/client/config.go` | 301 | `os.Getenv("LLM_API_KEY")` 无前缀。 | 加统一前缀或文档。 |
| 18 | `api/client/workflow.go` | 107 | 注册错误被静默丢弃。 | 日志记录。 |
| 19 | `api/service/graph/graph_builder.go` | 240 | 类型断言 `config.Config["agent_id"].(string)` 非 string 时 panic。 | 用 comma-ok。 |
| 20 | `api/service/memory/service.go` | 347 | `len(s)` 按字节非按字符计数，多字节字符截断产生无效 UTF-8。 | 用 `[]rune(s)`。 |

---

## 4. Low 级别问题（按目录分组）

### 4.1 `internal/` Bug-Low

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/arena/survival.go` | 219-227 | `ActionRemoveEdge` 在 len(ids)==2 时分布倾斜。 | 用排列法选不同索引。 |
| 2 | `internal/flight/genealogy.go` | 84-130 | 复活后旧节点仍显示在树中，未文档化。 | 过滤死节点或明确文档。 |
| 3 | `internal/protocol/ahp/queue.go` | 165-197 | `Size/IsEmpty/IsFull/Available` 读 chan len 无同步。 | 移到锁内或用 atomic。 |
| 4 | `internal/protocol/ahp/dlq.go` | 179-205 | `GetAll` 返回浅拷贝，`Remove` 用指针比较。 | 文档 `GetAll` 语义。 |
| 5 | `internal/ratelimit/sliding_window.go` | 49-85 | `Wait` TOCTOU：`Allow` 和锁获取间 window 可能被清空。 | 在锁内重查条件。 |
| 6 | `internal/ratelimit/backpressure.go` | 191-198 | `Metrics()` 读 `len(bp.queue)` 未持锁。 | 移到锁内。 |
| 7 | `internal/shutdown/phase.go` | 22-38 | 首次重试前总有 `InitialDelay`。 | 首次重试零延迟。 |
| 8 | `internal/shutdown/phase.go` | 40-42 | context 取消后仍返回最后 error。 | 返回前检查 `ctx.Err()`。 |
| 9 | `internal/storage/postgres/pool.go` | 40-42 | `pool.Ping()` 在初始化期间阻塞，DB 不可用则服务完全失败。 | 考虑懒初始化。 |
| 10 | `internal/events/memory_store_test.go` | 288 | `string(rune('A'+id))` 在 id>=26 时出意外字符。 | 用 `fmt.Sprintf("stream-%d", id)`。 |

### 4.2 `internal/tools/resource/` Bug-Low

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/tools/resources/builtin/knowledge/knowledge_base.go` | 107 | `top_k`、`min_score` 在 schema 中但未用。 | 传递参数或从 schema 移除。 |
| 2 | `internal/tools/resources/builtin/text/data_validation.go` | 163 | URL 正则不允许 localhost 或 IP。 | 用 `net/url.Parse`。 |
| 3 | `internal/tools/resources/builtin/memory/memory_tools.go` | 183-192 | 硬编码中文关键词。 | 使可配置。 |
| 4 | `internal/tools/resources/builtin/planning/task_planner.go` | 59 | `TaskPlanner` 能力设为 `CapabilityMath`，应为 `CapabilityKnowledge`。 | 修正。 |
| 5 | `internal/tools/resources/formatter/result_formatter.go` | 204 | 硬编码 emoji，纯文本终端异常。 | 使 emoji 可选。 |
| 6 | `internal/security/sanitizer.go` | 10-12 | `MustCompile` 在无效正则时 panic。 | 用 `Compile` + 错误处理。 |

### 4.3 `api/` Bug-Low (3)

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `api/adapters.go` | 82 | `success` 初始化 false，无 default case。 | 加 default case + logging。 |
| 2 | `api/client/workflow.go` | 189 | `WorkflowAgentExecutor.Process` 未验证 agent 是否已启动。 | 检查 `e.started`。 |
| 3 | `api/service/llm/service.go` | 237 | `buildPrompt` 迭代 messages 未判 nil。 | 判 nil 或文档前提条件。 |

### 4.4 Dead Code-Low

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/core/errors/errors.go` | 89-95 | `ModelValidationErrors` 导出但无处引用。 | 移除或文档。 |
| 2 | `internal/agents/sub/executor.go` | 93 | `result` 在 L93 创建后 L99 被覆盖。 | 重构提前返回。 |
| 3 | `internal/dashboard/router.go` | 1 | 空占位文件。 | 移除或实现 router。 |
| 4 | `internal/dashboard/handlers_test.go` | 1 | 空测试文件。 | 移除或添加测试。 |
| 5 | `internal/dashboard/api_test.go` | 387-388 | 不必要的 `var _ = time.After`。 | 移除。 |
| 6 | `internal/eval/types.go` | 133-157 | `ToJSON()` 方法定义但未调用。 | 移除或取消导出。 |
| 7 | `internal/eval/types.go` | 99-108 | `NewTestResult()` 定义但未使用。 | 用 `NewTestResult` 或移除。 |
| 8 | `internal/protocol/ahp/dlq.go` | 13 | `MaxRetriesUnlimited = 0` 从未引用。 | 移除。 |
| 9 | `internal/protocol/ahp/ahp_test.go` | 847-849 | 空测试。 | 移除或修复。 |
| 10 | `internal/protocol/ahp/message.go` | 208-216 | 自定义 `MarshalJSON` 与默认行为相同。 | 移除。 |
| 11 | `internal/memory/production_manager.go` | 35,134-137,160 | `embeddingQueue`、`experienceRepository` 存而不用。 | 移除。 |
| 12 | `internal/workflow/engine/types.go` | 12-26 | 多个哨兵错误定义但未被引用。 | 移除或验证。 |
| 13 | `internal/workflow/engine/registry.go` | 121-125 | `StepOutput.Variables` 初始化但未填充。 | 移除或实现。 |
| 14 | `internal/callbacks/callbacks.go` | 15,19,20 | `EventLLMError`、`EventAgentError`、`EventToolStart` 定义但未在包内使用。 | 移除或内部使用。 |
| 15 | `api/agent/errors.go` | 14 | `ErrAgentAlreadyExists` 声明但从未被 `agent.Service` 返回。 | 使用或移除。 |
| 16 | `api/retrieval/service.go` | 93 | `advancedRetrieval` 声明但从未赋值。 | 接入或移除字段。 |

### 4.5 Tech Debt-Low

| # | 文件 | 行号 | 问题 | 建议修复 |
|---|------|------|------|----------|
| 1 | `internal/agents/leader/profile.go` | 26-43 | `NewProfileParser` 不接收 eventStore，需额外 `WithEventStore()`。 | 作为必需参数。 |
| 2 | `internal/agents/leader/agent.go` | 653-657 | `stopCh` 读取无 `distillMu` 锁，不一致。 | 注释说明或移除。 |
| 3 | `internal/agents/leader/supervisor.go` | 41 | TODO "legacy"。 | 处理 TODO。 |
| 4 | `internal/agents/leader/event_recovery.go` | 62 | `Read` 无限制读取所有事件。 | 加限制或分页。 |
| 5 | `internal/agents/leader/recovery.go` | 13 | 表名 `"task_results_1024"` 环境特定后缀。 | 使可配置。 |
| 6 | `internal/agents/leader/recovery.go` | 54-62 | SQL 字符串插值拼接表名。 | 用 `quote_ident`。 |
| 7 | `internal/agents/leader/aggregator.go` | 12-18 | 常量导出但仅包内使用。 | 取消导出。 |
| 8 | `internal/agents/leader/supervisor.go` | 361 | 具体类型断言 `*leaderAgent`。 | 定义接口或通过配置。 |
| 9 | `internal/arena/survival.go` | 236-242 | `randomID` 用 `math/rand`。 | 用 `crypto/rand`。 |
| 10 | `internal/core/models/types.go` | 96-99 | `DefaultSessionTTL/TaskTTL` 可变包级变量。 | 取消导出并提供配置。 |
| 11 | `internal/core/errors/handler.go` | 76 | `1<<(attempt-1)` 32 位可能溢出。 | 添加 cap。 |
| 12 | `internal/dashboard/api.go` | 17 | 导入 `goagentx/...` 与目录名不匹配。 | 验证模块名。 |
| 13 | `internal/dashboard/ws_hub.go` | 237 | `ReadPump` 读限 4096 字节太小。 | 增加或使可配置。 |
| 14 | `internal/dashboard/orchestrator.go` | 807-816 | `BuildToolAliases` 多下划线时错误。 | 文档限制或改进。 |
| 15 | `internal/errors/wrap.go` | 11-16 | `New()` 遮蔽标准库 `errors.New()`。 | 考虑重命名。 |
| 16 | `internal/events/pg_store.go` | 450 | `argIdx++` 递增但未用。 | 移除。 |
| 17 | `internal/events/memory_store.go` | 146-149 | 返回 nil 而非空 slice。 | 返回 `[]*Event{}`。 |
| 18 | `internal/llm/client.go` | 100-115,325-338 | prompt 验证逻辑重复。 | 提取共享函数。 |
| 19 | `internal/llm/client.go` | 111-115,329-333 | `bytes.TrimSpace` 检查空白造成分配。 | 用 `strings.TrimSpace`。 |
| 20 | `internal/llm/client.go` | 141-142,201-202 | 硬编码 temperature/max_tokens。 | 添加配置字段。 |
| 21 | `internal/mcp/client.go` | 318 | `handleNotification` 忽略 ctx 参数。 | 用参数或移除。 |
| 22 | `internal/mcp/factory.go` | 106-110 | 迭代 map 取"第一个 tool"非确定。 | 排序或改设计。 |
| 23 | `internal/mcp/jsonrpc.go` | 128 | `Encode` 无尾部换行。 | 含换行或文档。 |
| 24 | `internal/memory/manager_impl.go` | 346-357 | 包装函数 `distillTaskOld/New` 无意义。 | 直接调用 `distillTaskCommon`。 |
| 25 | `internal/observability/tracer_test.go` | 1,336 | `// nolint:` 格式错误。 | 修正。 |
| 26 | `internal/plugins/resurrection/ahp_adapter.go` | 52-56 | nil→空 slice 不必要。 | 直接返回 nil。 |
| 27 | `internal/protocol/ahp/message.go` | 89-95 | `NewHeartbeatMessage` 未初始化 Payload。 | 初始化 map。 |
| 28 | `internal/protocol/ahp/codec.go` | 69-91 | `MustEncode/Decode` 已弃用但仍导出。 | 移到测试文件或取消导出。 |
| 29 | `internal/ratelimit/limiter.go` | 108-109 | `DefaultFactory` 可变全局。 | 取消导出或文档。 |
| 30 | `internal/ratelimit/token_bucket.go` | 52-80 | `Wait()` 用 `time.After` 忙等待。 | 用 `sync.Cond`。 |
| 31 | `internal/ratelimit/semaphore.go` | 61-67 | `Allow()` 读 chan len 有竞态。 | 文档"非权威"。 |
| 32 | `internal/ratelimit/backpressure.go` | 254-286 | 接受 `Limiter` 但仅用 `Rate()`。 | 直接接受 float64。 |
| 33 | `internal/runtime/manager.go` | 843-860 | 非 factory agent 健康检查失败静默。 | 记录警告。 |
| 34 | `internal/tools/resources/core/result.go` | 33-38 | 值类型上用指针接收者令人困惑。 | 统一接收者类型。 |
| 35 | `internal/tools/resources/base/base_tool.go` | 109-113 | `metadataTool` 不暴露 `IsDeprecated`。 | 在接口中暴露。 |
| 36 | `internal/tools/resources/core/registry.go` | 200-220 | `GlobalRegistry` 全局可变无初始化控制。 | 用构造函数注入。 |
| 37 | `internal/tools/resources/core/capability.go` | 102-121 | `Detect` 遍历所有能力/关键词，性能差。 | 建 trie 或倒排索引。 |
| 38 | `internal/tools/resources/agent/agent_tools.go` | 240-247 | `RegisterBuiltinToolsForAgent` 副作用影响全局。 | 用专用 registry。 |
| 39 | `internal/tools/resources/builtin/text/log_analyzer.go` | 50 | `LogAnalyzer` 无能力设置。 | 添加 `CapabilityText`。 |
| 40 | `internal/tools/resources/builtin/file/file_tools.go` | 141-144,222-227 | 冗余 `filepath.Abs`。 | 移除。 |
| 41 | `internal/tools/resources/builtin/execution/code_runner.go` | 196,201,224 | 小写匹配不防大小写混淆。 | 用 AST 级别分析。 |
| 42 | `internal/workflow/engine/executor.go` | 414-419 | `completedCopy` 创建但未用。 | 移除。 |
| 43 | `internal/workflow/engine/loader.go` | 254-261 | `getFileExt` 重复标准库。 | 用 `filepath.Ext`。 |
| 44 | `internal/workflow/engine/mutable_dag.go` | 82-100 | 回滚逻辑重复。 | 提取辅助方法。 |
| 45 | `internal/workflow/engine/graph_events.go` | 85-96 | 满缓冲丢弃事件。 | 考虑记录日志。 |
| 46 | `internal/security/sanitizer.go` | 14-60 | `[REDACTED]` 保留部分结构。 | 用等长 `***`。 |
| 47 | `internal/storage/memory/vector.go` | 50-60 | O(n) 线性扫描，>10K 不可扩展。 | 考虑 ANN 索引。 |
| 48 | `internal/callbacks/callbacks.go` | 52-56 | 零值 `Registry{}` 有 nil map，`Emit`/`On` 未初始化则 panic。 | 在 `On` 中懒初始化。 |
| 49 | `internal/callbacks/callbacks.go` | 36 | `Extra map[string]any` 无类型安全。 | 文档化或结构化类型。 |
| 50 | `internal/callbacks/callbacks_test.go` | 165 | 注释说"无竞态即通过"但依赖 `go test -race`。 | 更新注释。 |
| 51 | `internal/callbacks/callbacks_test.go` | 173 | `received = *ctx` 浅拷贝，`Extra` 共享底层 map。 | 深拷贝 Extra。 |
| 52 | `api/adapters.go` | 110 | `a.mu.Unlock()` 直接调用而非 defer。 | 用 `defer`。 |
| 53 | `api/core/types.go` | 73 | `NewRequestContext` 初始化 Metadata 但 `WithMetadata` 又判 nil，不一致。 | 统一模式。 |
| 54 | `api/errors/common.go` | 66,72 | `NewAppError` 分配 Context map，`WithContext` 又判 nil，多余。 | 移除一边。 |
| 55 | `api/client/simple.go` | 48 | `NewSimpleClient` 加载 config 两次。 | 传递已解析的 config。 |
| 56 | `api/service/graph/graph_builder.go` | 122 | `fmt.Printf` 不用 slog。 | 替换。 |
| 57 | `api/service/graph/graph_builder.go` | 153 | `fmt.Sprintf("%v", a)` 做值比较，每次分配字符串。 | 用类型 switch 或反射。 |
| 58 | `api/service/workflow/service.go` | 341 | `buildEngineSteps(def)` 调用两次。 | 调用一次传结果。 |
| 59 | `api/service/agent/service.go` | 162 | `UpdateAgent` 类型断言 `updates["status"].(core.AgentStatus)` 无 ok 检查。 | 加 boolean check。 |
| 60 | `api/service/memory/service.go` | 241,246 | `SearchSimilarTasks` 设 `matched=true` 仅因 len>0，无实际匹配逻辑；变量 `context` 遮蔽标准包名。 | 实现真实匹配；变量改名。 |
| 61 | `api/service/retrieval/memory_repository.go` | 198 | 手动冒泡排序。 | 用 `sort.Slice`。 |
| 62 | `api/service/graph/graph_builder.go` | 265 | 类型断言 `tool_id.(string)` 无 ok 检查。 | 加 comma-ok。 |
| 63 | `api/agent/service.go` | 55,56 | `CreateAgent` 静默覆盖已有 agent；存指针而非副本。 | 检查冲突；存副本。 |
| 64 | `api/agent/service_test.go` | 221 | `string(rune('0'+index))` index>9 时出错。 | 用 `fmt.Sprintf`。 |
| 65 | `api/memory/distillation_service_test.go` | 57 | 空白标识符赋值无断言。 | 实现逻辑或移除。 |

---

## 5. 跨领域/全局问题

| # | 类别 | 严重性 | 问题 | 建议修复 |
|---|------|--------|------|----------|
| 1 | Tech Debt | Medium | **包级 `// nolint: errcheck`**: 多处压制错误检查。 | 移除包级 nolint。 |
| 2 | Tech Debt | Medium | **重复 DAG**: `workflow/graph/graph.go` 和 `workflow/engine/types.go` 两套 DAG。 | 合并。 |
| 3 | Tech Debt | Medium | **大量文件测试不足**: executor.go(617行)、dynamic_executor.go(630行) 等缺测试。 | 加单元测试。 |
| 4 | Tech Debt | Medium | **代码重复**: 多个示例中 `truncate`、`jsonToMap`、`getEnv` 重复。 | 提取共享包。 |
| 5 | Tech Debt | Medium | **Makefile build 目标失效**: `build` 硬编码 `./cmd/server` 但目录不存在。 | 修复路径。 |
| 6 | Dead Code | Low | **kernel-fast/src/** 空目录。 | 移除或实现。 |
| 7 | Dead Code | Low | **`cmd/server` 不存在**但多处引用。 | 清理引用。 |

---

---

## 6. 修复状态汇总

| 状态 | 说明 | 数量 |
|------|------|------|
| ✅ 已修复 | 已完成代码修改并通过 `go vet` 和编译验证 | 20 |
| ⚠️ 误报 | 经确认非问题 | 1 |
| ⏸️ 需后续工作 | 需要接口重构、服务端改造等较大范围修改 | 3 |
| ❌ 未解决 | 当前未解决的技术债务 | 3 |

### 已修复文件清单

| 文件 | 修复内容 |
|------|----------|
| `internal/dashboard/ws_hub.go` | race condition: removeClient 锁顺序修正 |
| `internal/dashboard/orchestrator.go` | 无限复活: 添加 maxResurrections 计数器 + Stop() 方法 |
| `internal/events/memory_store.go` | 双重关闭 channel: 添加 sync.Once 保护 |
| `internal/experience/conflict_resolver.go` | 聚类算法: 与组内所有成员比较替代仅比较组首 |
| `internal/flight/collector.go` | 死代码: 移除不可达的 NodeLLM 分支 |
| `internal/flight/diagnostics.go` | 大小写匹配: strings.ToLower + strings.Contains 替代手写搜索 |
| `internal/flight/genealogy.go` | 添加 RecordRoot 方法修复封装 |
| `internal/flight/genealogy_collector.go` | 使用 RecordRoot 替代直接访问 struct 字段 |
| `internal/mcp/client.go` | nil cancel: 添加 if 判定 |
| `internal/mcp/transport_stdio.go` | 环境变量: append(os.Environ(), ...) |
| `internal/mcp/transport_sse.go` | channel 关闭竞态: 在 errgroup goroutine 内 close |
| `internal/memory/production_manager.go` | sql.ErrNoRows/pgx.ErrNoRows 双检查; 移除死字段; 修复重启标志 |
| `internal/core/errors/errors.go` | 移除死代码 ModelValidationErrors |
| `internal/runtime/manager.go` | errgroup 竞态: g.Go() 移至 mu.Unlock() 前 |
| `internal/ratelimit/backpressure.go` | 移除未使用的 queueSize 字段 |
| `internal/dashboard/types.go` | 移除 17+ 个未使用的类型定义 |
| `internal/agents/leader/dispatcher.go` | nil task 判定 |
| `internal/callbacks/callbacks.go` | nil ctx 判定 + handler panic recover 隔离 |
| `api/adapters.go` | Stats/History 返回真实数据; 改进 success 追踪 |
| `api/service.go` | MCP 空配置检查; bridge/fr 启动错误处理 |
| `api/reviews.go` | Tools 索引越界判定 |
| `api/client/config.go` | allowedConfigDir 添加 sync.RWMutex 保护 |
| `api/service/agent/service.go` | DeleteAgent nil repo 判定 |
| `api/service/workflow/service.go` | timeout cancel 移到 goroutine 内 |

---

*报告结束。共 228 项发现（27 High / 98 Medium / 103 Low），其中 20 项已修复。*
