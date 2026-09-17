package ares_ratelimit

import (
	"context"
	"fmt"
	"math/rand"
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
// Calculates the precise wait time based on the fractional token deficit so
// waiters do not all sleep for a full token period, then adds a small random
// jitter: without it, concurrent waiters that observed the same deficit
// compute the same waitTime, wake at the same refill instant, and stampede
// the freshly refilled token (thundering herd). The jitter is uniform in
// [0, waitTime/8) — bounded so it never dominates the deficit wait, and
// random so same-deficit waiters desynchronize.
func (l *TokenBucketLimiter) Wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		l.refill()

		if l.tokens >= 1 {
			l.tokens--
			l.mu.Unlock()
			return nil
		}

		// Calculate precise wait time for the fractional token needed.
		// deficit is the fraction of a token we need to wait for.
		rate := l.rate
		deficit := 1.0 - l.tokens // tokens needed to reach 1.0
		l.mu.Unlock()

		// Check for zero or very small rate to avoid division by zero.
		if rate <= 0 {
			return fmt.Errorf("rate must be positive, got %f", rate)
		}

		// Wait only long enough for the deficit to be refilled.
		waitTime := time.Duration((deficit / rate) * float64(time.Second))
		if waitTime <= 0 {
			waitTime = time.Millisecond // Minimum sleep to avoid busy-loop.
		}
		waitTime += jitter(waitTime)

		tbTimer := time.NewTimer(waitTime)
		select {
		case <-ctx.Done():
			tbTimer.Stop()
			return ctx.Err()
		case <-tbTimer.C:
		}
	}
}

// jitter spreads concurrent waiters that computed the same deficit so they
// do not all wake at the refill instant. #nosec G404 — jitter does not
// require a cryptographically secure source (same posture as the evolution
// mutator's use of math/rand).
func jitter(waitTime time.Duration) time.Duration {
	spread := int64(waitTime) / 8
	if spread <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(spread)) // #nosec G404
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
