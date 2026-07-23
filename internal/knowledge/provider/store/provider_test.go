package store

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStoreProviderNilStore verifies the constructor rejects invalid config.
func TestStoreProviderNilStore(t *testing.T) {
	if _, err := NewStoreProvider(nil, Config{Name: "x"}); err == nil {
		t.Error("expected error for nil store")
	}
	if _, err := NewStoreProvider(memorystore.New(), Config{}); err == nil {
		t.Error("expected error for empty name")
	}
}

// TestStoreProviderStreamQueryNamespace verifies the read-side: Stream with an
// empty goal lists the configured namespace via Query, so the compiler's
// persisted objects flow out of the store. This is the core "dead-end store
// now has a reader" proof.
func TestStoreProviderStreamQueryNamespace(t *testing.T) {
	ctx := context.Background()
	objs := []*knowledge.KnowledgeObject{
		{ID: "entity-ares", Namespace: "conversation-compiler", Summary: "ARES system", Confidence: 0.9, Tags: []string{"ares"}},
		{ID: "entity-patch", Namespace: "conversation-compiler", Summary: "Patch for updates", Confidence: 0.8, Tags: []string{"patch"}},
	}
	s := memorystore.New()
	require.NoError(t, s.Save(ctx, objs...))

	p, err := NewStoreProvider(s, Config{Name: "compiler-store", Namespace: "conversation-compiler", IntentTags: []string{"conversation"}, Limit: 50})
	require.NoError(t, err)

	objCh, errCh := p.Stream(ctx, knowledge.Intent{})
	got := collect(t, objCh, errCh)
	require.Len(t, got, 2, "both compiler objects must be streamed out of the store")
}

// TestStoreProviderStreamSearchKeyword verifies the keyword Search path: a
// goal mentioning "ares" returns only the matching object.
func TestStoreProviderStreamSearchKeyword(t *testing.T) {
	ctx := context.Background()
	objs := []*knowledge.KnowledgeObject{
		{ID: "e1", Namespace: "conversation-compiler", Summary: "ARES uses Patch for runtime updates", Confidence: 0.9},
		{ID: "e2", Namespace: "conversation-compiler", Summary: "Go implements concurrency", Confidence: 0.8},
	}
	s := memorystore.New()
	require.NoError(t, s.Save(ctx, objs...))

	p, err := NewStoreProvider(s, Config{Name: "compiler-store", Namespace: "conversation-compiler"})
	require.NoError(t, err)

	objCh, errCh := p.Stream(ctx, knowledge.Intent{Goal: "how does ares work"})
	got := collect(t, objCh, errCh)
	require.Len(t, got, 1, "keyword search should return only the ARES object")
	assert.Equal(t, "e1", got[0].ID)
}

// TestStoreProviderIntentMatch verifies the scoring heuristic mirrors the
// vector provider: a non-zero baseline keeps the store discoverable, a
// matching tag boosts the score, and an unmatched goal stays weak-but-selected.
func TestStoreProviderIntentMatch(t *testing.T) {
	p, err := NewStoreProvider(memorystore.New(), Config{Name: "x", IntentTags: []string{"conversation", "memory"}})
	require.NoError(t, err)

	assert.Equal(t, 0.4, p.IntentMatch(knowledge.Intent{}), "empty goal -> generic baseline 0.4")
	assert.Equal(t, 0.4, p.IntentMatch(knowledge.Intent{Goal: "anything"}), "tags configured but unmatched -> baseline 0.4 (above 0.35 discovery threshold, always consulted)")

	assert.Equal(t, 0.4, p.IntentMatch(knowledge.Intent{Goal: "stock price"}), "unmatched goal -> baseline 0.4")

	boosted := p.IntentMatch(knowledge.Intent{Goal: "summarize the conversation memory"})
	assert.Greater(t, boosted, 0.4, "matching tag must boost above baseline")
	assert.LessOrEqual(t, boosted, 1.0)
}

// collect drains the object channel (closed by the provider) and fails the
// test if the error channel carries a non-nil error.
func collect(t *testing.T, objCh <-chan *knowledge.KnowledgeObject, errCh <-chan error) []*knowledge.KnowledgeObject {
	t.Helper()
	var out []*knowledge.KnowledgeObject
	for o := range objCh {
		out = append(out, o)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
	default:
	}
	return out
}
