package main

import (
	"context"
	"fmt"
	"log"
	"os"

	ares_arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/llm"
)

// runScorerSmoke performs a live smoke test of the LLMArenaScorer: it scores
// the good and bad strategies on a single preserved case and reports both
// scores. With LLM_SMOKE_EXPECT_REGRESSION=1 the run fails unless the bad
// strategy scores lower, asserting the scorer can distinguish quality.
func runScorerSmoke(ctx context.Context, client *llm.Client) {
	scorer, err := buildScorer(client)
	if err != nil {
		log.Fatalf("build arena scorer: %v", err)
	}

	preservedCase := "Given numbers a and b, return their sum as an integer."
	oldScore, err := scorer.Score(ctx, ares_arena.TestCaseInput{
		Strategy: goodStrategy,
		TestCase: preservedCase,
	})
	if err != nil {
		log.Fatalf("score good strategy: %v", err)
	}
	newScore, err := scorer.Score(ctx, ares_arena.TestCaseInput{
		Strategy: badStrategy,
		TestCase: preservedCase,
	})
	if err != nil {
		log.Fatalf("score bad strategy: %v", err)
	}

	fmt.Printf("good strategy score: %.3f\n", oldScore)
	fmt.Printf("bad strategy score:  %.3f\n", newScore)

	if os.Getenv("LLM_SMOKE_EXPECT_REGRESSION") == "1" && newScore >= oldScore {
		log.Fatalf("expected the bad strategy to score lower, got good=%.3f bad=%.3f", oldScore, newScore)
	}
	log.Printf("scorer smoke ok (good=%.3f, bad=%.3f)", oldScore, newScore)
}
