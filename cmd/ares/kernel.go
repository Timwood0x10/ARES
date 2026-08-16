package main

import (
	"context"
	"fmt"
	"maps"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Kernel assembly entry (P4 D4: parallel + feature flag gradual cutover).
//
// wireKernelDispatcher assembles the dual-track dispatch kernel:
//
//   - legacy:   the existing leader.TaskDispatcher (real dispatch, unchanged
//     behavior — the current production path).
//   - newPath:  a taskfabric capability-aware scoring dispatcher (shadow only:
//     it computes "who would the Kernel pick" via taskfabric.Score/Pick without
//     calling fabric.Acquire, so it never double-runs a task).
//
// The PolicyFlag defaults to PolicyLegacyLeader (safety) with shadow mode ON,
// so the new path starts "turning" immediately as an observer: every legacy
// dispatch also scores the same task and counts mismatches (Mismatches()). An
// operator flips the flag to PolicyTaskFabric once shadow equivalence is
// verified (D4 gradual cutover).
//
// Returns:
//   - *agentipc.DualTrackDispatcher: the assembled kernel dispatcher.
//   - *agentipc.PolicyFlag: the feature flag (flip it to cut over).
func wireKernelDispatcher(
	leaderDispatcher leader.TaskDispatcher,
	subAgents []subAgentCapability,
) (*agentipc.DualTrackDispatcher, *agentipc.PolicyFlag) {
	flag := agentipc.NewPolicyFlag(agentipc.PolicyLegacyLeader)
	legacy := &kernelLegacyDispatcher{inner: leaderDispatcher}
	newPath := &kernelFabricDispatcher{candidates: subAgents}
	return agentipc.NewDualTrackDispatcher(flag, legacy, newPath, true), flag
}

// subAgentCapability is the minimal capability surface the new-path scorer
// needs for one agent (its type is the declared capability chain).
type subAgentCapability struct {
	ID   string
	Type string
	Load float64
}

// kernelTaskDispatcher adapts the agentipc.DualTrackDispatcher (single-task
// surface) back to the leader.TaskDispatcher batch surface. The leader keeps
// calling Dispatch(ctx, tasks) as before; each task is routed through the
// kernel dispatcher, so shadow scoring runs for every task without any change
// to leader behavior.
type kernelTaskDispatcher struct {
	kernel *agentipc.DualTrackDispatcher
}

// Dispatch routes every task through the kernel dispatcher and aggregates the
// per-task outcomes into the leader-expected []*models.TaskResult shape.
func (d *kernelTaskDispatcher) Dispatch(ctx context.Context, tasks []*models.Task) ([]*models.TaskResult, error) {
	results := make([]*models.TaskResult, 0, len(tasks))
	for _, task := range tasks {
		if task == nil {
			continue
		}
		payload := map[string]any{"agent_type": string(task.AgentType)}
		if task.Payload != nil {
			maps.Copy(payload, task.Payload)
		}
		err := d.kernel.Dispatch(ctx, "", task.TaskID, payload)
		res := models.NewTaskResult(task.TaskID, task.AgentType)
		if err != nil {
			res.SetError(err.Error())
		} else {
			res.SetSuccess(nil, "dispatched via kernel")
		}
		results = append(results, res)
	}
	return results, nil
}

// kernelLegacyDispatcher adapts the existing leader.TaskDispatcher to the
// agentipc.Dispatcher single-task surface. Each D call is forwarded to the
// leader's batch Dispatch (the exact legacy behavior); errors pass through.
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

// kernelFabricDispatcher is the shadow new-path dispatcher: it scores the task
// against the candidate agents with the Kernel's capability-aware formula
// (taskfabric.Score/Pick) and records the would-be winner. It never acquires
// or executes — the task is NOT double-run. This is the "shadow mode turning"
// surface: the dual-track dispatcher compares this outcome against the legacy
// dispatch via Mismatches().
type kernelFabricDispatcher struct {
	candidates []subAgentCapability
}

// D scores the task against candidates and returns the would-be winner. For
// tasks with a non-empty capability, no capable candidate is a shadow failure
// (the Kernel would reject the dispatch); for unconstrained tasks any
// candidate is acceptable.
func (d *kernelFabricDispatcher) D(ctx context.Context, agentID, taskID string, payload any) error {
	task, err := taskFromPayload(taskID, payload)
	if err != nil {
		return fmt.Errorf("kernel fabric dispatch: %w", err)
	}
	if len(d.candidates) == 0 {
		return nil
	}
	cands := make([]taskfabric.Candidate, 0, len(d.candidates))
	for _, c := range d.candidates {
		cands = append(cands, taskfabric.Candidate{
			AgentID:      c.ID,
			Capabilities: []string{c.Type},
			Load:         c.Load,
			Confidence:   1.0, // shadow: no experience store wired here
		})
	}
	if winner := taskfabric.Pick(string(task.AgentType), cands); winner == nil {
		return taskfabric.ErrNoCapableCandidate
	}
	return nil
}

// taskFromPayload builds a models.Task from the agentipc dispatch arguments.
// The payload is a map carrying the task's AgentType (capability) and any
// opaque user data; absent metadata falls back to the agentID-derived type.
func taskFromPayload(taskID string, payload any) (*models.Task, error) {
	if taskID == "" {
		return nil, fmt.Errorf("task id required")
	}
	task := models.NewTask(taskID, models.AgentTypeTop, nil)
	if m, ok := payload.(map[string]any); ok {
		if at, ok := m["agent_type"].(string); ok && at != "" {
			task.AgentType = models.AgentType(at)
		}
		task.Payload = m
	}
	return task, nil
}
