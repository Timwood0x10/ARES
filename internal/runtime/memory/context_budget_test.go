package memory

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEffectiveContextTokenBudget_Derives45Percent pins the decided default: when a model
// window is configured and no explicit budget is set, the prompt budget is 45% of it.
//
// 45 rather than 50+ because the prompt is not alone in the window — the answer, the skills
// block and RAG snippets are added around it, and a budget at the edge leaves them nowhere.
func TestEffectiveContextTokenBudget_Derives45Percent(t *testing.T) {
	cfg := MemoryConfig{ModelContextWindow: 100000}
	assert.Equal(t, 45000, cfg.EffectiveContextTokenBudget())

	// An explicit budget always wins: a deployment that measured its own prompts pins the
	// number, and a derived default must not override it.
	cfg = MemoryConfig{ModelContextWindow: 100000, ContextTokenBudget: 1234}
	assert.Equal(t, 1234, cfg.EffectiveContextTokenBudget())

	// Unknown window means disabled, not a guess.
	assert.Zero(t, MemoryConfig{}.EffectiveContextTokenBudget())
}
