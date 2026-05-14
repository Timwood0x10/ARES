// Package postgres provides PostgreSQL database operations for the storage system.
package postgres

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/errors"
)

// CircuitBreakerState represents the state of a circuit breaker.
type CircuitBreakerState string

const (
	CircuitBreakerStateClosed   CircuitBreakerState = "closed"
	CircuitBreakerStateOpen     CircuitBreakerState = "open"
	CircuitBreakerStateHalfOpen CircuitBreakerState = "half-open"
)

// CircuitBreaker provides failure detection and automatic fallback for unreliable services.
// This implements the circuit breaker pattern to prevent cascading failures.
type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitBreakerState
	failureCount     int
	failureThreshold int
	successThreshold int
	lastFailureTime  time.Time
	openTimeout      time.Duration
	halfOpenSuccess  int
	halfOpenInflight atomic.Int32
	lastCleanupTime  time.Time
	stopCh           chan struct{}
	cleanupStopped   atomic.Bool
}

// NewCircuitBreaker creates a new CircuitBreaker instance.
// Args:
// failureThreshold - number of failures before opening the circuit.
// openTimeout - time to wait before attempting half-open state.
// Returns new CircuitBreaker instance.
func NewCircuitBreaker(failureThreshold int, openTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		state:            CircuitBreakerStateClosed,
		failureThreshold: failureThreshold,
		successThreshold: 3,
		openTimeout:      openTimeout,
		lastCleanupTime:  time.Now(),
		stopCh:           make(chan struct{}),
	}

	// Start cleanup goroutine to prevent halfOpenInflight leaks
	go cb.cleanupLoop()

	return cb
}

// AllowRequest checks if a request should be allowed based on circuit breaker state.
// Returns error if circuit is open or enters open state.
func (cb *CircuitBreaker) AllowRequest() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitBreakerStateClosed:
		return nil

	case CircuitBreakerStateOpen:
		if time.Since(cb.lastFailureTime) > cb.openTimeout {
			// Move to half-open state and allow one probe request.
			cb.state = CircuitBreakerStateHalfOpen
			cb.halfOpenSuccess = 0
			cb.halfOpenInflight.Store(1) // Reserve the single half-open slot.
			return nil
		}
		return errors.ErrCircuitBreakerOpen

	case CircuitBreakerStateHalfOpen:
		if !cb.halfOpenInflight.CompareAndSwap(0, 1) {
			return errors.ErrCircuitBreakerOpen
		}
		return nil

	default:
		return errors.ErrInvalidState
	}
}

// RecordSuccess records a successful operation.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitBreakerStateHalfOpen {
		cb.halfOpenSuccess++
		cb.halfOpenInflight.Add(-1)
		if cb.halfOpenSuccess >= cb.successThreshold {
			cb.state = CircuitBreakerStateClosed
			cb.failureCount = 0
			cb.halfOpenSuccess = 0
		}
	}

	cb.failureCount = 0
}

// cleanupHalfOpenInflight cleans up leaked inflight counters.
// This should be called periodically to prevent counter leaks.
func (cb *CircuitBreaker) cleanupHalfOpenInflight() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	// If we're in half-open state and have inflight operations that haven't been
	// properly accounted for, reset the counter to prevent leaks
	if cb.state == CircuitBreakerStateHalfOpen {
		current := cb.halfOpenInflight.Load()
		if current > 0 {
			slog.Warn("Detected halfOpenInflight leak, resetting counter",
				"current_count", current)
			cb.halfOpenInflight.Store(0)
		}
	}
}

// RecordFailure records a failed operation.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.state == CircuitBreakerStateHalfOpen {
		cb.halfOpenInflight.Add(-1)
		cb.state = CircuitBreakerStateOpen
		cb.lastFailureTime = time.Now()
		return
	}

	cb.failureCount++
	cb.lastFailureTime = time.Now()

	if cb.failureCount >= cb.failureThreshold {
		cb.state = CircuitBreakerStateOpen
	}
}

// State returns the current circuit breaker state.
// Returns current state.
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// cleanupLoop runs periodic cleanup to prevent halfOpenInflight leaks.
// This should be started as a goroutine in NewCircuitBreaker.
func (cb *CircuitBreaker) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute) // Check every 5 minutes
	defer ticker.Stop()

	for {
		select {
		case <-cb.stopCh:
			return
		case <-ticker.C:
			cb.cleanupHalfOpenInflight()
		}
	}
}

// Close stops the cleanup goroutine and closes the circuit breaker.
func (cb *CircuitBreaker) Close() {
	if cb.cleanupStopped.CompareAndSwap(false, true) {
		close(cb.stopCh)
	}
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitBreakerStateClosed
	cb.failureCount = 0
	cb.lastFailureTime = time.Time{}
	cb.halfOpenSuccess = 0
	cb.halfOpenInflight.Store(0)
}
