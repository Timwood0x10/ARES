package query

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync/atomic"
	"time"

	gerr "github.com/Timwood0x10/ares/internal/errors"

	"golang.org/x/crypto/blake2b"
)

var (
	// ErrQueryNotFound indicates query result not found in cache.
	ErrQueryNotFound = errors.New("query result not found in cache")
)

// QueryCache provides caching for query results to bypass DB and embedding calls.
// It supports both Redis and in-memory cache with automatic fallback.
type QueryCache struct {
	redis   RedisClient
	memory  *MemoryQueryCache
	ttl     time.Duration
	enabled atomic.Bool
	stats   *CacheStats
}

// SearchResult represents a search result from retrieval.
type SearchResult struct {
	ID       string
	Content  string
	Source   string
	Score    float64
	Metadata map[string]interface{}
}

// SearchRequest represents a search request.
type SearchRequest struct {
	Query    string
	TenantID string
	Filters  map[string]interface{}
	TopK     int
}

// NewQueryCache creates a new query cache.
// redisClient is optional. If nil, it will use in-memory cache only.
func NewQueryCache(redisClient RedisClient, ttl time.Duration) *QueryCache {
	c := &QueryCache{
		redis:  redisClient,
		memory: NewMemoryQueryCache(),
		ttl:    ttl,
		stats:  &CacheStats{},
	}
	c.enabled.Store(true)
	return c
}

// Get retrieves cached search results.
func (c *QueryCache) Get(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	if !c.enabled.Load() {
		return nil, ErrQueryNotFound
	}

	cacheKey := c.getCacheKey(req)

	// Try Redis first
	if c.redis != nil {
		data, err := c.redis.Get(ctx, cacheKey)
		if err == nil {
			results, err := c.deserializeResults([]byte(data))
			if err == nil {
				c.stats.recordHit()
				return results, nil
			}
		}
	}

	// Fallback to memory cache
	if c.memory != nil {
		results, found := c.memory.Get(cacheKey)
		if found {
			c.stats.recordHit()
			return results, nil
		}
	}

	c.stats.recordMiss()
	return nil, ErrQueryNotFound
}

// Set stores search results in cache.
func (c *QueryCache) Set(ctx context.Context, req *SearchRequest, results []*SearchResult) error {
	if !c.enabled.Load() {
		return nil
	}

	cacheKey := c.getCacheKey(req)

	// Serialize results
	data, err := c.serializeResults(results)
	if err != nil {
		return gerr.Wrap(err, "serialize results")
	}

	// Try to store in Redis
	if c.redis != nil {
		if err := c.redis.Set(ctx, cacheKey, string(data), c.ttl); err != nil {
			// Redis error is not fatal, continue with memory cache
			slog.Debug("Failed to store in Redis cache", "error", err)
		}
	}

	// Always store in memory cache
	if c.memory != nil {
		c.memory.Set(cacheKey, results, c.ttl)
	}

	return nil
}

// Delete removes a query result from cache.
func (c *QueryCache) Delete(ctx context.Context, req *SearchRequest) error {
	if !c.enabled.Load() {
		return nil
	}

	cacheKey := c.getCacheKey(req)

	// Try to delete from Redis
	if c.redis != nil {
		_ = c.redis.Del(ctx, cacheKey)
	}

	// Delete from memory cache
	if c.memory != nil {
		c.memory.Delete(cacheKey)
	}

	return nil
}

// Clear removes all query results from cache.
func (c *QueryCache) Clear(ctx context.Context) error {
	if !c.enabled.Load() {
		return nil
	}

	// Try to clear Redis
	if c.redis != nil {
		keys, err := c.redis.Keys(ctx, "query_cache:*")
		if err == nil && len(keys) > 0 {
			_ = c.redis.Del(ctx, keys...)
		}
	}

	// Clear memory cache
	if c.memory != nil {
		c.memory.Clear()
	}

	// Reset stats
	c.stats.Reset()

	return nil
}

// GetStats returns cache statistics.
func (c *QueryCache) GetStats() *CacheStats {
	return c.stats
}

// Enable enables the cache.
func (c *QueryCache) Enable() {
	c.enabled.Store(true)
}

// Disable disables the cache.
func (c *QueryCache) Disable() {
	c.enabled.Store(false)
}

// IsEnabled returns whether the cache is enabled.
func (c *QueryCache) IsEnabled() bool {
	return c.enabled.Load()
}

// getCacheKey generates a cache key for the search request using BLAKE2b hash.
// BLAKE2b provides security and performance benefits over SHA256.
func (c *QueryCache) getCacheKey(req *SearchRequest) string {
	// Normalize query for consistent key generation
	normalizedQuery := normalizeText(req.Query)

	// Sort filter keys for consistent hash
	sortedFilters := c.sortFilters(req.Filters)

	// Combine all factors for unique key
	keyData := fmt.Sprintf("query:%s:%s:%v:%d", req.TenantID, normalizedQuery, sortedFilters, req.TopK)

	// Use BLAKE2b-256 and truncate to 128 bits for security and performance
	hash := blake2b.Sum256([]byte(keyData))

	// Convert first 16 bytes (128 bits) to hex string
	return fmt.Sprintf("query_cache:%s", hex.EncodeToString(hash[:16]))
}

// sortFilters sorts filter keys for consistent hash generation.
func (c *QueryCache) sortFilters(filters map[string]interface{}) map[string]interface{} {
	if filters == nil {
		return nil
	}

	// Get sorted keys
	keys := make([]string, 0, len(filters))
	for k := range filters {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Create sorted map
	sorted := make(map[string]interface{})
	for _, k := range keys {
		sorted[k] = filters[k]
	}

	return sorted
}

// serializeResults serializes search results to bytes.
func (c *QueryCache) serializeResults(results []*SearchResult) ([]byte, error) {
	// Use gob encoding for Go types
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(results); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// deserializeResults deserializes search results from bytes.
func (c *QueryCache) deserializeResults(data []byte) ([]*SearchResult, error) {
	var results []*SearchResult

	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&results); err != nil {
		return nil, err
	}

	return results, nil
}

// normalizeText normalizes query text for consistent key generation.
func normalizeText(text string) string {
	// Simple normalization: lowercase and trim
	text = toLower(text)
	text = trimSpace(text)
	return text
}

// CacheStats represents cache statistics.
type CacheStats struct {
	Hits   atomic.Int64
	Misses atomic.Int64
}

func (s *CacheStats) recordHit() {
	s.Hits.Add(1)
}

func (s *CacheStats) recordMiss() {
	s.Misses.Add(1)
}

// Reset resets all statistics to zero.
func (s *CacheStats) Reset() {
	s.Hits.Store(0)
	s.Misses.Store(0)
}

// HitRate returns the cache hit rate.
func (s *CacheStats) HitRate() float64 {
	total := s.Hits.Load() + s.Misses.Load()
	if total == 0 {
		return 0.0
	}
	return float64(s.Hits.Load()) / float64(total)
}

// RedisClient defines the interface for Redis operations.
type RedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error
	Del(ctx context.Context, keys ...string) error
	Keys(ctx context.Context, pattern string) ([]string, error)
}

// Simple string functions to avoid external dependencies
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		result[i] = c
	}
	return string(result)
}

func trimSpace(s string) string {
	start := 0
	end := len(s)

	// Trim leading spaces
	for start < end && s[start] == ' ' {
		start++
	}

	// Trim trailing spaces
	for end > start && s[end-1] == ' ' {
		end--
	}

	return s[start:end]
}

// Register types for gob encoding
// This init function registers types with the gob encoder for serialization.
// It is required for proper encoding/decoding of SearchResult and map[string]interface{} types.
func init() {
	gob.Register(&SearchResult{})
	gob.Register(map[string]interface{}{})
}
