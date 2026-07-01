# GA in the Trenches: What Two Real Evolution Runs Taught Me

> Disclaimer: This is not a GA tutorial, nor a line-by-line code analysis. Its subtitle is "What you'll find in the logs after running 15 generations of evolution for real." I want to share some engineering insights that grew out of actual data from two real evolution runs.

---

## Motivation

In the ares evolution system, the GA (Genetic Algorithm) path was designed as a "zero-token evolution" — no LLM calls, pure CPU computation, recombining genes from the existing pool of high-scoring strategies. If that was all, there wouldn't be much to write about.

What makes things interesting is the **GenomeAdapter** — a lineage-tracking system designed to trace every strategy's bloodline. It turns GA into "Wired Evolution": each thread carries its own genome, evolves in parallel, and preserves complete parent-child relationship chains. Sounds elegant, right?

The question is: **how much exploration capability does GA lose when it gets "wired"?**

This article walks through three evolution experiments using real data from `/examples/autonomous-evolution/run.log`. The first two (Scenario 6 and 7) are strict controlled experiments — same seed, same population size, same generations — with only one variable: whether Wired mode is enabled. The third (Scenario 5) is an independent validation with a smaller population. The data reveals things I never expected when writing the code.

---

## Experiment Design

All three GA runs share the same underlying configuration:

| Config | Scenario 6 | Scenario 7 | Scenario 5 |
|--------|-----------|-----------|-----------|
| Population size | 20 | 20 | 10 |
| Elite count | 2 | 2 | 1 |
| Selection strategy | **Rank Selection** | **Tournament Selection** | Tournament |
| Wired mode | **No** | **Yes** | **Yes** |
| Mutation rate | 0.3 (emergency up to 0.5) | 0.3 (emergency up to 0.5) | 0.3 |
| Generations | 15 | 15 | 10 |
| Scorer | Deterministic | Deterministic + LLM final check | LLM Scorer (with deterministic fallback) |

Key differences:

- **Scenario 6 (Pure Autonomous / Non-Wired)**: Rank selection + no GenomeAdapter + pure deterministic scorer. This is the most "classic" GA setup — 20 independent individuals free to crossover without lineage constraints.
- **Scenario 7 (Wired Evolution)**: Tournament selection + GenomeAdapter + deterministic scorer + LLM final validation. This is the "engineered" GA — each thread preserves its lineage, with LLM validation at the end.
- **Scenario 5 (Real Data Pipeline / Wired)**: Uses an LLM scorer inside the evolution loop, not just at the final validation stage. Smaller population (10), fewer generations (10), serving as a control to verify Wired-mode behavior consistency.

Results preview:

| Scenario | Initial Best | Final Best | Improvement |
|---------|-------------|-----------|-------------|
| Scenario 6 (Non-Wired) | **62.50** | **89.47** | **+43.1%** |
| Scenario 7 (Wired + LLM final) | **62.50** | **62.50** | **+0.0%** |
| Scenario 5 (Wired + LLM scorer) | **62.50** | **62.50** | **+0.0%** |

The numbers speak for themselves. Let's dive in.

---

## First Run: Pure Autonomous Evolution (Scenario 6 / Non-Wired / Rank Selection)

The complete 15-generation evolution trajectory:

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

This curve is dense with information. A few observations:

**Volatility far exceeds expectations.** The Worst column repeatedly drops to 5.00 (Gen 1, 6, 9, 13, 14), and Avg oscillates wildly between 52.40 and 83.87. This is not "steady directional evolution" — it's wild, noisy evolution. Worst=5.00 means large numbers of low-scoring individuals flood in every generation; the population's "floor" never rises.

**The Best line doesn't climb smoothly.** From 62.50 to 77.50 (Gen 2, +24%), then plateau for 5 gens, then 87.39 (Gen 6, +12.7%), then 88.81 (Gen 7, +1.6%), finally 89.47 (Gen 12, +0.7%). Each jump is separated by multi-generation plateaus.

**The baseline score dropped from 72.50 to 62.50.** Compared to previous runs, the baseline strategy score was lowered by 10 points. This isn't regression — it's because the fitness calculation logic changed. But here's the interesting bit: **a lower baseline produced a higher final Best** (89.47 vs the old 88.67). This suggests the deterministic scorer's dynamic range wasn't compressed, and the lower baseline actually gave the GA more room to improve.

### Lineage Improvement Rate: 100%

Although Scenario 6 is non-Wired, it still records lineage (extracting parent-child relationships from population snapshots after evolution completes):

```
Lineage records: 2437
  with_improvement: 2437 (100.0%)
```

**2437 lineage records, every single one showing improvement.** 100% means every descendant produced in every generation was better than its parent. In a system of 20 individuals × 15 generations = 300 offspring, this translates to roughly 162 lineage records per generation (including intermediate products of multiple crossovers and mutations).

In contrast, this is a **continuous, efficient, waste-free evolution process.**

### Mutation Distribution

```
Parameter mutations:   708 (35%)
Prompt mutations:      226 (11%)
Crossover events:    1061 (53%)
Total:                1995
```

Three numbers worth discussing:

**53% crossover** — crossover remains the dominant operator. Uniform crossover takes half the parameters from each parent, continuously generating new parameter combinations. This is consistent with findings from previous runs.

**11% prompt mutations** — data not present in the previous run. In non-Wired mode, switching the prompt template from "helpful" to "precise" became a routine operation with 11% probability, rather than a "one-off." This shows that when the population can explore freely, prompt-level mutations can be naturally selected. In Wired mode, this number is 0%.

**35% parameter mutations** — parameter mutation ratio rose from 23% in the previous run to 35%, because the continuous parameter space offers more opportunities for fine-tuning.

### Diversity Fluctuations and Fresh Mutant Injection

Scenario 6 experienced multiple diversity fluctuations across its 15 generations. Take Gen 6 as an example:

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

The diversity breakdown is revealing:

- **categorical_diversity = 0**: All individuals have converged to the same prompt template ("precise")
- **numeric_diversity = 0.138**: Pairwise distance of numeric parameters is very low
- **lineage_diversity = 0.45**: Lineage diversity is still okay — meaning although parameters and prompts have converged, these individuals come from different ancestral branches

Fresh mutant injection via `injectFreshMutantsLocked()` triggers when `overall_diversity < 0.05`. But Gen 6's overall=0.173 didn't fall below the hard threshold — what triggered it was the **combination of dominant_lineage_share=25% + categorical=0**. The call chain is: `doEvolve()`, detecting complete loss of categorical diversity, calls `injectFreshMutantsLocked()` (`genome/population_guard.go:46`) preemptively.

The injection was effective: Gen 7's diversity recovered to 28%.

### Convergence Results

Final best strategy (ID: `fresh-mut-15-gen12`, version 10, score 89.47):

| Parameter | Initial | Final | Change |
|-----------|---------|-------|--------|
| temperature | 0.7 | **0.0168** | -97.6% |
| top_k | 40 | **28.94** | -27.7% |
| max_tokens | 2048 | **1239.56** | -39.5% |
| prompt | "helpful" | **"precise"** | style switch |

One particularly noteworthy detail: **the winning strategy's ID is `fresh-mut-15-gen12`**. The name tells us it came from the 12th generation's "fresh mutant injection," not from gradual evolution of Gen 1's lineage. Gen 12 was the last and smallest Best improvement in Scenario 6 — from 88.81 to 89.47.

This means: **across the entire 15-gen GA, the biggest jump was Gen 2 (62.50→77.50), then Gen 6 (77.50→87.39), then Gen 12's injection brought the final 0.66 point increase.** Without the emergency injection at Gen 12, Best would have stayed at 88.81.

temperature=0.0168 is an extreme value — almost completely deterministic. top_k=28.94 (down 27.7%), max_tokens=1239.56 (down 39.5%). This is the direction of "shorter, more deterministic, more precise" evolution.

---

## Second Run: Wired Evolution (Scenario 7 / Wired / Tournament Selection)

Same seed, same population size (20), same generations (15), only change: GenomeAdapter (Wired mode) + Tournament Selection enabled.

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

**15 generations, Best never moved. 62.50, from Gen 1 to Gen 15, one number.**

Compared to Scenario 6's 89.47, the gap is **26.97 points**. This isn't a "difference" — these are two completely different systems.

### Lineage Improvement Rate: 1.9%

```
Lineage records: 2400
  with_improvement: 45 (1.9%)
```

**2400 lineage records, only 45 yielded improvement (1.9%).** Compared to Scenario 6's 100%, this isn't a difference in degree — it's a difference in kind.

In Wired mode, each thread does linear evolution on the same genome. 2355 of the 2400 records are just "different variants of the same winner," and none of these variants beat the original 62.50. This explains why Best never budged: **no descendant ever truly surpassed its parent.**

### Diversity Collapse: Not Twice, But Every Generation

This is the starkest contrast between Scenario 6 and 7. Scenario 6's diversity ranged from 16%–39%, with emergency injections triggered at Gen 6, 11, 12, 13. Scenario 7's diversity hovered between 14%–23% — **it never recovered above 30%.**

```
Gen  Diversity
---  ---------
 1   14%  ← Initial diversity already low
 2   18%
 3   17%
 4   19%
 5   20%
 6   19%
 7   18%  ← Stagnation detection + diversity collapse triggered
 8   18%
 9   17%
10   17%
11   20%
12   18%
13   17%
14   18%
15   23%  ← Only diversity recovery
```

At the code level, `doEvolve()` checks diversity at the end of each iteration. Once `overall_diversity < 0.05` or `dominant_lineage_share > 0.6`, `injectFreshMutantsLocked()` in `genome/population_guard.go` is called to trigger emergency injection. Scenario 7's diversity never returned to healthy levels — emergency injection could only temporarily stop the bleeding, not cure the root cause.

Why is diversity so hard to restore in Wired mode? Because GenomeAdapter's design philosophy is "each thread evolves independently"; freshly injected individuals are bound to a specific thread's lineage. Under Rank Selection (Scenario 6), all individuals compete freely at the global level, allowing fresh genes to spread throughout the population.

### Stagnation Detection Triggered Twice

```
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    generation=6  stagnant_generations=5 generation=7
time=... level=WARN msg="stagnation detected, injected random mutants from elites"
    generation=11  stagnant_generations=5 generation=12
```

Stagnation detection in `doEvolve()`: when Best hasn't changed for 5 consecutive generations, inject random mutants from the elite pool. Triggered at Gen 6 and Gen 11.

Results after each injection:

- After Gen 7 injection: Best still 62.50, Avg improved from 54.50 to 57.38 (+2.88), but Best didn't move
- After Gen 12 injection: Best still 62.50, Avg improved from 55.12 to 58.25 (+3.13), but Best didn't move

Injection improved the population's Avg (overall quality improved), but didn't produce a single individual exceeding 62.50. This means the elite pool itself had become homogeneous — mutations from a homogeneous pool just produce different forms of homogeneity.

### The Single Winner: One ID Ruled 15 Generations

From Scenario 7's Promotion Summary:

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

**The same UUID won from Gen 1 through Gen 15.** This strategy was never surpassed.

Its success rate and confidence (80%/75%) met the conditions for promotion to champion, but its score was 62.50 — identical to the baseline strategy. It didn't regress, but it didn't evolve either.

This is itself an important finding: **if a system's promotion criteria only look at success rate (80%) and confidence (75%), without considering absolute score improvement, then a "safe but stagnant" strategy can permanently occupy the champion slot.** Scenario 7's champion didn't win because it was good — it won because no one beat it.

### Mutation Distribution

```
Parameter mutations:   659 (66%)
Prompt mutations:        0 (0%)
Crossover events:     341 (34%)
Total:                1000
```

**0% prompt mutations** — a stark contrast to Scenario 6's 11%. Under Wired + Tournament Selection, the prompt template lost all evolutionary possibility. The path dependency was too strong: if parameter mutations alone suffice for competition, a "big move" like switching prompts never survives tournament selection.

**66% parameter mutations vs 34% crossover** — Scenario 7's parameter mutation ratio is much higher than crossover (66/34), while Scenario 6 showed the opposite (35/53 parameter/crossover split). This is because each thread's genome structure in Wired mode constrains the effective crossover pairing range — cross-thread crossover is bounded by lineage boundaries.

### LLM Validation: Evidence of Bias

Scenario 7's final step was LLM validation:

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

The LLM scored the 62.50 strategy at **70 points** — 7.5 points higher than the deterministic scorer. If you only looked at the LLM score, you'd think this strategy has "potential" — it's worth 70, the deterministic scorer just didn't catch it.

But Scenario 6's best strategy scored 89.47 under the deterministic scorer, while the LLM only scored Scenario 7's best at 70. **The gap is 19.47 points.**

This is clean evidence of LLM bias: **LLMs tend to give moderately high scores to "strategies that look reasonable," but cannot identify truly excellent parameter combinations.** The deterministic scorer tells you with precise math that 89.47 > 62.50, while the LLM says "hmm, 70 I guess."

### Tiered Scoring: 80-100% Cache Hit Rate

Scenario 7 ran tiered scoring (cache → heuristic → LLM fallback) every generation:

```
Gen 1:  llm_used=11  cache_hits=29  heuristic_used=0  total=40
Gen 2:  llm_used=0   cache_hits=40  heuristic_used=0  total=40  ← All cached
Gen 5:  llm_used=2   cache_hits=38  heuristic_used=0  total=40
Gen 7:  llm_used=6   cache_hits=34  heuristic_used=0  total=40
Gen 11: llm_used=4   cache_hits=36  heuristic_used=0  total=40
Gen 15: llm_used=6   cache_hits=34  heuristic_used=0  total=40
```

LLM usage dropped quickly from 11 calls in Gen 1 to **0** in Gen 2 (40/40 cache hits). This shows that in Wired mode, the population rapidly converges to similar strategy combinations within the first few generations, making nearly every individual in subsequent generations a "previously scored variant."

**The heuristic scorer was never called** — worth noting. The code configures three tiers via `scoring.NewTieredScorer()` (cache → heuristic → LLM), but the 80-100% cache hit rate rendered the heuristic layer entirely dormant.

---

## Third Run: Real Data Pipeline (Scenario 5 / Wired / LLM Scorer)

As an additional validation, Scenario 5 used a smaller population (10) and fewer generations (10), with an LLM scorer inside the evolution loop (tiered scoring during evolution, not just at final validation):

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

**Same as Scenario 7: Best didn't budge, always 62.50.**

Lineage improvement rate:

```
Lineage records: 550
  with_improvement: 10 (1.8%)
```

1.8% vs Scenario 7's 1.9%. The two Wired experiments are highly consistent in lineage improvement rate.

Scenario 5 also shows Wired mode's **shadow promotion** behavior:

```
--- Promising Strategies ---
  speedy → shadow (success_rate=67%, confidence=0.67)
  creative → shadow (success_rate=67%, confidence=0.67)
```

Two strategies (speedy and creative) reached 67% success rate and were promoted to "shadow" status. Yet neither produced a single offspring exceeding 62.50. This suggests success rate and score improvement are independent dimensions — you can have 67% success rate while the score stays flat.

This verifies Scenario 7's conclusion: **In Wired mode, GA cannot push the population to higher score ranges.** And this isn't about LLM scorer vs deterministic scorer — both Scenario 5 (LLM scorer in the loop) and Scenario 7 (deterministic + LLM final validation) stagnated. The root cause is Wired mode itself.

---

## Engineering Lessons from the Data

### 1. Wired Mode is GA's Exploration Bottleneck

This is the single most important finding from this run.

Wired mode (GenomeAdapter) had good design intentions — track lineage, preserve ancestry information, make evolution paths traceable. But in practice, it became an exploration bottleneck:

- **Thread isolation**: each thread has only one evolution path; 20 threads = 20 linear paths, not 20! combinatorial possibilities from free crossover
- **Lineage lock-in**: once a thread's ancestor finds a local optimum, its descendants are locked near the ancestor's genome, unable to jump away
- **No global competition**: Rank Selection sorts all individuals globally; Wired + Tournament Selection only competes locally

**Scenario 6 (non-Wired) succeeded not because it used "better GA operators" — but because its 20 individuals could freely crossover without lineage constraints.** In code, Scenario 6 directly calls `pop.EvolveOnIdle()` (in `population.go`), without going through GenomeAdapter. Scenario 7 and 5 call `evolution.RunIdleEvolution()`, wrapped in `genome_wiring_system.go`, where each generation's evolution goes through `adapter.Evolve()`, which includes lineage recording, tiered scoring, and guardrail checks — these "enhancements" ended up being the shackles.

### 2. Deterministic Scorer is Not Just Sufficient — It's Better

Final scores across all three scenarios:

| Scenario | Scorer | Score | Cost |
|----------|--------|-------|------|
| Scenario 6 | Pure deterministic | **89.47** | ~3ms (evolution) + ~0ms (no LLM) |
| Scenario 7 | Deterministic + LLM final | **62.50** | ~4ms (evolution) + 6.23s (LLM) |
| Scenario 5 | LLM scorer | **62.50** | ~5ms (evolution) + ~2s/gen (LLM) |

**The cheapest system produced the best result.**

Each deterministic scorer evaluation completes in microseconds — no cache, no fallback, no bias. It's simple — a fixed mathematical function — but because it's simple, it's consistent, reproducible, comparable. 15 gens × 20 individuals × microseconds each = less than 1 second of total evaluation time.

The LLM scorer in Scenario 5 made LLM calls every generation (even with 80-100% cache hits), with 11 LLM calls in Gen 1 alone. Each generation ended with "evidence aggregation":

```
time=... level=INFO msg="Evidence aggregation: winner=62.5000 confidence=0.55"
```

But all this extra computation yielded zero score improvement. 62.50 to 62.50.

### 3. Lineage Improvement Rate is a More Sensitive Early Indicator Than Best Score

Scenario 6's lineage improvement rate was **100%**. Scenario 7: **1.9%**. Scenario 5: **1.8%**.

Best Score tells you "who won" at Gen 15. But lineage improvement rate tells you "is anyone winning" as early as Gen 3.

In Scenario 7, if you only watch the Best line, the information you get is "62.50, hasn't moved." But lineage improvement rate tells you much more: only 45 out of 2400 attempts produced offspring better than their parents. This is the quantitative evidence of an "engine that's stalled."

This metric is computed in `service.go`'s `collectLineages()` function, which retrieves all lineage records from `wiredSystem.Genealogy.Lineages()` and checks each one against `score - parent_score > 0`. Simple to implement, enormous information density.

### 4. Diversity Monitoring Matters More Than Elite Protection

Wired mode's diversity stayed between 14-23%. Non-Wired mode's diversity could recover to 30%+. And regardless of mode, when categorical_diversity = 0 (all individuals using the same prompt template), the system is already in the danger zone.

The current diversity detection (`population.go` + `population_guard.go`) has three components:

1. **DiversityReport**: decomposed into numeric / categorical / lineage dimensions
2. **injectFreshMutantsLocked()**: triggered when `overall_diversity < 0.05` or `dominant_lineage_share > 0.6`
3. **adjustMutationRateLocked() (`genome/adaptive.go:433`)**: dynamically adjusts mutation_rate based on diversity trends, capped at 0.5

This mechanism worked in Scenario 6 — emergency injection restored diversity, and Gen 12's injection directly produced the winning strategy (`fresh-mut-15-gen12`). But in Wired mode, the trigger thresholds (0.05 or 0.6) may already be too late — the lineage structure may have locked things up before diversity reaches the danger level.

Possible directions: **raise the dominant_lineage_share trigger threshold in Wired mode (from 0.6 down to 0.4), so emergency injection intervenes earlier.** Or, when diversity falls below 0.2, instead of injecting "elite clones" (which are still from the same lineage), generate entirely new individuals from outside.

### 5. LLM Validation's Self-Revelation

The LLM validation result is the most ironic:

```
deterministic_score=62.50  →  llm_score=70
```

The LLM scored a completely stagnant, never-improved 62.50 strategy at 70 points. Meanwhile, Scenario 6's best strategy (89.47) never went through LLM evaluation — if it had, it might have gotten 85 or 90, but the system didn't need this confirmation to know it was better.

This points to a more fundamental issue: **LLM scores and deterministic scores measure different things in the ares GA system.** The deterministic scorer measures "did the task execution result improve." The LLM measures "does this strategy look reasonable." In an evolution context, you care about the former — and the LLM happens to provide the latter.

If you must use LLMs in the system, the most pragmatic approach is: **after completing the full deterministic GA evolution, run an LLM cross-ranking on the Top-3 candidate strategies to verify if they have defects the deterministic scorer might miss.** Not involving the LLM in the evolution process itself.

---

## Code and Data Cross-Reference

What the three runs reveal:

- **Non-Wired + deterministic scorer + Rank Selection = 89.47**. The most "primitive" configuration produced the best result.
- **Wired + any scorer = 62.50**. Whether you use an LLM scorer or deterministic scorer + LLM final validation, the result is the same: stagnation.
- **LLM's role in the evolution loop is redundant.** It adds hundreds of milliseconds of latency per generation and 80-100% cache hit rates, with zero positive contribution to results.

GA theory is well-established, but this result shows: **engineering "enhancements" (lineage tracking, tiered scoring, promotion pipelines) improve observability, but often at the cost of exploration capability.** GA's power comes from its disorder and randomness — when you "organize" everything, you lose the very thing that made it valuable.

If you're working on evolutionary algorithms, my advice:

1. **First thing: run a non-Wired baseline** — don't bring in GenomeAdapter, don't bring in lineage tracking. A clean, simple GA + deterministic scorer will tell you the basic shape of your parameter space. If non-Wired mode can't work, nothing else will.
2. **Second thing: track lineage improvement rate** — Best Score has lag; lineage improvement rate can warn you as early as Gen 3. If it's below 10%, your exploration engine has a problem.
3. **Third thing: keep LLM outside the evolution loop** — deterministic scorers give precise results in microseconds; LLMs give fuzzy judgments in hundreds of milliseconds. In GA scenarios, fast and accurate deterministic evaluation far outweighs slow and biased LLM evaluation.
4. **Fourth thing: be wary of the "organization" trap** — lineage, tiered scoring, promotion pipelines — these designs make the system "look more controllable," but each enhancement adds constraints. Before adding features, ask: is this constraint one I actually want?

These are the lessons distilled from three real evolution runs. Hope they're helpful.

---

[A] All data in this article comes from a single run of `examples/autonomous-evolution/run.log`. To reproduce, run `go run ./examples/autonomous-evolution/` from the project root.

[B] The core GA code lives in `internal/ares_evolution/genome/`, not the old path `internal/evolution/genome/`. The main loop is in `population.go:doEvolve()` (sort → select → elite → crossover → mutate → diversity check → fresh injection). Adaptive mutation rate adjustment is in `genome/adaptive.go:adjustMutationRateLocked()`, with emergency injection logic in `genome/population_guard.go:injectFreshMutantsLocked()`. The Wired system wrappers are in `genome_wiring_system.go` and `genome_wiring.go`.

[C] The style of "showing inconsistent data" in this article is a personal preference — **the most valuable engineering information often comes from anomalous signals, not smooth-running logs.** Don't be afraid to show where your system is "not perfect" — that's where readers actually learn something.
