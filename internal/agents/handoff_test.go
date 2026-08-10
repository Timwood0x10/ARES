package agents

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ArtifactRef ─────────────────────────────

func TestArtifactRef_String(t *testing.T) {
	artifact := ArtifactRef{Path: "/tmp/notes.md", Type: "file", Summary: "research notes"}
	assert.Equal(t, "file:/tmp/notes.md (research notes)", artifact.String())
}

// ── NewHandoff ──────────────────────────────

func TestNewHandoff_InitialState(t *testing.T) {
	h := NewHandoff("researcher", "coder", "implement the plan")
	require.NotNil(t, h)
	assert.Equal(t, "researcher", h.From)
	assert.Equal(t, "coder", h.To)
	assert.Equal(t, "implement the plan", h.Task)
	assert.NotNil(t, h.Context)
	assert.Empty(t, h.Context)
	assert.NotNil(t, h.Artifacts)
	assert.Empty(t, h.Artifacts)
	assert.NotNil(t, h.Metadata)
	assert.Empty(t, h.Metadata)
	assert.False(t, h.CreatedAt.IsZero())
	assert.WithinDuration(t, time.Now(), h.CreatedAt, time.Second)
}

// ── Builder chain ───────────────────────────

func TestHandoff_BuilderChain(t *testing.T) {
	h := NewHandoff("researcher", "coder", "implement the plan").
		WithContext("goal", "ship v0.3.0").
		WithArtifact("/tmp/notes.md", "file", "research notes").
		WithArtifact("key_findings", "data", "3 key facts").
		WithMetadata("trace_id", "trace-123")

	require.NotNil(t, h)
	assert.Equal(t, "ship v0.3.0", h.Context["goal"])
	require.Len(t, h.Artifacts, 2)
	assert.Equal(t, "/tmp/notes.md", h.Artifacts[0].Path)
	assert.Equal(t, "trace-123", h.Metadata["trace_id"])
}

func TestHandoff_WithContextOverwrites(t *testing.T) {
	h := NewHandoff("a", "b", "task").
		WithContext("key", "v1").
		WithContext("key", "v2")
	assert.Equal(t, "v2", h.Context["key"])
	assert.Len(t, h.Context, 1)
}

// ── HasArtifact / ArtifactOfType ────────────

func TestHandoff_HasArtifact(t *testing.T) {
	h := NewHandoff("a", "b", "task").
		WithArtifact("/tmp/notes.md", "file", "notes").
		WithArtifact("summary", "summary", "one-liner")

	assert.True(t, h.HasArtifact("file"))
	assert.True(t, h.HasArtifact("summary"))
	assert.False(t, h.HasArtifact("code"))
	assert.False(t, h.HasArtifact(""))
}

func TestHandoff_ArtifactOfType(t *testing.T) {
	t.Run("returns first matching artifact", func(t *testing.T) {
		h := NewHandoff("a", "b", "task").
			WithArtifact("/tmp/a.md", "file", "first").
			WithArtifact("/tmp/b.md", "file", "second")
		artifact := h.ArtifactOfType("file")
		require.NotNil(t, artifact)
		assert.Equal(t, "/tmp/a.md", artifact.Path)
	})

	t.Run("returns nil when type absent", func(t *testing.T) {
		h := NewHandoff("a", "b", "task").
			WithArtifact("/tmp/a.md", "file", "first")
		assert.Nil(t, h.ArtifactOfType("code"))
	})

	t.Run("returns nil on empty handoff", func(t *testing.T) {
		h := NewHandoff("a", "b", "task")
		assert.Nil(t, h.ArtifactOfType("file"))
	})
}

// ── Size ────────────────────────────────────

func TestHandoff_Size(t *testing.T) {
	t.Run("empty handoff has size zero", func(t *testing.T) {
		h := NewHandoff("a", "b", "task")
		assert.Zero(t, h.Size())
	})

	t.Run("counts artifacts and context keys", func(t *testing.T) {
		h := NewHandoff("a", "b", "task").
			WithContext("goal", "x").
			WithContext("constraint", "y").
			WithArtifact("/tmp/a.md", "file", "a")
		assert.Equal(t, 3, h.Size())
	})

	t.Run("metadata is not counted", func(t *testing.T) {
		h := NewHandoff("a", "b", "task").
			WithMetadata("trace_id", "t-1")
		assert.Zero(t, h.Size())
	})
}

// ── String ──────────────────────────────────

func TestHandoff_String(t *testing.T) {
	h := NewHandoff("researcher", "coder", "implement").
		WithArtifact("/tmp/a.md", "file", "a").
		WithContext("goal", "x")
	s := h.String()
	assert.Contains(t, s, "researcher→coder")
	assert.Contains(t, s, "1 artifacts")
	assert.Contains(t, s, "1 context keys")
}
