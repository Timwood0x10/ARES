package kernel

// §3.1 (MEDIUM) regressions for the orchestrator and
// registry: the Adopt/Shutdown registration race, the post-registration
// shutdown guard, and atomic status updates.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockingDepsComp blocks inside Dependencies() until released — the
// deterministic lever for pausing Adopt between its shutdown pre-check and
// its registration (the exact pre-fix race window).
type blockingDepsComp struct {
	lifecycleComp
	entered chan struct{}
	release chan struct{}
}

func (c *blockingDepsComp) Dependencies() []string {
	close(c.entered)
	<-c.release
	return c.deps
}

// blockingBindComp blocks inside Bind() until released — pauses Adopt AFTER
// registration, so a concurrent Shutdown owns the (registered) component.
type blockingBindComp struct {
	lifecycleComp
	entered chan struct{}
	release chan struct{}
}

func (c *blockingBindComp) Bind(_ context.Context, _ Resolver) error {
	close(c.entered)
	<-c.release
	return c.bindErr
}

// blockingReadyComp blocks inside Ready() until released.
type blockingReadyComp struct {
	lifecycleComp
	entered chan struct{}
	release chan struct{}
}

func (c *blockingReadyComp) Ready(_ context.Context) error {
	close(c.entered)
	<-c.release
	return c.readyErr
}

// TestAdoptConcurrentShutdownNeverRegistersMissedComponent pins the
// Adopt/Shutdown race: an Adopt that passed its early shutdown check but has
// not registered yet must be refused once Shutdown has begun — the teardown
// always wins. Pre-fix, the component registered after Shutdown had already
// snapshotted its stop list, so it ran forever and never saw Stop.
func TestAdoptConcurrentShutdownNeverRegistersMissedComponent(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	requireNoErr(t, reg.Register(&lifecycleComp{name: "base"}, ModeRequired))
	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	late := &blockingDepsComp{
		lifecycleComp: lifecycleComp{name: "late"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	adopted := make(chan error, 1)
	go func() {
		adopted <- o.Adopt(context.Background(), late, ModeRequired)
	}()

	// Adopt passed its pre-flight checks and is now parked inside
	// Dependencies() — after the early check, before Register.
	select {
	case <-late.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt never reached Dependencies")
	}

	// Shutdown completes while Adopt is parked.
	if err := o.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Release Adopt: registration must be REFUSED, not silently land in a
	// torn-down graph.
	close(late.release)
	select {
	case err := <-adopted:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("Adopt racing Shutdown must return ErrShuttingDown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt did not return after release")
	}
	if _, ok := reg.GetStatus("late"); ok {
		t.Fatal("a component adopted during Shutdown must not be registered — it would never be stopped")
	}
}

// TestAdoptMidBindShutdownKeepsStoppedState pins the post-registration half:
// once registered, the component IS in the stop list; a Shutdown that stops
// it while Adopt is still inside Bind must not be overwritten by Adopt's
// later Started/Ready status writes.
func TestAdoptMidBindShutdownKeepsStoppedState(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	requireNoErr(t, reg.Register(&lifecycleComp{name: "base"}, ModeRequired))
	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	late := &blockingBindComp{
		lifecycleComp: lifecycleComp{name: "late"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	adopted := make(chan error, 1)
	go func() {
		adopted <- o.Adopt(context.Background(), late, ModeRequired)
	}()

	// Adopt has REGISTERED and is now parked inside Bind().
	select {
	case <-late.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt never reached Bind")
	}

	// Shutdown stops the registered component (its Stop is immediate).
	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- o.Shutdown(context.Background())
	}()
	// Wait for Shutdown to finish tearing down "late".
	select {
	case <-shutdownDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown never completed")
	}

	close(late.release)
	select {
	case err := <-adopted:
		if !errors.Is(err, ErrShuttingDown) {
			t.Fatalf("Adopt whose component was stopped mid-adoption must return ErrShuttingDown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt did not return after release")
	}

	st, ok := reg.GetStatus("late")
	if !ok {
		t.Fatal("registered component must be visible")
	}
	if st.State != StateStopped {
		t.Fatalf("a component Shutdown stopped must stay Stopped, got %s", st.State)
	}
}

// TestAdoptDoesNotResurrectFailedBackgroundLoop pins the lost-update fix on
// the final status write: a background loop that marks the component Failed
// while Adopt is inside Ready() must not be silently overwritten by Adopt's
// final Ready stamp. Pre-fix the unconditional setStatus(Ready) resurrected
// the dead component.
func TestAdoptDoesNotResurrectFailedBackgroundLoop(t *testing.T) {
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	reg := NewRegistry()
	o := NewOrchestrator(reg, rootCtx)
	if err := o.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	late := &blockingReadyComp{
		lifecycleComp: lifecycleComp{name: "late"},
		entered:       make(chan struct{}),
		release:       make(chan struct{}),
	}
	adopted := make(chan error, 1)
	go func() {
		adopted <- o.Adopt(context.Background(), late, ModeRequired)
	}()

	// Adopt registered, stamped Started, and is parked inside Ready().
	select {
	case <-late.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt never reached Ready")
	}

	// The component's background loop dies while Ready() is blocked.
	o.markBackgroundFailed("late", "loop died")

	close(late.release)
	select {
	case err := <-adopted:
		if err != nil {
			t.Fatalf("Adopt with a successful Ready must return nil, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Adopt did not return after release")
	}

	st, ok := reg.GetStatus("late")
	if !ok {
		t.Fatal("component must be registered")
	}
	if st.State != StateFailed {
		t.Fatalf("background-loop failure must survive adoption's final write, got %s", st.State)
	}
	if !strings.Contains(st.Reason, "loop died") {
		t.Fatalf("failure reason must survive, got %q", st.Reason)
	}
	if st.StartedAt.IsZero() {
		t.Fatal("StartedAt stamped before the failure must not be lost")
	}
}

// TestRegistryUpdateStatusIsAtomic pins the atomic-update primitive: N
// concurrent field-level appends must all be visible. A GetStatus → mutate
// copy → SetStatus implementation silently drops concurrent updates (lost
// update), which is exactly what the orchestrator's status writers did
// before UpdateStatus existed.
func TestRegistryUpdateStatusIsAtomic(t *testing.T) {
	reg := NewRegistry()
	requireNoErr(t, reg.Register(&lifecycleComp{name: "comp"}, ModeRequired))

	const writers = 64
	const writes = 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < writes; j++ {
				reg.UpdateStatus("comp", func(st *ComponentStatus) {
					st.Reason += "x"
				})
			}
		}()
	}
	wg.Wait()

	st, ok := reg.GetStatus("comp")
	if !ok {
		t.Fatal("component missing")
	}
	if got := len(st.Reason); got != writers*writes {
		t.Fatalf("UpdateStatus lost concurrent writes: reason length = %d, want %d", got, writers*writes)
	}
}
