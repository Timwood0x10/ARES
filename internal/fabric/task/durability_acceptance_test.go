package taskfabric

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// appliedButTimedOutStore writes the event and then reports a timeout, which is what a store looks
// like when the commit happened and the acknowledgement did not.
type appliedButTimedOutStore struct {
	mu sync.Mutex
	// failOn limits the timeout to one event type, so the setup (create/acquire/start) can succeed and
	// only the transition under test is left unacknowledged.
	failOn EventType
	events []*ares_events.Event
}

func (s *appliedButTimedOutStore) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	s.mu.Lock()
	s.events = append(s.events, events...)
	fail := s.failOn != "" && events[len(events)-1].Type != ares_events.EventType(s.failOn)
	s.mu.Unlock()
	if s.failOn == "" || fail {
		return nil
	}
	return context.DeadlineExceeded
}
func (s *appliedButTimedOutStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *appliedButTimedOutStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *appliedButTimedOutStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (s *appliedButTimedOutStore) StreamVersion(context.Context, string) (int64, error) {
	return 0, nil
}

func (s *appliedButTimedOutStore) setFailOn(typ EventType) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failOn = typ
}

func (s *appliedButTimedOutStore) has(eventID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.ID == eventID {
			return true
		}
	}
	return false
}

// TestDurability_CommittedButUnacknowledgedCompletionSettles is the success-consumer case end to end:
// the store committed the completion and the acknowledgement was lost. Nobody may consume the result
// until reconciliation confirms it, and once it does, the result is there — with the checkpoint the
// quantum produced, and the dependent released.
func TestDurability_CommittedButUnacknowledgedCompletionSettles(t *testing.T) {
	store := &appliedButTimedOutStore{}
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("dur-pred")))
	dep := newTask("dur-dep")
	dep.Dependencies = []string{"dur-pred"}
	require.NoError(t, f.Create(dep))

	epoch, err := f.Acquire("dur-pred", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("dur-pred", "agent-a", epoch))

	// From here the completion is applied by the store and its acknowledgement is lost.
	store.setFailOn(EventTaskCompleted)

	checkpoint := map[string]any{"result": "the answer"}
	err = f.CompleteWithCheckpoint("dur-pred", "agent-a", epoch, checkpoint)
	require.Error(t, err, "an unacknowledged completion must not be reported as success")
	require.True(t, errors.Is(err, ErrCommitUnknown))

	// The consumer side: nothing was published.
	reverted, err := f.Task("dur-pred")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, reverted.State)
	ready, err := f.IsReady("dur-dep")
	require.NoError(t, err)
	assert.False(t, ready, "a dependent must not be released on an unacknowledged completion")

	// The record exists in the store — and the ledger has the id to ask about.
	pending := f.PendingReconciliations()
	require.Len(t, pending, 1)
	require.True(t, store.has(pending[0].EventID),
		"the store really did apply it; only the answer was lost")

	require.NoError(t, f.Reconcile(context.Background(), pending[0].EventID, true, pending[0].Generation))

	settled, err := f.Task("dur-pred")
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, settled.State)
	assert.Equal(t, checkpoint, settled.Checkpoint, "the confirmed result must be the one produced")
	ready, err = f.IsReady("dur-dep")
	require.NoError(t, err)
	assert.True(t, ready, "and only now is the dependent released")
}

// TestDurability_PartialBatchKeepsCommittedAndIsolatesTheRest is the batch case: a plan where one
// member's created event was refused. The committed members stay runnable, the refused one is kept for
// reconciliation and is NOT runnable, and a drain racing the decision cannot take it.
func TestDurability_PartialBatchKeepsCommittedAndIsolatesTheRest(t *testing.T) {
	store := &selectiveStubStore{failOn: string(EventTaskCreated), failTaskID: "batch-b", err: errors.New("disk full")}
	f := NewFabric().WithEventStore(store)

	_, err := f.CompilePlan(context.Background(), []PlanStep{
		{ID: "batch-a", Capability: "code"},
		{ID: "batch-b", Capability: "code"},
	})
	require.Error(t, err)
	var partial *PartialCommitError
	require.True(t, errors.As(err, &partial))

	// Committed member: runnable.
	epoch, acquireErr := f.Acquire("batch-a", "agent-a", time.Minute)
	require.NoError(t, acquireErr, "a committed member must stay runnable")
	require.NotZero(t, epoch)

	// Refused member: kept, isolated, not runnable.
	_, acquireErr = f.Acquire("batch-b", "agent-a", time.Minute)
	require.Error(t, acquireErr, "a refused member must not be handed to an executor")
	task, taskErr := f.Task("batch-b")
	require.NoError(t, taskErr, "but it is kept for reconciliation")
	assert.True(t, task.CommitUnconfirmed)

	// And it is not in any scheduling queue a drain or the preemption scan would read.
	assert.NotContains(t, f.ReadyTasks(), "batch-b")
	assert.NotContains(t, f.ResumableTasks(), "batch-b")
}

// TestDurability_DeleteDuringReconciliationIsNotUndone: a reconciliation and a deletion of the same
// task must not interleave into "deleted, then resurrected by a stale record".
func TestDurability_DeleteDuringReconciliationIsNotUndone(t *testing.T) {
	unknown := &selectiveStubStore{failOn: string(EventTaskDeleted), err: context.DeadlineExceeded}
	f := NewFabric().WithEventStore(unknown)
	require.NoError(t, f.Create(newTask("race-del")))
	require.Error(t, f.Delete("race-del"))

	rec := f.PendingReconciliations()[0]

	// The deletion is settled by a successful retry while the record is still open.
	f = f.WithEventStore(nil)
	require.NoError(t, f.Delete("race-del"))

	// The stale record must be gone with it.
	assert.Empty(t, f.PendingReconciliations())
	assert.ErrorIs(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation),
		ErrReconcileUnknownEvent)

	_, taskErr := f.Task("race-del")
	assert.Error(t, taskErr, "and the task stays deleted")
}
