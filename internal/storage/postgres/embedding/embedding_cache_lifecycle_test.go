package embedding

import (
	"context"
	"errors"
	"testing"
	"time"
)

func stubCacheKey(text string) *CacheKey {
	return &CacheKey{Text: text, Model: "stub-model", Method: "query:"}
}

// TestEmbeddingCacheDelete_RemovesRedisAndMemory verifies Delete purges both
// layers so a subsequent Get misses entirely.
func TestEmbeddingCacheDelete_RemovesRedisAndMemory(t *testing.T) {
	redis := NewMockRedisClient()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	key := stubCacheKey("delete me")
	if err := cache.Set(context.Background(), key, []float64{0.1, 0.2}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found := cache.Get(context.Background(), key); found {
		t.Fatal("Get must miss after Delete")
	}
}

// TestEmbeddingCacheDelete_RedisErrorStillPurgesMemory pins the non-fatal
// redis contract: a Del failure is logged, memory is still purged, and
// Delete returns nil.
func TestEmbeddingCacheDelete_RedisErrorStillPurgesMemory(t *testing.T) {
	redis := NewMockRedisClient()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	key := stubCacheKey("redis down")
	if err := cache.Set(context.Background(), key, []float64{0.3}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	redis.SetFailMode(true)

	if err := cache.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete must tolerate redis failure, got %v", err)
	}
	if _, found := cache.Get(context.Background(), key); found {
		t.Fatal("memory layer must be purged even when redis Del fails")
	}
}

// TestEmbeddingCacheDelete_DisabledIsNoop pins the enabled-gate on Delete.
func TestEmbeddingCacheDelete_DisabledIsNoop(t *testing.T) {
	cache := NewEmbeddingCache(NewMockRedisClient(), time.Minute)
	defer cache.Close()

	key := stubCacheKey("disabled delete")
	if err := cache.Set(context.Background(), key, []float64{0.4}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cache.Disable()
	if err := cache.Delete(context.Background(), key); err != nil {
		t.Fatalf("Delete on disabled cache: %v", err)
	}
	cache.Enable()
	if _, found := cache.Get(context.Background(), key); !found {
		t.Fatal("disabled Delete must not purge existing entries")
	}
}

// TestEmbeddingCacheClear_ScanDeletesAllRedisKeys verifies Clear removes only
// embed:* prefixed keys from redis and clears the memory layer.
func TestEmbeddingCacheClear_ScanDeletesAllRedisKeys(t *testing.T) {
	redis := newStubPagedRedis()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	matchKey := stubCacheKey("clear match")
	if err := cache.Set(context.Background(), matchKey, []float64{0.5}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Foreign key shares the mock map but not the embed: prefix.
	if err := redis.Set(context.Background(), "foreign:key", []float64{0.6}, time.Minute); err != nil {
		t.Fatalf("seed foreign key: %v", err)
	}
	// Script Scan to see the real cached key plus the foreign key.
	redis.pages[0] = stubRedisPage{keys: []string{matchKey.String(), "foreign:key"}, next: 0}

	if err := cache.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, found := cache.Get(context.Background(), matchKey); found {
		t.Fatal("memory layer must be cleared")
	}
	deleted := redis.deletedKeys()
	for _, k := range deleted {
		if k == "foreign:key" {
			t.Fatal("Clear must not delete keys outside embed:*")
		}
	}
	foundMatch := false
	for _, k := range deleted {
		if k == matchKey.String() {
			foundMatch = true
		}
	}
	if !foundMatch {
		t.Fatalf("Clear must delete the embed:* key, deleted=%v", deleted)
	}
	if _, ok := redis.data["foreign:key"]; !ok {
		t.Fatal("foreign key must survive Clear")
	}
}

// TestEmbeddingCacheClear_MultiCursorScan covers cursor chaining until the
// SCAN response reports next==0.
func TestEmbeddingCacheClear_MultiCursorScan(t *testing.T) {
	redis := newStubPagedRedis()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	redis.pages[0] = stubRedisPage{keys: []string{"embed:a", "embed:b"}, next: 1}
	redis.pages[1] = stubRedisPage{keys: []string{"embed:c"}, next: 0}
	for _, k := range []string{"embed:a", "embed:b", "embed:c"} {
		if err := redis.Set(context.Background(), k, []float64{0.1}, time.Minute); err != nil {
			t.Fatalf("seed %s: %v", k, err)
		}
	}

	if err := cache.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	deleted := redis.deletedKeys()
	if len(deleted) != 3 {
		t.Fatalf("deleted %d keys %v, want 3 across both scan pages", len(deleted), deleted)
	}
}

// TestEmbeddingCacheClear_ScanErrorBreaksLoop pins degrade-on-scan-error:
// Clear still returns nil and the memory layer is purged.
func TestEmbeddingCacheClear_ScanErrorBreaksLoop(t *testing.T) {
	redis := newStubPagedRedis()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	key := stubCacheKey("scan error")
	if err := cache.Set(context.Background(), key, []float64{0.7}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	redis.scanErr[0] = errors.New("stub scan failure")

	if err := cache.Clear(context.Background()); err != nil {
		t.Fatalf("Clear must tolerate scan failure, got %v", err)
	}
	if _, found := cache.Get(context.Background(), key); found {
		t.Fatal("memory layer must be cleared even when redis scan fails")
	}
}

// TestEmbeddingCacheClear_MemoryOnly covers the nil-redis path.
func TestEmbeddingCacheClear_MemoryOnly(t *testing.T) {
	cache := NewEmbeddingCache(nil, time.Minute)
	defer cache.Close()

	key := stubCacheKey("memory only")
	if err := cache.Set(context.Background(), key, []float64{0.8}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := cache.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, found := cache.Get(context.Background(), key); found {
		t.Fatal("Get must miss after memory-only Clear")
	}
}

// TestEmbeddingCacheClose_IdempotentAndStopsCleanup covers Close semantics:
// double Close is safe and entries are gone afterwards.
func TestEmbeddingCacheClose_IdempotentAndStopsCleanup(t *testing.T) {
	cache := NewEmbeddingCache(NewMockRedisClient(), time.Minute)

	key := stubCacheKey("close me")
	if err := cache.Set(context.Background(), key, []float64{0.9}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cache.Close()
	cache.Close()

	if _, found := cache.Get(context.Background(), key); found {
		t.Fatal("Get must miss after Close cleared the memory layer")
	}
}

// TestEmbeddingCacheGetStats_RedisAndMemoryCounts verifies stats aggregation
// across both layers plus TTL/enabled reporting.
func TestEmbeddingCacheGetStats_RedisAndMemoryCounts(t *testing.T) {
	redis := newStubPagedRedis()
	ttl := 5 * time.Minute
	cache := NewEmbeddingCache(redis, ttl)
	defer cache.Close()

	keyA := stubCacheKey("stats a")
	keyB := stubCacheKey("stats b")
	if err := cache.Set(context.Background(), keyA, []float64{0.1}); err != nil {
		t.Fatalf("Set a: %v", err)
	}
	if err := cache.Set(context.Background(), keyB, []float64{0.2}); err != nil {
		t.Fatalf("Set b: %v", err)
	}
	// Drop one redis entry so redis count (2 via scan script) diverges from
	// memory count (both still present until deleted).
	redis.pages[0] = stubRedisPage{keys: []string{keyA.String(), keyB.String()}, next: 0}

	stats, err := cache.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if !stats.Enabled {
		t.Error("stats.Enabled = false, want true")
	}
	if stats.RedisKeys != 2 {
		t.Errorf("RedisKeys = %d, want 2", stats.RedisKeys)
	}
	if stats.MemoryKeys != 2 {
		t.Errorf("MemoryKeys = %d, want 2", stats.MemoryKeys)
	}
	if stats.TotalKeys != 4 {
		t.Errorf("TotalKeys = %d, want 4", stats.TotalKeys)
	}
	if stats.TTL != ttl {
		t.Errorf("TTL = %v, want %v", stats.TTL, ttl)
	}
}

// TestEmbeddingCacheGetStats_MultiCursorAndScanError covers cursor chaining
// and the partial-count degrade when a later SCAN call fails.
func TestEmbeddingCacheGetStats_MultiCursorAndScanError(t *testing.T) {
	redis := newStubPagedRedis()
	cache := NewEmbeddingCache(redis, time.Minute)
	defer cache.Close()

	redis.pages[0] = stubRedisPage{keys: []string{"embed:a", "embed:b"}, next: 1}
	redis.pages[1] = stubRedisPage{keys: []string{"embed:c"}, next: 0}

	stats, err := cache.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.RedisKeys != 3 {
		t.Fatalf("RedisKeys = %d, want 3 across two scan pages", stats.RedisKeys)
	}

	// Scan error on the second page keeps the first page's count.
	redis2 := newStubPagedRedis()
	redis2.pages[0] = stubRedisPage{keys: []string{"embed:x"}, next: 1}
	redis2.scanErr[1] = errors.New("stub scan failure")
	cache2 := NewEmbeddingCache(redis2, time.Minute)
	defer cache2.Close()

	stats2, err := cache2.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats after scan error: %v", err)
	}
	if stats2.RedisKeys != 1 {
		t.Fatalf("RedisKeys = %d, want partial count 1 after scan error", stats2.RedisKeys)
	}
}

// TestEmbeddingCacheGetStats_Disabled pins the disabled-stats contract.
func TestEmbeddingCacheGetStats_Disabled(t *testing.T) {
	cache := NewEmbeddingCache(NewMockRedisClient(), time.Minute)
	defer cache.Close()
	cache.Disable()

	stats, err := cache.GetStats(context.Background())
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if stats.Enabled {
		t.Error("stats.Enabled = true, want false when cache is disabled")
	}
	if stats.RedisKeys != 0 || stats.MemoryKeys != 0 || stats.TotalKeys != 0 {
		t.Errorf("disabled stats must be zeroed, got %+v", stats)
	}
}
