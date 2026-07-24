package compiler

import (
	"context"
	"testing"

	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateNodeForAKG(t *testing.T) {
	tests := []struct {
		name   string
		node   *Node
		wantOK bool
		reason string
	}{
		{
			name:   "nil node rejected",
			node:   nil,
			wantOK: false, reason: "nil node",
		},
		{
			name:   "valid fact passes",
			node:   &Node{ID: "f1", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch"}},
			wantOK: true,
		},
		{
			name:   "fact with multi-word object passes",
			node:   &Node{ID: "f2", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch for evolution"}},
			wantOK: true,
		},
		{
			name:   "incomplete triple rejected",
			node:   &Node{ID: "f3", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": ""}},
			wantOK: false, reason: "incomplete triple",
		},
		{
			name:   "stopword triple rejected",
			node:   &Node{ID: "f4", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "a"}},
			wantOK: false, reason: "stopword triple",
		},
		{
			name:   "valid entity passes",
			node:   &Node{ID: "e1", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "ARES"}},
			wantOK: true,
		},
		{
			name:   "empty entity name rejected",
			node:   &Node{ID: "e2", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": ""}},
			wantOK: false, reason: "empty entity name",
		},
		{
			name:   "stopword entity rejected",
			node:   &Node{ID: "e3", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "a"}},
			wantOK: false, reason: reasonStopwordEntity,
		},
		{
			name:   "stopword entity 'the' rejected",
			node:   &Node{ID: "e4", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "the"}},
			wantOK: false, reason: reasonStopwordEntity,
		},
		{
			name:   "multi-token stopword entity rejected",
			node:   &Node{ID: "e5", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "of the"}},
			wantOK: false, reason: reasonStopwordEntity,
		},
		{
			name:   "entity with stopword prefix kept",
			node:   &Node{ID: "e6", Type: NodeEntity, Confidence: 0.9, Attributes: map[string]any{"name": "the Beatles"}},
			wantOK: true,
		},
		{
			name:   "decision with choice passes",
			node:   &Node{ID: "d1", Type: NodeDecision, Confidence: 0.9, Attributes: map[string]any{"choice": "adopt Rust"}},
			wantOK: true,
		},
		{
			name:   "decision without summary rejected",
			node:   &Node{ID: "d2", Type: NodeDecision, Confidence: 0.9, Attributes: map[string]any{}},
			wantOK: false, reason: "no extracted content",
		},
		{
			name:   "memory node always passes",
			node:   &Node{ID: "m1", Type: NodeMemory, Confidence: 0.9, Attributes: map[string]any{}},
			wantOK: true,
		},
		{
			name:   "negative confidence rejected",
			node:   &Node{ID: "x1", Type: NodeEntity, Confidence: -0.1, Attributes: map[string]any{"name": "ARES"}},
			wantOK: false, reason: "negative confidence",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := ValidateNodeForAKG(tc.node)
			assert.Equal(t, tc.wantOK, ok, "ok mismatch")
			if !tc.wantOK {
				assert.Equal(t, tc.reason, reason, "reason mismatch")
			}
		})
	}
}

// TestAKGBuilderQualityGateDropsInvalid proves the gate is enforced at Build
// time: when enabled, structurally invalid nodes are counted in Dropped and
// never persisted; when disabled, Build keeps its prior behavior.
func TestAKGBuilderQualityGateDropsInvalid(t *testing.T) {
	ctx := context.Background()
	sub := &SubGraph{Nodes: []*Node{
		{ID: "f-ok", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch"}},
		{ID: "f-bad", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": ""}},
	}}

	// Gate ON: one dropped, one saved.
	storeOn := memorystore.New()
	bOn := NewAKGBuilder(storeOn).WithQualityGate(true)
	resOn, err := bOn.Build(ctx, sub, "ns")
	require.NoError(t, err)
	assert.Equal(t, 1, resOn.Dropped, "exactly one invalid node must be dropped")
	assert.Equal(t, 1, resOn.Saved, "exactly one valid node must be saved")
	assert.Equal(t, 1, storeOn.Count(), "invalid node must not reach the store")

	// Gate OFF: backward-compatible, both saved.
	storeOff := memorystore.New()
	bOff := NewAKGBuilder(storeOff)
	resOff, err := bOff.Build(ctx, sub, "ns")
	require.NoError(t, err)
	assert.Equal(t, 0, resOff.Dropped, "gate off => nothing dropped")
	assert.Equal(t, 2, resOff.Saved, "gate off => all nodes saved")
	assert.Equal(t, 2, storeOff.Count(), "gate off => all nodes persisted")
}

// TestAKGBuilderQualityGateSkippedByDefault proves the gate is opt-in: a
// builder constructed without WithQualityGate(true) preserves prior behavior,
// so existing call sites are unaffected.
func TestAKGBuilderQualityGateSkippedByDefault(t *testing.T) {
	ctx := context.Background()
	b := NewAKGBuilder(memorystore.New()) // no WithQualityGate
	res, err := b.Build(ctx, &SubGraph{Nodes: []*Node{
		{ID: "f-bad", Type: NodeFact, Confidence: 0.9, Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": ""}},
	}}, "ns")
	require.NoError(t, err)
	assert.Equal(t, 0, res.Dropped, "default builder must not drop")
	assert.Equal(t, 1, res.Saved, "default builder must save regardless of structure")
}
