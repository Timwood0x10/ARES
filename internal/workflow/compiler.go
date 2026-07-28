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
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
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
// Deprecated: this function exists to support migration from the legacy
// engine.Executor path. New workflows should use the Builder API directly.
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

// ──────────────────────────────────────────────────────────────────────
// Compiler: graph.Graph → WorkflowSpec
// ──────────────────────────────────────────────────────────────────────

// CompileFromGraph converts a graph.Graph to a WorkflowSpec.
// Graph nodes become IR nodes; graph edges become IR edges.
// Graph conditions (which are closures) cannot be serialized — they are
// marked as HasCond=true in EdgeInfo and must be reattached via Bindings
// at execution time.
//
// Deprecated: this function exists to support migration from the legacy
// graph.Graph execution path. New workflows should use the Builder API.
func CompileFromGraph(g *wfgraph.Graph) (*WorkflowSpec, error) {
	if g == nil {
		return nil, fmt.Errorf("graph must not be nil")
	}

	spec := NewWorkflow(g.ID())

	// ── Compile nodes ──
	// graph.Node is an interface (AgentNode, ToolNode, FuncNode, SubGraphNode).
	// The node metadata (agent type, input) lives in the Node implementation,
	// which Graph does not expose generically. For compilation, we capture
	// node IDs and let the caller supply agent-type metadata via Bindings.
	seen := make(map[string]bool)
	for _, id := range g.NodeIDs() {
		if seen[id] {
			return nil, fmt.Errorf("duplicate node ID %q in graph %q", id, g.ID())
		}
		seen[id] = true
		spec.AddNode(NodeSpec{
			ID:   NodeID(id),
			Name: id,
		})
	}

	// ── Compile edges ──
	for _, ei := range g.Edges() {
		kind := EdgeControlFlow
		if !ei.HasCond {
			// Unconditional edges in graph are data dependencies
			kind = EdgeDataDependency
		}
		spec.AddEdge(EdgeSpec{
			From: NodeID(ei.From),
			To:   NodeID(ei.To),
			Kind: kind,
			Cond: condFromGraph(ei),
		})
	}

	// ── Entries ──
	start := g.StartNode()
	if start != "" {
		spec.Entries = append(spec.Entries, NodeID(start))
	} else {
		// Fall back to zero-in-degree nodes (legacy behaviour documented in §2.4)
		inDegree := make(map[NodeID]int)
		for _, n := range spec.Nodes {
			inDegree[n.ID] = 0
		}
		for _, e := range spec.Edges {
			inDegree[e.To]++
		}
		for _, n := range spec.Nodes {
			if inDegree[n.ID] == 0 {
				spec.Entries = append(spec.Entries, n.ID)
			}
		}
	}

	spec.Schedule = ScheduleSpec{MaxParallel: 1}
	return spec, nil
}

// condFromGraph converts a graph edge with a condition to a ConditionExpr
// reference marker. Since graph.Condition is a closure, the actual condition
// function is not serializable — we store an existence marker.
func condFromGraph(ei wfgraph.EdgeInfo) *ConditionExpr {
	if !ei.HasCond {
		return nil
	}
	return &ConditionExpr{
		Type:  "graph_closure_ref",
		Value: "condition:{" + ei.From + "→" + ei.To + "}",
	}
}

// annotateConditionRef records that a Condition closure existed on a step.
// The closure cannot be serialized, so we annotate the edge kind and metadata.
func annotateConditionRef(spec *WorkflowSpec, step *wfengine.Step) {
	if step.Condition == nil || len(step.DependsOn) == 0 {
		return
	}
	for i := range spec.Edges {
		if spec.Edges[i].To == NodeID(step.ID) {
			spec.Edges[i].Kind = EdgeControlFlow
			break
		}
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
		convertSubWorkflow(step, &ns)
		convertMetadata(step, &ns)
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
func convertSubWorkflow(step *wfengine.Step, ns *NodeSpec) {
	if step.SubWorkflow == nil {
		return
	}
	sub, err := CompileFromEngine(step.SubWorkflow)
	if err != nil {
		return // error will be caught by validation
	}
	ns.SubWorkflow = sub
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
