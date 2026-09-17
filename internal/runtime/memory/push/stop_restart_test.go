// stop_restart_test.go locks REVIEW 3.3#2: Stop's post-drain state reset
// was unconditional, so a Stop that was mid-flight while the loop exited
// and a concurrent Start() armed a NEW loop would clobber that loop's
// lifecycle state (isRunning=false, cancelFn=nil). The orphaned loop kept
// running with no cancel handle, and the next Start() spawned a SECOND
// concurrent loop. The fix removes the reset (finishLoop owns it — it
// always runs before <-doneCh returns).
//
// Reproduction shape per round: the scheduled loop is cancelled while a
// push batch is still in flight (the provider's 1ms sleep pins it), so
// Stop is genuinely parked on <-doneCh when finishLoop resets the state.
// A tight-loop Start retry arms the replacement loop in that instant; the
// pre-fix Stop then clobbers it on resume. With the fix, the round ends
// fully drained and the provider call count is stable afterwards.
package push

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingProvider struct {
	calls atomic.Int64
}

func (p *countingProvider) ListKnowledge(ctx context.Context) ([]KnowledgeItem, error) {
	p.calls.Add(1)
	// Small delay so the loop stays busy (Stop parks) and an orphaned loop
	// produces a steady, detectable call rate.
	time.Sleep(time.Millisecond)
	return []KnowledgeItem{}, nil
}

type nullTarget struct{}

func (nullTarget) ID() string                  { return "null" }
func (nullTarget) Criteria() RelevanceCriteria { return RelevanceCriteria{} }
func (nullTarget) Deliver(ctx context.Context, item KnowledgeItem) error {
	return nil
}

// waitForProviderCall blocks until the provider has been called at least
// once (the scheduled loop is inside its first batch).
func waitForProviderCall(p *countingProvider) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.calls.Load() > 0 {
			return true
		}
		time.Sleep(200 * time.Microsecond)
	}
	return false
}

func TestPushServiceStopDoesNotOrphanRestartedLoop(t *testing.T) {
	provider := &countingProvider{}
	ctx := context.Background()

	const rounds = 15
	for round := 0; round < rounds; round++ {
		svc, err := NewPushService(provider, &PushConfig{
			Policy: PolicyScheduled,
			// 1ms interval so the loop is promptly inside a batch and a
			// leaked loop is detected quickly.
			Interval: time.Millisecond,
		})
		if err != nil {
			t.Fatalf("round %d: new service: %v", round, err)
		}
		svc.RegisterTarget(nullTarget{})

		if err := svc.Start(ctx); err != nil {
			t.Fatalf("round %d: initial start: %v", round, err)
		}

		// Barrier: the loop must be inside a push batch so Stop's cancel
		// leaves it alive and Stop genuinely parks on <-doneCh.
		if !waitForProviderCall(provider) {
			t.Fatalf("round %d: scheduled loop never called the provider", round)
		}

		// Race Stop against a tight-loop Start retry: the retry succeeds
		// the instant finishLoop resets isRunning, while Stop is still
		// parked — exactly the clobber window.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			svc.Stop()
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 500000; i++ {
				if svc.Start(ctx) == nil {
					return
				}
				runtime.Gosched()
			}
		}()
		wg.Wait()

		// Drain whatever the round left armed. Post-fix this fully stops
		// the service; pre-fix a clobbered loop survives it (isRunning was
		// reset to false, so Stop is a no-op) and keeps calling the
		// provider.
		svc.Stop()
	}

	// Leak detection: with every loop properly stopped, the provider call
	// count must be stable.
	base := provider.calls.Load()
	time.Sleep(200 * time.Millisecond)
	after := provider.calls.Load()
	if after != base {
		t.Fatalf("push loop leaked across Stop/restart rounds: provider calls grew from %d to %d after all services were stopped",
			base, after)
	}
}
