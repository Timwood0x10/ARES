# Eval Module Performance Analysis

## 1. Module Overview

The eval module provides LLM evaluation and benchmarking capabilities, including test suite execution, multiple evaluator strategies (exact match, keyword presence, tool usage, LLM judge), concurrent test execution, and result reporting.

### Key Files

| File | Purpose |
|------|---------|
| `internal/eval/concurrent_runner.go` | Parallel test execution with configurable concurrency and per-test timeouts |
| `internal/eval/evaluator.go` | Evaluator interface and implementations (exact match, keyword, tool usage, registry) |
| `internal/eval/llm_judge.go` | LLM-based judge evaluator with dimension averaging and JSON parsing |
| `internal/eval/runner.go` | Core TestRunner and AgentExecutor interfaces |
| `internal/eval/types.go` | Test case, result, suite, and score types with YAML/JSON serialization |

### Architecture

The module follows a layered design:

1. **Types layer** (`types.go`): Defines `TestCase`, `TestResult`, `TestSuite`, `EvalScore` with serialization support including custom `Duration` type for YAML/JSON.

2. **Evaluator layer** (`evaluator.go`, `llm_judge.go`): Strategy pattern for scoring. `EvaluatorRegistry` provides thread-safe named evaluator management.

3. **Runner layer** (`runner.go`, `concurrent_runner.go`): `TestRunner` interface with `ConcurrentRunner` wrapper that adds `errgroup`-based parallel execution with configurable concurrency limits.

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Fix |
|----------|----------|---------|-----|
| HIGH | `concurrent_runner.go:96-128` | Each test case creates a new goroutine via `eg.Go()` even when `maxParallel=1`. The errgroup overhead (channel operations, goroutine creation) is unnecessary for sequential mode | Add a fast path: when `maxParallel == 1`, run tests in a simple for-loop without errgroup |
| MEDIUM | `llm_judge.go:189` | LLM judge calls are inherently sequential per test case. When used with `ConcurrentRunner`, each goroutine blocks on a synchronous LLM call. No batching or streaming of judge requests | Implement batched LLM evaluation where multiple test cases are scored in a single prompt |
| MEDIUM | `llm_judge.go:244-263` | `parseResponse` does a full JSON parse attempt on every candidate substring found in the LLM response. For malformed responses with many `{` characters, this is O(n^2) in the number of potential JSON starts | Parse once from the first `{` found; fail fast on parse error rather than trying all candidates |
| MEDIUM | `evaluator.go:97-105` | `ToolUsageEvaluator.Evaluate` uses nested O(n*m) loop to check tool usage. For test cases with many expected tools and many used tools, this is quadratic | Use a set (map[string]bool) for `result.ToolsUsed` lookup |
| LOW | `llm_judge.go:90-95` | `WithPrompt` silently ignores template parse errors. If the custom template is malformed, the evaluator falls back to the default template without notifying the caller | Return the parse error or log a warning |
| LOW | `types.go:122-147` | `ToJSON` methods on `TestCase`, `TestResult`, `TestSuite` use `json.MarshalIndent` which is slower than `json.Marshal`. These are called for serialization, not display | Use `json.Marshal` for non-display contexts; reserve `MarshalIndent` for human-readable output |
| LOW | `concurrent_runner.go:100-103` | Pre-flight `select` on `egCtx.Done()` before starting work is unnecessary since `r.inner.RunSingle(testCtx, tc)` will immediately return on cancelled context | Remove the redundant context check |

---

## 3. Code Quality Issues

| Severity | Location | Problem | Recommendation |
|----------|----------|---------|----------------|
| HIGH | `llm_judge.go:90-95` | `WithPrompt` option swallows template parse errors silently. A malformed template causes silent fallback to the default prompt, producing incorrect evaluation results | Return error from the option or validate at construction time |
| MEDIUM | `evaluator.go:29` | `ExactMatchEvaluator.Evaluate` accepts `ctx` but only uses `_ = ctx`. The context parameter should be propagated for timeout support in future LLM-based evaluators | Use `ctx` for at least `ctx.Err()` check |
| MEDIUM | `llm_judge.go:105-109` | `WithChinesePrompt` and `WithEnglishPrompt` use `template.Must` which panics on parse error. If the default prompt constants contain invalid template syntax, the entire application crashes at option-application time | Return errors instead of panicking |
| MEDIUM | `types.go:94-96` | `TestResult.Metrics` map is never populated by the runner. The field exists in the type but no code writes to it, creating confusion about its purpose | Either populate it during evaluation or remove the field |
| LOW | `evaluator.go:146-157` | `EvaluatorRegistry.Register` allows overwriting an existing evaluator without warning. This can cause subtle bugs when two components register the same name | Log a warning or return an error on duplicate registration |
| LOW | `llm_judge.go:245-263` | `extractJudgeJSON` has two code paths (markdown fence vs raw JSON) with overlapping logic. The markdown fence path can silently fail and fall through to the raw path, potentially extracting the wrong JSON | Make the fence extraction more robust or clearly document the precedence |

---

## 4. Code Snippets: Problems and Proposed Fixes

### Problem 1: Silent template error swallowing

**`llm_judge.go:88-95`**
```go
func WithPrompt(tmpl string) LLMJudgeOption {
    return func(e *LLMJudgeEvaluator) {
        parsed, err := template.New("judge").Parse(tmpl)
        if err == nil {
            e.promptTmpl = parsed
        }
        // Error is silently ignored!
    }
}
```

**Proposed fix:** Return an error-aware option or validate at construction.
```go
func WithPrompt(tmpl string) (LLMJudgeOption, error) {
    parsed, err := template.New("judge").Parse(tmpl)
    if err != nil {
        return nil, fmt.Errorf("invalid judge prompt template: %w", err)
    }
    return func(e *LLMJudgeEvaluator) {
        e.promptTmpl = parsed
    }, nil
}
```

### Problem 2: Unnecessary errgroup overhead for sequential mode

**`concurrent_runner.go:91-136`** - Full errgroup path even for `maxParallel=1`.

**Proposed fix:** Add a sequential fast path.
```go
func (r *ConcurrentRunner) RunSuite(ctx context.Context, suite TestSuite) ([]TestResult, error) {
    if len(suite.TestCases) == 0 {
        return []TestResult{}, nil
    }
    results := make([]TestResult, len(suite.TestCases))

    // Fast path for sequential execution
    if r.maxParallel == 1 {
        for i, tc := range suite.TestCases {
            testCtx := ctx
            if r.timeout > 0 {
                var cancel context.CancelFunc
                testCtx, cancel = context.WithTimeout(ctx, r.timeout)
                results[i], _ = r.inner.RunSingle(testCtx, tc)
                cancel()
            } else {
                results[i], _ = r.inner.RunSingle(testCtx, tc)
            }
        }
        return results, nil
    }
    // ... errgroup path for concurrent mode
}
```

### Problem 3: O(n*m) tool usage check

**`evaluator.go:97-105`**
```go
for _, expectedTool := range testCase.ExpectedTools {
    for _, usedTool := range result.ToolsUsed {
        if expectedTool == usedTool {
            used++
            break
        }
    }
}
```

**Proposed fix:** Use a set for O(1) lookup.
```go
usedSet := make(map[string]bool, len(result.ToolsUsed))
for _, t := range result.ToolsUsed {
    usedSet[t] = true
}
for _, expected := range testCase.ExpectedTools {
    if usedSet[expected] {
        used++
    }
}
```

---

## 5. Priority Action Items

1. **[P0 - Correctness]** Fix `WithPrompt` silent error swallowing in `llm_judge.go:88-95`. A malformed custom prompt silently falls back to the default, producing incorrect evaluation scores without any indication.

2. **[P0 - Correctness]** Replace `template.Must` in `WithChinesePrompt`/`WithEnglishPrompt` (`llm_judge.go:107-115`) with error-returning variants. A panic in these options crashes the entire application.

3. **[P1 - Performance]** Add a sequential fast path to `ConcurrentRunner.RunSuite` when `maxParallel == 1` to avoid errgroup overhead (goroutine creation, channel operations) for the common sequential case.

4. **[P1 - Performance]** Optimize `ToolUsageEvaluator.Evaluate` with a set-based lookup to reduce from O(n*m) to O(n+m).

5. **[P2 - Performance]** Consider implementing batched LLM judge evaluation where multiple test cases are scored in a single LLM call to reduce API round-trips.

6. **[P2 - Code Quality]** Populate or remove the unused `TestResult.Metrics` field to avoid confusion.

7. **[P3 - Code Quality]** Add duplicate-registration warning to `EvaluatorRegistry.Register`.
