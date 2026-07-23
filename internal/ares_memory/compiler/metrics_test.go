package compiler

import (
	"context"
	"testing"

	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAKGMetricsCollector exercises the bare collector math: every counter and
// the confidence histogram / signal-tier breakdown must accumulate exactly as
// recorded, and Snapshot must return an immutable copy while Reset zeroes it.
func TestAKGMetricsCollector(t *testing.T) {
	m := NewAKGMetrics()

	m.RecordInput(6)
	for i := 0; i < 2; i++ {
		m.RecordLowConfDrop()
	}
	for i := 0; i < 2; i++ {
		m.RecordStructuralDrop()
	}
	m.RecordDedupHit()
	m.RecordObjectBuilt(0.9)
	m.RecordObjectBuilt(0.95)

	snap := m.Snapshot()
	assert.Equal(t, int64(6), snap.NodesIn, "akg_objects_in")
	assert.Equal(t, int64(2), snap.DroppedLowConf, "dropped_lowconf")
	assert.Equal(t, int64(2), snap.DroppedStructural, "dropped_structural")
	assert.Equal(t, int64(1), snap.DedupHits, "dedup_hits")
	assert.Equal(t, int64(2), snap.ObjectsBuilt, "objects_built")
	assert.Equal(t, int64(2), snap.ConfidenceHistogram["0.9-1.0"])
	assert.Equal(t, int64(2), snap.SignalTiers["strong"])

	// The returned map must be a copy: mutating it does not affect the source.
	snap.ConfidenceHistogram["0.9-1.0"] = 99
	assert.Equal(t, int64(2), m.Snapshot().ConfidenceHistogram["0.9-1.0"], "snapshot must be a deep copy")

	// Reset returns the collector to a clean state for reuse.
	m.Reset()
	zero := m.Snapshot()
	assert.Equal(t, int64(0), zero.NodesIn)
	assert.Equal(t, int64(0), zero.ObjectsBuilt)
	assert.Empty(t, zero.ConfidenceHistogram)
}

// TestAKGMetricsSignalTiers verifies the three L2 signal tiers and their
// alignment with the confidence buckets: weak (<0.7), medium (0.7-0.9),
// strong (>=0.9). This is what the 1.3 evaluation harness reads to confirm the
// distribution is not piled onto a single value.
func TestAKGMetricsSignalTiers(t *testing.T) {
	m := NewAKGMetrics()
	m.RecordObjectBuilt(0.3) // weak, <0.4
	m.RecordObjectBuilt(0.7) // medium, 0.7-0.9
	m.RecordObjectBuilt(0.9) // strong, 0.9-1.0

	snap := m.Snapshot()
	assert.Equal(t, int64(1), snap.SignalTiers["weak"])
	assert.Equal(t, int64(1), snap.SignalTiers["medium"])
	assert.Equal(t, int64(1), snap.SignalTiers["strong"])
	assert.Equal(t, int64(1), snap.ConfidenceHistogram["<0.4"])
	assert.Equal(t, int64(1), snap.ConfidenceHistogram["0.7-0.9"])
	assert.Equal(t, int64(1), snap.ConfidenceHistogram["0.9-1.0"])
}

// TestAKGMetricsEndToEnd runs a mixed KnowledgeModel through the real selector
// + builder + resolver with a SHARED metrics instance, and asserts the snapshot
// reflects exactly what the gate did:
//
//	input=6 (3 entities + 3 facts)
//	dropped_lowconf=2   (e3 conf 0.3, f3 conf 0.3)
//	dropped_structural=2 (e2 stopword "a", f2 incomplete triple)
//	objects_built=2     (e1, f1 — both strong)
func TestAKGMetricsEndToEnd(t *testing.T) {
	ctx := context.Background()
	km := &KnowledgeModel{Nodes: map[string]*Node{
		"e1": {ID: "e1", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "ARES"}},
		"e2": {ID: "e2", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "a"}},
		"e3": {ID: "e3", Type: NodeEntity, Confidence: 0.3, Attributes: map[string]any{"name": "Rust"}},
		"f1": {ID: "f1", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "B"}},
		"f2": {ID: "f2", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": ""}},
		"f3": {ID: "f3", Type: NodeFact, Confidence: 0.3, Attributes: map[string]any{"subject": "C", "predicate": "has", "object": "D"}},
	}}

	m := NewAKGMetrics()
	store := memorystore.New()
	builder := NewAKGBuilder(store).
		WithResolver(NewResolver(store, 0).WithMetrics(m)).
		WithQualityGate(true).
		WithMetrics(m)
	sel := NewAKGSelector(0.6, 0).WithAKGMetrics(m)

	sub := sel.Select(km)
	require.NotNil(t, sub)
	res, err := builder.Build(ctx, sub, "test-ns")
	require.NoError(t, err)
	assert.Equal(t, 2, res.Dropped, "builder's own structural drop counter must match")

	snap := m.Snapshot()
	assert.Equal(t, int64(6), snap.NodesIn, "akg_objects_in = 3 entities + 3 facts")
	assert.Equal(t, int64(2), snap.DroppedLowConf, "low-conf drops = e3 + f3")
	assert.Equal(t, int64(2), snap.DroppedStructural, "structural drops = e2 + f2")
	assert.Equal(t, int64(2), snap.ObjectsBuilt, "only e1 + f1 survive")
	assert.Equal(t, int64(2), snap.SignalTiers["strong"], "both survivors are strong-signal")
	assert.Equal(t, int64(2), snap.ConfidenceHistogram["0.9-1.0"])
}

// TestAKGMetricsResolverDedup isolates the resolver's dedup_hits counter: when a
// second build emits knowledge Jaccard-similar to what is already persisted,
// the resolver drops it and records exactly one dedup hit while building zero
// new objects.
func TestAKGMetricsResolverDedup(t *testing.T) {
	ctx := context.Background()
	store := memorystore.New()
	m := NewAKGMetrics()
	builder := NewAKGBuilder(store).
		WithResolver(NewResolver(store, 0).WithMetrics(m)).
		WithQualityGate(true).
		WithMetrics(m)

	fact := func(id string) *SubGraph {
		return &SubGraph{Nodes: []*Node{{
			ID: id, Type: NodeFact, Confidence: 0.9,
			Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "B"},
		}}}
	}

	res1, err := builder.Build(ctx, fact("f1"), "ns")
	require.NoError(t, err)
	assert.Equal(t, 1, res1.Saved, "first fact persisted")

	res2, err := builder.Build(ctx, fact("f1b"), "ns") // same text, different id
	require.NoError(t, err)
	assert.Equal(t, 0, res2.Saved, "Jaccard-duplicate must be dropped")

	snap := m.Snapshot()
	assert.Equal(t, int64(1), snap.DedupHits, "exactly one dedup hit")
	assert.Equal(t, int64(1), snap.ObjectsBuilt, "only the first fact was built")
}

// TestAKGMetricsNilSafe confirms the collector is a no-op when nil (the wiring
// passes a nil-capable instance in tests / disabled paths) and that a metrics
// instance shared by selector and builder is returned by Builder.Metrics().
func TestAKGMetricsNilSafe(t *testing.T) {
	var m *AKGMetrics
	m.RecordInput(5) // must not panic
	m.RecordStructuralDrop()
	assert.Equal(t, int64(0), m.Snapshot().NodesIn)

	b := NewAKGBuilder(memorystore.New()).WithMetrics(nil)
	assert.Nil(t, b.Metrics(), "WithMetrics(nil) leaves metrics nil")

	shared := NewAKGMetrics()
	b2 := NewAKGBuilder(memorystore.New()).WithMetrics(shared)
	assert.Same(t, shared, b2.Metrics(), "Builder.Metrics returns the shared instance")
}
