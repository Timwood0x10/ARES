// Command ares_mcp-server provides an example MCP server demonstrating
// tool, resource, and prompt registration with ares MCP SDK.
//
// Usage: ares_mcp-server serve
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
		fmt.Fprintf(os.Stderr, "Usage: ares_mcp-server serve\n")
		os.Exit(1)
	}

	// Create MCPServer with stdio transport.
	server := ares_mcp.NewMCPServer(
		ares_mcp.Implementation{Name: "example-server", Version: "1.0.0"},
		ares_mcp.NewStdioServerTransport(),
	)

	// Register calculator tool.
	calculatorSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"a": {"type": "number"},
			"b": {"type": "number"},
			"op": {"type": "string", "enum": ["add", "sub", "mul", "div"]}
		},
		"required": ["a", "b", "op"]
	}`)

	err := server.RegisterTool("calculator", "Performs basic arithmetic operations", calculatorSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			a, _ := args["a"].(float64)
			b, _ := args["b"].(float64)
			op, _ := args["op"].(string)

			var result float64
			switch op {
			case "add":
				result = a + b
			case "sub":
				result = a - b
			case "mul":
				result = a * b
			case "div":
				if b == 0 {
					return &ares_mcp.ToolCallResult{
						Content: []ares_mcp.ContentBlock{
							{Type: "text", Text: "division by zero"},
						},
						IsError: true,
					}, nil
				}
				result = a / b
			default:
				return &ares_mcp.ToolCallResult{
					Content: []ares_mcp.ContentBlock{
						{Type: "text", Text: fmt.Sprintf("unknown operation: %s", op)},
					},
					IsError: true,
				}, nil
			}

			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("result: %v", result)},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register calculator", "error", err)
		os.Exit(1)
	}

	// Register echo tool.
	echoSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"message": {"type": "string"}
		},
		"required": ["message"]
	}`)

	err = server.RegisterTool("echo", "Echoes back the input message", echoSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			msg, _ := args["message"].(string)
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{
					{Type: "text", Text: msg},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register echo", "error", err)
		os.Exit(1)
	}

	// Register get_weather tool.
	weatherSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"city": {"type": "string"}
		},
		"required": ["city"]
	}`)

	err = server.RegisterTool("get_weather", "Gets mock weather data for a city", weatherSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			city, _ := args["city"].(string)
			weatherData := fmt.Sprintf(`{"city": "%s", "temperature": 22, "condition": "sunny", "humidity": 45}`, city)
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{
					{Type: "text", Text: weatherData},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register get_weather", "error", err)
		os.Exit(1)
	}

	// Register resource.
	err = server.RegisterResource("weather://current", "Current weather data", "application/json",
		func(ctx context.Context, uri string) (*ares_mcp.ReadResourceResult, error) {
			return &ares_mcp.ReadResourceResult{
				Contents: []ares_mcp.ResourceContent{
					{
						URI:      uri,
						MimeType: "application/json",
						Text:     `{"temperature": 22, "condition": "sunny", "humidity": 45}`,
					},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register resource", "error", err)
		os.Exit(1)
	}

	// Register prompt.
	err = server.RegisterPrompt("summarize", "Summarizes a topic",
		[]ares_mcp.PromptArgument{
			{Name: "topic", Description: "The topic to summarize", Required: true},
		},
		func(ctx context.Context, args map[string]string) (*ares_mcp.GetPromptResult, error) {
			topic, ok := args["topic"]
			if !ok {
				topic = "unknown topic"
			}
			return &ares_mcp.GetPromptResult{
				Description: fmt.Sprintf("Summary of %s", topic),
				Messages: []ares_mcp.PromptMessage{
					{Role: "user", Content: fmt.Sprintf("Please summarize: %s", topic)},
				},
			}, nil
		})
	if err != nil {
		log.Error("failed to register prompt", "error", err)
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
			log.Info("ares_mcp-server: shutting down...")
			cancel()
			return nil
		case <-sigCtx.Done():
			return sigCtx.Err()
		}
	})

	// Start serving.
	if err := server.Serve(ctx); err != nil {
		log.Error("ares_mcp-server: serve error", "error", err)
		os.Exit(1)
	}
	_ = sigEg.Wait() // Clean up signal goroutine
}
