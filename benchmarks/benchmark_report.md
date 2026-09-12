# Benchmark Report

Date: 2026-09-12
Go version: go1.27.1
Platform: darwin/arm64 (Apple M3 Max, 14 cores), macOS 15.7.3
Methodology: 1 run per benchmark, `-benchtime=500ms -count=1`, sequential package runs.
Raw outputs are not committed (machine-specific); regenerate with
`go test -run='^$' -bench=. -benchmem -count=1 -benchtime=500ms ./<pkg>` —
structured results live in `benchmark_results.json`.

## Event Store (`internal/ares_events`)

| Benchmark | Iterations | ns/op | B/op | allocs/op | Note |
|---|---|---|---|---|---|
| Append | 944,305 | 571 | 727 | 8 | |
| AppendBatch | 91,492 | 6,499 | 21,650 | 102 | richer rebuild payload since 0.3.1 T2 |
| Read | 127,173 | 4,726 | 17,528 | 11 | |
| ReadAll | 15,926 | 38,009 | 81,976 | 3 | |
| Subscribe | 6,858 | 119,029 | 181,945 | 699 | 100 subscribers |
| ConcurrentAppend | 873,266 | 753 | 729 | 7 | |

## Task & Agent Fabric (`internal/fabric/task`, `internal/fabric/agent`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| Fabric_Create | 1,408,638 | 395 | 352 | 4 |
| Fabric_Schedule | 1,097,323 | 568 | 428 | 10 |
| Fabric_RunQuantum | 466,479 | 1,301 | 1,162 | 16 |
| Fabric_ReadyTasks | 1,539,158 | 386 | 960 | 4 |
| Fabric_IsReady | 39,081,582 | 15.4 | 0 | 0 |
| Fabric_Spawn | 1,494,237 | 411 | 936 | 10 |
| Fabric_SpawnWithResources | 705,108 | 828 | 1,480 | 14 |
| Fabric_LifecycleSuspendResume | 24,865,699 | 24.0 | 0 | 0 |
| Fabric_Children | 22,655,936 | 26.4 | 80 | 1 |

## Agent IPC (`internal/agentipc`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| Bus_Send | 1,951,827 | 313 | 400 | 8 |
| Bus_RequestReply | 350,707 | 1,728 | 1,320 | 22 |
| Bus_Broadcast | 2,245,485 | 263 | 400 | 8 |

## GA Genome (`internal/runtime/ares_evolution/genome`)

### Crossover

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| CrossoverUniform | 261,633 | 2,293 | 3,045 | 31 |
| CrossoverUniform LargeParams | 34,124 | 17,224 | 21,128 | 38 |
| CrossoverParallel | 296,146 | 2,279 | 3,050 | 31 |

### Selection

| Benchmark | pop_size | k | Iterations | ns/op |
|---|---|---|---|---|
| TruncationSelection | 10 | — | 3,592,179 | 189 |
| TruncationSelection | 100 | — | 103,374 | 5,816 |
| TruncationSelection | 500 | — | 10,000 | 51,884 |
| TruncationSelection | 1,000 | — | 3,535 | 165,130 |
| TournamentSelection | 50 | 2 | 158,107 | 3,843 |
| TournamentSelection | 50 | 10 | 108,184 | 5,560 |
| RouletteWheelSelection | 10 | — | 2,977,826 | 205 |
| RouletteWheelSelection | 100 | — | 208,027 | 3,023 |
| RouletteWheelSelection | 500 | — | 14,067 | 43,074 |
| RouletteWheelSelection | 1,000 | — | 3,831 | 154,698 |
| SortByScore | 10 | — | 2,876,614 | 208 |
| SortByScore | 1,000 | — | 4,132 | 146,809 |

### Evolution

| Benchmark | Iterations | ns/op | generations | allocs/op |
|---|---|---|---|---|
| EvolveOneGeneration (pop=100) | 2,222,732 | 271 | 1 | 6 |
| EvolveOnIdle (pop=100) | 2,210,473 | 267 | 1 | 6 |
| EvolveMultiple (10 gen) | 229,296 | 2,611 | 10 | 60 |
| EvolveMultiple (100 gen) | 23,229 | 26,278 | 100 | 600 |
| RealWorldEvolution | 58 | 10,496,190 | 100 | 61,922 |

### Population

| Benchmark | size | Iterations | ns/op | allocs/op | Note |
|---|---|---|---|---|---|
| PopulationCreation | 10 | 39,964 | 15,136 | 66 | |
| PopulationCreation | 100 | 10,000 | 50,845 | 606 | |
| Best (pop=1000) | — | 600,019 | 1,072 | 3 | |
| Stats (pop=100) | — | 832 | 731,985 | 9 | O(n²) diversity |
| Stats (pop=1,000) | — | 12 | 46,081,622 | 12 | O(n²) diversity |
| CloneStrategy (100 params) | — | 261,409 | 2,280 | 5 | |

### Fitness Sharing

| Benchmark | pop_size | Iterations | ns/op | B/op | allocs/op | Note |
|---|---|---|---|---|---|---|
| ApplyFitnessSharing | 10 | 5,512 | 105,547 | 55,152 | 16 | Exact O(n²) |
| ApplyFitnessSharing | 100 | 429 | 1,357,811 | 539,970 | 106 | Exact O(n²) |
| ApplyFitnessSharing | 200 | 208 | 2,855,424 | 1,079,364 | 206 | Exact O(n²) |
| ApplyFitnessSharing | 500 | 76 | 7,900,230 | 2,696,773 | 506 | spatial variant |
| CustomSampling limit20_size10 | — | 523 | 1,136,145 | 539,808 | 106 | |
| CustomSampling limit0_exact | — | 913 | 661,266 | 290,448 | 56 | |

## GA Evolution (`internal/runtime/ares_evolution`)

| Benchmark | Iterations | ns/op | generations | B/op | allocs/op |
|---|---|---|---|---|---|
| DreamCycle SingleRun | 2,550,699 | 283 | — | 272 | 4 |
| WiredSystem Creation (pop=10) | 12,648 | 49,681 | — | 27,115 | 129 |
| WiredSystem Creation (pop=100) | 5,097 | 121,871 | — | 97,479 | 858 |
| WiredSystem IdleEvolution (10 gen) | 326 | 1,910,916 | 10 | 1,271,171 | 12,736 |
| WiredSystem IdleEvolution (100 gen) | 33 | 18,130,059 | 100 | 12,732,570 | 127,381 |
| FullPipeline (50 gen) | 67 | 8,970,315 | 50 | 6,358,065 | 63,701 |
| AdaptiveMutation (fixed) | 2,677 | 230,586 | — | 551,283 | 3,352 |
| AdaptiveMutation (adaptive) | 2,626 | 229,782 | — | 551,274 | 3,352 |

## Runtime Evolution (`internal/runtime/evolution`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| WorkflowGenome_Mutate | 19,239 | 31,526 | 47,597 | 534 |
| KnowledgeGenome_Mutate | 1,417,918 | 427 | 960 | 11 |
| RecoveryGenome_Mutate | 1,000,000 | 505 | 1,280 | 21 |
| DiffEngine_Workflow | 1,000,000 | 546 | 352 | 3 |
| Coordinator_Evaluate | 94,663,352 | 6.26 | 0 | 0 |
| FullEvolutionCycle | 59,260 | 9,811 | 13,868 | 157 |

## Knowledge Fabric (`internal/knowledge/*`)

### Linkers (100 objs)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| DecisionLinker | 36,307 | 16,626 | 10,880 | 295 |
| ArchitectureLinker | 14,828 | 40,744 | 166,960 | 85 |
| TimelineLinker | 314,290 | 1,803 | 3,120 | 11 |
| SimilarityLinker | 315 | 1,904,317 | 4,709,410 | 20,217 |

### Compiler (100 nodes)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| DefaultCompiler PromptFormat | 13,405 | 46,473 | 73,288 | 819 |
| DefaultCompiler MarkdownFormat | 10,000 | 54,205 | 98,266 | 1,020 |
| DefaultCompiler JSONFormat | 5,652 | 105,079 | 133,012 | 2,619 |
| DefaultCompiler AllFormats | 2,014 | 289,431 | 409,902 | 5,777 |

### Store / Pipeline / Planner / Retriever

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| Store_Save | 1,000,000 | 885 | 1,133 | 14 |
| Store_Get | 3,404,025 | 176 | 430 | 4 |
| Store_QueryByType | 23,508 | 26,087 | 74,000 | 512 |
| Store_Search | 4,294 | 144,387 | 277,444 | 3,014 |
| DefaultNormalizer_Normalize | 1,200,786 | 507 | 688 | 10 |
| KnowledgePlanner_Plan | 809,306 | 746 | 1,008 | 14 |
| Retriever_Retrieve (100 objs) | 69 | 32,084,136 | 58,622,655 | 453,393 |

## Memory Distillation (`internal/runtime/memory/distillation`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| ScoreMemory | 179,816 | 3,288 | 768 | 8 |
| ConflictDetection | 568,982 | 1,060 | 0 | 0 |
| NoiseFilter | 83,886 | 6,942 | 592 | 11 |
| MemoryClassification | 297,078 | 1,986 | 592 | 15 |
| ExperienceExtraction | 4,654 | 129,045 | 22,864 | 267 |
| TopNFilter | 260,293 | 3,706 | 16,328 | 10 |
| MemoryOperations/Create | 6,962,774 | 86 | 24 | 1 |
| MemoryOperations/Classification | 2,139,285 | 284 | 64 | 3 |
| StringOperations/Format | 9,124,797 | 66 | 64 | 3 |

## Tools (`internal/tools/planner`, `internal/tools/resources/core`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| Planner_FullPipeline | 148,855 | 4,087 | 4,786 | 38 |
| Planner_Parallel | 141,501 | 4,049 | 6,235 | 48 |
| Planner_UnknownRequest | 719,000 | 852 | 208 | 5 |
| Bridge_DirectExecution | 247,424 | 2,432 | 1,253 | 9 |
| Bridge_PlannerFallback | 73,696 | 8,256 | 6,862 | 57 |
| ToolRegistration | 123,824 | 4,778 | 9,464 | 12 |
| ToolExecution | 33,080,709 | 18.1 | 0 | 0 |
| ToolFiltering | 228,219 | 2,608 | 4,568 | 10 |
| ResultCreation/Success | 1,000,000,000 | 0.27 | 0 | 0 |
| ResultCreation/Error | 1,000,000,000 | 0.27 | 0 | 0 |
| ParameterValidation | 82,401,067 | 7.14 | 0 | 0 |
| ConcurrentToolExecution | 4,499,694 | 131 | 8 | 1 |

## Evaluator (`internal/runtime/eval`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| ExactMatch Evaluate | 253,612,212 | 2.35 | 0 |
| ToolUsage Evaluate | 22,030,677 | 27.8 | 0 |
| AgentTestRunner RunSingle | 1,999,849 | 301 | 5 |
| ReportGenerator GenerateMarkdown | 175,311 | 3,394 | 76 |
| Loader Load | 12,363 | 47,959 | 601 |

## Error Wrapping (`internal/errors`)

| Benchmark | Iterations | ns/op | allocs/op |
|---|---|---|---|
| Wrap | 1,000,000,000 | 0.28 | 0 |
| fmt.Errorf + %w | 7,656,523 | 80.3 | 2 |
| Wrap (multiple) | 1,000,000,000 | 0.57 | 0 |
| fmt.Errorf + %w (multiple) | 2,392,568 | 256 | 6 |

## Observability & Recovery (`internal/aresrecovery`)

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| GlobalTracer_TraceTask | 6,625,663 | 92.2 | 243 | 0 |
| GlobalTracer_TraceMessage | 6,886,102 | 85.9 | 293 | 0 |
| GlobalTracer_Spans (200 spans) | 424,521 | 1,239 | 10,032 | 5 |
| Sandbox_ReplayRecoveryChain | 208,430 | 2,872 | 7,533 | 66 |
| Sandbox_SimulateAgentDeath | 273,878 | 2,154 | 5,163 | 51 |

## Key Observations

1. **Hot-path constants unchanged**: tool dispatch (18 ns), exact-match evaluation (2.4 ns), error Wrap (0.28 ns), coordinator evaluate (6.3 ns) — all allocation-free.
2. **Scheduler-quantum cost dropped**: `Fabric_RunQuantum` is now 1.30µs / 16 allocs (was 1.60µs / 23) and `Fabric_Schedule` 568ns / 10 allocs (was 800ns / 18) after the B3 convergence slimmed the quantum path.
3. **`MemoryStore_AppendBatch` grew to 6.5µs / 102 allocs** (was 3.7µs / 1 alloc): must-persist events now carry the full cross-restart rebuild payload plus fencing epoch (release-readiness T2, 0.3.1) — a deliberate durability/cost trade.
4. **`Stats()` remains O(n²)** (0.73ms@100 → 46ms@1000) — periodic diagnostic, never on a hot path.
5. **`RealWorldEvolution`** completes 100 generations in ~10.5ms (pop=20, ~61.9K allocs) — steady-state GA stays millisecond-scale.
6. **Retriever end-to-end (100 objs)** is the heaviest knowledge op at 32ms / ~453K allocs; SimilarityLinker dominates (1.9ms, 20K allocs pairwise compare).

## Change Note (2026-09-12)

Full re-run after the SDK/kernel L2 convergence (B1–B5): `internal/agentloop` deleted,
all SDK entry points now execute as L2 sessions on the shared kernel scheduler. Package
paths in this report reflect the post-convergence layout (`internal/runtime/evolution`,
`internal/runtime/eval`, `internal/runtime/ares_evolution/genome`, `internal/fabric/task`,
`internal/fabric/agent`). Benchmarks that no longer exist were dropped: `api/handler`
stream handler (tree deleted), `DualTrackDispatch` (agentipc retired). Methodology note:
this run uses `-benchtime=500ms -count=1` (previous report: 1s, single run) — treat
cross-report sub-µs deltas with normal single-run variance.
