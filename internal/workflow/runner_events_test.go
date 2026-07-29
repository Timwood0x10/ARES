package workflow

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingRunnerEventSink struct {
	mu     sync.Mutex
	events []RunnerEvent
}

func (s *recordingRunnerEventSink) Publish(_ context.Context, event RunnerEvent) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func (s *recordingRunnerEventSink) Events() []RunnerEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]RunnerEvent(nil), s.events...)
}

func TestRunner_Contract_NativeEventsAreOrderedAndRealtime(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("native-events").AddNode(NodeSpec{ID: "node"}).WithEntry("node")
	executor := NewFuncNodeExecutor()
	executor.Register("node", func(context.Context, StateView) (map[string]any, error) {
		return map[string]any{"output": "done"}, nil
	})
	sink := &recordingRunnerEventSink{}
	result, err := NewRunner(executor, WithEventSink(sink)).Execute(context.Background(), spec)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Status != NodeStatusCompleted {
		t.Fatalf("status = %q, want completed", result.Status)
	}
	events := sink.Events()
	want := []RunnerEventType{
		RunnerEventWorkflowStarted,
		RunnerEventNodeStarted,
		RunnerEventNodeCompleted,
		RunnerEventWorkflowCompleted,
	}
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %#v", len(events), len(want), events)
	}
	for index, event := range events {
		if event.Type != want[index] {
			t.Fatalf("event[%d].Type = %q, want %q", index, event.Type, want[index])
		}
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event[%d].Sequence = %d, want %d", index, event.Sequence, index+1)
		}
	}
	if events[2].Output["output"] != "done" {
		t.Fatalf("node output = %#v, want done", events[2].Output)
	}
}

func TestRunner_Contract_PendingInterruptIsDurableBeforeHandlerWaits(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("durable-interrupt").AddNode(NodeSpec{
		ID: "review",
		Interrupt: &InterruptSpec{
			Message: "approve",
		},
	}).WithEntry("review")
	executor := NewFuncNodeExecutor()
	executor.Register("review", func(context.Context, StateView) (map[string]any, error) {
		return map[string]any{"output": "approved"}, nil
	})
	store := newContractCheckpointStore()
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	runner := NewRunner(
		executor,
		WithExecutionID("durable-interrupt-exec"),
		WithCheckpointStore(store),
		WithInterruptHandler(func(ctx context.Context, _ *InterruptSpec, _ StateView) (bool, error) {
			close(handlerStarted)
			select {
			case <-releaseHandler:
				return true, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}),
	)
	done := make(chan error, 1)
	go func() {
		_, err := runner.Execute(context.Background(), spec)
		done <- err
	}()
	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("interrupt handler did not start")
	}
	snapshot, err := runner.loadCheckpoint(context.Background(), "durable-interrupt-exec")
	if err != nil {
		t.Fatalf("loadCheckpoint() error = %v", err)
	}
	if len(snapshot.PendingInterrupts) != 1 {
		t.Fatalf("pending interrupts = %#v, want one durable interrupt", snapshot.PendingInterrupts)
	}
	pending := snapshot.PendingInterrupts[0]
	if pending.NodeID != "review" || pending.Token == "" {
		t.Fatalf("pending interrupt = %#v, want review with stable token", pending)
	}
	if len(snapshot.Scheduler.ReadyQueue) != 1 || snapshot.Scheduler.ReadyQueue[0] != "review" {
		t.Fatalf("ready queue = %#v, want pre-batch review token", snapshot.Scheduler.ReadyQueue)
	}
	close(releaseHandler)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("execution did not complete")
	}
}

func TestRunner_Contract_CheckpointPersistsRecoveryLifecycle(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("checkpoint-lifecycle").AddNode(NodeSpec{ID: "node"}).WithEntry("node")
	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	scope := NewExecutionScope("checkpoint-lifecycle-exec", spec)
	scope.InitNodeStates()
	scope.RestoreEventSequence(7)
	scope.SetPendingInterrupt(PendingInterrupt{NodeID: "node", Message: "approve", CreatedAt: time.Now()})
	scope.RecordMutationID("mutation-1")
	store := newContractCheckpointStore()
	sink := &recordingRunnerEventSink{}
	runner := NewRunner(nil, WithCheckpointStore(store), WithEventSink(sink))
	if err := runner.saveCheckpoint(context.Background(), scope, scheduler, 0); err != nil {
		t.Fatalf("saveCheckpoint() error = %v", err)
	}
	snapshot, err := runner.loadCheckpoint(context.Background(), scope.ExecutionID)
	if err != nil {
		t.Fatalf("loadCheckpoint() error = %v", err)
	}
	if snapshot.EventSequence != 8 {
		t.Fatalf("event sequence = %d, want checkpoint event sequence 8", snapshot.EventSequence)
	}
	events := sink.Events()
	if len(events) != 1 || events[0].Type != RunnerEventCheckpointSaved || events[0].Sequence != 8 {
		t.Fatalf("checkpoint events = %#v, want one saved event at sequence 8", events)
	}
	if len(snapshot.PendingInterrupts) != 1 || snapshot.PendingInterrupts[0].NodeID != "node" {
		t.Fatalf("pending interrupts = %#v", snapshot.PendingInterrupts)
	}
	if len(snapshot.MutationIDs) != 1 || snapshot.MutationIDs[0] != "mutation-1" {
		t.Fatalf("mutation IDs = %#v", snapshot.MutationIDs)
	}
	if snapshot.SchemaVersion != runnerCheckpointSchemaVersion {
		t.Fatalf("schema = %d, want %d", snapshot.SchemaVersion, runnerCheckpointSchemaVersion)
	}
}
