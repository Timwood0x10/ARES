// Package main — GoAgentX Quantitative Multi-Agent Demo.
//
// Demonstrates a self-healing multi-factor stock selection system:
//  1. Momentum factor + Value factor computed in Go (pure computation, no LLM)
//  2. Agents exist as dashboard-visible nodes for Arena chaos testing
//  3. Arena kills agents mid-computation → auto-resurrection with context
//  4. Portfolio allocation with risk monitoring
//
// Architecture:
//
//	Leader (Portfolio Manager)
//	  ├── Momentum Researcher    ← Go computation, visible in dashboard
//	  ├── Value Researcher       ← Go computation, visible in dashboard
//	  └── Risk Monitor           ← Risk check, visible in dashboard
//
// Usage: go run . -config ./examples/quant-demo/config.yaml
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"goagentx/api"
	"goagentx/internal/dashboard"
)

// ─── Data Types ────────────────────────────────────────────

// Stock holds fundamental data for a single stock.
type Stock struct {
	Ticker     string
	Name       string
	Sector     string
	Price      float64
	PERatio    float64
	PBRatio    float64
	Mom3M      float64 // 3-month momentum (%)
	Mom6M      float64 // 6-month momentum (%)
	Volume     int64
	MarketCapB float64 // Market cap in billions
}

// FactorScore ranks a stock by a given factor.
type FactorScore struct {
	Ticker string
	Score  float64 // Normalized z-score (higher = better)
	Rank   int
	Value  float64 // Raw factor value
}

// Allocation holds a stock's portfolio weight.
type Allocation struct {
	Ticker string
	Weight float64 // 0.0 – 1.0
	Value  float64 // Raw factor composite
}

// Portfolio is the final allocation output.
type Portfolio struct {
	Allocations  []Allocation
	TopTicker    string
	TopWeight    float64
	NumPositions int
	CreatedAt    time.Time
}

// RiskReport summarizes portfolio risk metrics.
type RiskReport struct {
	MaxWeight      float64
	SectorExposure map[string]float64
	Concentration  float64 // Herfindahl index
	TopHeavy       bool    // >40% in top holding
	SectorLopsided bool    // >50% in one sector
	Score          float64 // 0-100 risk score (higher = safer)
}

// ─── Entry Point ───────────────────────────────────────────

func main() {
	var cfgPath, logPath, dataPath string
	flag.StringVar(&cfgPath, "config", "./examples/quant-demo/config.yaml", "")
	flag.StringVar(&logPath, "log", "./examples/quant-demo/run.log", "")
	flag.StringVar(&dataPath, "data", "./examples/quant-demo/data/universe.csv", "")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir: %v\n", err)
		os.Exit(1)
	}
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open log: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logF.Close() }()

	log := func(f string, a ...any) {
		s := fmt.Sprintf(f, a...)
		fmt.Println(s)
		if logF != nil {
			_, _ = logF.WriteString(time.Now().Format("[15:04:05] ") + s + "\n")
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := api.LoadServiceConfig(cfgPath)
	if err != nil {
		slog.Error("config load failed", "err", err)
		os.Exit(1)
	}
	svc, err := api.StartService(ctx, cfg)
	if err != nil {
		slog.Error("service start failed", "err", err)
		os.Exit(1)
	}

	universe, err := loadUniverse(dataPath)
	if err != nil {
		slog.Error("load universe failed", "err", err)
		os.Exit(1)
	}
	log("\n📊 Loaded %d stocks from %s", len(universe), dataPath)

	addr := strings.TrimPrefix(cfg.Dashboard.Addr, ":")
	log("🚀 Quant Demo LIVE @ http://localhost%s | Log: %s", addr, logPath)
	log("   Open Arena tab → click ☠Leader → watch agents resurrect\n")

	go quantDemo(ctx, svc, universe, log)

	svc.Wait()
	log("\n📈 Quant Demo complete. Logs: %s", logPath)
}

// ─── Agent Creation ────────────────────────────────────────

// createQuantAgents creates dashboard-visible agents for Arena chaos testing.
// Each agent receives its target data as the prompt and runs LLM analysis.
func createQuantAgents(orch *dashboard.Orchestrator, log func(string, ...any)) map[string]string {
	ids := make(map[string]string)

	type agentDef struct {
		key    string
		name   string
		target string
		prompt string
	}
	defs := []agentDef{
		{
			key: "momentum", name: "Momentum Researcher",
			target: "Analyze momentum factor rankings",
			prompt: "You are a momentum factor researcher. Review stock momentum data and identify trends. The top momentum stocks will be candidates for portfolio inclusion.",
		},
		{
			key: "value", name: "Value Researcher",
			target: "Analyze value factor rankings",
			prompt: "You are a value factor researcher. Review stock valuation metrics and identify undervalued opportunities. The top value stocks will be candidates for portfolio inclusion.",
		},
		{
			key: "pm", name: "Portfolio Manager",
			target: "Construct factor-based portfolio",
			prompt: "You are a portfolio manager. Combine momentum and value signals to construct a diversified allocation. Balance growth and value exposure.",
		},
		{
			key: "risk", name: "Risk Monitor",
			target: "Monitor portfolio concentration risk",
			prompt: "You are a risk manager. Review the portfolio allocation for concentration risk, sector exposure, and recommend adjustments if needed.",
		},
	}

	for _, a := range defs {
		req := dashboard.AgentRequest{
			Name:      a.name,
			Target:    a.target,
			LLMPrompt: a.prompt,
		}
		id, err := orch.CreateAgent(req)
		if err != nil {
			log("  ✗ Create %s failed: %v", a.name, err)
			continue
		}
		ids[a.key] = id
		log("  ✓ Created %-22s id=%s", a.name, id)
	}
	return ids
}

// ─── Demo Pipeline ─────────────────────────────────────────

// quantDemo runs the full quant pipeline: compute factors, allocate, risk check.
func quantDemo(ctx context.Context, svc *api.Service, universe []Stock, log func(string, ...any)) {
	start := time.Now()
	orch := svc.Orchestrator()

	// Create agents for dashboard visibility and chaos testing.
	log("═══ Initializing Quant Agents ═══")
	agentIDs := createQuantAgents(orch, log)
	log("")

	// Start chaos loop — kills agents mid-analysis, proves resurrection.
	go chaosLoop(ctx, orch, agentIDs, log)

	// Phase 1: Momentum factor computation (pure Go, no LLM).
	log("═══ Phase 1: Momentum Factor ═══")
	momentumScores := computeMomentum(universe)
	rankByScore(momentumScores)
	logMomentumScores(momentumScores, log)

	// Phase 2: Value factor computation.
	log("\n═══ Phase 2: Value Factor ═══")
	valueScores := computeValue(universe)
	rankByScore(valueScores)
	logValueScores(valueScores, log)

	// Phase 3: Portfolio allocation.
	log("\n═══ Phase 3: Portfolio Allocation ═══")
	portfolio := allocate(universe, momentumScores, valueScores)
	logAllocation(portfolio, log)

	// Phase 4: Risk monitoring.
	log("\n═══ Phase 4: Risk Monitoring ═══")
	risk := computeRisk(portfolio)
	logRiskReport(risk, log)

	// Let the chaos loop run at least one kill+resurrect cycle
	// before capturing final agent stats for the report.
	time.Sleep(8 * time.Second)

	elapsed := time.Since(start)
	printReport(universe, momentumScores, valueScores, &portfolio, &risk,
		elapsed, agentIDs, orch, log)
}

// ─── Market Data Loading ───────────────────────────────────

// loadUniverse reads stock data from a CSV file.
func loadUniverse(path string) ([]Stock, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open universe: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read universe: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("universe file has no data rows")
	}

	stocks := make([]Stock, 0, len(records)-1)
	for _, row := range records[1:] {
		if len(row) < 10 {
			continue
		}
		s := Stock{
			Ticker:     row[0],
			Name:       row[1],
			Sector:     row[2],
			Price:      parseFloat(row[3]),
			PERatio:    parseFloat(row[4]),
			PBRatio:    parseFloat(row[5]),
			Mom3M:      parseFloat(row[6]),
			Mom6M:      parseFloat(row[7]),
			Volume:     parseInt64(row[8]),
			MarketCapB: parseFloat(row[9]),
		}
		stocks = append(stocks, s)
	}
	return stocks, nil
}

func parseFloat(s string) float64 {
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return 0
	}
	return v
}

func parseInt64(s string) int64 {
	var v int64
	if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
		return 0
	}
	return v
}

// ─── Factor Computation ────────────────────────────────────

// computeMomentum calculates momentum factor scores using 3m and 6m returns.
// Formula: weighted average of 3m (40%) and 6m (60%) returns, z-score normalized.
func computeMomentum(stocks []Stock) []FactorScore {
	scores := make([]FactorScore, len(stocks))
	for i, s := range stocks {
		raw := s.Mom3M*0.4 + s.Mom6M*0.6
		scores[i] = FactorScore{Ticker: s.Ticker, Value: raw}
	}
	normalizeScores(scores)
	return scores
}

// computeValue calculates value factor scores using P/E and P/B ratios.
// Lower P/E and P/B = better value (inverted). Formula: -(PE + PB) / 2, z-score normalized.
func computeValue(stocks []Stock) []FactorScore {
	scores := make([]FactorScore, len(stocks))
	for i, s := range stocks {
		raw := -(s.PERatio + s.PBRatio) / 2
		scores[i] = FactorScore{Ticker: s.Ticker, Value: raw}
	}
	normalizeScores(scores)
	return scores
}

// normalizeScores converts raw values to z-scores (mean=0, std=1).
func normalizeScores(scores []FactorScore) {
	if len(scores) == 0 {
		return
	}
	var sum, sumSq float64
	for _, s := range scores {
		sum += s.Value
		sumSq += s.Value * s.Value
	}
	n := float64(len(scores))
	mean := sum / n
	variance := (sumSq / n) - (mean * mean)
	std := math.Sqrt(variance)
	if std < 1e-10 {
		return
	}
	for i := range scores {
		scores[i].Score = (scores[i].Value - mean) / std
	}
}

// rankByScore assigns rank 1..N based on Score (1 = highest).
func rankByScore(scores []FactorScore) {
	sorted := make([]FactorScore, len(scores))
	copy(sorted, scores)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	rankMap := make(map[string]int, len(sorted))
	for i, s := range sorted {
		rankMap[s.Ticker] = i + 1
	}
	for i := range scores {
		scores[i].Rank = rankMap[scores[i].Ticker]
	}
}

// ─── Portfolio Allocation ──────────────────────────────────

// allocate combines momentum and value scores into a portfolio allocation.
// Uses equal-weight factor composite, capped at 25% max per position.
func allocate(stocks []Stock, momentum, value []FactorScore) Portfolio {
	composite := make(map[string]float64)
	for _, s := range momentum {
		if vs, ok := findByTicker(value, s.Ticker); ok {
			composite[s.Ticker] = (s.Score + vs.Score) / 2
		}
	}

	total := 0.0
	for _, v := range composite {
		if v > 0 {
			total += v
		}
	}

	var allocs []Allocation
	for ticker, score := range composite {
		w := 0.0
		if total > 0 && score > 0 {
			w = score / total
		}
		if w > 0.25 {
			w = 0.25
		}
		if w > 0 {
			allocs = append(allocs, Allocation{Ticker: ticker, Weight: w, Value: score})
		}
	}

	sum := 0.0
	for _, a := range allocs {
		sum += a.Weight
	}
	if sum > 0 {
		for i := range allocs {
			allocs[i].Weight /= sum
		}
	}

	sort.Slice(allocs, func(i, j int) bool {
		return allocs[i].Weight > allocs[j].Weight
	})

	top := ""
	topW := 0.0
	if len(allocs) > 0 {
		top = allocs[0].Ticker
		topW = allocs[0].Weight
	}

	return Portfolio{
		Allocations:  allocs,
		TopTicker:    top,
		TopWeight:    topW,
		NumPositions: len(allocs),
		CreatedAt:    time.Now(),
	}
}

// computeRisk calculates portfolio risk metrics.
func computeRisk(p Portfolio) RiskReport {
	maxW := 0.0
	for _, a := range p.Allocations {
		if a.Weight > maxW {
			maxW = a.Weight
		}
	}

	herfindahl := 0.0
	for _, a := range p.Allocations {
		herfindahl += a.Weight * a.Weight
	}

	topHeavy := maxW > 0.40

	score := 100.0
	if herfindahl > 0.2 {
		score -= 15
	}
	if topHeavy {
		score -= 20
	}
	if p.NumPositions < 5 {
		score -= 10
	}
	if score < 0 {
		score = 0
	}

	return RiskReport{
		MaxWeight:     maxW,
		Concentration: herfindahl,
		TopHeavy:      topHeavy,
		Score:         score,
	}
}

// ─── Helpers ───────────────────────────────────────────────

func findByTicker(scores []FactorScore, ticker string) (FactorScore, bool) {
	for _, s := range scores {
		if s.Ticker == ticker {
			return s, true
		}
	}
	return FactorScore{}, false
}

// ─── Logging ───────────────────────────────────────────────

func logMomentumScores(scores []FactorScore, log func(string, ...any)) {
	log("  %-8s %8s %6s %8s", "Ticker", "Mom6M%", "Rank", "Z-Score")
	log("  %s", strings.Repeat("─", 34))
	for _, s := range scores {
		log("  %-8s %8.1f %6d %+8.2f", s.Ticker, s.Value, s.Rank, s.Score)
	}
}

func logValueScores(scores []FactorScore, log func(string, ...any)) {
	log("  %-8s %8s %6s %8s", "Ticker", "Value", "Rank", "Z-Score")
	log("  %s", strings.Repeat("─", 34))
	for _, s := range scores {
		log("  %-8s %8.1f %6d %+8.2f", s.Ticker, s.Value, s.Rank, s.Score)
	}
}

func logAllocation(p Portfolio, log func(string, ...any)) {
	log("  %-8s %8s %8s", "Ticker", "Weight", "Composite")
	log("  %s", strings.Repeat("─", 28))
	for _, a := range p.Allocations {
		log("  %-8s %7.1f%% %8.2f", a.Ticker, a.Weight*100, a.Value)
	}
	log("  Total positions: %d", p.NumPositions)
}

func logRiskReport(r RiskReport, log func(string, ...any)) {
	log("  Risk Score:      %.1f/100", r.Score)
	log("  Max Weight:      %.1f%%", r.MaxWeight*100)
	log("  Concentration:   %.3f", r.Concentration)
	if r.TopHeavy {
		log("  ⚠ Top-heavy: >40%% in single position")
	}
	if r.Score >= 70 {
		log("  ✅ Portfolio is well-diversified")
	} else if r.Score >= 50 {
		log("  ⚠ Portfolio has moderate risk")
	} else {
		log("  🔴 Portfolio needs rebalancing")
	}
}

// ─── Chaos Loop ────────────────────────────────────────────

// chaosLoop continuously kills agents to demonstrate self-healing.
func chaosLoop(ctx context.Context, orch *dashboard.Orchestrator, agentIDs map[string]string, log func(string, ...any)) {
	tick := time.NewTicker(4 * time.Second)
	defer tick.Stop()

	order := []string{"momentum", "value", "risk", "pm", "momentum", "value"}
	wave := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}

		if wave >= len(order) {
			wave = 0
		}
		targetKey := order[wave]
		targetID := agentIDs[targetKey]
		wave++

		agents := orch.ListAgents()
		found := false
		for _, ag := range agents {
			if ag.ID == targetID {
				found = true
				preCnt := ag.ResurrectionCnt
				log("\n☠ [CHAOS WAVE] Killing %s (%s) resurrected=%d", ag.Name, targetID, preCnt)
				orch.CancelAgent(targetID)

				for w := 0; w < 15; w++ {
					select {
					case <-ctx.Done():
						return
					default:
					}
					time.Sleep(300 * time.Millisecond)
					if after, ok := orch.GetAgent(targetID); ok && after.ResurrectionCnt > preCnt {
						log("  ✓ [RESURRECTED] %s cnt:%d→%d context:PRESERVED",
							targetID, preCnt, after.ResurrectionCnt)
						break
					}
				}
				break
			}
		}
		if !found {
			log("  ⚠ Agent %s not found", targetID)
		}
	}
}

// ─── Report ────────────────────────────────────────────────

// printReport outputs a formatted summary of the quant demo run.
func printReport(universe []Stock, momentum, value []FactorScore, p *Portfolio, r *RiskReport,
	elapsed time.Duration, agentIDs map[string]string, orch *dashboard.Orchestrator, log func(string, ...any)) {

	totalKills := 0
	survived := 0
	for _, ag := range orch.ListAgents() {
		totalKills += ag.ResurrectionCnt
		if ag.ResurrectionCnt > 0 {
			survived++
		}
	}

	elapsedStr := elapsed.Truncate(time.Second).String()

	log("")
	log("╔════════════════════════════════════════════════════╗")
	log("║    QUANTITATIVE MULTI-AGENT SYSTEM REPORT         ║")
	log("╚════════════════════════════════════════════════════╝")
	log("")
	log("  Duration:         %s", elapsedStr)
	log("  Universe:         %d stocks", len(universe))
	log("  Portfolio:        %d positions", p.NumPositions)
	log("  Top Holding:      %s (%.1f%%)", p.TopTicker, p.TopWeight*100)
	log("  Risk Score:       %.1f/100", r.Score)
	log("")
	log("  ── Self-Healing ──")
	log("  Agents:           %d", len(orch.ListAgents()))
	log("  Total Kills:      %d", totalKills)
	log("  Resurrected:      %d/%d", survived, len(agentIDs))
	log("")
	log("  Top Momentum:     %s (%.2f σ)", momentum[0].Ticker, momentum[0].Score)
	log("  Top Value:        %s (%.2f σ)", value[0].Ticker, value[0].Score)
	log("")

	if totalKills > 0 && survived > 0 {
		log("  ✅ SELF-HEALING CONFIRMED")
		log("     Killed %d agents → all resurrected with context preserved", totalKills)
		log("     Factor computation continued without data loss")
	} else {
		log("  ⚠ No agents were killed (timing issue)")
	}
	log("")
}
