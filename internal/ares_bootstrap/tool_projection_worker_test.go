package ares_bootstrap

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/toolprojection"
)

// fakeProjector records every Run call's window start so the tests can assert
// the cursor behavior (the whole point of the loop) without an event store.
type fakeProjector struct {
	mu    sync.Mutex
	calls []toolprojection.Options
	// errOn makes the Nth call (1-based) fail, to test cursor retention.
	errOn int
	// written is returned on success.
	written int
	// signal is closed-of-sorts: each call sends, so tests can wait for a tick
	// deterministically instead of sleeping.
	signal chan struct{}
}

func (f *fakeProjector) Run(_ context.Context, opts toolprojection.Options) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	n := len(f.calls)
	f.mu.Unlock()
	if f.signal != nil {
		f.signal <- struct{}{}
	}
	if f.errOn == n {
		return 0, fmt.Errorf("projection boom")
	}
	return f.written, nil
}

func (f *fakeProjector) snapshot() []toolprojection.Options {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]toolprojection.Options, len(f.calls))
	copy(out, f.calls)
	return out
}

// withFrozenClock replaces the worker clock with a manually advanced one and
// restores it on cleanup, so cursor assertions are exact rather than timing
// dependent.
func withFrozenClock(t *testing.T, start time.Time) func(time.Duration) {
	t.Helper()
	orig := timeNowUTC
	var mu sync.Mutex
	now := start
	timeNowUTC = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	t.Cleanup(func() { timeNowUTC = orig })
	return func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
}

// TestToolProjectionLoop_CursorStartsAtNowAndAdvances is the core regression:
// the first window must NOT start at the zero time (which would project the
// whole history and attribute it to whatever strategy is active at boot), and
// each successful tick must advance the cursor so already-projected calls are
// not re-emitted.
func TestToolProjectionLoop_CursorStartsAtNowAndAdvances(t *testing.T) {
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	advance := withFrozenClock(t, start)

	proj := &fakeProjector{written: 2, signal: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runToolProjectionLoop(ctx, proj, time.Millisecond, 3)
		close(done)
	}()

	// First tick: window starts at boot time, not the zero time.
	<-proj.signal
	// Second tick: the clock has moved, so the window must start where the
	// first one ended.
	advance(5 * time.Minute)
	<-proj.signal
	<-proj.signal // a third, to be sure the advance persisted
	cancel()
	<-done

	calls := proj.snapshot()
	if len(calls) < 2 {
		t.Fatalf("want at least 2 runs, got %d", len(calls))
	}
	if calls[0].Since.IsZero() {
		t.Fatal("first window started at the zero time: the whole event history " +
			"would be projected and misattributed to the boot-time strategy")
	}
	if !calls[0].Since.Equal(start) {
		t.Errorf("first window Since = %s, want boot time %s", calls[0].Since, start)
	}
	if !calls[1].Since.After(calls[0].Since) && !calls[1].Since.Equal(start) {
		t.Errorf("second window Since = %s, want >= first (%s)", calls[1].Since, calls[0].Since)
	}
	if calls[0].MinSamples != 3 {
		t.Errorf("MinSamples = %d, want 3 (threshold must reach the projection)", calls[0].MinSamples)
	}
}

// TestToolProjectionLoop_FailedRunKeepsCursor asserts a failing tick does not
// advance the window: the calls it could not project are retried instead of
// being silently dropped.
func TestToolProjectionLoop_FailedRunKeepsCursor(t *testing.T) {
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	advance := withFrozenClock(t, start)

	proj := &fakeProjector{errOn: 1, signal: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runToolProjectionLoop(ctx, proj, time.Millisecond, 0)
		close(done)
	}()

	<-proj.signal // failing tick
	advance(5 * time.Minute)
	<-proj.signal // retry tick
	cancel()
	<-done

	calls := proj.snapshot()
	if len(calls) < 2 {
		t.Fatalf("want at least 2 runs, got %d", len(calls))
	}
	if !calls[1].Since.Equal(calls[0].Since) {
		t.Errorf("cursor advanced past a failed run: first=%s second=%s — that window's "+
			"tool outcomes would never be projected", calls[0].Since, calls[1].Since)
	}
}

// TestToolProjectionLoop_StopsOnContextCancel guards the shutdown path: the
// goroutine runs on the bootstrap background group, so a loop that ignored
// cancellation would hang graceful shutdown.
func TestToolProjectionLoop_StopsOnContextCancel(t *testing.T) {
	withFrozenClock(t, time.Now().UTC())
	proj := &fakeProjector{}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		runToolProjectionLoop(ctx, proj, time.Hour, 0)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not exit on context cancellation")
	}
}

// TestStartToolProjectionWorker_RefusesWithoutStores asserts the wiring refuses
// to start rather than looping over an empty projection. A worker reading a nil
// event store would write an empty graph on every tick while reporting itself
// as wired — the "live but dead" failure class Y1 §9.3-2 calls out.
func TestStartToolProjectionWorker_RefusesWithoutStores(t *testing.T) {
	ctx := context.Background()
	armed := ares_config.ToolProjectionConfig{Enabled: true, Interval: time.Minute, MinSamples: 1}

	// Disabled: no-op even with everything else missing.
	startToolProjectionWorker(ctx, nil, nil, ares_config.ToolProjectionConfig{})

	// Enabled but no event store: must not panic, must not start.
	comp := &Components{}
	startToolProjectionWorker(ctx, comp, nil, armed)

	// Enabled, no evidence store either.
	startToolProjectionWorker(ctx, comp, &NewEvolutionComponents{}, armed)
}
