package ares_bootstrap

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWaitBackgroundContext_IsBounded is the CLI's last shutdown step: after the kernel and the
// SystemRuntime have each spent their budget, the final wait for Bootstrap's workers must still be
// bounded. Unbounded here means a worker that ignores cancellation holds the process open after it
// has already reported a finished shutdown.
//
// synctest advances the fake clock, so the assertion is about the bound itself rather than about
// waiting for it.
func TestWaitBackgroundContext_IsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		comp := &Components{}
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })

		comp.GoBackground(context.Background(), "stuck-worker", func(context.Context) error {
			<-release // ignores cancellation on purpose
			return nil
		})

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		err := comp.WaitBackgroundContext(ctx)

		require.Error(t, err, "a worker that never exits must not be reported as a clean wait")
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

// TestWaitBackgroundContext_ReturnsWhenWorkersExit keeps the healthy path honest: a cooperative worker
// lets the wait finish without consuming the budget.
func TestWaitBackgroundContext_ReturnsWhenWorkersExit(t *testing.T) {
	comp := &Components{}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()

	done := make(chan struct{})
	comp.GoBackground(workerCtx, "fast-worker", func(ctx context.Context) error {
		<-ctx.Done()
		close(done)
		return nil
	})
	stopWorker() // the worker watches the context it was started with

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, comp.WaitBackgroundContext(waitCtx))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe cancellation")
	}
}
