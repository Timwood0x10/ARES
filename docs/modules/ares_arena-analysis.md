# Ares Arena Module Performance Analysis

## 1. Module Overview

The ares_arena module implements a chaos engineering layer that proves Ares is a self-healing runtime. It deliberately injects faults (kill agents, remove nodes/edges, partition networks, corrupt memory, etc.) and measures the system's ability to recover. It includes A/B regression testing with statistical significance, survival testing with random chaos injection, YAML-based scenario definitions, and a 3-dimensional resilience scoring system.

### Key Files

| File | Purpose |
|------|---------|
| `internal/ares_arena/regression.go` | A/B regression tester with Welch's t-test and adaptive batched execution |
| `internal/ares_arena/scorer.go` | Ensemble scorer, exact match scorer, map scorer |
| `internal/ares_arena/service.go` | Core arena service: action execution, history, event emission |
| `internal/ares_arena/survival.go` | Random chaos injection over time with status monitoring |
| `internal/ares_arena/metrics.go` | MetricsCollector: per-action-type aggregation, recovery timing |
| `internal/ares_arena/scenario.go` | YAML scenario loading, validation, and execution with warmup/cooldown |
| `internal/ares_arena/injector.go` | Fault injection via runtime and DAG provider interfaces |
| `internal/ares_arena/score.go` | 3-dimensional resilience scoring: availability, recovery, consistency |
| `internal/ares_arena/types.go` | Action, Result, Stats, ActionType constants |

### Architecture

The module has four subsystems:

1. **Injector layer** (`injector.go`): Wraps `RuntimeProvider` and `DAGProvider` interfaces to inject chaos. Each method delegates to the runtime or DAG with logging.

2. **Service layer** (`service.go`): Orchestrates action execution, records results, emits events, and maintains aggregate stats. The `Execute` method is the central dispatch for all action types.

3. **Testing layer** (`regression.go`, `survival.go`, `scenario.go`): Three testing modes:
   - **Regression**: A/B comparison with statistical significance (Welch's t-test)
   - **Survival**: Random chaos over time with resilience scoring
   - **Scenario**: YAML-defined action sequences with verification

4. **Scoring layer** (`score.go`, `scorer.go`): 3-dimensional scoring (availability 40%, recovery 30%, consistency 30%) with ensemble scorer support.

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Fix |
|----------|----------|---------|-----|
| HIGH | `regression.go:310-346` | `runStrategy` executes N scoring runs sequentially in a for-loop. Each `rt.scorer.Score(input)` call may involve LLM evaluation or agent execution. For `BaselineRuns=20` and `CompareRuns=20`, this is 40 sequential scoring calls | Parallelize scoring runs within each strategy using errgroup with concurrency limit |
| HIGH | `survival.go:117-153` | `RunSurvival` holds the main goroutine in a ticker loop for the entire duration (default 30 minutes). During this time, the goroutine is blocked and cannot be reused. Multiple concurrent survival tests consume one goroutine each | This is acceptable for a testing tool, but document the goroutine cost and consider an async API |
| MEDIUM | `service.go:131-143` | `Service.Execute` acquires `s.mu.Lock()` to append to `s.actions` and update `s.stats`. Since `Execute` is called on every chaos action (including during survival tests at 10s intervals), this lock serializes all action execution | Use a lock-free ring buffer for action history and atomic counters for stats |
| MEDIUM | `metrics.go:60-76` | `RecordActionResult` acquires `mc.mu.Lock()` on every action. During survival tests, this is called every 10 seconds. The lock also blocks `Snapshot()` which iterates all action counts | Use atomic counters for per-action-type metrics |
| MEDIUM | `regression.go:219-303` | `runAdaptive` creates a new `errgroup` on each batch iteration (line 255). For small batch sizes, the errgroup overhead (goroutine creation, channel ops) dominates the actual scoring work | Reuse errgroup or use a worker pool across batches |
| MEDIUM | `survival.go:223-296` | `randomChaosAction` calls `s.injector.AvailableAgentIDs()` up to 4 times per action generation (lines 249, 254, 259, 272). Each call iterates all agents in the runtime | Cache agent IDs once per action generation |
| MEDIUM | `scenario.go:183-269` | `RunScenarioReport` executes actions sequentially with `time.After` delays between them. For scenarios with many actions and small delays, the timer overhead accumulates | Use a single timer with Reset for sequential delays |
| LOW | `regression.go:376-385` | `computeMean` iterates the full score slice. Called multiple times per regression test (once for old, once for new, inside `computeVariance` again). Total: 6 iterations over the score data | Compute mean and variance in a single pass using Welford's online algorithm |
| LOW | `regression.go:432-472` | `computeSignificance` calls `computeMean` twice and `computeVariance` twice, each iterating the full score array. For small sample sizes (typical: 5-20), this is negligible but still wasteful | Compute all statistics in a single pass |
| LOW | `metrics.go:106-162` | `Snapshot` iterates all recovery durations to compute min/max/avg. For long-running tests with thousands of samples, this is O(n) per snapshot call | Maintain running min/max/avg with incremental updates |

---

## 3. Code Quality Issues

| Severity | Location | Problem | Recommendation |
|----------|----------|---------|----------------|
| HIGH | `regression.go:523-524` | `validateRegressionConfig` checks `cfg.BaselineRuns <= 0 && cfg.BaselineRuns != 0` which is a tautology (always false). The condition `x <= 0 && x != 0` simplifies to `x < 0`. The intent was probably to allow 0 as "use default" but the logic is wrong | Fix to `cfg.BaselineRuns < 0` or remove the condition since 0 is handled by the default-application code below |
| MEDIUM | `survival.go:239` | `rand.Intn` is used with `#nosec G404` annotation, acknowledging it uses a weak PRNG. For chaos testing this is acceptable, but the comment should explain why | Add a comment explaining that cryptographic randomness is not needed for chaos action selection |
| MEDIUM | `service.go:60-169` | `Execute` uses a type-switch with 13 cases, each calling a different injector method. Adding a new action type requires modifying this switch. The pattern does not scale | Use a registry pattern: `map[ActionType]func(ctx, Injector, Action) error` |
| MEDIUM | `score.go:83-89` | `calcAvailability` ignores the `recovered` parameter (marked with `_`). The function signature suggests it should factor in recovery, but it only uses `total` and `failed` | Either use the parameter or remove it from the signature |
| MEDIUM | `metrics.go:79-103` | `RecordRecovery`, `RecordFailover`, `RecordConsistency` are deprecated but still present and called by tests. The deprecation warnings use `slog.Warn` which pollutes test output | Suppress warnings in test mode or remove the deprecated methods |
| MEDIUM | `scenario.go:146-168` | `RunScenarioReport` warns about unsupported features (`parallel_actions`, `max_concurrent`, `depends_on`) but still proceeds. Users may expect these features to work based on the config field presence | Either implement the features or remove the config fields until ready |
| LOW | `regression.go:361-362` | `RegressionResult.OldStrategyID` and `NewStrategyID` are set to `fmt.Sprintf("%v", cfg.OldStrategy)` which produces the Go struct representation, not a meaningful ID | Accept explicit strategy IDs in the config or use a named interface |
| LOW | `scorer.go:26-28` | `NewEnsembleScorer` uses variadic `any` pairs instead of a typed struct slice. This sacrifices type safety for API brevity | Use `[]struct{ Scorer; Weight float64 }` or functional options |

---

## 4. Code Snippets: Problems and Proposed Fixes

### Problem 1: Sequential scoring in regression runs

**`regression.go:310-346`**
```go
func (rt *RegressionTester) runStrategy(ctx context.Context, strategy any, n int, testCases []any) ([]float64, error) {
    scores := make([]float64, 0, n)
    for i := 0; i < n; i++ {
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        default:
        }
        score, err := rt.scorer.Score(input)
        // ...
        scores = append(scores, score)
    }
    return scores, nil
}
```

**Proposed fix:** Parallelize with a concurrency limit.
```go
func (rt *RegressionTester) runStrategy(ctx context.Context, strategy any, n int, testCases []any, maxConcurrent int) ([]float64, error) {
    scores := make([]float64, n)
    g, gCtx := errgroup.WithContext(ctx)
    g.SetLimit(maxConcurrent)

    for i := 0; i < n; i++ {
        i := i
        g.Go(func() error {
            select {
            case <-gCtx.Done():
                return gCtx.Err()
            default:
            }
            input := strategy
            if len(testCases) > 0 {
                input = TestCaseInput{Strategy: strategy, TestCase: testCases[i%len(testCases)], Index: i}
            }
            score, err := rt.scorer.Score(input)
            if err != nil {
                return fmt.Errorf("arena: score run %d: %w", i, err)
            }
            scores[i] = score
            return nil
        })
    }
    return scores, g.Wait()
}
```

### Problem 2: Tautological validation condition

**`regression.go:523-524`**
```go
if cfg.BaselineRuns <= 0 && cfg.BaselineRuns != 0 { // 0 means "use default"
    return ErrInvalidRuns
}
```

The condition `x <= 0 && x != 0` is equivalent to `x < 0`. Since the comment says "0 means use default", this should be:

```go
if cfg.BaselineRuns < 0 {
    return ErrInvalidRuns
}
```

### Problem 3: Repeated AvailableAgentIDs calls

**`survival.go:223-296`**
```go
func (s *Service) randomChaosAction() Action {
    // ...
    switch actionType {
    case ActionKillAgent:
        ids := s.injector.AvailableAgentIDs()  // Call 1
        // ...
    case ActionRemoveNode:
        ids := s.injector.AvailableAgentIDs()  // Call 2
        // ...
    case ActionRemoveEdge:
        ids := s.injector.AvailableAgentIDs()  // Call 3
        // ...
    case ActionPauseAgent, ActionResumeAgent, /* ... */:
        ids := s.injector.AvailableAgentIDs()  // Call 4
        // ...
    }
}
```

**Proposed fix:** Cache agent IDs once.
```go
func (s *Service) randomChaosAction() Action {
    ids := s.injector.AvailableAgentIDs()  // Single call
    actionTypes := []ActionType{ /* ... */ }
    actionType := actionTypes[rand.Intn(len(actionTypes))]

    action := Action{ /* ... */ }

    switch actionType {
    case ActionKillAgent:
        if len(ids) > 0 {
            action.TargetID = ids[rand.Intn(len(ids))]
        }
    // ... use cached ids ...
    }
}
```

### Problem 4: Multiple-pass statistics computation

**`regression.go:376-400`** - `computeMean` and `computeVariance` each iterate the full slice independently.

**Proposed fix:** Single-pass Welford's algorithm.
```go
func computeStats(scores []float64) (mean, variance float64) {
    if len(scores) == 0 {
        return 0, 0
    }
    mean = scores[0]
    m2 := 0.0
    for i := 1; i < len(scores); i++ {
        delta := scores[i] - mean
        mean += delta / float64(i+1)
        delta2 := scores[i] - mean
        m2 += delta * delta2
    }
    if len(scores) > 1 {
        variance = m2 / float64(len(scores)-1)
    }
    return mean, variance
}
```

---

## 5. Priority Action Items

1. **[P0 - Performance]** Parallelize `runStrategy` scoring calls in `regression.go:310-346`. For LLM-based scorers, the current sequential execution makes regression tests N times slower than necessary.

2. **[✓] [P0 - Correctness]** Fix the tautological validation condition in `regression.go:523-524`. Changed `x <= 0 && x != 0` to `x < 0`.

3. **[P1 - Performance]** Cache `AvailableAgentIDs()` result in `randomChaosAction` (`survival.go:223-296`) to avoid up to 4 redundant runtime queries per action generation.

4. **[P1 - Performance]** Use atomic counters for `MetricsCollector` per-action-type stats (`metrics.go:60-76`) to reduce lock contention during survival tests.

5. **[P1 - Performance]** Compute mean and variance in a single pass using Welford's algorithm in `regression.go:376-400`.

6. **[P2 - Code Quality]** Refactor `Service.Execute` type-switch into a registry pattern for extensibility.

7. **[P2 - Code Quality]** Remove or implement the unsupported `parallel_actions`, `max_concurrent`, and `depends_on` scenario config fields to avoid user confusion.

8. **[P3 - Code Quality]** Fix `calcAvailability` signature to either use the `recovered` parameter or remove it.
