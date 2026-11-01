package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
)

// TestFinishBackgroundWait_ReportsTimeout: the CLI used to print this failure and then return nil, so
// the caller's errgroup finished clean while workers were still running.
func TestFinishBackgroundWait_ReportsTimeout(t *testing.T) {
	comp := &ares_bootstrap.Components{}
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	comp.GoBackground(context.Background(), "stuck", func(context.Context) error {
		<-release
		return nil
	})

	err := finishBackgroundWait(context.Background(), comp, 50*time.Millisecond)

	require.Error(t, err, "a timed-out background wait must not be reported as a clean shutdown")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestFinishBackgroundWait_CleanExitStaysNil keeps Ctrl-C semantics: workers gone means success, and
// the signal itself is not an error.
func TestFinishBackgroundWait_CleanExitStaysNil(t *testing.T) {
	comp := &ares_bootstrap.Components{}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	comp.GoBackground(workerCtx, "worker", func(ctx context.Context) error {
		<-ctx.Done()
		return nil
	})
	stopWorker()

	require.NoError(t, finishBackgroundWait(context.Background(), comp, 2*time.Second))
}
