// Package memory provides a GraphProvider that wraps a memory service,
// converting historical task data into KnowledgeObject streams.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TaskSearcher is the minimal interface needed to query historical tasks.
type TaskSearcher interface {
	SearchSimilarTasks(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// SearchResult is a single task returned by TaskSearcher.
type SearchResult struct {
	ID        string
	Summary   string
	Timestamp time.Time
}

// MemoryProvider wraps a TaskSearcher as a GraphProvider.
// It maps the user's intent.Goal to a similarity search over past tasks.
type MemoryProvider struct {
	name     string
	searcher TaskSearcher
}

// New creates a MemoryProvider.
func New(name string, searcher TaskSearcher) *MemoryProvider {
	return &MemoryProvider{name: name, searcher: searcher}
}

// Name returns the provider identifier.
func (p *MemoryProvider) Name() string { return p.name }

// IntentMatch returns 0.8 (high relevance for any knowledge intent).
func (p *MemoryProvider) IntentMatch(_ knowledge.Intent) float64 {
	return 0.8
}

// Stream searches similar tasks and emits them as KnowledgeObjects.
func (p *MemoryProvider) Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 64)
	errCh := make(chan error, 1)

	go func() {
		defer close(objCh)
		defer close(errCh)

		limit := intent.Scope.MaxObjects
		if limit <= 0 {
			limit = 20
		}

		results, err := p.searcher.SearchSimilarTasks(ctx, intent.Goal, limit)
		if err != nil {
			errCh <- fmt.Errorf("memory provider %q: %w", p.name, err)
			return
		}

		for _, r := range results {
			summary := r.Summary
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}

			obj := &knowledge.KnowledgeObject{
				ID:         fmt.Sprintf("%s_%s", p.name, r.ID),
				Type:       knowledge.ObjectMemory,
				Namespace:  p.name,
				Summary:    summary,
				Confidence: 0.7,
				CreatedAt:  r.Timestamp,
				UpdatedAt:  time.Now(),
			}

			select {
			case objCh <- obj:
			case <-ctx.Done():
				return
			}
		}
	}()

	return objCh, errCh
}
