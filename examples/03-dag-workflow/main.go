// DAG workflow — demonstrates graph-based workflow with linear and conditional execution,
// plus the new DAG flexible features: conditional routing, controlled loops, subgraph nesting.
//
// Run:
//
//	go run examples/03-dag-workflow/main.go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ── Inline agent for engine package demos ──────────────────────────

// demoAgent is a minimal Agent implementation used by the engine examples.
type demoAgent struct {
	id        string
	agentType string
	process   func(ctx context.Context, input any) (any, error)
}

func (a *demoAgent) ID() string                    { return a.id }
func (a *demoAgent) Type() models.AgentType        { return models.AgentType(a.agentType) }
func (a *demoAgent) Status() models.AgentStatus    { return models.AgentStatusReady }
func (a *demoAgent) Start(_ context.Context) error { return nil }
func (a *demoAgent) Stop(_ context.Context) error  { return nil }
func (a *demoAgent) Process(ctx context.Context, input any) (any, error) {
	return a.process(ctx, input)
}
func (a *demoAgent) ProcessStream(_ context.Context, _ any) (<-chan base.AgentEvent, error) {
	return nil, nil // nolint: nilnil // stream not supported for demo agent
}

func main() {
	ctx := context.Background()

	// ── DAG Flexible Features (engine package) ──
	engineDemo(ctx)
	loopDemo(ctx)
	subgraphDemo(ctx)

	fmt.Println("\n✅ All DAG workflow demos completed")
}

// engineDemo demonstrates Condition + Router using the engine package.
func engineDemo(ctx context.Context) {
	fmt.Println("\n═══ Condition + Router (engine package) ═══")

	registry := wfengine.NewAgentRegistry()
	executor := wfengine.NewExecutor(registry)

	// Register a simple agent that returns its input as-is.
	_ = registry.Register("echo-agent", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &demoAgent{
			id:        "echo",
			agentType: "echo-agent",
			process: func(_ context.Context, input any) (any, error) {
				return &models.RecommendResult{Items: []*models.RecommendItem{
					{ItemID: "r1", Name: fmt.Sprintf("echo:%v", input), Description: "result", Price: 100},
				}}, nil
			},
		}, nil
	})

	// Workflow: ingest → process (skip if mode=skip) → finalize
	workflow := &wfengine.Workflow{
		ID:   "wf-condition",
		Name: "Conditional Skip Demo",
		Steps: []*wfengine.Step{
			{ID: "ingest", Name: "Ingest", AgentType: "echo-agent", Input: "data"},
			{
				ID: "process", Name: "Process", AgentType: "echo-agent",
				DependsOn: []string{"ingest"},
				Condition: func(vars map[string]any) bool {
					mode, _ := vars["mode"].(string)
					return mode != "skip"
				},
			},
			{
				ID: "finalize", Name: "Finalize", AgentType: "echo-agent",
				DependsOn: []string{"process"},
			},
		},
	}

	// Run with mode=skip — the "process" step should be skipped.
	result, err := executor.Execute(ctx, workflow, "input")
	if err != nil {
		log.Fatalf("engine demo failed: %v", err)
	}
	for _, s := range result.Steps {
		status := "✅"
		if s.Status == wfengine.StepStatusSkipped {
			status = "⏭️"
		}
		fmt.Printf("  %s %s (%s)\n", status, s.StepID, s.Status)
	}

	// Re-run with mode=skip to demonstrate condition-triggered skip.
	workflow.Variables = map[string]string{"mode": "skip"}
	skipResult, skipErr := executor.Execute(ctx, workflow, "input")
	if skipErr != nil {
		log.Fatalf("engine demo (skip) failed: %v", skipErr)
	}
	fmt.Println("  --- With mode=skip ---")
	for _, s := range skipResult.Steps {
		status := "✅"
		if s.Status == wfengine.StepStatusSkipped {
			status = "⏭️"
		}
		fmt.Printf("  %s %s (%s)\n", status, s.StepID, s.Status)
	}

	// ── Dynamic Routing with Router callback ─────────────
	fmt.Println("  --- Routing demo ---")

	routerWorkflow := &wfengine.Workflow{
		ID:   "wf-router",
		Name: "Router Demo",
		Steps: []*wfengine.Step{
			{
				ID: "classify", Name: "Classify", AgentType: "echo-agent",
				Router: func(_ context.Context, _ string, _ map[string]any, output string) string {
					// Route based on the step output — choose path_a or path_b.
					// In a real system this decision would be driven by LLM output.
					return "path_b"
				},
			},
			{ID: "path_a", Name: "Path A", AgentType: "echo-agent", DependsOn: []string{"classify"}},
			{ID: "path_b", Name: "Path B", AgentType: "echo-agent", DependsOn: []string{"classify"}},
		},
	}

	rResult, rErr := executor.Execute(ctx, routerWorkflow, "classify me")
	if rErr != nil {
		log.Fatalf("router demo failed: %v", rErr)
	}
	for _, s := range rResult.Steps {
		fmt.Printf("  [%s] status=%s\n", s.StepID, s.Status)
	}
}

// loopDemo demonstrates controlled loops with LoopConfig.
func loopDemo(ctx context.Context) {
	fmt.Println("\n═══ Controlled Loop (engine package) ═══")

	registry := wfengine.NewAgentRegistry()
	executor := wfengine.NewExecutor(registry)

	_ = registry.Register("loop-agent", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &demoAgent{
			id:        "loop",
			agentType: "loop-agent",
			process: func(_ context.Context, input any) (any, error) {
				return &models.RecommendResult{Items: []*models.RecommendItem{
					{ItemID: "loop", Name: fmt.Sprintf("iter:%v", input), Description: "loop", Price: 100},
				}}, nil
			},
		}, nil
	})

	// Workflow with a loop body that repeats up to 3 times.
	workflow := &wfengine.Workflow{
		ID:   "wf-loop",
		Name: "Controlled Loop Demo",
		Steps: []*wfengine.Step{
			{ID: "collect", Name: "Collect", AgentType: "loop-agent"},
			{ID: "process", Name: "Process", AgentType: "loop-agent", DependsOn: []string{"collect"}},
		},
		LoopConfig: &wfengine.LoopConfig{
			MaxIterations: 3,
			LoopSteps:     []string{"collect", "process"},
		},
	}

	result, err := executor.Execute(ctx, workflow, "loop input")
	if err != nil {
		log.Fatalf("loop demo failed: %v", err)
	}

	fmt.Printf("  Total steps executed: %d (expected 6 = 3 iterations × 2 steps)\n", len(result.Steps))
	for _, s := range result.Steps {
		fmt.Printf("  [%s] status=%s\n", s.StepID, s.Status)
	}
}

// subgraphDemo demonstrates sub-workflow nesting.
func subgraphDemo(ctx context.Context) {
	fmt.Println("\n═══ Subgraph Nesting (engine package) ═══")

	registry := wfengine.NewAgentRegistry()
	executor := wfengine.NewExecutor(registry)

	_ = registry.Register("sub-agent", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &demoAgent{
			id:        "sub",
			agentType: "sub-agent",
			process: func(_ context.Context, input any) (any, error) {
				return &models.RecommendResult{Items: []*models.RecommendItem{
					{ItemID: "sub", Name: fmt.Sprintf("sub:%v", input), Description: "result", Price: 100},
				}}, nil
			},
		}, nil
	})

	// Define a reusable sub-workflow for data validation.
	subWorkflow := &wfengine.Workflow{
		ID:   "sub-validate",
		Name: "Data Validation",
		Steps: []*wfengine.Step{
			{ID: "check_format", Name: "Check Format", AgentType: "sub-agent"},
			{ID: "enrich", Name: "Enrich Data", AgentType: "sub-agent", DependsOn: []string{"check_format"}},
		},
	}

	// Parent workflow that uses the sub-workflow as a step.
	parentWorkflow := &wfengine.Workflow{
		ID:   "wf-parent",
		Name: "Parent with Sub-workflow",
		Steps: []*wfengine.Step{
			{ID: "receive", Name: "Receive", AgentType: "sub-agent"},
			{
				ID:          "validate_step",
				Name:        "Validate",
				SubWorkflow: subWorkflow, // <-- nested sub-workflow
				DependsOn:   []string{"receive"},
			},
			{ID: "respond", Name: "Respond", AgentType: "sub-agent", DependsOn: []string{"validate_step"}},
		},
	}

	result, err := executor.Execute(ctx, parentWorkflow, "incoming data")
	if err != nil {
		log.Fatalf("subgraph demo failed: %v", err)
	}

	fmt.Printf("  Parent steps: %d (sub-workflow counts as 1 parent step)\n", len(result.Steps))
	for _, s := range result.Steps {
		fmt.Printf("  [%s] status=%s\n", s.StepID, s.Status)
	}
}
