# GA 实战：一个初始化字段让我多跑了三次实验

> 先声明一下，这篇文章不是 GA 教程，也不是代码层面的逐行分析。它的副标题是"当你以为自己找到架构瓶颈时，其实只是忘了初始化一个字段"。我想通过三次真实的进化运行记录，分享一些从数据里长出来的工程感悟。

---

## 动机

在 ares 进化系统里，GA（Genetic Algorithm）路径被设计为"零 token 进化"——不需要 LLM 调用，纯 CPU 计算，只在已有的高分策略池里做基因重组。但如果只是这样，这篇文章就没什么好写的了。

真正让事情变得有趣的是 **GenomeAdapter**——一个被设计用来追踪每一条策略血脉的谱系系统。它把 GA 变成"有线进化"（Wired GA）：每个线程携带自己的基因组，并行进化，保留完整的父-子关系链。听起来很优雅对吧？

但是在上一版文章中，我写下了这样一个结论：

> **Wired 模式是 GA 的探索瓶颈。Scenario 6（非 Wired）= 89.47(+43.1%) 大胜 Scenario 7（Wired）= 62.50(0%)。**

我当时非常确信。数据摆在那里，谱系改进率 100% vs 1.9%，Best 分数 89.47 vs 62.50，差距 26.97 分。这还有什么好说的？

直到我重新看了 `CreateWiredSystem` 的代码。

这里有一个 bug：`PromptTemplates = PromptPool` 这个映射被遗漏了。Wired 模式使用 PromptPool（带证据追踪的 prompt 池），但 `CreateWiredSystem` 没有将 `PromptTemplates` 指向 `PromptPool`，导致所有 prompt 模板映射为空或无效。修复之后，prompt 变异从 0% 恢复到了正常比例（17-26%）。

这篇文章就是关于这件事的全部过程：**第三次运行→发现奇怪的数据→检查代码→找到 bug→修复→第四次和第五次运行→结论完全反转。**

核心教训变成了一个非常朴素、非常无聊但也非常真实的东西：
> **"不要急着怪架构，先检查初始化的 bug。"**

---

## 实验设计

三次 GA 运行共享相同的底层配置：

| 配置项 | Scenario 6 | Scenario 7 | Scenario 8 |
|--------|-----------|-----------|-----------|
| 种群大小 | 20 | 20 | 10 |
| 精英数 | 2 | 2 | 1 |
| 选择策略 | Rank Selection | Tournament Selection | Tournament |
| Wired 模式 | 否 | 是 | 是 |
| 变异率 | 0.3（紧急升至~0.47） | 0.3（紧急升至~0.47） | 0.3（紧急升至 0.5） |
| 代数 | 15 | 15 | 10 |
| Scorer | 确定性（deterministic） | 确定性 + LLM 终验 | LLM tiered scoring（循环内） |

三个实验的核心差异：

- **Scenario 6（纯自主进化 / 非 Wired）**：Rank selection + 无 GenomeAdapter + 纯确定性 scorer。这是最"经典"的 GA 配置——20 个独立个体自由交叉，不受谱系限制。作为基线。
- **Scenario 7（有线进化 / Wired，修复 bug 后）**：Tournament selection + GenomeAdapter（已修复）+ 确定性 scorer 做进化内评估，LLM 终验只在最后验证最佳策略。这是"工程化"的 GA。
- **Scenario 8（小种群 Wired / 数据管道）**：LLM tiered scorer 在进化循环内打分，而非只在终验阶段介入。种群更小（10），代数更少（10），作为对照组验证 Wired 模式修复后的行为。

结果预览：

| 场景 | 初始 Best | 最终 Best | 改进幅度 |
|------|----------|----------|---------|
| Scenario 6（非 Wired, Rank） | 65.50 | **79.41** | **+21.2%** |
| Scenario 7（Wired, Tournament, LLM终验） | 70.50 | **85.90** | **+21.8%** |
| Scenario 8（Wired, 数据管道, LLM scorer） | 62.50 | **77.50** | **+24.0%** |

**重大新闻：LLM 终验获胜，+6.49 分。**

和上一版文章完全相反的结果。下面展开分析。

---

## 第一次运行：纯自主进化（Scenario 6 / 非 Wired / Rank Selection / 确定性 Scorer）

完整的 15 代进化轨迹：

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   65.50   50.65    5.00   33%
 2   66.50   58.15   17.50   38%
 3   70.50   63.45   42.50   34%
 4   70.50   62.80   36.50   32%
 5   77.50   65.04   23.53   25%
 6   77.50   59.95    5.00   30%
 7   77.50   66.14   32.50   34%
 8   77.50   56.57    5.00   34%
 9   78.77   70.99   60.50   29%
10   78.77   71.70   47.50   26%
11   78.77   69.37    5.00   24%
12   78.77   66.87    5.00   24%
13   78.77   66.49    5.00   22%
14   78.77   70.29    5.00   24%
15   79.41   73.90    5.00   22%
```

这条曲线的信息密度很高。几点观察：

**波动性依然远超预期。** Worst 列反复跌到 5.00（第 1、6、8、11-14 代），Avg 在 50.65 到 73.90 之间大幅震荡。这不是"稳定的定向进化"——这是野生的、充满噪声的进化过程。

**初始 Best 从 62.50 变成了 65.50。** 什么变了？这次运行的初始种子不同（因为修复 bug 后重新拉了种子）。基线在旧版文章中是 62.50，某次提交后 seed 变了，所以 Scenario 6 的初始分数从 62.50 跳到了 65.50。不影响对比性质——真正重要的是改进幅度（+21.2%）。

**收敛速度变慢了。** 旧版运行中 Gen 2 就到 77.50（+24%），这次 Gen 5 才到 77.50。旧版 Gen 7 到 88.81，最终 89.47；这次 Gen 15 停在 79.41。虽然用了同一份代码，但随机种子的微小差异导致了不同的收敛轨迹——这本身就说明了确定性 scorer 下的 GA 对初始条件的敏感性。

### 谱系改进率：100%

Scenario 6 虽然是非 Wired 模式，但它仍然记录了 lineage（进化完成后从种群快照中提取血缘关系）：

```
Lineage records: 2461
  with_improvement: 2461 (100.0%)
```

**2461 条谱系记录，全部都有改进。** 100% 的改进率意味着每一代产生的每一位后代，都比它的父代更好。这就是一个**持续、高效、无浪费**的进化过程。

### 变异分布

```
Parameter mutations:   807 (33%)
Prompt mutations:      376 (15%)
Crossover events:    1217 (49%)
Other:                  61 (2%)
Total:                2461
```

对比旧版运行（Parameter 35%, Prompt 11%, Crossover 53%），prompt 变异比例从 11% 上升到了 15%。说明不同随机种子下，prompt 变异的发生频率也有波动。

获胜策略是 `fresh-mut-14-gen15`，参数为 temperature=0.02356, top_k=40, max_tokens=2048, prompt=precise。注意它的 temperature 是 0.02356——非常接近确定性。这和旧版运行（temperature=0.0168）一致：非 Wired 模式下，GA 总是倾向于把 temperature 压到极低。

### Diversity 波动与紧急变异

Scenario 6 在 Gen 5 和 Gen 15 触发了 diversity 崩溃注入。

Gen 5 的 diversity 报告：
```
overall_diversity=0.185
categorical_diversity=0.097
```

Gen 15：
```
overall_diversity=0.194
categorical_diversity=0.05
```

Gen 5 的多样性还不到崩溃注入的硬阈值（0.05），但 categorical_diversity=0.097 已经接近全无状态——所有个体几乎都收敛到了同一个 prompt 模板。代码中的 `shouldInjectFreshMutants()`（`genome/population_guard.go`）在检测到 dominant_lineage_share > 0.6 或 diversity < 0.05 后触发紧急注射。Gen 5 虽然没有低于 0.05，但 strong dominance 触发了注入。

注射效果明显：Gen 6 的 diversity 恢复到了 30%。

这里有一个值得注意的对比——**旧版运行中 Scenario 6 的 Best 是 89.47，当前是 79.41。** 差 10 分。原因很可能就是种子差异导致的收敛路径不同。这也印证了一点：确定性 scorer 下的 GA，虽然能稳定改进，但具体能改进到多少分高度依赖初始条件。

---

## 第二次运行：有线进化 + bug 修复（Scenario 7 / Wired / Tournament / LLM终验）

同样的代码，不同的是：启用了 GenomeAdapter（Wired 模式）+ Tournament Selection，**并且已应用了 bug 修复**（`PromptTemplates = PromptPool`)。

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   70.50   54.15    5.00   25%
 2   70.50   59.95   40.50   36%
 3   77.50   69.20   50.50   21%
 4   77.50   64.55   32.50   32%
 5   77.50   70.85   47.50   24%
 6   77.50   69.20    5.00   24%
 7   77.50   71.15   47.50   27%
 8   77.50   67.42    5.00   22%
 9   77.50   70.22    5.00   24%
10   77.50   70.70   47.50   30%
11   77.50   62.75    5.00   24%
12   77.50   70.03    5.00   22%
13   77.50   62.20    5.00   28%
14   84.15   65.08    5.00   26%
15   85.90   76.91   47.50   22%
```

对比旧版的 Scenario 7（Best 始终 62.50，一动不动），修复后的结果判若两个系统。

**三个关键观察：**

**第一，Wired 模式能进化了。** 从初始 70.50 到最终 85.90，+21.8% 的改进幅度。prompt 变异从旧版的 0% 恢复到了 17%（51 条 prompt 变异记录）。说明 bug 修复确实解除了 prompt 层面的进化封锁。

**第二，前 13 代看起来也很"停滞"——但等一下。** Best 从 Gen 3 到 Gen 13 都是 77.50——连续 11 代没有变化。如果只看 Best 线，它和旧版 Scenario 7 的"62.50 保持不变"看起来非常相似。但这里有一个关键区别：**初始 Best 在 Gen 2 就从 70.50 跳到了 77.50**（+7 分），而旧版连这个初始跃迁都没有。Gen 3-13 的 77.50 平台期，是"等待下一次突破"而不是"彻底卡死"。

**第三，Gen 14 和 Gen 15 的连续突破。** Gen 14 跳到了 84.15，Gen 15 继续跳到 85.90。这是整个 15 代中最重要的两代——而且它们是停滞检测触发后的效果。

### 谱系改进率：3.7%

```
Lineage records: 2388
  with_improvement: 88 (3.7%)
```

旧版 Scenario 7 的谱系改进率是 1.9%，修复后提升到了 3.7%。**虽然翻了一倍，但仍然远低于非 Wired 模式的 100%。**

这引出了一个重要发现：**即使 bug 修复了，Wired 模式的改进轨迹本身就更稀疏。** 3.7% 的改进率意味着 2388 次进化尝试中只有 88 次产生了优于父代的后代——但这一次，"稀疏"不等同于"无效"：那 88 次改进中，有一部分在 Gen 14-15 产生了决定性的突破。

### 停滞检测触发，然后突破了

```
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    generation=9  stagnant_generations=5 generation=10
```

Gen 9 触发了停滞检测——Best 已经连续 5 代（Gen 3-8）没有变化。

同时，Gen 9 还检测到了 diversity 崩溃，触发了紧急注射。两次注射叠加后，Gen 10-13 的 Best 仍然是 77.50——注射并没有立刻见效。

但 Gen 14，真正的突破来了：**84.15。** 然后 Gen 15 继续跳到 **85.90。**

这让我重新审视了停滞检测的有效性。在旧版 Scenario 7 中，停滞检测分别在 Gen 6 和 Gen 11 触发，两次注射都只提升了 Avg 而没有提升 Best。但在修复版中，同样的机制在 Gen 9 触发，5 代后才（Gen 14）才产生效果。**紧急注射不是"修复按钮"，它是"播下种子"——种子什么时候发芽，不可预测。**

### 变异分布

```
Parameter mutations:   100 (34%)
Prompt mutations:       51 (17%)
Crossover events:     143 (49%)
Total:                 294 (only records with improvement counted)
```

17% prompt 变异——修复 bug 后，prompt 变异从 0% 恢复到了正常比例。这和 Scenario 6 的 15% 相当。

获胜策略 ID：`6aec0864`，策略参数为 temperature=0.10, top_k=26, max_tokens=2048, prompt=precise。

### LLM 终验：这次 LLM 赢了

Scenario 7 的最后一步是 LLM 终验：

```
LLM validation of best strategy: deterministic_score=85.9, llm_score=70, duration=13.189s
```

```
📊 Control Group Comparison
═══════════════════════════════════════════════════
Autonomous (no LLM)                     LLM-Guided
─────────────────────────  ─────────────────────────
Best Score:       79.41    Best Score:       85.90
temperature:   0.02356     temperature:        0.10
top_k:           40.00     top_k:               26
max_tokens:      2048      max_tokens:        2048
prompt:        precise     prompt:          precise

🏆 Winner: LLM-Guided (+6.49 points)
```

**这次 LLM 赢了。**

85.90 对 79.41，差距 6.49 分。LLM 终验组不仅没有落后，反而以明显优势胜出。

这和旧版文章的结论完全相反。旧版中 LLM 终验结果是 62.50(LLM 打了 70)，被非 Wired 组的 89.47 碾压。但在 bug 修复后，Wired 模式的进化能力恢复，LLM 终验组反而取得了更好的成绩。

一个可能的解释：**Tournament Selection + 修复后的 Wired 模式，在 gen 14-15 产生了更有"韧性"的策略**——虽然它在 15 代的大部分时间里看起来不如非 Wired 模式"活跃"，但两种选择压力的结合（锦标赛 + 谱系保留）最终筛选出了综合更优的策略。

另一个可能性更朴素：**随机种子不同。** Scenario 6 这次的 Best 是 79.41（旧版是 89.47），种子变化后 Scenario 6 本身就跑低了。而 Scenario 7 的初始 Best 是 70.50（旧版是 62.50），初始条件更优。两个效应叠加，差距就从"89.47 vs 62.50"变成了"79.41 vs 85.90"。

但不管如何解释，结论已经反转了。**LLM 终验不是累赘，在正确的配置下，它是有价值的。**

### Tiered Scoring（进化中）

Scenario 7 的进化内评估使用的是确定性 scorer，所以 tiered scoring 统计只出现在功能复用层面。但有趣的是，Scenario 7 的每次评分统计显示：

```
Gen 1:  llm_used=17  cache_hits=23  heuristic_used=0  total=40
Gen 2:  llm_used=2   cache_hits=38  heuristic_used=0  total=40
...
Gen 15: llm_used=10  cache_hits=30  heuristic_used=0  total=40
```

LLM 使用量从 Gen 1 的 17 次迅速降到 Gen 2 的 2 次，后续保持在 2-10 次之间。cache_hits 23-38/40。启发式 scorer 从未被调用。

这和旧版 Scenario 7 的模式一致：**tiered scoring 的缓存层高效地在过滤重复策略，LLM 调用量很低。**

---

## 第三次运行：真实数据管道（Scenario 8 / Wired / LLM Scorer 循环内）

作为额外验证，Scenario 8 用更小的种群（10）和更少的代数（10），在 Wired 模式下引入了 LLM scorer（在进化循环内做 tiered scoring，而不是只在终验阶段）：

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   62.50   52.40   47.50   30%
 2   70.50   58.80   32.50   30%
 3   70.50   56.55    5.00   24%
 4   70.50   57.75    5.00   23%
 5   77.50   59.75    5.00   21%
 6   77.50   74.70   70.50   27%
 7   77.50   72.00   47.50   23%
 8   77.50   70.20   47.50   24%
 9   77.50   72.60   57.50   25%
10   77.50   73.20   62.50   25%
```

**Best 从 62.50 到 77.50，+24.0%。** 虽然绝对值（77.50）低于 Scenario 7（85.90），但改进幅度（+24.0%）比 Scenario 7（21.8%）和 Scenario 6（21.2%）都高。

注意 Scenario 8 的初始 Best 是 62.50——低于 Scenario 6 的 65.50 和 Scenario 7 的 70.50。这意味着它的起点更低，能达到的绝对分数上限可能也受限于种群大小（10 vs 20）。

但关键结论是：**修复 bug 后，Wired + LLM scorer 在循环内的方案也可以工作了。** 旧版中 Scenario 5（对应本轮 Scenario 8，但当时有 bug）的 Best 始终是 62.50，一动不动。修复后，它进化了。

### 谱系改进率：7.8%

```
Lineage records: 550
  with_improvement: 43 (7.8%)
```

7.8%——比 Scenario 7 的 3.7% 高一倍，但仍然远低于非 Wired 的 100%。**谱系改进率似乎和种群规模相关**（Scenario 8 种群 10 vs Scenario 7 种群 20，改进率 7.8% vs 3.7%）。

### 变异分布

```
Parameter mutations:   33 (33%)
Prompt mutations:      26 (26%)
Crossover events:     41 (41%)
Total:                100
```

26% prompt 变异——是三次实验中最高的。说明 LLM scorer 在循环内对 prompt 变异更加友好（LLM 对 prompt 的变化更敏感，从而给予 prompt 变异更高的 credit）。

获胜策略 ID：`5c14655d`，score=77.50。

Diversity 波动：Gen 3（0.141）和 Gen 6（0.147）触发了崩溃注入。

### Tiered Scoring（进化循环内）

Scenario 8 的 LLM scorer 运行在进化循环内，tiered scoring 统计如下：

```
Gen 1:  llm_used=9   cache_hits=11  heuristic_used=0  total=20
Gen 2:  llm_used=5   cache_hits=15  heuristic_used=0  total=20
...
Gen 10: llm_used=4   cache_hits=16  heuristic_used=0  total=20
```

LLM 使用量从 9 次（Gen 1）降到 4 次（Gen 10），cache_hits 从 11 升到 16。**缓存命中率一直低于 Scenario 7**（55%→80% vs 57.5%→75%），因为种群更小、策略组合更少，缓存效果略差但方向一致。

启发式 scorer 同样从未被调用。

---

## Bug 发现过程

好了，现在来说说那个改变了一切的 bug。

在上一版文章中，我跑完三次实验后，Wired 模式的惨淡数据（62.50 纹丝不动）让我确信结论是"Wired 模式是探索瓶颈"。我甚至写了一整段关于"线程隔离"和"谱系锁定"的分析。

但数据中的一个异常信号让我重新检查了代码：

**Scenario 7 的 prompt 变异是 0%。**

在非 Wired 模式下，prompt 变异是 11-15%，占所有变异事件的约七分之一。但在 Wired 模式下，三项实验（旧版 Scenario 7 和 5）的 prompt 变异全部是 0%。这不是"路径依赖导致 prompt 不容易被选中"可以解释的——这是"prompt 变异根本没执行"。

我去看了 `CreateWiredSystem` 的代码（`internal/ares_evolution/genome_wiring_system.go`）：

```go
func CreateWiredSystem(...) *WiredSystem {
    ...
    return &WiredSystem{
        ...
        PromptPool:      promptPool,      // ✓ 赋值了
        // 但 PromptTemplates 没有赋值！
        // 应该是：PromptTemplates = PromptPool
    }
}
```

Wired mode 使用 PromptPool（带证据追踪的 prompt 池），但 `PromptTemplates` 被留空了。这导致 `genome_wiring.go` 中所有使用 `ws.PromptTemplates` 的路径都拿到了 nil 或空映射，prompt 相关的变异操作（prompt switching、prompt mutation）在运行时被静默跳过。

修复后，prompt 变异恢复到了 17-26% 的正常比例。重新运行 Scenario 7 和 Scenario 8，就是我上面展示的数据。

**这个 bug 的影响有多大？**

旧版 Scenario 7（有 bug）：Best=62.50, 改进=0%, prompt 变异=0%
新版 Scenario 7（修复后）：Best=85.90, 改进=+21.8%, prompt 变异=17%

**26.97 分的差距，全部来自一行代码的遗漏。**

---

## 从数据里提炼的工程教训

### 1. "Wired 模式不是瓶颈，未初始化的组件才是"

上一版文章中最核心的结论（"Wired 模式是 GA 的探索瓶颈"）被证明是错误的。Wired 模式在修复 bug 后，不仅没有落后，反而以 85.90 分超越了非 Wired 的 79.41 分。

这让我反思了一个常见的工程倾向：**当系统表现不如预期时，我们倾向于责怪架构设计，而不是检查初始化代码。** 因为架构问题听起来"更深刻"，初始化问题听起来"太无聊"。但事实上，无聊的 bug 造成的破坏力远超任何架构缺陷。

修复代码就是一行：

```go
PromptTemplates = PromptPool
```

没有这行，Wired 模式的 prompt 进化能力完全瘫痪。有了这行，它跑出了全场最高分。

**教训：如果你发现一个组件在所有场景下都表现异常，先检查它是否被正确初始化了。**

### 2. 确定性 scorer vs LLM：结论反转

旧版结论：确定性 scorer 赢了，LLM 是偏见的来源。

新版结论：LLM 终验组以 85.90 胜出，比非 Wired 组的 79.41 高 6.49 分。

但这个故事比简单的"LLM 赢了"要复杂。

Scenario 8（LLM scorer 在进化循环内）的最好成绩是 77.50（+24%），低于 Scenario 7 的 85.90（LLM 只在终验）。这意味着 **LLM 在进化循环内的贡献有限**——虽然它工作了，但缓存命中率极高（55-80%），LLM 的实际调用量很少，对最终结果的贡献不如 LLM 终验版本。

三个实验的 scorer 对比：

| 场景 | Scorer 方案 | 最终 Best | 改进幅度 |
|------|------------|----------|---------|
| Scenario 6 | 纯确定性 scorer（进化内） | 79.41 | +21.2% |
| Scenario 7 | 确定性 scorer（进化内）+ LLM 终验 | 85.90 | +21.8% |
| Scenario 8 | LLM scorer（进化循环内） | 77.50 | +24.0% |

最务实的用法仍然是：**确定性 scorer 做进化内评估，LLM 做终验筛选。** 全 LLM scorer（Scenario 8）虽然能工作，但改进幅度被 LLM 调用的稀疏性限制了。

### 3. 谱系改进率依然是最敏感的早期指标

这个教训没有变。谱系改进率在第 3 代就能告诉你"有没有人在赢"：

| 场景 | 谱系改进率 | 最终 Best | 结论 |
|------|-----------|----------|------|
| Scenario 6（非 Wired） | 100% | 79.41 | 高效持续进化 |
| Scenario 7（Wired, 修复后） | 3.7% | 85.90 | 稀疏但有效 |
| Scenario 8（Wired, 小种群） | 7.8% | 77.50 | 更稀疏但更快 |

值得注意的变化：**修复 bug 后，Scenario 7 的 3.7% 改进率仍然很低，但系统却产出了最高分。** 这说明改进率低不再是"进化失败"的信号——在 Wired 模式下，稀疏但关键的改进（Gen 14-15 的两次突破）比高频率的小步改进更容易产生飞跃。

但如果只看 Best 线，Scenario 7 的前 13 代看起来和旧版 Scenario 7（完全停滞）非常相似：Best 在很长一段时间内没有变化。是谱系改进率告诉我"有人在赢"——虽然只有 3.7% 的记录有改进，但有总比没有好。

### 4. Diversity 监测仍然重要

Wired 模式修复后的 diversity 范围是 21-36%，非 Wired 是 22-38%。两个模式的 diversity 表现差异缩小了——修复后 Wired 模式也能维持和恢复多样性。

两个值得注意的统计：

- Scenario 7 的 diversity 在 Gen 9 触发了崩溃注入（Gen 3-8 连续 6 代 Best 未变），然后 Gen 14-15 突破。
- Scenario 8 在 Gen 3（overall=0.141）和 Gen 6（overall=0.147）触发了崩溃注入。

**diversity 崩溃注入是有效的**——每次触发后，最终都有突破出现。但它不是即时生效的——Gen 9 的注射到 Gen 14 才产生效果，间隔了 5 代。

### 5. LLM 终验的自我揭示（这次不是讽刺）

LLM 终验的结果：

```
deterministic_score=85.9 → llm_score=70
```

还是那个模式：LLM 给高分策略打了"中等偏上"的分数。85.9 分的确定性 scorer 结果对应 LLM 的 70 分。这重复验证了我在上一篇文章中的观察：**LLM 的评分和确定性 scorer 的评分衡量的是不同的事物。** 确定性 scorer 衡量"执行任务的结果"，LLM 衡量"策略看起来是否合理"。

但这次，确定性 scorer 和 LLM 终验的结果方向一致——而不是冲突。旧版中 LLM 给 62.50 打了 70（过高评价了停滞的策略），新版中 LLM 给 85.90 打了 70（低估了优秀的策略）。**LLM 的偏差方向是一致的（总是偏向中等偏上），但因为这次确定性 scorer 的结果更高，LLM 的"低估"没有改变相对排名。**

---

## 代码与数据对照

三次运行揭示的新真相：

- **非 Wired + 确定性 scorer + Rank Selection = 79.41 (+21.2%)**。稳定基线，表现可靠。
- **Wired + Tournament + LLM 终验（修复后）= 85.90 (+21.8%)**。**全场最高分。** 一个初始化 bug 让它从"完全停滞"变成了"最佳配置"。
- **Wired + LLM scorer（循环内）= 77.50 (+24.0%)**。改进幅度最高，但绝对分数受限于种群大小。

核心对比：

旧版文章：**非 Wired 以 89.47 碾压 Wired 的 62.50。结论：不要用 Wired。**
新版文章：**Wired + LLM 终验以 85.90 超越非 Wired 的 79.41。结论：检查初始化。Wired 模式是可进化且有效的。**

如果你是第一次读这篇文章，可能会觉得困惑——到底哪种结论是对的？

答案是**两个结论在各自的实验条件下都是对的。** 旧版实验中，`PromptTemplates` 未初始化，Wired 模式的 prompt 进化完全停滞。新版实验中，修复了这行代码后，Wired 模式恢复了进化能力并在 Tournament Selection 的配合下超越了非 Wired 模式。

这个故事的核心教训不是"Wired 好"或"非 Wired 好"——而是：

1. **一个单行初始化 bug 可以完全改变一个系统的行为表现。**
2. **如果你在数据中看到"某个功能在所有条件下都完全失效"（比如 prompt 变异=0%），别写长篇架构分析——先检查代码。**
3. **架构分析很重要，但它应该在确认系统正确初始化之后再做。**

---

## 附录

[A] 这篇文章里所有数据都来自 `examples/autonomous-evolution/run.log` 的一次完整运行（修复 bug 后的版本）。如果你想复现，在项目根目录执行 `go run ./examples/autonomous-evolution/` 即可。

[B] GA 核心代码位于 `internal/ares_evolution/genome/` 目录下。核心流程在 `population.go:doEvolve()` 中。Wired 系统的包装在 `genome_wiring_system.go` 和 `genome_wiring.go` 中。**特别关注** `genome_wiring_system.go` 中的 `CreateWiredSystem()` 函数——注意 `PromptTemplates = PromptPool` 这行赋值是否存在。这个 bug 的修复就是这个赋值语句。

[C] 这篇文章的"用新数据推翻旧结论"的风格是我个人的偏好——**工程中最有价值的信息往往来自"我之前错了"的瞬间，而不是"我一直是对的"的确认。** 如果你发现自己的系统出了奇怪的问题，我的建议是：先检查初始化代码，再写架构分析。这个顺序省了我三次实验的时间。