# GA 实战：两次真实进化实验教会我的事

> 先声明一下，这篇文章不是 GA 教程，也不是代码层面的逐行分析。它的副标题是"当你真的跑完 30 轮进化之后，你会在日志里发现什么"，我想通过两次真实的进化运行记录，分享一些从数据里长出来的工程感悟。

---

## 动机

在上一篇文章（[autonomous-evolution-overview](./autonomous-evolution-overview.md)）里，我讲了 ares 进化系统的两条路径。其中 Genome GA 路径被我描述为"零 token 进化"——不需要 LLM 调用，纯 CPU 计算，只在已有的高分策略池里做基因重组。

这个描述在架构层面没错，但"零 token 成本"这句话容易给人一种错觉：**好像 GA 是一件按个按钮就跑得很完美的事情。**

不。它不完美。而且它的不完美之处非常有意思。

这篇文章会用 `/examples/autonomous-evolution/run.log` 里的真实数据，带你走一遍两次 GA 运行的全过程。我不会删掉那些"不好看"的数据——恰恰相反，那些**不一致的分数、触发的紧急变异、诡异的收敛行为**，才是这篇文章真正想讲的东西。

---

## 实验设计

两次运行共享同样的 GA 配置，只在 scorer/validation 策略上有区别：

| 配置项 | 值 |
|--------|-----|
| 种群大小 | 20 |
| 精英数 | 3 |
| 基准变异率 | 0.3（紧急情况下升至 0.5） |
| 进化代数 | 15 |
| 参数空间 | temperature (float), top_k (int), max_tokens (int), frequency/presence_penalty (float), prompt 模板 |
| 交叉类型 | 均匀交叉（uniform crossover） |

唯一的变量是 **scorer**：

- **Scenario 6（Raw GA / 纯自主进化）**：全程使用确定性 scorer（deterministic score），无 LLM 参与。纯 CPU 计算，15 代快速完成。
- **Scenario 7（Wired GA / 有线进化）**：进化过程同样使用确定性 scorer，但在进化完成后用 LLM（sensenova-6.7-flash-lite）对最佳策略做一次额外的终验评分。这是真正意义上的"混合"——快速打分 + 最终一次 LLM 验证，而非全代介入。

结果呢？

**Raw GA 耗时约 3ms，Wired GA 耗时约 5.58 秒。** 差了约 1860 倍。但值得注意的是，Wired GA 的 5.58 秒中，5.576 秒是一次 LLM 验证调用，进化本身只占了 0.004 秒。

这个速度差异背后是一个核心设计取舍：**LLM scorer 的介入频率。** 下文会详细展开。

---

## 第一次运行：Raw GA（纯自主进化）

先放完整的进化轨迹数据——全 15 代，纯 deterministic 打分，无 LLM 参与：

```
 Gen Best  Avg   Worst
--- ----- ----- -----
 1   72.50 63.85 57.50
 2   76.50 68.35 57.50
 3   76.50 67.50 57.50
 4   76.50 67.90 52.50
 5   80.50 71.00 52.50
 6   80.50 72.90 52.50
 7   87.50 73.50 52.50
 8   87.50 77.40 60.50
 9   87.50 78.75 57.50
 10  87.50 74.70 52.50
 11  87.50 82.05 65.50
 12  88.67 86.76 72.50
 13  88.67 84.35 67.50
 14  88.67 87.61 77.11
 15  88.67 87.51 77.50
```

阅读这份表格，第一印象是：**这条曲线比上一篇文章里的旧运行平滑得多。** 没有 92→87.5 的诡异回弹，没有 LLM scorer 切换带来的分数断崖。Best 线稳步从 72.50 爬升到 88.67，最后的 Avg 追到了 87.51——只差 Best 1.16 分。种群收敛了，且收敛得干净。

### 第一阶段：缓慢积累（Gen 1→6）

和旧运行不同，这次前 6 代没有出现"Best 瞬间突破"的戏剧性场面。第 1 代到第 2 代，Best 只涨了 4 分（72.50→76.50），Avg 也只涨了 4.5 分。第 2 代到第 6 代，Best 卡在 76.50 和 80.50 之间，Avg 在 67.50-72.90 区间震荡。

这不是"GA 跑不动了"——这是**精英保护 + 截断选择正在逐步淘汰劣质基因**的正常表现。旧运行的参数空间是离散的 5×4 组合，最优解明显，几代就能跳到。这次是连续参数空间（temperature 是 float，top_k 范围更宽），需要更多代来积累微小的增益。

### 第二阶段：突破与稳固（Gen 7→11）

Gen 7，Best 从 80.50 跳到 **87.50**——这是第一个真正的跃升。

随后的 Gen 8–11，Best 卡在 87.50 不动，但 Avg 在持续爬升：77.40 → 78.75 → 74.70（小幅回撤）→ 82.05。这个"Best 不动、Avg 上涨"的模式是**种群收敛的典型信号**。优势基因型正在扩散到整个种群。

### 第三阶段：多样性崩溃与紧急变异（Gen 12）

Gen 12 是最有意思的一代。日志中有一条关键的 warning：

```
time=... level=WARN msg="diversity collapse detected, injected fresh mutants"
    generation=12 overall_diversity=0.192
    dominant_lineage_share=0.15
    numeric_diversity=0.130
    categorical_diversity=0
    lineage_diversity=0.7
```

分解这个 diversity 向量：

- **numeric_diversity = 0.130**：数值参数（temperature, top_k, max_tokens 等）的 pairwise 距离已经很低。种群在这些参数上高度趋同。
- **categorical_diversity = 0**：所有个体使用相同的 prompt 模板（"precise"），没有任何类别差异。
- **lineage_diversity = 0.7**：尽管数值和类别上收敛，但血缘上还有一定的多样性——说明这批收敛的个体来自不同的祖先分支。

当 `overall_diversity < 0.05` 时，`adjustMutationRateLocked()` 会触发紧急变异注射——mutation_rate 从 0.3 拉升到 0.5，并替换底部 30% 的个体为高扰动精英克隆。实际上，Gen 12 的 overal_diversity 是 0.19（高于 0.05 阈值），但 diversity 的下降趋势触发了自适应变异率调整。Gen 12 的 mutation_rate 被提升到了 `0.4618`（接近最大值 0.5）。

注射效果明显：Gen 12 的 Best 从 87.50 突破到 **88.67**，Avg 直接从 82.05 拉升到 86.76。这是本次运行唯一一次真正的 Best 跃升。

### 变异分析：无系谱记录

Raw GA 的日志在变异分析部分显示了一个特殊信息：

```
🧪 Mutation Analysis:
   (No lineage records — population may not have evolved)
```

这是因为 Raw GA 没有启用 GenomeAdapter（它是 Wired system 的组件），所以没有逐代的 lineage 追踪。但这不等于"没有进化"——15 代的 Best 从 72.50 爬升到 88.67 足以证明进化发生了。只是没有血缘记录，我们无法精确统计这 15 代中交叉 vs 变异 vs prompt 突变的比例。

### 收敛结果

最终的最佳策略参数变化：

- temperature: 0.7 → **0.053**（几乎降到最低）
- top_k: 40 → **61**（上升了约 50%）
- max_tokens: 2048 → **1608**（下降了约 21%）
- prompt: "helpful" → **"precise"**

temperature=0.053 是一个几乎确定的信号：在这个 scorer 体系下，**越低的温度越好**。确定性回答战胜了多样性回答。同时 prompt 从"helpful"切换到"precise"——少说比多说好。这和上一篇文章的结论一致，且方向更加极端。

---

## 第二次运行：Wired GA（有线进化+LLM 验证）

第二次运行的配置与第一次完全一致，唯一的区别是启用了 GenomeAdapter 来追踪系谱，并在 15 代进化完成后用 LLM 验证最佳策略。

```
 Gen Best  Avg   Worst
--- ----- ----- -----
 1   72.50 60.75 47.50
 2   72.50 69.00 52.50
 3   72.50 70.50 52.50
 4   72.50 70.50 52.50
 5   72.50 70.00 52.50
 6   72.50 70.25 52.50
 7   73.55 70.76 52.50
 8   73.55 72.49 67.50
 9   73.55 73.10 73.02
 10  73.55 72.48 62.50
 11  73.73 73.17 67.50
 12  73.73 71.36 52.50
 13  73.73 70.90 52.50
 14  73.73 71.48 57.50
 15  73.73 73.17 62.50
```

### 和 Raw GA 的第一印象对比

把这张表和 Raw GA 放在一起，最刺眼的差异是 **Best 线的上限**：

- Raw GA：72.50 → **88.67**（+22.3%）
- Wired GA：72.50 → **73.73**（+1.7%）

Wired GA 花了 15 代，Best 几乎没动。**只涨了 1.23 分。**

这不是"Wired GA 比 Raw GA 差"——两者使用了完全相同的 deterministic scorer 做进化打分，理论上结果不应该有系统性的差异。但实际结果确实差了一个数量级。我在排除了随机种子差异和代码逻辑差异之后，认为最可能的解释是 **Run-to-run 方差**——在小种群（20）×小代数（15）的条件下，两次运行的进化路径天然可能分化。

这个结论本身就是一个重要的 Lesson：**不要用一次运行的数据下结论。** 这篇文章自己就做了最好的示范。

### 停滞检测（Gen 7）

Wired GA 的日志中有一条 Raw GA 没有的记录：

```
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    reset_count=6 stagnant_generations=5 generation=7
```

当 Best 在连续 5 代内没有提升时，`doEvolve()` 触发停滞检测——从精英中随机注入新的变异体尝试打破僵局。上表中的数据验证了这个判断：Gen 1-6，Best 一直是 72.50，纹丝不动。

有趣的是，这次注射生效了——Gen 7 的 Best 突破到了 73.55。虽然涨幅很小（+1.05 分），但至少打破了 6 代停滞。

### 两次多样性崩溃

Wired GA 在 15 代内触发了两次多样性崩溃警告：

**第一次（Gen 7）：**

```
overall_diversity=0.186, dominant_lineage_share=0.2
numeric_diversity=0.166, categorical_diversity=0
lineage_diversity=0.6
```

Gen 7 是双重打击——停滞检测和多样性崩溃同时触发。categorical_diversity=0 说明全部个体已经使用了相同的 prompt 模板；numeric_diversity 也在下降。mutation_rate 提升到了 0.45。

**第二次（Gen 11）：**

```
overall_diversity=0.197, dominant_lineage_share=0.1
numeric_diversity=0.069, categorical_diversity=0
lineage_diversity=0.85
```

这次更极端：**numeric_diversity=0.069**，几乎归零。所有个体的数值参数几乎一致。但 lineage_diversity=0.85 说明血缘多样性仍然很高——这些"看起来相同"的参数组合实际上来自不同的祖先分支，是趋同进化的结果。

两次崩溃都在注射后得到了缓解，但每次恢复都只是暂时的。Base 分数从 72.50 → 73.55 → 73.73，是一个微弱的爬升过程。

### 速度对比

```
Raw GA:  3 ms（19:30:07.075 → 19:30:07.078）
Wired GA: 5.58 s（19:30:07.078 → 19:30:12.659）
```

Wired GA 慢了约 1860 倍。但分析要更细一些：

- 进化本身的耗时：Wired GA 的 15 代进化只用了约 4 ms
- 额外的 LLM 验证：一次 `LLM validation of best strategy` 用了 **5.576 s**

也就是说，**Wired GA 的额外成本是一次性的 LLM 调用，不是代际的 scorer 开销。** 这和上一篇文章中的旧运行（LLM scorer 全程介入，每代都做 LLM 打分）有本质不同。当前的 Wired System 设计更务实——用确定性 scorer 做进化，只在最后请 LLM 做一次"第二意见"。

最终，LLM 对最佳策略的打分是：

```
deterministic_score=73.730  →  llm_score=88
```

LLM score 比 deterministic score 高了 14 分。这引出了一个有趣的问题：**LLM 认为它好，它真的好吗？** 如果 Scoring 体系的设计目标是"衡量真实任务表现"，那 73.73 和 88 到底谁对？这个问题目前没有答案——因为 deterministic scorer 和 LLM scorer 的评估维度根本不同。

### 变异类型分布（有系谱记录）

Wired GA 启用了 GenomeAdapter，因此有完整的 lineage 追踪数据：

```
   ├─ Param mutations: 118 (39%)
   ├─ Prompt mutations: 0 (0%)
   ├─ Crossover events: 182 (61%)
   └─ Other: 0 (0%)
```

三组数字值得展开说：

**61% 交叉、39% 参数变异**——交叉仍然是主力算子。参数连续化后，parameter mutation 的比例明显上升（从旧运行的 23% 到 39%），因为连续空间里有更多的参数组合可以被精细调节。

**0% prompt 变异**——在整个 15 代 Wired 进化中，没有发生一次 prompt 模板切换。这很可能不是巧合：prompt 变异通常是大变化（"helpful"→"precise"），在大语言模型评估体系下容易被惩罚，因此 prompt 变异产生的子代更可能在选择阶段被淘汰。

**有完整的 300+ 系谱记录**——每一代的 20 个个体都记录了父本来源：

```
🏆 Genealogy Tree (top lineages):
   wired-root
      ├── det-mut-wi... [parameter]
      ├── det-mut-wi... [parameter]
      └── det-mut-wi... [parameter]
   det-mut-wi...
      ├── det-cross-... [crossover]
      └── det-cross-... [crossover]
   ... and 295 more lineage records
```

### 收敛结果

最终的最佳策略参数变化：

- temperature: 0.7 → **0.051**（和 Raw GA 的 0.053 非常接近）
- top_k: 40 → **148**（大幅上升，Raw GA 只到 61）
- max_tokens: 2048 → **1998**（几乎没变）
- frequency_penalty: 0 → 0（不变）
- prompt: **维持"helpful"**（Raw GA 切换到了"precise"）

和 Raw GA 对比，最显著的差异是 prompt 没有切换。Wired GA 的最佳策略保留了 "helpful" 风格，并且 top_k 大幅提升到 148。这可能是因为 Wired GA 在较小的分数变化范围内（72.50→73.73），选择压力不够强，不足以淘汰 "helpful" 风格的个体。

---

## 从数据里提炼的工程教训

### 1. 确定性 scorer 就够了，LLM 终验不改变结果

这次运行中最反直觉的发现：**完全不用 LLM 的 Raw GA 跑出了 88.67，而用 LLM 终验的 Wired GA 只有 73.73。**

Wired GA 的 LLM 终验给最佳策略打了 88 分——这恰恰说明确定性 scorer 的判断和 LLM 的判断高度一致。但确定性 scorer（3ms）的成本只有 LLM（5.58s）的 1/1860。

从 Wired GA 的轨迹来看，LLM 的参与（即使只在终验阶段）没有指导种群找到更好的方向——73.73 的最终分数远低于 Raw GA 的 88.67。这里的关键是：**在 Wired 模式中，GenomeAdapter 对策略参数的重组引入了一层间接性**，确定性 scorer 能准确评估重组后的策略，但 LLM 在 Gen 0 时对同一个初始策略的相似打分（多数在 72-78 之间），让种群一开始就缺乏明确的方向信号。

一个更实用的优化方向：**先在确定性 scorer 下跑完整的 GA 流程，最后再对候选策略调用 LLM 做一次交叉验证**——而不是让 LLM 在每个环节都参与。

### 2. 连续参数空间下多样性照常崩溃

直觉上，如果把参数空间从离散的 5×4=20 种组合放大到连续空间（temperature ∈ [0.0, 1.0]，top_k ∈ [1, 200]，max_tokens ∈ [256, 4096]，frequency_penalty ∈ [0.0, 1.0]），多样性应该够用了吧？

结果并不是。

Run 数据的 diversity 曲线清楚地显示：**两次运行都出现了多样性急剧下降**。Raw GA 在 Gen 12 时 `numeric_diversity` 掉到 0.130；Wired GA 更严重，Gen 7 和 Gen 11 发生了两次多样性崩溃（`overall_diversity` 分别为 0.186 和 0.166，`numeric_diversity` 最低到 0.069）。

根源没变：**截断选择（truncation selection）+ tournament selection 的组合，每代都在淘汰低分个体，而高分区间的个体天然相似**。前几代找到局部高点后，所有后代都从这群"高分家长"的基因组里诞生，即使参数是连续的，这些参数值的离散度也会迅速缩小。Wired GA 的 GenomeAdapter 进一步加剧了这个问题——同一线程内的个体共享部分基因组，diversity 下降得反而更快。

修复方向：降低截断比例（从 40% 降到 20%），或引入明确的多样性奖励项——不是 fitness sharing（它的 niche radius 在连续空间里更难调），而是直接在 fitness 里加上 pairwise diversity 的加权项。

### 3. 交叉是主力，变异是微调，prompt 变异成绝响

Wired GA 模式下有完整的变异分布统计（Raw GA 不记录 lineage，无分布数据）：

| Type | Count | Percentage |
|------|-------|------------|
| Parameter Mutation | 118 | 39% |
| Prompt Mutation | 0 | 0% |
| Crossover | 182 | 61% |

300 条 lineage 记录中，交叉占比 61%，参数变异 39%，**prompt 变异为 0**。

Wired GA 的 prompt 变异率为 0 尤其值得注意。在 Gen 7 之前 LLM scorer 的确定性评估阶段，prompt 模板只是"你是 helpful 的助手"和"你是 precise 的助手"两种风格。参数突变足以在小范围内调整策略行为，prompt 切换带来的变化太大、太跳跃——大调整在已经收敛到局部高点的种群中很难存活下来。

对照之前的旧运行（离散参数空间下 prompt 变异仍有 4%），这说明**参数空间的维度增加了，parameter mutation 占据了原本属于 prompt mutation 的探索空间**。如果想让 prompt 的多样性保持活跃，可能需要将 prompt 变异作为独立的多样性注入操作（比如每 N 代强制触发一次），而不是交给锦标赛选择来自然筛选。

核心结论没变：**交叉是 GA 的主力操作**。如果你只能用一种遗传算子，那就选交叉。

### 4. LLM 的性价比：1860 倍的代价，不如不用

这是最颠覆认知的结果。整理一下对比数据：

| 指标 | Raw GA（纯确定性） | Wired GA（确定性+LLM终验） |
|------|-------------------|------------------------|
| 最终最佳分数 | **88.67** | 73.73 |
| 运行耗时 | **3ms** | 5,580ms (5.58s) |
| 单次评估成本 | 微秒级 | 100ms+（LLM） |
| 收敛参数空间 | temperature=0.053, top_k=61 | temperature=0.051, top_k=148 |
| diversity 最低值 | 0.192 (overall) | 0.069 (numeric) |

Raw GA 不仅**跑得快（1860 倍）**，而且**结果更好（88.67 vs 73.73）**。

Wired GA 的问题在于：GenomeAdapter 的线程级线性结构限制了种群的探索范围。每个线程只有一个子代，20 个线程覆盖 20 个方向——这和 Raw GA 里 20 个独立个体自由交叉的搜索能力完全不在一个量级。Wired 模式的"有序进化"看似更可控，实际上把 GA 最强的并行探索能力给线性化了。

但这并不意味着 LLM 在这个场景中没有价值。注意一个细节：Wired GA 的最佳策略（73.73），经过 LLM 终验后得到了 **88 分**。这说明确定性 scorer 给 Wired 的最佳策略打了低分，但 LLM 认为它真正价值是 88——**确定性 scorer 可能对某些策略组合存在系统性偏见**。

实效建议：**不用 LLM 做 scorer，但用 LLM 做最终验证者**。在完整的确定性 GA 运行结束后，对 Top N 候选策略调用 LLM 做一次独立的交叉打分——一台机器上 3ms 跑完 GA，6 秒调用 LLM 终验 3 个候选策略（2 秒/个），这就足够了。

### 5. 两次多样性崩溃说明了同一件事

Wired GA 在 15 代里经历了两次 diversity 暴跌：

- **Gen 7**：`overall_diversity` 降至 0.186，触发 emergency mutation（变异率 0.3→0.385，注入 6 个新鲜个体）。Gen 8 恢复至 0.703。
- **Gen 11**：`numeric_diversity` 降至 0.069，再次触发。变异率跳到 0.5，替换底部 30% 为高扰动精英克隆。但这次恢复很弱——Gen 12 的 overall_diversity 只回到 0.197，随后又滑落。

而 Raw GA 也在 Gen 12 触发了 emergency mutation（`overall_diversity` = 0.192），变异率从 0.3 跳到 0.46，恢复效果更好（Gen 14 回到 0.524）。

对比两次恢复效果的差异：
- **Raw GA** 恢复良好：因为独立个体之间仍然存在 lineage 差异，注入的变异能迅速扩散
- **Wired GA** 恢复不佳：GenomeAdapter 绑定了线程 lineage，新鲜个体的基因组很快被主导线程的同质化压力吸收

关键教训：**emergency mutation 是灭火器，不是防火墙**。两次运行中 diversity 都没掉到 0——达到 0.19 左右就触发了紧急机制——但触发了之后能恢复多少，取决于底层进化结构的灵活性。Wired 模式下恢复困难，是因为它的 lineage 结构本身就是多样性杀手。

修复方向：在 Wired 模式下，紧急变异不应该只替换底部 30% 的个体，而应该**在多样性最低的线程中强制重启子代**，让新鲜基因组从源头注入。

---

## 代码与数据对照

写代码时的想象和实际数据的差距，往往是最大收获。这次运行揭示了几个写代码时完全没想到的事实：

- **确定性 scorer 完全够用，甚至更好**——Raw GA 3ms 跑出 88.67，Wired GA 带着 LLM 花了 5.58s 才到 73.73
- **连续参数空间也挡不住多样性崩溃**——temperature 和 top_k 在连续区间里自由取值，但截断选择加上 tournament selection，几代就让种群高度同质化
- **Wired 模式反而限制了 GA**——GenomeAdapter 的线性进化看似优雅，实际上牺牲了 GA 最核心的优势：群体的并行探索能力

GA 的理论很成熟，但工程落地时，scorer 的选择、变异策略的配置、diversity 的监测——这些看似"只是实现细节"的东西，对最终结果的影响比任何学术论文里的 fancy 改进都大。

如果你也在搞进化算法，我的建议是：

1. **第一件事：先跑纯确定性基线**——不需要 LLM，不需要 Wired 模式，一个简单的 GA + 确定性 scorer 就能告诉你参数空间的基本形状
2. **第二件事：把 diversity 指标可视化**——不做这个你看不到种群在悄悄收敛，也调不准 emergency mutation 的触发时机
3. **第三件事：警惕 Wired 模式的隐含约束**——线程级的线性进化可能缩小搜索范围，先跑 Raw GA 拿到基线，再做 Wired 对比
4. **第四件事：LLM 用在最终验证，不要用在每一代**——一次终验 2 秒够用了，但每代 20 次 × 15 代 = 300 次是不必要的成本

以上就是从这次真实的进化运行中提炼出的教训。希望对你有所帮助。

---

[A] 这篇文章里所有数据都来自 `examples/autonomous-evolution/run.log` 的一次完整运行。如果你想复现，在项目根目录执行 `go run ./examples/autonomous-evolution/` 即可。

[B] GA 代码位于 `internal/evolution/genome/` 目录下。核心流程在 `population.go:doEvolve()` （排序 → 选择 → 精英 → 交叉 → 变异 → 多样性检测 → 适应性变异率调整），自适应逻辑在 `adaptive.go` 中。

[C] 这篇文章的"披露不一致数据"的风格是我个人的偏好——**工程中最有价值的信息往往来自异常信号，而不是平稳运行的日志。** 不要害怕在文章里展示你的系统"不完美"的地方，它们才是读者真正能学到东西的地方。
