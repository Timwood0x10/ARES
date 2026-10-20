package taskfabric

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingDecodeObserver struct {
	mu       sync.Mutex
	outcomes []bool
}

func (r *recordingDecodeObserver) OnCheckpointDecode(ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, ok)
}

func (r *recordingDecodeObserver) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]bool(nil), r.outcomes...)
}

type panicDecodeObserver struct{}

func (panicDecodeObserver) OnCheckpointDecode(bool) { panic("observer bug") }

// TestFabricDecodeCheckpoint_ObservesSuccessAndFailure is the P4 acceptance at
// the fabric seam: a successful read-back and a failed one both reach the
// observer, in order.
func TestFabricDecodeCheckpoint_ObservesSuccessAndFailure(t *testing.T) {
	f := NewFabric()
	obs := &recordingDecodeObserver{}
	f.WithConsistencyObserver(obs)

	_, err := f.DecodeCheckpoint(EncodeCheckpoint(DecodedCheckpoint{SessionID: "s-1"}))
	require.NoError(t, err)

	_, err = f.DecodeCheckpoint(&CheckpointEnvelope{SchemaVersion: CurrentCheckpointSchemaVersion + 1})
	require.Error(t, err)

	assert.Equal(t, []bool{true, false}, obs.snapshot())
}

// TestFabricDecodeCheckpoint_PureFunctionDoesNotObserve pins the observation
// boundary: the package function stays pure, so a helper/test decode cannot
// silently count into a fabric metric.
func TestFabricDecodeCheckpoint_PureFunctionDoesNotObserve(t *testing.T) {
	f := NewFabric()
	obs := &recordingDecodeObserver{}
	f.WithConsistencyObserver(obs)

	_, err := DecodeCheckpoint(EncodeCheckpoint(DecodedCheckpoint{SessionID: "s-1"}))
	require.NoError(t, err)
	assert.Empty(t, obs.snapshot())
}

// TestFabricDecodeCheckpoint_ObserverIsFabricScoped: two fabrics in one process
// (serving + evaluation/scratch) must not observe each other's decodes.
func TestFabricDecodeCheckpoint_ObserverIsFabricScoped(t *testing.T) {
	serving := NewFabric()
	scratch := NewFabric()
	servingObs := &recordingDecodeObserver{}
	scratchObs := &recordingDecodeObserver{}
	serving.WithConsistencyObserver(servingObs)
	scratch.WithConsistencyObserver(scratchObs)

	_, err := serving.DecodeCheckpoint(EncodeCheckpoint(DecodedCheckpoint{SessionID: "s-1"}))
	require.NoError(t, err)

	assert.Equal(t, []bool{true}, servingObs.snapshot())
	assert.Empty(t, scratchObs.snapshot(), "another fabric's decode must not leak")
}

// TestFabricDecodeCheckpoint_PanicSafe: a broken observer must never break a
// task read-back. The decode result is still returned.
func TestFabricDecodeCheckpoint_PanicSafe(t *testing.T) {
	f := NewFabric()
	f.WithConsistencyObserver(panicDecodeObserver{})

	dc, err := f.DecodeCheckpoint(EncodeCheckpoint(DecodedCheckpoint{SessionID: "s-1"}))
	require.NoError(t, err)
	assert.Equal(t, "s-1", dc.SessionID)
}

// TestFabricDecodeCheckpoint_NilObserverNoOp keeps the default path unchanged.
func TestFabricDecodeCheckpoint_NilObserverNoOp(t *testing.T) {
	f := NewFabric()
	f.WithConsistencyObserver(nil)

	dc, err := f.DecodeCheckpoint(EncodeCheckpoint(DecodedCheckpoint{SessionID: "s-1"}))
	require.NoError(t, err)
	assert.Equal(t, "s-1", dc.SessionID)
}
