package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_shutdown"
	"github.com/Timwood0x10/ares/internal/kernel"
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

// TestFinalShutdownError_CancellationDoesNotMaskAFailure is the exact report: on Ctrl-C the errgroup's
// first error is the cancellation, Wait returns context.Canceled, and normalizing it to nil discarded
// the shutdown step that had failed. The recorded failure must survive.
func TestFinalShutdownError_CancellationDoesNotMaskAFailure(t *testing.T) {
	cases := []struct {
		name        string
		waitErr     error
		recorded    error
		wantErr     error
		wantContain string
	}{
		{name: "clean shutdown", waitErr: context.Canceled},
		{name: "a real failure is kept", waitErr: errors.New("kaboom"), wantContain: "kaboom"},
		{
			name:        "cancellation does not mask a deadline",
			waitErr:     context.Canceled,
			recorded:    context.DeadlineExceeded,
			wantContain: "deadline exceeded",
		},
		{
			name:        "both are reported when they differ",
			waitErr:     errors.New("kaboom"),
			recorded:    context.DeadlineExceeded,
			wantContain: "kaboom",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			failures := &shutdownFailures{}
			failures.add(tc.recorded)

			err := finalShutdownError(tc.waitErr, failures)

			if tc.wantContain == "" {
				if err != nil {
					t.Fatalf("expected a clean exit, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantContain) {
				t.Fatalf("want an error containing %q, got %v", tc.wantContain, err)
			}
		})
	}
}

// stopFailingComp is a component whose Stop reports failure.
type stopFailingComp struct{ name string }

func (c *stopFailingComp) Name() string                { return c.name }
func (c *stopFailingComp) Dependencies() []string      { return nil }
func (c *stopFailingComp) Start(context.Context) error { return nil }
func (c *stopFailingComp) Ready(context.Context) error { return nil }
func (c *stopFailingComp) Stop(context.Context) error {
	return errors.New("stop failed: flush incomplete")
}

// TestShutdownSystemRuntime_PropagatesFailure: this used to print and return nothing, so a Runtime
// whose components failed to stop still ended with a nil CLI error whenever the last background wait
// happened to succeed.
func TestShutdownSystemRuntime_PropagatesFailure(t *testing.T) {
	reg := kernel.NewRegistry()
	require.NoError(t, reg.Register(&stopFailingComp{name: "store"}, kernel.ModeRequired))

	orc := kernel.NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	var ptr atomic.Pointer[ares_bootstrap.Components]
	ptr.Store(&ares_bootstrap.Components{SystemRuntime: orc})

	err := shutdownSystemRuntime(&ptr, context.Background())

	require.Error(t, err, "a Runtime that did not stop cleanly must not be reported as success")
	assert.Contains(t, err.Error(), "system runtime shutdown")
	assert.Contains(t, err.Error(), "flush incomplete", "the component's own reason must stay reachable")
}

// TestShutdownSystemRuntime_NilIsSafe keeps the bootstrap-failure path working: no components means
// nothing to shut down, not a panic.
func TestShutdownSystemRuntime_NilIsSafe(t *testing.T) {
	var ptr atomic.Pointer[ares_bootstrap.Components]
	require.NoError(t, shutdownSystemRuntime(&ptr, context.Background()))

	ptr.Store(&ares_bootstrap.Components{})
	require.NoError(t, shutdownSystemRuntime(&ptr, context.Background()))
}

// cleanComp is a component that stops without complaint.
type cleanComp struct {
	name    string
	stopped atomic.Bool
}

func (c *cleanComp) Name() string                { return c.name }
func (c *cleanComp) Dependencies() []string      { return nil }
func (c *cleanComp) Start(context.Context) error { return nil }
func (c *cleanComp) Ready(context.Context) error { return nil }
func (c *cleanComp) Stop(context.Context) error {
	c.stopped.Store(true)
	return nil
}

// TestRunServeTeardown_CollectsPhaseFailure: the phase callbacks (HTTP → MCP → runtime) reported
// their error to stderr only, so an HTTP drain that timed out produced a clean CLI exit whenever the
// Runtime and the background wait happened to succeed.
func TestRunServeTeardown_CollectsPhaseFailure(t *testing.T) {
	mgr := ares_shutdown.NewManager(time.Second)
	mgr.RegisterPhase(ares_shutdown.PhaseGraceful, 0)
	require.NoError(t, mgr.AddCallback(ares_shutdown.PhaseGraceful, func(context.Context) error {
		return context.DeadlineExceeded
	}))

	var ptr atomic.Pointer[ares_bootstrap.Components]
	failures := &shutdownFailures{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runServeTeardown(ctx, cancel, mgr, &ptr, failures)

	err := failures.err()
	require.Error(t, err, "a failed shutdown phase must reach the caller")
	assert.Contains(t, err.Error(), "shutdown phases")
}

// TestWiringServeSignalWatch_NonSignalCancellationRunsTeardown is the second half: the config watcher
// (or any background task) cancelling the group's context is not a signal, and that path used to skip
// the teardown entirely — components stayed Ready and the HTTP listener kept accepting requests, so
// the wait that follows never returned.
func TestWiringServeSignalWatch_NonSignalCancellationRunsTeardown(t *testing.T) {
	reg := kernel.NewRegistry()
	comp := &cleanComp{name: "store"}
	require.NoError(t, reg.Register(comp, kernel.ModeRequired))
	orc := kernel.NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	var ptr atomic.Pointer[ares_bootstrap.Components]
	ptr.Store(&ares_bootstrap.Components{SystemRuntime: orc})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1) // no signal will arrive: the cancellation is the trigger
	failures := &shutdownFailures{}

	var g errgroup.Group
	wiringServeSignalWatch(&g, sigCh, ctx, cancel, ares_shutdown.NewManager(time.Second), &ptr, failures)

	cancel() // what a failing background task does
	require.NoError(t, g.Wait())

	assert.True(t, comp.stopped.Load(),
		"a non-signal cancellation must run the same teardown as Ctrl-C")
}

// TestServeShutdownPath_SignalCollectsFailuresIntoTheFinalResult is the end-to-end shape the helper
// tests cannot give: signal → watch goroutine → teardown → collector → final result. Both a failing
// phase and a failing Runtime must survive normalization, and the CLI must not report success.
func TestServeShutdownPath_SignalCollectsFailuresIntoTheFinalResult(t *testing.T) {
	// A phase callback that fails, like an HTTP drain that ran out of budget.
	mgr := ares_shutdown.NewManager(time.Second)
	mgr.RegisterPhase(ares_shutdown.PhaseGraceful, 0)
	require.NoError(t, mgr.AddCallback(ares_shutdown.PhaseGraceful, func(context.Context) error {
		return context.DeadlineExceeded
	}))

	// A Runtime whose component fails to stop.
	reg := kernel.NewRegistry()
	require.NoError(t, reg.Register(&stopFailingComp{name: "store"}, kernel.ModeRequired))
	orc := kernel.NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	var ptr atomic.Pointer[ares_bootstrap.Components]
	ptr.Store(&ares_bootstrap.Components{SystemRuntime: orc})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	failures := &shutdownFailures{}

	var g errgroup.Group
	wiringServeSignalWatch(&g, sigCh, ctx, cancel, mgr, &ptr, failures)

	sigCh <- os.Interrupt // the real trigger
	waitErr := g.Wait()

	final := finalShutdownError(waitErr, failures)

	require.Error(t, final, "a shutdown whose phase and components failed must not report success")
	assert.Contains(t, final.Error(), "shutdown phases")
	assert.Contains(t, final.Error(), "system runtime shutdown")
	assert.Contains(t, final.Error(), "flush incomplete", "the component's own reason must survive both layers")
}
