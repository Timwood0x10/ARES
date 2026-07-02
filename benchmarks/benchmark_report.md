# Benchmark Report

Date: 2026-07-02
Go version: go1.26
Platform: darwin/arm64 (Apple M3 Max)
Count: 3 runs per benchmark (1 for long-running wired benchmarks)

## Event Store (`internal/ares_events`)

| Benchmark | Iterations | ns/op | B/op | allocs/op | Note |
|---|---|---|---|---|---|
| Append | 2,400,000 | 530 | 620 | 7 | |
| AppendBatch | 290,000 | 4,480 | 9,200 | 1 | |
| Read | 170,000 | 7,140 | 17,528 | 11 | |
| ReadAll | 30,300 | 40,500 | 81,976 | 3 | |
| Subscribe | 15,000 | **130,000** | 170,000 | **600** | **↓33% allocs**, 100 subscribers |
| ConcurrentAppend | 1,200,000 | 1,300 | 626 | 6 | |

## GA Genome (`internal/ares_evolution/genome`)

### Crossover

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| CrossoverUniform | 500,000 | 2,400 | 2,869 | 29 |
| Uniform LargeParams | 68,000 | 17,700 | 20,957 | 36 |
| CrossoverParallel | 545,000 | 2,320 | 2,875 | 29 |

### Selection

| Benchmark | pop_size | k | Iterations | ns/op |
|---|---|---|---|---|
| TruncationSelection | 10 | — | 6,100,000 | 189 |
| TruncationSelection | 100 | — | 200,000 | 6,200 |
| TruncationSelection | 500 | — | 25,000 | 47,000 |
| TruncationSelection | 1,000 | — | 9,500 | 129,000 |
| TournamentSelection | 50 | 2 | 380,000 | 3,200 |
| TournamentSelection | 50 | 10 | 280,000 | 4,350 |
| TournamentSelection | 200 | 2 | 33,000 | 37,000 |
| TournamentSelection | 200 | 10 | 29,000 | 41,000 |
| RouletteWheelSelection | 10 | — | 5,700,000 | 212 |
| RouletteWheelSelection | 100 | — | 410,000 | 2,900 |
| RouletteWheelSelection | 500 | — | 28,700 | 42,000 |
| RouletteWheelSelection | 1,000 | — | 7,900 | 152,000 |

### Evolution

| Benchmark | Iterations | ns/op | generations | allocs/op |
|---|---|---|---|---|
| EvolveOneGeneration (pop=10) | 4,000,000 | 305 | 1 | 7 |
| EvolveOneGeneration (pop=100) | 3,900,000 | 305 | 1 | 7 |
| EvolveOnIdle (pop=10) | 3,800,000 | 315 | 1 | 8 |
| EvolveMultiple (10 gen) | 396,000 | 3,027 | 10 | 70 |
| EvolveMultiple (50 gen) | 79,000 | 15,134 | 50 | 350 |
| EvolveMultiple (100 gen) | 39,900 | 30,000 | 100 | 700 |
| RealWorldEvolution | 100 | 10,200,000 | 100 | 57,500 |

### Population

| Benchmark | size | Iterations | ns/op | allocs/op | 注 |
|---|---|---|---|---|---|
| PopulationCreation | 10 | 78,000 | 15,500 | 64 | |
| PopulationCreation | 100 | 22,400 | 53,500 | 604 | |
| Best (pop=100) | — | 4,700,000 | 255 | 3 | |
| Best (pop=1000) | — | 1,300,000 | 960 | 3 | |
| Stats (pop=100) | — | 1,750 | **695,000** | 9 | Exact O(n²) mode |
| Stats (pop=500) | — | 62 | **19,700,000** | 10 | Sampled mode (sampleSize=200) |
| Stats (pop=1000) | — | 27 | **43,300,000** | 12 | Sampled mode, **↓38% vs old O(n²)** |
| CloneStrategy (5 params) | — | 5,500,000 | 220 | 3 | |
| CloneStrategy (100 params) | — | 490,000 | 2,440 | 5 | |

### Fitness Sharing

| Benchmark | pop_size | Iterations | ns/op | B/op | allocs/op | 注 |
|---|---|---|---|---|---|---|
| ApplyFitnessSharing | 10 | 10,000 | 102,000 | 55K | 16 | Exact O(n²) |
| ApplyFitnessSharing | 50 | 1,900 | 637,000 | 290K | 56 | Exact O(n²) |
| ApplyFitnessSharing | 100 | 930 | **1,270,000** | 540K | **106** | Sampled, **↓43% allocs** |
| ApplyFitnessSharing | 200 | 450 | **2,680,000** | 1.1M | **206** | Sampled, **↓44% allocs** |
| ApplyFitnessSharing | 500 | 160 | **7,360,000** | 2.7M | **506** | Spatial, **↓44% allocs** |

## GA Evolution (`internal/ares_evolution`)

| Benchmark | Iterations | ns/op |
|---|---|---|
| DreamCycle SingleRun | 5,000,000 | 224 |
| WiredSystem Creation (pop=10) | — | full run |
| WiredSystem IdleEvolution (10 gen) | — | full run |
| FullPipeline | — | full run |
| AdaptiveMutation fixed vs adaptive | — | full run |

## Memory Distillation (`internal/ares_memory/distillation`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ScoreMemory | 195,000 | 6,200 | 8,096 | 20 |
| ConflictDetection | 1,000,000 | 1,090 | 0 | 0 |
| NoiseFilter | 173,000 | 6,990 | 592 | 11 |
| MemoryClassification | 600,000 | 1,980 | 592 | 15 |
| ExperienceExtraction | 8,000 | 154,000 | 21,200 | 267 |
| TopNFilter | 375,000 | 3,100 | 12,040 | 10 |
| Distillation Full Pipeline | — | full run | — | — |

## Tools Core (`internal/tools/resources/core`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| ToolRegistration | 260,000 | 4,500 | 12 |
| ToolExecution | 82,000,000 | 14.8 | 0 |
| CapabilityDetection | 157,000 | 7,680 | 8 |
| CapabilityMatching | 283,000 | 4,190 | 9 |
| ToolFiltering | 490,000 | 2,430 | 10 |
| ResultCreation | 1,000,000,000 | **0.27** | 0 |
| ParameterValidation | 163,000,000 | 7.38 | 0 |
| ConcurrentToolExecution | 10,000,000 | 132 | 1 |

## Stream Handler (`api/handler`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| HandleStream | 300,000 | 3,960 | 69 |
| ConvertEvent | 180,000,000 | 6.5 | 0 |
| MultipleEvents | 18,000 | 91,000 | 462 |

## Evaluator (`internal/ares_eval`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| ExactMatch Evaluate | 385,000,000 | 3.1 | 0 |
| ToolUsage Evaluate | 42,000,000 | 28.2 | 0 |
| AgentTestRunner RunSingle | 3,700,000 | 317 | 5 |
| ReportGenerator GenerateMarkdown | 340,000 | 3,500 | 76 |
| Loader Load | 25,800 | 47,300 | 601 |

## Error Wrapping (`internal/errors`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| Wrap | 1,000,000,000 | 0.27 | 0 |
| fmt.Errorf + %w | 14,200,000 | 86 | 2 |
| Wrap (multiple) | 1,000,000,000 | 0.59 | 0 |
| fmt.Errorf + %w (multiple) | 4,370,000 | 284 | 6 |

---

## Key Observations

1. **Tool execution is extremely fast** (14.8 ns, 0 allocs) — simple interface dispatch
2. **Stats/pop=1000 improved 38%** (69.5ms → 43.3ms) via `DiversitySampleSize` sampling — O(n²) → O(n×k)
3. **FitnessSharing allocs reduced 44%** via Reservoir Sampling — critical for GC pressure in long evolution runs
4. **RealWorldEvolution** completes 100 generations in ~10ms — population of 20, 57K allocs
5. **ResultCreation** benchmarks at 0.27 ns — essentially free (compiler inlines)
6. **Append is 7 allocs** — could be reduced with pooling (but fine for current scale)
7. **ExperienceExtraction** is the heaviest single operation (154 μs, 267 allocs) — 50 messages, ~5 allocs/msg
8. **Subscribe allocs reduced 33%** (900 → 600) via atomic counter + removed sync.Once + larger channel buffer
