// Command mcp-null provides a minimal MCP server for demos that don't need
// external MCP tools. It uses the GoAgentX MCP SDK to respond to initialize,
// tools/list, and tools/call with proper responses.
//
// Usage: mcp-null serve
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"goagentx/internal/mcp"

	"golang.org/x/sync/errgroup"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: mcp-null serve\n")
		os.Exit(1)
	}

	// Create MCPServer with stdio transport.
	server := mcp.NewMCPServer(
		mcp.Implementation{Name: "mcp-null", Version: "1.0.0"},
		mcp.NewStdioServerTransport(),
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
		func(ctx context.Context, args map[string]any) (*mcp.ToolCallResult, error) {
			msg, _ := args["message"].(string)
			return &mcp.ToolCallResult{
				Content: []mcp.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("mcp-null: %s", msg)},
				},
			}, nil
		})
	if err != nil {
		slog.Error("failed to register echo tool", "error", err)
		os.Exit(1)
	}

	// Set up signal handling for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	sigEg, sigCtx := errgroup.WithContext(ctx)
	sigEg.Go(func() error {
		select {
		case <-sigCh:
			slog.Info("mcp-null: shutting down...")
			cancel()
			return nil
		case <-sigCtx.Done():
			return sigCtx.Err()
		}
	})

	// Start serving.
	if err := server.Serve(ctx); err != nil {
		slog.Error("mcp-null: serve error", "error", err)
		os.Exit(1)
	}
	_ = sigEg.Wait() // Clean up signal goroutine
}
