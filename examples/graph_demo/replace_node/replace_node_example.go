// Package main demonstrates MutableDAG.ReplaceNode for dynamic step replacement.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

type stepAgent struct {
	id        string
	agentType string
	fn        func(ctx context.Context, input any) (any, error)
}

func (a *stepAgent) ID() string                                          { return a.id }
func (a *stepAgent) Type() models.AgentType                              { return models.AgentType(a.agentType) }
func (a *stepAgent) Status() models.AgentStatus                          { return models.AgentStatusReady }
func (a *stepAgent) Start(ctx context.Context) error                     { return nil }
func (a *stepAgent) Stop(ctx context.Context) error                      { return nil }
func (a *stepAgent) Process(ctx context.Context, input any) (any, error) { return a.fn(ctx, input) }
func (a *stepAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	return nil, errors.New("streaming not supported")
}

func makeResult(desc string) *models.RecommendResult {
	return &models.RecommendResult{Items: []*models.RecommendItem{{Description: desc}}}
}

func main() {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("worker", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &stepAgent{id: "worker-instance", agentType: "worker",
			fn: func(ctx context.Context, input any) (any, error) {
				return makeResult("processed: " + fmt.Sprint(input)), nil
			},
		}, nil
	})

	ctx := context.Background()
	dag, _ := engine.NewMutableDAG([]*engine.Step{
		{ID: "s1", Name: "Fetch Data", AgentType: "worker", Input: "fetch"},
		{ID: "s2", Name: "Transform", AgentType: "worker", Input: "transform", DependsOn: []string{"s1"}},
		{ID: "s3", Name: "Load", AgentType: "worker", Input: "load", DependsOn: []string{"s2"}},
	})

	// Subscribe before mutations to capture all events.
	sub := dag.Subscribe()
	drainEvents := func() {
		for {
			select {
			case ev := <-sub:
				fmt.Printf("  Event: type=%d node=%q oldNode=%q success=%v\n",
					ev.Change.Type, ev.Change.NodeID, ev.Change.OldNodeID, ev.Success)
			default:
				return
			}
		}
	}

	fmt.Println("=== Before ReplaceNode ===")
	order, _ := dag.GetExecutionOrder()
	fmt.Printf("Execution order: %v\n", order)

	// Replace s2 with an updated version that has a different ID.
	replacement := &engine.Step{
		ID:        "s2_v2",
		Name:      "Transform v2",
		AgentType: "worker",
		Input:     "transform_v2",
		DependsOn: []string{"s1"},
	}

	fmt.Println("\n--- Replace s2 → s2_v2 (different ID) ---")
	if err := dag.ReplaceNode(ctx, "s2", replacement); err != nil {
		log.Fatalf("ReplaceNode failed: %v", err)
	}
	drainEvents()

	fmt.Println("\n=== After ReplaceNode ===")
	order, _ = dag.GetExecutionOrder()
	fmt.Printf("Execution order: %v\n", order)
	for _, s := range dag.Steps() {
		fmt.Printf("  Step %q (name=%q, dependsOn=%v)\n", s.ID, s.Name, s.DependsOn)
	}

	// Demonstrate in-place replacement (same ID).
	fmt.Println("\n--- Replace s3 → s3 v2 (same ID, in-place) ---")
	updated := &engine.Step{
		ID:        "s3",
		Name:      "Load v2",
		AgentType: "worker",
		Input:     "load_v2",
		DependsOn: []string{"s2_v2"},
	}
	if err := dag.ReplaceNode(ctx, "s3", updated); err != nil {
		log.Fatalf("ReplaceNode (same ID) failed: %v", err)
	}
	drainEvents()
	for _, s := range dag.Steps() {
		fmt.Printf("  Step %q (name=%q, dependsOn=%v)\n", s.ID, s.Name, s.DependsOn)
	}

	fmt.Println("\nReplaceNode example completed successfully!")
}
