package scoring

import (
	"fmt"
	"sync"
)

// ErrInvalidBudgetLimit is returned when maxLLMCalls is <= 0.
var ErrInvalidBudgetLimit = fmt.Errorf("max LLM calls must be > 0")

// Budget holds LLM scoring resource limits and current usage for one evolution generation.
type Budget struct {
	mu sync.Mutex

	// MaxLLMCalls is the maximum number of LLM scorer calls allowed per generation.
	MaxLLMCalls int

	// UsedLLMCalls is the number of LLM scorer calls made in the current generation.
	UsedLLMCalls int

	// CacheHits is the number of score lookups served from cache.
	CacheHits int

	// FallbackCount is the number of times LLM scoring failed and fell back.
	FallbackCount int
}

// NewBudget creates a new scoring budget.
//
// Args:
//
//	maxLLMCalls - maximum LLM calls allowed per generation (must be > 0).
//
// Returns:
//
//	*Budget - the budget instance.
//	error - non-nil if maxLLMCalls <= 0.
func NewBudget(maxLLMCalls int) (*Budget, error) {
	if maxLLMCalls <= 0 {
		return nil, fmt.Errorf("new budget: %w", ErrInvalidBudgetLimit)
	}
	return &Budget{
		MaxLLMCalls: maxLLMCalls,
	}, nil
}

// CanCallLLM checks if an LLM call is still within budget.
//
// Returns:
//
//	bool - true if an LLM call can be made.
func (b *Budget) CanCallLLM() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.UsedLLMCalls < b.MaxLLMCalls
}

// RecordLLMCall records one LLM call against the budget.
func (b *Budget) RecordLLMCall() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.UsedLLMCalls++
}

// RecordCacheHit records a cache hit (does not consume budget).
func (b *Budget) RecordCacheHit() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.CacheHits++
}

// RecordFallback records a fallback to heuristic scoring.
func (b *Budget) RecordFallback() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.FallbackCount++
}

// Reset resets usage counters for a new generation while keeping limits.
func (b *Budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.UsedLLMCalls = 0
	b.CacheHits = 0
	b.FallbackCount = 0
}

// Usage returns current budget utilization.
//
// Returns:
//
//	used - LLM calls used.
//	max - max LLM calls allowed.
//	cacheHits - cache hit count.
//	fallbacks - fallback count.
func (b *Budget) Usage() (used, max, cacheHits, fallbacks int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.UsedLLMCalls, b.MaxLLMCalls, b.CacheHits, b.FallbackCount
}
