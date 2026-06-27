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
	cfg := bootstrap.DefaultConfig()
	// Disable evolution for quickstart (requires base strategy).
	cfg.Evolution = nil

	ares, err := bootstrap.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer ares.Stop()

	// Start runtime (manages agent lifecycles).
	if err := ares.Start(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Println("✅ ARES started")

	// List runtime stats.
	stats := ares.Runtime.Stats()
	fmt.Printf("📊 Runtime: active=%d, restarts=%d\n",
		stats.ActiveAgents, stats.TotalRestarts)

	fmt.Println("✅ Quick start complete")
	_ = ares // Use ares for further operations
}
