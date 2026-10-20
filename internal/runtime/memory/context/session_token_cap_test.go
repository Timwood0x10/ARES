package context

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSessionMemory_TokenCapBoundsStorage: the message count bounds how MANY messages a
// session keeps, not how much memory it holds — one large tool result is a single message.
func TestSessionMemory_TokenCapBoundsStorage(t *testing.T) {
	ctx := context.Background()
	mem := NewSessionMemory(10, time.Hour).WithMaxMessages(100).WithMaxTokens(3000)

	require.NoError(t, mem.Set(ctx, "s1", "u1", nil))
	big := strings.Repeat("a", 4000) // ~1200 tokens by the 0.3/char rate
	for i := 0; i < 6; i++ {
		require.NoError(t, mem.AddMessage(ctx, "s1", Message{Role: "user", Content: big}))
	}

	msgs, err := mem.GetMessages(ctx, "s1")
	require.NoError(t, err)
	require.NotEmpty(t, msgs, "the newest message must survive: an empty session looks missing")
	assert.Less(t, len(msgs), 6, "the token cap must drop older messages")

	total := 0
	for _, m := range msgs {
		total += sessionMessageTokens(m)
	}
	assert.LessOrEqual(t, total, 3000+1200, "the retained history must fit the cap plus one message")
}
