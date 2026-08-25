package ares_bootstrap

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeCleaner counts CleanupExpired invocations and can be made to fail.
type fakeCleaner struct {
	calls atomic.Int64
	err   error
	panic bool
}

func (f *fakeCleaner) CleanupExpired(context.Context) (int64, error) {
	f.calls.Add(1)
	if f.panic {
		panic("boom")
	}
	return f.calls.Load(), f.err
}

// TestRunExpiredCleanup_AllCleanersInvoked verifies one pass calls every
// registered cleaner exactly once and keeps going after individual failures
// (error and panic) — maintenance must never take the process down.
func TestRunExpiredCleanup_AllCleanersInvoked(t *testing.T) {
	ok1 := &fakeCleaner{}
	bad := &fakeCleaner{err: errors.New("db down")}
	exploding := &fakeCleaner{panic: true}
	ok2 := &fakeCleaner{}

	cleaners := []NamedExpiryCleaner{
		{Name: "a", Cleaner: ok1},
		{Name: "bad", Cleaner: bad},
		{Name: "exploding", Cleaner: exploding},
		{Name: "d", Cleaner: ok2},
	}
	require.NotPanics(t, func() { runExpiredCleanup(context.Background(), cleaners) })

	require.Equal(t, int64(1), ok1.calls.Load())
	require.Equal(t, int64(1), bad.calls.Load())
	require.Equal(t, int64(1), exploding.calls.Load())
	require.Equal(t, int64(1), ok2.calls.Load(), "panic in earlier cleaner must not skip later ones")
}

// TestStartExpiryCleanupWorker_NoCleanersIsNoOp verifies the worker is not
// started (no goroutine on bgGroup) when nothing registered.
func TestStartExpiryCleanupWorker_NoCleanersIsNoOp(t *testing.T) {
	var comp Components
	comp.ExpiryCleaners = nil
	startExpiryCleanupWorker(context.Background(), &comp)
	// WaitBackground must return immediately: no goroutine was spawned.
	done := make(chan struct{})
	go func() { comp.WaitBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("WaitBackground blocked although no cleaner was wired")
	}
}
