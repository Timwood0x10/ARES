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

## 相关：answer 三态与 success 判定（供 G1 定位）

| 答案形态 | 出处 | 当前 outcome | 应为（G1 建议） |
|---|---|---|---|
| pass-through（真完成） | `l2graph.go` answerCognition | success | success ✅ |
| synthesizer 合成 | `synthesizeAnswer`（`:622`） | success | 需判"合成对不对" |
| gap body（`unansweredBody`） | `:518` "no answer content supplied" | **success**（`COMPLETED`） | **应降为 degraded** |
| truncated（`answerTruncatedKey`） | `:512`（depth guard 打的） | success + `truncated=true` | **应降为 degraded** |

> 最后一列"应为"即 G1 的最小改动：**把 gap/truncated 答案在 observer 侧记成 degraded（不算 success），并加一个 accuracy 维度权重高于 cost/latency。**
