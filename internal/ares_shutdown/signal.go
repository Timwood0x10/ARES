package ares_shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// SignalHandler handles system signals for graceful shutdown.
type SignalHandler struct {
	signals []os.Signal
	ctx     context.Context
	cancel  context.CancelFunc
	manager *Manager
	sigChan chan os.Signal // Store the channel for stopping
	mu      struct {
		sync.RWMutex
		started bool
	}
}

// NewSignalHandler creates a new SignalHandler.
func NewSignalHandler(manager *Manager) *SignalHandler {
	return &SignalHandler{
		signals: []os.Signal{
			os.Interrupt,
			syscall.SIGTERM,
			syscall.SIGINT,
		},
		manager: manager,
	}
}

// Start starts listening for signals.
func (h *SignalHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.mu.started {
		h.mu.Unlock()
		return ErrSignalHandlerAlreadyStarted
	}
	h.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	h.ctx = ctx
	h.cancel = cancel

	h.sigChan = make(chan os.Signal, len(h.signals))
	signal.Notify(h.sigChan, h.signals...)

	go h.handleSignals(h.sigChan)

	h.mu.Lock()
	h.mu.started = true
	h.mu.Unlock()

	return nil
}

// Stop stops listening for signals.
func (h *SignalHandler) Stop() error {
	h.mu.RLock()
	if !h.mu.started {
		h.mu.RUnlock()
		return nil
	}
	h.mu.RUnlock()

	if h.cancel != nil {
		h.cancel()
	}

	// Stop the actual channel that was registered
	if h.sigChan != nil {
		signal.Stop(h.sigChan)
	}

	h.mu.Lock()
	h.mu.started = false
	h.mu.Unlock()

	return nil
}

// handleSignals handles incoming signals.
func (h *SignalHandler) handleSignals(sigChan <-chan os.Signal) {
	defer func() {
		h.mu.Lock()
		h.mu.started = false
		h.mu.Unlock()
	}()

	for {
		select {
		case <-h.ctx.Done():
			return
		case sig := <-sigChan:
			h.handleSignal(sig)
		}
	}
}

// handleSignal handles a single signal.
func (h *SignalHandler) handleSignal(sig os.Signal) {
	switch sig {
	case os.Interrupt, syscall.SIGINT, syscall.SIGTERM:
		// Trigger graceful shutdown
		if h.manager != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), h.manager.timeout)
			defer cancel()

			if err := h.manager.StartShutdown(shutdownCtx); err != nil {
				// Log error but continue with shutdown
				log.Warn("Shutdown initiated with error", "error", err)
			}
		}
	}
}

// AddSignal adds a signal to listen for. If the handler is already started,
// the new signal is registered with signal.Notify immediately.
func (h *SignalHandler) AddSignal(sig os.Signal) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.signals = append(h.signals, sig)
	// If already started, register the new signal so it is delivered to sigChan.
	if h.mu.started && h.sigChan != nil {
		signal.Notify(h.sigChan, sig)
	}
}

// SetContext sets the context for signal handling.
func (h *SignalHandler) SetContext(ctx context.Context) {
	h.ctx = ctx
}

// SignalHandler errors.
var (
	ErrSignalHandlerAlreadyStarted = &SignalError{"signal handler already started"}
)

// SignalError represents a signal handler error.
type SignalError struct {
	msg string
}

func (e *SignalError) Error() string {
	return e.msg
}

// WaitForSignal blocks until a signal is received.
func WaitForSignal(signals ...os.Signal) os.Signal {
	if len(signals) == 0 {
		signals = []os.Signal{os.Interrupt}
	}

	sigChan := make(chan os.Signal, len(signals))
	signal.Notify(sigChan, signals...)
	defer signal.Stop(sigChan)

	return <-sigChan
}

// WaitForContextOrSignal blocks until context is cancelled or signal received.
func WaitForContextOrSignal(ctx context.Context, signals ...os.Signal) (os.Signal, error) {
	if len(signals) == 0 {
		signals = []os.Signal{os.Interrupt}
	}

	sigChan := make(chan os.Signal, len(signals))
	signal.Notify(sigChan, signals...)
	defer signal.Stop(sigChan)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case sig := <-sigChan:
		return sig, nil
	}
}
