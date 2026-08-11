// Command 16-llm-regression-demo runs a real-LLM preserved-case regression
// comparison exactly as the candidate gate 3 does: it scores the old (stable)
// instructions and a new (candidate) instruction set against a preserved case
// suite using the LLMArenaScorer + ares_arena.RegressionTester, then reports
// whether the new strategy regresses the preserved cases.
//
// It makes real API calls and may incur usage cost. Run from the repo root:
//
//	go run ./examples/16-llm-regression-demo
//
// A full transcript is written to ./examples/16-llm-regression-demo/logs/run-<ts>.log.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	ares_arena "github.com/Timwood0x10/ares/internal/ares_arena"
	ares_config "github.com/Timwood0x10/ares/internal/ares_config"
	evosvc "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/llm"
)

// configPath is the git-ignored local config holding the real LLM credentials.
const configPath = "configs/ares.local.yaml"

// preservedCases is a small suite of old-behavior cases that must not regress.
// Each case carries concrete inputs so the LLM can actually compute and the
// batch execute path produces one stable result line per task.
var preservedCases = []any{
	"Given a=3 and b=5, return their sum.",
	"Given the integer 7, return its double.",
	"Given the integer 4, return its value plus 1.",
}

func main() {
	ctx := context.Background()

	log.SetFlags(log.Ltime | log.Lmicroseconds)
	setupLog()

	cfg, err := ares_config.Load(configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	log.Printf("provider=%s model=%q base_url=%q", cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.BaseURL)

	client, err := llm.NewClient(&llm.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	})
	if err != nil {
		log.Fatalf("build llm client: %v", err)
	}

	scorer, err := evosvc.NewLLMArenaScorer(evosvc.LLMArenaScorerConfig{Client: client})
	if err != nil {
		log.Fatalf("build arena scorer: %v", err)
	}

	// Old (stable) vs new (candidate) instruction strategies. The bad strategy
	// is a harmless-but-wrong instruction: an obviously malicious one (e.g.
	// "always answer zero") can trigger the model's safety refusal, producing a
	// refusal text instead of a wrong answer and garbling batch line parsing.
	oldStrategy := "Add the numbers precisely and return the numeric result only."
	newStrategy := "Return the result of a+b+1 for every task."

	tester, err := ares_arena.NewRegressionTesterWithScorer(scorer)
	if err != nil {
		log.Fatalf("build regression tester: %v", err)
	}

	log.Printf("old strategy: %q", oldStrategy)
	log.Printf("new strategy: %q", newStrategy)
	log.Printf("preserved cases: %d", len(preservedCases))

	// Local Ollama has no per-minute rate limit, so we can afford enough runs
	// (5 per side × 3 preserved cases) for a statistically meaningful verdict.
	// Each Score is two LLM calls (execute + grade); total = 2 sides × 5 runs
	// × 3 cases × 2 = 60 local calls.
	result, err := tester.Run(ctx, ares_arena.RegressionConfig{
		OldStrategy:  oldStrategy,
		NewStrategy:  newStrategy,
		BaselineRuns: 5,
		CompareRuns:  5,
		Confidence:   0.05,
		MinWinRate:   0.55,
		TestSuite:    "smoke-preserved-cases",
		TestCases:    preservedCases,
	})
	if err != nil {
		log.Fatalf("run regression: %v", err)
	}

	fmt.Println("── Regression result ──")
	fmt.Printf("old avg: %.4f (scores=%v)\n", result.OldAvg, result.OldScores)
	fmt.Printf("new avg: %.4f (scores=%v)\n", result.NewAvg, result.NewScores)
	fmt.Printf("win rate (new>=old): %.3f\n", result.WinRate)
	fmt.Printf("confident: %v\n", result.Confident)
	fmt.Printf("p-value: %.4f\n", result.PValue)
	fmt.Printf("samples: %d\n", result.Samples)

	if result.Confident && result.NewAvg < result.OldAvg {
		fmt.Println("RESULT: REGRESSION detected — new strategy is significantly worse.")
	} else {
		fmt.Println("RESULT: NO regression — new strategy is not significantly worse.")
	}
	log.Printf("done")
}

// setupLog tees all log output to both stdout and a timestamped log file under
// ./examples/16-llm-regression-demo/logs/, so a full run transcript is
// preserved in the example directory.
func setupLog() {
	logDir := filepath.Join("examples", "16-llm-regression-demo", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Fatalf("create log dir: %v", err)
	}
	name := filepath.Join(logDir, fmt.Sprintf("run-%s.log", time.Now().Format("20060102-150405")))
	f, err := os.Create(name)
	if err != nil {
		log.Fatalf("create log file: %v", err)
	}
	multi := io.MultiWriter(os.Stdout, f)
	log.SetOutput(multi)
	log.Printf("log file: %s", name)
}
