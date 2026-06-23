package scoring

import (
	"sync"
	"time"
)

// CacheEntry holds a cached score record for a strategy.
type CacheEntry struct {
	// Hash is the strategy hash this entry corresponds to.
	Hash uint64

	// Score is the cached fitness score.
	Score float64

	// ScorerType identifies which scorer produced this score (e.g., "heuristic", "llm", "arena").
	ScorerType string

	// Timestamp when this entry was created (Unix nanos).
	Timestamp int64

	// SampleCount is how many evaluation samples contributed to this score.
	SampleCount int

	// Confidence is the confidence level (0-1) of this score.
	Confidence float64
}

// ScoreCache provides thread-safe score caching for evolved strategies.
// It avoids redundant LLM calls by caching previously computed scores.
//
// Zero-value is NOT usable; use NewScoreCache to create an instance.
type ScoreCache struct {
	mu        sync.RWMutex
	entries   map[uint64]CacheEntry // hash -> cache entry
	maxSize   int                   // maximum cache entries; 0 = unlimited
	hits      int64                 // number of cache hits
	misses    int64                 // number of cache misses
	evictions int64                 // number of entries evicted due to capacity
}

// NewScoreCache creates a new score cache.
//
// Args:
//
//	maxSize - maximum entries (0 = unlimited).
//
// Returns:
//
//	*ScoreCache - the cache instance.
func NewScoreCache(maxSize int) *ScoreCache {
	return &ScoreCache{
		entries: make(map[uint64]CacheEntry),
		maxSize: maxSize,
	}
}

// Get retrieves a cached score for the given strategy hash.
// This method is thread-safe and uses a read lock for concurrent access.
//
// Args:
//
//	hash - the strategy hash to look up.
//
// Returns:
//
//	CacheEntry - the cached entry, zero value if not found.
//	bool - true if found in cache.
func (c *ScoreCache) Get(hash uint64) (CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[hash]
	if ok {
		c.mu.RUnlock()
		c.mu.Lock()
		c.hits++
		c.mu.Unlock()
		c.mu.RLock()
	} else {
		c.mu.RUnlock()
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		c.mu.RLock()
	}

	return entry, ok
}

// Put stores a score in the cache.
// If the cache is full, evicts the oldest entry (by timestamp).
// This method is thread-safe and uses a write lock.
//
// Args:
//
//	hash - the strategy hash.
//	entry - the cache entry to store.
func (c *ScoreCache) Put(hash uint64, entry CacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest entry if at capacity.
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
			c.evictions++
		}
	}

	c.entries[hash] = entry
}

// Stats returns cache statistics.
//
// Returns:
//
//	hits - number of cache hits since creation (or last ResetStats).
//	misses - number of cache misses.
//	size - current entry count.
//	evictions - number of entries evicted due to capacity.
func (c *ScoreCache) Stats() (hits, misses, size, evictions int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hits, c.misses, int64(len(c.entries)), c.evictions
}

// Clear removes all entries from the cache and resets statistics.
func (c *ScoreCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[uint64]CacheEntry)
	c.hits = 0
	c.misses = 0
	c.evictions = 0
}

// nowUnixNano returns the current Unix time in nanoseconds.
// Extracted as a package-level var for testability.
var nowUnixNano = func() int64 {
	return time.Now().UnixNano()
}

// MakeEntry constructs a CacheEntry with the current timestamp.
//
// Args:
//
//	hash - the strategy hash.
//	score - the fitness score.
//	scorerType - label identifying the scorer (e.g., "llm", "heuristic").
//	sampleCount - number of evaluation samples that contributed.
//	confidence - confidence level (0-1).
//
// Returns:
//
//	CacheEntry - the constructed cache entry.
func MakeEntry(hash uint64, score float64, scorerType string, sampleCount int, confidence float64) CacheEntry {
	return CacheEntry{
		Hash:         hash,
		Score:        score,
		ScorerType:   scorerType,
		Timestamp:    nowUnixNano(),
		SampleCount:  sampleCount,
		Confidence:   confidence,
	}
}
