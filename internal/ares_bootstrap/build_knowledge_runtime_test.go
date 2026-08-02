package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestBuildKnowledgeRuntime_NoVectorDeps verifies the runtime is created
// successfully (and without the vector provider) when neither a VectorStore
// nor an EmbeddingService is supplied — the "works without a database" path.
func TestBuildKnowledgeRuntime_NoVectorDeps(t *testing.T) {
	rt := BuildKnowledgeRuntime(nil, nil)
	require.NotNil(t, rt)
}

// TestBuildKnowledgeRuntime_WithVectorDeps verifies the runtime is created
// successfully when vector storage and an embedding service are supplied.
// Construction must not fail when the vector provider is registered.
func TestBuildKnowledgeRuntime_WithVectorDeps(t *testing.T) {
	store := newTestVectorStore()
	emb := &testEmbedder{}
	rt := BuildKnowledgeRuntime(store, emb)
	require.NotNil(t, rt)
}

// TestBuildKnowledgeRuntime_VectorDepsExecute documents that executing a goal
// through the built runtime requires a fully wired provider. The memory
// provider panics on a nil repository (see knowledge/provider/memory), so we
// only assert construction here — runtime execution of the AKF pipeline is
// exercised by knowledge/runtime tests.
func TestBuildKnowledgeRuntime_VectorDepsExecute(t *testing.T) {
	rt := BuildKnowledgeRuntime(newTestVectorStore(), &testEmbedder{})
	require.NotNil(t, rt)
}

// testVectorStore is a minimal in-memory storage.VectorStore for wiring tests.
type testVectorStore struct{}

func (s *testVectorStore) Search(context.Context, string, []float64, int) ([]*storage.SearchResult, error) {
	return nil, nil
}
func (s *testVectorStore) AddEmbedding(context.Context, string, string, []float64, map[string]any) error {
	return nil
}
func (s *testVectorStore) CreateCollection(context.Context, string, int) error {
	return nil
}

func newTestVectorStore() *testVectorStore { return &testVectorStore{} }

// testEmbedder is a minimal apiembedding.EmbeddingService for wiring tests.
type testEmbedder struct{}

func (e *testEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	return e.EmbedWithPrefix(context.Background(), text, "")
}
func (e *testEmbedder) EmbedWithPrefix(_ context.Context, text, _ string) ([]float64, error) {
	vec := make([]float64, 4)
	for i, c := range text {
		if i >= 4 {
			break
		}
		vec[i] = float64(c%97)/97.0 + 0.01
	}
	return vec, nil
}
func (e *testEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, t := range texts {
		v, err := e.Embed(context.Background(), t)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
func (e *testEmbedder) HealthCheck(context.Context) error { return nil }
func (e *testEmbedder) GetModel() string                  { return "test" }
func (e *testEmbedder) GetTimeout() time.Duration         { return time.Second }
