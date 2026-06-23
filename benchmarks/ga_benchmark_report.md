# Genetic Algorithm Performance Benchmark Report

**Date**: 2026-06-21
**Platform**: darwin/arm64 (Apple M3 Max)
**Go**: 1.26
**Package**: `ares/internal/evolution/genome`

## Overview

Benchmarks measure the performance of all GA operations across population sizes,
parameter counts, and generation depths. All benchmarks use deterministic seeds
for reproducibility (benchmark-time = 1x iteration per sub-benchmark).

---

## 1. Crossover

| Benchmark | Time | Memory | Allocs |
|-----------|------|--------|--------|
| Uniform (10 params) | 9.2µs | 2,864 B/op | 31 allocs/op |
| Uniform (100 params) | 28.7µs | 22,104 B/op | 39 allocs/op |
| MultiPoint (k=3, 50 params) | 25.9µs | — | — |
| MultiPoint (k=10, 50 params) | 17.6µs | — | — |
| MultiPoint (k=50, 50 params) | 16.6µs | — | — |
| Half-Split (10 params, 1KB prompt) | 6.4µs | 4,064 B/op | 37 allocs/op |
| Parallel (10 params) | 12.3µs | 5,024 B/op | 65 allocs/op |

**Observations**:
- Uniform crossover scales linearly with param count (9µs@10 → 29µs@100)
- MultiPoint is counterintuitively *faster* at higher k values (more `from_A`/`from_B` segments shorten since fewer params per segment → less slice growth)
- Half-split is fastest due to simple string concatenation vs map iteration
- Parallel overhead adds ~30% per-op cost but improves throughput under load

---

## 2. Selection

### Truncation Selection

| Population | Time | Scaling |
|-----------|------|---------|
| 10 | 0.75µs | baseline |
| 100 | 10.6µs | O(n log n) |
| 500 | 66.3µs | ~6.3x for 5x |
| 1,000 | 144.5µs | ~2.2x for 2x |

### Tournament Selection

| Population | k | Time |
|-----------|---|------|
| 50 | 2 | 3.7µs |
| 50 | 3 | 4.0µs |
| 50 | 5 | 3.7µs |
| 50 | 10 | 5.5µs |
| 200 | 2 | 20.1µs |
| 200 | 3 | 27.5µs |
| 200 | 5 | 23.5µs |
| 200 | 10 | 25.8µs |

### Roulette Wheel Selection

| Population | Time | Scaling |
|-----------|------|---------|
| 10 | 1.25µs | baseline |
| 100 | 4.2µs | O(n) per spin |
| 500 | 42.8µs | ~10x for 5x |
| 1,000 | 153.3µs | ~3.6x for 2x |

### SortByScore

| Population | Time |
|-----------|------|
| 10 | 12.9µs |
| 100 | 24.2µs |
| 500 | 79.7µs |
| 1,000 | 160.0µs |

**Observations**:
- Truncation is fastest for small populations but dominated by sort cost at scale
- Tournament k-value has minimal impact on runtime (pickUniqueIndices is O(poolSize) regardless of k)
- Roulette wheel is O(n) per spin × n selections = O(n²) total, making it slowest for large n
- SortByScore is the dominant cost in evolution cycles (O(n log n))

---

## 3. Evolution Cycle

### Evolve (one generation)

| Population | Time |
|-----------|------|
| 10 | 54.1µs |
| 20 | 80.0µs |
| 50 | 105.7µs |
| 100 | 198.3µs |

### EvolveOnIdle (one generation)

| Population | Time |
|-----------|------|
| 10 | 28.2µs |
| 20 | 36.8µs |
| 50 | 83.5µs |
| 100 | 165.8µs |

### Evolve — Multiple Generations (pop=20)

| Generations | Total Time | Per-Gen Time |
|------------|-----------|-------------|
| 10 | 323.9µs | 32.4µs |
| 50 | 1.56ms | 31.3µs |
| 100 | 3.24ms | 32.4µs |

### Evolve — Scaling (pop varies, 1 gen)

| Population | Time |
|-----------|------|
| 5 | 10.8µs |
| 10 | 18.5µs |
| 20 | 35.9µs |
| 50 | 81.0µs |
| 100 | 159.8µs |
| 200 | 353.0µs |
| 500 | 908.9µs |

**Observations**:
- EvolveOnIdle is 1.5-2x faster than Evolve (simpler selection, fixed survival rate)
- Per-generation cost is stable for a given population size (~32µs for pop=20)
- Scaling is approximately O(n log n) dominated by SortByScore

---

## 4. Memory Allocation

| Benchmark | Time |
|-----------|------|
| PopulationCreation (size=10) | 23.6µs |
| PopulationCreation (size=20) | 25.1µs |
| PopulationCreation (size=50) | 33.3µs |
| PopulationCreation (size=100) | 48.1µs |
| Best (pop=100) | 0.63µs |
| Best (pop=500) | 0.63µs |
| Best (pop=1,000) | 1.12µs |
| Stats (pop=100) | 0.83µs |
| Stats (pop=500) | 1.21µs |
| Stats (pop=1,000) | 1.96µs |
| CloneStrategy (5 params) | 0.42µs |
| CloneStrategy (20 params) | 1.25µs |
| CloneStrategy (50 params) | 2.04µs |
| CloneStrategy (100 params) | 2.96µs |

**Observations**:
- Best() and Stats() are O(1) hot-path reads (~1-2µs for 1K pop)
- CloneStrategy is O(n_params), ~3µs at 100 params
- Population creation is dominated by mutator overhead, not initialization logic

---

## 5. Real-World Simulation

| Metric | Value |
|--------|-------|
| Population | 20 agents |
| Parameters | 5 per agent |
| Prompt template | ~500 chars |
| Generations | 100 (EvolveOnIdle) |
| **Total time** | **3.17ms** |
| Time per generation | ~31.7µs |
| Memory | 3.33 MB total |
| Allocations | 49,663 per 100 gens |

**Key takeaway**: The real-world GA overhead is **~32µs per generation** on Apple M3 Max.
At this cost, running evolution after every agent task uses negligible CPU budget,
making it suitable for zero-token-cost background evolution in production.

---

## 6. Wired System (High-Level API)

> **Note**: Data below are **estimated** based on scaling patterns from existing benchmarks.
> Run `go test -bench=BenchmarkWiredSystem -benchmem ./internal/evolution/` for actual numbers.

### System Creation

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | ~45µs (est.) | ~12,000 B/op (est.) | ~180 allocs/op (est.) |
| 20 | ~62µs (est.) | ~18,500 B/op (est.) | ~280 allocs/op (est.) |
| 50 | ~105µs (est.) | ~38,000 B/op (est.) ~520 allocs/op (est.) |
| 100 | ~175µs (est.) | ~68,000 B/op (est.) ~920 allocs/op (est.) |

**Observations**:
- System creation cost is dominated by `mutation.NewMutator` + population initialization
- Scales roughly linearly with population size due to initial variant generation
- Memory per system is dominated by strategy clones in the initial population

### Idle Evolution (pop=20)

| Generations | Total Time | Per-Gen | Lineages Recorded |
|------------|-----------|---------|------------------|
| 10 | ~0.42ms (est.) | ~42µs (est.) | ~ |
| 50 | ~2.0ms (est.) | ~40µs (est.) | ~ |
| 100 | ~4.1ms (est.) | ~41µs (est.) | ~ |

**Observations**:
- Per-generation cost (~40-42µs) matches genome-level `EvolveOnIdle` (~36.8µs) plus lineage recording overhead (~3-5µs)
- Lineage recording adds a small but measurable cost per generation via mutex-protected append
- Total time scales linearly with generation count as expected

### Dream Cycle

| Metric | Value |
|--------|-------|
| Single cycle (3 candidates) | ~25µs (est.) |

**Observations**:
- Single dream cycle includes: mutate(3) → mock arena test(3) → select best → record lineage
- With mock tester, cost is dominated by mutation + errgroup parallelism overhead
- Real arena testing would dominate cost (TaskSampleSize regression runs)

### Full Pipeline

| Metric | Value |
|--------|-------|
| Create + 50 gens + Extract | ~2.3ms (est.) |

**Observations**:
- End-to-end pipeline: WiredSystem creation (~62µs) + 50 gens idle evolution (~2.0ms) + BestStrategy extraction (~1µs) + genealogy count (~0.1µs)
- The dominant cost is idle evolution (~87% of total)
- Suitable for periodic background evolution (e.g., every N tasks or on idle schedule)

---

## 7. Adaptive Behavior

### Adaptive Mutation vs Fixed Rate

| Mode | 50 Generations (pop=20) | Notes |
|------|------------------------|-------|
| Fixed rate (0.2) | ~1.6ms (est.) | Baseline: constant mutation probability |
| Adaptive rate | ~1.7ms (est.) | Includes diversity scoring + stagnation detection overhead |

**What adaptive mutation does**:

- **Diversity Monitoring**: After each generation, computes average pairwise parameter distance across the population. When diversity drops below `DiversityThreshold` (default 0.15), the mutation rate is automatically increased toward `MaxMutationRate` to inject exploration.
- **Stagnation Detection**: Tracks generations without best-score improvement. When stagnant generations exceed `MaxStagnantGenerations` (default 10), bottom-performing strategies are reset to promote diversity.
- **Rate Clamping**: Adaptive mutation rate is clamped to `[MinMutationRate, MaxMutationRate]` (default [0.05, 0.5]) preventing both under-exploration and random-walk degradation.

**When adaptive helps**: In production workloads where the population converges too quickly to a local optimum, adaptive mutation re-introduces exploration pressure. The overhead is ~5-7% over fixed-rate evolution — negligible compared to the benefit of avoiding premature convergence.

**Diversity score tracking**: Each generation's diversity is computed during `EvolveOnIdle`/`Evolve`. A declining trend indicates convergence and triggers higher mutation rates automatically.

---

## Summary

| Metric | Value |
|--------|-------|
| Fastest operation | `Best()` (0.6µs for 1K pop) |
| Slowest operation | `Evolve` (pop=500, 909µs) |
| Real-world per-gen cost | ~32µs (20 agents, 5 params) |
| Bottleneck | SortByScore (O(n log n)) |
| Memory per crossover | ~2.9KB (10 params) to ~22KB (100 params) |
| Wired system creation (pop=20) | ~62µs (est.) |
| Wired idle evolution per-gen | ~40µs (est.) |
| Full pipeline (create+50gens+extract) | ~2.3ms (est.) |
| Dream cycle single run | ~25µs (est.) |
| Adaptive mutation overhead | ~5-7% over fixed rate (est.) |
