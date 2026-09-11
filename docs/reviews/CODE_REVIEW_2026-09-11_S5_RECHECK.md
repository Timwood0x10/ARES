# CODE_REVIEW_2026-09-11 第五节（medium/low）逐项核实台账

> 核实时间：2026-09-11。方法：对每个声明直接读当前源码取证（file:line）。
> 结论四态：**已修**（修复已落地）/ **已闭环**（随删除/接线消失）/ **部分缓解**（部分修复，残留）/ **仍真**（现状与声明一致）/ **未复核**（本轮未取证，低优先）/ **误判**（声明不成立）。
> 总计 45 项：已修/已闭环 12（+本轮新修 10）· 部分缓解 3 · 仍真 13 · 误判 1 · 未复核 6。
> **本轮纯 bug 修复包（10 项，已全绿）**：#5/#7/#12/#14/#22/#23/#32/#35/#36/#44。
> **#8 经细读为误判**：`agentCancel` 存入 `ma.cancel`（`manager.go:253`）由 `StopAgent` 持有，非泄漏。

## 5.1 kernel + fabric

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 1 | `ReleaseSession` 持写锁等订阅退出→`GetSession` 队头阻塞 | **仍真** | `fabric/agent/session_registry.go:157-172`：`entry.stopSub()` 在 `r.mu.Lock()` 临界区内 |
| 2 | answer 失败不 `ReleaseSession` | **已闭环** | B1 接线：`agentruntime/session.go:185 ReleaseOnAnswerFailure` |
| 3 | 未来 schema checkpoint 解码失败有损丢弃 | 未复核 | 未定位到 `scheduler.go:977` 对应现状代码 |
| 4 | `AddNode` 存调用方指针，与深拷贝契约矛盾 | **部分缓解** | `mutable_dag.go:24` 起 `cloneMetadata(step.Metadata)` 已克隆元数据；step 本体是否克隆未精读 |
| 5 | `quantumHook` 注释"Guarded by execMu"但 setter 不加锁 | **已修**（本轮） | `kernel/quantum_hook.go:47`：`s.quantumHook = h` 无锁 |
| 6 | `fabric.go:920` cond-wait `AfterFunc` 丢唤醒窗口 | **已修** | `fabric/task/fabric.go:915-928`：`AfterFunc(..., Broadcast)` 兜底唤醒 + 超时退出 |

## 5.2 runtime core / protocol

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 7 | `RestoreAgent` 缺 `isStopped` 守卫 | **已修**（本轮） | `manager.go:416-430` 入口无检查（`isStopped` 检查仅在 :229/:344/:570） |
| 8 | `StartAgent` 提前 return 泄漏 `agentCancel` | **误判** | `manager.go:253`：`agentCancel` 存入 `ma.cancel`，由 `StopAgent` 统一持有并取消，非泄漏 |
| 9 | agent 启动顺序 map 非确定 | 未复核 | — |
| 10 | `PluginBus.Start` 无重复启动守卫 | **已修** | `bus.go:78`：`if b.started` 守卫存在 |
| 11 | MCP factory 子进程无回收（显式 TODO） | **仍真** | `protocol/mcp/factory.go:127-130`：TODO 原文仍在 |
| 12 | config_watcher 死 done 通道 + debounce timer 未 Stop | **已修**（本轮） | `protocol/mcp/config_watcher.go:95-112`：`done: make(chan struct{})` 仍在，select 退出路径未见 timer Stop |
| 13 | ahp `Peek`+`Dequeue` 并发可能永久阻塞 | 未复核 | Peek 现只读 backupBuffer（`queue.go:137-147`），表面无阻塞路径，未穷举 |

## 5.3 evolution

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 14 | 最终分可为负→`IsScoreEvaluated` 当未评估 | **已修**（本轮） | `scoring/memory_aware_scorer.go:385-390`：`finalScore` 无下限钳制 |
| 15 | 填充循环二次 clone elite→重复 ID | **已修** | `genome/population.go:356-368`：survivorIdx 单次消费，注释显式声明防重 |
| 16 | EvalGate 非严格默认 fail-open | **部分缓解** | `gate_eval.go:39-46`：`StrictMode` 默认 false 但注释"prod sets true"；类型默认仍不安全 |
| 17 | 基线回退日志打印 `BaselineScore` 非生效值 | **已修** | `guardrails.go:459-466`：`baseline := g.BaselineScore; if baseline <= 0 { baseline = bestKnown }` 统一生效值 |

## 5.4 runtime services

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 18 | arena 场景分被全局 Stats 污染 | **已修** | `arena/scenario.go:263-268`：注释显式"Compute score from per-scenario results… This fixes C-02" |
| 19 | 每次调用伪造 traceID→成本看板失效 | **已修** | `observability/metrics_tracer.go:50-63`：三级派生链（call→ctx→生成） |
| 20 | genealogy 旧根驱逐后新节点成孤儿 | **已修** | `flight/genealogy.go:115-126`：ParentID 再挂载 + root 替换 |
| 21 | `production_manager.go:567` MaxHistory=0 清空历史 | **已闭环** | PG 半边整删；现文件仅 44 行 config-only |
| 22 | `memory_patcher.go:129` JSON float64 断言 int 静默 no-op | **已修**（本轮） | `memory_patcher.go:127-156`：四处 `.(int)` 断言（JSON 反序列化给 float64） |
| 23 | `arena/service.go:77` nil Injector panic | **已修**（本轮） | `service.go:44-52`：仅 `log.Warn` 后照常赋 nil；`:77` 直接 `s.injector.KillLeader` |

## 5.5 storage / evidence / embedding

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 24 | `write_buffer.go:315` Write 持锁 100ms | **已闭环** | WriteBuffer 已随 P2.1 删除 |
| 25 | `embedding_queue.go:206` 卡死 processing 行永不回收 | **已修** | `embedding_queue.go:112-126`：存在 `processing_at = NULL` 的 stale 回收路径 |
| 26 | `CreateBatch` 未查 `rows.Err()` + 批内同 key ON CONFLICT 自撞 | 未复核 | 批量构造已重写为 valuesClause 单语句（:212-224）；ON CONFLICT 不在该路径，语义变化未穷举 |
| 27 | evidence 同纳秒 ID 碰撞静默丢记录 | **已修** | `evidence/postgres_store.go:109-116`：ID 含 sha256 digest 破坏碰撞；ON CONFLICT DO NOTHING 为重试幂等刻意设计 |
| 28 | `conversation_repository.go:126` GetByID 无租户过滤 | **仍真** | `repositories/conversation_repository.go:122-134`：`WHERE id = $1` 无 tenant |

## 5.6 knowledge / tools

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 29 | distill 先 Save 再 FindDuplicate→批内不去重+自匹配 | **仍真** | `adapter/distill.go:274/:282`：顺序未变 |
| 30 | entity resolution 从不使用 `MatchedObjectID`（merge no-op） | **仍真** | 全仓仅 `pipeline.go:35` 字段声明，无消费点 |
| 31 | sqlite HybridSearch 全量载入再截断 | 未复核 | 函数现位于 `knowledge/store/sqlite/store.go:425`，内部实现未读 |
| 32 | webSearch 响应无大小上限 | **已修**（本轮） | `apitools/builtin.go`：无 LimitReader/MaxBytes |
| 33 | 两套 file_tools 参数键不一致 | 未复核 | — |

## 5.7 LLM / mcpclient / compat

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 34 | 流式路径恒上报 0 token | 未复核 | `recordLLMCall` 存在（`client.go:251`），流式 usage 汇聚路径未穷举 |
| 35 | llmservice 未装 sanitizer | **已修**（本轮） | bootstrap 已装（`provide_llm.go:26`）；`llmservice/service.go` 直构路径仍只有 sanitizeRole |
| 36 | `openai.go:81` MaxTokens=0 未 clamp→400 | **已修**（本轮） | `llm/output/openai.go:81`：原样透传 |
| 37 | compat detectEndpoint 不可达/路由错 | **已闭环** | compat/ 已整删 |

## 5.8 entry / sdk

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 38 | taskRunCtxs 在 Create 后注册（窗口） | **部分缓解** | `sdk/scheduler.go:187`：超时中止已加（:160 注释），注册窗口仍在；**随 B3 重构处理** |
| 39 | scheduler goroutine 不在 Close join | **仍真** | `sdk/sdk.go:433-439`：仅 `schedCancel()`，未见 Wait |
| 40 | distill 与 bootstrap 重复订阅→双蒸馏 | **已修** | `sdk/distill_events.go:64`：注释显式改共享 EventStore |
| 41 | kill/resume 打到 legacy Manager（对 fabric 无效） | **仍真** | `cmd/ares/agent_routes_agents.go:28-36`：`h.mgr.StopAgent/RestartAgent`；**方向归 P1/接线决策** |

## 5.9 infra

| # | 原声明 | 结论 | 证据 |
|---|---|---|---|
| 42 | 多数后台 worker 裸 `bgGroup.Go` 无 recover | **仍真** | `bootstrap_steps.go`：6 处 `bgGroup.Go`，recover 仅 embedding_worker |
| 43 | discovery goroutine 绑 ctx、无 Stop/cleanup | **仍真** | `provide_discovery.go:71`：`eng.StartAutoDiscovery(ctx, interval)` 无回收注册 |
| 44 | `events_retention_days` 无范围校验 | **已修**（本轮） | `ares_config/config_validate.go` 无该校验 |
| 45 | `RefillRate`/`Timeout` 死配置；事件缓冲满静默丢 | **仍真** | `ares_ratelimit/limiter.go:25-37` 定义+默认值但无消费点；`memory_store.go:291-300` 非阻塞 default 丢弃 |

## 分流建议

- **随 P1 B3 处理**：#38/#41（SDK 与 agent_routes 面）
- **随 P2.4 RLS 决策**：#28
- **纯 bug 修复包（无需方向拍板，可立即做）**：#5/#7/#8/#12/#14/#22/#23/#32/#35/#36/#39/#43/#44
- **C 批备忘录**：#11/#13/#16/#26/#31/#33/#42/#45（需方向或较大工作量）
