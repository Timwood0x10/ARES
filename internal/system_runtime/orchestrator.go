// Package system_runtime — lifecycle orchestrator.
package system_runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/logger"
)

var log = logger.Module("system_runtime")

// stopTimeout bounds a single component's Stop/Wait during shutdown.
const stopTimeout = 30 * time.Second

// waitTimeout bounds the errgroup drain during Shutdown. Components that
// ignore the cancelled root context are reported instead of blocking forever.
const waitTimeout = 30 * time.Second

// Orchestrator drives the component lifecycle: Construct → Bind → Start → Ready
// on startup, and Stop → Wait → Close on shutdown, in topological order.
type Orchestrator struct {
	registry *Registry
	rootCtx  context.Context
	cancel   context.CancelFunc
	eg       *errgroup.Group

	mu      sync.Mutex
	started bool
	stopped bool
}

// NewOrchestrator creates a new orchestrator with the given registry.
// The root context is derived from the provided context and will be
// used as the parent for all component lifecycle operations.
func NewOrchestrator(reg *Registry, rootCtx context.Context) *Orchestrator {
	ctx, cancel := context.WithCancel(rootCtx)
	eg, egCtx := errgroup.WithContext(ctx)
	// The errgroup-derived context is the managed root context: it is
	// cancelled both by Cancel() and whenever any managed goroutine fails,
	// so components that select on RootContext().Done() are signalled in
	// both paths.
	return &Orchestrator{
		registry: reg,
		rootCtx:  egCtx,
		cancel:   cancel,
		eg:       eg,
	}
}

// Start executes the full startup sequence for all registered components
// in topological order: Construct (already done by registration) →
// Bind → Start → Ready. On failure, the failing component is cleaned up
// if it had started, then all previously started components are stopped
// in reverse order (rollback). Start is not idempotent: calling it twice
// returns an error.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.started || o.stopped {
		o.mu.Unlock()
		return errors.New("system_runtime: Start called after startup already began or completed")
	}
	o.started = true
	o.mu.Unlock()

	order, err := o.registry.TopologicalOrder()
	if err != nil {
		return fmt.Errorf("system_runtime: startup: %w", err)
	}

	var started []string

	for _, name := range order {
		if err := o.startComponent(ctx, name); err != nil {
			// The failing component may have partially started (Start()
			// succeeded, Ready() failed, or Start() returned error after
			// spawning goroutines). Clean it up before rolling back the
			// previously started components so nothing leaks.
			if status, ok := o.registry.GetStatus(name); ok && status.State == StateFailed {
				o.cleanupComponent(ctx, name)
			}
			o.rollback(ctx, started)
			return fmt.Errorf("system_runtime: startup failed at %q: %w", name, err)
		}
		started = append(started, name)
	}

	log.Info("system_runtime: all components started",
		"count", len(started))
	return nil
}

// startComponent executes Bind → Start → Ready for one component.
// Skips Disabled components. Updates the registry status on each transition.
func (o *Orchestrator) startComponent(ctx context.Context, name string) error {
	comp := o.registry.GetComponent(name)
	if comp == nil {
		return fmt.Errorf("component %q not found", name)
	}

	mode, _ := o.registry.GetMode(name)

	// Check if component is disabled (by config gate in Stage 2).
	status, _ := o.registry.GetStatus(name)
	if status.State == StateDisabled {
		log.Info("system_runtime: skipping disabled component",
			"component", name)
		return nil
	}

	// Bind phase.
	if binder, ok := comp.(Binder); ok {
		if err := binder.Bind(ctx, o.registry); err != nil {
			o.setStatus(name, StateFailed, err.Error())
			return fmt.Errorf("bind: %w", err)
		}
	}
	o.setStatus(name, StateBound, "")

	// Start phase.
	if starter, ok := comp.(Starter); ok {
		if err := starter.Start(ctx); err != nil {
			o.setStatus(name, StateFailed, err.Error())
			return fmt.Errorf("start: %w", err)
		}
	}
	o.setStatusStarted(name, StateStarted)

	// Ready phase.
	if checker, ok := comp.(ReadinessChecker); ok {
		if err := checker.Ready(ctx); err != nil {
			// For Degraded mode, a Ready failure enters Degraded state.
			if mode == ModeDegraded {
				o.setStatus(name, StateDegraded, err.Error())
				log.Warn("system_runtime: component degraded",
					"component", name, "reason", err)
			} else {
				o.setStatus(name, StateFailed, err.Error())
				return fmt.Errorf("ready: %w", err)
			}
		}
	}

	// If we get here without entering Degraded, mark as Ready.
	currentStatus, _ := o.registry.GetStatus(name)
	if currentStatus.State != StateDegraded {
		o.setStatus(name, StateReady, "")
	}

	log.Info("system_runtime: component ready",
		"component", name, "state", currentStatus.State)
	return nil
}

// Shutdown executes the full shutdown sequence: it cancels the managed root
// context first (so goroutines waiting on RootContext().Done() exit), then
// stops all started components in reverse topological order, then drains the
// errgroup with a bounded timeout. Stop/Wait/errgroup errors are aggregated
// and returned. Shutdown is idempotent and safe to call concurrently: only
// the first call runs the sequence.
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.mu.Lock()
	if o.stopped {
		o.mu.Unlock()
		return nil
	}
	o.stopped = true
	o.mu.Unlock()

	// Signal all managed goroutines first so they stop accepting work
	// before we Stop the components that feed them.
	o.cancel()

	var errs []error

	order, err := o.registry.TopologicalOrder()
	if err != nil {
		// On error, just stop in registration order.
		order = o.registry.Names()
	}

	// Reverse order for shutdown.
	for i := len(order) - 1; i >= 0; i-- {
		name := order[i]
		if err := o.stopComponent(ctx, name); err != nil {
			errs = append(errs, fmt.Errorf("system_runtime: stop %q: %w", name, err))
		}
	}

	// Wait for all errgroup goroutines, bounded so a misbehaving component
	// cannot hang shutdown forever.
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- o.eg.Wait()
	}()
	select {
	case waitErr := <-waitCh:
		if waitErr != nil {
			errs = append(errs, fmt.Errorf("system_runtime: errgroup wait: %w", waitErr))
		}
	case <-time.After(waitTimeout):
		errs = append(errs, fmt.Errorf("system_runtime: errgroup wait timed out after %s", waitTimeout))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	log.Info("system_runtime: shutdown complete")
	return nil
}

// stopComponent executes Stop → Wait for one component.
func (o *Orchestrator) stopComponent(ctx context.Context, name string) error {
	comp := o.registry.GetComponent(name)
	if comp == nil {
		return nil
	}

	status, _ := o.registry.GetStatus(name)
	if status.State == StateDisabled || status.State == StateStopped {
		return nil
	}

	o.setStatus(name, StateStopping, "")

	if stopper, ok := comp.(Stopper); ok {
		stopCtx, stopCancel := context.WithTimeout(ctx, stopTimeout)
		defer stopCancel()
		if err := stopper.Stop(stopCtx); err != nil {
			o.setStatus(name, StateFailed, err.Error())
			return err
		}
	}

	if waiter, ok := comp.(Waiter); ok {
		// Wait() has no context, so bound it both by stopTimeout and by the
		// caller's context. A shorter Shutdown deadline must not be held up
		// by a component that ignores cancellation.
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- waiter.Wait()
		}()
		select {
		case waitErr := <-waitCh:
			if waitErr != nil {
				log.Warn("system_runtime: wait error",
					"component", name, "error", waitErr)
			}
		case <-time.After(stopTimeout):
			log.Warn("system_runtime: wait timed out (goroutine leaked)",
				"component", name, "timeout", stopTimeout)
		case <-ctx.Done():
			log.Warn("system_runtime: wait aborted by shutdown context (goroutine leaked)",
				"component", name)
		}
	}

	o.setStatus(name, StateStopped, "")
	return nil
}

// cleanupComponent best-effort Stops and Waits a component that failed
// startup after it had already started. Unlike stopComponent it does not
// touch the registry status, so the Failed state remains observable.
func (o *Orchestrator) cleanupComponent(ctx context.Context, name string) {
	comp := o.registry.GetComponent(name)
	if comp == nil {
		return
	}
	if stopper, ok := comp.(Stopper); ok {
		stopCtx, stopCancel := context.WithTimeout(ctx, stopTimeout)
		defer stopCancel()
		if err := stopper.Stop(stopCtx); err != nil {
			log.Warn("system_runtime: cleanup stop error",
				"component", name, "error", err)
		}
	}
	if waiter, ok := comp.(Waiter); ok {
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- waiter.Wait()
		}()
		select {
		case waitErr := <-waitCh:
			if waitErr != nil {
				log.Warn("system_runtime: cleanup wait error",
					"component", name, "error", waitErr)
			}
		case <-time.After(stopTimeout):
			log.Warn("system_runtime: cleanup wait timed out (goroutine leaked)",
				"component", name, "timeout", stopTimeout)
		case <-ctx.Done():
			log.Warn("system_runtime: cleanup wait aborted by context (goroutine leaked)",
				"component", name)
		}
	}
}

// rollback stops already-started components in reverse order after a
// startup failure. This ensures no goroutine or resource leaks.
func (o *Orchestrator) rollback(ctx context.Context, started []string) {
	log.Warn("system_runtime: rolling back startup",
		"started_count", len(started))
	for i := len(started) - 1; i >= 0; i-- {
		name := started[i]
		if err := o.stopComponent(ctx, name); err != nil {
			log.Warn("system_runtime: rollback stop error",
				"component", name, "error", err)
		}
	}
}

// Go submits a background goroutine to the orchestrator's errgroup.
// Use this instead of bare `go` for all managed background work.
func (o *Orchestrator) Go(fn func() error) {
	o.eg.Go(fn)
}

// RootContext returns the managed root context. Components should use
// this context (or a derived one) for goroutines that need to respect
// the orchestrator's lifecycle. It is cancelled by Cancel(), by Shutdown,
// and when any managed goroutine returns an error.
func (o *Orchestrator) RootContext() context.Context {
	return o.rootCtx
}

// Cancel cancels the root context, signalling all managed goroutines.
func (o *Orchestrator) Cancel() {
	o.cancel()
}

// setStatus updates the component status in the registry.
func (o *Orchestrator) setStatus(name string, state State, reason string) {
	status, ok := o.registry.GetStatus(name)
	if !ok {
		return
	}
	status.State = state
	status.Reason = reason
	o.registry.SetStatus(name, status)
}

// setStatusStarted updates status with a timestamp when a component starts.
func (o *Orchestrator) setStatusStarted(name string, state State) {
	status, ok := o.registry.GetStatus(name)
	if !ok {
		return
	}
	status.State = state
	status.StartedAt = time.Now()
	status.InstanceID = fmt.Sprintf("%s-%d", name, status.StartedAt.UnixNano())
	o.registry.SetStatus(name, status)
}
