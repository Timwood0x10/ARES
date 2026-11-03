package taskfabric

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// idRecordingStore records the id of every append it is asked to make.
type idRecordingStore struct {
	mu     sync.Mutex
	ids    []string
	byType map[string][]string
	failOn string
	err    error
}

func newIDRecordingStore() *idRecordingStore {
	return &idRecordingStore{byType: make(map[string][]string)}
}

func (s *idRecordingStore) Append(_ context.Context, _ string, events []*ares_events.Event, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range events {
		s.ids = append(s.ids, ev.ID)
		s.byType[string(ev.Type)] = append(s.byType[string(ev.Type)], ev.ID)
		if s.failOn != "" && string(ev.Type) == s.failOn {
			return s.err
		}
	}
	return nil
}
func (s *idRecordingStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *idRecordingStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *idRecordingStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (s *idRecordingStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

func (s *idRecordingStore) idsFor(typ EventType) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.byType[string(typ)]...)
}

// TestEventID_IsAssignedBeforeAppend: the store used to mint the id, so the append call carried an
// empty one, and the replacement embedded the task id — which made a legal 255-character task id
// produce a 274-character event id and fail the store's VARCHAR(255) column. Nothing downstream could name the event it was asking the store about, which is the
// precondition for reconciling an unknown outcome at all.
func TestEventID_IsAssignedBeforeAppend(t *testing.T) {
	store := newIDRecordingStore()
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("id-1")))

	ids := store.idsFor(EventTaskCreated)
	require.Len(t, ids, 1)
	assert.NotEmpty(t, ids[0], "the append must carry an id of its own")
	// Fixed length: events.id is VARCHAR(255) and the task id is already carried by StreamID.
	assert.LessOrEqual(t, len(ids[0]), 64, "the id must stay well inside the column limit")
	assert.NotContains(t, ids[0], "id-1", "and must not embed a task id of unbounded length")
}

// TestEventID_IsUniquePerEvent: two events of one task must not share an id, or reconciliation would
// settle one of them with the other's outcome.
func TestEventID_IsUniquePerEvent(t *testing.T) {
	store := newIDRecordingStore()
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("id-2")))
	epoch, err := f.Acquire("id-2", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("id-2", "agent-a", epoch))
	require.NoError(t, f.Complete("id-2", "agent-a", epoch))

	store.mu.Lock()
	defer store.mu.Unlock()
	seen := make(map[string]bool, len(store.ids))
	for _, id := range store.ids {
		require.False(t, seen[id], "duplicate event id %q", id)
		seen[id] = true
	}
}

// TestDelete_ReusesTheUnverifiedTombstoneID is the second half: an unknown outcome left a tombstone
// whose fate is undetermined, and a later delete used to mint a NEW event. Two task.deleted records
// for one deletion cannot both be settled — the reconciliation would settle the one it happened to
// read and leave the other to resurrect the task.
func TestDelete_ReusesTheUnverifiedTombstoneID(t *testing.T) {
	unknown := newIDRecordingStore()
	unknown.failOn = string(EventTaskDeleted)
	unknown.err = context.DeadlineExceeded

	f := NewFabric().WithEventStore(unknown)
	require.NoError(t, f.Create(newTask("id-3")))
	require.Error(t, f.Delete("id-3"))

	firstIDs := unknown.idsFor(EventTaskDeleted)
	require.Len(t, firstIDs, 1)
	require.NotEmpty(t, firstIDs[0])

	// Second attempt, against a store that accepts the append.
	retry := newIDRecordingStore()
	f = f.WithEventStore(retry)
	require.NoError(t, f.Delete("id-3"))

	secondIDs := retry.idsFor(EventTaskDeleted)
	require.Len(t, secondIDs, 1)
	assert.Equal(t, firstIDs[0], secondIDs[0],
		"the retry must append the SAME event, not a second tombstone for one deletion")

	_, taskErr := f.Task("id-3")
	assert.Error(t, taskErr, "and a confirmed delete still removes the task")
}

// TestDelete_RetryTreatsVersionConflictAsCommitted is the stuck-deletion case: PostgreSQL committed the
// tombstone and the acknowledgement timed out. Retrying appends the SAME id, which the store rejects as
// a duplicate key — indistinguishable, from the client's side, from a version conflict. Reading that as
// a refusal kept the task PendingDelete forever, because every retry hit the same wall.
func TestDelete_RetryTreatsVersionConflictAsCommitted(t *testing.T) {
	unknown := newIDRecordingStore()
	unknown.failOn = string(EventTaskDeleted)
	unknown.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(unknown)
	require.NoError(t, f.Create(newTask("id-conflict")))
	require.Error(t, f.Delete("id-conflict"))

	conflicting := newIDRecordingStore()
	conflicting.failOn = string(EventTaskDeleted)
	conflicting.err = ares_events.ErrVersionConflict
	f = f.WithEventStore(conflicting)

	require.NoError(t, f.Delete("id-conflict"),
		"a conflict on a re-append means the earlier attempt landed, not that the delete failed")

	_, taskErr := f.Task("id-conflict")
	assert.Error(t, taskErr, "the task is deleted, as the store always said")
}

// pgUniqueViolation stands in for a driver error carrying SQLSTATE 23505, which is what a duplicate
// event id looks like when the store does not translate it.
type pgUniqueViolation struct{}

func (pgUniqueViolation) Error() string {
	return "ERROR: duplicate key value violates unique constraint"
}
func (pgUniqueViolation) SQLState() string { return "23505" }

// TestAlreadyPersisted_RecognisesEveryShapeOfConflict pins the three forms the retry has to survive.
//
// Only the sentinel form is guaranteed by the interface; the other two are what a real backend
// produces when it does not translate. Missing them is not cosmetic — it is the difference between a
// deletion settling and a task stuck in PendingDelete forever.
func TestAlreadyPersisted_RecognisesEveryShapeOfConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"the store's sentinel", ares_events.ErrVersionConflict, true},
		{"a wrapped sentinel", fmt.Errorf("append: %w", ares_events.ErrVersionConflict), true},
		{"postgres unique violation", pgUniqueViolation{}, true},
		{"an untranslated message", errors.New("ERROR: duplicate key value violates unique constraint \"events_pkey\""), true},
		{"a real refusal is not a conflict", errors.New("connection refused"), false},
		{"nil stays nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, alreadyPersisted(tc.err))
		})
	}
}

// TestDelete_UnrecognisedRetryFailureStaysRefused keeps the safety net honest: a retry that fails for
// a reason nobody recognises must NOT be treated as committed. Guessing "it probably landed" would
// silently delete a task the store still holds.
func TestDelete_UnrecognisedRetryFailureStaysRefused(t *testing.T) {
	unknown := newIDRecordingStore()
	unknown.failOn = string(EventTaskDeleted)
	unknown.err = context.DeadlineExceeded
	f := NewFabric().WithEventStore(unknown)
	require.NoError(t, f.Create(newTask("id-refused")))
	require.Error(t, f.Delete("id-refused"))

	refusing := newIDRecordingStore()
	refusing.failOn = string(EventTaskDeleted)
	refusing.err = errors.New("permission denied")
	f = f.WithEventStore(refusing)

	require.Error(t, f.Delete("id-refused"), "an unrecognised failure is still a failure")
	_, taskErr := f.Task("id-refused")
	assert.NoError(t, taskErr, "and the task stays, isolated, waiting for a real answer")
}
