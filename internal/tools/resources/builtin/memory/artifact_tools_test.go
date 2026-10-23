package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memctx "github.com/Timwood0x10/ares/internal/runtime/memory/context"
	"github.com/Timwood0x10/ares/internal/userctx"
)

// artifactMock wires the two manager calls the tool consumes: who owns the
// session, and what the ref resolves to.
func artifactMock(owner string, blobs map[string]string) *MockMemoryManager {
	return &MockMemoryManager{
		ownerFunc: func(_ context.Context, _ string) (string, error) { return owner, nil },
		artifactFunc: func(_ context.Context, ref string) (string, error) {
			if blob, ok := blobs[ref]; ok {
				return blob, nil
			}
			return "", memctx.ErrArtifactNotFound
		},
	}
}

// TestArtifactRead_ResolvesTheRef is the closure of the chat path's contract:
// the ref printed in a compressed observation is readable back, byte for byte.
func TestArtifactRead_ResolvesTheRef(t *testing.T) {
	const ref = "artifact://sess-1/3"
	original := strings.Repeat("full transcript line\n", 50)
	tool := NewArtifactRead(artifactMock("user-1", map[string]string{ref: original}))

	res, err := tool.Execute(userctx.With(context.Background(), "user-1"), map[string]interface{}{"ref": ref})
	require.NoError(t, err)
	require.True(t, res.Success)

	data, ok := res.Data.(map[string]interface{})
	require.True(t, ok, "artifact_read returns a structured payload: %v", res.Data)
	assert.Equal(t, original, data["content"])
	assert.Equal(t, false, data["truncated"])
	assert.Equal(t, len([]rune(original)), data["total_runes"])
}

// TestArtifactRead_TruncatesToThePromptGuard: reading back an artifact must not
// be a bypass around the bound that made it an artifact in the first place.
func TestArtifactRead_TruncatesToThePromptGuard(t *testing.T) {
	const ref = "artifact://sess-1/4"
	original := strings.Repeat("x", 5000)
	tool := NewArtifactRead(artifactMock("user-1", map[string]string{ref: original}))

	res, err := tool.Execute(userctx.With(context.Background(), "user-1"),
		map[string]interface{}{"ref": ref, "max_runes": float64(100)})
	require.NoError(t, err)
	require.True(t, res.Success)

	data := res.Data.(map[string]interface{})
	assert.Equal(t, 100, data["returned"])
	assert.Equal(t, true, data["truncated"])
	assert.Equal(t, 5000, data["total_runes"])
	assert.Contains(t, data["hint"], "truncated")
}

// TestArtifactRead_RefusesOtherUsersSession is the security half: a ref is
// model-supplied input, and it names a session. Cross-user reads fail closed
// and say nothing about the content.
func TestArtifactRead_RefusesOtherUsersSession(t *testing.T) {
	const ref = "artifact://sess-other/1"
	tool := NewArtifactRead(artifactMock("user-2", map[string]string{ref: "secret"}))

	res, err := tool.Execute(userctx.With(context.Background(), "user-1"), map[string]interface{}{"ref": ref})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "another user")
	assert.NotContains(t, res.Error, "secret", "a refused read must not leak the content")
}

// TestArtifactRead_DegradesHonestly: a missing blob and a malformed ref are
// different failures, and neither pretends the content exists.
func TestArtifactRead_DegradesHonestly(t *testing.T) {
	tool := NewArtifactRead(artifactMock("user-1", map[string]string{}))
	callerCtx := userctx.With(context.Background(), "user-1")

	res, err := tool.Execute(callerCtx, map[string]interface{}{"ref": "artifact://sess-1/9"})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "no longer retained")
	assert.Contains(t, res.Error, "compressed summary", "the caller is told what to fall back on")

	res, err = tool.Execute(callerCtx, map[string]interface{}{"ref": "not-a-ref"})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "not an artifact ref")

	res, err = tool.Execute(callerCtx, map[string]interface{}{})
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Contains(t, res.Error, "ref is required")
}

// TestArtifactRead_EndToEndWithTheRealStore wires the real SessionMemory behind
// the manager's two methods: the ref that a stored+cleaned message carries must
// be readable through the tool with no hand-made fixture in between.
func TestArtifactRead_EndToEndWithTheRealStore(t *testing.T) {
	ctx := context.Background()
	store := memctx.NewSessionMemory(10, time.Minute).WithMaxArtifactBytes(64 << 10)
	require.NoError(t, store.Set(ctx, "sess-e2e", "user-1", nil))

	original := strings.Repeat("log line with context\n", 30)
	require.NoError(t, store.AddMessage(ctx, "sess-e2e", memctx.Message{
		Role:    memctx.RoleToolResult,
		Content: original,
	}))

	msgs, err := store.GetMessages(ctx, "sess-e2e")
	require.NoError(t, err)
	require.NotEmpty(t, msgs[0].ArtifactRefs)
	ref := msgs[0].ArtifactRefs[0]

	// What the model actually sees: the compressed marker carrying the ref.
	cleaned := memctx.NewContextCleaner().Clean(msgs)
	require.Contains(t, cleaned[0].Content, "ref:"+ref)

	tool := NewArtifactRead(&MockMemoryManager{
		ownerFunc: func(_ context.Context, sessionID string) (string, error) {
			if s, ok := store.Get(ctx, sessionID); ok {
				return s.UserID, nil
			}
			return "", nil
		},
		artifactFunc: func(_ context.Context, r string) (string, error) { return store.GetArtifact(ctx, r) },
	})

	res, err := tool.Execute(userctx.With(ctx, "user-1"), map[string]interface{}{"ref": ref})
	require.NoError(t, err)
	require.True(t, res.Success, "tool error: %s", res.Error)
	assert.Equal(t, original, res.Data.(map[string]interface{})["content"],
		"the round trip returns the ORIGINAL bytes")
}

// TestArtifactRead_FailsClosedOnUnattributableOwner is the regression test for
// the fail-open the previous check had: `caller != "" && owner != ""` meant an
// unattributed session (owner "") was readable by ANY caller, and a caller with
// no user scope skipped the check entirely. Both are refusals now, matching
// UserProfile's rule ("no user ⇒ no read") rather than contradicting it.
func TestArtifactRead_FailsClosedOnUnattributableOwner(t *testing.T) {
	const ref = "artifact://sess-anon/1"
	blobs := map[string]string{ref: "someone's bytes"}

	t.Run("unattributed session is not readable", func(t *testing.T) {
		tool := NewArtifactRead(artifactMock("", blobs))
		res, err := tool.Execute(userctx.With(context.Background(), "user-1"), map[string]interface{}{"ref": ref})
		require.NoError(t, err)
		assert.False(t, res.Success)
		assert.Contains(t, res.Error, "no attributable owner")
		assert.NotContains(t, res.Error, "bytes", "a refusal must not leak content")
	})

	t.Run("caller with no user scope is refused", func(t *testing.T) {
		tool := NewArtifactRead(artifactMock("user-1", blobs))
		res, err := tool.Execute(context.Background(), map[string]interface{}{"ref": ref})
		require.NoError(t, err)
		assert.False(t, res.Success)
		assert.Contains(t, res.Error, "user_id is required")
	})

	t.Run("legacy user_id parameter still satisfies an attributed session", func(t *testing.T) {
		tool := NewArtifactRead(artifactMock("user-1", blobs))
		res, err := tool.Execute(context.Background(), map[string]interface{}{"ref": ref, "user_id": "user-1"})
		require.NoError(t, err)
		require.True(t, res.Success, "tool error: %s", res.Error)
		assert.Equal(t, "someone's bytes", res.Data.(map[string]interface{})["content"])
	})
}
