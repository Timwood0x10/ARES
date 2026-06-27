// Package main compares ApplyAtCheckpoint vs ApplyImmediate in DynamicExecutor,
// demonstrating the runtime plugin system via PluginBus with ObserverPlugin.
package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/runtime"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

type dynAgent struct {
	id        string
	agentType string
	fn        func(ctx context.Context, input any) (any, error)
}

func (a *dynAgent) ID() string                                          { return a.id }
func (a *dynAgent) Type() models.AgentType                              { return models.AgentType(a.agentType) }
func (a *dynAgent) Status() models.AgentStatus                          { return models.AgentStatusReady }
func (a *dynAgent) Start(ctx context.Context) error                     { return nil }
func (a *dynAgent) Stop(ctx context.Context) error                      { return nil }
func (a *dynAgent) Process(ctx context.Context, input any) (any, error) { return a.fn(ctx, input) }
func (a *dynAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	return nil, nil
}

func makeResult(desc string) *models.RecommendResult {
	return &models.RecommendResult{Items: []*models.RecommendItem{{Description: desc}}}
}

// agentWithDAG dynamically adds a downstream step during Process.
type agentWithDAG struct {
	id         string
	agentType  string
	dag        *engine.MutableDAG
	ctx        context.Context
	stepID     string
	childName  string
	childInput string
	mu         sync.Mutex
	childAdded bool
}

func (a *agentWithDAG) ID() string                      { return a.id }
func (a *agentWithDAG) Type() models.AgentType          { return models.AgentType(a.agentType) }
func (a *agentWithDAG) Status() models.AgentStatus      { return models.AgentStatusReady }
func (a *agentWithDAG) Start(ctx context.Context) error { return nil }
func (a *agentWithDAG) Stop(ctx context.Context) error  { return nil }
func (a *agentWithDAG) Process(ctx context.Context, input any) (any, error) {
	time.Sleep(50 * time.Millisecond)

	a.mu.Lock()
	if !a.childAdded {
		a.childAdded = true
		err := a.dag.AddNode(a.ctx, &engine.Step{
			ID:        a.stepID + "_child",
			Name:      a.childName,
			AgentType: "worker",
			Input:     a.childInput,
			DependsOn: []string{a.stepID},
		})
		if err != nil {
			a.mu.Unlock()
			return nil, fmt.Errorf("AddNode: %w", err)
		}
	}
	a.mu.Unlock()

	return makeResult("processed: " + fmt.Sprint(input)), nil
}
func (a *agentWithDAG) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	return nil, nil
}

func main() {
	fmt.Println("=== ApplyAtCheckpoint ===")
	runDemo(engine.ApplyAtCheckpoint)

	fmt.Println("\n=== ApplyImmediate ===")
	runDemo(engine.ApplyImmediate)
}

func runDemo(mode engine.ApplyMode) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dag, err := engine.NewMutableDAG([]*engine.Step{
		{ID: "s1", Name: "First", AgentType: "adder", Input: "first"},
	})
	if err != nil {
		log.Fatalf("NewMutableDAG: %v", err)
	}

	wf := &engine.Workflow{
		ID:    fmt.Sprintf("applymode-%d", mode),
		Name:  "ApplyMode Demo",
		Steps: dag.Steps(),
	}

	registry := engine.NewAgentRegistry()
	_ = registry.Register("adder", func(actx context.Context, config interface{}) (base.Agent, error) {
		return &agentWithDAG{
			id:         "adder-instance",
			agentType:  "adder",
			dag:        dag,
			ctx:        ctx,
			stepID:     "s1",
			childName:  "Child (added dynamically)",
			childInput: "child_of_s1",
		}, nil
	})
	_ = registry.Register("worker", func(actx context.Context, config interface{}) (base.Agent, error) {
		return &dynAgent{id: "w", agentType: "worker",
			fn: func(actx context.Context, input any) (any, error) {
				return makeResult("processed: " + fmt.Sprint(input)), nil
			},
		}, nil
	})

	// — Runtime Plugin System —
	// Create an in-memory event store and observer plugin to capture workflow
	// lifecycle ares_events (workflow.started, step.completed, workflow.completed, etc.).
	eventStore := ares_events.NewMemoryEventStore()
	observer := runtime.NewObserverPlugin("demo-observer", eventStore)

	bus := runtime.NewPluginBus()
	if err := bus.Register(observer); err != nil {
		log.Fatalf("register observer: %v", err)
	}
	if err := bus.Start(ctx); err != nil {
		log.Fatalf("start plugin bus: %v", err)
	}
	defer func() { _ = bus.Stop(ctx) }()

	executor := engine.NewDynamicExecutor(registry, mode, engine.WithMaxParallel(1)).
		WithPluginBus(bus)

	result, err := executor.ExecuteDynamic(ctx, wf, "start", dag)
	if err != nil {
		log.Printf("ExecuteDynamic (mode=%d): %v", mode, err)
	}
	if result != nil {
		fmt.Printf("  Status: %s, steps: %d\n", result.Status, len(result.Steps))
		for _, s := range result.Steps {
			fmt.Printf("    %q: status=%s output=%q\n", s.StepID, s.Status, s.Output)
		}
	}

	// Print observed plugin ares_events.
	evts, err := eventStore.ReadAll(ctx, ares_events.ReadOptions{Direction: ares_events.ReadAscending})
	if err != nil {
		log.Printf("read ares_events: %v", err)
	} else {
		fmt.Printf("  PluginBus ares_events observed: %d\n", len(evts))
		for _, e := range evts {
			fmt.Printf("    [%s] %s\n", e.StreamID, e.Type)
		}
	}
}
