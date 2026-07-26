// Tool calling — demonstrates how to create and use multiple tools with ARES.
//
// Shows YAML-driven config + custom tool registration (the main customization
// point for most projects). The runtime, LLM, memory, distillation, AKG, and
// evolution are all configured via ares.yaml; only custom tools need Go code.
//
// Run:
//
//	go run examples/02-tool-calling/main.go
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()

	// ── 1. Load ares.yaml + wire everything ────────────────────
	cfg, err := sdk.LoadConfigFile("ares.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ load config: %v\n", err)
		return
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ config: %v\n", err)
		return
	}
	rt := sdk.NewRuntime(opts...)
	defer rt.Close()

	// ── 2. Register custom tools (the only customization needed) ─
	for _, t := range customTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			fmt.Fprintf(os.Stderr, "❌ register %s: %v\n", t.Name(), err)
			return
		}
	}

	// ── 3. Create Agent ─────────────────────────────────────────
	agent := rt.NewAgent("assistant",
		sdk.WithInstruction(`You are a helpful assistant with access to tools.
Use the calculator for math, weather for forecasts, and string_tools for text operations.`),
	)

	// ── 4. Run ──────────────────────────────────────────────────
	tasks := []string{
		"Calculate (15*23 + 100) / 5",
		"Reverse the string 'hello world' and uppercase it",
	}
	for _, input := range tasks {
		fmt.Printf("\n---\n🧑 %s\n", input)
		result, err := agent.Run(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			continue
		}
		fmt.Printf("🤖 %s\n", result.Output)
		fmt.Printf("   tools: %d calls | tokens: %d | took: %v\n",
			result.ToolCalls, result.TokenUsage.Total, result.Duration)
	}
}

// ── Custom Tools ─────────────────────────────────────────────
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
	expr = strings.ReplaceAll(expr, " ", "")
	if expr == "" {
		return 0, fmt.Errorf("empty expression")
	}
	for _, c := range expr {
		if !strings.ContainsRune("0123456789+-*/().", c) {
			return 0, fmt.Errorf("invalid character: %c", c)
		}
	}
	tokens := tokenize(expr)
	result, err := parseExpr(tokens)
	if err != nil {
		return 0, err
	}
	return result, nil
}

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

func parseExpr(tokens []string) (float64, error) {
	p := &tokenParser{tokens: tokens}
	result, err := p.parseAddSub()
	if err != nil {
		return 0, err
	}
	if p.pos < len(p.tokens) {
		return 0, fmt.Errorf("unexpected token: %s", p.tokens[p.pos])
	}
	return result, nil
}

type tokenParser struct {
	tokens []string
	pos    int
}

func (p *tokenParser) peek() string {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return ""
}

func (p *tokenParser) consume() string {
	tok := p.peek()
	p.pos++
	return tok
}

func (p *tokenParser) parseAddSub() (float64, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "+" && op != "-" {
			break
		}
		p.consume()
		right, err := p.parseMulDiv()
		if err != nil {
			return 0, err
		}
		if op == "+" {
			left += right
		} else {
			left -= right
		}
	}
	return left, nil
}

func (p *tokenParser) parseMulDiv() (float64, error) {
	left, err := p.parsePrimary()
	if err != nil {
		return 0, err
	}
	for {
		op := p.peek()
		if op != "*" && op != "/" {
			break
		}
		p.consume()
		right, err := p.parsePrimary()
		if err != nil {
			return 0, err
		}
		if op == "*" {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			left /= right
		}
	}
	return left, nil
}

func (p *tokenParser) parsePrimary() (float64, error) {
	tok := p.peek()
	if tok == "" {
		return 0, fmt.Errorf("unexpected end of expression")
	}
	if tok == "(" {
		p.consume()
		val, err := p.parseAddSub()
		if err != nil {
			return 0, err
		}
		if p.peek() != ")" {
			return 0, fmt.Errorf("expected closing parenthesis")
		}
		p.consume()
		return val, nil
	}
	if tok == "-" {
		p.consume()
		val, err := p.parsePrimary()
		if err != nil {
			return 0, err
		}
		return -val, nil
	}
	p.consume()
	val, err := strconv.ParseFloat(tok, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid number %q: %w", tok, err)
	}
	return val, nil
}
