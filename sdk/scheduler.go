package sdk

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/kernelscheduler"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// This file implements the H1/H2 merge (aresos-agentos-plan H1/H2: 合并 SDK
// 和 kernel 两条路径 — sdk.Runtime.Submit goes through the Task Fabric and the
// shared kernelscheduler, not a divergent direct-run path). The SDK is a
// peer-runtime facade over the SAME scheduling engine the kernel uses:
//
//	Submit → fabric.Create → kernelscheduler.Scheduler (Schedule → Acquire →
//	RunQuantum via the registered sdk agent executor) → COMPLETED → result.
//
// A sdk agent is an executor like any other: it runs its ReAct loop inside one
// quantum (agentloop.Engine) and the scheduler owns capability matching,
// concurrency, retries and outcome bookkeeping — no second scheduling loop.

// sdkAgentExecutor adapts a sdk.Agent to the shared scheduler's
// CapabilityExecutor contract. One agent = one capability executor, matching
// the kernel's flat peer pool. Execution runs the agent's full ReAct loop in a
// single quantum (the agentloop engine iterates internally), so Done is always
// true; the fabric's retry policy still bounds failures.
type sdkAgentExecutor struct {
	agent *Agent
}

var _ kernelscheduler.CapabilityExecutor = (*sdkAgentExecutor)(nil)

func (e *sdkAgentExecutor) ID() string { return e.agent.name }

func (e *sdkAgentExecutor) Type() models.AgentType { return models.AgentType(e.agent.name) }

func (e *sdkAgentExecutor) ExecuteStep(ctx context.Context, task *models.Task) (*sub.StepOutcome, error) {
	input, _ := task.Payload["input"].(string)
	res, err := e.agent.Run(ctx, input)
	if err != nil {
		return nil, err
	}
	out := &sub.StepOutcome{Done: true}
	if res != nil {
		tr := models.NewTaskResult(task.TaskID, task.AgentType)
		tr.SetSuccess(nil, res.Output)
		// Carry the full sdk.Result back through the quantum checkpoint so
		// Submit can restore Output/ToolCalls/TokenUsage/Duration exactly.
		tr.Metadata = map[string]any{sdkResultKey: res}
		out.Result = tr
	}
	return out, nil
}

// sdkResultKey is the metadata key under which the sdk.Result rides through
// the fabric checkpoint (same-process reference — no JSON round-trip).
const sdkResultKey = "sdk_result"

// sdkTaskSeq assigns monotonic fabric task ids for submitted tasks.
var sdkTaskSeq atomic.Int64

// ensureScheduler lazily starts the shared scheduler over the runtime's own
// Task Fabric. It runs exactly once; subsequent calls reuse the started
// scheduler. The scheduler goroutine lives until Runtime.Close cancels
// schedCtx.
func (r *Runtime) ensureScheduler() {
	r.schedOnce.Do(func() {
		r.sdkFabric = taskfabric.NewFabric()
		r.schedCtx, r.schedCancel = context.WithCancel(context.Background())
		r.sched = kernelscheduler.New(r.sdkFabric, r.sdkExecutors, nil)
		r.sched.PollInterval = 20 * time.Millisecond
		go r.sched.Run(r.schedCtx)
		// D1: the SDK is a peer-runtime facade — wire the same kernel
		// syscalls (spawn_agent/create_task) into the tool registry so SDK
		// users can autonomously decompose tasks. Registered after sched
		// exists because the syscall Kernel needs the shared fabric + sched.
		r.wireSyscalls()
	})
}

// submitThroughScheduler creates the task in the fabric and waits for the
// scheduler to drive it to a terminal state, then restores the result. It is
// the H1/H2 merged dispatch path: the shared scheduler (not a direct agent
// call) owns capability matching and execution.
func (r *Runtime) submitThroughScheduler(ctx context.Context, t Task) (*Result, error) {
	r.ensureScheduler()

	// Resolve the executor first: a capability with no registered agent
	// auto-creates one (the runtime never refuses a well-formed task).
	executor := r.ensureExecutor(t.Capability)

	taskID := fmt.Sprintf("sdk-task-%d", sdkTaskSeq.Add(1))
	if t.ID != "" {
		taskID = t.ID
	}
	if err := r.sdkFabric.Create(&taskfabric.Task{
		ID:          taskID,
		Capability:  string(executor.Type()),
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		Checkpoint: &taskfabric.CheckpointEnvelope{
			Payload: map[string]any{"input": t.Input},
		},
	}); err != nil {
		return nil, fmt.Errorf("sdk submit: %w", err)
	}

	// Wait for a terminal state. A timeout, when set, bounds the whole wait
	// (and the execution — the executor receives the same context). The wait
	// context propagates DeadlineExceeded so a timed-out Submit surfaces a
	// deadline-exceeded cause, never a generic error (code_rules_v2 §3.1).
	deadline := t.Timeout
	if deadline <= 0 {
		deadline = 5 * time.Minute
	}
	waitCtx, waitCancel := context.WithTimeout(ctx, deadline)
	defer waitCancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if waitCtx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("sdk submit: task %s timed out after %s: %w", taskID, deadline, context.DeadlineExceeded)
			}
			return nil, waitCtx.Err()
		case <-ticker.C:
			tk, err := r.sdkFabric.Task(taskID)
			if err != nil {
				return nil, fmt.Errorf("sdk submit: %w", err)
			}
			switch tk.State {
			case taskfabric.StateCompleted:
				return r.resultFromFabric(tk)
			case taskfabric.StateFailed:
				return nil, fmt.Errorf("sdk submit: task %s failed", taskID)
			}
		}
	}
}

// resultFromFabric restores the sdk.Result from a completed fabric task's
// checkpoint. The full sdk.Result rides in the quantum metadata (same-process
// reference); the checkpoint's reason is the fallback output.
func (r *Runtime) resultFromFabric(tk *taskfabric.Task) (*Result, error) {
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		return nil, fmt.Errorf("sdk submit: decode result: %w", err)
	}
	step, ok := dc.StepCheckpoint.(map[string]any)
	if ok {
		if md, ok := step["metadata"].(map[string]any); ok {
			if res, ok := md[sdkResultKey].(*Result); ok && res != nil {
				return res, nil
			}
		}
		if reason, ok := step["reason"].(string); ok && reason != "" {
			return &Result{Output: reason}, nil
		}
	}
	return &Result{}, nil
}

// ensureExecutor returns the executor for the capability, creating and
// registering a capability-named agent on demand when none is registered.
// The registration is protected by agentMu (same lock as RegisterAgent). It
// returns the interface so a caller-provided adapter (e.g. a test probe) is
// preserved.
func (r *Runtime) ensureExecutor(capability string) kernelscheduler.CapabilityExecutor {
	if capability == "" {
		capability = "agent"
	}
	r.agentMu.Lock()
	defer r.agentMu.Unlock()
	if ex, ok := r.sdkExecutors[capability]; ok {
		return ex
	}
	a := r.NewAgent(capability)
	ex := &sdkAgentExecutor{agent: a}
	r.sdkExecutors[capability] = ex
	return ex
}
