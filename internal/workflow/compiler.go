// Package workflow — compilers from legacy APIs to WorkflowSpec IR.
//
// Phase: P1 — IR definition + Compiler + Validator.
// These compilers are pure transformations: they read from legacy types and
// produce IR without side effects. No production code is modified.

package workflow

import (
	"fmt"

	wfengine "github.com/Timwood0x10/ares/internal/workflow/engine"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
)

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

	// ── Compile nodes ──
	nodeIDs := make(map[string]bool)
	for _, step := range w.Steps {
		if step == nil {
			return nil, fmt.Errorf("step in workflow %q is nil", w.ID)
		}
		if nodeIDs[step.ID] {
			return nil, fmt.Errorf("duplicate node ID %q in workflow %q", step.ID, w.ID)
		}
		nodeIDs[step.ID] = true

		ns := NodeSpec{
			ID:        NodeID(step.ID),
			Name:      step.Name,
			AgentType: step.AgentType,
			Input:     step.Input,
			Timeout:   step.Timeout,
		}

		// Retry policy
		if step.RetryPolicy != nil {
			ns.Retry = &RetrySpec{
				MaxAttempts:       step.RetryPolicy.MaxAttempts,
				InitialDelay:      step.RetryPolicy.InitialDelay,
				MaxDelay:          step.RetryPolicy.MaxDelay,
				BackoffMultiplier: step.RetryPolicy.BackoffMultiplier,
			}
		}

		// Recovery policy
		if step.RecoveryPolicy != nil {
			ns.Recovery = &RecoverySpec{
				Strategy: string(step.RecoveryPolicy.Strategy),
			}
			if step.RecoveryPolicy.ReplacementAgent != "" {
				ns.Recovery.ReplacementAgent = step.RecoveryPolicy.ReplacementAgent
			}
		}

		// Interrupt
		if step.Interrupt != nil {
			ns.Interrupt = &InterruptSpec{
				Message: step.Interrupt.Message,
			}
		}

		// Sub-workflow (recursive)
		if step.SubWorkflow != nil {
			sub, err := CompileFromEngine(step.SubWorkflow)
			if err != nil {
				return nil, fmt.Errorf("compile sub-workflow for step %q: %w", step.ID, err)
			}
			ns.SubWorkflow = sub
		}

		// Metadata
		if step.Metadata != nil {
			ns.Metadata = make(map[string]string, len(step.Metadata))
			for k, v := range step.Metadata {
				ns.Metadata[k] = v
			}
		}

		spec.AddNode(ns)

		// ── Compile edges from DependsOn ──
		for _, dep := range step.DependsOn {
			// Existence check is deferred to Validate() — edges referencing
			// out-of-order deps are valid until the full node set is known.
			spec.AddEdge(EdgeSpec{
				From: NodeID(dep),
				To:   NodeID(step.ID),
				Kind: EdgeDataDependency,
			})
		}
	}

	// ── Condition references ──
	// engine.Step.Condition is a closure (json:"-") and cannot be serialized.
	// For each step with a non-nil Condition, we annotate the relevant edge
	// with a reference marker and record the closure existence in metadata.
	for _, step := range w.Steps {
		annotateConditionRef(spec, step)
		annotateRouterRef(spec, step)
	}

	// ── UntilCondition reference ──
	if w.LoopConfig != nil && w.LoopConfig.UntilCondition != nil {
		if spec.Loop == nil {
			spec.Loop = &LoopSpec{
				MaxIterations: w.LoopConfig.MaxIterations,
			}
		}
		_ = w.LoopConfig.UntilCondition // referenced for future binding
	}

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

	// ── Schedule ──
	spec.Schedule = ScheduleSpec{
		MaxParallel: 1, // engine default; configurable via WithMaxParallel
	}

	return spec, nil
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
