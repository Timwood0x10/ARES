// Package graph exposes compatibility execution APIs backed by the unified Runner.
package graph

import (
	"context"
	"fmt"

	workflowcore "github.com/Timwood0x10/ares/internal/workflow"
)

// Execute runs the graph through the unified workflow Runner.
func (g *Graph) Execute(ctx context.Context, state *State) (*Result, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if state == nil {
		return nil, fmt.Errorf("state cannot be nil")
	}
	compiled, err := CompileBound(g)
	if err != nil {
		return nil, fmt.Errorf("compile graph %q: %w", g.ID(), err)
	}
	result, execErr := executeCompiledGraph(ctx, state, compiled, compiled.Bound)
	return buildGraphResult(g.ID(), state, result, execErr)
}

func executeCompiledGraph(
	ctx context.Context,
	state *State,
	compiled *CompiledGraph,
	bound *workflowcore.BoundWorkflow,
) (*workflowcore.Result, error) {
	options := append(
		[]workflowcore.RunnerOption{workflowcore.WithInitialState(state.ToParams())},
		compiled.Options...,
	)
	runner := workflowcore.NewRunner(compiled.Executor, options...)
	result, err := runner.ExecuteBound(ctx, bound)
	mergeUnifiedResultState(state, result)
	return result, err
}

func buildGraphResult(
	graphID string,
	state *State,
	result *workflowcore.Result,
	execErr error,
) (*Result, error) {
	graphResult := &Result{GraphID: graphID, State: state, Error: execErr}
	if result != nil {
		graphResult.Duration = result.Duration
	}
	if execErr != nil {
		return graphResult, execErr
	}
	return graphResult, nil
}

func mergeUnifiedResultState(state *State, result *workflowcore.Result) {
	if state == nil || result == nil {
		return
	}
	for key, value := range result.State {
		state.Set(key, value)
	}
}
