package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/kernel"
)

// refusingFailedAppendStore accepts everything except the task.failed append, so an expiry can fail
// to be recorded while the task is already failed in memory.
type refusingFailedAppendStore struct{}

func (refusingFailedAppendStore) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	for _, ev := range events {
		if string(ev.Type) == string(taskfabric.EventTaskFailed) {
			return errors.New("disk full")
		}
	}
	return nil
}
func (refusingFailedAppendStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (refusingFailedAppendStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (refusingFailedAppendStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (refusingFailedAppendStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

// TestRunSweepChain_ReturnsUnsettledExpiry is the error exit the recovery loop was missing: the sweep
// failed tasks in memory, could not record why, and told nobody. The caller saw a clean sweep.
func TestRunSweepChain_ReturnsUnsettledExpiry(t *testing.T) {
	fabric := taskfabric.NewFabric().WithEventStore(refusingFailedAppendStore{})
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:         "sweep-1",
		Capability: "code",
		Deadline:   time.Now().Add(-time.Second),
	}))

	rec := aresrecovery.New(fabric, agentfabric.NewFabric(), aresrecovery.DefaultRestartPolicy())

	err := runSweepChain(context.Background(), rec, nil)

	require.Error(t, err, "a sweep that left durable state unsettled must not report success")
	// The sweep reports per member, so a single refused expiry arrives as a partial commit rather
	// than as one flat verdict for the whole chain.
	assert.ErrorIs(t, err, taskfabric.ErrPartialCommit)
	assert.Contains(t, err.Error(), "sweep-1")

	// The task is failed in memory: the sweep did what it could, and that is exactly why the failure
	// has to be reported rather than undone.
	task, taskErr := fabric.Task("sweep-1")
	require.NoError(t, taskErr)
	assert.Equal(t, taskfabric.StateFailed, task.State)
}

// TestRunSweepChain_CleanSweepReturnsNil keeps the healthy path honest.
func TestRunSweepChain_CleanSweepReturnsNil(t *testing.T) {
	fabric := taskfabric.NewFabric()
	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:         "sweep-2",
		Capability: "code",
		Deadline:   time.Now().Add(-time.Second),
	}))

	rec := aresrecovery.New(fabric, agentfabric.NewFabric(), aresrecovery.DefaultRestartPolicy())

	require.NoError(t, runSweepChain(context.Background(), rec, nil))
}

// TestReportComponentFault_PublishesWithoutStopping: the sweep callback used to only accumulate, so
// component health stayed clean for the whole run and the failure surfaced (if at all) after the loop
// exited. A fault that does not stop the component must still be visible while it keeps working.
func TestReportComponentFault_PublishesWithoutStopping(t *testing.T) {
	reg := kernel.NewRegistry()
	require.NoError(t, reg.Register(&cleanComp{name: sysCompRecovery}, kernel.ModeRequired))
	orc := kernel.NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	before, ok := reg.GetStatus(sysCompRecovery)
	require.True(t, ok)

	orc.ReportComponentFault(sysCompRecovery, errors.New("deadline expiry left durable state unsettled"))

	after, ok := reg.GetStatus(sysCompRecovery)
	require.True(t, ok)
	assert.Contains(t, after.Reason, "durable state unsettled", "the reason must be visible immediately")
	assert.Equal(t, before.State, after.State,
		"a running component must not be marked failed for a fault it survived")
}

// TestReportComponentFault_DoesNotOverwriteLifecycleState asserts that a fault report leaves the
// lifecycle state alone.
//
// Honest scope: the defect it guards against is a RACE (read the state, write it back, lose a
// concurrent update), and that interleaving cannot be staged deterministically without a hook inside
// the writer. What this test does catch is any implementation that changes the state as a side effect;
// the race itself is prevented by construction — the update goes through the registry's locked
// UpdateStatus and touches only Reason.
func TestReportComponentFault_DoesNotOverwriteLifecycleState(t *testing.T) {
	reg := kernel.NewRegistry()
	require.NoError(t, reg.Register(&cleanComp{name: sysCompRecovery}, kernel.ModeRequired))
	orc := kernel.NewOrchestrator(reg, context.Background())
	require.NoError(t, orc.Start(context.Background()))

	// Another writer marks it Failed between the read and the write of a naive implementation.
	reg.SetStatus(sysCompRecovery, kernel.ComponentStatus{State: kernel.StateFailed, Reason: "component failed"})

	orc.ReportComponentFault(sysCompRecovery, errors.New("sweep left durable state unsettled"))

	after, ok := reg.GetStatus(sysCompRecovery)
	require.True(t, ok)
	assert.Equal(t, kernel.StateFailed, after.State,
		"a fault report must not restore a lifecycle state it merely read")
	assert.Contains(t, after.Reason, "durable state unsettled")
}
