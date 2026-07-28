// Package workflow — unified single Runner for DAG-based workflow execution.
//
// Phase: P2 — Single Runner.
// The Runner consumes a WorkflowSpec IR and drives execution through the
// Scheduler and ExecutionScope. It is the single production execution path.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
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
	initialInput     string
	initialVariables map[string]string
	interruptHandler func(ctx context.Context, spec *InterruptSpec, view StateView) (approved bool, err error)
	recoveryHandler  func(ctx context.Context, nodeID NodeID, nodeErr error, spec *NodeSpec) (recovered bool, replacement map[string]any, err error)
	condEvaluator    func(expr *ConditionExpr, view StateView) bool
	pluginBus        *ares_runtime.PluginBus
	checkpointStore  ares_runtime.CheckpointStore
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

// WithInitialInput sets the initial workflow input string that is injected
// into the execution scope's state before any node runs. The input is
// accessible via StateView.Get("input") during node execution.
func WithInitialInput(input string) RunnerOption {
	return func(r *Runner) {
		r.initialInput = input
	}
}

// WithInitialVariables sets the initial workflow variables that are
// injected into the execution scope's state before any node runs.
func WithInitialVariables(vars map[string]string) RunnerOption {
	return func(r *Runner) {
		r.initialVariables = vars
	}
}

// WithPluginBus attaches a PluginBus for BeforeStep/AfterStep hooks and event
// emission during workflow execution. When set, the Runner invokes all
// registered WorkflowHook plugins before and after each node executes.
func WithPluginBus(bus *ares_runtime.PluginBus) RunnerOption {
	return func(r *Runner) {
		r.pluginBus = bus
	}
}

// WithCheckpointStore attaches a CheckpointStore for crash recovery. When set,
// the Runner saves a checkpoint after each node completes, persisting the
// current execution state so it can be resumed after a restart.
func WithCheckpointStore(store ares_runtime.CheckpointStore) RunnerOption {
	return func(r *Runner) {
		r.checkpointStore = store
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
	// Inject initial input and variables into the execution state.
	scope.SetInitialState(r.initialInput, r.initialVariables)

	// Read MaxParallel as execution-local variable (not shared Runner field).
	maxParallel := spec.Schedule.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1 // default to sequential
	}

	// Create scheduler.
	sched, err := NewScheduler(spec, r.strategy)
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}
	// Wire condition evaluator if set.
	if r.condEvaluator != nil {
		sched.SetCondEval(func(expr *ConditionExpr) bool {
			return r.condEvaluator(expr, scope.State())
		})
	}

	// Run loop (with optional loop iterations).
	loop := spec.Loop
	if loop != nil && loop.MaxIterations > 0 {
		var accumulatedState map[string]any

		// Build a loop-body sub-spec once (reused for iterations > 1).
		var bodySpec *WorkflowSpec
		if len(loop.LoopNodes) > 0 {
			bodySpec = buildLoopBodySpec(spec, loop.LoopNodes)
		}

		for iteration := 0; iteration < loop.MaxIterations; iteration++ {
			// Use the full spec for iteration 0 (includes setup nodes),
			// then the body-only sub-spec for subsequent iterations.
			iterSpec := spec
			if iteration > 0 && bodySpec != nil {
				iterSpec = bodySpec
			}

			scope = NewExecutionScope("", iterSpec)
			scope.InitNodeStates()
			if accumulatedState != nil {
				w := scope.Writer()
				for k, v := range accumulatedState {
					w.Set(k, v)
				}
				scope.CommitState()
			}
			sched, err = NewScheduler(iterSpec, r.strategy)
			if err != nil {
				return nil, fmt.Errorf("create scheduler for iteration %d: %w", iteration+1, err)
			}
			if r.condEvaluator != nil {
				sched.SetCondEval(func(expr *ConditionExpr) bool {
					return r.condEvaluator(expr, scope.State())
				})
			}
			r.execLoop(ctx, scope, sched, maxParallel)
			accumulatedState = scope.StateSnapshot()
		}
	} else {
		r.execLoop(ctx, scope, sched, maxParallel)
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

func (r *Runner) execLoop(ctx context.Context, scope *ExecutionScope, sched *Scheduler, maxParallel int) {
	resultCh := make(chan nodeResult, len(scope.Spec.Nodes))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxParallel)
	var running int32 // atomic counter for active goroutines

	// Main scheduling loop
	for {
		if ctxCancelled(ctx) {
			r.cancelAll(ctx, scope, &wg, resultCh)
			return
		}

		// Dispatch all ready nodes (parallel fan-out)
		dispatched := r.dispatchReady(ctx, scope, sched, resultCh, &wg, sem, &running, maxParallel)

		if dispatched == 0 && atomic.LoadInt32(&running) == 0 {
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
	for {
		// Dispatch any nodes that became ready from drained results.
		for r.dispatchReady(ctx, scope, sched, resultCh, &wg, sem, &running, maxParallel) > 0 {
		}

		select {
		case res, ok := <-resultCh:
			if !ok {
				return
			}
			r.handleResult(res, scope, sched)
		default:
			if atomic.LoadInt32(&running) == 0 {
				close(resultCh)
				r.finaliseUnprocessed(scope, sched)
				return
			}
			// Still waiting — brief poll before re-checking.
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
				continue
			}
		}
	}
}

// dispatchReady sends all currently ready nodes to goroutines.
// Returns the number of nodes dispatched.
func (r *Runner) dispatchReady(ctx context.Context, scope *ExecutionScope, sched *Scheduler,
	resultCh chan nodeResult, wg *sync.WaitGroup, sem chan struct{}, running *int32, maxParallel int) int {
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
		atomic.AddInt32(running, 1)
		wg.Add(1)
		go func(id NodeID, spec *NodeSpec, mp int) {
			defer func() {
				<-sem
				atomic.AddInt32(running, -1)
			}()
			r.executeNodeGoroutine(ctx, spec, id, scope, resultCh, wg, mp)
		}(nodeSpec.ID, nodeSpec, maxParallel)
	}
	return dispatched
}

// executeNodeGoroutine runs a single node with HITL, retry, and recovery.
func (r *Runner) executeNodeGoroutine(ctx context.Context, spec *NodeSpec, id NodeID,
	scope *ExecutionScope, resultCh chan nodeResult, wg *sync.WaitGroup, maxParallel int) {
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

	// BeforeStep hook
	startTime := time.Now()
	r.emitBeforeStep(ctx, id, spec)

	if spec.SubWorkflow != nil {
		// Execute sub-workflow with inherited parent scope state.
		subResult, subErr := r.executeChildScope(ctx, spec.SubWorkflow, scope, maxParallel)
		if subErr != nil {
			execErr = fmt.Errorf("sub-workflow %s: %w", spec.SubWorkflow.ID, subErr)
		} else {
			output = subResult
		}
	} else {
		output, execErr = r.executeSingle(ctx, spec, id, scope, resultCh)
	}

	// AfterStep hook
	r.emitAfterStep(ctx, id, spec, output, execErr, startTime, scope)

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
		resultCh <- nodeResult{id: id, err: fmt.Errorf("interrupt timed out, auto-skipped: %s", spec.Interrupt.Message)}
		return nil, fmt.Errorf("auto-skipped")
	default:
		resultCh <- nodeResult{id: id, err: fmt.Errorf("interrupt timed out: %s", spec.Interrupt.Message)}
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

		// Apply node-level timeout if configured.
		execCtx := ctx
		if spec.Timeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
			defer cancel()
		}

		output, execErr = r.executor.ExecuteNode(execCtx, spec, scope)
		if execErr == nil {
			return output, nil
		}

		// Record failed attempt.
		scope.RecordAttempt(id)

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

// executeChildScope runs a sub-workflow as a child of the given parent scope,
// inheriting the parent's state. Returns the child's final state snapshot.
func (r *Runner) executeChildScope(ctx context.Context, subSpec *WorkflowSpec, parentScope *ExecutionScope, maxParallel int) (map[string]any, error) {
	if subSpec == nil {
		return nil, fmt.Errorf("sub-workflow spec must not be nil")
	}
	if report := Validate(subSpec); !report.Valid() {
		return nil, fmt.Errorf("sub-workflow %q validation failed: %v", subSpec.ID, report.Errors)
	}

	childScope := NewExecutionScope("", subSpec)
	childScope.InitNodeStates()

	// Inherit parent state: copy all committed state from parent to child.
	parentState := parentScope.StateSnapshot()
	if parentState != nil {
		w := childScope.Writer()
		for k, v := range parentState {
			w.Set(k, v)
		}
		childScope.CommitState()
	}

	childSched, err := NewScheduler(subSpec, r.strategy)
	if err != nil {
		return nil, fmt.Errorf("create child scheduler: %w", err)
	}
	if r.condEvaluator != nil {
		childSched.SetCondEval(func(expr *ConditionExpr) bool {
			return r.condEvaluator(expr, childScope.State())
		})
	}

	r.execLoop(ctx, childScope, childSched, maxParallel)
	childScope.MarkFinished()
	return childScope.StateSnapshot(), nil
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
					switch {
					case sched.BranchSkipped(n.ID):
						scope.SetNodeStatus(n.ID, NodeStatusNotSelected)
					case failedUpstream:
						scope.SetNodeStatus(n.ID, NodeStatusBlocked)
					default:
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

	if delay > maxDelay {
		delay = maxDelay
	}

	jitter := time.Duration(float64(delay) * (0.75 + rand.Float64()*0.5)) //nolint:gosec
	return jitter
}

// ── PluginBus hooks ─────────────────────────────────────────────

// stepFromSpec creates an ares_runtime Step from a NodeSpec.
func stepFromSpec(spec *NodeSpec) *ares_runtime.Step {
	return &ares_runtime.Step{
		ID:        string(spec.ID),
		Name:      spec.Name,
		AgentType: spec.AgentType,
		Status:    ares_runtime.StepStatusRunning,
		StartedAt: time.Now(),
	}
}

// stepResultFromOutput creates an ares_runtime StepResult from execution output.
func stepResultFromOutput(id NodeID, spec *NodeSpec, output map[string]any, execErr error, startTime time.Time) *ares_runtime.StepResult {
	r := &ares_runtime.StepResult{
		StepID:   string(id),
		Name:     spec.Name,
		Duration: time.Since(startTime),
	}
	if execErr != nil {
		r.Status = ares_runtime.StepStatusFailed
		r.Error = execErr.Error()
	} else {
		r.Status = ares_runtime.StepStatusCompleted
	}
	return r
}

// emitBeforeStep calls BeforeStep on all registered WorkflowHook plugins
// and emits a step.started event.
func (r *Runner) emitBeforeStep(ctx context.Context, id NodeID, spec *NodeSpec) {
	if r.pluginBus == nil {
		return
	}
	step := stepFromSpec(spec)
	if err := r.pluginBus.BeforeStep(ctx, string(id), step); err != nil {
		slog.Warn("runner: BeforeStep hook failed (continuing)",
			"node_id", id, "error", err)
	}
	r.pluginBus.Emit(ctx, string(id), ares_runtime.EventStepStarted, "workflow", map[string]any{
		ares_runtime.PayloadKeyExecutionID: string(id),
		ares_runtime.PayloadKeyStepID:      string(id),
	})
}

// emitAfterStep calls AfterStep on all registered WorkflowHook plugins,
// emits a step.completed/step.failed event, and saves a checkpoint if
// a CheckpointStore is configured.
func (r *Runner) emitAfterStep(ctx context.Context, id NodeID, spec *NodeSpec, output map[string]any, execErr error, startTime time.Time, scope *ExecutionScope) {
	if r.pluginBus == nil && r.checkpointStore == nil {
		return
	}

	result := stepResultFromOutput(id, spec, output, execErr, startTime)

	if r.pluginBus != nil {
		if err := r.pluginBus.AfterStep(ctx, string(id), result); err != nil {
			slog.Warn("runner: AfterStep hook failed (continuing)",
				"node_id", id, "error", err)
		}
		// Emit step completion event.
		eventType := ares_runtime.EventStepCompleted
		if execErr != nil {
			eventType = ares_runtime.EventStepFailed
		}
		r.pluginBus.Emit(ctx, string(id), eventType, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: string(id),
			ares_runtime.PayloadKeyStepID:      string(id),
			ares_runtime.PayloadKeyStatus:      string(result.Status),
			ares_runtime.PayloadKeyDuration:    result.Duration.Milliseconds(),
		})
	}

	// Save checkpoint after each completed node for crash recovery.
	if r.checkpointStore != nil {
		if err := r.saveRunnerCheckpoint(ctx, scope, id, result); err != nil {
			slog.Warn("runner: checkpoint save failed (continuing)",
				"node_id", id, "error", err)
		}
	}
}

// saveRunnerCheckpoint persists the complete execution state for crash recovery.
// The checkpoint includes:
//   - execution ID and spec ID
//   - all node statuses
//   - committed state
//   - the current node result
func (r *Runner) saveRunnerCheckpoint(ctx context.Context, scope *ExecutionScope, nodeID NodeID, result *ares_runtime.StepResult) error {
	if scope == nil {
		return fmt.Errorf("scope must not be nil")
	}
	// Build a snapshot of all node states.
	nodeStates := make([]map[string]any, 0)
	for _, ns := range scope.NodeStates() {
		nodeStates = append(nodeStates, map[string]any{
			"id":       string(ns.ID),
			"status":   string(ns.Status),
			"error":    ns.Error,
			"attempts": ns.Attempts,
			"output":   ns.Output,
		})
	}

	data := map[string]any{
		"saved_at":     time.Now(),
		"execution_id": scope.ExecutionID,
		"spec_id":      scope.Spec.ID,
		"node_id":      string(nodeID),
		"node_result": map[string]any{
			"status":   string(result.Status),
			"error":    result.Error,
			"duration": result.Duration.Milliseconds(),
		},
		"state":       scope.StateSnapshot(),
		"node_states": nodeStates,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}
	return r.checkpointStore.Save(ctx, ares_runtime.CheckpointKey(scope.ExecutionID), payload)
}

// buildLoopBodySpec creates a sub-spec containing only the loop body nodes.
func buildLoopBodySpec(fullSpec *WorkflowSpec, loopNodes []NodeID) *WorkflowSpec {
	if fullSpec == nil || len(loopNodes) == 0 {
		return nil
	}
	bodySet := make(map[NodeID]bool, len(loopNodes))
	for _, id := range loopNodes {
		bodySet[id] = true
	}
	body := NewWorkflow(fullSpec.ID + ".loop-body")
	for _, n := range fullSpec.Nodes {
		if bodySet[n.ID] {
			body.AddNode(n)
		}
	}
	for _, e := range fullSpec.Edges {
		if bodySet[e.From] && bodySet[e.To] {
			body.AddEdge(e)
		}
	}
	bodyEntries := make(map[NodeID]bool)
	for _, id := range loopNodes {
		bodyEntries[id] = true
	}
	for _, e := range body.Edges {
		if e.Kind == EdgeDataDependency {
			delete(bodyEntries, e.To)
		}
	}
	for id := range bodyEntries {
		body.Entries = append(body.Entries, id)
	}
	body.Schedule = fullSpec.Schedule
	return body
}
