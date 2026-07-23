// Package compiler provides the Conversation Compiler — the core pipeline that
// transforms conversation messages into a structured Knowledge Model (KM) graph.
// The KM graph is a unified semantic representation consumed by Prompt Builder,
// Memory Emitter, AKG Builder, and Analytics.
package compiler

import (
	"fmt"
	"sort"
	"time"
)

// NodeType identifies the type of a knowledge node.
type NodeType string

const (
	NodeEntity     NodeType = "entity"     // Entity (Rust, ARES, RuntimePatch)
	NodeFact       NodeType = "fact"       // Fact (ARES → uses → Patch)
	NodeDecision   NodeType = "decision"   // Decision (采用 Patch，拒绝热更新)
	NodeGoal       NodeType = "goal"       // Goal (实现 Context Lifecycle)
	NodeConstraint NodeType = "constraint" // Constraint (SaaS 成本必须可控)
	NodeTradeoff   NodeType = "tradeoff"   // Tradeoff (成本↓ 准确率↓)
	NodeQuestion   NodeType = "question"   // Open question
	NodeTask       NodeType = "task"       // Task item
	NodeEvidence   NodeType = "evidence"   // Evidence
	NodeReference  NodeType = "reference"  // External reference
	NodeMemory     NodeType = "memory"     // Distilled memory node (from Distiller)
)

// EdgeType identifies the type of relationship between two nodes.
type EdgeType string

const (
	EdgeDependsOn   EdgeType = "depends_on"
	EdgeSupports    EdgeType = "supports"
	EdgeContradicts EdgeType = "contradicts"
	EdgeImplements  EdgeType = "implements"
	EdgeCreates     EdgeType = "creates"
	EdgeResolves    EdgeType = "resolves"
	EdgeMentions    EdgeType = "mentions"
	EdgeReferences  EdgeType = "references"
	EdgeDecidedBy   EdgeType = "decided_by"
	EdgeLearnsFrom  EdgeType = "learns_from"
)

// Attribute key constants used across multiple compiler files.
const (
	attrName          = "name"
	attrSubject       = "subject"
	attrPredicate     = "predicate"
	attrObject        = "object"
	attrSelector      = "selector"
	attrNodesSelected = "nodes_selected"
	attrDescription   = "description"
	attrSummary       = "summary"
)

// Node is a single knowledge node in the Knowledge Model graph.
// Every node has a type, attributes, confidence score, and access count
// for pruning decisions.
type Node struct {
	ID          string         `json:"id"`
	Type        NodeType       `json:"type"`
	Attributes  map[string]any `json:"attributes"`
	Confidence  float64        `json:"confidence"`
	AccessCount int64          `json:"access_count"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Version     int            `json:"version"`
	Source      string         `json:"source,omitempty"` // Source message ID
}

// Edge connects two nodes with a named relationship.
type Edge struct {
	ID        string    `json:"id"`
	Type      EdgeType  `json:"type"`
	Source    string    `json:"source"` // Source node ID
	Target    string    `json:"target"` // Target node ID
	Weight    float64   `json:"weight"`
	CreatedAt time.Time `json:"created_at"`
}

// KnowledgeModel is the core semantic representation of compiled conversation.
// It is a directed graph where nodes are knowledge units and edges are
// relationships between them. Unlike a flat struct, the graph structure
// supports traversal, subgraph extraction, and incremental updates.
type KnowledgeModel struct {
	Nodes    map[string]*Node `json:"nodes"`
	Edges    []Edge           `json:"edges"`
	Metadata ModelMeta        `json:"metadata"`
}

// ModelMeta holds metadata about the Knowledge Model.
type ModelMeta struct {
	Version       int       `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	SourceCount   int       `json:"source_count"`             // Number of source messages compiled
	CompileCount  int       `json:"compile_count"`            // Number of times compiled
	PreviousModel string    `json:"previous_model,omitempty"` // ID of previous model version
}

// SubGraph is a subset of the Knowledge Model, selected by a Selector.
// Consumers (PromptBuilder, MemoryEmitter, etc.) operate on SubGraphs.
type SubGraph struct {
	Nodes    []*Node `json:"nodes"`
	Edges    []Edge  `json:"edges"`
	Metadata any     `json:"metadata,omitempty"`
}

// NewKnowledgeModel creates an empty Knowledge Model.
func NewKnowledgeModel() *KnowledgeModel {
	return &KnowledgeModel{
		Nodes: make(map[string]*Node),
		Edges: make([]Edge, 0),
		Metadata: ModelMeta{
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
}

// AddNode adds a node to the model. Returns error if node ID already exists.
func (km *KnowledgeModel) AddNode(node *Node) error {
	if node == nil {
		return fmt.Errorf("compiler: node must not be nil")
	}
	if node.ID == "" {
		return fmt.Errorf("compiler: node ID must not be empty")
	}
	if _, exists := km.Nodes[node.ID]; exists {
		return fmt.Errorf("compiler: node %q already exists", node.ID)
	}
	km.Nodes[node.ID] = node
	km.Metadata.UpdatedAt = time.Now()
	return nil
}

// AddEdge adds an edge to the model. Validates that source and target exist.
func (km *KnowledgeModel) AddEdge(edge Edge) error {
	if edge.ID == "" {
		return fmt.Errorf("compiler: edge ID must not be empty")
	}
	if _, exists := km.Nodes[edge.Source]; !exists {
		return fmt.Errorf("compiler: edge source node %q not found", edge.Source)
	}
	if _, exists := km.Nodes[edge.Target]; !exists {
		return fmt.Errorf("compiler: edge target node %q not found", edge.Target)
	}
	km.Edges = append(km.Edges, edge)
	km.Metadata.UpdatedAt = time.Now()
	return nil
}

// NodeCount returns the total number of nodes in the model.
func (km *KnowledgeModel) NodeCount() int { return len(km.Nodes) }

// EdgeCount returns the total number of edges in the model.
func (km *KnowledgeModel) EdgeCount() int { return len(km.Edges) }

// GetNodesByType returns all nodes of the given type.
func (km *KnowledgeModel) GetNodesByType(nt NodeType) []*Node {
	var result []*Node
	for _, n := range km.Nodes {
		if n.Type == nt {
			result = append(result, n)
		}
	}
	return result
}

// GetEdgesByType returns all edges of the given type.
func (km *KnowledgeModel) GetEdgesByType(et EdgeType) []Edge {
	var result []Edge
	for _, e := range km.Edges {
		if e.Type == et {
			result = append(result, e)
		}
	}
	return result
}

// GetOutgoingEdges returns all edges originating from the given node.
func (km *KnowledgeModel) GetOutgoingEdges(nodeID string) []Edge {
	var result []Edge
	for _, e := range km.Edges {
		if e.Source == nodeID {
			result = append(result, e)
		}
	}
	return result
}

// GetIncomingEdges returns all edges targeting the given node.
func (km *KnowledgeModel) GetIncomingEdges(nodeID string) []Edge {
	var result []Edge
	for _, e := range km.Edges {
		if e.Target == nodeID {
			result = append(result, e)
		}
	}
	return result
}

// ToSubGraph returns a SubGraph containing all nodes of the given types
// and their connected edges.
func (km *KnowledgeModel) ToSubGraph(types ...NodeType) *SubGraph {
	typeSet := make(map[NodeType]bool)
	for _, t := range types {
		typeSet[t] = true
	}
	nodeSet := make(map[string]bool)
	var nodes []*Node
	for _, n := range km.Nodes {
		if typeSet[n.Type] {
			nodeSet[n.ID] = true
			nodes = append(nodes, n)
		}
	}
	var edges []Edge
	for _, e := range km.Edges {
		if nodeSet[e.Source] && nodeSet[e.Target] {
			edges = append(edges, e)
		}
	}
	return &SubGraph{Nodes: nodes, Edges: edges}
}

// Prune removes nodes and their edges based on the pruning policy.
// Returns the number of nodes removed.
func (km *KnowledgeModel) Prune(maxNodes int, minConfidence float64) int {
	if len(km.Nodes) <= maxNodes {
		return 0
	}

	// Collect nodes for pruning: low confidence, non-memory nodes.
	type scored struct {
		id    string
		score float64
	}
	var candidates []scored
	for id, n := range km.Nodes {
		if n.Type == NodeMemory {
			continue // Always keep memory nodes.
		}
		candidates = append(candidates, scored{id: id, score: n.Confidence * float64(n.AccessCount+1)})
	}

	// Sort ascending by score (O(n log n) — replaces the previous O(n^2)
	// bubble sort). sort.Slice is not stable, but ties share the same score
	// and are interchangeable for pruning.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score < candidates[j].score
	})

	// Remove lowest-scored nodes until under maxNodes or no more candidates.
	removed := 0
	target := len(km.Nodes) - maxNodes
	for _, c := range candidates {
		if removed >= target {
			break
		}
		if c.score < minConfidence {
			km.removeNode(c.id)
			removed++
		}
	}
	return removed
}

// removeNode deletes a node and all its edges from the model.
func (km *KnowledgeModel) removeNode(id string) {
	delete(km.Nodes, id)
	var kept []Edge
	for _, e := range km.Edges {
		if e.Source != id && e.Target != id {
			kept = append(kept, e)
		}
	}
	km.Edges = kept
}
