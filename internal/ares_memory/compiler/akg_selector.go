// Package compiler — AKGSelector picks nodes destined for the Agent Knowledge
// Graph: entities, facts, and references (the structural backbone). Entities
// are always kept; facts/references are capped by confidence. Zero LLM calls.
package compiler

import "sort"

// AKGSelector picks the structural backbone of the KnowledgeModel for the
// Agent Knowledge Graph. Unlike the prompt selector, it does not need a token
// budget — the AKG is a graph store, not a prompt window — but it filters by
// confidence so low-quality facts and references don't pollute the graph.
type AKGSelector struct {
	// MinConfidence is the minimum confidence for Fact and Reference nodes.
	// Entity nodes are always kept regardless of this threshold.
	MinConfidence float64

	// MaxFacts caps the number of Fact nodes returned (highest-confidence
	// first). Zero means no cap.
	MaxFacts int
}

// NewAKGSelector creates an AKGSelector with the given filters.
func NewAKGSelector(minConfidence float64, maxFacts int) *AKGSelector {
	return &AKGSelector{
		MinConfidence: minConfidence,
		MaxFacts:      maxFacts,
	}
}

// Select returns a SubGraph containing all entity nodes plus the qualifying
// facts and references. Returns a non-nil empty SubGraph for nil input.
func (s *AKGSelector) Select(km *KnowledgeModel) *SubGraph {
	if km == nil {
		return &SubGraph{Metadata: map[string]any{attrSelector: extractorNameAKG, attrNodesSelected: 0}}
	}

	entities := km.GetNodesByType(NodeEntity)
	facts := s.selectFacts(km)
	references := s.selectReferences(km)

	candidates := make([]scoredCandidate, 0, len(entities)+len(facts)+len(references))
	candidates = appendScored(candidates, entities, akgScore)
	candidates = appendScored(candidates, facts, akgScore)
	candidates = appendScored(candidates, references, akgScore)

	// Preserve a stable, deterministic ordering: score desc, then ID asc.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	return buildSubGraph(km, candidatesToNodes(candidates), map[string]any{
		attrSelector:      extractorNameAKG,
		"nodes_selected":  len(candidates),
		"entity_count":    len(entities),
		"fact_count":      len(facts),
		"reference_count": len(references),
		"min_confidence":  s.MinConfidence,
	})
}

// Name returns the selector's identifier.
func (s *AKGSelector) Name() string { return "akg" }

// selectFacts returns Fact nodes whose Confidence is at least MinConfidence,
// capped to MaxFacts (highest-confidence first, ties broken by ID for
// determinism).
func (s *AKGSelector) selectFacts(km *KnowledgeModel) []*Node {
	facts := km.GetNodesByType(NodeFact)
	qualified := make([]*Node, 0, len(facts))
	for _, n := range facts {
		if n == nil {
			continue
		}
		if n.Confidence >= s.MinConfidence {
			qualified = append(qualified, n)
		}
	}
	sort.Slice(qualified, func(i, j int) bool {
		if qualified[i].Confidence != qualified[j].Confidence {
			return qualified[i].Confidence > qualified[j].Confidence
		}
		return qualified[i].ID < qualified[j].ID
	})
	if s.MaxFacts > 0 && len(qualified) > s.MaxFacts {
		qualified = qualified[:s.MaxFacts]
	}
	return qualified
}

// selectReferences returns Reference nodes whose Confidence is at least
// MinConfidence. References are not capped: external pointers are cheap in a
// graph store and useful for provenance.
func (s *AKGSelector) selectReferences(km *KnowledgeModel) []*Node {
	refs := km.GetNodesByType(NodeReference)
	qualified := make([]*Node, 0, len(refs))
	for _, n := range refs {
		if n == nil {
			continue
		}
		if n.Confidence >= s.MinConfidence {
			qualified = append(qualified, n)
		}
	}
	return qualified
}

// akgScore scores a node for ordering inside the AKG subgraph. Entities get a
// small fixed boost so they sort first within equal-confidence groups; facts
// and references are ordered by confidence and access frequency.
func akgScore(n *Node) float64 {
	if n == nil {
		return 0
	}
	boost := 0.0
	if n.Type == NodeEntity {
		boost = 0.5
	}
	return n.Confidence*float64(n.AccessCount+1) + boost
}

// appendScored converts a slice of nodes into scoredCandidates using the given
// scoring function and appends them to dst.
func appendScored(dst []scoredCandidate, nodes []*Node, score func(*Node) float64) []scoredCandidate {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		dst = append(dst, scoredCandidate{node: n, score: score(n)})
	}
	return dst
}
