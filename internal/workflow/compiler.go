// Package workflow — compilers from legacy APIs to WorkflowSpec IR.
//
// Phase: P1 — IR definition + Compiler + Validator.
// These compilers are pure transformations: they read from legacy types and
// produce IR without side effects. No production code is modified.

package workflow

import (
	"context"
	"fmt"

	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
)

// CompiledWorkflow holds a WorkflowSpec IR together with closures that cannot
// be serialized (Condition, Router, UntilCondition). These closures are
// captured during compilation and must be reattached at execution time via
// Runner.WithConditionEvaluator or passed through a Bindings adapter.
//
// When CompileFromEngine or CompileFromGraph encounter a closure that they
// cannot preserve, they either capture it here (best-effort) or return an
// error (fail-fast) depending on the strictness mode.
type CompiledWorkflow struct {
	// Spec is the serializable intermediate representation.
	Spec *WorkflowSpec `json:"spec"`
	// ConditionFuncs holds the Condition closures keyed by node ID.
	ConditionFuncs map[NodeID]func(vars map[string]any) bool `json:"-"`
	// RouterFuncs holds the Router closures keyed by node ID.
	RouterFuncs map[NodeID]func(ctx context.Context, stepID string, variables map[string]any, stepOutput string) string `json:"-"`
	// UntilCondition holds the loop's UntilCondition closure, if any.
	UntilCondition func(variables map[string]any, iteration int) bool `json:"-"`
}

// ──────────────────────────────────────────────────────────────────────
// Compiler: engine.Workflow → WorkflowSpec
// ──────────────────────────────────────────────────────────────────────

// CompileFromEngine converts an engine.Workflow to a WorkflowSpec.
// The legacy Condition and Router closures cannot be serialized; they are
// stored as reference markers in the EdgeSpec.Cond field. The caller must
// provide a Bindings map to reattach executable closures before execution.
//
// Deprecated: this function supports engine.Workflow source compatibility.
// New workflows should use the Builder API directly.
func CompileFromEngine(w *wfengine.Workflow) (*WorkflowSpec, error) {
	if w == nil {
		return nil, fmt.Errorf("workflow must not be nil")
	}

	spec := NewWorkflow(w.ID)
	nodeIDs := make(map[string]bool)
	if err := compileEngineSteps(spec, w.Steps, nodeIDs); err != nil {
		return nil, err
	}

	// ── Condition references ──
	for _, step := range w.Steps {
		annotateConditionRef(spec, step)
		annotateRouterRef(spec, step)
	}

	// ── UntilCondition reference ──
	compileUntilCondition(spec, w.LoopConfig)

	// ── Entries: zero-in-degree nodes ──
	spec.Entries = computeEntries(spec)

	// ── Loop ──
	if w.LoopConfig != nil {
		loopNodes := make([]NodeID, len(w.LoopConfig.LoopSteps))
		for i, s := range w.LoopConfig.LoopSteps {
			loopNodes[i] = NodeID(s)
		}
		spec.Loop = &LoopSpec{
			MaxIterations: w.LoopConfig.MaxIterations,
			LoopNodes:     loopNodes,
		}
	}

	spec.Schedule = ScheduleSpec{MaxParallel: 1}
	return spec, nil
}

// CompileFromEngineWithBindings compiles an engine.Workflow and captures all
// closures (Condition, Router, UntilCondition) that the basic CompileFromEngine
// silently drops. Returns a CompiledWorkflow containing both the spec and the
// captured closures.
//
// If any step has a Condition or Router that cannot be preserved, this function
// captures it into the bindings map rather than dropping it silently.
// The closures can be reattached at execution time via the Runner's
// WithConditionEvaluator option.
func CompileFromEngineWithBindings(w *wfengine.Workflow) (*CompiledWorkflow, error) {
	spec, err := CompileFromEngine(w)
	if err != nil {
		return nil, err
	}

	cw := &CompiledWorkflow{
		Spec:           spec,
		ConditionFuncs: make(map[NodeID]func(vars map[string]any) bool),
		RouterFuncs:    make(map[NodeID]func(ctx context.Context, stepID string, variables map[string]any, stepOutput string) string),
	}

	for _, step := range w.Steps {
		if step.Condition != nil {
			// Capture the Condition closure.
			fn := step.Condition
			cw.ConditionFuncs[NodeID(step.ID)] = fn
		}
		if step.Router != nil {
			// Capture the Router closure.
			fn := step.Router
			cw.RouterFuncs[NodeID(step.ID)] = fn
		}
	}

	if w.LoopConfig != nil && w.LoopConfig.UntilCondition != nil {
		cw.UntilCondition = w.LoopConfig.UntilCondition
	}

	return cw, nil
}

// annotateConditionRef records that a Condition closure existed on a step.
// The closure cannot be serialized, so we annotate the edge kind and metadata.
func annotateConditionRef(spec *WorkflowSpec, step *wfengine.Step) {
	if step.Condition == nil || len(step.DependsOn) == 0 {
		return
	}
	setNodeMeta(spec, step.ID, "_closure_condition", "true")
}

// annotateRouterRef records that a Router closure existed on a step.
func annotateRouterRef(spec *WorkflowSpec, step *wfengine.Step) {
	if step.Router == nil {
		return
	}
	setNodeMeta(spec, step.ID, "_closure_router", "true")
}

// setNodeMeta sets a metadata key-value pair on a node in the spec.
func setNodeMeta(spec *WorkflowSpec, nodeID, key, value string) {
	for i := range spec.Nodes {
		if spec.Nodes[i].ID == NodeID(nodeID) {
			if spec.Nodes[i].Metadata == nil {
				spec.Nodes[i].Metadata = make(map[string]string)
			}
			spec.Nodes[i].Metadata[key] = value
			return
		}
	}
}

// compileEngineSteps compiles workflow steps into NodeSpecs and edges.
func compileEngineSteps(spec *WorkflowSpec, steps []*wfengine.Step, nodeIDs map[string]bool) error {
	for _, step := range steps {
		if step == nil {
			return fmt.Errorf("step is nil")
		}
		if nodeIDs[step.ID] {
			return fmt.Errorf("duplicate node ID %q", step.ID)
		}
		nodeIDs[step.ID] = true

		ns := NodeSpec{
			ID:        NodeID(step.ID),
			Name:      step.Name,
			AgentType: step.AgentType,
			Input:     step.Input,
			Timeout:   step.Timeout,
		}
		convertPolicies(step, &ns)
		if err := convertSubWorkflow(step, &ns); err != nil {
			return fmt.Errorf("compile sub-workflow for step %q: %w", step.ID, err)
		}
		convertMetadata(step, &ns)
		if step.Condition != nil {
			ns.Condition = &ConditionExpr{Type: "bound", Value: step.ID}
		}
		spec.AddNode(ns)

		for _, dep := range step.DependsOn {
			spec.AddEdge(EdgeSpec{
				From: NodeID(dep),
				To:   NodeID(step.ID),
				Kind: EdgeDataDependency,
			})
		}
	}
	return nil
}

// convertPolicies copies RetryPolicy and RecoveryPolicy from a step to a NodeSpec.
func convertPolicies(step *wfengine.Step, ns *NodeSpec) {
	if step.RetryPolicy != nil {
		ns.Retry = &RetrySpec{
			MaxAttempts:       step.RetryPolicy.MaxAttempts,
			InitialDelay:      step.RetryPolicy.InitialDelay,
			MaxDelay:          step.RetryPolicy.MaxDelay,
			BackoffMultiplier: step.RetryPolicy.BackoffMultiplier,
		}
	}
	if step.RecoveryPolicy != nil {
		ns.Recovery = &RecoverySpec{
			Strategy: string(step.RecoveryPolicy.Strategy),
		}
		if step.RecoveryPolicy.ReplacementAgent != "" {
			ns.Recovery.ReplacementAgent = step.RecoveryPolicy.ReplacementAgent
		}
	}
	if step.Interrupt != nil {
		ns.Interrupt = &InterruptSpec{Message: step.Interrupt.Message}
	}
}

// convertSubWorkflow recursively compiles a nested sub-workflow.
func convertSubWorkflow(step *wfengine.Step, ns *NodeSpec) error {
	if step.SubWorkflow == nil {
		return nil
	}
	sub, err := CompileFromEngine(step.SubWorkflow)
	if err != nil {
		return fmt.Errorf("compile %q: %w", step.SubWorkflow.ID, err)
	}
	ns.SubWorkflow = sub
	return nil
}

// convertMetadata copies metadata from a step to a NodeSpec.
func convertMetadata(step *wfengine.Step, ns *NodeSpec) {
	if step.Metadata != nil {
		ns.Metadata = make(map[string]string, len(step.Metadata))
		for k, v := range step.Metadata {
			ns.Metadata[k] = v
		}
	}
}

// compileUntilCondition records the UntilCondition closure reference from a loop config.
func compileUntilCondition(spec *WorkflowSpec, loopCfg *wfengine.LoopConfig) {
	if loopCfg == nil || loopCfg.UntilCondition == nil {
		return
	}
	if spec.Loop == nil {
		spec.Loop = &LoopSpec{MaxIterations: loopCfg.MaxIterations}
	}
}

// computeEntries computes entry nodes as all zero-in-degree nodes.
func computeEntries(spec *WorkflowSpec) []NodeID {
	inDegree := make(map[NodeID]int)
	for _, n := range spec.Nodes {
		inDegree[n.ID] = 0
	}
	for _, e := range spec.Edges {
		if e.Kind == EdgeDataDependency {
			inDegree[e.To]++
		}
	}
	var entries []NodeID
	for _, n := range spec.Nodes {
		if inDegree[n.ID] == 0 {
			entries = append(entries, n.ID)
		}
	}
	return entries
}
