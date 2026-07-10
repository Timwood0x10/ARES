// Package scheduler provides idle-time evolution triggers for autonomous evolution.
// It monitors system idle conditions and triggers evolution during idle windows,
// not just nighttime, but any idle period when conditions are met.
package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Sentinel errors for the scheduler package.
var (
	// ErrSchedulerNotStarted indicates the scheduler has not been started.
	ErrSchedulerNotStarted = errors.New("scheduler not started")
	// ErrSchedulerAlreadyRunning indicates the scheduler is already running.
	ErrSchedulerAlreadyRunning = errors.New("scheduler already running")
	// ErrEvolutionInProgress indicates an evolution is already in progress.
	ErrEvolutionInProgress = errors.New("evolution already in progress")
	// ErrCooldownNotElapsed indicates the cooldown period has not elapsed.
	ErrCooldownNotElapsed = errors.New("cooldown period not elapsed")
	// ErrNotIdle indicates the system is not in an idle state.
	ErrNotIdle = errors.New("system not idle")
	// ErrDisabled indicates the scheduler is disabled.
	ErrDisabled = errors.New("scheduler disabled")
)

// Scheduler defines the interface for idle-time evolution triggers.
// Implementations monitor system idle conditions and trigger evolution
// during idle windows when conditions are met.
type Scheduler interface {
	// Start begins the scheduler's background idle monitoring loop.
	// The scheduler will periodically check idle status and trigger
	// evolution when conditions are met. Returns error if already running.
	Start(ctx context.Context) error

	// Stop gracefully stops the scheduler and cancels any running evolution.
	// It waits for in-progress operations to complete before returning.
	Stop() error

	// TriggerEvolution manually triggers an evolution cycle.
	// Returns error if evolution is already in progress or cooldown not elapsed.
	TriggerEvolution(ctx context.Context) error

	// IsIdle checks if the system is currently in an idle state.
	// Returns true if all idle conditions are met.
	IsIdle(ctx context.Context) bool

	// GetNextEvolutionTime returns the estimated time for the next evolution.
	// Returns error if scheduler is not running or disabled.
	GetNextEvolutionTime(ctx context.Context) (time.Time, error)
}

// SchedulerConfig holds configuration for the idle-time evolution scheduler.
type SchedulerConfig struct {
	// Enabled enables autonomous evolution triggering.
	Enabled bool `json:"enabled"`

	// MinCooldownPeriod is the minimum time between evolutions (default 30min).
	MinCooldownPeriod time.Duration `json:"min_cooldown_period"`

	// MaxCooldownPeriod is the maximum cooldown (default 2h).
	MaxCooldownPeriod time.Duration `json:"max_cooldown_period"`

	// IdleCheckInterval is how often to check idle status (default 5min).
	IdleCheckInterval time.Duration `json:"idle_check_interval"`

	// MinIdleDuration is the minimum idle time to trigger evolution (default 10min).
	MinIdleDuration time.Duration `json:"min_idle_duration"`

	// MaxEvolutionDuration is the maximum time for one evolution run (default 30min).
	MaxEvolutionDuration time.Duration `json:"max_evolution_duration"`

	// SampleThreshold is the minimum new samples to trigger (default 100).
	SampleThreshold int `json:"sample_threshold"`

	// IdleLoadThreshold is the maximum system load for idle detection.
	// Systems with load above this threshold are not considered idle.
	// Range: [0, 1]. Default: 0.5.
	IdleLoadThreshold float64 `json:"idle_load_threshold"`
}

// DefaultSchedulerConfig returns sensible defaults for the scheduler.
func DefaultSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Enabled:              false,
		MinCooldownPeriod:    30 * time.Minute,
		MaxCooldownPeriod:    2 * time.Hour,
		IdleCheckInterval:    5 * time.Minute,
		MinIdleDuration:      10 * time.Minute,
		MaxEvolutionDuration: 30 * time.Minute,
		SampleThreshold:      100,
		IdleLoadThreshold:    0.5,
	}
}

// IdleChecker defines the interface for checking system idle status.
// This is a plugin-based interface allowing different implementations
// for production vs testing environments.
type IdleChecker interface {
	// IsSystemIdle returns true if system load is low enough for evolution.
	IsSystemIdle(ctx context.Context) bool

	// GetQueueLength returns the number of pending tasks in the queue.
	GetQueueLength(ctx context.Context) int

	// GetSystemLoad returns the current system load metric (0-1 scale).
	GetSystemLoad(ctx context.Context) float64
}

// EvolutionRunner defines the interface for running evolution cycles.
// The scheduler delegates actual evolution execution to this interface.
type EvolutionRunner interface {
	// RunEvolution executes one evolution cycle.
	// The context should respect MaxEvolutionDuration timeout.
	RunEvolution(ctx context.Context) error
}

// SampleCounter defines the interface for counting new samples.
// Used to check if enough new data has accumulated to warrant evolution.
type SampleCounter interface {
	// GetNewSampleCount returns the number of new samples since last evolution.
	GetNewSampleCount(ctx context.Context) int
}

// SchedulerState represents the lifecycle state of the scheduler.
type SchedulerState int

const (
	StateStopped SchedulerState = iota
	StateRunning
	StateStopping
)

func (s SchedulerState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	default:
		return "unknown"
	}
}

// DefaultScheduler implements the Scheduler interface with background
// idle monitoring and evolution triggering.
type DefaultScheduler struct {
	config        SchedulerConfig
	idleChecker   IdleChecker
	runner        EvolutionRunner
	sampleCounter SampleCounter

	mu              sync.RWMutex
	state           SchedulerState
	lastEvolution   time.Time
	idleStartTime   time.Time
	evolutionActive bool

	// monitorEg tracks the monitorLoop goroutine.
	monitorEg     *errgroup.Group
	monitorCtx    context.Context
	monitorCancel context.CancelFunc

	// evolutionWg tracks evolution goroutines spawned by TriggerEvolution.
	evolutionWg sync.WaitGroup
}

// NewDefaultScheduler creates a new DefaultScheduler with the given configuration.
//
// Args:
//
//	config - scheduler configuration.
//	idleChecker - plugin for checking idle status.
//	runner - evolution runner for executing evolution cycles.
//	sampleCounter - counter for new samples (optional, can be nil).
//
// Returns:
//
//	*DefaultScheduler - the configured scheduler instance.
func NewDefaultScheduler(config SchedulerConfig, idleChecker IdleChecker, runner EvolutionRunner, sampleCounter SampleCounter) *DefaultScheduler {
	return &DefaultScheduler{
		config:        config,
		idleChecker:   idleChecker,
		runner:        runner,
		sampleCounter: sampleCounter,
	}
}

// Start begins the scheduler's background idle monitoring loop.
//
// Args:
//
//	ctx - parent context for cancellation.
//
// Returns:
//
//	error - ErrSchedulerAlreadyRunning if already started, nil on success.
func (s *DefaultScheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state != StateStopped {
		return ErrSchedulerAlreadyRunning
	}

	if !s.config.Enabled {
		log.InfoContext(ctx, "[Scheduler] Scheduler is disabled, not starting")
		return nil
	}

	// Create errgroup with derived context for graceful shutdown.
	monitorCtx, monitorCancel := context.WithCancel(ctx)
	monitorEg, monitorCtx := errgroup.WithContext(monitorCtx)

	s.monitorEg = monitorEg
	s.monitorCtx = monitorCtx
	s.monitorCancel = monitorCancel
	s.state = StateRunning

	// Start the background idle monitoring goroutine.
	monitorEg.Go(func() error {
		return s.monitorLoop(monitorCtx)
	})

	log.InfoContext(ctx, "[Scheduler] Started idle-time evolution scheduler",
		"idle_check_interval", s.config.IdleCheckInterval,
		"min_cooldown", s.config.MinCooldownPeriod)

	return nil
}

// monitorLoop is the main background goroutine that periodically checks
// idle status and triggers evolution when conditions are met.
func (s *DefaultScheduler) monitorLoop(ctx context.Context) error {
	ticker := time.NewTicker(s.config.IdleCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.InfoContext(ctx, "[Scheduler] Monitor loop stopped")
			// Mark scheduler as not running when context is cancelled.
			s.mu.Lock()
			s.state = StateStopped
			s.mu.Unlock()
			return ctx.Err()
		case <-ticker.C:
			s.checkAndTrigger(ctx)
		}
	}
}

// checkAndTrigger checks idle conditions and triggers evolution if met.
func (s *DefaultScheduler) checkAndTrigger(ctx context.Context) {
	if !s.IsIdle(ctx) {
		s.mu.Lock()
		s.idleStartTime = time.Time{} // Reset idle start time when not idle
		s.mu.Unlock()
		return
	}

	// Track when idle period started.
	s.mu.Lock()
	if s.idleStartTime.IsZero() {
		s.idleStartTime = time.Now()
		log.DebugContext(ctx, "[Scheduler] Idle period started",
			"idle_start", s.idleStartTime.Format(time.RFC3339))
	}
	idleStart := s.idleStartTime
	s.mu.Unlock()

	// Check if we've been idle long enough.
	idleDuration := time.Since(idleStart)
	if idleDuration < s.config.MinIdleDuration {
		log.DebugContext(ctx, "[Scheduler] Idle duration not sufficient",
			"current_idle", idleDuration,
			"required_idle", s.config.MinIdleDuration)
		return
	}

	// Check cooldown period.
	s.mu.RLock()
	lastEvolution := s.lastEvolution
	s.mu.RUnlock()

	if !lastEvolution.IsZero() {
		cooldownElapsed := time.Since(lastEvolution)
		if cooldownElapsed < s.config.MinCooldownPeriod {
			log.DebugContext(ctx, "[Scheduler] Cooldown period not elapsed",
				"cooldown_elapsed", cooldownElapsed,
				"required_cooldown", s.config.MinCooldownPeriod)
			return
		}
	}

	// Check sample threshold if counter is available.
	if s.sampleCounter != nil {
		newSamples := s.sampleCounter.GetNewSampleCount(ctx)
		if newSamples < s.config.SampleThreshold {
			log.DebugContext(ctx, "[Scheduler] Sample threshold not met",
				"new_samples", newSamples,
				"required_samples", s.config.SampleThreshold)
			return
		}
	}

	// All conditions met, trigger evolution.
	log.InfoContext(ctx, "[Scheduler] Idle conditions met, triggering evolution",
		"idle_duration", idleDuration,
		"cooldown_elapsed", time.Since(lastEvolution))

	if err := s.TriggerEvolution(ctx); err != nil {
		log.WarnContext(ctx, "[Scheduler] Failed to trigger evolution",
			"error", err)
	}
}

// Stop gracefully stops the scheduler and cancels any running evolution.
//
// Returns:
//
//	error - ErrSchedulerNotStarted if not running, nil on success.
func (s *DefaultScheduler) Stop() error {
	s.mu.Lock()
	if s.state != StateRunning {
		s.mu.Unlock()
		return ErrSchedulerNotStarted
	}

	log.Info("[Scheduler] Stopping scheduler")

	monitorCancel := s.monitorCancel
	monitorEg := s.monitorEg

	s.state = StateStopping
	s.monitorCancel = nil
	s.mu.Unlock()

	// Cancel the monitor context after releasing lock to avoid deadlock.
	// The monitor loop may be in checkAndTrigger trying to acquire the lock.
	if monitorCancel != nil {
		monitorCancel()
	}

	// Wait for monitor errgroup to finish after releasing lock.
	if monitorEg != nil {
		if err := monitorEg.Wait(); err != nil {
			log.Warn("evolution scheduler: wait", "error", err)
		}
	}

	// Wait for any evolution goroutines to complete.
	s.evolutionWg.Wait()

	// Reset idle period tracking after all goroutines have stopped.
	s.mu.Lock()
	s.idleStartTime = time.Time{}
	s.state = StateStopped
	s.monitorEg = nil
	s.monitorCtx = nil
	s.mu.Unlock()

	log.Info("[Scheduler] Scheduler stopped")
	return nil
}

// TriggerEvolution manually triggers an evolution cycle.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	error - various errors depending on state (disabled, in progress, cooldown).
func (s *DefaultScheduler) TriggerEvolution(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Guard against triggering during shutdown to prevent goroutine leaks.
	if s.state == StateStopping {
		return ErrSchedulerNotStarted
	}

	if !s.config.Enabled {
		return ErrDisabled
	}

	if s.evolutionActive {
		return ErrEvolutionInProgress
	}

	// Check cooldown period.
	if !s.lastEvolution.IsZero() {
		cooldownElapsed := time.Since(s.lastEvolution)
		if cooldownElapsed < s.config.MinCooldownPeriod {
			return ErrCooldownNotElapsed
		}
	}

	if s.runner == nil {
		log.WarnContext(ctx, "[Scheduler] No evolution runner configured")
		return errors.New("no evolution runner configured")
	}

	s.evolutionActive = true
	s.evolutionWg.Add(1)

	// Run evolution with timeout in a tracked goroutine.
	evolutionCtx, evolutionCancel := context.WithTimeout(ctx, s.config.MaxEvolutionDuration)

	go func() {
		defer s.evolutionWg.Done()
		defer evolutionCancel()

		err := s.runner.RunEvolution(evolutionCtx)
		if err != nil {
			log.ErrorContext(ctx, "[Scheduler] Evolution run failed",
				"error", err)
		} else {
			log.InfoContext(ctx, "[Scheduler] Evolution completed successfully")
		}

		// Update state after evolution completes.
		s.mu.Lock()
		s.evolutionActive = false
		s.lastEvolution = time.Now()
		s.idleStartTime = time.Time{} // Reset idle period after evolution
		s.mu.Unlock()
	}()

	return nil
}

// IsIdle checks if the system is currently in an idle state.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	bool - true if all idle conditions are met.
func (s *DefaultScheduler) IsIdle(ctx context.Context) bool {
	if s.idleChecker == nil {
		log.WarnContext(ctx, "[Scheduler] No idle checker configured, assuming not idle")
		return false
	}

	// Check system load.
	if !s.idleChecker.IsSystemIdle(ctx) {
		log.DebugContext(ctx, "[Scheduler] System not idle (load check)")
		return false
	}

	// Check queue length (must be empty).
	queueLen := s.idleChecker.GetQueueLength(ctx)
	if queueLen > 0 {
		log.DebugContext(ctx, "[Scheduler] Queue not empty",
			"queue_length", queueLen)
		return false
	}

	// Check system load metric.
	load := s.idleChecker.GetSystemLoad(ctx)
	threshold := s.config.IdleLoadThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	if load > threshold {
		log.DebugContext(ctx, "[Scheduler] System load too high",
			"load", load, "threshold", threshold)
		return false
	}

	return true
}

// GetNextEvolutionTime returns the estimated time for the next evolution.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	time.Time - estimated next evolution time.
//	error - ErrDisabled or ErrSchedulerNotStarted if appropriate.
func (s *DefaultScheduler) GetNextEvolutionTime(ctx context.Context) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.config.Enabled {
		return time.Time{}, ErrDisabled
	}

	if s.state != StateRunning {
		return time.Time{}, ErrSchedulerNotStarted
	}

	// If evolution is active, return when it might finish (approximate).
	if s.evolutionActive {
		return time.Now().Add(s.config.MaxEvolutionDuration), nil
	}

	// If never evolved, next evolution is when idle conditions are met.
	if s.lastEvolution.IsZero() {
		return time.Now().Add(s.config.MinIdleDuration), nil
	}

	// Calculate next evolution based on cooldown.
	nextTime := s.lastEvolution.Add(s.config.MinCooldownPeriod)

	// If cooldown elapsed but not idle, we need to wait for idle.
	if time.Now().After(nextTime) {
		// Add minimum idle duration as estimate.
		nextTime = time.Now().Add(s.config.MinIdleDuration)
	}

	return nextTime, nil
}

// IsRunning returns whether the scheduler is currently running.
//
// Returns:
//
//	bool - true if running, false otherwise.
func (s *DefaultScheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state == StateRunning
}

// LastEvolutionTime returns the timestamp of the last evolution cycle.
//
// Returns:
//
//	time.Time - the last evolution time, or zero value if never run.
func (s *DefaultScheduler) LastEvolutionTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastEvolution
}

// SetEnabled enables or disables the scheduler at runtime.
// If disabling while running, the scheduler will stop monitoring.
//
// Args:
//
//	enabled - true to enable, false to disable.
func (s *DefaultScheduler) SetEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Enabled = enabled
}

// SetIdleChecker allows changing the idle checker at runtime.
//
// Args:
//
//	checker - the new idle checker implementation.
func (s *DefaultScheduler) SetIdleChecker(checker IdleChecker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.idleChecker = checker
}

// WiredSystemRunner adapts WiredEvolutionSystem to EvolutionRunner interface.
// This allows the scheduler to trigger evolution on a wired system.
type WiredSystemRunner struct {
	system interface {
		RunIdleEvolution(ctx context.Context, generations int) error
	}
	generations int
}

// NewWiredSystemRunner creates a runner that triggers evolution on a WiredEvolutionSystem.
//
// Args:
//
//	system - the wired evolution system.
//	generations - number of generations to run per evolution (default 1).
//
// Returns:
//
//	*WiredSystemRunner - the runner instance.
func NewWiredSystemRunner(system interface {
	RunIdleEvolution(ctx context.Context, generations int) error
}, generations int) *WiredSystemRunner {
	if generations <= 0 {
		generations = 1
	}
	return &WiredSystemRunner{
		system:      system,
		generations: generations,
	}
}

// RunEvolution triggers idle evolution on the wired system.
//
// Args:
//
//	ctx - operation context.
//
// Returns:
//
//	error - error from RunIdleEvolution or nil.
func (r *WiredSystemRunner) RunEvolution(ctx context.Context) error {
	if r.system == nil {
		return errors.New("wired system is nil")
	}
	return r.system.RunIdleEvolution(ctx, r.generations)
}
