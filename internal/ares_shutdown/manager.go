package ares_shutdown

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
)

// Phase represents a shutdown phase.
type Phase int

const (
	PhasePreShutdown Phase = iota
	PhaseGraceful
	PhaseForce
	PhaseDone
)

// Phase names for logging.
var phaseNames = map[Phase]string{
	PhasePreShutdown: "pre-shutdown",
	PhaseGraceful:    "graceful",
	PhaseForce:       "force",
	PhaseDone:        "done",
}

// String returns the phase name.
func (p Phase) String() string {
	name, ok := phaseNames[p]
	if !ok {
		return "unknown"
	}
	return name
}

// IsValid checks if the phase is valid.
func (p Phase) IsValid() bool {
	_, ok := phaseNames[p]
	return ok
}

// Manager coordinates the shutdown process across multiple components.
type Manager struct {
	phases       map[Phase]*PhaseHandler
	currentPhase Phase
	mu           sync.RWMutex
	timeout      time.Duration
	wg           sync.WaitGroup
}

// PhaseHandler handles a specific shutdown phase.
type PhaseHandler struct {
	phase     Phase
	callbacks []Callback
	timeout   time.Duration
	onTimeout func()
	onPanic   func(interface{})
}

// Callback is a function called during shutdown.
type Callback func(ctx context.Context) error

// NewManager creates a new ShutdownManager with the specified timeout.
// Args:
// timeout - maximum duration for the entire shutdown process.
// Returns:
// *Manager - a new ShutdownManager instance.
func NewManager(timeout time.Duration) *Manager {
	return &Manager{
		phases:  make(map[Phase]*PhaseHandler),
		timeout: timeout,
	}
}

// RegisterPhase registers a handler for a shutdown phase.
// Args:
// phase - the shutdown phase to register (PhasePreShutdown, PhaseGraceful, PhaseForce, PhaseDone).
// timeout - maximum duration for this phase.
func (m *Manager) RegisterPhase(phase Phase, timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.phases[phase] = &PhaseHandler{
		phase:   phase,
		timeout: timeout,
	}
}

// AddCallback adds a callback function to a shutdown phase.
// Args:
// phase - the shutdown phase to add the callback to.
// callback - the function to call during shutdown.
// Returns:
// error - error if phase is not registered.
func (m *Manager) AddCallback(phase Phase, callback Callback) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	handler, exists := m.phases[phase]
	if !exists {
		return fmt.Errorf("phase %s not registered", phase)
	}

	handler.callbacks = append(handler.callbacks, callback)
	return nil
}

// StartShutdown initiates the shutdown process, executing all registered phases in order.
// Args:
// ctx - context for cancellation and timeout control.
// Returns:
// error - error if shutdown fails or is already in progress.
func (m *Manager) StartShutdown(ctx context.Context) error {
	m.mu.Lock()
	if m.currentPhase != 0 {
		m.mu.Unlock()
		return fmt.Errorf("shutdown already in progress")
	}
	m.mu.Unlock()

	// Execute phases in order
	phases := []Phase{PhasePreShutdown, PhaseGraceful, PhaseForce, PhaseDone}

	for _, phase := range phases {
		m.mu.Lock()
		m.currentPhase = phase
		m.mu.Unlock()

		if err := m.executePhase(ctx, phase); err != nil {
			return errors.Wrapf(err, "phase %s failed", phase)
		}
	}

	return nil
}

// executePhase executes all callbacks for a phase.
func (m *Manager) executePhase(ctx context.Context, phase Phase) error {
	m.mu.RLock()
	handler, exists := m.phases[phase]
	m.mu.RUnlock()

	if !exists {
		return nil
	}

	if len(handler.callbacks) == 0 {
		return nil
	}

	phaseCtx, cancel := context.WithTimeout(ctx, handler.timeout)
	defer cancel()

	errChan := make(chan error, len(handler.callbacks))
	panicChan := make(chan interface{}, len(handler.callbacks))

	for _, callback := range handler.callbacks {
		m.wg.Add(1)
		go func(cb Callback) {
			defer m.wg.Done()

			defer func() {
				if r := recover(); r != nil {
					if handler.onPanic != nil {
						handler.onPanic(r)
					}
					select {
					case panicChan <- r:
					case <-phaseCtx.Done():
					}
				}
			}()

			if err := cb(phaseCtx); err != nil {
				select {
				case errChan <- err:
				case <-phaseCtx.Done():
				}
			}
		}(callback)
	}

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(errChan)
		close(panicChan)

		panicCount := 0
		for panicInfo := range panicChan {
			panicCount++
			log.Error("Shutdown panic recovered",
				"phase", phase,
				"panic", panicInfo)
		}

		var errs []error
		for err := range errChan {
			if err != nil {
				errs = append(errs, err)
			}
		}

		if panicCount > 0 {
			return fmt.Errorf("%d callback(s) panicked during shutdown phase %s", panicCount, phase)
		}

		if len(errs) > 0 {
			return fmt.Errorf("%d callback(s) failed during shutdown phase %s: %v", len(errs), phase, errs)
		}

		return nil
	case <-phaseCtx.Done():
		if handler.onTimeout != nil {
			handler.onTimeout()
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			log.Warn("Timeout waiting for callbacks to complete during shutdown",
				"phase", phase)
		}
		close(errChan)
		close(panicChan)
		return phaseCtx.Err()
	}
}

// SetOnTimeout sets the callback function to invoke when a phase times out.
// Args:
// phase - the shutdown phase to set the timeout callback for.
// fn - the function to call on timeout.
func (m *Manager) SetOnTimeout(phase Phase, fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if handler, exists := m.phases[phase]; exists {
		handler.onTimeout = fn
	}
}

// SetOnPanic sets the callback function to invoke when a panic occurs during phase execution.
// Args:
// phase - the shutdown phase to set the panic callback for.
// fn - the function to call on panic, receives the panic value.
func (m *Manager) SetOnPanic(phase Phase, fn func(interface{})) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if handler, exists := m.phases[phase]; exists {
		handler.onPanic = fn
	}
}

// CurrentPhase returns the current shutdown phase.
// Returns:
// Phase - the current shutdown phase (0 if shutdown has not started).
func (m *Manager) CurrentPhase() Phase {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.currentPhase
}

// Wait blocks until all in-progress shutdown operations complete.
func (m *Manager) Wait() {
	m.wg.Wait()
}

// IsShutdown returns true if shutdown has started (past PhasePreShutdown phase).
// Returns:
// bool - true if shutdown has started, false otherwise.
func (m *Manager) IsShutdown() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Shutdown has started if we're past PhasePreShutdown
	return m.currentPhase > PhasePreShutdown
}
