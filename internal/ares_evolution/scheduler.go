package evolution

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/callbacks"

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

// WithSchedulerGuardrails attaches guardrails to the scheduler for pre-evolution checks.
//
// Args:
//
//	guardrails - the evolution guardrails instance (may be nil to disable).
//
// Returns:
//
//	SchedulerOption - the option function.
func WithSchedulerGuardrails(guardrails *EvolutionGuardrails) SchedulerOption {
	return func(s *EvolutionScheduler) {
		s.guardrails = guardrails
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
		s.enabled.Store(enabled)
	}
}

// scoreWindowSize is the number of recent task scores to track for trend detection.
const scoreWindowSize = 50

// degradationThreshold is the fraction of score drop that triggers evolution (15%).
const degradationThreshold = 0.15

// minScoreCountForReliability is the minimum number of scores required before
// the trend data is considered reliable enough for evolution decisions.
const minScoreCountForReliability = 20

// periodicEvolutionScoreThreshold is the score count threshold that triggers
// periodic exploration evolution even without detected degradation.
const periodicEvolutionScoreThreshold = 100

// EvolutionScheduler triggers evolution cycles based on callback events.
// It registers handlers with the callback registry and decides when to run
// the adapter based on configurable trigger conditions.
type EvolutionScheduler struct {
	callbacks    callbacks.CallbackRegistrar
	adapter      AdapterRunner
	minInterval  time.Duration
	mu           sync.Mutex
	lastRun      time.Time
	trigger      EvolutionTrigger
	enabled      atomic.Bool
	evolveMu     sync.Mutex
	evolveCancel context.CancelFunc
	dreamCycle   *DreamCycle
	scores       []float64
	scoreMu      sync.Mutex
	guardrails   *EvolutionGuardrails
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
	s := &EvolutionScheduler{
		callbacks:   callbacks,
		adapter:     adapter,
		minInterval: 5 * time.Minute,
		lastRun:     time.Time{},
		trigger:     TriggerOnIdle,
	}
	// enabled defaults to false (atomic.Bool zero value).

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// RecordScore adds a task score to the sliding window for trend detection.
// Thread-safe. Keeps only the most recent scoreWindowSize scores.
//
// Args:
//
//	score - the task execution score to record (0-100).
func (s *EvolutionScheduler) RecordScore(score float64) {
	s.scoreMu.Lock()
	defer s.scoreMu.Unlock()

	if len(s.scores) >= scoreWindowSize {
		n := make([]float64, scoreWindowSize-1)
		copy(n, s.scores[1:])
		s.scores = n
	}
	s.scores = append(s.scores, score)
}

// OnAgentEnd handles agent completion events as a callback handler.
// It checks if an evolution cycle should be triggered and runs the adapter if so.
//
// Args:
//
//	ctx - operation context.
//	data - the callback data containing agent completion information.
func (s *EvolutionScheduler) OnAgentEnd(ctx context.Context, data CallbackData) {
	if !s.enabled.Load() {
		return
	}

	if s.adapter == nil {
		slog.WarnContext(ctx, "[Evolution] Adapter is nil, skipping evolution")
		return
	}

	if !s.shouldEvolve(ctx, data) {
		return
	}

	if !s.checkGuardrails(ctx) {
		return
	}

	slog.InfoContext(ctx, "[Evolution] Starting evolution cycle",
		"agent_id", data.AgentID,
		"trigger", s.trigger.String())

	// Cancel any previously running evolution before starting a new one
	// to prevent concurrent evolution cycles and goroutine leaks.
	{
		s.evolveMu.Lock()
		if s.evolveCancel != nil {
			s.evolveCancel()
		}
		s.evolveMu.Unlock()
	}

	// Run the adapter asynchronously via errgroup with context for cancellation support.
	// lastRun is only updated after successful completion so that failures
	// do not incorrectly trigger the cooldown timer and suppress retries.
	egCtx, egCancel := context.WithCancel(ctx)
	eg, _ := errgroup.WithContext(egCtx)

	s.evolveMu.Lock()
	s.evolveCancel = egCancel
	s.evolveMu.Unlock()

	eg.Go(func() error {
		if err := s.adapter.Run(egCtx); err != nil {
			slog.ErrorContext(ctx, "[Evolution] Evolution cycle failed",
				"agent_id", data.AgentID,
				"error", err)
			return err
		}
		// Update lastRun only after successful evolution.
		s.mu.Lock()
		s.lastRun = time.Now()
		s.mu.Unlock()
		return nil
	})

	// Start error group in background; errors are logged above.
	// NOTE: bare goroutine is used intentionally here because OnAgentEnd must return
	// immediately (it's a callback handler). The errgroup itself manages the lifecycle
	// of the inner work, and evolveCancel provides external cancellation via Shutdown().
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
	// Step 1: Check minimum interval protection.
	s.mu.Lock()
	lastRun := s.lastRun
	s.mu.Unlock()

	if !lastRun.IsZero() && time.Since(lastRun) < s.minInterval {
		slog.DebugContext(ctx, "[Evolution] Skipping: minimum interval not elapsed",
			"last_run", lastRun.Format(time.RFC3339),
			"min_interval", s.minInterval)
		return false
	}

	// Step 2: Snapshot score state under a single lock to avoid TOCTOU.
	avg, recent, scoreCount := s.scoreSnapshot()

	// Step 3: Check trigger mode.
	switch s.trigger {
	case TriggerOnDemand:
		return false

	case TriggerOnThreshold:
		if avg <= 0 || recent <= 0 {
			return false
		}
		drop := (avg - recent) / avg
		if drop >= degradationThreshold {
			slog.InfoContext(ctx, "[Evolution] Score degradation detected",
				"overall_avg", avg,
				"recent_avg", recent,
				"drop_pct", drop)
			return true
		}
		return false

	case TriggerOnIdle:
		if scoreCount < minScoreCountForReliability {
			return false
		}

		if avg > 0 && recent > 0 {
			drop := (avg - recent) / avg
			if drop >= degradationThreshold {
				slog.InfoContext(ctx, "[Evolution] Score degradation detected (idle)",
					"overall_avg", avg,
					"recent_avg", recent,
					"drop_pct", drop)
				return true
			}
		}

		if scoreCount >= periodicEvolutionScoreThreshold {
			slog.DebugContext(ctx, "[Evolution] Periodic evolution triggered",
				"score_count", scoreCount)
			return true
		}
		return false

	default:
		return false
	}
}

// scoreSnapshot reads avg, recent avg, and score count atomically under a single lock.
func (s *EvolutionScheduler) scoreSnapshot() (avg, recent float64, count int) {
	s.scoreMu.Lock()
	defer s.scoreMu.Unlock()

	if len(s.scores) == 0 {
		return 0, 0, 0
	}

	var total float64
	for _, v := range s.scores {
		total += v
	}
	avg = total / float64(len(s.scores))

	window := 10
	if window > len(s.scores) {
		window = len(s.scores)
	}
	var recentTotal float64
	for _, v := range s.scores[len(s.scores)-window:] {
		recentTotal += v
	}
	recent = recentTotal / float64(window)

	count = len(s.scores)
	return
}

// checkGuardrails runs a pre-evolution guardrail check.
// Returns true if evolution should proceed, false if guardrails block it.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	bool - true if evolution may proceed.
func (s *EvolutionScheduler) checkGuardrails(ctx context.Context) bool {
	if s.guardrails == nil {
		return true
	}
	result := s.guardrails.PreEvolveCheck(ctx, 0, 0, 0, 0)
	if result.ShouldStop {
		slog.WarnContext(ctx, "[Evolution] Guardrails block evolution cycle",
			"events", len(result.Events))
		return false
	}
	return true
}

// SetEnabled enables or disables the scheduler at runtime.
//
// Args:
//
//	enabled - true to enable, false to disable.
func (s *EvolutionScheduler) SetEnabled(enabled bool) {
	s.enabled.Store(enabled)
}

// IsEnabled returns whether the scheduler is currently enabled.
//
// Returns:
//
//	bool - true if enabled, false otherwise.
func (s *EvolutionScheduler) IsEnabled() bool {
	return s.enabled.Load()
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
	s.evolveMu.Lock()
	defer s.evolveMu.Unlock()
	if s.evolveCancel != nil {
		s.evolveCancel()
	}
}
