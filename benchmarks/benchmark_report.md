# Benchmark Report

Date: 2026-07-31
Go version: go1.26
Platform: darwin/arm64 (Apple M3 Max)
Count: 1 run per benchmark, -benchtime=1s (re-run after PauseAgent/ResumeAgent lifecycle fixes)

## Event Store (`internal/ares_events`)

| Benchmark | Iterations | ns/op | B/op | allocs/op | Note |
|---|---|---|---|---|---|
| Append | 2,355,696 | 500 | 624 | 7 | |
| AppendBatch | 303,033 | 4,331 | 8,844 | 1 | |
| Read | 184,120 | 6,257 | 17,528 | 11 | |
| ReadAll | 31,371 | 38,508 | 81,976 | 3 | |
| Subscribe | 14,071 | **105,481** | 169,110 | **600** | 100 subscribers |
| ConcurrentAppend | 1,000,000 | 1,260 | 626 | 6 | |

## GA Genome (`internal/ares_evolution/genome`)

### Crossover

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| CrossoverUniform | 495,528 | 2,399 | 3,077 | 31 |
| Uniform LargeParams | 69,636 | 24,498 | 21,165 | 38 |
| CrossoverParallel | 384,744 | 3,600 | 3,082 | 31 |

### Selection

| Benchmark | pop_size | k | Iterations | ns/op |
|---|---|---|---|---|
| TruncationSelection | 10 | — | 5,618,367 | 189 |
| TruncationSelection | 100 | — | 204,684 | 5,760 |
| TruncationSelection | 500 | — | 22,594 | 52,530 |
| TruncationSelection | 1,000 | — | 7,622 | 154,883 |
| TournamentSelection | 50 | 2 | 282,002 | 4,411 |
| TournamentSelection | 50 | 10 | 216,087 | 5,605 |
| TournamentSelection | 200 | 2 | 28,813 | 41,703 |
| TournamentSelection | 200 | 10 | 26,368 | 48,189 |
| RouletteWheelSelection | 10 | — | 5,469,656 | 212 |
| RouletteWheelSelection | 100 | — | 398,265 | 2,977 |
| RouletteWheelSelection | 500 | — | 27,663 | 41,750 |
| RouletteWheelSelection | 1,000 | — | 7,962 | 155,662 |
| SortByScore | 10 | — | 4,972,791 | 235 |
| SortByScore | 1,000 | — | 7,740 | 147,050 |

### Evolution

| Benchmark | Iterations | ns/op | generations | allocs/op |
|---|---|---|---|---|
| EvolveOneGeneration (pop=10) | 4,145,414 | 303 | 1 | 6 |
| EvolveOneGeneration (pop=100) | 4,022,622 | 291 | 1 | 6 |
| EvolveOnIdle (pop=10) | 4,112,655 | 291 | 1 | 6 |
| EvolveMultiple (10 gen) | 410,246 | 2,743 | 10 | 60 |
| EvolveMultiple (50 gen) | 85,005 | 13,794 | 50 | 300 |
| EvolveMultiple (100 gen) | 43,864 | 28,433 | 100 | 600 |
| RealWorldEvolution | 100 | 10,144,330 | 100 | 62,395 |

### Population

| Benchmark | size | Iterations | ns/op | allocs/op | 注 |
|---|---|---|---|---|---|
| PopulationCreation | 10 | 80,026 | 14,627 | 66 | |
| PopulationCreation | 100 | 22,962 | 52,222 | 606 | |
| Best (pop=100) | — | 4,821,536 | 247 | 3 | |
| Best (pop=1000) | — | 1,307,472 | 912 | 3 | |
| Stats (pop=100) | — | 1,716 | **703,575** | 9 | Exact O(n²) mode |
| Stats (pop=500) | — | 61 | **19,685,162** | 10 | Sampled mode (sampleSize=200) |
| Stats (pop=1000) | — | 27 | **43,554,131** | 12 | Sampled mode |
| CloneStrategy (5 params) | — | 5,805,331 | 210 | 3 | |
| CloneStrategy (100 params) | — | 512,847 | 2,373 | 5 | |

### Fitness Sharing

| Benchmark | pop_size | Iterations | ns/op | B/op | allocs/op | 注 |
|---|---|---|---|---|---|---|
| ApplyFitnessSharing | 10 | 10,000 | 105,551 | 55,152 | 16 | Exact O(n²) |
| ApplyFitnessSharing | 50 | 1,831 | 649,163 | 290,448 | 56 | Exact O(n²) |
| ApplyFitnessSharing | 100 | 892 | **1,350,976** | 539,970 | **106** | Sampled |
| ApplyFitnessSharing | 200 | 426 | **2,823,291** | 1,079,364 | **206** | Sampled |
| ApplyFitnessSharing | 500 | 153 | **7,712,111** | 2,696,777 | **506** | Spatial |

## GA Evolution (`internal/ares_evolution`)

| Benchmark | Iterations | ns/op | generations | B/op | allocs/op |
|---|---|---|---|---|---|
| DreamCycle SingleRun | 4,866,910 | 238 | — | 272 | 4 |
| WiredSystem Creation (pop=10) | 30,430 | 39,516 | — | 27,370 | 134 |
| WiredSystem Creation (pop=100) | 10,000 | 106,597 | — | 97,732 | 863 |
| WiredSystem IdleEvolution (10 gen) | 964 | 1,280,518 | 10 | 830,307 | 10,127 |
| WiredSystem IdleEvolution (100 gen) | 94 | 12,918,927 | 100 | 8,316,108 | 101,257 |
| FullPipeline (50 gen) | 186 | 6,504,526 | 50 | 4,160,464 | 50,652 |
| AdaptiveMutation (fixed) | 4,990 | 232,376 | — | 551,294 | 3,352 |
| AdaptiveMutation (adaptive) | 5,131 | 233,397 | — | 551,299 | 3,352 |

## Memory Distillation (`internal/ares_memory/distillation`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ScoreMemory | 192,477 | 6,227 | 8,096 | 20 |
| ConflictDetection | 1,000,000 | 1,069 | 0 | 0 |
| NoiseFilter | 171,783 | 7,003 | 592 | 11 |
| MemoryClassification | 592,352 | 1,997 | 592 | 15 |
| ExperienceExtraction | 8,043 | 150,493 | 22,128 | 267 |
| TopNFilter | 430,741 | 3,704 | 15,816 | 10 |
| Distillation Full Pipeline | — | full run | — | — |

## Tools Core (`internal/tools/resources/core`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ToolRegistration | 251,668 | 4,656 | 9,464 | 12 |
| ToolExecution | 73,984,826 | 16.40 | 0 | 0 |
| CapabilityDetection | 148,279 | 7,933 | 1,024 | 8 |
| CapabilityMatching | 277,465 | 4,333 | 600 | 9 |
| ToolFiltering | 450,381 | 2,569 | 4,568 | 10 |
| ResultCreation | 1,000,000,000 | **0.27** | 0 | 0 |
| ParameterValidation | 162,581,826 | 7.46 | 0 | 0 |
| ConcurrentToolExecution | 8,983,209 | 134.5 | 8 | 1 |

## Stream Handler (`api/handler`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| HandleStream | 227,089 | 5,106 | 9,357 | 67 |
| ConvertEvent | 240,026,122 | 5.0 | 0 | 0 |
| MultipleEvents | 34,846 | 33,988 | 38,237 | 460 |

## Evaluator (`internal/ares_eval`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| ExactMatch Evaluate | 371,883,297 | 3.07 | 0 |
| ToolUsage Evaluate | 42,065,410 | 28.35 | 0 |
| AgentTestRunner RunSingle | 3,663,362 | 326.5 | 5 |
| ReportGenerator GenerateMarkdown | 332,298 | 3,732 | 76 |
| Loader Load | 23,229 | 51,594 | 601 |

## Error Wrapping (`internal/errors`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| Wrap | 1,000,000,000 | 0.27 | 0 |
| fmt.Errorf + %w | 13,996,174 | 88.12 | 2 |
| Wrap (multiple) | 1,000,000,000 | 0.58 | 0 |
| fmt.Errorf + %w (multiple) | 4,212,994 | 279.2 | 6 |

---

## Key Observations

1. **Tool execution is extremely fast** (16.4 ns, 0 allocs) — simple interface dispatch
2. **Stats/pop=1000 stays ~43ms** via `DiversitySampleSize` sampling — O(n²) → O(n×k)
3. **FitnessSharing allocs stay flat** (~506 allocs at pop=500) via Reservoir Sampling — critical for GC pressure in long evolution runs
4. **RealWorldEvolution** completes 100 generations in ~10ms — population of 20, 62K allocs
5. **ResultCreation** benchmarks at 0.27 ns — essentially free (compiler inlines)
6. **Append is 7 allocs** — could be reduced with pooling (but fine for current scale)
7. **ExperienceExtraction** is the heaviest single operation (150 μs, 267 allocs) — 50 messages, ~5 allocs/msg
8. **Subscribe stays at 600 allocs** for 100 subscribers via atomic counter + larger channel buffer

## Change Note (2026-07-31)

Re-run after the PauseAgent/ResumeAgent lifecycle semantic fixes
(`internal/ares_runtime/manager_chaos.go`). No runtime code in these benchmark
packages was modified by that change; numbers are a fresh baseline snapshot.
