// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/errors"
)

// Execute runs the graph with the given state.
//
// Execute acquires a read lock on the graph for the duration of execution,
// preventing concurrent mutations. Multiple Execute calls may run concurrently
// with each other but not with mutation methods.
func (g *Graph) Execute(ctx context.Context, state *State) (*Result, error) {
	return g.execute(ctx, state, nil)
}

// ExecuteFromCheckpoint resumes graph execution from a previous checkpoint.
// The executed slice contains node IDs that were completed in a prior run and
// should not be re-executed. Their successors' in-degrees are automatically
// adjusted so the graph continues from the first unexecuted node.
//
// The caller is responsible for restoring state from the checkpoint before
// calling this method. State must contain the same values as when the
// checkpoint was created.
func (g *Graph) ExecuteFromCheckpoint(ctx context.Context, state *State, executed []string) (*Result, error) {
	initial := make(map[string]bool, len(executed))
	for _, id := range executed {
		initial[id] = true
	}
	return g.execute(ctx, state, initial)
}

// execute is the shared execution core used by Execute and ExecuteFromCheckpoint.
// initialExecuted contains node IDs that were completed in a prior execution
// and should not be re-run (nil for fresh executions).
func (g *Graph) execute(ctx context.Context, state *State, initialExecuted map[string]bool) (*Result, error) {
	if err := g.validateGraph(); err != nil {
		return nil, err
	}

	if state == nil {
		return nil, fmt.Errorf("state cannot be nil")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	if err := g.validateStartNode(); err != nil {
		return nil, err
	}

	if err := g.applyRateLimit(ctx); err != nil {
		return nil, err
	}

	startTime := time.Now()
	iteration := 0
	loopIterKey := "__loop_iteration"

	for {
		iteration++
		g.updateLoopIteration(state, iteration, loopIterKey)

		if g.pluginBus != nil {
			g.emitWorkflowStarted(ctx, iteration, initialExecuted)
		}

		executed := g.initializeExecutedSet(iteration, initialExecuted)

		inDegree := g.buildInDegreeMap()
		g.decrementPreExecutedSuccessors(inDegree, iteration, initialExecuted)

		readyQueue, readySet := g.seedReadyQueue(inDegree, executed)

		if err := g.executeReadyQueue(ctx, state, startTime, iteration, readyQueue, readySet, inDegree, executed); err != nil {
			return nil, err
		}

		if g.shouldContinueLoop(ctx, state, iteration) {
			continue
		}
		break
	}

	g.finalizeExecution(ctx, state, startTime)

	return g.buildResult(state, startTime), nil
}

// validateGraph validates basic graph properties
func (g *Graph) validateGraph() error {
	if g == nil {
		return fmt.Errorf("graph is nil")
	}
	return nil
}

// validateStartNode validates the start node exists
func (g *Graph) validateStartNode() error {
	if g.start == "" {
		return fmt.Errorf("graph start node is not set")
	}
	if _, ok := g.nodes[g.start]; !ok {
		return fmt.Errorf("start node %s not found", g.start)
	}
	return nil
}

// applyRateLimit applies rate limiting if configured
func (g *Graph) applyRateLimit(ctx context.Context) error {
	if g.limiter != nil {
		if err := g.limiter.Wait(ctx); err != nil {
			return errors.Wrap(err, "rate limit")
		}
	}
	return nil
}

// updateLoopIteration updates state with loop iteration count
func (g *Graph) updateLoopIteration(state *State, iteration int, loopIterKey string) {
	if iteration > 1 {
		state.Set(loopIterKey, iteration)
	}
}

// emitWorkflowStarted emits workflow started event
func (g *Graph) emitWorkflowStarted(ctx context.Context, iteration int, initialExecuted map[string]bool) {
	payload := map[string]any{
		ares_runtime.PayloadKeyExecutionID: g.id,
		ares_runtime.PayloadKeyWorkflowID:  g.id,
	}
	if iteration == 1 && len(initialExecuted) > 0 {
		payload["resumed"] = true
	}
	g.pluginBus.Emit(ctx, g.id, ares_runtime.EventWorkflowStarted, "workflow", payload)
}

// initializeExecutedSet initializes the executed set
func (g *Graph) initializeExecutedSet(iteration int, initialExecuted map[string]bool) map[string]bool {
	executed := make(map[string]bool)
	if iteration == 1 {
		for k, v := range initialExecuted {
			executed[k] = v
		}
	}
	return executed
}

// buildInDegreeMap builds in-degree map for nodes
func (g *Graph) buildInDegreeMap() map[string]int {
	inDegree := make(map[string]int, len(g.nodes))
	for id := range g.nodes {
		inDegree[id] = 0
	}
	for _, edges := range g.edges {
		for _, edge := range edges {
			inDegree[edge.to]++
		}
	}
	return inDegree
}

// decrementPreExecutedSuccessors decrements in-degree for pre-executed node successors
func (g *Graph) decrementPreExecutedSuccessors(inDegree map[string]int, iteration int, initialExecuted map[string]bool) {
	if iteration == 1 {
		for id := range initialExecuted {
			for _, edge := range g.edges[id] {
				inDegree[edge.to]--
			}
		}
	}
}

// seedReadyQueue seeds the ready queue with nodes having no predecessors
func (g *Graph) seedReadyQueue(inDegree map[string]int, executed map[string]bool) ([]string, map[string]bool) {
	readyQueue := make([]string, 0)
	readySet := make(map[string]bool)
	for id, deg := range inDegree {
		if deg == 0 && !executed[id] {
			readyQueue = append(readyQueue, id)
			readySet[id] = true
		}
	}
	return readyQueue, readySet
}

// executeReadyQueue executes nodes in the ready queue
func (g *Graph) executeReadyQueue(ctx context.Context, state *State, startTime time.Time, iteration int,
	readyQueue []string, readySet map[string]bool, inDegree map[string]int, executed map[string]bool) error {
	for len(readyQueue) > 0 {
		nodeID := g.scheduler.Select(readyQueue)
		if nodeID == "" {
			break
		}

		readyQueue, readySet = g.removeFromQueue(readyQueue, readySet, nodeID)

		if executed[nodeID] {
			continue
		}

		node, ok := g.nodes[nodeID]
		if !ok {
			return fmt.Errorf("node %s not found", nodeID)
		}

		if err := g.checkContextCancellation(ctx, startTime); err != nil {
			return err
		}

		nodeDuration, execErr := g.executeSingleNode(ctx, state, startTime, nodeID, node)

		if err := g.handleNodeError(ctx, startTime, nodeID, execErr, nodeDuration); err != nil {
			return err
		}

		executed[nodeID] = true

		readyQueue = g.handleDynamicRouting(ctx, state, nodeID, readyQueue, readySet, executed)

		readyQueue = g.processSuccessors(state, nodeID, inDegree, readyQueue, readySet, executed)
	}
	return nil
}

// removeFromQueue removes a node from the ready queue
func (g *Graph) removeFromQueue(readyQueue []string, readySet map[string]bool, nodeID string) ([]string, map[string]bool) {
	for i, id := range readyQueue {
		if id == nodeID {
			readyQueue = append(readyQueue[:i], readyQueue[i+1:]...)
			break
		}
	}
	delete(readySet, nodeID)
	return readyQueue, readySet
}

// checkContextCancellation checks if context is cancelled
func (g *Graph) checkContextCancellation(ctx context.Context, startTime time.Time) error {
	select {
	case <-ctx.Done():
		if g.pluginBus != nil {
			g.pluginBus.Emit(ctx, g.id, ares_runtime.EventWorkflowFailed, "workflow", map[string]any{
				ares_runtime.PayloadKeyExecutionID: g.id,
				ares_runtime.PayloadKeyWorkflowID:  g.id,
				ares_runtime.PayloadKeyStatus:      ares_runtime.StepStatusFailed,
				ares_runtime.PayloadKeyError:       ctx.Err().Error(),
				ares_runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
			})
		}
		return errors.Wrap(ctx.Err(), "execution cancelled")
	default:
		return nil
	}
}

// executeSingleNode executes a single node and returns duration and error
func (g *Graph) executeSingleNode(ctx context.Context, state *State, startTime time.Time, nodeID string, node Node) (time.Duration, error) {
	g.beforeNodeExecution(ctx, nodeID)

	nodeStart := time.Now()
	execErr := node.Execute(ctx, state)
	nodeDuration := time.Since(nodeStart)

	g.afterNodeExecution(ctx, startTime, nodeID, execErr, nodeDuration)

	return nodeDuration, execErr
}

// beforeNodeExecution runs hooks before node execution
func (g *Graph) beforeNodeExecution(ctx context.Context, nodeID string) {
	step := &ares_runtime.Step{ID: nodeID, Name: nodeID, StartedAt: time.Now()}
	if g.pluginBus != nil {
		if err := g.pluginBus.BeforeStep(ctx, g.id, step); err != nil {
			log.Warn("graph: before step hook failed (continuing)",
				"graph_id", g.id, "node", nodeID, "error", err)
		}
		g.pluginBus.Emit(ctx, g.id, ares_runtime.EventStepStarted, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: g.id,
			ares_runtime.PayloadKeyStepID:      nodeID,
		})
	}

	if g.tracer != nil {
		g.tracer.RecordAgentStep(ctx, &ares_observability.AgentStep{
			TraceID:  g.tracer.GetTraceID(ctx),
			AgentID:  nodeID,
			StepName: "execute",
		})
	}
}

// afterNodeExecution runs hooks after node execution
func (g *Graph) afterNodeExecution(ctx context.Context, startTime time.Time, nodeID string, execErr error, nodeDuration time.Duration) {
	stepResult := &ares_runtime.StepResult{
		StepID:   nodeID,
		Status:   ares_runtime.StepStatusCompleted,
		Duration: nodeDuration,
	}

	if execErr != nil {
		stepResult.Status = ares_runtime.StepStatusFailed
		stepResult.Error = execErr.Error()
		if g.tracer != nil {
			g.tracer.RecordError(ctx, &ares_observability.AgentError{
				TraceID:   g.tracer.GetTraceID(ctx),
				AgentID:   nodeID,
				ErrorType: "execution_error",
				Message:   execErr.Error(),
			})
		}
	}

	if g.pluginBus != nil {
		if err := g.pluginBus.AfterStep(ctx, g.id, stepResult); err != nil {
			log.Warn("graph: after step hook failed (continuing)",
				"graph_id", g.id, "node", nodeID, "error", err)
		}
		g.emitStepResult(ctx, startTime, nodeID, execErr, nodeDuration)
	}

	if execErr == nil && g.tracer != nil {
		g.tracer.RecordAgentStep(ctx, &ares_observability.AgentStep{
			TraceID:  g.tracer.GetTraceID(ctx),
			AgentID:  nodeID,
			StepName: "execute",
			Duration: nodeDuration,
		})
	}
}

// emitStepResult emits step completion or failure event
func (g *Graph) emitStepResult(ctx context.Context, startTime time.Time, nodeID string, execErr error, nodeDuration time.Duration) {
	if execErr != nil {
		g.pluginBus.Emit(ctx, g.id, ares_runtime.EventStepFailed, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: g.id,
			ares_runtime.PayloadKeyStepID:      nodeID,
			ares_runtime.PayloadKeyStatus:      ares_runtime.StepStatusFailed,
			ares_runtime.PayloadKeyError:       execErr.Error(),
			ares_runtime.PayloadKeyDuration:    nodeDuration.Milliseconds(),
		})
	} else {
		g.pluginBus.Emit(ctx, g.id, ares_runtime.EventStepCompleted, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: g.id,
			ares_runtime.PayloadKeyStepID:      nodeID,
			ares_runtime.PayloadKeyStatus:      ares_runtime.StepStatusCompleted,
			ares_runtime.PayloadKeyDuration:    nodeDuration.Milliseconds(),
		})
	}
}

// handleNodeError handles node execution error
func (g *Graph) handleNodeError(ctx context.Context, startTime time.Time, nodeID string, execErr error, nodeDuration time.Duration) error {
	if execErr == nil {
		return nil
	}

	if g.pluginBus != nil {
		g.pluginBus.Emit(ctx, g.id, ares_runtime.EventWorkflowFailed, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: g.id,
			ares_runtime.PayloadKeyWorkflowID:  g.id,
			ares_runtime.PayloadKeyStatus:      ares_runtime.StepStatusFailed,
			ares_runtime.PayloadKeyError:       execErr.Error(),
			ares_runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
		})
	}
	return errors.Wrapf(execErr, "node %s execution failed", nodeID)
}

// handleDynamicRouting handles dynamic routing after node execution
func (g *Graph) handleDynamicRouting(ctx context.Context, state *State, nodeID string, readyQueue []string, readySet map[string]bool, executed map[string]bool) []string {
	routedID, routeReason, routeSource := g.getRoutedNode(ctx, nodeID, state)

	if routedID != "" {
		if g.collector != nil {
			g.collector.RecordRoute(nodeID, routedID, routeReason, routeSource)
		}
		if _, ok := g.nodes[routedID]; ok && !executed[routedID] && !readySet[routedID] {
			readyQueue = append(readyQueue, routedID)
			readySet[routedID] = true
		}
	}
	return readyQueue
}

// getRoutedNode gets the next routed node from router or plugin bus
func (g *Graph) getRoutedNode(ctx context.Context, nodeID string, state *State) (string, string, string) {
	if g.router != nil {
		routedID := g.router(ctx, nodeID, state)
		if routedID != "" {
			return routedID, "dynamic routing", "node-router"
		}
	}

	if g.pluginBus != nil {
		return routeFromPluginBusExt(ctx, g.pluginBus, g.collector, nodeID, state)
	}

	return "", "", ""
}

// processSuccessors processes successor nodes after execution
func (g *Graph) processSuccessors(state *State, nodeID string, inDegree map[string]int, readyQueue []string, readySet map[string]bool, executed map[string]bool) []string {
	for _, edge := range g.edges[nodeID] {
		inDegree[edge.to]--
		if inDegree[edge.to] == 0 && !executed[edge.to] && !readySet[edge.to] {
			if hasAnySatisfiedEdge(g, edge.to, state) {
				readyQueue = append(readyQueue, edge.to)
				readySet[edge.to] = true
			}
		}
	}
	return readyQueue
}

// shouldContinueLoop checks if loop should continue
func (g *Graph) shouldContinueLoop(ctx context.Context, state *State, iteration int) bool {
	if g.pluginBus == nil {
		return false
	}

	loopPlugins := g.pluginBus.PluginsByCap(ares_runtime.CapLoop)
	if len(loopPlugins) == 0 {
		return false
	}

	loop, ok := loopPlugins[0].(*ares_runtime.LoopPlugin)
	if !ok {
		return false
	}

	cfg := loop.Config()
	if cfg.MaxIterations > 0 && iteration >= cfg.MaxIterations {
		log.Debug("graph: loop max iterations reached",
			"graph_id", g.id, "iteration", iteration, "max", cfg.MaxIterations)
		return false
	}

	if cfg.UntilCondition != nil && cfg.UntilCondition(state.ToParams()) {
		log.Debug("graph: loop until condition met",
			"graph_id", g.id, "iteration", iteration)
		return false
	}

	log.Debug("graph: loop iteration completed, continuing",
		"graph_id", g.id, "iteration", iteration)
	return true
}

// finalizeExecution emits workflow completion event and records trace
func (g *Graph) finalizeExecution(ctx context.Context, state *State, startTime time.Time) {
	if g.pluginBus != nil {
		g.pluginBus.Emit(ctx, g.id, ares_runtime.EventWorkflowCompleted, "workflow", map[string]any{
			ares_runtime.PayloadKeyExecutionID: g.id,
			ares_runtime.PayloadKeyWorkflowID:  g.id,
			ares_runtime.PayloadKeyStatus:      ares_runtime.StepStatusCompleted,
			ares_runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
		})
	}

	if g.tracer != nil {
		g.tracer.RecordToolCall(ctx, &ares_observability.ToolCall{
			TraceID:  g.tracer.GetTraceID(ctx),
			ToolName: g.id,
			Input:    state.ToParams(),
			Output:   state.ToParams(),
			Duration: time.Since(startTime),
			Error:    nil,
		})
	}
}

// buildResult builds the final execution result
func (g *Graph) buildResult(state *State, startTime time.Time) *Result {
	return &Result{
		GraphID:  g.id,
		State:    state,
		Duration: time.Since(startTime),
	}
}

// routeFromPluginBusExt returns the routed node ID, reason, and source from
// the plugin bus router. Returns ("", "", "") if no router is available.
// in addition to the route ID. If collector is non-nil, it populates the
// RouteState with collected data and sets the collector for router recording.
func routeFromPluginBusExt(ctx context.Context, bus *ares_runtime.PluginBus, collector *ares_runtime.ExecutionCollector, nodeID string, state *State) (string, string, string) {
	routers := bus.PluginsByCap(ares_runtime.CapRouter)
	if len(routers) == 0 {
		return "", "", ""
	}
	router, ok := routers[0].(ares_runtime.RouterPlugin)
	if !ok || router == nil {
		return "", "", ""
	}
	routeState := ares_runtime.RouteState{
		CurrentStepID: nodeID,
	}
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

// hasAnySatisfiedEdge checks if node targetID has at least one incoming edge
// whose condition is satisfied (or has no condition). This is used when
// inDegree reaches 0 to determine if the node should be enqueued: a node
// with only unsatisfied conditional edges is considered unreachable and is
// skipped rather than silently lost.
func hasAnySatisfiedEdge(g *Graph, targetID string, state *State) bool {
	for _, edges := range g.edges {
		for _, edge := range edges {
			if edge.to == targetID {
				if edge.cond == nil || edge.cond(state) {
					return true
				}
			}
		}
	}
	// No incoming edges at all, or all conditions are false.
	return false
}
