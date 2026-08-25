# Genetic Algorithm Performance Benchmark Report

**Date**: 2026-08-25
**Platform**: darwin/arm64 (Apple M3 Max, 14 cores)
**Go**: 1.26
**Package**: `internal/ares_evolution/genome`

## Overview

Benchmarks measure the performance of all GA operations across population sizes,
parameter counts, and generation depths. All numbers below are measured with
`go test -bench=. -benchmem ./internal/ares_evolution/genome/` on the machine
above. To reproduce:

```
go test -bench=. -benchmem -run='^$' ./internal/ares_evolution/genome/
```

---

## 1. Crossover

| Benchmark | Time | Memory | Allocs |
|-----------|------|--------|--------|
| Uniform (10 params) | 2.39µs | 3,077 B/op | 31 allocs/op |
| Uniform (100 params) | 17.6µs | 21,167 B/op | 38 allocs/op |
| Parallel (10 params) | 2.29µs | 3,082 B/op | 31 allocs/op |

**Observations**:
- Uniform crossover scales roughly linearly with param count (2.4µs@10 → 17.6µs@100).
- Parallel crossover is on par with the serial path at 10 params — the errgroup
  overhead is amortized only for larger genomes / heavier fitness work.

---

## 2. Selection

### Truncation Selection

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 0.16µs | 136 B/op | 3 allocs/op |
| 100 | 5.82µs | 952 B/op | 3 allocs/op |
| 500 | 50.9µs | 4,152 B/op | 3 allocs/op |
| 1,000 | 165.4µs | 8,248 B/op | 3 allocs/op |

### Tournament Selection

| Population | k | Time | Memory | Allocs |
|-----------|---|------|--------|--------|
| 50 | 2 | 4.39µs | 14,408 B/op | 101 allocs/op |
| 50 | 3 | 4.62µs | 14,408 B/op | 101 allocs/op |
| 50 | 5 | 4.98µs | 14,408 B/op | 101 allocs/op |
| 50 | 10 | 5.97µs | 14,408 B/op | 101 allocs/op |
| 200 | 2 | 42.7µs | 195,297 B/op | 401 allocs/op |
| 200 | 3 | 44.1µs | 195,297 B/op | 401 allocs/op |
| 200 | 5 | 44.9µs | 195,297 B/op | 401 allocs/op |
| 200 | 10 | 46.9µs | 195,297 B/op | 401 allocs/op |

### Roulette Wheel Selection

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 0.21µs | 320 B/op | 4 allocs/op |
| 100 | 2.92µs | 3,424 B/op | 7 allocs/op |
| 500 | 43.7µs | 15,424 B/op | 9 allocs/op |
| 1,000 | 156.9µs | 29,760 B/op | 10 allocs/op |

### SortByScore

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 0.21µs | 136 B/op | 3 allocs/op |
| 100 | 6.07µs | 952 B/op | 3 allocs/op |
| 500 | 48.1µs | 4,152 B/op | 3 allocs/op |
| 1,000 | 145.7µs | 8,248 B/op | 3 allocs/op |

**Observations**:
- Truncation and SortByScore share the same allocation profile (3 allocs) — both
  are dominated by the sort itself, scaling ~O(n log n).
- Tournament k-value has only minor impact on runtime; allocation count is fixed
  by pool size, not k.
- Roulette wheel scales O(n) per spin.

---

## 3. Evolution Cycle

### Evolve (one generation)

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 0.27µs | 344 B/op | 6 allocs/op |
| 20 | 0.28µs | 344 B/op | 6 allocs/op |
| 50 | 0.27µs | 344 B/op | 6 allocs/op |
| 100 | 0.27µs | 296 B/op | 6 allocs/op |

### EvolveOnIdle (one generation)

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 0.27µs | 344 B/op | 6 allocs/op |
| 20 | 0.26µs | 296 B/op | 6 allocs/op |
| 50 | 0.27µs | 344 B/op | 6 allocs/op |
| 100 | 0.28µs | 344 B/op | 6 allocs/op |

### Evolve — Multiple Generations (pop=20)

| Generations | Total Time | Per-Gen Time | Memory | Allocs |
|------------|-----------|-------------|--------|--------|
| 10 | 2.79µs | 0.28µs | 3,442 B/op | 60 allocs/op |
| 50 | 13.8µs | 0.28µs | 17,214 B/op | 300 allocs/op |
| 100 | 27.3µs | 0.27µs | 34,428 B/op | 600 allocs/op |

### Evolve — Scaling (pop varies, 1 gen)

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 5 | 0.27µs | 344 B/op | 6 allocs/op |
| 10 | 0.27µs | 344 B/op | 6 allocs/op |
| 20 | 0.26µs | 296 B/op | 6 allocs/op |
| 50 | 0.27µs | 344 B/op | 6 allocs/op |
| 100 | 0.27µs | 344 B/op | 6 allocs/op |
| 200 | 0.27µs | 344 B/op | 6 allocs/op |
| 500 | 0.29µs | 344 B/op | 6 allocs/op |

**Observations**:
- Per-generation `Evolve`/`EvolveOnIdle` hot-path cost is flat (~0.27µs) across
  population sizes: the single-generation step reuses the existing population
  slice and only mutates/selects, so it does not re-pay creation cost.
- Multi-generation runs scale linearly (~0.28µs/gen) with per-gen allocation
  fixed (6 allocs/gen).

---

## 4. Memory Allocation

| Benchmark | Time | Memory | Allocs |
|-----------|------|--------|--------|
| PopulationCreation (size=10) | 14.5µs | 12,723 B/op | 66 allocs/op |
| PopulationCreation (size=20) | 18.4µs | 19,219 B/op | 126 allocs/op |
| PopulationCreation (size=50) | 30.4µs | 38,738 B/op | 306 allocs/op |
| PopulationCreation (size=100) | 49.4µs | 71,364 B/op | 606 allocs/op |
| Best (pop=100) | 0.24µs | 528 B/op | 3 allocs/op |
| Best (pop=500) | 0.46µs | 528 B/op | 3 allocs/op |
| Best (pop=1,000) | 0.93µs | 528 B/op | 3 allocs/op |
| Stats (pop=100) | 715.9µs | 4,200 B/op | 9 allocs/op |
| Stats (pop=500) | 20.1ms | 29,800 B/op | 10 allocs/op |
| Stats (pop=1,000) | 44.5ms | 57,104 B/op | 12 allocs/op |
| CloneStrategy (5 params) | 0.20µs | 528 B/op | 3 allocs/op |
| CloneStrategy (20 params) | 0.57µs | 1,432 B/op | 5 allocs/op |
| CloneStrategy (50 params) | 1.18µs | 2,584 B/op | 5 allocs/op |
| CloneStrategy (100 params) | 2.31µs | 5,144 B/op | 5 allocs/op |

**Observations**:
- `Best()` is an O(n) scan with a fixed 3-alloc profile (~0.9µs for 1K pop).
- `CloneStrategy` is O(n_params), ~2.3µs at 100 params.
- **`Stats()` is the outlier** — it grows super-linearly (716µs@100 → 44.5ms@1000)
  because it computes pairwise diversity across the population (O(n²) parameter
  comparisons). It is intended for periodic diagnostics, not the hot path; do not
  call it every generation on large populations.

---

## 5. Fitness Sharing

| Population | Time | Memory | Allocs |
|-----------|------|--------|--------|
| 10 | 101.9µs | 55,152 B/op | 16 allocs/op |
| 50 | 656.8µs | 290,448 B/op | 56 allocs/op |
| 100 | 1.34ms | 539,969 B/op | 106 allocs/op |
| 200 | 2.89ms | 1,079,360 B/op | 206 allocs/op |

**Observations**:
- Fitness sharing is O(n²) in population size (pairwise niche distance), which
  dominates its cost and memory. Use a sampled variant
  (`ApplyFitnessSharing_CustomSampling`) for large populations.

---

## 6. Real-World Simulation

| Metric | Value |
|--------|-------|
| Generations | 100 (RealWorldEvolution) |
| **Total time** | **10.2ms** |
| Time per generation | ~102µs |
| Memory | 4.65 MB total |
| Allocations | 62,803 per 100 gens |

**Key takeaway**: An end-to-end real-world evolution run costs **~102µs per
generation** on Apple M3 Max. At this cost, running evolution on idle cycles
uses a negligible CPU budget, making it suitable for zero-token-cost background
evolution in production.

---

## Summary

| Metric | Value |
|--------|-------|
| Fastest operation | `CloneStrategy` (5 params) / `Best()` (pop=100) — ~0.2µs |
| Most expensive operation | `Stats()` (pop=1,000, 44.5ms — O(n²) diversity) |
| Per-generation `Evolve` hot path | ~0.27µs (flat across pop size) |
| Real-world per-gen cost | ~102µs (RealWorldEvolution, 100 gens) |
| Bottleneck at scale | `Stats()` and `ApplyFitnessSharing` (both O(n²)) |
| Memory per crossover | ~3KB (10 params) to ~21KB (100 params) |
