// Package store implements a GraphProvider backed by a knowledge.KnowledgeStore.
//
// It is the read-side counterpart to any producer that persists
// KnowledgeObjects into the same store (e.g. the Conversation Compiler's
// AKGBuilder). The AKG data path is Provider -> Pipeline -> KnowledgeRuntime,
// and the store is otherwise a dead-end cache that nothing reads. Registering
// this provider into the runtime's ProviderRegistry makes the persisted
// objects flow back into retrieval (Discover -> Load), so a producer's writes
// are actually consumed instead of sitting unwatched.
package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// StoreProvider implements GraphProvider by reading KnowledgeObjects from a
// knowledge.KnowledgeStore. The same store instance a producer writes into is
// handed to this provider, closing the write->read loop for that data.
type StoreProvider struct {
	store  knowledge.KnowledgeStore
	config Config
}

// Config configures the StoreProvider.
type Config struct {
	// Name is the provider name used for identification and routing.
	Name string
	// Namespace filters objects read from the store (empty = all namespaces).
	Namespace string
	// IntentTags are keywords used by IntentMatch to score relevance.
	IntentTags []string
	// Limit caps the number of objects streamed per Execute (0 = DefaultLimit).
	Limit int
}

// DefaultLimit is the cap applied when Config.Limit is unset.
const DefaultLimit = 200

// NewStoreProvider creates a StoreProvider backed by the given store.
func NewStoreProvider(store knowledge.KnowledgeStore, cfg Config) (*StoreProvider, error) {
	if store == nil {
		return nil, fmt.Errorf("store provider %s: store is nil", cfg.Name)
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("store provider: name is required")
	}
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultLimit
	}
	return &StoreProvider{store: store, config: cfg}, nil
}

// Name returns the provider identifier.
func (p *StoreProvider) Name() string { return p.config.Name }

// IntentMatch scores relevance based on configured intent tags. Returns
// 0.0-1.0 where higher = more relevant. The store holds the agent's own
// conversation-derived knowledge, so it is a general fallback that must be
// consulted for essentially every query — hence a baseline of 0.4, comfortably
// above the discovery threshold (0.35), even when no intent tag matches. A
// matching tag boosts the score so targeted queries rank the store higher.
func (p *StoreProvider) IntentMatch(intent knowledge.Intent) float64 {
	if len(p.config.IntentTags) == 0 || intent.Goal == "" {
		return 0.4 // generic baseline: always discoverable
	}
	goal := strings.ToLower(intent.Goal)
	matches := 0
	for _, tag := range p.config.IntentTags {
		if strings.Contains(goal, strings.ToLower(tag)) {
			matches++
		}
	}
	if matches == 0 {
		return 0.4 // baseline: general agent knowledge, always consulted
	}
	return 0.5 + (float64(matches)/float64(len(p.config.IntentTags)))*0.5
}

// Stream reads objects from the backing store and emits them one at a time.
// The store holds the agent's own conversation-derived knowledge, so the
// primary behavior is to dump the configured namespace (Query) and let the
// runtime's pipeline/reducer rank by relevance. When the intent goal is
// non-empty a keyword Search is attempted first as a refinement; if it finds
// nothing (a keyword miss) we fall back to the full namespace so the store's
// knowledge is never silently dropped. The channels are closed on completion
// or when ctx is cancelled. Errors are sent through the error channel rather
// than swallowed.
func (p *StoreProvider) Stream(ctx context.Context, intent knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	objCh := make(chan *knowledge.KnowledgeObject, 32)
	errCh := make(chan error, 1)

	go func() {
		defer close(objCh)
		defer close(errCh)

		limit := intent.Scope.MaxObjects
		if limit <= 0 || limit > p.config.Limit {
			limit = p.config.Limit
		}

		var objs []*knowledge.KnowledgeObject
		var err error
		if intent.Goal != "" {
			objs, err = p.store.Search(ctx, intent.Goal, "", limit)
			if err == nil && len(objs) == 0 {
				// Keyword miss: fall back to the full namespace so the
				// store's knowledge is still surfaced to retrieval.
				objs, err = p.store.Query(ctx, knowledge.Query{Namespace: p.config.Namespace, Limit: limit})
			}
		} else {
			objs, err = p.store.Query(ctx, knowledge.Query{Namespace: p.config.Namespace, Limit: limit})
		}
		if err != nil {
			errCh <- fmt.Errorf("store provider %s: read: %w", p.config.Name, err)
			return
		}

		for _, o := range objs {
			if o == nil {
				continue
			}
			select {
			case objCh <- o:
			case <-ctx.Done():
				return
			}
		}
	}()

	return objCh, errCh
}

// compile-time interface check.
var _ provider.GraphProvider = (*StoreProvider)(nil)
