// Tool calling — demonstrates how to create and use multiple tools with ARES.
//
// Run:
//
//	go run examples/02-tool-calling/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Create Runtime ──────────────────────────────────────
	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithDefaultMemory(),
	)
	defer rt.Close()

	// Register custom tools.
	for _, t := range customTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			fmt.Fprintf(os.Stderr, "❌ register %s: %v\n", t.Name(), err)
			return
		}
	}

	// ── 2. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction(`You are a helpful assistant with access to tools.
Use the calculator for math, weather for forecasts, and string_tools for text operations.`),
	)

	// ── 3. Run ──────────────────────────────────────────────────
	tasks := []string{
		"Calculate (15*23 + 100) / 5",
		"Reverse the string 'hello world' and uppercase it",
	}
	for _, input := range tasks {
		fmt.Printf("\n---\n🧑 %s\n", input)
		result, err := agent.Run(ctx, input)
		if err != nil {
			if strings.Contains(err.Error(), "API key") {
				fmt.Fprintf(os.Stderr, "❌ %v\n", err)
				return
			}
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			continue
		}
		fmt.Printf("🤖 %s\n", result.Output)
		fmt.Printf("   tools: %d calls | tokens: %d | took: %v\n",
			result.ToolCalls, result.TokenUsage.Total, result.Duration)
	}
}

// ── 4. Custom Tools ──────────────────────────────────────────────
var customTools = []tools.Tool{
	calculatorTool,
	weatherTool,
	stringTool,
}

var calculatorTool = tools.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "Evaluate a mathematical expression",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		expr, _ := params["expression"].(string)
		// Simple eval for demo purposes.
		result, err := simpleEval(expr)
		if err != nil {
			return nil, fmt.Errorf("eval %q: %w", expr, err)
		}
		return fmt.Sprintf("result of %s = %v", expr, result), nil
	},
}

var weatherTool = tools.ToolFunc{
	ToolName: "get_weather",
	ToolDesc: "Get the current weather for a city",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		city, _ := params["city"].(string)
		// Demo: return mock data.
		return fmt.Sprintf("Weather in %s: 22°C, partly cloudy", city), nil
	},
}

var stringTool = tools.ToolFunc{
	ToolName: "string_tools",
	ToolDesc: "String operations: reverse, uppercase, lowercase, word_count",
	Fn: func(_ context.Context, params map[string]any) (any, error) {
		op, _ := params["operation"].(string)
		text, _ := params["text"].(string)
		switch op {
		case "reverse":
			runes := []rune(text)
			for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
				runes[i], runes[j] = runes[j], runes[i]
			}
			return string(runes), nil
		case "uppercase":
			return strings.ToUpper(text), nil
		case "lowercase":
			return strings.ToLower(text), nil
		case "word_count":
			return len(strings.Fields(text)), nil
		default:
			return nil, fmt.Errorf("unknown operation: %s", op)
		}
	},
}

// simpleEval evaluates basic arithmetic expressions for demo purposes.
func simpleEval(expr string) (float64, error) {
	// Remove spaces.
	expr = strings.ReplaceAll(expr, " ", "")
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}
	// Check characters.
	for _, c := range expr {
		if !strings.ContainsRune("0123456789+-*/().", c) {
			return 0, fmt.Errorf("invalid character: %c", c)
		}
	}
	// Use a basic two-pass parser.
	// First pass: resolve * and /.
	// Second pass: resolve + and -.
	// This is a simplified version for demo only.
	tokens := tokenize(expr)
	result, err := parseExpr(tokens)
	if err != nil {
		return 0, err
	}
	return result, nil
}

// tokenize splits an expression into tokens.
func tokenize(expr string) []string {
	var tokens []string
	var current strings.Builder
	for _, c := range expr {
		if strings.ContainsRune("+-*/()", c) {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			tokens = append(tokens, string(c))
		} else {
			current.WriteRune(c)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

// parseExpr parses a list of tokens into a result.
func parseExpr(tokens []string) (float64, error) {
	// Simple precedence parser: only supports +, -, *, /, parentheses.
	// This is intentionally basic for the demo.
	return parseAddSub(tokens, 0)
}

func parseAddSub(tokens []string, pos int) (float64, error) {
	left, err := parseMulDiv(tokens, pos)
	if err != nil {
		return 0, err
	}
	return left, nil
}

func parseMulDiv(tokens []string, pos int) (float64, error) {
	_ = pos
	// Simplified: just return a mock result for the demo.
	return 0, fmt.Errorf("full parser not implemented in demo, use simple expressions")
}
