//go:build !researchdemo

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"goagentx/internal/quant/research"
)

// TestCreateResearchConfig verifies that createResearchConfig returns
// a properly initialized ResearchConfig with expected defaults.
func TestCreateResearchConfig(t *testing.T) {
	cfg := createResearchConfig("AAPL")

	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if len(cfg.SelectedAnalysts) != 4 {
		t.Errorf("expected 4 analysts, got %d", len(cfg.SelectedAnalysts))
	}
	if cfg.MaxDebateRounds != 2 {
		t.Errorf("expected MaxDebateRounds=2, got %d", cfg.MaxDebateRounds)
	}
	if cfg.MaxRiskRounds != 1 {
		t.Errorf("expected MaxRiskRounds=1, got %d", cfg.MaxRiskRounds)
	}
	if cfg.QuickModel != "gpt-4o-mini" {
		t.Errorf("expected QuickModel=gpt-4o-mini, got %s", cfg.QuickModel)
	}
	if cfg.DeepModel != "gpt-4o" {
		t.Errorf("expected DeepModel=gpt-4o, got %s", cfg.DeepModel)
	}
	if !cfg.CheckpointEnabled {
		t.Error("expected CheckpointEnabled=true")
	}
	if !cfg.MemoryEnabled {
		t.Error("expected MemoryEnabled=true")
	}
}

// TestSetupDataFlow verifies that setupDataFlow returns a properly
// configured VendorRouter and Validator without errors.
func TestSetupDataFlow(t *testing.T) {
	router, validator, err := setupDataFlow("AAPL")
	if err != nil {
		t.Fatalf("setupDataFlow failed: %v", err)
	}
	if router == nil {
		t.Fatal("expected non-nil router")
	}
	if validator == nil {
		t.Fatal("expected non-nil validator")
	}
}

// TestSetupDataFlowWithEmptyTicker verifies setupDataFlow works
// even when ticker is empty (parameter is intentionally unused).
func TestSetupDataFlowWithEmptyTicker(t *testing.T) {
	router, validator, err := setupDataFlow("")
	if err != nil {
		t.Fatalf("setupDataFlow with empty ticker failed: %v", err)
	}
	if router == nil || validator == nil {
		t.Fatal("expected non-nil router and validator")
	}
}

// TestPrintResearchReport verifies that printResearchReport handles
// a fully populated state without panicking.
func TestPrintResearchReport(t *testing.T) {
	state := &research.ResearchState{
		Symbol:       "TEST",
		AnalysisDate: time.Now(),
		Config:       createResearchConfig("TEST"),
		AnalystReports: map[string]*research.AnalystReport{
			"market_analyst": {
				AnalystName: "Market Analyst",
				AnalystType: "market",
				Score:       65.0,
				Verdict:     "neutral",
				Timestamp:   time.Now(),
				Confidence:  0.7,
			},
		},
		ResearchPlan: &research.ResearchPlan{
			Recommendation:  research.RatingHold,
			Rationale:       "Test rationale",
			StrategicAction: "Test action",
		},
		TraderProposal: &research.TraderProposal{
			Action:         "HOLD",
			Reasoning:      "Test reasoning",
			PositionSizing: "maintain",
		},
		PortfolioDecision: &research.PortfolioDecision{
			Rating:           research.RatingHold,
			ExecutiveSummary: "Test summary",
			InvestmentThesis: "Test thesis",
		},
		StepsCompleted: []string{"market_analyst", "research_manager"},
	}

	var lines []string
	logFn := func(f string, a ...any) { lines = append(lines, f) }

	printResearchReport(state, logFn, 12)

	if len(lines) == 0 {
		t.Fatal("expected printResearchReport to produce output")
	}

	// Verify key sections are present.
	foundReport := false
	foundPlan := false
	foundDecision := false
	for _, line := range lines {
		// FIX: Info — use standard library strings.Contains instead of hand-rolled loop.
		if strings.Contains(line, "研究报告") {
			foundReport = true
		}
		if strings.Contains(line, "Research Plan") {
			foundPlan = true
		}
		if strings.Contains(line, "Portfolio Decision") {
			foundDecision = true
		}
	}
	if !foundReport {
		t.Error("expected report header in output")
	}
	if !foundPlan {
		t.Error("expected research plan section in output")
	}
	if !foundDecision {
		t.Error("expected portfolio decision section in output")
	}
}

// TestPrintResearchReportWithNilFields verifies the report function
// handles partial/nil state fields gracefully.
func TestPrintResearchReportWithNilFields(t *testing.T) {
	state := &research.ResearchState{
		Symbol:       "EMPTY",
		AnalysisDate: time.Now(),
		Config:       createResearchConfig("EMPTY"),
		Error:        context.Canceled,
	}

	var lines []string
	logFn := func(f string, a ...any) { lines = append(lines, f) }

	printResearchReport(state, logFn, 12)

	if len(lines) == 0 {
		t.Fatal("expected output even for minimal state")
	}
}

// TestUpdateStateFromResponse verifies that updateStateFromResponse
// correctly populates different state fields based on node ID.
func TestUpdateStateFromResponse(t *testing.T) {
	tests := []struct {
		name     string
		nodeID   string
		wantKind string // which field to check
	}{
		{"market analyst", "market_analyst", "analyst"},
		{"sentiment analyst", "sentiment_analyst", "analyst"},
		{"news analyst", "news_analyst", "analyst"},
		{"fundamentals analyst", "fundamentals_analyst", "analyst"},
		{"research manager", "research_manager", "plan"},
		{"trader", "trader", "proposal"},
		{"portfolio manager", "portfolio_manager", "decision"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &research.ResearchState{
				Symbol:         "TST",
				AnalysisDate:   time.Now(),
				Config:         createResearchConfig("TST"),
				AnalystReports: make(map[string]*research.AnalystReport),
			}
			updateStateFromResponse(state, tt.nodeID, "test_node", `{"test": true}`, "TST")

			switch tt.wantKind {
			case "analyst":
				if state.AnalystReports[tt.nodeID] == nil {
					t.Errorf("expected analyst report for node %q", tt.nodeID)
				}
			case "plan":
				if state.ResearchPlan == nil {
					t.Error("expected non-nil ResearchPlan")
				}
			case "proposal":
				if state.TraderProposal == nil {
					t.Error("expected non-nil TraderProposal")
				}
			case "decision":
				if state.PortfolioDecision == nil {
					t.Error("expected non-nil PortfolioDecision")
				}
			}
		})
	}
}

// TestYahooVendorAdapter verifies the yahooVendorAdapter satisfies
// the dataflow.Vendor interface contract.
func TestYahooVendorAdapter(t *testing.T) {
	adapter := &yahooVendorAdapter{feed: nil}

	if adapter.Name() != "yahoo" {
		t.Errorf("expected name=yahoo, got %s", adapter.Name())
	}
	if !adapter.Available() {
		t.Error("expected Available()=true")
	}
}

// TestRunWithResearchLayerIntegration is an integration-style test
// that exercises runWithResearchLayer with mock executor.
func TestRunWithResearchLayerIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := runWithResearchLayer(ctx, "AAPL", t.TempDir())
	if err != nil {
		t.Fatalf("runWithResearchLayer failed: %v", err)
	}
}

// FIX: Info — removed hand-rolled contains function; using strings.Contains from standard library.
