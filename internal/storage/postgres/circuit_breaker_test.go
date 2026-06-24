package postgres

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/errors"
)

func TestNewCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	defer cb.Close()

	if cb.State() != CircuitBreakerStateClosed {
		t.Errorf("expected closed state, got %s", cb.State())
	}
}

func TestCircuitBreaker_AllowRequest_Closed(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	defer cb.Close()

	if err := cb.AllowRequest(); err != nil {
		t.Errorf("closed circuit should allow requests, got %v", err)
	}
}

func TestCircuitBreaker_RecordFailure_Opens(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	defer cb.Close()

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitBreakerStateOpen {
		t.Errorf("expected open state after 3 failures, got %s", cb.State())
	}

	if err := cb.AllowRequest(); err != errors.ErrCircuitBreakerOpen {
		t.Errorf("open circuit should reject requests, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpen_AllowsSingleRequest(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Millisecond)
	defer cb.Close()

	// Open the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for open timeout to transition to half-open.
	time.Sleep(60 * time.Millisecond)

	// First request in half-open should be allowed.
	if err := cb.AllowRequest(); err != nil {
		t.Errorf("half-open should allow first request, got %v", err)
	}
}

func TestCircuitBreaker_HalfOpen_BlocksConcurrentRequests(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Millisecond)
	defer cb.Close()

	// Open the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for open timeout to transition to half-open.
	time.Sleep(60 * time.Millisecond)

	// Use many goroutines to try to get through half-open simultaneously.
	var allowed int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cb.AllowRequest(); err == nil {
				atomic.AddInt32(&allowed, 1)
			}
		}()
	}
	wg.Wait()

	// Only one goroutine should have been allowed through half-open.
	if allowed != 1 {
		t.Errorf("expected exactly 1 request allowed in half-open, got %d", allowed)
	}

	// After half-open allows one, record enough successes to close.
	// successThreshold is 3, so we need 3 successful calls.
	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordSuccess()
	if cb.State() != CircuitBreakerStateClosed {
		t.Errorf("expected closed after %d successes in half-open, got %s", cb.successThreshold, cb.State())
	}
}

func TestCircuitBreaker_HalfOpen_FailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Millisecond)
	defer cb.Close()

	// Open the circuit.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for open timeout.
	time.Sleep(60 * time.Millisecond)

	// Allow one request through half-open.
	if err := cb.AllowRequest(); err != nil {
		t.Fatalf("half-open should allow request, got %v", err)
	}

	// Record failure should re-open.
	cb.RecordFailure()
	if cb.State() != CircuitBreakerStateOpen {
		t.Errorf("expected open after failure in half-open, got %s", cb.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	defer cb.Close()

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitBreakerStateOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}

	cb.Reset()
	if cb.State() != CircuitBreakerStateClosed {
		t.Errorf("expected closed after reset, got %s", cb.State())
	}

	if err := cb.AllowRequest(); err != nil {
		t.Errorf("should allow after reset, got %v", err)
	}
}

func TestCircuitBreaker_Close_StopsCleanup(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	cb.Close()

	// Double close should be safe (uses CompareAndSwap).
	cb.Close()
}

func TestCircuitBreaker_RecordSuccess_ResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(3, 5*time.Second)
	defer cb.Close()

	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess()

	// Should need 3 more failures to open.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitBreakerStateClosed {
		t.Errorf("expected still closed after 2 failures (success reset count), got %s", cb.State())
	}
}
