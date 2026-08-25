# Benchmark Report

Date: 2026-08-25
Go version: go1.26
Platform: darwin/arm64 (Apple M3 Max, 14 cores)
Count: 1 run per benchmark, -benchtime=1s

## Event Store (`internal/ares_events`)

| Benchmark | Iterations | ns/op | B/op | allocs/op | Note |
|---|---|---|---|---|---|
| Append | 2,359,664 | 497 | 624 | 7 | |
| AppendBatch | 289,414 | 4,429 | 9,260 | 1 | |
| Read | 265,628 | 4,831 | 17,528 | 11 | |
| ReadAll | 31,668 | 37,021 | 81,976 | 3 | |
| Subscribe | 10,000 | **111,460** | 185,949 | **700** | 100 subscribers |
| ConcurrentAppend | 1,673,084 | 705 | 625 | 6 | |

## GA Genome (`internal/ares_evolution/genome`)

### Crossover

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| CrossoverUniform | 492,152 | 2,390 | 3,077 | 31 |
| Uniform LargeParams | 66,855 | 17,625 | 21,167 | 38 |
| CrossoverParallel | 546,472 | 2,287 | 3,082 | 31 |

### Selection

| Benchmark | pop_size | k | Iterations | ns/op |
|---|---|---|---|---|
| TruncationSelection | 10 | — | 7,361,667 | 163 |
| TruncationSelection | 100 | — | 205,578 | 5,823 |
| TruncationSelection | 500 | — | 23,178 | 50,950 |
| TruncationSelection | 1,000 | — | 6,864 | 165,371 |
| TournamentSelection | 50 | 2 | 281,716 | 4,392 |
| TournamentSelection | 50 | 10 | 200,995 | 5,968 |
| TournamentSelection | 200 | 2 | 27,874 | 42,667 |
| TournamentSelection | 200 | 10 | 25,262 | 46,945 |
| RouletteWheelSelection | 10 | — | 6,015,346 | 206 |
| RouletteWheelSelection | 100 | — | 419,427 | 2,922 |
| RouletteWheelSelection | 500 | — | 27,792 | 43,664 |
| RouletteWheelSelection | 1,000 | — | 7,808 | 156,905 |
| SortByScore | 10 | — | 5,494,602 | 213 |
| SortByScore | 1,000 | — | 7,963 | 145,748 |

### Evolution

| Benchmark | Iterations | ns/op | generations | allocs/op |
|---|---|---|---|---|
| EvolveOneGeneration (pop=10) | 4,443,360 | 272 | 1 | 6 |
| EvolveOneGeneration (pop=100) | 4,391,343 | 274 | 1 | 6 |
| EvolveOnIdle (pop=10) | 4,503,309 | 273 | 1 | 6 |
| EvolveMultiple (10 gen) | 433,705 | 2,786 | 10 | 60 |
| EvolveMultiple (50 gen) | 84,134 | 13,804 | 50 | 300 |
| EvolveMultiple (100 gen) | 44,607 | 27,329 | 100 | 600 |
| RealWorldEvolution | 100 | 10,207,379 | 100 | 62,803 |

### Population

| Benchmark | size | Iterations | ns/op | allocs/op | 注 |
|---|---|---|---|---|---|
| PopulationCreation | 10 | 83,556 | 14,471 | 66 | |
| PopulationCreation | 100 | 24,075 | 49,408 | 606 | |
| Best (pop=100) | — | 4,929,394 | 239 | 3 | |
| Best (pop=1000) | — | 1,276,916 | 933 | 3 | |
| Stats (pop=100) | — | 1,694 | **715,902** | 9 | O(n²) diversity |
| Stats (pop=500) | — | 60 | **20,097,363** | 10 | O(n²) diversity |
| Stats (pop=1000) | — | 26 | **44,516,729** | 12 | O(n²) diversity |
| CloneStrategy (5 params) | — | 6,125,188 | 200 | 3 | |
| CloneStrategy (100 params) | — | 524,752 | 2,305 | 5 | |

### Fitness Sharing

| Benchmark | pop_size | Iterations | ns/op | B/op | allocs/op | 注 |
|---|---|---|---|---|---|---|
| ApplyFitnessSharing | 10 | 10,000 | 101,880 | 55,152 | 16 | Exact O(n²) |
| ApplyFitnessSharing | 50 | 1,826 | 656,765 | 290,448 | 56 | Exact O(n²) |
| ApplyFitnessSharing | 100 | 896 | **1,343,218** | 539,969 | **106** | Exact O(n²) |
| ApplyFitnessSharing | 200 | 420 | **2,886,993** | 1,079,360 | **206** | Exact O(n²) |

## GA Evolution (`internal/ares_evolution`)

| Benchmark | Iterations | ns/op | generations | B/op | allocs/op |
|---|---|---|---|---|---|
| DreamCycle SingleRun | 5,279,052 | 221 | — | 272 | 4 |
| WiredSystem Creation (pop=10) | 29,239 | 40,315 | — | 27,191 | 132 |
| WiredSystem Creation (pop=100) | 10,000 | 110,160 | — | 97,575 | 861 |
| WiredSystem IdleEvolution (10 gen) | 954 | 1,330,946 | 10 | 825,918 | 10,097 |
| WiredSystem IdleEvolution (100 gen) | 88 | 13,653,518 | 100 | 8,334,302 | 101,776 |
| FullPipeline (50 gen) | 171 | 6,719,860 | 50 | 4,142,590 | 50,487 |
| AdaptiveMutation (fixed) | 4,932 | 242,629 | — | 551,377 | 3,352 |
| AdaptiveMutation (adaptive) | 5,070 | 243,087 | — | 551,378 | 3,352 |

## Memory Distillation (`internal/ares_memory/distillation`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ScoreMemory | 198,861 | 5,826 | 8,096 | 20 |
| ConflictDetection | 1,000,000 | 1,020 | 0 | 0 |
| NoiseFilter | 177,136 | 6,780 | 592 | 11 |
| MemoryClassification | 632,649 | 1,924 | 592 | 15 |
| ExperienceExtraction | 9,382 | 130,166 | 22,864 | 267 |
| TopNFilter | 341,924 | 3,311 | 16,328 | 10 |
| MemoryOperations/Create | 13,879,095 | 85.41 | 24 | 2 |
| MemoryOperations/Classification | 4,386,504 | 270.0 | 64 | 3 |
| StringOperations/Format | 19,918,072 | 61.45 | 64 | 3 |

## Tools Core (`internal/tools/resources/core`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ToolRegistration | 259,053 | 4,458 | 9,464 | 12 |
| ToolExecution | 67,222,024 | 18.00 | 0 | 0 |
| ToolFiltering | 482,982 | 2,450 | 4,568 | 10 |
| ResultCreation/Success | 1,000,000,000 | **0.27** | 0 | 0 |
| ResultCreation/Error | 1,000,000,000 | **0.26** | 0 | 0 |
| ParameterValidation | 171,608,034 | 6.96 | 0 | 0 |
| ConcurrentToolExecution | 8,862,127 | 136.8 | 8 | 1 |

## Evaluator (`internal/ares_eval`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| ExactMatch Evaluate | 509,769,519 | 2.31 | 0 |
| ToolUsage Evaluate | 39,486,456 | 30.86 | 0 |
| AgentTestRunner RunSingle | 4,177,563 | 289.6 | 5 |
| ReportGenerator GenerateMarkdown | 348,309 | 3,452 | 76 |
| Loader Load | 26,068 | 48,708 | 601 |

## Error Wrapping (`internal/errors`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| Wrap | 1,000,000,000 | 0.27 | 0 |
| fmt.Errorf + %w | 15,064,920 | 76.36 | 2 |
| Wrap (multiple) | 1,000,000,000 | 0.58 | 0 |
| fmt.Errorf + %w (multiple) | 5,071,636 | 236.4 | 6 |

---

## Key Observations

1. **Tool execution is extremely fast** (18 ns, 0 allocs) — simple interface dispatch
2. **`Stats()` grows super-linearly** (716µs@100 → 44.5ms@1000) — O(n²) pairwise diversity; it is a periodic diagnostic, not a hot-path call
3. **FitnessSharing is O(n²)** in memory and time (16→206 allocs, 55KB→1.08MB across pop 10→200) — use a sampled variant for large populations
4. **RealWorldEvolution** completes 100 generations in ~10ms — population of 20, ~62.8K allocs
5. **ResultCreation** benchmarks at ~0.27 ns — essentially free (compiler inlines)
6. **Append is 7 allocs** — could be reduced with pooling (fine for current scale)
7. **ExperienceExtraction** is the heaviest distillation op (130µs, 267 allocs) — 50 messages, ~5 allocs/msg
8. **Subscribe stays at 700 allocs** for 100 subscribers via atomic counter + larger channel buffer

## Change Note (2026-08-25)

Full re-run on Apple M3 Max (14 cores, Go 1.26) after the taskfabric off-lock
event-append refactor and the evolution scheduler / lineage-cap fixes. Two stale
sections from the previous report were removed because their benchmarks no longer
exist in the tree: the `Stream Handler (api/handler)` package (removed) and the
`CapabilityDetection`/`CapabilityMatching` entries under Tools Core (removed).
`ApplyFitnessSharing` now reports the exact O(n²) path for pop ≤ 200 (the pop=500
spatial variant benchmark is no longer defined).
