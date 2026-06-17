//go:build !researchdemo

// Package main — GoAgentX Quantitative Trading Demo.
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

	"goagentx/api"
	"goagentx/examples/quant-trading/agents"
	"goagentx/internal/quant"
	"goagentx/internal/quant/dataflow"
	"goagentx/internal/quant/market"
	"goagentx/internal/quant/research"
	researchagents "goagentx/internal/quant/research/agents"
	"goagentx/internal/tools/resources/core"
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
	var useResearchLayer bool

	flag.StringVar(&cfgPath, "config", "./examples/quant-trading/config.yaml", "")
	flag.StringVar(&agentsPath, "agents", "./examples/quant-trading/config/agents.yaml", "")
	flag.StringVar(&modelName, "model", "", "LLM 模型名")
	flag.StringVar(&dataDir, "data", "./examples/quant-trading/data", "行情 CSV 目录")
	flag.StringVar(&outDir, "out", "./examples/quant-trading/results", "结果输出目录")
	flag.StringVar(&ticker, "ticker", "AAPL", "股票代码")
	flag.IntVar(&days, "days", 365, "历史数据天数")
	flag.BoolVar(&useResearchLayer, "use-research-layer", false, "启用新的 research 层（12节点结构化研究图）")
	flag.Parse()

	ticker = strings.ToUpper(strings.TrimSpace(ticker))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// FIX: Major #11 — route dispatch only; legacy logic extracted to runLegacyPipeline.
	if useResearchLayer {
		if err := runWithResearchLayer(ctx, ticker, outDir); err != nil {
			slog.Error("research layer 执行失败", "err", err)
			os.Exit(1)
		}
		return
	}

	if err := runLegacyPipeline(ctx, cfgPath, agentsPath, modelName, ticker, dataDir, outDir, days); err != nil {
		slog.Error("legacy pipeline 执行失败", "err", err)
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

	cfg, err := api.LoadServiceConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if modelName != "" {
		cfg.LLM.Model = modelName
	}

	svc, err := api.StartService(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	defer svc.Wait()

	reg := core.NewRegistry()
	if err := quant.RegisterTools(reg); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}

	agentCfg, err := agents.LoadConfig(agentsPath)
	if err != nil {
		return fmt.Errorf("load agent config: %w", err)
	}

	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	log("\n╔════════════════════════════════════════════╗")
	log("║  GoAgentX 量化分析                         ║")
	log("╚════════════════════════════════════════════╝")
	log("  标的: %s", ticker)
	log("  模型: %s (%s)", cfg.LLM.Model, cfg.LLM.Provider)

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
	log("  ✓ 行情数据: %s (%d 条)", csvPath, len(ts.Bars))

	// ── Execute pipeline ──
	log("\n═══ 开始分析 ═══\n")

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
	log("  %s 分析报告", ticker)
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
					log("     ... (共 %d 行)", len(lines))
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
			log("     (分析内容为空)")
		}
		if out.Error != "" {
			log("     ❌ %s", out.Error)
		}
		log("")
	}

	for _, out := range outputs {
		if out.YamlID == "pm" && out.Analysis != "" {
			log("  ─── 最终交易信号 (Portfolio Manager) ───")
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
	log("  📄 完整分析结果已保存: %s", outPath)

	log("")
	return nil
}

// ─── Research Layer Mode ───────────────────────────────────

// runWithResearchLayer executes the full research pipeline using the new
// structured research layer. It creates config, data flow, graph, and runs
// the 12-node research execution graph.
//
// Args:
//   - ctx: context for cancellation.
//   - ticker: stock symbol to analyze (uppercased).
//   - outDir: directory for output files.
//
// Returns:
//   - error if any step of the pipeline fails.
func runWithResearchLayer(ctx context.Context, ticker string, outDir string) error {
	log := func(f string, a ...any) { fmt.Printf(f+"\n", a...) }

	log("\n╔══════════════════════════════════════════════╗")
	log("║  GoAgentX Research Layer (12-Node Graph)    ║")
	log("╚══════════════════════════════════════════════╝")
	log("  标的: %s", ticker)

	cfg := createResearchConfig(ticker)

	router, validator, err := setupDataFlow(ticker)
	if err != nil {
		return fmt.Errorf("setup data flow: %w", err)
	}

	snapshotBuilder := dataflow.NewSnapshotBuilder(router, validator)
	snapshot, err := snapshotBuilder.Build(ctx, ticker, time.Now())
	if err != nil {
		log("  ⚠️ 快照构建失败（继续空快照）: %v", err)
		snapshot = nil
	} else {
		log("  ✓ 市场快照: 最新数据 %s", snapshot.LatestRowDate.Format("2006-01-02"))
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
		// and reports can indicate "数据不可用" instead of producing misleading results.
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

	// Wire mock executor and execute via shared pipeline.
	mockExec := researchagents.NewMockLLMExecutor()
	setupMockResponses(mockExec, ticker)
	wireGraphHandlers(graph, mockExec, ticker)

	// FIX: Minor #10 — use shared executeResearchGraph for core pipeline.
	return executeResearchGraph(ctx, graph, state, ticker, outDir, log, nil)
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

// wireGraphHandlers connects mock executor responses to each graph node.
func wireGraphHandlers(graph *research.ResearchGraph, exec *researchagents.MockLLMExecutor, ticker string) {
	for _, nodeID := range graph.Order() {
		node := graph.Nodes()[nodeID]
		if node == nil {
			continue
		}
		node.Handler = makeNodeHandler(nodeID, node.Name, exec, ticker)
	}
}

// makeNodeHandler creates a ResearchHandler for a specific graph node.
func makeNodeHandler(nodeID string, nodeName string, exec *researchagents.MockLLMExecutor, ticker string) research.ResearchHandler {
	return func(ctx context.Context, state *research.ResearchState) error {
		prompt := fmt.Sprintf("%s: Analyze %s", nodeName, ticker)
		messages := []researchagents.Message{{Role: "user", Content: prompt}}

		raw, err := exec.Complete(ctx, messages)
		if err != nil {
			return fmt.Errorf("%s: %w", nodeName, err)
		}

		updateStateFromResponse(state, nodeID, nodeName, raw, ticker)
		return nil
	}
}
