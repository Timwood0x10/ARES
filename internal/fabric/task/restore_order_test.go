package taskfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// appendRestoreEvent appends one must-persist event to the store with an
// EXPLICIT timestamp and stream version — the test builds the durable log by
// hand so it can construct the exact orderings (and ties) the fold must
// survive.
func appendRestoreEvent(t *testing.T, store *ares_events.MemoryEventStore, ts time.Time, version int64, typ ares_events.EventType, payload map[string]any) {
	t.Helper()
	ev := &ares_events.Event{
		ID:        string(typ) + "-" + payload[restoreKeyTaskID].(string) + "-" + time.Now().Format("150405.000000000"),
		StreamID:  "restore-order-probe",
		Type:      typ,
		Payload:   payload,
		Version:   version,
		Timestamp: ts,
	}
	require.NoError(t, store.Append(context.Background(), "restore-order-probe", []*ares_events.Event{ev}, version-1))
}

// basePayload is the task.created payload for the probe task.
func basePayload(state TaskState) map[string]any {
	return map[string]any{
		restoreKeyTaskID:     "t-order",
		restoreKeyCapability: "cap",
		restoreKeyOrigin:     "probe",
		restoreKeyState:      string(state),
		restoreKeyEpoch:      float64(1),
	}
}

// TestRestoreSameTimestampKeepsTerminalState locks F-03: a task whose
// task.completed and task.checkpointed carry the SAME timestamp must fold to
// the terminal state no matter how the sort breaks the tie. ReadAll sorts by
// Timestamp alone with a non-stable sort, so a checkpointed(SUSPENDED) event
// can land AFTER the completed event — the fold then reset the completed task
// back to READY, and a restart re-executed finished work (violating the
// file-header contract "terminal tasks are restored as terminal and never
// revived").
func TestRestoreSameTimestampKeepsTerminalState(t *testing.T) {
	same := time.Now().UTC()

	for _, tc := range []struct {
		name  string
		first ares_events.EventType
	}{
		// completed first in log order (version ordering) — the tie-break
		// must keep it terminal even if the sort shuffles the pair.
		{"completed_logged_before_checkpointed", ares_events.EventTaskCompleted},
		{"checkpointed_logged_before_completed", ares_events.EventTaskCheckpointed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := ares_events.NewMemoryEventStore()

			// task.created, strictly earlier.
			appendRestoreEvent(t, store, same.Add(-time.Second), 1,
				ares_events.EventTaskCreated, basePayload(StateReady))

			// The pair sharing one timestamp. The completed event is the
			// LATER lifecycle fact (version 3), checkpointed is version 2.
			appendRestoreEvent(t, store, same, 2,
				ares_events.EventTaskCheckpointed, basePayload(StateSuspended))
			appendRestoreEvent(t, store, same, 3,
				ares_events.EventTaskCompleted, basePayload(StateCompleted))

			f := NewFabric().WithEventStore(store)
			require.NoError(t, f.RestoreFromStore(context.Background()))

			got, err := f.Task("t-order")
			require.NoError(t, err)
			assert.Equal(t, StateCompleted, got.State,
				"a completed task must stay COMPLETED regardless of tie order (F-03: terminal tasks are never revived)")
		})
	}
}
