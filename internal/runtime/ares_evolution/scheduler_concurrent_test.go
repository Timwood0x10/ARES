package evolution

import (
	"context"
	"sync"
	"testing"
	"time"
)

// countingScoreProvider records TaskScore calls for the concurrency test.
type countingScoreProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *countingScoreProvider) TaskScore(success bool) float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if success {
		return 1.0
	}
	return 0.0
}

// TestEvolutionScheduler_SettersConcurrentWithReaders locks the atomic-field
// fix (REVIEW 2.4#25): adapter / scoreProvider / dreamCycle are written by
// post-construction setters while live paths (task events, ticks) read them.
// The fields are atomic.Pointer; before the fix the reads raced with the
// unlocked or differently-locked writes. Run under -race.
func TestEvolutionScheduler_SettersConcurrentWithReaders(t *testing.T) {
	adapter := newMockAdapterForScheduler()
	scheduler := NewEvolutionScheduler(nil, adapter)
	scheduler.SetEnabled(true)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Reader 1: the subscription-loop path — taskScore reads scoreProvider
	// without any lock on every task event.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				scheduler.RecordScore(scheduler.taskScore(true))
				_ = scheduler.DreamCycle()
				scheduler.Tick(context.Background())
			}
		}
	}()

	// Reader 2: the agent-end path reads the adapter (nil check + Run) and
	// checkGuardrails type-switches on it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				scheduler.OnAgentEnd(context.Background(), CallbackData{AgentID: "race"})
			}
		}
	}()

	// Writer: swap all three wired dependencies continuously.
	wg.Add(1)
	go func() {
		defer wg.Done()
		provider := &countingScoreProvider{}
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			scheduler.SetAdapter(newMockAdapterForScheduler())
			scheduler.SetScoreProvider(provider)
			scheduler.SetDreamCycle(nil)
			if i%2 == 0 {
				scheduler.SetScoreProvider(nil)
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	time.Sleep(300 * time.Millisecond)
	close(stop)
	<-done
}
