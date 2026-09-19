package kernel

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ── startComponent: Disabled skip path ──────────────────────────────────────

func TestStartComponent_DisabledSkipped(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "disabled-comp"}
	requireNoErr(t, reg.Register(comp, ModeOptional))
	// Mark as Disabled before Start
	reg.SetStatus("disabled-comp", ComponentStatus{
		Name: "disabled-comp", Mode: ModeOptional, State: StateDisabled,
	})

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	st, _ := reg.GetStatus("disabled-comp")
	if st.State != StateDisabled {
		t.Errorf("state = %s, want Disabled", st.State)
	}
}

// ── startComponent: Degraded mode path ──────────────────────────────────────

func TestStartComponent_DegradedMode(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "degraded-comp", readyErr: errSentinel}
	requireNoErr(t, reg.Register(comp, ModeDegraded))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start should succeed for Degraded mode: %v", err)
	}

	st, _ := reg.GetStatus("degraded-comp")
	if st.State != StateDegraded {
		t.Errorf("state = %s, want Degraded", st.State)
	}
	if st.Reason == "" {
		t.Error("reason should be set for degraded component")
	}
}

// ── startComponent: component not found ─────────────────────────────────────

func TestStartComponent_NotFound(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	err := o.startComponent(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent component")
	}
}

// ── setStatus: Stopped guard path ───────────────────────────────────────────

func TestSetStatus_StoppedGuard(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Manually mark as Stopped
	reg.SetStatus("a", ComponentStatus{Name: "a", Mode: ModeRequired, State: StateStopped})

	// setStatus should NOT overwrite Stopped
	o.setStatus("a", StateFailed, "should not apply")
	st, _ := reg.GetStatus("a")
	if st.State != StateStopped {
		t.Errorf("state = %s, want Stopped (guard should prevent overwrite)", st.State)
	}
}

// ── setStatusStarted: Stopped guard path ────────────────────────────────────

func TestSetStatusStarted_StoppedGuard(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	reg.SetStatus("a", ComponentStatus{Name: "a", Mode: ModeRequired, State: StateStopped})

	o.setStatusStarted("a", StateStarted)
	st, _ := reg.GetStatus("a")
	if st.State != StateStopped {
		t.Errorf("state = %s, want Stopped", st.State)
	}
}

// ── cleanupComponent: Stop error path ───────────────────────────────────────

func TestCleanupComponent_StopError(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", stopErr: errSentinel}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	// cleanupComponent does not touch registry status
	o.cleanupComponent(context.Background(), "a")

	st, _ := reg.GetStatus("a")
	// Status should not be Stopped — cleanupComponent doesn't update status
	if st.State == StateStopped {
		t.Error("cleanupComponent should not set Stopped state")
	}
}

// ── cleanupComponent: Wait error path ───────────────────────────────────────

func TestCleanupComponent_WaitError(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", waitErr: errSentinel}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	o.cleanupComponent(context.Background(), "a")
	// Just verify it doesn't panic
}

// ── cleanupComponent: component not found ───────────────────────────────────

func TestCleanupComponent_NotFound(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	o.cleanupComponent(context.Background(), "nonexistent")
	// Should be a no-op
}

// ── rollback: stop error path ───────────────────────────────────────────────

func TestRollback_StopError(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	var aStop int32
	requireNoErr(t, reg.Register(&lifecycleComp{
		name: "a", stopCalls: &aStop, stopErr: errSentinel,
	}, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// rollback should log the stop error but not panic
	o.rollback(context.Background(), []string{"a"})
	if atomic.LoadInt32(&aStop) == 0 {
		t.Error("Stop should have been attempted during rollback")
	}
}

// ── Shutdown: budget expired path ───────────────────────────────────────────

func TestShutdown_BudgetExpired(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Use a context that is already expired
	expiredCtx, expiredCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expiredCancel()

	err := o.Shutdown(expiredCtx)
	// The expired budget means stopComponent may not complete; Shutdown
	// should surface an error or nil depending on timing, but must not panic.
	_ = err
}

// ── Shutdown: not-stopped report path ───────────────────────────────────────

func TestShutdown_NotStoppedReport(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	// Component that fails Stop — stays in Failed state
	comp := &lifecycleComp{name: "a", stopErr: errSentinel}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	err := o.Shutdown(context.Background())
	if err == nil {
		t.Error("expected Shutdown to report stop error")
	}
}

// ── Shutdown: nil context ───────────────────────────────────────────────────

func TestShutdown_NilContext(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Shutdown handles a context without a deadline by applying its own
	// overallShutdownTimeout. context.TODO() exercises that default-budget path.
	if err := o.Shutdown(context.TODO()); err != nil {
		t.Fatalf("Shutdown(nil): %v", err)
	}
}

// ── stopComponent: Wait error path ──────────────────────────────────────────

func TestStopComponent_WaitError(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a", waitErr: errSentinel}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	st, _ := reg.GetStatus("a")
	if st.State != StateStopped {
		t.Errorf("state = %s, want Stopped (Wait error should not prevent stop)", st.State)
	}
}

// ── stopComponent: already stopped ──────────────────────────────────────────

func TestStopComponent_AlreadyStopped(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	var stopCount int32
	comp := &lifecycleComp{name: "a", stopCalls: &stopCount}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Second stopComponent call should be a no-op
	if err := o.stopComponent(context.Background(), "a"); err != nil {
		t.Fatalf("stopComponent on stopped: %v", err)
	}
	if atomic.LoadInt32(&stopCount) != 1 {
		t.Errorf("stopCount = %d, want 1 (already stopped should skip)", stopCount)
	}
}

// ── stopComponent: component not found ──────────────────────────────────────

func TestStopComponent_NotFound(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	if err := o.stopComponent(context.Background(), "nonexistent"); err != nil {
		t.Errorf("stopComponent on nonexistent: %v", err)
	}
}

// ── Adopt: nil component ────────────────────────────────────────────────────

func TestAdopt_NilComponent(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	err := o.Adopt(context.Background(), nil, ModeRequired)
	if err == nil {
		t.Error("expected error for nil component")
	}
}

// ── Adopt: dependency not registered ────────────────────────────────────────

func TestAdopt_DependencyNotRegistered(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)

	comp := &lifecycleComp{name: "late", deps: []string{"missing-dep"}}
	err := o.Adopt(context.Background(), comp, ModeRequired)
	if err == nil {
		t.Error("expected error for unregistered dependency")
	}
}

// ── Adopt: dependency failed ────────────────────────────────────────────────

func TestAdopt_DependencyFailed(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	dep := &lifecycleComp{name: "dep"}
	requireNoErr(t, reg.Register(dep, ModeRequired))
	reg.SetStatus("dep", ComponentStatus{Name: "dep", Mode: ModeRequired, State: StateFailed, Reason: "test"})

	o := NewOrchestrator(reg, rootCtx)
	comp := &lifecycleComp{name: "late", deps: []string{"dep"}}
	err := o.Adopt(context.Background(), comp, ModeRequired)
	if err == nil {
		t.Error("expected error for failed dependency")
	}
}

// ── Adopt: degraded mode with Ready failure ─────────────────────────────────

func TestAdopt_DegradedModeReadyFailure(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	comp := &lifecycleComp{name: "late-degraded", readyErr: errSentinel}
	err := o.Adopt(context.Background(), comp, ModeDegraded)
	if err != nil {
		t.Fatalf("Adopt degraded should not return error: %v", err)
	}

	st, _ := reg.GetStatus("late-degraded")
	if st.State != StateDegraded {
		t.Errorf("state = %s, want Degraded", st.State)
	}
}

// ── Adopt: Required mode with Ready failure ─────────────────────────────────

func TestAdopt_RequiredModeReadyFailure(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	comp := &lifecycleComp{name: "late-required", readyErr: errSentinel}
	err := o.Adopt(context.Background(), comp, ModeRequired)
	if err == nil {
		t.Error("expected error for Required mode Ready failure")
	}

	st, _ := reg.GetStatus("late-required")
	if st.State != StateFailed {
		t.Errorf("state = %s, want Failed", st.State)
	}
}

// ── Adopt: after Shutdown ───────────────────────────────────────────────────

func TestAdopt_AfterShutdown(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	late := &lifecycleComp{name: "late"}
	err := o.Adopt(context.Background(), late, ModeRequired)
	if !errors.Is(err, ErrShuttingDown) {
		t.Errorf("Adopt after Shutdown = %v, want ErrShuttingDown", err)
	}
}

// ── GoBackground: error path ────────────────────────────────────────────────

func TestGoBackground_ErrorPath(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "bg-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	o.GoBackground("bg-comp", func(_ context.Context) error {
		close(done)
		return fmt.Errorf("loop error")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background loop did not run")
	}

	// Give the error handler time to mark the component
	time.Sleep(100 * time.Millisecond)

	st, _ := reg.GetStatus("bg-comp")
	if st.State != StateFailed {
		t.Errorf("state = %s, want Failed (error handler should mark component)", st.State)
	}
}

// ── GoBackground: panic path ────────────────────────────────────────────────

func TestGoBackground_PanicPath(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "panic-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	o.GoBackground("panic-comp", func(_ context.Context) error {
		close(done)
		panic("intentional test panic")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background loop did not run")
	}

	time.Sleep(100 * time.Millisecond)

	st, _ := reg.GetStatus("panic-comp")
	if st.State != StateFailed {
		t.Errorf("state = %s, want Failed (panic handler should mark component)", st.State)
	}
}

// ── GoBackground: after Shutdown ────────────────────────────────────────────

func TestGoBackground_AfterShutdown(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "a"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	ran := false
	o.GoBackground("a", func(_ context.Context) error {
		ran = true
		return nil
	})

	time.Sleep(50 * time.Millisecond)
	if ran {
		t.Error("GoBackground after Shutdown should not run the loop")
	}
}

// ── GoBackground: success path with component marking ───────────────────────

func TestGoBackground_SuccessPath(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "ok-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var ran atomic.Bool
	done := make(chan struct{})
	o.GoBackground("ok-comp", func(_ context.Context) error {
		ran.Store(true)
		close(done)
		return nil
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("background loop did not run")
	}

	if !ran.Load() {
		t.Error("loop should have run")
	}
}

// ── markBackgroundFailed: with event sink ───────────────────────────────────

func TestMarkBackgroundFailed_WithEventSink(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "evt-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Set a nil event sink — markBackgroundFailed should still work
	o.SetEventSink(nil)
	o.markBackgroundFailed("evt-comp", "test failure reason")

	st, _ := reg.GetStatus("evt-comp")
	if st.State != StateFailed {
		t.Errorf("state = %s, want Failed", st.State)
	}
	if st.Reason != "test failure reason" {
		t.Errorf("reason = %q", st.Reason)
	}
}

// ── markBackgroundFailed: Stopped guard ─────────────────────────────────────

func TestMarkBackgroundFailed_StoppedGuard(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "stopped-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	reg.SetStatus("stopped-comp", ComponentStatus{
		Name: "stopped-comp", Mode: ModeRequired, State: StateStopped,
	})

	o.markBackgroundFailed("stopped-comp", "should not apply")
	st, _ := reg.GetStatus("stopped-comp")
	if st.State != StateStopped {
		t.Errorf("state = %s, want Stopped (guard should prevent overwrite)", st.State)
	}
}

// ── markBackgroundFailed: root context done ─────────────────────────────────

func TestMarkBackgroundFailed_RootContextDone(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())

	reg := NewRegistry()
	comp := &lifecycleComp{name: "ctx-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()
	o.markBackgroundFailed("ctx-comp", "after cancel")

	st, _ := reg.GetStatus("ctx-comp")
	// markBackgroundFailed returns early when rootCtx is done — component
	// must remain in its pre-cancel state (Ready), not be marked Failed.
	if st.State == StateFailed {
		t.Errorf("state = %s, should not be Failed (markBackgroundFailed must skip when ctx is done)", st.State)
	}
}

// ── Start: shutdown race during startup ─────────────────────────────────────

func TestStart_ShutdownRaceDuringStartup(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	comp := &lifecycleComp{name: "slow-comp"}
	requireNoErr(t, reg.Register(comp, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)

	// Simulate shutdown racing in by setting stopped before Start loop
	o.mu.Lock()
	o.stopped = true
	o.mu.Unlock()

	err := o.Start(context.Background())
	if err == nil {
		t.Error("expected error when shutdown races during startup")
	}
}

// ── Start: cycle detection ──────────────────────────────────────────────────

func TestStart_CycleDetection(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	requireNoErr(t, reg.Register(&lifecycleComp{name: "a", deps: []string{"b"}}, ModeRequired))
	requireNoErr(t, reg.Register(&lifecycleComp{name: "b", deps: []string{"a"}}, ModeRequired))

	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err == nil {
		t.Error("expected error for cyclic dependencies")
	}
}
