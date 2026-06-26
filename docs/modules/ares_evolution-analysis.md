# Performance Analysis: `internal/ares_evolution/`

## 1. Module Overview

### Purpose

The `ares_evolution` module implements an autonomous evolution system for agent strategies using a genetic algorithm (GA) approach. It enables the system to continuously optimize agent behavior through selection, crossover, mutation, and fitness evaluation of strategy populations.

### Key Files

| File | Purpose |
|------|---------|
| `genetic/population.go` | Core GA population management, selection, crossover, mutation |
| `genetic/scoring.go` | Tiered scoring pipeline (cache -> LLM -> heuristic) |
| `genetic/memory_aware_scoring.go` | Memory-augmented scoring with experience lookups |
| `genetic/budget.go` | LLM call budget tracking and gating |
| `genetic/cache.go` | Score cache with LRU-like eviction |
| `genetic/hash.go` | Strategy hashing for cache keys |
| `scheduler.go` | Evolution cycle scheduling and trigger logic |
| `dream_cycle.go` | Full evolution orchestration (mutate -> test -> deploy) |
| `regression_tester.go` | Arena-style strategy comparison |
| `feedback_recorder.go` | Outcome recording with circuit breaker |
| `shadow_evaluator.go` | Safe deployment via shadow evaluation |
| `genome_wiring.go` | Wiring layer connecting all components |
| `pg_strategy_store.go` | PostgreSQL-backed strategy persistence |

### Architecture

```
Scheduler (trigger)
    |
    v
DreamCycle (orchestrate)
    |
    +-- Mutator (generate candidates)
    +-- RegressionTester (evaluate candidates)
    +-- ShadowEvaluator (safe deploy gate)
    +-- FeedbackRecorder (outcome tracking)
    |
    v
GenomePopulationAdapter
    |
    +-- Population (GA core)
    +-- TieredScorer (cost-controlled scoring)
    +-- ScoreCache (avoid redundant LLM calls)
    +-- Budget (LLM call gating)
```

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Recommended Fix |
|----------|----------|---------|-----------------|
| **HIGH** | `genetic/population.go:254-260` | `applyFitnessSharingExact` allocates `m*m` distance matrix every generation | Pre-allocate matrix on Population struct, reuse across generations |
| **HIGH** | `genetic/population.go:314-321` | `applyFitnessSharingSampled` allocates `[]int` of size `m` per agent in loop | Use `sync.Pool` or pre-allocate once and shuffle in-place |
| **HIGH** | `genetic/scoring.go:127-142` | `TieredScorer.Score` acquires mutex on every cache hit for stats | Use `atomic.Int64` for counter fields instead of mutex |
| **HIGH** | `scheduler.go:200-203` | `RecordScore` shifts slice with `s.scores = s.scores[1:]` causing memory leak | Use ring buffer or `copy()` to avoid retaining references to old elements |
| **MED** | `genetic/population.go:157-168` | `measureNumericDiversityLocked` runs O(n^2) pairwise comparison | Sample pairs for large populations (>50 agents) |
| **MED** | `genetic/population.go:209-240` | `measureCategoricalDiversityLocked` runs O(n^2) pairwise comparison | Sample pairs for large populations |
| **MED** | `genetic/scoring.go:155` | `MakeEntry` calls `time.Now()` on every cache put | Batch timestamp or use generation counter |
| **MED** | `genetic/cache.go:96-108` | `Put` scans all entries for oldest on eviction | Use doubly-linked list for O(1) LRU eviction |
| **MED** | `feedback_recorder.go:97-103` | `Register` trims outcomes slice with full copy | Use ring buffer for fixed-size outcome storage |
| **LOW** | `genetic/population.go:957-962` | `Snapshot` deep-clones all agents on every call | Consider COW (copy-on-write) or shallow clone option |
| **LOW** | `genetic/hash.go:47-51` | `StrategyHash` allocates sorted key slice on every call | Cache hash on Strategy struct or use sync.Pool |

---

## 3. Code Quality Issues

| Category | Location | Issue | Recommendation |
|----------|----------|-------|----------------|
| **Concurrency** | `scheduler.go:149` | `evolveMu` and `mu` are separate mutexes protecting related state | Consolidate to single mutex or document lock ordering |
| **Concurrency** | `dream_cycle.go:174-179` | `taskCount` read and `lastCycle` read under same lock, but `config` read also under that lock | Separate concerns: task count atomic, config immutable after init |
| **Concurrency** | `feedback_recorder.go:111-121` | Circuit breaker check and update spans two separate critical sections | Use single lock acquisition for check-and-update |
| **Memory** | `scheduler.go:200` | `s.scores = s.scores[1:]` keeps backing array alive | Use `copy()` to compact or ring buffer |
| **Memory** | `genetic/population.go:600` | `history` slice grows unbounded when `HistoryMaxSize=0` | Default to reasonable max (e.g., 1000) |
| **Error Handling** | `genetic/scoring.go:186-199` | LLM scorer panic is silently swallowed, logged at Warn level | Consider escalating after N consecutive panics |
| **Error Handling** | `dream_cycle.go:553-566` | `findWinner` returns `nil` error when `quickResults[i]` is nil | Distinguish between "no candidates" and "all failed" |
| **Testing** | `genetic/population.go:652` | `rand.NewSource` is not safe for concurrent use, but `rng` is accessed under lock | Verify all `rng` access paths hold the lock |
| **Design** | `genetic/scoring.go:50-62` | `TieredScorer` mixes caching, budgeting, and scoring logic | Extract cache and budget into separate concerns |
| **Design** | `genome_wiring.go:249-285` | `Run()` method is 136 lines with deeply nested conditionals | Extract scoring pipeline setup and guardrail checks |
| **Naming** | `genetic/population.go:129` | `FitnessSharingSigma` is actually the penalty coefficient, not sigma | Rename to `FitnessSharingPenaltyCoeff` |

---

## 4. Concurrency Analysis

### 4.1 Lock Contention Points

**Population.mu (RWMutex)**
- `ScoreAgents()` holds write lock for entire scoring loop (`population.go:975-999`)
- `Evolve()` holds write lock for entire evolution cycle (`population.go:736-857`)
- `Snapshot()` holds read lock while deep-cloning all agents (`population.go:953-962`)
- `Stats()` holds read lock while computing diversity (`population.go:1152-1185`)

**Contention Risk**: High. Scoring and evolution are long-running operations that block all reads.

**Mitigation**: `EvolveAfterScoring` (`population.go:1354-1390`) acquires/releases lock 3 times (pre-score, evolve, post-score), allowing interleaved reads between phases. This is documented as intentional.

### 4.2 Race Conditions

**TieredScorer.stats** (`scoring.go:56-62`)
```go
// PROBLEM: stats fields protected by mutex, but Get/Record interleave
ts.mu.Lock()
ts.cacheHits++  // line 137
ts.totalScored++ // line 138
ts.mu.Unlock()
```
- Two goroutines calling `Score()` simultaneously will serialize on this mutex
- **Fix**: Use `atomic.Int64` for all counter fields

**FeedbackRecorder.circuitBreaker** (`feedback_recorder.go:111-121`)
```go
// PROBLEM: Check and update are not atomic
r.mu.Lock()
if r.circuitBreakerConsecutiveErrors >= r.circuitBreakerMaxErrors {
    // ... check cooldown
}
r.mu.Unlock()
// ... do work ...
r.mu.Lock()
r.circuitBreakerConsecutiveErrors++  // line 143
r.mu.Unlock()
```
- Between the two critical sections, another goroutine could also pass the check
- **Fix**: Combine check-and-increment into single critical section

### 4.3 Goroutine Leaks

**EvolutionScheduler.OnAgentEnd** (`scheduler.go:295-318`)
```go
go func() {
    if err := eg.Wait(); err != nil {
        slog.ErrorContext(ctx, "[Evolution] Evolution goroutine exited with error", ...)
    }
}()
```
- The bare goroutine will leak if `eg.Wait()` blocks indefinitely
- `evolveCancel` provides cancellation, but `Shutdown()` must be called
- **Risk**: Medium. Mitigated by `Shutdown()` in `genome_wiring.go:1411-1419`

### 4.4 Safe Patterns

- `Population.bestEver` updated only under write lock (`population.go:1015-1025`)
- `ShadowEvaluator` uses `sync.RWMutex` consistently (`shadow_evaluator.go:91`)
- `Budget` uses `sync.Mutex` for all operations (`budget.go:13`)
- `ScoreCache.Get` uses `RLock` for concurrent reads (`cache.go:71`)

---

## 5. Specific Code Issues and Fixes

### 5.1 Memory Leak in Score Window

**File**: `scheduler.go:200-203`

```go
// PROBLEM: s.scores[1:] keeps reference to underlying array
func (s *EvolutionScheduler) RecordScore(score float64) {
    s.scoreMu.Lock()
    defer s.scoreMu.Unlock()

    if len(s.scores) >= scoreWindowSize {
        s.scores = s.scores[1:]  // LEAK: old elements not GC'd
    }
    s.scores = append(s.scores, score)
}
```

**Fix**:
```go
func (s *EvolutionScheduler) RecordScore(score float64) {
    s.scoreMu.Lock()
    defer s.scoreMu.Unlock()

    if len(s.scores) >= scoreWindowSize {
        // Copy to new slice to allow GC of old elements
        newScores := make([]float64, scoreWindowSize-1)
        copy(newScores, s.scores[1:])
        s.scores = newScores
    }
    s.scores = append(s.scores, score)
}
```

### 5.2 O(n^2) Distance Matrix Allocation

**File**: `genetic/population.go:254-260`

```go
// PROBLEM: Allocates m*m float64 slice every generation
distMatrix := make([]float64, m*m)
for ki := 0; ki < m; ki++ {
    for kj := ki + 1; kj < m; kj++ {
        dist := paramDistance(scored[ki], scored[kj], keys, ranges)
        distMatrix[ki*m+kj] = dist
        distMatrix[kj*m+ki] = dist
    }
}
```

**Fix**: Pre-allocate on Population struct
```go
type Population struct {
    // ... existing fields ...
    distMatrix []float64 // Pre-allocated distance matrix, resized as needed
}

func (p *Population) applyFitnessSharingExact(...) {
    m := len(scoredIdx)
    needed := m * m
    if cap(p.distMatrix) < needed {
        p.distMatrix = make([]float64, needed)
    }
    distMatrix := p.distMatrix[:needed]
    // ... rest of logic ...
}
```

### 5.3 Cache Eviction Scan

**File**: `genetic/cache.go:96-108`

```go
// PROBLEM: O(n) scan for oldest entry on every eviction
if c.maxSize > 0 && len(c.entries) >= c.maxSize {
    if _, exists := c.entries[hash]; !exists {
        var oldestHash uint64
        oldestTime := int64(0)
        for h, e := range c.entries {
            if oldestTime == 0 || e.Timestamp < oldestTime {
                oldestTime = e.Timestamp
                oldestHash = h
            }
        }
        delete(c.entries, oldestHash)
    }
}
```

**Fix**: Use doubly-linked list for O(1) LRU
```go
import "container/list"

type ScoreCache struct {
    mu       sync.RWMutex
    entries  map[uint64]*list.Element
    order    *list.List  // Front = newest, Back = oldest
    maxSize  int
    // ...
}

func (c *ScoreCache) Put(hash uint64, entry CacheEntry) {
    c.mu.Lock()
    defer c.mu.Unlock()

    if elem, ok := c.entries[hash]; ok {
        c.order.MoveToFront(elem)
        elem.Value = entry
        return
    }

    if c.maxSize > 0 && len(c.entries) >= c.maxSize {
        oldest := c.order.Back()
        delete(c.entries, oldest.Value.(CacheEntry).Hash)
        c.order.Remove(oldest)
    }

    elem := c.order.PushFront(entry)
    c.entries[hash] = elem
}
```

### 5.4 Mutex Contention in TieredScorer

**File**: `genetic/scoring.go:56-62, 135-142`

```go
// PROBLEM: Mutex acquired on every Score() call for stats
type TieredScorer struct {
    mu             sync.Mutex  // Lines 56
    cacheHits      int64       // Line 57
    llmCalls       int64       // Line 58
    heuristicCalls int64       // Line 59
    fallbacks      int64       // Line 60
    totalScored    int64       // Line 61
}

// In Score():
ts.mu.Lock()      // Line 136
ts.cacheHits++    // Line 137
ts.totalScored++  // Line 138
ts.mu.Unlock()    // Line 139
```

**Fix**: Use atomic operations
```go
type TieredScorer struct {
    cacheHits      atomic.Int64
    llmCalls       atomic.Int64
    heuristicCalls atomic.Int64
    fallbacks      atomic.Int64
    totalScored    atomic.Int64
}

// In Score():
ts.cacheHits.Add(1)
ts.totalScored.Add(1)
```

### 5.5 Non-Atomic Circuit Breaker Check

**File**: `feedback_recorder.go:111-148`

```go
// PROBLEM: Two separate critical sections for check and update
r.mu.Lock()
if r.circuitBreakerConsecutiveErrors >= r.circuitBreakerMaxErrors {
    if time.Since(r.circuitBreakerOpenedAt) < r.circuitBreakerCooldown {
        r.mu.Unlock()
        return nil
    }
    r.circuitBreakerConsecutiveErrors = 0
}
r.mu.Unlock()

// ... work happens ...

r.mu.Lock()
r.circuitBreakerConsecutiveErrors++  // Line 143
if r.circuitBreakerConsecutiveErrors >= r.circuitBreakerMaxErrors {
    r.circuitBreakerOpenedAt = time.Now()
}
r.mu.Unlock()
```

**Fix**: Single critical section with deferred unlock
```go
func (r *FeedbackRecorder) shouldSkipFeedback() bool {
    r.mu.Lock()
    defer r.mu.Unlock()

    if r.circuitBreakerConsecutiveErrors >= r.circuitBreakerMaxErrors {
        if time.Since(r.circuitBreakerOpenedAt) < r.circuitBreakerCooldown {
            return true
        }
        r.circuitBreakerConsecutiveErrors = 0
    }
    return false
}

func (r *FeedbackRecorder) recordCircuitBreakerFailure() {
    r.mu.Lock()
    defer r.mu.Unlock()

    r.circuitBreakerConsecutiveErrors++
    if r.circuitBreakerConsecutiveErrors >= r.circuitBreakerMaxErrors {
        r.circuitBreakerOpenedAt = time.Now()
    }
}
```

---

## 6. Priority Action Items

### P0 - Critical (Fix Immediately)

1. **Memory leak in `RecordScore`** (`scheduler.go:200`)
   - Impact: Unbounded memory growth in long-running processes
   - Effort: 15 minutes
   - Fix: Use `copy()` to compact slice

2. **Race condition in `FeedbackRecorder`** (`feedback_recorder.go:111-148`)
   - Impact: Circuit breaker may not open when expected
   - Effort: 30 minutes
   - Fix: Consolidate to single critical section

### P1 - High (Fix This Sprint)

3. **Distance matrix allocation** (`population.go:254`)
   - Impact: GC pressure every evolution cycle
   - Effort: 1 hour
   - Fix: Pre-allocate on Population struct

4. **Per-agent allocation in sampled fitness sharing** (`population.go:314`)
   - Impact: O(n) allocations per generation
   - Effort: 30 minutes
   - Fix: Pre-allocate once, shuffle in-place

5. **Mutex contention in TieredScorer** (`scoring.go:56`)
   - Impact: Serialized scoring in concurrent scenarios
   - Effort: 30 minutes
   - Fix: Use `atomic.Int64`

### P2 - Medium (Fix Next Sprint)

6. **Cache eviction O(n) scan** (`cache.go:96`)
   - Impact: Linear degradation as cache fills
   - Effort: 2 hours
   - Fix: Implement LRU with doubly-linked list

7. **O(n^2) diversity computation** (`population.go:157-240`)
   - Impact: Quadratic scaling for large populations
   - Effort: 2 hours
   - Fix: Sample pairs for populations > 50

8. **Snapshot deep-clone overhead** (`population.go:957`)
   - Impact: Expensive for large populations
   - Effort: 1 hour
   - Fix: Add shallow clone option or COW semantics

### P3 - Low (Backlog)

9. **StrategyHash allocation** (`hash.go:47`)
   - Impact: Minor GC pressure
   - Fix: Cache hash on Strategy struct

10. **History unbounded growth** (`population.go:600`)
    - Impact: Memory in long-running systems
    - Fix: Default `HistoryMaxSize` to 1000

---

## Appendix: Profiling Recommendations

### Recommended pprof endpoints

```bash
# CPU profile during evolution cycle
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Heap allocations
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine count (detect leaks)
go tool pprof http://localhost:6060/debug/pprof/goroutine

# Mutex contention
go tool pprof http://localhost:6060/debug/pprof/mutex
```

### Key metrics to monitor

- `population_evolution_duration_seconds` - Evolution cycle latency
- `scoring_cache_hit_ratio` - Cache effectiveness
- `scoring_llm_calls_total` - LLM cost tracking
- `population_diversity_score` - Convergence detection
- `circuit_breaker_state` - Feedback service health

### Benchmark targets

```
BenchmarkFitnessSharing-8          < 5ms  for n=20
BenchmarkFitnessSharing-8          < 50ms for n=100
BenchmarkScoreCache-8              < 1us  per Get/Put
BenchmarkStrategyHash-8            < 500ns per hash
BenchmarkPopulationSnapshot-8      < 1ms  for n=20
```
