package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// TokenBucketLimiter implements token bucket rate limiting.
type TokenBucketLimiter struct {
	tokens    float64
	maxTokens float64
	rate      float64
	mu        sync.Mutex
	lastCheck time.Time
	config    *LimiterConfig
}

// NewTokenBucketLimiter creates a new TokenBucketLimiter.
func NewTokenBucketLimiter(config *LimiterConfig) *TokenBucketLimiter {
	if config == nil {
		config = &LimiterConfig{Rate: 1, Burst: 1}
	}
	limiter := &TokenBucketLimiter{
		tokens:    float64(config.Burst),
		maxTokens: float64(config.Burst),
		rate:      config.Rate,
		config:    config,
		lastCheck: time.Now(),
	}

	return limiter
}

// Allow checks if a request is allowed without blocking.
func (l *TokenBucketLimiter) Allow(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()

	if l.tokens >= 1 {
		l.tokens--
		return true, nil
	}

	return false, nil
}

// Wait blocks until a request can be processed.
func (l *TokenBucketLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		l.refill()

		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()

			return nil
		}

		rate := l.rate
		l.mu.Unlock()

		// Check for zero or very small rate to avoid division by zero
		if rate <= 0 {
			return fmt.Errorf("rate must be positive, got %f", rate)
		}

		waitTime := time.Duration(float64(time.Second) / rate)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
		}
	}
}

// Reset resets the limiter to full capacity.
func (l *TokenBucketLimiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.tokens = l.maxTokens
	l.lastCheck = time.Now()
}

// Rate returns the current rate.
func (l *TokenBucketLimiter) Rate() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.rate
}

// refill adds tokens based on elapsed time.
func (l *TokenBucketLimiter) refill() {
	now := time.Now()
	elapsed := now.Sub(l.lastCheck)

	tokensToAdd := elapsed.Seconds() * l.rate
	l.tokens += tokensToAdd

	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}

	l.lastCheck = now
}

// AvailableTokens returns the number of available tokens.
func (l *TokenBucketLimiter) AvailableTokens() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refill()
	return l.tokens
}

// SetRate sets a new rate.
func (l *TokenBucketLimiter) SetRate(rate float64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.rate = rate
}

// SetBurst sets a new burst size.
func (l *TokenBucketLimiter) SetBurst(burst int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.maxTokens = float64(burst)
	if l.tokens > l.maxTokens {
		l.tokens = l.maxTokens
	}
}
