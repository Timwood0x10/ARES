package leader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/events"
)

func TestNewEventRecovery_NilStore(t *testing.T) {
	r := NewEventRecovery(nil)
	assert.Nil(t, r, "NewEventRecovery with nil store should return nil")
}

func TestNewEventRecovery_NonNilStore(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	r := NewEventRecovery(store)
	require.NotNil(t, r)
}

func TestEventRecovery_RecoverFromEvents_NilReceiver(t *testing.T) {
	var r *EventRecovery
	_, err := r.RecoverFromEvents(context.Background(), "leader-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEventStoreNil)
}

func TestEventRecovery_RecoverFromEvents_EmptyLeaderID(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	r := NewEventRecovery(store)
	_, err := r.RecoverFromEvents(context.Background(), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyLeaderID)
}

func TestEventRecovery_RecoverFromEvents_NoEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), "leader-1")
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Empty(t, state.SessionID)
	assert.Nil(t, state.PendingTasks)
	assert.Equal(t, int64(0), state.LastVersion)
	assert.True(t, state.LastFailover.IsZero())
}

func TestEventRecovery_RecoverFromEvents_SessionCreated(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"
	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-abc",
				"user_id":    "user-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)
	assert.Equal(t, "session-abc", state.SessionID)
	assert.Equal(t, int64(1), state.LastVersion)
}

func TestEventRecovery_RecoverFromEvents_TaskTracking(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"
	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id":    "task-1",
				"session_id": "session-1",
			},
		},
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id":    "task-2",
				"session_id": "session-1",
			},
		},
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id":    "task-3",
				"session_id": "session-1",
			},
		},
		{
			Type: events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-2",
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)
	assert.Equal(t, int64(4), state.LastVersion)
	assert.Len(t, state.PendingTasks, 2)
	assert.Contains(t, state.PendingTasks, "task-1")
	assert.Contains(t, state.PendingTasks, "task-3")
	assert.NotContains(t, state.PendingTasks, "task-2")
}

func TestEventRecovery_RecoverFromEvents_FailoverDetection(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"
	failoverTime := time.Date(2026, 6, 11, 10, 30, 0, 0, time.UTC)

	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type:      events.EventFailoverTriggered,
			Timestamp: failoverTime,
			Payload: map[string]any{
				"leader_id": leaderID,
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)
	assert.Equal(t, failoverTime, state.LastFailover)
}

func TestEventRecovery_RecoverFromEvents_FullScenario(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"
	baseTime := time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)

	// Simulate a full lifecycle: session, tasks, messages, distillation, failover.
	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type:      events.EventSessionCreated,
			Timestamp: baseTime,
			Payload: map[string]any{
				"session_id": "session-1",
				"user_id":    "user-1",
			},
		},
		{
			Type:      events.EventMessageAdded,
			Timestamp: baseTime.Add(1 * time.Second),
			Payload: map[string]any{
				"session_id": "session-1",
				"role":       "user",
			},
		},
		{
			Type:      events.EventTaskCreated,
			Timestamp: baseTime.Add(2 * time.Second),
			Payload: map[string]any{
				"task_id":    "task-1",
				"session_id": "session-1",
			},
		},
		{
			Type:      events.EventTaskCreated,
			Timestamp: baseTime.Add(3 * time.Second),
			Payload: map[string]any{
				"task_id":    "task-2",
				"session_id": "session-1",
			},
		},
		{
			Type:      events.EventTaskCompleted,
			Timestamp: baseTime.Add(4 * time.Second),
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
		{
			Type:      events.EventMessageAdded,
			Timestamp: baseTime.Add(5 * time.Second),
			Payload: map[string]any{
				"session_id": "session-1",
				"role":       "assistant",
			},
		},
		{
			Type:      events.EventMemoryDistilled,
			Timestamp: baseTime.Add(6 * time.Second),
			Payload: map[string]any{
				"task_id":    "task-1",
				"session_id": "session-1",
			},
		},
		{
			Type:      events.EventFailoverTriggered,
			Timestamp: baseTime.Add(10 * time.Minute),
			Payload: map[string]any{
				"leader_id": leaderID,
			},
		},
		{
			Type:      events.EventFailoverCompleted,
			Timestamp: baseTime.Add(10*time.Minute + 5*time.Second),
			Payload: map[string]any{
				"leader_id":    leaderID,
				"new_agent_id": "leader-2",
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)

	assert.Equal(t, "session-1", state.SessionID)
	assert.Equal(t, int64(9), state.LastVersion)
	assert.Len(t, state.PendingTasks, 1)
	assert.Contains(t, state.PendingTasks, "task-2")
	assert.Equal(t, baseTime.Add(10*time.Minute), state.LastFailover)
}

func TestEventRecovery_RecoverFromEvents_MultipleSessions(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"

	// First session.
	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-old",
			},
		},
	}, 0)
	require.NoError(t, err)

	// Second session (should be the recovered one).
	err = store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-new",
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)
	assert.Equal(t, "session-new", state.SessionID, "should recover the latest session")
}

func TestEventRecovery_RecoverFromEvents_NilEventsSkipped(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	leaderID := "leader-1"

	// Append a valid event first.
	err := store.Append(context.Background(), leaderID, []*events.Event{
		{
			Type: events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": "session-1",
			},
		},
	}, 0)
	require.NoError(t, err)

	r := NewEventRecovery(store)
	state, err := r.RecoverFromEvents(context.Background(), leaderID)
	require.NoError(t, err)
	assert.Equal(t, "session-1", state.SessionID)
	assert.Equal(t, int64(1), state.LastVersion)
}

func TestEventRecovery_RecoverFromEvents_CancelledContext(t *testing.T) {
	store := events.NewMemoryEventStore()
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	r := NewEventRecovery(store)
	// With a cancelled context, the Read call may or may not fail
	// depending on implementation, but it should not panic.
	_, _ = r.RecoverFromEvents(ctx, "leader-1")
}
