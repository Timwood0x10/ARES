package flight

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// NodeType classifies a graph node.
type NodeType string

const (
	NodeAgent NodeType = "agent"
	NodeTool  NodeType = "tool"
	NodeLLM   NodeType = "llm"
)

// NodeStatus represents the current state of a graph node.
type NodeStatus string

const (
	StatusRunning   NodeStatus = "running"
	StatusCompleted NodeStatus = "completed"
	StatusFailed    NodeStatus = "failed"
)

// GraphNode represents a single node in the call graph.
type GraphNode struct {
	ID       string         `json:"id"`
	ParentID string         `json:"parent_id,omitempty"`
	Type     NodeType       `json:"type"`
	Name     string         `json:"name"`
	Status   NodeStatus     `json:"status"`
	StartAt  time.Time      `json:"start_at"`
	EndAt    time.Time      `json:"end_at,omitempty"`
	Duration time.Duration  `json:"duration"`
	Children []*GraphNode   `json:"children,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Graph represents an agent call graph — a tree of agents, tools, and LLM calls.
type Graph struct {
	root  *GraphNode
	nodes map[string]*GraphNode
	mu    sync.RWMutex
}

// NewGraph creates an empty graph.
func NewGraph() *Graph {
	return &Graph{
		nodes: make(map[string]*GraphNode),
	}
}

// AddNode adds a node to the graph. If ParentID is set, it becomes a child of the parent.
func (g *Graph) AddNode(node *GraphNode) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.nodes[node.ID] = node

	if node.ParentID == "" {
		g.root = node
		return
	}

	if parent, ok := g.nodes[node.ParentID]; ok {
		parent.Children = append(parent.Children, node)
	}
}

// GetNode returns a node by ID.
func (g *Graph) GetNode(id string) (*GraphNode, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	n, ok := g.nodes[id]
	return n, ok
}

// Root returns the root node.
func (g *Graph) Root() *GraphNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root
}

// Nodes returns all nodes.
func (g *Graph) Nodes() []*GraphNode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]*GraphNode, 0, len(g.nodes))
	for _, n := range g.nodes {
		result = append(result, n)
	}
	return result
}

// Depth returns the maximum depth of the tree.
func (g *Graph) Depth() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.root == nil {
		return 0
	}
	return nodeDepth(g.root, 0)
}

func nodeDepth(n *GraphNode, current int) int {
	if len(n.Children) == 0 {
		return current
	}
	maxChild := current
	for _, c := range n.Children {
		if d := nodeDepth(c, current+1); d > maxChild {
			maxChild = d
		}
	}
	return maxChild
}

// ExportMermaid renders the graph as a Mermaid flowchart.
func (g *Graph) ExportMermaid() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.root == nil {
		return "graph LR\n    empty[No data]"
	}

	var b strings.Builder
	b.WriteString("graph LR\n")

	g.writeMermaidNode(&b, g.root, "    ")
	return b.String()
}

func (g *Graph) writeMermaidNode(b *strings.Builder, n *GraphNode, indent string) {
	icon := nodeIcon(n.Type)
	label := fmt.Sprintf("%s%s %s", icon, n.Name, statusEmoji(n.Status))
	nodeID := sanitizeID(n.ID)
	fmt.Fprintf(b, "%s%s[\"%s\"]\n", indent, nodeID, label)

	for _, child := range n.Children {
		childID := sanitizeID(child.ID)
		fmt.Fprintf(b, "%s%s --> %s\n", indent, nodeID, childID)
		g.writeMermaidNode(b, child, indent)
	}
}

func nodeIcon(t NodeType) string {
	switch t {
	case NodeAgent:
		return "🤖 "
	case NodeTool:
		return "🔧 "
	case NodeLLM:
		return "🧠 "
	default:
		return ""
	}
}

func statusEmoji(s NodeStatus) string {
	switch s {
	case StatusRunning:
		return "⏳"
	case StatusCompleted:
		return "✅"
	case StatusFailed:
		return "❌"
	default:
		return ""
	}
}

func sanitizeID(id string) string {
	return strings.ReplaceAll(id, "-", "_")
}

// ExportDOT renders the graph as a Graphviz DOT diagram.
func (g *Graph) ExportDOT() string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if g.root == nil {
		return "digraph {}"
	}

	var b strings.Builder
	b.WriteString("digraph AgentCallGraph {\n")
	b.WriteString("  rankdir=LR;\n")
	b.WriteString("  node [shape=box, style=rounded];\n")

	g.writeDOTNode(&b, g.root)
	b.WriteString("}\n")
	return b.String()
}

func (g *Graph) writeDOTNode(b *strings.Builder, n *GraphNode) {
	color := nodeColor(n.Status)
	fmt.Fprintf(b, "  \"%s\" [label=\"%s\\n%s\", fillcolor=\"%s\", style=\"rounded,filled\"];\n",
		n.ID, string(n.Type), n.Name, color)

	for _, child := range n.Children {
		fmt.Fprintf(b, "  \"%s\" -> \"%s\";\n", n.ID, child.ID)
		g.writeDOTNode(b, child)
	}
}

func nodeColor(s NodeStatus) string {
	switch s {
	case StatusRunning:
		return "#FFF3CD"
	case StatusCompleted:
		return "#D4EDDA"
	case StatusFailed:
		return "#F8D7DA"
	default:
		return "#E2E3E5"
	}
}

// ExportJSON serializes the graph as JSON.
func (g *Graph) ExportJSON() ([]byte, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return json.MarshalIndent(g.root, "", "  ")
}
