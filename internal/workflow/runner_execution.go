package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"golang.org/x/sync/errgroup"
)

const (
	interruptActionApprove  = "approve"
	interruptActionFallback = "fallback"
	routeMetadataKey        = "route"
)

var errInterruptApproved = errors.New("interrupt approved")

type nodeResult struct {
	id     NodeID
	output map[string]any
	err    error
}

func (r *Runner) executeWorkflow(ctx context.Context, scope *ExecutionScope, spec *WorkflowSpec, startIteration int) error {
	loop := spec.Loop
	if loop == nil || loop.MaxIterations <= 0 {
		return r.executeIteration(ctx, scope, spec, nil, 0)
	}
	body := buildLoopBodySpec(spec, loop.LoopNodes)
	for iteration := startIteration + 1; iteration <= loop.MaxIterations; iteration++ {
		iterationSpec := spec
		if iteration > 1 && body != nil {
			iterationSpec = body
			scope.ResetNodesForIteration(loop.LoopNodes)
		}
		scheduler, err := NewScheduler(iterationSpec, r.strategy)
		if err != nil {
			return fmt.Errorf("create scheduler for iteration %d: %w", iteration, err)
		}
		if err := r.executeIteration(ctx, scope, iterationSpec, scheduler, iteration); err != nil {
			return fmt.Errorf("execute iteration %d: %w", iteration, err)
		}
		scope.RecordLoopIteration(iteration, nodeIDs(iterationSpec))
		if err := r.saveCheckpoint(ctx, scope, scheduler, iteration); err != nil {
			return fmt.Errorf("save iteration %d checkpoint: %w", iteration, err)
		}
		if r.untilCondition != nil && r.untilCondition(scope.StateSnapshot(), iteration) {
			break
		}
	}
	return nil
}

func (r *Runner) executeIteration(ctx context.Context, scope *ExecutionScope, spec *WorkflowSpec, scheduler *Scheduler, loopIteration int) error {
	if scheduler == nil {
		var err error
		scheduler, err = NewScheduler(spec, r.strategy)
		if err != nil {
			return fmt.Errorf("create scheduler: %w", err)
		}
	}
	scheduler.SetCondEval(func(expr *ConditionExpr) bool {
		return r.evaluateCondition(expr, scope)
	})
	effectiveSpec := spec
	maxParallel := effectiveSpec.Schedule.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}
	for {
		mutatedScheduler, mutated, err := r.applyQueuedMutations(ctx, scope, scheduler, loopIteration)
		if err != nil {
			return err
		}
		if mutated {
			scheduler = mutatedScheduler
			effectiveSpec = scope.Spec
			maxParallel = effectiveSpec.Schedule.MaxParallel
			if maxParallel <= 0 {
				maxParallel = 1
			}
		}
		if !scheduler.HasReady() {
			break
		}
		preBatch := scheduler.Snapshot()
		batch, err := r.takeReadyBatch(scheduler, maxParallel)
		if err != nil {
			return err
		}
		hasInterrupt, err := r.prepareBatchInterrupts(ctx, scope, effectiveSpec, batch)
		if err != nil {
			return err
		}
		if hasInterrupt {
			if err := r.saveCheckpointSnapshot(ctx, scope, preBatch, loopIteration); err != nil {
				return fmt.Errorf("save pending interrupt checkpoint: %w", err)
			}
		}
		results, err := r.executeBatch(ctx, scope, effectiveSpec, batch)
		if err != nil {
			return err
		}
		for _, result := range results {
			if err := r.commitResult(ctx, scope, scheduler, effectiveSpec, result); err != nil {
				if checkpointErr := r.saveCheckpoint(ctx, scope, scheduler, loopIteration); checkpointErr != nil {
					return fmt.Errorf("commit result: %v; save failure checkpoint: %w", err, checkpointErr)
				}
				return err
			}
		}
		if err := r.saveCheckpoint(ctx, scope, scheduler, loopIteration); err != nil {
			return err
		}
	}
	r.finaliseUnprocessed(scope, scheduler, effectiveSpec)
	if err := r.saveCheckpoint(ctx, scope, scheduler, loopIteration); err != nil {
		return err
	}
	return nil
}

func (r *Runner) takeReadyBatch(scheduler *Scheduler, limit int) ([]NodeID, error) {
	batch := make([]NodeID, 0, limit)
	for len(batch) < limit && scheduler.HasReady() {
		id, err := scheduler.NextWithSelector(r.readySelector)
		if err != nil {
			return nil, fmt.Errorf("select ready node: %w", err)
		}
		if id == "" {
			break
		}
		batch = append(batch, id)
	}
	return batch, nil
}

func (r *Runner) prepareBatchInterrupts(
	ctx context.Context,
	scope *ExecutionScope,
	spec *WorkflowSpec,
	ids []NodeID,
) (bool, error) {
	prepared := false
	for _, id := range ids {
		node, err := findNode(spec, id)
		if err != nil {
			return false, err
		}
		if node.Interrupt == nil || r.interruptHandler == nil {
			continue
		}
		if _, exists := scope.PendingInterrupt(id); exists {
			prepared = true
			continue
		}
		interrupt := PendingInterrupt{
			Token:     interruptToken(scope.ExecutionID, id),
			NodeID:    id,
			Message:   node.Interrupt.Message,
			CreatedAt: time.Now(),
		}
		scope.SetNodeStatus(id, NodeStatusInterrupted)
		scope.SetPendingInterrupt(interrupt)
		if err := r.publishEvent(ctx, scope, RunnerEvent{
			Type:   RunnerEventInterruptPending,
			NodeID: id,
			Status: NodeStatusInterrupted,
			Metadata: map[string]any{
				"message": node.Interrupt.Message,
				"token":   interrupt.Token,
			},
		}); err != nil {
			return false, fmt.Errorf("publish interrupt pending: %w", err)
		}
		prepared = true
	}
	return prepared, nil
}

func (r *Runner) executeBatch(ctx context.Context, scope *ExecutionScope, spec *WorkflowSpec, ids []NodeID) ([]nodeResult, error) {
	results := make([]nodeResult, len(ids))
	group, groupCtx := errgroup.WithContext(ctx)
	for index, id := range ids {
		index := index
		id := id
		group.Go(func() error {
			node, err := findNode(spec, id)
			if err != nil {
				results[index] = nodeResult{id: id, err: err}
				return nil
			}
			output, execErr := r.executeNode(groupCtx, node, scope, spec.Schedule.MaxParallel)
			results[index] = nodeResult{id: id, output: output, err: execErr}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, fmt.Errorf("execute ready batch: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

var errNodeNotSelected = errors.New("node condition not selected")

func (r *Runner) executeNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope, maxParallel int) (map[string]any, error) {
	if spec.Interrupt != nil && r.interruptHandler != nil {
		if err := r.handleInterrupt(ctx, spec, scope); err != nil && !errors.Is(err, errInterruptApproved) {
			return nil, err
		}
	}
	scope.SetNodeStatus(spec.ID, NodeStatusRunning)
	if err := r.publishEvent(ctx, scope, RunnerEvent{
		Type: RunnerEventNodeStarted, NodeID: spec.ID, Status: NodeStatusRunning,
	}); err != nil {
		return nil, fmt.Errorf("publish node started: %w", err)
	}
	startedAt := time.Now()
	r.emitBeforeStep(ctx, scope.ExecutionID, spec)
	var output map[string]any
	var err error
	if spec.SubWorkflow != nil {
		output, err = r.executeChildScope(ctx, spec.SubWorkflow, scope, maxParallel)
	} else {
		output, err = r.executeSingle(ctx, spec, scope)
	}
	r.emitAfterStep(ctx, scope.ExecutionID, spec, output, err, startedAt)
	if err != nil {
		scope.Collector().RecordError(string(spec.ID), err.Error())
	}
	return output, err
}

func (r *Runner) executeSingle(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error) {
	maxAttempts := 1
	if spec.Retry != nil && spec.Retry.MaxAttempts > 0 {
		maxAttempts = spec.Retry.MaxAttempts
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		output, err := r.executeAttempt(ctx, spec, scope)
		if err == nil {
			return output, nil
		}
		lastErr = err
		scope.RecordAttempt(spec.ID)
		if attempt < maxAttempts {
			if err := waitRetry(ctx, retryBackoff(spec.Retry, attempt)); err != nil {
				return nil, err
			}
		}
	}
	return r.recoverNode(ctx, spec, scope, lastErr)
}

func (r *Runner) executeAttempt(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error) {
	execCtx := ctx
	if spec.Timeout <= 0 {
		return r.executor.ExecuteNode(execCtx, spec, scope)
	}
	execCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	return r.executor.ExecuteNode(execCtx, spec, scope)
}

func (r *Runner) recoverNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope, nodeErr error) (map[string]any, error) {
	if spec.Recovery != nil && spec.Recovery.Strategy == "replace_node" && spec.Recovery.ReplacementAgent != "" {
		replacement := *spec
		replacement.AgentType = spec.Recovery.ReplacementAgent
		output, err := r.executor.ExecuteNode(ctx, &replacement, scope)
		if err == nil {
			return output, nil
		}
		nodeErr = fmt.Errorf("replacement agent %q: %w", replacement.AgentType, err)
	}
	if r.recoveryHandler == nil || (spec.Recovery != nil && spec.Recovery.Strategy == "fail_fast") {
		return nil, nodeErr
	}
	recovered, replacement, err := r.recoveryHandler(ctx, spec.ID, nodeErr, spec)
	if err != nil {
		return nil, fmt.Errorf("recover node %q: %w", spec.ID, err)
	}
	if recovered {
		return replacement, nil
	}
	return nil, nodeErr
}

func (r *Runner) handleInterrupt(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) error {
	interrupt, pending := scope.PendingInterrupt(spec.ID)
	if !pending {
		return fmt.Errorf("interrupt node %q was not prepared at a Runner safe point", spec.ID)
	}
	interruptCtx := ctx
	if spec.Interrupt.TimeoutSec > 0 {
		var cancel context.CancelFunc
		interruptCtx, cancel = context.WithTimeout(ctx, time.Duration(spec.Interrupt.TimeoutSec)*time.Second)
		defer cancel()
	}
	approved, err := r.interruptHandler(interruptCtx, spec.Interrupt, scope.State())
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			switch spec.Interrupt.AutoAction {
			case interruptActionApprove:
				approved = true
			case "skip", interruptActionFallback:
				approved = false
			default:
				return interruptTimeoutError(spec.Interrupt)
			}
		} else {
			return fmt.Errorf("interrupt node %q: %w", spec.ID, err)
		}
	}
	scope.ResolvePendingInterrupt(spec.ID)
	scope.SetNodeStatus(spec.ID, NodeStatusRunning)
	status := "rejected"
	if approved {
		status = "approved"
	}
	scope.Collector().RecordInterrupt(string(spec.ID), status, spec.Interrupt.Message)
	if publishErr := r.publishEvent(ctx, scope, RunnerEvent{
		Type:   RunnerEventInterruptResolved,
		NodeID: spec.ID,
		Metadata: map[string]any{
			"decision": status,
			"token":    interrupt.Token,
		},
	}); publishErr != nil {
		return fmt.Errorf("publish interrupt resolved: %w", publishErr)
	}
	if !approved {
		return fmt.Errorf("node %q rejected by human: %s", spec.ID, spec.Interrupt.Message)
	}
	return errInterruptApproved
}

func interruptTimeoutError(spec *InterruptSpec) error {
	switch spec.AutoAction {
	case interruptActionApprove:
		return errInterruptApproved
	case "skip", interruptActionFallback:
		return fmt.Errorf("interrupt timed out with auto action %q: %s", spec.AutoAction, spec.Message)
	default:
		return fmt.Errorf("interrupt timed out: %s", spec.Message)
	}
}

func (r *Runner) commitResult(ctx context.Context, scope *ExecutionScope, scheduler *Scheduler, spec *WorkflowSpec, result nodeResult) error {
	if errors.Is(result.err, errNodeNotSelected) {
		scope.SetNodeStatus(result.id, NodeStatusNotSelected)
		scheduler.OnNodeNotSelected(result.id)
		return r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventNodeSkipped, NodeID: result.id, Status: NodeStatusNotSelected})
	}
	if result.err != nil {
		scope.SetNodeError(result.id, result.err)
		scheduler.OnNodeFailed(result.id)
		return r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventNodeFailed, NodeID: result.id, Status: NodeStatusFailed, Error: result.err.Error()})
	}
	scope.SetNodeOutput(result.id, result.output)
	scope.CommitState()
	route, err := r.routeTarget(ctx, scope, spec, result)
	if err != nil {
		scope.SetNodeError(result.id, err)
		scheduler.OnNodeFailed(result.id)
		return err
	}
	if route != "" {
		recordRunnerRoute(scope.Collector(), result.id, route)
	}
	scheduler.OnNodeCompletedWithRoute(result.id, route)
	return r.publishEvent(ctx, scope, RunnerEvent{
		Type:   RunnerEventNodeCompleted,
		NodeID: result.id,
		Status: NodeStatusCompleted,
		Output: cloneAnyMap(result.output),
		Metadata: map[string]any{
			routeMetadataKey: route,
		},
	})
}

func recordRunnerRoute(collector *ares_runtime.ExecutionCollector, from, to NodeID) {
	for _, record := range collector.RouteHistory() {
		if record.StepID == string(from) && record.Decision == string(to) {
			return
		}
	}
	collector.RecordRoute(string(from), string(to), "bound router", "runner")
}

func (r *Runner) routeTarget(ctx context.Context, scope *ExecutionScope, spec *WorkflowSpec, result nodeResult) (NodeID, error) {
	router, ok := r.routers[result.id]
	if !ok {
		return "", nil
	}
	payload, err := json.Marshal(result.output)
	if err != nil {
		return "", fmt.Errorf("marshal router output for node %q: %w", result.id, err)
	}
	target := NodeID(router(ctx, string(result.id), scope.StateSnapshot(), string(payload)))
	if target == "" {
		return "", nil
	}
	for _, edge := range spec.Edges {
		if edge.From == result.id && edge.To == target && edge.Kind == EdgeControlFlow {
			return target, nil
		}
	}
	return "", fmt.Errorf("router for node %q selected non-control-flow target %q", result.id, target)
}

func (r *Runner) executeChildScope(ctx context.Context, spec *WorkflowSpec, parent *ExecutionScope, maxParallel int) (map[string]any, error) {
	if report := Validate(spec); !report.Valid() {
		return nil, fmt.Errorf("sub-workflow %q validation failed: %v", spec.ID, report.Errors)
	}
	child := NewExecutionScope("", spec)
	child.InitNodeStates()
	child.RestoreState(parent.StateSnapshot())
	if err := r.executeWorkflow(ctx, child, spec, 0); err != nil {
		return nil, fmt.Errorf("execute sub-workflow %q: %w", spec.ID, err)
	}
	child.MarkFinished()
	// Merge child collector data (route/tool/memory/interrupt/error) back into
	// the parent scope so sub-workflow execution data is not lost.
	parent.Collector().Import(child.Collector().Export())
	return child.StateSnapshot(), nil
}

func (r *Runner) finaliseUnprocessed(scope *ExecutionScope, scheduler *Scheduler, spec *WorkflowSpec) {
	for _, node := range spec.Nodes {
		if scope.IsCompleted(node.ID) {
			continue
		}
		if scheduler.BranchSkipped(node.ID) {
			scope.SetNodeStatus(node.ID, NodeStatusNotSelected)
			continue
		}
		status := NodeStatusUnreachable
		for _, edge := range spec.Edges {
			if edge.To == node.ID && edge.Kind == EdgeDataDependency && scope.NodeStatus(edge.From) == NodeStatusFailed {
				status = NodeStatusBlocked
				break
			}
		}
		scope.SetNodeStatus(node.ID, status)
	}
}

func findNode(spec *WorkflowSpec, id NodeID) (*NodeSpec, error) {
	for i := range spec.Nodes {
		if spec.Nodes[i].ID == id {
			return &spec.Nodes[i], nil
		}
	}
	return nil, fmt.Errorf("node %q not found in workflow %q", id, spec.ID)
}

func nodeIDs(spec *WorkflowSpec) []NodeID {
	ids := make([]NodeID, 0, len(spec.Nodes))
	for _, node := range spec.Nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryBackoff(policy *RetrySpec, attempt int) time.Duration {
	if policy == nil {
		return 0
	}
	initial := policy.InitialDelay
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	maximum := policy.MaxDelay
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	multiplier := policy.BackoffMultiplier
	if multiplier <= 0 {
		multiplier = 2
	}
	delay := time.Duration(float64(initial) * math.Pow(multiplier, float64(attempt-1)))
	if delay > maximum {
		delay = maximum
	}
	return time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5)) //nolint:gosec
}

func buildLoopBodySpec(full *WorkflowSpec, loopNodes []NodeID) *WorkflowSpec {
	if full == nil || len(loopNodes) == 0 {
		return nil
	}
	included := make(map[NodeID]bool, len(loopNodes))
	for _, id := range loopNodes {
		included[id] = true
	}
	body := NewWorkflow(full.ID + ".loop-body")
	for _, node := range full.Nodes {
		if included[node.ID] {
			body.AddNode(node)
		}
	}
	for _, edge := range full.Edges {
		if included[edge.From] && included[edge.To] {
			body.AddEdge(edge)
		}
	}
	entries := make(map[NodeID]bool, len(loopNodes))
	for _, id := range loopNodes {
		entries[id] = true
	}
	for _, edge := range body.Edges {
		delete(entries, edge.To)
	}
	for _, id := range loopNodes {
		if entries[id] {
			body.WithEntry(id)
		}
	}
	body.Schedule = full.Schedule
	return body
}
