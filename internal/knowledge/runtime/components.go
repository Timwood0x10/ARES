package runtime

import (
	"context"
	"sort"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// Linker generates relations between KnowledgeObjects.
type Linker interface {
	// Name returns the linker identifier.
	Name() string
	// Link generates relations from a set of knowledge objects.
	Link(ctx context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error)
}

// Reducer prunes and compresses a WorkingGraph to fit within a token budget.
type Reducer interface {
	// Name returns the reducer identifier.
	Name() string
	// Reduce compresses the graph to fit within the budget.
	Reduce(ctx context.Context, graph *knowledge.WorkingGraph, budget knowledge.TokenBudget) (*knowledge.WorkingGraph, error)
}

// DefaultLinker generates basic relations based on shared tags and types.
type DefaultLinker struct{}

func (l *DefaultLinker) Name() string { return "default-linker" }

func (l *DefaultLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	var edges []knowledge.Relation
	byTag := make(map[string][]*knowledge.KnowledgeObject)

	for _, obj := range objects {
		for _, tag := range obj.Tags {
			byTag[tag] = append(byTag[tag], obj)
		}
	}

	// Create relations between objects sharing the same tag.
	for _, group := range byTag {
		for i := 0; i < len(group) && i < 10; i++ {
			for j := i + 1; j < len(group) && j < 10; j++ {
				edges = append(edges, knowledge.Relation{
					From:  group[i].ID,
					To:    group[j].ID,
					Name:  knowledge.RelBelongsTo,
					Score: 0.5,
				})
			}
		}
	}

	return edges, nil
}

// DefaultReducer removes low-confidence nodes to fit the token budget.
type DefaultReducer struct{}

func (r *DefaultReducer) Name() string { return "default-reducer" }

func (r *DefaultReducer) Reduce(_ context.Context, graph *knowledge.WorkingGraph, budget knowledge.TokenBudget) (*knowledge.WorkingGraph, error) {
	if graph == nil || len(graph.Nodes) == 0 {
		return graph, nil
	}

	// Estimate: each node Summary consumes ~50 tokens.
	estTokensPerNode := 50
	maxNodes := budget.ForGraph / estTokensPerNode
	if maxNodes <= 0 {
		maxNodes = 1
	}

	if len(graph.Nodes) <= maxNodes {
		return graph, nil
	}

	// Prune: keep nodes with highest confidence.
	type scored struct {
		id   string
		conf float64
	}
	s := make([]scored, 0, len(graph.Nodes))
	for id, obj := range graph.Nodes {
		s = append(s, scored{id: id, conf: obj.Confidence})
	}

	// Sort by confidence descending.
	sort.Slice(s, func(i, j int) bool {
		return s[i].conf > s[j].conf
	})

	kept := make(map[string]bool, maxNodes)
	for i := 0; i < maxNodes && i < len(s); i++ {
		kept[s[i].id] = true
	}

	prunedNodes := make(map[string]*knowledge.KnowledgeObject, maxNodes)
	for id, obj := range graph.Nodes {
		if kept[id] {
			prunedNodes[id] = obj
		}
	}

	// Filter edges to only include kept nodes.
	var prunedEdges []knowledge.Relation
	for _, e := range graph.Edges {
		if kept[e.From] && kept[e.To] {
			prunedEdges = append(prunedEdges, e)
		}
	}

	return &knowledge.WorkingGraph{Nodes: prunedNodes, Edges: prunedEdges}, nil
}
