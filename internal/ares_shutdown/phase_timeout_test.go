package ares_shutdown

import (
	"context"
	"testing"
	"time"
)

// TestExecutePhase_FallsBackToTheManagerTimeout: a phase registered without a timeout used to become
// context.WithTimeout(ctx, 0) — an already-cancelled context, so its callbacks were abandoned
// instead of bounded. The manager's own timeout looked like the fallback and was never consulted
// here, which made it a setting that appeared to work and did nothing.
func TestExecutePhase_FallsBackToTheManagerTimeout(t *testing.T) {
	m := NewManager(2 * time.Second)
	m.RegisterPhase(PhaseGraceful, 0) // registered, but the phase itself names no timeout
	if err := m.AddCallback(PhaseGraceful, func(context.Context) error {
		// Work that needs a moment: with an immediately-cancelled context it would never run.
		time.Sleep(20 * time.Millisecond)
		return nil
	}); err != nil {
		t.Fatalf("add callback: %v", err)
	}

	start := time.Now()
	if err := m.executePhase(context.Background(), PhaseGraceful); err != nil {
		t.Fatalf("a phase without its own timeout was abandoned: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the phase ran for %v; it should have used the bounded fallback", elapsed)
	}
}

// TestResolvePhaseTimeout pins the fallback chain. It is a pure function so the "nothing is
// configured anywhere" case does not have to wait out the real default to be asserted.
func TestResolvePhaseTimeout(t *testing.T) {
	cases := []struct {
		name          string
		phase, manger time.Duration
		want          time.Duration
	}{
		{"phase wins", time.Second, 2 * time.Second, time.Second},
		{"manager is the fallback", 0, 2 * time.Second, 2 * time.Second},
		{"negative phase falls through", -time.Second, 2 * time.Second, 2 * time.Second},
		{"nothing configured is still bounded", 0, 0, defaultPhaseTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolvePhaseTimeout(tc.phase, tc.manger); got != tc.want {
				t.Fatalf("resolvePhaseTimeout(%v, %v) = %v, want %v", tc.phase, tc.manger, got, tc.want)
			}
		})
	}
}
