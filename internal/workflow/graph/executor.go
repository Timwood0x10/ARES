// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/observability"
)

// Execute runs the graph with the given state.
//
// Execute acquires a read lock on the graph for the duration of execution,
// preventing concurrent mutations. Multiple Execute calls may run concurrently
// with each other but not with mutation methods.
func (g *Graph) Execute(ctx context.Context, state *State) (*Result, error) {
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
	executed := make(map[string]bool) // nodes that have been executed

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
	// Seed the ready queue with ALL nodes that have no predecessors.
	readyQueue := make([]string, 0)
	readySet := make(map[string]bool)
	for id, deg := range inDegree {
		if deg == 0 {
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
			return nil, errors.Wrap(ctx.Err(), "execution cancelled")
		default:
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
		err := node.Execute(ctx, state)
		if err != nil {
			if g.tracer != nil {
				g.tracer.RecordError(ctx, &observability.AgentError{
					TraceID:   g.tracer.GetTraceID(ctx),
					AgentID:   nodeID,
					ErrorType: "execution_error",
					Message:   err.Error(),
				})
			}
			return nil, errors.Wrapf(err, "node %s execution failed", nodeID)
		}

		// Record agent step completion
		if g.tracer != nil {
			g.tracer.RecordAgentStep(ctx, &observability.AgentStep{
				TraceID:  g.tracer.GetTraceID(ctx),
				AgentID:  nodeID,
				StepName: "execute",
				Duration: time.Since(nodeStart),
			})
		}

		// Mark as executed
		executed[nodeID] = true

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
