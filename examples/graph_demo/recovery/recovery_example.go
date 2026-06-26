// Package main demonstrates node-level recovery via StepRecoveryHandler
// and the runtime plugin system via PluginBus with CheckpointPlugin.
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

// inMemoryStore is an in-memory CheckpointStore for demonstration purposes.
type inMemoryStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newInMemoryStore() *inMemoryStore {
	return &inMemoryStore{data: make(map[string][]byte)}
}

func (s *inMemoryStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

func (s *inMemoryStore) Load(_ context.Context, key string) ([]byte, error) {
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

func main() {
	registry := engine.NewAgentRegistry()
	_ = registry.Register("ok", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &simpleAgent{id: "ok-instance", agentType: "ok",
			fn: func(ctx context.Context, input any) (any, error) {
				return makeResult("success: " + fmt.Sprint(input)), nil
			},
		}, nil
	})
	_ = registry.Register("fails", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &simpleAgent{id: "fails-instance", agentType: "fails",
			fn: func(ctx context.Context, input any) (any, error) {
				return nil, fmt.Errorf("step failed")
			},
		}, nil
	})

	// Recovery handler replaces a failed step with a working alternative.
	handler := &myRecoveryHandler{}

	// Event sink records recovery lifecycle events.
	var emittedEvents []string
	eventSink := func(ctx context.Context, evType events.EventType, payload map[string]any) {
		emittedEvents = append(emittedEvents, string(evType))
	}

	// — Runtime Plugin System —
	// The CheckpointPlugin saves execution checkpoints at each step boundary,
	// enabling crash recovery and execution observability.
	store := newInMemoryStore()
	checkpoint := runtime.NewCheckpointPlugin("demo-checkpoint", store)

	bus := runtime.NewPluginBus()
	if err := bus.Register(checkpoint); err != nil {
		log.Fatalf("register checkpoint plugin: %v", err)
	}
	if err := bus.Start(context.Background()); err != nil {
		log.Fatalf("start plugin bus: %v", err)
	}
	defer bus.Stop(context.Background())

	executor := engine.NewDynamicExecutor(registry, engine.ApplyAtCheckpoint).
		WithRecoveryHandler(handler).
		WithRecoveryEventSink(eventSink).
		WithPluginBus(bus)

	// Build a workflow where step1 uses a failing agent, but has a
	// RecoveryPolicy that triggers replace_node.
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

	wf := &engine.Workflow{
		ID:    "recovery-demo",
		Name:  "Recovery Demo",
		Steps: dag.Steps(),
	}

	result, err := executor.ExecuteDynamic(context.Background(), wf, "input", dag)
	if err != nil {
		log.Fatalf("ExecuteDynamic: %v", err)
	}

	fmt.Printf("Workflow status: %s\n", result.Status)
	fmt.Printf("Steps executed: %d\n", len(result.Steps))
	for _, s := range result.Steps {
		fmt.Printf("  Step %q: status=%s output=%q\n", s.StepID, s.Status, s.Output)
	}
	fmt.Printf("Recovery events: %v\n", emittedEvents)

	// Show checkpoint data saved by the CheckpointPlugin.
	ckptData, err := store.Load(context.Background(), runtime.CheckpointKey(result.ExecutionID))
	if err != nil {
		log.Printf("load checkpoint: %v", err)
	} else if ckptData != nil {
		fmt.Printf("Checkpoint saved: %d bytes\n", len(ckptData))
	} else {
		fmt.Println("No checkpoint data found")
	}

	// Alternatively, get the in-memory snapshot via the CheckpointPlugin API.
	if snap := checkpoint.Snapshot(result.ExecutionID); snap != nil {
		fmt.Printf("Checkpoint state version: %d\n", snap.StateVersion)
		fmt.Printf("Checkpoint step states: %d\n", len(snap.StepStates))
		for _, ss := range snap.StepStates {
			fmt.Printf("  Step %q: status=%s output=%q\n", ss.StepID, ss.Status, ss.Output)
		}
	}

	fmt.Println("Recovery example completed successfully!")
}

type myRecoveryHandler struct{}

func (h *myRecoveryHandler) RecoverStep(ctx context.Context, failure engine.StepFailure, dag *engine.MutableDAG) (*engine.RecoveryDecision, error) {
	fmt.Printf("Recovery triggered for step %q (error: %s)\n", failure.StepID, failure.Error)

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
