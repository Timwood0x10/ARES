package evolution

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// TestStrategyLifecycle_Submit_PendingApprovalHeldIDReadUnderLock locks the
// race fix in the pending-approval reject path (REVIEW 2.4#23):
// heldCandidateIDLocked must be called while STILL holding l.mu. The old
// code called it after Unlock, so a concurrent Approve (which nils
// heldCandidate under the lock) raced with Submit's unlocked read of the
// same field. Run under -race this test fails without the capture-before-
// unlock fix.
func TestStrategyLifecycle_Submit_PendingApprovalHeldIDReadUnderLock(t *testing.T) {
	cfg := DefaultLifecycleConfig()
	cfg.Gates.RequireManualApproval = true

	lc, asm, _ := newTestLifecycle(t, cfg)
	require.NoError(t, asm.Deploy(context.Background(),
		&mutation.Strategy{ID: "base", Version: 1, Score: 50.0},
	))

	lc.Submit(context.Background(), &mutation.Strategy{ID: "held", Version: 2, Score: 80.0}, 1)
	require.True(t, lc.Snapshot().PendingApproval)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Writer side: hammer Submit — each call takes the pending-approval
	// reject path that reads the held candidate's ID.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				lc.Submit(context.Background(),
					&mutation.Strategy{ID: "racing", Version: 3, Score: 70.0}, 2)
			}
		}(i)
	}

	// Mutator side: repeatedly re-hold and release a candidate so
	// heldCandidate/pendingApproval churn under the lock.
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				lc.mu.Lock()
				lc.pendingApproval = true
				lc.heldCandidate = &mutation.Strategy{ID: "reheld", Version: 4, Score: 60.0}
				lc.mu.Unlock()
				lc.mu.Lock()
				lc.heldCandidate = nil
				lc.pendingApproval = false
				lc.mu.Unlock()
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
