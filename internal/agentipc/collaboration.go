package agentipc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// High-level multi-agent collaboration patterns (v0.4.0 M1). These build on
// the peer primitives (Send/Request/Reply/Delegate/Handoff/Subscribe) and
// provide the three coordination modes from the roadmap:
//
//   - delegation:    Leader → Specialist task handoff with result return
//   - pipeline:      A → B → C ordered execution (data flows via IPC)
//   - orchestration: Coordinator fans work out to multiple Workers in
//     parallel and aggregates results
//
// They do not change the Bus primitives; they are composition layers on top.

// taskIDKey is the payload key carrying the task id in collaboration
// requests (delegation / pipeline / orchestration). Shared with Handoff
// (primitives.go) so the wire format stays consistent.
const taskIDKey = "task_id"

// DelegateToSpecialist implements the delegation pattern (v0.4.0 M1-1): a
// delegator (typically the Leader) hands a task to a Specialist agent and
// waits for the result. The request carries the task id and the required
// specialization so the receiving handler can decide acceptance. This is a
// synchronous request/reply: the caller blocks until the specialist replies
// or the timeout elapses.
//
// Args:
//   - ctx: cancellation.
//   - delegator: the delegating agent id (From on the wire).
//   - specialist: the target agent id that must handle the task.
//   - taskID: the task being delegated.
//   - specialization: the capability the specialist must provide (the
//     receiving handler decides acceptance; no scheduler is involved).
//   - payload: the task body.
//   - timeout: how long to wait for the specialist's result.
//
// Returns:
//   - *Message: the specialist's result reply.
//   - error: ErrAgentNotRegistered when the specialist is unknown, or
//     ErrTimeout when no reply arrives in time.
func (b *Bus) DelegateToSpecialist(ctx context.Context, delegator, specialist, taskID, specialization string, payload any, timeout time.Duration) (*Message, error) {
	if specialist == "" {
		return nil, fmt.Errorf("agentipc: specialist id required")
	}
	if specialization == "" {
		return nil, fmt.Errorf("agentipc: specialization required")
	}
	body := map[string]any{
		taskIDKey:        taskID,
		"specialization": specialization,
		"payload":        payload,
	}
	return b.Request(ctx, delegator, specialist, "delegate-task", body, timeout)
}

// ErrPipelineEmpty is returned when a pipeline is created with no stages.
var ErrPipelineEmpty = errors.New("agentipc: pipeline requires at least one stage")

// Pipeline runs a sequence of stages A → B → C in order (v0.4.0 M1-2). Each
// stage is an agent id; the output of stage N is passed as the input of stage
// N+1 through the bus (Request/Reply). Stages execute strictly sequentially —
// a stage's result is forwarded to the next stage before it starts. A stage
// that fails stops the pipeline and returns the error.
type Pipeline struct {
	bus     *Bus
	stages  []string
	timeout time.Duration
}

// NewPipeline creates a pipeline over the given stages.
//
// Args:
//   - bus: the IPC bus to run the pipeline on.
//   - stages: ordered agent ids; at least one is required.
//   - timeout: per-stage request timeout.
//
// Returns:
//   - *Pipeline: ready to Run.
//   - error: ErrPipelineEmpty when stages is empty.
func NewPipeline(bus *Bus, stages []string, timeout time.Duration) (*Pipeline, error) {
	if len(stages) == 0 {
		return nil, ErrPipelineEmpty
	}
	return &Pipeline{bus: bus, stages: append([]string(nil), stages...), timeout: timeout}, nil
}

// Run executes the pipeline: stage[0] receives the initial payload, each
// subsequent stage receives the previous stage's result, and the final
// stage's result is returned.
//
// Args:
//   - ctx: cancellation.
//   - from: the pipeline initiator (From on the first request).
//   - input: the initial payload for the first stage.
//
// Returns:
//   - *Message: the final stage's result.
//   - error: the first stage error, or ErrPipelineEmpty (defensive).
func (p *Pipeline) Run(ctx context.Context, from string, input any) (*Message, error) {
	if len(p.stages) == 0 {
		return nil, ErrPipelineEmpty
	}
	payload := input
	for _, stage := range p.stages {
		reply, err := p.bus.Request(ctx, from, stage, "pipeline-stage", payload, p.timeout)
		if err != nil {
			return nil, fmt.Errorf("agentipc: pipeline stage %s: %w", stage, err)
		}
		payload = reply.Payload
	}
	return &Message{From: p.stages[len(p.stages)-1], Topic: "pipeline-result", Payload: payload, At: p.bus.now()}, nil
}

// ErrNoWorkers is returned when orchestration is requested without workers.
var ErrNoWorkers = errors.New("agentipc: orchestration requires at least one worker")

// OrchestrationResult is one worker's outcome in an orchestration run.
type OrchestrationResult struct {
	// Worker is the worker agent id.
	Worker string
	// Reply is the worker's result (nil when the worker failed).
	Reply *Message
	// Err is the worker's error (nil on success).
	Err error
}

// Orchestrate implements the orchestration pattern (v0.4.0 M1-3): a
// coordinator fans a task out to multiple workers in parallel and aggregates
// the results. Each worker receives the same payload; workers run
// concurrently (one goroutine each, bounded by a WaitGroup). A worker failure
// is captured in OrchestrationResult.Err and does not cancel the other
// workers; the caller decides how to aggregate.
//
// Args:
//   - ctx: cancellation (propagated to every worker request).
//   - coordinator: the coordinating agent id (From on each request).
//   - workers: the worker agent ids to fan out to.
//   - taskID: the task being orchestrated.
//   - payload: the work item sent to every worker.
//   - timeout: per-worker request timeout.
//
// Returns:
//   - []OrchestrationResult: one entry per worker, in the same order.
//   - error: ErrNoWorkers when workers is empty.
func (b *Bus) Orchestrate(ctx context.Context, coordinator string, workers []string, taskID string, payload any, timeout time.Duration) ([]OrchestrationResult, error) {
	if len(workers) == 0 {
		return nil, ErrNoWorkers
	}
	results := make([]OrchestrationResult, len(workers))
	var wg sync.WaitGroup
	wg.Add(len(workers))
	for i, worker := range workers {
		i, worker := i, worker
		go func() {
			defer wg.Done()
			body := map[string]any{
				taskIDKey: taskID,
				"payload": payload,
			}
			reply, err := b.Request(ctx, coordinator, worker, "orchestrate-worker", body, timeout)
			results[i] = OrchestrationResult{Worker: worker, Reply: reply, Err: err}
		}()
	}
	wg.Wait()
	return results, nil
}
