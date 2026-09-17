package ares_ratelimit

import "time"

// Default configuration constants for rate limiting.
// D3: Removed dead constants for SlidingWindow, Semaphore, and Backpressure
// (only TokenBucket is retained).
const (
	// DefaultRate is the default request rate (requests per second).
	DefaultRate = 10.0

	// DefaultBurst is the default burst size for rate limiter.
	DefaultBurst = 20

	// DefaultTokenCapacity is the default capacity of the token bucket.
	DefaultTokenCapacity = 20

	// DefaultLimiterTimeout is the default timeout for acquiring permission.
	DefaultLimiterTimeout = 5 * time.Second

	// DefaultRefillRate is the default refill rate for token bucket (tokens per second).
	DefaultRefillRate = 1.0
)
