package workflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

const outputStateKey = "output"

// EngineStepExecutor executes one legacy engine step after the unified Runner resolves its input.
type EngineStepExecutor interface {
	Execute(ctx context.Context, step *engine.Step, input string, taskCtx *models.TaskContext) (string, error)
}

// EngineNodeExecutor adapts legacy engine steps to the unified NodeExecutor contract.
type EngineNodeExecutor struct {
	executor EngineStepExecutor
	steps    map[NodeID]*engine.Step
}

// NewEngineNodeExecutor creates a unified node executor for legacy engine workflow definitions.
func NewEngineNodeExecutor(registry *engine.AgentRegistry, steps []*engine.Step) (*EngineNodeExecutor, error) {
	if registry == nil {
		return nil, fmt.Errorf("agent registry must not be nil")
	}
	return NewEngineNodeExecutorWithStepExecutor(engine.NewAgentExecutor(registry), steps)
}

// NewEngineNodeExecutorWithStepExecutor creates an engine adapter with an injected step executor.
func NewEngineNodeExecutorWithStepExecutor(
	executor EngineStepExecutor,
	steps []*engine.Step,
) (*EngineNodeExecutor, error) {
	if executor == nil {
		return nil, fmt.Errorf("engine step executor must not be nil")
	}
	bindings := make(map[NodeID]*engine.Step, len(steps))
	for index, step := range steps {
		if step == nil {
			return nil, fmt.Errorf("engine step at index %d must not be nil", index)
		}
		id := NodeID(step.ID)
		if id == "" {
			return nil, fmt.Errorf("engine step at index %d has empty ID", index)
		}
		if _, exists := bindings[id]; exists {
			return nil, fmt.Errorf("duplicate engine step binding %q", id)
		}
		bindings[id] = step
	}
	return &EngineNodeExecutor{executor: executor, steps: bindings}, nil
}

// ExecuteNode resolves legacy input semantics and executes the bound engine step.
func (e *EngineNodeExecutor) ExecuteNode(
	ctx context.Context,
	spec *NodeSpec,
	scope *ExecutionScope,
) (map[string]any, error) {
	if spec == nil {
		return nil, fmt.Errorf("node spec must not be nil")
	}
	if scope == nil {
		return nil, fmt.Errorf("execution scope must not be nil")
	}
	step, exists := e.steps[spec.ID]
	if !exists {
		return nil, fmt.Errorf("node %q: engine step binding not found", spec.ID)
	}
	input := resolveEngineStepInput(step, scope.State())
	output, err := e.executor.Execute(ctx, step, input, &models.TaskContext{})
	if err != nil {
		return nil, fmt.Errorf("execute engine step %q: %w", step.ID, err)
	}
	return map[string]any{outputStateKey: output}, nil
}

func resolveEngineStepInput(step *engine.Step, state StateView) string {
	initialInput := stateString(state, "input")
	if step.Input != "" {
		return replaceEngineInputTemplates(step.Input, initialInput, step.DependsOn, state)
	}
	if len(step.DependsOn) == 0 {
		return initialInput
	}
	outputs := make([]string, 0, len(step.DependsOn))
	for _, dependency := range step.DependsOn {
		if output, exists := engineNodeOutput(state, NodeID(dependency)); exists {
			outputs = append(outputs, output)
		}
	}
	if len(outputs) == 0 {
		return initialInput
	}
	return strings.Join(outputs, "\n\n")
}

func replaceEngineInputTemplates(template, initialInput string, dependencies []string, state StateView) string {
	result := strings.ReplaceAll(template, "{{.input}}", initialInput)
	for _, dependency := range dependencies {
		output, exists := engineNodeOutput(state, NodeID(dependency))
		if !exists {
			continue
		}
		result = strings.ReplaceAll(result, "{{."+dependency+"}}", output)
	}
	return result
}

func engineNodeOutput(state StateView, id NodeID) (string, bool) {
	output, exists := state.GetNodeOutput(id)
	if !exists {
		return "", false
	}
	value, exists := output[outputStateKey]
	if !exists {
		return "", false
	}
	return fmt.Sprint(value), true
}

func stateString(state StateView, key string) string {
	value, exists := state.Get(key)
	if !exists {
		return ""
	}
	return fmt.Sprint(value)
}
