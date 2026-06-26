//go:build ignore

// Example usage of the ARES Runtime Plugin System (PluginBus, EventBus, WorkflowHook)
// and the Evaluation Framework.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/eval"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

func main() {
	ctx := context.Background()

	fmt.Println("=== Runtime Plugin System Example ===")
	examplePluginSystem(ctx)

	fmt.Println("\n=== Evaluation Framework Example ===")
	exampleEvaluationFramework(ctx)
}

// examplePluginSystem demonstrates PluginBus with ObserverPlugin and ToolPlugin,
// event observation via MemoryEventStore, and direct EventBus subscription.
func examplePluginSystem(ctx context.Context) {
	// 1. Create an agent registry with a simple worker agent.
	registry := engine.NewAgentRegistry()
	_ = registry.Register("worker", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &simpleAgent{
			id: "w", agentType: "worker",
			fn: func(ctx context.Context, input any) (any, error) {
				return &models.RecommendResult{Items: []*models.RecommendItem{
					{Description: "processed: " + fmt.Sprint(input)},
				}}, nil
			},
		}, nil
	})

	// 2. Create the runtime plugin bus and register plugins.
	//
	// ObserverPlugin: subscribes to all lifecycle events (workflow start/complete,
	// step start/complete, etc.) and writes them to a MemoryEventStore.
	eventStore := events.NewMemoryEventStore()
	observer := runtime.NewObserverPlugin("integration-observer", eventStore)

	// ToolPlugin: records tool invocations during step execution.
	toolPlugin := runtime.NewToolPlugin("integration-tools")

	bus := runtime.NewPluginBus()
	if err := bus.Register(observer); err != nil {
		log.Fatalf("register observer: %v", err)
	}
	if err := bus.Register(toolPlugin); err != nil {
		log.Fatalf("register tool plugin: %v", err)
	}

	if err := bus.Start(ctx); err != nil {
		log.Fatalf("start plugin bus: %v", err)
	}
	defer bus.Stop(ctx)

	fmt.Println("✓ Created PluginBus with ObserverPlugin and ToolPlugin")

	// 3. Subscribe to events directly via the EventBus.
	sub, err := bus.Subscribe(ctx, events.EventFilter{Types: []events.EventType{
		runtime.EventWorkflowStarted,
		runtime.EventWorkflowCompleted,
	}})
	if err != nil {
		log.Fatalf("subscribe: %v", err)
	}

	// 4. Wire the PluginBus into a DynamicExecutor.
	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint,
		engine.WithMaxParallel(1)).WithPluginBus(bus)

	// 5. Build a simple 2-step workflow.
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "s1", Name: "Step One", AgentType: "worker", Input: "hello"},
		{ID: "s2", Name: "Step Two", AgentType: "worker", Input: "world", DependsOn: []string{"s1"}},
	})
	if err != nil {
		log.Fatalf("NewMutableDAG: %v", err)
	}

	wf := &engine.Workflow{
		ID:    "integration-demo",
		Name:  "Integration Demo",
		Steps: dag.Steps(),
	}

	// 6. Execute the workflow.
	result, err := executor.ExecuteDynamic(ctx, wf, "start", dag)
	if err != nil {
		log.Fatalf("ExecuteDynamic: %v", err)
	}

	fmt.Printf("✓ Workflow executed: %s (status=%s, steps=%d)\n",
		result.ExecutionID, result.Status, len(result.Steps))

	// 7. Read events from the direct EventBus subscription.
	fmt.Println("\n--- Direct EventBus Subscription ---")
	select {
	case evt := <-sub:
		fmt.Printf("  Event: [%s] %s\n", evt.StreamID, evt.Type)
	case <-time.After(100 * time.Millisecond):
		fmt.Println("  No event received (already drained)")
	}

	// 8. Read all events from the MemoryEventStore (ObserverPlugin).
	fmt.Println("\n--- Observed Events (MemoryEventStore) ---")
	evts, err := eventStore.ReadAll(ctx, events.ReadOptions{Direction: events.ReadAscending})
	if err != nil {
		log.Printf("read events: %v", err)
	} else {
		fmt.Printf("  Total events: %d\n", len(evts))
		for _, e := range evts {
			execID, _ := e.Payload[runtime.PayloadKeyExecutionID].(string)
			fmt.Printf("  [%s] %s (execution=%s)\n", e.StreamID, e.Type, execID)
		}
	}

	// 9. Show the PluginBus capabilities.
	fmt.Println("\n--- PluginBus Capabilities ---")
	toolPlugins := bus.PluginsByCap(runtime.CapTool)
	fmt.Printf("  Plugins with CapTool: %d\n", len(toolPlugins))
	for _, p := range toolPlugins {
		fmt.Printf("    - %s\n", p.Name())
	}
	observerPlugins := bus.PluginsByCap(runtime.CapObserver)
	fmt.Printf("  Plugins with CapObserver: %d\n", len(observerPlugins))
	for _, p := range observerPlugins {
		fmt.Printf("    - %s\n", p.Name())
	}
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

// simpleAgent implements the base.Agent interface.
type simpleAgent struct {
	id        string
	agentType string
	fn        func(ctx context.Context, input any) (any, error)
}

func (a *simpleAgent) ID() string                                          { return a.id }
func (a *simpleAgent) Type() models.AgentType                              { return models.AgentType(a.agentType) }
func (a *simpleAgent) Status() models.AgentStatus                          { return models.AgentStatusReady }
func (a *simpleAgent) Start(ctx context.Context) error                     { return nil }
func (a *simpleAgent) Stop(ctx context.Context) error                      { return nil }
func (a *simpleAgent) Process(ctx context.Context, input any) (any, error) { return a.fn(ctx, input) }
func (a *simpleAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	return nil, nil
}

// MockAgentExecutor is a mock agent executor for demo.
type MockAgentExecutor struct{}

func (e *MockAgentExecutor) Execute(ctx context.Context, input string) (output string, toolsUsed []string, tokensUsed int, err error) {
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