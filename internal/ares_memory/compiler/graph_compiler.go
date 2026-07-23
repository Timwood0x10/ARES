// Package compiler — GraphCompiler builds the KM graph from extracted entities and facts.
package compiler

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

// GraphCompiler is the third stage of the Compiler pipeline.
// It takes normalized entities and facts and builds the Knowledge Model graph:
// deduplication, merging, linking, and pruning.
type GraphCompiler struct{}

// NewGraphCompiler creates a new GraphCompiler.
func NewGraphCompiler() *GraphCompiler {
	return &GraphCompiler{}
}

// Compile builds a Knowledge Model from normalized entities and facts.
//
// Pipeline:
//  1. Create nodes for each entity (deduplicated by name).
//  2. Create nodes for each fact (deduplicated by subject + predicate + object).
//  3. Link nodes via edges based on entity references in facts.
//  4. Merge with previous model if incremental.
//  5. Prune to configured limits.
//
// Args:
//
//	ctx — context for cancellation and timeout.
//	entities — normalized entities.
//	facts — normalized facts.
//	cfg — CompileConfig with limits and incremental mode.
//
// Returns:
//
//	*KnowledgeModel — the compiled knowledge model.
//	error — non-nil if compilation fails.
func (g *GraphCompiler) Compile(ctx context.Context, entities []ExtractedEntity, facts []ExtractedFact, cfg CompileConfig) (*KnowledgeModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("graph compiler: context cancelled: %w", err)
	}

	var model *KnowledgeModel
	if cfg.Incremental && cfg.PreviousModel != nil {
		model = cfg.PreviousModel
	} else {
		model = NewKnowledgeModel()
	}

	// Phase 1: Add entity nodes.
	entityAdded := make(map[string]bool) // Track deduplicated entities.
	for _, e := range entities {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("graph compiler: context cancelled during entity phase: %w", err)
		}
		if entityAdded[e.Name] {
			continue // Duplicate entity name within this batch.
		}
		entityID := fmt.Sprintf("entity-%s", e.Name)
		if _, exists := model.Nodes[entityID]; exists {
			// Entity already present from a previous (incremental) compile.
			entityAdded[e.Name] = true
			continue
		}
		entityAdded[e.Name] = true

		node := &Node{
			ID:         entityID,
			Type:       NodeEntity,
			Confidence: e.Confidence,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Source:     e.SourceID,
			Attributes: map[string]any{
				attrName:     e.Name,
				"type":       e.Type,
				"aliases":    e.Aliases,
				"properties": e.Properties,
			},
		}
		if err := model.AddNode(node); err != nil {
			return nil, fmt.Errorf("graph compiler: add entity node %q: %w", e.Name, err)
		}
	}

	// Phase 2: Add fact nodes and link to entities.
	for _, f := range facts {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("graph compiler: context cancelled during fact phase: %w", err)
		}

		// Content-based fact ID deduplicates identical facts across incremental
		// compiles (same triple → same node), instead of colliding on batch index.
		factID := fmt.Sprintf("fact-%s", factHash(f))
		if _, exists := model.Nodes[factID]; exists {
			continue // Duplicate fact already in model (incremental merge).
		}
		factNode := &Node{
			ID:         factID,
			Type:       NodeFact,
			Confidence: f.Confidence,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
			Source:     f.SourceID,
			Attributes: map[string]any{
				attrSubject:   f.Subject,
				attrPredicate: f.Predicate,
				attrObject:    f.Object,
			},
		}
		if err := model.AddNode(factNode); err != nil {
			return nil, fmt.Errorf("graph compiler: add fact node: %w", err)
		}

		// Link fact to subject entity if it exists.
		subjectID := fmt.Sprintf("entity-%s", f.Subject)
		if _, exists := model.Nodes[subjectID]; exists {
			_ = model.AddEdge(Edge{
				ID:        fmt.Sprintf("edge-%s-subject", factID),
				Type:      EdgeMentions,
				Source:    factID,
				Target:    subjectID,
				Weight:    f.Confidence,
				CreatedAt: time.Now(),
			})
		}

		// Link fact to object entity if it exists.
		objectID := fmt.Sprintf("entity-%s", f.Object)
		if _, exists := model.Nodes[objectID]; exists {
			_ = model.AddEdge(Edge{
				ID:        fmt.Sprintf("edge-%s-object", factID),
				Type:      EdgeMentions,
				Source:    factID,
				Target:    objectID,
				Weight:    f.Confidence,
				CreatedAt: time.Now(),
			})
		}
	}

	model.Metadata.CompileCount++
	model.Metadata.SourceCount = len(entities) + len(facts)
	model.Metadata.UpdatedAt = time.Now()

	return model, nil
}

// factHash returns a deterministic short hash of a fact's content (subject,
// predicate, object), lowercased and pipe-joined. It is used to deduplicate
// identical facts across incremental compiles so the same triple always maps to
// the same node ID instead of colliding on a batch-local index.
func factHash(f ExtractedFact) string {
	raw := strings.ToLower(f.Subject + "|" + f.Predicate + "|" + f.Object)
	h := fnv.New32a()
	_, _ = h.Write([]byte(raw))
	return fmt.Sprintf("%x", h.Sum32())
}
