package research

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── Graph Build Tests ──────────────────────────────────────

func TestGraphBuilder_Build_AllNodes(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)
	require.NotNil(t, graph)

	nodes := graph.Nodes()
	assert.Len(t, nodes, 12, "expected 12 nodes in full graph")

	order := graph.Order()
	assert.Len(t, order, 12, "expected 12 entries in execution order")
}

func TestGraphBuilder_Build_NodeNames(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	expectedNodes := map[string]string{
		"market_analyst":       "Market Analyst",
		"sentiment_analyst":    "Sentiment Analyst",
		"news_analyst":         "News Analyst",
		"fundamentals_analyst": "Fundamentals Analyst",
		"bull_researcher":      "Bull Researcher",
		"bear_researcher":      "Bear Researcher",
		"research_manager":     "Research Manager",
		"trader":               "Trader",
		"aggressive_risk":      "Aggressive Risk Analyst",
		"conservative_risk":    "Conservative Risk Analyst",
		"neutral_risk":         "Neutral Risk Analyst",
		"portfolio_manager":    "Portfolio Manager",
	}
	for id, name := range expectedNodes {
		node, ok := graph.Nodes()[id]
		assert.True(t, ok, "node %q should exist", id)
		if ok {
			assert.Equal(t, name, node.Name)
		}
	}
}

func TestGraphBuilder_Build_StableOrder(t *testing.T) {
	builder := NewGraphBuilder(nil)

	graph1, err := builder.Build()
	require.NoError(t, err)
	order1 := graph1.Order()

	graph2, err := builder.Build()
	require.NoError(t, err)
	order2 := graph2.Order()

	assert.Equal(t, order1, order2, "graph order should be deterministic across builds")
}

func TestGraphBuilder_Build_DisableAnalysts(t *testing.T) {
	builder := NewGraphBuilder(&GraphConfig{
		EnabledAnalysts: []string{"market_analyst", "sentiment_analyst"},
	})
	graph, err := builder.Build()
	require.NoError(t, err)

	nodes := graph.Nodes()
	// Only the two enabled analysts should exist among analyst nodes.
	assert.Contains(t, nodes, "market_analyst")
	assert.Contains(t, nodes, "sentiment_analyst")
	assert.NotContains(t, nodes, "news_analyst")
	assert.NotContains(t, nodes, "fundamentals_analyst")

	// Non-analyst nodes should still be present.
	assert.Contains(t, nodes, "bull_researcher")
	assert.Contains(t, nodes, "portfolio_manager")
}

func TestGraphBuilder_Build_AllDisabledAnalysts(t *testing.T) {
	builder := NewGraphBuilder(&GraphConfig{
		EnabledAnalysts: []string{}, // empty = all disabled
	})
	graph, err := builder.Build()
	require.NoError(t, err)

	// No analyst nodes but debaters and managers still present.
	nodes := graph.Nodes()
	assert.NotContains(t, nodes, "market_analyst")
	assert.Contains(t, nodes, "trader")
	assert.Contains(t, nodes, "portfolio_manager")
}

// ─── Node Type Tests ────────────────────────────────────────

func TestNodeType_Values(t *testing.T) {
	tests := []struct {
		typ  NodeType
		want string
	}{
		{NodeTypeAnalyst, "analyst"},
		{NodeTypeDebater, "debater"},
		{NodeTypeManager, "manager"},
		{NodeTypeTrader, "trader"},
		{NodeType(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.typ.String())
		})
	}
}

func TestNodeType_IotaStartsAtOne(t *testing.T) {
	assert.Greater(t, int(NodeTypeAnalyst), 0, "iota+1 must start at 1")
}

// ─── Dependency Tests ───────────────────────────────────────

func TestGraphDependencies_BullBearDependOnAnalysts(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	bull := graph.Nodes()["bull_researcher"]
	bear := graph.Nodes()["bear_researcher"]

	// Both debaters depend on all four analysts.
	expectedDeps := []string{"market_analyst", "sentiment_analyst", "news_analyst", "fundamentals_analyst"}
	for _, dep := range expectedDeps {
		assert.Contains(t, bull.DependsOn, dep, "bull_researcher should depend on %s", dep)
		assert.Contains(t, bear.DependsOn, dep, "bear_researcher should depend on %s", dep)
	}
}

func TestGraphDependencies_ManagerDependsOnDebaters(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	rm := graph.Nodes()["research_manager"]
	assert.Contains(t, rm.DependsOn, "bull_researcher")
	assert.Contains(t, rm.DependsOn, "bear_researcher")
}

func TestGraphDependencies_TraderDependsOnManager(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	trader := graph.Nodes()["trader"]
	assert.Equal(t, []string{"research_manager"}, trader.DependsOn)
}

func TestGraphDependencies_RiskDependsOnTrader(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	riskIDs := []string{"aggressive_risk", "conservative_risk", "neutral_risk"}
	for _, id := range riskIDs {
		node := graph.Nodes()[id]
		assert.Equal(t, []string{"trader"}, node.DependsOn, "%s should only depend on trader", id)
	}
}

func TestGraphDependencies_PMDependsOnAllRisk(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	pm := graph.Nodes()["portfolio_manager"]
	expectedRisk := []string{"aggressive_risk", "conservative_risk", "neutral_risk"}
	for _, r := range expectedRisk {
		assert.Contains(t, pm.DependsOn, r)
	}
}

// ─── Execute Tests (Serial) ──────────────────────────────────

func TestGraphExecute_Serial_AllPass(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	// Attach no-op handlers to all nodes.
	callOrder := make([]string, 0, 12)
	for _, nodeID := range graph.Order() {
		node := graph.nodes[nodeID]
		capturedID := nodeID
		node.Handler = func(ctx context.Context, state *ResearchState) error {
			callOrder = append(callOrder, capturedID)
			return nil
		}
	}

	state := NewResearchState("TEST", time.Now().UTC(), nil)
	err = graph.Execute(context.Background(), state)
	require.NoError(t, err)

	// All steps completed.
	assert.Len(t, state.StepsCompleted, 12)
	assert.Nil(t, state.Error)
	// Execution order matches graph order (serial).
	assert.Equal(t, graph.Order(), callOrder)
}

func TestGraphExecute_Serial_HandlerErrorAborts(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	targetErr := errors.New("simulated failure")

	// Make research_manager fail.
	graph.nodes["research_manager"].Handler = func(ctx context.Context, state *ResearchState) error {
		return targetErr
	}

	state := NewResearchState("FAIL", time.Now().UTC(), nil)
	err = graph.Execute(context.Background(), state)

	assert.Error(t, err)
	assert.True(t, errors.Is(err, targetErr))
	// Should have completed up to (but not including) research_manager.
	assert.Less(t, len(state.StepsCompleted), 12)
	// Error is recorded in state.
	assert.NotNil(t, state.Error)
}

func TestGraphExecute_Serial_NilHandlerSkips(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	// Leave handlers as nil — they should be skipped gracefully.
	state := NewResearchState("SKIP", time.Now().UTC(), nil)
	err = graph.Execute(context.Background(), state)
	require.NoError(t, err)
	assert.Len(t, state.StepsCompleted, 12)
}

func TestGraphExecute_ContextCancellation(t *testing.T) {
	builder := NewGraphBuilder(nil)
	graph, err := builder.Build()
	require.NoError(t, err)

	callCount := 0
	for _, nodeID := range graph.Order() {
		node := graph.nodes[nodeID]
		node.Handler = func(ctx context.Context, state *ResearchState) error {
			callCount++
			if callCount >= 3 {
				return context.Canceled
			}
			return nil
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	state := NewResearchState("CANCEL", time.Now().UTC(), nil)
	err = graph.Execute(ctx, state)
	assert.Error(t, err)
	assert.Less(t, len(state.StepsCompleted), 12)
}

// ─── Cycle Detection Tests ─────────────────────────────────

func TestCycleDetection_AcyclicGraph_OK(t *testing.T) {
	builder := NewGraphBuilder(nil)
	_, err := builder.Build()
	assert.NoError(t, err, "the standard TradingAgents graph should be acyclic")
}

func TestCycleDetection_CustomCycle(t *testing.T) {
	// Manually construct a cyclic graph to verify detection works.
	nodes := map[string]*Node{
		"a": {ID: "a", DependsOn: []string{"b"}},
		"b": {ID: "b", DependsOn: []string{"c"}},
		"c": {ID: "c", DependsOn: []string{"a"}}, // cycle!
	}
	edges := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	err := detectCycle(nodes, edges)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle detected")
}

func TestCycleDetection_NoCycles(t *testing.T) {
	nodes := map[string]*Node{
		"a": {ID: "a", DependsOn: nil},
		"b": {ID: "b", DependsOn: []string{"a"}},
		"c": {ID: "c", DependsOn: []string{"a"}},
		"d": {ID: "d", DependsOn: []string{"b", "c"}},
	}
	edges := map[string][]string{
		"b": {"a"},
		"c": {"a"},
		"d": {"b", "c"},
	}
	err := detectCycle(nodes, edges)
	assert.NoError(t, err)
}

// ─── Config Tests ───────────────────────────────────────────

func TestNewGraphBuilder_NilConfig(t *testing.T) {
	builder := NewGraphBuilder(nil)
	assert.NotNil(t, builder)
	graph, err := builder.Build()
	require.NoError(t, err)
	assert.NotNil(t, graph)
}

func TestGraphConfig_DefaultValues(t *testing.T) {
	cfg := &GraphConfig{}
	assert.Empty(t, cfg.EnabledAnalysts)
	assert.Zero(t, cfg.MaxDebateRounds)
	assert.Zero(t, cfg.MaxRiskRounds)
}
