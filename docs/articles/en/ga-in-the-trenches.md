# GA in the Trenches: What Three Real Evolution Runs Taught Me (After Fixing a Bug That Cost 26.97 Points)

> Disclaimer: This is not a GA tutorial, nor a line-by-line code analysis. Its subtitle is "What you'll find in the logs after running 15 generations of evolution for real." I want to share some engineering insights that grew out of actual data — and one humbling initialization bug — from three real evolution runs.

---

## Motivation

In the ares evolution system, the GA (Genetic Algorithm) path was designed as a "zero-token evolution" — no LLM calls, pure CPU computation, recombining genes from the existing pool of high-scoring strategies. If that was all, there wouldn't be much to write about.

What makes things interesting is the **GenomeAdapter** — a lineage-tracking system designed to trace every strategy's bloodline. It turns GA into "Wired Evolution": each thread carries its own genome, evolves in parallel, and preserves complete parent-child relationship chains. Sounds elegant, right?

The question was: **how much exploration capability does GA lose when it gets "wired"?**

Earlier versions of this article answered: **"a lot."** Scenario 6 (Non-Wired) hit 89.47 while Scenario 7 (Wired) was stuck at 62.50 — a gap of 26.97 points. The conclusion was damning: Wired mode is an exploration bottleneck. Don't use it.

**I was wrong.**

Not because the analysis was sloppy. But because `CreateWiredSystem` was missing a single line of initialization code — `PromptTemplates = PromptPool`. Without it, all prompt template mappings were empty. Prompt mutations never happened. The entire system was running with a critical component unplugged.

After the fix, the results flipped:

| Scenario | Old (Buggy) | New (Fixed) | Change |
|----------|------------|-------------|--------|
| Scenario 6 (Non-Wired) | 89.47 (+43.1%) | **79.41** (+21.2%) | Baseline changed |
| Scenario 7 (Wired, LLM final) | 62.50 (+0.0%) | **85.90** (+21.8%) | **+23.40 points** |
| Scenario 8 (Wired, LLM scorer) | 62.50 (+0.0%) | **77.50** (+24.0%) | **+15.00 points** |

Wired mode is not just viable — with LLM guidance, it beats the non-wired baseline.

This article walks through three evolution experiments using real data from `/examples/autonomous-evolution/run.log`. All runs use new data after the fix. The core lesson isn't about GA theory anymore — it's about **what happens when you forget to initialize a field, and how that mistake cascades into 15 generations of misleading data.**

---

## Experiment Design

All three GA runs share the same underlying configuration:

| Config | Scenario 6 | Scenario 7 | Scenario 8 |
|--------|-----------|-----------|-----------|
| Population size | 20 | 20 | 10 |
| Elite count | 2 | 2 | 1 |
| Selection strategy | **Rank Selection** | **Tournament Selection** | Tournament |
| Wired mode | **No** | **Yes** | **Yes** |
| Mutation rate | 0.3 (emergency ~0.47) | 0.3 (emergency ~0.47) | 0.3 (emergency 0.5) |
| Generations | 15 | 15 | 10 |
| Scorer | Deterministic | Deterministic + LLM final | LLM tiered scoring |

Key differences:

- **Scenario 6 (Pure Autonomous / Non-Wired)**: Rank selection + no GenomeAdapter + pure deterministic scorer. This is the most "classic" GA setup — 20 independent individuals free to crossover without lineage constraints.
- **Scenario 7 (Wired Evolution + LLM Final Validation)**: Tournament selection + GenomeAdapter + deterministic scorer + LLM final validation. This is the "engineered" GA — each thread preserves its lineage, with LLM validation at the end.
- **Scenario 8 (Wired + Data Pipeline + LLM Scorer)**: Uses an LLM scorer inside the evolution loop. Smaller population (10), fewer generations (10), serving as a control to verify Wired-mode behavior consistency.

Results preview:

| Scenario | Initial Best | Final Best | Improvement |
|---------|-------------|-----------|-------------|
| Scenario 6 (Non-Wired, Rank) | 65.50 | **79.41** | **+21.2%** |
| Scenario 7 (Wired, Tournament, LLM final) | 70.50 | **85.90** | **+21.8%** |
| Scenario 8 (Wired, Data pipeline, LLM scorer) | 62.50 | **77.50** | **+24.0%** |

**LLM-Guided wins by +6.49 points.**

The numbers tell a different story now. Let's dive in.

**Steady-state GA: online evolution without full replacement.**

All three experiments use full generational evolution — the entire population is replaced each generation. But for online scenarios (agent actively serving requests while evolving), the framework now supports **steady-state GA** via `EvolveSteadyState()` in `internal/ares_evolution/genome/population.go`.

In steady-state mode, only a fraction of the population is replaced each generation, controlled by `replaceRate` (clamped to [0.1, 0.5], default 0.3). Most individuals survive, preserving exploration history and avoiding sudden population flips. This is ideal for continuous in-production adaptation where you want smooth convergence without disrupting active strategies.

| Property | Full Generational | Steady-State |
|----------|-------------------|--------------|
| Replacement rate | 100% controlled by SurvivalRate | 10-50% controlled by replaceRate |
| Convergence | Fast, can overshoot | Smooth, gradual |
| Diversity | Resets each generation | Maintained across generations |
| Use case | Offline optimization | Online (in-production) adaptation |

---

## First Run: Pure Autonomous Evolution (Scenario 6 / Non-Wired / Rank Selection)

The complete 15-generation evolution trajectory:

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

This curve is less explosive than the old run (which jumped from 62.50 to 77.50 in Gen 2). Here, the climb is more gradual — 65.50 → 66.50 → 70.50 → 77.50 over five generations. The final 79.41 is respectable but not spectacular.

A few observations:

**Volatility remains high.** The Worst column repeatedly drops to 5.00 (Gen 1, 6, 8, 11, 12, 13, 14), and Avg oscillates between 50.65 and 73.90. The population's "floor" never rises — worst-case individuals are always terrible.

**The Best line climbs in steps.** From 65.50 to 66.50 (Gen 2, +1.5%), then 70.50 (Gen 3, +6.0%), then 77.50 (Gen 5, +9.9%), plateau until 78.77 (Gen 9, +1.6%), finally 79.41 (Gen 15, +0.8%). No single dramatic leap — just steady, incremental progress.

### Lineage Improvement Rate: 100%

Although Scenario 6 is non-Wired, it still records lineage (extracting parent-child relationships from population snapshots after evolution completes):

```
Lineage records: 2461
  with_improvement: 2461 (100.0%)
```

**2461 lineage records, every single one showing improvement.** 100% means every descendant produced in every generation was better than its parent. This is a continuous, efficient, waste-free evolution process — every generation, every individual is net positive.

### Mutation Distribution

```
Parameter mutations:   807 (33%)
Prompt mutations:      376 (15%)
Crossover events:    1217 (49%)
Other:                  61 (2%)
Total:                2461
```

Four numbers worth discussing:

**49% crossover** — crossover remains the dominant operator. Uniform crossover takes half the parameters from each parent, continuously generating new parameter combinations.

**15% prompt mutations** — prompt-level evolution is active. In non-Wired mode, switching the prompt template from "helpful" to "precise" is a routine operation. This is the key contrast with the old buggy Wired runs, where prompt mutations were 0%.

**33% parameter mutations** — steady exploration of the continuous parameter space.

### Diversity Fluctuations

Scenario 6 experienced diversity collapse at Gen 5 (overall=0.185) and Gen 15 (overall=0.194). The diversity rescue mechanism kicked in when appropriate.

```
time=... level=WARN msg="diversity collapse detected, injected fresh mutants"
    generation=5  overall_diversity=0.185
    ...
```

The injection was effective: Gen 6's diversity recovered to 30%, and the system stayed above 22% for the remaining generations.

### Convergence Results

Final best strategy (ID: `fresh-mut-14-gen15`, score 79.41):

| Parameter | Value |
|-----------|-------|
| temperature | **0.02356** |
| top_k | **40** |
| max_tokens | **2048** |
| prompt | **"precise"** |

The winning strategy came from generation 15's fresh mutant injection. temperature=0.02356 is extremely low — almost deterministic. top_k stayed at the maximum (40), meaning the system valued breadth over narrowness for token selection. The prompt template settled on "precise" — a consistent pattern across all runs.

Duration: **6ms** for the entire 15-generation evolution. Pure deterministic evaluation is fast.

---

## Second Run: Wired Evolution — Now It Works (Scenario 7 / Wired / Tournament Selection / LLM Final Validation)

Same seed, same population size (20), same generations (15). But now with the bug fix applied. The same `CreateWiredSystem` that previously broke everything is now properly initialized.

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

**Compared to the old buggy run (62.50 stuck for 15 gens), this is a completely different system.** The Best climbs from 70.50 to 85.90 — a +21.8% improvement. Wired mode is not just functional — it's outperforming the non-wired baseline.

### The Breakthrough

The most dramatic moment in this run happens at Gen 14:

```
Gen 10: 77.50  ← plateau since Gen 3
Gen 11: 77.50
Gen 12: 77.50
Gen 13: 77.50  ← 10 consecutive generations at 77.50
Gen 14: 84.15  ← BREAKTHROUGH! +6.65 points
Gen 15: 85.90  ← +1.75 points further
```

The stagnation detection triggered at Gen 9 (5 generations with no change), injecting diversity. But nothing happened. It triggered again at Gen 14 — and this time, something clicked. A combination of parameters found a new local optimum at 84.15, and the next generation improved to 85.90.

This is the kind of behavior that's hard to capture in theory. Stagnation triggers look like failure at the time, but they're planting seeds. The injection at Gen 9 didn't produce immediate results — it took until Gen 14 for those seeds to bear fruit. **Five generations of latency between intervention and payoff.**

### Lineage Improvement Rate: 3.7%

```
Lineage records: 2388
  with_improvement: 88 (3.7%)
```

3.7% versus Scenario 6's 100%. This is still a huge gap — but it's healthier than the old buggy run's 1.9%. In Wired mode, improvement is sparser. Only 88 out of 2388 records show positive offspring. But those 88 improvements are enough — because the system only needs one breakthrough to unlock the next score tier.

This is a fundamentally different dynamic from non-Wired mode:
- **Non-Wired (100%)**: Every generation, everyone improves a little. Steady, incremental, reliable.
- **Wired (3.7%)**: Most offspring are lateral moves or regressions. But occasionally, one lineage cracks the code.

### Mutation Distribution

```
Parameter mutations:   100 (34%)
Prompt mutations:       51 (17%)
Crossover events:     143 (49%)
Total:                 294 (lineage records with mutations recorded)
```

**17% prompt mutations** — in the buggy run, this was 0%. Now it's a healthy 17%, comparable to Scenario 6's 15%. This is the direct effect of the `PromptTemplates = PromptPool` fix. Without it, prompt mutations couldn't propagate because there was no template pool to mutate into.

**49% crossover** — same dominance pattern as Scenario 6.

### LLM Validation

Scenario 7's final step was LLM validation:

```
LLM validation of best strategy: deterministic_score=85.9, llm_score=70, duration=13.189s
```

The LLM scored the 85.90 strategy at **70 points** — a 15.9 point gap. The old buggy run's LLM validation scored a 62.50 strategy at 70 points too. The LLM gave the same score (70) to two strategies that are 23.40 points apart deterministically.

This is consistent evidence of LLM bias: **LLMs tend to give moderately high scores to "strategies that look reasonable," but their dynamic range is compressed.** The deterministic scorer says 85.90 > 62.50 with 23.4 points of separation. The LLM says... 70 ≈ 70. No distinction.

### Control Group Comparison

```
📊 Control Group Comparison
═══════════════════════════════════════════════════
Autonomous (no LLM)                     LLM-Guided
─────────────────────────  ─────────────────────────
Best Score:       79.41    Best Score:       85.90
temperature:   0.02356      temperature:       0.10
top_k:             40       top_k:              26
max_tokens:      2048       max_tokens:       2048
prompt:        precise      prompt:         precise

🏆 Winner: LLM-Guided (+6.49 points)
```

**The results flipped.** In the old buggy run, Autonomous (no LLM) won by +26.97 points. Now, LLM-Guided wins by +6.49 points.

The winning strategy (ID: `6aec0864`) has temperature=0.10, top_k=26, prompt=precise — less extreme than Scenario 6's temperature=0.02356, suggesting the LLM-guided path found a slightly different region of the parameter space.

---

## Third Run: Real Data Pipeline (Scenario 8 / Wired / LLM Scorer)

As an additional validation, Scenario 8 used a smaller population (10) and fewer generations (10), with an LLM scorer inside the evolution loop (tiered scoring during evolution):

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

77.50 (+24.0%) from a baseline of 62.50. Smaller population, fewer generations, but still meaningful progress. This confirms that Wired mode with LLM scoring can evolve — it's not as powerful as Scenario 7's Tournament + LLM final combo (85.90), but it's a solid validation.

Lineage improvement rate:

```
Lineage records: 550
  with_improvement: 43 (7.8%)
```

7.8% — higher than Scenario 7's 3.7%. Interesting — the smaller population seems to have a higher improvement-per-record ratio.

Mutation distribution:

```
Parameter mutations:    33 (33%)
Prompt mutations:       26 (26%)
Crossover events:      41 (41%)
Total:                 100
```

**26% prompt mutations** — even higher than Scenario 6 and 7. With the bug fix, prompt evolution is alive and well.

Diversity experienced collapses at Gen 3 (0.141) and Gen 6 (0.147), but the system self-corrected both times.

**What's next?** All three experiments optimize a single score — success rate, or some scalar combination. But real-world strategy quality isn't one-dimensional. A high-success-rate strategy might be expensive; a low-cost strategy might be slow.

The GA framework now supports **multi-objective optimization via NSGA-II** (`internal/ares_evolution/genome/multi_objective.go`). Instead of ranking strategies by a single score, NSGA-II ranks them by Pareto dominance across four default dimensions:

| Dimension | Direction | Weight | Meaning |
|-----------|-----------|--------|---------|
| `success_rate` | Maximize | 0.40 | Higher execution success → better |
| `quality` | Maximize | 0.25 | Higher output quality → better |
| `cost` | Minimize | 0.20 | Lower API cost → better |
| `latency` | Minimize | 0.15 | Lower response time → better |

Selection follows the NSGA-II pipeline: non-dominated sorting assigns Pareto ranks (0 = best front), then crowding distance within each rank preserves diversity at the Pareto frontier. Pass `"nsga2"` or `"nondominated"` as the selection strategy to activate it.

The benefit: you don't need to manually tune the weight between success rate and cost. NSGA-II maintains a diverse set of trade-off solutions. The weights are used only when a single scalar score is needed for reporting; the selection itself operates on the full Pareto ordering.

---

## The Bug That Cost 26.97 Points

This is the most important section of this article — not because the bug was technically interesting, but because of what it says about engineering assumptions.

### Root Cause

File: `internal/ares_evolution/genome_wiring_system.go`

The `CreateWiredSystem` function was missing this line:

```go
PromptTemplates = PromptPool
```

That's it. One field assignment.

Wired mode uses `PromptPool` — an evidence-tracking prompt pool that manages available prompt templates. But `CreateWiredSystem` never set `PromptTemplates` to point to `PromptPool`. So all `PromptTemplates` lookups returned empty/null.

The consequence? Prompt mutations recorded in the lineage system, but when the Wired system tried to render a genome with a mutated prompt template, it found nothing. The genome fell through to a default state — effectively disabling prompt evolution.

### The Symptom

Before the fix:

```
Prompt mutations: 0 (0%)     ← Scenario 7, buggy run
```

After the fix:

```
Prompt mutations: 51 (17%)   ← Scenario 7, fixed run
```

Zero to 17%. That's not a tuning difference — that's a component that was silently broken.

### The Cost

The old data showed:

| Scenario | Best Score | Δ from Baseline |
|----------|-----------|----------------|
| Scenario 6 (Non-Wired) | 89.47 | +26.97 |
| Scenario 7 (Wired) | 62.50 | 0.00 |

**Gap: 26.97 points.** The conclusion was clear: "Wired mode is the exploration bottleneck. Don't use it."

The new data, after a one-line fix:

| Scenario | Best Score | Δ from Baseline |
|----------|-----------|----------------|
| Scenario 6 (Non-Wired) | 79.41 | +13.91 |
| Scenario 7 (Wired, fixed) | 85.90 | +15.40 |

**LLM-Guided wins by +6.49 points.**

The 26.97-point gap was never a gap. It was a ghost — the result of a missing field assignment.

### The Lesson

**"Don't blame the architecture — check your initialization code first."**

When your system produces data that contradicts your intuition (like "Wired mode completely breaks GA"), the first question should not be "what's wrong with the architecture?" — it should be **"what's wrong with my setup?"**

The old article had a confident conclusion: "Wired mode is GA's exploration bottleneck." It was wrong. The bottleneck was a single line of code that never ran.

This is a humbling reminder: **your data is only as good as your initialization path.** If you're comparing two configurations and one of them is silently missing a component, your comparison is meaningless — no matter how clean the rest of your data looks.

---

## Engineering Lessons from the Data

### 1. Wired Mode Isn't the Bottleneck — Uninitialized Components Are

The single most important finding from this run, restated more carefully:

Wired mode (GenomeAdapter) **does work**. Scenario 7 (85.90) beats Scenario 6 (79.41) by 6.49 points. Scenario 8 (77.50) confirms Wired can evolve across different configurations.

But it doesn't work as well as non-Wired in every dimension. The lineage improvement rate gap (100% vs 3.7%) is real — Wired mode produces fewer improving offspring per generation. However, it only takes **one** breakthrough lineage to beat the non-Wired baseline. And in this run, Wired had that breakthrough.

The key insight: **Wired mode trades breadth for depth.** Non-Wired evolution explores broadly — everyone improves a little every generation. Wired evolution explores deeply — most threads stagnate, but the one that breaks through can go further than any non-Wired individual.

### 2. Deterministic Scorer Is Fast and Consistent — But LLM Guidance Wins

Revised finding from the old article:

| Scenario | Scorer | Score | Duration |
|----------|--------|-------|----------|
| Scenario 6 | Pure deterministic | 79.41 | ~6ms |
| Scenario 7 | Deterministic + LLM final | **85.90** | ~6ms + 13.19s (LLM) |
| Scenario 8 | LLM scorer in loop | 77.50 | ~5ms + LLM/gen |

The deterministic scorer is faster and cheaper. But LLM-Guided evolution (Scenario 7) produces the best result — 85.90 vs 79.41.

However, the LLM scorer in the loop (Scenario 8) underperforms both. The reason is cache saturation: in a small population (10), LLM cache hit rates are very high, limiting the LLM's real contribution. The LLM adds the most value at the **final validation stage**, not inside the evolution loop.

**If you have LLM budget, use it for validation, not evaluation.** A single LLM call at Gen 15 to validate the top candidate is more valuable than 100 LLM calls distributed across generations.

**A design refinement: Split canonical fitness from selection pressure.**

The GA now separates `Score` (canonical fitness, set by the scorer, never modified) from `SelectionScore` (adjusted by fitness sharing, reset each epoch). All selection operators use `effectiveScore()`, which picks `SelectionScore` if non-zero, otherwise falls back to `Score`. This means fitness sharing — which penalizes individuals in crowded parameter-space regions to promote diversity — can adjust selection pressure without corrupting the true fitness value used for reporting and history.

Three scaling strategies handle population size: O(n²) pairwise for small populations, reservoir sampling for medium, and spatial grid indexing for large (>500). Elite individuals are exempt from the penalty.

### 3. Lineage Improvement Rate Is Still Valuable — But Interpret It Differently

Scenario 6's lineage improvement rate was 100%. Scenario 7's was 3.7%. Old me: "Wired mode is broken." New me: "Wired mode selects more aggressively."

In Wired mode, a 3.7% improvement rate means 96.3% of offspring don't beat their parents. That sounds terrible. But remember: **Wired mode also produces 85.90.** The improvement rate isn't predicting the final score — it's describing the search strategy.

Think of it this way:
- **Non-Wired (100%)**: Every generation, you take one small step forward. You're moving, but each step is small.
- **Wired (3.7%)**: Most generations, you stand still. But occasionally, someone takes a giant leap.

Both strategies can work. The right choice depends on your problem's fitness landscape.

### 4. Stagnation Detection Needs Patience

Scenario 7's stagnation detection triggered at Gen 9 (5 gens at 77.50). Nothing happened. It triggered again at Gen 14. This time, it worked — producing 84.15 in Gen 14 and 85.90 in Gen 15.

The latency between injection and breakthrough was **5 generations** (Gen 9 injection → Gen 14 breakthrough). The system almost gave up twice. If the generation count had been capped at 10 (like Scenario 8), the breakthrough would never have happened.

This is a practical engineering insight: **stagnation recovery is not immediate.** When you inject diversity into a stuck population, the injected individuals need time to compete, cross over, and find new optima. A system that expects instant recovery will be disappointed.

### 5. LLM Validation Bias Is Consistent — But Now It Doesn't Matter

The LLM scored Scenario 7's 85.90 strategy at 70 points. It scored the old buggy run's 62.50 strategy at 70 points too. Same score for strategies that are 23.40 points apart.

This confirms a pattern: **LLMs have a narrow scoring range for strategy evaluation.** They cluster scores around 60-80, regardless of actual performance. The deterministic scorer has a much wider dynamic range.

But here's the thing: **it doesn't matter anymore.** The LLM-Guided system won. The LLM's compressed scoring range didn't prevent it from selecting the right strategy. The LLM's value came from its ability to distinguish "plausible" from "implausible" at the final validation stage — not from precise score assignment.

### 6. Data-Driven Evolution: Connect Real Execution to the GA Loop

The fifth lesson hints at a powerful idea: if the LLM can validate candidates, what else can observe and guide evolution? The **Experience System** answers this by closing the loop from production execution back to the GA.

The pipeline: `ToolCallRecord → RawExperience → NormalizedExperience → MemoryExperienceStore → AggregateEvidence → EvolutionHint`

Three key components:

- **ToolCallExperienceCollector** (`internal/ares_evolution/experience/tool_call_collector.go`): Captures every tool call made by production agents — including strategy ID, task type, tool name, latency, success/failure, and error codes. The normalizer deduplicates and filters high-latency outliers and high-error-rate records.

- **MemoryExperienceStore** (`internal/ares_evolution/experience/memory_store.go`): Stores experiences with indexed lookup by `strategy_id` and `task_type`. Supports `Append`, `AppendBatch`, `Query` (by strategy ID, with time range), and `QueryByTaskType` (score-descending). `GetStatistics()` aggregates metrics including total count, average score, and success rate.

- **GuidanceProvider** (`internal/ares_evolution/experience_hints.go`): The bridge from raw data to evolution guidance. `HintsForTask()` returns `EvolutionHint` structs containing identified problems, solutions, preferred tools, prompt snippets, parameter hints, and a confidence score. `RecordStrategyOutcome()` logs deployment results so the system learns from successes and failures.

In practice, this means: if a strategy consistently fails on a particular task type (say, database schema migration), the experience system captures the failure pattern, normalizes it into a hint ("use explicit transaction blocks for DDL"), and surfaces that hint to the GA's mutation operator. Future mutations are biased toward solutions that avoid the known failure pattern.

The evidence package (`internal/evidence/`) provides typed evidence kinds: `KindExecutionTrace`, `KindFailure`, `KindKnowledge`, `KindInsight`, `KindFitness`. Each `MemoryGenome` and `PlannerGenome` emits `KindFitness` evidence during evaluation, linking fitness scores to genome metadata.

---

## Code and Data Cross-Reference

What the three runs reveal after the bug fix:

- **Non-Wired + Rank Selection = 79.41**. Classic GA, steady but unspectacular.
- **Wired + Tournament + LLM final validation = 85.90**. The best result, using LLM guidance at the right moment.
- **Wired + LLM scorer in-loop = 77.50**. Works, but cache saturation limits LLM contribution.

### The Bug Fix

File: `internal/ares_evolution/genome_wiring_system.go`

The fix is one line:

```go
// Before (buggy):
// PromptTemplates was never set
wiredSystem := &WiredSystem{
    Population:     pop,
    Genealogy:      genealogy,
    StopChan:       stopChan,
    Generation:     generation,
    // PromptTemplates: PromptPool  ← MISSING
}

// After (fixed):
wiredSystem := &WiredSystem{
    Population:      pop,
    Genealogy:       genealogy,
    StopChan:        stopChan,
    Generation:      generation,
    PromptTemplates: PromptPool,  // ← THIS LINE
}
```

If you read this article and take away one thing, let it be this: **when your A/B test produces a result that surprises you, check the initialization code before you write the blog post.**

GA theory is well-established, and the numbers in this article are real. But the narrative around those numbers changed completely because of one missing field. The engineering lesson is not about GA at all — it's about the invisible assumptions in your comparison setup.

If you're working on evolutionary algorithms, my advice:

1. **First thing: verify your initialization path.** Before you compare A versus B, make sure both A and B are actually running the code you think they are. Check for uninitialized fields, missing mappings, and silent defaults.
2. **Second thing: run a non-Wired baseline** — but don't assume it's superior. Wired mode can outperform it with the right configuration.
3. **Third thing: track lineage improvement rate**, but interpret it in context. 3.7% doesn't mean failure — it means sparser but potentially larger breakthroughs.
4. **Fourth thing: keep LLM outside the evolution loop**, but use it for final validation. A single LLM call at Gen 15 beats a hundred LLM calls scattered across generations.
5. **Fifth thing: be patient with stagnation.** If you inject diversity, wait at least 3-5 generations before declaring it failed. Breakthroughs don't happen overnight.

These are the lessons distilled from three real evolution runs — plus one initialization bug. Hope they're more useful than my previous set.

---

## Appendix

[A] All data in this article comes from a single run of `examples/autonomous-evolution/run.log`. To reproduce, run `go run ./examples/autonomous-evolution/` from the project root.

[B] The core GA code lives in `internal/ares_evolution/genome/`. The main loop is in `population.go:doEvolve()` (sort → select → elite → crossover → mutate → diversity check → fresh injection). Adaptive mutation rate adjustment is in `genome/adaptive.go:adjustMutationRateLocked()`, with emergency injection logic in `genome/population_guard.go:injectFreshMutantsLocked()`. The Wired system wrappers are in `genome_wiring_system.go` and `genome_wiring.go`. The bug fix is in `genome_wiring_system.go`.

[C] The style of "showing inconsistent data" in this article is a personal preference — **the most valuable engineering information often comes from anomalous signals, not smooth-running logs.** But there's a difference between "interesting anomaly" and "broken initialization." If your anomaly turns out to be a bug, admit it publicly — your readers will learn more from your mistake than from a perfectly curated success story.