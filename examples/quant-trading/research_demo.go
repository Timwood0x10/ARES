//go:build researchdemo

// Package main — standalone demo for the research layer.
// Demonstrates how to use the new research layer to run a complete
// 12-node structured stock analysis pipeline without requiring an external LLM.
//
// Build tag required (excludes default build to avoid main() conflict):
//
//	go run -tags=researchdemo research_demo.go --ticker AAPL
//	go run -tags=researchdemo research_demo.go --ticker TSLA --output ./demo_output.json
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_quant/research"
	researchagents "github.com/Timwood0x10/ares/internal/ares_quant/research/agents"
)

func main() {
	var ticker, outputPath string

	flag.StringVar(&ticker, "ticker", "AAPL", "Stock symbol to analyze")
	flag.StringVar(&outputPath, "output", "", "Path to save JSON output (optional)")
	flag.Parse()

	ticker = strings.ToUpper(strings.TrimSpace(ticker))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := runResearchDemo(ctx, ticker, outputPath); err != nil {
		slog.Error("research demo failed", "err", err)
		os.Exit(1)
	}
}

// runResearchDemo runs the full 12-node research pipeline using MockLLMExecutor.
// It demonstrates each step of the research graph with mock LLM responses,
// producing structured JSON and markdown reports without external dependencies.
//
// Args:
//   - ctx: context for cancellation.
//   - ticker: stock symbol to analyze (uppercased).
//   - outputPath: optional file path for JSON output; empty means stdout only.
//
// Returns:
//   - error if any critical step fails.
func runResearchDemo(ctx context.Context, ticker string, outputPath string) error {
	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	log("╔════════════════════════════════════════════════╗")
	log("║  Research Layer Demo (Offline / No LLM Needed) ║")
	log("╚════════════════════════════════════════════════╝")
	log("  Ticker: %s", ticker)
	log("  Time: %s\n", time.Now().Format(time.RFC3339))

	// Step 1: Create state with default config.
	cfg := &research.ResearchConfig{
		SelectedAnalysts:  []string{"market", "sentiment", "news", "fundamentals"},
		MaxDebateRounds:   2,
		MaxRiskRounds:     1,
		QuickModel:        "gpt-4o-mini",
		DeepModel:         "gpt-4o",
		OutputLanguage:    "english",
		DataVendors:       []string{"yahoo"},
		CheckpointEnabled: true,
		MemoryEnabled:     true,
	}
	state := research.NewResearchState(ticker, time.Now(), cfg)

	// Step 2: Build research graph.
	graphCfg := &research.GraphConfig{
		EnabledAnalysts: cfg.SelectedAnalysts,
		MaxDebateRounds: cfg.MaxDebateRounds,
		MaxRiskRounds:   cfg.MaxRiskRounds,
	}
	builder := research.NewGraphBuilder(graphCfg)
	graph, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	log("  Research graph built (%d nodes):", len(graph.Order()))
	for i, nodeID := range graph.Order() {
		node := graph.Nodes()[nodeID]
		if node != nil {
			log("    %2d. [%s] %s (%s)", i+1, nodeID, node.Name, node.Type)
		}
	}
	log("")

	// Step 3: Set up mock executor with predefined responses.
	mockExec := researchagents.NewMockLLMExecutor()
	setupDemoMockResponses(mockExec, ticker)

	// Step 4: Wire handlers and execute via shared pipeline.
	wireDemoGraphHandlers(graph, mockExec, ticker)

	// FIX: Minor #10 — use shared executeResearchGraph with demo-specific reporter.
	if err := executeResearchGraph(ctx, graph, state, ticker, outputPath, log, printDemoReport); err != nil {
		return fmt.Errorf("execute graph: %w", err)
	}

	// Demo-only: when no output path, print compact JSON to stdout.
	if outputPath == "" {
		data, err := state.ToJSON()
		if err != nil {
			return fmt.Errorf("serialize state: %w", err)
		}
		log("\n  ─── Full State (JSON) ───\n%s\n", string(data))
	}

	return nil
}

// setupDemoMockResponses configures realistic mock responses for all 12 nodes.
// Each response matches the expected JSON schema for that node type.
//
// Args:
//   - exec: mock executor to configure.
//   - ticker: stock symbol used in response templates.
func setupDemoMockResponses(exec *researchagents.MockLLMExecutor, ticker string) {
	exec.SetResponse("Market Analyst", `{
  "score": 68,
  "verdict": "bullish",
  "confidence": 0.72,
  "findings": {
    "trend": "uptrend",
    "rsi_state": "neutral",
    "macd_signal": "bullish",
    "reasoning": "Price above key moving averages with expanding volume."
  }
}`)

	exec.SetResponse("Sentiment Analyst", `{
  "band": "bullish",
  "score": 0.62,
  "confidence": 0.72,
  "signals": ["positive_social_sentiment", "increasing_call_volume"]
}`)

	exec.SetResponse("News Analyst", `{
  "score": 65,
  "verdict": "bullish",
  "confidence": 0.68,
  "findings": {
    "positive_factors": ["Q3 earnings beat by 15%", "New AI product line announced"],
    "negative_factors": [],
    "topics": ["earnings", "product", "AI"],
    "reasoning": "Strong earnings beat and positive product momentum."
  }
}`)

	exec.SetResponse("Fundamentals Analyst", `{
  "score": 75,
  "verdict": "bullish",
  "confidence": 0.78,
  "findings": {
    "revenue_growth": "12%",
    "pe_ratio": "26.5",
    "debt_to_equity": "1.3",
    "strengths": ["Strong revenue growth", "Healthy margins"],
    "risks": ["Elevated valuation", "Competitive pressure"],
    "reasoning": "Fundamentals support continued growth with manageable risk."
  }
}`)

	exec.SetResponse("Bull Researcher", fmt.Sprintf(`{
  "score": 80,
  "thesis": "%s has strong moat with recurring revenue growth accelerating to 15%% YoY.",
  "arguments": ["AI tailwind", "Margin expansion", "Strong brand"],
  "target": "250",
  "confidence": 0.75
}`, ticker))

	exec.SetResponse("Bear Researcher", fmt.Sprintf(`{
  "score": 45,
  "thesis": "%s valuation at 28x earnings leaves limited margin of safety; competition intensifying.",
  "arguments": ["High valuation", "Competition risk", "Cyclical exposure"],
  "target": "170",
  "confidence": 0.55
}`, ticker))

	exec.SetResponse("Research Manager", `{
  "recommendation": "Overweight",
  "rationale": "Strong fundamentals and product momentum outweigh valuation concerns.",
  "strategic_action": "Accumulate on dips; target 20% position size."
}`)

	exec.SetResponse("Trader", fmt.Sprintf(`{
  "action": "buy",
  "reasoning": "Research plan for %s is Overweight with clear entry zone near current levels.",
  "entry_price": 195.0,
  "stop_loss": 175.0,
  "position_sizing": "5%%"
}`, ticker))

	exec.SetResponse("Aggressive Risk Analyst", `{
  "risk_level": "medium",
  "tail_risks": ["Regulatory crackdown", "Black swan event"],
  "max_drawdown_estimate": "15%",
  "recommendation": "proceed",
  "reasoning": "Acceptable risk-reward for aggressive stance."
}`)

	exec.SetResponse("Conservative Risk Analyst", `{
  "risk_level": "low",
  "downside_risks": ["Valuation compression", "Earnings miss"],
  "capital_preservation_score": 85,
  "recommendation": "proceed",
  "reasoning": "Conservative metrics within acceptable bounds."
}`)

	exec.SetResponse("Neutral Risk Analyst", `{
  "risk_level": "medium",
  "var_estimate": "8%",
  "expected_shortfall": "12%",
  "probability_weighted_return": "15-25%",
  "recommendation": "proceed",
  "reasoning": "Balanced risk profile with favorable risk-adjusted return."
}`)

	exec.SetResponse("Portfolio Manager", fmt.Sprintf(`{
  "rating": "Overweight",
  "executive_summary": "%s shows strong fundamental momentum with manageable risk.",
  "investment_thesis": "AI-driven revenue growth justifies premium; buy on weakness.",
  "price_target": 235.0,
  "time_horizon": "12 months"
}`, ticker))
}

// wireDemoGraphHandlers attaches handler functions to all nodes in the graph.
// Each handler calls the mock executor and parses the result into state.
//
// Args:
//   - graph: research graph to wire handlers onto.
//   - exec: mock LLM executor.
//   - ticker: stock symbol for state updates.
func wireDemoGraphHandlers(graph *research.ResearchGraph, exec researchagents.LLMExecutor, ticker string) {
	for _, nodeID := range graph.Order() {
		node := graph.Nodes()[nodeID]
		if node == nil {
			continue
		}
		node.Handler = makeDemoNodeHandler(nodeID, node.Name, exec, ticker)
	}
}

// makeDemoNodeHandler creates a ResearchHandler for demo execution.
// It logs each node's progress, calls the mock executor, and updates state.
//
// Args:
//   - nodeID: unique identifier for this node.
//   - nodeName: human-readable name.
//   - exec: mock LLM executor.
//   - ticker: stock symbol.
//
// Returns:
//   - ResearchHandler function for graph execution.
func makeDemoNodeHandler(nodeID string, nodeName string, exec researchagents.LLMExecutor, ticker string) research.ResearchHandler {
	return func(ctx context.Context, state *research.ResearchState) error {
		fmt.Printf("  → [%s] %s ...\n", nodeID, nodeName)

		prompt := fmt.Sprintf("%s: Analyze %s", nodeName, ticker)
		messages := []researchagents.Message{{Role: "user", Content: prompt}}

		raw, err := exec.Complete(ctx, messages)
		if err != nil {
			return fmt.Errorf("%s: %w", nodeName, err)
		}

		parseDemoResponse(state, nodeID, nodeName, raw, ticker)
		fmt.Printf("    ✓ %s completed\n", nodeName)
		return nil
	}
}

// parseDemoResponse parses raw mock output into typed state fields based on node type.
// This maps each graph node to its corresponding state field (analyst reports,
// research plan, trader proposal, or portfolio decision).
//
// Args:
//   - state: mutable research state to update.
//   - nodeID: identifier of the producing node.
//   - nodeName: human-readable name for report metadata.
//   - raw: raw JSON response from mock executor.
//   - ticker: stock symbol.
func parseDemoResponse(state *research.ResearchState, nodeID string, nodeName string, raw string, ticker string) {
	switch nodeID {
	case "market_analyst":
		state.AnalystReports[nodeID] = &research.AnalystReport{
			AnalystName: nodeName,
			AnalystType: "market",
			Score:       68.0,
			Verdict:     "bullish",
			RawOutput:   raw,
			Timestamp:   time.Now(),
			Confidence:  0.72,
		}
	case "sentiment_analyst":
		state.AnalystReports[nodeID] = &research.AnalystReport{
			AnalystName: nodeName,
			AnalystType: "sentiment",
			Score:       62.0,
			Verdict:     "bullish",
			RawOutput:   raw,
			Timestamp:   time.Now(),
			Confidence:  0.70,
		}
	case "news_analyst":
		state.AnalystReports[nodeID] = &research.AnalystReport{
			AnalystName: nodeName,
			AnalystType: "news",
			Score:       65.0,
			Verdict:     "positive",
			RawOutput:   raw,
			Timestamp:   time.Now(),
			Confidence:  0.68,
		}
	case "fundamentals_analyst":
		state.AnalystReports[nodeID] = &research.AnalystReport{
			AnalystName: nodeName,
			AnalystType: "fundamentals",
			Score:       75.0,
			Verdict:     "undervalued",
			RawOutput:   raw,
			Timestamp:   time.Now(),
			Confidence:  0.78,
		}
	case "bull_researcher", "bear_researcher":
		// Debate arguments stored in debate state.
		if state.DebateState == nil {
			state.DebateState = &research.InvestDebateState{}
		}
		if nodeID == "bull_researcher" {
			state.DebateState.BullArguments = append(state.DebateState.BullArguments, raw)
		} else {
			state.DebateState.BearArguments = append(state.DebateState.BearArguments, raw)
		}
	case "research_manager":
		state.ResearchPlan = &research.ResearchPlan{
			Recommendation:  research.RatingOverweight,
			Rationale:       "Strong fundamentals and product momentum outweigh valuation concerns.",
			StrategicAction: "Accumulate on dips; target 20% position size.",
		}
	case "trader":
		state.TraderProposal = &research.TraderProposal{
			Action:         "BUY",
			Reasoning:      fmt.Sprintf("Research plan for %s is Overweight.", ticker),
			EntryPrice:     func(v float64) *float64 { return &v }(195.0),
			StopLoss:       func(v float64) *float64 { return &v }(175.0),
			PositionSizing: "5%",
		}
	// FIX: Critical #5 — split risk debate cases so each view gets its own distinct value
	// instead of all three being assigned the same raw string.
	case "aggressive_risk":
		if state.RiskDebateState == nil {
			state.RiskDebateState = &research.RiskDebateState{}
		}
		state.RiskDebateState.AggressiveView = raw
	case "conservative_risk":
		if state.RiskDebateState == nil {
			state.RiskDebateState = &research.RiskDebateState{}
		}
		state.RiskDebateState.ConservativeView = raw
	case "neutral_risk":
		if state.RiskDebateState == nil {
			state.RiskDebateState = &research.RiskDebateState{}
		}
		state.RiskDebateState.NeutralView = raw
	case "portfolio_manager":
		state.PortfolioDecision = &research.PortfolioDecision{
			Rating:           research.RatingOverweight,
			ExecutiveSummary: fmt.Sprintf("%s shows strong fundamental momentum with manageable risk.", ticker),
			InvestmentThesis: "AI-driven revenue growth justifies premium; buy on weakness.",
			PriceTarget:      func(v float64) *float64 { return &v }(235.0),
			TimeHorizon:      "12 months",
		}
	}
}

// printDemoReport renders the completed research state as a formatted markdown report.
//
// Args:
//   - state: completed ResearchState with all analysis results.
//   - log: logging function for console output.
//   - totalNodes: total number of nodes in the graph for progress display.
func printDemoReport(state *research.ResearchState, log func(string, ...any), totalNodes int) {
	log("═════════════════════════════════════════════════")
	log("  %s Research Report (Research Layer Demo)", state.Symbol)
	log("═════════════════════════════════════════════════\n")

	// Phase 1: Analyst Reports.
	log("  ── Phase 1: Analyst Reports ──\n")
	for _, report := range state.AnalystReports {
		log(research.RenderMarkdownAR(report))
		log("")
	}

	// Phase 2: Debate Summary.
	if state.DebateState != nil && (len(state.DebateState.BullArguments) > 0 || len(state.DebateState.BearArguments) > 0) {
		log("  ── Phase 2: Bull/Bear Debate ──")
		log("  Bull rounds: %d/%d", state.DebateState.Round, state.DebateState.MaxRounds)
		log("  Bear rounds: %d/%d", state.DebateState.Round, state.DebateState.MaxRounds)
		log("")
	}

	// Phase 3: Research Plan.
	if state.ResearchPlan != nil {
		log("  ── Phase 3: Research Plan ──")
		log(research.RenderMarkdown(state.ResearchPlan))
		log("")
	}

	// Phase 4: Trader Proposal.
	if state.TraderProposal != nil {
		log("  ── Phase 4: Trader Proposal ──")
		log(research.RenderMarkdownTP(state.TraderProposal))
		log("")
	}

	// Phase 5: Risk Debate.
	if state.RiskDebateState != nil && state.RiskDebateState.AggressiveView != "" {
		log("  ── Phase 5: Risk Debate ──")
		log("  Risk rounds: %d/%d", state.RiskDebateState.Round, state.RiskDebateState.MaxRounds)
		log("")
	}

	// Phase 6: Portfolio Decision (final).
	if state.PortfolioDecision != nil {
		log("  ── Phase 6: Final Decision ──")
		log(research.RenderMarkdownPD(state.PortfolioDecision))
		log("")
	}

	// Execution summary.
	log("  ── Execution Summary ──")
	log("  Total steps completed: %d/%d", len(state.StepsCompleted), totalNodes)
	for _, step := range state.StepsCompleted {
		log("    ✓ %s", step)
	}
	if state.Error != nil {
		log("\n  ⚠️ Error: %v", state.Error)
	}
	log("")
}
