// Package main demonstrates subscribing to GraphEventHub for graph mutation events.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

const agentTypeWorker = "worker"

type execAgent struct {
	id        string
	agentType string
	fn        func(ctx context.Context, input any) (any, error)
}

func (a *execAgent) ID() string                                          { return a.id }
func (a *execAgent) Type() models.AgentType                              { return models.AgentType(a.agentType) }
func (a *execAgent) Status() models.AgentStatus                          { return models.AgentStatusReady }
func (a *execAgent) Start(ctx context.Context) error                     { return nil }
func (a *execAgent) Stop(ctx context.Context) error                      { return nil }
func (a *execAgent) Process(ctx context.Context, input any) (any, error) { return a.fn(ctx, input) }
func (a *execAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	return nil, errors.New("streaming not supported")
}

func makeResult(desc string) *models.RecommendResult {
	return &models.RecommendResult{Items: []*models.RecommendItem{{Description: desc}}}
}

func main() {
	registry := engine.NewAgentRegistry()
	_ = registry.Register(agentTypeWorker, func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &execAgent{id: "w", agentType: agentTypeWorker,
			fn: func(ctx context.Context, input any) (any, error) {
				return makeResult("ok"), nil
			},
		}, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	dag, err := engine.NewMutableDAG(nil)
	if err != nil {
		cancel()
		log.Fatalf("NewMutableDAG: %v", err)
	}
	defer cancel()

	// Subscribe to all graph change events.
	ch := dag.Subscribe()

	// Print events from the subscription.
	go func() {
		for ev := range ch {
			ct := ev.Change.Type
			var typeName string
			switch ct {
			case engine.ChangeAddNode:
				typeName = "AddNode"
			case engine.ChangeRemoveNode:
				typeName = "RemoveNode"
			case engine.ChangeAddEdge:
				typeName = "AddEdge"
			case engine.ChangeRemoveEdge:
				typeName = "RemoveEdge"
			case engine.ChangeReplaceNode:
				typeName = "ReplaceNode"
			}
			fmt.Printf("[EVENT] %s node=%q oldNode=%q from=%q to=%q success=%v\n",
				typeName, ev.Change.NodeID, ev.Change.OldNodeID,
				ev.Change.FromID, ev.Change.ToID, ev.Success)
		}
	}()

	// Perform mutations.
	_ = dag.AddNode(ctx, &engine.Step{ID: "s1", Name: "Step 1", AgentType: agentTypeWorker, Input: "a"})
	fmt.Println("Added s1")

	_ = dag.AddNode(ctx, &engine.Step{ID: "s2", Name: "Step 2", AgentType: agentTypeWorker, Input: "b"})
	fmt.Println("Added s2")

	_ = dag.AddNode(ctx, &engine.Step{ID: "s3", Name: "Step 3", AgentType: agentTypeWorker, Input: "c"})
	fmt.Println("Added s3")

	_ = dag.AddEdge(ctx, "s1", "s2")
	fmt.Println("Added edge s1->s2")

	_ = dag.AddEdge(ctx, "s2", "s3")
	fmt.Println("Added edge s2->s3")

	_ = dag.ReplaceNode(ctx, "s2", &engine.Step{
		ID:        "s2_v2",
		Name:      "Step 2 v2",
		AgentType: agentTypeWorker,
		Input:     "b_v2",
		DependsOn: []string{"s1"},
	})
	fmt.Println("Replaced s2 -> s2_v2")

	_ = dag.RemoveEdge(ctx, "s2_v2", "s3")
	fmt.Println("Removed edge s2_v2->s3")

	_ = dag.RemoveNode(ctx, "s3")
	fmt.Println("Removed s3")

	order, _ := dag.GetExecutionOrder()
	fmt.Printf("Final order: %v\n", order)

	// Give the event goroutine time to print.
	time.Sleep(100 * time.Millisecond)

	fmt.Println("Subscribe example completed successfully!")
}
