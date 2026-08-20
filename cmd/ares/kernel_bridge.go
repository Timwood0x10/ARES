package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/agentipc"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Kernel assembly entry (P4 D4: parallel + feature flag gradual cutover).
//
// wireKernelDispatcher assembles the dual-track dispatch kernel:
//
//   - legacy:   the leader.TaskDispatcher path (real dispatch, unchanged
//     behavior — retained as an explicit opt-out via kernel.policy=legacy).
//   - newPath:  a taskfabric capability-aware scoring dispatcher. It is a
//     pure observer in this stage: it computes "who would the Kernel pick" via
//     taskfabric.Score/Pick without creating, acquiring or executing, so a
//     task is never double-run.
//
// The PolicyFlag starts at PolicyLegacyLeader (safety) with shadow mode ON, so
// the new path begins as an observer: every legacy dispatch also scores the
// same task and counts mismatches (Mismatches()). Production flips to
// PolicyTaskFabric by default — wireKernelPolicy treats any value other than
// explicit "legacy" as taskfabric (D4 gradual cutover completed); the real
// Create→Schedule→Acquire→RunQuantum executor is wired at that point.
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
// Order matters for a live mid-run flip: shadow is disabled BEFORE the new
// path is swapped in, so a dispatch racing the flip can never run legacy in
// shadow against the executing new path (double execution). In-flight legacy
// dispatches complete synchronously (Dispatch blocks until the legacy path
// returns), so nothing is orphaned.
//
// Args:
//   - kernel: the dual-track dispatcher assembled by wireKernelDispatcher.
//   - fabric: the Task Fabric that executes tasks.
func enableKernelExecution(
	kernel *agentipc.DualTrackDispatcher,
	fabric *taskfabric.Fabric,
) {
	// Turn shadow off first: with the new path about to become live, running
	// legacy in shadow would re-dispatch every task (double execution).
	kernel.SetShadow(false)
	// Replace the shadow-only new path with the submitting one. IMPORTANT: the
	// leader dispatch only SUBMITS the task to the fabric (Create); the
	// kernelScheduler is the single executor (Schedule→Acquire→RunQuantum on
	// every READY task). Keeping the full execution in the dispatch path as
	// well caused a double-path race: both the leader dispatch and the
	// scheduler tried to acquire the same task, surfacing as
	// "task not ready for acquire" in serve logs (GAP #2 fix).
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
// "task not ready for acquire" in serve logs (GAP #2 fix).
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
	var deps []string
	if task.Context != nil {
		deps = append([]string(nil), task.Context.Dependencies...)
	}
	if err := fabric.Create(&taskfabric.Task{
		ID:           task.TaskID,
		Capability:   string(task.AgentType),
		Dependencies: deps,
		Priority:     task.Priority,
		// RetryPolicy.MaxRetries counts TOTAL attempts, not retries-after-the-first
		// (taskfabric.CanRetry: Attempts < MaxRetries). MaxRetries: 1 therefore
		// grants ZERO retries — a transient failure finalizes FAILED immediately
		// (v0.3.0 review Bug 2). 2 = first attempt + one retry.
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		// Carry the submission-time metadata in the Checkpoint slot so the
		// scheduler's toModelTask can restore it for the executor (LLM path
		// needs the profile; the outcome recorder needs UsedExperienceID).
		// The envelope is the W3 versioned protocol (*CheckpointEnvelope);
		// a genuine progress checkpoint replaces it once a quantum runs
		// (RunQuantum yield).
		Checkpoint: &taskfabric.CheckpointEnvelope{
			UserProfile:      task.UserProfile,
			Payload:          task.Payload,
			UsedExperienceID: task.UsedExperienceID,
		},
	}); err != nil && err != taskfabric.ErrTaskExists {
		return fmt.Errorf("kernel fabric create: %w", err)
	}
	return nil
}

// subAgentCapability is the minimal capability surface the new-path scorer
// needs for one agent (its type is the declared capability chain).
type subAgentCapability struct {
	ID   string
	Type string
	Load float64
}
