package ratelimit

import (
	"context"
	"math"
	"sync"
	"time"
)

// SlidingWindowLimiter implements sliding window rate limiting.
type SlidingWindowLimiter struct {
	requests    []time.Time
	windowSize  time.Duration
	maxRequests int
	mu          sync.Mutex
	config      *LimiterConfig
}

// NewSlidingWindowLimiter creates a new SlidingWindowLimiter.
func NewSlidingWindowLimiter(config *LimiterConfig) Limiter {
	if config == nil {
		config = &LimiterConfig{Rate: 1, Burst: 1}
	}
	maxRequests := int(math.Ceil(config.Rate))
	return &SlidingWindowLimiter{
		requests:    make([]time.Time, 0),
		windowSize:  time.Second,
		maxRequests: maxRequests,
		config:      config,
	}
}

// Allow checks if a request is allowed.
func (l *SlidingWindowLimiter) Allow(ctx context.Context) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanup()

	if len(l.requests) < l.maxRequests {
		l.requests = append(l.requests, time.Now())
		return true, nil
	}

	return false, nil
}

// Wait blocks until a request can be processed.
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error {
	for {
		allowed, err := l.Allow(ctx)
		if err != nil {
			return err
		}

		if allowed {
			return nil
		}

		// Wait until oldest request expires from window
		l.mu.Lock()
		if len(l.requests) > 0 {
			oldest := l.requests[0]
			waitTime := l.windowSize - time.Since(oldest)
			l.mu.Unlock()

			if waitTime > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(waitTime):
				}
			}
		} else {
			l.mu.Unlock()
			// Window is empty but rate limit hit - wait a short time before retry
			// This prevents busy-waiting when the window is empty
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(l.windowSize / 10):
			}
		}
	}
}

// Reset clears all requests.
func (l *SlidingWindowLimiter) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.requests = make([]time.Time, 0)
}

// Rate returns the current rate.
func (l *SlidingWindowLimiter) Rate() float64 {
	return float64(l.maxRequests)
}

// cleanup removes expired requests from the window.
func (l *SlidingWindowLimiter) cleanup() {
	now := time.Now()
	cutoff := now.Add(-l.windowSize)

	i := 0
	for ; i < len(l.requests); i++ {
		if l.requests[i].After(cutoff) {
			break
		}
	}

	if i > 0 {
		l.requests = l.requests[i:]
	}
}

// CurrentCount returns the number of requests in current window.
func (l *SlidingWindowLimiter) CurrentCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanup()
	return len(l.requests)
}

// Remaining returns the number of remaining requests in current window.
func (l *SlidingWindowLimiter) Remaining() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanup()
	remaining := l.maxRequests - len(l.requests)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ResetAt resets the limiter at a specific time.
func (l *SlidingWindowLimiter) ResetAt(t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := t.Add(-l.windowSize)
	i := 0
	for ; i < len(l.requests); i++ {
		if l.requests[i].After(cutoff) {
			break
		}
	}

	if i > 0 {
		l.requests = l.requests[i:]
	}
}
