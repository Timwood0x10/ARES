//go:build ignore

// Example usage of Phase 3 (Plugin Tool System) and Phase 4 (Evaluation Framework)
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/eval"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

func main() {
	ctx := context.Background()

	fmt.Println("=== Phase 3: Plugin Tool System Example ===")
	examplePluginSystem()

	fmt.Println("\n=== Phase 4: Evaluation Framework Example ===")
	exampleEvaluationFramework(ctx)
}

// examplePluginSystem demonstrates how to use the plugin tool system.
func examplePluginSystem() {
	// 1. Create a plugin registry
	registry := core.NewPluginRegistry()
	fmt.Println("✓ Created plugin registry")

	// 2. Register a custom tool factory
	calculatorFactory := &CalculatorToolFactory{}
	if err := registry.RegisterFactory(calculatorFactory); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✓ Registered factory: %s\n", calculatorFactory.Name())

	// 3. Load plugins from configuration
	configs := []core.PluginConfig{
		{
			Name:    "my-calculator",
			Factory: "calculator",
			Enabled: true,
			Config:  map[string]interface{}{"precision": 2},
		},
		{
			Name:    "disabled-tool",
			Factory: "calculator",
			Enabled: false, // This one won't be loaded
			Config:  map[string]interface{}{},
		},
	}

	if err := registry.LoadPlugins(configs); err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Loaded plugins from config")

	// 4. Get and use a tool
	tool, exists := registry.GetTool("my-calculator")
	if !exists {
		log.Fatal("tool not found")
	}
	fmt.Printf("✓ Got tool: %s - %s\n", tool.Name(), tool.Description())

	// 5. List all loaded plugins
	plugins := registry.ListPlugins()
	fmt.Printf("✓ Loaded plugins: %v\n", plugins)

	// 6. List all registered factories
	factories := registry.ListFactories()
	fmt.Printf("✓ Registered factories: %v\n", factories)
}

// exampleEvaluationFramework demonstrates how to use the evaluation framework.
func exampleEvaluationFramework(ctx context.Context) {
	// 1. Create a test suite loader
	loader := eval.NewLoader()
	fmt.Println("✓ Created test suite loader")

	// 2. Load test suite from YAML
	suite, err := loader.Load("test/eval/basic.yaml")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✓ Loaded test suite: %s (%d test cases)\n", suite.Name, len(suite.TestCases))

	// 3. Create an agent executor (mock for demo)
	executor := &MockAgentExecutor{}
	fmt.Println("✓ Created agent executor")

	// 4. Create a test runner
	runner, err := eval.NewAgentTestRunner(executor)
	if err != nil {
		fmt.Printf("✗ Failed to create test runner: %v\n", err)
		return
	}
	fmt.Println("✓ Created test runner")

	// 5. Run the test suite
	results, err := runner.RunSuite(ctx, *suite)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("✓ Ran %d test cases\n", len(results))

	// 6. Create evaluators
	exactMatchEval := eval.NewExactMatchEvaluator()
	toolUsageEval := eval.NewToolUsageEvaluator()
	keywordEval := eval.NewKeywordPresenceEvaluator([]string{"AI", "technology", "analysis"})
	fmt.Println("✓ Created evaluators")

	// 7. Evaluate results
	allScores := make([][]eval.EvalScore, len(results))
	for i, result := range results {
		scores, err := exactMatchEval.Evaluate(ctx, suite.TestCases[i], result)
		if err != nil {
			log.Fatal(err)
		}

		toolScores, _ := toolUsageEval.Evaluate(ctx, suite.TestCases[i], result)
		scores = append(scores, toolScores...)

		keywordScores, _ := keywordEval.Evaluate(ctx, suite.TestCases[i], result)
		scores = append(scores, keywordScores...)

		allScores[i] = scores
	}
	fmt.Println("✓ Evaluated all results")

	// 8. Generate report
	reportGen := eval.NewReportGenerator()
	markdown, err := reportGen.GenerateMarkdown(*suite, results, allScores)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("✓ Generated markdown report")
	fmt.Println("\n--- Report Preview ---")
	lines := splitLines(markdown)
	if len(lines) > 20 {
		fmt.Println(strings.Join(lines[:20], "\n"))
		fmt.Println("... (truncated)")
	} else {
		fmt.Println(markdown)
	}

	// 9. Generate JSON report for CI
	jsonReport, err := reportGen.GenerateJSON(*suite, results, allScores)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\n✓ Generated JSON report for CI")
	fmt.Printf("JSON report size: %d bytes\n", len(jsonReport))
}

// CalculatorToolFactory is an example tool factory.
type CalculatorToolFactory struct{}

func (f *CalculatorToolFactory) Name() string {
	return "calculator"
}

func (f *CalculatorToolFactory) Description() string {
	return "A simple calculator tool for arithmetic operations"
}

func (f *CalculatorToolFactory) Create(config map[string]interface{}) (core.Tool, error) {
	return &CalculatorTool{precision: 2}, nil
}

func (f *CalculatorToolFactory) ValidateConfig(config map[string]interface{}) error {
	return nil // Accept any config
}

// CalculatorTool is an example tool.
type CalculatorTool struct {
	precision int
}

func (t *CalculatorTool) Name() string                    { return "calculator" }
func (t *CalculatorTool) Description() string             { return "Performs arithmetic calculations" }
func (t *CalculatorTool) Category() core.ToolCategory     { return core.CategoryCore }
func (t *CalculatorTool) Capabilities() []core.Capability { return nil }
func (t *CalculatorTool) Parameters() *core.ParameterSchema {
	return &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"expression": {Type: "string", Description: "Mathematical expression to evaluate"},
		},
		Required: []string{"expression"},
	}
}
func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	return core.NewResult(true, "42"), nil
}

// MockAgentExecutor is a mock agent executor for demo.
type MockAgentExecutor struct{}

func (e *MockAgentExecutor) Execute(ctx context.Context, input string) (output string, toolsUsed []string, tokensUsed int, err error) {
	// Simulate agent execution
	time.Sleep(100 * time.Millisecond)
	return "This is a mock response about AI and technology analysis.", []string{"web_search", "analyzer"}, 150, nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
