// Package services provides retrieval services for the storage system.
package services

import (
	"context"

	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	"github.com/Timwood0x10/ares/internal/truncate"
)

// getEmbedding retrieves embedding for a query with caching.
func (s *RetrievalService) getEmbedding(ctx context.Context, query string) []float64 {
	if query == "" {
		return nil
	}

	// Use the unified embedding pipeline when available.
	s.pipelineMu.RLock()
	p := s.pipeline
	s.pipelineMu.RUnlock()
	if p != nil {
		spec, err := p.BuildSpec(memembed.KindMemoryQuery, query)
		if err != nil {
			s.logger.Warn("Failed to build query spec", "error", err)
			return nil
		}
		vec, err := p.Embed(ctx, spec)
		if err != nil {
			s.logger.Warn("Failed to get embedding via pipeline", "query", query, "error", err)
			return nil
		}
		return vec
	}

	// Fallback to direct embedding client.
	if s.embeddingClient == nil {
		s.logger.Warn("Embedding client is nil, cannot get embedding")
		return nil
	}

	vec, err := s.embeddingClient.Embed(ctx, query)
	if err != nil {
		s.logger.Warn("Failed to get embedding", "query", query, "error", err)
		return nil
	}

	// Note: embedding service already returns normalized vectors, so no need to normalize again
	return vec
}

// getEmbeddingCached retrieves embedding with caching to reduce LLM calls.
// This can reduce 50-75% of embedding computations for repeated queries.
//
// Thread-safety: Uses read-write mutex to protect cache access.
// Implements LRU eviction to prevent unbounded memory growth: a cache hit
// refreshes the key's recency (moves it to the back of the access list), so
// eviction removes the least-recently-used entry. The previous code never
// touched the access list on hits, making it a FIFO queue that evicted
// hot entries just as eagerly as cold ones.
//
// Args:
// ctx - operation context.
// query - query text.
// Returns embedding vector or nil if failed.
func (s *RetrievalService) getEmbeddingCached(ctx context.Context, query string) []float64 {
	if query == "" {
		return nil
	}

	// 1. Check cache (read lock). On a hit we must still refresh recency,
	// which mutates the access list — handled under the write lock below.
	s.embeddingCacheMu.RLock()
	_, cached := s.embeddingCache[query]
	s.embeddingCacheMu.RUnlock()
	if cached {
		var embedding []float64
		s.embeddingCacheMu.Lock()
		// Re-check: the entry may have been evicted between releasing RLock
		// and acquiring the write lock.
		e, still := s.embeddingCache[query]
		if still {
			embedding = e
			s.touchAccessListLocked(query)
		}
		s.embeddingCacheMu.Unlock()
		if still {
			s.logger.Debug("Embedding cache hit", "query", truncate.WithEllipsis(query, 30))
			return embedding
		}
		// Evicted concurrently: fall through and recompute.
	}

	// 2. Compute embedding
	embedding := s.getEmbedding(ctx, query)
	if len(embedding) == 0 {
		return nil
	}

	// 3. Store in cache with LRU eviction (write lock)
	s.embeddingCacheMu.Lock()
	defer s.embeddingCacheMu.Unlock()

	// Another goroutine may have inserted the same key while we computed;
	// keep its entry and only refresh recency.
	if _, exists := s.embeddingCache[query]; exists {
		s.touchAccessListLocked(query)
		return embedding
	}

	// Check if eviction is needed
	if len(s.embeddingCache) >= s.embeddingCacheSizeLimit {
		if len(s.embeddingCacheAccessList) > 0 {
			oldestKey := s.embeddingCacheAccessList[0]
			delete(s.embeddingCache, oldestKey)
			s.embeddingCacheAccessList = s.embeddingCacheAccessList[1:]
			s.logger.Debug("Embedding cache eviction", "evicted_key", truncate.WithEllipsis(oldestKey, 30))
		}
	}

	s.embeddingCache[query] = embedding
	s.embeddingCacheAccessList = append(s.embeddingCacheAccessList, query)
	s.logger.Debug("Embedding cache miss, stored in cache", "query", truncate.WithEllipsis(query, 30), "cache_size", len(s.embeddingCache))

	return embedding
}

// touchAccessListLocked moves key to the back of the access list, marking it
// as most recently used. Caller must hold embeddingCacheMu (write).
func (s *RetrievalService) touchAccessListLocked(query string) {
	for i, k := range s.embeddingCacheAccessList {
		if k == query {
			s.embeddingCacheAccessList = append(
				append(s.embeddingCacheAccessList[:i:i], s.embeddingCacheAccessList[i+1:]...),
				query)
			return
		}
	}
	// Not in the list (shouldn't happen for a cached key): append for
	// consistency so eviction can still find it.
	s.embeddingCacheAccessList = append(s.embeddingCacheAccessList, query)
}
