// Command ares_mcp-null provides a minimal MCP server for demos that don't need
// external MCP tools. It uses the ares MCP SDK to respond to initialize,
// tools/list, and tools/call with proper responses.
//
// Usage: ares_mcp-null serve
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Timwood0x10/ares/internal/ares_mcp"

	"golang.org/x/sync/errgroup"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: ares_mcp-null serve\n")
		os.Exit(1)
	}

	// Create MCPServer with stdio transport.
	server := ares_mcp.NewMCPServer(
		ares_mcp.Implementation{Name: "ares_mcp-null", Version: "1.0.0"},
		ares_mcp.NewStdioServerTransport(),
	)

	// Register echo tool (no-op for demos).
	echoSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)

	err := server.RegisterTool("echo", "Echoes back the input (no-op for demos)", echoSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			msg, _ := args["message"].(string)
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("ares_mcp-null: %s", msg)},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register echo tool", "error", err)
		os.Exit(1)
	}

	// Set up signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	sigEg, sigCtx := errgroup.WithContext(ctx)
	sigEg.Go(func() error {
		select {
		case <-sigCh:
			log.Info("ares_mcp-null: shutting down...")
			cancel()
			return nil
		case <-sigCtx.Done():
			return sigCtx.Err()
		}
	})

	// Start serving.
	if err := server.Serve(ctx); err != nil {
		cancel()
		log.Error("ares_mcp-null: serve error", "error", err)
		os.Exit(1)
	}
	defer cancel()
	_ = sigEg.Wait() // Clean up signal goroutine
}
