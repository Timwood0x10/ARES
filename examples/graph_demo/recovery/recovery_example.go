// Package main demonstrates node-level recovery via StepRecoveryHandler
// combined with the full runtime plugin stack: CheckpointPlugin for crash
// recovery, ObserverPlugin for event logging, and ToolPlugin for tool-call
// recording — all wired through a single PluginBus.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// memoryStore is an in-memory CheckpointStore for demonstration.
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

// ────────────────────────────────────────────────────────────────────────────
// Plugin wiring — one function to compose the full stack
// ────────────────────────────────────────────────────────────────────────────

// pluginStack bundles the runtime plugins used in this example.
type pluginStack struct {
	bus        *runtime.PluginBus
	checkpoint *runtime.CheckpointPlugin
	collector  *runtime.ExecutionCollector
}

// newPluginStack creates and wires all plugins into a single PluginBus.
func newPluginStack() *pluginStack {
	store := newMemoryStore()
	collector := runtime.NewExecutionCollector("recovery-demo")

	// CheckpointPlugin: persists execution state at each step boundary.
	checkpoint := runtime.NewCheckpointPlugin("checkpoint", store).
		WithCollector(collector).
		WithFlushInterval(1)

	// ObserverPlugin: captures lifecycle events to an in-memory store.
	observer := runtime.NewObserverPlugin("observer", events.NewMemoryEventStore())

	// ToolPlugin: records tool invocations via the collector.
	tool := runtime.NewToolPlugin("tool").
		WithCollector(collector)

	bus := runtime.NewPluginBus()
	for _, p := range []runtime.RuntimePlugin{checkpoint, observer, tool} {
		if err := bus.Register(p); err != nil {
			log.Fatalf("register %s: %v", p.Name(), err)
		}
	}

	return &pluginStack{
		bus:        bus,
		checkpoint: checkpoint,
		collector:  collector,
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Recovery handler
// ────────────────────────────────────────────────────────────────────────────

type myRecoveryHandler struct{}

func (h *myRecoveryHandler) RecoverStep(_ context.Context, failure engine.StepFailure, _ *engine.MutableDAG) (*engine.RecoveryDecision, error) {
	fmt.Printf("  Recovery triggered for step %q (error: %s)\n", failure.StepID, failure.Error)
	return &engine.RecoveryDecision{
		Strategy: engine.RecoveryReplaceNode,
		NewStep: &engine.Step{
			ID:        failure.StepID + "_recovery",
			Name:      "Fetch (replacement)",
			AgentType: "ok",
			Input:     "retry_" + failure.StepID,
			DependsOn: []string{},
		},
	}, nil
}

// ────────────────────────────────────────────────────────────────────────────
// Main
// ────────────────────────────────────────────────────────────────────────────

func main() {
	// Agent registry: one that always fails, one that always succeeds.
	registry := engine.NewAgentRegistry()
	_ = registry.Register("fails", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "fails", agentType: "fails",
			fn: func(_ context.Context, _ any) (any, error) {
				return nil, fmt.Errorf("step failed")
			}}, nil
	})
	_ = registry.Register("ok", func(_ context.Context, _ interface{}) (base.Agent, error) {
		return &simpleAgent{id: "ok", agentType: "ok",
			fn: func(_ context.Context, input any) (any, error) {
				return makeResult("success: " + fmt.Sprint(input)), nil
			}}, nil
	})

	// Event sink records recovery lifecycle events.
	var emittedEvents []string
	eventSink := func(_ context.Context, evType events.EventType, _ map[string]any) {
		emittedEvents = append(emittedEvents, string(evType))
	}

	// Wire the full plugin stack.
	stack := newPluginStack()
	ctx := context.Background()
	if err := stack.bus.Start(ctx); err != nil {
		log.Fatalf("start plugin bus: %v", err)
	}
	defer stack.bus.Stop(ctx)

	// Build workflow: s1 uses a failing agent with a recovery policy.
	dag, err := engine.NewMutableDAG([]*engine.Step{
		{
			ID:        "s1",
			Name:      "Fetch (unreliable)",
			AgentType: "fails",
			Input:     "data",
			RecoveryPolicy: &engine.RecoveryPolicy{
				Strategy:         engine.RecoveryReplaceNode,
				ReplacementAgent: "ok",
			},
		},
		{ID: "s2", Name: "Process", AgentType: "ok", Input: "process", DependsOn: []string{"s1"}},
	})
	if err != nil {
		log.Fatalf("NewMutableDAG: %v", err)
	}

	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint).
		WithRecoveryHandler(&myRecoveryHandler{}).
		WithRecoveryEventSink(eventSink).
		WithPluginBus(stack.bus)

	wf := &engine.Workflow{
		ID:    "recovery-demo",
		Name:  "Recovery Demo",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(ctx, wf, "input", dag)
	if err != nil {
		log.Fatalf("ExecuteDynamic: %v", err)
	}

	// Print execution results.
	fmt.Printf("Workflow status: %s\n", result.Status)
	fmt.Printf("Steps executed: %d\n", len(result.Steps))
	for _, s := range result.Steps {
		fmt.Printf("  Step %q: status=%s output=%q\n", s.StepID, s.Status, s.Output)
	}
	fmt.Printf("Recovery events: %v\n", emittedEvents)

	// Show checkpoint data saved by CheckpointPlugin.
	if snap := stack.checkpoint.Snapshot(result.ExecutionID); snap != nil {
		fmt.Printf("Checkpoint state version: %d\n", snap.StateVersion)
		fmt.Printf("Checkpoint step states: %d\n", len(snap.StepStates))
		for _, ss := range snap.StepStates {
			fmt.Printf("  Step %q: status=%s output=%q\n", ss.StepID, ss.Status, ss.Output)
		}
	}

	// Show collector data (routes, tools, errors).
	c := stack.collector
	fmt.Printf("Collector: routes=%d tools=%d errors=%d\n",
		len(c.RouteHistory()), len(c.ToolHistory()), len(c.ErrorLog()))

	fmt.Println("Recovery example completed successfully!")
}
