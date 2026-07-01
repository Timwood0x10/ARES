package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestDefaultSchedulerConfig tests the default configuration values.
func TestDefaultSchedulerConfig(t *testing.T) {
	cfg := DefaultSchedulerConfig()

	if cfg.Enabled {
		t.Error("expected Enabled to be false by default")
	}
	if cfg.MinCooldownPeriod != 30*time.Minute {
		t.Errorf("expected MinCooldownPeriod 30m, got %v", cfg.MinCooldownPeriod)
	}
	if cfg.MaxCooldownPeriod != 2*time.Hour {
		t.Errorf("expected MaxCooldownPeriod 2h, got %v", cfg.MaxCooldownPeriod)
	}
	if cfg.IdleCheckInterval != 5*time.Minute {
		t.Errorf("expected IdleCheckInterval 5m, got %v", cfg.IdleCheckInterval)
	}
	if cfg.MinIdleDuration != 10*time.Minute {
		t.Errorf("expected MinIdleDuration 10m, got %v", cfg.MinIdleDuration)
	}
	if cfg.MaxEvolutionDuration != 30*time.Minute {
		t.Errorf("expected MaxEvolutionDuration 30m, got %v", cfg.MaxEvolutionDuration)
	}
	if cfg.SampleThreshold != 100 {
		t.Errorf("expected SampleThreshold 100, got %d", cfg.SampleThreshold)
	}
}

// TestNewDefaultScheduler tests the scheduler constructor.
func TestNewDefaultScheduler(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	counter := NewMockSampleCounter()

	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, counter)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler")
	}
	if scheduler.config.Enabled != true {
		t.Error("expected Enabled to be true")
	}
	if scheduler.idleChecker == nil {
		t.Error("expected idle checker to be set")
	}
	if scheduler.runner == nil {
		t.Error("expected runner to be set")
	}
}

// TestNewDefaultScheduler_NilDependencies tests with nil dependencies.
func TestNewDefaultScheduler_NilDependencies(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true

	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	if scheduler == nil {
		t.Fatal("expected non-nil scheduler even with nil dependencies")
	}
}

// TestStart_Success tests successful scheduler start.
func TestStart_Success(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 100 * time.Millisecond // Fast for testing

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx := context.Background()
	err := scheduler.Start(ctx)
	if err != nil {
		t.Errorf("expected no error on start, got %v", err)
	}

	if !scheduler.IsRunning() {
		t.Error("expected scheduler to be running")
	}

	// Stop the scheduler
	err = scheduler.Stop()
	if err != nil {
		t.Errorf("expected no error on stop, got %v", err)
	}
}

// TestStart_AlreadyRunning tests error when starting twice.
func TestStart_AlreadyRunning(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 100 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	err := scheduler.Start(ctx)
	if err != ErrSchedulerAlreadyRunning {
		t.Errorf("expected ErrSchedulerAlreadyRunning, got %v", err)
	}

	_ = scheduler.Stop()
}

// TestStart_Disabled tests that disabled scheduler does not start.
func TestStart_Disabled(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = false

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx := context.Background()
	err := scheduler.Start(ctx)
	if err != nil {
		t.Errorf("expected no error when disabled, got %v", err)
	}

	if scheduler.IsRunning() {
		t.Error("expected scheduler not to be running when disabled")
	}
}

// TestStop_NotStarted tests error when stopping without start.
func TestStop_NotStarted(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	err := scheduler.Stop()
	if err != ErrSchedulerNotStarted {
		t.Errorf("expected ErrSchedulerNotStarted, got %v", err)
	}
}

// TestTriggerEvolution_Success tests successful evolution trigger.
func TestTriggerEvolution_Success(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.MinCooldownPeriod = 10 * time.Millisecond
	cfg.MaxEvolutionDuration = 100 * time.Millisecond

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	ctx := context.Background()

	err := scheduler.TriggerEvolution(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Wait for evolution to complete
	time.Sleep(150 * time.Millisecond)

	if runner.RunCount() != 1 {
		t.Errorf("expected 1 evolution run, got %d", runner.RunCount())
	}
}

// TestTriggerEvolution_Disabled tests error when disabled.
func TestTriggerEvolution_Disabled(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = false

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	ctx := context.Background()
	err := scheduler.TriggerEvolution(ctx)
	if err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
}

// TestTriggerEvolution_CooldownNotElapsed tests cooldown protection.
func TestTriggerEvolution_CooldownNotElapsed(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.MinCooldownPeriod = 1 * time.Hour // Long cooldown

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	ctx := context.Background()

	// First trigger should succeed (no last evolution)
	err := scheduler.TriggerEvolution(ctx)
	if err != nil {
		t.Errorf("expected first trigger to succeed, got %v", err)
	}

	// Wait for evolution to complete and set lastEvolution
	time.Sleep(100 * time.Millisecond)

	// Second trigger should fail due to cooldown
	err = scheduler.TriggerEvolution(ctx)
	if err != ErrCooldownNotElapsed {
		t.Errorf("expected ErrCooldownNotElapsed, got %v", err)
	}
}

// TestTriggerEvolution_AlreadyInProgress tests error when evolution is active.
func TestTriggerEvolution_AlreadyInProgress(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.MaxEvolutionDuration = 1 * time.Second

	runner := NewMockEvolutionRunner()
	runner.SetRunDuration(500 * time.Millisecond) // Evolution takes 500ms
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	ctx := context.Background()

	// First trigger
	err := scheduler.TriggerEvolution(ctx)
	if err != nil {
		t.Errorf("expected first trigger to succeed, got %v", err)
	}

	// Second trigger immediately should fail
	err = scheduler.TriggerEvolution(ctx)
	if err != ErrEvolutionInProgress {
		t.Errorf("expected ErrEvolutionInProgress, got %v", err)
	}

	// Wait for evolution to complete
	time.Sleep(600 * time.Millisecond)
}

// TestTriggerEvolution_NilRunner tests error with nil runner.
func TestTriggerEvolution_NilRunner(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true

	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	ctx := context.Background()
	err := scheduler.TriggerEvolution(ctx)
	if err == nil {
		t.Error("expected error with nil runner")
	}
}

// TestIsIdle_AllConditionsMet tests idle detection when all conditions met.
func TestIsIdle_AllConditionsMet(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)
	idleChecker.SetQueueLength(0)
	idleChecker.SetSystemLoad(0.1) // Low load

	scheduler := NewDefaultScheduler(cfg, idleChecker, nil, nil)

	ctx := context.Background()
	if !scheduler.IsIdle(ctx) {
		t.Error("expected system to be idle")
	}
}

// TestIsIdle_SystemNotIdle tests idle detection when system is busy.
func TestIsIdle_SystemNotIdle(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(false)

	scheduler := NewDefaultScheduler(cfg, idleChecker, nil, nil)

	ctx := context.Background()
	if scheduler.IsIdle(ctx) {
		t.Error("expected system not to be idle when systemIdle=false")
	}
}

// TestIsIdle_QueueNotEmpty tests idle detection with pending tasks.
func TestIsIdle_QueueNotEmpty(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)
	idleChecker.SetQueueLength(5) // Non-empty queue

	scheduler := NewDefaultScheduler(cfg, idleChecker, nil, nil)

	ctx := context.Background()
	if scheduler.IsIdle(ctx) {
		t.Error("expected system not to be idle with non-empty queue")
	}
}

// TestIsIdle_HighLoad tests idle detection with high system load.
func TestIsIdle_HighLoad(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)
	idleChecker.SetQueueLength(0)
	idleChecker.SetSystemLoad(0.8) // High load (> 0.5 threshold)

	scheduler := NewDefaultScheduler(cfg, idleChecker, nil, nil)

	ctx := context.Background()
	if scheduler.IsIdle(ctx) {
		t.Error("expected system not to be idle with high load")
	}
}

// TestIsIdle_NilChecker tests idle detection with nil checker.
func TestIsIdle_NilChecker(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	ctx := context.Background()
	if scheduler.IsIdle(ctx) {
		t.Error("expected system not to be idle with nil checker")
	}
}

// TestGetNextEvolutionTime_Disabled tests error when disabled.
func TestGetNextEvolutionTime_Disabled(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = false

	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	ctx := context.Background()
	_, err := scheduler.GetNextEvolutionTime(ctx)
	if err != ErrDisabled {
		t.Errorf("expected ErrDisabled, got %v", err)
	}
}

// TestGetNextEvolutionTime_NotStarted tests error when not started.
func TestGetNextEvolutionTime_NotStarted(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true

	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	ctx := context.Background()
	_, err := scheduler.GetNextEvolutionTime(ctx)
	if err != ErrSchedulerNotStarted {
		t.Errorf("expected ErrSchedulerNotStarted, got %v", err)
	}
}

// TestGetNextEvolutionTime_NeverEvolved tests estimation before first evolution.
func TestGetNextEvolutionTime_NeverEvolved(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 100 * time.Millisecond
	cfg.MinIdleDuration = 50 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	scheduler := NewDefaultScheduler(cfg, idleChecker, nil, nil)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	nextTime, err := scheduler.GetNextEvolutionTime(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Should be approximately MinIdleDuration from now
	expectedMin := time.Now().Add(cfg.MinIdleDuration)
	if nextTime.Before(expectedMin.Add(-10 * time.Millisecond)) {
		t.Errorf("expected next time >= %v, got %v", expectedMin, nextTime)
	}

	_ = scheduler.Stop()
}

// TestGetNextEvolutionTime_AfterCooldown tests estimation after cooldown.
func TestGetNextEvolutionTime_AfterCooldown(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 100 * time.Millisecond
	cfg.MinCooldownPeriod = 50 * time.Millisecond
	cfg.MaxEvolutionDuration = 10 * time.Millisecond

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	// Trigger an evolution
	_ = scheduler.TriggerEvolution(ctx)
	time.Sleep(100 * time.Millisecond) // Wait for completion

	nextTime, err := scheduler.GetNextEvolutionTime(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Should be approximately MinCooldownPeriod after lastEvolution
	lastEvolution := scheduler.LastEvolutionTime()
	expectedMin := lastEvolution.Add(cfg.MinCooldownPeriod)
	if nextTime.Before(expectedMin.Add(-10 * time.Millisecond)) {
		t.Errorf("expected next time >= %v, got %v", expectedMin, nextTime)
	}

	_ = scheduler.Stop()
}

// TestLastEvolutionTime tests last evolution time tracking.
func TestLastEvolutionTime(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.MaxEvolutionDuration = 10 * time.Millisecond

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, nil, runner, nil)

	initialTime := scheduler.LastEvolutionTime()
	if !initialTime.IsZero() {
		t.Error("expected zero LastEvolutionTime initially")
	}

	ctx := context.Background()
	_ = scheduler.TriggerEvolution(ctx)

	// Wait for evolution to complete
	time.Sleep(50 * time.Millisecond)

	lastTime := scheduler.LastEvolutionTime()
	if lastTime.IsZero() {
		t.Error("expected non-zero LastEvolutionTime after evolution")
	}
}

// TestSetEnabled tests runtime enable/disable.
func TestSetEnabled(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true

	scheduler := NewDefaultScheduler(cfg, nil, nil, nil)

	scheduler.SetEnabled(false)
	if scheduler.config.Enabled {
		t.Error("expected Enabled to be false after SetEnabled(false)")
	}

	scheduler.SetEnabled(true)
	if !scheduler.config.Enabled {
		t.Error("expected Enabled to be true after SetEnabled(true)")
	}
}

// TestSetIdleChecker tests runtime idle checker replacement.
func TestSetIdleChecker(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	oldChecker := NewSimpleIdleChecker()
	scheduler := NewDefaultScheduler(cfg, oldChecker, nil, nil)

	newChecker := NewSimpleIdleChecker()
	newChecker.SetIdleStatus(false)

	scheduler.SetIdleChecker(newChecker)

	ctx := context.Background()
	if scheduler.IsIdle(ctx) {
		t.Error("expected IsIdle to use new checker")
	}
}

// TestSimpleIdleChecker tests SimpleIdleChecker functionality.
func TestSimpleIdleChecker(t *testing.T) {
	checker := NewSimpleIdleChecker()

	// Test default values
	ctx := context.Background()
	if !checker.IsSystemIdle(ctx) {
		t.Error("expected default IsSystemIdle to be true")
	}
	if checker.GetQueueLength(ctx) != 0 {
		t.Error("expected default queue length to be 0")
	}
	if checker.GetSystemLoad(ctx) != 0.0 {
		t.Error("expected default system load to be 0")
	}

	// Test setters
	checker.SetIdleStatus(false)
	if checker.IsSystemIdle(ctx) {
		t.Error("expected IsSystemIdle to be false after setter")
	}

	checker.SetQueueLength(10)
	if checker.GetQueueLength(ctx) != 10 {
		t.Errorf("expected queue length 10, got %d", checker.GetQueueLength(ctx))
	}

	checker.SetSystemLoad(0.5)
	if checker.GetSystemLoad(ctx) != 0.5 {
		t.Errorf("expected system load 0.5, got %f", checker.GetSystemLoad(ctx))
	}
}

// TestMockEvolutionRunner tests MockEvolutionRunner functionality.
func TestMockEvolutionRunner(t *testing.T) {
	runner := NewMockEvolutionRunner()

	ctx := context.Background()

	// Test default behavior
	err := runner.RunEvolution(ctx)
	if err != nil {
		t.Errorf("expected no error by default, got %v", err)
	}
	if runner.RunCount() != 1 {
		t.Errorf("expected run count 1, got %d", runner.RunCount())
	}

	// Test error behavior
	testErr := errors.New("test error")
	runner.SetRunError(testErr)
	err = runner.RunEvolution(ctx)
	if err != testErr {
		t.Errorf("expected test error, got %v", err)
	}

	// Test duration behavior
	runner.SetRunError(nil)
	runner.SetRunDuration(50 * time.Millisecond)

	start := time.Now()
	err = runner.RunEvolution(ctx)
	duration := time.Since(start)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if duration < 40*time.Millisecond {
		t.Errorf("expected duration >= 50ms, got %v", duration)
	}
}

// TestMockEvolutionRunner_ContextCancellation tests context cancellation.
func TestMockEvolutionRunner_ContextCancellation(t *testing.T) {
	runner := NewMockEvolutionRunner()
	runner.SetRunDuration(1 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := runner.RunEvolution(ctx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestMockSampleCounter tests MockSampleCounter functionality.
func TestMockSampleCounter(t *testing.T) {
	counter := NewMockSampleCounter()

	ctx := context.Background()

	// Test default value
	if counter.GetNewSampleCount(ctx) != 0 {
		t.Error("expected default count to be 0")
	}

	// Test setter
	counter.SetSampleCount(150)
	if counter.GetNewSampleCount(ctx) != 150 {
		t.Errorf("expected count 150, got %d", counter.GetNewSampleCount(ctx))
	}
}

// TestWiredSystemRunner tests WiredSystemRunner functionality.
func TestWiredSystemRunner(t *testing.T) {
	mockSystem := &mockWiredSystem{}
	runner := NewWiredSystemRunner(mockSystem, 3)

	ctx := context.Background()
	err := runner.RunEvolution(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if mockSystem.generationsRun != 3 {
		t.Errorf("expected 3 generations, got %d", mockSystem.generationsRun)
	}
}

// TestWiredSystemRunner_DefaultGenerations tests default generations.
func TestWiredSystemRunner_DefaultGenerations(t *testing.T) {
	mockSystem := &mockWiredSystem{}
	runner := NewWiredSystemRunner(mockSystem, 0) // Should default to 1

	ctx := context.Background()
	_ = runner.RunEvolution(ctx)

	if mockSystem.generationsRun != 1 {
		t.Errorf("expected 1 generation as default, got %d", mockSystem.generationsRun)
	}
}

// TestWiredSystemRunner_NilSystem tests error with nil system.
func TestWiredSystemRunner_NilSystem(t *testing.T) {
	runner := NewWiredSystemRunner(nil, 1)

	ctx := context.Background()
	err := runner.RunEvolution(ctx)
	if err == nil {
		t.Error("expected error with nil system")
	}
}

// TestGracefulShutdown tests graceful shutdown with context cancellation.
func TestGracefulShutdown(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 100 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx, cancel := context.WithCancel(context.Background())

	err := scheduler.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Cancel context after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	// Wait for scheduler to stop
	time.Sleep(300 * time.Millisecond)

	if scheduler.IsRunning() {
		t.Error("expected scheduler to stop after context cancellation")
	}
}

// TestConcurrency tests thread-safety with concurrent operations.
func TestConcurrency(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 50 * time.Millisecond
	cfg.MinCooldownPeriod = 10 * time.Millisecond
	cfg.MaxEvolutionDuration = 10 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	runner := NewMockEvolutionRunner()
	counter := NewMockSampleCounter()
	counter.SetSampleCount(200) // Above threshold

	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, counter)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	var wg sync.WaitGroup

	// Concurrent IsIdle calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scheduler.IsIdle(ctx)
		}()
	}

	// Concurrent GetNextEvolutionTime calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = scheduler.GetNextEvolutionTime(ctx)
		}()
	}

	// Concurrent LastEvolutionTime calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scheduler.LastEvolutionTime()
		}()
	}

	wg.Wait()
	_ = scheduler.Stop()
}

// TestAutoTriggerOnIdle tests automatic triggering when idle conditions are met.
func TestAutoTriggerOnIdle(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 50 * time.Millisecond // Check frequently
	cfg.MinIdleDuration = 100 * time.Millisecond  // Short idle duration
	cfg.MinCooldownPeriod = 50 * time.Millisecond // Short cooldown
	cfg.MaxEvolutionDuration = 10 * time.Millisecond
	cfg.SampleThreshold = 50 // Low threshold

	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)
	idleChecker.SetQueueLength(0)
	idleChecker.SetSystemLoad(0.1)

	runner := NewMockEvolutionRunner()
	counter := NewMockSampleCounter()
	counter.SetSampleCount(100) // Above threshold

	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, counter)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	// Wait for multiple idle checks and potential auto-trigger
	time.Sleep(300 * time.Millisecond)

	_ = scheduler.Stop()

	// Should have triggered at least one evolution
	if runner.RunCount() < 1 {
		t.Errorf("expected at least 1 auto-triggered evolution, got %d", runner.RunCount())
	}
}

// TestAutoTrigger_NotIdle tests no trigger when not idle.
func TestAutoTrigger_NotIdle(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 50 * time.Millisecond
	cfg.MinIdleDuration = 10 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(false) // Not idle

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	_ = scheduler.Stop()

	// Should not have triggered any evolution
	if runner.RunCount() > 0 {
		t.Errorf("expected no evolution when not idle, got %d", runner.RunCount())
	}
}

// TestAutoTrigger_SampleThresholdNotMet tests no trigger when samples insufficient.
func TestAutoTrigger_SampleThresholdNotMet(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 50 * time.Millisecond
	cfg.MinIdleDuration = 10 * time.Millisecond
	cfg.SampleThreshold = 1000 // High threshold

	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)

	runner := NewMockEvolutionRunner()
	counter := NewMockSampleCounter()
	counter.SetSampleCount(10) // Below threshold

	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, counter)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	time.Sleep(200 * time.Millisecond)

	_ = scheduler.Stop()

	// Should not have triggered due to sample threshold
	if runner.RunCount() > 0 {
		t.Errorf("expected no evolution with insufficient samples, got %d", runner.RunCount())
	}
}

// TestIdlePeriodReset tests that idle period resets after evolution.
func TestIdlePeriodReset(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	cfg.Enabled = true
	cfg.IdleCheckInterval = 20 * time.Millisecond
	cfg.MinIdleDuration = 30 * time.Millisecond
	cfg.MinCooldownPeriod = 100 * time.Millisecond
	cfg.MaxEvolutionDuration = 10 * time.Millisecond

	idleChecker := NewSimpleIdleChecker()
	idleChecker.SetIdleStatus(true)

	runner := NewMockEvolutionRunner()
	scheduler := NewDefaultScheduler(cfg, idleChecker, runner, nil)

	ctx := context.Background()
	_ = scheduler.Start(ctx)

	// Wait for first evolution to trigger and complete
	time.Sleep(150 * time.Millisecond)

	_ = scheduler.Stop()

	// After evolution, idleStartTime should be reset
	scheduler.mu.RLock()
	idleStart := scheduler.idleStartTime
	scheduler.mu.RUnlock()

	if !idleStart.IsZero() {
		t.Error("expected idleStartTime to be reset after evolution")
	}
}

// mockWiredSystem is a mock implementation for testing WiredSystemRunner.
type mockWiredSystem struct {
	generationsRun int
}

func (m *mockWiredSystem) RunIdleEvolution(ctx context.Context, generations int) error {
	m.generationsRun = generations
	return nil
}
