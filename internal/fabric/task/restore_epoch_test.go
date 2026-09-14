package taskfabric

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// TestRestoreEpochClampAtMaxUint64 locks F-6: a corrupted payload can report
// maxEpoch == MaxUint64, and the unguarded `+1` used to wrap to 0 — the
// epoch would never grow past it and pre-crash fencing tokens could be
// re-issued. The clamp pins the epoch to MaxUint64 instead, and a repeated
// restore (idempotency contract) keeps it there.
func TestRestoreEpochClampAtMaxUint64(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	// task.acquired is observability-only for state folding (default branch),
	// but its payload carries the fencing epoch — exactly the channel a
	// corrupted payload uses to smuggle in a bogus maxEpoch.
	ev := &ares_events.Event{
		ID:       "ev-epoch-max",
		StreamID: "stream-epoch",
		Type:     ares_events.EventType("task.acquired"),
		Payload:  map[string]any{"epoch": uint64(math.MaxUint64)},
	}
	require.NoError(t, store.Append(context.Background(), "stream-epoch", []*ares_events.Event{ev}, 0))

	f := NewFabric().WithEventStore(store)
	require.NoError(t, f.RestoreFromStore(context.Background()))
	assert.Equal(t, uint64(math.MaxUint64), f.epoch,
		"MaxUint64 epoch must clamp, not wrap to 0 (pre-crash tokens would become re-issuable)")

	// Idempotency: a second restore must neither shrink nor wrap the epoch.
	require.NoError(t, f.RestoreFromStore(context.Background()))
	assert.Equal(t, uint64(math.MaxUint64), f.epoch)
}
