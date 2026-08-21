package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

type kernelLegacyDispatcher struct {
	inner leader.TaskDispatcher
}

// D dispatches one task through the legacy leader path.
func (d *kernelLegacyDispatcher) D(ctx context.Context, agentID, taskID string, payload any) error {
	if d.inner == nil {
		return agentipc.ErrDispatcherNotRegistered
	}
	task, err := taskFromPayload(taskID, payload)
	if err != nil {
		return fmt.Errorf("kernel legacy dispatch: %w", err)
	}
	_, dispatchErr := d.inner.Dispatch(ctx, []*models.Task{task})
	return dispatchErr
}

// kernelFabricDispatcher is the new Task Fabric path. Its D() behavior depends
// on whether an executeFn is attached (enableKernelExecution):
//
//   - scoring mode (shadow, default): scores the task against the candidate
//     agents with the Kernel's capability-aware formula (taskfabric.Score/Pick)
//     and reports the would-be outcome. It never creates, acquires or executes
//     — a task is never double-run.
//   - execution mode (flag flipped to PolicyTaskFabric): runs the real Task
//     Fabric path via executeFn (Create → Schedule → Acquire → RunQuantum).
type kernelFabricDispatcher struct {
	candidates []subAgentCapability
	executeFn  func(ctx context.Context, task *models.Task) error // nil = scoring only
}

// D routes the task through the kernel's new path: scoring (shadow) or real
// execution (active), depending on whether an executeFn is attached.
func (d *kernelFabricDispatcher) D(ctx context.Context, agentID, taskID string, payload any) error {
	task, err := taskFromPayload(taskID, payload)
	if err != nil {
		return fmt.Errorf("kernel fabric dispatch: %w", err)
	}
	if d.executeFn != nil {
		return d.executeFn(ctx, task)
	}
	if len(d.candidates) == 0 {
		return nil
	}
	cands := make([]taskfabric.Candidate, 0, len(d.candidates))
	for _, c := range d.candidates {
		caps := c.Caps
		if len(caps) == 0 {
			caps = []string{c.Type}
		}
		cands = append(cands, taskfabric.Candidate{
			AgentID:      c.ID,
			Capabilities: caps,
			Load:         c.Load,
			Confidence:   1.0, // shadow: no experience store wired here
		})
	}
	if winner := taskfabric.Pick(string(task.AgentType), cands); winner == nil {
		return taskfabric.ErrNoCapableCandidate
	}
	return nil
}

// kernelTaskDispatcher adapts the agentipc.DualTrackDispatcher (single-task
// surface) back to the leader.TaskDispatcher batch surface. The leader keeps
// calling Dispatch(ctx, tasks) as before; each task is routed through the
// kernel dispatcher, so shadow scoring runs for every task without any change
// to leader behavior.
//
// Result flow (fix for the fake-success bug): Dispatch does NOT return a
// placeholder success after submitting. The kernel submits each task to the
// Task Fabric (asynchronous execution — the kernelScheduler owns
// Schedule→Acquire→RunQuantum) and then BLOCKS until the worker's real
// completion event arrives for every task (or the dispatch timeout elapses),
// reconstructing the leader-expected []*models.TaskResult from the actual
// worker outcome. This restores the event-driven result contract the legacy
// leader dispatcher had (dispatchViaEvents: subscribe → publish → collect)
// that kernelTaskDispatcher previously bypassed, which made every leader
// dispatch a silent no-op (success=true, items=0).
type kernelTaskDispatcher struct {
	kernel *agentipc.DualTrackDispatcher
	// store is the shared EventStore the worker's EventTaskCompleted/Failed
	// events land on (subAgent.Execute emits them under the sub-agent's
	// stream with task_id in the payload). Nil disables event collection: a
	// task whose result cannot be observed is reported as failed rather than
	// silently faked as success (code_rules_v2 §0.2: no fake implementation).
	store ares_events.EventStore
	// eventTimeout bounds how long Dispatch waits for a task's completion
	// event. It mirrors the legacy leader dispatcher's timeout contract
	// (DefaultDispatcherTimeoutSeconds = 300s); <= 0 falls back to the same
	// default.
	eventTimeout time.Duration
	// fabric lets Dispatch read the worker's structured output back from the
	// completed fabric task (the scheduler stored it in the quantum
	// checkpoint). Nil disables the read-back: the result still carries the
	// event's textual output. Injected by the live flip.
	fabric *taskfabric.Fabric
}

// newKernelTaskDispatcher assembles the batch adapter with the shared event
// store wired for result collection.
func newKernelTaskDispatcher(kernel *agentipc.DualTrackDispatcher, store ares_events.EventStore) *kernelTaskDispatcher {
	return &kernelTaskDispatcher{kernel: kernel, store: store}
}

// Dispatch routes every task through the kernel dispatcher and aggregates the
// per-task outcomes into the leader-expected []*models.TaskResult shape.
//
// The submission is asynchronous (fabric Create; the kernelScheduler runs the
// task in the background), but the return is synchronous: Dispatch waits for
// each task's real completion/failure event (broadcast subscription, so it
// never competes with the scheduler/trace/recovery consumers) and reports the
// worker's actual outcome. A task that times out or whose result cannot be
// observed is reported as failed with the reason, never as a fake success.
func (d *kernelTaskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, 0, len(tasks))
	if len(tasks) == 0 {
		return results, nil
	}

	// The wait bound applies to BOTH the subscription and the collection loop
	// (C2): the subscription is scoped to waitCtx, so a finished Dispatch
	// cancels it and the store releases its per-subscriber cleanup goroutine.
	// Subscribing with the raw parent ctx would leave every completed
	// Dispatch's subscription (and its goroutine) alive until the parent
	// context is cancelled — accumulating across dispatches.
	timeout := d.eventTimeout
	if timeout <= 0 {
		timeout = kernelDispatchTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Subscribe to the worker completion events BEFORE submitting so no
	// completion can be missed between submit and the collection loop
	// (mirrors dispatchViaEvents: subscribe-then-publish ordering). The
	// broadcast store delivers every matching event to every subscriber, so
	// the scheduler/trace/recovery consumers are unaffected.
	//
	// Pure legacy path (fabric == nil): the inner dispatcher runs each task
	// synchronously inside kernel.Dispatch, so no async completion event
	// exists to wait for — skip the subscription entirely and report the
	// synchronous success below.
	resultCh, subErr := d.subscribeResults(waitCtx)
	if subErr != nil && !errors.Is(subErr, errNoResultSubscription) {
		return d.failAll(tasks, "kernel dispatch: result collection unavailable: "+subErr.Error()), subErr
	}

	// Resolve the per-task final outcome as events arrive.
	pending := make(map[string]*models.Task, len(tasks))
	taskIndex := make(map[string]int, len(tasks))
	for i, task := range tasks {
		if task == nil {
			continue
		}
		pending[task.TaskID] = task
		taskIndex[task.TaskID] = i
		results = append(results, nil) // placeholder, filled by the collection loop
	}

	// Submit every task through the kernel (async: fabric Create; the
	// scheduler executes in the background). A submit error is a real failure
	// for that task — record it and drop it from the pending set.
	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := d.kernel.Dispatch(ctx, "", task.TaskID, dispatchPayload(task)); err != nil {
			idx := taskIndex[task.TaskID]
			res := models.NewTaskResult(task.TaskID, task.AgentType)
			res.SetError(err.Error())
			results[idx] = res
			delete(pending, task.TaskID)
		}
	}
	if len(pending) == 0 {
		return results, nil
	}

	// No event store, or no fabric at all: the results cannot be observed via
	// events. Two distinct cases:
	//
	//   - fabric == nil (pure legacy path): the inner dispatcher ran each task
	//     SYNCHRONOUSLY inside d.kernel.Dispatch above, so the task already
	//     completed. Report real success (the dispatch did happen) with an
	//     empty reason — this is not a fake worker output.
	//   - fabric != nil but no store: the worker runs in the background and
	//     cannot be observed without a store. Fail loudly rather than report
	//     fake success (code_rules_v2 §0.2).
	if resultCh == nil {
		if d.fabric == nil {
			for tid, task := range pending {
				idx := taskIndex[tid]
				res := models.NewTaskResult(tid, task.AgentType)
				res.SetSuccess(nil, "dispatched via kernel (legacy sync)")
				results[idx] = res
			}
			return results, nil
		}
		for tid, task := range pending {
			idx := taskIndex[tid]
			res := models.NewTaskResult(tid, task.AgentType)
			res.SetError("kernel dispatch: no event store, result collection disabled")
			results[idx] = res
		}
		return results, nil
	}

	// Block until every submitted task's final outcome is known or the
	// dispatch timeout elapses. This is the synchronous wait that turns the
	// kernel's async execution into the leader-expected blocking dispatch.
	// waitCtx was created above (before subscribing) and is cancelled on
	// return, which also releases the result subscription.
	for len(pending) > 0 {
		select {
		case ev, ok := <-resultCh:
			if !ok {
				// Stream closed: whatever is still pending can never be
				// observed. Fail them explicitly rather than leave nil
				// placeholders (which would aggregate as fake zeros).
				d.failPending(results, pending, taskIndex, "kernel dispatch: event stream closed before result")
				pending = map[string]*models.Task{}
				continue
			}
			tid, ok := ev.Payload["task_id"].(string)
			if !ok || tid == "" {
				continue
			}
			if _, wanted := pending[tid]; !wanted {
				continue
			}
			if res, done := d.resolveOutcome(ev, tid, pending[tid]); done {
				results[taskIndex[tid]] = res
				delete(pending, tid)
			}
		case <-waitCtx.Done():
			// Timeout / parent cancel: mark every still-pending task failed
			// with the reason so the leader never aggregates a fake success.
			d.failPending(results, pending, taskIndex, "kernel dispatch: timed out waiting for worker result: "+waitCtx.Err().Error())
			pending = map[string]*models.Task{}
		}
	}

	// Any nil placeholder left behind (a task was in tasks but never got a
	// result) must never surface: fail it explicitly.
	for i, task := range tasks {
		if task == nil {
			continue
		}
		if results[i] == nil {
			res := models.NewTaskResult(task.TaskID, task.AgentType)
			res.SetError("kernel dispatch: no result observed for task")
			results[i] = res
		}
	}
	return results, nil
}

// errNoResultSubscription signals that no event subscription is needed (no
// store, or pure legacy path without fabric). It is not an error: Dispatch
// falls back to the synchronous legacy success path.
var errNoResultSubscription = errors.New("kernel dispatch: no result subscription needed")

// subscribeResults opens the broadcast subscription on the shared event store
// for the worker's terminal events. Returns errNoResultSubscription when no
// subscription is needed (no store, or pure legacy path without fabric).
func (d *kernelTaskDispatcher) subscribeResults(ctx context.Context) (<-chan *ares_events.Event, error) {
	if d.store == nil || d.fabric == nil {
		return nil, errNoResultSubscription
	}
	return d.store.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			// The worker's terminal events. EventTaskCompleted fires from
			// both subAgent.Execute (carries EventKeyResult, the worker's
			// textual output) and fabric.record (task_id/agent_id/state
			// only). EventTaskFailed fires from subAgent.Execute (real
			// failure, carries error text) and from fabric.Fail (retry
			// requeue — followed by EventTaskReady — or final FAILED). The
			// collection loop resolves the final outcome per task.
			ares_events.EventTaskCompleted,
			ares_events.EventTaskFailed,
		},
	})
}

// failAll builds a failed TaskResult for every non-nil task in tasks.
func (d *kernelTaskDispatcher) failAll(tasks []*models.Task, reason string) []*models.TaskResult {
	results := make([]*models.TaskResult, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		res := models.NewTaskResult(task.TaskID, task.AgentType)
		res.SetError(reason)
		results = append(results, res)
	}
	return results
}

// failPending marks every task still in pending with a failed result. It
// writes through taskIndex into results and is used when the collection loop
// can no longer observe the tasks (stream closed / timeout / cancel).
func (d *kernelTaskDispatcher) failPending(results []*models.TaskResult, pending map[string]*models.Task, taskIndex map[string]int, reason string) {
	for tid, task := range pending {
		idx := taskIndex[tid]
		res := models.NewTaskResult(tid, task.AgentType)
		res.SetError(reason)
		results[idx] = res
	}
}

// dispatchPayload builds the agentipc dispatch payload for a task: the
// capability (agent_type), the DAG dependencies and any opaque user data. The
// UserProfile rides through as the same-process struct reference (no JSON
// round-trip) so the executor sees the real profile — without this it
// silently degrades to executeByType (empty result), the serve no-op chain.
func dispatchPayload(task *models.Task) map[string]any {
	payload := map[string]any{"agent_type": string(task.AgentType)}
	if task.Context != nil && len(task.Context.Dependencies) > 0 {
		payload["dependencies"] = append([]string(nil), task.Context.Dependencies...)
	}
	if task.Payload != nil {
		maps.Copy(payload, task.Payload)
	}
	if task.UserProfile != nil {
		payload["user_profile"] = task.UserProfile
	}
	return payload
}

// resolveOutcome decides whether ev is the final outcome for tid and, if so,
// builds the leader-visible TaskResult from the worker's real output. task is
// the pending task (non-nil when the caller found it in the pending set).
//
// Retry resolution: fabric.Fail publishes EventTaskFailed BEFORE a retry
// requeues the task (failed→ready→re-execute). A single failed event is
// therefore not proof of a final failure. The loop treats failed as final
// only when the event carries the worker's error text (subAgent.Execute
// emits KeyError; fabric.Fail does not) — a bare fabric failed is a retry in
// flight and stays pending until the retry's terminal event resolves it.
func (d *kernelTaskDispatcher) resolveOutcome(ev *ares_events.Event, tid string, task *models.Task) (*models.TaskResult, bool) {
	if task == nil {
		return nil, false
	}
	res := models.NewTaskResult(tid, task.AgentType)

	switch ev.Type {
	case ares_events.EventTaskCompleted:
		// Terminal success. Prefer the worker's structured output read back
		// from the fabric checkpoint; fall back to the event's text.
		if out := d.outcomeFromFabric(tid); out != nil {
			res.SetSuccess(out.items, out.reason)
			res.Metadata = out.metadata
			return res, true
		}
		if text, ok := ev.Payload[ares_events.EventKeyResult].(string); ok && text != "" {
			res.SetSuccess(nil, text)
			return res, true
		}
		// Neither the fabric checkpoint nor the event carries output: the
		// task genuinely completed with no result (e.g. a pure state-machine
		// transition). Success with an empty reason beats faking output.
		res.SetSuccess(nil, "kernel: task completed")
		return res, true
	case ares_events.EventTaskFailed:
		// A worker failure carries the error text under KeyError (subAgent
		// emits it on real failures and output-guard rejections). A
		// fabric-side failed carries only task_id/agent_id/state and is
		// ambiguous: it fires both when a retry requeues the task
		// (failed→ready→re-execute) and when the retry budget is exhausted
		// (final FAILED). Resolve the ambiguity against the fabric state —
		// the authoritative terminal state — instead of guessing from the
		// event alone:
		//   - fabric StateFailed  → final failure (retries exhausted): fail.
		//   - fabric StateReady   → retry in flight: not final, keep waiting.
		//   - no fabric / other   → fall back to the event's error text.
		if errMsg, ok := ev.Payload["error"].(string); ok && errMsg != "" {
			res.SetError(errMsg)
			return res, true
		}
		if d.fabric != nil {
			if tk, err := d.fabric.Task(tid); err == nil && tk != nil {
				if tk.State == taskfabric.StateFailed {
					res.SetError("kernel: task failed after retries exhausted")
					return res, true
				}
				// READY (or anything else): retry in flight, not final.
				return nil, false
			}
		}
		return nil, false
	}
	return nil, false
}

// outcomeFromFabric reads the worker's structured output back from the
// completed fabric task's checkpoint (the scheduler stored items/reason/
// metadata there via RunQuantum → CompleteWithCheckpoint). Returns nil when
// the fabric is not wired, the task is not yet COMPLETED, or the checkpoint
// carries no output.
func (d *kernelTaskDispatcher) outcomeFromFabric(tid string) *taskOutcome {
	if d.fabric == nil {
		return nil
	}
	tk, err := d.fabric.Task(tid)
	if err != nil || tk == nil {
		return nil
	}
	if tk.State != taskfabric.StateCompleted {
		return nil
	}
	return outcomeFromCheckpoint(tk)
}

// taskOutcome is the worker output read back from a completed fabric task.
type taskOutcome struct {
	items    []*models.RecommendItem
	reason   string
	metadata map[string]any
	err      string
}

// outcomeFromCheckpoint extracts the worker output the scheduler stored in
// the quantum checkpoint (see kernelScheduler.execute: items/reason/metadata
// ride inside a map[string]any). The completed checkpoint is a
// *taskfabric.CheckpointEnvelope (W3 schema; the meta is re-wrapped around
// every quantum's output via EncodeCheckpoint), so the step output is read
// from inside the envelope through the single shared decode path. A missing
// or non-map checkpoint means the task completed without a payload — still a
// success, just empty.
func outcomeFromCheckpoint(tk *taskfabric.Task) *taskOutcome {
	out := &taskOutcome{}
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		return out
	}
	cp, ok := dc.StepCheckpoint.(map[string]any)
	if !ok || cp == nil {
		return out
	}
	if items, ok := cp["items"]; ok {
		if list, ok := items.([]*models.RecommendItem); ok {
			out.items = list
		}
	}
	if reason, ok := cp["reason"].(string); ok {
		out.reason = reason
	}
	if md, ok := cp["metadata"].(map[string]any); ok {
		out.metadata = md
	}
	if e, ok := cp["error"].(string); ok && e != "" {
		out.err = e
	}
	return out
}

// taskFromPayload builds a models.Task from the agentipc dispatch arguments.
// The payload is a map carrying the task's AgentType (capability), its DAG
// dependencies (Task Fabric gate, ares-runtime.md §9) and any opaque user
// data; absent metadata falls back to a default type.
func taskFromPayload(taskID string, payload any) (*models.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task id required")
	}
	task := models.NewTask(taskID, models.AgentTypeTop, nil)
	if m, ok := payload.(map[string]any); ok {
		if at, ok := m["agent_type"].(string); ok && at != "" {
			task.AgentType = models.AgentType(at)
		}
		// UserProfile arrives as the same-process struct reference (the
		// kernelTaskDispatcher passes it through untouched) — OR as a plain
		// map after a JSON round-trip (web serve → HTTP → decode). Both are
		// restored so the executor never sees profile==nil and degrades to
		// executeByType — the serve no-op chain.
		if up, ok := m["user_profile"].(*models.UserProfile); ok && up != nil {
			task.UserProfile = up
		} else if raw, ok := m["user_profile"].(map[string]any); ok {
			if buf, err := json.Marshal(raw); err == nil {
				var up models.UserProfile
				if err := json.Unmarshal(buf, &up); err == nil {
					task.UserProfile = &up
				}
			}
		}
		// Dependencies arrive as []string when the payload passes through the
		// kernel dispatcher directly (kernelTaskDispatcher.Dispatch) and as
		// []any after a JSON round-trip — accept both so the DAG gate is
		// never silently dropped.
		switch deps := m["dependencies"].(type) {
		case []string:
			task.Context.Dependencies = append(task.Context.Dependencies, deps...)
		case []any:
			for _, dep := range deps {
				if s, ok := dep.(string); ok && s != "" {
					task.Context.Dependencies = append(task.Context.Dependencies, s)
				}
			}
		}
		task.Payload = m
	}
	return task, nil
}
