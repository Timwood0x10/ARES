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

### Phase 1: Experience Pipeline

```text
Execution → Raw Experience → Normalizer → Experience Store
```

Goal: every real execution becomes structured experience.

### Phase 2: Evidence-driven Evolution

```text
Experience → Evidence → MemoryAwareScorer → GA
```

Goal: GA uses evidence, not heuristics alone.

### Phase 3: Autonomous Evolution

```text
Scheduler → Shadow Execution → Evolution → Promotion
```

Goal: evolve during idle windows with low token cost.

### Phase 4: Long-term Memory

```text
Experience → Distillation → Knowledge → Report
```

Goal: convert repeated experience into durable knowledge.

## Desired outcome

- Real execution feeds learning.
- Learning stays cheap and mostly automatic.
- Storage remains replaceable.
- Reports stay human-readable.
- The system improves primarily through execution evidence, with LLM remaining optional.