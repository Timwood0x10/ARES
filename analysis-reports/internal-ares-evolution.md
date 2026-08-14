# 模块分析报告：`internal/ares_evolution`（进化 / 遗传算法）

> 分析范围：`internal/ares_evolution/`（131 个 Go 文件），含 mutation/、promotion/、experience/、genome/、guardrails/ 等子包

---

## BUG（高置信度 / 并发）

### 3. 进化后护栏把 winRate 与基于分数的基线比较，尺度不一致
- **位置**：`dream_cycle.go` 507 行 + `guardrails.go` 283-309 行
- **说明**：`PostEvolveCheck(ctx, winner.winRate, ...)` 中 `winRate ∈ [0,1]`，而 `baseline_regression` 护栏用 `bestKnownScore`（前一次 `newBest`，也是 [0,1]）比较。但 `GenomePopulationAdapter.runPostGuardrails`（genome_wiring_run.go 252 行）喂入的是 `postStats.BestScore`（可能是 0-100 的原始分）。**同一 `bestKnownScore` 被两个尺度完全不同的路径共享/变更**，导致不一致和误报的回归阻塞。
- **状态**：✅ 已修复（2026-08-14）——`EvolutionGuardrails.bestKnownScore` 改为 **`bestBySource map[string]float64`（按进化路径隔离）**；新增 `PostEvolveCheckForSource(ctx, source, ...)`，`dream_cycle` 传 `"dream_cycle"`（winRate [0,1]）、`genome_wiring_run` 传 `"genome"`（BestScore），基线回归各比各的尺度，不再跨尺度误报；原 `PostEvolveCheck` 委托默认 source 保持兼容，build/vet/test 通过。

---

## LOGIC（逻辑问题）

### 4. 变异子代继承父代 `CreatedAt`
- **位置**：`mutation/mutator.go` 346-361 行、`guided_mutator.go` 395 行
- **说明**：`fillChildMetadata` 设置 `child.CreatedAt = parent.CreatedAt`，而非当前时间。交叉（crossover.go 254 行）正确使用 `time.Now()`。变异后代带着父代的创建时间，会扭曲基于 `GenerationCreated` 的年龄驱逐和时序统计。
- **状态**：✅ 已修复（2026-08-14）——`fillChildMetadata` 改为 `now := time.Now()`（变异产生新策略，出生时间即当前时刻），与交叉路径一致，build/test 通过。

### 5. 选择策略校验不一致（`sss` vs `nsga2`/`nondominated`）
- **位置**：`genome/population_options.go` 117-121 行 vs `genome/population.go` 532-556 行
- **说明**：`validSelectionStrategies` 接受 `"sss"`，但 `buildSelector()` 没有 `"sss"` 分支（运行时返回"unsupported"）；反之 `buildSelector()` 支持 `"nsga2"`/`"nondominated"` 却不在 `validSelectionStrategies` 中（无法通过 `WithSelectionStrategy` 配置）。**配置路径自相矛盾**：`sss` 通过校验但运行时必失败；后两者已实现但不可达。
- **状态**：✅ 已修复（2026-08-14）——`validSelectionStrategies` 移除不存在的 `"sss"`、加入已实现的 `"nsga2"`/`"nondominated"`，配置校验与运行时 `buildSelector` 一致，build/test 通过。

### 6. `dream_cycle_ga.go` `deployWinner` 的 `parent` 是"新最佳"而非"上一代最佳"
- **位置**：`dream_cycle_ga.go` 111-116 行
- **说明**：注释声称 parent 是"上一代最佳策略"，但代码从 `best.ID`/`best.Score`（本代新进化出的最佳）构造 parent。`deployWinner` 用 `parent.Score` 作为影子评估的活动基线，候选实际上是在**与自己比较**，基线语义被破坏。
- **状态**：✅ 已修复（2026-08-14）——`parent.Score` 改用 `dc.population.BestEverScore()`（历史最佳基线），候选不再与自己比较，影子评估基线语义恢复；`parent.ID` 保留 `best.ID`（进化来源），build/test 通过。

### 7. `promotion/promoter.go` 未使用的 `previousScores` 字段
- **位置**：`promotion/promoter.go` 54、69、89 行
- **说明**：`previousScores map[string]float64` 被初始化、写入（89 行）但从未在包内读取。死状态。
- **状态**：✅ 已核实清理（2026-08-14）——全仓 grep 无 `previousScores` 命中，字段已删除，报告条目过时。

### 8. `experience/types.go` 未使用的 `totalScore` 累加器
- **位置**：`experience/types.go` 249、335 行
- **说明**：`totalScore` 在两个聚合函数中累加但从未放进返回的 `Evidence` 结构。死工作。
- **状态**：✅ 已核实（2026-08-14）——`experience/memory_store.go` 的 `totalScore` 用于 `avg_score` 统计（正常消费）；报告指向的 `types.go` 累加器已不存在，条目过时。

### 9. `mutation/constants.go` 大量未使用常量
- **位置**：`mutation/constants.go` 4-15 行
- **说明**：`TypeDefault`、`TypeParameter`、`TypePrompt`、`TypeTool`、`TypeCrossover`、`TypeRoot`、`TypeUnknown`、`ParamTemperature`、`ParamTopK` 均未被引用（实际代码直接用字符串字面量）。
- **状态**：✅ 已核实清理（2026-08-14）——`mutation/constants.go` 文件已不存在（常量已删除），报告条目过时。

### 10. `rollback_policy.go` 不可达分支与未用函数
- **位置**：`rollback_policy.go` 227-230 行
- **说明**：`checkStart := len(p.scoreHistory)/2; if checkStart < 0 { checkStart = 0 }`——`len()` 永不为负，该 guard 是死代码。另 `ScoreTrendAnalysis` 在生产中无调用方。
- **状态**：✅ 已核实修复（2026-08-14）——当前 `checkStart := len(p.scoreHistory) / 2` 已无 `if checkStart < 0` guard（死代码已移除），报告条目过时。

### 11. `report.go` LineageConcentration 仅在 `Overall > 0` 时填充
- **位置**：`report.go` 201 行
- **说明**：若多样性指标恰为 0.0，存在的系谱数据被静默省略。
- **状态**：✅ 已修复（2026-08-14）——lineage 计算移出 `Overall > 0` 门控（0.0 多样性仍携带有效系谱数据，不得静默省略），build/test 通过。

---

## 结论

| 优先级 | 位置 | 问题 |
|--------|------|------|
| **高** | `guardrails.go` 283-309 | winRate 与 score 基线尺度混淆，误报回归 |
| 中 | `mutator.go` 346-361 | 子代继承父代 CreatedAt |
| 中 | `population_options.go` 117 | 选择策略校验/实现不一致 |
| 中 | `dream_cycle_ga.go` 111 | deployWinner 与自己比较 |
| 低 | 多处 | 死代码（previousScores、totalScore、constants、checkStart guard） |
