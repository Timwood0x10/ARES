package main

// Loop-clock falsifiable acceptance: the kernel loop clock must actually drive
// round-end settling through the registered LoopPlugin — not just tick.
//
// C1.3 (runtime plugin half-closed-loop burial) removed OnRoundEnd's
// capability dispatch (CapCheckpoint flush / CapMemory advise / CapEvolution
// record were deleted with their zero-production plugin faces), so per-round
// identity observability — which execution ID each round flushed — retired
// with the dispatch. The remaining falsifiable observation points are:
//
//   - LoopPlugin.Iteration(): the last settled round number, proving the
//     settle-then-gate order (a budget gate evaluated BEFORE the settle would
//     swallow the final round's bookkeeping);
//   - the bus AfterStep passthrough counter, proving the hook keeps serving
//     the bus regardless of the loop budget;
//   - process survival under concurrent boundaries (no panic).
//
// A grep for NewLoopPlugin cannot distinguish "beat wired" from "rounds
// actually settle", so every test here asserts one of those effects.

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/runtime"
)

// stepPassthroughSpy is a plain bus plugin whose only job is counting AfterStep
// invocations: the concrete, observable proof that the hook keeps projecting
// quanta onto the bus after the loop budget is exhausted.
type stepPassthroughSpy struct {
	mu         sync.Mutex
	afterSteps int
}

func (s *stepPassthroughSpy) Name() string { return "step-passthrough-spy" }
func (s *stepPassthroughSpy) Capabilities() []runtime.Capability {
	return nil
}
func (s *stepPassthroughSpy) Start(context.Context, runtime.EventBus) error { return nil }
func (s *stepPassthroughSpy) Stop(context.Context) error                    { return nil }

// AfterStep implements runtime.WorkflowHook (auto-registered by the bus).
func (s *stepPassthroughSpy) AfterStep(_ context.Context, _ string, _ *runtime.StepResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterSteps++
	return nil
}

// BeforeStep implements runtime.WorkflowHook.
func (s *stepPassthroughSpy) BeforeStep(context.Context, string, *runtime.Step) error {
	return nil
}

func (s *stepPassthroughSpy) stepCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.afterSteps
}

// startSpyBus assembles the production wiring path with the spy registered
// before Start (the conventional batch order — hot-plug also works, but the
// batch path is what serve uses).
func startSpyBus(ctx context.Context, loopCfg kernelLoopConfig) (*pluginBusHook, *stepPassthroughSpy, error) {
	bus := runtime.NewPluginBus()
	spy := &stepPassthroughSpy{}
	if err := bus.Register(spy); err != nil {
		return nil, nil, err
	}
	loop := runtime.NewLoopPlugin("kernel-loop", runtime.LoopConfig{
		MaxIterations: loopCfg.LoopMaxIterations,
	})
	if err := bus.Register(loop); err != nil {
		return nil, nil, err
	}
	if err := bus.Start(ctx); err != nil {
		return nil, nil, err
	}
	return newPluginBusHook(bus, loop, loopCfg), spy, nil
}

// TestLoopClock_SettlesRounds proves quanta aggregate into settled rounds:
// 4 quanta at 2 quanta/round must settle rounds 1 and 2 (Iteration == 2).
// Per-round identity (kernel-round-N flush targets) is no longer observable
// — it retired with the C1.3 capability-dispatch burial.
func TestLoopClock_SettlesRounds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hook, spy, err := startSpyBus(ctx, kernelLoopConfig{LoopRoundQuanta: 2})
	if err != nil {
		t.Fatalf("startSpyBus: %v", err)
	}

	// 4 quanta at 2 quanta/round → rounds 1 and 2 settle.
	for i := 0; i < 4; i++ {
		hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", i), "agent", nil)
	}

	if got := hook.loop.Iteration(); got != 2 {
		t.Fatalf("expected Iteration 2 after 4 quanta at 2 quanta/round, got %d", got)
	}
	if got := spy.stepCount(); got != 4 {
		t.Fatalf("expected 4 AfterStep passthroughs, got %d", got)
	}
}

// TestLoopClock_MaxIterationsSettlesFinalRound locks the settle-then-gate
// order: with MaxIterations=1 the FIRST boundary must still settle round 1
// (OnRoundEnd records it), and only THEN stop advancing. Asking
// ShouldExecuteRound before settling would swallow the final round's
// end-of-round bookkeeping — the exact bug the review flagged.
func TestLoopClock_MaxIterationsSettlesFinalRound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hook, _, err := startSpyBus(ctx, kernelLoopConfig{LoopMaxIterations: 1, LoopRoundQuanta: 1})
	if err != nil {
		t.Fatalf("startSpyBus: %v", err)
	}

	// Round 1 settles at the first quantum; rounds 2+ never process.
	for i := 0; i < 5; i++ {
		hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", i), "agent", nil)
	}

	if got := hook.loop.Iteration(); got != 1 {
		t.Fatalf("expected exactly the final round-1 settle (Iteration 1), got %d", got)
	}
}

// TestLoopClock_ConcurrentBoundariesNoPanic proves the concurrent boundary
// path is safe and lossless at the bus layer: every one of the 200 concurrent
// quanta must pass through AfterStep exactly once, and the process must
// survive (no panic). Per-round uniqueness (no double-fired or skipped
// rounds) is no longer directly observable post-C1.3 — its enforcement, the
// AddInt64 return-value contract, is covered by the budget-concurrency test
// below.
func TestLoopClock_ConcurrentBoundariesNoPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const quanta = 200
	hook, spy, err := startSpyBus(ctx, kernelLoopConfig{LoopRoundQuanta: 1})
	if err != nil {
		t.Fatalf("startSpyBus: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < quanta; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", n), "agent", nil)
		}(i)
	}
	wg.Wait()

	if got := spy.stepCount(); got != quanta {
		t.Fatalf("expected %d AfterStep passthroughs under concurrency, got %d (lost or double-projected quanta)", quanta, got)
	}
	if got := hook.loop.Iteration(); got < 1 || got > quanta {
		t.Fatalf("Iteration %d outside [1, %d] after concurrent boundaries", got, quanta)
	}
}

// TestLoopClock_BudgetExhaustedKeepsServingBus proves the round clock is
// observational: once the budget latches, the hook still projects every
// quantum onto the bus (AfterStep passthrough keeps counting) — the
// scheduler's task flow is never gated by the evolution clock.
func TestLoopClock_BudgetExhaustedKeepsServingBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hook, spy, err := startSpyBus(ctx, kernelLoopConfig{LoopMaxIterations: 1, LoopRoundQuanta: 1})
	if err != nil {
		t.Fatalf("startSpyBus: %v", err)
	}

	for i := 0; i < 8; i++ {
		hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", i), "agent", nil)
	}

	if got := spy.stepCount(); got != 8 {
		t.Fatalf("bus AfterStep passthrough must survive the loop budget: got %d, want 8", got)
	}
	if got := hook.loop.Iteration(); got != 1 {
		t.Fatalf("settled rounds after budget: Iteration %d, want 1", got)
	}
}

// TestLoopClock_BudgetHoldsUnderConcurrency closes the matrix hole between
// TestLoopClock_MaxIterationsSettlesFinalRound (budget, serial) and
// TestLoopClock_ConcurrentBoundariesNoPanic (concurrent, no budget):
// budget × concurrency. A read-then-set stop flag lets every concurrent
// boundary caller observe "not stopped" before any latches it, over-settling
// rounds past the budget (observed: max_iterations=1 settling 3 rounds).
// Deriving the round from each caller's own unique quantum count fixes it.
// Observable post-C1.3: Iteration must never exceed the budget, while at
// least one in-budget round must settle. Repeated attempts because the
// interleaving is probabilistic.
func TestLoopClock_BudgetHoldsUnderConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const (
		attempts   = 100
		goroutines = 64
		maxRounds  = 2
	)
	for attempt := 0; attempt < attempts; attempt++ {
		hook, _, err := startSpyBus(ctx, kernelLoopConfig{
			LoopMaxIterations: maxRounds,
			LoopRoundQuanta:   1,
		})
		if err != nil {
			t.Fatalf("startSpyBus: %v", err)
		}
		var wg sync.WaitGroup
		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", n), "agent", nil)
			}(i)
		}
		wg.Wait()

		it := hook.loop.Iteration()
		if it > maxRounds {
			t.Fatalf("attempt %d: round budget overrun — Iteration %d, want <= %d",
				attempt, it, maxRounds)
		}
		// The budget must be reached, not merely respected: with far more
		// quanta than the budget, in-budget rounds must have settled.
		if it < 1 {
			t.Fatalf("attempt %d: no round settled (Iteration %d), want >= 1", attempt, it)
		}
	}
}

// TestNewPluginBusHook_NormalizesRoundQuanta locks the invariant inside the
// type: a zero/negative LoopRoundQuanta (a caller that skipped withDefaults —
// e.g. an adopt path constructing kernelLoopConfig{}) must still beat once
// per quantum rather than silently never ticking.
func TestNewPluginBusHook_NormalizesRoundQuanta(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, quanta := range []int{0, -5} {
		hook, spy, err := startSpyBus(ctx, kernelLoopConfig{LoopRoundQuanta: quanta})
		if err != nil {
			t.Fatalf("startSpyBus(quanta=%d): %v", quanta, err)
		}
		for i := 0; i < 3; i++ {
			hook.AfterQuantum(ctx, fmt.Sprintf("task-%d", i), "agent", nil)
		}
		if got := hook.loop.Iteration(); got != 3 {
			t.Fatalf("LoopRoundQuanta=%d must normalize to 1: Iteration %d, want 3", quanta, got)
		}
		if got := spy.stepCount(); got != 3 {
			t.Fatalf("LoopRoundQuanta=%d: AfterStep passthroughs %d, want 3", quanta, got)
		}
	}
}

// TestParseKernelLoopConfig_LoopKnobs covers the config regression contract:
// unset knobs fall back to the zero-value-safe defaults (quanta 1, unlimited
// rounds); explicit values pass through.
func TestParseKernelLoopConfig_LoopKnobs(t *testing.T) {
	cfg := parseKernelLoopConfig(&ares_config.Config{})
	if cfg.LoopRoundQuanta != 1 {
		t.Fatalf("default LoopRoundQuanta = %d, want 1", cfg.LoopRoundQuanta)
	}
	if cfg.LoopMaxIterations != 0 {
		t.Fatalf("default LoopMaxIterations = %d, want 0 (unlimited)", cfg.LoopMaxIterations)
	}

	withKnobs := &ares_config.Config{}
	withKnobs.Kernel.LoopRoundQuanta = 3
	withKnobs.Kernel.LoopMaxIterations = 7
	parsed := parseKernelLoopConfig(withKnobs)
	if parsed.LoopRoundQuanta != 3 || parsed.LoopMaxIterations != 7 {
		t.Fatalf("explicit knobs not honored: quanta=%d max=%d", parsed.LoopRoundQuanta, parsed.LoopMaxIterations)
	}
}
