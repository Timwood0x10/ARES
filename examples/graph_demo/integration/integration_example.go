// Package main demonstrates a real-world integration example using the graph
// system with the full runtime plugin stack. It shows a customer support
// ticket processing workflow with checkpoint persistence, event observation,
// tool recording, and recovery — all wired through PluginBus.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ────────────────────────────────────────────────────────────────────────────
// Domain model
// ────────────────────────────────────────────────────────────────────────────

// SupportTicket represents a customer support ticket.
type SupportTicket struct {
	ID       string
	Category string
	Priority string
	Message  string
}

// ────────────────────────────────────────────────────────────────────────────
// Agents
// ────────────────────────────────────────────────────────────────────────────

// simpleAgent is a minimal base.Agent implementation for demonstration.
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

func makeResult(desc string) *models.RecommendResult {
	return &models.RecommendResult{Items: []*models.RecommendItem{{Description: desc}}}
}

func contains(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// ────────────────────────────────────────────────────────────────────────────
// In-memory checkpoint store (for demonstration)
// ────────────────────────────────────────────────────────────────────────────

type memoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryStore() *memoryStore {
	return &memoryStore{data: make(map[string][]byte)}
}

func (s *memoryStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

func (s *memoryStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key], nil
}

// ────────────────────────────────────────────────────────────────────────────
// Plugin stack — one abstraction to wire everything
// ────────────────────────────────────────────────────────────────────────────

// runtimeStack bundles all runtime plugins behind a single Start/Stop surface.
type runtimeStack struct {
	bus        *runtime.PluginBus
	collector  *runtime.ExecutionCollector
	checkpoint *runtime.CheckpointPlugin
	observer   *runtime.ObserverPlugin
	tool       *runtime.ToolPlugin
	recovery   *runtime.BasicRecoveryPlugin
	store      *memoryStore
}

// newRuntimeStack creates a fully-wired plugin stack for the given execution.
// Plugins communicate through the EventBus; no plugin references another directly.
func newRuntimeStack(executionID string) *runtimeStack {
	store := newMemoryStore()
	collector := runtime.NewExecutionCollector(executionID)

	checkpoint := runtime.NewCheckpointPlugin("checkpoint", store).
		WithCollector(collector).
		WithFlushInterval(1)

	observer := runtime.NewObserverPlugin("observer", events.NewMemoryEventStore())

	tool := runtime.NewToolPlugin("tool").
		WithCollector(collector)

	recovery := runtime.NewBasicRecoveryPlugin("recovery")

	return &runtimeStack{
		collector:  collector,
		checkpoint: checkpoint,
		observer:   observer,
		tool:       tool,
		recovery:   recovery,
		store:      store,
	}
}

// Start creates the bus, registers all plugins, and starts them.
func (s *runtimeStack) Start(ctx context.Context) error {
	s.bus = runtime.NewPluginBus(
		runtime.WithPluginTimeout(10 * time.Second),
	)
	for _, p := range []runtime.RuntimePlugin{s.checkpoint, s.observer, s.tool, s.recovery} {
		if err := s.bus.Register(p); err != nil {
			return fmt.Errorf("register %s: %w", p.Name(), err)
		}
	}
	return s.bus.Start(ctx)
}

// Stop shuts down all plugins in reverse order.
func (s *runtimeStack) Stop(ctx context.Context) error {
	return s.bus.Stop(ctx)
}

// Bus returns the PluginBus for engine integration.
func (s *runtimeStack) Bus() *runtime.PluginBus { return s.bus }

// Collector returns the execution collector for manual recording.
func (s *runtimeStack) Collector() *runtime.ExecutionCollector { return s.collector }

// ────────────────────────────────────────────────────────────────────────────
// Workflow definition
// ────────────────────────────────────────────────────────────────────────────

// buildWorkflow creates the support ticket processing DAG.
func buildWorkflow() (*engine.MutableDAG, error) {
	return engine.NewMutableDAG([]*engine.Step{
		{ID: "validate", Name: "Validate Ticket", AgentType: "validator", Input: "raw"},
		{ID: "classify", Name: "Classify Category", AgentType: "classifier", Input: "validated", DependsOn: []string{"validate"}},
		{ID: "prioritize", Name: "Analyze Priority", AgentType: "prioritizer", Input: "classified", DependsOn: []string{"classify"}},
		{ID: "route", Name: "Route to Team", AgentType: "router", Input: "prioritized", DependsOn: []string{"prioritize"}},
		{ID: "log", Name: "Log Resolution", AgentType: "logger", Input: "routed", DependsOn: []string{"route"}},
	})
}

// buildRegistry creates agents and registers them with the engine registry.
func buildRegistry(ticket *SupportTicket) *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()

	mustRegister := func(name string, fn func(context.Context, interface{}) (base.Agent, error)) {
		if err := registry.Register(name, fn); err != nil {
			log.Fatalf("register agent %s: %v", name, err)
		}
	}

	mustRegister("validator", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "validator", agentType: "validator",
			fn: func(_ context.Context, _ any) (any, error) {
				if ticket.Message == "" {
					return nil, fmt.Errorf("ticket message cannot be empty")
				}
				fmt.Printf("   ✓ Validated %s\n", ticket.ID)
				return makeResult("validated"), nil
			}}, nil
	})

	mustRegister("classifier", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "classifier", agentType: "classifier",
			fn: func(_ context.Context, _ any) (any, error) {
				ticket.Category = "general"
				if contains(ticket.Message, []string{"payment", "billing", "invoice"}) {
					ticket.Category = "billing"
				} else if contains(ticket.Message, []string{"login", "password", "account"}) {
					ticket.Category = "account"
				} else if contains(ticket.Message, []string{"bug", "error", "crash"}) {
					ticket.Category = "technical"
				}
				fmt.Printf("   ✓ Classified as %s\n", ticket.Category)
				return makeResult("category:" + ticket.Category), nil
			}}, nil
	})

	mustRegister("prioritizer", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "prioritizer", agentType: "prioritizer",
			fn: func(_ context.Context, _ any) (any, error) {
				ticket.Priority = "low"
				if contains(ticket.Message, []string{"urgent", "emergency", "critical"}) {
					ticket.Priority = "high"
				} else if ticket.Category == "technical" {
					ticket.Priority = "medium"
				}
				fmt.Printf("   ✓ Priority: %s\n", ticket.Priority)
				return makeResult("priority:" + ticket.Priority), nil
			}}, nil
	})

	mustRegister("router", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "router", agentType: "router",
			fn: func(_ context.Context, _ any) (any, error) {
				handler := "general_team"
				switch ticket.Category {
				case "billing":
					handler = "billing_team"
				case "account":
					handler = "account_team"
				case "technical":
					handler = "technical_team"
				}
				msg := fmt.Sprintf("Ticket %s → %s (priority: %s)", ticket.ID, handler, ticket.Priority)
				fmt.Printf("   ✓ %s\n", msg)
				return makeResult(msg), nil
			}}, nil
	})

	mustRegister("logger", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "logger", agentType: "logger",
			fn: func(_ context.Context, _ any) (any, error) {
				fmt.Printf("   ✓ Logged resolution for %s\n", ticket.ID)
				return makeResult("logged"), nil
			}}, nil
	})

	return registry
}

// ────────────────────────────────────────────────────────────────────────────
// Main
// ────────────────────────────────────────────────────────────────────────────

func main() {
	fmt.Println("=== Customer Support Ticket Processing System ===")
	fmt.Println("Powered by DynamicExecutor + Runtime Plugin Stack")
	fmt.Println()

	tickets := []*SupportTicket{
		{ID: "TICKET-001", Message: "I cannot login to my account, password reset not working"},
		{ID: "TICKET-002", Message: "Payment failed for order #12345, please help urgent"},
		{ID: "TICKET-003", Message: "App crashes when I try to upload files"},
	}

	for i, ticket := range tickets {
		fmt.Printf("--- Processing %s ---\n", ticket.ID)
		fmt.Printf("Message: %s\n\n", ticket.Message)

		if err := processTicket(ticket, i); err != nil {
			log.Printf("FAILED: %v\n", err)
			continue
		}

		fmt.Println()
	}

	fmt.Println("=== All tickets processed successfully! ===")
}

// processTicket runs a single ticket through the full plugin-equipped workflow.
func processTicket(ticket *SupportTicket, index int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Build workflow and registry.
	dag, err := buildWorkflow()
	if err != nil {
		return fmt.Errorf("build workflow: %w", err)
	}
	registry := buildRegistry(ticket)

	// Wire the runtime plugin stack.
	stack := newRuntimeStack(fmt.Sprintf("exec-%d", index))
	if err := stack.Start(ctx); err != nil {
		return fmt.Errorf("start plugins: %w", err)
	}
	defer func() { _ = stack.Stop(ctx) }()

	// Create executor with plugins attached.
	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint).
		WithPluginBus(stack.Bus())

	wf := &engine.Workflow{
		ID:    fmt.Sprintf("support-%s", ticket.ID),
		Name:  "Support Ticket Workflow",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(ctx, wf, ticket.ID, dag)
	if err != nil {
		return fmt.Errorf("execute: %w", err)
	}

	// Summarize execution from the plugin collector.
	fmt.Printf("\n  Execution %s: status=%s steps=%d\n",
		result.ExecutionID, result.Status, len(result.Steps))
	for _, s := range result.Steps {
		fmt.Printf("    %s: %s (%v)\n", s.StepID, s.Status, s.Duration)
	}

	c := stack.Collector()
	fmt.Printf("  Collector: routes=%d tools=%d memory=%d errors=%d\n",
		len(c.RouteHistory()), len(c.ToolHistory()),
		len(c.MemoryHits()), len(c.ErrorLog()))

	return nil
}
