package leader

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/events"
)

// Sentinel errors for event recovery operations.
var (
	ErrEventStoreNil = errors.New("event store cannot be nil")
)

// RecoveryState holds the state reconstructed from event replay.
type RecoveryState struct {
	// SessionID is the active session recovered from events.
	SessionID string
	// PendingTasks lists task IDs that were created but not completed.
	PendingTasks []string
	// LastVersion is the final event version seen during replay.
	LastVersion int64
	// LastFailover records when the most recent failover occurred.
	LastFailover time.Time
}

// EventRecovery reconstructs leader state by replaying events from the event store.
type EventRecovery struct {
	eventStore events.EventStore
}

// NewEventRecovery creates an EventRecovery.
// Returns nil if eventStore is nil.
func NewEventRecovery(eventStore events.EventStore) *EventRecovery {
	if eventStore == nil {
		return nil
	}
	return &EventRecovery{eventStore: eventStore}
}

// RecoverFromEvents reads all events for the given leader stream and replays
// them to reconstruct the recovery state.
//
// Args:
//
//	ctx - timeout and cancellation context.
//	leaderID - the leader's stream identifier in the event store.
//
// Returns:
//
//	state - reconstructed state with sessionID, pending tasks, last version.
//	err - ErrEventStoreNil, ErrEmptyLeaderID, or event store read error.
func (r *EventRecovery) RecoverFromEvents(ctx context.Context, leaderID string) (*RecoveryState, error) {
	if r == nil || r.eventStore == nil {
		return nil, ErrEventStoreNil
	}
	if leaderID == "" {
		return nil, ErrEmptyLeaderID
	}

	evts, err := r.eventStore.Read(ctx, leaderID, events.ReadOptions{})
	if err != nil {
		return nil, err
	}

	state := &RecoveryState{}
	completedTasks := make(map[string]bool)

	for _, evt := range evts {
		if evt == nil {
			continue
		}

		if evt.Version > state.LastVersion {
			state.LastVersion = evt.Version
		}

		switch evt.Type {
		case events.EventSessionCreated:
			if id, ok := evt.Payload["session_id"].(string); ok && id != "" {
				state.SessionID = id
			}

		case events.EventTaskCreated:
			if id, ok := evt.Payload["task_id"].(string); ok && id != "" {
				if !completedTasks[id] {
					state.PendingTasks = append(state.PendingTasks, id)
				}
			}

		case events.EventTaskCompleted:
			if id, ok := evt.Payload["task_id"].(string); ok && id != "" {
				completedTasks[id] = true
			}

		case events.EventFailoverTriggered:
			if evt.Timestamp.After(state.LastFailover) {
				state.LastFailover = evt.Timestamp
			}
		}
	}

	// Filter out completed tasks from pending list.
	if len(state.PendingTasks) > 0 {
		pending := state.PendingTasks[:0]
		for _, id := range state.PendingTasks {
			if !completedTasks[id] {
				pending = append(pending, id)
			}
		}
		state.PendingTasks = pending
	}

	return state, nil
}
