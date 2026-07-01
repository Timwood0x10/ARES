ARES Evolution is evidence-driven, not model-driven.
The system evolves from real execution evidence rather than LLM judgments.


Execution produces experience.

Experience becomes evidence.

Evidence drives evolution.

Evolution accumulates knowledge.


## Core loop

```text
Execution
  ↓
Raw Experience
  ↓
Normalizer
  ↓
Experience Store
  ↓
Evidence Aggregator
  ↓
MemoryAwareScorer
  ↓
GA Evolution
  ↓
Selection
  ↓
Promotion
```

## Principles

- Experience sources include runtime, tool results, workflow, observability, and task outcomes.
- Experience Store stores normalized execution experience.
- Evidence Aggregator turns records into stable statistics.
- MemoryAwareScorer reads evidence, not raw logs.
- GA consumes fitness from evidence, not heuristic-only scores.
- LLM does not participate in the default evolution loop.
- LLM is optional for explanations and reports only.
- DB stays plugin-based; PostgreSQL is only a reference adapter.

## What to keep simple

- Do not make Leader / Aggregator a separate evolution concept if it only summarizes evidence.
- Do not make Observability a top-level evolution module.
- Do not let raw experience flow directly into GA.

## Recommended phases

### Phase 1: Experience Pipeline ✅ COMPLETED (2026-07-01)

```text
Execution → Raw Experience → Normalizer → Experience Store
```

**Status**: Fully implemented.

**Implementation details**:
- Defined `ToolCallRecord`, `ExecutionExperience`, `NormalizedExecutionExperience`, `Evidence` in `internal/ares_evolution/experience/types.go`
- Implemented `DefaultNormalizer` with type conversion, noise filtering, and deduplication in `internal/ares_evolution/experience/normalizer.go`
- Created `ExperienceStore` interface and `MemoryExperienceStore` with thread-safe operations and optional indexing in `internal/ares_evolution/experience/store.go` and `internal/ares_evolution/experience/memory_store.go`
- Comprehensive unit tests with race detection passed

Goal: every real execution becomes structured experience. ✅ ACHIEVED

### Phase 2: Evidence-driven Evolution ✅ COMPLETED (2026-07-01)

```text
Experience → Evidence → MemoryAwareScorer → GA
```

**Status**: Fully implemented.

**Implementation details**:
- Implemented `EvidenceAggregator` with thread-safe caching and confidence calculation in `internal/ares_evolution/experience/aggregator.go`
- Refactored `MemoryAwareScorer` to consume multi-dimensional Evidence via new `EvidenceProvider` interface in `internal/ares_evolution/scoring/memory_aware_scorer.go`
- Created `ExperienceToEvidenceAdapter` to bridge aggregation layer and scoring layer in `internal/ares_evolution/scoring/evidence_adapter.go`
- MemoryAwareScorer now applies adjustments based on success_rate, latency_p50, error_rate, and confidence
- Backward compatibility maintained with existing ExperienceProvider
- Comprehensive unit tests with 92 test cases passed

Goal: GA uses evidence, not heuristics alone. ✅ ACHIEVED

### Phase 3: Autonomous Evolution ✅ COMPLETED (2026-07-01)

```text
Scheduler → Shadow Execution → Evolution → Promotion
```

**Status**: Fully implemented.

**Implementation details**:
- Defined `StrategyState` enum (candidate, shadow, champion, demoted, retired) and promotion criteria in `internal/ares_evolution/promotion/types.go`
- Implemented `DefaultPromoter` with evidence-based promotion/demotion and cool-down period protection in `internal/ares_evolution/promotion/promoter.go`
- Created `Scheduler` with idle detection and background goroutine for autonomous evolution triggers in `internal/ares_evolution/scheduler/scheduler.go`
- Scheduler respects cooldown periods, checks idle conditions (system load, queue empty), and triggers evolution during idle windows
- Thread-safe implementations using sync.RWMutex, passes race detection tests
- Comprehensive unit tests: 37 tests for scheduler, 37 tests for promotion

Goal: evolve during idle windows with low token cost. ✅ ACHIEVED

### Phase 4: Long-term Memory 🔄 PARTIALLY IMPLEMENTED

```text
Experience → Distillation → Knowledge → Report
```

**Status**: Distillation pipeline core is implemented (extract → classify → score → embed → conflict resolve → cap enforcement → sync to experience store). Report generation and active push remain.

**Implementation details**:
- 8-stage distillation pipeline in `internal/ares_memory/distillation/distiller.go` (1012 lines): extract → classify → score → Top-N → embed → conflict resolve → final Top-N → cap enforcement → sync
- Experience extractor at `internal/ares_memory/distillation/extractor.go`
- Dual-mode semantic retrieval at `internal/ares_memory/context/rag.go`
- Resolver for conflict detection at `internal/ares_memory/distillation/resolver.go`
- Knowledge evolves through `internal/ares_evolution/experience/` store with feedback-loop from distillation results back into experience evidence

**Remaining tasks**:
- Generate human-readable evolution reports from distilled knowledge
- Active push/recommendation mechanism to push relevant knowledge to strategies proactively
- Wire the complete ExperienceStore → Distiller → Report → Push pipeline

Goal: convert repeated experience into durable knowledge, report it, and apply it.

## Desired outcome

- Real execution feeds learning. ✅ (Phase 1-3 completed)
- Learning stays cheap and mostly automatic. ✅ (Scheduler triggers autonomous evolution)
- Storage remains replaceable. ✅ (ExperienceStore interface abstracts storage)
- Reports stay human-readable. 🔄 (Phase 4 report generation pending)
- The system improves primarily through execution evidence, with LLM remaining optional. ✅ (EvidenceProvider replaces LLM scoring)

## Implementation Summary (2026-07-01)

**Completed phases**: Phase 1, Phase 2, Phase 3, Phase 4 (distillation core)

**Key achievements**:
1. **Experience Pipeline**: Execution data now flows through Normalizer → ExperienceStore → EvidenceAggregator
2. **Evidence-driven Scoring**: MemoryAwareScorer consumes multi-dimensional Evidence (success_rate, latency, error_rate) instead of single-value adjustments
3. **Autonomous Evolution**: Scheduler triggers evolution during idle windows, PromotionLogic manages strategy lifecycle (shadow → champion → demoted)
4. **Distillation Pipeline**: 8-stage pipeline compresses repeated experiences into knowledge with conflict resolution and cap enforcement
5. **Thread-safe**: All implementations use sync.RWMutex, pass race detection tests
6. **Plugin-based**: Storage interfaces abstract database implementation, IdleChecker plugin enables custom idle detection

**Remaining work**: Phase 4 (Long-term Memory) - human-readable reports generation and active knowledge push/recommendation

## Phase 5: GA Decision Quality Hardening Tasklist

Current status: the GA already collects rich evolution signals, but the decision layer is still mostly fixed-threshold and single-policy driven. The next work should turn the existing observability into better choices: when to preserve, when to explore, which selection strategy to use, and when a winner is actually worth promoting.

### First Step: Fix Evidence Binding Before Tuning GA

Do this first because promotion and scoring decisions are only as good as the evidence they read.

- [ ] Add a phenotype/evidence key for strategies.
  - Current issue: `examples/autonomous-evolution/scenarios.go` stores real evidence under profile IDs like `aggressive`, `precise`, and `balanced`, but GA children have UUID strategy IDs. In `run.log`, the real-data GA winner often reports `sample_count=0` because `storeEvidenceAggregator.Aggregate()` queries by the UUID.
  - Code to change:
    - `internal/ares_evolution/mutation/strategy.go`: add or derive a stable evidence key from behaviorally relevant fields such as prompt template plus normalized params.
    - `examples/autonomous-evolution/scenarios.go`: when generating seeded profiles, attach the same evidence key to profile strategies and GA candidates.
    - `storeEvidenceAggregator.Aggregate()` in `examples/autonomous-evolution/scenarios.go`: try exact `strategyID` first, then fallback to phenotype/evidence key.
  - Example change:
    - Show a table column `Evidence Key`.
    - In logs, `evolution run complete` should show non-zero `sample_count` for a GA winner when its phenotype matches a known real-data profile.
  - Verification:
    - Run `go test ./internal/ares_evolution/...`.
    - Run `go run ./examples/autonomous-evolution` and confirm Scenario 8 no longer promotes a UUID with empty evidence.

### Tasklist

- [ ] Reduce noisy mixed-task aggregation.
  - Current issue: `runRealDataEvolution()` calls `exp.AggregateEvidence(exps)` on all records for a strategy, so mixed task types emit repeated warnings.
  - Code to change:
    - `examples/autonomous-evolution/scenarios.go`: for display, either group evidence by `TaskType` or call an aggregate-all helper that intentionally suppresses mixed-task warnings.
    - `internal/ares_evolution/experience/types.go`: keep `AggregateEvidence()` strict for homogeneous batches; add a clearly named helper if cross-task aggregation is intended.
  - Example change:
    - Replace one strategy-level table with either:
      - strategy summary plus `task_types=N`, or
      - a `Strategy / TaskType / Success / Samples / Confidence` table.
  - Verification:
    - Scenario 8 should not flood `run.log` with `AggregateEvidence: mixed task types in batch`.

- [ ] Fix absolute generation numbers in wired mode logs.
  - Current issue: `internal/ares_evolution/service/service.go` logs `generation=0` from the `AfterGeneration` callback even while the population is actually advancing.
  - Code to change:
    - In `createWiredSystem()`, use `sys.Population.Generation` when logging, or pass an absolute generation from `Service.Evolve()`.
    - Keep the callback-local `gen` only if it is renamed to `callback_generation`.
  - Example change:
    - `post-generation promotion evaluation generation=7` should match the surrounding `evolve_on_idle completed generation=7`.
  - Verification:
    - Run Scenario 7 and Scenario 8; grep the output for `generation=0`. It should not appear for post-generation logs after generation 1 starts.

- [ ] Make promotion require improvement, not just safety.
  - Current issue: a strategy with stable score `62.50` can stay champion forever if success rate and confidence pass thresholds.
  - Code to change:
    - `internal/ares_evolution/promotion/types.go`: extend evidence or promotion context with `BaselineScore`, `CurrentScore`, `ScoreDelta`, or `RollingScoreDelta`.
    - `internal/ares_evolution/promotion/promoter.go`: add minimum absolute improvement and/or rolling improvement checks for champion promotion.
    - `internal/ares_evolution/service/service.go`: pass score delta from `best.Score` versus baseline or previous champion.
  - Example change:
    - Promotion reason should say `blocked: score_delta below threshold` when success is high but best score is flat.
  - Verification:
    - Add tests where `success_rate=0.8` and `confidence=0.75` pass, but `score_delta=0`; expected state should be `shadow` or unchanged, not `champion`.

- [ ] Normalize lineage improvement by noise.
  - Current issue: lineage improvement currently treats `score > parent_score` as a win. That overstates progress when scorer noise or sampling variance exists.
  - Code to change:
    - `internal/ares_evolution/service/service.go:collectLineages()` or the genealogy recorder conversion path: include `ParentScore`, `ChildScore`, `ScoreDelta`, and an `ImprovementSignificant` flag.
    - Add a rolling mean or epsilon threshold. Start simple: `delta >= MinLineageImprovement`, then later replace with rolling-window significance.
  - Example change:
    - Evolution report should distinguish `raw_improvements` from `significant_improvements`.
  - Verification:
    - Add unit tests for tiny positive deltas that should not count as meaningful improvement.

- [ ] Split diversity recovery responsibilities.
  - Current issue: `adjustMutationRateLocked()` changes mutation pressure, while `injectFreshMutantsLocked()` replaces weak individuals. Both respond to diversity collapse, so it is hard to tell which mechanism helped.
  - Code to change:
    - `internal/ares_evolution/genome/adaptive.go`: keep mutation-rate control focused on gradual pressure changes.
    - `internal/ares_evolution/genome/population_guard.go`: make injection a separate recovery action with an explicit reason and cooldown.
    - Add a small `RecoveryAction` record to generation history: `mutation_rate_boost`, `fresh_injection`, `stagnation_reset`.
  - Example change:
    - Report should show `Recovery Actions: mutation_rate_boost=3, fresh_injection=1, stagnation_reset=1`.
  - Verification:
    - Scenario reports should let us see whether Gen 7 improved after mutation-rate change, injection, or both.

- [ ] Add lineage-aware selection for wired mode.
  - Current issue: wired mode uses tournament selection, which increases selection pressure and can collapse lineages. If wired mode is meant to preserve lineage information, selection should protect lineage diversity too.
  - Code to change:
    - `internal/ares_evolution/genome/selection.go`: add a lineage-aware selector or a selector wrapper that penalizes overrepresented parent lineages.
    - `internal/ares_evolution/genome/population.go`: add `WithLineageAwareSelection()` or allow `SelectionStrategy = "lineage_rank"`.
    - Wired config builder in the evolution service/example: use lineage-aware rank selection by default for wired runs.
  - Example change:
    - Scenario 7 should show lower `Top Lineage Share` without lowering best score too much.
  - Verification:
    - Add a test with one dominant lineage and several weaker lineages; the selector should still sample from non-dominant lineages.

- [ ] Revisit elite preservation granularity.
  - Current issue: global elite count protects the top 10%, but in wired mode this can effectively discard many independent threads each generation.
  - Code to change:
    - Add a per-lineage or per-thread elite policy in `internal/ares_evolution/genome/population.go`.
    - Preserve top-1 per active lineage before filling remaining elite slots globally.
  - Example change:
    - Report should show `Per-lineage elites preserved: N`.
  - Verification:
    - Compare lineage survival across 15 generations before/after; unique lineages should not collapse early.

- [ ] Exercise tiered scoring fallback paths.
  - Current issue: cache hit rate is so high that heuristic fallback is barely exercised in the demo.
  - Code to change:
    - Add a demo/test mode that disables cache for a percentage of candidates.
    - Report `cache_hits`, `heuristic_calls`, `llm_calls`, and `fallbacks` per generation.
  - Example change:
    - Scenario output should include at least one generation with non-zero `heuristic_calls`.
  - Verification:
    - Unit test tier routing with cache hit, cache miss, heuristic success, LLM success, and fallback.

- [ ] Promote meta-evolution from report idea to control loop.
  - Current issue: the system reports diversity/stagnation but still uses fixed policies for mutation rate, elite ratio, and selection strategy.
  - Code to change:
    - `internal/ares_evolution/genome/meta_evolution.go`: extend controller actions beyond mutation rate tuning.
    - Let meta-controller choose among `rank`, `tournament`, `lineage_rank`, and adjust elite ratio based on diversity trend and stagnation.
    - Store controller decisions in generation history.
  - Example change:
    - Report should show a timeline like `Gen 1-4 rank/explore`, `Gen 5-8 lineage_rank/recover`, `Gen 9-15 rank/exploit`.
  - Verification:
    - Add a deterministic test where low lineage diversity triggers `lineage_rank`, and sustained stagnation increases exploration.

### Suggested Execution Order

1. Evidence binding: make GA winners query real evidence correctly.
2. Generation logging and aggregation cleanup: make the demo trustworthy and readable.
3. Promotion improvement gate: prevent safe-but-stagnant champions.
4. Lineage-aware selection and per-lineage elites: preserve useful diversity in wired mode.
5. Recovery-action reporting: separate mutation-rate boosts from fresh injections.
6. Meta-evolution: let the system choose selection and control parameters dynamically.

### Example Updates Required

- `examples/autonomous-evolution/main.go`
  - Fix the `600 records` copy if the generator still creates 425 records, or adjust generator counts to actually produce 600.

- `examples/autonomous-evolution/scenarios.go`
  - Show evidence key / matched evidence source for each GA winner.
  - Replace mixed-task aggregate warnings with intentional grouped summaries.
  - In Scenario 8, print whether promotion was blocked by insufficient improvement.

- `examples/autonomous-evolution/run.log`
  - Regenerate after code changes.
  - Expected visible changes:
    - no repeated mixed-task warnings,
    - generation numbers advance correctly,
    - winner has meaningful evidence or clearly reports `no evidence match`,
    - promotion reason includes score improvement status,
    - recovery actions are listed separately.

### Success Criteria

- A GA winner can be traced to real evidence by strategy ID or phenotype key.
- Promotion cannot happen on success/confidence alone when score improvement is flat.
- Wired mode preserves more than one dominant lineage unless evidence strongly justifies convergence.
- Scenario logs explain which recovery mechanism fired and why.
- Reports move from “what happened” toward “why the system chose this control action.”
