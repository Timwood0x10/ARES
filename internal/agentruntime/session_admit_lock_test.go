package agentruntime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAdmitAndDo_HoldsTheLockAcrossTheCallersWork pins the structural property the submission
// cleanup depends on, and it is deterministic: while do runs, nobody else may admit the same
// session.
//
// Why it matters: ownership of the rollback is decided by "did THIS call create the session". If
// the lock covers only the admission, a second submission can slip in, be answered 202 with the
// second slot, and then watch the first submission — the one that owns the session — fail and roll
// it back. The caller ends up holding a task whose session no longer exists.
//
// The integration test in cmd/ares asserts the same invariant end to end, but only a rare
// interleaving violates it, so it cannot prove the property by itself. This one can: the second
// admission must still be blocked when the timer fires.
func TestAdmitAndDo_HoldsTheLockAcrossTheCallersWork(t *testing.T) {
	sessions, _ := newTestSessions(t)
	ctx := context.Background()

	entered := make(chan struct{})
	hold := make(chan struct{})
	firstDone := make(chan struct{})

	go func() {
		defer close(firstDone)
		_, err := sessions.AdmitAndDo(ctx, "shared", "prompt", func(created bool) error {
			close(entered)
			<-hold
			return nil
		})
		_ = err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the first admission never entered its callback")
	}

	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_, _ = sessions.AdmitFresh(ctx, "shared", "prompt")
	}()

	select {
	case <-secondDone:
		t.Fatal("a second admission of the same session completed while the first still held the lock; " +
			"cleanup ownership is then decided by timing instead of by the lock")
	case <-time.After(150 * time.Millisecond):
		// Still blocked, which is the contract.
	}

	close(hold)

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the first admission did not finish after its callback returned")
	}
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("the second admission did not proceed after the lock was released")
	}
}

// TestAdmitAndDo_ReportsCreationToTheCallback: the callback decides whether a failure rolls back
// the session, so the flag must be true only for the call that created it.
func TestAdmitAndDo_ReportsCreationToTheCallback(t *testing.T) {
	sessions, _ := newTestSessions(t)
	ctx := context.Background()

	var first, second bool
	_, err := sessions.AdmitAndDo(ctx, "shared", "prompt", func(created bool) error {
		first = created
		return nil
	})
	require.NoError(t, err)

	_, err = sessions.AdmitAndDo(ctx, "shared", "prompt", func(created bool) error {
		second = created
		return nil
	})
	require.NoError(t, err)

	require.True(t, first, "the first admission created the session")
	require.False(t, second, "the second joined it and must not be allowed to roll it back")
}
