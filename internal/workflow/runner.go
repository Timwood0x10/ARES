// Package workflow — unified single Runner for DAG-based workflow execution.
//
// Phase: P2 — Single Runner.
// The Runner consumes a WorkflowSpec IR and drives execution through the
// Scheduler and ExecutionScope. It is the single production execution path.

package workflow

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// ──────────────────────────────────────────────────────────────────────
// ExecutableFunc — convenience adapter for function-based nodes
// ──────────────────────────────────────────────────────────────────────

// ExecutableFunc wraps a function as an Executor for a single node.
type ExecutableFunc func(ctx context.Context, view StateView) (map[string]any, error)

// ──────────────────────────────────────────────────────────────────────
// NodeExecutor — resolves and executes a single node
// ──────────────────────────────────────────────────────────────────────

// NodeExecutor resolves a node specification into an executable function
// at runtime. The abstraction hides whether the node is backed by an Agent,
// a Tool, a FuncNode, a SubGraph, or a user-provided callback.
type NodeExecutor interface {
	// ExecuteNode executes the given node spec and returns its output.
	ExecuteNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error)
}

// FuncNodeExecutor adapts a map of NodeID→ExecutableFunc as a NodeExecutor.
type FuncNodeExecutor struct {
	mu  sync.RWMutex
	fns map[NodeID]ExecutableFunc
}

// NewFuncNodeExecutor creates a FuncNodeExecutor from a function map.
func NewFuncNodeExecutor() *FuncNodeExecutor {
	return &FuncNodeExecutor{
		fns: make(map[NodeID]ExecutableFunc),
	}
}

// Register binds a function to a node ID.
func (e *FuncNodeExecutor) Register(id NodeID, fn ExecutableFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fns[id] = fn
}

// ExecuteNode runs the registered function for the node.
func (e *FuncNodeExecutor) ExecuteNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error) {
	e.mu.RLock()
	fn, ok := e.fns[spec.ID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("node %q: no executor registered — missing binding", spec.ID)
	}
	output, err := fn(ctx, scope.State())
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", spec.ID, err)
	}
	return output, nil
}

// ──────────────────────────────────────────────────────────────────────
// Runner — single execution engine
// ──────────────────────────────────────────────────────────────────────

// Runner executes a WorkflowSpec IR through a single unified pipeline.
// It replaces engine.Executor, engine.DynamicExecutor, and graph.Graph.Execute.
type Runner struct {
	executor         NodeExecutor
	strategy         ScheduleStrategy
	maxParallel      int
	interruptHandler func(ctx context.Context, spec *InterruptSpec, view StateView) (approved bool, err error)
	recoveryHandler  func(ctx context.Context, nodeID NodeID, nodeErr error, spec *NodeSpec) (recovered bool, replacement map[string]any, err error)
	condEvaluator    func(expr *ConditionExpr, view StateView) bool
}

// NewRunner creates a new Runner with the given NodeExecutor.
func NewRunner(executor NodeExecutor, opts ...RunnerOption) *Runner {
	r := &Runner{
		executor: executor,
		strategy: ScheduleFIFO,
		// Default condition evaluator: reads named values from execution state.
		// Users can override via WithConditionEvaluator.
		condEvaluator: func(expr *ConditionExpr, view StateView) bool {
			if expr == nil {
				return true
			}
			// type "state" reads the named key from state and checks truthiness.
			if expr.Type == "state" {
				val, ok := view.Get(expr.Value)
				return ok && val == true
			}
			// Unknown condition types default to false.
			return false
		},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithScheduleStrategy sets the scheduling strategy.
func WithScheduleStrategy(strategy ScheduleStrategy) RunnerOption {
	return func(r *Runner) {
		r.strategy = strategy
	}
}

// WithInterruptHandler sets the HITL interrupt handler.
// When set, the Runner pauses before executing nodes that have an InterruptSpec
// and waits for the handler to approve or reject execution.
func WithInterruptHandler(handler func(ctx context.Context, spec *InterruptSpec, view StateView) (bool, error)) RunnerOption {
	return func(r *Runner) {
		r.interruptHandler = handler
	}
}

// WithRecoveryHandler sets the step recovery handler.
// When set, the Runner calls the handler when a node fails after exhausting
// all retry attempts. The handler may return a replacement output to recover
// the node, or an error to fail the workflow.
func WithRecoveryHandler(handler func(ctx context.Context, nodeID NodeID, nodeErr error, spec *NodeSpec) (bool, map[string]any, error)) RunnerOption {
	return func(r *Runner) {
		r.recoveryHandler = handler
	}
}

// WithConditionEvaluator sets the condition expression evaluator.
// The evaluator receives a serialized ConditionExpr and the current execution
// state view, and returns true if the condition is satisfied. When set, the
// scheduler uses this callback to decide which control-flow edges to traverse.
//
// Example: match a ConditionExpr of type "state" against a state key:
//
//	runner := NewRunner(exec, WithConditionEvaluator(func(expr *ConditionExpr, view StateView) bool {
//	    val, ok := view.Get(expr.Value)
//	    return ok && val == "approved"
//	}))
func WithConditionEvaluator(eval func(expr *ConditionExpr, view StateView) bool) RunnerOption {
	return func(r *Runner) {
		r.condEvaluator = eval
	}
}

// errInterruptApproved is a sentinel indicating the HITL interrupt was approved.
// It is returned by handleInterrupt and is not propagated to the caller.
var errInterruptApproved = fmt.Errorf("interrupt approved")

// ──────────────────────────────────────────────────────────────────────

// Execute runs a workflow spec to completion and returns the result.
//
// Execution contract (DAG_UNIFIED_PIPELINE.md §10):
//   - All nodes have a status (no silent disappearances).
//   - Condition-false nodes are NotSelected or Unreachable.
//   - Failed-upstream nodes are Blocked.
//   - The checkpointer, if set, persists after each node commit.
func (r *Runner) Execute(ctx context.Context, spec *WorkflowSpec) (*Result, error) {
	if spec == nil {
		return nil, fmt.Errorf("workflow spec must not be nil")
	}

	// Validate before execution.
	if report := Validate(spec); !report.Valid() {
		return nil, fmt.Errorf("workflow %q validation failed: %v", spec.ID, report.Errors)
	}

	// Create execution scope.
	scope := NewExecutionScope("", spec)
	scope.InitNodeStates()

	// Apply MaxParallel from spec schedule.
	r.maxParallel = spec.Schedule.MaxParallel
	if r.maxParallel <= 0 {
		r.maxParallel = 1 // default to sequential
	}

	// Create scheduler.
	sched, err := NewScheduler(spec, r.strategy)
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}
	// Wire condition evaluator if set.
	if r.condEvaluator != nil {
		sched.SetCondEval(func(expr *ConditionExpr) bool {
			// Create a state view that the condition evaluator can access.
			return r.condEvaluator(expr, scope.State())
		})
	}

	// Run loop (with optional loop iterations).
	loop := spec.Loop
	if loop != nil && loop.MaxIterations > 0 {
		var accumulatedState map[string]any
		for iteration := 0; iteration < loop.MaxIterations; iteration++ {
			// Re-create scope and scheduler for each iteration.
			scope = NewExecutionScope("", spec)
			scope.InitNodeStates()
			// Inject accumulated state from previous iterations.
			if accumulatedState != nil {
				w := scope.Writer()
				for k, v := range accumulatedState {
					w.Set(k, v)
				}
				scope.CommitState()
			}
			sched, err = NewScheduler(spec, r.strategy)
			if err != nil {
				return nil, fmt.Errorf("create scheduler for iteration %d: %w", iteration+1, err)
			}
			if r.condEvaluator != nil {
				sched.SetCondEval(func(expr *ConditionExpr) bool {
					return r.condEvaluator(expr, scope.State())
				})
			}
			r.execLoop(ctx, scope, sched)
			// Preserve state for the next iteration.
			accumulatedState = scope.StateSnapshot()
		}
	} else {
		r.execLoop(ctx, scope, sched)
	}

	scope.MarkFinished()
	return scope.ToResult(), nil
}

// execLoop is the core execution loop.
// It consumes nodes from the scheduler, executes them, and handles results.
// nodeResult carries the outcome of a single node execution.
type nodeResult struct {
	id     NodeID
	output map[string]any
	err    error
}

func (r *Runner) execLoop(ctx context.Context, scope *ExecutionScope, sched *Scheduler) {
	resultCh := make(chan nodeResult, len(scope.Spec.Nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, r.maxParallel)

	// Main scheduling loop
	for {
		if ctxCancelled(ctx) {
			r.cancelAll(ctx, scope, &wg, resultCh)
			return
		}

		// Dispatch all ready nodes (parallel fan-out)
		dispatched := r.dispatchReady(ctx, scope, sched, resultCh, &wg, sem)

		if dispatched == 0 {
			// No nodes ready and nothing running: check if we're done
			break
		}

		// Wait for the next node to complete (blocking)
		select {
		case res := <-resultCh:
			r.handleResult(res, scope, sched)
		case <-ctx.Done():
			r.cancelAll(ctx, scope, &wg, resultCh)
			return
		}
	}

	// Drain remaining results — may enqueue new nodes via OnNodeCompleted.
	// Use a polling select to handle both results and completion detection
	// without racing dispatchReady() against wg.Wait().
	for {
		// Dispatch any nodes that became ready from drained results.
		for r.dispatchReady(ctx, scope, sched, resultCh, &wg, sem) > 0 {
		}

		select {
		case res, ok := <-resultCh:
			if !ok {
				return // channel closed
			}
			r.handleResult(res, scope, sched)
		default:
			// No results pending. Check if all goroutines are done.
			done := make(chan struct{}, 1)
			go func() {
				wg.Wait()
				done <- struct{}{}
			}()
			select {
			case <-done:
				close(resultCh)
				r.finaliseUnprocessed(scope, sched)
				return
			case <-time.After(50 * time.Millisecond):
				// Still waiting for goroutines. Re-check for results.
				continue
			}
		}
	}
}

// dispatchReady sends all currently ready nodes to goroutines.
// Returns the number of nodes dispatched.
func (r *Runner) dispatchReady(ctx context.Context, scope *ExecutionScope, sched *Scheduler,
	resultCh chan nodeResult, wg *sync.WaitGroup, sem chan struct{}) int {
	dispatched := 0
	for sched.HasReady() {
		nodeID := sched.Next()
		if nodeID == "" {
			break
		}

		scope.SetNodeStatus(nodeID, NodeStatusRunning)
		dispatched++

		// Find node spec
		var nodeSpec *NodeSpec
		for i := range scope.Spec.Nodes {
			if scope.Spec.Nodes[i].ID == nodeID {
				nodeSpec = &scope.Spec.Nodes[i]
				break
			}
		}
		if nodeSpec == nil {
			scope.SetNodeError(nodeID, fmt.Errorf("node %q not found in spec", nodeID))
			sched.OnNodeFailed(nodeID)
			continue
		}

		// Acquire semaphore (blocking if maxParallel already reached).
		sem <- struct{}{}
		wg.Add(1)
		go func(id NodeID, spec *NodeSpec) {
			defer func() { <-sem }()
			r.executeNodeGoroutine(ctx, spec, id, scope, resultCh, wg)
		}(nodeSpec.ID, nodeSpec)
	}
	return dispatched
}

// executeNodeGoroutine runs a single node with HITL, retry, and recovery.
func (r *Runner) executeNodeGoroutine(ctx context.Context, spec *NodeSpec, id NodeID,
	scope *ExecutionScope, resultCh chan nodeResult, wg *sync.WaitGroup) {
	defer wg.Done()

	// ── HITL interrupt check ──
	if spec.Interrupt != nil && r.interruptHandler != nil {
		_, iErr := r.handleInterrupt(ctx, spec, id, scope, resultCh)
		if iErr != nil {
			if errors.Is(iErr, errInterruptApproved) {
				// Approved — continue to execute
			} else {
				// Already sent result via resultCh inside handleInterrupt
				return
			}
		}
	}

	// ── Execute with retry (or SubWorkflow) ──
	var output map[string]any
	var execErr error

	if spec.SubWorkflow != nil {
		// Execute sub-workflow recursively.
		subResult, subErr := r.Execute(ctx, spec.SubWorkflow)
		if subErr != nil {
			execErr = fmt.Errorf("sub-workflow %s: %w", spec.SubWorkflow.ID, subErr)
		} else {
			output = subResult.State
		}
	} else {
		output, execErr = r.executeSingle(ctx, spec, id, scope, resultCh)
	}

	select {
	case resultCh <- nodeResult{id: id, output: output, err: execErr}:
	case <-ctx.Done():
	}
}

// handleInterrupt checks for HITL and returns nil if approved.
// Returns an error result if rejected or if the handler fails.
func (r *Runner) handleInterrupt(ctx context.Context, spec *NodeSpec, id NodeID,
	scope *ExecutionScope, resultCh chan nodeResult) (map[string]any, error) {
	// Apply timeout if configured.
	interruptCtx := ctx
	if spec.Interrupt.TimeoutSec > 0 {
		var cancel context.CancelFunc
		interruptCtx, cancel = context.WithTimeout(ctx,
			time.Duration(spec.Interrupt.TimeoutSec)*time.Second)
		defer cancel()
	}

	approved, iErr := r.interruptHandler(interruptCtx, spec.Interrupt, scope.State())
	if iErr != nil {
		if errors.Is(iErr, context.DeadlineExceeded) || errors.Is(iErr, context.Canceled) {
			// Timeout or context cancellation: apply auto action.
			return r.handleInterruptTimeout(spec, id, resultCh)
		}
		select {
		case resultCh <- nodeResult{id: id, err: fmt.Errorf("interrupt: %w", iErr)}:
		case <-ctx.Done():
		}
		return nil, iErr
	}
	if !approved {
		select {
		case resultCh <- nodeResult{id: id, err: fmt.Errorf("rejected by human: %s", spec.Interrupt.Message)}:
		case <-ctx.Done():
		}
		return nil, fmt.Errorf("rejected")
	}
	return nil, errInterruptApproved // approved, continue execution (sentinel, not nil)
}

// handleInterruptTimeout applies the configured AutoAction when an interrupt times out.
func (r *Runner) handleInterruptTimeout(spec *NodeSpec, id NodeID,
	resultCh chan nodeResult) (map[string]any, error) {
	switch spec.Interrupt.AutoAction {
	case "approve":
		return nil, errInterruptApproved // auto-approve
	case "skip", "fallback":
		select {
		case resultCh <- nodeResult{id: id, err: fmt.Errorf("interrupt timed out, auto-skipped: %s", spec.Interrupt.Message)}:
		case <-resultCh: // best-effort send
		}
		return nil, fmt.Errorf("auto-skipped")
	default:
		// No auto-action configured: treat as rejected.
		select {
		case resultCh <- nodeResult{id: id, err: fmt.Errorf("interrupt timed out: %s", spec.Interrupt.Message)}:
		case <-resultCh:
		}
		return nil, fmt.Errorf("timed out")
	}
}

// executeSingle runs a node with retry and recovery. Returns the final output or error.
func (r *Runner) executeSingle(ctx context.Context, spec *NodeSpec, id NodeID,
	scope *ExecutionScope, resultCh chan nodeResult) (map[string]any, error) {
	maxAttempts := 1
	if spec.Retry != nil && spec.Retry.MaxAttempts > 0 {
		maxAttempts = spec.Retry.MaxAttempts
	}

	var output map[string]any
	var execErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctxCancelled(ctx) {
			select {
			case resultCh <- nodeResult{id: id, err: ctx.Err()}:
			case <-ctx.Done():
			}
			return nil, ctx.Err()
		}

		output, execErr = r.executor.ExecuteNode(ctx, spec, scope)
		if execErr == nil {
			return output, nil
		}

		if attempt < maxAttempts {
			delay := retryBackoff(spec.Retry, attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				select {
				case resultCh <- nodeResult{id: id, err: ctx.Err()}:
				case <-ctx.Done():
				}
				return nil, ctx.Err()
			}
		}
	}

	// ── Recovery handler (if retries exhausted) ──
	if execErr != nil {
		// Try spec-driven auto-recovery first.
		if spec.Recovery != nil {
			switch spec.Recovery.Strategy {
			case "retry":
				// Already retried per spec.Retry above; if still failing, fall through.
			case "replace_node":
				// Replace_node strategy: execute with a different agent type.
				// Create a modified spec with the replacement agent type.
				if spec.Recovery.ReplacementAgent != "" {
					replSpec := *spec
					replSpec.AgentType = spec.Recovery.ReplacementAgent
					output, replErr := r.executor.ExecuteNode(ctx, &replSpec, scope)
					if replErr == nil {
						return output, nil
					}
					execErr = fmt.Errorf("replace_node %s failed: %w", spec.Recovery.ReplacementAgent, replErr)
				}
			case "fail_fast":
				// Don't attempt recovery — return the original error immediately.
			}
		}

		// Then try the custom recovery handler if set.
		if r.recoveryHandler != nil {
			recovered, replacement, rErr := r.recoveryHandler(ctx, id, execErr, spec)
			if rErr != nil {
				return nil, rErr
			}
			if recovered && replacement != nil {
				return replacement, nil
			}
		}
	}

	return nil, execErr
}

// cancelAll marks all pending nodes as Cancelled and waits for goroutines.
func (r *Runner) cancelAll(ctx context.Context, scope *ExecutionScope, wg *sync.WaitGroup, resultCh chan nodeResult) {
	for _, n := range scope.Spec.Nodes {
		if !scope.IsCompleted(n.ID) {
			scope.SetNodeStatus(n.ID, NodeStatusCancelled)
		}
	}
	wg.Wait()
	close(resultCh)
}

// ctxCancelled returns true if the context has been cancelled.
func ctxCancelled(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// handleResult processes a single node completion.
func (r *Runner) handleResult(res nodeResult, scope *ExecutionScope, sched *Scheduler) {
	if res.err != nil {
		scope.SetNodeError(res.id, res.err)
		sched.OnNodeFailed(res.id)
		return
	}

	// Commit the node output
	scope.SetNodeOutput(res.id, res.output)
	scope.CommitState()

	// Notify scheduler to evaluate downstream edges
	sched.OnNodeCompleted(res.id)
}

// finaliseUnprocessed marks any nodes that were never reached.
func (r *Runner) finaliseUnprocessed(scope *ExecutionScope, sched *Scheduler) {
	for _, n := range scope.Spec.Nodes {
		if !scope.IsCompleted(n.ID) {
			status := scope.NodeStatus(n.ID)
			switch status {
			case NodeStatusPending:
				// Node was never reached — determine why
				if sched != nil {
					// Check if any data-dependency predecessor failed
					failedUpstream := false
					for _, e := range scope.Spec.Edges {
						if e.To == n.ID && e.Kind == EdgeDataDependency {
							ns := scope.NodeStatus(e.From)
							if ns == NodeStatusFailed {
								failedUpstream = true
								break
							}
						}
					}
					if failedUpstream {
						scope.SetNodeStatus(n.ID, NodeStatusBlocked)
					} else {
						// All conditions evaluated false, or node is truly unreachable
						scope.SetNodeStatus(n.ID, NodeStatusUnreachable)
					}
				}
			default:
				scope.SetNodeStatus(n.ID, NodeStatusCancelled)
			}
		}
	}
}

// ──────────────────────────────────────────────────────────────────────
// Convenience: Run a workflow from a spec with a simple executor function
// ──────────────────────────────────────────────────────────────────────

// RunWorkflow executes a WorkflowSpec using the provided function map.
// This is the simplest entry point for tests and simple use cases.
func RunWorkflow(ctx context.Context, spec *WorkflowSpec, fns map[NodeID]ExecutableFunc, opts ...RunnerOption) (*Result, error) {
	if fns == nil {
		fns = make(map[NodeID]ExecutableFunc)
	}
	exec := NewFuncNodeExecutor()
	for id, fn := range fns {
		exec.Register(id, fn)
	}
	runner := NewRunner(exec, opts...)
	return runner.Execute(ctx, spec)
}

// ──────────────────────────────────────────────────────────────────────
// Helper: build a WorkflowSpec and run it with a simple function map
// ──────────────────────────────────────────────────────────────────────

// QuickRun builds a workflow from a Builder-like callback and runs it.
// Example:
//
//	spec := NewWorkflow("demo").
//	    AddNode(NodeSpec{ID: "a", AgentType: "echo"}).
//	    AddNode(NodeSpec{ID: "b", AgentType: "echo"}).
//	    AddEdge(EdgeSpec{From: "a", To: "b", Kind: EdgeDataDependency})
//	result, err := RunWorkflow(ctx, spec, map[NodeID]ExecutableFunc{
//	    "a": func(ctx context.Context, view StateView) (map[string]any, error) {
//	        return map[string]any{"result": "done"}, nil
//	    },
//	})

// retryBackoff calculates the backoff delay for the given attempt.
// Uses exponential backoff with jitter.
func retryBackoff(policy *RetrySpec, attempt int) time.Duration {
	if policy == nil {
		return 0
	}
	initial := policy.InitialDelay
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	maxDelay := policy.MaxDelay
	if maxDelay <= 0 {
		maxDelay = 30 * time.Second
	}
	multiplier := policy.BackoffMultiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}
	base := float64(initial) * math.Pow(multiplier, float64(attempt-1))
	delay := time.Duration(base)

	// Cap at max delay
	if delay > maxDelay {
		delay = maxDelay
	}

	// Add jitter (±25%)
	jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5)) //nolint:gosec // jitter does not need crypto randomness
	return jitter
}
