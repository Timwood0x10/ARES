//go:build researchdemo

package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"goagentx/internal/quant/research"
	researchagents "goagentx/internal/quant/research/agents"
)

// TestRunResearchDemo verifies that runResearchDemo completes without error
// using mock executor and produces valid output.
//
// NOTE: outputPath is treated as a directory by executeResearchGraph;
// the actual file name is auto-generated as {ticker}_research_{timestamp}.json.
func TestRunResearchDemo(t *testing.T) {
	tmpDir := t.TempDir()

	err := runResearchDemo(context.Background(), "AAPL", tmpDir)
	if err != nil {
		t.Fatalf("runResearchDemo failed: %v", err)
	}

	// Find the generated result file inside the output directory.
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one output file")
	}

	// Read the first (and should be only) JSON result file.
	resultPath := tmpDir + "/" + entries[0].Name()
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read output file %s: %v", resultPath, err)
	}

	// Verify it's valid JSON containing expected fields.
	var state research.ResearchState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if state.Symbol != "AAPL" {
		t.Errorf("expected symbol=AAPL, got %s", state.Symbol)
	}
	if len(state.StepsCompleted) == 0 {
		t.Error("expected at least one completed step")
	}
	if state.PortfolioDecision == nil {
		t.Error("expected non-nil PortfolioDecision")
	}
}

// TestRunResearchDemoWithOutputPath tests that omitting output path
// still runs successfully (prints to stdout only).
func TestRunResearchDemoWithOutputPath(t *testing.T) {
	err := runResearchDemo(context.Background(), "TSLA", "")
	if err != nil {
		t.Fatalf("runResearchDemo with empty outputPath failed: %v", err)
	}
}

// TestSetupDemoMockResponses verifies mock responses are properly configured
// for all 12 graph nodes.
func TestSetupDemoMockResponses(t *testing.T) {
	exec := researchagents.NewMockLLMExecutor()
	setupDemoMockResponses(exec, "TEST")

	// Verify each node has a response configured.
	nodes := []string{
		"Market Analyst", "Sentiment Analyst", "News Analyst",
		"Fundamentals Analyst", "Bull Researcher", "Bear Researcher",
		"Research Manager", "Trader", "Aggressive Risk Analyst",
		"Conservative Risk Analyst", "Neutral Risk Analyst",
		"Portfolio Manager",
	}

	for _, name := range nodes {
		ctx := context.Background()
		msgs := []researchagents.Message{{Role: "user", Content: name}}
		resp, err := exec.Complete(ctx, msgs)
		if err != nil {
			t.Errorf("node %q: unexpected error: %v", name, err)
			continue
		}
		if resp == "" {
			t.Errorf("node %q: expected non-empty response", name)
		}
	}
}

// TestParseDemoResponse verifies response parsing for each node type.
func TestParseDemoResponse(t *testing.T) {
	state := &research.ResearchState{
		Symbol:         "TST",
		AnalysisDate:   time.Now(),
		Config:         createResearchConfig("TST"),
		AnalystReports: make(map[string]*research.AnalystReport),
	}

	tests := []struct {
		nodeID   string
		nodeName string
		check    func(*research.ResearchState) bool
	}{
		{
			"market_analyst", "Market Analyst",
			func(s *research.ResearchState) bool { return s.AnalystReports["market_analyst"] != nil },
		},
		{
			"sentiment_analyst", "Sentiment Analyst",
			func(s *research.ResearchState) bool { return s.AnalystReports["sentiment_analyst"] != nil },
		},
		{
			"news_analyst", "News Analyst",
			func(s *research.ResearchState) bool { return s.AnalystReports["news_analyst"] != nil },
		},
		{
			"fundamentals_analyst", "Fundamentals Analyst",
			func(s *research.ResearchState) bool { return s.AnalystReports["fundamentals_analyst"] != nil },
		},
		{
			"bull_researcher", "Bull Researcher",
			func(s *research.ResearchState) bool { return len(s.DebateState.BullArguments) > 0 },
		},
		{
			"bear_researcher", "Bear Researcher",
			func(s *research.ResearchState) bool { return len(s.DebateState.BearArguments) > 0 },
		},
		{
			"research_manager", "Research Manager",
			func(s *research.ResearchState) bool { return s.ResearchPlan != nil },
		},
		{
			"trader", "Trader",
			func(s *research.ResearchState) bool { return s.TraderProposal != nil },
		},
		{
			"portfolio_manager", "Portfolio Manager",
			func(s *research.ResearchState) bool { return s.PortfolioDecision != nil },
		},
	}

	for _, tt := range tests {
		t.Run(tt.nodeID, func(t *testing.T) {
			parseDemoResponse(state, tt.nodeID, tt.nodeName, `{"test":true}`, "TST")
			if !tt.check(state) {
				t.Errorf("parseDemoResponse for %q did not populate expected field", tt.nodeID)
			}
		})
	}
}

// TestWireDemoGraphHandlers verifies all nodes get handlers attached.
func TestWireDemoGraphHandlers(t *testing.T) {
	cfg := &research.GraphConfig{
		EnabledAnalysts: []string{"market", "sentiment"},
		MaxDebateRounds: 1,
		MaxRiskRounds:   1,
	}
	builder := research.NewGraphBuilder(cfg)
	graph, err := builder.Build()
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	exec := researchagents.NewMockLLMExecutor()
	setupDemoMockResponses(exec, "AAPL")
	wireDemoGraphHandlers(graph, exec, "AAPL")

	for _, nodeID := range graph.Order() {
		node := graph.Nodes()[nodeID]
		if node == nil {
			t.Errorf("node %q is nil", nodeID)
			continue
		}
		if node.Handler == nil {
			t.Errorf("node %q has no handler", nodeID)
		}
	}
}

// TestPrintDemoReport verifies the demo report renders correctly.
func TestPrintDemoReport(t *testing.T) {
	state := &research.ResearchState{
		Symbol:         "DEMO",
		AnalysisDate:   time.Now(),
		Config:         createResearchConfig("DEMO"),
		StepsCompleted: []string{"market_analyst", "portfolio_manager"},
		PortfolioDecision: &research.PortfolioDecision{
			Rating:           research.RatingBuy,
			ExecutiveSummary: "Test summary",
			InvestmentThesis: "Test thesis",
		},
	}

	var lines []string
	logFn := func(f string, a ...any) { lines = append(lines, f) }

	printDemoReport(state, logFn, 12)

	if len(lines) == 0 {
		t.Fatal("expected printDemoReport to produce output")
	}
}
