package shutdown

import (
	"context"
	"sync"
	"time"
)

// PhaseState represents the state of a phase.
type PhaseState int

const (
	PhaseStatePending PhaseState = iota
	PhaseStateRunning
	PhaseStateCompleted
	PhaseStateFailed
	PhaseStateSkipped
)

// PhaseState names.
var phaseStateNames = map[PhaseState]string{
	PhaseStatePending:   "pending",
	PhaseStateRunning:   "running",
	PhaseStateCompleted: "completed",
	PhaseStateFailed:    "failed",
	PhaseStateSkipped:   "skipped",
}

// String returns the state name.
func (s PhaseState) String() string {
	name, ok := phaseStateNames[s]
	if !ok {
		return "unknown"
	}
	return name
}

// PhaseExecutor executes a phase with retries and rollback support.
type PhaseExecutor struct {
	phase      Phase
	state      PhaseState
	startTime  time.Time
	endTime    time.Time
	error      error
	retries    int
	maxRetries int
	rollbackFn func() error
	onComplete func() error
	onFailure  func(error) error
	mu         sync.RWMutex
}

// NewPhaseExecutor creates a new PhaseExecutor.
func NewPhaseExecutor(phase Phase, maxRetries int) *PhaseExecutor {
	return &PhaseExecutor{
		phase:      phase,
		state:      PhaseStatePending,
		maxRetries: maxRetries,
	}
}

// Execute executes the phase.
func (e *PhaseExecutor) Execute(ctx context.Context, fn func(ctx context.Context) error) error {
	e.mu.Lock()
	if e.state == PhaseStateRunning {
		e.mu.Unlock()
		return ErrPhaseAlreadyRunning
	}
	e.state = PhaseStateRunning
	e.startTime = time.Now()
	e.mu.Unlock()

	var lastErr error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		e.retries = attempt

		select {
		case <-ctx.Done():
			e.setState(PhaseStateFailed)
			return ctx.Err()
		default:
		}

		if err := fn(ctx); err != nil {
			lastErr = err

			e.mu.RLock()
			if e.onFailure != nil {
				rollbackErr := e.onFailure(err)
				if rollbackErr != nil {
					lastErr = rollbackErr
				}
			}
			e.mu.RUnlock()

			if attempt < e.maxRetries {
				// Exponential backoff with overflow protection
				var backoff time.Duration
				if attempt > 30 { // Prevent overflow
					backoff = time.Duration(1<<30) * time.Second
				} else {
					backoff = time.Duration(1<<uint(attempt)) * time.Second
				}
				timer := time.NewTimer(backoff)
				select {
				case <-ctx.Done():
					timer.Stop()
					e.setState(PhaseStateFailed)
					return ctx.Err()
				case <-timer.C:
				}
				continue
			}

			e.setState(PhaseStateFailed)
			e.error = lastErr
			return lastErr
		}

		// Success
		break
	}

	e.setState(PhaseStateCompleted)
	e.endTime = time.Now()

	e.mu.RLock()
	if e.onComplete != nil {
		return e.onComplete()
	}
	e.mu.RUnlock()

	return nil
}

// Rollback performs rollback if available.
func (e *PhaseExecutor) Rollback() error {
	e.mu.RLock()
	rollbackFn := e.rollbackFn
	e.mu.RUnlock()

	if rollbackFn == nil {
		return nil
	}

	return rollbackFn()
}

// State returns the current state.
func (e *PhaseExecutor) State() PhaseState {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.state
}

// Phase returns the phase.
func (e *PhaseExecutor) Phase() Phase {
	return e.phase
}

// Duration returns the execution duration.
func (e *PhaseExecutor) Duration() time.Duration {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.endTime.IsZero() {
		return time.Since(e.startTime)
	}
	return e.endTime.Sub(e.startTime)
}

// Error returns the error if any.
func (e *PhaseExecutor) Error() error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.error
}

// Retries returns the number of retries attempted.
func (e *PhaseExecutor) Retries() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return e.retries
}

// SetRollbackFn sets the rollback function.
func (e *PhaseExecutor) SetRollbackFn(fn func() error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.rollbackFn = fn
}

// SetOnComplete sets the completion callback.
func (e *PhaseExecutor) SetOnComplete(fn func() error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.onComplete = fn
}

// SetOnFailure sets the failure callback.
func (e *PhaseExecutor) SetOnFailure(fn func(error) error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.onFailure = fn
}

func (e *PhaseExecutor) setState(state PhaseState) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.state = state
}

// PhaseExecutor errors.
var (
	ErrPhaseAlreadyRunning = &PhaseError{"phase already running"}
)

// PhaseError represents a phase execution error.
type PhaseError struct {
	msg string
}

func (e *PhaseError) Error() string {
	return e.msg
}
