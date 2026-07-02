package research

import (
	"context"
	"fmt"
	"time"
)

// NodeType classifies research graph nodes by their role in the pipeline.
type NodeType int

const (
	// NodeTypeAnalyst represents data-gathering analyst nodes.
	NodeTypeAnalyst NodeType = iota + 1
	// NodeTypeDebater represents debate/convergence nodes.
	NodeTypeDebater
	// NodeTypeManager represents decision-making manager nodes.
	NodeTypeManager
	// NodeTypeTrader represents trading proposal nodes.
	NodeTypeTrader
)

// String returns a human-readable name for the node type.
func (nt NodeType) String() string {
	switch nt {
	case NodeTypeAnalyst:
		return "analyst"
	case NodeTypeDebater:
		return "debater"
	case NodeTypeManager:
		return "manager"
	case NodeTypeTrader:
		return "trader"
	default:
		return "unknown"
	}
}

// Node represents a single step in the research execution graph.
type Node struct {
	ID        string
	Name      string
	Type      NodeType
	DependsOn []string
	Handler   ResearchHandler
	Timeout   time.Duration
}

// ResearchHandler is the function signature for node execution logic.
// It receives the current research state and may mutate it.
type ResearchHandler func(ctx context.Context, state *ResearchState) error

// GraphConfig configures the research graph builder.
type GraphConfig struct {
	EnabledAnalysts  []string // analysts to include (empty = all)
	MaxDebateRounds  int
	MaxRiskRounds    int
	EnableCheckpoint bool
	EnableMemory     bool
}

// ResearchGraphBuilder constructs a fixed-order research execution graph
// modeled after the TradingAgents pipeline architecture.
type ResearchGraphBuilder struct {
	config *GraphConfig
}

// NewGraphBuilder creates a new ResearchGraphBuilder with the given configuration.
func NewGraphBuilder(cfg *GraphConfig) *ResearchGraphBuilder {
	if cfg == nil {
		cfg = &GraphConfig{}
	}
	return &ResearchGraphBuilder{config: cfg}
}

// Build constructs the fixed research graph with all nodes and edges.
//
// Node order (TradingAgents reference):
//
//  1. MarketAnalyst
//  2. SentimentAnalyst
//  3. NewsAnalyst
//  4. FundamentalsAnalyst
//  5. BullResearcher     (depends on 1-4)
//  6. BearResearcher     (depends on 1-4)
//  7. ResearchManager   (depends on 5,6)
//  8. Trader            (depends on 7)
//  9. AggressiveRisk    (depends on 8)
//  10. ConservativeRisk (depends on 8)
//  11. NeutralRisk      (depends on 8)
//  12. PortfolioManager (depends on 9,10,11)
func (b *ResearchGraphBuilder) Build() (*ResearchGraph, error) {
	nodes := make(map[string]*Node, 12)
	edges := make(map[string][]string, 12)
	var order []string

	enabled := b.enabledSet()

	// Phase 1: Analysts (parallel-ready, no dependencies).
	analysts := []struct {
		id   string
		name string
	}{
		{"market_analyst", "Market Analyst"},
		{"sentiment_analyst", "Sentiment Analyst"},
		{"news_analyst", "News Analyst"},
		{"fundamentals_analyst", "Fundamentals Analyst"},
	}
	for _, a := range analysts {
		if enabled != nil && !enabled[a.id] {
			continue
		}
		nodes[a.id] = &Node{
			ID:        a.id,
			Name:      a.name,
			Type:      NodeTypeAnalyst,
			DependsOn: nil,
			Timeout:   2 * time.Minute,
		}
		order = append(order, a.id)
	}

	// Phase 2: Debaters (depend on all analysts completing).
	analystIDs := orderIDs(nodes, analystsToIDs(analysts), enabled)
	nodes["bull_researcher"] = &Node{
		ID:        "bull_researcher",
		Name:      "Bull Researcher",
		Type:      NodeTypeDebater,
		DependsOn: analystIDs,
		Timeout:   3 * time.Minute,
	}
	nodes["bear_researcher"] = &Node{
		ID:        "bear_researcher",
		Name:      "Bear Researcher",
		Type:      NodeTypeDebater,
		DependsOn: analystIDs,
		Timeout:   3 * time.Minute,
	}
	order = append(order, "bull_researcher", "bear_researcher")

	// Phase 3: Research Manager (depends on debaters).
	nodes["research_manager"] = &Node{
		ID:        "research_manager",
		Name:      "Research Manager",
		Type:      NodeTypeManager,
		DependsOn: []string{"bull_researcher", "bear_researcher"},
		Timeout:   2 * time.Minute,
	}
	order = append(order, "research_manager")

	// Phase 4: Trader (depends on Research Manager).
	nodes["trader"] = &Node{
		ID:        "trader",
		Name:      "Trader",
		Type:      NodeTypeTrader,
		DependsOn: []string{"research_manager"},
		Timeout:   2 * time.Minute,
	}
	order = append(order, "trader")

	// Phase 5: Risk Analysts (depend on Trader).
	riskIDs := []string{"aggressive_risk", "conservative_risk", "neutral_risk"}
	riskNames := []string{"Aggressive Risk Analyst", "Conservative Risk Analyst", "Neutral Risk Analyst"}
	for i, id := range riskIDs {
		nodes[id] = &Node{
			ID:        id,
			Name:      riskNames[i],
			Type:      NodeTypeDebater,
			DependsOn: []string{"trader"},
			Timeout:   2 * time.Minute,
		}
	}
	order = append(order, riskIDs...)

	// Phase 6: Portfolio Manager (depends on all risk analysts).
	nodes["portfolio_manager"] = &Node{
		ID:        "portfolio_manager",
		Name:      "Portfolio Manager",
		Type:      NodeTypeManager,
		DependsOn: riskIDs,
		Timeout:   2 * time.Minute,
	}
	order = append(order, "portfolio_manager")

	// Build edge map from dependency declarations.
	for id, node := range nodes {
		if len(node.DependsOn) > 0 {
			edges[id] = make([]string, len(node.DependsOn))
			copy(edges[id], node.DependsOn)
		}
	}

	// Cycle detection.
	if err := detectCycle(nodes, edges); err != nil {
		return nil, fmt.Errorf("graph build: %w", err)
	}

	return &ResearchGraph{
		nodes: nodes,
		edges: edges,
		order: order,
	}, nil
}

// ResearchGraph is an executable research pipeline with nodes and dependency edges.
type ResearchGraph struct {
	nodes map[string]*Node
	edges map[string][]string // nodeID -> dependency IDs
	order []string            // topological execution order
}

// Execute runs the research graph serially over nodes in topological order.
// Each node's handler is called with the current state; errors abort execution.
// This first version uses simple sequential execution — no parallel scheduling.
func (g *ResearchGraph) Execute(ctx context.Context, state *ResearchState) error {
	for _, nodeID := range g.order {
		node, ok := g.nodes[nodeID]
		if !ok {
			return fmt.Errorf("execute: node %q not found", nodeID)
		}

		state.CurrentStep = nodeID

		if node.Handler == nil {
			// No-op handler: mark as completed and continue.
			state.StepsCompleted = append(state.StepsCompleted, nodeID)
			continue
		}

		// Apply timeout if configured.
		// Cancel is called explicitly after handler returns to prevent context
		// resource accumulation when handlers block.
		execCtx := ctx
		var cancel context.CancelFunc
		if node.Timeout > 0 {
			execCtx, cancel = context.WithTimeout(ctx, node.Timeout)
		}

		handlerErr := node.Handler(execCtx, state)

		// Cancel per-node timeout context immediately after handler returns.
		if cancel != nil {
			cancel()
		}

		if handlerErr != nil {
			state.Error = fmt.Errorf("node %q (%s): %w", nodeID, node.Name, handlerErr)
			return state.Error
		}

		state.StepsCompleted = append(state.StepsCompleted, nodeID)
	}
	return nil
}

// Nodes returns all registered nodes keyed by ID.
func (g *ResearchGraph) Nodes() map[string]*Node {
	return g.nodes
}

// Order returns the deterministic topological execution order.
func (g *ResearchGraph) Order() []string {
	return g.order
}

// ─── Internal Helpers ──────────────────────────────────────

func (b *ResearchGraphBuilder) enabledSet() map[string]bool {
	if b.config == nil {
		return nil // nil means all enabled
	}
	// Distinguish: nil EnabledAnalysts = all enabled; empty slice = explicitly none.
	if b.config.EnabledAnalysts == nil {
		return nil
	}
	set := make(map[string]bool, len(b.config.EnabledAnalysts))
	for _, a := range b.config.EnabledAnalysts {
		set[a] = true
	}
	return set
}

func analystsToIDs(analysts []struct {
	id   string
	name string
}) []string {
	ids := make([]string, len(analysts))
	for i, a := range analysts {
		ids[i] = a.id
	}
	return ids
}

func orderIDs(nodes map[string]*Node, allIDs []string, enabled map[string]bool) []string {
	if enabled == nil {
		return allIDs
	}
	var result []string
	for _, id := range allIDs {
		if _, exists := nodes[id]; exists {
			result = append(result, id)
		}
	}
	return result
}

// detectCycle checks for cycles using DFS-based topological sort validation.
func detectCycle(nodes map[string]*Node, edges map[string][]string) error {
	visited := make(map[string]int, len(nodes)) // 0=unvisited, 1=in-progress, 2=done

	var visit func(id string) error
	visit = func(id string) error {
		if visited[id] == 1 {
			return fmt.Errorf("cycle detected involving node %q", id)
		}
		if visited[id] == 2 {
			return nil
		}
		visited[id] = 1
		for _, dep := range edges[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		visited[id] = 2
		return nil
	}

	for id := range nodes {
		if visited[id] == 0 {
			if err := visit(id); err != nil {
				return err
			}
		}
	}
	return nil
}
