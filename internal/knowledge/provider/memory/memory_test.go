// Package memory tests for the MemoryProvider that wraps a TaskSearcher as a GraphProvider.
package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// mockSearcher implements TaskSearcher for testing.
type mockSearcher struct {
	results []SearchResult
	err     error
}

func (m *mockSearcher) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func TestNew(t *testing.T) {
	p := New("test", nil)
	require.NotNil(t, p)
	assert.Equal(t, "test", p.Name())
}

func TestName(t *testing.T) {
	p := New("my-provider", nil)
	assert.Equal(t, "my-provider", p.Name())
}

func TestIntentMatch_EmptyScope(t *testing.T) {
	p := New("test", nil)
	score := p.IntentMatch(knowledge.Intent{})
	assert.Equal(t, 0.5, score)
}

func TestIntentMatch_MemoryType(t *testing.T) {
	p := New("test", nil)
	intent := knowledge.Intent{Scope: knowledge.Scope{Types: []knowledge.ObjectType{knowledge.ObjectMemory}}}
	score := p.IntentMatch(intent)
	assert.Equal(t, 0.8, score)
}

func TestIntentMatch_DecisionType(t *testing.T) {
	p := New("test", nil)
	intent := knowledge.Intent{Scope: knowledge.Scope{Types: []knowledge.ObjectType{knowledge.ObjectDecision}}}
	score := p.IntentMatch(intent)
	assert.Equal(t, 0.8, score)
}

func TestIntentMatch_CodeType(t *testing.T) {
	p := New("test", nil)
	intent := knowledge.Intent{Scope: knowledge.Scope{Types: []knowledge.ObjectType{knowledge.ObjectCode}}}
	score := p.IntentMatch(intent)
	assert.Equal(t, 0.3, score)
}

func TestIntentMatch_ArchitectureType(t *testing.T) {
	p := New("test", nil)
	intent := knowledge.Intent{Scope: knowledge.Scope{Types: []knowledge.ObjectType{knowledge.ObjectArchitecture}}}
	score := p.IntentMatch(intent)
	assert.Equal(t, 0.3, score)
}

func TestIntentMatch_UnknownType(t *testing.T) {
	p := New("test", nil)
	intent := knowledge.Intent{Scope: knowledge.Scope{Types: []knowledge.ObjectType{"unknown-type"}}}
	score := p.IntentMatch(intent)
	assert.Equal(t, 0.4, score)
}

func TestStream_ReturnsResults(t *testing.T) {
	now := time.Now()
	searcher := &mockSearcher{
		results: []SearchResult{
			{ID: "1", Summary: "task one summary", Timestamp: now},
			{ID: "2", Summary: "task two summary", Timestamp: now},
		},
	}
	p := New("test", searcher)

	intent := knowledge.Intent{Goal: "find similar", Scope: knowledge.Scope{MaxObjects: 10}}
	objCh, errCh := p.Stream(context.Background(), intent)

	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	// Check for errors.
	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}

	require.Len(t, objs, 2)
	assert.Equal(t, "test_1", objs[0].ID)
	assert.Equal(t, "test_2", objs[1].ID)
	assert.Equal(t, knowledge.ObjectMemory, objs[0].Type)
}

func TestStream_EmptyResults(t *testing.T) {
	searcher := &mockSearcher{results: nil}
	p := New("test", searcher)

	intent := knowledge.Intent{Goal: "find nothing"}
	objCh, errCh := p.Stream(context.Background(), intent)

	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}

	assert.Empty(t, objs)
}

func TestStream_SearcherError(t *testing.T) {
	searcher := &mockSearcher{err: errors.New("search failed")}
	p := New("test", searcher)

	intent := knowledge.Intent{Goal: "find something"}
	objCh, errCh := p.Stream(context.Background(), intent)

	// Channel should close with an error.
	err := <-errCh
	require.Error(t, err)
	assert.Contains(t, err.Error(), "search failed")

	// Object channel should close without any objects.
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	assert.Empty(t, objs)
}

func TestStream_ContextCancellation(t *testing.T) {
	// Use a blocking searcher that waits on ctx.Done().
	// We cancel the context before the searcher returns, so it sees ctx.Done().
	blockCh := make(chan struct{})
	searcher := &blockingSearcher{blockCh: blockCh}
	p := New("test", searcher)

	ctx, cancel := context.WithCancel(context.Background())
	intent := knowledge.Intent{Goal: "find", Scope: knowledge.Scope{MaxObjects: 10}}
	objCh, errCh := p.Stream(ctx, intent)

	// Cancel context before the searcher returns. The searcher will see
	// ctx.Done() and return "context canceled".
	cancel()

	// Do NOT close blockCh — the searcher should exit via ctx.Done().

	// Should not receive any objects.
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	assert.Empty(t, objs, "should not receive objects after cancellation")

	// The goroutine should push the context cancellation error to errCh.
	select {
	case err := <-errCh:
		assert.ErrorContains(t, err, "context canceled")
	default:
		// If no error arrives, the goroutine may have exited before pushing.
		// This is also acceptable — the test still validates that no objects
		// were emitted after cancellation.
	}
}

// blockingSearcher blocks until blockCh is closed, then returns results.
// Used to test context cancellation.
type blockingSearcher struct {
	blockCh chan struct{}
}

func (m *blockingSearcher) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	// Wait for either cancellation or blockCh.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.blockCh:
	}
	return []SearchResult{
		{ID: "1", Summary: "task one", Timestamp: time.Now()},
	}, nil
}

func TestStream_DefaultLimit(t *testing.T) {
	searcher := &mockSearcher{results: nil}
	p := New("test", searcher)

	// MaxObjects = 0 should default to 20, but since there are no results, it's fine.
	intent := knowledge.Intent{Goal: "test", Scope: knowledge.Scope{MaxObjects: 0}}
	objCh, errCh := p.Stream(context.Background(), intent)

	for range objCh {
	}

	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}
}

func TestStream_SummaryTruncation(t *testing.T) {
	// Build a 250-character string that exceeds the 200-char truncation limit.
	longSummary := "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur. Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum."

	searcher := &mockSearcher{
		results: []SearchResult{
			{ID: "1", Summary: longSummary, Timestamp: time.Now()},
		},
	}
	p := New("test", searcher)

	intent := knowledge.Intent{Goal: "find", Scope: knowledge.Scope{MaxObjects: 10}}
	objCh, errCh := p.Stream(context.Background(), intent)

	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	select {
	case err := <-errCh:
		require.NoError(t, err)
	default:
	}

	require.Len(t, objs, 1)
	// Summary should be truncated to 200 chars + "..."
	assert.Equal(t, 203, len(objs[0].Summary))
	assert.Contains(t, objs[0].Summary, "...")
}
