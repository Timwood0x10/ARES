# ARES Evolution Intelligence Layer API

**Version**: v0.2.4 (2026-06-30)  
**Package**: `github.com/Timwood0x10/ares/internal/ares_evolution/genome`

## Overview

The Intelligence Layer provides LLM-guided genetic algorithm evolution, enhancing traditional GA operations with semantic understanding and experience-based mutation hints.

---

## Core Components

### 1. Hypothesis Package

#### ParamRanges - LLM Parameter Constraints

**Purpose**: Safe parameter clamping for all LLM hyperparameters.

```go
var paramRanges = map[string]struct {
    min float64
    max float64
}{
    "temperature":   {min: 0.0, max: 2.0},
    "top_k":         {min: 1.0, max: 100.0},
    "top_p":         {min: 0.0, max: 1.0},
    "max_tokens":    {min: 50.0, max: 4096.0},
    "memory_limit":  {min: 1.0, max: 20.0},
    "recall_limit":  {min: 1.0, max: 10.0},
    "chunk_size":    {min: 100.0, max: 2000.0},
}
```

#### clampParam - Safe Parameter Validation

**Signature**:
```go
func clampParam(name string, value float64) float64
```

**Behavior**:
- Returns clamped value within `[min, max]` range
- Logs debug warning if param name not in whitelist
- Guards against non-float64 params (memory_limit, recall_limit)

**Usage**:
```go
// Ensure temperature stays within valid range
temp := clampParam("temperature", 2.5)  // Returns 2.0 (max)
temp := clampParam("temperature", -0.1) // Returns 0.0 (min)
temp := clampParam("temperature", 0.7)  // Returns 0.7 (valid)
```

---

### 2. Knowledge Package

#### Record - Experience Storage

**Signature**:
```go
func (r *Recorder) Record(ctx context.Context, exp Experience) error
```

**Confidence Calculation** (Updated):
- **Ratio-based**: `confidence = successCount / (successCount + failCount)`
- **No longer hardcoded 0.5/0.6 thresholds**

**Sorting Algorithm** (Optimized):
- Changed from **bubble sort** → `sort.SliceStable` (O(n log n))
- Preserves original order for equal elements

**Fields**:
```go
type Experience struct {
    TaskType     string
    Strategy     Strategy
    SuccessCount int  // Moved into struct literal
    FailCount    int
    Confidence   float64  // Ratio-based
}
```

---

### 3. Population Package

#### computeStatsLocked - Eliminated Duplicate Logic

**Before** (Duplicated):
```go
func (p *Population) Stats() PopulationStats {
    // Scoring logic duplicated here
}

func (p *Population) appendHistoryLocked() {
    // Same scoring logic duplicated again
}
```

**After** (Extracted):
```go
func (p *Population) computeStatsLocked() PopulationStats {
    // Single source of truth
}

func (p *Population) Stats() PopulationStats {
    return p.computeStatsLocked()
}

func (p *Population) appendHistoryLocked() {
    stats := p.computeStatsLocked()
    // Use extracted function
}
```

---

### 4. Guided Pipeline

#### HintsForTask - Removed Param Filtering

**Before**:
```go
// Filtered out temperature/top_k params
if param == "temperature" || param == "top_k" {
    continue  // ← Removed this filter
}
```

**After**:
```go
// All params now pass through (clampParam handles validation)
for param, value := range hints {
    clamped := clampParam(param, value)
    // Process all params uniformly
}
```

**TOCTOU Caveat** (Documented):
- Async feedback design may have Time-Of-Check-To-Time-Of-Use race
- Hints are generated from history, may be stale during application

---

### 5. Meta Evolution

#### Generation Guard - Tuning Threshold

**Before**:
```go
if p.generation < 10 {
    return  // Skip tuning too early
}
```

**After**:
```go
if p.generation < 20 {
    return  // Wait for meaningful metrics
}
```

**Rationale**: Need more generations before metrics stabilize for adaptive tuning.

---

### 6. Multi-Objective Optimization

#### ParetoRank - Pareto Front Ranking

**Signature**:
```go
func ParetoRank(strategies []Strategy) [][]Strategy
```

**Returns**:
- Rank 0: Pareto-optimal strategies (non-dominated)
- Rank 1: Strategies dominated only by Rank 0
- Rank N: Strategies dominated by all previous ranks

**Fallback Warning**:
```go
// Warn when mixing single/multi-objective strategies
if hasSingleObj && hasMultiObj {
    slog.Warn("mixed single/multi-objective strategies, falling back to Score comparison")
}
```

---

### 7. Reflection Package

#### extractJSONBracketOuter - Bracket Depth Parsing

**Purpose**: Extract JSON from LLM responses with nested braces.

**Behavior** (Correct):
- Tracks bracket depth: `{` increments, `}` decrements
- Extracts outermost balanced JSON object
- No behavior change, improved error messages

**Error Handling**:
```go
// Improved error messages
if depth < 0 {
    return "", fmt.Errorf("unbalanced brackets: depth=%d at position %d", depth, i)
}
```

---

## Public API Methods (New Exports)

### Population Export Methods

```go
// StagnantGenerations returns count of consecutive stagnant generations
func (p *Population) StagnantGenerations() int

// CurrentMutationRate returns adaptive mutation rate
func (p *Population) CurrentMutationRate() float64
```

**Usage**:
```go
pop := NewPopulation(config)
// Monitor evolution health
stagnant := pop.StagnantGenerations()
if stagnant > 10 {
    log.Warn("population stagnating, consider increasing mutation rate")
}

rate := pop.CurrentMutationRate()
fmt.Printf("Current mutation rate: %.2f\n", rate)
```

---

## Validation Strategies

### ValidSelectionStrategies Whitelist

```go
var validSelectionStrategies = map[string]bool{
    "tournament": true,
    "roulette":   true,
    "rank":       true,
}
```

**Behavior**: Invalid strategies rejected early, preventing runtime errors.

---

## Truncation Strategy (New Option)

```go
type TruncationConfig struct {
    Enabled     bool
    MaxSize     int     // Truncate population to this size after evolution
    PreserveElites bool  // Always keep eliteCount strategies
}
```

**Usage**:
```go
config := PopulationConfig{
    Truncation: TruncationConfig{
        Enabled:       true,
        MaxSize:       50,
        PreserveElites: true,
    },
}
pop := NewPopulation(config)
```

---

## Test Coverage

**新增938行测试覆盖**：

| Test Suite | Coverage |
|------------|----------|
| ParetoRank edge cases | ✅ |
| clampParam bounds | ✅ |
| extractJSONBracketOuter | ✅ |
| FormatHypotheses | ✅ |
| ApplyHypothesis | ✅ |
| GuidedPipeline nil guards | ✅ |
| DistillFromHistory | ✅ |
| LLMReflector error paths | ✅ |
| Selection strategies | ✅ |
| Multi-objective helpers | ✅ |

**Total**: 938 lines of test code added for Intelligence Layer.

---

## Performance Improvements

### Sorting Optimization

- **Before**: Bubble sort O(n²)
- **After**: `sort.SliceStable` O(n log n)
- **Impact**: Significant speedup for large experience databases

### Code Deduplication

- Eliminated duplicate scoring logic in `computeStatsLocked`
- Single source of truth for population statistics
- Reduced maintenance burden

---

## Error Handling Improvements

### Nil Guards

```go
// GuidedPipeline nil population guard
if pop == nil {
    return nil, fmt.Errorf("population is nil")
}
```

### Parameter Validation

```go
// clampParam with debug logging
if !exists {
    slog.Debug("unknown param: %s, skipping clamp", name)
    return value  // Return original value
}
```

---

## Migration Guide

### From v0.2.3 to v0.2.4

#### 1. Update Confidence Thresholds

**Before**:
```go
if confidence > 0.6 {  // Hardcoded threshold
    // ...
}
```

**After**:
```go
// Use ratio-based confidence
confidence := successCount / (successCount + failCount)
if confidence > 0.7 {  // Dynamic threshold
    // ...
}
```

#### 2. Use clampParam for LLM Params

```go
// Before: Manual validation
if temperature < 0 { temperature = 0 }
if temperature > 2 { temperature = 2 }

// After: Use clampParam
temperature = clampParam("temperature", temperature)
```

#### 3. Use New Public Methods

```go
// Monitor evolution health
stagnant := pop.StagnantGenerations()
rate := pop.CurrentMutationRate()
```

---

## Best Practices

### 1. Always Clamp LLM Parameters

```go
// Safe parameter application
strategy.Temperature = clampParam("temperature", strategy.Temperature)
strategy.TopK = clampParam("top_k", strategy.TopK)
strategy.MemoryLimit = int(clampParam("memory_limit", float64(strategy.MemoryLimit)))
```

### 2. Use ValidSelectionStrategies

```go
// Validate strategy before applying
if !validSelectionStrategies[selectionType] {
    return fmt.Errorf("invalid selection strategy: %s", selectionType)
}
```

### 3. Monitor Stagnation

```go
// Detect evolution stagnation
if pop.StagnantGenerations() > 20 {
    pop.AdaptiveMutationRate *= 1.5  // Increase mutation
}
```

---

## API Stability

**Stable APIs**:
- `clampParam`: Stable, will not change
- `ParetoRank`: Stable, Pareto front algorithm standardized
- `StagnantGenerations()`: Stable, public export
- `CurrentMutationRate()`: Stable, public export

**Experimental APIs**:
- `TruncationConfig`: Beta, may evolve
- `GuidedPipeline` async feedback: Experimental

---

## Version History

**v0.2.4 (2026-06-30)**:
- Intelligence Layer hardening after review
- +938 lines test coverage
- Pareto front ranking
- Safe parameter clamping
- Code deduplication
- Error handling improvements

**v0.2.3 (Previous)**:
- Initial LLM-guided DreamCycle
- Intelligence Layer introduction

---

## References

- [GA Benchmark Report](file:///Users/scc/go/src/ARES/benchmarks/ga_benchmark_report.md)
- [Autonomous Evolution Guide](file:///Users/scc/go/src/ARES/docs/en/features/autonomous-evolution.md)

---

## License

MIT License