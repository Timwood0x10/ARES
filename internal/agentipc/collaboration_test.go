package agentipc

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDelegateToSpecialistRoundTrip verifies the delegation pattern: the
// specialist handler receives the delegation request (task id + specialization
// + payload) and its reply is returned to the delegator.
func TestDelegateToSpecialistRoundTrip(t *testing.T) {
	bus := NewBus()
	var gotTaskID, gotSpecialization, gotPayload string
	if err := bus.Register("code_agent", func(_ context.Context, msg *Message) (*Message, error) {
		body, _ := msg.Payload.(map[string]any)
		gotTaskID, _ = body["task_id"].(string)
		gotSpecialization, _ = body["specialization"].(string)
		gotPayload, _ = body["payload"].(string)
		return &Message{From: msg.To, To: msg.From, Topic: "reply", Payload: "done"}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	ctx := context.Background()
	reply, err := bus.DelegateToSpecialist(ctx, "leader", "code_agent", "t1", "code", "impl feature", time.Second)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if gotTaskID != "t1" || gotSpecialization != "code" || gotPayload != "impl feature" {
		t.Fatalf("specialist got wrong delegation: task=%q spec=%q payload=%q", gotTaskID, gotSpecialization, gotPayload)
	}
	if reply.Payload != "done" {
		t.Fatalf("reply payload = %v, want done", reply.Payload)
	}
}

// TestDelegateToSpecialistUnknownTarget verifies that delegating to an
// unregistered specialist fails with ErrAgentNotRegistered.
func TestDelegateToSpecialistUnknownTarget(t *testing.T) {
	bus := NewBus()
	_, err := bus.DelegateToSpecialist(context.Background(), "leader", "missing", "t1", "code", nil, time.Second)
	if !errors.Is(err, ErrAgentNotRegistered) {
		t.Fatalf("want ErrAgentNotRegistered, got %v", err)
	}
}

// TestDelegateToSpecialistTimeout verifies the delegation waits for the reply
// and times out when the specialist does not respond.
func TestDelegateToSpecialistTimeout(t *testing.T) {
	bus := NewBus()
	if err := bus.Register("slow", func(context.Context, *Message) (*Message, error) {
		time.Sleep(100 * time.Millisecond) // exceed the timeout
		return &Message{Topic: "reply"}, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	_, err := bus.DelegateToSpecialist(context.Background(), "leader", "slow", "t1", "code", nil, 10*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("want ErrTimeout, got %v", err)
	}
}

// TestPipelineRunsStagesInOrder verifies the pipeline pattern: A → B → C,
// each stage receives the previous stage's output and the final result is
// returned.
func TestPipelineRunsStagesInOrder(t *testing.T) {
	bus := NewBus()
	// A uppercases, B appends "-b", C appends "-c".
	if err := bus.Register("a", func(_ context.Context, msg *Message) (*Message, error) {
		s, _ := msg.Payload.(string)
		return &Message{Topic: "reply", Payload: s + "-a"}, nil
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := bus.Register("b", func(_ context.Context, msg *Message) (*Message, error) {
		s, _ := msg.Payload.(string)
		return &Message{Topic: "reply", Payload: s + "-b"}, nil
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := bus.Register("c", func(_ context.Context, msg *Message) (*Message, error) {
		s, _ := msg.Payload.(string)
		return &Message{Topic: "reply", Payload: s + "-c"}, nil
	}); err != nil {
		t.Fatalf("register c: %v", err)
	}

	p, err := NewPipeline(bus, []string{"a", "b", "c"}, time.Second)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	reply, err := p.Run(context.Background(), "leader", "base")
	if err != nil {
		t.Fatalf("pipeline run: %v", err)
	}
	if got := reply.Payload; got != "base-a-b-c" {
		t.Fatalf("pipeline output = %q, want base-a-b-c", got)
	}
}

// TestPipelineEmptyRejected verifies an empty pipeline is rejected.
func TestPipelineEmptyRejected(t *testing.T) {
	if _, err := NewPipeline(NewBus(), nil, time.Second); !errors.Is(err, ErrPipelineEmpty) {
		t.Fatalf("want ErrPipelineEmpty, got %v", err)
	}
}

// TestPipelineStageFailureStops verifies a failing stage stops the pipeline
// and returns the error.
func TestPipelineStageFailureStops(t *testing.T) {
	bus := NewBus()
	if err := bus.Register("a", func(_ context.Context, msg *Message) (*Message, error) {
		return &Message{Topic: "reply", Payload: "ok-a"}, nil
	}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := bus.Register("b", func(context.Context, *Message) (*Message, error) {
		return nil, errors.New("stage b exploded")
	}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := bus.Register("c", func(_ context.Context, msg *Message) (*Message, error) {
		return &Message{Topic: "reply", Payload: "should-not-run"}, nil
	}); err != nil {
		t.Fatalf("register c: %v", err)
	}

	p, err := NewPipeline(bus, []string{"a", "b", "c"}, time.Second)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	_, err = p.Run(context.Background(), "leader", "x")
	if err == nil {
		t.Fatal("pipeline must fail when a stage fails")
	}
}

// TestOrchestrateFansOutToAllWorkers verifies the orchestration pattern: every
// worker receives the task and the coordinator gets one result per worker.
func TestOrchestrateFansOutToAllWorkers(t *testing.T) {
	bus := NewBus()
	for _, w := range []string{"w1", "w2", "w3"} {
		w := w
		if err := bus.Register(w, func(_ context.Context, msg *Message) (*Message, error) {
			body, _ := msg.Payload.(map[string]any)
			taskID, _ := body["task_id"].(string)
			return &Message{Topic: "reply", Payload: w + ":" + taskID}, nil
		}); err != nil {
			t.Fatalf("register %s: %v", w, err)
		}
	}

	results, err := bus.Orchestrate(context.Background(), "coord", []string{"w1", "w2", "w3"}, "t9", "work", time.Second)
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	for _, r := range results {
		if r.Err != nil {
			t.Fatalf("worker %s failed: %v", r.Worker, r.Err)
		}
		want := r.Worker + ":t9"
		if r.Reply.Payload != want {
			t.Fatalf("worker %s payload = %v, want %s", r.Worker, r.Reply.Payload, want)
		}
	}
}

// TestOrchestrateCapturesWorkerFailure verifies a failing worker is captured
// per-result and does not cancel the others.
func TestOrchestrateCapturesWorkerFailure(t *testing.T) {
	bus := NewBus()
	if err := bus.Register("good", func(_ context.Context, msg *Message) (*Message, error) {
		return &Message{Topic: "reply", Payload: "ok"}, nil
	}); err != nil {
		t.Fatalf("register good: %v", err)
	}
	if err := bus.Register("bad", func(context.Context, *Message) (*Message, error) {
		return nil, errors.New("worker down")
	}); err != nil {
		t.Fatalf("register bad: %v", err)
	}

	results, err := bus.Orchestrate(context.Background(), "coord", []string{"good", "bad"}, "t1", nil, time.Second)
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	byWorker := map[string]OrchestrationResult{}
	for _, r := range results {
		byWorker[r.Worker] = r
	}
	if byWorker["good"].Err != nil || byWorker["good"].Reply == nil {
		t.Fatal("good worker must succeed")
	}
	if byWorker["bad"].Err == nil {
		t.Fatal("bad worker must report its error")
	}
}

// TestOrchestrateNoWorkersRejected verifies orchestration without workers is
// rejected.
func TestOrchestrateNoWorkersRejected(t *testing.T) {
	_, err := NewBus().Orchestrate(context.Background(), "coord", nil, "t1", nil, time.Second)
	if !errors.Is(err, ErrNoWorkers) {
		t.Fatalf("want ErrNoWorkers, got %v", err)
	}
}
