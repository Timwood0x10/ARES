package llm

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	goerrors "errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// Default retry/circuit-breaker constants. The client enables these by
// default so transient provider failures (rate limits, 5xx, transport errors)
// do not fail a whole regression run; callers can override via options.
const (
	defaultRetryMaxAttempts    = 3 // total attempts, first try included
	defaultRetryInitialBackoff = 500 * time.Millisecond
	defaultRetryMaxBackoff     = 8 * time.Second
	defaultRetryFactor         = 2.0

	defaultCircuitFailureThreshold = 3 // consecutive failures before opening
	defaultCircuitSuccessThreshold = 2 // half-open successes before closing
	defaultCircuitOpenTimeout      = 30 * time.Second

	retryJitterRatio = 0.4 // ±20% jitter around the computed backoff
)

// RetryPolicy controls how many times a retryable LLM call is attempted and
// how long the client waits between attempts (exponential backoff with jitter).
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts, first try included.
	// 0 or 1 means no retry. Negative values are treated as 1.
	MaxAttempts int
	// InitialBackoff is the wait after the first failure.
	InitialBackoff time.Duration
	// MaxBackoff caps the exponential growth.
	MaxBackoff time.Duration
	// Factor multiplies the backoff after each consecutive failure.
	Factor float64
}

// DefaultRetryPolicy returns the client's default policy: up to 3 attempts
// with 500ms -> 1s -> 2s exponential backoff, capped at 8s.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts:    defaultRetryMaxAttempts,
		InitialBackoff: defaultRetryInitialBackoff,
		MaxBackoff:     defaultRetryMaxBackoff,
		Factor:         defaultRetryFactor,
	}
}

// backoff returns the sleep duration before attempt `failedAttempts+1`,
// computed as initial * factor^failedAttempts with ±20% jitter.
func (p RetryPolicy) backoff(failedAttempts int) time.Duration {
	if failedAttempts <= 0 {
		return p.InitialBackoff
	}
	delay := float64(p.InitialBackoff) * math.Pow(p.Factor, float64(failedAttempts-1))
	if max := float64(p.MaxBackoff); delay > max {
		delay = max
	}
	delay *= 1 - retryJitterRatio/2 + retryJitterRatio*secureRandomFloat()
	return time.Duration(delay)
}

// secureRandomFloat returns a uniform value in [0, 1) from crypto/rand.
// On RNG failure it falls back to 0.5 so backoff still behaves sanely; the
// jitter is a scheduling nicety, not a security boundary.
func secureRandomFloat() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0.5
	}
	// Use the top 53 bits (like math.Float64frombits for [0,1)).
	return float64(binary.BigEndian.Uint64(buf[:])>>11) / (1 << 53)
}

// isRetryableError reports whether a failed LLM call is worth retrying:
// HTTP 429 rate limits, any 5xx server error, or a transport-level failure
// (connection refused/reset/timeout). 4xx client errors and decode errors are
// not retryable because retrying cannot fix them.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if goerrors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusTooManyRequests ||
			httpErr.StatusCode >= http.StatusInternalServerError
	}
	var urlErr *url.Error
	return goerrors.As(err, &urlErr)
}

// CircuitBreaker guards LLM calls against sustained provider failures
// (Ch.8 release-gate resilience): after `failureThreshold` consecutive
// failures it opens and fails fast for `openTimeout`, then admits one probe
// request in half-open state. A single probe failure re-opens; two consecutive
// probe successes close the circuit.
//
// The breaker is safe for concurrent use; all state is mutex-protected.
type CircuitBreaker struct {
	mu               sync.Mutex
	state            circuitState
	failureCount     int
	failureThreshold int
	successThreshold int
	lastFailureTime  time.Time
	openTimeout      time.Duration
	halfOpenSuccess  int
	halfOpenInflight int
	lastProbeTime    time.Time
}

// circuitState is the breaker lifecycle state.
type circuitState int

const (
	circuitClosed circuitState = iota
	circuitOpen
	circuitHalfOpen
)

// NewCircuitBreaker creates a breaker that opens after failureThreshold
// consecutive failures and allows a probe after openTimeout.
func NewCircuitBreaker(failureThreshold int, openTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:            circuitClosed,
		failureThreshold: failureThreshold,
		successThreshold: defaultCircuitSuccessThreshold,
		openTimeout:      openTimeout,
	}
}

// Allow returns nil when the call may proceed, or errors.ErrCircuitBreakerOpen
// when the circuit is open (or a half-open probe is already in flight).
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		return nil
	case circuitOpen:
		if time.Since(cb.lastFailureTime) <= cb.openTimeout {
			return errors.ErrCircuitBreakerOpen
		}
		// Open timeout elapsed: move to half-open and admit one probe.
		cb.state = circuitHalfOpen
		cb.halfOpenSuccess = 0
		cb.halfOpenInflight = 1
		cb.lastProbeTime = time.Now()
		return nil
	case circuitHalfOpen:
		// Leak guard: if the previous probe never returned (e.g. the request
		// was cancelled after Allow), reclaim the slot after the open timeout.
		if cb.halfOpenInflight > 0 && time.Since(cb.lastProbeTime) > cb.openTimeout {
			cb.halfOpenInflight = 0
		}
		if cb.halfOpenInflight > 0 {
			return errors.ErrCircuitBreakerOpen
		}
		cb.halfOpenInflight = 1
		cb.lastProbeTime = time.Now()
		return nil
	default:
		return fmt.Errorf("llm: circuit breaker in unknown state %d", cb.state)
	}
}

// RecordSuccess reports a successful call: resets the failure count in closed
// state, or counts toward closing the circuit in half-open state.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		cb.failureCount = 0
	case circuitHalfOpen:
		cb.halfOpenInflight = 0
		cb.halfOpenSuccess++
		if cb.halfOpenSuccess >= cb.successThreshold {
			cb.state = circuitClosed
			cb.failureCount = 0
			cb.halfOpenSuccess = 0
		}
	}
}

// RecordFailure reports a failed call: counts consecutive failures in closed
// state (opening the circuit at the threshold), or immediately re-opens from
// half-open state.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case circuitClosed:
		cb.failureCount++
		cb.lastFailureTime = time.Now()
		if cb.failureCount >= cb.failureThreshold {
			cb.state = circuitOpen
		}
	case circuitHalfOpen:
		cb.halfOpenInflight = 0
		cb.state = circuitOpen
		cb.lastFailureTime = time.Now()
	}
}

// IsOpen reports whether the circuit currently rejects requests.
func (cb *CircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state == circuitOpen
}

// withRetry runs fn under the client's retry policy and circuit breaker.
// The breaker is consulted before every attempt; retryable failures sleep with
// exponential backoff and retry up to MaxAttempts. Non-retryable failures and
// context cancellation return immediately.
//
// A standalone generic function (not a method): Go methods cannot declare
// their own type parameters.
func withRetry[T any](c *Client, ctx context.Context, fn func() (T, error)) (T, error) {
	var zero T
	attempts := c.retryPolicy.MaxAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if c.circuit != nil {
			if err := c.circuit.Allow(); err != nil {
				return zero, err
			}
		}
		result, err := fn()
		if err == nil {
			if c.circuit != nil {
				c.circuit.RecordSuccess()
			}
			return result, nil
		}
		lastErr = err
		if c.circuit != nil {
			c.circuit.RecordFailure()
		}
		if attempt >= attempts || !isRetryableError(err) || ctx.Err() != nil {
			return zero, lastErr
		}
		backoff := c.retryPolicy.backoff(attempt)
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(backoff):
		}
	}
	return zero, lastErr
}
