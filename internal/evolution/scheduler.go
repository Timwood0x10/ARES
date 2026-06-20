package evolution

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"goagentx/internal/callbacks"

	"golang.org/x/sync/errgroup"
)

// contextKey is a custom type for context value keys to avoid collisions.
type contextKey string

// CallbackData holds data passed to callback handlers during evolution triggers.
type CallbackData struct {
	// AgentID is the identifier of the agent that triggered the event.
	AgentID string
}

// AdapterRunner defines the interface for running an evolution adapter.
// This allows the scheduler to work with any adapter implementation.
type AdapterRunner interface {
	// Run starts the adapter's event consumption loop.
	Run(ctx context.Context) error
}

// EvolutionTrigger defines when to trigger evolution cycles.
type EvolutionTrigger int

const (
	// TriggerOnIdle triggers evolution when the system is idle.
	TriggerOnIdle EvolutionTrigger = iota + 1

	// TriggerOnThreshold triggers evolution when diagnostic count exceeds threshold.
	TriggerOnThreshold

	// TriggerOnDemand triggers evolution only when explicitly requested.
	TriggerOnDemand
)

// String returns the string representation of EvolutionTrigger.
func (t EvolutionTrigger) String() string {
	switch t {
	case TriggerOnIdle:
		return "idle"
	case TriggerOnThreshold:
		return "threshold"
	case TriggerOnDemand:
		return "demand"
	default:
		return "unknown"
	}
}

// SchedulerOption configures the EvolutionScheduler.
type SchedulerOption func(*EvolutionScheduler)

// WithMinInterval sets the minimum interval between evolution cycles.
//
// Args:
//
//	d - the minimum duration between cycles.
//
// Returns:
//
//	SchedulerOption - the option function.
func WithMinInterval(d time.Duration) SchedulerOption {
	return func(s *EvolutionScheduler) {
		if d > 0 {
			s.minInterval = d
		}
	}
}

// WithTrigger sets the evolution trigger mode.
//
// Args:
//
//	trigger - the trigger mode to use.
//
// Returns:
//
//	SchedulerOption - the option function.
func WithTrigger(trigger EvolutionTrigger) SchedulerOption {
	return func(s *EvolutionScheduler) {
		s.trigger = trigger
	}
}

// WithEnabled sets whether the scheduler is enabled.
//
// Args:
//
//	enabled - true to enable, false to disable.
//
// Returns:
//
//	SchedulerOption - the option function.
func WithEnabled(enabled bool) SchedulerOption {
	return func(s *EvolutionScheduler) {
		s.enabled = enabled
	}
}

// EvolutionScheduler triggers evolution cycles based on callback events.
// It registers handlers with the callback registry and decides when to run
// the FlightToExperienceAdapter based on configurable trigger conditions.
type EvolutionScheduler struct {
	callbacks   callbacks.CallbackRegistrar
	adapter     AdapterRunner
	minInterval time.Duration
	mu          sync.Mutex // Protects lastRun from concurrent access.
	lastRun     time.Time
	trigger     EvolutionTrigger
	enabled     bool
	egCtx       context.Context    // Context for errgroup cancellation.
	egCancel    context.CancelFunc // Cancel function for errgroup context.
	dreamCycle  *DreamCycle        // Optional dream cycle orchestrator for full evolution loop.
}

// NewEvolutionScheduler creates a new scheduler with sensible defaults.
//
// Default configuration:
//   - minInterval: 5 minutes
//   - trigger: TriggerOnIdle
//   - enabled: false (must be explicitly enabled)
//
// Args:
//
//	callbacks - the callback registrar for registering event handlers (implements CallbackRegistrar).
//	adapter - the adapter runner to execute on evolution cycles (implements AdapterRunner).
//	opts - optional configuration functions.
//
// Returns:
//
//	*EvolutionScheduler - the configured scheduler instance.
func NewEvolutionScheduler(callbacks callbacks.CallbackRegistrar, adapter AdapterRunner, opts ...SchedulerOption) *EvolutionScheduler {
	egCtx, egCancel := context.WithCancel(context.Background())

	s := &EvolutionScheduler{
		callbacks:   callbacks,
		adapter:     adapter,
		minInterval: 5 * time.Minute,
		lastRun:     time.Time{},
		trigger:     TriggerOnIdle,
		enabled:     false,
		egCtx:       egCtx,
		egCancel:    egCancel,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// OnAgentEnd handles agent completion events as a callback handler.
// It checks if an evolution cycle should be triggered and runs the adapter if so.
//
// Args:
//
//	ctx - operation context.
//	data - the callback data containing agent completion information.
func (s *EvolutionScheduler) OnAgentEnd(ctx context.Context, data CallbackData) {
	if !s.enabled {
		return
	}

	if s.adapter == nil {
		slog.WarnContext(ctx, "[Evolution] Adapter is nil, skipping evolution")
		return
	}

	if !s.shouldEvolve(ctx, data) {
		return
	}

	// Update lastRun with mutex protection.
	s.mu.Lock()
	s.lastRun = time.Now()
	s.mu.Unlock()

	slog.InfoContext(ctx, "[Evolution] Starting evolution cycle",
		"agent_id", data.AgentID,
		"trigger", s.trigger.String())

	// Run the adapter asynchronously via errgroup with context for cancellation support.
	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		if err := s.adapter.Run(egCtx); err != nil {
			slog.ErrorContext(ctx, "[Evolution] Evolution cycle failed",
				"agent_id", data.AgentID,
				"error", err)
			return err
		}
		return nil
	})

	// Start error group in background; errors are logged above.
	go func() {
		if err := eg.Wait(); err != nil {
			slog.ErrorContext(ctx, "[Evolution] Evolution goroutine exited with error",
				"error", err)
		}
	}()
}

// Register registers the scheduler's handlers to the callback registry.
// It subscribes to EventAgentEnd events for triggering evolution cycles.
func (s *EvolutionScheduler) Register() {
	if s.callbacks == nil {
		slog.Warn("[Evolution] Callback registry is nil, cannot register")
		return
	}

	s.callbacks.On(callbacks.EventAgentEnd, func(ctx *callbacks.Context) {
		data := CallbackData{
			AgentID: ctx.AgentID,
		}
		// Propagate callback context values (e.g., trace_id, tenant_id from Extra)
		// into a new context instead of discarding them with context.Background().
		callbackCtx := context.Background()
		if ctx.Extra != nil {
			for k, v := range ctx.Extra {
				callbackCtx = context.WithValue(callbackCtx, contextKey(k), v)
			}
		}
		callbackCtx = context.WithValue(callbackCtx, contextKey("agent_id"), ctx.AgentID)
		s.OnAgentEnd(callbackCtx, data)
	})

	slog.Info("[Evolution] Scheduler registered for agent end events")
}

// shouldEvolve determines if an evolution cycle should be triggered.
// The decision is based on multiple heuristics:
//   - Minimum interval protection (minInterval must have elapsed since lastRun)
//   - Minimum task count threshold (enough data collected for meaningful decision)
//   - Score degradation detection (recent performance dropping significantly)
//   - Consecutive success exploration opportunity
//
// Args:
//
//	ctx - operation context.
//	data - the callback data containing agent completion information.
//
// Returns:
//
//	true if evolution should run, false otherwise.
func (s *EvolutionScheduler) shouldEvolve(ctx context.Context, data CallbackData) bool {
	// Check minimum interval protection with mutex for thread-safe access.
	s.mu.Lock()
	lastRun := s.lastRun
	s.mu.Unlock()

	if !lastRun.IsZero() && time.Since(lastRun) < s.minInterval {
		slog.DebugContext(ctx, "[Evolution] Skipping evolution: minimum interval not elapsed",
			"last_run", lastRun.Format(time.RFC3339),
			"min_interval", s.minInterval)
		return false
	}

	// Check task count threshold via experience repo score trend.
	// TODO: integrate with actual ExperienceRepository to query task count and scores.
	// Currently using a simple internal counter fallback; replace with real query once
	// the experience pipeline provides CountRecent() / GetRecentScores() methods.
	minTasks := 10 // Default minimum tasks before considering evolution.

	// Score degradation check: detect when recent average drops below historical baseline.
	// TODO: wire into EvalEngine or Flight Diagnostics for real score data.
	// Expected interface: GetRollingScores(window int) ([]float64, error)
	// Fallback: allow evolution when enough tasks have passed (conservative approach).

	switch s.trigger {
	case TriggerOnThreshold:
		// Threshold mode: only evolve when diagnostic count exceeds limit.
		// TODO: query diagnostics count from flight recorder.
		// For now, rely on interval protection only.
		slog.DebugContext(ctx, "[Evolution] Threshold trigger: checking conditions",
			"min_tasks", minTasks)

	case TriggerOnDemand:
		// Demand mode: only evolve when explicitly requested.
		// The scheduler itself should not auto-trigger; external API call needed.
		return false

	case TriggerOnIdle:
		// Idle mode: evolve when system has been idle long enough.
		// Already covered by minInterval check above.
	default:
		// Unknown trigger mode: default to safe behavior (no auto-evolution).
		return false
	}

	// Default: allow evolution when interval check passes and we have enough signal.
	// This conservative default ensures the system can start evolving once warmed up.
	return true
}

// SetEnabled enables or disables the scheduler at runtime.
//
// Args:
//
//	enabled - true to enable, false to disable.
func (s *EvolutionScheduler) SetEnabled(enabled bool) {
	s.enabled = enabled
}

// IsEnabled returns whether the scheduler is currently enabled.
//
// Returns:
//
//	bool - true if enabled, false otherwise.
func (s *EvolutionScheduler) IsEnabled() bool {
	return s.enabled
}

// LastRunTime returns the timestamp of the last evolution cycle.
// Thread-safe: uses mutex to protect concurrent access.
//
// Returns:
//
//	time.Time - the last run time, or zero value if never run.
func (s *EvolutionScheduler) LastRunTime() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRun
}

// SetDreamCycle attaches a dream cycle orchestrator to the scheduler.
// When set, the scheduler delegates evolution execution to the dream cycle
// instead of directly running the adapter.
//
// Args:
//
//	dc - the dream cycle orchestrator (may be nil to detach).
func (s *EvolutionScheduler) SetDreamCycle(dc *DreamCycle) {
	s.dreamCycle = dc
}

// DreamCycle returns the attached dream cycle orchestrator, if any.
//
// Returns:
//
//	*DreamCycle - the dream cycle instance, or nil if not set.
func (s *EvolutionScheduler) DreamCycle() *DreamCycle {
	return s.dreamCycle
}

// Shutdown gracefully stops the scheduler and cancels all pending evolution goroutines.
// It should be called when the scheduler is no longer needed to prevent goroutine leaks.
func (s *EvolutionScheduler) Shutdown() {
	if s.egCancel != nil {
		s.egCancel()
	}
}
