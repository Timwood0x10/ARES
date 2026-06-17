// Package main — GoAgentX Quantitative Trading Demo.
// 分析单只股票，结果保存到文件，供后续验证准确率。
//
// Usage:
//
//	go run . -ticker AAPL
//	go run . -ticker AAPL -days 180
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
	"goagentx/internal/dashboard"
	"goagentx/internal/quant"
	"goagentx/internal/quant/market"
	"goagentx/internal/tools/resources/core"
)

// AnalysisResult 保存完整的分析结果，输出为 JSON 供验证.
type AnalysisResult struct {
	Ticker      string         `json:"ticker"`
	Model       string         `json:"model"`
	AnalyzedAt  string         `json:"analyzed_at"`
	DataPoints  int            `json:"data_points"`
	Agents      []AgentOutput  `json:"agents"`
	Allocation  string         `json:"allocation,omitempty"`
}

type AgentOutput struct {
	Name     string `json:"name"`
	Status   string `json:"status"`
	Duration string `json:"duration"`
	Analysis string `json:"analysis"`
	Error    string `json:"error,omitempty"`
}

func main() {
	var (
		cfgPath    string
		agentsPath string
		modelName  string
		dataDir    string
		outDir     string
		ticker     string
		days       int
	)
	flag.StringVar(&cfgPath, "config", "./examples/quant-trading/config.yaml", "")
	flag.StringVar(&agentsPath, "agents", "./examples/quant-trading/config/agents.yaml", "")
	flag.StringVar(&modelName, "model", "", "LLM 模型名")
	flag.StringVar(&dataDir, "data", "./examples/quant-trading/data", "行情 CSV 目录")
	flag.StringVar(&outDir, "out", "./examples/quant-trading/results", "分析结果输出目录")
	flag.StringVar(&ticker, "ticker", "AAPL", "股票代码")
	flag.IntVar(&days, "days", 365, "历史数据天数")
	flag.Parse()

	ticker = strings.ToUpper(strings.TrimSpace(ticker))

	// ── 启动服务 ──
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cfg, err := api.LoadServiceConfig(cfgPath)
	if err != nil {
		slog.Error("配置文件加载失败", "err", err)
		os.Exit(1)
	}
	if modelName != "" {
		cfg.LLM.Model = modelName
	}

	svc, err := api.StartService(ctx, cfg)
	if err != nil {
		slog.Error("服务启动失败", "err", err)
		os.Exit(1)
	}

	// 注册量化 MCP 工具.
	registry := core.NewRegistry()
	if err := quant.RegisterTools(registry); err != nil {
		slog.Error("注册量化工具失败", "err", err)
		os.Exit(1)
	}

	agentCfg, err := agents.LoadConfig(agentsPath)
	if err != nil {
		slog.Error("加载 Agent 配置失败", "err", err)
		os.Exit(1)
	}

	// ── 下载行情数据 ──
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Error("创建数据目录失败", "err", err)
		os.Exit(1)
	}

	log := func(f string, a ...any) {
		fmt.Printf(f+"\n", a...)
	}

	log("\n╔════════════════════════════════════════════╗")
	log("║  GoAgentX 量化分析                         ║")
	log("╚════════════════════════════════════════════╝")
	log("  标的: %s", ticker)
	log("  模型: %s (%s)", cfg.LLM.Model, cfg.LLM.Provider)
	log("  天数: %d\n", days)

	// 下载数据.
	feed := market.NewYahooFeed()
	end := time.Now()
	start := end.AddDate(0, 0, -days)

	ts, err := feed.Candles(ticker, start, end, market.Res1d)
	if err != nil {
		slog.Error("获取行情失败", "err", err)
		os.Exit(1)
	}

	csvPath := filepath.Join(dataDir, ticker+".csv")
	f, err := os.Create(csvPath)
	if err != nil {
		slog.Error("创建 CSV 失败", "err", err)
		os.Exit(1)
	}
	w := csv.NewWriter(f)
	_ = w.Write([]string{"date", "open", "high", "low", "close", "volume"})
	for _, bar := range ts.Bars {
		_ = w.Write([]string{
			bar.Date.Format("2006-01-02"),
			fmt.Sprintf("%.2f", bar.Open), fmt.Sprintf("%.2f", bar.High),
			fmt.Sprintf("%.2f", bar.Low), fmt.Sprintf("%.2f", bar.Close),
			fmt.Sprintf("%d", bar.Volume),
		})
	}
	w.Flush()
	_ = f.Close()
	log("  ✓ 行情数据已保存: %s (%d 条)", csvPath, len(ts.Bars))

	// ── 创建 Agent 分析 ──
	orch := svc.Orchestrator()
	agentIDs := agents.CreateFromConfig(orch, agentCfg, ticker)
	if len(agentIDs) == 0 {
		slog.Error("创建 Agent 失败")
		os.Exit(1)
	}
	log("  ✓ 已创建 %d 个 Agent，正在分析 %s ...\n", len(agentIDs), ticker)

	// 等待完成.
	agentOutputs := waitAndCollect(orch, agentIDs)

	// ── 输出结果 ──
	log("\n════════════════════════════════════════════")
	log("  %s 分析结果", ticker)
	log("════════════════════════════════════════════\n")

	result := AnalysisResult{
		Ticker:     ticker,
		Model:      cfg.LLM.Model,
		AnalyzedAt: time.Now().Format(time.RFC3339),
		DataPoints: len(ts.Bars),
		Agents:     agentOutputs,
	}

	for _, a := range agentOutputs {
		status := "✅"
		if a.Status != "completed" {
			status = "❌"
		}
		log("  %s %s (%s)", status, a.Name, a.Duration)

		analysis := strings.TrimSpace(a.Analysis)
		if analysis != "" {
			// 只打印关键结论的前几行.
			lines := strings.Split(analysis, "\n")
			for j, line := range lines {
				if j >= 6 {
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
		}
		if a.Error != "" {
			log("     ❌ 错误: %s", a.Error)
		}
	}

	// ── Portfolio Manager 的最终结论突出显示 ──
	for _, a := range agentOutputs {
		if a.Name == "Portfolio Manager" && a.Status == "completed" {
			log("")
			log("  ─── 最终交易信号 ───")
			lines := strings.Split(strings.TrimSpace(a.Analysis), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					log("    %s", line)
				}
			}
		}
	}

	// ── 保存结果到文件 ──
	if err := os.MkdirAll(outDir, 0o755); err == nil {
		result.Allocation = csvPath
		data, _ := json.MarshalIndent(result, "", "  ")
		outPath := filepath.Join(outDir, fmt.Sprintf("%s_%s.json", ticker, time.Now().Format("20060102_150405")))
		_ = os.WriteFile(outPath, data, 0o644)
		log("\n  📄 完整分析结果已保存: %s", outPath)
	}

	log("")
	svc.Wait()
}

// waitAndCollect 等待 Agent 完成并收集结果.
func waitAndCollect(orch *dashboard.Orchestrator, ids map[string]string) []AgentOutput {
	order := []string{"fundamentals", "sentiment", "news", "technical", "bull", "bear", "trader", "risk", "pm"}

	// 等待最多 60 秒.
	for range 60 {
		allDone := true
		for _, id := range ids {
			if ag, ok := orch.GetAgent(id); ok && ag.Status != "completed" && ag.Status != "failed" {
				allDone = false
				break
			}
		}
		if allDone {
			break
		}
		time.Sleep(time.Second)
	}

	var outputs []AgentOutput
	for _, key := range order {
		id, ok := ids[key]
		if !ok {
			continue
		}
		ag, ok := orch.GetAgent(id)
		if !ok {
			continue
		}
		outputs = append(outputs, AgentOutput{
			Name:     ag.Name,
			Status:   ag.Status,
			Duration: ag.Duration,
			Analysis: ag.Analysis,
			Error:    ag.Error,
		})
	}
	return outputs
}
