// Package compiler — AKGBuilder is the Consumer layer (design §5.3) that
// projects a KM SubGraph into AKG KnowledgeObjects and Relations. Entities and
// facts become KnowledgeObjects; SubGraph edges become Relations. Summaries
// are derived directly from node attributes — ZERO LLM calls.
package compiler

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// AKGBuilder consumes a KM SubGraph and projects it into AKG KnowledgeObjects
// (entities → objects, facts → objects with relations). It writes to a
// knowledge.KnowledgeStore when configured. Zero LLM: summaries are derived
// directly from node attributes, never via an LLM summarizer.
type AKGBuilder struct {
	store    knowledge.KnowledgeStore     // optional, nil = build-only
	pipeline *knowledge.KnowledgePipeline // optional, nil = skip AKG refinement
}

// NewAKGBuilder creates an AKGBuilder. A nil store means Build will only
// construct objects/relations without persisting them.
//
// Args:
//
//	store — optional knowledge.KnowledgeStore for persistence; may be nil.
//
// Returns:
//
//	*AKGBuilder — the configured builder. Always non-nil.
func NewAKGBuilder(store knowledge.KnowledgeStore) *AKGBuilder {
	return &AKGBuilder{store: store}
}

// WithAKGPipeline attaches an optional AKG KnowledgePipeline used to refine
// each projected KnowledgeObject (Normalizer → EntityMatcher → Validator →
// Summarizer) before persistence. This lets the builder reuse AKG's shared
// processing instead of persisting raw node summaries, closing the
// broken-link-2 gap from the review (the builder previously bypassed the
// pipeline and wrote coarse node summaries straight to the store).
//
// When nil (the default), Build keeps the previous build-only-direct behavior
// for backward compatibility.
//
// Args:
//
//	p — optional *knowledge.KnowledgePipeline; may be nil.
//
// Returns:
//
//	*AKGBuilder — the same builder for chaining.
func (b *AKGBuilder) WithAKGPipeline(p *knowledge.KnowledgePipeline) *AKGBuilder {
	b.pipeline = p
	return b
}

// BuildResult holds the built KnowledgeObjects and Relations, plus the count
// persisted when a store is configured.
type BuildResult struct {
	Objects   []*knowledge.KnowledgeObject
	Relations []knowledge.Relation
	Saved     int
}

// Build converts the subgraph into KnowledgeObjects + relations and optionally
// persists them. Each Node becomes a KnowledgeObject; each Edge becomes a
// Relation. The node's precise type is preserved in the object's Tags and
// Metadata even when no dedicated AKG ObjectType exists.
//
// When an AKG KnowledgePipeline is attached via WithAKGPipeline, every
// projected KnowledgeObject is refined through it (Normalizer → EntityMatcher
// → Validator → Summarizer) before persistence, so the builder reuses AKG's
// shared processing instead of writing raw node summaries. With no pipeline
// (the default), objects are persisted exactly as projected (backward
// compatible).
//
// Args:
//
//	ctx       — context for cancellation and timeout.
//	sub       — the SubGraph to build; nil returns an empty result.
//	namespace — the AKG namespace for the produced objects.
//
// Returns:
//
//	*BuildResult — the built objects, relations, and saved count.
//	error        — non-nil if persistence fails.
func (b *AKGBuilder) Build(ctx context.Context, sub *SubGraph, namespace string) (*BuildResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("akg builder: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("akg builder: context cancelled: %w", err)
	}

	result := &BuildResult{
		Objects:   []*knowledge.KnowledgeObject{},
		Relations: []knowledge.Relation{},
	}
	if sub == nil || len(sub.Nodes) == 0 {
		return result, nil
	}

	objects := make([]*knowledge.KnowledgeObject, 0, len(sub.Nodes))
	for _, n := range sub.Nodes {
		if n == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("akg builder: context cancelled: %w", err)
		}
		obj := nodeToKnowledgeObject(n, namespace)
		if b.pipeline != nil {
			processed, pErr := b.pipeline.Process(ctx, obj)
			if pErr != nil {
				return nil, fmt.Errorf("akg builder: pipeline process %q: %w", obj.ID, pErr)
			}
			if processed != nil {
				obj = processed
			}
		}
		objects = append(objects, obj)
	}
	result.Objects = objects

	relations := make([]knowledge.Relation, 0, len(sub.Edges))
	for _, e := range sub.Edges {
		relations = append(relations, edgeToRelation(e))
	}
	result.Relations = relations

	if b.store != nil && len(objects) > 0 {
		if err := b.store.Save(ctx, objects...); err != nil {
			return nil, fmt.Errorf("akg builder: save: %w", err)
		}
		result.Saved = len(objects)
	}

	return result, nil
}

// nodeToKnowledgeObject projects a single KM Node into an AKG KnowledgeObject.
// The node's precise type is preserved in Tags and Metadata; ObjectType is the
// closest coarse category available in the knowledge package.
func nodeToKnowledgeObject(n *Node, namespace string) *knowledge.KnowledgeObject {
	now := time.Now()
	created := n.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := n.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	summary := nodeSummary(n)
	return &knowledge.KnowledgeObject{
		ID:         n.ID,
		Type:       nodeObjectType(n.Type),
		Namespace:  namespace,
		Normalized: summary,
		Summary:    summary,
		Metadata: map[string]any{
			"node_type": string(n.Type),
			"source":    n.Source,
		},
		Tags:       []string{string(n.Type)},
		Confidence: n.Confidence,
		Version:    int64(n.Version),
		CreatedAt:  created,
		UpdatedAt:  updated,
	}
}

// nodeObjectType maps a KM NodeType to the closest AKG ObjectType. The
// knowledge package has no dedicated entity/fact object types, so those (and
// any unmapped type) fall back to ObjectDocument; the precise type is retained
// in the object's Tags and Metadata.
func nodeObjectType(t NodeType) knowledge.ObjectType {
	switch t {
	case NodeDecision:
		return knowledge.ObjectDecision
	case NodeMemory:
		return knowledge.ObjectMemory
	default:
		return knowledge.ObjectDocument
	}
}

// edgeToRelation projects a KM Edge into an AKG Relation. Edge types with a
// matching built-in relation name use it; others fall back to the raw edge
// type string (Relation.Name is free-form by design). The original edge id and
// type are kept in Properties for traceability.
func edgeToRelation(e Edge) knowledge.Relation {
	return knowledge.Relation{
		From: e.Source,
		To:   e.Target,
		Name: edgeTypeToRelationName(e.Type),
		Properties: map[string]any{
			"edge_id":   e.ID,
			"edge_type": string(e.Type),
		},
		Score: e.Weight,
	}
}

// edgeTypeToRelationName maps a KM EdgeType to an AKG relation name. Returns
// the built-in relation constant when one matches semantically; otherwise
// returns the raw edge type string so no information is lost.
func edgeTypeToRelationName(et EdgeType) string {
	switch et {
	case EdgeDependsOn:
		return knowledge.RelDependsOn
	case EdgeImplements:
		return knowledge.RelImplements
	case EdgeDecidedBy:
		return knowledge.RelDecidedBy
	case EdgeLearnsFrom:
		return knowledge.RelLearnsFrom
	default:
		return string(et)
	}
}
