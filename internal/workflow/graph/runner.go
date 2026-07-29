package graph

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
	workflowcore "github.com/Timwood0x10/ares/internal/workflow"
)

const graphConditionType = "graph_closure_ref"

// CompiledGraph contains one executable unified graph definition.
type CompiledGraph struct {
	Bound    *workflowcore.BoundWorkflow
	Executor workflowcore.NodeExecutor
	Options  []workflowcore.RunnerOption
}

// CompileBound compiles graph nodes, predicates, routing, and runtime options for the unified Runner.
func CompileBound(graphDef *Graph) (*CompiledGraph, error) {
	if graphDef == nil {
		return nil, fmt.Errorf("graph must not be nil")
	}
	nodes := graphDef.RuntimeNodes()
	if len(nodes) == 0 {
		return nil, fmt.Errorf("graph %q has no nodes", graphDef.ID())
	}
	spec := workflowcore.NewWorkflow(graphDef.ID())
	for id, node := range nodes {
		if node == nil {
			return nil, fmt.Errorf("graph node %q must not be nil", id)
		}
		spec.AddNode(workflowcore.NodeSpec{
			ID:   workflowcore.NodeID(id),
			Name: id,
		})
	}
	predicates := make(map[workflowcore.NodeID]workflowcore.Predicate)
	compileGraphEdges(spec, predicates, graphDef.RuntimeEdges(), hasGraphRouter(graphDef))
	start := graphDef.StartNode()
	if start == "" {
		return nil, fmt.Errorf("graph %q start node is not set", graphDef.ID())
	}
	if _, exists := nodes[start]; !exists {
		return nil, fmt.Errorf("graph %q start node %q not found", graphDef.ID(), start)
	}
	for _, entry := range graphEntries(spec) {
		spec.WithEntry(entry)
	}
	spec.Schedule.MaxParallel = 1
	compileGraphLoop(graphDef, spec)
	bound := &workflowcore.BoundWorkflow{
		Spec:       spec,
		Predicates: predicates,
		Routers:    compileGraphRouters(graphDef, spec),
		Until:      compileGraphUntil(graphDef),
	}
	options := graphRunnerOptions(graphDef)
	return &CompiledGraph{
		Bound:    bound,
		Executor: newGraphNodeExecutor(nodes),
		Options:  options,
	}, nil
}

func compileGraphEdges(
	spec *workflowcore.WorkflowSpec,
	predicates map[workflowcore.NodeID]workflowcore.Predicate,
	edges []RuntimeEdge,
	forceControl bool,
) {
	groups := make(map[string][]RuntimeEdge)
	for _, edge := range edges {
		groups[edge.From] = append(groups[edge.From], edge)
	}
	for from, outgoing := range groups {
		conditionalGroup := forceControl
		for _, edge := range outgoing {
			if edge.Condition != nil {
				conditionalGroup = true
				break
			}
		}
		for _, edge := range outgoing {
			compileGraphEdge(spec, predicates, from, edge, conditionalGroup)
		}
	}
}

func compileGraphEdge(
	spec *workflowcore.WorkflowSpec,
	predicates map[workflowcore.NodeID]workflowcore.Predicate,
	from string,
	edge RuntimeEdge,
	conditionalGroup bool,
) {
	edgeSpec := workflowcore.EdgeSpec{
		From: workflowcore.NodeID(from),
		To:   workflowcore.NodeID(edge.To),
		Kind: workflowcore.EdgeDataDependency,
	}
	if conditionalGroup {
		edgeSpec.Kind = workflowcore.EdgeControlFlow
		edgeSpec.Branch = workflowcore.BranchMany
		edgeSpec.Group = from
		if edge.Condition != nil {
			bindingID := graphConditionBindingID(from, edge.To)
			edgeSpec.Cond = &workflowcore.ConditionExpr{Type: graphConditionType, Value: bindingID}
			condition := edge.Condition
			predicates[workflowcore.NodeID(bindingID)] = func(state map[string]any) bool {
				return condition(NewStateFromValues(state))
			}
		}
	}
	spec.AddEdge(edgeSpec)
}

func hasGraphRouter(graphDef *Graph) bool {
	if graphDef.RuntimeRouter() != nil {
		return true
	}
	bus := graphDef.RuntimePluginBus()
	return bus != nil && len(bus.PluginsByCap(ares_runtime.CapRouter)) > 0
}

func compileGraphRouters(
	graphDef *Graph,
	spec *workflowcore.WorkflowSpec,
) map[workflowcore.NodeID]workflowcore.Router {
	nodeRouter := graphDef.RuntimeRouter()
	pluginBus := graphDef.RuntimePluginBus()
	collector := graphDef.RuntimeCollector()
	if nodeRouter == nil && pluginBus == nil {
		return nil
	}
	routers := make(map[workflowcore.NodeID]workflowcore.Router, len(spec.Nodes))
	for _, node := range spec.Nodes {
		nodeID := node.ID
		routers[nodeID] = func(ctx context.Context, current string, state map[string]any, _ string) string {
			graphState := NewStateFromValues(state)
			if nodeRouter != nil {
				target := nodeRouter(ctx, current, graphState)
				if target != "" {
					if collector != nil {
						collector.RecordRoute(current, target, "dynamic routing", "node-router")
					}
					return target
				}
			}
			if pluginBus == nil {
				return ""
			}
			target, reason, source := routeFromPluginBus(ctx, pluginBus, collector, current, graphState)
			if target != "" && collector != nil {
				collector.RecordRoute(current, target, reason, source)
			}
			return target
		}
	}
	return routers
}

func routeFromPluginBus(
	ctx context.Context,
	bus *ares_runtime.PluginBus,
	collector *ares_runtime.ExecutionCollector,
	nodeID string,
	state *State,
) (string, string, string) {
	routers := bus.PluginsByCap(ares_runtime.CapRouter)
	if len(routers) == 0 {
		return "", "", ""
	}
	router, ok := routers[0].(ares_runtime.RouterPlugin)
	if !ok || router == nil {
		return "", "", ""
	}
	routeState := ares_runtime.RouteState{CurrentStepID: nodeID}
	if collector != nil {
		routeState.Collector = collector
		routeState.CollectedRoutes = collector.RouteHistory()
		routeState.CollectedTools = collector.ToolHistory()
		routeState.CollectedMemory = collector.MemoryHits()
	}
	decision, err := router.Route(ctx, routeState)
	if err != nil || decision == nil {
		return "", "", ""
	}
	return decision.NextStepID, decision.Reason, decision.Source
}

func graphRunnerOptions(graphDef *Graph) []workflowcore.RunnerOption {
	executionID := graphDef.ID()
	if collector := graphDef.RuntimeCollector(); collector != nil {
		executionID = collector.ExecutionID()
	}
	options := []workflowcore.RunnerOption{
		workflowcore.WithExecutionID(executionID),
		workflowcore.WithFailOnNodeError(true),
	}
	if scheduler := graphDef.RuntimeScheduler(); scheduler != nil {
		options = append(options, workflowcore.WithReadySelector(func(ready []workflowcore.NodeID) workflowcore.NodeID {
			candidates := make([]string, len(ready))
			for index, id := range ready {
				candidates[index] = string(id)
			}
			return workflowcore.NodeID(scheduler.Select(candidates))
		}))
	}
	if bus := graphDef.RuntimePluginBus(); bus != nil {
		options = append(options, workflowcore.WithPluginBus(bus))
	}
	if collector := graphDef.RuntimeCollector(); collector != nil {
		options = append(options, workflowcore.WithExecutionCollector(collector))
	}
	if store := graphDef.RuntimeCheckpointStore(); store != nil {
		options = append(options, workflowcore.WithCheckpointStore(store))
	}
	return options
}

func graphEntries(spec *workflowcore.WorkflowSpec) []workflowcore.NodeID {
	inDegree := make(map[workflowcore.NodeID]int, len(spec.Nodes))
	for _, node := range spec.Nodes {
		inDegree[node.ID] = 0
	}
	for _, edge := range spec.Edges {
		inDegree[edge.To]++
	}
	entries := make([]workflowcore.NodeID, 0)
	for _, node := range spec.Nodes {
		if inDegree[node.ID] == 0 {
			entries = append(entries, node.ID)
		}
	}
	return entries
}

func graphLoopPlugin(graphDef *Graph) *ares_runtime.LoopPlugin {
	bus := graphDef.RuntimePluginBus()
	if bus == nil {
		return nil
	}
	for _, plugin := range bus.PluginsByCap(ares_runtime.CapLoop) {
		if loop, ok := plugin.(*ares_runtime.LoopPlugin); ok {
			return loop
		}
	}
	return nil
}

func compileGraphLoop(graphDef *Graph, spec *workflowcore.WorkflowSpec) {
	loop := graphLoopPlugin(graphDef)
	if loop == nil {
		return
	}
	config := loop.Config()
	maxIterations := config.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 1000
	}
	loopNodes := make([]workflowcore.NodeID, 0, len(spec.Nodes))
	for _, node := range spec.Nodes {
		loopNodes = append(loopNodes, node.ID)
	}
	spec.Loop = &workflowcore.LoopSpec{
		MaxIterations: maxIterations,
		LoopNodes:     loopNodes,
	}
}

func compileGraphUntil(graphDef *Graph) workflowcore.LoopPredicate {
	loop := graphLoopPlugin(graphDef)
	if loop == nil || loop.Config().UntilCondition == nil {
		return nil
	}
	condition := loop.Config().UntilCondition
	return func(state map[string]any, _ int) bool {
		return condition(state)
	}
}

func graphConditionBindingID(from, to string) string {
	return "condition:{" + from + "→" + to + "}"
}

type graphNodeExecutor struct {
	nodes map[workflowcore.NodeID]Node
}

func newGraphNodeExecutor(nodes map[string]Node) *graphNodeExecutor {
	bindings := make(map[workflowcore.NodeID]Node, len(nodes))
	for id, node := range nodes {
		bindings[workflowcore.NodeID(id)] = node
	}
	return &graphNodeExecutor{nodes: bindings}
}

func (e *graphNodeExecutor) ExecuteNode(
	ctx context.Context,
	spec *workflowcore.NodeSpec,
	scope *workflowcore.ExecutionScope,
) (map[string]any, error) {
	node, exists := e.nodes[spec.ID]
	if !exists {
		return nil, fmt.Errorf("graph node %q binding not found", spec.ID)
	}
	state := NewStateFromValues(scope.StateSnapshot())
	if err := node.Execute(ctx, state); err != nil {
		return nil, fmt.Errorf("execute graph node %q: %w", spec.ID, err)
	}
	return state.ToParams(), nil
}
