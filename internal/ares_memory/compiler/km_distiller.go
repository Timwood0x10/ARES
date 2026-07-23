// Package compiler — KMDistiller is the "distillation is pruning" bridge that
// deeply binds the Knowledge Model (KM) graph with the existing distillation
// module. It compresses a KM SubGraph into compact Memory nodes and removes
// (prunes) the original source nodes in the same pass.
//
// The distillation module's MemoryClassifier and ImportanceScorer are reused
// unchanged; both are rule/keyword based, so this path has ZERO LLM token cost
// and ZERO embedding dependency. Memory dedup against existing NodeMemory nodes
// uses token-overlap similarity (deterministic, no network).
package compiler

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
)

// memorySimilarityThreshold is the token-Jaccard threshold above which a new
// cluster is merged into an existing NodeMemory instead of creating a new one.
const memorySimilarityThreshold = 0.75

// maxMemorySummaryLen caps the compressed memory solution text.
const maxMemorySummaryLen = 500

// KMDistiller compresses a KM SubGraph into compact Memory nodes and prunes
// the original nodes — implementing "distillation is pruning". It reuses the
// existing distillation module's MemoryClassifier and ImportanceScorer (both
// rule/keyword based, zero LLM). Grouping is graph-connectivity based; memory
// dedup against existing NodeMemory nodes uses token-overlap similarity.
type KMDistiller struct {
	classifier   *distillation.MemoryClassifier
	scorer       *distillation.ImportanceScorer
	minScore     float64
	similarityFn func(a, b string) float64
}

// KMDistillerOption configures a KMDistiller at construction.
type KMDistillerOption func(*KMDistiller)

// WithMinScore sets the minimum importance score for memory creation. Clusters
// scoring below this are pruned directly without producing a Memory node.
func WithMinScore(s float64) KMDistillerOption {
	return func(d *KMDistiller) { d.minScore = s }
}

// WithSimilarityFunc overrides the default token-Jaccard similarity used for
// dedup against existing memory nodes.
func WithSimilarityFunc(f func(a, b string) float64) KMDistillerOption {
	return func(d *KMDistiller) {
		if f != nil {
			d.similarityFn = f
		}
	}
}

// NewKMDistiller creates a KMDistiller with default options
// (minScore = 0.6, token-Jaccard similarity).
func NewKMDistiller(opts ...KMDistillerOption) *KMDistiller {
	d := &KMDistiller{
		classifier:   distillation.NewMemoryClassifier(),
		scorer:       distillation.NewImportanceScorer(),
		minScore:     0.6,
		similarityFn: tokenJaccard,
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// DistillResult reports the outcome of a distill-and-prune pass.
type DistillResult struct {
	MemoryNodesCreated int
	NodesPruned        int
	MemoryMerged       int     // existing memory nodes merged into
	MemoryNodes        []*Node // newly created memory nodes
}

// DistillSubGraph compresses the given SubGraph's nodes into Memory nodes,
// writes the Memory nodes back into km, and removes (prunes) the original
// source nodes. Edges that referenced pruned nodes are re-linked to the new
// Memory node when the other endpoint survives, otherwise dropped.
//
// Rules:
//   - NodeMemory nodes in the subgraph are passed through untouched.
//   - Non-memory nodes are grouped into clusters by subgraph connectivity;
//     orphans form singleton clusters.
//   - Each cluster is classified and scored (rule-based, zero LLM). Clusters
//     scoring below minScore are pruned WITHOUT creating a memory node.
//   - A cluster whose summary is >= 0.75 similar to an existing NodeMemory in
//     km is merged into that node instead of creating a new one.
//
// Args:
//
//	ctx - context for cancellation and timeout.
//	km - the Knowledge Model to mutate (must not be nil).
//	sub - the SubGraph of candidates to compress (nil = no-op).
//
// Returns:
//
//	*DistillResult - counts and newly created memory nodes.
//	error - non-nil if km is nil or the context is cancelled.
func (d *KMDistiller) DistillSubGraph(ctx context.Context, km *KnowledgeModel, sub *SubGraph) (*DistillResult, error) {
	if km == nil {
		return nil, fmt.Errorf("km distiller: knowledge model must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("km distiller: context cancelled: %w", err)
	}

	result := &DistillResult{}
	if sub == nil || len(sub.Nodes) == 0 {
		return result, nil
	}

	clusters := clusterNodes(sub)
	prunedIDs := make(map[string]bool)
	memoryLink := make(map[string]string) // prunedID -> memoryNodeID (only clusters that produced a memory)

	for _, cluster := range clusters {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("km distiller: context cancelled: %w", err)
		}
		for _, n := range cluster {
			prunedIDs[n.ID] = true
		}
		problem, solution := buildClusterSummary(cluster)
		memType := d.classifier.ClassifyMemory(&distillation.Experience{
			Problem:  problem,
			Solution: solution,
		})
		score := d.scorer.ScoreMemory(memType, problem, solution)

		if score < d.minScore {
			// Prune directly without producing a memory node.
			continue
		}

		memID, merged, err := d.resolveMemory(km, problem, solution, memType, score, cluster)
		if err != nil {
			return nil, fmt.Errorf("km distiller: resolve memory: %w", err)
		}
		for _, n := range cluster {
			memoryLink[n.ID] = memID
		}
		if merged {
			result.MemoryMerged++
		} else {
			result.MemoryNodesCreated++
			if n := km.Nodes[memID]; n != nil {
				result.MemoryNodes = append(result.MemoryNodes, n)
			}
		}
	}

	result.NodesPruned = d.pruneAndRelink(km, prunedIDs, memoryLink)

	el.Info(ctx, "km distiller", "distill complete",
		"memory_created", result.MemoryNodesCreated,
		"memory_merged", result.MemoryMerged,
		"nodes_pruned", result.NodesPruned)

	return result, nil
}

// resolveMemory either merges the cluster into an existing NodeMemory (idempotent
// same-summary hit, or similarity dedup) or creates a new Memory node in km.
//
// Returns:
//
//	memID - the memory node ID the cluster was folded into.
//	merged - true if an existing memory node was reused/merged.
//	err - non-nil only on an unrecoverable AddNode failure.
func (d *KMDistiller) resolveMemory(km *KnowledgeModel, problem, solution string, memType distillation.MemoryType, score float64, cluster []*Node) (string, bool, error) {
	candidateID := memoryID(solution)

	// Idempotent path: identical summary already produced this memory node.
	if existing, ok := km.Nodes[candidateID]; ok && existing.Type == NodeMemory {
		mergeIntoMemory(existing, cluster, score)
		return candidateID, true, nil
	}

	// Similarity dedup against existing memory nodes.
	for _, n := range km.Nodes {
		if n.Type != NodeMemory {
			continue
		}
		if d.similarityFn(solution, attrString(n, "summary")) >= memorySimilarityThreshold {
			mergeIntoMemory(n, cluster, score)
			return n.ID, true, nil
		}
	}

	node := createMemoryNode(candidateID, problem, solution, memType, score, cluster)
	if err := km.AddNode(node); err != nil {
		return "", false, fmt.Errorf("add memory node %q: %w", candidateID, err)
	}
	return candidateID, false, nil
}

// clusterNodes groups the non-memory nodes of a SubGraph into connected
// components using the SubGraph's edges (undirected). NodeMemory nodes are
// excluded (passed through untouched by the distiller).
func clusterNodes(sub *SubGraph) [][]*Node {
	idToNode := make(map[string]*Node)
	for _, n := range sub.Nodes {
		if n == nil || n.Type == NodeMemory {
			continue
		}
		idToNode[n.ID] = n
	}

	adj := make(map[string]map[string]bool, len(idToNode))
	for id := range idToNode {
		adj[id] = make(map[string]bool)
	}
	for _, e := range sub.Edges {
		if _, ok1 := idToNode[e.Source]; !ok1 {
			continue
		}
		if _, ok2 := idToNode[e.Target]; !ok2 {
			continue
		}
		adj[e.Source][e.Target] = true
		adj[e.Target][e.Source] = true
	}

	visited := make(map[string]bool, len(idToNode))
	var clusters [][]*Node
	for id := range idToNode {
		if visited[id] {
			continue
		}
		cluster := bfsComponent(id, idToNode, adj, visited)
		clusters = append(clusters, cluster)
	}
	return clusters
}

// bfsComponent returns the connected component containing startID.
func bfsComponent(startID string, idToNode map[string]*Node, adj map[string]map[string]bool, visited map[string]bool) []*Node {
	var cluster []*Node
	queue := []string{startID}
	visited[startID] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cluster = append(cluster, idToNode[cur])
		for nb := range adj[cur] {
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return cluster
}

// buildClusterSummary produces a (problem, solution) pair describing a cluster.
// The problem is a topic label; the solution is a compact, type-tagged listing
// of each node's description, capped at maxMemorySummaryLen chars. Nodes are
// sorted by ID first so the summary — and thus the derived memory ID — is
// deterministic regardless of graph traversal order.
func buildClusterSummary(nodes []*Node) (problem, solution string) {
	sorted := make([]*Node, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var entities []string
	for _, n := range sorted {
		if n.Type == NodeEntity {
			if name := attrString(n, "name"); name != "" {
				entities = append(entities, name)
			}
		}
	}
	topic := "knowledge cluster"
	if len(entities) > 0 {
		topic = "knowledge cluster about " + strings.Join(entities, ", ")
	}
	problem = topic

	var lines []string
	if len(sorted) > 0 {
		lines = make([]string, 0, len(sorted))
	}
	for _, n := range sorted {
		lines = append(lines, fmt.Sprintf("%s: %s", n.Type, nodeSummary(n)))
	}
	solution = strings.Join(lines, "; ")
	if len(solution) > maxMemorySummaryLen {
		solution = solution[:maxMemorySummaryLen-3] + "..."
	}
	return problem, solution
}

// createMemoryNode builds a new NodeMemory from a compressed cluster.
func createMemoryNode(id, problem, solution string, memType distillation.MemoryType, score float64, cluster []*Node) *Node {
	sourceIDs := make([]string, len(cluster))
	for i, n := range cluster {
		sourceIDs[i] = n.ID
	}
	now := time.Now()
	return &Node{
		ID:   id,
		Type: NodeMemory,
		Attributes: map[string]any{
			attrSummary:       solution,
			"topic":           problem,
			"source_node_ids": sourceIDs,
			"memory_type":     memType.String(),
			"cluster_size":    len(cluster),
		},
		Confidence: score,
		CreatedAt:  now,
		UpdatedAt:  now,
		Source:     strings.Join(sourceIDs, ","),
	}
}

// mergeIntoMemory folds a cluster's source node IDs into an existing Memory
// node, bumping confidence and version. Source IDs are de-duplicated and
// sorted for deterministic output.
func mergeIntoMemory(mem *Node, cluster []*Node, score float64) {
	existing := readSourceIDs(mem)
	set := make(map[string]bool, len(existing)+len(cluster))
	for _, id := range existing {
		set[id] = true
	}
	for _, n := range cluster {
		set[n.ID] = true
	}
	merged := make([]string, 0, len(set))
	for id := range set {
		merged = append(merged, id)
	}
	sort.Strings(merged)
	mem.Attributes["source_node_ids"] = merged
	mem.Attributes["cluster_size"] = len(merged)
	if score > mem.Confidence {
		mem.Confidence = score
	}
	mem.Version++
	mem.UpdatedAt = time.Now()
}

// readSourceIDs reads the source_node_ids attribute defensively, handling both
// []string (in-memory) and []any (post-JSON-unmarshal) representations.
func readSourceIDs(mem *Node) []string {
	if mem == nil || mem.Attributes == nil {
		return nil
	}
	v, ok := mem.Attributes["source_node_ids"]
	if !ok {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		return arr
	case []any:
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// pruneAndRelink removes pruned nodes from km and rewrites edges so that edges
// referencing a pruned node (whose cluster produced a memory) point to the
// memory node instead. Edges where both endpoints are pruned, or where the
// pruned endpoint has no memory target, are dropped. Returns the node count
// actually removed.
func (d *KMDistiller) pruneAndRelink(km *KnowledgeModel, prunedIDs map[string]bool, memoryLink map[string]string) int {
	var newEdges []Edge
	seen := make(map[string]bool)
	for _, e := range km.Edges {
		srcPruned := prunedIDs[e.Source]
		tgtPruned := prunedIDs[e.Target]
		if srcPruned && tgtPruned {
			continue
		}
		newSrc, newTgt, ok := rewriteEndpoints(e.Source, e.Target, srcPruned, tgtPruned, memoryLink)
		if !ok {
			continue
		}
		if newSrc == newTgt {
			continue
		}
		key := string(e.Type) + "|" + newSrc + "|" + newTgt
		if seen[key] {
			continue
		}
		seen[key] = true
		if !srcPruned && !tgtPruned {
			// Untouched edge: preserve original identity.
			newEdges = append(newEdges, e)
			continue
		}
		newEdges = append(newEdges, Edge{
			ID:        "edge-rel-" + key,
			Type:      e.Type,
			Source:    newSrc,
			Target:    newTgt,
			Weight:    e.Weight,
			CreatedAt: time.Now(),
		})
	}
	km.Edges = newEdges

	removed := 0
	for id := range prunedIDs {
		if _, ok := km.Nodes[id]; ok {
			delete(km.Nodes, id)
			removed++
		}
	}
	km.Metadata.UpdatedAt = time.Now()
	return removed
}

// rewriteEndpoints maps pruned endpoints to their memory node IDs. Returns
// ok=false when a pruned endpoint has no memory target (edge must be dropped).
func rewriteEndpoints(src, tgt string, srcPruned, tgtPruned bool, memoryLink map[string]string) (string, string, bool) {
	newSrc, newTgt := src, tgt
	if srcPruned {
		mem, ok := memoryLink[src]
		if !ok {
			return "", "", false
		}
		newSrc = mem
	}
	if tgtPruned {
		mem, ok := memoryLink[tgt]
		if !ok {
			return "", "", false
		}
		newTgt = mem
	}
	return newSrc, newTgt, true
}

// memoryID derives a deterministic memory node ID from the summary text.
// Uses FNV-64a so the collision risk is far lower than the previous FNV-32a
// (important because many clusters can map to similar summaries).
func memoryID(solution string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(solution))
	return "memory-" + fmt.Sprintf("%x", h.Sum64())
}

// tokenJaccard computes the Jaccard similarity over the lowercase token sets of
// a and b. Returns 0 for empty inputs.
func tokenJaccard(a, b string) float64 {
	setA := tokenize(a)
	setB := tokenize(b)
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	inter := 0
	for t := range setA {
		if setB[t] {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// tokenize splits s into a lowercase token set.
func tokenize(s string) map[string]bool {
	set := make(map[string]bool)
	for _, t := range strings.Fields(strings.ToLower(s)) {
		set[t] = true
	}
	return set
}
