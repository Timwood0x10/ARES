package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

const runnerCheckpointSchemaVersion = 3

// stateSnapshotKey returns the storage key for a runtime state snapshot of an
// execution. It is namespaced apart from the authoritative checkpoint key so a
// versioned state snapshot can be recovered independently.
func stateSnapshotKey(executionID string) string {
	return "state-snapshot/" + executionID
}

// CheckpointSnapshot is the atomic recovery protocol for unified workflow execution.
type CheckpointSnapshot struct {
	SchemaVersion         int                `json:"schema_version"`
	ExecutionID           string             `json:"execution_id"`
	SpecID                string             `json:"spec_id"`
	BaseSpecHash          string             `json:"base_spec_hash"`
	SpecHash              string             `json:"spec_hash"`
	EffectiveSpec         *WorkflowSpec      `json:"effective_spec"`
	State                 map[string]any     `json:"state"`
	NodeStates            []NodeStatusValue  `json:"node_states"`
	Scheduler             SchedulerSnapshot  `json:"scheduler"`
	LoopIteration         int                `json:"loop_iteration"`
	LoopIterationComplete bool               `json:"loop_iteration_complete"`
	LoopHistory           []LoopIteration    `json:"loop_history,omitempty"`
	PendingInterrupts     []PendingInterrupt `json:"pending_interrupts,omitempty"`
	MutationIDs           []string           `json:"mutation_ids,omitempty"`
	PendingMutations      []Mutation         `json:"pending_mutations,omitempty"`
	EventSequence         uint64             `json:"event_sequence"`
	SavedAt               time.Time          `json:"saved_at"`
	// CollectorData carries the execution collector snapshot (route/tool/memory
	// history) so the resume path can restore observability and evolution data.
	// Saved/restored via ExecutionCollector.Export() / Import().
	CollectorData map[string]any `json:"collector_data,omitempty"`
}

// ResumeExecution resumes a workflow from an atomic scheduler checkpoint.
func (r *Runner) ResumeExecution(ctx context.Context, spec *WorkflowSpec, executionID string) (*Result, error) {
	if r.checkpointStore == nil {
		return nil, fmt.Errorf("resume execution requires a checkpoint store")
	}
	if err := validateExecutionInput(r.executor, spec); err != nil {
		return nil, err
	}
	snapshot, err := r.loadCheckpoint(ctx, executionID)
	if err != nil {
		return nil, err
	}
	if snapshot.SpecID != spec.ID {
		return nil, fmt.Errorf("checkpoint spec ID %q does not match provided spec %q", snapshot.SpecID, spec.ID)
	}
	baseHash, err := workflowSpecHash(spec)
	if err != nil {
		return nil, err
	}
	if snapshot.BaseSpecHash != baseHash {
		return nil, fmt.Errorf("checkpoint base spec hash %q does not match provided spec hash %q", snapshot.BaseSpecHash, baseHash)
	}
	if snapshot.EffectiveSpec == nil {
		return nil, fmt.Errorf("checkpoint effective spec must not be nil")
	}
	effectiveHash, err := workflowSpecHash(snapshot.EffectiveSpec)
	if err != nil {
		return nil, err
	}
	if snapshot.SpecHash != effectiveHash {
		return nil, fmt.Errorf("checkpoint effective spec hash %q does not match stored spec hash %q", effectiveHash, snapshot.SpecHash)
	}
	scope := NewExecutionScope(snapshot.ExecutionID, snapshot.EffectiveSpec)
	scope.baseSpec = spec
	scope.InitNodeStates()
	// Reuse the caller's collector (if set) instead of the default empty one.
	if r.collector != nil {
		scope.SetCollector(r.collector)
	}
	scope.RestoreState(snapshot.State)
	scope.RestoreNodeStates(snapshot.NodeStates)
	scope.RestoreLoopHistory(snapshot.LoopHistory)
	scope.RestorePendingInterrupts(snapshot.PendingInterrupts)
	scope.RestoreMutationIDs(snapshot.MutationIDs)
	scope.RestoreEventSequence(snapshot.EventSequence)
	// Restore collector data from the checkpoint — this ensures route/tool/memory
	// history recorded before the crash is available for evolution scoring and
	// observability after resume.
	if len(snapshot.CollectorData) > 0 {
		scope.Collector().Import(snapshot.CollectorData)
	}
	if len(snapshot.PendingMutations) > 0 {
		if r.patchQueue == nil {
			return nil, fmt.Errorf("checkpoint has pending mutations but Runner has no patch queue")
		}
		if err := r.patchQueue.Restore(scope.ExecutionID, snapshot.PendingMutations); err != nil {
			return nil, fmt.Errorf("restore pending mutations: %w", err)
		}
	}
	if err := r.publishEvent(ctx, scope, RunnerEvent{
		Type:   RunnerEventWorkflowResumed,
		Status: overallNodeStatus(scope.NodeStates()),
	}); err != nil {
		return scope.ToResult(), fmt.Errorf("publish workflow resumed: %w", err)
	}

	effectiveSpec := scope.Spec
	iterationSpec := effectiveSpec
	if snapshot.LoopIteration > 1 && effectiveSpec.Loop != nil {
		iterationSpec = buildLoopBodySpec(effectiveSpec, effectiveSpec.Loop.LoopNodes)
	}
	scheduler, err := NewScheduler(iterationSpec, r.strategy)
	if err != nil {
		return nil, fmt.Errorf("create resume scheduler: %w", err)
	}
	if err := scheduler.Restore(snapshot.Scheduler); err != nil {
		return nil, fmt.Errorf("restore scheduler: %w", err)
	}
	if scheduler.HasReady() {
		if err := r.executeIteration(ctx, scope, iterationSpec, scheduler, snapshot.LoopIteration); err != nil {
			return r.finishResumedExecution(ctx, scope, err)
		}
	}
	// Finalise any nodes that were never scheduled (e.g., resume found an
	// empty ready queue). Without this, nodes still in NodeStatusPending
	// would be recorded as part of a "completed" iteration and silently
	// dropped instead of receiving a terminal status (M5).
	r.finaliseUnprocessed(scope, scheduler, iterationSpec)
	completedIteration := snapshot.LoopIteration
	if completedIteration > 0 && !snapshot.LoopIterationComplete {
		scope.RecordLoopIteration(completedIteration, nodeIDs(iterationSpec))
		if err := r.saveCheckpoint(ctx, scope, scheduler, completedIteration); err != nil {
			return nil, fmt.Errorf("save completed resumed iteration %d: %w", completedIteration, err)
		}
	}
	if err := r.resumeRemainingLoops(ctx, scope, effectiveSpec, completedIteration); err != nil {
		return r.finishResumedExecution(ctx, scope, err)
	}
	return r.finishResumedExecution(ctx, scope, nil)
}

func (r *Runner) finishResumedExecution(ctx context.Context, scope *ExecutionScope, execErr error) (*Result, error) {
	scope.MarkFinished()
	result := scope.ToResult()
	if execErr != nil || result.Status == NodeStatusFailed {
		if execErr == nil {
			execErr = scope.Err()
		}
		if execErr == nil {
			execErr = fmt.Errorf("workflow %q failed after resume", scope.Spec.ID)
		}
		r.emitWorkflowFinished(ctx, scope, execErr)
		if publishErr := r.publishEvent(ctx, scope, RunnerEvent{
			Type: RunnerEventWorkflowFailed, Status: NodeStatusFailed, Error: execErr.Error(),
		}); publishErr != nil {
			return result, errors.Join(execErr, fmt.Errorf("publish resumed workflow failed: %w", publishErr))
		}
		if r.failOnNodeError || result.Status != NodeStatusFailed {
			return result, execErr
		}
		return result, nil
	}
	r.emitWorkflowFinished(ctx, scope, nil)
	if err := r.publishEvent(ctx, scope, RunnerEvent{
		Type: RunnerEventWorkflowCompleted, Status: result.Status,
	}); err != nil {
		return result, fmt.Errorf("publish resumed workflow completed: %w", err)
	}
	return result, nil
}

func (r *Runner) resumeRemainingLoops(ctx context.Context, scope *ExecutionScope, spec *WorkflowSpec, completedIteration int) error {
	if spec.Loop == nil || completedIteration <= 0 || completedIteration >= spec.Loop.MaxIterations {
		return nil
	}
	if r.untilCondition != nil && r.untilCondition(scope.StateSnapshot(), completedIteration) {
		return nil
	}
	body := buildLoopBodySpec(spec, spec.Loop.LoopNodes)
	for iteration := completedIteration + 1; iteration <= spec.Loop.MaxIterations; iteration++ {
		scope.ResetNodesForIteration(spec.Loop.LoopNodes)
		scheduler, err := NewScheduler(body, r.strategy)
		if err != nil {
			return fmt.Errorf("create scheduler for resumed iteration %d: %w", iteration, err)
		}
		if err := r.executeIteration(ctx, scope, body, scheduler, iteration); err != nil {
			return fmt.Errorf("execute resumed iteration %d: %w", iteration, err)
		}
		scope.RecordLoopIteration(iteration, nodeIDs(body))
		if err := r.saveCheckpoint(ctx, scope, scheduler, iteration); err != nil {
			return fmt.Errorf("save resumed iteration %d checkpoint: %w", iteration, err)
		}
		if r.untilCondition != nil && r.untilCondition(scope.StateSnapshot(), iteration) {
			break
		}
	}
	return nil
}

func (r *Runner) saveCheckpoint(ctx context.Context, scope *ExecutionScope, scheduler *Scheduler, loopIteration int) error {
	return r.saveCheckpointSnapshot(ctx, scope, scheduler.Snapshot(), loopIteration)
}

func (r *Runner) saveCheckpointSnapshot(
	ctx context.Context,
	scope *ExecutionScope,
	scheduler SchedulerSnapshot,
	loopIteration int,
) error {
	if r.checkpointStore == nil {
		return nil
	}
	event := prepareRunnerEvent(scope, RunnerEvent{
		Type:     RunnerEventCheckpointSaved,
		Status:   overallNodeStatus(scope.NodeStates()),
		Metadata: map[string]any{"loop_iteration": loopIteration},
	})
	if r.eventSink == nil {
		snapshot, err := r.checkpointSnapshot(scope, scheduler, loopIteration, scope.EventSequence())
		if err != nil {
			return err
		}
		return r.persistCheckpoint(ctx, snapshot)
	}
	return scope.PersistOrderedEvent(
		func(sequence uint64) error {
			snapshot, err := r.checkpointSnapshot(scope, scheduler, loopIteration, sequence)
			if err != nil {
				return err
			}
			return r.persistCheckpoint(ctx, snapshot)
		},
		func(sequence uint64) error {
			return r.publishSequencedEvent(ctx, event, sequence)
		},
	)
}

func (r *Runner) checkpointSnapshot(
	scope *ExecutionScope,
	scheduler SchedulerSnapshot,
	loopIteration int,
	eventSequence uint64,
) (CheckpointSnapshot, error) {
	specHash, err := workflowSpecHash(scope.Spec)
	if err != nil {
		return CheckpointSnapshot{}, err
	}
	baseSpecHash, err := workflowSpecHash(scope.baseSpec)
	if err != nil {
		return CheckpointSnapshot{}, err
	}
	effectiveSpec, err := cloneWorkflowSpec(scope.Spec)
	if err != nil {
		return CheckpointSnapshot{}, err
	}
	return CheckpointSnapshot{
		SchemaVersion:         runnerCheckpointSchemaVersion,
		ExecutionID:           scope.ExecutionID,
		SpecID:                scope.Spec.ID,
		BaseSpecHash:          baseSpecHash,
		SpecHash:              specHash,
		EffectiveSpec:         effectiveSpec,
		State:                 scope.StateSnapshot(),
		NodeStates:            copyNodeStates(scope.NodeStates()),
		Scheduler:             scheduler,
		LoopIteration:         loopIteration,
		LoopIterationComplete: loopIteration > 0 && loopIteration <= len(scope.LoopHistory()),
		LoopHistory:           scope.LoopHistory(),
		PendingInterrupts:     scope.PendingInterrupts(),
		MutationIDs:           scope.MutationIDs(),
		PendingMutations:      r.pendingMutations(scope.ExecutionID),
		EventSequence:         eventSequence,
		SavedAt:               time.Now(),
		CollectorData:         exportCollectorData(scope),
	}, nil
}

func (r *Runner) commitMutationCheckpoint(
	ctx context.Context,
	scope *ExecutionScope,
	candidate *WorkflowSpec,
	scheduler *Scheduler,
	loopIteration int,
	ids []string,
) error {
	if r.checkpointStore == nil {
		return fmt.Errorf("runtime mutations require a checkpoint store")
	}
	previousSpec := scope.Spec
	scope.Spec = candidate
	for _, id := range ids {
		scope.RecordMutationID(id)
	}
	pending := r.patchQueue.Pending(scope.ExecutionID)
	remaining := cloneMutations(pending[len(ids):])
	events := r.mutationCommitEvents(scope, loopIteration, ids)
	if r.eventSink == nil {
		snapshot, err := r.checkpointSnapshot(scope, scheduler.Snapshot(), loopIteration, scope.EventSequence())
		if err != nil {
			scope.Spec = previousSpec
			scope.RemoveMutationIDs(len(ids))
			return fmt.Errorf("build mutation checkpoint: %w", err)
		}
		snapshot.PendingMutations = remaining
		if err := r.persistCheckpoint(ctx, snapshot); err != nil {
			scope.Spec = previousSpec
			scope.RemoveMutationIDs(len(ids))
			return fmt.Errorf("persist mutation commit: %w", err)
		}
		if err := r.patchQueue.Acknowledge(scope.ExecutionID, ids); err != nil {
			return fmt.Errorf("acknowledge durable mutations: %w", err)
		}
		return nil
	}
	persisted := false
	publishIndex := 0
	err := scope.PersistOrderedEvents(
		uint64(len(events)),
		func(_, last uint64) error {
			snapshot, snapshotErr := r.checkpointSnapshot(scope, scheduler.Snapshot(), loopIteration, last)
			if snapshotErr != nil {
				return snapshotErr
			}
			snapshot.PendingMutations = remaining
			if persistErr := r.persistCheckpoint(ctx, snapshot); persistErr != nil {
				return persistErr
			}
			persisted = true
			return nil
		},
		func(sequence uint64) error {
			event := events[publishIndex]
			publishIndex++
			return r.publishSequencedEvent(ctx, event, sequence)
		},
	)
	if !persisted {
		scope.Spec = previousSpec
		scope.RemoveMutationIDs(len(ids))
		return fmt.Errorf("persist mutation commit: %w", err)
	}
	if ackErr := r.patchQueue.Acknowledge(scope.ExecutionID, ids); ackErr != nil {
		return fmt.Errorf("acknowledge durable mutations: %w", ackErr)
	}
	if err != nil {
		return fmt.Errorf("publish durable mutation commit: %w", err)
	}
	return nil
}

func (r *Runner) mutationCommitEvents(scope *ExecutionScope, loopIteration int, ids []string) []RunnerEvent {
	events := make([]RunnerEvent, 0, len(ids)+1)
	events = append(events, prepareRunnerEvent(scope, RunnerEvent{
		Type:     RunnerEventCheckpointSaved,
		Metadata: map[string]any{"loop_iteration": loopIteration},
	}))
	for _, id := range ids {
		events = append(events, prepareRunnerEvent(scope, RunnerEvent{
			Type:     RunnerEventMutationApplied,
			Metadata: map[string]any{"mutation_id": id},
		}))
	}
	return events
}

func (r *Runner) pendingMutations(executionID string) []Mutation {
	if r.patchQueue == nil {
		return nil
	}
	return r.patchQueue.Pending(executionID)
}

func (r *Runner) persistCheckpoint(ctx context.Context, snapshot CheckpointSnapshot) error {
	if snapshot.SpecHash == "" {
		return fmt.Errorf("hash workflow %q for checkpoint", snapshot.SpecID)
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("marshal checkpoint for execution %q: %w", snapshot.ExecutionID, err)
	}
	key := ares_runtime.CheckpointKey(snapshot.ExecutionID)
	if err := r.checkpointStore.Save(ctx, key, payload); err != nil {
		return fmt.Errorf("save checkpoint %q: %w", key, err)
	}
	// Runtime state snapshot (primitive 5): persist the execution state under a
	// versioned snapshot key in the SAME store. A failure here is non-fatal —
	// the authoritative checkpoint above already carries State — but a valid
	// state snapshot gives recovery an independently version-checked source.
	if err := ares_runtime.SaveStateSnapshot(ctx, r.checkpointStore, stateSnapshotKey(snapshot.ExecutionID), snapshot.State); err != nil {
		slog.WarnContext(ctx, "workflow state snapshot save failed; checkpoint still persisted",
			"execution_id", snapshot.ExecutionID, "error", err)
	}
	return nil
}

func (r *Runner) loadCheckpoint(ctx context.Context, executionID string) (*CheckpointSnapshot, error) {
	key := ares_runtime.CheckpointKey(executionID)
	payload, err := r.checkpointStore.Load(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("load checkpoint %q: %w", key, err)
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("checkpoint %q not found", key)
	}
	var snapshot CheckpointSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint %q: %w", key, err)
	}
	if snapshot.SchemaVersion != runnerCheckpointSchemaVersion {
		return nil, fmt.Errorf("checkpoint schema version %d is unsupported, want %d", snapshot.SchemaVersion, runnerCheckpointSchemaVersion)
	}
	if snapshot.ExecutionID != executionID {
		return nil, fmt.Errorf("checkpoint execution ID %q does not match requested ID %q", snapshot.ExecutionID, executionID)
	}
	// Runtime state snapshot (primitive 5): cross-check the versioned state
	// snapshot when one exists. A missing snapshot (ErrStateSnapshotNotFound)
	// is tolerated — older checkpoints predate the snapshot sidecar — but an
	// unsupported schema version is surfaced so recovery never restores state
	// written by an incompatible schema.
	if stateSnap, stateErr := ares_runtime.LoadStateSnapshot(ctx, r.checkpointStore, stateSnapshotKey(executionID)); stateErr != nil {
		if !errors.Is(stateErr, ares_runtime.ErrStateSnapshotNotFound) {
			return nil, fmt.Errorf("load state snapshot %q: %w", executionID, stateErr)
		}
	} else if stateSnap != nil && stateSnap.State != nil {
		// The state snapshot is the version-checked authority for State; prefer
		// it over the checkpoint's embedded State when both exist.
		snapshot.State = stateSnap.State
	}
	return &snapshot, nil
}

func workflowSpecHash(spec *WorkflowSpec) (string, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return "", fmt.Errorf("marshal workflow %q for checkpoint hash: %w", spec.ID, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// exportCollectorData extracts the execution-scoped collector data for checkpoint
// persistence. Returns nil when no data is available or the scope has no collector.
func exportCollectorData(scope *ExecutionScope) map[string]any {
	if scope == nil {
		return nil
	}
	col := scope.Collector()
	if col == nil {
		return nil
	}
	return col.Export()
}

func copyNodeStates(states []*NodeStatusValue) []NodeStatusValue {
	result := make([]NodeStatusValue, 0, len(states))
	for _, state := range states {
		copyValue := *state
		copyValue.Output = cloneAnyMap(state.Output)
		result = append(result, copyValue)
	}
	return result
}
