# GoAgent Performance Benchmark Report

**Date:** 2026-06-24
**Platform:** darwin/arm64 (Apple M3 Max, 14 cores)
**Go Version:** go1.26.4
**OS:** Darwin 24.6.0

---

## Summary

| Category | Benchmarks | Hot (< 1us) | Normal (1-100us) | Cold (> 100us) |
|----------|-----------|-------------|-------------------|-----------------|
| Eval | 5 | 2 | 2 | 1 |
| Handler | 3 | 1 | 2 | 0 |
| Tools/Core | 8 | 3 | 5 | 0 |
| Distillation | 10 | 1 | 8 | 1 |
| Errors | 4 | 2 | 2 | 0 |
| Event Sourcing | 6 | 0 | 5 | 1 |
| **Total** | **36** | **9** | **24** | **3** |

---

## Eval Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| ExactMatchEvaluator_Evaluate | 180.3 | 0 | 0 | Hot |
| ToolUsageEvaluator_Evaluate | 361.3 | 0 | 0 | Hot |
| AgentTestRunner_RunSingle | 2,931 | 320 | 5 | Normal |
| ReportGenerator_GenerateMarkdown | 15,764 | 4,258 | 77 | Normal |
| Loader_Load | 158,986 | 34,062 | 602 | Cold |

---

## Handler Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| StreamHandler_ConvertEvent | 97.33 | 0 | 0 | Hot |
| StreamHandler_HandleStream | 24,431 | 11,426 | 66 | Normal |
| StreamHandler_MultipleEvents | 68,097 | 39,781 | 382 | Normal |

---

## Tools/Core Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| ToolExecution | 347.3 | 0 | 0 | Hot |
| ParameterValidation | 208.3 | 0 | 0 | Hot |
| ResultCreation_Success | 125.0 | 0 | 0 | Hot |
| ResultCreation_Error | 55.33 | 0 | 0 | Hot |
| ConcurrentToolExecution | 3,736 | 1,048 | 5 | Normal |
| ToolFiltering | 9,667 | 10,200 | 10 | Normal |
| CapabilityMatching | 15,708 | 760 | 9 | Normal |
| ToolRegistration | 16,306 | 10,200 | 12 | Normal |
| CapabilityDetection | 20,167 | 1,104 | 8 | Normal |

---

## Memory Distillation Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| ConflictDetection | 2,125 | 0 | 0 | Normal |
| StringOperations_Format | 3,653 | 749 | 3 | Normal |
| MemoryOperations_Create | 3,847 | 765 | 2 | Normal |
| MemoryOperations_Classification | 5,181 | 64 | 3 | Normal |
| MemoryClassification | 8,778 | 592 | 15 | Normal |
| NoiseFilter | 20,347 | 592 | 11 | Normal |
| TopNFilter | 20,361 | 12,040 | 10 | Normal |
| ScoreMemory | 39,056 | 8,096 | 20 | Normal |
| BenchmarkDistillation | 76,861 | 8,914 | 102 | Normal |
| ExperienceExtraction | 373,708 | 21,200 | 267 | Cold |

---

## Errors Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| Wrap | 69.67 | 0 | 0 | Hot |
| WrapMultipleWraps | 69.33 | 0 | 0 | Hot |
| FmtErrorfW | 3,361 | 64 | 2 | Normal |
| FmtErrorfMultipleWraps | 4,667 | 216 | 6 | Normal |

---

## Event Sourcing Benchmarks

| Benchmark | ns/op | B/op | allocs/op | Tier |
|-----------|-------|------|-----------|------|
| MemoryStore_ConcurrentAppend | 6,222 | 608 | 6 | Normal |
| MemoryStore_Read (1000 events) | 7,278 | 17,528 | 11 | Normal |
| MemoryStore_Append | 7,653 | 598 | 7 | Normal |
| MemoryStore_AppendBatch (100 events) | 34,167 | 9,114 | 1 | Normal |
| MemoryStore_ReadAll (10000 events) | 47,056 | 81,976 | 3 | Normal |
| MemoryStore_Subscribe (100 subscribers) | 125,653 | 145,424 | 799 | Cold |

---

## Key Observations

1. **Zero-allocation hot paths**: Benchmarks across evaluation, tool execution, result creation, error wrapping, event conversion, and conflict detection all maintain 0 allocations.
2. **ConvertEvent is extremely fast**: At 97.33 ns/op with 0 allocations — the fastest handler operation.
3. **ConflictDetection remains zero-allocation**: 2,125 ns/op with 0 B/op and 0 allocs/op.
4. **Distillation benchmarks shifted**: Most distillation benchmarks are now in the Normal (1-100 us) range, with ExperienceExtraction as the only cold benchmark at 373,708 ns/op.
5. **Subscribe is the coldest operation**: 125,653 ns/op with 799 allocs for 100 subscribers.
6. **Loader_Load remains allocation-heavy**: 602 allocs/op, the highest allocation count across all benchmarks.
7. **BenchmarkDistillation introduced**: New end-to-end distillation benchmark at 76,861 ns/op with 102 allocs.

---

## Raw Logs

Full benchmark output: `benchmarks/logs/benchmark_2026-06-24.log`
