package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
)

// TestDistillConversationWithReport_ProducesCounters is M4's REAL acceptance: the report has
// to come out of a run.
//
// The previous acceptance test (TestAKG_BuildReport_ExposesCounters in the knowledge
// package) built a BuildReport literal and asserted the fields it had just set — it proved
// the struct compiles, not that anything produces one. This one drives the ingest and checks
// the numbers against what that run actually read and wrote.
func TestDistillConversationWithReport_ProducesCounters(t *testing.T) {
	store := newTestStore()
	bridge := NewDistillBridge(&testDistiller{}, nil, store, "akf")
	msgs := []distillation.Message{{Role: "user", Content: "Why Redis?"}}

	objects, report, err := bridge.DistillConversationWithReport(context.Background(), "conv1", msgs, "t1", "")
	require.NoError(t, err)
	require.NotEmpty(t, objects, "the fixture must produce at least one object")

	assert.Equal(t, 2, report.ReadCount,
		"ReadCount is what the distiller RETURNED (two memories), not the message count")
	assert.Equal(t, len(objects), report.NewEntities,
		"every object that reached the store is a new entity in this run")

	// Consistency between the report and the objects it describes: these two must agree, or
	// the report is describing a different run than the one the caller sees.
	relations := 0
	for _, obj := range objects {
		relations += len(obj.Relations)
		assert.Contains(t,
			[]knowledge.ObjectStatus{knowledge.StatusActive, knowledge.StatusCandidate, knowledge.StatusSuperseded},
			obj.Status, "an object may only end up active, candidate, or superseded")
	}
	assert.Equal(t, relations, report.NewRelations)
	assert.Zero(t, report.Conflicts, "no pre-existing relations in this fixture means no conflicts")
	assert.Positive(t, report.Duration, "a run takes measurable time")
	assert.Less(t, report.Duration, time.Minute, "the duration must be this run's, not a zero value")
}

// TestDistillConversation_StillReturnsTheSameObjects pins the wrapper: splitting the report
// out must not change what the original call returns.
func TestDistillConversation_StillReturnsTheSameObjects(t *testing.T) {
	store := newTestStore()
	bridge := NewDistillBridge(&testDistiller{}, nil, store, "akf")

	objects, err := bridge.DistillConversation(context.Background(), "conv1",
		[]distillation.Message{{Role: "user", Content: "Why Redis?"}}, "t1", "")
	require.NoError(t, err)
	require.NotEmpty(t, objects)
	for _, obj := range objects {
		got, gerr := store.Get(context.Background(), "", obj.ID)
		require.NoError(t, gerr, "the wrapper must still persist objects")
		assert.Equal(t, obj.ID, got.ID)
	}
}

// changingDistiller returns one memory whose content the test controls, with a STABLE id —
// stable identity plus changing content is exactly the shape an incremental build must
// distinguish.
type changingDistiller struct{ content string }

func (d *changingDistiller) DistillConversation(_ context.Context, _ string, _ []distillation.Message, _, _ string) ([]distillation.Memory, error) {
	return []distillation.Memory{{
		ID: "mem1", Type: distillation.MemoryKnowledge, Content: d.content, Importance: 85.0,
	}}, nil
}

// TestAKG_IncrementalBuild_OnlyChangedByFingerprint is M4's incremental acceptance: a run
// that changes nothing must write nothing, and a run whose content changed must write it.
//
// Skipping is safe only because identity is derived from the source record and the
// fingerprint covers the content fields — same id + same fingerprint means the stored record
// already says what this run would write.
func TestAKG_IncrementalBuild_OnlyChangedByFingerprint(t *testing.T) {
	ctx := context.Background()
	msgs := []distillation.Message{{Role: "user", Content: "Why Redis?"}}
	store := newTestStore()

	bridge := NewDistillBridge(&changingDistiller{content: "Redis was chosen for caching."}, nil, store, "akf")
	_, first, err := bridge.DistillConversationWithReport(ctx, "conv1", msgs, "t1", "")
	require.NoError(t, err)
	assert.Equal(t, 1, first.NewEntities, "the first run has everything to write")
	assert.Zero(t, first.Unchanged)

	// Identical content again: the fingerprint matches, so the write is skipped.
	_, second, err := bridge.DistillConversationWithReport(ctx, "conv1", msgs, "t1", "")
	require.NoError(t, err)
	assert.Zero(t, second.NewEntities, "unchanged content must not be written again")
	assert.Equal(t, 1, second.Unchanged)

	// Content changed: the same id now fingerprints differently and must be written.
	changed := NewDistillBridge(&changingDistiller{content: "Memcached replaced Redis for caching."}, nil, store, "akf")
	_, third, err := changed.DistillConversationWithReport(ctx, "conv1", msgs, "t1", "")
	require.NoError(t, err)
	assert.Equal(t, 1, third.NewEntities, "changed content must be written")
	assert.Zero(t, third.Unchanged)
}
