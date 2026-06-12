// nolint: errcheck // Test code may ignore return values
package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTokenBucketLimiter(t *testing.T) {
	t.Run("create token bucket", func(t *testing.T) {
		config := &LimiterConfig{
			Rate:  10,
			Burst: 20,
		}
		limiter := NewTokenBucketLimiter(config)

		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("allow request", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		allowed, err := limiter.Allow(context.Background())
		if err != nil {
			t.Errorf("allow error: %v", err)
		}
		if !allowed {
			t.Errorf("should be allowed")
		}
	})

	t.Run("rate", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		rate := limiter.Rate()
		if rate != 10 {
			t.Errorf("expected rate 10, got %f", rate)
		}
	})

	t.Run("reset", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		limiter.Allow(context.Background())
		limiter.Reset()

		allowed, _ := limiter.Allow(context.Background())
		if !allowed {
			t.Errorf("should be allowed after reset")
		}
	})

	t.Run("burst limit", func(t *testing.T) {
		config := &LimiterConfig{Rate: 1, Burst: 2}
		limiter := NewTokenBucketLimiter(config)

		// First request should be allowed
		allowed1, _ := limiter.Allow(context.Background())
		if !allowed1 {
			t.Errorf("first request should be allowed")
		}

		// Second request should be allowed (burst)
		allowed2, _ := limiter.Allow(context.Background())
		if !allowed2 {
			t.Errorf("second request should be allowed (burst)")
		}

		// Third request should be denied
		allowed3, _ := limiter.Allow(context.Background())
		if allowed3 {
			t.Errorf("third request should be denied (burst exhausted)")
		}
	})

	t.Run("wait with context", func(t *testing.T) {
		config := &LimiterConfig{Rate: 100, Burst: 1}
		limiter := NewTokenBucketLimiter(config)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := limiter.Wait(ctx)
		if err != nil {
			t.Errorf("wait error: %v", err)
		}
	})

	t.Run("wait context cancellation", func(t *testing.T) {
		config := &LimiterConfig{Rate: 1, Burst: 0}
		limiter := NewTokenBucketLimiter(config)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := limiter.Wait(ctx)
		if err != context.Canceled {
			t.Errorf("expected context cancelled error, got %v", err)
		}
	})

	t.Run("set rate", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		limiter.SetRate(20)
		rate := limiter.Rate()
		if rate != 20 {
			t.Errorf("expected rate 20, got %f", rate)
		}
	})

	t.Run("set burst", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		limiter.SetBurst(20)
		tokens := limiter.AvailableTokens()
		if tokens > 20 {
			t.Errorf("expected tokens <= 20, got %f", tokens)
		}
	})

	t.Run("available tokens", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		tokens := limiter.AvailableTokens()
		if tokens < 9.9 || tokens > 10.1 {
			t.Errorf("expected approximately 10 tokens, got %f", tokens)
		}

		limiter.Allow(context.Background())
		tokens = limiter.AvailableTokens()
		if tokens < 8.9 || tokens > 9.1 {
			t.Errorf("expected approximately 9 tokens, got %f", tokens)
		}
	})

	t.Run("token refill", func(t *testing.T) {
		config := &LimiterConfig{Rate: 100, Burst: 1}
		limiter := NewTokenBucketLimiter(config)

		limiter.Allow(context.Background())
		allowed, _ := limiter.Allow(context.Background())
		if allowed {
			t.Errorf("should be denied after consuming token")
		}

		time.Sleep(15 * time.Millisecond)
		allowed, _ = limiter.Allow(context.Background())
		if !allowed {
			t.Errorf("should be allowed after refill")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		config := &LimiterConfig{Rate: 100, Burst: 10}
		limiter := NewTokenBucketLimiter(config)

		var wg sync.WaitGroup
		successCount := 0
		var mu sync.Mutex

		for i := 0; i < 15; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				allowed, _ := limiter.Allow(context.Background())
				if allowed {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}()
		}

		wg.Wait()

		if successCount != 10 {
			t.Errorf("expected 10 successful requests, got %d", successCount)
		}
	})
}

func TestSlidingWindowLimiter(t *testing.T) {
	t.Run("create sliding window", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewSlidingWindowLimiter(config)

		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("allow request", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewSlidingWindowLimiter(config)

		allowed, err := limiter.Allow(context.Background())
		if err != nil {
			t.Errorf("allow error: %v", err)
		}
		if !allowed {
			t.Errorf("should be allowed")
		}
	})

	t.Run("rate limit", func(t *testing.T) {
		config := &LimiterConfig{Rate: 5, Burst: 5}
		limiter := NewSlidingWindowLimiter(config)

		for i := 0; i < 5; i++ {
			allowed, _ := limiter.Allow(context.Background())
			if !allowed {
				t.Errorf("request %d should be allowed", i)
			}
		}

		// 6th request should be denied
		allowed, _ := limiter.Allow(context.Background())
		if allowed {
			t.Errorf("6th request should be denied")
		}
	})

	t.Run("reset", func(t *testing.T) {
		config := &LimiterConfig{Rate: 5, Burst: 5}
		limiter := NewSlidingWindowLimiter(config)

		for i := 0; i < 5; i++ {
			limiter.Allow(context.Background())
		}

		limiter.Reset()

		allowed, _ := limiter.Allow(context.Background())
		if !allowed {
			t.Errorf("should be allowed after reset")
		}
	})

	t.Run("current count", func(t *testing.T) {
		config := &LimiterConfig{Rate: 5, Burst: 5}
		limiter := NewSlidingWindowLimiter(config)

		count := limiter.(*SlidingWindowLimiter).CurrentCount()
		if count != 0 {
			t.Errorf("expected 0 count, got %d", count)
		}

		limiter.Allow(context.Background())
		count = limiter.(*SlidingWindowLimiter).CurrentCount()
		if count != 1 {
			t.Errorf("expected 1 count, got %d", count)
		}
	})

	t.Run("remaining", func(t *testing.T) {
		config := &LimiterConfig{Rate: 5, Burst: 5}
		limiter := NewSlidingWindowLimiter(config)

		remaining := limiter.(*SlidingWindowLimiter).Remaining()
		if remaining != 5 {
			t.Errorf("expected 5 remaining, got %d", remaining)
		}

		limiter.Allow(context.Background())
		remaining = limiter.(*SlidingWindowLimiter).Remaining()
		if remaining != 4 {
			t.Errorf("expected 4 remaining, got %d", remaining)
		}
	})

	t.Run("sliding window expiration", func(t *testing.T) {
		config := &LimiterConfig{Rate: 5, Burst: 5}
		limiter := NewSlidingWindowLimiter(config)

		// Fill the window
		for i := 0; i < 5; i++ {
			limiter.Allow(context.Background())
		}

		// Wait for window to slide
		time.Sleep(1100 * time.Millisecond)

		// Should be allowed again
		allowed, _ := limiter.Allow(context.Background())
		if !allowed {
			t.Errorf("should be allowed after window slides")
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewSlidingWindowLimiter(config)

		var wg sync.WaitGroup
		successCount := 0
		var mu sync.Mutex

		for i := 0; i < 15; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				allowed, _ := limiter.Allow(context.Background())
				if allowed {
					mu.Lock()
					successCount++
					mu.Unlock()
				}
			}()
		}

		wg.Wait()

		if successCount != 10 {
			t.Errorf("expected 10 successful requests, got %d", successCount)
		}
	})
}

func TestSemaphoreLimiter(t *testing.T) {
	t.Run("create semaphore", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewSemaphoreLimiter(config)

		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("acquire and release", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		err := limiter.Acquire(context.Background(), "test")
		if err != nil {
			t.Errorf("acquire error: %v", err)
		}

		limiter.Release("test")
	})

	t.Run("acquire with context cancellation", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 1}
		limiter := NewSemaphoreLimiter(config)

		// Acquire the only slot
		limiter.Acquire(context.Background(), "test1")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := limiter.Acquire(ctx, "test2")
		if err != context.Canceled {
			t.Errorf("expected context cancelled error, got %v", err)
		}
	})

	t.Run("rate", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		rate := limiter.Rate()
		if rate != 5 {
			t.Errorf("expected rate 5, got %f", rate)
		}
	})

	t.Run("available", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		available := limiter.Available()
		if available != 5 {
			t.Errorf("expected 5 available, got %d", available)
		}

		limiter.Acquire(context.Background(), "test")
		available = limiter.Available()
		if available != 4 {
			t.Errorf("expected 4 available, got %d", available)
		}
	})

	t.Run("acquired count", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		limiter.Acquire(context.Background(), "test")
		limiter.Acquire(context.Background(), "test")

		acquired := limiter.Acquired("test")
		if acquired != 2 {
			t.Errorf("expected 2 acquired, got %d", acquired)
		}
	})

	t.Run("reset", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		limiter.Acquire(context.Background(), "test")
		limiter.Reset()

		available := limiter.Available()
		if available != 5 {
			t.Errorf("expected 5 available after reset, got %d", available)
		}
	})

	t.Run("concurrent access", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 5}
		limiter := NewSemaphoreLimiter(config)

		var wg sync.WaitGroup
		successCount := 0
		var mu sync.Mutex

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := limiter.Acquire(context.Background(), "test")
				if err == nil {
					mu.Lock()
					successCount++
					mu.Unlock()
					limiter.Release("test")
				}
			}()
		}

		wg.Wait()

		if successCount != 10 {
			t.Errorf("expected 10 successful acquires, got %d", successCount)
		}
	})
}

func TestWeightedSemaphoreLimiter(t *testing.T) {
	t.Run("create weighted semaphore", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewWeightedSemaphoreLimiter(config)

		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("acquire with weight", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewWeightedSemaphoreLimiter(config)

		err := limiter.Acquire(context.Background(), "test", 5)
		if err != nil {
			t.Errorf("acquire error: %v", err)
		}

		limiter.Release("test", 5)
	})

	t.Run("available", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewWeightedSemaphoreLimiter(config)

		limiter.Acquire(context.Background(), "test", 3)

		available := limiter.Available()
		if available != 7 {
			t.Errorf("expected 7 available, got %d", available)
		}
	})

	t.Run("reset", func(t *testing.T) {
		config := &LimiterConfig{Rate: 10, Burst: 10}
		limiter := NewWeightedSemaphoreLimiter(config)

		limiter.Acquire(context.Background(), "test", 5)
		limiter.Reset()

		available := limiter.Available()
		if available != 10 {
			t.Errorf("expected 10 available after reset, got %d", available)
		}
	})
}

func TestLimiterFactory(t *testing.T) {
	t.Run("create limiter", func(t *testing.T) {
		factory := NewFactory()
		limiter, err := factory.Create(LimiterTypeTokenBucket, &LimiterConfig{Rate: 10, Burst: 10})

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		factory := NewFactory()
		_, err := factory.Create("unknown", nil)

		if err != ErrUnsupportedLimiterType {
			t.Errorf("expected unsupported type error")
		}
	})

	t.Run("create sliding window", func(t *testing.T) {
		factory := NewFactory()
		limiter, err := factory.Create(LimiterTypeSlidingWindow, &LimiterConfig{Rate: 10, Burst: 10})

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("create semaphore", func(t *testing.T) {
		factory := NewFactory()
		limiter, err := factory.Create(LimiterTypeSemaphore, &LimiterConfig{Rate: 10, Burst: 10})

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("create with nil config", func(t *testing.T) {
		factory := NewFactory()
		limiter, err := factory.Create(LimiterTypeTokenBucket, nil)

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})

	t.Run("register custom limiter", func(t *testing.T) {
		factory := NewFactory()
		factory.Register("custom", func(config *LimiterConfig) Limiter {
			return NewTokenBucketLimiter(config)
		})

		limiter, err := factory.Create("custom", &LimiterConfig{Rate: 10, Burst: 10})

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("default configuration", func(t *testing.T) {
		config := DefaultConfig()

		if config.Rate != 10 {
			t.Errorf("expected rate 10, got %f", config.Rate)
		}

		if config.Burst != 20 {
			t.Errorf("expected burst 20, got %d", config.Burst)
		}
	})
}

func TestCreateLimiter(t *testing.T) {
	t.Run("create using helper function", func(t *testing.T) {
		limiter, err := CreateLimiter(LimiterTypeTokenBucket, &LimiterConfig{Rate: 10, Burst: 10})

		if err != nil {
			t.Errorf("create error: %v", err)
		}
		if limiter == nil {
			t.Errorf("limiter should not be nil")
		}
	})
}

func TestLimiterError(t *testing.T) {
	t.Run("error message", func(t *testing.T) {
		err := &LimiterError{msg: "test error"}
		if err.Error() != "test error" {
			t.Errorf("expected 'test error', got %s", err.Error())
		}
	})
}

// nolint: errcheck // Test code may ignore return values
