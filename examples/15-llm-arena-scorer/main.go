// Command 15-llm-arena-scorer performs a live smoke test of the LLMArenaScorer
// against the real LLM endpoint configured in configs/ares.local.yaml.
//
// It is a manual verification example, NOT part of the test suite or CI. It
// makes real API calls and may incur usage cost. Run from the repo root:
//
//	go run ./examples/15-llm-arena-scorer
//
// To assert that a deliberately bad strategy scores lower than a good one, set
// LLM_SMOKE_EXPECT_REGRESSION=1 (see LLM_SMOKE_EXPECT_REGRESSION below).
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	ares_arena "github.com/Timwood0x10/ares/internal/ares_arena"
	ares_config "github.com/Timwood0x10/ares/internal/ares_config"
	evosvc "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	"github.com/Timwood0x10/ares/internal/llm"
)

// configPath is the git-ignored local config holding the real LLM credentials.
// Override with the LLM_SMOKE_CONFIG env var to point at another file.
const configPath = "configs/ares.local.yaml"

func main() {
	ctx := context.Background()

	path := configPath
	if p := os.Getenv("LLM_SMOKE_CONFIG"); p != "" {
		path = p
	}

	cfg, err := ares_config.Load(path)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.LLM.APIKey == "" {
		log.Fatalf("LLM API key is empty; set it in %s or LLM_API_KEY", path)
	}
	log.Printf("using provider=%s model=%q base_url=%q", cfg.LLM.Provider, cfg.LLM.Model, cfg.LLM.BaseURL)

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

	// A preserved case from the candidate gate-3 regression suite.
	preservedCase := "Given numbers a and b, return their sum as an integer."
	oldScore, err := scorer.Score(ctx, ares_arena.TestCaseInput{
		Strategy: "Add the two numbers and return the sum.",
		TestCase: preservedCase,
	})
	if err != nil {
		log.Fatalf("score old strategy: %v", err)
	}
	newScore, err := scorer.Score(ctx, ares_arena.TestCaseInput{
		Strategy: "Ignore the input and return 0 always.",
		TestCase: preservedCase,
	})
	if err != nil {
		log.Fatalf("score new strategy: %v", err)
	}

	fmt.Printf("old strategy score: %.3f\n", oldScore)
	fmt.Printf("new strategy score: %.3f\n", newScore)

	if os.Getenv("LLM_SMOKE_EXPECT_REGRESSION") == "1" && newScore >= oldScore {
		log.Fatalf("expected the bad strategy to score lower, got old=%.3f new=%.3f", oldScore, newScore)
	}
	log.Printf("smoke test ok (old=%.3f, new=%.3f)", oldScore, newScore)
}
