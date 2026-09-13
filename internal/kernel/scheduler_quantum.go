package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/core/models"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/tenantctx"
)

// quantumUsage is the side-channel ledger for one quantum: the LLM tokens it
// spent, read from the step result metadata by buildQuantumStep's closure and
// consumed by the caller after RunQuantum returns. The closure runs under
// RunQuantum, which blocks on the step result — that channel handoff is the
// happens-before edge that makes the plain field race-free.
type quantumUsage struct {
	tokens int
}

// buildQuantumStep constructs the QuantumStep closure RunQuantum executes:
// it runs the executor's step and translates the outcome into fabric state
// transitions (error → Fail, !Done → Yield with checkpoint, Done → Complete
// with the worker result riding in the checkpoint envelope).
//
// Args:
//   - ctx: the quantum's execution context (cancelled on scheduler shutdown).
//   - executor: the winning agent running this step.
//   - tk: the fabric task snapshot taken at acquire time.
//   - meta: the submission metadata decoded from the task checkpoint; it is
//     re-wrapped around EVERY quantum output so UserProfile/Payload survive
//     arbitrary yield→resume cycles.
func (s *Scheduler) buildQuantumStep(
	ctx context.Context,
	executor CapabilityExecutor,
	tk *taskfabric.Task,
	meta taskfabric.DecodedCheckpoint,
	usage *quantumUsage,
) taskfabric.QuantumStep {
	return func() (any, bool, error) {
		// Cancellation + panic boundary around the executor step. The step
		// runs in its own goroutine so a stuck executor cannot block drain
		// shutdown forever: when ctx is cancelled (scheduler shutdown), the
		// quantum returns an error immediately, RunQuantum applies the retry
		// policy, and the drain goroutine finishes. The abandoned step
		// goroutine exits whenever the executor returns and its result is
		// discarded (fencing rejects any late completion anyway). The
		// goroutine-local recover keeps an executor panic from crashing the
		// process now that the step no longer runs on the drain goroutine.
		type stepResult struct {
			out *sub.StepOutcome
			err error
		}
		done := make(chan stepResult, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- stepResult{err: apperrors.Kernel("run_quantum", "executor_panic",
						tk.ID, executor.ID(), fmt.Errorf("executor panicked: %v", r))}
				}
			}()
			// The task's tenant rides the quantum's context: every
			// cognition, tool call and knowledge query downstream resolves
			// tenantctx.From(ctx) instead of a process-global, so recall is
			// scoped per request (the fix for the AKG read/write coherence
			// gap that akgActiveNamespace used to paper over).
			mt := s.ToModelTask(tk)
			out, stepErr := executor.ExecuteStep(tenantctx.With(ctx, mt.TenantID), mt)
			done <- stepResult{out: out, err: stepErr}
		}()
		var out *sub.StepOutcome
		var stepErr error
		select {
		case res := <-done:
			out, stepErr = res.out, res.err
		case <-ctx.Done():
			return nil, false, fmt.Errorf("quantum aborted by scheduler shutdown: %w", ctx.Err())
		}
		if stepErr != nil {
			// A step error is returned to RunQuantum, which owns the fabric
			// transition: a genuine failure goes through Fail (retry-budget
			// requeue or terminal FAILED), while a cancellation — which is
			// what the ctx.Done case above produces on scheduler shutdown —
			// is RELEASED instead, so it neither burns Attempts nor cascades
			// FAILED downstream (see taskfabric.isCancellation).
			return nil, false, stepErr
		}
		if out == nil {
			return nil, false, apperrors.Kernel("run_quantum", "nil_step_outcome", tk.ID, executor.ID(), ErrNilStepOutcome)
		}
		if out.Result != nil && out.Result.Error != "" {
			return nil, false, apperrors.Kernel("run_quantum", "step_error", tk.ID, executor.ID(), errors.New(out.Result.Error))
		}
		// Capture THIS quantum's token spend for the post-quantum governance
		// accounting (consumed by the caller after RunQuantum returns). Read
		// once here so the yield/done branches below stay the checkpoint's
		// concern. A failed/nil step contributed nothing measurable → 0.
		if usage != nil && out.Result != nil {
			usage.tokens = tokenUsageFromResult(out.Result, "input") + tokenUsageFromResult(out.Result, "output")
		}
		if !out.Done {
			// Yield (Execution Quantum): the quantum made progress but the
			// task is not complete. RunQuantum's not-done branch SUSPENDEDs the
			// task with this checkpoint preserved; the next drain re-acquires
			// it and the next quantum resumes from this PCB.
			return taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
				UserProfile:      meta.UserProfile,
				Payload:          meta.Payload,
				UsedExperienceID: meta.UsedExperienceID,
				StrategyID:       meta.StrategyID,
				SessionID:        meta.SessionID,
				StepCheckpoint:   out.Checkpoint,
				InputTokens:      meta.InputTokens + tokenUsageFromResult(out.Result, "input"),
				OutputTokens:     meta.OutputTokens + tokenUsageFromResult(out.Result, "output"),
			}), false, nil
		}
		// Done: carry the worker's real output back through the fabric so the
		// dispatch layer can surface actual items instead of an "ok"
		// placeholder. The result rides in the quantum checkpoint:
		// RunQuantum's done branch stores it via CompleteWithCheckpoint, and
		// the dispatcher reads it back after polling the task to COMPLETED.
		outMap := map[string]any{"result": "ok"}
		if res := out.Result; res != nil {
			if items := res.Items; len(items) > 0 {
				outMap["items"] = items
			}
			if res.Reason != "" {
				outMap["reason"] = res.Reason
			}
			if len(res.Metadata) > 0 {
				outMap["metadata"] = res.Metadata
			}
		}
		// Re-wrap the step output in the metadata envelope so the dispatcher's
		// outcomeFromFabric unwraps it on COMPLETED (same as pre-quantum).
		// Token usage accumulates across quanta (the session total, not the
		// last quantum's) — the terminal task.completed event carries it for
		// the RuntimeObserver's cost channel. SessionID rides like the yield
		// path: CompleteWithCheckpoint stores this envelope BEFORE recording
		// the terminal event, so dropping it here would strip session_id
		// from every task.completed payload (recordLocked reads the session
		// scope from the checkpoint it is about to persist).
		return taskfabric.EncodeCheckpoint(taskfabric.DecodedCheckpoint{
			UserProfile:      meta.UserProfile,
			Payload:          meta.Payload,
			UsedExperienceID: meta.UsedExperienceID,
			StrategyID:       meta.StrategyID,
			SessionID:        meta.SessionID,
			StepCheckpoint:   outMap,
			InputTokens:      meta.InputTokens + tokenUsageFromResult(out.Result, "input"),
			OutputTokens:     meta.OutputTokens + tokenUsageFromResult(out.Result, "output"),
		}), true, nil
	}
}

// tokenUsageFromResult reads one side of the quantum's LLM token usage from
// the StepOutcome result metadata (the planner cognition stamps
// input_tokens/output_tokens there — the cross-package key contract).
// side is "input" or "output". A nil result, a missing key, or a non-number
// value yields 0: an unreported quantum contributes nothing to the session
// total, never an error.
func tokenUsageFromResult(res *models.TaskResult, side string) int {
	if res == nil || len(res.Metadata) == 0 {
		return 0
	}
	v, ok := res.Metadata[side+"_tokens"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

// quantumRetries derives the number of retries THIS quantum consumed from the
// RunQuantum error. RunQuantum calls fabric.Fail at most ONCE per quantum —
// only on a step error — and Fail is the sole incrementer of
// RetryPolicy.Attempts (a monotonically increasing LIFETIME counter that never
// resets). Therefore:
//
//   - 1 when the step failed (Fail ran, the task consumed one retry);
//   - 0 otherwise (completed/yielded, or the quantum never started).
//
// Start-stage sentinels (fencing ErrNotOwner/ErrEpochMismatch, ErrIllegalState,
// ErrTaskNotFound) are NOT step failures — Fail never ran — so they contribute
// 0. Executor step errors cannot equal taskfabric's sentinels, so errors.Is
// cleanly separates the two.
//
// This derivation is used instead of reading RetryPolicy.Attempts (cumulative,
// over-attributes later quanta and inflates the deterministic scorer's retry
// component with retry depth) or re-reading the task after RunQuantum (races
// another drain that may have re-acquired and re-failed our requeued task,
// attributing ITS retry to US).
func quantumRetries(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, taskfabric.ErrNotOwner) ||
		errors.Is(err, taskfabric.ErrEpochMismatch) ||
		errors.Is(err, taskfabric.ErrIllegalState) ||
		errors.Is(err, taskfabric.ErrTaskNotFound) {
		return 0
	}
	return 1
}

// endQuantumOutcome releases the winner's busy slot and attributes the
// quantum outcome to the evolution feedback loop.
//
// A benign fencing rejection (cooperative preemption handed the task back
// while the stale holder was still mid-step) is NOT the executor's failure:
// recording it as one would poison the agent's success rate toward 0, and
// Score's confidence factor would make the preempted task permanently
// unschedulable. Such rejections end NEUTRAL — load is released but no
// success/failure enters the history, and attribution is skipped.
func (s *Scheduler) endQuantumOutcome(winner, capability, taskID string, err error, latency time.Duration, retries int) {
	if errors.Is(err, taskfabric.ErrNotOwner) || errors.Is(err, taskfabric.ErrEpochMismatch) {
		s.tracker.EndNeutral(winner)
		log.Debug("kernel scheduler: quantum ended by preemption fencing (benign); outcome not attributed", "task_id", taskID)
		return
	}
	// Fabric start-stage sentinels and scheduler-shutdown cancellation are
	// not the executor's failure either. ErrIllegalState/ErrTaskNotFound mean
	// the task was concurrently finalized or removed between acquire and the
	// quantum — the executor never got to run. context.Canceled means the
	// scheduler is shutting down mid-quantum: attributing graceful-shutdown
	// aborts as failures poisoned every in-flight agent's confidence on every
	// restart. All end neutral (load released, no history recorded).
	if errors.Is(err, taskfabric.ErrIllegalState) || errors.Is(err, taskfabric.ErrTaskNotFound) ||
		errors.Is(err, context.Canceled) {
		s.tracker.EndNeutral(winner)
		log.Debug("kernel scheduler: quantum ended by a non-executor condition; outcome not attributed",
			"task_id", taskID, "error", err)
		return
	}
	s.tracker.End(winner, err == nil)
	// Evolution feedback: record the outcome for the feedback loop. The
	// attribution is read by the EvolutionFeedbackAdapter and pushed back into
	// the tracker's confidence override (SetAgentConfidence) so the next
	// Schedule sees the evolution-derived confidence.
	//
	// RecordWithMetrics carries the real quantum latency and retry
	// budget so the deterministic scorer has non-degenerate evidence.
	// Recovery count stays 0 (normal quantum: no replacement was needed).
	if s.attribution != nil {
		s.attribution.RecordWithMetrics(winner, capability, err == nil, latency, retries, 0)
	}
}

// toModelTask maps a fabric Task back to the models.Task shape the sub-agent
// executor expects. The submission-time metadata (UserProfile + Payload +
// UsedExperienceID) rides in the fabric Checkpoint slot inside a
// *taskfabric.CheckpointEnvelope; restoring it here is what lets
// the executor take the real LLM path instead of degrading to an empty
// fallback result (profile==nil → executeByType). A genuine progress
// checkpoint (plain map, written by RunQuantum) is preserved in the payload so
// a resumed quantum can observe where the previous step left off. Decode goes
// through the single shared protocol (taskfabric.DecodeCheckpoint) — the same
// path recovery and every other consumer use.
func (s *Scheduler) ToModelTask(tk *taskfabric.Task) *models.Task {
	t := models.NewTask(tk.ID, models.AgentType(tk.Capability), nil)
	if tk.Checkpoint == nil {
		return t
	}
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		log.Warn("kernel scheduler: toModelTask decode checkpoint failed", "task_id", tk.ID, "error", err)
		t.Payload = map[string]any{"checkpoint": tk.Checkpoint}
		return t
	}
	t.UserProfile = reifyUserProfile(dc.UserProfile)
	// Payload must be a COPY, not an alias of the envelope's map: the
	// executor (and the "checkpoint" key stamped below) writes into
	// t.Payload, and an alias would persist those quantum-scoped keys into
	// the durable checkpoint envelope on the next yield/done re-wrap —
	// permanently polluting the persisted payload (audit HIGH #1).
	t.Payload = copyPayloadMap(dc.Payload)
	t.UsedExperienceID = dc.UsedExperienceID
	// The submission-time strategy attribution rides to the executor so
	// the sub-agent's task.completed/failed events carry the same key the
	// fabric's own events do — RuntimeObserver attributes fitness samples by
	// it, and a promote mid-task must not re-credit the new strategy.
	t.StrategyID = dc.StrategyID
	// SessionID rides to the executor so the plannerCognition can look
	// up the per-session L2 graph registry.
	t.SessionID = dc.SessionID
	// TenantID rides to the executor (and, via the ctx stamp below, to every
	// tool call and knowledge query the quantum makes) so recall resolves the
	// same tenant the distillation write side attributes facts to.
	t.TenantID = dc.TenantID
	// A resumed quantum observes where the previous step left off:
	// the step checkpoint is surfaced to the executor as payload["checkpoint"].
	if dc.StepCheckpoint != nil {
		if t.Payload == nil {
			t.Payload = make(map[string]any)
		}
		t.Payload["checkpoint"] = dc.StepCheckpoint
	}
	return t
}

// copyPayloadMap returns a shallow copy of the payload map (nil stays nil).
// A shallow copy is sufficient: the values themselves are treated as
// immutable by the executor contract; only the map's key set must be
// protected from quantum-scoped writes ("checkpoint", executing-agent
// stamps) leaking into the shared envelope.
func copyPayloadMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src)+1)
	for k, v := range src {
		out[k] = v
	}
	return out
}

// reifyUserProfile converts a decoded envelope UserProfile (typed pointer, or
// a raw map after a JSON round-trip) back into the *models.UserProfile the
// executor expects. A value that cannot be reified yields nil (the executor
// then degrades exactly as before).
func reifyUserProfile(v any) *models.UserProfile {
	switch up := v.(type) {
	case *models.UserProfile:
		return up
	case nil:
		return nil
	default:
		if buf, err := json.Marshal(up); err == nil {
			var p models.UserProfile
			if err := json.Unmarshal(buf, &p); err == nil {
				return &p
			}
		}
		return nil
	}
}
