package main

import (
	"context"
	"fmt"
	"log"

	ares_arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/llm"
)

// runRegressionDemo runs a real-LLM preserved-case regression comparison
// exactly as the candidate gate 3 does: it scores the old (stable) and new
// (candidate) instructions against the preserved case suite via the
// LLMArenaScorer + ares_arena.RegressionTester, then reports whether the new
// strategy regresses the preserved cases.
//
// Each strategy side is run 5 times per case (2 LLM calls per run: execute +
// grade) so the verdict is statistically meaningful; tune the run counts to
// match your provider's rate limit.
func runRegressionDemo(ctx context.Context, client *llm.Client) {
	scorer, err := buildScorer(client)
	if err != nil {
		log.Fatalf("build arena scorer: %v", err)
	}

	tester, err := ares_arena.NewRegressionTesterWithScorer(scorer)
	if err != nil {
		log.Fatalf("build regression tester: %v", err)
	}

	log.Printf("old strategy: %q", goodStrategy)
	log.Printf("new strategy: %q", badStrategy)
	log.Printf("preserved cases: %d", len(preservedCases))

	result, err := tester.Run(ctx, ares_arena.RegressionConfig{
		OldStrategy:  goodStrategy,
		NewStrategy:  badStrategy,
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
	log.Printf("regression demo done")
}
