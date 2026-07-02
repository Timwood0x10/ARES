# Benchmark Report

Date: 2026-07-02
Go version: go1.26
Platform: darwin/arm64 (Apple M3 Max)
Count: 3 runs per benchmark (1 for long-running wired benchmarks)

## Event Store (`internal/ares_events`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| Append | 2,400,000 | 530 | 620 | 7 |
| AppendBatch | 290,000 | 4,480 | 9,200 | 1 |
| Read | 170,000 | 7,140 | 17,528 | 11 |
| ReadAll | 30,300 | 40,500 | 81,976 | 3 |
| Subscribe | 10,000 | 187,000 | 152,000 | 900 |
| ConcurrentAppend | 1,200,000 | 1,300 | 626 | 6 |

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

| Benchmark | size | Iterations | ns/op |
|---|---|---|---|
| PopulationCreation | 10 | 78,000 | 15,500 |
| PopulationCreation | 100 | 22,400 | 53,500 |
| Best (pop=100) | — | 4,700,000 | 255 |
| Best (pop=1000) | — | 1,300,000 | 960 |
| Stats (pop=100) | — | 1,700 | 693,000 |
| Stats (pop=1000) | — | 16 | 69,500,000 |
| CloneStrategy (5 params) | — | 5,500,000 | 220 |
| CloneStrategy (100 params) | — | 490,000 | 2,440 |

### Fitness Sharing

| Benchmark | pop_size | Iterations | ns/op |
|---|---|---|---|
| ApplyFitnessSharing | 10 | 10,000 | 105,000 |
| ApplyFitnessSharing | 100 | 870 | 1,380,000 |
| ApplyFitnessSharing | 500 | 150 | 8,250,000 |

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
2. **Stats/pop=1000 is the slowest benchmark** (69.5 ms) — O(n²) distance matrix in fitness sharing
3. **RealWorldEvolution** completes 100 generations in ~10ms — population of 20
4. **ResultCreation** benchmarks at 0.27 ns — essentially free (compiler inlines)
5. **Append is 7 allocs** — could be reduced with pooling (but fine for current scale)
6. **ExperienceExtraction** is the heaviest single operation (154 μs, 267 allocs)
