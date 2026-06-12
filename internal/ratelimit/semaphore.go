package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// SemaphoreLimiter implements semaphore-based rate limiting.
type SemaphoreLimiter struct {
	sem      chan struct{}
	acquired map[string]int
	mu       sync.RWMutex
	config   *LimiterConfig
}

// NewSemaphoreLimiter creates a new SemaphoreLimiter.
func NewSemaphoreLimiter(config *LimiterConfig) *SemaphoreLimiter {
	if config == nil {
		config = &LimiterConfig{Burst: 1}
	}
	return &SemaphoreLimiter{
		sem:      make(chan struct{}, config.Burst),
		acquired: make(map[string]int),
		config:   config,
	}
}

// Acquire acquires a semaphore slot.
func (l *SemaphoreLimiter) Acquire(ctx context.Context, key string) error {
	select {
	case l.sem <- struct{}{}:
		l.mu.Lock()
		l.acquired[key]++
		l.mu.Unlock()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release releases a semaphore slot.
// Logs a warning if no slot is available to release (indicates potential bug in caller).
func (l *SemaphoreLimiter) Release(key string) {
	select {
	case <-l.sem:
		l.mu.Lock()
		if count, exists := l.acquired[key]; exists && count > 0 {
			l.acquired[key]--
		}
		l.mu.Unlock()
	default:
		slog.Warn("SemaphoreLimiter.Release: no slot available to release",
			"key", key,
			"hint", "this indicates a bug: releasing more times than acquired")
	}
}

// Allow checks if a request is allowed without blocking or acquiring a slot.
func (l *SemaphoreLimiter) Allow(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	return len(l.sem) < cap(l.sem), nil
}

// Wait blocks until a request can be processed.
func (l *SemaphoreLimiter) Wait(ctx context.Context) error {
	return l.Acquire(ctx, "default")
}

// Reset releases all slots.
func (l *SemaphoreLimiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Drain the semaphore
	for {
		select {
		case <-l.sem:
		default:
			goto done
		}
	}

done:
	l.acquired = make(map[string]int)
}

// Rate returns the current rate.
func (l *SemaphoreLimiter) Rate() float64 {
	return float64(l.config.Burst)
}

// Available returns the number of available slots.
func (l *SemaphoreLimiter) Available() int {
	return cap(l.sem) - len(l.sem)
}

// Acquired returns the number of acquired slots for a key.
func (l *SemaphoreLimiter) Acquired(key string) int {
	l.mu.RLock()
	defer l.mu.RUnlock()

	return l.acquired[key]
}

// WeightedSemaphoreLimiter implements weighted semaphore-based rate limiting.
type WeightedSemaphoreLimiter struct {
	mu        sync.Mutex
	available int
	used      int
	weighted  map[string]int
	config    *LimiterConfig
	cond      *sync.Cond // Use condition variable for efficient waiting
}

// NewWeightedSemaphoreLimiter creates a new WeightedSemaphoreLimiter.
func NewWeightedSemaphoreLimiter(config *LimiterConfig) *WeightedSemaphoreLimiter {
	if config == nil {
		config = &LimiterConfig{Burst: 1}
	}
	limiter := &WeightedSemaphoreLimiter{
		available: config.Burst,
		weighted:  make(map[string]int),
		config:    config,
	}
	limiter.cond = sync.NewCond(&limiter.mu)
	return limiter
}

// Acquire acquires weighted slots.
// Uses sync.Cond for efficient waiting without busy-looping.
// Context cancellation is propagated via context.AfterFunc to wake cond.Wait().
func (l *WeightedSemaphoreLimiter) Acquire(ctx context.Context, key string, weight int) error {
	if weight <= 0 {
		return fmt.Errorf("weight must be positive, got %d", weight)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for l.available < weight {
		if err := ctx.Err(); err != nil {
			return err
		}

		stop := context.AfterFunc(ctx, func() {
			l.mu.Lock()
			l.cond.Broadcast()
			l.mu.Unlock()
		})
		l.cond.Wait()
		stop()

		if err := ctx.Err(); err != nil {
			return err
		}
	}

	l.available -= weight
	l.used += weight
	l.weighted[key] += weight
	return nil
}

// Release releases weighted slots.
func (l *WeightedSemaphoreLimiter) Release(key string, weight int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if count, exists := l.weighted[key]; exists && count >= weight {
		l.weighted[key] -= weight
		l.available += weight
		l.used -= weight

		if l.weighted[key] <= 0 {
			delete(l.weighted, key)
		}
		// Wake up waiting goroutines
		l.cond.Broadcast()
	}
}

// Allow checks if request is allowed.
func (l *WeightedSemaphoreLimiter) Allow(ctx context.Context, weight int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.available >= weight {
		l.available -= weight
		l.used += weight
		return true, nil
	}

	return false, nil
}

// Reset releases all slots.
func (l *WeightedSemaphoreLimiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.available = l.config.Burst
	l.used = 0
	l.weighted = make(map[string]int)
	l.cond.Broadcast()
}

// Available returns available slots.
func (l *WeightedSemaphoreLimiter) Available() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.available
}
