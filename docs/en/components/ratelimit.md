# RateLimiter Design Document

## 1. Overview

The RateLimiter module provides rate limiting and backpressure mechanisms for controlling request rates and preventing system overload.

## 2. Rate Limiting Strategies

| Strategy | Use Case | Implementation |
|----------|----------|----------------|
| Token Bucket | LLM rate limiting | Smooth rate |
| Sliding Window | Global QPS control | Precise counting |
| Semaphore | Agent concurrency control | Channel-based |
| Weighted Semaphore | Resource weight control | Condition variable |

## 3. Core Interface

```go
type Limiter interface {
    // Allow checks if request is allowed
    Allow(ctx context.Context) (bool, error)
    
    // Wait blocks until request can proceed
    Wait(ctx context.Context) error
    
    // Reset resets the limiter
    Reset()
    
    // Rate returns current rate
    Rate() float64
}
```

## 4. Limiter Implementations

### 4.1 Token Bucket (TokenBucketLimiter)

```go
type TokenBucketLimiter struct {
    tokens     float64
    maxTokens  float64
    rate       float64
    lastCheck  time.Time
    mu         sync.Mutex
    config     *LimiterConfig
}

// Key methods
func (l *TokenBucketLimiter) Allow(ctx context.Context) (bool, error)
func (l *TokenBucketLimiter) Wait(ctx context.Context) error
func (l *TokenBucketLimiter) AvailableTokens() float64
func (l *TokenBucketLimiter) SetRate(rate float64)
func (l *TokenBucketLimiter) SetBurst(burst int)
```

### 4.2 Sliding Window (SlidingWindowLimiter)

```go
type SlidingWindowLimiter struct {
    requests     []time.Time
    windowSize    time.Duration
    maxRequests   int
    mu            sync.Mutex
    config        *LimiterConfig
}

// Key methods
func (l *SlidingWindowLimiter) Allow(ctx context.Context) (bool, error)
func (l *SlidingWindowLimiter) Wait(ctx context.Context) error
func (l *SlidingWindowLimiter) CurrentCount() int
func (l *SlidingWindowLimiter) Remaining() int
```

### 4.3 Semaphore (SemaphoreLimiter)

Channel-based semaphore implementation.

```go
type SemaphoreLimiter struct {
    sem      chan struct{}
    acquired map[string]int
    mu       sync.RWMutex
    config   *LimiterConfig
}

// Key methods
func (l *SemaphoreLimiter) Acquire(ctx context.Context, key string) error
func (l *SemaphoreLimiter) Release(key string)
func (l *SemaphoreLimiter) Allow(ctx context.Context) (bool, error)
func (l *SemaphoreLimiter) Available() int
```

### 4.4 Weighted Semaphore (WeightedSemaphoreLimiter)

Supports weighted resource acquisition.

```go
type WeightedSemaphoreLimiter struct {
    mu        sync.Mutex
    available int
    used      int
    weighted  map[string]int
    cond      *sync.Cond
    config    *LimiterConfig
}

// Key methods
func (l *WeightedSemaphoreLimiter) Acquire(ctx context.Context, key string, weight int) error
func (l *WeightedSemaphoreLimiter) Release(key string, weight int)
func (l *WeightedSemaphoreLimiter) Allow(ctx context.Context, weight int) (bool, error)
```

## 5. Factory Pattern

Create limiters through factory:

```go
type Factory struct {
    creators map[LimiterType]func(*LimiterConfig) Limiter
}

// Create limiter
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeTokenBucket, config)
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeSlidingWindow, config)
limiter, err := ratelimit.CreateLimiter(ratelimit.LimiterTypeSemaphore, config)
```

## 6. Usage Example

```go
// Create limiter
config := &ratelimit.LimiterConfig{
    Rate:  10,            // Requests per second
    Burst: 50,            // Burst capacity
}

llmLimiter := ratelimit.NewTokenBucketLimiter(config)

// In request handling
func handleRequest(ctx context.Context) error {
    // Non-blocking check
    if ok, _ := llmLimiter.Allow(ctx); !ok {
        return ErrRateLimitExceeded
    }
    
    // Or block and wait
    if err := llmLimiter.Wait(ctx); err != nil {
        return err
    }
    
    // Process request
    return doProcess(ctx)
}
```

### Keyed Semaphore

```go
semLimiter := ratelimit.NewSemaphoreLimiter(config)

// Rate limit by user
userID := getUserID(ctx)
if err := semLimiter.Acquire(ctx, userID); err != nil {
    return err
}
defer semLimiter.Release(userID)
```

## 7. Configuration Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| Rate | 10 | Requests per second |
| Burst | 10 | Burst capacity |
| Timeout | 30s | Wait timeout |
| RefillRate | 1s | Token refill interval |
