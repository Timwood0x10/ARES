package ares_shutdown

import (
	"context"
	"sync"
	"time"
)

// CallbackRegistry manages shutdown callbacks.
type CallbackRegistry struct {
	callbacks map[Phase][]RegisteredCallback
	mu        sync.RWMutex
}

// RegisteredCallback represents a registered callback.
type RegisteredCallback struct {
	ID       string
	Priority int
	Fn       Callback
	Timeout  time.Duration
	OnError  func(error)
}

// NewCallbackRegistry creates a new CallbackRegistry instance.
// Returns:
// *CallbackRegistry - a new CallbackRegistry instance.
func NewCallbackRegistry() *CallbackRegistry {
	return &CallbackRegistry{
		callbacks: make(map[Phase][]RegisteredCallback),
	}
}

// Register registers a callback for a shutdown phase with priority sorting.
// Args:
// phase - the shutdown phase to register the callback for.
// id - unique identifier for the callback.
// priority - callback priority (higher values execute first).
// fn - the callback function to execute.
// timeout - maximum duration for the callback execution.
// Returns:
// error - error if registration fails.
func (r *CallbackRegistry) Register(phase Phase, id string, priority int, fn Callback, timeout time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	callback := RegisteredCallback{
		ID:       id,
		Priority: priority,
		Fn:       fn,
		Timeout:  timeout,
	}

	r.callbacks[phase] = append(r.callbacks[phase], callback)

	// Sort by priority (higher priority first)
	for i := 0; i < len(r.callbacks[phase])-1; i++ {
		for j := i + 1; j < len(r.callbacks[phase]); j++ {
			if r.callbacks[phase][j].Priority > r.callbacks[phase][i].Priority {
				r.callbacks[phase][i], r.callbacks[phase][j] = r.callbacks[phase][j], r.callbacks[phase][i]
			}
		}
	}

	return nil
}

// Unregister removes a callback by its unique identifier.
// Args:
// phase - the shutdown phase to remove the callback from.
// id - the unique identifier of the callback to remove.
// Returns:
// error - ErrCallbackNotFound if callback does not exist.
func (r *CallbackRegistry) Unregister(phase Phase, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	callbacks, exists := r.callbacks[phase]
	if !exists {
		return ErrCallbackNotFound
	}

	for i, cb := range callbacks {
		if cb.ID == id {
			r.callbacks[phase] = append(callbacks[:i], callbacks[i+1:]...)
			return nil
		}
	}

	return ErrCallbackNotFound
}

// GetCallbacks returns all callbacks for a phase, sorted by priority.
// Args:
// phase - the shutdown phase to get callbacks for.
// Returns:
// []Callback - slice of callback functions, sorted by priority (higher first).
func (r *CallbackRegistry) GetCallbacks(phase Phase) []Callback {
	r.mu.RLock()
	defer r.mu.RUnlock()

	callbacks, exists := r.callbacks[phase]
	if !exists {
		return nil
	}

	result := make([]Callback, 0, len(callbacks))
	for _, cb := range callbacks {
		result = append(result, cb.Fn)
	}

	return result
}

// Clear removes all callbacks for a specific shutdown phase.
// Args:
// phase - the shutdown phase to clear callbacks from.
func (r *CallbackRegistry) Clear(phase Phase) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.callbacks, phase)
}

// Count returns the number of registered callbacks for a phase.
// Args:
// phase - the shutdown phase to count callbacks for.
// Returns:
// int - the number of callbacks registered for the phase.
func (r *CallbackRegistry) Count(phase Phase) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.callbacks[phase])
}

// SetOnError sets an error handler for a specific callback.
// Args:
// phase - the shutdown phase containing the callback.
// id - the unique identifier of the callback.
// onError - the error handler function to call on error.
// Returns:
// error - ErrCallbackNotFound if callback does not exist.
func (r *CallbackRegistry) SetOnError(phase Phase, id string, onError func(error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	callbacks, exists := r.callbacks[phase]
	if !exists {
		return ErrCallbackNotFound
	}

	for i, cb := range callbacks {
		if cb.ID == id {
			callbacks[i].OnError = onError
			return nil
		}
	}

	return ErrCallbackNotFound
}

// CallbackRegistry errors.
var (
	ErrCallbackNotFound = &CallbackError{"callback not found"}
)

// CallbackError represents a callback error.
type CallbackError struct {
	msg string
}

func (e *CallbackError) Error() string {
	return e.msg
}

// CallbackChain allows chaining multiple callbacks.
type CallbackChain struct {
	callbacks []Callback
}

// NewCallbackChain creates a new CallbackChain instance.
// Returns:
// *CallbackChain - a new CallbackChain instance.
func NewCallbackChain() *CallbackChain {
	return &CallbackChain{
		callbacks: make([]Callback, 0),
	}
}

// Add adds a callback to the chain.
// Args:
// fn - the callback function to add to the chain.
// Returns:
// *CallbackChain - the CallbackChain for method chaining.
func (c *CallbackChain) Add(fn Callback) *CallbackChain {
	c.callbacks = append(c.callbacks, fn)
	return c
}

// Execute executes all callbacks in the chain sequentially.
// Args:
// ctx - context for cancellation and timeout control.
// Returns:
// error - error if any callback fails.
func (c *CallbackChain) Execute(ctx context.Context) error {
	for _, fn := range c.callbacks {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ExecuteParallel executes all callbacks in the chain concurrently.
// Args:
// ctx - context for cancellation and timeout control.
// Returns:
// error - error if any callback fails or context is cancelled.
func (c *CallbackChain) ExecuteParallel(ctx context.Context) error {
	if len(c.callbacks) == 0 {
		return nil
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(c.callbacks))
	done := make(chan struct{})

	wg.Add(len(c.callbacks))

	for _, fn := range c.callbacks {
		go func(callback Callback) {
			defer wg.Done()

			if err := callback(ctx); err != nil {
				select {
				case errChan <- err:
				case <-ctx.Done():
				}
			}
		}(fn)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		close(errChan)
		for err := range errChan {
			if err != nil {
				return err
			}
		}
		return nil
	case <-ctx.Done():
		<-done
		close(errChan)
		return ctx.Err()
	}
}
