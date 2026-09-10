package flight

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AgentRelation classifies the relationship between agents.
type AgentRelation string

const (
	RelationSpawned     AgentRelation = "spawned"
	RelationResurrected AgentRelation = "resurrected"
	RelationPromoted    AgentRelation = "promoted"
)

// LineageNode represents a single agent in the genealogy tree.
type LineageNode struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	ParentID  string         `json:"parent_id,omitempty"`
	Relation  AgentRelation  `json:"relation"`
	SpawnedAt time.Time      `json:"spawned_at"`
	DiedAt    time.Time      `json:"died_at,omitempty"`
	IsAlive   bool           `json:"is_alive"`
	Children  []*LineageNode `json:"children,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// Genealogy tracks the family tree of all agents.
type Genealogy struct {
	roots []*LineageNode
	nodes map[string]*LineageNode
	mu    sync.RWMutex
}

// maxGenealogyNodes caps the genealogy's node map: agent rotation
// (resurrection chains, repeated spawn/death cycles) otherwise grows it
// without bound for the process lifetime. Declared as a var so white-box
// tests can shrink it; nothing outside the package mutates it.
var maxGenealogyNodes = 1000

// NewGenealogy creates an empty genealogy tree.
func NewGenealogy() *Genealogy {
	return &Genealogy{
		roots: make([]*LineageNode, 0),
		nodes: make(map[string]*LineageNode),
	}
}

// RecordSpawn records a parent-child relationship.
func (g *Genealogy) RecordSpawn(parentID, childID, agentType string, metadata map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()

	child := &LineageNode{
		ID:        childID,
		Type:      agentType,
		Relation:  RelationSpawned,
		SpawnedAt: time.Now(),
		IsAlive:   true,
		Metadata:  metadata,
	}

	if parentID != "" {
		child.ParentID = parentID

		// Ensure parent exists (create placeholder if needed).
		parent, ok := g.nodes[parentID]
		if !ok {
			parent = &LineageNode{
				ID:        parentID,
				SpawnedAt: time.Now(),
				IsAlive:   true,
			}
			g.nodes[parentID] = parent
			g.roots = append(g.roots, parent)
		}
		parent.Children = append(parent.Children, child)
	} else {
		g.roots = append(g.roots, child)
	}

	g.nodes[childID] = child
	g.evictLocked()
}

// RecordResurrection records that oldID died and was resurrected as newID.
func (g *Genealogy) RecordResurrection(oldID, newID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	oldNode, hasOld := g.nodes[oldID]
	if hasOld {
		oldNode.IsAlive = false
		oldNode.DiedAt = time.Now()
	}

	newNode := &LineageNode{
		ID:        newID,
		Relation:  RelationResurrected,
		SpawnedAt: time.Now(),
		IsAlive:   true,
	}

	// Inherit type and parent from old node.
	if hasOld {
		newNode.Type = oldNode.Type
		newNode.ParentID = oldNode.ParentID

		// Add as child of old node's parent.
		if oldNode.ParentID != "" {
			if parent, ok := g.nodes[oldNode.ParentID]; ok {
				parent.Children = append(parent.Children, newNode)
			}
		} else {
			// Old node was a root — new node replaces it.
			for i, r := range g.roots {
				if r.ID == oldID {
					g.roots[i] = newNode
					break
				}
			}
		}

		// Old node's children now belong to new node.
		newNode.Children = oldNode.Children
		oldNode.Children = nil
	} else {
		g.roots = append(g.roots, newNode)
	}

	g.nodes[newID] = newNode
	g.evictLocked()
}

// RecordRoot records a root agent (no parent).
func (g *Genealogy) RecordRoot(id, agentType string, metadata map[string]any) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, exists := g.nodes[id]; !exists {
		node := &LineageNode{
			ID:        id,
			Type:      agentType,
			SpawnedAt: time.Now(),
			IsAlive:   true,
			Metadata:  metadata,
		}
		g.nodes[id] = node
		g.roots = append(g.roots, node)
	} else {
		// Node already exists (e.g., from a spawn record) — just mark alive.
		if node, ok := g.nodes[id]; ok {
			node.IsAlive = true
			node.SpawnedAt = time.Now()
		}
	}
	g.evictLocked()
}

// RecordDeath marks an agent as dead.
func (g *Genealogy) RecordDeath(agentID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node, ok := g.nodes[agentID]; ok {
		node.IsAlive = false
		node.DiedAt = time.Now()
	}
}

// evictLocked bounds the genealogy's memory (caller holds g.mu for
// writing). When the node count exceeds maxGenealogyNodes:
//
//  1. Whole oldest root subtrees are dropped while more than one root
//     remains — a retired lineage is history, and dropping it by the root
//     keeps the tree shape consistent (descendants go with their ancestor).
//  2. Within the remaining subtree(s), the oldest DEAD leaf nodes are pruned
//     one by one until under the cap or no dead leaf remains. Live nodes are
//     never pruned, so the active lineage is always fully queryable; dead
//     interior nodes become leaves as their descendants are pruned and are
//     reclaimed by later evictions.
func (g *Genealogy) evictLocked() {
	if maxGenealogyNodes <= 0 || len(g.nodes) <= maxGenealogyNodes {
		return
	}
	// Phase 1: retire whole oldest root subtrees (roots are in insertion
	// order; RecordResurrection replaces in place).
	for len(g.roots) > 1 && len(g.nodes) > maxGenealogyNodes {
		oldest := g.roots[0]
		g.roots = g.roots[1:]
		g.removeSubtreeLocked(oldest)
	}
	// Phase 2: prune oldest dead leaves in the surviving subtree(s).
	for len(g.nodes) > maxGenealogyNodes {
		if !g.pruneOldestDeadLeafLocked() {
			break
		}
	}
}

// removeSubtreeLocked deletes a node and every descendant from the nodes
// map (caller holds g.mu).
func (g *Genealogy) removeSubtreeLocked(n *LineageNode) {
	if n == nil {
		return
	}
	delete(g.nodes, n.ID)
	for _, c := range n.Children {
		g.removeSubtreeLocked(c)
	}
}

// pruneOldestDeadLeafLocked removes the oldest dead leaf node (no children,
// not alive) and unlinks it from its parent (or the roots list). Reports
// whether anything was pruned. Caller holds g.mu.
func (g *Genealogy) pruneOldestDeadLeafLocked() bool {
	var victim *LineageNode
	for _, n := range g.nodes {
		if n == nil || n.IsAlive || len(n.Children) > 0 {
			continue
		}
		if victim == nil || n.SpawnedAt.Before(victim.SpawnedAt) {
			victim = n
		}
	}
	if victim == nil {
		return false
	}
	delete(g.nodes, victim.ID)
	if victim.ParentID != "" {
		if parent, ok := g.nodes[victim.ParentID]; ok {
			kept := make([]*LineageNode, 0, len(parent.Children))
			for _, c := range parent.Children {
				if c != victim {
					kept = append(kept, c)
				}
			}
			parent.Children = kept
		}
	} else {
		for i, r := range g.roots {
			if r == victim {
				g.roots = append(g.roots[:i], g.roots[i+1:]...)
				break
			}
		}
	}
	return true
}

// RecordPromotion marks an agent as promoted to leader.
func (g *Genealogy) RecordPromotion(agentID string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if node, ok := g.nodes[agentID]; ok {
		node.Relation = RelationPromoted
		if node.Metadata == nil {
			node.Metadata = make(map[string]any)
		}
		node.Metadata["promoted_at"] = time.Now()
	}
}

// GetNode returns a node by ID.
func (g *Genealogy) GetNode(id string) (*LineageNode, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// Roots returns root nodes (agents with no parent).
func (g *Genealogy) Roots() []*LineageNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*LineageNode, len(g.roots))
	copy(result, g.roots)
	return result
}

// Descendants returns all descendants of an agent.
func (g *Genealogy) Descendants(id string) []*LineageNode {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, ok := g.nodes[id]
	if !ok {
		return nil
	}

	var result []*LineageNode
	collectDescendants(node, &result)
	return result
}

func collectDescendants(node *LineageNode, result *[]*LineageNode) {
	for _, child := range node.Children {
		*result = append(*result, child)
		collectDescendants(child, result)
	}
}

// Ancestors returns the ancestor chain from root to this agent.
func (g *Genealogy) Ancestors(id string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	var chain []string
	current := id
	for current != "" {
		chain = append([]string{current}, chain...)
		node, ok := g.nodes[current]
		if !ok {
			break
		}
		current = node.ParentID
	}
	return chain
}

// IsAlive checks if an agent is currently alive.
func (g *Genealogy) IsAlive(id string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, ok := g.nodes[id]
	if !ok {
		return false
	}
	return node.IsAlive
}

// ExportMermaid renders the genealogy as a Mermaid flowchart.
func (g *Genealogy) ExportMermaid() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if len(g.roots) == 0 {
		return "graph LR\n    empty[No agents]"
	}

	var b strings.Builder
	b.WriteString("graph LR\n")

	for _, root := range g.roots {
		writeLineageMermaid(&b, root, "    ")
	}
	return b.String()
}

func writeLineageMermaid(b *strings.Builder, n *LineageNode, indent string) {
	icon := "🤖"
	if !n.IsAlive {
		icon = "💀"
	}
	if n.Relation == RelationPromoted {
		icon = "👑"
	}

	status := "alive"
	if !n.IsAlive {
		status = "dead"
	}

	nodeID := strings.ReplaceAll(n.ID, "-", "_")
	fmt.Fprintf(b, "%s%s[\"%s %s (%s) %s\"]\n", indent, nodeID, icon, n.ID, n.Type, status)

	for _, child := range n.Children {
		childID := strings.ReplaceAll(child.ID, "-", "_")
		rel := string(child.Relation)
		fmt.Fprintf(b, "%s%s -->|%s| %s\n", indent, nodeID, rel, childID)
		writeLineageMermaid(b, child, indent)
	}
}

// ExportJSON serializes the genealogy as JSON.
func (g *Genealogy) ExportJSON() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return json.MarshalIndent(g.roots, "", "  ")
}

// AllNodes returns all tracked nodes.
func (g *Genealogy) AllNodes() []*LineageNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*LineageNode, 0, len(g.nodes))
	for _, n := range g.nodes {
		result = append(result, n)
	}
	return result
}
