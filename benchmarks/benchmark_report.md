# Benchmark Report

Date: 2026-09-12
Go version: go1.27.1
Platform: darwin/arm64 (Apple M3 Max, 14 cores), macOS 15.7.3
Methodology: 1 run per benchmark, `-benchtime=500ms -count=1`, sequential package runs.
Raw outputs are not committed (machine-specific); regenerate with
`go test -run='^$' -bench=. -benchmem -count=1 -benchtime=500ms ./<pkg>` —
structured results live in `benchmark_results.json`.

**Latest re-run: see [0.3.2 re-run (2026-10-09)](#032-re-run-2026-10-09) at the end of this
file — 191 benchmarks across 21 packages, including the kernel scheduler / L2-growth numbers
the README quotes. The tables in this file's first half are the 2026-09-12 baseline.**

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
| UngatedPatcher_Evaluate | 94,663,352 | 6.26 | 0 | 0 |
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

## 0.3.2 re-run (2026-10-09)

Methodology: identical to the baseline above — `-benchtime=500ms -count=1 -benchmem`,
sequential per-package runs, same machine (Apple M3 Max, 14 cores, macOS 15.7.3, go1.27.1).
Raw output: `benchmarks/0.3.2_bench.txt` (not committed — machine-specific). Covers 191
benchmarks across 21 packages, including the kernel scheduler/L2-growth numbers the README quotes.


**./internal/agentipc**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkBus_Broadcast-14 | 2393271 | 269.1 | 400 | 8 |
| BenchmarkBus_RequestReply-14 | 355506 | 1618 | 1320 | 22 |
| BenchmarkBus_Send-14 | 1921470 | 303.4 | 400 | 8 |

**./internal/ares_events**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkMemoryStore_Append-14 | 955096 | 1397 | 4156 | 10 |
| BenchmarkMemoryStore_AppendBatch-14 | 94596 | 6451 | 18276 | 105 |
| BenchmarkMemoryStore_ConcurrentAppend-14 | 780048 | 1431 | 2512 | 10 |
| BenchmarkMemoryStore_Read-14 | 129968 | 4616 | 17528 | 11 |
| BenchmarkMemoryStore_ReadAll-14 | 7134 | 85072 | 81976 | 3 |
| BenchmarkMemoryStore_Subscribe-14 | 4536 | 116022 | 179980 | 700 |

**./internal/aresrecovery**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkGlobalTracerSpans-14 | 395737 | 1346 | 10032 | 5 |
| BenchmarkGlobalTracerTraceMessage-14 | 6863476 | 88.94 | 294 | 0 |
| BenchmarkGlobalTracerTraceTask-14 | 6064809 | 95.38 | 266 | 0 |
| BenchmarkSandboxReplayRecoveryChain-14 | 198320 | 2682 | 7629 | 66 |
| BenchmarkSandboxSimulateAgentDeath-14 | 299272 | 2027 | 5211 | 51 |

**./internal/errors**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkFmtErrorfMultipleWraps-14 | 2380538 | 250.6 | 216 | 6 |
| BenchmarkFmtErrorfW-14 | 7249936 | 72.39 | 64 | 2 |
| BenchmarkWrap-14 | 1000000000 | 0.2741 | 0 | 0 |
| BenchmarkWrapMultipleWraps-14 | 1000000000 | 0.5534 | 0 | 0 |

**./internal/fabric/task**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkFabric_Create-14 | 1618850 | 357.8 | 357 | 4 |
| BenchmarkFabric_IsReady-14 | 38030846 | 16.41 | 0 | 0 |
| BenchmarkFabric_ReadyTasks-14 | 1525789 | 379.9 | 960 | 4 |
| BenchmarkFabric_RunQuantum-14 | 428768 | 1313 | 1161 | 16 |
| BenchmarkFabric_Schedule-14 | 1087756 | 552.1 | 444 | 10 |

**./internal/kernel**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkL2GraphGrowthChain64-14 | 4 | 131798844 | 1406496 | 13733 |
| BenchmarkSchedulerDrain100Tasks-14 | 620 | 1025741 | 578154 | 4525 |
| BenchmarkSchedulerDrainEmpty-14 | 69714750 | 8.626 | 0 | 0 |

**./internal/knowledge/compiler**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkDefaultCompiler_AllFormats/nodes_10-14 | 20510 | 29934 | 33953 | 616 |
| BenchmarkDefaultCompiler_AllFormats/nodes_100-14 | 2180 | 279340 | 409878 | 5777 |
| BenchmarkDefaultCompiler_AllFormats/nodes_50-14 | 3951 | 140487 | 185991 | 2915 |
| BenchmarkDefaultCompiler_AllFormats/nodes_500-14 | 400 | 1433721 | 2034687 | 28610 |
| BenchmarkDefaultCompiler_JSONFormat/nodes_10-14 | 57559 | 10249 | 10270 | 271 |
| BenchmarkDefaultCompiler_JSONFormat/nodes_100-14 | 5563 | 101243 | 133006 | 2619 |
| BenchmarkDefaultCompiler_JSONFormat/nodes_50-14 | 12165 | 48566 | 58465 | 1316 |
| BenchmarkDefaultCompiler_JSONFormat/nodes_500-14 | 1173 | 523335 | 624831 | 13026 |
| BenchmarkDefaultCompiler_MarkdownFormat/nodes_10-14 | 116774 | 5227 | 6268 | 112 |
| BenchmarkDefaultCompiler_MarkdownFormat/nodes_100-14 | 10000 | 54804 | 98266 | 1020 |
| BenchmarkDefaultCompiler_MarkdownFormat/nodes_50-14 | 21111 | 27126 | 40595 | 517 |
| BenchmarkDefaultCompiler_MarkdownFormat/nodes_500-14 | 2360 | 275757 | 455169 | 5027 |
| BenchmarkDefaultCompiler_PromptFormat/nodes_10-14 | 133365 | 4486 | 5947 | 92 |
| BenchmarkDefaultCompiler_PromptFormat/nodes_100-14 | 13368 | 45170 | 73288 | 819 |
| BenchmarkDefaultCompiler_PromptFormat/nodes_50-14 | 29912 | 20833 | 29514 | 416 |
| BenchmarkDefaultCompiler_PromptFormat/nodes_500-14 | 2876 | 211760 | 340765 | 4026 |

**./internal/knowledge/linker**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkArchitectureLinker/objs_10-14 | 655707 | 874.4 | 1104 | 10 |
| BenchmarkArchitectureLinker/objs_100-14 | 15337 | 39409 | 166960 | 85 |
| BenchmarkArchitectureLinker/objs_50-14 | 52921 | 11397 | 41624 | 48 |
| BenchmarkArchitectureLinker/objs_500-14 | 231 | 2295114 | 9097963 | 367 |
| BenchmarkDecisionLinker/objs_10-14 | 379044 | 1547 | 800 | 25 |
| BenchmarkDecisionLinker/objs_100-14 | 38061 | 16524 | 10880 | 295 |
| BenchmarkDecisionLinker/objs_50-14 | 74799 | 8208 | 5280 | 145 |
| BenchmarkDecisionLinker/objs_500-14 | 7424 | 82477 | 55680 | 1495 |
| BenchmarkSimilarityLinker/objs_10-14 | 75126 | 7840 | 22544 | 49 |
| BenchmarkSimilarityLinker/objs_100-14 | 876 | 693770 | 3008454 | 419 |
| BenchmarkSimilarityLinker/objs_50-14 | 4312 | 136838 | 499832 | 214 |
| BenchmarkSimilarityLinker/objs_500-14 | 31 | 18648626 | 79839289 | 2032 |
| BenchmarkTimelineLinker/objs_10-14 | 2100205 | 285.5 | 384 | 8 |
| BenchmarkTimelineLinker/objs_100-14 | 331195 | 1828 | 3120 | 11 |
| BenchmarkTimelineLinker/objs_50-14 | 621680 | 1004 | 1488 | 10 |
| BenchmarkTimelineLinker/objs_500-14 | 65720 | 8885 | 13488 | 13 |

**./internal/knowledge/pipeline**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkDefaultNormalizer_AlreadyNormalized-14 | 11376536 | 53.88 | 24 | 1 |
| BenchmarkDefaultNormalizer_Normalize-14 | 1000000 | 503.2 | 688 | 10 |

**./internal/knowledge/planner**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkKnowledgePlanner_Plan-14 | 841971 | 740.1 | 1008 | 14 |
| BenchmarkKnowledgePlanner_PlanComplexQuery-14 | 429614 | 1320 | 1553 | 18 |

**./internal/knowledge/retriever**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkRetriever_Retrieve/objs_10-14 | 584 | 945513 | 895867 | 7797 |
| BenchmarkRetriever_Retrieve/objs_100-14 | 100 | 13019297 | 11161381 | 100202 |
| BenchmarkRetriever_Retrieve/objs_50-14 | 100 | 6374007 | 8058819 | 44456 |
| BenchmarkRetriever_Retrieve/objs_500-14 | 6 | 138850868 | 106346208 | 1029675 |
| BenchmarkRetriever_RetrieveMultipleFormats-14 | 100 | 12886081 | 11094381 | 99854 |

**./internal/knowledge/store/memory**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkStore_Delete-14 | 1675011 | 357.8 | 709 | 11 |
| BenchmarkStore_Get-14 | 12325896 | 49.38 | 13 | 1 |
| BenchmarkStore_QueryByType-14 | 34734115 | 17.41 | 0 | 0 |
| BenchmarkStore_Save-14 | 1852016 | 316.6 | 623 | 12 |
| BenchmarkStore_SaveBatch10-14 | 204832 | 3100 | 6235 | 114 |
| BenchmarkStore_SaveBatch100-14 | 19659 | 32338 | 60913 | 1104 |
| BenchmarkStore_Search-14 | 9465836 | 60.68 | 48 | 2 |

**./internal/runtime**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
| BenchmarkAgentPoolConcurrentRegisterStartStop/pool=16-14 | 3822 | 165927 | 35568 | 606 |
| BenchmarkAgentPoolConcurrentRegisterStartStop/pool=256-14 | 230 | 2552515 | 511502 | 8660 |
| BenchmarkAgentPoolConcurrentRegisterStartStop/pool=64-14 | 955 | 656924 | 130798 | 2221 |
| BenchmarkAgentPoolResurrection/pool=16-14 | 3861 | 158775 | 36703 | 612 |
| BenchmarkAgentPoolResurrection/pool=64-14 | 964 | 627093 | 137940 | 2258 |
|---|---|---|---|---|

**./internal/runtime/ares_evolution**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkAdaptiveMutation/adaptive_rate-14 | 2788 | 218695 | 551272 | 3351 |
| BenchmarkAdaptiveMutation/fixed_rate-14 | 2797 | 217713 | 551277 | 3352 |
| BenchmarkDreamCycle_SingleRun-14 | 2415312 | 234.3 | 272 | 4 |
| BenchmarkFullPipeline-14 | 64 | 9597854 | 6631365 | 63814 |
| BenchmarkWiredSystem_Creation/pop_10-14 | 13762 | 43781 | 27119 | 129 |
| BenchmarkWiredSystem_Creation/pop_100-14 | 5404 | 114916 | 97481 | 858 |
| BenchmarkWiredSystem_Creation/pop_20-14 | 10000 | 52881 | 34912 | 210 |
| BenchmarkWiredSystem_Creation/pop_50-14 | 7968 | 75066 | 58334 | 453 |
| BenchmarkWiredSystem_IdleEvolution/100_generations-14 | 31 | 19031789 | 13277796 | 127554 |
| BenchmarkWiredSystem_IdleEvolution/10_generations-14 | 310 | 1892012 | 1328004 | 12757 |
| BenchmarkWiredSystem_IdleEvolution/50_generations-14 | 63 | 9562511 | 6642412 | 63774 |

**./internal/runtime/ares_evolution/genome**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkApplyFitnessSharing/pop_10-14 | 5752 | 105515 | 55152 | 16 |
| BenchmarkApplyFitnessSharing/pop_100-14 | 438 | 1381531 | 539968 | 106 |
| BenchmarkApplyFitnessSharing/pop_200-14 | 206 | 2873668 | 1079363 | 206 |
| BenchmarkApplyFitnessSharing/pop_50-14 | 898 | 680076 | 290448 | 56 |
| BenchmarkApplyFitnessSharing/pop_500-14 | 76 | 7937787 | 2696770 | 506 |
| BenchmarkApplyFitnessSharing_CustomSampling/limit0_exact-14 | 961 | 627117 | 290448 | 56 |
| BenchmarkApplyFitnessSharing_CustomSampling/limit100_size20-14 | 229 | 2555897 | 1079282 | 206 |
| BenchmarkApplyFitnessSharing_CustomSampling/limit20_size10-14 | 494 | 1155374 | 539808 | 106 |
| BenchmarkBest/pop_100-14 | 2692509 | 213.4 | 528 | 3 |
| BenchmarkBest/pop_1000-14 | 685789 | 881.4 | 528 | 3 |
| BenchmarkBest/pop_500-14 | 1224865 | 417.4 | 528 | 3 |
| BenchmarkCloneStrategy/params_100-14 | 295105 | 2178 | 5144 | 5 |
| BenchmarkCloneStrategy/params_20-14 | 1000000 | 535.8 | 1432 | 5 |
| BenchmarkCloneStrategy/params_5-14 | 3407923 | 175.6 | 528 | 3 |
| BenchmarkCloneStrategy/params_50-14 | 572359 | 1069 | 2584 | 5 |
| BenchmarkCrossoverParallel-14 | 287168 | 2305 | 3050 | 31 |
| BenchmarkCrossoverUniform-14 | 282073 | 2106 | 3045 | 31 |
| BenchmarkCrossoverUniform_LargeParams-14 | 39000 | 15576 | 21126 | 38 |
| BenchmarkEvolve_MultipleGenerations/100_generations-14 | 23400 | 25716 | 34425 | 600 |
| BenchmarkEvolve_MultipleGenerations/10_generations-14 | 237640 | 2577 | 3442 | 60 |
| BenchmarkEvolve_MultipleGenerations/50_generations-14 | 46975 | 12923 | 17212 | 300 |
| BenchmarkEvolve_OneGeneration/pop_10-14 | 2342240 | 253.7 | 296 | 6 |
| BenchmarkEvolve_OneGeneration/pop_100-14 | 2330431 | 258.2 | 344 | 6 |
| BenchmarkEvolve_OneGeneration/pop_20-14 | 2349704 | 254.5 | 344 | 6 |
| BenchmarkEvolve_OneGeneration/pop_50-14 | 2379936 | 254.4 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_10-14 | 2344905 | 257.5 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_100-14 | 2301610 | 259.1 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_20-14 | 2288458 | 256.9 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_200-14 | 2248326 | 263.9 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_5-14 | 2333937 | 257.1 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_50-14 | 2343584 | 257.0 | 344 | 6 |
| BenchmarkEvolve_Scaling/pop_500-14 | 1782680 | 283.3 | 296 | 6 |
| BenchmarkEvolveOnIdle_OneGeneration/pop_10-14 | 2344047 | 254.2 | 344 | 6 |
| BenchmarkEvolveOnIdle_OneGeneration/pop_100-14 | 2308219 | 258.4 | 344 | 6 |
| BenchmarkEvolveOnIdle_OneGeneration/pop_20-14 | 2296868 | 257.3 | 344 | 6 |
| BenchmarkEvolveOnIdle_OneGeneration/pop_50-14 | 2186066 | 260.7 | 344 | 6 |
| BenchmarkPopulationCreation/size_10-14 | 41516 | 13618 | 12685 | 66 |
| BenchmarkPopulationCreation/size_100-14 | 13368 | 46014 | 71299 | 606 |
| BenchmarkPopulationCreation/size_20-14 | 35278 | 16810 | 19177 | 126 |
| BenchmarkPopulationCreation/size_50-14 | 21813 | 27572 | 38683 | 306 |
| BenchmarkRealWorldEvolution-14 | 54 | 11285061 | 5015098 | 61568 |
| BenchmarkRouletteWheelSelection/pop_10-14 | 3246802 | 185.9 | 320 | 4 |
| BenchmarkRouletteWheelSelection/pop_100-14 | 220598 | 2713 | 3424 | 7 |
| BenchmarkRouletteWheelSelection/pop_1000-14 | 3987 | 151331 | 29760 | 10 |
| BenchmarkRouletteWheelSelection/pop_500-14 | 14528 | 41423 | 15424 | 9 |
| BenchmarkSortByScore/pop_10-14 | 2950009 | 197.0 | 136 | 3 |
| BenchmarkSortByScore/pop_100-14 | 100777 | 6081 | 952 | 3 |
| BenchmarkSortByScore/pop_1000-14 | 4147 | 139532 | 8249 | 3 |
| BenchmarkSortByScore/pop_500-14 | 13317 | 53045 | 4152 | 3 |
| BenchmarkStats/pop_100-14 | 842 | 719403 | 4216 | 9 |
| BenchmarkStats/pop_1000-14 | 13 | 45417173 | 57120 | 12 |
| BenchmarkStats/pop_500-14 | 28 | 20278832 | 29816 | 10 |
| BenchmarkTournamentSelection/pop_200/k=10-14 | 15669 | 38329 | 192096 | 401 |
| BenchmarkTournamentSelection/pop_200/k=2-14 | 18336 | 33509 | 192096 | 401 |
| BenchmarkTournamentSelection/pop_200/k=3-14 | 18007 | 33189 | 192096 | 401 |
| BenchmarkTournamentSelection/pop_200/k=5-14 | 17398 | 34692 | 192096 | 401 |
| BenchmarkTournamentSelection/pop_50/k=10-14 | 106016 | 5616 | 13608 | 101 |
| BenchmarkTournamentSelection/pop_50/k=2-14 | 171458 | 3806 | 13608 | 101 |
| BenchmarkTournamentSelection/pop_50/k=3-14 | 152558 | 4034 | 13608 | 101 |
| BenchmarkTournamentSelection/pop_50/k=5-14 | 142882 | 4418 | 13608 | 101 |
| BenchmarkTruncationSelection/pop_10-14 | 3819781 | 161.8 | 136 | 3 |
| BenchmarkTruncationSelection/pop_100-14 | 106563 | 5829 | 952 | 3 |
| BenchmarkTruncationSelection/pop_1000-14 | 3805 | 157699 | 8248 | 3 |
| BenchmarkTruncationSelection/pop_500-14 | 10000 | 61513 | 4152 | 3 |

**./internal/runtime/eval**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkAgentTestRunner_RunSingle-14 | 2130114 | 293.0 | 320 | 5 |
| BenchmarkExactMatchEvaluator_Evaluate-14 | 251219986 | 2.325 | 0 | 0 |
| BenchmarkLoader_Load-14 | 13225 | 44634 | 34063 | 601 |
| BenchmarkReportGenerator_GenerateMarkdown-14 | 186298 | 3198 | 4258 | 76 |
| BenchmarkToolUsageEvaluator_Evaluate-14 | 21775554 | 27.42 | 0 | 0 |

**./internal/runtime/evolution**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkUngatedPatcher_Evaluate-14 | 93505875 | 6.267 | 0 | 0 |
| BenchmarkDiffEngine_Workflow-14 | 1000000 | 531.5 | 352 | 3 |
| BenchmarkFullEvolutionCycle-14 | 65258 | 9206 | 13870 | 157 |
| BenchmarkKnowledgeGenome_Mutate-14 | 1561977 | 385.2 | 960 | 11 |
| BenchmarkRecoveryGenome_Mutate-14 | 1329824 | 473.9 | 1280 | 21 |
| BenchmarkWorkflowGenome_Mutate-14 | 19112 | 31612 | 47607 | 534 |

**./internal/runtime/memory/distillation**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkConflictDetection-14 | 580879 | 1061 | 0 | 0 |
| BenchmarkDistillation-14 | 19911 | 29405 | 4435 | 98 |
| BenchmarkExperienceExtraction-14 | 4701 | 132159 | 22864 | 267 |
| BenchmarkMemoryClassification-14 | 297762 | 2050 | 592 | 15 |
| BenchmarkMemoryOperations/Classification-14 | 2159322 | 286.6 | 64 | 3 |
| BenchmarkMemoryOperations/Create-14 | 7130823 | 85.40 | 24 | 1 |
| BenchmarkNoiseFilter-14 | 84712 | 7053 | 592 | 11 |
| BenchmarkScoreMemory-14 | 188377 | 3247 | 768 | 8 |
| BenchmarkStringOperations/Format-14 | 9324412 | 65.34 | 64 | 3 |
| BenchmarkTopNFilter-14 | 256652 | 3573 | 16328 | 10 |

**./internal/runtime/protocol/skills**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkCatalogBuild100Skills-14 | 78 | 7000764 | 2085096 | 17174 |
| BenchmarkCatalogSearch100Skills/fts5-hit-14 | 39049 | 14899 | 1009 | 26 |
| BenchmarkCatalogSearch100Skills/keyword-fallback-14 | 40464 | 14794 | 632 | 23 |
| BenchmarkExperienceBestMatch100-14 | 190228 | 3200 | 96 | 2 |

**./internal/tools/planner**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkBridge_DirectExecution-14 | 232789 | 2734 | 1255 | 9 |
| BenchmarkBridge_PlannerFallback-14 | 90970 | 6529 | 6848 | 57 |
| BenchmarkPlanner_FullPipeline-14 | 148581 | 3701 | 4780 | 38 |
| BenchmarkPlanner_Parallel-14 | 141294 | 4014 | 6247 | 48 |
| BenchmarkPlanner_UnknownRequest-14 | 708855 | 806.3 | 208 | 5 |

**./internal/tools/resources/core**

| Benchmark | Iterations | ns/op | B/op | allocs/op |
|---|---|---|---|---|
| BenchmarkConcurrentToolExecution-14 | 4527225 | 133.0 | 8 | 1 |
| BenchmarkParameterValidation-14 | 86558739 | 7.011 | 0 | 0 |
| BenchmarkResultCreation/Error-14 | 1000000000 | 0.2638 | 0 | 0 |
| BenchmarkResultCreation/Success-14 | 1000000000 | 0.2723 | 0 | 0 |
| BenchmarkToolExecution-14 | 33680664 | 17.77 | 0 | 0 |
| BenchmarkToolFiltering-14 | 247032 | 2445 | 4568 | 10 |
| BenchmarkToolRegistration-14 | 136197 | 4405 | 9464 | 12 |
