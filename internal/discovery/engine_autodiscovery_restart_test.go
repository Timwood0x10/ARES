package discovery

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// panickingHealth panics on its first CheckHealth call and behaves normally
// afterwards, standing in for a defect in a health probe. The loop must
// recover and keep going rather than dying with auto-discovery silently off.
type panickingHealth struct {
	calls atomic.Int32
}

func (p *panickingHealth) CheckHealth(_ context.Context, _ *DiscoveredService) (*HealthStatus, error) {
	if p.calls.Add(1) == 1 {
		panic("injected health-check panic")
	}
	return &HealthStatus{Healthy: true}, nil
}

// TestAutoDiscoveryRestartsAfterPanic pins the self-healing contract: a panic
// inside a cycle must not end auto-discovery for the life of the process.
//
// The loop runs with no caller to observe a failure, so a goroutine that
// exited on panic would leave the subsystem looking healthy while doing
// nothing — the "silent feature death" shape. The recover boundary restarts
// it; only ctx cancellation stops it.
func TestAutoDiscoveryRestartsAfterPanic(t *testing.T) {
	store := NewMemoryStore()
	// BestSource=OperationRegister keeps the seeded service alive across
	// DiscoverNow: with no providers, diffServices would otherwise treat it
	// as gone and delete it, leaving CheckHealth with nothing to iterate.
	if err := store.Save(context.Background(), &DiscoveredService{
		Identity:   ServiceIdentity{ID: "svc-1", Name: "svc-1"},
		BestSource: OperationRegister,
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	health := &panickingHealth{}
	engine := NewEngine(store, health)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine.StartAutoDiscovery(ctx, 20*time.Millisecond)

	// First cycle panics in CheckHealth; restart backs off 1s before the
	// second lifetime, so allow well past that.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if health.calls.Load() >= 3 {
			return // survived the panic and ran further cycles
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("loop did not recover from panic: CheckHealth called %d times, want >= 3",
		health.calls.Load())
}

// TestAutoDiscoveryDoesNotRestartOnCleanShutdown pins the other half: a
// cancelled context ends the loop for good. Without this the restart loop
// would fight shutdown, respawning a worker whose ctx is already done.
func TestAutoDiscoveryDoesNotRestartOnCleanShutdown(t *testing.T) {
	store := NewMemoryStore()
	health := &panickingHealth{}
	engine := NewEngine(store, health)

	ctx, cancel := context.WithCancel(context.Background())
	engine.StartAutoDiscovery(ctx, 10*time.Millisecond)
	cancel()

	// Give any erroneous restart attempt time to show up as extra calls.
	time.Sleep(300 * time.Millisecond)
	if got := health.calls.Load(); got != 0 {
		t.Fatalf("CheckHealth called %d times after shutdown, want 0 (empty store, no services to check)", got)
	}
}

// TestAutoDiscoveryRestartBackoffCaps pins the backoff schedule: exponential
// from 1s, saturating at autoDiscoveryMaxBackoff. The shift is guarded so a
// large attempt count cannot overflow to a negative (immediate) delay, which
// would turn a deterministic panic into a hot spin.
func TestAutoDiscoveryRestartBackoffCaps(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{0, time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, autoDiscoveryMaxBackoff},
		{64, autoDiscoveryMaxBackoff}, // would overflow an unguarded shift
	}
	for _, tc := range cases {
		if got := autoDiscoveryRestartBackoff(tc.attempt); got != tc.want {
			t.Errorf("backoff(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}
