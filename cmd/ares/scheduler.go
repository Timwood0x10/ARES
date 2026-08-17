package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// kernelScheduler is the "no leader" execution engine (ares-runtime.md §核心
// 命题: Agents are not orchestrated. They are scheduled). It repeatedly drains
// the fabric's ReadyTasks — the work source — and for each ready task:
//
//	Schedule (capability-aware) → Acquire (lease + fencing) → RunQuantum (one
//	agent step) → finalize (COMPLETED / FAILED / SUSPENDED).
//
// No leader decides "B is done, now run C"; the fabric's dependency-completed
// states make C ready. The scheduler is only a consumer of ReadyTasks.
//
// Failure policy: a scheduling or execution failure for one task is logged and
// the loop continues — one bad task must never take down the scheduler.
type kernelScheduler struct {
	fabric    *taskfabric.Fabric
	executors map[string]sub.Agent
	// pollInterval is how often ReadyTasks is drained (default 500ms).
	pollInterval time.Duration
	// ttl is the lease granted to each winning agent.
	ttl time.Duration
	// scheduled counts successfully executed tasks (for observability).
	scheduled int
	// noCandidateMu guards lastNoCandidateLog for throttling unschedulable-task
	// logs.
	noCandidateMu      sync.Mutex
	lastNoCandidateLog time.Time
}

// noCandidateLogInterval throttles "no capable candidate" logs to one per
// window — the condition is a waiting state, not an error worth per-poll noise.
const noCandidateLogInterval = 5 * time.Second

// NewKernelScheduler creates a scheduler over a fabric with the given
// executors (agentID → sub.Agent).
//
// Args:
//   - fabric: the Task Fabric backing this scheduler.
//   - executors: the agent registry (agentID → sub.Agent).
//
// Returns:
//   - *kernelScheduler: ready to Run.
func NewKernelScheduler(fabric *taskfabric.Fabric, executors map[string]sub.Agent) *kernelScheduler {
	return &kernelScheduler{
		fabric:       fabric,
		executors:    executors,
		pollInterval: 500 * time.Millisecond,
		ttl:          5 * time.Minute,
	}
}

// Run drains ReadyTasks until ctx is cancelled or the fabric becomes nil.
// It runs synchronously; callers start it in a goroutine. Panics from one
// task's execution are recovered so a single bad step cannot kill the loop.
//
// Args:
//   - ctx: lifetime of the scheduling loop.
func (s *kernelScheduler) Run(ctx context.Context) {
	if s.fabric == nil {
		log.Printf("kernel scheduler: fabric nil, scheduler disabled")
		return
	}
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.drain(ctx)
		}
	}
}

// drain executes every currently ready task.
func (s *kernelScheduler) drain(ctx context.Context) {
	for _, taskID := range s.fabric.ReadyTasks() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		func() {
			defer func() {
				if recover() != nil {
					log.Printf("kernel scheduler: panic executing task %q, continuing", taskID)
				}
			}()
			if err := s.execute(ctx, taskID); err != nil {
				s.logFailure(taskID, err)
			}
		}()
	}
}

// logFailure logs a task failure, throttling ErrNoCapableCandidate: an
// unschedulable task is a legitimate "waiting for a capable agent" state that
// the scheduler re-polls every interval, so it must not spam the log. Other
// errors are logged every time (they are transient and need attention).
func (s *kernelScheduler) logFailure(taskID string, err error) {
	if err == taskfabric.ErrNoCapableCandidate {
		now := time.Now()
		s.noCandidateMu.Lock()
		defer s.noCandidateMu.Unlock()
		if now.Sub(s.lastNoCandidateLog) < noCandidateLogInterval {
			return
		}
		s.lastNoCandidateLog = now
	}
	log.Printf("kernel scheduler: execute task %q failed: %v", taskID, err)
}

// execute runs the full fabric path for one task: Schedule → Acquire →
// RunQuantum (delegating the actual work to the winning sub-agent) →
// finalize. Errors are returned to the caller for logging; the fabric
// state machine (RetryPolicy) decides requeue vs. final failure.
func (s *kernelScheduler) execute(ctx context.Context, taskID string) error {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return err
	}
	// Build the candidate list from the registered executors so scheduling is
	// always consistent with what can actually run. Each candidate declares its
	// OWN capabilities (from the agent's Type), NOT the task's — the scorer
	// compares the task's required capability against what the agent can do.
	cands := make([]taskfabric.Candidate, 0, len(s.executors))
	for agentID, agent := range s.executors {
		if agent == nil {
			continue
		}
		cands = append(cands, taskfabric.Candidate{
			AgentID:      agentID,
			Capabilities: []string{string(agent.Type())},
			Load:         float64(len(s.executors)) / 10, // coarse; replaced by real load in P5
			Confidence:   1.0,                            // shadow: experience not threaded here
		})
	}
	if len(cands) == 0 {
		return taskfabric.ErrNoCapableCandidate
	}
	winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)
	if err != nil {
		return err
	}
	executor, ok := s.executors[winner]
	if !ok || executor == nil {
		// Release so the task can be retried by another agent. A failed
		// release leaves the task leased — surface it instead of dropping
		// the error (code_rules_v2 §3.1).
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Printf("kernel scheduler: release %q for missing executor %q failed: %v", taskID, winner, releaseErr)
		}
		return nil
	}
	err = s.fabric.RunQuantum(taskID, winner, epoch, func() (any, bool, error) {
		res, execErr := executor.Execute(ctx, s.toModelTask(tk))
		if execErr != nil {
			// A step error flows to fabric.Fail, which requeues (retry budget)
			// or finalizes FAILED — the fabric owns the retry policy.
			return nil, false, execErr
		}
		if res != nil && res.Error != "" {
			return nil, false, fmt.Errorf("%s", res.Error)
		}
		return map[string]any{"result": "ok"}, true, nil
	})
	if err == nil {
		s.scheduled++
	}
	return err
}

// toModelTask maps a fabric Task back to the models.Task shape the sub-agent
// executor expects. The fabric checkpoint (durable progress) rides in the
// payload so a resumed quantum can observe where the previous step left off.
func (s *kernelScheduler) toModelTask(tk *taskfabric.Task) *models.Task {
	t := models.NewTask(tk.ID, models.AgentType(tk.Capability), nil)
	if tk.Checkpoint != nil {
		t.Payload = map[string]any{"checkpoint": tk.Checkpoint}
	}
	return t
}
