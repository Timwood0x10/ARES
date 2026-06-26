# Ares Memory Module Performance Analysis

## 1. Module Overview

The ares_memory module provides unified memory management for the Ares agent framework. It coordinates session memory (conversation history), task memory (execution context), and distilled experience storage. The distillation pipeline extracts problem-solution pairs from conversations, scores their importance, generates embeddings, detects conflicts, and stores experiences for future retrieval.

### Key Files

| File | Purpose |
|------|---------|
| `internal/ares_memory/manager_impl.go` | MemoryManager implementation: session/task CRUD, distillation, search |
| `internal/ares_memory/distillation/distiller.go` | Multi-phase distillation pipeline: extract, classify, score, embed, resolve conflicts |
| `internal/ares_memory/distillation/extractor.go` | ExperienceExtractor: problem-solution pair extraction with cross-turn support |
| `internal/ares_memory/distillation/scorer.go` | ImportanceScorer: keyword-based and type-based importance scoring |
| `internal/ares_memory/context/cache.go` | In-memory cache with TTL eviction and LRU cache implementation |
| `internal/ares_memory/embedding/pipeline.go` | EmbeddingPipeline: centralized embedding generation with spec builders |

### Architecture

The module has three layers:

1. **Session layer**: `SessionMemory` manages conversation history with TTL-based cleanup. `ContextCleaner` strips tool call noise before LLM calls.

2. **Distillation layer**: An 8-phase pipeline (extract, classify/score, top-N pre-filter, embed, conflict resolve, final top-N, solution cap, experience store sync) that converts conversations into reusable experiences.

3. **Storage layer**: `ExperienceRepository` provides vector-based similarity search. `EmbeddingPipeline` centralizes embedding generation with canonical spec builders.

---

## 2. Performance Bottlenecks

| Severity | Location | Problem | Fix |
|----------|----------|---------|-----|
| HIGH | `distiller.go:466-530` | `resolveConflictsPhase` makes a vector search call (`DetectConflict`) for EACH embedded memory sequentially. For N memories, this is N sequential network round-trips to the embedding store | Batch conflict detection: generate all vectors first, then do a single batch similarity search |
| HIGH | `manager_impl.go:340-375` | `BuildContext` iterates all messages and builds a string via repeated `+=` concatenation in a loop. Each `+=` allocates a new string, making this O(n^2) in total message length | Use `strings.Builder` (already used in `BuildPromptMessages` but not here) |
| HIGH | `distiller.go:672-706` | `enforceSolutionCap` loads ALL solution memories for a tenant via `GetByMemoryType`, sorts them, then deletes the excess one-by-one. For tenants with 5000+ solutions, this loads the entire collection into memory | Use a database-level query with LIMIT/OFFSET or maintain a count index |
| MEDIUM | `distiller.go:255-338` | `classifyAndScorePhase` creates a new `Memory` struct with a `map[string]interface{}` metadata map for every experience. The metadata map contains 10+ string keys, each requiring heap allocation | Use a struct-based metadata type or pool the maps |
| MEDIUM | `extractor.go:60-131` | `ExtractExperiences` iterates all messages with nested cross-turn detection logic. The cross-turn check at line 90 accesses `messages[i+2]` and `messages[i+3]` without bounds checking beyond `i+3 < len(messages)`, but the main loop only checks `i < len(messages)-1` | The bounds check at line 90 (`i+3 < len(messages)`) is correct, but the logic is hard to follow. Extract into a named predicate |
| MEDIUM | `cache.go:55-71` | `Cache.Set` calls `evictOldest()` when at capacity, which iterates ALL items to find the oldest by expiration time. This is O(n) per insert when the cache is full | Use a min-heap or sorted structure for expiration-based eviction |
| MEDIUM | `cache.go:196-207` | `LRUCache.Get` acquires a write lock (`c.mu.Lock()`) even for read operations because it needs to move the accessed item to front. This serializes all cache reads | Consider a lock-free LRU or sharded cache for read-heavy workloads |
| MEDIUM | `manager_impl.go:472-517` | `StoreDistilledTask` launches concurrent goroutines via `errgroup` to store each experience, but each goroutine extracts metadata fields via type assertions (`mem.Metadata["problem"].(string)`) with repeated warn logging on failure | Extract metadata once before the loop, validate upfront |
| LOW | `scorer.go:49-159` | `ScoreMemory` creates a new `content` string via `strings.ToLower(problem + " " + solution)` on every call. For large inputs, this is an unnecessary allocation | Pass pre-lowered content or lower in-place |
| LOW | `scorer.go:56-95` | Keyword scoring iterates a map of 30+ keywords with `strings.Contains` for each. This is O(k * n) where k is keyword count and n is content length | Use Aho-Corasick or a trie for multi-pattern matching, or accept the constant-factor cost for small keyword sets |
| LOW | `embedding/pipeline.go:66-78` | `Embed` validates `spec.Text` and `spec.Prefix` on every call. These are already validated during `BuildSpec` | Remove redundant validation or make it debug-only |

---

## 3. Code Quality Issues

| Severity | Location | Problem | Recommendation |
|----------|----------|---------|----------------|
| HIGH | `manager_impl.go:340-375` | `BuildContext` uses `fmt.Sprintf` for each message role in a loop, creating format strings like `"User: %s\n"`. This is both slow (format parsing) and hard to localize | Use string constants or a message formatter interface |
| MEDIUM | `distiller.go:255-338` | `classifyAndScorePhase` does 6+ type assertions on `mem.Metadata` fields with warn logging on each failure. The metadata was created just lines above in the same function, so these assertions should never fail | Use typed metadata or validate once at creation |
| MEDIUM | `extractor.go:295-330` | `calculateConfidence` uses magic numbers (0.5 base, 0.1/0.05 bonuses, -0.2 penalty) with no documentation of the scoring rationale. Different content lengths trigger different multipliers without clear justification | Extract constants and document the scoring model |
| MEDIUM | `extractor.go:457-462` | `parseUserProfile` has a duplicate professions list (line 461: `"developer", "engineer", "programmer", "student", "teacher"` appears twice in the same slice literal) | Remove duplicate entries |
| MEDIUM | `cache.go:55-71` | `Cache.Set` evicts by expiration time, not by access pattern. Items inserted later with longer TTLs will never be evicted before items inserted earlier with shorter TTLs, even if the early items are frequently accessed | Consider LRU or LFU eviction as the primary strategy, with TTL as a secondary cleanup |
| LOW | `distiller.go:104-111` | `atomicMetrics` and `DistillationMetrics` have identical `String()` methods with different receiver types. The duplication is error-prone | Generate `DistillationMetrics` from `atomicMetrics` via `GetMetrics().String()` |
| LOW | `manager_impl.go:206-208` | `CreateSession` generates session IDs using `time.Now().UnixNano()` which is not collision-safe under high concurrency | Use UUID or add a random suffix |
| LOW | `manager_impl.go:378-379` | `CreateTask` generates task IDs the same way as session IDs with `time.Now().UnixNano()`. Same collision risk | Use UUID or atomic counter |

---

## 4. Code Snippets: Problems and Proposed Fixes

### Problem 1: O(n^2) string concatenation in BuildContext

**`manager_impl.go:340-375`**
```go
var contextBuilder string
if len(cleaned) > 0 {
    contextBuilder = "Previous conversation history:\n\n"
    for _, msg := range cleaned {
        switch msg.Role {
        case memctx.RoleUser:
            contextBuilder += fmt.Sprintf("User: %s\n", truncpkg.WithEllipsis(msg.Content, 100))
        // ... more cases ...
        }
    }
    contextBuilder += "\nCurrent request:\n"
}
contextBuilder += input
```

**Proposed fix:** Use `strings.Builder`.
```go
var sb strings.Builder
if len(cleaned) > 0 {
    sb.WriteString("Previous conversation history:\n\n")
    for _, msg := range cleaned {
        truncContent := truncpkg.WithEllipsis(msg.Content, 100)
        switch msg.Role {
        case memctx.RoleUser:
            sb.WriteString("User: ")
        case memctx.RoleAssistant:
            sb.WriteString("Assistant: ")
        // ...
        }
        sb.WriteString(truncContent)
        sb.WriteByte('\n')
    }
    sb.WriteString("\nCurrent request:\n")
}
sb.WriteString(input)
return sb.String(), nil
```

### Problem 2: Sequential conflict detection

**`distiller.go:466-530`** - Each memory triggers a separate `DetectConflict` call.

**Proposed fix:** Batch the conflict detection.
```go
func (d *Distiller) resolveConflictsBatch(ctx context.Context, tenantID string, embedded []memWithEmbedding) []Memory {
    // Collect all valid vectors
    vectors := make([][]float64, 0, len(embedded))
    validIndices := make([]int, 0, len(embedded))
    for i, ew := range embedded {
        if ew.valid {
            vectors = append(vectors, ew.mem.Vector)
            validIndices = append(validIndices, i)
        }
    }

    // Single batch search for all conflicts
    conflicts, err := d.resolver.DetectConflictsBatch(ctx, vectors, tenantID)
    // ... process batch results ...
}
```

### Problem 3: O(n) cache eviction

**`cache.go:111-125`**
```go
func (c *Cache) evictOldest() {
    var oldest *CacheItem
    var oldestKey string
    for key, item := range c.items {
        if oldest == nil || item.Expiration.Before(oldest.Expiration) {
            oldest = item
            oldestKey = key
        }
    }
    if oldestKey != "" {
        delete(c.items, oldestKey)
    }
}
```

**Proposed fix:** Use a min-heap for O(log n) eviction.
```go
import "container/heap"

type expirationHeap []*CacheItem

func (h expirationHeap) Len() int           { return len(h) }
func (h expirationHeap) Less(i, j int) bool { return h[i].Expiration.Before(h[j].Expiration) }
func (h expirationHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expirationHeap) Push(x any)        { *h = append(*h, x.(*CacheItem)) }
func (h *expirationHeap) Pop() any          { old := *h; n := len(old); x := old[n-1]; *h = old[:n-1]; return x }
```

### Problem 4: Duplicate professions list

**`extractor.go:457-462`**
```go
professions := []string{"developer", "engineer", "programmer", "student", "teacher",
    "developer", "engineer", "programmer", "student", "teacher"}
```

**Proposed fix:** Remove duplicates.
```go
professions := []string{"developer", "engineer", "programmer", "student", "teacher"}
```

---

## 5. Priority Action Items

1. **[✓] [P0 - Performance]** Replace `BuildContext` string concatenation with `strings.Builder` in `manager_impl.go:340-375` and `ToBuildContextFormat` in `manager.go:263-286`. Eliminates O(n^2) allocation.

2. **[P0 - Performance]** Batch conflict detection in `distiller.go:466-530`. The current N sequential vector search calls are the dominant cost in the distillation pipeline. A single batch search would reduce latency by N-fold.

3. **[P1 - Performance]** Optimize `enforceSolutionCap` in `distiller.go:672-706` to use database-level sorting and deletion instead of loading all solutions into memory.

4. **[P1 - Performance]** Replace `Cache.evictOldest()` O(n) linear scan with a min-heap for O(log n) eviction in `cache.go:111-125`.

5. **[P2 - Performance]** Consider sharding the `LRUCache` to reduce lock contention for read-heavy workloads in `cache.go:196-207`.

6. **[✓] [P2 - Code Quality]** Fix duplicate professions list in `extractor.go:457-462`. Removed duplicate entries.

7. **[P2 - Code Quality]** Extract magic numbers in `calculateConfidence` into named constants with documentation.

8. **[P3 - Correctness]** Replace `time.Now().UnixNano()` session/task ID generation with UUID or atomic counter to prevent collisions under high concurrency.
