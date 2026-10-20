package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestExtractRelations_AppliesTheGate is M2.5's production wiring: the extractor's output
// must pass the relation gate before it reaches the object.
//
// Before this, `extractRelations` wrote the extractor's output straight onto the object, so
// a relation the gate rejects (here: a `user` subject claiming `fixes`) still fed the
// extraction score and the graph. The gate existed, had tests, and had no caller — which is
// why "M2.5 已交付" in the plan's §8.1 and "零生产调用方" in §8.2 were both true.
func TestExtractRelations_AppliesTheGate(t *testing.T) {
	bridge := &DistillBridge{extractor: knowledge.NewRelationExtractor()}

	t.Run("type-incompatible relation is dropped", func(t *testing.T) {
		obj := &knowledge.KnowledgeObject{
			ID: "akg:user-1", Type: knowledge.ObjectUser,
			Summary: "alice fixes the auth bug", Normalized: "alice fixes the auth bug",
		}
		bridge.extractRelations([]*knowledge.KnowledgeObject{obj})
		assert.Empty(t, obj.Relations,
			"a `fixes` relation from a user-typed subject must not reach the object")
	})

	t.Run("compatible relation survives", func(t *testing.T) {
		// No declared subject type: the gate cannot check compatibility and allows it. This
		// is the positive half — without it the test would pass on a gate that rejects
		// everything.
		obj := &knowledge.KnowledgeObject{
			ID:      "akg:svc-1",
			Summary: "auth.service depends on redis", Normalized: "auth.service depends on redis",
		}
		bridge.extractRelations([]*knowledge.KnowledgeObject{obj})
		require.NotEmpty(t, obj.Relations, "the extractor must still be able to add relations")
		assert.Equal(t, knowledge.RelDependsOn, obj.Relations[0].Predicate)
	})
}

// TestExtractRelations_FlagsConflictsWithoutDroppingThem: a candidate that disagrees with a
// relation the object already carries is reported and STILL attached. Dropping it would
// erase the disagreement, and the graph would look unanimous about something two sources
// disagree on.
func TestExtractRelations_FlagsConflictsWithoutDroppingThem(t *testing.T) {
	bridge := &DistillBridge{extractor: knowledge.NewRelationExtractor()}
	obj := &knowledge.KnowledgeObject{
		ID:      "akg:svc-2",
		Summary: "auth.service depends on redis", Normalized: "auth.service depends on redis",
		// The object already asserts the opposite about the same pair.
		Relations: []knowledge.Relation{{
			From: "akg:svc-2", ObjectText: "redis", Predicate: "contradicts",
		}},
	}

	bridge.extractRelations([]*knowledge.KnowledgeObject{obj})

	// The object's own pre-existing relation is replaced by the gated set (unchanged
	// overwrite semantics), but the candidate survives rather than being dropped on
	// conflict — the flagging happens in the log, which this test does not assert on
	// (slog output is not the contract; "the candidate is still there" is).
	require.NotEmpty(t, obj.Relations)
	assert.Equal(t, knowledge.RelDependsOn, obj.Relations[0].Predicate,
		"a conflicting candidate must be attached and flagged, not dropped")
}
