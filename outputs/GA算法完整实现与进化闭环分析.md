# ARES GA 进化引擎：完整实现与模块协作分析

> 扫描范围：`internal/ares_evolution`、`internal/evolution`、`internal/evidence`、`internal/ares_runtime`、`internal/ares_bootstrap`、`api/bootstrap`、`sdk`、`cmd`
> 结论均附 `file:line` 证据。只读分析，未修改任何源码。

---

## 一、总览：两套 GA 并存，两条进化闭环

| | 策略型 GA（运行时） | patch 型 GA（legacy） |
|---|---|---|
| 位置 | `internal/ares_evolution` | `internal/evolution` |
| 进化对象 | Agent 执行策略（params / prompt / tools） | 系统配置（DAG / 调度器 / 恢复策略 / 知识 / 记忆参数） |
| 基因表示 | `mutation.Strategy` | 6 种 `genome`（workflow/scheduler/recovery/knowledge/memory/prompt） |
| 适应度 | arena 回归测试 winRate（+ 三层评分器） | Evidence 总线实测值均值 |
| 部署 | `deployWinner → StrategyStore → agent executor` | `Coordinator → patchReg → live DAG executors` |
| 驱动 | DreamCycle（事件/定时器） | Coordinator（各 Source Submit 后 Evaluate） |

两条闭环都落地到 live 组件，非空转：

- **闭环 A（策略）**：GA 选出 best → `deployWinner` → `ActiveStrategyManager.Deploy` → `StrategyStore.SetActive` → serve 注入 agent → executor 运行时真实读取
- **闭环 B（配置）**：genome 变异 → diff → patch → `coordinator.Evaluate` → patch 应用到真实 live DAG
- 共用 **Evidence 总线** 作为适应度输入与运行时反馈通道。

---

## 二、策略型 GA 完整实现（internal/ares_evolution）

### 2.1 基因表示

`mutation.Strategy`（`mutation/types.go:82-145`）：

```go
type Strategy struct {
    ID, ParentID        string            // 个体标识 + 血统
    EvidenceKey         string            // 表型稳定键（prompt + 归一化参数），供按表型查证据
    Version             int
    Params              map[string]any    // 可变参数（temperature/top_k/tools 等）
    PromptTemplate      string            // 行为提示模板
    StrategyMutationType MutationType     // parameter/prompt/tool/crossover/root
    Score               float64           // 规范适应度（-1 = 未评估）
    SelectionScore      float64           // 选择用分数（可被 fitness sharing 调整）
    DimensionScores     map[string]float64 // 多目标模式下的分维分数
    GenerationCreated   int               // 进入种群的代数（供 AgentMaxAge 淘汰）
}
```

变异类型 `MutationType`（`types.go:16-36`）：`MutationParameter / MutationPrompt / MutationTool / MutationCrossover / MutationRoot`。

### 2.2 种群与初始化

`genome/population.go:28-84`：`Population{Agents, Size, Generation, cfg, rng, bestScore, bestEver, paretoFront, stagnantGens, currentMutationRate, recoveryActions, history}`。

初始化 `NewPopulation`（:101-151）→ `initializeFromBase`（:155-177）：克隆 base 为根个体（`MutationRoot`），再用 mutator 变异出 `Size-1` 个变体填充种群。

### 2.3 进化主循环

`runGAEvolution`（`dream_cycle_ga.go:40-117`）：

1. **终止检查**：`MaxGenerations` 或 `TargetFitness`（`BestEverScore() >= TargetFitness`）达到即停止（:52-63）
2. **评分**：`buildGAScorer` → `population.ScoreAgents(scorer)`（:66-67）
3. **进化一代**：`Evolve`（全量替换）或 `EvolveSteadyState`（仅替换 replaceRate 比例，稳态在线学习，population.go:222-245）（:71-79）
4. **取最优**：`population.BestStrategy()`（跨代数 best-ever，:82）
5. **部署**：`deployWinner(ctx, cycleCtx, data, winner, parent)`（:116），并记录血统 `genealogy.Record`（:97-109）

### 2.4 一代内的完整流程：doEvolve

`doEvolve`（`population.go:258-428`）步骤链：

1. 校验（mutator/crosser 非空、种群非空、**拒绝从未评估种群选父母** `ensureEvaluatedBeforeSelection` :274）
2. `SortByScore` 降序排序（未评估的排最后，selection.go:231-255）
3. **按 AgentMaxAge 淘汰**（:287-299）：age > AgentMaxAge 移除，根策略（`MutationRoot`）与 `GenerationCreated==0`（legacy）豁免
4. **幸存者**：`survivorCount = len * SurvivalRate`（:301-303）
5. **精英保留** `preserveElites`（:306）
6. **Prompt 多样性保护** `preservePromptDiversityLocked`（:309）：全精英用同一 prompt 模板时补充异质个体
7. **生成子代** `generateOffspring`（:345，见 2.5）
8. **组装下一代** + 填充到 Size + `Generation++`（:350-364）
9. **best-ever 更新**（:367）
10. **适应度共享** `applyFitnessSharing`（:372）：惩罚参数空间拥挤区域，精英豁免
11. **三类恢复机制**（:374-412）：
    - `adjustMutationRateLocked` — 变异率自适应
    - `handleStagnationLocked` — 停滞重置（注入强扰动克隆）
    - `injectFreshMutantsLocked` — 多样性崩溃（`Overall < DiversityThreshold` 或 `DominantLineageShare > 0.6`）时注入新鲜突变

### 2.5 子代生成：generateOffspring

`generateOffspring`（`population.go:449-528`）：循环生成至目标数——

- 选择父母：配置了选择器则 `sel.Select(pool, 2)`；否则随机取 2 个（向后兼容）
- `crosser.Crossover(parentA, parentB)` 得子代
- 概率 `rng.Float64() < currentMutationRate` 时 `mutator.Mutate(child, 1)` 变异
- 记录 `GenerationCreated = Generation+1`

### 2.6 选择算子（7 种）

`Selection` 接口（`selection.go:14-34`），工厂 `buildSelector`（`population.go:532-556`）：

| 策略 | 说明 |
|---|---|
| `tournament`（**默认**） | 每次随机抽 k=3 个取最高分（selection.go:159-222），k 越大选择压力越大 |
| `rank` | 按排名分配概率 |
| `sus` | 随机通用采样（随机数等比选择） |
| `roulette` | 轮盘赌（按分数占比） |
| `truncation` | 截断选择 |
| `lineage_rank` | 血统惩罚（避免单一血统垄断） |
| `nsga2` / `nondominated` | NSGA-II 非支配排序（多目标） |

### 2.7 交叉算子

`genome/crossover.go`：

- **参数重组**（:37-49）：`CrossoverUniform`（逐参数 50% 随机取父）/ `CrossoverTwoPoint`（双切点交换中段）/ `CrossoverSegment`（B 的连续块 + A 其余）
- **Prompt 继承**（:24-35）：`PromptInherit`（继承高分父）/ `PromptHalfSplit`（前半 A 后半 B）/ `PromptUniform`（随机，促进多样性）
- 默认 uniform + inherit。

### 2.8 变异算子

- **基础变异**：`mutation/mutator.go` 依参数池随机扰动（parameter/prompt/tool 三类）
- **经验引导变异** `ExperienceGuidedMutator`（`guided_mutator.go:170-176`）：取 hints → 置信度过滤（默认 ≥0.5）→ `mergeHints` 合并 → `chooseGuidedMutationType`（:403-480，用 prompt/tool boost 与失败模式惩罚调整概率）；无 hint 时回退 base mutator（:247-255）
- **LLM hint 生成**（`llm_hint_provider.go`）：基于 outcome 滑动窗口（MaxHistory=10）让 LLM 输出 JSON hints，失败优雅降级为空

### 2.9 自适应变异率与多样性

`adjustMutationRateLocked`（`adaptive.go:506-553`）：

- 多样性 < 紧急阈值 → 强制最大变异率（急诊模式）
- 多样性 < 阈值 → 提升 1.5–2.5x（按赤字比例）
- 多样性 > 3×阈值 → 温和衰减（×0.95）
- 下限保护：正常 ≥ 0.15，多样性过低时抬升下限
- 停滞处理 `handleStagnationLocked`（:558+）：超过 `MaxStagnantGenerations` 重置底部个体为强扰动克隆

**元进化** `MetaController.Tune`（`meta_evolution.go:102`）：依据多样性/改进/停滞信号自我调整 MutationRate、SelectionStrategy 等超参，`ApplyMetaToPopulation`（:280）落地到种群。

辅助设施：`spatial_index.go` 网格空间哈希加速最近邻（多样性/小生境度量）；`hypothesis.go` 把反思转可测变异假设；`reflection.go` LLM 分析进化历史产出 Pattern/Recommendation；`knowledge.go` KnowledgeBase 用 Laplace 平滑算置信度复用进化经验。

### 2.10 适应度评估（三层）

- **GA 路径**：`buildGAScorer`（`dream_cycle_ga.go:121-144`）→ `dc.tester.Run(RegressionConfig{Candidate, Baseline, ...})`（`regression_tester.go`）：多轮加 ±0.02 噪声扰动，算 winRate；已评估的直接返回 Score
- **分层评分器** `tiered_scorer.go:30-37`：Cache → Heuristic → LLM（预算允许才走 LLM，失败回退 heuristic）
- **记忆感知评分** `memory_aware_scorer.go:139-165`：`FinalScore = Quality + MemoryBonus − Cost − Latency − Regression`（MemoryWeight 默认 0.2）
- **多目标**：`ScoreAgentsMulti`（population.go:657-682）写 `DimensionScores`，聚合为 Score，NSGA-II 维护 Pareto 前沿
- **缓存与预算**：`cached_scorer.go` + `budget.go`（MaxLLMCalls CAS 计数）

---

## 三、patch 型 GA（legacy，internal/evolution）

### 3.1 基因：6 种 genome

`genome.go:33-44` 定义接口：`Name() / Mutate(ctx,n) / Snapshot(ctx)`；可选 `FitnessGenome`（返回 [0,1]）、`CrossoverGenome`。具体 genome：

| Genome | 基因内容 | 变异算子 |
|---|---|---|
| Workflow（workflow_genome.go:78-81） | `MutableDAG` + 配置（MaxNodes=20, InsertionRate=0.3, PruneRate=0.2） | 8 个算子：插/删节点、加/删边等（:191-379） |
| Scheduler（scheduler_genome.go:28-32） | `Scheduler` + 候选池（Default/RoundRobin） | 随机换池（:134-140） |
| Recovery（recovery_genome.go:46-49） | `RecoveryPolicy`（Strategy/MaxAttempts/ReplacementAgent） | MaxAttempts ±1 限幅 1-5（:74-82） |
| Knowledge（knowledge_genome.go:57-60） | 检索管线（MaxResults/Reducer/Planner/Summarizer） | 4 参数扰动（MaxResults ±20 限 5-200） |
| Memory（memory_genome.go:54-56） | 记忆配置（MaxHistory=10/MaxSessions=100/MaxDistilledTasks=5000） | 4 参数扰动 |
| Prompt（prompt_genome.go:18-24） | 桥接旧 GA `mutation.Strategy` | 委托 `mutation.Mutator` |

交叉：各 genome 实现 `CrossoverGenome`，均按 0.5 概率逐字段/节点均匀重组。

### 3.2 选择策略

legacy 包无轮盘赌/锦标赛，采用**精英式**：循环层只取每个 genome 的最优子代（api/bootstrap/bootstrap.go:329-333），Coordinator 再按 fitness 阈值门控。

### 3.3 适应度：Evidence 驱动

`genome/fitness.go`：

- 证据契约 `fitnessEvidence{Value float64}`（[0,1]，:23-27）
- `avgFitnessValue`（:52-77）：按 `Source=genome名` 过滤 Query，`json.Unmarshal` 解出 Value 求均值，越界/空 payload 跳过；全空返回 `errNoEvidence`（:16）
- 各 genome Fitness 取窗口 0、限 100/50 条；**无证据回退中性 0.5**，保证 GA 持续探索
- 证据生产者：FlightRecorder 以 `{"value": 1.0/0.0}` 上报 workflow 任务成功率 / scheduler 调度结果 / recovery 恢复成功率（ares_flight/collector.go:169-202）

### 3.4 diff → patch

`diff/diff.go:33-40`：`Differ` 接口 + `DiffAll`（:103-117）按 genome 名分发。5 个 differ 生成节点/边、调度类型、知识预算、恢复策略、记忆参数变更。`RuntimePatch` 含 `Rollback *RuntimePatch`（patch/patch.go:76-84）。`Registry.Apply`（:251-299）：按 Target 找 executor（无则 fallback）、ID 幂等、失败自动执行 rollback；`ApplySet` 原子批处理失败逆序回滚。

### 3.5 Coordinator 决策

`coordinator/coordinator.go`：所有 Source（GA/Chaos/AKF/Human/LLM/K8s/Rule）汇入，**只做决策**（Apply/Reject/Delay/Drop）：

- `Evaluate`（:373-432）：清空 pending → 逐条 `decide` → 应用/重排/丢弃 → 记录 DecisionHistory（含 ApplyError）
- `decide`（:447-488）：
  - 全源速率限制 4 条/分钟（超出 → Delay，重试 3 次后 Drop，`maxProposalRetries=3`）
  - **SourceGA 按 fitness 门控**：`≥70 Apply`、`<30 Reject`、中间 Delay（默认策略 DefaultPolicy :135-144，阈值即 5 genome 成功率均值×100 的标定）
  - 非 GA 源按 Priority ≥ 8 自动应用
- 紧急路径 `ApplyEmergency`（:231-255）：chaos 关键故障 priority=10 绕过决策直接应用

### 3.6 金丝雀部署管线（可选）

`deployment/deployment.go`：默认关闭（:48-56）。`Deploy`（:161-233）：staging.Apply → staging.Evaluate 影子评分 → `shadowScore >= PromotionThreshold(0.05)` 才 live.Apply，否则 rollback/拒绝。Coordinator 经 `PatchDeployer` 接口接入（:202-208，:393-396）。

---

## 四、模块协作：进化闭环如何运转

### 4.1 Evidence 总线（internal/evidence）

5 种 Kind（evidence.go:26-32）：`ExecutionTrace`(Flight) / `Failure`(Chaos) / `Knowledge`(Memory Distillation) / `Insight`(AKF) / `Fitness`(GA 只读)。

- `Store` 接口：`Append / Query / Aggregate`（:74-85）
- 内存版 `MemoryStore`（:88-156）；PG 版 `PostgresStore`（postgres_store.go:20-59，表 `evidence_records`）
- **生产者接线**：
  - Flight：ares_flight/collector.go:53-62 建 4 个 collector，发 ExecutionTrace（:150）与 workflow/scheduler/recovery 的 Fitness（:178/:187/:201）
  - Memory：retriever_wiring.go:105 `NewCollector(evStore,"memory")` → Fitness（memory_retriever.go:217）+ Knowledge（production_manager_tasks.go:246）
  - AKF：knowledge/runtime/runtime.go:85-88，`"akf"`→Insight（:176）、`"knowledge"`→Fitness（:192）
  - GA：只读不写（无 KindFitness 生产者）

### 4.2 装配链（ares serve）

`internal/ares_bootstrap/provide_new_evolution.go:79` `ProvideNewEvolution(dag, rt, memoryStore, evStore)`：EvidenceStore（nil → 内存版）→ Genome Registry（各 genome 注入 evStore）→ Diff Registry（5 differ）→ Patch Registry（graph/recovery/knowledge/memory executors）→ Coordinator（:235）。

bootstrap 顺序（bootstrap.go）：DAG 构建 :277 → 内存 store :287 → KnowledgeRuntime :302 → PG evidence 选择 :305-330 → `wireNewEvolution` :332 → 共享 FlightRecorder :347-357 → legacy evolution :365 → deployment 接线 :392-400 → `RegisterAgentDAG("evolution", dag)` :406 → `wireGAEvolution` :413。

### 4.3 闭环 A：GA 策略 → agent 运行时（实证接通）

```
dream_cycle.deployWinner (dream_cycle.go:486-606)
  → post guardrail → shadow eval (shadow_evaluator.go:162-203, MinSamples=10, winRate≥0.55)
  → ActiveStrategyManager.Deploy (rollback_policy.go:330-349)
  → StrategyStore.SetActive (:344)   // PG 可选，否则内存
  → serve.go:238-241 NewStrategySource → createAgents 注入
  → executor.go:395-405 GetActiveStrategy(ctx) 真实读取（strategy_adapter.go:36-45）
```

失败回滚内存态；guardrail critical 自动回退。反馈经 `feedback_recorder.go`（成功/失败记录，熔断 3 次错误冷却 30s）。

### 4.4 闭环 B：GA patch → live DAG（实证接通）

```
coordinator.Evaluate (coordinator.go:373-432)
  → DecisionApply → d.Deploy (deployer 非空启用时) 或 patchReg.Apply (patch.go:251-299)
  → liveDAGPatchExecutor (serve.go:517-527, 注册为 fallback)
  → mgr.GetAgentDAG → 直接改真实 MutableDAG（Add/RemoveNode、Add/RemoveEdge、SchedulerType，serve.go:778-853）
```

`wireEvolutionLiveDAGs`（serve.go:496）：buildLeaderLiveDAG → `mgr.RegisterAgentDAG(leaderID, liveDAG)`（:334）→ 更新 WorkflowGenome.SetDAG（:539-547）+ `UpdateLiveKnowledgeRuntime`（:554）。

### 4.5 触发源（谁驱动进化）

| 触发 | 位置 | 节奏/条件 |
|---|---|---|
| 事件 | `scheduler.go:227-293` `OnAgentEnd` 注册于 EventAgentEnd（:303） | minInterval + 分数退化 15%（:127）+ 每 100 分周期（:135） |
| 定时器 | `bootstrap_steps.go:200` evoTicker / :221 LLM ticker | 5 分钟 GA / 15 分钟 LLM 建议 |
| 混沌 | `ares_arena/evolution_bridge.go:33` | 故障事件 Submit；priority≥9 走 ApplyEmergency（:52-67） |
| HTTP | `api/bootstrap/bootstrap.go:305` runEvolutionCycle | 手动触发：每 genome Mutate(3) 取最优子代 → Diff → Submit → Evaluate |

### 4.6 持久化现状

- **EvidenceStore**：仅 `Storage.Enabled && Host != ""` 时建 PG 版（bootstrap.go:308-330）；否则 nil → 内存版。→ **生产恒为内存版是当前唯一真实持久化缺口**：进程内 GA 反馈环实时；重启后 evidence 清零（champion 仅 PG 下留存），频繁重启时 GA 似空转（fitness 回基线直到证据重累积）。属设计取舍/待办，非代码缺陷。
- **StrategyStore**：`Storage.Enabled && Type==postgres && Host!=""` 时 `newPGStrategyStore`（bootstrap_steps.go:317-346，表 `evolution_strategies`）；否则内存。SDK 恒为 `memStrategyStore`（sdk.go:426-429）。

### 4.7 SDK 路径差异（已立项：plan/evidence_persistence_and_sdk_live_dag_plan.md）

`sdk.go:716 wireEvolutionHotUpdate(cfg, kw.rt, nil, nil)` → `ProvideNewEvolution(nil, knowRt, nil, nil)`（:565）：**dag=nil、memoryStore=nil、evStore=nil**，仅 knowledge executor 接 live KnowledgeRuntime；SDK 的 Manager 是 `memory.MemoryManager`，全仓 SDK 路径无 `RegisterAgentDAG`/`GetAgentDAG`，**无 live DAG 供给链**；且 SDK 内无任何 `Coordinator.Evaluate/Submit` 调用——进化补丁只具接线、无触发循环。即完整闭环仅 `ares serve` 路径成立。

---

## 五、关键结论

1. **两套 GA 互为补充**：策略型 GA 进化"行为策略"，patch 型 GA 进化"系统配置"，共用 Evidence 反馈与 Coordinator/StrategyStore 双出口。
2. **闭环均已实证落地**：闭环 A（StrategyStore → executor.GetActiveStrategy）与闭环 B（patchReg → liveDAGPatchExecutor → 真实 DAG）都作用于 live 组件，非空转。
3. **适应度是表现函数而非配置函数**：legacy fitness 从 evidence payload 解真实 Value（[0,1] 均值，5 genome 成功率），阈值 70/30 与其匹配（08-02 修复 commit a952206e）。
4. **进化全程带安全网**：shadow 评估、guardrails（未评估 >50% 停跑/基线回归 Critical）、rollback 策略、熔断、金丝雀部署可选。
5. **已知缺口**：EvidenceStore 生产恒为内存版（重启丢失，GA 似空转）；SDK/arena/CLI 路径仅部分接线（无 live DAG、无触发循环）——均已有立项文档。

---

## 附录：关键文件索引

| 关注点 | 文件 |
|---|---|
| 策略 GA 主循环 | internal/ares_evolution/dream_cycle_ga.go:40 |
| 种群进化 | internal/ares_evolution/genome/population.go:258 |
| 选择算子 | internal/ares_evolution/genome/selection.go:159 |
| 交叉 | internal/ares_evolution/genome/crossover.go:37 |
| 经验引导变异 | internal/ares_evolution/mutation/guided_mutator.go:170 |
| 自适应变异率 | internal/ares_evolution/genome/adaptive.go:506 |
| 三层评分 | internal/ares_evolution/scoring/tiered_scorer.go:30 |
| 影子评估/回滚 | internal/ares_evolution/shadow_evaluator.go:162 / rollback_policy.go:330 |
| legacy 决策器 | internal/evolution/coordinator/coordinator.go:373 |
| evidence fitness | internal/evolution/genome/fitness.go:52 |
| patch 注册与应用 | internal/evolution/patch/patch.go:251 |
| 装配入口 | internal/ares_bootstrap/provide_new_evolution.go:79 |
| live DAG 绑定 | internal/ares_bootstrap/serve.go:496 |
| 策略注入/读取 | serve.go:238-241 / agents/sub/executor.go:395-405 |
| Evidence 总线 | internal/evidence/evidence.go:26 |
| SDK 缺口 | sdk/sdk.go:565,716 |
