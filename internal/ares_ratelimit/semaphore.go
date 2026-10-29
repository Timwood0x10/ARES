package ares_ratelimit

import (
	"context"
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
		log.Warn("SemaphoreLimiter.Release: no slot available to release",
			"key", key,
			"hint", "this indicates a bug: releasing more times than acquired")
	}
}

// Allow checks if a request is allowed and acquires a slot if so.
// This is a non-blocking try-acquire: if a slot is available, it is acquired
// and the caller MUST call Release to free it.
func (l *SemaphoreLimiter) Allow(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	select {
	case l.sem <- struct{}{}:
		l.mu.Lock()
		l.acquired["default"]++
		l.mu.Unlock()
		return true, nil
	default:
		return false, nil
	}
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
