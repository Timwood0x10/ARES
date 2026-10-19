package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildPromptMessages_AppliesTheTokenBudget is the gap a message-count window leaves open:
// MaxHistory bounds how many TURNS are considered, not how large the request is. Ten long tool
// results pass a window of ten and still overflow the model's context, and this is the slice
// that actually reaches the model.
func TestBuildPromptMessages_AppliesTheTokenBudget(t *testing.T) {
	ctx := context.Background()
	// ~200 conservative tokens per message: large enough that a small budget must bite.
	long := strings.Repeat("context detail ", 100)

	build := func(budget int) []Message {
		cfg := DefaultMemoryConfig()
		cfg.MaxHistory = 10
		cfg.ContextTokenBudget = budget

		mgr, err := NewMemoryManager(cfg)
		require.NoError(t, err)
		t.Cleanup(func() { _ = mgr.Stop(ctx) })

		sessionID, err := mgr.CreateSession(ctx, "budget-user")
		require.NoError(t, err)
		for i := 0; i < 10; i++ {
			require.NoError(t, mgr.AddStructuredMessage(ctx, sessionID,
				Message{Role: "user", Content: long}))
		}

		msgs, err := mgr.BuildPromptMessages(ctx, sessionID)
		require.NoError(t, err)
		return msgs
	}

	// 0 = disabled: the count window alone decides, which is the previous behaviour.
	unbounded := build(0)
	require.NotEmpty(t, unbounded)

	// A budget far below the window's cost must cut it down.
	bounded := build(300)
	assert.Less(t, len(bounded), len(unbounded),
		"a token budget must cut the prompt below what the count window alone allows")
}
