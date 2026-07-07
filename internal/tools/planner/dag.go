package planner

import (
	"fmt"
)

// DAGValidator validates execution plan DAG structure before execution.
type DAGValidator struct{}

// NewDAGValidator creates a DAG validator.
func NewDAGValidator() *DAGValidator {
	return &DAGValidator{}
}

// ValidationError describes a single DAG validation failure.
type ValidationError struct {
	StepID  string
	Code    string
	Message string
}

// Error returns the validation error message.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("dag: step %q %s: %s", e.StepID, e.Code, e.Message)
}

// Validate checks an execution plan for DAG correctness.
// Returns nil if valid, or a slice of validation errors.
func (v *DAGValidator) Validate(plan *ExecutionPlan) []*ValidationError {
	if plan == nil {
		return []*ValidationError{{StepID: "", Code: "nil_plan", Message: "plan is nil"}}
	}
	if len(plan.Steps) == 0 {
		return []*ValidationError{{StepID: "", Code: "no_steps", Message: "plan has no steps"}}
	}

	var errs []*ValidationError

	// Build step index for lookup.
	stepIndex := make(map[string]int)
	for i, step := range plan.Steps {
		if step.StepID == "" {
			errs = append(errs, &ValidationError{
				StepID:  fmt.Sprintf("step[%d]", i),
				Code:    "empty_id",
				Message: "step has empty StepID",
			})
			continue
		}
		if _, exists := stepIndex[step.StepID]; exists {
			errs = append(errs, &ValidationError{
				StepID:  step.StepID,
				Code:    "duplicate_id",
				Message: "duplicate StepID",
			})
		}
		stepIndex[step.StepID] = i
	}
	if len(errs) > 0 {
		return errs
	}

	// Check for missing dependencies.
	for _, step := range plan.Steps {
		for _, dep := range step.DependsOn {
			if _, exists := stepIndex[dep]; !exists {
				errs = append(errs, &ValidationError{
					StepID:  step.StepID,
					Code:    "missing_dependency",
					Message: fmt.Sprintf("depends on %q which does not exist", dep),
				})
			}
		}
	}

	// Check for cycles using DFS.
	cycleErrs := v.detectCycles(plan.Steps, stepIndex)
	errs = append(errs, cycleErrs...)

	// Check for input/output compatibility.
	compatErrs := v.checkIOCompatibility(plan)
	errs = append(errs, compatErrs...)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

// detectCycles detects cycles in the step dependency graph using DFS.
func (v *DAGValidator) detectCycles(steps []ExecutionStep, stepIndex map[string]int) []*ValidationError {
	var errs []*ValidationError
	// 0 = unvisited, 1 = visiting, 2 = visited
	state := make(map[string]int)
	for _, step := range steps {
		state[step.StepID] = 0
	}

	var dfs func(stepID string) bool
	dfs = func(stepID string) bool {
		state[stepID] = 1
		idx, ok := stepIndex[stepID]
		if !ok {
			state[stepID] = 2
			return false
		}
		for _, dep := range steps[idx].DependsOn {
			depState, exists := state[dep]
			if !exists {
				continue
			}
			if depState == 1 {
				return true
			}
			if depState == 0 {
				if dfs(dep) {
					return true
				}
			}
		}
		state[stepID] = 2
		return false
	}

	for _, step := range steps {
		if state[step.StepID] == 0 {
			if dfs(step.StepID) {
				errs = append(errs, &ValidationError{
					StepID:  step.StepID,
					Code:    "cycle_detected",
					Message: "dependency cycle detected in execution plan",
				})
			}
		}
	}
	return errs
}

// checkIOCompatibility validates that each step's inputs match its
// dependencies' outputs using capability type matching.
func (v *DAGValidator) checkIOCompatibility(plan *ExecutionPlan) []*ValidationError {
	var errs []*ValidationError

	if !plan.IsMultiStep || len(plan.Steps) < 2 {
		return nil
	}

	// Build output type lookup from capability definitions.
	outputTypes := make(map[string]string)
	for _, c := range BuiltinCapabilities() {
		outputTypes[c.Name] = c.OutputType
	}

	// Track the output type of each step.
	stepOutput := make(map[string]string)
	for _, step := range plan.Steps {
		// Use capability name or tool name to find output type.
		if outType, ok := outputTypes[step.CapabilityName]; ok {
			stepOutput[step.StepID] = outType
		}
	}

	for _, step := range plan.Steps {
		for _, dep := range step.DependsOn {
			depOutput, depExists := stepOutput[dep]
			if !depExists {
				continue
			}

			// Check if the step's input type matches the dependency's output type.
			stepInput := inputTypeFor(step.CapabilityName)
			if depOutput != "Any" && stepInput != "Any" && depOutput != stepInput {
				errs = append(errs, &ValidationError{
					StepID: step.StepID,
					Code:   "incompatible_io",
					Message: fmt.Sprintf("step needs %q input but dependency %q produces %q output",
						stepInput, dep, depOutput),
				})
			}
		}
	}

	return errs
}
