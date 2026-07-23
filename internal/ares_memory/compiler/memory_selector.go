// Package compiler — MemorySelector selects nodes from the Knowledge Model
// that are suitable for long-term memory storage. It scores candidates by
// type, confidence, and access count, then returns a prioritized SubGraph
// of memory candidates for the MemoryEmitter to store.
package compiler

import (
	"sort"
)

// MemorySelector selects memory candidates from a Knowledge Model.
// Candidates are scored by type priority, confidence, and access count,
// then returned as a SubGraph for the MemoryEmitter to persist.
type MemorySelector struct {
	// MinConfidence is the minimum confidence threshold for a node to be
	// considered a memory candidate. Nodes below this threshold are filtered out.
	MinConfidence float64

	// MaxCandidates is the maximum number of candidates to return per selection.
	// Zero means no limit.
	MaxCandidates int

	// TypePriority maps node types to their memory priority score.
	// Higher priority types are more likely to be selected.
	TypePriority map[NodeType]float64
}

// DefaultMemorySelector returns a MemorySelector with sensible defaults.
//
// Priority tiers:
//   - Decision, Constraint: highest priority (3.0)
//   - Tradeoff, Goal: high priority (2.0)
//   - Fact, Task: medium priority (1.5)
//   - Question, Evidence: normal priority (1.0)
//   - Entity, Reference: low priority (0.5)
//   - Memory: never selected (already stored)
func DefaultMemorySelector() *MemorySelector {
	return &MemorySelector{
		MinConfidence: 0.4,
		MaxCandidates: 50,
		TypePriority: map[NodeType]float64{
			NodeDecision:   3.0,
			NodeConstraint: 3.0,
			NodeTradeoff:   2.0,
			NodeGoal:       2.0,
			NodeFact:       1.5,
			NodeTask:       1.5,
			NodeQuestion:   1.0,
			NodeEvidence:   1.0,
			NodeEntity:     0.5,
			NodeReference:  0.5,
			// NodeMemory: never from Selector (already stored)
		},
	}
}

// scoredCandidate holds a node with its computed score.
type scoredCandidate struct {
	node  *Node
	score float64
}

// Select selects memory candidates from a Knowledge Model.
//
// Args:
//
//	km — the Knowledge Model to select from.
//
// Returns:
//
//	*SubGraph — the selected memory candidates, sorted by score descending.
//	May be empty (nil nodes slice) if no candidates qualify.
func (ms *MemorySelector) Select(km *KnowledgeModel) *SubGraph {
	if km == nil || len(km.Nodes) == 0 {
		return &SubGraph{}
	}

	var candidates []scoredCandidate
	for _, n := range km.Nodes {
		// Skip memory nodes (already stored).
		if n.Type == NodeMemory {
			continue
		}
		// Skip low-confidence nodes.
		if n.Confidence < ms.MinConfidence {
			continue
		}
		// Compute score: type priority × confidence × access count factor.
		priority := ms.TypePriority[n.Type]
		if priority == 0 {
			priority = 1.0 // Default priority for unknown types.
		}
		accessFactor := 1.0
		if n.AccessCount > 0 {
			accessFactor = 1.0 + float64(n.AccessCount)*0.1
			if accessFactor > 2.0 {
				accessFactor = 2.0
			}
		}
		score := priority * n.Confidence * accessFactor
		candidates = append(candidates, scoredCandidate{node: n, score: score})
	}

	// Sort by score descending; break ties by node ID for deterministic output,
	// matching the package's determinism contract and the other selectors.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].node.ID < candidates[j].node.ID
	})

	// Apply max candidates limit.
	if ms.MaxCandidates > 0 && len(candidates) > ms.MaxCandidates {
		candidates = candidates[:ms.MaxCandidates]
	}

	// Build SubGraph.
	nodes := make([]*Node, len(candidates))
	nodeSet := make(map[string]bool)
	for i, c := range candidates {
		nodes[i] = c.node
		nodeSet[c.node.ID] = true
	}

	// Include edges between selected nodes.
	var edges []Edge
	for _, e := range km.Edges {
		if nodeSet[e.Source] && nodeSet[e.Target] {
			edges = append(edges, e)
		}
	}

	return &SubGraph{
		Nodes: nodes,
		Edges: edges,
		Metadata: map[string]any{
			attrSelector:     string(NodeMemory),
			"total_scored":   len(candidates),
			"min_confidence": ms.MinConfidence,
		},
	}
}

// Score returns the computed score for a single node.
// Useful for debugging and logging.
//
// Args:
//
//	n — the node to score.
//
// Returns:
//
//	float64 — the computed score.
func (ms *MemorySelector) Score(n *Node) float64 {
	if n == nil {
		return 0
	}
	priority := ms.TypePriority[n.Type]
	if priority == 0 {
		priority = 1.0
	}
	accessFactor := 1.0
	if n.AccessCount > 0 {
		accessFactor = 1.0 + float64(n.AccessCount)*0.1
		if accessFactor > 2.0 {
			accessFactor = 2.0
		}
	}
	return priority * n.Confidence * accessFactor
}
