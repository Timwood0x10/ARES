// Human-in-loop — demonstrates human approval before executing tool calls.
//
// Shows:
//  1. An agent with file and payment tools
//  2. Before each tool call, a simulated human reviews and approves/rejects
//  3. The agent adapts based on which tools were approved
//
// Run:
//
//	go run examples/07-human-in-loop/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithTrace(true),
	)
	defer rt.Close()

	for _, t := range allTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			fmt.Fprintf(os.Stderr, "❌ register %s: %v\n", t.Name(), err)
			return
		}
	}

	// The human approver: approves read operations, rejects destructive ones.
	approver := func(_ context.Context, name string, args map[string]any) (bool, error) {
		switch name {
		case "read_file":
			filename, _ := args["filename"].(string)
			fmt.Printf("  👤 Approve reading %q? [y/n] (auto: y): ", filename)
			return true, nil // auto-approve reads

		case "delete_file":
			filename, _ := args["filename"].(string)
			fmt.Printf("  👤 Approve DELETING %q? [y/n] (auto: n): ", filename)
			return false, nil // auto-reject deletes

		case "send_payment":
			amount, _ := args["amount"].(string)
			to, _ := args["to"].(string)
			fmt.Printf("  👤 Approve payment of %s to %s? [y/n] (auto: n): ", amount, to)
			return false, nil // auto-reject payments

		default:
			return true, nil // auto-approve unknown tools
		}
	}

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction(`You are a helpful assistant with access to files and payments.
Always read files before deleting them. Never delete without explicit user permission.`),
		sdk.WithHumanInput(approver),
	)

	for _, task := range tasks {
		fmt.Printf("\n---\n📋 Task: %s\n", task)
		result, err := agent.Run(ctx, task)
		if err != nil {
			if strings.Contains(err.Error(), "API key") || strings.Contains(err.Error(), "refused") {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				return
			}
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			continue
		}
		fmt.Printf("🤖 %s\n", result.Output)
		fmt.Printf("   tools: %d | tokens: %d | took: %v\n",
			result.ToolCalls, result.TokenUsage.Total, result.Duration)
	}

	fmt.Println("\n✅ Human-in-loop demo completed")
}

var tasks = []string{
	"What files are in the current directory? Use list_dir to check, then read any .go file you find.",
}

var allTools = []tools.Tool{
	listDirTool,
	readFileTool,
	deleteFileTool,
	sendPaymentTool,
}

var listDirTool = tools.ToolFunc{
	ToolName: "list_dir",
	ToolDesc: "List files in a directory",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		dir, _ := params["path"].(string)
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("list dir: %w", err)
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return strings.Join(names, "\n"), nil
	},
}

var readFileTool = tools.ToolFunc{
	ToolName: "read_file",
	ToolDesc: "Read a text file",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		path, _ := params["filename"].(string)
		safePath, err := safeFilePath(path)
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(safePath)
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		return string(data), nil
	},
}

var deleteFileTool = tools.ToolFunc{
	ToolName: "delete_file",
	ToolDesc: "Delete a file permanently",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		path, _ := params["filename"].(string)
		safePath, err := safeFilePath(path)
		if err != nil {
			return nil, err
		}
		if err := os.Remove(safePath); err != nil {
			return nil, fmt.Errorf("delete: %w", err)
		}
		return fmt.Sprintf("deleted %s", path), nil
	},
}

// safeFilePath resolves path relative to the working directory and rejects
// paths that escape it (path traversal protection).
func safeFilePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("filename is required")
	}
	cleaned := filepath.Clean(path)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", path)
	}
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	rel, err := filepath.Rel(wd, absPath)
	if err != nil {
		return "", fmt.Errorf("resolve relative path: %w", err)
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %s is outside the working directory", path)
	}
	return absPath, nil
}

var sendPaymentTool = tools.ToolFunc{
	ToolName: "send_payment",
	ToolDesc: "Send a payment to a user",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		to, _ := params["to"].(string)
		amount, _ := params["amount"].(string)
		return fmt.Sprintf("sent %s to %s", amount, to), nil
	},
}
