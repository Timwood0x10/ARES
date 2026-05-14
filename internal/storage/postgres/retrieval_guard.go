// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"context"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/errors"

	"golang.org/x/time/rate"
)

// RetrievalGuard provides protection mechanisms for retrieval operations.
// This implements rate limiting, circuit breaking, and timeout protection to prevent system overload.
type RetrievalGuard struct {
	rateLimiter       *rate.Limiter
	circuitBreaker    *CircuitBreaker
	dbTimeout         time.Duration
	maxRequestsPerSec int
}

// NewRetrievalGuard creates a new RetrievalGuard instance.
// Args:
// maxRequestsPerSec - maximum requests per second allowed.
// failureThreshold - number of failures before opening circuit breaker.
// openTimeout - time to wait before attempting half-open state.
// dbTimeout - database operation timeout.
// Returns new RetrievalGuard instance.
func NewRetrievalGuard(maxRequestsPerSec int, failureThreshold int, openTimeout, dbTimeout time.Duration) *RetrievalGuard {
	return &RetrievalGuard{
		rateLimiter:       rate.NewLimiter(rate.Limit(maxRequestsPerSec), maxRequestsPerSec),
		circuitBreaker:    NewCircuitBreaker(failureThreshold, openTimeout),
		dbTimeout:         dbTimeout,
		maxRequestsPerSec: maxRequestsPerSec,
	}
}

// AllowRateLimit checks if a request should be allowed based on rate limiting.
// Returns error if rate limit is exceeded.
func (g *RetrievalGuard) AllowRateLimit() error {
	if !g.rateLimiter.Allow() {
		return errors.ErrRateLimitExceeded
	}
	return nil
}

// CheckEmbeddingCircuitBreaker checks if embedding service is available.
// Returns error if circuit breaker is open, triggering fallback to keyword-only search.
func (g *RetrievalGuard) CheckEmbeddingCircuitBreaker() error {
	if err := g.circuitBreaker.AllowRequest(); err != nil {
		return errors.ErrCircuitBreakerOpen
	}
	return nil
}

// RecordEmbeddingFailure records a failed embedding operation.
// This may trigger circuit breaker opening if failure threshold is reached.
func (g *RetrievalGuard) RecordEmbeddingFailure() {
	g.circuitBreaker.RecordFailure()
}

// RecordEmbeddingSuccess records a successful embedding operation.
// This may help recover circuit breaker from open state.
func (g *RetrievalGuard) RecordEmbeddingSuccess() {
	g.circuitBreaker.RecordSuccess()
}

// WithDBTimeout creates a context with database timeout protection.
// This prevents long-running database queries from blocking the system.
// Args:
// ctx - original context.
// Returns new context with timeout and cancel function.
func (g *RetrievalGuard) WithDBTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, g.dbTimeout)
}

// CheckDBTimeout checks if a database operation exceeded the timeout.
// Returns error if context deadline was exceeded.
func (g *RetrievalGuard) CheckDBTimeout(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.ErrDBTimeout
	default:
		return nil
	}
}

// GetCircuitBreakerState returns the current circuit breaker state.
// This is primarily used for monitoring and debugging.
// Returns current circuit breaker state.
func (g *RetrievalGuard) GetCircuitBreakerState() CircuitBreakerState {
	return g.circuitBreaker.State()
}

// ResetCircuitBreaker resets the circuit breaker to closed state.
// This is primarily used for testing purposes.
func (g *RetrievalGuard) ResetCircuitBreaker() {
	g.circuitBreaker.Reset()
}
