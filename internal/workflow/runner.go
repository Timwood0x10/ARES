// Package workflow executes the unified workflow intermediate representation.
package workflow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// ExecutableFunc wraps a function as one executable workflow node.
type ExecutableFunc func(ctx context.Context, view StateView) (map[string]any, error)

// NodeExecutor resolves and executes one node specification.
type NodeExecutor interface {
	// ExecuteNode executes the node and returns its state contribution.
	ExecuteNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error)
}

// FuncNodeExecutor adapts registered functions to NodeExecutor.
type FuncNodeExecutor struct {
	mu  sync.RWMutex
	fns map[NodeID]ExecutableFunc
}

// NewFuncNodeExecutor creates an empty function executor.
func NewFuncNodeExecutor() *FuncNodeExecutor {
	return &FuncNodeExecutor{fns: make(map[NodeID]ExecutableFunc)}
}

// Register binds a function to a node ID.
func (e *FuncNodeExecutor) Register(id NodeID, fn ExecutableFunc) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fns[id] = fn
}

// ExecuteNode runs the function registered for the node.
func (e *FuncNodeExecutor) ExecuteNode(ctx context.Context, spec *NodeSpec, scope *ExecutionScope) (map[string]any, error) {
	e.mu.RLock()
	fn, ok := e.fns[spec.ID]
	e.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("node %q: no executor registered: missing binding", spec.ID)
	}
	output, err := fn(ctx, scope.State())
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", spec.ID, err)
	}
	return output, nil
}

// Runner executes WorkflowSpec through the single scheduler and scope lifecycle.
type Runner struct {
	executor         NodeExecutor
	strategy         ScheduleStrategy
	initialInput     string
	initialVariables map[string]string
	initialState     map[string]any
	interruptHandler func(context.Context, *InterruptSpec, StateView) (bool, error)
	recoveryHandler  func(context.Context, NodeID, error, *NodeSpec) (bool, map[string]any, error)
	customCondition  func(*ConditionExpr, StateView) bool
	predicates       map[NodeID]Predicate
	routers          map[NodeID]Router
	untilCondition   LoopPredicate
	pluginBus        *ares_runtime.PluginBus
	checkpointStore  ares_runtime.CheckpointStore
	readySelector    func([]NodeID) NodeID
	executionID      string
	failOnNodeError  bool
	eventSink        RunnerEventSink
	patchQueue       *PatchQueue
	collector        *ares_runtime.ExecutionCollector
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// NewRunner creates a Runner with deterministic default semantics.
func NewRunner(executor NodeExecutor, opts ...RunnerOption) *Runner {
	runner := &Runner{
		executor:   executor,
		strategy:   ScheduleFIFO,
		predicates: make(map[NodeID]Predicate),
		routers:    make(map[NodeID]Router),
	}
	for _, option := range opts {
		option(runner)
	}
	return runner
}

// WithScheduleStrategy sets the scheduling strategy.
func WithScheduleStrategy(strategy ScheduleStrategy) RunnerOption {
	return func(runner *Runner) { runner.strategy = strategy }
}

// WithReadySelector sets a custom selector for the current ready-node set.
func WithReadySelector(selector func([]NodeID) NodeID) RunnerOption {
	return func(runner *Runner) { runner.readySelector = selector }
}

// WithInterruptHandler sets the human approval callback.
func WithInterruptHandler(handler func(context.Context, *InterruptSpec, StateView) (bool, error)) RunnerOption {
	return func(runner *Runner) { runner.interruptHandler = handler }
}

// WithRecoveryHandler sets the exhausted-retry recovery callback.
func WithRecoveryHandler(handler func(context.Context, NodeID, error, *NodeSpec) (bool, map[string]any, error)) RunnerOption {
	return func(runner *Runner) { runner.recoveryHandler = handler }
}

// WithConditionEvaluator sets an evaluator for serializable condition languages.
func WithConditionEvaluator(evaluator func(*ConditionExpr, StateView) bool) RunnerOption {
	return func(runner *Runner) { runner.customCondition = evaluator }
}

// WithBindings attaches legacy condition closures by node ID.
func WithBindings(bindings map[NodeID]func(map[string]any) bool) RunnerOption {
	return func(runner *Runner) {
		runner.predicates = make(map[NodeID]Predicate, len(bindings))
		for id, predicate := range bindings {
			runner.predicates[id] = predicate
		}
	}
}

// WithUntilCondition sets the loop termination predicate.
func WithUntilCondition(predicate func(map[string]any, int) bool) RunnerOption {
	return func(runner *Runner) { runner.untilCondition = predicate }
}

// WithCompiledWorkflow attaches all non-serializable compiler bindings.
func WithCompiledWorkflow(compiled *CompiledWorkflow) RunnerOption {
	return func(runner *Runner) {
		if compiled == nil {
			return
		}
		runner.predicates = make(map[NodeID]Predicate, len(compiled.ConditionFuncs))
		for id, predicate := range compiled.ConditionFuncs {
			runner.predicates[id] = predicate
		}
		runner.routers = make(map[NodeID]Router, len(compiled.RouterFuncs))
		for id, router := range compiled.RouterFuncs {
			runner.routers[id] = router
		}
		runner.untilCondition = compiled.UntilCondition
	}
}

// WithInitialInput sets the input stored under the input state key.
func WithInitialInput(input string) RunnerOption {
	return func(runner *Runner) { runner.initialInput = input }
}

// WithInitialVariables sets initial execution variables.
func WithInitialVariables(variables map[string]string) RunnerOption {
	return func(runner *Runner) { runner.initialVariables = variables }
}

// WithInitialState sets arbitrary initial state before execution starts.
func WithInitialState(state map[string]any) RunnerOption {
	return func(runner *Runner) { runner.initialState = cloneAnyMap(state) }
}

// WithPluginBus attaches workflow lifecycle plugins.
func WithPluginBus(bus *ares_runtime.PluginBus) RunnerOption {
	return func(runner *Runner) { runner.pluginBus = bus }
}

// WithCheckpointStore attaches durable execution storage.
func WithCheckpointStore(store ares_runtime.CheckpointStore) RunnerOption {
	return func(runner *Runner) { runner.checkpointStore = store }
}

// WithExecutionID sets the execution identity used by lifecycle integrations.
func WithExecutionID(executionID string) RunnerOption {
	return func(runner *Runner) { runner.executionID = executionID }
}

// WithFailOnNodeError makes terminal node failures return a Go error.
func WithFailOnNodeError(enabled bool) RunnerOption {
	return func(runner *Runner) { runner.failOnNodeError = enabled }
}

// WithEventSink attaches the native ordered Runner event sink.
func WithEventSink(sink RunnerEventSink) RunnerOption {
	return func(runner *Runner) { runner.eventSink = sink }
}

// WithPatchQueue attaches the only supported runtime topology mutation path.
func WithPatchQueue(queue *PatchQueue) RunnerOption {
	return func(runner *Runner) { runner.patchQueue = queue }
}

// WithExecutionCollector attaches lifecycle collection to the next execution.
func WithExecutionCollector(collector *ares_runtime.ExecutionCollector) RunnerOption {
	return func(runner *Runner) { runner.collector = collector }
}

// Execute validates and executes a workflow specification.
func (r *Runner) Execute(ctx context.Context, spec *WorkflowSpec) (*Result, error) {
	if err := validateExecutionInput(r.executor, spec); err != nil {
		return nil, err
	}
	scope := NewExecutionScope(r.executionID, spec)
	if r.collector != nil {
		if r.collector.ExecutionID() != scope.ExecutionID {
			return nil, fmt.Errorf("execution collector %q does not match execution %q", r.collector.ExecutionID(), scope.ExecutionID)
		}
		scope.SetCollector(r.collector)
	}
	scope.InitNodeStates()
	scope.SetInitialState(r.initialInput, r.initialVariables)
	for key, value := range r.initialState {
		scope.Writer().Set(key, value)
	}
	scope.CommitState()
	if err := r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventWorkflowStarted, Status: NodeStatusRunning}); err != nil {
		return scope.ToResult(), fmt.Errorf("publish workflow started: %w", err)
	}
	r.emitWorkflowStarted(ctx, scope)
	if err := r.executeWorkflow(ctx, scope, spec, 0); err != nil {
		scope.MarkFinished()
		r.emitWorkflowFinished(ctx, scope, err)
		if publishErr := r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventWorkflowFailed, Status: NodeStatusFailed, Error: err.Error()}); publishErr != nil {
			return scope.ToResult(), errors.Join(err, fmt.Errorf("publish workflow failed: %w", publishErr))
		}
		return scope.ToResult(), err
	}
	scope.MarkFinished()
	result := scope.ToResult()
	if result.Status == NodeStatusFailed {
		execErr := scope.Err()
		if execErr == nil {
			execErr = fmt.Errorf("workflow %q failed", spec.ID)
		}
		r.emitWorkflowFinished(ctx, scope, execErr)
		if publishErr := r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventWorkflowFailed, Status: NodeStatusFailed, Error: execErr.Error()}); publishErr != nil {
			return result, errors.Join(execErr, fmt.Errorf("publish workflow failed: %w", publishErr))
		}
		if r.failOnNodeError {
			return result, execErr
		}
		return result, nil
	}
	r.emitWorkflowFinished(ctx, scope, nil)
	if err := r.publishEvent(ctx, scope, RunnerEvent{Type: RunnerEventWorkflowCompleted, Status: result.Status}); err != nil {
		return result, fmt.Errorf("publish workflow completed: %w", err)
	}
	return result, nil
}

func validateExecutionInput(executor NodeExecutor, spec *WorkflowSpec) error {
	if spec == nil {
		return fmt.Errorf("workflow spec must not be nil")
	}
	if report := Validate(spec); !report.Valid() {
		return fmt.Errorf("workflow %q validation failed: %v", spec.ID, report.Errors)
	}
	if executor == nil && len(spec.Nodes) > 0 {
		return fmt.Errorf("node executor must not be nil")
	}
	return nil
}

func (r *Runner) evaluateCondition(expr *ConditionExpr, scope *ExecutionScope) bool {
	if expr == nil {
		return true
	}
	switch expr.Type {
	case conditionTypeState:
		value, ok := scope.State().Get(expr.Value)
		result, isBool := value.(bool)
		return ok && isBool && result
	case "bound", "graph_closure_ref":
		predicate, ok := r.predicates[NodeID(expr.Value)]
		return ok && predicate(scope.StateSnapshot())
	default:
		return r.customCondition != nil && r.customCondition(expr, scope.State())
	}
}

// RunWorkflow executes a spec using a function map.
func RunWorkflow(ctx context.Context, spec *WorkflowSpec, functions map[NodeID]ExecutableFunc, opts ...RunnerOption) (*Result, error) {
	executor := NewFuncNodeExecutor()
	for id, function := range functions {
		executor.Register(id, function)
	}
	return NewRunner(executor, opts...).Execute(ctx, spec)
}
