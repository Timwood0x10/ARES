// nolint: errcheck // Test code may ignore return values
package query

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestQueryCache(t *testing.T) {
	ctx := context.Background()
	cache := NewQueryCache(nil, time.Hour)

	// Test initial state
	if !cache.IsEnabled() {
		t.Error("Cache should be enabled")
	}

	// Test Get/Set
	req := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-1",
		Filters: map[string]interface{}{
			"type": "knowledge",
		},
		TopK: 10,
	}

	results := []*SearchResult{
		{
			ID:      "1",
			Content: "Result 1",
			Source:  "knowledge",
			Score:   0.9,
			Metadata: map[string]interface{}{
				"key": "value",
			},
		},
		{
			ID:      "2",
			Content: "Result 2",
			Source:  "knowledge",
			Score:   0.8,
		},
	}

	// Store results
	err := cache.Set(ctx, req, results)
	if err != nil {
		t.Errorf("Set() error = %v", err)
	}

	// Retrieve results
	cached, err := cache.Get(ctx, req)
	if err != nil {
		t.Errorf("Get() error = %v", err)
	}

	if cached == nil {
		t.Error("Get() should return results")
	}

	if len(cached) != len(results) {
		t.Errorf("Get() returned wrong length, got %d, want %d", len(cached), len(results))
	}

	// Test stats
	stats := cache.GetStats()
	if stats.Hits.Load() != 1 {
		t.Errorf("Expected 1 hit, got %d", stats.Hits.Load())
	}

	hitRate := stats.HitRate()
	if hitRate != 1.0 {
		t.Errorf("Expected hit rate 1.0, got %f", hitRate)
	}
}

func TestQueryCacheMiss(t *testing.T) {
	ctx := context.Background()
	cache := NewQueryCache(nil, time.Hour)

	req := &SearchRequest{
		Query:    "non-existent query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	// Try to get non-existent result
	_, err := cache.Get(ctx, req)
	if err != ErrQueryNotFound {
		t.Errorf("Expected ErrQueryNotFound, got %v", err)
	}

	// Test stats
	stats := cache.GetStats()
	if stats.Misses.Load() != 1 {
		t.Errorf("Expected 1 miss, got %d", stats.Misses.Load())
	}

	hitRate := stats.HitRate()
	if hitRate != 0.0 {
		t.Errorf("Expected hit rate 0.0, got %f", hitRate)
	}
}

func TestQueryCacheDelete(t *testing.T) {
	ctx := context.Background()
	cache := NewQueryCache(nil, time.Hour)

	req := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	results := []*SearchResult{
		{
			ID:      "1",
			Content: "Result 1",
			Score:   0.9,
		},
	}

	// Store results
	err := cache.Set(ctx, req, results)
	if err != nil {
		t.Errorf("Set() error = %v", err)
	}

	// Delete results
	err = cache.Delete(ctx, req)
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}

	// Verify deletion
	_, err = cache.Get(ctx, req)
	if err != ErrQueryNotFound {
		t.Errorf("Expected ErrQueryNotFound after deletion, got %v", err)
	}
}

func TestQueryCacheClear(t *testing.T) {
	ctx := context.Background()
	cache := NewQueryCache(nil, time.Hour)

	// Add multiple items
	for i := 0; i < 5; i++ {
		req := &SearchRequest{
			Query:    fmt.Sprintf("query %d", i),
			TenantID: "tenant-1",
			TopK:     10,
		}

		results := []*SearchResult{
			{
				ID:      fmt.Sprintf("result %d", i),
				Content: fmt.Sprintf("Content %d", i),
				Score:   float64(i) / 10,
			},
		}

		err := cache.Set(ctx, req, results)
		if err != nil {
			t.Errorf("Set() error = %v", err)
		}
	}

	// Clear cache
	err := cache.Clear(ctx)
	if err != nil {
		t.Errorf("Clear() error = %v", err)
	}

	// Verify all items are cleared
	for i := 0; i < 5; i++ {
		req := &SearchRequest{
			Query:    fmt.Sprintf("query %d", i),
			TenantID: "tenant-1",
			TopK:     10,
		}

		_, err := cache.Get(ctx, req)
		if err != ErrQueryNotFound {
			t.Errorf("Expected ErrQueryNotFound after clear, got %v", err)
		}
	}
}

func TestQueryCacheDisable(t *testing.T) {
	ctx := context.Background()
	cache := NewQueryCache(nil, time.Hour)

	req := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	results := []*SearchResult{
		{
			ID:      "1",
			Content: "Result 1",
			Score:   0.9,
		},
	}

	// Disable cache
	cache.Disable()
	if cache.IsEnabled() {
		t.Error("Cache should be disabled")
	}

	// Try to store (should succeed but not cache)
	err := cache.Set(ctx, req, results)
	if err != nil {
		t.Errorf("Set() should succeed even when disabled")
	}

	// Try to get (should return not found)
	_, err = cache.Get(ctx, req)
	if err != ErrQueryNotFound {
		t.Errorf("Expected ErrQueryNotFound when disabled, got %v", err)
	}

	// Re-enable cache
	cache.Enable()
	if !cache.IsEnabled() {
		t.Error("Cache should be enabled")
	}
}

func TestQueryCacheKeyGeneration(t *testing.T) {
	cache := NewQueryCache(nil, time.Hour)

	req1 := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	req2 := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	key1 := cache.getCacheKey(req1)
	key2 := cache.getCacheKey(req2)

	if key1 != key2 {
		t.Error("Same requests should generate same cache key")
	}

	// Different tenant
	req3 := &SearchRequest{
		Query:    "test query",
		TenantID: "tenant-2",
		TopK:     10,
	}

	key3 := cache.getCacheKey(req3)
	if key3 == key1 {
		t.Error("Different tenants should generate different cache keys")
	}

	// Different query
	req4 := &SearchRequest{
		Query:    "different query",
		TenantID: "tenant-1",
		TopK:     10,
	}

	key4 := cache.getCacheKey(req4)
	if key4 == key1 {
		t.Error("Different queries should generate different cache keys")
	}
}

func TestMemoryQueryCache(t *testing.T) {
	cache := NewMemoryQueryCache()

	key := "test-key"
	results := []*SearchResult{
		{
			ID:      "1",
			Content: "Result 1",
			Score:   0.9,
		},
	}

	// Test Set/Get
	cache.Set(key, results, time.Hour)

	cached, found := cache.Get(key)
	if !found {
		t.Error("Memory cache should find stored item")
	}

	if len(cached) != len(results) {
		t.Errorf("Memory cache returned wrong length, got %d, want %d", len(cached), len(results))
	}

	// Test Delete
	cache.Delete(key)

	_, found = cache.Get(key)
	if found {
		t.Error("Memory cache should not find deleted item")
	}

	// Test Clear
	cache.Set("key1", results, time.Hour)
	cache.Set("key2", results, time.Hour)
	cache.Clear()

	if cache.Len() != 0 {
		t.Errorf("Memory cache should be empty after clear, got %d items", cache.Len())
	}
}

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "lowercase",
			input:    "Hello World",
			expected: "hello world",
		},
		{
			name:     "trim spaces",
			input:    "  hello world  ",
			expected: "hello world",
		},
		{
			name:     "mixed",
			input:    "  HELLO WORLD  ",
			expected: "hello world",
		},
		{
			name:     "accented_latin_uppercase",
			input:    "Époque",
			expected: "époque",
		},
		{
			name:     "accented_latin_already_lowercase",
			input:    "époque",
			expected: "époque",
		},
		{
			name:     "german_eszett_uppercase",
			input:    "STRAẞE",
			expected: "straße",
		},
		{
			name:     "cyrillic_uppercase",
			input:    "ПРИВЕТ",
			expected: "привет",
		},
		{
			name:     "cyrillic_already_lowercase",
			input:    "привет",
			expected: "привет",
		},
		{
			name:     "greek_uppercase",
			input:    "ΕΛΛΆΔΑ",
			expected: "ελλάδα",
		},
		{
			name:     "trim_unicode_whitespace",
			input:    "\u2003hello\u2003",
			expected: "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeText(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeText() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestNormalizeTextCaseFolding verifies that uppercase and lowercase forms of
// the same non-ASCII word normalize identically, so they hit the same cache
// key instead of producing spurious cache misses.
func TestNormalizeTextCaseFolding(t *testing.T) {
	pairs := []struct {
		upper string
		lower string
	}{
		{"Époque", "époque"},
		{"ПРИВЕТ", "привет"},
		{"ÖSTERREICH", "österreich"},
		{"ΕΛΛΆΔΑ", "ελλάδα"},
	}
	for _, p := range pairs {
		if normalizeText(p.upper) != normalizeText(p.lower) {
			t.Errorf(
				"normalizeText(%q)=%q != normalizeText(%q)=%q",
				p.upper, normalizeText(p.upper), p.lower, normalizeText(p.lower),
			)
		}
	}
}

// TestFiltersKeyDeterministic verifies filtersKey produces the same output for
// the same map regardless of Go's randomized map iteration order. Before the
// fix, sortFilters built a new map and %v rendered it in random order,
// producing non-deterministic cache keys.
func TestFiltersKeyDeterministic(t *testing.T) {
	cache := NewQueryCache(nil, time.Hour)

	filters := map[string]interface{}{
		"alpha":   1,
		"bravo":   "two",
		"charlie": 3.14,
		"delta":   true,
		"echo":    []int{1, 2, 3},
	}

	const iterations = 200
	first := cache.filtersKey(filters)
	for i := 0; i < iterations; i++ {
		if got := cache.filtersKey(filters); got != first {
			t.Fatalf(
				"filtersKey not deterministic at iteration %d:\n  first=%q\n  got  =%q",
				i, first, got,
			)
		}
	}
}

// TestFiltersKeySorted verifies the rendered output has keys in lexicographic
// order and matches the expected deterministic representation.
func TestFiltersKeySorted(t *testing.T) {
	cache := NewQueryCache(nil, time.Hour)

	filters := map[string]interface{}{
		"zebra":  1,
		"apple":  2,
		"mango":  3,
		"banana": 4,
	}

	// Keys must appear in sorted order: apple, banana, mango, zebra.
	want := "apple=2,banana=4,mango=3,zebra=1"
	if got := cache.filtersKey(filters); got != want {
		t.Errorf("filtersKey() = %q, want %q", got, want)
	}
}

// TestFiltersKeyEmpty verifies nil and empty maps produce a stable empty
// representation (no panic, no stray separators).
func TestFiltersKeyEmpty(t *testing.T) {
	cache := NewQueryCache(nil, time.Hour)

	if got := cache.filtersKey(nil); got != "" {
		t.Errorf("filtersKey(nil) = %q, want empty", got)
	}
	if got := cache.filtersKey(map[string]interface{}{}); got != "" {
		t.Errorf("filtersKey(empty) = %q, want empty", got)
	}
}

// TestGetCacheKeyStableWithFilters verifies the end-to-end cache key is stable
// for identical filter maps across many calls. This is the user-visible
// behavior that the sortFilters bug broke.
func TestGetCacheKeyStableWithFilters(t *testing.T) {
	cache := NewQueryCache(nil, time.Hour)

	req := &SearchRequest{
		Query:    "Époque",
		TenantID: "tenant-1",
		Filters: map[string]interface{}{
			"type":     "knowledge",
			"source":   "doc",
			"language": "fr",
			"version":  2,
		},
		TopK: 10,
	}

	first := cache.getCacheKey(req)
	for i := 0; i < 200; i++ {
		if got := cache.getCacheKey(req); got != first {
			t.Fatalf("getCacheKey not stable at iteration %d: first=%q got=%q", i, first, got)
		}
	}
}

// TestMemoryQueryCacheMaxSizeEvictsWhenFull verifies that adding entries beyond
// maxSize triggers eviction so the cache stays bounded.
func TestMemoryQueryCacheMaxSizeEvictsWhenFull(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(3))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	for i := 0; i < 10; i++ {
		cache.Set(fmt.Sprintf("k%d", i), results, time.Hour)
	}

	if got := cache.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3 (maxSize bound)", got)
	}
}

// TestMemoryQueryCacheLRUEviction verifies the eviction policy is
// least-recently-used: accessing an entry keeps it alive while the oldest
// access is evicted.
func TestMemoryQueryCacheLRUEviction(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(3))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	// Fill cache: A, B, C (insertion order = access order).
	cache.Set("A", results, time.Hour)
	cache.Set("B", results, time.Hour)
	cache.Set("C", results, time.Hour)

	// Access A to make it most-recently-used. B is now the LRU candidate.
	if _, ok := cache.Get("A"); !ok {
		t.Fatal("Get(A) missing before eviction")
	}

	// Insert D: should evict B (LRU), keeping A, C, D.
	cache.Set("D", results, time.Hour)

	for _, key := range []string{"A", "C", "D"} {
		if _, ok := cache.Get(key); !ok {
			t.Errorf("Get(%q) missing after LRU eviction, want present", key)
		}
	}
	if _, ok := cache.Get("B"); ok {
		t.Errorf("Get(B) present after LRU eviction, want evicted")
	}
	if got := cache.Len(); got != 3 {
		t.Errorf("Len() = %d, want 3", got)
	}
}

// TestMemoryQueryCacheUpdateExistingDoesNotEvict verifies that refreshing an
// existing key does not grow the cache or trigger eviction of other entries.
func TestMemoryQueryCacheUpdateExistingDoesNotEvict(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(2))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	cache.Set("A", results, time.Hour)
	cache.Set("B", results, time.Hour)

	// Refresh A repeatedly; B must survive.
	for i := 0; i < 5; i++ {
		cache.Set("A", results, time.Hour)
	}

	for _, key := range []string{"A", "B"} {
		if _, ok := cache.Get(key); !ok {
			t.Errorf("Get(%q) missing after repeated update", key)
		}
	}
	if got := cache.Len(); got != 2 {
		t.Errorf("Len() = %d, want 2", got)
	}
}

// TestMemoryQueryCacheMaxSizeDisabled verifies that a non-positive maxSize
// disables the cap and the cache grows with TTL only.
func TestMemoryQueryCacheMaxSizeDisabled(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(0))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	const n = 50
	for i := 0; i < n; i++ {
		cache.Set(fmt.Sprintf("k%d", i), results, time.Hour)
	}
	if got := cache.Len(); got != n {
		t.Errorf("Len() = %d, want %d (maxSize disabled)", got, n)
	}
}

// TestMemoryQueryCacheDefaultMaxSize verifies the default cap is applied when
// no option is supplied.
func TestMemoryQueryCacheDefaultMaxSize(t *testing.T) {
	cache := NewMemoryQueryCache()
	defer cache.Close()

	if cache.maxSize != defaultMaxSize {
		t.Errorf("default maxSize = %d, want %d", cache.maxSize, defaultMaxSize)
	}
}

// TestMemoryQueryCacheTTLExpiry verifies an expired entry is reported as
// missing even though the cleanup goroutine has not yet reaped it.
func TestMemoryQueryCacheTTLExpiry(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(10))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	// TTL of 1ms; spin briefly until the entry has expired. No time.Sleep
	// is used for synchronization with the cache itself — we only wait for
	// wall-clock time to pass the expiry deadline.
	cache.Set("k", results, time.Millisecond)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, ok := cache.Get("k"); !ok {
			return // expired as expected
		}
		// Busy-wait a tiny bit to let the clock advance. This is not
		// synchronizing with a goroutine, only waiting for TTL.
	}
	t.Fatal("entry did not expire within 1s")
}

// TestMemoryQueryCacheConcurrent exercises Set/Get/Delete/Clear under heavy
// concurrent access to verify -race cleanliness and that the cache stays
// bounded by maxSize.
func TestMemoryQueryCacheConcurrent(t *testing.T) {
	const maxSize = 50
	cache := NewMemoryQueryCache(WithMaxSize(maxSize))
	defer cache.Close()

	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}

	const goroutines = 8
	const opsPerGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := fmt.Sprintf("k%d", (seed+i)%maxSize)
				cache.Set(key, results, time.Hour)
				_, _ = cache.Get(key)
				if i%7 == 0 {
					cache.Delete(key)
				}
			}
		}(g)
	}
	wg.Wait()

	if got := cache.Len(); got > maxSize {
		t.Errorf("Len() = %d, exceeded maxSize %d under concurrent access", got, maxSize)
	}
}

// TestMemoryQueryCacheCloseStopsGoroutine verifies Close is idempotent and
// releases the cleanup goroutine (no goroutine leak / deadlock).
func TestMemoryQueryCacheCloseStopsGoroutine(t *testing.T) {
	cache := NewMemoryQueryCache(WithMaxSize(5))
	cache.Close()
	// Second Close must be a no-op (stopOnce guard).
	cache.Close()

	// A fresh cache should still work after another was closed.
	cache2 := NewMemoryQueryCache(WithMaxSize(5))
	defer cache2.Close()
	results := []*SearchResult{{ID: "1", Content: "x", Score: 1.0}}
	cache2.Set("k", results, time.Hour)
	if _, ok := cache2.Get("k"); !ok {
		t.Fatal("fresh cache Get missing after another cache was Closed")
	}
}

// nolint: errcheck // Test code may ignore return values
