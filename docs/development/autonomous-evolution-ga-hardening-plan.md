# Autonomous Evolution GA Hardening Plan

## Current Position

The genetic algorithm in the autonomous evolution demo is usable as a 0.1.0 engine:

- It can generate mutations, perform crossover, select candidates, preserve elites, record genealogy, and persist the final strategy.
- The latest demo log shows meaningful learning signals: `temperature` moved from `0.7` to `0.1`, `top_k` moved from `40` to `10`, and raw mode also found a prompt shift from `helpful` to `precise`.
- It is suitable for demos, offline experiments, and small controlled exploration runs.

It is not yet a long-running autonomous production subsystem. The main gaps are best-strategy accounting, scoring consistency, diversity protection, prompt evolution stability, and LLM scoring cost control.

## Target

Evolve the GA subsystem into a reproducible, explainable, cost-controlled, and regression-safe autonomous evolution engine.

The next versions should guarantee:

- The saved strategy is the best-ever strategy, not merely the final generation's best.
- Raw, wired, and scheduler-triggered paths use the same scoring semantics.
- Selection never operates on unevaluated individuals.
- Diversity collapse is detected and corrected.
- LLM scoring has a budget, cache, and fallback path.
- Reports are derived from real run statistics instead of template claims.

## Phase 1: Correctness And Accounting

Recommended version: `0.1.1`.

### 1. Best-Ever Tracking

Problem:

Logs show generations where `best_score` reaches `92` or `95`, while the final saved strategy is lower. This suggests the system may save the current/final generation best rather than the global best seen across the run.

Plan:

- Add `BestEverStrategy` tracking at the `Population` or `Service` layer.
- Update best-ever immediately after each scoring pass.
- Make `BestStrategy()` return best-ever by default.
- Log both `generation_best` and `best_ever`.

Acceptance:

- If generation 7 reaches score `95` and generation 15 reaches score `90`, the saved strategy must be the score `95` strategy.
- Add a regression test where the best score appears mid-run and later disappears from the current population.

### 2. Scoring Lifecycle Consistency

Problem:

Evolution paths can diverge: raw mode, wired mode, and scheduler-triggered mode may score at different points.

Plan:

Standardize each generation as:

1. Select parents from the previous evaluated population.
2. Generate offspring via crossover and mutation.
3. Score newly generated offspring.
4. Apply fitness sharing only to evaluated individuals.
5. Update best-ever.
6. Update diversity and adaptive mutation rate.
7. Emit generation statistics.

Acceptance:

- No individual entering parent selection may have an unevaluated score.
- `ScoreUnevaluated` should only exist briefly between offspring creation and scoring.
- Raw and wired mode should produce equivalent score-accounting behavior under the same seed and scorer.

### 3. Metadata Cleanup

Problem:

The demo logs still contain warning noise such as unknown empty mutation type values.

Plan:

- Give root strategies an explicit mutation type such as `root` or `none`.
- Treat empty mutation type as a known root/default value.
- Warn only on unknown non-empty mutation type strings.

Acceptance:

- Normal demo runs do not emit `unknown mutation type string` for root strategies.

## Phase 2: Search Quality

Recommended version: `0.2.0`.

### 4. Diversity Protection

Problem:

The wired demo reached `diversity=0`, which means the population can collapse into one niche.

Plan:

- Split diversity into:
  - Numeric diversity: `temperature`, `top_k`, and other numeric parameters.
  - Categorical diversity: prompt template, tools, model, and other discrete choices.
  - Lineage diversity: parent/source concentration.
- Add a hard guard for low diversity:
  - Inject fresh mutants when diversity is below threshold.
  - Limit reproduction from a single dominant lineage.
  - Increase mutation rate only when the scorer has enough evaluated individuals.

Acceptance:

- Diversity should not remain at `0` for multiple consecutive generations in a 15-generation run.
- The top lineage should not exceed a configurable share, for example `60%`, unless explicitly allowed.

### 5. Parent Selection

Problem:

Using only the top survivor slice as the breeding pool can create excessive selection pressure.

Plan:

- Prefer tournament selection for parent choice.
- Keep elite preservation separate from parent selection.
- Make these settings configurable:
  - `selection=tournament`
  - `tournament_size=3`
  - `elite_count=2` or `3`
  - `breeding_pool_ratio=0.6`

Acceptance:

- Under fixed seed, best score improves without diversity collapsing early.
- Compare old top-slice selection against tournament selection in benchmarks.

### 6. Prompt Evolution

Problem:

Raw mode found a useful prompt shift, but wired mode showed zero prompt mutations.

Plan:

- Ensure wired mode passes `PromptPool` into the mutator.
- Support explicit prompt crossover modes:
  - `inherit`: inherit the higher-scoring parent prompt.
  - `uniform`: randomly choose either parent prompt.
  - `half_split`: combine prompt halves.
  - `pool_mutation`: choose from configured prompt templates.
- Track prompt mutation contribution separately from parameter mutation contribution.

Acceptance:

- Wired mode should show prompt mutations when `PromptPool` is configured.
- Reports should show whether prompt changes improved or hurt score.

## Phase 3: Scorer Cost Control

Recommended version: `0.2.1`.

### 7. Tiered Scoring

Problem:

LLM scoring makes later generations very slow and expensive.

Plan:

Use a tiered scorer pipeline:

1. Heuristic scorer for all individuals.
2. Cached LLM scorer only for top candidates, novel candidates, or uncertain cases.
3. Arena regression only for final promotion candidates.

Acceptance:

- Each generation has an explicit LLM call budget.
- Logs include `llm_calls`, `cache_hits`, `score_budget_used`, and fallback count.
- LLM scorer failures degrade to heuristic scoring without stopping the run.

### 8. Strategy Hash And Score Cache

Problem:

Repeated equivalent strategies can be scored multiple times.

Plan:

- Define a stable strategy hash from:
  - Params.
  - Prompt template.
  - Tool config.
  - Model config.
- Cache score records with:
  - Score.
  - Scorer type.
  - Timestamp.
  - Sample count.
  - Confidence.

Acceptance:

- Identical strategy hashes reuse cached scores.
- Genealogy records can trace which scorer produced each score.

## Phase 4: Reporting And Guardrails

Recommended version: `0.3.0`.

### 9. Data-Driven Evolution Report

Problem:

The current report is useful, but some conclusions are template-like and can conflict with raw logs.

Plan:

Generate reports from collected run metrics:

- Best-ever trajectory.
- Generation-best trajectory.
- Average and worst score trajectory.
- Diversity trajectory.
- Mutation type contribution.
- Prompt contribution.
- Lineage concentration.
- Scorer cost summary.
- Final saved strategy provenance.

Acceptance:

- Every report claim can be traced to stored metrics.
- The report cannot claim a lower final best if a higher best-ever appeared earlier.

### 10. Failure Protection

Problem:

Long-running autonomous evolution needs explicit stop and fallback behavior.

Plan:

- Stop or pause evolution if every individual is unevaluated.
- Do not save a strategy that fails to beat baseline.
- Trigger stagnation handling after configurable no-improvement windows.
- Degrade scorer mode when LLM failure rate crosses a threshold.
- Emit structured failure reasons.

Acceptance:

- Tests cover scorer failure, all-unevaluated population, best regression, and diversity collapse.

## Version Roadmap

### 0.1.1

- Best-ever tracking.
- Unified scoring lifecycle.
- Root mutation type cleanup.
- Regression tests for mid-run best preservation.

### 0.2.0

- Diversity metric upgrade.
- Tournament parent selection.
- Wired prompt mutation support.
- Prompt contribution reporting.

### 0.2.1

- Strategy score cache.
- LLM scoring budget.
- Tiered scorer pipeline.
- Scorer fallback telemetry.

### 0.3.0

- Data-driven reports.
- Production guardrails.
- Long-running scheduler stability validation.

## Minimum Release Gate

Before promoting the GA subsystem beyond experimental use:

- Fixed-seed runs must be reproducible.
- Final saved strategy must be best-ever.
- Selection must never use unevaluated individuals.
- Raw and wired modes must use consistent scoring semantics.
- Diversity must not remain collapsed.
- LLM scorer must have budget, cache, and fallback behavior.
- These test groups must pass:

```bash
go test ./internal/evolution/... ./api/evolution/... ./internal/arena/...
```
