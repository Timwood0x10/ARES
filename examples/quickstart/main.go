// Package main demonstrates ARES quick start using the bootstrap API.
// This example shows how to create an ARES instance, run evolution,
// and execute chaos engineering actions using the high-level API.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Timwood0x10/ares/api/bootstrap"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Create ARES with default configuration.
	ares, err := bootstrap.New(ctx, bootstrap.DefaultConfig())
	if err != nil {
		log.Fatal(err)
	}
	defer ares.Stop()

	// Start runtime (manages agent lifecycles).
	if err := ares.Start(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("✅ ARES started")

	// Run genetic algorithm evolution for 5 generations.
	result, err := ares.RunEvolution(ctx, 5)
	if err != nil {
		log.Printf("Evolution error: %v", err)
	} else {
		fmt.Printf("🧬 Evolution complete: best score=%.2f, generations=%d\n",
			result.BestStrategy.Score, result.TotalGens)
	}

	// List runtime stats.
	stats := ares.Runtime.Stats()
	fmt.Printf("📊 Runtime: active=%d, restarts=%d\n",
		stats.ActiveAgents, stats.TotalRestarts)

	fmt.Println("✅ Quick start complete")
}
