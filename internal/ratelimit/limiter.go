package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter defines the rate limiter interface.
type Limiter interface {
	// Allow checks if a request is allowed.
	Allow(ctx context.Context) (bool, error)
	// Wait blocks until a request can be processed or context is cancelled.
	Wait(ctx context.Context) error
	// Reset resets the limiter state.
	Reset()
	// Rate returns the current rate.
	Rate() float64
}

// LimiterConfig holds limiter configuration.
type LimiterConfig struct {
	Rate       float64       // requests per second
	Burst      int           // maximum burst size
	Timeout    time.Duration // wait timeout
	RefillRate time.Duration // token refill interval
}

// DefaultConfig returns the default rate limiter configuration.
// Returns:
// *LimiterConfig - default configuration with sensible defaults.
func DefaultConfig() *LimiterConfig {
	return &LimiterConfig{
		Rate:       DefaultRate,
		Burst:      DefaultBurst,
		Timeout:    DefaultLimiterTimeout,
		RefillRate: DefaultRefillRate,
	}
}

// LimiterType represents the type of limiter.
type LimiterType string

const (
	LimiterTypeTokenBucket   LimiterType = "token_bucket"
	LimiterTypeSlidingWindow LimiterType = "sliding_window"
	LimiterTypeSemaphore     LimiterType = "semaphore"
)

// Factory creates limiters based on type.
type Factory struct {
	mu       sync.RWMutex
	creators map[LimiterType]func(*LimiterConfig) Limiter
}

// NewFactory creates a new Factory with built-in limiter types registered.
// Returns:
// *Factory - a new Factory instance with token bucket, sliding window, and semaphore limiters registered.
func NewFactory() *Factory {
	f := &Factory{
		creators: make(map[LimiterType]func(*LimiterConfig) Limiter),
	}

	f.Register(LimiterTypeTokenBucket, func(config *LimiterConfig) Limiter {
		return NewTokenBucketLimiter(config)
	})

	f.Register(LimiterTypeSlidingWindow, func(config *LimiterConfig) Limiter {
		return NewSlidingWindowLimiter(config)
	})

	f.Register(LimiterTypeSemaphore, func(config *LimiterConfig) Limiter {
		return NewSemaphoreLimiter(config)
	})

	return f
}

// Register registers a limiter creator function for a specific limiter type.
func (f *Factory) Register(limiterType LimiterType, creator func(*LimiterConfig) Limiter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creators[limiterType] = creator
}

// Create creates a limiter instance of the specified type.
// Args:
// limiterType - the type of limiter to create.
// config - the limiter configuration (uses defaults if nil).
// Returns:
// Limiter - the created limiter instance.
// error - ErrUnsupportedLimiterType if limiter type is not registered.
func (f *Factory) Create(limiterType LimiterType, config *LimiterConfig) (Limiter, error) {
	f.mu.RLock()
	creator, exists := f.creators[limiterType]
	f.mu.RUnlock()
	if !exists {
		return nil, ErrUnsupportedLimiterType
	}

	if config == nil {
		config = DefaultConfig()
	}

	return creator(config), nil
}

// DefaultFactory is the default limiter factory.
var DefaultFactory = NewFactory()

// CreateLimiter creates a limiter using the default factory.
// Args:
// limiterType - the type of limiter to create.
// config - the limiter configuration (uses defaults if nil).
// Returns:
// Limiter - the created limiter instance.
// error - error if limiter creation fails.
func CreateLimiter(limiterType LimiterType, config *LimiterConfig) (Limiter, error) {
	return DefaultFactory.Create(limiterType, config)
}

// Limiter errors.
var (
	ErrUnsupportedLimiterType = &LimiterError{"unsupported limiter type"}
)

// LimiterError represents a limiter error.
type LimiterError struct {
	msg string
}

func (e *LimiterError) Error() string {
	return e.msg
}
