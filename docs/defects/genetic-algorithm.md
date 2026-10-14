# 缺陷与边界记录 · GA 进化（genetic algorithm）

> 本文档**只记"会咬你"的坑与已知边界**，不重复"GA 怎么跑"（打分→选育→三道门→部署→回滚网）。
> 每条带源码锚点（v0.3.3 行号可能随 dev 分支漂移，定位以符号为准）。
> 性质标记：`缺陷`（建议修）/ `隐患`（规模/时间下暴露）/ `取舍`（设计权衡，知边界即可）/ `已做`（有保护，确认非缺陷）。
>
> **一句话总纲**：GA 的**骨架是扎实的**（真实成绩打分、三道安全门、回滚网、样本门槛），
> 但**进化方向缺一根"锚"**——**运行时** fitness 里没有"做对了没"这个正向信号
> （`DimensionEval` 通道存在、默认权重 0.25，但运行时 observer 不填充），
> 而"少做、早交差"在当前 outcome 语义下被算成成功。所以**开自动进化，方向可能越跑越"糊弄"**。

---

## G1 · fitness 没有"正确率"信号 → 往"糊弄"漂 〔缺陷 · 最致命〕

**fitness 的"行为方向"实际只有 outcome（成败）+ 快省，**运行时路径**没有 accuracy 信号**（`fitness_aggregator.go:29` `FitnessWeights`）：

| 维度 | 有？ | 说明 |
|---|---|---|
| `Outcome`（success/failure） | ✅ 默认 0.40 | 运行时唯一的行为方向项 |
| `DimensionEval`（维度评测证据） | ⚠️ **权重存在（默认 0.25），但运行时路径不产出** | `FitnessWeights.DimensionEval`（`:33`/`:57`）；由评测层 `dimension_judge.go` emit，需接 `DimensionJudgeBridge` 才有数据 |
| cost penalty（token） | ✅ | 乘进 success 分（`observer.go` `costPenalty`，`:96`） |
| latency penalty（耗时） | ✅ | 乘进 success 分（`observer.go` `latencyPenalty`，`:128`） |
| `Workflow` / `Scheduler` / `Recovery` | ✅ 默认非 0 | `DefaultFitnessWeights`（`:54`） |
| `Collaboration` / `ToolCall` | ⚠️ 默认 0（opt-in） | `DefaultFitnessWeights`（`:54`） |
| **runtime 路径里的 accuracy / "做对没"** | ❌ **没有** | 运行时 observer 自称 "the sole producer of runtime fitness samples"（`observer.go:422`），只写 outcome（+cost/latency 惩罚），**不写 `dimension_eval`** |

> **更正（v0.3.3 实测）**：`FitnessWeights` **并非"没有质量维度"**——它有一个
> `DimensionEval float64`，默认权重 **0.25**，可承载评测框架的 correctness/completeness。
> G1 的准确表述是：**"运行时 observe 路径不产生正确率信号（`dimension_eval` 通道存在但默认无人填充）"**，
> 而不是"结构里完全没有质量维度"。在默认未接评测桥的部署下，GA 确实只吃 success + 快省。

**实锤链路（"提前交差"被算成 success，且分还更高）**：

```
maxPlanDepth=10 撞墙 / L1 全拦
  → planner 强制出 answer（l2graph.go:512 "truncated" / grown==0 强制）
  → gap body（unansweredBody，l2graph.go:518；注释 "gap body rather than an error" 在 :560）
  → 任务仍 COMPLETED（不是 FAILED）
  → observer 从 terminal task.completed 读 Success=true（observer.go:326）
  → cost/latency 还低（没做多少事）→ success 分更高
```

**后果**：GA 选育会偏好"**少做、早收、gap 交差**"的策略——不是往"做得更对"漂，是往"**做得更少但看起来成功**"漂。这是经典 **reward hacking**：没有 oracle/golden 答案判"做对没"，fitness 只有"快省"信号，**越跑越聪明 ≈ 越跑越会糊弄**。

**修复（2026-10-13，P0-1(ii) 第一步：把 `dimension_eval` 通道真正打通）**

- **核实到的断裂是"两处字符串对不上"，而非缺机制**（机制齐全：判决器有四维 rubrics、桥能落 `KindDimensionEval`、
  aggregator 有权重 0.25）：
  1. `DimensionJudgeBridge` 写 `Source = "dimension_judge"`（按 `Evidence.Source` 契约=**verifier 名**），
     而 `fitness_aggregator` 的查询要的是 source `"dimension_eval"`（那是 **kind** 字符串）→ **全仓无任何生产者写这个
     source**，查询**永远 0 命中**，0.25 的权重**静默地什么都没加权**；
  2. 桥写出的 payload 是完整 `Evidence` 诊断结构，**没有 `"value"` 数值键**，而 aggregator 只从 payload 里
     `{"value": 0..1}` 取数 → 即便 source 对齐也读不到数。
- **修法（不另造路径）**：`Evidence` 增 `Value float64 json:"value"`（= 四维归一均值，与判官给门的分**同一条
  规则** `normalizedDimensionMean`，一处实现两处读，防漂移）；`DimensionJudgeSource` 导出；aggregator 改按
  **kind 过滤**（`Source: ""` = 不限来源）并说明为何 kind 才是契约。
- **顺带修掉一个"静默错误信号"**：payload 解析原用 `Value float64`，**缺失即 0.0 且落在 [0,1] 内被计入** →
  一条旧格式/无标量记录会把该来源的均值**拉向 0**（读起来像"全都做错了"）。改为 `*float64` + 必须存在。
- **接线**：`setupEvaluators` 开 `WithDimensionAveraging()` + `WithEvidenceStore(evStore)`（`ProvideEvolution`
  新增变参 `WithEvalEvidenceStore`，由 `comp.EvidenceStore` 传入）。**此前生产根本没接桥**，故通道恒空。
- **已记录的代价（非副作用）**：门的评分从"一个总评分"变为"四维 rubric 均值"。两者都归一到 [0,1]，故
  `min_score` 的**刻度不变**，但**测量的量变了** —— 对比历史门判定的人需要知道这点。
- **实机反证（§7.1）**：把 aggregator 的查询退回修复前的 `"dimension_eval"` →
  `TestAggregator_ReadsDimensionAccuracyFromTheBridge` 失败（"the accuracy signal … must be visible to the
  aggregator"）；恢复即通过。
- **测试**：`dimension_evidence_value_test.go`（payload 带 `value`；门分与 fitness 标量同源）、
  `fitness_aggregator_dimension_test.go`（**真生产者→真消费者**端到端 + 无标量必须被跳过）、
  `provide_evolution_eval_wiring_test.go`（生产装配落证据 + 无 store 时行为不变）。
- **仍未做（P0-1(ii) 的第二步，需拍板）**：**golden 判据**本身 —— ① oracle 形态（`ExpectedOutput` 精确/包含匹配
  vs rubric+LLM judge vs 分层；注：四维提示词**已含** `{{.ExpectedOutput}}`，即参照物已喂给判官）；
  ② golden 语料来源与规模（现仅 `test/eval/basic.yaml`、`tools.yaml` 两份小样例）；③ 触发方式（离线/定时/evolution 前）。
  **（更新 2026-10-13）**：**"gap 交差必须低分"这条已落地并真跑通过** ——
  `sdk/adversarial_e2e_test.go` 在 SDK 的 L2 core 上真跑会话（零 LLM），gap 用例被 floor 压到 0
  且聚合器窗口均值 ≤0+0.05，**对照用例 >0.8**（防全低假绿）；
  **truncated 用例已真跑通过（2026-10-13，`t.Skip` 已删除）**：`WithMaxPlanDepth(1)` 下会话真的迭代到
  增长上限（planner 打印 `max plan depth reached, depth=1 max=1`）→ 答案节点带 `truncated` → floor 压到
  **0.5**（中间档：高于 gap 的 0，低于真答案）→ 聚合器窗口均值落在该档。
  **当初 skip 的真因不是"到不了上限"，而是一个缺陷**：节点 arg 在 engine 里是**字符串化**存储的
  （`argsMetadata` 对每个值 `json.Marshal`），而读者用 `.(bool)` 断言 —— `truncated` 被每个撞墙会话写入、
  被**所有读取方**读丢，于是 0.5 这一档在生产侧根本打不出来（fitness observer 与 eval floor 双双失效）。
  已修（`answerArgTruncated` 按 wire 形状读取），并做了实机反证（停用该读取 → 单测与 e2e 同时失败）。
- **已接：信号进入"选育"而不只是"部署门"（2026-10-13，P0-1(ii) 的第二个缺口）**。此前 `RuntimeFitnessAggregator`
  只服务**晋升/回滚门**，而 GA 的选择打分器（`EvolveAfterScoring` 的 `buildRunScorer`）走的是判官/启发式/常量
  兜底 —— 于是"策略该不该部署"由生产证据决定，"策略该不该被选中繁殖"由判官对文本的印象决定，同一个策略
  两套结论。**修法（一个信号两个消费者）**：`genome_wiring_system` 把 lifecycle 用的**同一个**聚合器注入 adapter
  （`popAdapter.evidence = agg`，绝不建第二个），打分器按**收缩估计**混入实测 fitness：
  `score = (n·measuredLifted + k·judge) / (n + k)`，`n` = 窗口样本数、`k` = 先验（`defaultEvidencePriorSamples = 3`）。
  **无证据的策略分数与判官完全一致**（`n=0` 原样返回），证据稀少时判官占多数，所以"一次侥幸"不能推翻模型判断。
  测试：`genome_wiring_evidence_test.go`（公式表 + nil/无 id 回退 + **装配断言：选育读的必须就是回滚门读的那个聚合器**）。
- **更正（2026-10-14，量纲混用 —— 初版实现是错的，且错在"注释声明"而非代码注释缺失）**：初版把实测与判官
  直接相加，并在注释里**断言"两边都是 [0,1]"**。该断言是错的，钉死的量纲事实是：
  - 实测侧 = 聚合器 `Mean`，**[0,1]**（`fitness_aggregator.go` 字段 doc + clamp）；
  - 判官侧 = `genome.ScorerFunc`，**0–100**：生产装的是 `service.DeterministicScore`（base **50**、clamp **[5,100]**，
    LLM 提示词写 "Score this strategy on a scale of 0 to 100"；`bootstrap_evolution.go` 装配），无 LLM 时兜底
    `genome.ConstantScorer(50)`。
  **后果（实测）**：0.95（优）与 0.2（差）在 0–100 judge 前只差 ~1 分（都落在 ~13），实测信号被淹没；且**证据越多、
  分越低**（把 0-100 稀释向 0-1），选择方向反了——"让实测驱动选择"实际近似 no-op。
  **修法**：实测侧乘 `judgeScaleCeiling = 100` 抬到 judge 的量纲再混，**结果仍留在 judge 量纲**（`Strategy.Score`
  的所有下游消费者不变：阈值、patch fitness、coordinator）。同时更正 `scaleUpFitnessToCoordinator` 的 doc ——
  它此前把 `Strategy.Score` 也算进 [0,1] 那一侧，正是本 bug 的温床。
  **测试为什么全绿反而危险（已修）**：原公式用例用 `judgeOf(0.9)`（[0,1] 假 judge），只证明了"公式在假量纲下成立"，
  而生产是 0–100 —— 假夹具把 bug 遮住了。现在用例一律用**真实 0–100 judge**，并新增
  `TestRunScorer_MeasuredEvidenceIsNotDrowned` 锁两条性质："优/差必须可区分"、"证据越多必须越靠近实测"。
  实机反证：去掉量纲提升 → 0.95 实测仅 **23.21**、0.2 实测 **22.65**（正是"不可区分"），恢复即 93.75 vs 37.5。
- **跨量纲接缝扫描与加固（2026-10-14，承接上面的量纲 bug）**：
  1. **越界证据值不再哑静默**：聚合器一直**跳过** `value ∉ [0,1]` 的记录（无法使用），但跳过本身不可见 ——
     症状只是"这个源没有证据"，读起来像"什么都没跑"，而实际是"跑了但写错单位"（与 `dimension_eval`
     权重静默空转同一类失败模式）。现在 `WindowResult.OutOfRange` 报告被丢弃的条数（**无 value 的记录不计入**，
     那是设计上的诊断记录、不是单位错误），并对每个 (source, kind) **限频告警一次**（上限 32 个源 + 抑制通告，
     沿用 MCP 未匹配身份的同一手法）。这样"指标里少了样本"这件事由数字自己带出来，而不是靠人去猜。
  2. **夹具审计**（P0 的真教训是"夹具自造量纲"，所以专查一遍）：抽查了 promoter 的 `ScoreHistory` /
     `CalculateEvidenceScore`（由 [0,1] 的 `SuccessRate/ErrorRate` 导出）、`experience.Evidence`（[0,1]，含 `LatencyMs`）、
     聚合器窗口、eval 分数（`EvalScore.Score` [0,1]，配 `EvalMinScore 0.7`）、coordinator 阈值（0–100，且只有
     `scaleUpFitnessToCoordinator` 一个换算点）、记忆打分器（基础分 0–100 + bonus ≤20 + 惩罚 ×0.1）。
     **结论：除已修的选育混合外，没有第二处错量纲夹具。** 并新增一条守卫测试：混合结果**恒在 judge 量纲内**，
     且"无 scorer 时兜底 50（不是 0.5）"被写成契约 —— 这样未来改量纲会**测试失败**而不是静默错。
     **剩余（UI 级，未动）**：`evoapi` 把 `Strategy.Score`（0–100）直接作为 `Score` 暴露，而同一演化面另有
     [0,1] 的窗口均值；两者是不同量，建议展示层命名区分（如 `score` vs `fitness_mean`），否则运维跨面板比较会误判。
  3. **两处常量与单位统一**：① `RegressionTester` 的平局抖动改为**从 `judgeScaleCeiling` 推导**
     （`/5000` → 0.02，低于 judge 一个点），并补意图测试：**精确平局在多轮下必须给出"接近随机"的胜率**
     （否则等价策略会被读成"全票退步"），而**1 分之差不能被抖动翻盘**；原实现是裸常量 0.02，量纲一变就
     会静默变成 no-op 或决策者。② 记忆打分器的惩罚参数键改为**单位自明**：`cost_units` / `latency_seconds` /
     `regression_units`，**不再读裸 `cost`/`latency`** —— 后者是本包多目标**维度名**（`DefaultDimensionBounds` = {0,100}），
     同一个键一边是"归一化 0–100 维度值"、一边被当成"秒"，乘 0.1 权重后一个普通维度值会被罚掉约 9 分。
     仓库内无生产者写这两个键，故无需迁移；碰撞已用测试钉死（0–100 维度值不再被当秒罚）。
- **复核修复（2026-10-14 第二轮，针对上面的加固自审）**：
  1. **`OutOfRange` 的两处漏计（真）**：① 维度通道的累加写在 `if dimCount > 0` **里面** → **整通道越界**
     （正是本特性要抓的场景：`dimCount==0`）时计数为 0，而告警照样 fire → **数字与日志互相打脸**；
     ② 越界自增发生在**作用域过滤之前** → `WindowToolStepAt` 会把他人的坏值算进本工具步（计数总体 ≠ 可用总体）。
     两处都已修（累加移到分支外；过滤移到取值检查之前），并补两条分别针对"整通道越界"与"只算本窗口"的测试。
     **教训**：计数器必须覆盖"它存在的那个场景"，而不是"samples 恰好可用"的场景。
  2. **`tieBreakJitter` 的表述过强**：写成"随 judge range 自适应"会让人高估其强度 —— 实值是
     `JudgeScaleCeiling / 5000`，而那个常量**仍需人工维护**。改为"单一来源，非自适应"，并写明改量纲要
     同时改常量与混合处的提升。
  3. **惩罚参数改键属破坏性变更（包外）**：仓库内无生产者写旧键，但**包外**若写过 `cost`/`latency`/
     `regression`，惩罚会**静默归零**。已列入运维通告（见 00-INDEX）：包外生产者请改用
     `cost_units`/`latency_seconds`/`regression_units`；未改的旧键不会被读，也不会报错。
  4. **⭐ 顺带查出一处新的同类真接缝：`Strategy.Score` 被两套量纲写**。字段本身是 GA 的
     **0–100**（每条 scorer 都如此，且有数值比较：`population.go` 的 elitism/best-ever、
     `multi_objective.go` 的排序），但：
     - `cmd/ares` 的 `WriteActiveScore` **强制输入 [0,1]**（`score < 0 || score > 1` 直接报错）并原样写入 → 一个
       0.9 质量的策略在字段里看起来比刚被常量 scorer 打 50 分的变异体**差 100 倍**；
     - `dream_cycle_winner.go` 把**胜率**（[0,1]）直接赋给 `Score`（同一问题）。
     影响面：该字段会被 `evoapi` **公开给面板**（所以这是 UI 级接缝的根因），并参与按数值比较的决策。
     **修法（边界换算，一处一改）**：`WriteActiveScore` 在写入前 `score *= evolution.JudgeScaleCeiling`
     （输入契约仍是 [0,1]，字段得到 0–100）；`winnerToMutationStrategy` 同样把胜率提升到该量纲。
     **公开面同时加机器可读标注**：`evoapi.Strategy.ScoreMax`、`GenerationStats.score_max`（= `evolve.JudgeScaleCeiling`），
     并在 doc 里写明"这是 GA 分数，不是 [0,1] 的窗口均值；负值 = 未评估哨兵"。有测试锁：分数不被缩放、`score_max`
     出现在线形状、每个历史点都带标度、`JudgeScaleCeiling == 100` 是**要刻意改的契约**。
     **未做（如实）**：`evoapi.Strategy` 这个 DTO **没有 JSON tag**（线上字段名是 PascalCase，兄弟 DTO 是 snake_case）——
     统一字段名是破坏性 API 变更，**交给运维拍板**，本轮只做加法标注。

- **量级提醒（别过度解读本批）**：语料仍是两份小样例、判据是 `ExpectedOutput` 比对，所以这批交付的准确表述是
  "**正确率信号从 0 变成非 0（且链路可复核）**"，**不是**"GA 已经会做对了"。真正防糊弄取决于语料扩到能代表
  任务面之后。另：门的评分量纲不变（0..1）但**被测量的量变了**，这一点应随 commit 信息一起告知运维。
- **已缓解但未解决（2026-10-14 明确口径）**：确定性 floor（`gap` → 0、`truncated` → 0.5，见 P0-1(i)）加上
  答案溯源（运行时给出"这个 body 从哪来"，不是判官从文本猜）**实质缓解**了 G1 最担心的 reward hacking ——
  "少做还像成功"这类答案拿不到高分，且该信号现在同时进了晋升门与选育。但它**不替代正确率锚**：
  "答得对不对"仍需 golden 回放进 `DimensionEval`（下面"仍未做"那一项）。

- **注意**：这是**方向性缺陷**，不是"GA 会崩"。GA 能正常跑、能出策略、有安全门——只是**优化目标本身是错的**。
- **建议（上 GA 的硬前置）**：给 fitness 加"正确率信号"。最低成本做法是 **golden 任务回放**（对一批已知答案的任务跑新旧策略，用正确率作为**独立维度**进权重，且**必须高于 cost/latency 权重**）；进一步把 gap body / truncated 答案在 outcome 层面**降级为非 success**（`answerTruncatedKey=true` 或 `unansweredBody` 时，observer 记为 degraded 而非 success），让"早交差"拿不到高分。

---

## G2 · 三道门防"回归"，不防"整体往糊弄漂" 〔隐患 · 依赖 G1〕

G2（影子 A/B）的判定是"**新策略打赢 baseline ≥ 5%**"（`deployment.go` `Deploy`，staging 试跑 → `delta = shadow - baseline`）。它防的是"**比现在的差**"。

- **坑**：如果 **baseline 本身已经是"糊弄型"**（被 G1 漂过去的），影子 A/B 里"打赢 baseline"的也可能是**更糊弄**的——门只保证"不回归"，不保证"朝向做对"。
- **性质**：三道门（G1 护栏 / G2 影子 / G3 评测）**都建立在"score 方向对"的前提上**。G1 不修，门拦不住"整体漂"。
- **建议**：修 G1 后，三道门才真正有意义（否则是给错误目标装的安全带）。

---

## G3 · 冷启动与样本门槛（确认"已做"，非缺陷） 〔已做〕

以下是**好的保护**，列出来是为了别误判成"没做"：

| 保护 | 现状 | 锚点 |
|---|---|---|
| 最小任务数门槛 | `MinTasksBeforeEvolve`（不到不进化） | `dream_cycle.go:41/136/349`（**非** `dream_cycle_ga.go`） |
| 最小样本数（回滚看护网门槛） | `MinSamples` + `WithMinRollbackSamples`（属 `RollbackPolicy`，**非**影子门） | `genome_wiring_idle.go:28-29` |
| 影子门的样本门槛 | `ShadowEvalConfig.MinSamples` | `genome_wiring_idle.go:107-115` |
| 冷启动中性分 | `ColdStartScore = 0.5`（无证据时不给偏向） | `fitness_aggregator.go:94` |
| 确定性 scorer 告警 | 影子用确定性打分时**打 warn**（"comparisons are identical, MinSamples satisfied by repetition, not independent evidence"） | `genome_wiring_idle.go:107-108` |

**含义**：GA **不会**在冷启动拿 3 个样本乱进化，这块是稳的。缺陷只在 G1（方向）和 G2（门的盲区）。

---

## G4 · 生产未大规模验证 〔隐患 · 成熟度〕

- GA 进化（`GenomePopulationAdapter.Run`，生产实际驱动；`DreamCycle` 是同一引擎的轻量壳，**生产关着** `EnableDreamCycle=false`）。
- 框架自标 **dev、生态小、GA 未经大规模生产验证**。三道门 + 回滚网是"敢试"的基建，但**没有"试错在真实流量上收敛"的证据**。
- **建议**：上线先开**观测态**（记录 fitness/trajectory，不自动 promote），跑一段时间看方向（尤其配合 G1 修复后），再放开自动进化。

---

## G5 · 基因只改 prompt/params，不改代码 〔取舍〕

进化产物是 `Strategy`（`PromptTemplate` + LLM params，如 temperature）。**改的是"怎么说话"，不是"怎么做"（工具逻辑/图结构）**。

- **代价**：进化天花板被锁在"提示词 + 参数"层——它能优化"措辞/温度"，但**优化不了"该不该长这条边/该调哪个工具"的图结构**。
- **性质**：这是**有意为之**（安全、可回滚、不打代码），但要知道"GA 让 agent 变聪明"的"聪明"是有边界的。
- 图结构层的"进化"走的是另一条路（`UpdateLiveDAG` 的结构补丁 + DeploymentPipeline），**不在这套 GA 基因里**。

---

## G6 · 进化改 L1，"在跑的老任务"无感 〔已做/确认边界〕

GA 出的补丁改的是 **L1（能力/策略层）**，不影响**正在跑的 L2 会话**（旧会话的 tool 节点已编译成 fabric task，patch 只管"下次"）。上线 30s 后 `MonitorAndRollback` 盯着，回归了自动撤销。

- **含义**："GA 会不会把在跑的量子搞挂"——**不会**。最坏是"新会话用了新策略效果变差"→ 监控发现 → 回滚。这条是**对的**，不用修。
- **唯一衔接**：新策略要"生效"，得等下一个会话/下一个 plan 量子（`assembleAnswerMessages` 在量子开头组好 context 就不再变）。

---

## 性质小结

| 项 | 性质 | 优先级 |
|---|---|---|
| **G1 无正确率信号 → 糊弄漂** | 缺陷 | **最高**（GA 入生产的硬前置） |
| G2 三道门盲区（依赖 G1） | 隐患 | 高（G1 修了才有意义） |
| G3 样本门槛/冷启动中性 | 已做 | 无需动 |
| G4 生产未大规模验证 | 隐患 | 中（先观测态） |
| G5 基因不改代码 | 取舍 | 知边界 |
| G6 改 L1 不在跑任务无感 | 已做 | 无需动 |

**GA 这块"创新可行性强"，但 G1 是方向性死结**：
**不上"做对了没"的信号，GA 越跑越好用是伪命题——它只会越跑越会省、越快，但不会越做越对。**

---

## G7 · 澄清：空闲 GA 为什么不需要 LLM 参与（热/冷路径解耦）〔说明 · 非缺陷〕

GA 循环（`GenomePopulationAdapter.Run`）本身**零 LLM 调用**，常被误以为"进化要靠 LLM 评测"：

- **打分**：`ReplayScorer.Score`（`replay_scorer.go:219`）只是查 evidence store 里
  该 `StrategyID` 的历史 fitness 均值（纯 DB 读）；无记录返回 `ColdStartScore=0.5`。
  默认配置（`evolution.llm_scoring` 关）scorer 全是数值路径，无 LLM。
- **选育**：`pop.EvolveAfterScoring`（`genome_wiring_run.go:76`）按分排序 → 淘汰 →
  `PromptTemplate` 字符串交叉 + `Params` 浮点变异，纯拼接/算术。
- **数据从哪来**：任务**执行时**（热路径 planner 量子）LLM 产生成绩，内核在
  `kernel/scheduler_quantum.go:258` 内联写 `ExecutionAttribution{StrategyID, Success,
  TokenCost, Latency}` 进事件总线 → observer 转成 `KindFitness` 证据入库。
  GA 是**事后消费**这些已存在的记录，不重跑 LLM 评测。
- **唯一可选的 LLM 介入**：`evolution.llm_scoring=true` 时 `TieredScorer` 挂 LLM Judge
  （`genome_wiring_idle.go:155`），受 `MaxLLMCallsPerGeneration` 预算限制，超预算降级 heuristic。

> 已同步写入 `docs/system-internals.zh.md` §14.1 / `.en.md` §14.1。

---

## 相关：answer 三态与 success 判定（供 G1 定位）

| 答案形态 | 出处 | 当前 outcome | 应为（G1 建议） |
|---|---|---|---|
| pass-through（真完成） | `l2graph.go` answerCognition | success | success ✅ |
| synthesizer 合成 | `synthesizeAnswer`（`:622`） | success | 需判"合成对不对" |
| gap body（`unansweredBody`） | `:518` "no answer content supplied" | **success**（`COMPLETED`） | **应降为 degraded** |
| truncated（`answerTruncatedKey`） | `:512`（depth guard 打的） | success + `truncated=true` | **应降为 degraded** |

> 最后一列"应为"即 G1 的最小改动：**把 gap/truncated 答案在 observer 侧记成 degraded（不算 success），并加一个 accuracy 维度权重高于 cost/latency。**
