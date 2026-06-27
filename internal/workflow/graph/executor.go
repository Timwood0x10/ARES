// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/runtime"
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
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
	if state == nil {
		return nil, fmt.Errorf("state cannot be nil")
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.start == "" {
		return nil, fmt.Errorf("graph start node is not set")
	}
	if _, ok := g.nodes[g.start]; !ok {
		return nil, fmt.Errorf("start node %s not found", g.start)
	}

	// Apply rate limiting if configured
	if g.limiter != nil {
		if err := g.limiter.Wait(ctx); err != nil {
			return nil, errors.Wrap(err, "rate limit")
		}
	}

	// Initialize execution
	startTime := time.Now()
	iteration := 0
	loopIterKey := "__loop_iteration"

	// Outer loop supports LoopPlugin: after each full graph execution,
	// check if the loop should continue and re-execute from the start.
	for {
		iteration++
		if iteration > 1 {
			state.Set(loopIterKey, iteration)
		}

		if g.pluginBus != nil {
			payload := map[string]any{
				runtime.PayloadKeyExecutionID: g.id,
				runtime.PayloadKeyWorkflowID:  g.id,
			}
			if iteration == 1 && len(initialExecuted) > 0 {
				payload["resumed"] = true
			}
			g.pluginBus.Emit(ctx, g.id, runtime.EventWorkflowStarted, payload)
		}

		executed := make(map[string]bool)
		if iteration == 1 {
			for k, v := range initialExecuted {
				executed[k] = v
			}
		}

		// Build in-degree map so nodes with multiple predecessors
		// are only added to the ready queue when ALL predecessors have completed.
		inDegree := make(map[string]int, len(g.nodes))
		for id := range g.nodes {
			inDegree[id] = 0
		}
		for _, edges := range g.edges {
			for _, edge := range edges {
				inDegree[edge.to]++
			}
		}
		// For nodes that were pre-executed, decrement their successors'
		// in-degree so the graph can continue from the right place.
		if iteration == 1 {
			for id := range initialExecuted {
				for _, edge := range g.edges[id] {
					inDegree[edge.to]--
				}
			}
		}
		// Seed the ready queue with nodes that have no predecessors
		// and are not pre-executed.
		readyQueue := make([]string, 0)
		readySet := make(map[string]bool)
		for id, deg := range inDegree {
			if deg == 0 && !executed[id] {
				readyQueue = append(readyQueue, id)
				readySet[id] = true
			}
		}
		// Execute graph using BFS with scheduler
		for len(readyQueue) > 0 {
			// Select next node using scheduler
			nodeID := g.scheduler.Select(readyQueue)
			if nodeID == "" {
				break // no more nodes to execute
			}

			// Remove node from ready queue and set
			for i, id := range readyQueue {
				if id == nodeID {
					readyQueue = append(readyQueue[:i], readyQueue[i+1:]...)
					break
				}
			}
			delete(readySet, nodeID)

			// Skip if already executed
			if executed[nodeID] {
				continue
			}

			// Get and validate node
			node, ok := g.nodes[nodeID]
			if !ok {
				return nil, fmt.Errorf("node %s not found", nodeID)
			}

			// Check if context is cancelled
			select {
			case <-ctx.Done():
				if g.pluginBus != nil {
					g.pluginBus.Emit(ctx, g.id, runtime.EventWorkflowFailed, map[string]any{
						runtime.PayloadKeyExecutionID: g.id,
						runtime.PayloadKeyWorkflowID:  g.id,
						runtime.PayloadKeyStatus:      runtime.StepStatusFailed,
						runtime.PayloadKeyError:       ctx.Err().Error(),
						runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
					})
				}
				return nil, errors.Wrap(ctx.Err(), "execution cancelled")
			default:
			}

			// Convert node to runtime.Step for plugin hooks.
			step := &runtime.Step{ID: nodeID, Name: nodeID, StartedAt: time.Now()}
			if g.pluginBus != nil {
				if err := g.pluginBus.BeforeStep(ctx, g.id, step); err != nil {
					slog.Warn("graph: before step hook failed (continuing)",
						"graph_id", g.id, "node", nodeID, "error", err,
					)
				}
				g.pluginBus.Emit(ctx, g.id, runtime.EventStepStarted, map[string]any{
					runtime.PayloadKeyExecutionID: g.id,
					runtime.PayloadKeyStepID:      nodeID,
				})
			}

			// Record agent step start
			if g.tracer != nil {
				g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
					TraceID:  g.tracer.GetTraceID(ctx),
					AgentID:  nodeID,
					StepName: "execute",
				})
			}

			// Execute node
			nodeStart := time.Now()
			execErr := node.Execute(ctx, state)
			nodeDuration := time.Since(nodeStart)

			stepResult := &runtime.StepResult{
				StepID:   nodeID,
				Status:   runtime.StepStatusCompleted,
				Duration: nodeDuration,
			}
			if execErr != nil {
				stepResult.Status = runtime.StepStatusFailed
				stepResult.Error = execErr.Error()
				if g.tracer != nil {
					g.tracer.RecordError(ctx, &observability.AgentError{
						TraceID:   g.tracer.GetTraceID(ctx),
						AgentID:   nodeID,
						ErrorType: "execution_error",
						Message:   execErr.Error(),
					})
				}
			}

			if g.pluginBus != nil {
				if err := g.pluginBus.AfterStep(ctx, g.id, stepResult); err != nil {
					slog.Warn("graph: after step hook failed (continuing)",
						"graph_id", g.id, "node", nodeID, "error", err,
					)
				}
				if execErr != nil {
					g.pluginBus.Emit(ctx, g.id, runtime.EventStepFailed, map[string]any{
						runtime.PayloadKeyExecutionID: g.id,
						runtime.PayloadKeyStepID:      nodeID,
						runtime.PayloadKeyStatus:      stepResult.Status,
						runtime.PayloadKeyError:       execErr.Error(),
						runtime.PayloadKeyDuration:    nodeDuration.Milliseconds(),
					})
				} else {
					g.pluginBus.Emit(ctx, g.id, runtime.EventStepCompleted, map[string]any{
						runtime.PayloadKeyExecutionID: g.id,
						runtime.PayloadKeyStepID:      nodeID,
						runtime.PayloadKeyStatus:      stepResult.Status,
						runtime.PayloadKeyDuration:    nodeDuration.Milliseconds(),
					})
				}
			}

			if execErr != nil {
				if g.pluginBus != nil {
					g.pluginBus.Emit(ctx, g.id, runtime.EventWorkflowFailed, map[string]any{
						runtime.PayloadKeyExecutionID: g.id,
						runtime.PayloadKeyWorkflowID:  g.id,
						runtime.PayloadKeyStatus:      runtime.StepStatusFailed,
						runtime.PayloadKeyError:       execErr.Error(),
						runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
					})
				}
				return nil, errors.Wrapf(execErr, "node %s execution failed", nodeID)
			}

			// Record agent step completion
			if g.tracer != nil {
				g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
					TraceID:  g.tracer.GetTraceID(ctx),
					AgentID:  nodeID,
					StepName: "execute",
					Duration: nodeDuration,
				})
			}

			// Mark as executed
			executed[nodeID] = true

			// Dynamic routing: after successful completion, check if router
			// wants to override the next node (overrides static edge traversal).
			// Priority: explicit NodeRouter > RouterPlugin from PluginBus.
			var routedID string
			var routeReason, routeSource string
			if execErr == nil {
				if g.router != nil {
					routedID = g.router(ctx, nodeID, state)
					if routedID != "" {
						routeReason = "dynamic routing"
						routeSource = "node-router"
					}
				} else if g.pluginBus != nil {
					routedID, routeReason, routeSource = routeFromPluginBusExt(ctx, g.pluginBus, g.collector, nodeID, state)
				}
			}
			if routedID != "" {
				if g.collector != nil {
					g.collector.RecordRoute(nodeID, routedID, routeReason, routeSource)
				}
				if _, ok := g.nodes[routedID]; ok && !executed[routedID] && !readySet[routedID] {
					readyQueue = append(readyQueue, routedID)
					readySet[routedID] = true
				}
			}

			// C7 fix: decrement in-degree for successor nodes.
			// Decrement unconditionally (structural dependency satisfied),
			// but only enqueue when inDegree reaches 0 AND at least one
			// incoming edge has a satisfied condition. This prevents:
			//   - Silent node loss: a node with multiple predecessors where
			//     some conditional edges are false still gets enqueued as
			//     long as ONE edge condition is satisfied.
			//   - Ghost execution: a node whose ALL conditional edges are
			//     false is correctly skipped.
			for _, edge := range g.edges[nodeID] {
				inDegree[edge.to]--
				if inDegree[edge.to] == 0 && !executed[edge.to] && !readySet[edge.to] {
					if hasAnySatisfiedEdge(g, edge.to, state) {
						readyQueue = append(readyQueue, edge.to)
						readySet[edge.to] = true
					}
				}
			}
		}

		// Check LoopPlugin: after a full graph execution, check if the loop
		// should continue. Uses the loop config directly rather than the
		// LoopPlugin's internal per-step counter (which doesn't map to
		// graph-level iterations). If no LoopPlugin is configured, break.
		if g.pluginBus != nil {
			loopPlugins := g.pluginBus.PluginsByCap(runtime.CapLoop)
			if len(loopPlugins) > 0 {
				if loop, ok := loopPlugins[0].(*runtime.LoopPlugin); ok {
					cfg := loop.Config()
					if cfg.MaxIterations > 0 && iteration >= cfg.MaxIterations {
						slog.Debug("graph: loop max iterations reached",
							"graph_id", g.id, "iteration", iteration, "max", cfg.MaxIterations,
						)
					} else if cfg.UntilCondition != nil && cfg.UntilCondition(state.ToParams()) {
						slog.Debug("graph: loop until condition met",
							"graph_id", g.id, "iteration", iteration,
						)
					} else {
						slog.Debug("graph: loop iteration completed, continuing",
							"graph_id", g.id, "iteration", iteration,
						)
						continue
					}
				}
			}
		}

		break
	}

	if g.pluginBus != nil {
		g.pluginBus.Emit(ctx, g.id, runtime.EventWorkflowCompleted, map[string]any{
			runtime.PayloadKeyExecutionID: g.id,
			runtime.PayloadKeyWorkflowID:  g.id,
			runtime.PayloadKeyStatus:      runtime.StepStatusCompleted,
			runtime.PayloadKeyDuration:    time.Since(startTime).Milliseconds(),
		})
	}

	// Record execution trace
	if g.tracer != nil {
		g.tracer.RecordToolCall(ctx, &observability.ToolCall{
			TraceID:  g.tracer.GetTraceID(ctx),
			ToolName: g.id,
			Input:    state.ToParams(),
			Output:   state.ToParams(),
			Duration: time.Since(startTime),
			Error:    nil,
		})
	}

	return &Result{GraphID: g.id,
		State:    state,
		Duration: time.Since(startTime),
	}, nil
}

// routeFromPluginBusExt returns the routed node ID, reason, and source from
// the plugin bus router. Returns ("", "", "") if no router is available.
// in addition to the route ID. If collector is non-nil, it populates the
// RouteState with collected data and sets the collector for router recording.
func routeFromPluginBusExt(ctx context.Context, bus *runtime.PluginBus, collector *runtime.ExecutionCollector, nodeID string, state *State) (string, string, string) {
	routers := bus.PluginsByCap(runtime.CapRouter)
	if len(routers) == 0 {
		return "", "", ""
	}
	router, ok := routers[0].(runtime.RouterPlugin)
	if !ok || router == nil {
		return "", "", ""
	}
	routeState := runtime.RouteState{
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
