package ares_ratelimit

import "time"

// Default configuration constants for rate limiting.
const (
	// DefaultRate is the default request rate (requests per second).
	DefaultRate = 10.0

	// DefaultBurst is the default burst size for rate limiter.
	DefaultBurst = 20

	// DefaultTokenCapacity is the default capacity of the token bucket.
	DefaultTokenCapacity = 20

	// DefaultWindowDuration is the default sliding window duration.
	DefaultWindowDuration = time.Second

	// DefaultLimiterTimeout is the default timeout for acquiring permission.
	DefaultLimiterTimeout = 5 * time.Second

	// DefaultRefillRate is the default refill rate for token bucket (tokens per second).
	DefaultRefillRate = 1.0

	// DefaultSemaphoreWaitTime is the default wait time for semaphore acquisition.
	DefaultSemaphoreWaitTime = 10 * time.Millisecond

	// DefaultSemaphoreMaxWaitTime is the default maximum wait time for semaphore.
	DefaultSemaphoreMaxWaitTime = 100 * time.Millisecond

	// DefaultBackpressureQueueSize is the default queue size for backpressure limiter.
	DefaultBackpressureQueueSize = 100

	// DefaultBackpressureWaitTime is the default wait time for backpressure operations.
	DefaultBackpressureWaitTime = 10 * time.Millisecond

	// DefaultBackpressureMaxWaitTime is the default maximum wait time for backpressure.
	DefaultBackpressureMaxWaitTime = 100 * time.Millisecond
)
