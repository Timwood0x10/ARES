# GA 实战：两次真实进化实验教会我的事

> 先声明一下，这篇文章不是 GA 教程，也不是代码层面的逐行分析。它的副标题是"当你真的跑完 15 代进化之后，你会在日志里发现什么"，我想通过两次真实的进化运行记录，分享一些从数据里长出来的工程感悟。

---

## 动机

在 ares 进化系统里，GA（Genetic Algorithm）路径被设计为"零 token 进化"——不需要 LLM 调用，纯 CPU 计算，只在已有的高分策略池里做基因重组。但如果只是这样，这篇文章就没什么好写的了。

真正让事情变得有趣的是 **GenomeAdapter**——一个被设计用来追踪每一条策略血脉的谱系系统。它把 GA 变成"有线进化"（Wired GA）：每个线程携带自己的基因组，并行进化，保留完整的父-子关系链。听起来很优雅对吧？

问题是：**当 GA 被"有线化"之后，它的探索能力被削弱了多少？**

这篇文章会用 `/examples/autonomous-evolution/run.log` 里的真实数据，带你走一遍三次进化实验的全过程。前两次（Scenario 6 和 7）是严格对照实验——相同的种子、相同的种群大小、相同的代数，唯一变量是"是否使用 Wired 模式"。第三次（Scenario 5）用小种群做了独立验证。数据会揭示一些我在写代码时完全没想到的事情。

---

## 实验设计

三次 GA 运行共享相同的底层配置：

| 配置项 | Scenario 6 | Scenario 7 | Scenario 5 |
|--------|-----------|-----------|-----------|
| 种群大小 | 20 | 20 | 10 |
| 精英数 | 2 | 2 | 1 |
| 选择策略 | **Rank Selection** | **Tournament Selection** | Tournament |
| Wired 模式 | **否** | **是** | **是** |
| 变异率 | 0.3（紧急升至 0.5） | 0.3（紧急升至 0.5） | 0.3 |
| 代数 | 15 | 15 | 10 |
| Scorer | 确定性（deterministic） | 确定+LLM终验 | LLM Scorer（带确定性回退） |

三个实验的核心差异：

- **Scenario 6（纯自主进化 / 非 Wired）**：Rank selection + 无 GenomeAdapter + 纯确定性 scorer。这是最"经典"的 GA 配置——20 个独立个体自由交叉，不受谱系限制。
- **Scenario 7（有线进化 / Wired）**：Tournament selection + GenomeAdapter + 确定性 scorer + LLM 终验。这是"工程化"的 GA——每个线程保留自己的谱系，进化完成后用 LLM 验证最佳策略。
- **Scenario 5（真实数据管道 / Wired）**：用 LLM scorer 在进化循环内打分，而非只在终验阶段介入。种群更小（10），代数更少（10），作为对照组验证 Wired 模式下的行为是否一致。

结果预览：

| 场景 | 初始 Best | 最终 Best | 改进幅度 |
|------|----------|----------|---------|
| Scenario 6（非 Wired） | **62.50** | **89.47** | **+43.1%** |
| Scenario 7（Wired + LLM终验） | **62.50** | **62.50** | **+0.0%** |
| Scenario 5（Wired + LLM scorer） | **62.50** | **62.50** | **+0.0%** |

数字已经说明了一切。下面展开分析。

---

## 第一次运行：纯自主进化（Scenario 6 / 非 Wired / Rank Selection）

完整的 15 代进化轨迹：

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   62.50   52.40    5.00   32%
 2   77.50   59.35   32.50   28%
 3   77.50   61.85   42.50   39%
 4   77.50   63.35   42.50   33%
 5   77.50   69.60   40.50   33%
 6   87.39   68.68    5.00   17%
 7   88.81   76.49   47.50   28%
 8   88.81   79.77   47.50   25%
 9   88.81   78.23    5.00   21%
10   88.81   81.15   67.39   26%
11   88.81   81.88   41.70   16%
12   89.47   83.87   48.92   17%
13   89.47   68.31    5.00   18%
14   89.47   76.29    5.00   25%
15   89.47   75.88    5.00   21%
```

这条曲线的信息密度很高。几点观察：

**波动性远超预期**。Worst 列反复跌到 5.00（第 1、6、9、13、14 代），Avg 在 52.40 到 83.87 之间大幅震荡。这不是"稳定的定向进化"——这是野生的、充满噪声的进化过程。Worst=5.00 说明每代都有大量低分个体涌入，种群的"下限"始终没有提升。

**Best 线不是平滑上升的**。从 62.50 到 77.50（Gen 2，+24%），然后稳住 5 代，再到 87.39（Gen 6，+12.7%），再到 88.81（Gen 7，+1.6%），最后 89.47（Gen 12，+0.7%）。每次跃升之间都有长达数代的平台期。

**Base 分从 72.50 降到了 62.50**。对比之前的运行日志，目前的基线策略分数被调低了 10 分。这不是退化——是因为 fitness 计算逻辑改了。但一个有趣的事情是：**基线低了，但最终的 Best 却更高了**（89.47 vs 旧运行的 88.67）。说明确定性 scorer 的动态范围没有被压缩，低基线反而给了 GA 更大的改进空间。

### 谱系改进率：100%

Scenario 6 虽然是非 Wired 模式，但它仍然记录了 lineage（进化完成后从种群快照中提取血缘关系）：

```
Lineage records: 2437
  with_improvement: 2437 (100.0%)
```

**2437 条谱系记录，全部都有改进。** 100% 的改进率意味着每一代产生的每一位后代，都比它的父代更好。这在一个 20 个体 × 15 代 = 300 子代的系统中意味着每代产生约 162 条谱系记录（包含多次交叉变异的中间产物）。

对比来看，这就是一个**持续、高效、无浪费**的进化过程。

### 变异分布

```
Parameter mutations:   708 (35%)
Prompt mutations:      226 (11%)
Crossover events:    1061 (53%)
Total:                1995
```

三个数字值得讨论：

**53% 交叉**——交叉仍然是主力算子。均匀交叉（uniform crossover）从两个父代各取一半参数，持续生成新的参数组合。这和旧运行的结论一致。

**11% prompt 变异**——这是旧运行中没有的数据。非 Wired 模式下，prompt 模板从 "helpful" 切换到 "precise" 变成了一个有 11% 概率发生的常规操作，而不是"绝响"。这说明当种群能够自由探索时，prompt 层面的突变也能被自然选择沉淀。而在 Wired 模式中，这个数字是 0。

**35% 参数变异**——parameter mutation 的比例从旧运行的 23% 上升到 35%，因为连续参数空间提供了更多的精细调节机会。

### Diversity 波动与紧急变异

Scenario 6 在 15 代中有多次 diversity 波动。以 Gen 6 为例：

```
time=... level=WARN msg="diversity collapse detected, injected fresh mutants"
    generation=6  overall_diversity=0.173
    dominant_lineage_share=0.25
    numeric_diversity=0.138
    categorical_diversity=0
    lineage_diversity=0.45
```

```
=== Diversity Report ===
  Overall:           0.1730
  Numeric:           0.1379
  Categorical:       0.0000
  Lineage:           0.4500
  Dominant Lineage:  25.00%
```

Diversity 的分解很说明问题：

- **categorical_diversity = 0**：所有个体已收敛到同一个 prompt 模板（"precise"）
- **numeric_diversity = 0.138**：数值参数的 pairwise 距离很低
- **lineage_diversity = 0.45**：血缘多样性尚可——说明虽然参数和 prompt 都趋同了，但这些个体来自不同的祖先分支

当 `overall_diversity < 0.05` 时，紧急变异注射通过 `injectFreshMutantsLocked()` 触发。但 Gen 6 的 overall=0.173 并没有低于硬阈值——触发它的是 **dominant_lineage_share=25% + categorical=0 的组合风险**。代码调用栈是：`doEvolve()` 在检测到 categorical 多样性完全消失时，调用 `injectFreshMutantsLocked()`（`genome/population_guard.go:46`）提前介入。

注射效果明显：Gen 7 的 diversity 恢复到了 28%。

### 收敛结果

最终最佳策略（ID: `fresh-mut-15-gen12`，版本 10，分数 89.47）：

| 参数 | 初始值 | 最终值 | 变化 |
|------|-------|-------|------|
| temperature | 0.7 | **0.0168** | -97.6% |
| top_k | 40 | **28.94** | -27.7% |
| max_tokens | 2048 | **1239.56** | -39.5% |
| prompt | "helpful" | **"precise"** | 风格切换 |

有一个非常值得注意的细节：**获胜策略的 ID 是 `fresh-mut-15-gen12`**。这个名字说明它来自第 12 代的"新鲜突变注射"（fresh mutant injection），不是从第 1 代的血脉逐步进化而来的。第 12 代是 Scenario 6 中最后也是最小的一次 Best 提升——从 88.81 到 89.47。

这意味着：**整个 15 代 GA 中，最大的跳变发生在 Gen 2（62.50→77.50），然后 Gen 6（77.50→87.39），然后 Gen 12 的注射操作带来了最终的 0.66 分提升。** 如果不做第 12 代的紧急注射，Best 可能就停在 88.81 了。

temperature=0.0168 是一个极端值——几乎完全确定性。top_k=28.94（下降 27.7%），max_tokens=1239.56（下降 39.5%）。这是"更短、更确定、更精准"的进化方向。

---

## 第二次运行：有线进化（Scenario 7 / Wired / Tournament Selection）

同样的种子、同样的种群大小（20）、同样的代数（15），唯一变化是：启用了 GenomeAdapter（Wired 模式）+ Tournament Selection。

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   62.50   56.12    5.00   14%
 2   62.50   55.88    5.00   18%
 3   62.50   60.00   32.50   17%
 4   62.50   59.25   32.50   19%
 5   62.50   56.50   32.50   20%
 6   62.50   54.50   32.50   19%
 7   62.50   57.38    5.00   18%
 8   62.50   56.62    5.00   18%
 9   62.50   54.88    5.00   17%
10   62.50   54.75    5.00   17%
11   62.50   55.12    5.00   20%
12   62.50   58.25   32.50   18%
13   62.50   56.12    5.00   17%
14   62.50   59.50   32.50   18%
15   62.50   57.50   32.50   23%
```

**15 代，Best 纹丝不动。62.50，从第 1 代到第 15 代，一个数字。**

对比 Scenario 6 的 89.47，差距 **26.97 分**。这不是"差距"——这是完全不同的两个系统。

### 谱系改进率：1.9%

```
Lineage records: 2400
  with_improvement: 45 (1.9%)
```

**2400 条谱系记录，只有 45 条带来了改进（1.9%）。** 和 Scenario 6 的 100% 对比，这不是量级的差异——这是质的差异。

Wired 模式下，每个线程在同一份基因组上做线性进化。2400 条记录中的 2355 条都只是"同一个赢家的不同变体"，而且这些变体全部没有超越原始的 62.50。这解释了为什么 Best 一动不动：因为**没有后代真正超过了父代**。

### Diversity 崩溃：不是两次，是每代

这是对比 Scenario 6 和 7 时最刺眼的差异。Scenario 6 的 diversity 范围是 16%–39%，在 Gen 6、11、12、13 触发过紧急注射。Scenario 7 的 diversity 始终在 14%–23% 之间徘徊——**从来没有恢复到过 30% 以上**。

```
Gen  Diversity
---  ---------
 1   14%  ← 初始多样性就已很低
 2   18%
 3   17%
 4   19%
 5   20%
 6   19%
 7   18%  ← 触发停滞检测 + diversity 崩溃
 8   18%
 9   17%
10   17%
11   20%
12   18%
13   17%
14   18%
15   23%  ← 唯一的多样性回升
```

在代码层面，`doEvolve()` 中每次迭代末尾都会检查 diversity。一旦 `overall_diversity < 0.05` 或 `dominant_lineage_share > 0.6`，`injectFreshMutantsLocked()` 在 `genome/population_guard.go` 中被调用，触发紧急注射。Scenario 7 的 diversity 从未恢复到健康水平——紧急注射只能暂时止血，无法根治。

为什么 Wired 模式下 diversity 这么难恢复？因为 GenomeAdapter 的设计哲学是"每个线程独立进化"，新鲜个体注入后会被绑定到一个特定的线程谱系中。而 Rank Selection（Scenario 6）让所有个体在全局范围内自由竞争，新鲜基因更容易扩散到整个种群。

### 停滞检测触发了两次

```
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    generation=6  stagnant_generations=5 generation=7
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    generation=11  stagnant_generations=5 generation=12
```

`doEvolve()` 中的停滞检测逻辑：当 Best 连续 5 代没有变化时，从精英池中注入随机变异体。Gen 6 和 Gen 11 各触发了一次。

两次注射的结果：

- Gen 7 注射后：Best 仍为 62.50，Avg 从 54.50 提升到 57.38（+2.88），但 Best 不动
- Gen 12 注射后：Best 仍为 62.50，Avg 从 55.12 提升到 58.25（+3.13），但 Best 不动

注射提高了种群的 Avg（整体变好了），但没有产生任何一个超过 62.50 的个体。说明精英池本身已经同质化了——从一个同质的池子里做变异，产生的也只是不同形式的同质。

### 唯一的赢家：同一个 ID 统治 15 代

在 Scenario 7 的 Promotion Summary 中：

```
--- Promotion Summary ---
  Winner:        13d2197e-b8e6-446c-bc53-3628d80eefe7
  Winner Score:  62.5000
  State:         champion
  Reason:        high confidence + success rate exceed thresholds
  Samples:       10
  Success Rate:  80.00%
  Confidence:    75.00%
```

**同一个 UUID 从第 1 代赢到第 15 代。** 这个策略从未被超越。

它的成功率和置信度（80%/75%）满足了 promotion 到 champion 的条件，但它的分数就是 62.50——和基线策略一模一样。它没有退化，但也没有进化。

这本身是一个重要的发现：**如果一个系统的 promotion 标准只看成功率（80%）和置信度（75%），而不看 score 的绝对改进，那么一个"安全但不进步"的策略可以永久占据冠军位置。** Scenario 7 的冠军不是因为它好——而是因为没人打败它。

### 变异分布

```
Parameter mutations:   659 (66%)
Prompt mutations:        0 (0%)
Crossover events:     341 (34%)
Total:                1000
```

**0% prompt 变异**——和 Scenario 6 的 11% 形成鲜明对比。Wired + Tournament Selection 的组合下，prompt 模板完全失去了进化可能性。路径依赖太强了：既然参数变异就够竞争了，prompt 切换这种"大调整"从来没有在锦标赛选择中存活下来。

**66% 参数变异 vs 34% 交叉**——Scenario 7 的参数变异远高于交叉（66/34），而 Scenario 6 恰恰相反（35/53 的参数/交叉比）。这是因为 Wired 模式下每个线程的基因组结构限制了有效的交叉配对范围——线程之间的交叉被谱系边界约束了。

### LLM 终验：偏见的证据

Scenario 7 的最后一步是 LLM 终验：

```
LLM validation of best strategy (deterministic_score=62.50)
  llm_score=70.00  duration=6.234s
  Winner: Autonomous (no LLM) (+26.97 points)
```

```
📊 Control Group Comparison
═══════════════════════════════════════════════════
Autonomous (no LLM)                     LLM-Guided
─────────────────────────  ─────────────────────────
Best Score:       89.47    Best Score:       62.50
temperature:   0.0168      temperature:          0.1
top_k:         28.94       top_k:                 40
max_tokens:   1239.56      max_tokens:          2048
prompt:        precise     prompt:           helpful

🏆 Winner: Autonomous (no LLM) (+26.97 points)
```

LLM 给 62.50 的策略打了 **70 分**——比确定性 scorer 高了 7.5 分。如果只看 LLM 的打分，你会以为这个策略有"潜力"——它值 70，只是确定性 scorer 没测出来。

但 Scenario 6 的最佳策略在确定性 scorer 下是 89.47，而 LLM 给 Scenario 7 的最佳策略只打了 70。**差距是 19.47 分。**

这是 LLM bias 的一个干净证据：**LLM 倾向于给"看起来合理的策略"打一个中等偏上的分数，它无法识别那些真正优秀的策略组合。** 确定性 scorer 用精确的数学计算告诉你 89.47 > 62.50，而 LLM 说 "hmm, 70 吧"。

### Tiered Scoring：缓存命中率 80-100%

Scenario 7 的每一代都运行了 tiered scoring（缓存 → 启发式 → LLM 回退）：

```
Gen 1:  llm_used=11  cache_hits=29  heuristic_used=0  total=40
Gen 2:  llm_used=0   cache_hits=40  heuristic_used=0  total=40  ← 全部缓存
Gen 5:  llm_used=2   cache_hits=38  heuristic_used=0  total=40
Gen 7:  llm_used=6   cache_hits=34  heuristic_used=0  total=40
Gen 11: llm_used=4   cache_hits=36  heuristic_used=0  total=40
Gen 15: llm_used=6   cache_hits=34  heuristic_used=0  total=40
```

LLM 的使用从 Gen 1 的 11 次迅速降到 Gen 2 的 **0 次**（40 次全部缓存命中）。这说明在 Wired 模式下，种群在前几代就迅速收敛到了相似的策略组合，后续每一代的 40 个个体几乎都是"已评分过的变体"。

**启发式 scorer 从未被调用**——这一点也值得注意。代码层级为 `scoring.NewTieredScorer()` 配置了三层回退（cache → heuristic → LLM），但 80-100% 的缓存命中率让 heuristic 层完全没有发挥作用。

---

## 第三次运行：真实数据管道（Scenario 5 / Wired / LLM Scorer）

作为额外验证，Scenario 5 用更小的种群（10）和更少的代数（10），在 Wired 模式下引入了 LLM scorer（在进化循环内做 tiered scoring，而不是只在终验阶段）：

```
Gen  Best    Avg     Worst   Diversity
---  ------  ------  ------  ---------
 1   62.50   55.00   32.50   21%
 2   62.50   56.50   47.50   24%
 3   62.50   57.50   47.50   30%
 4   62.50   59.00   47.50   32%
 5   62.50   57.50   47.50   28%
 6   62.50   58.50   47.50   28%
 7   62.50   60.50   47.50   27%
 8   62.50   59.50   47.50   16%
 9   62.50   60.00   32.50   16%
10   62.50   59.50   47.50   16%
```

**和 Scenario 7 一模一样：Best 纹丝不动，始终 62.50。**

谱系改进率：

```
Lineage records: 550
  with_improvement: 10 (1.8%)
```

1.8% vs Scenario 7 的 1.9%。两个 Wired 实验在谱系改进率上高度一致。

Scenario 5 还展示了 Wired 模式的 **shadow promotion** 行为：

```
--- Promising Strategies ---
  speedy → shadow (success_rate=67%, confidence=0.67)
  creative → shadow (success_rate=67%, confidence=0.67)
```

两个策略（speedy 和 creative）达到了 67% 的成功率，被 promotion 到 "shadow" 状态。但它们仍然没有产生任何一个超过 62.50 的后代。这说明成功率和分数改进是两个独立维度——你可以有 67% 的成功率但分数原地踏步。

这重复验证了 Scenario 7 的结论：**Wired 模式下，GA 无法将种群推向更高的分数区间。** 而且这不是 LLM scorer vs 确定性 scorer 的问题——Scenario 5（LLM scorer in loop）和 Scenario 7（确定+LLM终验）都停滞了。根因是 Wired 模式本身。

---

## 从数据里提炼的工程教训

### 1. Wired 模式是 GA 的探索瓶颈

这是本次运行中最重要的发现。

Wired 模式（GenomeAdapter）的设计动机是好的——追踪谱系、保留血缘信息、让进化路径可回溯。但它在实践中变成了探索的瓶颈：

- **线程隔离**：每个线程只有一条进化路径，20 个线程 = 20 条线性路径，而不是 20 个个体自由交叉的 20! 种组合可能
- **谱系锁定**：一旦某个线程的祖先找到了局部优解，它的后代被锁定在祖先的基因组附近，难以跳脱
- **全局竞争缺失**：Rank Selection 在全局范围内对所有个体排序，而 Wired + Tournament Selection 只在局部范围内竞争

**Scenario 6（非 Wired）之所以成功，不是因为它用了"更好的 GA 算子"——而是因为它的 20 个个体可以自由交叉，不受谱系约束。** 代码中，Scenario 6 直接调用 `pop.EvolveOnIdle()`（在 `population.go` 中），没有经过 GenomeAdapter 的包裹。Scenario 7 和 5 调用的是 `evolution.RunIdleEvolution()`，它包裹在 `genome_wiring_system.go` 中，每一代的进化通过 `adapter.Evolve()` 完成，其中包含了谱系记录、tiered scoring、guardrail 检查——这些"增强"反而加上了枷锁。

### 2. 确定性 scorer 不仅够用，而且更好

整理三个场景的终局比分：

| 场景 | Scorer | 分数 | 成本 |
|------|--------|------|------|
| Scenario 6 | 纯确定性 | **89.47** | ~3ms（进化）+ ~0ms（无LLM） |
| Scenario 7 | 确定性+LLM终验 | **62.50** | ~4ms（进化）+ 6.23s（LLM） |
| Scenario 5 | LLM scorer | **62.50** | ~5ms（进化）+ ~2s/代（LLM） |

**最便宜的系统跑出了最好的结果。**

确定性 scorer 的每次评估在微秒级完成，没有缓存、没有回退、没有偏见。它很简单——一个固定函数的数学计算——但正因为简单，它一致、可重复、可比较。15 代 × 20 个体 × 每次微秒 = 不到 1 秒的总评估时间。

LLM scorer 在 Scenario 5 中每一代都有 LLM 调用（尽管 80-100% 被缓存命中），Gen 1 就做了 11 次 LLM 调用。每代结束时还有 "evidence aggregation"：

```
time=... level=INFO msg="Evidence aggregation: winner=62.5000 confidence=0.55"
```

但这些额外的计算没有带来任何分数提升。62.50 到 62.50。

### 3. 谱系改进率是比 Best 分数更敏感的早期指标

Scenario 6 的谱系改进率是 **100%**。Scenario 7 是 **1.9%**。Scenario 5 是 **1.8%**。

Best 分数在第 15 代告诉你"谁赢了"。但谱系改进率在第 3 代就告诉你"有没有人在赢"。

在 Scenario 7 中，如果只看 Best 线，你得到的信息是"62.50，没动"。但谱系改进率告诉了你更多：2400 次尝试中只有 45 次产生了比父代更好的后代。这是"探索引擎熄火"的量化证据。

该指标的计算在 `service.go` 的 `collectLineages()` 函数中，从 `wiredSystem.Genealogy.Lineages()` 获取所有谱系记录后，逐条检查 `score - parent_score > 0`。实现简单，但信息量巨大。

### 4. Diversity 监测比精英保护更重要

Wired 模式的 diversity 始终在 14-23% 之间。非 Wired 模式的 diversity 可以恢复到 30%+。而不论哪种模式，当 categorical_diversity = 0（所有个体使用相同的 prompt 模板）时，系统就已经进入了危险区。

当前代码中的 diversity 检测（`population.go` + `population_guard.go`）由三个组件组成：

1. **DiversityReport**：分解为 numeric / categorical / lineage 三个维度
2. **shouldInjectFreshMutants()**：在 `overall_diversity < 0.05` 或 `dominant_lineage_share > 0.6` 时触发
3. **adjustMutationRateLocked() (genome/adaptive.go:433)**：根据 diversity 趋势动态调整 mutation_rate，上限 0.5

这套机制在 Scenario 6 中有效——紧急注射恢复了 diversity，Gen 12 的注射直接产生了最终获胜策略（`fresh-mut-15-gen12`）。但在 Wired 模式下，触发阈值（0.05 或 0.6）可能在 diversity 降到危险水平之前就被谱系结构锁死了。

可能的方向：**在 Wired 模式下提高 dominant_lineage_share 的触发阈值（从 0.6 降到 0.4），让紧急注射更早介入。** 或者，在 diversity 低于 0.2 时不再注入"精英克隆"（它们仍然来自同一谱系），而是从外部生成全新个体。

### 5. LLM 终验的自我揭示

LLM 终验的结果最讽刺：

```
deterministic_score=62.50  →  llm_score=70
```

LLM 给一个完全停滞、从未产生任何改进的 62.50 策略打了 70 分。而 Scenario 6 的最佳策略（89.47）根本没有经过 LLM 评估——如果问了，它可能会得到 85 或 90，但系统不需要这个确认就已经知道它是更好的。

这指向一个更根本的问题：**LLM 的评分和确定性 scorer 的评分，在 ares GA 系统中衡量的是不同的事物。** 确定性 scorer 衡量"执行任务的结果是否更好"。LLM 衡量"这个策略看起来是否合理"。在进化上下文中，你关心的是前者——而 LLM 提供的恰好是后者。

如果硬要在系统中使用 LLM，最务实的用法是：**完成确定性 GA 的全部进化后，对 Top-3 候选策略做一次 LLM 交叉排序，验证它们是否有确定性 scorer 无法捕捉的缺陷。** 而不是让 LLM 参与进化过程本身。

---

## 代码与数据对照

三次运行揭示的真相：

- **非 Wired + 确定性 scorer + Rank Selection = 89.47**。最"原始"的配置跑出了最好的结果。
- **Wired + 任何 scorer = 62.50**。不论你用 LLM scorer 还是确定性 scorer + LLM 终验，结果都一样：停滞。
- **LLM 在进化循环中的角色是冗余的**。它每代产生数百毫秒的延迟和 80-100% 的缓存命中率，对结果没有正面贡献。

GA 的理论很成熟，但这个结果说明：**工程实现的"增强"（谱系追踪、Tiered Scoring、Promotion Pipeline）在改善可观测性的同时，往往以牺牲探索能力为代价。** GA 的强大来自于它的无序和随机——当你把一切都"组织好"时，反而失去了它最宝贵的东西。

如果你也在搞进化算法，我的建议是：

1. **第一件事：先跑非 Wired 基线**——不要上 GenomeAdapter，不要上谱系追踪，一个干净简单的 GA + 确定性 scorer 就能告诉你参数空间的基本形状。如果非 Wired 模式跑不动，其他模式更跑不动。
2. **第二件事：追踪谱系改进率**——Best 分数有滞后性，谱系改进率在第 3 代就能提前预警。如果改进率低于 10%，说明探索引擎出问题了。
3. **第三件事：把 LLM 留在进化循环之外**——确定性 scorer 在微秒级给出精准结果，LLM 在百毫秒级给出模糊判断。在 GA 场景中，快而准的确定性评估远胜于慢而偏的 LLM 评估。
4. **第四件事：警惕"组织化"的诱惑**——谱系、分级 scoring、promotion pipeline——这些设计让系统"看起来更可控"，但每次增强都附加了约束条件。在加功能之前，先问：这个约束是我想要的吗？

以上就是从三次真实的进化运行中提炼出的教训。希望对你有所帮助。

---

[A] 这篇文章里所有数据都来自 `examples/autonomous-evolution/run.log` 的一次完整运行。如果你想复现，在项目根目录执行 `go run ./examples/autonomous-evolution/` 即可。

[B] GA 核心代码位于 `internal/ares_evolution/genome/` 目录下，注意不是旧路径 `internal/evolution/genome/`。核心流程在 `population.go:doEvolve()`（排序 → 选择 → 精英 → 交叉 → 变异 → 多样性检测 → 紧急注射）。适应性变异率调整在 `genome/adaptive.go:adjustMutationRateLocked()` 中，紧急注射逻辑在 `genome/population_guard.go:injectFreshMutantsLocked()` 中。Wired 系统的包装在 `genome_wiring_system.go` 和 `genome_wiring.go` 中。

[C] 这篇文章的"披露不一致数据"的风格是我个人的偏好——**工程中最有价值的信息往往来自异常信号，而不是平稳运行的日志。** 不要害怕在文章里展示你的系统"不完美"的地方，它们才是读者真正能学到东西的地方。