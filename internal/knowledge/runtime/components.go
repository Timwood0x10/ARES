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
		for i := 0; i < len(group) && i < 200; i++ {
			for j := i + 1; j < len(group) && j < 200; j++ {
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
	if budget.ForGraph <= 0 {
		// Budget unset: do not aggressively prune; keep all nodes.
		maxNodes = len(graph.Nodes)
	} else if maxNodes <= 0 {
		// Budget too small for a single node: keep at least one.
		maxNodes = 1
	}

	if len(graph.Nodes) <= maxNodes {
		return graph, nil
	}

	// Score each pair of edges from node to count of neighbor domain presence.
	// Group nodes by domain tag so selection preserves diversity.
	type scored struct {
		id     string
		conf   float64
		domain string
	}

	s := make([]scored, 0, len(graph.Nodes))
	for id, obj := range graph.Nodes {
		domain := extractDomain(obj.Tags)
		s = append(s, scored{id: id, conf: obj.Confidence, domain: domain})
	}

	// Sort by confidence descending.
	sort.Slice(s, func(i, j int) bool {
		return s[i].conf > s[j].conf
	})

	// Diversity-aware selection: reserve slots per domain proportionally.
	// This prevents the reducer from picking top-N nodes all from different
	// domains, which would lose cross-domain relations (edges).
	domainCount := make(map[string]int)
	for _, sc := range s {
		domainCount[sc.domain]++
	}

	// Calculate how many slots each domain gets (at least 1).
	domainSlots := make(map[string]int)
	totalSlots := maxNodes
	for domain, count := range domainCount {
		slots := maxNodes * count / len(graph.Nodes)
		if slots < 1 {
			slots = 1
		}
		if slots > count {
			slots = count
		}
		domainSlots[domain] = slots
		totalSlots -= slots
	}

	// Distribute remaining slots to domains with the highest confidence nodes.
	if totalSlots > 0 {
		for _, sc := range s {
			if totalSlots <= 0 {
				break
			}
			if domainSlots[sc.domain] < domainCount[sc.domain] {
				domainSlots[sc.domain]++
				totalSlots--
			}
		}
	}

	// Select nodes: for each domain, pick its top-confidence nodes.
	kept := make(map[string]bool, maxNodes)
	domainPicked := make(map[string]int)
	for _, sc := range s {
		if domainPicked[sc.domain] < domainSlots[sc.domain] {
			kept[sc.id] = true
			domainPicked[sc.domain]++
		}
		if len(kept) >= maxNodes {
			break
		}
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

// extractDomain extracts the domain tag from an object's tags.
// Falls back to "default" if no domain:xxx tag is found.
func extractDomain(tags []string) string {
	for _, tag := range tags {
		if len(tag) > 7 && tag[:7] == "domain:" {
			return tag[7:]
		}
	}
	return "default"
}
