// Package main demonstrates a real-world integration example using the graph system.
// This example shows a complete workflow for processing customer support tickets.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/api/service/graph"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/observability"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

// SupportTicket represents a customer support ticket
type SupportTicket struct {
	ID       string
	Category string
	Priority string
	Message  string
}

// TicketClassifier is an agent that classifies tickets
type TicketClassifier struct {
	id string
}

func (t *TicketClassifier) Process(ctx context.Context, input any) (any, error) {
	ticket, ok := input.(*SupportTicket)
	if !ok {
		return nil, fmt.Errorf("invalid input type")
	}

	// Simple classification logic
	category := "general"
	if contains(ticket.Message, []string{"payment", "billing", "invoice"}) {
		category = "billing"
	} else if contains(ticket.Message, []string{"login", "password", "account"}) {
		category = "account"
	} else if contains(ticket.Message, []string{"bug", "error", "crash"}) {
		category = "technical"
	}

	ticket.Category = category
	return ticket, nil
}

// ProcessStream handles input and returns a stream of events.
func (t *TicketClassifier) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := t.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func (t *TicketClassifier) ID() string {
	return t.id
}

func (t *TicketClassifier) Type() models.AgentType {
	return models.AgentTypeLeader
}

func (t *TicketClassifier) Status() models.AgentStatus {
	return models.AgentStatusReady
}

func (t *TicketClassifier) Start(ctx context.Context) error {
	return nil
}

func (t *TicketClassifier) Stop(ctx context.Context) error {
	return nil
}

// PriorityAnalyzer is an agent that determines ticket priority
type PriorityAnalyzer struct {
	id string
}

func (p *PriorityAnalyzer) Process(ctx context.Context, input any) (any, error) {
	ticket, ok := input.(*SupportTicket)
	if !ok {
		return nil, fmt.Errorf("invalid input type")
	}

	// Priority analysis
	priority := "low"
	if contains(ticket.Message, []string{"urgent", "emergency", "critical"}) {
		priority = "high"
	} else if ticket.Category == "technical" {
		priority = "medium"
	}

	ticket.Priority = priority
	return ticket, nil
}

// ProcessStream handles input and returns a stream of events.
func (p *PriorityAnalyzer) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := p.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func (p *PriorityAnalyzer) ID() string {
	return p.id
}

func (p *PriorityAnalyzer) Type() models.AgentType {
	return models.AgentTypeTop
}

func (p *PriorityAnalyzer) Status() models.AgentStatus {
	return models.AgentStatusReady
}

func (p *PriorityAnalyzer) Start(ctx context.Context) error {
	return nil
}

func (p *PriorityAnalyzer) Stop(ctx context.Context) error {
	return nil
}

// TicketRouter routes tickets to appropriate handlers
type TicketRouter struct {
	id string
}

func (r *TicketRouter) Process(ctx context.Context, input any) (any, error) {
	ticket, ok := input.(*SupportTicket)
	if !ok {
		return nil, fmt.Errorf("invalid input type")
	}

	handler := "general_team"
	switch ticket.Category {
	case "billing":
		handler = "billing_team"
	case "account":
		handler = "account_team"
	case "technical":
		handler = "technical_team"
	}

	return fmt.Sprintf("Ticket %s routed to %s (priority: %s)", ticket.ID, handler, ticket.Priority), nil
}

// ProcessStream handles input and returns a stream of events.
func (r *TicketRouter) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	result, err := r.Process(ctx, input)
	ch := make(chan base.AgentEvent, 1)
	ch <- base.AgentEvent{Type: base.EventComplete, Data: result, Err: err}
	close(ch)
	return ch, nil
}

func (r *TicketRouter) ID() string {
	return r.id
}

func (r *TicketRouter) Type() models.AgentType {
	return models.AgentTypeBottom
}

func (r *TicketRouter) Status() models.AgentStatus {
	return models.AgentStatusReady
}

func (r *TicketRouter) Start(ctx context.Context) error {
	return nil
}

func (r *TicketRouter) Stop(ctx context.Context) error {
	return nil
}

func contains(text string, keywords []string) bool {
	for _, kw := range keywords {
		if len(text) >= len(kw) {
			for i := 0; i <= len(text)-len(kw); i++ {
				if text[i:i+len(kw)] == kw {
					return true
				}
			}
		}
	}
	return false
}

func main() {
	// Create graph service with observability
	service, err := graph.NewService(&graph.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("failed to create service: %v", err)
	}

	fmt.Println("=== Customer Support Ticket Processing System ===")
	fmt.Println()

	// Create agents
	classifier := &TicketClassifier{id: "classifier"}
	priorityAnalyzer := &PriorityAnalyzer{id: "priority_analyzer"}
	router := &TicketRouter{id: "router"}

	// Build support ticket processing workflow
	g, err2 := wfgraph.NewGraph("support-workflow")
	if err2 != nil {
		log.Fatalf("failed to create graph: %v", err2)
	}
	addNode := func(id string, n wfgraph.Node) {
		if _, err := g.Node(id, n); err != nil {
			log.Fatalf("failed to add node %s: %v", id, err)
		}
	}
	addEdge := func(from, to string) {
		if _, err := g.Edge(from, to); err != nil {
			log.Fatalf("failed to add edge %s->%s: %v", from, to, err)
		}
	}
	newFuncNode := func(id string, fn func(context.Context, *wfgraph.State) error) wfgraph.Node {
		n, err := wfgraph.NewFuncNode(id, fn)
		if err != nil {
			log.Fatalf("failed to create func node: %v", err)
		}
		return n
	}
	mustAgentNode := func(id string, agent base.Agent) {
		n, err := wfgraph.NewAgentNode(agent)
		if err != nil {
			log.Fatalf("failed to create agent node %s: %v", id, err)
		}
		addNode(id, n)
	}

	addNode("validate", newFuncNode("validate", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("1. Validating ticket...")
		ticketVal, _ := state.Get("ticket")
		ticket := ticketVal.(*SupportTicket)
		if ticket.Message == "" {
			return fmt.Errorf("ticket message cannot be empty")
		}
		fmt.Printf("   ✓ Ticket %s validated\n", ticket.ID)
		state.Set("input", ticket)
		return nil
	}))
	mustAgentNode("classify", classifier)
	mustAgentNode("prioritize", priorityAnalyzer)
	mustAgentNode("route", router)
	addNode("log", newFuncNode("log", func(ctx context.Context, state *wfgraph.State) error {
		fmt.Println("4. Logging ticket resolution...")
		result, _ := state.Get("node.router")
		fmt.Printf("   ✓ %s\n", result)
		return nil
	}))
	addEdge("validate", "classify")
	addEdge("classify", "prioritize")
	addEdge("prioritize", "route")
	addEdge("route", "log")
	if _, err := g.Start("validate"); err != nil {
		log.Fatalf("failed to set start: %v", err)
	}

	// Process sample tickets
	tickets := []*SupportTicket{
		{
			ID:      "TICKET-001",
			Message: "I cannot login to my account, password reset not working",
		},
		{
			ID:      "TICKET-002",
			Message: "Payment failed for order #12345, please help urgent",
		},
		{
			ID:      "TICKET-003",
			Message: "App crashes when I try to upload files",
		},
	}

	for _, ticket := range tickets {
		fmt.Printf("\n--- Processing %s ---\n", ticket.ID)
		fmt.Printf("Message: %s\n", ticket.Message)

		request := &graph.ExecuteRequest{
			GraphID: "support-workflow",
			State: map[string]any{
				"ticket": ticket,
			},
		}

		response, err := service.Execute(context.Background(), g, request)
		if err != nil {
			log.Printf("Execution failed: %v\n", err)
			continue
		}

		fmt.Printf("\nResult: %s\n", response.State["node.router"])
		fmt.Printf("Duration: %v\n", response.Duration)
	}

	fmt.Println("\n=== All tickets processed successfully! ===")
}
