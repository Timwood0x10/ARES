// Package main demonstrates agent integration with graph.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/goagent/api/service/graph"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/observability"
	wfgraph "github.com/Timwood0x10/goagent/internal/workflow/graph"
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
	service, err := graph.NewService(&graph.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         observability.NewLogTracer(nil),
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
	g := wfgraph.NewGraph("agent-pipeline").
		Node("collect", wfgraph.NewFuncNode("collect", func(ctx context.Context, state *wfgraph.State) error {
			fmt.Println("1. Collecting data from external sources...")
			state.Set("data", "sample data from API")
			return nil
		})).
		Node("agent_collector", wfgraph.NewAgentNode(collectorAgent)).
		Node("agent_analyzer", wfgraph.NewAgentNode(analyzerAgent)).
		Node("agent_aggregator", wfgraph.NewAgentNode(aggregatorAgent)).
		Edge("collect", "agent_collector").
		Edge("agent_collector", "agent_analyzer").
		Edge("agent_analyzer", "agent_aggregator").
		Start("collect")

	// Execute graph
	request := &graph.ExecuteRequest{
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
