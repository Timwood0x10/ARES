//go:build !researchdemo

// Package main — ares Quantitative Trading Demo.
// Supports two execution modes:
//
//   - Legacy mode (default): 8 agents via Orchestrator, YAML-driven pipeline.
//   - Research layer mode (--use-research-layer): structured 12-node research graph
//     with typed schemas, data validation, and markdown reporting.
//
// Usage:
//
//	go run . -ticker AAPL
//	go run . -ticker AAPL -model deepseek-v4-flash
//	go run . -ticker AAPL --use-research-layer
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/examples/quant-trading/agents"
	apiimpl "github.com/Timwood0x10/ares/internal/api_impl"
	"github.com/Timwood0x10/ares/internal/ares_quant"
	"github.com/Timwood0x10/ares/internal/ares_quant/dataflow"
	"github.com/Timwood0x10/ares/internal/ares_quant/market"
	marketmakingapi "github.com/Timwood0x10/ares/internal/ares_quant/marketmaking_api"
	"github.com/Timwood0x10/ares/internal/ares_quant/research"
	researchagents "github.com/Timwood0x10/ares/internal/ares_quant/research/agents"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"

	"gopkg.in/yaml.v3"
)

type AnalysisResult struct {
	Ticker     string        `json:"ticker"`
	Model      string        `json:"model"`
	AnalyzedAt string        `json:"analyzed_at"`
	Bars       int           `json:"bars"`
	Agents     []AgentOutput `json:"agents"`
}

type AgentOutput struct {
	YamlID   string `json:"yaml_id"`
	Name     string `json:"name"`
	Phase    int    `json:"phase"`
	Status   string `json:"status"`
	Duration string `json:"duration,omitempty"`
	Analysis string `json:"analysis"`
	Error    string `json:"error,omitempty"`
}

func main() {
	var cfgPath, agentsPath, modelName, dataDir, outDir, ticker string
	var days int
	var useResearchLayer, simulate bool
	var mode, date, dataVendor, outputLang string
	var capital float64
	var symbols string
	var from, to, strategy string

	flag.StringVar(&cfgPath, "config", "./examples/quant-trading/config.yml", "")
	flag.StringVar(&agentsPath, "agents", "./examples/quant-trading/config/agents.yaml", "")
	flag.StringVar(&modelName, "model", "", "LLM model name")
	flag.StringVar(&dataDir, "data", "./examples/quant-trading/data", "Market data CSV directory")
	flag.StringVar(&outDir, "out", "./examples/quant-trading/results", "Output directory for results")
	flag.StringVar(&ticker, "ticker", "AAPL", "Stock ticker symbol")
	flag.IntVar(&days, "days", 365, "Number of historical data days")
	flag.BoolVar(&useResearchLayer, "use-research-layer", false, "Enable new research layer (12-node structured research graph)")
	flag.BoolVar(&simulate, "simulate", false, "Run investment simulation (backtest) after analysis")

	var signalsPath string

	flag.StringVar(&mode, "mode", "analyze", "Execution mode: analyze or backtest")
	flag.StringVar(&date, "date", "", "Analysis date (YYYY-MM-DD)")
	flag.StringVar(&dataVendor, "data-vendor", "", "Data source vendor (yahoo, csv)")
	flag.StringVar(&outputLang, "output-language", "", "Output language (en, zh, ja)")
	flag.Float64Var(&capital, "capital", 100_000.0, "Initial capital for backtest")
	flag.StringVar(&symbols, "symbols", "", "Comma-separated symbols for backtest")
	flag.StringVar(&from, "from", "", "Backtest start date (YYYY-MM-DD)")
	flag.StringVar(&to, "to", "", "Backtest end date (YYYY-MM-DD)")
	flag.StringVar(&strategy, "strategy", "buy_hold", "Backtest strategy: buy_hold, research_signal, csv_signal")
	flag.StringVar(&signalsPath, "signals", "", "Path to signals CSV file")
	flag.Parse()

	ticker = strings.ToUpper(strings.TrimSpace(ticker))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if mode == "backtest" {
		if err := runBacktestMode(ctx, ticker, dataDir, outDir, capital, symbols, from, to, strategy, dataVendor, date, signalsPath); err != nil {
			slog.Error("backtest execution failed", "err", err)
			os.Exit(1)
		}
		return
	}

	if useResearchLayer {
		if err := runWithResearchLayer(ctx, ticker, outDir, dataDir, simulate, date, dataVendor, outputLang); err != nil {
			slog.Error("research layer execution failed", "err", err)
			os.Exit(1)
		}
		return
	}

	if simulate && !useResearchLayer {
		slog.Warn("--simulate flag is only supported in research layer mode. Use --use-research-layer to enable simulation.")
	}

	if err := runLegacyPipeline(ctx, cfgPath, agentsPath, modelName, ticker, dataDir, outDir, days); err != nil {
		slog.Error("legacy pipeline execution failed", "err", err)
		os.Exit(1)
	}
}

// ─── Legacy Pipeline Mode ──────────────────────────────────

// FIX: Major #11 — extracted from main() to keep main() under 30 lines.
// runLegacyPipeline runs the 8-agent Orchestrator-driven analysis pipeline.
//
// Args:
//   - ctx: context for cancellation.
//   - cfgPath: path to service config YAML.
//   - agentsPath: path to agent config YAML.
//   - modelName: optional LLM model override (empty = use config default).
//   - ticker: stock symbol (uppercased).
//   - dataDir: directory for CSV market data.
//   - outDir: directory for result JSON output.
//   - days: number of historical days to fetch.
//
// Returns:
//   - error if any critical step fails.
func runLegacyPipeline(ctx context.Context, cfgPath string, agentsPath string,
	modelName string, ticker string, dataDir string, outDir string, days int) error {

	// Try local config.yml first, fall back to config.example.yml.
	if _, statErr := os.Stat(cfgPath); statErr != nil {
		alt := cfgPath[:len(cfgPath)-4] + ".example.yml"
		cfgPath = alt
	}
	cfg, err := apiimpl.LoadServiceConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if modelName != "" {
		cfg.LLM.Model = modelName
	}

	svc, err := apiimpl.StartService(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	defer svc.Wait()

	reg := core.NewRegistry()
	if err := ares_quant.RegisterTools(reg); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}

	agentCfg, err := agents.LoadConfig(agentsPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}

	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	log("\n╔════════════════════════════════════════════╗")
	log("║  ares Quantitative Analysis               ║")
	log("╚════════════════════════════════════════════╝")
	log("  Ticker: %s", ticker)
	log("  Model: %s (%s)", cfg.LLM.Model, cfg.LLM.Provider)

	// ── Download market data ──
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	feed := market.NewYahooFeed()
	end := time.Now()
	start := end.AddDate(0, 0, -days)

	ts, err := feed.Candles(ticker, start, end, market.Res1d)
	if err != nil {
		return fmt.Errorf("fetch candles: %w", err)
	}

	// FIX: Minor — check CSV write/flush/close errors instead of discarding with _.
	csvPath := filepath.Join(dataDir, ticker+".csv")
	f, err := os.Create(csvPath)
	if err != nil {
		return fmt.Errorf("create CSV: %w", err)
	}
	w := csv.NewWriter(f)

	if err := w.Write([]string{"date", "open", "high", "low", "close", "volume"}); err != nil {
		slog.Error("CSV header write failed", "err", err)
	}
	for _, bar := range ts.Bars {
		if err := w.Write([]string{
			bar.Date.Format("2006-01-02"),
			fmt.Sprintf("%.2f", bar.Open), fmt.Sprintf("%.2f", bar.High),
			fmt.Sprintf("%.2f", bar.Low), fmt.Sprintf("%.2f", bar.Close),
			fmt.Sprintf("%d", bar.Volume),
		}); err != nil {
			slog.Error("CSV row write failed", "err", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		slog.Error("CSV flush error", "err", err)
	}
	if err := f.Close(); err != nil {
		slog.Error("CSV file close error", "err", err)
	}
	log("  ✓ Market data: %s (%d bars)", csvPath, len(ts.Bars))

	// ── Execute pipeline ──
	log("\n═══ Starting Analysis ═══\n")

	orch := svc.Orchestrator()

	yamlToOrch := agents.RunPipeline(orch, agentCfg, ticker)

	phaseMap := make(map[string]int)
	groups := agentCfg.PhaseGroups()
	for phase, group := range groups {
		for _, a := range group {
			phaseMap[a.ID] = phase
		}
	}

	var outputs []AgentOutput
	for _, yamlID := range agentCfg.Order() {
		orchID, hasMapping := yamlToOrch[yamlID]
		out := AgentOutput{
			YamlID: yamlID,
			Name:   agentCfg.AgentNameByID(yamlID),
			Phase:  phaseMap[yamlID],
		}
		if hasMapping {
			if ag, ok := orch.GetAgent(orchID); ok {
				out.Status = ag.Status
				out.Duration = ag.Duration
				out.Analysis = ag.Analysis
				out.Error = ag.Error
			}
		} else {
			out.Status = "not_created"
		}
		outputs = append(outputs, out)
	}

	// ── Print results ──
	log("════════════════════════════════════════════")
	log("  %s Analysis Report", ticker)
	log("════════════════════════════════════════════\n")

	for _, out := range outputs {
		icon := "✅"
		if out.Status != "completed" {
			icon = "❌"
		}
		log("  %s [Phase %d] %s (%s)", icon, out.Phase, out.Name, out.Duration)

		if out.Analysis != "" {
			formatted := agents.FormatOutput(out.Analysis)
			lines := strings.Split(formatted, "\n")
			for j, line := range lines {
				if j >= 8 {
					log("     ... (%d lines total)", len(lines))
					break
				}
				line = strings.TrimSpace(line)
				if line != "" {
					if len(line) > 100 {
						line = line[:100] + "..."
					}
					log("     %s", line)
				}
			}
		} else if out.Status == "completed" {
			log("     (analysis output is empty)")
		}
		if out.Error != "" {
			log("     ❌ %s", out.Error)
		}
		log("")
	}

	for _, out := range outputs {
		if out.YamlID == "pm" && out.Analysis != "" {
			log("  ─── Final Trading Signal (Portfolio Manager) ───")
			formatted := agents.FormatOutput(out.Analysis)
			for _, line := range strings.Split(formatted, "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					log("    %s", line)
				}
			}
			log("")
		}
	}

	// FIX: Minor — check json.MarshalIndent and os.WriteFile errors.
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		slog.Error("create output dir failed", "err", err)
		return nil // non-fatal: analysis already completed
	}
	result := AnalysisResult{
		Ticker:     ticker,
		Model:      cfg.LLM.Model,
		AnalyzedAt: time.Now().Format(time.RFC3339),
		Bars:       len(ts.Bars),
		Agents:     outputs,
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		slog.Error("marshal result JSON failed", "err", err)
		return nil
	}
	outPath := filepath.Join(outDir, fmt.Sprintf("%s_%s.json", ticker, time.Now().Format("20060102_150405")))
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		slog.Error("save result file failed", "path", outPath, "err", err)
		return nil
	}
	log("  📄 Full analysis results saved: %s", outPath)

	log("")
	return nil
}

// ─── Research Layer Mode ───────────────────────────────────

// runWithResearchLayer executes the full research pipeline using the new
// structured research layer. It creates config, data flow, graph, and runs
// the 12-node research execution graph. If simulate is true, it also runs
// the investment simulator on the research output.
//
// Args:
//   - ctx: context for cancellation.
//   - ticker: stock symbol to analyze (uppercased).
//   - outDir: directory for output files.
//   - dataDir: directory containing CSV market data.
//   - simulate: if true, run backtest simulation after research completes.
//
// Returns:
//   - error if any step of the pipeline fails.
func runWithResearchLayer(ctx context.Context, ticker string, outDir string, dataDir string, simulate bool, analysisDate string, dataVendor string, outputLang string) error {
	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	log("\n╔══════════════════════════════════════════════╗")
	log("║  ares Research Layer (12-Node Graph)    ║")
	log("╚══════════════════════════════════════════════╝")
	log("  Ticker: %s", ticker)
	if analysisDate != "" {
		log("  Date:   %s", analysisDate)
	}
	if dataVendor != "" {
		log("  Vendor: %s", dataVendor)
	}
	if outputLang != "" {
		log("  Lang:   %s", outputLang)
	}

	cfg := createResearchConfig(ticker)
	if dataVendor != "" {
		cfg.DataVendors = []string{dataVendor}
	}
	if outputLang != "" {
		cfg.OutputLanguage = outputLang
	}

	router, validator, err := setupDataFlow(ticker)
	if err != nil {
		return fmt.Errorf("setup data flow: %w", err)
	}

	snapshotBuilder := dataflow.NewSnapshotBuilder(router, validator)
	snapshot, err := snapshotBuilder.Build(ctx, ticker, time.Now())
	if err != nil {
		log("  ⚠️ Snapshot build failed (continuing with empty snapshot): %v", err)
		snapshot = nil
	} else {
		log("  ✓ Market snapshot: latest data %s", snapshot.LatestRowDate.Format("2006-01-02"))
	}

	state := research.NewResearchState(ticker, time.Now(), cfg)
	if snapshot != nil {
		state.MarketSnapshot = &research.VerifiedMarketSnapshot{
			RequestedDate: snapshot.RequestedDate,
			LatestRowDate: snapshot.LatestRowDate,
			OHLCV:         snapshot.OHLCV,
			Indicators:    snapshot.Indicators,
			RecentCloses:  snapshot.RecentCloses,
			Warning:       snapshot.Warning,
		}
	} else {
		// FIX: Minor — mark state when snapshot unavailable so downstream nodes
		// and reports can indicate "data unavailable" instead of producing misleading results.
		state.StepsCompleted = append(state.StepsCompleted, "market_data_unavailable")
	}

	graphCfg := &research.GraphConfig{
		EnabledAnalysts:  cfg.SelectedAnalysts,
		MaxDebateRounds:  cfg.MaxDebateRounds,
		MaxRiskRounds:    cfg.MaxRiskRounds,
		EnableCheckpoint: cfg.CheckpointEnabled,
		EnableMemory:     cfg.MemoryEnabled,
	}
	builder := research.NewGraphBuilder(graphCfg)
	graph, err := builder.Build()
	if err != nil {
		return fmt.Errorf("build research graph: %w", err)
	}

	// Create LLM executor: use real LLM if config available, fallback to mock.
	cfgPath := resolveConfigPath()
	llmExec, err := newLLMExecutor(cfgPath)
	if err != nil {
		log("  ⚠️ LLM executor init failed (using mock): %v", err)
		llmExec = researchagents.NewMockLLMExecutor()
		setupMockResponses(llmExec.(*researchagents.MockLLMExecutor), ticker)
	}
	wireGraphHandlers(graph, llmExec, ticker)

	// Load historical memory context for the Portfolio Manager prompt.
	memStore, memErr := research.EnsureMemoryStore("")
	if memErr != nil {
		log("  ⚠️ Memory store init failed (continuing without memory): %v", memErr)
	} else {
		memLog := research.NewMemoryLog(memStore)
		research.PopulateMemoryContext(ctx, memLog, state)
		if state.MemoryContext != nil && len(state.MemoryContext.PastDecisions) > 0 {
			log("  ✓ Historical memory loaded (%d past decisions)", len(state.MemoryContext.PastDecisions))
		}
		defer func() {
			research.SaveDecisionToMemory(ctx, memLog, state)
			if err := memStore.Close(); err != nil {
				slog.Warn("memory store close", "err", err)
			}
		}()
	}

	// FIX: Minor #10 — use shared executeResearchGraph for core pipeline.
	if err := executeResearchGraphWithDate(ctx, graph, state, ticker, outDir, log, nil, analysisDate); err != nil {
		return err
	}

	// Run investment simulation if requested.
	if simulate {
		runSimulation(ctx, ticker, dataDir, outDir, state, log)
	}

	return nil
}

// ─── Backtest Mode ─────────────────────────────────────────

// runBacktestMode executes a standalone backtest using the portfolio simulator
// and writes standard output files (summary.json, equity_curve.csv, trades.csv,
// config.json) to the results directory.
func runBacktestMode(ctx context.Context, ticker string, dataDir string, outDir string,
	capital float64, symbols string, from string, to string, strategy string,
	dataVendor string, date string, signalsPath string) error {

	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	symList := []string{ticker}
	if symbols != "" {
		symList = strings.Split(symbols, ",")
		for i := range symList {
			symList[i] = strings.ToUpper(strings.TrimSpace(symList[i]))
		}
	}

	var startTime, endTime time.Time
	if from != "" {
		t, err := time.Parse("2006-01-02", from)
		if err != nil {
			return fmt.Errorf("parse --from date: %w", err)
		}
		startTime = t
	} else {
		startTime = time.Now().AddDate(0, 0, -365)
	}
	if to != "" {
		t, err := time.Parse("2006-01-02", to)
		if err != nil {
			return fmt.Errorf("parse --to date: %w", err)
		}
		endTime = t
	} else {
		endTime = time.Now()
	}

	log("\n═══ Backtest Mode ═══")
	log("  Symbols: %v", symList)
	log("  Period:  %s to %s", startTime.Format("2006-01-02"), endTime.Format("2006-01-02"))
	log("  Capital: $%.2f", capital)
	log("  Strategy: %s", strategy)

	runner := marketmakingapi.NewDefaultBacktestRunnerWithDataDir(dataDir)

	req := &marketmakingapi.BacktestRequest{
		Symbols:        symList,
		StartTime:      startTime,
		EndTime:        endTime,
		InitialCapital: capital,
		Strategy:       strategy,
		DataDir:        dataDir,
	}
	if dataVendor != "" {
		req.DataSource = dataVendor
	}
	if date != "" {
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil {
			return fmt.Errorf("parse --date: %w", err)
		}
		req.StartTime = parsed
	}

	resp, err := runner.Run(ctx, req)
	if err != nil {
		return fmt.Errorf("backtest failed: %w", err)
	}

	log("════════════════════════════════════════════")
	log("  Backtest Results")
	log("════════════════════════════════════════════")
	log("  Final Equity:  $%.2f", resp.TotalPnL+capital)
	log("  Total P&L:     $%.2f", resp.TotalPnL)
	log("  Total Return:  %.2f%%", resp.TotalReturn)
	log("  Sharpe Ratio:  %.2f", resp.SharpeRatio)
	log("  Max Drawdown:  %.2f%%", resp.MaxDrawdown*100)
	log("  Win Rate:      %.1f%%", resp.WinRate*100)
	log("  Total Trades:  %d", resp.TotalTrades)
	log("  Summary:       %s", resp.Summary)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if err := writeBacktestSummary(outDir, resp); err != nil {
		log("  ⚠️ Failed to write summary: %v", err)
	}
	if err := writeEquityCurveCSV(outDir, resp.EquityCurve); err != nil {
		log("  ⚠️ Failed to write equity curve: %v", err)
	}
	if err := writeTradesCSV(outDir, resp.TradeLog); err != nil {
		log("  ⚠️ Failed to write trades: %v", err)
	}
	if err := writeBacktestConfig(outDir, req); err != nil {
		log("  ⚠️ Failed to write config: %v", err)
	}

	log("  Results saved to: %s", outDir)
	log("")
	return nil
}

// writeBacktestSummary writes summary.json with the backtest results.
func writeBacktestSummary(outDir string, resp *marketmakingapi.BacktestResponse) error {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "summary.json"), data, 0o644)
}

// writeEquityCurveCSV writes equity_curve.csv from the equity curve.
func writeEquityCurveCSV(outDir string, curve []marketmakingapi.EquityPoint) error {
	f, err := os.Create(filepath.Join(outDir, "equity_curve.csv"))
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("equity curve file close error", "err", err)
		}
	}()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	if err := writer.Write([]string{"time", "equity", "cash", "exposure", "drawdown"}); err != nil {
		return err
	}
	for _, ep := range curve {
		if err := writer.Write([]string{
			ep.Time.Format(time.RFC3339),
			fmt.Sprintf("%.2f", ep.Equity),
			fmt.Sprintf("%.2f", ep.Cash),
			fmt.Sprintf("%.2f", ep.Exposure),
			fmt.Sprintf("%.6f", ep.Drawdown),
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeTradesCSV writes trades.csv from the trade log.
func writeTradesCSV(outDir string, trades []marketmakingapi.TradeRecord) error {
	f, err := os.Create(filepath.Join(outDir, "trades.csv"))
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("trades file close error", "err", err)
		}
	}()

	writer := csv.NewWriter(f)
	defer writer.Flush()

	if err := writer.Write([]string{"id", "symbol", "side", "price", "quantity", "timestamp", "pnl"}); err != nil {
		return err
	}
	for _, tr := range trades {
		pnlStr := ""
		if tr.PnL != 0 {
			pnlStr = fmt.Sprintf("%.2f", tr.PnL)
		}
		if err := writer.Write([]string{
			tr.ID, tr.Symbol, tr.Side,
			fmt.Sprintf("%.2f", tr.Price),
			fmt.Sprintf("%.4f", tr.Quantity),
			tr.Timestamp.Format(time.RFC3339),
			pnlStr,
		}); err != nil {
			return err
		}
	}
	return nil
}

// writeBacktestConfig writes config.json with the backtest configuration.
func writeBacktestConfig(outDir string, req *marketmakingapi.BacktestRequest) error {
	data, err := json.MarshalIndent(req, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, "config.json"), data, 0o644)
}

// saveSimulationJSON writes a BacktestResponse to a JSON file for simulation results.
func saveSimulationJSON(resp *marketmakingapi.BacktestResponse, outPath string) error {
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal simulation result: %w", err)
	}
	return os.WriteFile(outPath, data, 0o644)
}

// runSimulation executes the investment backtest using the public marketmaking API.
// It converts the research layer's PortfolioDecision into trade signals, then
// delegates to BacktestRunner for execution and reporting.
func runSimulation(ctx context.Context, ticker string, dataDir string,
	outDir string, state *research.ResearchState, log func(string, ...any)) {

	log("\n═══ Investment Simulation (Backtest) ═══\n")

	signals := GenerateSignalsFromResearch(state.PortfolioDecision)
	if len(signals) == 0 {
		log("  No signals generated; skipping simulation.")
		return
	}
	log("  Generated %d signal(s) from research decision:", len(signals))
	for _, sig := range signals {
		log("    [%s] %s — %s (confidence: %.1f%%)",
			sig.Date.Format("2006-01-02"), sig.Action, sig.Reason, sig.Confidence*100)
	}

	// Convert research signals to public API TradeSignal type.
	apiSignals := make([]marketmakingapi.TradeSignal, len(signals))
	for i, s := range signals {
		apiSignals[i] = marketmakingapi.TradeSignal{
			Date:       s.Date,
			Action:     s.Action,
			Reason:     s.Reason,
			Confidence: s.Confidence,
		}
	}

	runner := marketmakingapi.NewDefaultBacktestRunnerWithDataDir(dataDir)
	req := &marketmakingapi.BacktestRequest{
		Symbols:        []string{ticker},
		InitialCapital: 100_000.0,
		PositionSize:   0.10,
		Commission:     0.001,
		Strategy:       "research_signal",
		DataDir:        dataDir,
		Signals:        apiSignals,
	}

	resp, err := runner.Run(ctx, req)
	if err != nil {
		log("  Simulation failed: %v", err)
		return
	}

	// Print simulation report from public API response.
	log("════════════════════════════════════════════")
	log("  Simulation Report for %s", ticker)
	log("════════════════════════════════════════════")
	log("  Initial Capital: $%.2f", req.InitialCapital)
	log("  Final Equity:    $%.2f", resp.TotalPnL+req.InitialCapital)
	log("  Total P&L:       $%.2f", resp.TotalPnL)
	log("  Total Return:    %.2f%%", resp.TotalReturn*100)
	log("  Sharpe Ratio:    %.2f", resp.SharpeRatio)
	log("  Max Drawdown:    %.2f%%", resp.MaxDrawdown*100)
	log("  Win Rate:        %.1f%%", resp.WinRate*100)
	log("  Total Trades:    %d", resp.TotalTrades)
	log("  Winning Trades:  %d", resp.WinningTrades)
	log("  Losing Trades:   %d", resp.LosingTrades)
	log("  Equity Points:   %d", len(resp.EquityCurve))
	log("")
	log("  Summary: %s", resp.Summary)

	// Save results to JSON via standard backtest output helpers.
	if err := writeBacktestSummary(outDir, resp); err != nil {
		log("  Failed to write summary: %v", err)
	} else {
		simOutPath := filepath.Join(outDir, fmt.Sprintf("%s_simulation_%s.json",
			ticker, time.Now().Format("20060102_150405")))
		if err := saveSimulationJSON(resp, simOutPath); err != nil {
			log("  Failed to save simulation results: %v", err)
		} else {
			log("  Simulation results saved: %s", simOutPath)
		}
	}
	log("")
}

// setupMockResponses pre-configures a MockLLMExecutor with realistic responses
// for each node in the research graph.
func setupMockResponses(exec *researchagents.MockLLMExecutor, ticker string) {
	exec.SetDefaultResponse(fmt.Sprintf(`{
  "action": "HOLD",
  "reasoning": "Based on mixed signals from analysts for %s.",
  "entry_price": null,
  "stop_loss": null,
  "position_sizing": "maintain"
}`, ticker))

	exec.SetResponse("Market Analyst", fmt.Sprintf(`{
  "ticker": "%s",
  "score": 6.5,
  "trend": "sideways",
  "rsi_state": "neutral",
  "macd_signal": "neutral",
  "verdict": "neutral",
  "reasoning": "Price consolidating near key support level."
}`, ticker))

	exec.SetResponse("Sentiment Analyst", `{
  "band": "neutral",
  "score": 0.52,
  "confidence": 0.65,
  "signals": ["stable_put_call_ratio", "moderate_social_sentiment"]
}`)

	exec.SetResponse("News Analyst", `{
  "sentiment_score": 0.48,
  "key_headlines": ["Q3 earnings beat expectations", "New product launch announced"],
  "overall_tone": "slightly_positive"
}`)

	exec.SetResponse("Fundamentals Analyst", `{
  "pe_ratio": 28.5,
  "peg_ratio": 1.2,
  "revenue_growth": 0.08,
  "debt_to_equity": 1.5,
  "verdict": "fairly_valued",
  "score": 6.0
}`)

	exec.SetResponse("Bull Researcher", `{
  "thesis": "Strong product pipeline and brand moat support upside.",
  "price_target": 220.0,
  "confidence": 0.7
}`)

	exec.SetResponse("Bear Researcher", `{
  "thesis": "Valuation stretched; margin pressure from competition.",
  "price_target": 160.0,
  "confidence": 0.6
}`)

	exec.SetResponse("Research Manager", `{
  "recommendation": "Hold",
  "rationale": "Balanced view: strong fundamentals offset by valuation concerns.",
  "strategic_action": "Maintain current position; monitor Q4 guidance."
}`)

	exec.SetResponse("Trader", fmt.Sprintf(`{
  "action": "HOLD",
  "reasoning": "Research plan suggests holding %s pending clearer signals.",
  "entry_price": null,
  "stop_loss": null,
  "position_sizing": "maintain"
}`, ticker))

	exec.SetResponse("Aggressive Risk Analyst", `{
  "view": "bullish",
  "max_position_size": "10%",
  "leverage_ok": true
}`)

	exec.SetResponse("Conservative Risk Analyst", `{
  "view": "cautious",
  "max_position_size": "3%",
  "leverage_ok": false
}`)

	exec.SetResponse("Neutral Risk Analyst", `{
  "view": "balanced",
  "max_position_size": "5%",
  "leverage_ok": false
}`)

	exec.SetResponse("Portfolio Manager", `{
  "rating": "Hold",
  "executive_summary": "Mixed signals suggest maintaining current allocation.",
  "investment_thesis": "Strong fundamentals with elevated valuation; wait for better entry.",
  "price_target": 195.0,
  "time_horizon": "6-12 months"
}`)
}

// wireGraphHandlers attaches handler functions to all nodes in the graph.
func wireGraphHandlers(graph *research.ResearchGraph, exec researchagents.LLMExecutor, ticker string) {
	for _, nodeID := range graph.Order() {
		node := graph.Nodes()[nodeID]
		if node == nil {
			continue
		}
		node.Handler = makeNodeHandler(nodeID, node.Name, exec, ticker)
	}
}

// buildNodePrompt selects the appropriate prompt for a graph node using
// the real prompts from prompt_builder.go. Falls back to a simple
// "Analyze {ticker}" prompt if no specific builder exists.
func buildNodePrompt(nodeID string, state *research.ResearchState, ticker string) string {
	switch nodeID {
	case "market_analyst":
		return researchagents.BuildMarketAnalystPrompt(state)
	case "sentiment_analyst":
		return researchagents.BuildSentimentAnalystPrompt(state)
	case "news_analyst":
		return researchagents.BuildNewsAnalystPrompt(state)
	case "fundamentals_analyst":
		return researchagents.BuildFundamentalsAnalystPrompt(state)
	case "bull_researcher":
		return researchagents.BuildBullResearcherPrompt(state)
	case "bear_researcher":
		return researchagents.BuildBearResearcherPrompt(state)
	case "research_manager":
		return researchagents.BuildResearchManagerPrompt(state)
	case "trader":
		return researchagents.BuildTraderPrompt(state)
	case "aggressive_risk":
		return researchagents.BuildAggressiveRiskPrompt(state)
	case "conservative_risk":
		return researchagents.BuildConservativeRiskPrompt(state)
	case "neutral_risk":
		return researchagents.BuildNeutralRiskPrompt(state)
	case "portfolio_manager":
		return researchagents.BuildPortfolioManagerPrompt(state)
	default:
		return fmt.Sprintf("Analyze %s as %s. Output JSON with your analysis.", ticker, nodeID)
	}
}

// makeNodeHandler creates a ResearchHandler for a specific graph node.
// Uses real prompts from prompt_builder.go when the executor is real;
// falls back to simple mock prompts when using MockLLMExecutor.
func makeNodeHandler(nodeID string, nodeName string, exec researchagents.LLMExecutor, ticker string) research.ResearchHandler {
	return func(ctx context.Context, state *research.ResearchState) error {
		// Build prompt from the real prompt library, using the current state.
		prompt := buildNodePrompt(nodeID, state, ticker)
		messages := []researchagents.Message{{Role: "user", Content: prompt}}

		raw, err := exec.Complete(ctx, messages)
		if err != nil {
			return fmt.Errorf("%s: %w", nodeName, err)
		}

		updateStateFromResponse(state, nodeID, nodeName, raw, ticker)
		return nil
	}
}

// ─── Real LLM Executor ─────────────────────────────────────

// llmConfigFile is a minimal config file reader for the llm section.
type llmConfigFile struct {
	LLM struct {
		Provider string `yaml:"provider"`
		APIKey   string `yaml:"api_key"`
		BaseURL  string `yaml:"base_url"`
		Model    string `yaml:"model"`
		Timeout  int    `yaml:"timeout"`
	} `yaml:"llm"`
}

// llmExecutorAdapter wraps internal/llm.Client to implement researchagents.LLMExecutor.
type llmExecutorAdapter struct {
	client *llm.Client
}

// Complete sends a message to the LLM and returns the text response.
func (a *llmExecutorAdapter) Complete(ctx context.Context, messages []researchagents.Message) (string, error) {
	// Join all messages into a single prompt string.
	var prompt string
	for _, msg := range messages {
		if prompt != "" {
			prompt += "\n"
		}
		prompt += msg.Content
	}
	return a.client.Generate(ctx, prompt)
}

// CompleteStructured is not yet supported for real LLM.
func (a *llmExecutorAdapter) CompleteStructured(ctx context.Context, messages []researchagents.Message, schema any) (any, error) {
	return nil, fmt.Errorf("structured completion not supported")
}

// resolveConfigPath finds config.yml relative to this source file.
// Handles both go run (module root cwd) and go test (package dir cwd).
func resolveConfigPath() string {
	candidates := []string{
		"./examples/quant-trading/config.yml", // module root
		"./config.yml",                        // package dir
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return candidates[0] // return the first candidate; caller will produce a clear error
}

// newLLMExecutor creates a real LLM executor from the config file.
// Returns an error if the config file is missing or invalid.
func newLLMExecutor(cfgPath string) (researchagents.LLMExecutor, error) {
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", cfgPath, err)
	}

	var cfgFile llmConfigFile
	if err := yaml.Unmarshal(data, &cfgFile); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	provider := cfgFile.LLM.Provider
	// Map "openai" to "openrouter" — both use the same OpenAI-compatible
	// chat completions API. The internal llm client only recognizes
	// "openrouter" and "ollama" as valid providers.
	if provider == "openai" {
		provider = "openrouter"
	}

	llmCfg := &llm.Config{
		Provider: provider,
		APIKey:   cfgFile.LLM.APIKey,
		BaseURL:  cfgFile.LLM.BaseURL,
		Model:    cfgFile.LLM.Model,
		Timeout:  cfgFile.LLM.Timeout,
	}

	if llmCfg.Provider == "" || llmCfg.Model == "" {
		return nil, fmt.Errorf("incomplete LLM config: provider=%q model=%q", llmCfg.Provider, llmCfg.Model)
	}

	client, err := llm.NewClient(llmCfg)
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	return &llmExecutorAdapter{client: client}, nil
}
