// Package main — shared research layer helpers used by both main.go (legacy/research
// mode) and research_demo.go (offline demo). This file has no build tag so it is
// always included in the package.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_quant/dataflow"
	"github.com/Timwood0x10/ares/internal/ares_quant/market"
	"github.com/Timwood0x10/ares/internal/ares_quant/research"
	researchagents "github.com/Timwood0x10/ares/internal/ares_quant/research/agents"
)

// createResearchConfig builds a ResearchConfig from default values.
// In production, these would be loaded from config.yaml research section.
//
// Args:
//   - ticker: stock symbol being analyzed (reserved for future config loading).
//
// Returns:
//   - pointer to initialized ResearchConfig.
func createResearchConfig(_ string) *research.ResearchConfig {
	return &research.ResearchConfig{
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
}

// setupDataFlow creates the data flow components: VendorRouter with Yahoo vendor
// and Validator with staleness guards.
//
// Args:
//   - ticker: stock symbol for validation context (reserved for future use).
//
// Returns:
//   - router: configured VendorRouter with Yahoo vendor registered.
//   - validator: configured Validator for data freshness checks.
//   - error if setup fails.
func setupDataFlow(_ string) (*dataflow.VendorRouter, *dataflow.Validator, error) {
	routerCfg := &dataflow.RouterConfig{
		CoreStockAPIs:       []string{"coingecko", "yahoo"},
		TechnicalIndicators: []string{"coingecko", "yahoo"},
		Fundamentals:        []string{"coingecko", "yahoo"},
		News:                []string{"yahoo"},
	}
	router := dataflow.NewVendorRouter(routerCfg)

	yahooVendor := &yahooVendorAdapter{feed: market.NewYahooFeed()}
	router.Register(yahooVendor)

	coingeckoVendor := &coingeckoVendorAdapter{feed: market.NewCoinGeckoFeed()}
	router.Register(coingeckoVendor)

	validator := dataflow.NewValidator(&dataflow.ValidationConfig{
		MaxStaleDuration:   120 * time.Hour,
		HolidayGracePeriod: 72 * time.Hour,
		AnalysisDate:       time.Now(),
		IsBacktestMode:     false,
	})

	return router, validator, nil
}

// printResearchReport renders the final research state as a markdown report
// to stdout using the render package functions.
//
// Args:
//   - state: completed ResearchState containing all analysis results.
//   - log: logging function for console output.
//   - totalNodes: total number of nodes in the research graph (for progress display).
//
// FIX: Minor — totalNodes parameter replaces hardcoded 12.
func printResearchReport(state *research.ResearchState, log func(string, ...any), totalNodes int) {
	log("════════════════════════════════════════════")
	log("  %s Research Report (Research Layer)", state.Symbol)
	log("════════════════════════════════════════════\n")

	for _, report := range state.AnalystReports {
		log(research.RenderMarkdownAR(report))
		log("")
	}

	if state.ResearchPlan != nil {
		log(research.RenderMarkdown(state.ResearchPlan))
		log("")
	}

	if state.TraderProposal != nil {
		log(research.RenderMarkdownTP(state.TraderProposal))
		log("")
	}

	if state.PortfolioDecision != nil {
		log("  ─── Final Investment Decision (Portfolio Manager) ───")
		log(research.RenderMarkdownPD(state.PortfolioDecision))
		log("")
	}

	log("  Steps completed (%d/%d):", len(state.StepsCompleted), totalNodes)
	for _, step := range state.StepsCompleted {
		log("    ✓ %s", step)
	}

	if state.Error != nil {
		log("\n  ⚠️ Execution error: %v", state.Error)
	}
	log("")
}

// ─── Raw LLM Response Structs ──────────────────────────────

// researchPlanRaw matches the Research Manager prompt's output JSON schema.
type researchPlanRaw struct {
	Recommendation  string `json:"recommendation"`
	Rationale       string `json:"rationale"`
	StrategicAction string `json:"strategic_action"`
}

// traderProposalRaw matches the Trader prompt's output JSON schema.
type traderProposalRaw struct {
	Action         string   `json:"action"`
	Reasoning      string   `json:"reasoning"`
	EntryPrice     *float64 `json:"entry_price,omitempty"`
	StopLoss       *float64 `json:"stop_loss,omitempty"`
	PositionSizing string   `json:"position_sizing,omitempty"`
}

// portfolioDecisionRaw matches the Portfolio Manager prompt's output JSON schema.
type portfolioDecisionRaw struct {
	Rating           string   `json:"rating"`
	ExecutiveSummary string   `json:"executive_summary"`
	InvestmentThesis string   `json:"investment_thesis"`
	PriceTarget      *float64 `json:"price_target,omitempty"`
	TimeHorizon      string   `json:"time_horizon,omitempty"`
}

// updateStateFromResponse parses a raw LLM response string and updates
// the research state with typed results depending on which node produced it.
//
// Args:
//   - state: mutable research state to update.
//   - nodeID: identifier of the producing node.
//   - nodeName: human-readable name for the report.
//   - raw: raw JSON response string from LLM/mock.
//   - ticker: stock symbol for report metadata.
func updateStateFromResponse(state *research.ResearchState, nodeID string, nodeName string, raw string, ticker string) {
	switch nodeID {
	case "market_analyst", "sentiment_analyst", "news_analyst", "fundamentals_analyst":
		state.AnalystReports[nodeID] = &research.AnalystReport{
			AnalystName: nodeName,
			AnalystType: strings.TrimSuffix(nodeID, "_analyst"),
			Score:       65.0,
			Verdict:     "completed",
			RawOutput:   raw,
			Timestamp:   time.Now(),
			Confidence:  0.7,
		}
	// FIX: Major — add missing debate/researcher/risk node handlers.
	case "bull_researcher", "bear_researcher":
		if state.DebateState == nil {
			state.DebateState = &research.InvestDebateState{}
		}
		if nodeID == "bull_researcher" {
			state.DebateState.BullArguments = append(state.DebateState.BullArguments, raw)
		} else {
			state.DebateState.BearArguments = append(state.DebateState.BearArguments, raw)
		}
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
	case "research_manager":
		parser := researchagents.NewJSONParser[researchPlanRaw]()
		if parsed, err := parser.Parse(raw); err == nil {
			rating, _ := research.ParsePortfolioRating(parsed.Recommendation)
			state.ResearchPlan = &research.ResearchPlan{
				Recommendation:  rating,
				Rationale:       parsed.Rationale,
				StrategicAction: parsed.StrategicAction,
			}
		} else {
			// Fallback: try markdown parser.
			mdParser := researchagents.NewMarkdownParser()
			plan, mdErr := mdParser.ParseResearchPlan(raw)
			if mdErr == nil && plan != nil {
				state.ResearchPlan = plan
			} else {
				state.ResearchPlan = &research.ResearchPlan{
					Recommendation:  research.RatingHold,
					Rationale:       "Balanced analyst views with moderate risk.",
					StrategicAction: "Maintain position; monitor quarterly results.",
				}
			}
		}
	case "trader":
		traderParser := researchagents.NewJSONParser[traderProposalRaw]()
		if parsed, err := traderParser.Parse(raw); err == nil {
			var entryPrice, stopLoss *float64
			if parsed.EntryPrice != nil && *parsed.EntryPrice > 0 {
				entryPrice = parsed.EntryPrice
			}
			if parsed.StopLoss != nil && *parsed.StopLoss > 0 {
				stopLoss = parsed.StopLoss
			}
			state.TraderProposal = &research.TraderProposal{
				Action:         parsed.Action,
				Reasoning:      parsed.Reasoning,
				EntryPrice:     entryPrice,
				StopLoss:       stopLoss,
				PositionSizing: parsed.PositionSizing,
			}
		} else {
			// Fallback.
			state.TraderProposal = &research.TraderProposal{
				Action:         "HOLD",
				Reasoning:      fmt.Sprintf("Research plan for %s indicates hold.", ticker),
				PositionSizing: "maintain",
			}
		}
	case "portfolio_manager":
		pmParser := researchagents.NewJSONParser[portfolioDecisionRaw]()
		if parsed, err := pmParser.Parse(raw); err == nil {
			rating, _ := research.ParsePortfolioRating(parsed.Rating)
			var priceTarget *float64
			if parsed.PriceTarget != nil && *parsed.PriceTarget > 0 {
				priceTarget = parsed.PriceTarget
			}
			state.PortfolioDecision = &research.PortfolioDecision{
				Rating:           rating,
				ExecutiveSummary: parsed.ExecutiveSummary,
				InvestmentThesis: parsed.InvestmentThesis,
				PriceTarget:      priceTarget,
				TimeHorizon:      parsed.TimeHorizon,
			}
		} else {
			state.PortfolioDecision = &research.PortfolioDecision{
				Rating:           research.RatingHold,
				ExecutiveSummary: "Mixed signals favor maintaining current allocation.",
				InvestmentThesis: "Solid fundamentals offset by premium valuation.",
				PriceTarget:      func(v float64) *float64 { return &v }(195.0),
				TimeHorizon:      "6-12 months",
			}
		}
	}
}

// ReporterFunc is the signature for a custom report rendering function.
// If nil, executeResearchGraph uses the default printResearchReport.
type ReporterFunc func(state *research.ResearchState, log func(string, ...any), totalNodes int)

// FIX: Minor #10 — extract shared execute+report+save pipeline so both
// runWithResearchLayer and runResearchDemo reuse core execution logic.
// executeResearchGraph runs the research graph, prints a report, and saves JSON output.
//
// Args:
//   - ctx: context for cancellation.
//   - graph: fully built and wired research graph.
//   - state: pre-initialized research state (config, snapshot already set).
//   - ticker: stock symbol for file naming.
//   - outDir: directory for JSON output (empty = skip saving).
//   - log: logging function for console output.
//   - reporter: optional custom report renderer (nil = use default printResearchReport).
//
// Returns:
//   - error if graph execution or serialization fails.
//
//lint:ignore U1000 used by research_demo.go (researchdemo build tag)
func executeResearchGraph(ctx context.Context, graph *research.ResearchGraph,
	state *research.ResearchState, ticker string, outDir string,
	log func(string, ...any), reporter ReporterFunc) error {
	return executeResearchGraphWithDate(ctx, graph, state, ticker, outDir, log, reporter, "")
}

func executeResearchGraphWithDate(ctx context.Context, graph *research.ResearchGraph,
	state *research.ResearchState, ticker string, outDir string,
	log func(string, ...any), reporter ReporterFunc, analysisDate string) error {
	totalNodes := len(graph.Order())
	log("\n═══ Research graph execution (%d nodes) ═══\n", totalNodes)

	if err := graph.Execute(ctx, state); err != nil {
		return fmt.Errorf("execute research graph: %w", err)
	}

	if reporter != nil {
		reporter(state, log, totalNodes)
	} else {
		printResearchReport(state, log, totalNodes)
	}

	if outDir != "" {
		if err := os.MkdirAll(outDir, 0750); err != nil {
			return fmt.Errorf("create output dir: %w", err)
		}
		data, err := state.ToJSON()
		if err != nil {
			return fmt.Errorf("serialize state: %w", err)
		}
		dateStr := analysisDate
		if dateStr == "" {
			dateStr = time.Now().Format("20060102")
		}
		outPath := filepath.Join(outDir, fmt.Sprintf("%s_%s_research.json",
			ticker, dateStr))
		if err := os.WriteFile(outPath, data, 0600); err != nil {
			return fmt.Errorf("save result: %w", err)
		}
		log("  Research results saved: %s", outPath)
	}

	return nil
}

// ─── Vendor Adapter ────────────────────────────────────────

// yahooVendorAdapter wraps market.YahooFeed to satisfy the dataflow.Vendor interface.
type yahooVendorAdapter struct {
	feed *market.YahooFeed
}

// Name returns the vendor identifier.
func (a *yahooVendorAdapter) Name() string { return "yahoo" }

// Candles fetches historical OHLCV data via Yahoo feed.
func (a *yahooVendorAdapter) Candles(ctx context.Context, symbol string, days int) ([]market.Candle, error) {
	end := time.Now()
	start := end.AddDate(0, 0, -days)
	ts, err := a.feed.Candles(symbol, start, end, market.Res1d)
	if err != nil {
		return nil, err
	}
	return ts.Bars, nil
}

// Quote fetches the latest quote via Yahoo feed.
func (a *yahooVendorAdapter) Quote(ctx context.Context, symbol string) (*market.Quote, error) {
	q, err := a.feed.Quote(symbol)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// Available reports whether the Yahoo vendor is operational.
func (a *yahooVendorAdapter) Available() bool { return true }

// coingeckoVendorAdapter wraps market.CoinGeckoFeed to satisfy dataflow.Vendor.
type coingeckoVendorAdapter struct {
	feed *market.CoinGeckoFeed
}

// Name returns the vendor identifier.
func (a *coingeckoVendorAdapter) Name() string { return "coingecko" }

// Candles fetches historical OHLCV data via CoinGecko feed.
func (a *coingeckoVendorAdapter) Candles(ctx context.Context, symbol string, days int) ([]market.Candle, error) {
	end := time.Now()
	start := end.AddDate(0, 0, -days)
	ts, err := a.feed.Candles(symbol, start, end, market.Res1d)
	if err != nil {
		return nil, err
	}
	return ts.Bars, nil
}

// Quote fetches the latest quote via CoinGecko feed.
func (a *coingeckoVendorAdapter) Quote(ctx context.Context, symbol string) (*market.Quote, error) {
	q, err := a.feed.Quote(symbol)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// Available reports whether the CoinGecko vendor is operational.
func (a *coingeckoVendorAdapter) Available() bool { return true }
