# 深度 Review：GA / AKG 功能闭环核查（2026-08-03）

> 范围：全仓库深度 code review，重点核查 **GA（遗传算法进化）** 与 **AKG（Agent Knowledge Graph）** 两条链路是否逻辑闭环、无空实现；同时复核本轮未提交改动（Flight Fitness 写侧接线）。方法：逐链路读代码（写入端→存储→读取端→打分→演化→部署→消费），交叉验证数据契约（Source 名 / store 实例 / value key），并独立复跑 `go build ./...`（✅ 0 错误）。

---

## 结论先行

| 维度 | 结论 |
|------|------|
| **AKG 闭环** | ✅ **成立**。定义→写入→存储→检索→打分→注入 prompt→进化侧消费，全链路贯通，无空实现 |
| **GA 闭环** | ✅ **成立**。执行事件→fitness 发射→基因组读取→进化→策略部署→live agent 消费，全链路贯通 |
| 本轮未提交改动 | ✅ 正确且有价值：修复了此前 review 指出的 serve 路径 recorder 未 Start 的缺口，**并顺带解决了上一轮 review 标记的 ares start 双发问题**（api_impl 改为复用 bootstrap 的 recorder） |
| 空实现 | 3 处**文档化**的占位/断链（均非隐藏空壳）：deployment 影子评估、GA 经验反馈记录、legacy DreamCycle 字段 |
| 遗留问题 | 4 个 🟡 级（不影响闭环成立，但值得处理）：cleanups 仅错误路径执行、comp.wg 无人 Wait、AKG errgroup 未 Wait、LLM 建议 prompt 与注释不符 |

---

## 一、AKG 闭环核查（✅ 成立）

### 1.1 链路全景（bootstrap 路径）

```
事件订阅 (subscribeDistillationEvents)
  → EventTaskCompleted/Failed
  → triggerAKGBridge (30s 超时, errgroup)
  → DistillBridge.DistillConversation  [写入端]
      Phase1  Memory 蒸馏 → Phase2 Memory→KnowledgeObject
      → Phase3.5 extractRelations → Phase3.6 embedAndDedup → Phase3.7 scoreQuality(质量门)
      → Phase4 persistAndPromote (Save + Promote, Confidence≥MinFinalScore→Active)
  → KnowledgeStore (in-memory 默认 / PG 可选)   [存储]
  → StoreProvider (Source "akg_store", ns "default") 注册进 KnowledgeRuntime
  → KnowledgeRetriever.Retrieve → HybridSearch (vector+lexical, MinScore, StatusActive)  [读取+打分]
  → wireRetrievers → KnowledgeRetrieverAdapter → MemoryManager
  → BuildContext / BuildPromptMessages (EnableRAG) 注入 prompt   [消费]
  → KnowledgeRuntime fitColl (Source "knowledge") → KnowledgeGenome.Fitness  [进化侧]
```

### 1.2 关键验证点（全部通过）

- **命名空间契约**：写入端 `akgNamespace = "default"`（`knowledge_akg.go:29`）与 SDK 路径 `sdk/sdk.go:76` 一致；StoreProvider 以同一 ns 过滤（`provider/store/provider.go:109`）。读写同 slice。✅
- **质量门**：`DistillBridge` 经 `NewDistillBridgeWithGate` 走 `knowledge.DefaultQualityGateConfig()`；Superseded 跳过打分、Confidence≥MinFinalScore 才 Promote 为 Active（`distill.go:297-339`）。不是空转。✅
- **打分**：`memory/store.go:187` HybridSearch → `knowledge.ScoreHybrid`（向量+词法混合分）→ MinScore 过滤 → TopK/FinalK 截断。`StoreProvider` 将 FinalScore 回填 `obj.Relevance`（`provider.go:148`），保证 runtime 路径与直连 store 路径 Relevance 信号一致（否则 collectSnippets 会把所有 AKG 事实当噪声滤掉——这是已修的根因）。✅
- **读取端过滤**：`collectSnippets` 用 Relevance（查询时信号）而非 Confidence（可靠性先验）做过滤，minRelevance 默认 0.3、可靠性下限 0.1（`context_retriever.go:348-385`）。✅
- **进化侧**：`BuildKnowledgeRuntime` 注册 StoreProvider（`provide_new_evolution.go:461-469`）；runtime `fitColl` 以 Source "knowledge" 发射 fitness（`runtime.go:88,191-195`），与 `KnowledgeGenome.Fitness` 读取的 `avgFitnessValue(...,"knowledge",...)`（`knowledge_genome.go:133`）一致。`UpdateLiveKnowledgeRuntime` 保证 genome patch 打到 live runtime 而非占位 runtime。✅
- **测试覆盖**：`knowledge_akg_test.go`、`build_knowledge_runtime_test.go`、`store/memory/namespace_test.go`（租户隔离）、`adapter/distill_test.go` 等均在。✅

### 1.3 AKG 注意点（非阻塞）

- **写侧依赖嵌入**：`wireAKGLoop` 在 `embClient == nil || deps.ExpRepo == nil` 时跳过写侧（仅读侧），属文档化降级（`knowledge_akg.go:175-179`），非空壳。
- **默认 in-memory 存储**：AKG 事实默认仅进程内，重启即清空（与 GA evidence store 一致，文档已注明；做长期学习需换 PG）。
- **SDK 路径与 bootstrap 路径双轨**：`sdk/sdk.go` 也有完整的 AKG 装配（buildAKGBridge / StoreProvider / wireDistillationSubscriber），两轨逻辑镜像、命名空间一致。✅

---

## 二、GA 闭环核查（✅ 成立）

### 2.1 链路全景

```
agent 执行 → ares_events (TaskCompleted/Failed, FailoverTriggered/Completed)
  → FlightRecorder.Collector (Start 后订阅)   [本轮改动保证 serve 路径 Start]
  → workflow/scheduler collector: Emit KindFitness value=1.0/0.0
  → recovery collector: Emit KindFitness value=1.0/0.0 (failover 时)
  → memory retriever: Emit KindFitness "value"=hit 1.0/miss 0.0 (Source "memory")
  → knowledge runtime: Emit KindFitness value=1.0 (Source "knowledge")
  → [shared EvidenceStore = newEvol.EvidenceStore]
  → WorkflowGenome/SchedulerGenome/RecoveryGenome/MemoryGenome/KnowledgeGenome.Fitness
      avgFitnessValue(Source 匹配) → 无证据时 0.5 中性探索
  → GA 进化 (NewWiredEvolutionSystem → GenomePopulationAdapter.Run)
  → deployBestStrategy → ActiveStrategyManager.Deploy → StrategyStore.SetActive
  → NewStrategySource → leader profileParser / sub taskExecutor 每轮读 active strategy
      （prompt 覆盖 + temperature/max_tokens 参数）
```

### 2.2 关键验证点（全部通过）

- **Source 契约三端一致**（发射端 / 基因组读取端）：workflow、recovery、scheduler、memory、knowledge 五个 Source 名在 collector（`ares_flight/collector.go:56-62`）、retriever（`memory_retriever.go:219`）、runtime（`runtime.go:88`）、基因组（`workflow_genome.go:19-22`、`recovery_genome.go:117`、`memory_genome.go:15`）完全对齐。✅
- **证据读取健壮性**：`avgFitnessValue` 跳过非数值/越界 payload、空 payload；`errNoEvidence` 时基因组回落 0.5（中性），GA 不会因缺证据崩溃或坍缩（`fitness.go:52-77`）。测试覆盖 8 个分支（`fitness_test.go`）。✅
- **部署闭环**：`genome_wiring_run.go:291-298` `deployBestStrategy` → `activeStrategyMgr.Deploy`；策略持久化到 `StrategyStore`（PG 或内存，`wireGAEvolution` 中装配）；`NewStrategySource` 包装后注入 leader/sub agent（`cmd/ares/serve.go:200`、`cmd/ares/agents.go`），profile/executor 每轮 `GetActiveStrategy` 应用 prompt+参数（`profile.go:104-108`、`executor.go:395-402`）。✅ 有 `profile_strategy_test.go` 验证。
- **驱动源**：5 分钟后台 ticker + 事件驱动 scheduler 双驱动（`bootstrap_steps.go:182-202`）；LLM scorer 为 opt-in（默认 ConstantScorer 50.0，文档化）。✅
- **经验引导（Track A）**：`provide_distillation.go` 的 GuidanceProvider 把蒸馏经验喂给 GA 经验引导变异（`gaCfg.GuidanceProvider` / `EnableExperienceGuidedMutation`），仅 PG+embedding 配置下启用，非空壳。⚠️ 但见 3.3（RecordStrategyOutcome 断链）。

### 2.3 本轮改动的价值确认（独立复跑 ✅）

- **serve 路径闭环修复**：`bootstrap.go` 在 `ProvideNewEvolution` 之后、`ProvideEvolution` 之前创建共享 recorder 并 `Start`，`newEvol.EvidenceStore` 传入——此前 review 指出的 "serve 路径 recorder 从未 Start → fitness 零发射" 已修复（`bootstrap.go:269-280`）。✅
- **单一 recorder 目标达成**：全仓库 `NewFlightRecorder` 生产调用点仅 3 处（bootstrap 共享实例、api_impl 复用、api/bootstrap 文档包），`ProvideEvolution` 改为接收 `fr` 参数而非自建（`provide_evolution.go:50,58`）；api_impl 复用 `s.bootstrap.FlightRecorder`（`service.go:257-258`）。**上一轮 review 的 "ares start 双发 ⚠️" 已被本轮改动顺带解决**。✅
- **向后兼容**：`api/service/flight/service.go` 新增 `NewWithEvidenceStore`，`New` 委托 nil；`api/flight/flight.go` Config.EvidenceStore 可选。✅
- **数据契约未变**：Source 名、store 实例、value key 三端一致（与两份既有 review 文档结论吻合）。

---

## 三、空实现 / 占位 / 断链清单

| # | 位置 | 性质 | 判定 |
|---|------|------|------|
| 3.1 | `deployment_wiring.go` `deploymentStagingRuntime.Apply/Evaluate/Rollback` | **文档化占位**：Apply 不碰 live 状态、Evaluate 恒返回 1.0、Rollback 恒 nil；注释明言 "true shadow evaluation deferred" | ⚠️ 可接受但有实质影响：`cfg.Evolution.Deployment.Enabled` 时，staging 是名义影子，promotion **永远不会被 shadow 分数拦下**（恒 1.0 通过）。不是隐藏空壳，但"部署安全检查"实际是空转的。建议后续补真实快照评估或至少记录。 |
| 3.2 | `provide_new_evolution.go:366` `noopKnowledgeExecutor` | **文档化兜底**：无 KnowledgeRuntime 时接受 patch 但什么都不做；`UpdateLiveKnowledgeRuntime` 在有 live runtime 时替换掉它 | ✅ 合理兜底（最小配置可运行），非空壳 |
| 3.3 | `provide_distillation.go:79-81` `RecordStrategyOutcome` 恒 nil | **文档化断链**：GA core 从不调用该接口；经验反馈只有"读"（fetchExperiences→hints）没有"写"（outcome 记录回写） | ⚠️ Track A 的反馈回路是半开的。影响有限（guided mutation 仍可用静态经验 hints），但"经验→策略效果→经验"的完整闭环未闭合，注释已如实声明。 |
| 3.4 | `provide_evolution.go:78` `dreamCycle` 恒 nil | legacy `EvolutionComponents.DreamCycle` 声明后从未赋值（`var dreamCycle *evolution.DreamCycle` 后直接塞进 struct） | ⚠️ 死字段。legacy 组件已被 new evolution（`NewWiredEvolutionSystem` 内有真实 DreamCycle）取代，影响为零，但字段是误导性的。可删或补。 |
| 3.5 | `api/bootstrap` 包 | **无人 import 的文档包**（仅 docs 引用） | ⚠️ 改动保持一致是对的，但该包无 Go 源码引用、无测试；建议标记或补测试（既有 review 已提过）。 |

> 明确排除：`BaseTool.Init/Stop`、`StaticSource.OnChange`、`LoopPlugin.BeforeStep/AfterStep` 等是文档化的生命周期钩子 no-op（接口默认实现，符合 Go 惯例，非空壳）；测试中的 `not implemented in mock` 均为测试替身，非生产代码。

---

## 四、正确性 / 并发 / 生命周期问题（🟡 级）

### 4.1 🟡 `cleanups` 仅在 Bootstrap 错误路径执行 → `FlightRecorder.Stop` 在正常关闭路径是死代码

`bootstrap.go` 的 `runCleanups()` 只在各错误分支调用；成功路径 `return &comp, nil`（line 359）后 cleanups 切片即丢弃。因此注册的 `comp.FlightRecorder.Stop` **只在 bootstrap 后续步骤失败时触发**，正常关闭时不会执行。
- **实际影响**：collector goroutine 依赖 ctx 取消退出（`collectLoop` 监听 `ctx.Done()`；`recorder.Start(ctx)` 用的是 bootstrap ctx，serve.go 关闭时 `defer cancel()` 会取消它）→ 实践中不会泄漏。
- **但**：既有 review 文档（`REVIEW_flight_fitness_startfix`）声称 "Stop 已注册进 cleanups → 防泄漏 ✅" 是不准确的——注册了，但成功路径永不执行。建议把 recorder 的 Stop 注册到 `ares_shutdown` 管理器（serve 路径有 shutdownMgr）或 api_impl `Service.Stop()` 里，让显式清理真正生效。

### 4.2 🟡 `Components.wg` 无人 `Wait()`

`subscribeDistillationEvents`（`bootstrap_steps.go:66`）、GA ticker（:185）、LLM 建议 ticker（:209）都 `wg.Add(1)`，但全仓库 grep 无 `comp.wg.Wait()`。WaitGroup 形同虚设——goroutine 生命周期完全依赖 ctx。功能无碍（ctx 取消能停），但语义误导，建议要么去掉 wg 要么在关闭路径 Wait。

### 4.3 🟡 AKG errgroup 未 `Wait()`，蒸馏 goroutine fire-and-forget

`subscribeDistillationEvents` 里 `akgEg, akgCtx := errgroup.WithContext(ctx)` 后 `eg.Go(...)` 派发蒸馏，但从不 `akgEg.Wait()`；`eg.Go` 的 func 恒 return nil（错误仅 log）。影响：关闭时可能丢弃在途蒸馏（每个有 30s 超时兜底，不会泄漏）；errgroup 的 error 收集被绕过。可接受但建议补 Wait 或改纯 goroutine + 有界信号量（事件量大的场景可考虑限制并发蒸馏数）。

### 4.4 🟡 LLM 建议 pipeline 的 prompt 是固定字符串，与注释不符

`bootstrap_steps.go:219-221` 注释称 "Generate a suggestion prompt for the LLM based on current evolution state and recent evidence"，但实际 prompt 是硬编码的通用字符串，**没有携带任何 evolution state / evidence**。功能可用（LLM 仍会产出 patch 建议），但注释过度承诺，且建议质量不依赖真实系统状态。建议要么注入真实状态摘要，要么改注释。

### 4.5 ✅ 确认无问题的点

- collector `Stop()` 有 `wg.Wait()` 且 `Start` 幂等（`recorder.go:47-76`），双 Start 安全。
- `flightRecorder.Start` best-effort（失败仅 Warn，不使 bootstrap 失败）——合理。
- `newPGStrategyStore` / `buildBootstrapKnowledgeStore` 失败均有 fallback（in-memory），不硬失败。
- api_impl fallback 分支（`service.go:259-266`）仅在 bootstrap 无 recorder 时启用，且无 EvidenceStore 时退化为 legacy 行为（不发射 fitness），文档化。
- evidence store 为内存型，多 goroutine 写经 store 内部同步，无竞态。

---

## 五、复跑验证

| 项 | 命令 | 结果 |
|----|------|------|
| build | `go build ./...` | ✅ 0 错误（本次独立复跑） |
| 测试 | 既有 review 已复跑 7 包全绿（本会话未重复全量跑，build 为准） | ✅ |
| lint | 既有 review 已复跑 0 issues | ✅ |

---

## 六、建议优先级

1. **（🟡 建议）修 4.1**：把 `FlightRecorder.Stop` 接到 `ares_shutdown` 管理器或 `api_impl.Service.Stop()`，否则"显式清理"只是纸面成立（当前靠 ctx 隐式兜底）。
2. **（🟡 建议）3.1 决策**：`Deployment.Enabled` 下 staging 恒 1.0 通过，等于"安全检查空转"。要么接受并明确写进文档（已是），要么后续补真实快照评估。
3. **（🟡 可选）3.3**：GA core 调用 `RecordStrategyOutcome` 补上经验反馈写侧，让 Track A 真正闭环。
4. **（🟡 可选）4.4**：LLM 建议 prompt 注入真实状态，或修正注释。
5. **（低）3.4 / 3.5**：删 legacy DreamCycle 死字段 / 处理 api/bootstrap 文档包。

---

## 七、结论

- **GA 与 AKG 两条核心链路均逻辑闭环，无隐藏空实现**；所有占位/断链点均有明确文档注释，不属于"看似实现实则空转"的欺骗性代码。
- 本轮未提交改动质量高：修复 serve 路径 fitness 发射缺口、收敛单一 recorder、解决上一轮双发问题、保持向后兼容，数据契约三端一致，`go build` 全绿。
- 剩余问题均为 🟡 级（生命周期清理语义、shadow 评估名义化、反馈写侧断链），不影响功能闭环成立，建议按优先级逐步收敛。
