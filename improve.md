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

### Phase 4: Long-term Memory 🔄 NOT YET IMPLEMENTED

```text
Experience → Distillation → Knowledge → Report
```

**Status**: Not implemented. Future work.

**Remaining tasks**:
- Implement experience distillation to compress repeated patterns
- Create knowledge extraction from aggregated experiences
- Generate human-readable evolution reports
- Integration with existing distillation system in `internal/ares_experience/`

Goal: convert repeated experience into durable knowledge.

## Desired outcome

- Real execution feeds learning. ✅ (Phase 1-3 completed)
- Learning stays cheap and mostly automatic. ✅ (Scheduler triggers autonomous evolution)
- Storage remains replaceable. ✅ (ExperienceStore interface abstracts storage)
- Reports stay human-readable. 🔄 (Phase 4 pending)
- The system improves primarily through execution evidence, with LLM remaining optional. ✅ (EvidenceProvider replaces LLM scoring)

## Implementation Summary (2026-07-01)

**Completed phases**: Phase 1, Phase 2, Phase 3

**Key achievements**:
1. **Experience Pipeline**: Execution data now flows through Normalizer → ExperienceStore → EvidenceAggregator
2. **Evidence-driven Scoring**: MemoryAwareScorer consumes multi-dimensional Evidence (success_rate, latency, error_rate) instead of single-value adjustments
3. **Autonomous Evolution**: Scheduler triggers evolution during idle windows, PromotionLogic manages strategy lifecycle (shadow → champion → demoted)
4. **Thread-safe**: All implementations use sync.RWMutex, pass race detection tests
5. **Plugin-based**: Storage interfaces abstract database implementation, IdleChecker plugin enables custom idle detection

**Remaining work**: Phase 4 (Long-term Memory) - distillation, knowledge extraction, and human-readable reports