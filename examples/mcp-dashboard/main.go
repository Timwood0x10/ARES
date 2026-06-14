// package main — GoAgentX MCP Code Review Service.
//
// Usage:
//
//	go run . -config ./config.yaml
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"goagentx/api"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "./examples/mcp-dashboard/config.yaml", "Config file")
	flag.Parse()

	cfg, err := api.LoadServiceConfig(configPath)
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	svc, err := api.StartService(ctx, cfg)
	if err != nil {
		slog.Error("service start failed", "error", err)
		os.Exit(1)
	}

	svc.RunReview()
	slog.Info("service running")
	svc.Wait()
}
