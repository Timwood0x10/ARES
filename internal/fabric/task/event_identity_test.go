package taskfabric

import (
	"context"
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
// empty one. Nothing downstream could name the event it was asking the store about, which is the
// precondition for reconciling an unknown outcome at all.
func TestEventID_IsAssignedBeforeAppend(t *testing.T) {
	store := newIDRecordingStore()
	f := NewFabric().WithEventStore(store)

	require.NoError(t, f.Create(newTask("id-1")))

	ids := store.idsFor(EventTaskCreated)
	require.Len(t, ids, 1)
	assert.NotEmpty(t, ids[0], "the append must carry an id of its own")
	assert.Contains(t, ids[0], "id-1", "and it must name the task it belongs to")
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
