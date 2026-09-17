package distillation

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The HIGH regression #10: the ReplaceOld conflict
// strategy must actually delete the superseded old experience from the
// repository. Pre-fix the branch only kept the new memory — the old
// near-duplicate row stayed forever, so conflict resolution was a write-side
// no-op and near-duplicates accumulated on every re-distillation.

func TestReplaceOldDeletesSupersededExperience(t *testing.T) {
	old := Experience{
		ID:         "exp-old",
		Type:       MemoryKnowledge,
		Problem:    "how to deploy the service",
		Solution:   "old answer",
		Confidence: 0.5,
		Vector:     []float64{1, 0, 0},
	}
	repo := NewMockExperienceRepository([]Experience{old})
	distiller := NewDistiller(nil, NewMockEmbeddingService(), repo)

	// A new memory with the same vector (similarity 1.0 > the 0.85 conflict
	// threshold) but higher confidence: ResolveConflict must pick ReplaceOld.
	newMem := Memory{
		ID:         "mem-new",
		Type:       MemoryKnowledge,
		Content:    "how to deploy the service",
		Importance: 0.9,
		Vector:     []float64{1, 0, 0},
		Metadata: map[string]interface{}{
			"problem":  "how to deploy the service",
			"solution": "better answer",
		},
	}

	final := distiller.resolveConflictsPhase(context.Background(), "conv-1", "tenant-1",
		[]memWithEmbedding{{mem: newMem, valid: true}})

	require.Len(t, final, 1, "the new higher-confidence memory must be kept")
	assert.Equal(t, "mem-new", final[0].ID)

	deleted := repo.DeletedIDs()
	require.Equal(t, []string{"exp-old"}, deleted,
		"ReplaceOld must delete the superseded old experience from the repository")
}

// TestKeepOldDoesNotDelete pins the sibling contract: KeepOld (the stored
// memory has higher confidence) discards the incoming duplicate and leaves
// the repository untouched — the delete belongs to ReplaceOld only.
func TestKeepOldDoesNotDelete(t *testing.T) {
	old := Experience{
		ID:         "exp-old",
		Type:       MemoryKnowledge,
		Problem:    "how to deploy the service",
		Solution:   "trusted answer",
		Confidence: 0.95,
		Vector:     []float64{1, 0, 0},
	}
	repo := NewMockExperienceRepository([]Experience{old})
	distiller := NewDistiller(nil, NewMockEmbeddingService(), repo)

	newMem := Memory{
		ID:         "mem-new",
		Type:       MemoryKnowledge,
		Content:    "how to deploy the service",
		Importance: 0.4,
		Vector:     []float64{1, 0, 0},
		Metadata: map[string]interface{}{
			"problem":  "how to deploy the service",
			"solution": "shaky answer",
		},
	}

	final := distiller.resolveConflictsPhase(context.Background(), "conv-1", "tenant-1",
		[]memWithEmbedding{{mem: newMem, valid: true}})

	assert.Empty(t, final, "the lower-confidence duplicate must be discarded")
	assert.Empty(t, repo.DeletedIDs(), "KeepOld must not touch the repository")
}
