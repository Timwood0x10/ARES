// Package main demonstrates agent integration with graphservice.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/core/models"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
	"github.com/Timwood0x10/ares/internal/workflow/graphservice"
)

// mockAgent simulates an agent for demonstration
type mockAgent struct {
	id   string
	name string
}

func (m *mockAgent) Process(ctx context.Context, input any) (any, error) {
	fmt.Printf("  [Agent %s] Processing...\n", m.name)

	inputStr, ok := input.(string)
	if !ok {
		inputStr = fmt.Sprintf("%v", input)
	}

	result := fmt.Sprintf("[Agent %s] Processed: %s", m.name, inputStr)
	return result, nil
}

// ProcessStream handles input and returns a stream of events.
func (m *mockAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := m.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func (m *mockAgent) ID() string {
	return m.id
}

func (m *mockAgent) Type() models.AgentType {
	return models.AgentTypeLeader
}

func (m *mockAgent) Status() models.AgentStatus {
	return models.AgentStatusReady
}

func (m *mockAgent) Start(ctx context.Context) error {
	fmt.Printf("  [Agent %s] Started\n", m.name)
	return nil
}

func (m *mockAgent) Stop(ctx context.Context) error {
	fmt.Printf("  [Agent %s] Stopped\n", m.name)
	return nil
}

func main() {
	// Create graph service
	service, err := graphservice.NewService(&graphservice.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         ares_observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	fmt.Println("=== Agent Integration Example ===")

	// Create agents
	collectorAgent := &mockAgent{id: "collector", name: "Data Collector"}
	analyzerAgent := &mockAgent{id: "analyzer", name: "Data Analyzer"}
	aggregatorAgent := &mockAgent{id: "aggregator", name: "Data Aggregator"}

	// Build graph with agents
	g, err := wfgraph.NewGraph("agent-pipeline")
	if err != nil {
		log.Fatalf("failed to create graph: %v", err)
	}
	n1, err := wfgraph.NewFuncNode("collect", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("1. Collecting data from external sources...")
		state.Set("data", "sample data from API")
		return nil
	})
	if err != nil {
		log.Fatalf("failed to create node: %v", err)
	}
	n2, err := wfgraph.NewAgentNode(collectorAgent)
	if err != nil {
		log.Fatalf("failed to create agent node: %v", err)
	}
	n3, err := wfgraph.NewAgentNode(analyzerAgent)
	if err != nil {
		log.Fatalf("failed to create agent node: %v", err)
	}
	n4, err := wfgraph.NewAgentNode(aggregatorAgent)
	if err != nil {
		log.Fatalf("failed to create agent node: %v", err)
	}
	mustOp := func(_ *wfgraph.Graph, err error) {
		if err != nil {
			log.Fatalf("graph operation failed: %v", err)
		}
	}
	mustOp(g.Node("collect", n1))
	mustOp(g.Node("agent_collector", n2))
	mustOp(g.Node("agent_analyzer", n3))
	mustOp(g.Node("agent_aggregator", n4))
	mustOp(g.Edge("collect", "agent_collector"))
	mustOp(g.Edge("agent_collector", "agent_analyzer"))
	mustOp(g.Edge("agent_analyzer", "agent_aggregator"))
	mustOp(g.Start("collect"))

	// Execute graph
	request := &graphservice.ExecuteRequest{
		GraphID: "agent-pipeline",
		State: map[string]any{
			"input": "collect user activity logs",
		},
	}

	response, err := service.Execute(context.Background(), g, request)
	if err != nil {
		log.Fatalf("execution failed: %v", err)
	}

	// Print results
	fmt.Printf("\nGraph ID: %s\n", response.GraphID)
	fmt.Printf("Duration: %v\n", response.Duration)
	fmt.Printf("Final State:\n")
	for key, value := range response.State {
		fmt.Printf("  %s: %v\n", key, value)
	}

	fmt.Println("\nAgent integration example completed successfully!")
}
