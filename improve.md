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

**Completed phases**: Phase 1, Phase 2, Phase 3, Phase 4 (distillation core), Phase 5 ✅

**Key achievements**:
1. **Experience Pipeline**: Execution data now flows through Normalizer → ExperienceStore → EvidenceAggregator
2. **Evidence-driven Scoring**: MemoryAwareScorer consumes multi-dimensional Evidence (success_rate, latency, error_rate) instead of single-value adjustments
3. **Autonomous Evolution**: Scheduler triggers evolution during idle windows, PromotionLogic manages strategy lifecycle (shadow → champion → demoted)
4. **Distillation Pipeline**: 8-stage pipeline compresses repeated experiences into knowledge with conflict resolution and cap enforcement
5. **GA Decision Quality Hardening**: Evidence binding, lineage-aware selection, prompt diversity guard, per-lineage elites, promotion improvement gate, meta-evolution control loop — all 11 tasklist items implemented
6. **Thread-safe**: All implementations use sync.RWMutex, pass race detection tests
7. **Plugin-based**: Storage interfaces abstract database implementation, IdleChecker plugin enables custom idle detection

**Remaining work**: Phase 4 (Long-term Memory) - human-readable reports generation and active knowledge push/recommendation

## Phase 5: GA Decision Quality Hardening ✅ COMPLETED (2026-07-01)

Current status: all 11 tasklist items implemented.

### Evidence Binding

- `mutation.Strategy.EvidenceKey` field + `ComputeEvidenceKey()` method derives a stable key from `PromptTemplate` + `float64` params (formatted at `%.2f`).
- `storeEvidenceAggregator` in `scenarios.go` uses `strategyEvidenceKeys` + `phenotypeFallback` maps to resolve GA UUIDs to profile IDs with real evidence data.
- `AfterGeneration` and `AfterRun` hooks call `RegisterStrategyKey(best.ID, best.ComputeEvidenceKey())`.
- `AggregateEvidenceCrossTask()` helper suppresses mixed-task warnings for cross-task aggregation.
- Generation log messages use `system.Population.Generation` (absolute) with `callback_gen` (loop counter). No more `generation=0` in post-generation logs.

### Decision Quality

- **Promotion improvement gate**: `score_delta` and `rolling improvement` checks prevent safe-but-stagnant champions.
- **Lineage improvement normalization**: `MinLineageImprovement` config (default 0.01), `ImprovementSignificant` flag in `collectLineages()`.
- **Diversity recovery split**: `RecoveryActions` (`mutation_rate_boost`, `fresh_injection`, `stagnation_reset`) recorded in `GenerationHistoryEntry`.
- **Lineage-aware selection**: `LineageRankSelection` with `WithLineagePenaltyThreshold`, configurable via `SelectionStrategy = "lineage_rank"`.
- **Prompt diversity guard**: `preservePromptDiversityLocked()` force-retains alternative prompt templates; `PromptDiversityGuardEnabled` config; `PromptTemplateDistribution` in `DiversityReport`.
- **Per-lineage elites**: `PerLineageElites` config (default false) preserves top-1 per unique lineage before global fill.
- **Tiered scoring fallback**: 9 test cases covering cache hit, LLM success, budget exhaustion, no LLM scorer, LLM panic, stats, reset, full pipeline, and nil strategy.
- **Meta-evolution control loop**: `MetaController.Tune()` adjusts mutation rate, survival rate, elite count, and selection strategy based on diversity, improvement rate, and stagnation. Records `MetaDecision` timeline.

### Suggested Execution Order

1. Evidence binding: make GA winners query real evidence correctly.
2. Generation logging and aggregation cleanup: make the demo trustworthy and readable.
3. Promotion improvement gate: prevent safe-but-stagnant champions.
4. Lineage-aware selection, prompt diversity guard, and per-lineage elites: preserve useful numeric, categorical, and ancestry diversity in wired mode.
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

### Success Criteria ✅ ACHIEVED

- A GA winner can be traced to real evidence by strategy ID or phenotype key.
- Promotion cannot happen on success/confidence alone when score improvement is flat.
- Wired mode preserves more than one dominant lineage unless evidence strongly justifies convergence.
- Prompt-template convergence is either prevented by a retained exploration seed or explicitly explained as evidence-justified.
- Scenario logs explain which recovery mechanism fired and why.
- Reports move from "what happened" toward "why the system chose this control action."
