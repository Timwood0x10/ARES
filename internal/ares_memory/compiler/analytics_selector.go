// Package compiler — AnalyticsSelector picks nodes for timeline/analytics
// views: decisions, tasks, evidence, and goals. Results are sorted by
// CreatedAt ascending for timeline display. Zero LLM calls.
package compiler

import "sort"

// analyticsCandidateTypes returns the node types that carry timeline value.
// Decisions mark inflection points, tasks track work, evidence grounds claims,
// and goals frame direction. Entities and references are excluded because
// they are structural rather than temporal.
func analyticsCandidateTypes() []NodeType {
	return []NodeType{NodeDecision, NodeTask, NodeEvidence, NodeGoal}
}

// AnalyticsSelector selects nodes suitable for timeline and analytics views.
// The selected nodes are sorted by CreatedAt ascending so a downstream
// timeline renderer can stream them in chronological order.
type AnalyticsSelector struct {
	// MaxNodes is the maximum number of nodes to return. Zero means no cap.
	// When the cap is exceeded, the most recent nodes are kept (i.e. the
	// tail of the ascending-by-CreatedAt ordering).
	MaxNodes int
}

// NewAnalyticsSelector creates an AnalyticsSelector with the given node cap.
func NewAnalyticsSelector(maxNodes int) *AnalyticsSelector {
	return &AnalyticsSelector{MaxNodes: maxNodes}
}

// Select returns a SubGraph of analytics-relevant nodes sorted by CreatedAt
// ascending. Returns a non-nil empty SubGraph for nil input.
func (s *AnalyticsSelector) Select(km *KnowledgeModel) *SubGraph {
	if km == nil {
		return &SubGraph{Metadata: map[string]any{attrSelector: "analytics_selector", attrNodesSelected: 0}}
	}

	candidateTypes := analyticsCandidateTypes()
	typeSet := make(map[NodeType]bool, len(candidateTypes))
	for _, t := range candidateTypes {
		typeSet[t] = true
	}

	var nodes []*Node
	for _, n := range km.Nodes {
		if n == nil {
			continue
		}
		if typeSet[n.Type] {
			nodes = append(nodes, n)
		}
	}

	// Sort ascending by CreatedAt; tie-break by ID for determinism.
	sort.Slice(nodes, func(i, j int) bool {
		if !nodes[i].CreatedAt.Equal(nodes[j].CreatedAt) {
			return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
		}
		return nodes[i].ID < nodes[j].ID
	})

	// Apply cap: keep the most recent when over the limit.
	if s.MaxNodes > 0 && len(nodes) > s.MaxNodes {
		nodes = nodes[len(nodes)-s.MaxNodes:]
	}

	return buildSubGraph(km, nodes, map[string]any{
		attrSelector:     "analytics_selector",
		"nodes_selected": len(nodes),
		"max_nodes":      s.MaxNodes,
	})
}

// Name returns the selector's identifier.
func (s *AnalyticsSelector) Name() string { return "analytics" }
