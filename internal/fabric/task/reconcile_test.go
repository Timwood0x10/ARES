package taskfabric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReconcile_LedgerTracksAndClears pins both halves of the ledger's lifecycle for a deletion:
// an unsettled tombstone is recorded with the identity and generation that produced it, and a retry
// that finally lands removes the entry — otherwise the ledger reports an event that is already
// durable, and a later Reconcile would "settle" something that was never pending.
func TestReconcile_LedgerTracksAndClears(t *testing.T) {
	unknown := newIDRecordingStore()
	unknown.failOn = string(EventTaskDeleted)
	unknown.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(unknown)
	require.NoError(t, f.Create(newTask("rec-1")))

	require.Error(t, f.Delete("rec-1"))

	pending := f.PendingReconciliations()
	require.Len(t, pending, 1, "an unknown outcome must leave something to reconcile")
	rec := pending[0]
	assert.Equal(t, "rec-1", rec.TaskID)
	assert.Equal(t, EventTaskDeleted, rec.Type)
	assert.NotEmpty(t, rec.EventID)
	assert.NotZero(t, rec.Generation, "the record must capture the state it described")
	assert.Equal(t, rec.Generation, mustTaskGeneration(t, f, "rec-1"),
		"and it must be the task's generation, not a copy made at a different moment")
	assert.Equal(t, CommitUnknown, rec.Outcome)

	// The retry lands (same event id), so there is nothing left to reconcile.
	f = f.WithEventStore(newIDRecordingStore())
	require.NoError(t, f.Delete("rec-1"))
	assert.Empty(t, f.PendingReconciliations(),
		"a settled event must not stay in the ledger")
}

// TestReconcile_OneMissIsNotProof is the rule the whole mechanism exists for: a store answering
// "not found" does not prove an in-flight append will not land, so the record stays.
func TestReconcile_OneMissIsNotProof(t *testing.T) {
	store := newIDRecordingStore()
	store.failOn = string(EventTaskDeleted)
	store.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("rec-2")))
	require.Error(t, f.Delete("rec-2"))

	rec := f.PendingReconciliations()[0]

	err := f.Reconcile(context.Background(), rec.EventID, false, rec.Generation)

	assert.ErrorIs(t, err, ErrReconcileInconclusive)
	assert.Len(t, f.PendingReconciliations(), 1, "the record must survive an inconclusive answer")
	_, taskErr := f.Task("rec-2")
	assert.NoError(t, taskErr, "and the task must stay isolated meanwhile")
}

// TestReconcile_GenerationMismatchRefuses: settling against a task that has moved on would apply an
// outcome to a state that no longer exists.
func TestReconcile_GenerationMismatchRefuses(t *testing.T) {
	store := newIDRecordingStore()
	store.failOn = string(EventTaskDeleted)
	store.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("rec-3")))
	require.Error(t, f.Delete("rec-3"))

	rec := f.PendingReconciliations()[0]

	err := f.Reconcile(context.Background(), rec.EventID, true, rec.Generation+1)

	assert.ErrorIs(t, err, ErrReconcileGenerationMismatch)
	assert.Len(t, f.PendingReconciliations(), 1)
}

// TestReconcile_FoundSettlesTheDeletion: the tombstone existed after all, so the deletion is applied.
func TestReconcile_FoundSettlesTheDeletion(t *testing.T) {
	store := newIDRecordingStore()
	store.failOn = string(EventTaskDeleted)
	store.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.Create(newTask("rec-4")))
	require.Error(t, f.Delete("rec-4"))

	rec := f.PendingReconciliations()[0]
	require.NoError(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation))

	assert.Empty(t, f.PendingReconciliations())
	_, taskErr := f.Task("rec-4")
	assert.Error(t, taskErr, "a confirmed tombstone means the task is gone")

	// Reconciling the same id again is an error, not a silent no-op: the caller is settling
	// something this fabric is no longer waiting on.
	assert.ErrorIs(t, f.Reconcile(context.Background(), rec.EventID, true, rec.Generation), ErrReconcileUnknownEvent)
}

// mustTaskGeneration reads a task's generation or fails the test.
func mustTaskGeneration(t *testing.T, f *Fabric, id string) uint64 {
	t.Helper()
	task, err := f.Task(id)
	if err != nil {
		t.Fatalf("Task(%s): %v", id, err)
	}
	return task.Generation
}
