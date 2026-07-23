// Package compiler — Selector interface and shared helpers.
//
// A Selector chooses a SubGraph from a KnowledgeModel for a specific
// downstream consumer (prompt builder, memory emitter, AKG builder,
// analytics). All selectors in this package are deterministic and LLM-free:
// they score nodes by structural signals (type, confidence, access count,
// timestamps) only.
package compiler

import "sort"

// Selector chooses a SubGraph from a KnowledgeModel for a specific downstream
// consumer. All selectors are deterministic and LLM-free.
type Selector interface {
	// Select returns a SubGraph picked from the given KnowledgeModel.
	// Implementations MUST return a non-nil SubGraph even when no nodes
	// match (so callers can read Metadata safely).
	Select(km *KnowledgeModel) *SubGraph

	// Name returns the selector's identifier (e.g. "memory", "prompt").
	Name() string
}

// Name returns the memory selector's identifier so it satisfies Selector.
// This adds the method to the existing MemorySelector in the same package.
func (ms *MemorySelector) Name() string { return string(NodeMemory) }

// Compile-time assertions that every concrete selector satisfies Selector.
var (
	_ Selector = (*MemorySelector)(nil)
	_ Selector = (*PromptSelector)(nil)
	_ Selector = (*AKGSelector)(nil)
	_ Selector = (*AnalyticsSelector)(nil)
)

// SelectByType is a shared helper: returns a SubGraph of nodes whose Type is
// in types, sorted by score descending, capped at maxNodes (0 = no cap).
// Edges among the selected nodes are included.
//
// Args:
//
//	km       — the Knowledge Model to select from; nil returns an empty SubGraph.
//	types    — node types to include; empty means no nodes match.
//	maxNodes — maximum number of nodes to return (0 = no cap).
//	score    — scoring function; nil falls back to the node's Confidence.
func SelectByType(km *KnowledgeModel, types []NodeType, maxNodes int, score func(*Node) float64) *SubGraph {
	if km == nil {
		return &SubGraph{}
	}
	if score == nil {
		score = func(n *Node) float64 {
			if n == nil {
				return 0
			}
			return n.Confidence
		}
	}

	typeSet := make(map[NodeType]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}

	var candidates []scoredCandidate
	for _, n := range km.Nodes {
		if n == nil {
			continue
		}
		if !typeSet[n.Type] {
			continue
		}
		candidates = append(candidates, scoredCandidate{node: n, score: score(n)})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		// Stable tie-breaker: earlier ID wins, so output is deterministic.
		return candidates[i].node.ID < candidates[j].node.ID
	})

	if maxNodes > 0 && len(candidates) > maxNodes {
		candidates = candidates[:maxNodes]
	}

	return buildSubGraph(km, candidatesToNodes(candidates), map[string]any{
		attrSelector:      "by_type",
		attrNodesSelected: len(candidates),
	})
}

// defaultScore is a reusable scoring helper that callers can pass to
// SelectByType. It weights confidence by access frequency so
// frequently-referenced nodes rank higher. Note: SelectByType with a nil
// score func falls back to plain Confidence, not this helper.
func defaultScore(n *Node) float64 {
	if n == nil {
		return 0
	}
	return n.Confidence * float64(n.AccessCount+1)
}

// candidatesToNodes extracts the []*Node view of a scoredCandidate slice in
// order, preserving the caller's sort.
func candidatesToNodes(cs []scoredCandidate) []*Node {
	out := make([]*Node, len(cs))
	for i, c := range cs {
		out[i] = c.node
	}
	return out
}

// buildSubGraph assembles a SubGraph from the given nodes and the edges of
// the source KnowledgeModel that connect them. The node order is preserved.
// Metadata is attached as-is.
func buildSubGraph(km *KnowledgeModel, nodes []*Node, metadata map[string]any) *SubGraph {
	nodeSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n != nil {
			nodeSet[n.ID] = true
		}
	}

	var edges []Edge
	for _, e := range km.Edges {
		if nodeSet[e.Source] && nodeSet[e.Target] {
			edges = append(edges, e)
		}
	}

	return &SubGraph{
		Nodes:    nodes,
		Edges:    edges,
		Metadata: metadata,
	}
}
