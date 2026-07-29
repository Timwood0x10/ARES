package workflow

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestRunner_Contract_QueuedMutationCommitsAtSafePoint(t *testing.T) {
	t.Parallel()

	base := NewWorkflow("mutation-safe-point").
		AddNode(NodeSpec{ID: "base"}).
		WithEntry("base")
	queue := NewPatchQueue()
	if err := queue.Enqueue("mutation-exec", Mutation{
		ID:    "add-injected",
		Type:  MutationAddNode,
		Node:  &NodeSpec{ID: "injected"},
		Entry: true,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	var mu sync.Mutex
	calls := make(map[NodeID]int)
	executor := NewFuncNodeExecutor()
	for _, id := range []NodeID{"base", "injected"} {
		id := id
		executor.Register(id, func(context.Context, StateView) (map[string]any, error) {
			mu.Lock()
			calls[id]++
			mu.Unlock()
			return map[string]any{"output": string(id)}, nil
		})
	}
	store := newContractCheckpointStore()
	sink := &recordingRunnerEventSink{}
	runner := NewRunner(
		executor,
		WithExecutionID("mutation-exec"),
		WithPatchQueue(queue),
		WithCheckpointStore(store),
		WithEventSink(sink),
	)
	result, err := runner.Execute(context.Background(), base)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != NodeStatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	mu.Lock()
	baseCalls := calls["base"]
	injectedCalls := calls["injected"]
	mu.Unlock()
	if baseCalls != 1 || injectedCalls != 1 {
		t.Fatalf("calls base=%d injected=%d, want 1 and 1", baseCalls, injectedCalls)
	}
	if pending := queue.Pending("mutation-exec"); len(pending) != 0 {
		t.Fatalf("pending mutations = %#v, want empty after durable commit", pending)
	}
	snapshot, err := runner.loadCheckpoint(context.Background(), "mutation-exec")
	if err != nil {
		t.Fatalf("loadCheckpoint() error = %v", err)
	}
	if len(snapshot.MutationIDs) != 1 || snapshot.MutationIDs[0] != "add-injected" {
		t.Fatalf("mutation IDs = %#v", snapshot.MutationIDs)
	}
	if _, exists := nodeIndex(snapshot.EffectiveSpec, "injected"); !exists {
		t.Fatal("effective checkpoint spec does not contain injected node")
	}
	var applied bool
	for _, event := range sink.Events() {
		if event.Type == RunnerEventMutationApplied && event.Metadata["mutation_id"] == "add-injected" {
			applied = true
		}
	}
	if !applied {
		t.Fatal("mutation.applied event was not emitted")
	}
}

func TestRunner_Contract_InvalidMutationDoesNotPolluteExecution(t *testing.T) {
	t.Parallel()

	base := NewWorkflow("mutation-rollback").AddNode(NodeSpec{ID: "base"}).WithEntry("base")
	queue := NewPatchQueue()
	if err := queue.Enqueue("rollback-exec", Mutation{
		ID:     "remove-missing",
		Type:   MutationRemoveNode,
		NodeID: "missing",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	executor := NewFuncNodeExecutor()
	var calls int
	executor.Register("base", func(context.Context, StateView) (map[string]any, error) {
		calls++
		return nil, nil
	})
	_, err := NewRunner(
		executor,
		WithExecutionID("rollback-exec"),
		WithPatchQueue(queue),
		WithCheckpointStore(newContractCheckpointStore()),
	).Execute(context.Background(), base)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid mutation failure")
	}
	if calls != 0 {
		t.Fatalf("base calls = %d, want no execution before invalid safe-point commit", calls)
	}
	if len(base.Nodes) != 1 || base.Nodes[0].ID != "base" {
		t.Fatalf("base spec was polluted: %#v", base.Nodes)
	}
	if pending := queue.Pending("rollback-exec"); len(pending) != 1 {
		t.Fatalf("pending mutations = %#v, want invalid mutation retained", pending)
	}
}

func TestRunner_Contract_MutationCheckpointSaveFailureRollsBack(t *testing.T) {
	t.Parallel()

	base := NewWorkflow("mutation-store-failure").AddNode(NodeSpec{ID: "base"}).WithEntry("base")
	queue := NewPatchQueue()
	if err := queue.Enqueue("store-failure-exec", Mutation{
		ID:    "add-node",
		Type:  MutationAddNode,
		Node:  &NodeSpec{ID: "added"},
		Entry: true,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	executor := NewFuncNodeExecutor()
	executor.Register("base", func(context.Context, StateView) (map[string]any, error) { return nil, nil })
	executor.Register("added", func(context.Context, StateView) (map[string]any, error) { return nil, nil })
	_, err := NewRunner(
		executor,
		WithExecutionID("store-failure-exec"),
		WithPatchQueue(queue),
		WithCheckpointStore(failingCheckpointStore{err: errors.New("disk unavailable")}),
	).Execute(context.Background(), base)
	if err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("Execute() error = %v, want disk unavailable", err)
	}
	if len(base.Nodes) != 1 {
		t.Fatalf("base spec was mutated after save failure: %#v", base.Nodes)
	}
	if pending := queue.Pending("store-failure-exec"); len(pending) != 1 {
		t.Fatalf("pending mutations = %#v, want retained after save failure", pending)
	}
}

type failingCheckpointStore struct {
	err error
}

func (store failingCheckpointStore) Save(context.Context, string, []byte) error { return store.err }
func (store failingCheckpointStore) Load(context.Context, string) ([]byte, error) {
	return nil, store.err
}
