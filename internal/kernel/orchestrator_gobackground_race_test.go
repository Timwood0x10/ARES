package kernel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

// TestOrchestrator_GoBackground_RacingShutdownIsAlwaysAccountedFor locks the
// admission race between GoBackground and Shutdown.
//
// GoBackground used to read o.stopped under the mutex, RELEASE it, and only
// then call errgroup.Go. Shutdown sets the flag under the same mutex and
// later drains the errgroup with Wait. A loop that passed the flag check
// just before Shutdown's Wait completed therefore joined the errgroup AFTER
// Wait had already returned — which the errgroup contract forbids ("Go must
// not be called once Wait has returned"), and which left the loop running
// with nobody waiting on it. Adopt already closed the equivalent window by
// sharing one critical section with Shutdown's flag set
// (orchestrator.go:438-449); GoBackground did not.
//
// Detector: the loop records whether it observed Shutdown already returned.
// That is a sound late-admission signal, because errgroup.Wait cannot
// return while a registered goroutine is still running — so a loop that was
// correctly admitted before Shutdown's Wait always enters first. Only a loop
// admitted through the racy window can start after Shutdown has returned.
func TestOrchestrator_GoBackground_RacingShutdownIsAlwaysAccountedFor(t *testing.T) {
	const iterations = 500

	var refused, joined int
	for i := 0; i < iterations; i++ {
		rootCtx, cancel := context.WithCancel(context.Background())
		reg := NewRegistry()
		if err := reg.Register(&lifecycleComp{name: "loop-owner"}, ModeRequired); err != nil {
			cancel()
			t.Fatalf("Register: %v", err)
		}
		o := NewOrchestrator(reg, rootCtx)
		if err := o.Start(context.Background()); err != nil {
			cancel()
			t.Fatalf("Start: %v", err)
		}

		var shutdownDone atomic.Bool
		var started atomic.Bool
		var lateAdmission atomic.Bool

		var wg sync.WaitGroup
		wg.Add(2)
		// Overlapping on purpose: GoBackground's flag check races
		// Shutdown's flag set.
		go func() {
			defer wg.Done()
			o.GoBackground("loop-owner", func(ctx context.Context) error {
				if shutdownDone.Load() {
					lateAdmission.Store(true)
				}
				started.Store(true)
				<-ctx.Done()
				return nil
			})
		}()
		go func() {
			defer wg.Done()
			if err := o.Shutdown(context.Background()); err != nil {
				// The loop respects ctx, so a healthy teardown reports
				// nothing. Surface anything unexpected.
				t.Errorf("iteration %d: Shutdown: %v", i, err)
			}
			shutdownDone.Store(true)
		}()
		wg.Wait()
		cancel()

		switch {
		case lateAdmission.Load():
			t.Fatalf("iteration %d: GoBackground admitted a loop AFTER Shutdown had already drained the errgroup — "+
				"the stopped-flag check and errgroup.Go are not one critical section", i)
		case started.Load():
			joined++
		default:
			refused++
		}
	}

	t.Logf("joined=%d refused=%d of %d", joined, refused, iterations)
}
