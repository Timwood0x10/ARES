package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// FallbackStrategy defines the fallback behavior when embedding fails.
type FallbackStrategy int

const (
	// FallbackToCache falls back to cache only.
	FallbackToCache FallbackStrategy = iota

	// FallbackToKeyword falls back to keyword search (returns error).
	FallbackToKeyword

	// FallbackToError returns the error directly.
	FallbackToError
)

// FallbackClient wraps EmbeddingClient with fallback strategies.
type FallbackClient struct {
	client   *EmbeddingClient
	mu       sync.RWMutex
	strategy FallbackStrategy
}

// NewFallbackClient creates a new fallback client.
func NewFallbackClient(client *EmbeddingClient, strategy FallbackStrategy) *FallbackClient {
	return &FallbackClient{
		client:   client,
		strategy: strategy,
	}
}

// Embed generates embedding with fallback support.
func (f *FallbackClient) Embed(ctx context.Context, text string) ([]float64, error) {
	// Try normal embedding first
	embedding, err := f.client.Embed(ctx, text)
	if err == nil {
		return embedding, nil
	}

	// Read strategy under lock to avoid data race with SetStrategy.
	f.mu.RLock()
	strategy := f.strategy
	f.mu.RUnlock()

	// Apply fallback strategy
	switch strategy {
	case FallbackToCache:
		// Try to get from cache only
		return f.getFromCache(ctx, text)
	case FallbackToKeyword:
		// Return nil to trigger keyword search
		return nil, ErrEmbeddingFailed
	case FallbackToError:
		// Return the original error
		return nil, err
	default:
		return nil, err
	}
}

// getFromCache tries to get embedding from cache only.
func (f *FallbackClient) getFromCache(ctx context.Context, text string) ([]float64, error) {
	cacheKey := f.client.getCacheKey(text, "query")

	if f.client.redis == nil {
		return nil, ErrEmbeddingFailed
	}

	cached, err := f.client.redis.Get(ctx, cacheKey)
	if err != nil {
		return nil, ErrEmbeddingFailed
	}
	var embedding []float64
	if err := json.Unmarshal([]byte(cached), &embedding); err != nil {
		return nil, ErrEmbeddingFailed
	}

	return embedding, nil
}

// EmbedBatch generates embeddings with fallback support.
func (f *FallbackClient) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	// Try normal batch embedding first
	embeddings, err := f.client.EmbedBatch(ctx, texts)
	if err == nil {
		return embeddings, nil
	}

	// Read strategy under lock to avoid data race with SetStrategy.
	f.mu.RLock()
	strategy := f.strategy
	f.mu.RUnlock()

	// Apply fallback strategy
	switch strategy {
	case FallbackToCache:
		// Try to get from cache for each text
		return f.getBatchFromCache(ctx, texts)
	case FallbackToKeyword:
		// Return nil to trigger keyword search
		return nil, ErrEmbeddingFailed
	case FallbackToError:
		// Return the original error
		return nil, err
	default:
		return nil, err
	}
}

// getBatchFromCache tries to get embeddings from cache only.
func (f *FallbackClient) getBatchFromCache(ctx context.Context, texts []string) ([][]float64, error) {
	if f.client.redis == nil {
		return nil, ErrEmbeddingFailed
	}

	embeddings := make([][]float64, len(texts))
	allFound := true

	for i, text := range texts {
		cacheKey := f.client.getCacheKey(text, "query")

		cached, err := f.client.redis.Get(ctx, cacheKey)
		if err != nil {
			allFound = false
			continue
		}
		var embedding []float64
		if err := json.Unmarshal([]byte(cached), &embedding); err != nil {
			allFound = false
			continue
		}

		embeddings[i] = embedding
	}

	if !allFound {
		return nil, ErrEmbeddingFailed
	}

	return embeddings, nil
}

// SetStrategy updates the fallback strategy.
func (f *FallbackClient) SetStrategy(strategy FallbackStrategy) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.strategy = strategy
}

// GetStrategy returns the current fallback strategy.
func (f *FallbackClient) GetStrategy() FallbackStrategy {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.strategy
}

// Embedding errors
var (
	// ErrEmbeddingFailed indicates embedding generation failed.
	ErrEmbeddingFailed = fmt.Errorf("embedding generation failed")
)
