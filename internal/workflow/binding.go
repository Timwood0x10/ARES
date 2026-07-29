package workflow

import (
	"context"
	"fmt"
)

// Predicate evaluates a serializable predicate reference against committed state.
type Predicate func(state map[string]any) bool

// Router selects the next control-flow target after a node completes.
type Router func(ctx context.Context, nodeID string, state map[string]any, output string) string

// LoopPredicate determines whether a loop stops after a committed iteration.
type LoopPredicate func(state map[string]any, iteration int) bool

// BoundWorkflow is the executable form of a compiled workflow specification.
type BoundWorkflow struct {
	Spec       *WorkflowSpec
	Predicates map[NodeID]Predicate
	Routers    map[NodeID]Router
	Until      LoopPredicate
}

// BindCompiledWorkflow converts compiler output into the single runtime binding object.
func BindCompiledWorkflow(compiled *CompiledWorkflow) (*BoundWorkflow, error) {
	if compiled == nil {
		return nil, fmt.Errorf("compiled workflow must not be nil")
	}
	if compiled.Spec == nil {
		return nil, fmt.Errorf("compiled workflow spec must not be nil")
	}
	bound := &BoundWorkflow{
		Spec:       compiled.Spec,
		Predicates: make(map[NodeID]Predicate, len(compiled.ConditionFuncs)),
		Routers:    make(map[NodeID]Router, len(compiled.RouterFuncs)),
		Until:      compiled.UntilCondition,
	}
	for id, predicate := range compiled.ConditionFuncs {
		if predicate == nil {
			return nil, fmt.Errorf("predicate binding %q must not be nil", id)
		}
		bound.Predicates[id] = predicate
	}
	for id, router := range compiled.RouterFuncs {
		if router == nil {
			return nil, fmt.Errorf("router binding %q must not be nil", id)
		}
		bound.Routers[id] = router
	}
	return bound, nil
}

// ExecuteBound executes a bound workflow through this Runner.
func (r *Runner) ExecuteBound(ctx context.Context, bound *BoundWorkflow) (*Result, error) {
	if bound == nil {
		return nil, fmt.Errorf("bound workflow must not be nil")
	}
	configured := *r
	configured.predicates = clonePredicates(bound.Predicates)
	configured.routers = cloneRouters(bound.Routers)
	configured.untilCondition = bound.Until
	return configured.Execute(ctx, bound.Spec)
}

func clonePredicates(source map[NodeID]Predicate) map[NodeID]Predicate {
	result := make(map[NodeID]Predicate, len(source))
	for id, predicate := range source {
		result[id] = predicate
	}
	return result
}

func cloneRouters(source map[NodeID]Router) map[NodeID]Router {
	result := make(map[NodeID]Router, len(source))
	for id, router := range source {
		result[id] = router
	}
	return result
}
