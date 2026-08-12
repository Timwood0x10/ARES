package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	apiimpl "github.com/Timwood0x10/ares/internal/api_impl"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the full ARES service via the embedded launcher (LLM + MCP + dashboard + event store + flight)",
	Long: `Starts the complete ARES application using internal/api_impl.StartService — a
single-call launcher that wires LLM, MCP servers, the dashboard, event store,
and flight recorder. This is the embeddable / alternative launch path to "serve".

Flags:
  --config  Path to an api_impl ServiceConfig YAML (default: configs/api_impl.yaml)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStart()
	},
}

var startConfigPath string

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().StringVarP(&startConfigPath, "config", "c", "configs/api_impl.yaml", "Path to api_impl ServiceConfig YAML")
}

func runStart() error {
	configPath := startConfigPath
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found: %s (use --config to point at an api_impl ServiceConfig YAML)", configPath)
	}

	cfg, err := apiimpl.LoadServiceConfig(configPath)
	if err != nil {
		return fmt.Errorf("load service config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// svcPtr is exchanged via atomic.Store/Load so the signal goroutine never
	// races with the assignment on the main goroutine (the previous shared
	// variable produced an unsynchronized read/write race, and a signal that
	// arrived before StartService returned silently skipped Stop).
	var svcPtr atomic.Pointer[apiimpl.Service]

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		if svc := svcPtr.Load(); svc != nil {
			_ = svc.Stop(context.Background())
		}
		cancel()
	}()

	svc, err := apiimpl.StartService(ctx, cfg)
	if err != nil {
		return fmt.Errorf("start service: %w", err)
	}
	svcPtr.Store(svc)
	svc.Wait()
	return nil
}
