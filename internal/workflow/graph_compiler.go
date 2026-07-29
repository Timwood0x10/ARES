package workflow

import "fmt"

// GraphCompileEdge contains the serializable topology required by graph compilation.
type GraphCompileEdge struct {
	From       string
	To         string
	HasCond    bool
	BindingRef string
}

// GraphCompileSource exposes graph topology without coupling the unified runtime to one builder package.
type GraphCompileSource interface {
	ID() string
	NodeIDs() []string
	StartNode() string
	CompileEdges() []GraphCompileEdge
}

// CompileFromGraph converts a legacy graph definition to the unified workflow specification.
//
// Deprecated: use graph.CompileBound for executable graph migration.
func CompileFromGraph(graphDef GraphCompileSource) (*WorkflowSpec, error) {
	if graphDef == nil {
		return nil, fmt.Errorf("graph must not be nil")
	}

	spec := NewWorkflow(graphDef.ID())
	seen := make(map[string]bool)
	for _, id := range graphDef.NodeIDs() {
		if seen[id] {
			return nil, fmt.Errorf("duplicate node ID %q in graph %q", id, graphDef.ID())
		}
		seen[id] = true
		spec.AddNode(NodeSpec{ID: NodeID(id), Name: id})
	}
	for _, edgeInfo := range graphDef.CompileEdges() {
		kind := EdgeDataDependency
		var condition *ConditionExpr
		if edgeInfo.HasCond {
			kind = EdgeControlFlow
			condition = &ConditionExpr{Type: "graph_closure_ref", Value: edgeInfo.BindingRef}
		}
		spec.AddEdge(EdgeSpec{
			From: NodeID(edgeInfo.From),
			To:   NodeID(edgeInfo.To),
			Kind: kind,
			Cond: condition,
		})
	}
	start := graphDef.StartNode()
	if start != "" {
		spec.Entries = append(spec.Entries, NodeID(start))
	} else {
		spec.Entries = computeGraphEntries(spec)
	}
	spec.Schedule = ScheduleSpec{MaxParallel: 1}
	return spec, nil
}

func computeGraphEntries(spec *WorkflowSpec) []NodeID {
	inDegree := make(map[NodeID]int, len(spec.Nodes))
	for _, node := range spec.Nodes {
		inDegree[node.ID] = 0
	}
	for _, edge := range spec.Edges {
		inDegree[edge.To]++
	}
	entries := make([]NodeID, 0)
	for _, node := range spec.Nodes {
		if inDegree[node.ID] == 0 {
			entries = append(entries, node.ID)
		}
	}
	return entries
}
