package query

import (
	"context"
	"sync"
	"time"
)

// defaultMaxSize is the default upper bound on the number of cached entries.
// It keeps the cache bounded even when callers use a long TTL.
const defaultMaxSize = 10000

// MemoryQueryCacheOption configures a MemoryQueryCache at construction time.
type MemoryQueryCacheOption func(*MemoryQueryCache)

// WithMaxSize caps the number of cached entries. When a new entry would exceed
// the cap, the least-recently-used entry is evicted before the new entry is
// stored. A value <= 0 disables the cap (cache grows bound to TTL only).
// Defaults to defaultMaxSize.
func WithMaxSize(n int) MemoryQueryCacheOption {
	return func(m *MemoryQueryCache) {
		m.maxSize = n
	}
}

// MemoryQueryCache provides in-memory caching for query results.
//
// The cache is bounded by maxSize entries with least-recently-used eviction
// (see Set/evictLRU) and a TTL-based cleanup goroutine that drops expired
// entries every cleanupInterval. Both policies cooperate so the cache cannot
// grow unbounded under long TTLs.
type MemoryQueryCache struct {
	// mu guards items and every field of the *cacheItem values it holds
	// (results, expiresAt, lastAccess). All public methods take mu for both
	// reads and writes because Get updates lastAccess for LRU tracking.
	mu       sync.Mutex
	items    map[string]*cacheItem
	maxSize  int
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// cacheItem is a single cached entry. lastAccess is updated on every
// successful Get so evictLRU can pick the least-recently-used entry.
type cacheItem struct {
	results    []*SearchResult
	expiresAt  time.Time
	lastAccess time.Time
}

// NewMemoryQueryCache creates a new in-memory query cache.
// The cleanup goroutine runs periodically to remove expired items and is
// stopped by Close to avoid goroutine leaks.
func NewMemoryQueryCache(opts ...MemoryQueryCacheOption) *MemoryQueryCache {
	ctx, cancel := context.WithCancel(context.Background())
	m := &MemoryQueryCache{
		items:   make(map[string]*cacheItem),
		maxSize: defaultMaxSize,
		ctx:     ctx,
		cancel:  cancel,
	}
	for _, opt := range opts {
		opt(m)
	}

	// Start cleanup goroutine with proper lifecycle management.
	m.wg.Add(1)
	go m.cleanup()

	return m
}

// Close stops the cleanup goroutine and cleans up resources.
// This should be called when the cache is no longer needed to prevent goroutine leaks.
func (m *MemoryQueryCache) Close() {
	m.stopOnce.Do(func() {
		m.cancel()
		m.wg.Wait()

		// Clear all items.
		m.mu.Lock()
		m.items = make(map[string]*cacheItem)
		m.mu.Unlock()
	})
}

// Get retrieves search results from cache. A successful Get refreshes the
// entry's lastAccess timestamp so the LRU policy reflects recent usage.
func (m *MemoryQueryCache) Get(key string) ([]*SearchResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, found := m.items[key]
	if !found {
		return nil, false
	}

	// Check expiration: treat expired entries as missing. The cleanup
	// goroutine eventually reclaims them; we do not delete here to keep
	// Get's behavior uniform with the previous implementation.
	now := time.Now()
	if now.After(item.expiresAt) {
		return nil, false
	}

	item.lastAccess = now
	return item.results, true
}

// Set stores search results in cache. If the cache is at capacity, the
// least-recently-used entry is evicted first. Updating an existing entry
// refreshes its value, TTL, and lastAccess without triggering eviction.
func (m *MemoryQueryCache) Set(key string, results []*SearchResult, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// Refreshing an existing entry does not grow the cache, so no eviction.
	if _, exists := m.items[key]; exists {
		m.items[key] = &cacheItem{
			results:    results,
			expiresAt:  now.Add(ttl),
			lastAccess: now,
		}
		return
	}

	// New entry: enforce the size cap before inserting.
	if m.maxSize > 0 && len(m.items) >= m.maxSize {
		m.evictLRU()
	}

	m.items[key] = &cacheItem{
		results:    results,
		expiresAt:  now.Add(ttl),
		lastAccess: now,
	}
}

// evictLRU removes the least-recently-used entry to make room for one new
// entry. The caller must hold m.mu. If the cache is empty, this is a no-op.
//
// The scan is O(n) in cache size, but eviction only runs when a new entry
// would exceed maxSize, so it is not on the hot read path.
func (m *MemoryQueryCache) evictLRU() {
	if len(m.items) == 0 {
		return
	}

	var lruKey string
	var lruTime time.Time
	first := true
	for k, item := range m.items {
		if first || item.lastAccess.Before(lruTime) {
			lruKey = k
			lruTime = item.lastAccess
			first = false
		}
	}
	if !first {
		delete(m.items, lruKey)
	}
}

// Delete removes search results from cache.
func (m *MemoryQueryCache) Delete(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.items, key)
}

// Clear removes all items from cache.
func (m *MemoryQueryCache) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items = make(map[string]*cacheItem)
}

// cleanup removes expired items periodically.
// This goroutine runs until the context is cancelled or Close is called.
func (m *MemoryQueryCache) cleanup() {
	defer m.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			// Context cancelled, stop cleanup.
			return

		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			for k, item := range m.items {
				if now.After(item.expiresAt) {
					delete(m.items, k)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Len returns the number of items in cache.
func (m *MemoryQueryCache) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.items)
}
