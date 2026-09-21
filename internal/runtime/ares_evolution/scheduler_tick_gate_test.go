package evolution

import (
	"context"
	"sync/atomic"
	"testing"
)

// tickProbeAdapter counts Run invocations.
type tickProbeAdapter struct{ runs atomic.Int64 }

func (a *tickProbeAdapter) Run(context.Context) error { a.runs.Add(1); return nil }

// TestTickFiresOnTriggerOnIdleGate locks the serve ticker gate contract:
// wired.Scheduler (EnableScheduler=true, TriggerOnIdle) runs the population
// adapter on Tick only when shouldEvolve passes — score count ≥ 20 AND
// (recent-score degradation ≥ 15% OR count ≥ 100). The GA soak found
// generation stuck at 0; this test pins the gate's truth table so a soak
// failure can be attributed to EVENT DELIVERY (scores never recorded) rather
// than gate logic.
func TestTickFiresOnTriggerOnIdleGate(t *testing.T) {
	newSched := func(t *testing.T) (*EvolutionScheduler, *tickProbeAdapter) {
		t.Helper()
		adapter := &tickProbeAdapter{}
		s := NewEvolutionScheduler(nil, adapter,
			WithTrigger(TriggerOnIdle), WithMinInterval(0), WithEnabled(true))
		return s, adapter
	}

	t.Run("degradation after score floor fires", func(t *testing.T) {
		s, adapter := newSched(t)
		for i := 0; i < 25; i++ {
			s.RecordScore(taskScoreSuccess)
		}
		for i := 0; i < 10; i++ {
			s.RecordScore(taskScoreFailure)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() == 0 {
			t.Fatal("count=35 with all-recent failures must fire (drop=1.0 >= 0.15)")
		}
	})

	t.Run("below reliability floor does not fire", func(t *testing.T) {
		s, adapter := newSched(t)
		for i := 0; i < 15; i++ {
			s.RecordScore(taskScoreFailure)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() != 0 {
			t.Fatalf("count=15 < 20 reliability floor must not fire, runs=%d", adapter.runs.Load())
		}
	})

	t.Run("all-failure window fires at reliability floor", func(t *testing.T) {
		s, adapter := newSched(t)
		for i := 0; i < 25; i++ {
			s.RecordScore(taskScoreFailure)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() == 0 {
			t.Fatal("avg==0 all-failure window must fire at count>=20 — broken fleet is when GA must explore")
		}
	})

	t.Run("all-success below periodic threshold does not fire", func(t *testing.T) {
		s, adapter := newSched(t)
		for i := 0; i < 30; i++ {
			s.RecordScore(taskScoreSuccess)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() != 0 {
			t.Fatalf("count=30 all-success < periodic threshold must not fire, runs=%d", adapter.runs.Load())
		}
	})

	t.Run("periodic threshold fires without degradation", func(t *testing.T) {
		s, adapter := newSched(t)
		// The threshold must be observable within the score window cap:
		// RecordScore evicts beyond scoreWindowSize (50), so a threshold
		// above the cap would be unreachable dead config.
		if periodicEvolutionScoreThreshold >= scoreWindowSize {
			t.Fatalf("periodicEvolutionScoreThreshold=%d must stay below scoreWindowSize=%d",
				periodicEvolutionScoreThreshold, scoreWindowSize)
		}
		for i := 0; i < periodicEvolutionScoreThreshold; i++ {
			s.RecordScore(taskScoreSuccess)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() == 0 {
			t.Fatalf("count=%d periodic threshold must fire", periodicEvolutionScoreThreshold)
		}
	})

	t.Run("disabled scheduler never fires", func(t *testing.T) {
		adapter := &tickProbeAdapter{}
		s := NewEvolutionScheduler(nil, adapter,
			WithTrigger(TriggerOnIdle), WithMinInterval(0), WithEnabled(false))
		for i := 0; i < 100; i++ {
			s.RecordScore(taskScoreSuccess)
		}
		s.Tick(context.Background())
		if adapter.runs.Load() != 0 {
			t.Fatal("disabled scheduler must ignore Tick")
		}
	})
}

// panickingAdapter simulates a genome/diff panic inside the evolution cycle.
type panickingAdapter struct{}

func (a *panickingAdapter) Run(context.Context) error {
	panic("adapter exploded")
}

// TestTickRecoversFromAdapterPanic locks the serve-liveness contract: a
// panic inside the Tick-triggered evolution run must NOT kill the process
// (errgroup never recovers panics — GA soak 2026-09-21 lost serve to
// rand.Intn(0) in WorkflowGenome.mutateInsertNode). Recover → log → return;
// lastRun stays stale so the next tick retries.
func TestTickRecoversFromAdapterPanic(t *testing.T) {
	bad := &panickingAdapter{}
	s := NewEvolutionScheduler(nil, bad,
		WithTrigger(TriggerOnIdle), WithMinInterval(0), WithEnabled(true))
	for i := 0; i < 40; i++ {
		s.RecordScore(taskScoreFailure)
	}
	// Must return (not crash) despite the adapter panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Tick(context.Background())
	}()
	<-done

	// lastRun must remain unset so the next tick is eligible to retry.
	s.mu.Lock()
	last := s.lastRun
	s.mu.Unlock()
	if !last.IsZero() {
		t.Fatalf("lastRun=%v, want zero after a panicked run (next tick must retry)", last)
	}

	// Swap in a healthy adapter — the retry path must work.
	healthy := &tickProbeAdapter{}
	s.SetAdapter(healthy)
	s.Tick(context.Background())
	if healthy.runs.Load() == 0 {
		t.Fatal("post-panic retry with a healthy adapter must run the cycle")
	}
}
