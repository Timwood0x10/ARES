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

// Linking thresholds for DefaultLinker. Centralised as constants so the
// behaviour is documented and tunable in one place.
const (
	// maxAllPairs is the group size below which Link emits all-pairs edges
	// (O(n²) but bounded). Above it Link switches to a star topology — each
	// member linked to the group representative — which is O(n) and, unlike
	// the old hard cap of 200, leaves no member orphaned from its cluster.
	maxAllPairs = 64
	// maxEdgesTotal caps the total number of edges emitted across all groups
	// to bound memory for pathological inputs (e.g. one tag shared by every
	// object). Once hit, remaining groups are skipped.
	maxEdgesTotal = 5000
)

func (l *DefaultLinker) Name() string { return "default-linker" }

func (l *DefaultLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	edges := make([]knowledge.Relation, 0, min(len(objects), maxEdgesTotal))
	byTag := make(map[string][]*knowledge.KnowledgeObject)

	for _, obj := range objects {
		for _, tag := range obj.Tags {
			byTag[tag] = append(byTag[tag], obj)
		}
	}

	// Create relations between objects sharing the same tag.
	for _, group := range byTag {
		if len(edges) >= maxEdgesTotal {
			break
		}
		edges = linkGroup(edges, group)
	}

	return edges, nil
}

// linkGroup appends belongs_to edges for a single tag group. For small groups
// (≤ maxAllPairs) it emits all pairs; for larger groups it emits a star
// (every member → the first member) so growth is linear and no member is left
// unlinked. It respects the maxEdgesTotal cap by stopping early.
func linkGroup(edges []knowledge.Relation, group []*knowledge.KnowledgeObject) []knowledge.Relation {
	if len(group) < 2 {
		return edges
	}
	emit := func(from, to string) {
		edges = append(edges, knowledge.Relation{
			From:  from,
			To:    to,
			Name:  knowledge.RelBelongsTo,
			Score: 0.5,
		})
	}

	if len(group) <= maxAllPairs {
		// All-pairs: every member linked to every other member.
		for i := 0; i < len(group) && len(edges) < maxEdgesTotal; i++ {
			for j := i + 1; j < len(group) && len(edges) < maxEdgesTotal; j++ {
				emit(group[i].ID, group[j].ID)
			}
		}
		return edges
	}

	// Star topology: link each member to the group representative (group[0]).
	// This keeps large clusters O(n) instead of O(n²) while still connecting
	// every member to the cluster via the representative.
	rep := group[0]
	for i := 1; i < len(group) && len(edges) < maxEdgesTotal; i++ {
		emit(rep.ID, group[i].ID)
	}
	return edges
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
