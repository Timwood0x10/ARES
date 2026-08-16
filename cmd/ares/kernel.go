package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Kernel assembly entry (P4 D4: parallel + feature flag gradual cutover).
//
// wireKernelDispatcher assembles the dual-track dispatch kernel:
//
//   - legacy:   the existing leader.TaskDispatcher (real dispatch, unchanged
//     behavior — the current production path).
//   - newPath:  a taskfabric capability-aware scoring dispatcher. It is a
//     pure observer in this stage: it computes "who would the Kernel pick" via
//     taskfabric.Score/Pick without creating, acquiring or executing, so a
//     task is never double-run.
//
// The PolicyFlag defaults to PolicyLegacyLeader (safety) with shadow mode ON,
// so the new path starts "turning" immediately as an observer: every legacy
// dispatch also scores the same task and counts mismatches (Mismatches()). An
// operator flips the flag to PolicyTaskFabric once shadow equivalence is
// verified (D4 gradual cutover); the real Create→Schedule→Acquire→RunQuantum
// executor is wired at that point.
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

// enableKernelExecution switches the kernel's new path from shadow (scoring
// only) to real Task Fabric execution, and turns shadow mode off on the
// dual-track dispatcher so the legacy path is not re-run for every task (which
// would double-execute). Callers invoke this in the same critical section as
// flag.Set(PolicyTaskFabric).
//
// Args:
//   - kernel: the dual-track dispatcher assembled by wireKernelDispatcher.
//   - fabric: the Task Fabric that executes tasks.
//   - executors: agentID → sub.Agent that runs the acquired task.
func enableKernelExecution(
	kernel *agentipc.DualTrackDispatcher,
	fabric *taskfabric.Fabric,
	executors map[string]sub.Agent,
) {
	// Replace the shadow-only new path with the executing one.
	exec := &kernelFabricDispatcher{
		candidates: kernelNewPathCandidates(kernel),
		executeFn: func(ctx context.Context, task *models.Task) error {
			return executeFabricTask(ctx, fabric, executors, task)
		},
	}
	kernel.SetNewPath(exec)
	// Turn shadow off: with the new path live, running legacy in shadow would
	// re-dispatch every task (double execution).
	kernel.SetShadow(false)
}

// kernelNewPathCandidates extracts the candidate list from the kernel's new
// path so enableKernelExecution can rebuild it with an executor attached.
func kernelNewPathCandidates(kernel *agentipc.DualTrackDispatcher) []subAgentCapability {
	if fp, ok := kernel.NewPath().(*kernelFabricDispatcher); ok {
		return fp.candidates
	}
	return nil
}

// executeFabricTask runs the full Task Fabric path for one task: Create →
// Schedule (capability-aware) → Acquire (lease + fencing) → RunQuantum (one
// agent step) → finalize. It is the real (non-shadow) new-path body.
func executeFabricTask(
	ctx context.Context,
	fabric *taskfabric.Fabric,
	executors map[string]sub.Agent,
	task *models.Task,
) error {
	if fabric == nil {
		return taskfabric.ErrTaskNotFound
	}
	if err := fabric.Create(&taskfabric.Task{
		ID:          task.TaskID,
		Capability:  string(task.AgentType),
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		Checkpoint:  nil,
	}); err != nil && err != taskfabric.ErrTaskExists {
		return fmt.Errorf("kernel fabric create: %w", err)
	}
	cands := make([]taskfabric.Candidate, 0, len(executors))
	for agentID, agent := range executors {
		if agent == nil {
			continue
		}
		cands = append(cands, taskfabric.Candidate{
			AgentID:      agentID,
			Capabilities: []string{string(agent.Type())},
			Load:         float64(len(executors)) / 10,
			Confidence:   1.0,
		})
	}
	winner, epoch, err := fabric.Schedule(task.TaskID, cands, 5*time.Minute)
	if err != nil {
		return fmt.Errorf("kernel fabric schedule: %w", err)
	}
	executor, ok := executors[winner]
	if !ok {
		_ = fabric.Release(task.TaskID, winner, epoch)
		return fmt.Errorf("kernel fabric: executor %q not registered", winner)
	}
	return fabric.RunQuantum(task.TaskID, winner, epoch, func() (any, bool, error) {
		res, execErr := executor.Execute(ctx, task)
		if execErr != nil {
			return nil, false, execErr
		}
		if res != nil && res.Error != "" {
			return nil, false, fmt.Errorf("%s", res.Error)
		}
		return map[string]any{"result": "ok"}, true, nil
	})
}

// subAgentCapability is the minimal capability surface the new-path scorer
// needs for one agent (its type is the declared capability chain).
type subAgentCapability struct {
	ID   string
	Type string
	Load float64
}

// kernelHandle carries the assembled dual-track kernel from agent construction
// to the serve wiring so the policy can be flipped and the Task Fabric
// scheduler started per configuration.
type kernelHandle struct {
	dual *agentipc.DualTrackDispatcher
	flag *agentipc.PolicyFlag
}

// wireKernelPolicy applies the configured dispatch policy to the assembled
// kernel:
//
//   - "taskfabric": flips the flag to PolicyTaskFabric, replaces the shadow
//     scorer with the real Task Fabric executor (Create→Schedule→Acquire→
//     RunQuantum via the sub-agent executors), turns shadow mode off (avoiding
//     double execution) and starts the kernelScheduler to drain ReadyTasks.
//   - anything else (default "legacy"): keeps the leader path live; the Task
//     Fabric path stays in shadow (scores only, Mismatches observable).
//
// The flip happens once at startup. A live mid-run flip is intentionally not
// wired yet (ares-runtime.md P4 D4): it requires the scheduler to take over
// from a running leader without orphaning in-flight tasks.
func wireKernelPolicy(
	ctx context.Context,
	cfg *ares_config.Config,
	kernel *kernelHandle,
	subAgents []sub.Agent,
) {
	if kernel == nil || kernel.dual == nil || kernel.flag == nil {
		return
	}
	if strings.ToLower(strings.TrimSpace(cfg.Kernel.Policy)) != "taskfabric" {
		log.Printf("kernel: policy=legacy (default), Task Fabric path in shadow (Mismatches observable)")
		return
	}
	// Build the fabric and executor registry for the real path.
	fabric := taskfabric.NewFabric()
	executors := make(map[string]sub.Agent, len(subAgents))
	for _, a := range subAgents {
		if a != nil {
			executors[a.ID()] = a
		}
	}
	enableKernelExecution(kernel.dual, fabric, executors)
	kernel.flag.Set(agentipc.PolicyTaskFabric)
	// Start the no-leader scheduler: it drains ReadyTasks and runs them via
	// the fabric path. The leader stays registered but stops receiving new
	// dispatches (the kernel routes to the fabric path now).
	sched := NewKernelScheduler(fabric, executors)
	log.Printf("kernel: policy=taskfabric, Task Fabric scheduler started (%d executors)", len(executors))
	go sched.Run(ctx)
}

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

// taskFromPayload builds a models.Task from the agentipc dispatch arguments.
// The payload is a map carrying the task's AgentType (capability) and any
// opaque user data; absent metadata falls back to a default type.
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
