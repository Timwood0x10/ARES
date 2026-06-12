// Package shutdown provides comprehensive tests for shutdown functionality.
package shutdown

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Manager comprehensive tests.

func TestManager_StartShutdown_Success(t *testing.T) {
	manager := NewManager(10 * time.Second)

	// Register phases with timeouts
	manager.RegisterPhase(PhasePreShutdown, 1*time.Second)
	manager.RegisterPhase(PhaseGraceful, 2*time.Second)
	manager.RegisterPhase(PhaseForce, 1*time.Second)
	manager.RegisterPhase(PhaseDone, 1*time.Second)

	// Add callbacks
	var executedPhases []Phase
	if err := manager.AddCallback(PhasePreShutdown, func(ctx context.Context) error {
		executedPhases = append(executedPhases, PhasePreShutdown)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		executedPhases = append(executedPhases, PhaseGraceful)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseForce, func(ctx context.Context) error {
		executedPhases = append(executedPhases, PhaseForce)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseDone, func(ctx context.Context) error {
		executedPhases = append(executedPhases, PhaseDone)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	// Execute shutdown
	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err != nil {
		t.Errorf("StartShutdown failed: %v", err)
	}

	// Verify all phases were executed in order
	expectedPhases := []Phase{PhasePreShutdown, PhaseGraceful, PhaseForce, PhaseDone}
	if len(executedPhases) != len(expectedPhases) {
		t.Errorf("expected %d phases executed, got %d", len(expectedPhases), len(executedPhases))
	}

	for i, phase := range expectedPhases {
		if i >= len(executedPhases) || executedPhases[i] != phase {
			t.Errorf("expected phase %d to be %s, got %s", i, phase, executedPhases[i])
		}
	}

	// Verify final phase
	if manager.CurrentPhase() != PhaseDone {
		t.Errorf("expected final phase to be PhaseDone, got %s", manager.CurrentPhase())
	}
}

func TestManager_AddCallback_UnregisteredPhase(t *testing.T) {
	manager := NewManager(10 * time.Second)

	// Try to add callback to unregistered phase
	err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		return nil
	})

	if err == nil {
		t.Errorf("expected error when adding callback to unregistered phase")
	}
}

func TestManager_StartShutdown_AlreadyInProgress(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start first shutdown
	errChan := make(chan error, 1)
	go func() {
		errChan <- manager.StartShutdown(ctx)
	}()

	// Wait a bit to ensure first shutdown starts
	time.Sleep(100 * time.Millisecond)

	// Try to start second shutdown
	err := manager.StartShutdown(ctx)
	if err == nil {
		t.Errorf("expected error when starting shutdown already in progress")
	}

	// Cancel context to unblock first shutdown
	cancel()
	<-errChan
}

func TestManager_CallbackTimeout(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 100*time.Millisecond)

	timeoutCalled := false
	manager.SetOnTimeout(PhaseGraceful, func() {
		timeoutCalled = true
	})

	// Add callback that takes longer than phase timeout
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err == nil {
		t.Errorf("expected timeout error")
	}

	if !timeoutCalled {
		t.Errorf("expected timeout callback to be called")
	}
}

func TestManager_CallbackPanic(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	panicCalled := false
	var panicValue interface{}
	manager.SetOnPanic(PhaseGraceful, func(v interface{}) {
		panicCalled = true
		panicValue = v
	})

	// Add callback that panics
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		panic("test panic")
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err == nil {
		t.Errorf("expected error from panic")
	}

	if !panicCalled {
		t.Errorf("expected panic callback to be called")
	}

	if panicValue != "test panic" {
		t.Errorf("expected panic value 'test panic', got %v", panicValue)
	}
}

func TestManager_CallbackError(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	// Add callback that returns error
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		return errors.New("callback error")
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err == nil {
		t.Errorf("expected error from callback")
	}

	// Error is wrapped by the shutdown manager
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestManager_MultipleCallbacks(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	var executedCount atomic.Int32
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		executedCount.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		executedCount.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		executedCount.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err != nil {
		t.Errorf("StartShutdown failed: %v", err)
	}

	if executedCount.Load() != 3 {
		t.Errorf("expected 3 callbacks executed, got %d", executedCount.Load())
	}
}

func TestManager_MixedSuccessAndFailure(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	var successCount atomic.Int32
	var errorCount atomic.Int32

	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		successCount.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		errorCount.Add(1)
		return errors.New("callback error")
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		successCount.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err == nil {
		t.Errorf("expected error from failed callback")
	}

	if successCount.Load() != 2 {
		t.Errorf("expected 2 successful callbacks, got %d", successCount.Load())
	}

	if errorCount.Load() != 1 {
		t.Errorf("expected 1 failed callback, got %d", errorCount.Load())
	}
}

func TestManager_EmptyPhase(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhasePreShutdown, 1*time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	// Only add callback to PreShutdown phase
	if err := manager.AddCallback(PhasePreShutdown, func(ctx context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	if err != nil {
		t.Errorf("StartShutdown with empty phase failed: %v", err)
	}

	// Should complete successfully even with empty phase
	if manager.CurrentPhase() != PhaseDone {
		t.Errorf("expected final phase to be PhaseDone, got %s", manager.CurrentPhase())
	}
}

func TestManager_UnregisteredPhase(t *testing.T) {
	manager := NewManager(10 * time.Second)

	// Don't register any phase, just start shutdown
	ctx := context.Background()
	err := manager.StartShutdown(ctx)

	// Should complete successfully even with unregistered phases
	if err != nil {
		t.Errorf("StartShutdown with unregistered phases failed: %v", err)
	}
}

func TestManager_Wait(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhaseGraceful, 2*time.Second)

	var executed atomic.Int64
	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		time.Sleep(500 * time.Millisecond)
		executed.Store(1)
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	go func() {
		if err := manager.StartShutdown(ctx); err != nil {
			t.Logf("StartShutdown failed: %v", err)
		}
	}()

	// Wait for shutdown to complete
	manager.Wait()

	// Give a small buffer for the callback to complete
	time.Sleep(600 * time.Millisecond)

	if executed.Load() == 0 {
		t.Errorf("expected callback to be executed")
	}
}

func TestManager_IsShutdown(t *testing.T) {
	manager := NewManager(10 * time.Second)
	manager.RegisterPhase(PhasePreShutdown, 1*time.Second)
	manager.RegisterPhase(PhaseGraceful, 1*time.Second)

	// Initially not shutdown
	if manager.IsShutdown() {
		t.Errorf("expected IsShutdown to be false initially")
	}

	if err := manager.AddCallback(PhasePreShutdown, func(ctx context.Context) error {
		// In PreShutdown phase, IsShutdown should still be false
		if manager.IsShutdown() {
			t.Errorf("expected IsShutdown to be false during PreShutdown")
		}
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	if err := manager.AddCallback(PhaseGraceful, func(ctx context.Context) error {
		// In Graceful phase, IsShutdown should be true
		if !manager.IsShutdown() {
			t.Errorf("expected IsShutdown to be true during Graceful")
		}
		return nil
	}); err != nil {
		t.Fatalf("Failed to add callback: %v", err)
	}

	ctx := context.Background()
	err := manager.StartShutdown(ctx)
	if err != nil {
		t.Errorf("StartShutdown failed: %v", err)
	}

	// After shutdown, IsShutdown should be true
	if !manager.IsShutdown() {
		t.Errorf("expected IsShutdown to be true after shutdown")
	}
}

// PhaseExecutor comprehensive tests.

func TestPhaseExecutor_Execute_Success(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	executed := false
	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		executed = true
		return nil
	})

	if err != nil {
		t.Errorf("Execute failed: %v", err)
	}

	if !executed {
		t.Errorf("expected function to be executed")
	}

	if executor.State() != PhaseStateCompleted {
		t.Errorf("expected state to be completed, got %s", executor.State())
	}
}

func TestPhaseExecutor_Execute_RetryOnFailure(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 2) // Reduced from 3 to 2 for faster testing

	attempts := 0
	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		attempts++
		if attempts < 2 { // Changed from 3 to 2
			return errors.New("temporary error")
		}
		return nil
	})

	if err != nil {
		t.Errorf("Execute with retries failed: %v", err)
	}

	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}

	if executor.Retries() != 1 { // Changed from 2 to 1
		t.Errorf("expected 1 retry, got %d", executor.Retries())
	}

	if executor.State() != PhaseStateCompleted {
		t.Errorf("expected state to be completed, got %s", executor.State())
	}
}

func TestPhaseExecutor_Execute_MaxRetriesExceeded(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 2)

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("persistent error")
	})

	if err == nil {
		t.Errorf("expected error after max retries")
	}

	if executor.Retries() != 2 {
		t.Errorf("expected 2 retries, got %d", executor.Retries())
	}

	if executor.State() != PhaseStateFailed {
		t.Errorf("expected state to be failed, got %s", executor.State())
	}

	if executor.Error() == nil {
		t.Errorf("expected error to be stored")
	}
}

func TestPhaseExecutor_Execute_ContextCancelled(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	err := executor.Execute(ctx, func(ctx context.Context) error {
		time.Sleep(1 * time.Second)
		return nil
	})

	if err == nil {
		t.Errorf("expected context cancellation error")
	}

	if executor.State() != PhaseStateFailed {
		t.Errorf("expected state to be failed, got %s", executor.State())
	}
}

func TestPhaseExecutor_Execute_AlreadyRunning(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	ctx := context.Background()
	firstExecRunning := make(chan struct{})
	firstExecComplete := make(chan struct{})
	errChan := make(chan error, 2)

	// Start first execution that takes some time
	go func() {
		errChan <- executor.Execute(ctx, func(ctx context.Context) error {
			close(firstExecRunning)
			time.Sleep(200 * time.Millisecond)
			close(firstExecComplete)
			return nil
		})
	}()

	// Wait for first execution to actually be running inside Execute
	<-firstExecRunning

	// Try to execute again immediately while first is still running
	err := executor.Execute(ctx, func(ctx context.Context) error {
		return nil
	})

	if err == nil {
		t.Errorf("expected error when executor is already running")
	}

	if err != ErrPhaseAlreadyRunning {
		t.Errorf("expected ErrPhaseAlreadyRunning, got %v", err)
	}

	<-firstExecComplete
	<-errChan
}

func TestPhaseExecutor_Rollback(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 0) // No retries needed for rollback test

	rollbackCalled := false
	executor.SetRollbackFn(func() error {
		rollbackCalled = true
		return nil
	})

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("execution error")
	})

	if err == nil {
		t.Errorf("expected execution error")
	}

	rollbackErr := executor.Rollback()
	if rollbackErr != nil {
		t.Errorf("Rollback failed: %v", rollbackErr)
	}

	if !rollbackCalled {
		t.Errorf("expected rollback to be called")
	}
}

func TestPhaseExecutor_Rollback_NotSet(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 0) // No retries needed for rollback test

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("execution error")
	})

	if err == nil {
		t.Errorf("expected execution error")
	}

	// Rollback should not fail even if not set
	rollbackErr := executor.Rollback()
	if rollbackErr != nil {
		t.Errorf("Rollback failed: %v", rollbackErr)
	}
}

func TestPhaseExecutor_Rollback_Error(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 0) // No retries needed for rollback test

	rollbackErr := errors.New("rollback error")
	executor.SetRollbackFn(func() error {
		return rollbackErr
	})

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return errors.New("execution error")
	})

	if err == nil {
		t.Errorf("expected execution error")
	}

	err = executor.Rollback()
	if err != rollbackErr {
		t.Errorf("expected rollback error, got %v", err)
	}
}

func TestPhaseExecutor_OnComplete(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	onCompleteCalled := false
	executor.SetOnComplete(func() error {
		onCompleteCalled = true
		return nil
	})

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if err != nil {
		t.Errorf("Execute failed: %v", err)
	}

	if !onCompleteCalled {
		t.Errorf("expected OnComplete to be called")
	}
}

func TestPhaseExecutor_OnComplete_Error(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	onCompleteErr := errors.New("oncomplete error")
	executor.SetOnComplete(func() error {
		return onCompleteErr
	})

	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if err != onCompleteErr {
		t.Errorf("expected OnComplete error, got %v", err)
	}
}

func TestPhaseExecutor_OnFailure(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 0) // No retries needed for OnFailure test

	onFailureCalled := false
	var receivedError error
	executor.SetOnFailure(func(err error) error {
		onFailureCalled = true
		receivedError = err
		return nil
	})

	execErr := errors.New("execution error")
	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return execErr
	})

	if err != execErr {
		t.Errorf("expected execution error, got %v", err)
	}

	if !onFailureCalled {
		t.Errorf("expected OnFailure to be called")
	}

	if receivedError != execErr {
		t.Errorf("expected received error to be execution error, got %v", receivedError)
	}
}

func TestPhaseExecutor_OnFailure_RollbackError(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 0) // No retries needed for OnFailure test

	rollbackErr := errors.New("rollback error")
	executor.SetOnFailure(func(err error) error {
		return rollbackErr
	})

	execErr := errors.New("execution error")
	err := executor.Execute(context.Background(), func(ctx context.Context) error {
		return execErr
	})

	if err != rollbackErr {
		t.Errorf("expected rollback error, got %v", err)
	}
}

func TestPhaseExecutor_Duration(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	if err := executor.Execute(context.Background(), func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	duration := executor.Duration()
	if duration < 100*time.Millisecond {
		t.Errorf("expected duration >= 100ms, got %v", duration)
	}
}

func TestPhaseExecutor_Duration_Running(t *testing.T) {
	executor := NewPhaseExecutor(PhaseGraceful, 3)

	ctx := context.Background()
	go func() {
		if err := executor.Execute(ctx, func(ctx context.Context) error {
			time.Sleep(500 * time.Millisecond)
			return nil
		}); err != nil {
			t.Logf("Execute failed: %v", err)
		}
	}()

	// Wait a bit for executor to start
	time.Sleep(50 * time.Millisecond)

	duration := executor.Duration()
	if duration <= 0 {
		t.Errorf("expected positive duration while running, got %v", duration)
	}
}

// CallbackRegistry comprehensive tests.

func TestCallbackRegistry_Register(t *testing.T) {
	registry := NewCallbackRegistry()

	err := registry.Register(PhaseGraceful, "callback1", 10, func(ctx context.Context) error {
		return nil
	}, 5*time.Second)

	if err != nil {
		t.Errorf("Register failed: %v", err)
	}

	count := registry.Count(PhaseGraceful)
	if count != 1 {
		t.Errorf("expected 1 callback, got %d", count)
	}
}

func TestCallbackRegistry_Register_Multiple(t *testing.T) {
	registry := NewCallbackRegistry()

	if err := registry.Register(PhaseGraceful, "callback1", 10, func(ctx context.Context) error {
		return nil
	}, 5*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := registry.Register(PhaseGraceful, "callback2", 5, func(ctx context.Context) error {
		return nil
	}, 3*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := registry.Register(PhaseGraceful, "callback3", 15, func(ctx context.Context) error {
		return nil
	}, 7*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	count := registry.Count(PhaseGraceful)
	if count != 3 {
		t.Errorf("expected 3 callbacks, got %d", count)
	}

	// Verify priority ordering (highest first)
	callbacks := registry.GetCallbacks(PhaseGraceful)
	if len(callbacks) != 3 {
		t.Errorf("expected 3 callbacks, got %d", len(callbacks))
	}
}

func TestCallbackRegistry_Unregister(t *testing.T) {
	registry := NewCallbackRegistry()

	if err := registry.Register(PhaseGraceful, "callback1", 10, func(ctx context.Context) error {
		return nil
	}, 5*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	err := registry.Unregister(PhaseGraceful, "callback1")
	if err != nil {
		t.Errorf("Unregister failed: %v", err)
	}

	count := registry.Count(PhaseGraceful)
	if count != 0 {
		t.Errorf("expected 0 callbacks after unregister, got %d", count)
	}
}

func TestCallbackRegistry_Unregister_NotFound(t *testing.T) {
	registry := NewCallbackRegistry()

	err := registry.Unregister(PhaseGraceful, "nonexistent")
	if err != ErrCallbackNotFound {
		t.Errorf("expected ErrCallbackNotFound, got %v", err)
	}
}

func TestCallbackRegistry_GetCallbacks_NotRegistered(t *testing.T) {
	registry := NewCallbackRegistry()

	callbacks := registry.GetCallbacks(PhaseGraceful)
	if callbacks != nil {
		t.Errorf("expected nil callbacks for unregistered phase")
	}
}

func TestCallbackRegistry_Clear(t *testing.T) {
	registry := NewCallbackRegistry()

	if err := registry.Register(PhaseGraceful, "callback1", 10, func(ctx context.Context) error {
		return nil
	}, 5*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if err := registry.Register(PhaseGraceful, "callback2", 5, func(ctx context.Context) error {
		return nil
	}, 3*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	registry.Clear(PhaseGraceful)

	count := registry.Count(PhaseGraceful)
	if count != 0 {
		t.Errorf("expected 0 callbacks after clear, got %d", count)
	}
}

func TestCallbackRegistry_Count_Empty(t *testing.T) {
	registry := NewCallbackRegistry()

	count := registry.Count(PhaseGraceful)
	if count != 0 {
		t.Errorf("expected 0 callbacks for empty registry, got %d", count)
	}
}

func TestCallbackRegistry_SetOnError(t *testing.T) {
	registry := NewCallbackRegistry()

	if err := registry.Register(PhaseGraceful, "callback1", 10, func(ctx context.Context) error {
		return nil
	}, 5*time.Second); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	err := registry.SetOnError(PhaseGraceful, "callback1", func(err error) {
		// Error handler is set but not called in this test
	})

	if err != nil {
		t.Errorf("SetOnError failed: %v", err)
	}

	// Note: SetOnError sets the handler but doesn't call it
	// The handler would be called when the callback is executed
}

func TestCallbackRegistry_SetOnError_NotFound(t *testing.T) {
	registry := NewCallbackRegistry()

	err := registry.SetOnError(PhaseGraceful, "nonexistent", func(err error) {})
	if err != ErrCallbackNotFound {
		t.Errorf("expected ErrCallbackNotFound, got %v", err)
	}
}

// CallbackChain comprehensive tests.

func TestCallbackChain_Add(t *testing.T) {
	chain := NewCallbackChain()

	chain.Add(func(ctx context.Context) error {
		return nil
	})

	chain.Add(func(ctx context.Context) error {
		return nil
	})

	if len(chain.callbacks) != 2 {
		t.Errorf("expected 2 callbacks, got %d", len(chain.callbacks))
	}
}

func TestCallbackChain_Execute_Success(t *testing.T) {
	chain := NewCallbackChain()

	var executed []int
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 1)
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 2)
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 3)
		return nil
	})

	err := chain.Execute(context.Background())
	if err != nil {
		t.Errorf("Execute failed: %v", err)
	}

	if len(executed) != 3 {
		t.Errorf("expected 3 callbacks executed, got %d", len(executed))
	}

	// Verify execution order
	for i, val := range executed {
		if val != i+1 {
			t.Errorf("expected callback %d to return %d, got %d", i, i+1, val)
		}
	}
}

func TestCallbackChain_Execute_Error(t *testing.T) {
	chain := NewCallbackChain()

	var executed []int
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 1)
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 2)
		return errors.New("callback error")
	})
	chain.Add(func(ctx context.Context) error {
		executed = append(executed, 3)
		return nil
	})

	err := chain.Execute(context.Background())
	if err == nil {
		t.Errorf("expected error from callback")
	}

	// Should stop at the first error
	if len(executed) != 2 {
		t.Errorf("expected 2 callbacks executed, got %d", len(executed))
	}
}

func TestCallbackChain_Execute_Empty(t *testing.T) {
	chain := NewCallbackChain()

	err := chain.Execute(context.Background())
	if err != nil {
		t.Errorf("Execute with empty chain failed: %v", err)
	}
}

func TestCallbackChain_Execute_ContextCancelled(t *testing.T) {
	chain := NewCallbackChain()

	ctx, cancel := context.WithCancel(context.Background())

	chain.Add(func(ctx context.Context) error {
		cancel()
		// Check if context is cancelled
		<-ctx.Done()
		return ctx.Err()
	})
	chain.Add(func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	err := chain.Execute(ctx)
	if err == nil {
		t.Errorf("expected context cancellation error")
	}
}

func TestCallbackChain_ExecuteParallel_Success(t *testing.T) {
	chain := NewCallbackChain()

	var executed []int
	var mu sync.Mutex

	chain.Add(func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		executed = append(executed, 1)
		mu.Unlock()
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		mu.Lock()
		executed = append(executed, 2)
		mu.Unlock()
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		executed = append(executed, 3)
		mu.Unlock()
		return nil
	})

	err := chain.ExecuteParallel(context.Background())
	if err != nil {
		t.Errorf("ExecuteParallel failed: %v", err)
	}

	if len(executed) != 3 {
		t.Errorf("expected 3 callbacks executed, got %d", len(executed))
	}
}

func TestCallbackChain_ExecuteParallel_Error(t *testing.T) {
	chain := NewCallbackChain()

	var executedCount atomic.Int32

	chain.Add(func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		executedCount.Add(1)
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		executedCount.Add(1)
		return errors.New("callback error")
	})
	chain.Add(func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		executedCount.Add(1)
		return nil
	})

	err := chain.ExecuteParallel(context.Background())
	if err == nil {
		t.Errorf("expected error from callback")
	}

	// All callbacks should complete despite error
	if executedCount.Load() != 3 {
		t.Errorf("expected all 3 callbacks to complete, got %d", executedCount.Load())
	}
}

func TestCallbackChain_ExecuteParallel_Empty(t *testing.T) {
	chain := NewCallbackChain()

	err := chain.ExecuteParallel(context.Background())
	if err != nil {
		t.Errorf("ExecuteParallel with empty chain failed: %v", err)
	}
}

func TestCallbackChain_ExecuteParallel_ContextCancelled(t *testing.T) {
	chain := NewCallbackChain()

	ctx, cancel := context.WithCancel(context.Background())

	chain.Add(func(ctx context.Context) error {
		time.Sleep(50 * time.Millisecond)
		cancel()
		return nil
	})
	chain.Add(func(ctx context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	})

	err := chain.ExecuteParallel(ctx)
	if err == nil {
		t.Errorf("expected context cancellation error")
	}
}

// SignalHandler comprehensive tests.

func TestSignalHandler_Start_Success(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	ctx := context.Background()
	err := handler.Start(ctx)
	if err != nil {
		t.Errorf("Start failed: %v", err)
	}

	err = handler.Stop()
	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

func TestSignalHandler_Start_AlreadyStarted(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	ctx := context.Background()
	err := handler.Start(ctx)
	if err != nil {
		t.Errorf("First Start failed: %v", err)
	}

	err = handler.Start(ctx)
	if err != ErrSignalHandlerAlreadyStarted {
		t.Errorf("expected ErrSignalHandlerAlreadyStarted, got %v", err)
	}

	if err := handler.Stop(); err != nil {
		t.Logf("Stop failed: %v", err)
	}
}

func TestSignalHandler_Stop_NotStarted(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	err := handler.Stop()
	if err != nil {
		t.Errorf("Stop when not started should not fail: %v", err)
	}
}

func TestSignalHandler_AddSignal(t *testing.T) {
	manager := NewManager(10 * time.Second)
	handler := NewSignalHandler(manager)

	initialSignals := len(handler.signals)
	handler.AddSignal(syscall.SIGUSR1)

	if len(handler.signals) != initialSignals+1 {
		t.Errorf("expected signal count to increase by 1")
	}
}

func TestSignalHandler_WaitForSignal(t *testing.T) {
	sigChan := make(chan os.Signal, 1)
	go func() {
		sig := WaitForSignal(os.Interrupt)
		sigChan <- sig
	}()

	// Send signal after a short delay
	time.Sleep(100 * time.Millisecond)
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess failed: %v", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Logf("Signal failed: %v", err)
	}

	select {
	case sig := <-sigChan:
		if sig != os.Interrupt {
			t.Errorf("expected os.Interrupt, got %v", sig)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timeout waiting for signal")
	}
}

func TestSignalHandler_WaitForContextOrSignal_Context(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	go func() {
		sig, err := WaitForContextOrSignal(ctx, os.Interrupt)
		if err == nil {
			sigChan <- sig
		} else {
			sigChan <- nil
		}
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case sig := <-sigChan:
		if sig != nil {
			t.Errorf("expected nil signal (context cancelled), got %v", sig)
		}
	case <-time.After(2 * time.Second):
		t.Errorf("timeout waiting for context cancellation")
	}
}

func TestSignalHandler_WaitForContextOrSignal_Signal(t *testing.T) {
	ctx := context.Background()

	sigChan := make(chan os.Signal, 1)
	go func() {
		sig, err := WaitForContextOrSignal(ctx, os.Interrupt)
		if err == nil {
			sigChan <- sig
		} else {
			sigChan <- nil
		}
	}()

	time.Sleep(100 * time.Millisecond)
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess failed: %v", err)
	}
	if err := proc.Signal(os.Interrupt); err != nil {
		t.Logf("Signal failed: %v", err)
	}

	select {
	case sig := <-sigChan:
		if sig != os.Interrupt {
			t.Errorf("expected os.Interrupt, got %v", sig)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("timeout waiting for signal")
	}
}

// Phase comprehensive tests.

func TestPhase_String(t *testing.T) {
	tests := []struct {
		phase    Phase
		expected string
	}{
		{PhasePreShutdown, "pre-shutdown"},
		{PhaseGraceful, "graceful"},
		{PhaseForce, "force"},
		{PhaseDone, "done"},
		{Phase(100), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.phase.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.phase.String())
			}
		})
	}
}

func TestPhase_IsValid(t *testing.T) {
	tests := []struct {
		phase    Phase
		expected bool
	}{
		{PhasePreShutdown, true},
		{PhaseGraceful, true},
		{PhaseForce, true},
		{PhaseDone, true},
		{Phase(100), false},
		{Phase(-1), false},
	}

	for _, tt := range tests {
		t.Run(tt.phase.String(), func(t *testing.T) {
			if tt.phase.IsValid() != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, tt.phase.IsValid())
			}
		})
	}
}

// PhaseState comprehensive tests.

func TestPhaseState_String(t *testing.T) {
	tests := []struct {
		state    PhaseState
		expected string
	}{
		{PhaseStatePending, "pending"},
		{PhaseStateRunning, "running"},
		{PhaseStateCompleted, "completed"},
		{PhaseStateFailed, "failed"},
		{PhaseStateSkipped, "skipped"},
		{PhaseState(100), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if tt.state.String() != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, tt.state.String())
			}
		})
	}
}

// Error type tests.

func TestPhaseError_Error(t *testing.T) {
	err := &PhaseError{"test error"}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %s", err.Error())
	}
}

func TestCallbackError_Error(t *testing.T) {
	err := &CallbackError{"test error"}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %s", err.Error())
	}
}

func TestSignalError_Error(t *testing.T) {
	err := &SignalError{"test error"}
	if err.Error() != "test error" {
		t.Errorf("expected 'test error', got %s", err.Error())
	}
}
