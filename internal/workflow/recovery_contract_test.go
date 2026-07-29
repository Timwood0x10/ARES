package workflow

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

type contractCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newContractCheckpointStore() *contractCheckpointStore {
	return &contractCheckpointStore{data: make(map[string][]byte)}
}

func (store *contractCheckpointStore) Save(_ context.Context, key string, data []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.data[key] = append([]byte(nil), data...)
	return nil
}

func (store *contractCheckpointStore) Load(_ context.Context, key string) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return append([]byte(nil), store.data[key]...), nil
}

func TestRunner_Contract_RouterSelectsExplicitControlTarget(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("router-contract").
		AddNode(NodeSpec{ID: "source"}).
		AddNode(NodeSpec{ID: "left"}).
		AddNode(NodeSpec{ID: "right"}).
		AddEdge(EdgeSpec{
			From: "source", To: "left", Kind: EdgeControlFlow, Branch: BranchOne, Group: "route",
			Cond: &ConditionExpr{Type: "state", Value: "left"},
		}).
		AddEdge(EdgeSpec{
			From: "source", To: "right", Kind: EdgeControlFlow, Branch: BranchOne, Group: "route",
			Cond: &ConditionExpr{Type: "state", Value: "right"},
		}).
		WithEntry("source")

	var leftCalls int
	var rightCalls int
	executor := NewFuncNodeExecutor()
	executor.Register("source", func(context.Context, StateView) (map[string]any, error) {
		return map[string]any{"route": "right"}, nil
	})
	executor.Register("left", func(context.Context, StateView) (map[string]any, error) {
		leftCalls++
		return nil, nil
	})
	executor.Register("right", func(context.Context, StateView) (map[string]any, error) {
		rightCalls++
		return nil, nil
	})

	runner := NewRunner(executor)
	result, err := runner.ExecuteBound(context.Background(), &BoundWorkflow{
		Spec: spec,
		Routers: map[NodeID]Router{
			"source": func(context.Context, string, map[string]any, string) string { return "right" },
		},
	})
	if err != nil {
		t.Fatalf("ExecuteBound() error = %v", err)
	}
	if leftCalls != 0 || rightCalls != 1 {
		t.Fatalf("route calls left=%d right=%d, want 0 and 1", leftCalls, rightCalls)
	}
	if got := statusByID(result, "left"); got != NodeStatusNotSelected {
		t.Fatalf("left status = %q, want %q", got, NodeStatusNotSelected)
	}
}

func TestRunner_Contract_ResumeRestoresSchedulerWithoutReplayingCompletedNodes(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("resume-contract").
		AddNode(NodeSpec{ID: "first"}).
		AddNode(NodeSpec{ID: "second"}).
		AddEdge(EdgeSpec{From: "first", To: "second", Kind: EdgeDataDependency}).
		WithEntry("first")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	if got := scheduler.Next(); got != "first" {
		t.Fatalf("first ready node = %q, want first", got)
	}
	scheduler.OnNodeCompleted("first")

	store := newContractCheckpointStore()
	specHash, err := workflowSpecHash(spec)
	if err != nil {
		t.Fatalf("workflowSpecHash() error = %v", err)
	}
	snapshot := CheckpointSnapshot{
		SchemaVersion: runnerCheckpointSchemaVersion,
		ExecutionID:   "resume-1",
		SpecID:        spec.ID,
		BaseSpecHash:  specHash,
		SpecHash:      specHash,
		EffectiveSpec: spec,
		State:         map[string]any{"first_value": "committed"},
		NodeStates: []NodeStatusValue{
			{ID: "first", Status: NodeStatusCompleted, Output: map[string]any{"first_value": "committed"}},
			{ID: "second", Status: NodeStatusPending},
		},
		Scheduler:             scheduler.Snapshot(),
		LoopIterationComplete: true,
		EventSequence:         12,
		SavedAt:               time.Now(),
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := store.Save(context.Background(), ares_runtime.CheckpointKey("resume-1"), payload); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	var firstCalls int
	var secondCalls int
	executor := NewFuncNodeExecutor()
	executor.Register("first", func(context.Context, StateView) (map[string]any, error) {
		firstCalls++
		return nil, nil
	})
	executor.Register("second", func(_ context.Context, state StateView) (map[string]any, error) {
		secondCalls++
		value, _ := state.Get("first_value")
		return map[string]any{"observed": value}, nil
	})

	sink := &recordingRunnerEventSink{}
	result, err := NewRunner(executor, WithCheckpointStore(store), WithEventSink(sink)).ResumeExecution(context.Background(), spec, "resume-1")
	if err != nil {
		t.Fatalf("ResumeExecution() error = %v", err)
	}
	if firstCalls != 0 || secondCalls != 1 {
		t.Fatalf("resume calls first=%d second=%d, want 0 and 1", firstCalls, secondCalls)
	}
	if result.ExecutionID != "resume-1" {
		t.Fatalf("execution ID = %q, want resume-1", result.ExecutionID)
	}
	if got := result.State["observed"]; got != "committed" {
		t.Fatalf("observed state = %#v, want committed", got)
	}
	events := sink.Events()
	if len(events) == 0 || events[0].Type != RunnerEventWorkflowResumed || events[0].Sequence != 13 {
		t.Fatalf("resume events = %#v, want workflow.resumed at sequence 13", events)
	}
	for index := 1; index < len(events); index++ {
		if events[index].Sequence <= events[index-1].Sequence {
			t.Fatalf("event sequences are not increasing: %#v", events)
		}
	}
	if events[len(events)-1].Type != RunnerEventWorkflowCompleted {
		t.Fatalf("terminal resume event = %q, want workflow.completed", events[len(events)-1].Type)
	}
}

func statusByID(result *Result, id NodeID) NodeStatus {
	for _, state := range result.NodeStates {
		if state.ID == id {
			return state.Status
		}
	}
	return ""
}
