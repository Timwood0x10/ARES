package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// Kernel assembly entry (leader path removed).
//
// wireKernelDispatcher assembles the single-track Task Fabric dispatch kernel:
// the legacy leader track has been deleted, so the dispatcher always routes
// through kernelFabricDispatcher — scoring first, then (after
// enableKernelExecution attaches the executor) real
// Create→Schedule→Acquire→RunQuantum execution. The PolicyFlag starts at
// PolicyTaskFabric with shadow mode OFF (there is no legacy track left to
// compare against).
//
// Returns:
//   - *agentipc.DualTrackDispatcher: the assembled kernel dispatcher.
//   - *agentipc.PolicyFlag: the execution policy flag.
//
// TODO(tech-debt): agentipc has no retry/dead-letter semantics (the legacy ahp
// DLQProcessor was removed with the leader-sub protocol). Wire IPC retry or a
// dead-letter path when multi-agent messaging scales.
func wireKernelDispatcher(
	subAgents []subAgentCapability,
) (*agentipc.DualTrackDispatcher, *agentipc.PolicyFlag) {
	flag := agentipc.NewPolicyFlag(agentipc.PolicyTaskFabric)
	newPath := &kernelFabricDispatcher{candidates: subAgents}
	// nil legacy track: the leader path is removed, so the "dual" track is
	// single-track from the start and shadow mode is off.
	return agentipc.NewDualTrackDispatcher(flag, nil, newPath, false), flag
}

// enableKernelExecution switches the kernel's Task Fabric path from scoring
// (shadow) to real execution: it attaches the submitting executor (Create with
// DAG edges — the kernelScheduler owns Schedule→Acquire→RunQuantum) to the
// dispatcher. Callers invoke this at startup (peer mode) in the same critical
// section as the flag set to PolicyTaskFabric.
//
// Args:
//   - kernel: the dispatcher assembled by wireKernelDispatcher.
//   - fabric: the Task Fabric that executes tasks.
func enableKernelExecution(
	kernel *agentipc.DualTrackDispatcher,
	fabric *taskfabric.Fabric,
) {
	// Turn shadow off first: with the new path about to become live, running
	// the previous path in shadow would re-dispatch every task (double
	// execution).
	kernel.SetShadow(false)
	// Replace the scoring-only path with the submitting one. IMPORTANT: the
	// dispatch only SUBMITS the task to the fabric (Create); the kernelScheduler
	// is the single executor (Schedule→Acquire→RunQuantum on every READY task).
	// Keeping the full execution in the dispatch path as well caused a
	// double-path race: both the dispatch and the scheduler tried to acquire the
	// same task, surfacing as "task not ready for acquire" in serve logs.
	exec := &kernelFabricDispatcher{
		candidates: kernelNewPathCandidates(kernel),
		executeFn: func(ctx context.Context, task *models.Task) error {
			return submitFabricTask(ctx, fabric, task)
		},
	}
	kernel.SetNewPath(exec)
}

// kernelNewPathCandidates extracts the candidate list from the kernel's new
// path so enableKernelExecution can rebuild it with an executor attached.
func kernelNewPathCandidates(kernel *agentipc.DualTrackDispatcher) []subAgentCapability {
	if fp, ok := kernel.NewPath().(*kernelFabricDispatcher); ok {
		return fp.candidates
	}
	return nil
}

// submitFabricTask SUBMITS a task to the Task Fabric (Create with DAG edges)
// WITHOUT executing it. Execution is the kernelScheduler's sole job: its
// drain runs Schedule→Acquire→RunQuantum on every READY task. The leader
// dispatch path must NOT also schedule the task — doing so created a
// double-path race where both the leader dispatch (executeFabricTask) and the
// kernelScheduler tried to acquire the same task, surfacing as
// "task not ready for acquire" in serve logs.
//
// Args:
//   - ctx: task lifetime (unused; kept for signature symmetry).
//   - fabric: the Task Fabric that owns the task.
//   - task: the task to submit.
//
// Returns:
//   - error: fabric create error (ErrTaskExists is tolerated).
func submitFabricTask(
	ctx context.Context,
	fabric *taskfabric.Fabric,
	task *models.Task,
) error {
	if fabric == nil {
		return taskfabric.ErrTaskNotFound
	}
	// Single execution path. The dispatcher only materializes
	// L2-routable tasks — anything else would starve with no candidate
	// executor. Fail fast instead of creating an unrunnable task.
	if !agentfabric.IsL2Capability(string(task.AgentType)) {
		return fmt.Errorf("kernel bridge: agent type %q is not L2-routable (want ares/plan, ares/answer, ares/root, or tool/<name>)", task.AgentType)
	}
	var deps []string
	if task.Context != nil {
		deps = append([]string(nil), task.Context.Dependencies...)
	}
	// Carry the submission-time metadata in the Checkpoint slot so the
	// scheduler's toModelTask can restore it for the executor (LLM path needs
	// the profile; the outcome recorder needs UsedExperienceID). The envelope
	// is the versioned protocol; a genuine progress checkpoint replaces it once
	// a quantum runs (RunQuantum yield). Built through the constructor so the
	// schema version is always stamped.
	env := taskfabric.NewCheckpointEnvelope(task.Payload)
	env.UserProfile = task.UserProfile
	env.UsedExperienceID = task.UsedExperienceID
	env.TenantID = task.TenantID

	if err := fabric.Create(&taskfabric.Task{
		ID:           task.TaskID,
		Capability:   string(task.AgentType),
		Dependencies: deps,
		Priority:     task.Priority,
		// Origin stays "" — kernel-bridge submissions are root tasks
		// (user/submitter-originated), not agent-created. Agent-created
		// tasks carry their caller via Task.Origin (create_task syscall).
		// RetryPolicy.MaxRetries counts TOTAL attempts, not retries-after-the-first
		// (taskfabric.CanRetry: Attempts < MaxRetries). MaxRetries: 1 therefore
		// grants ZERO retries — a transient failure finalizes FAILED immediately
		// (once a review bug). 2 = first attempt + one retry.
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		Checkpoint:  env,
	}); err != nil && err != taskfabric.ErrTaskExists {
		return fmt.Errorf("kernel fabric create: %w", err)
	}
	return nil
}

// subAgentCapability is the minimal capability surface the new-path scorer
// needs for one agent. Caps is the full declared capability set (flat
// peers); Type is the primary capability (first of Caps, or the legacy single
// Type for sub-structure configs).
type subAgentCapability struct {
	ID   string
	Type string
	Caps []string
	Load float64
}

// kernelFabricDispatcher is the kernel's Task Fabric dispatch path. Its D()
// behavior depends on whether an executeFn is attached (enableKernelExecution):
//
//   - scoring mode (no executeFn): scores the task against the candidate
//     agents with the Kernel's capability-aware formula (taskfabric.Score/Pick)
//     and reports the would-be outcome. It never creates, acquires or executes
//     — a task is never double-run.
//   - execution mode (executeFn attached): runs the real Task Fabric path
//     (Create → Schedule → Acquire → RunQuantum).
type kernelFabricDispatcher struct {
	candidates []subAgentCapability
	executeFn  func(ctx context.Context, task *models.Task) error // nil = scoring only
}

// D routes the task through the kernel's Task Fabric path: scoring (no
// executeFn) or real execution (executeFn attached).
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

// taskFromPayload builds a models.Task from the agentipc dispatch arguments.
// The payload is a map carrying the task's AgentType (capability), its DAG
// dependencies (Task Fabric gate) and any opaque user
// data; absent metadata falls back to a default type.
func taskFromPayload(taskID string, payload any) (*models.Task, error) {
	if taskID == "" {
		return nil, errors.New("task id required")
	}
	task := models.NewTask(taskID, models.AgentTypeTop, nil)
	if m, ok := payload.(map[string]any); ok {
		if at, ok := m["agent_type"].(string); ok && at != "" {
			task.AgentType = models.AgentType(at)
		}
		// UserProfile arrives as the same-process struct reference (the
		// kernel dispatcher passes it through untouched) — OR as a plain
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
		// kernel dispatcher directly and as []any after a JSON round-trip —
		// accept both so the DAG gate is never silently dropped.
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
