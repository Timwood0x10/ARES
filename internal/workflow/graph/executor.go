// package graph - provides dynamic agent orchestration with pluggable scheduling.

package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/observability"
)

// Execute runs the graph with the given state.
func (g *Graph) Execute(ctx context.Context, state *State) (*Result, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}
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
	readySet := make(map[string]bool) // nodes ready for execution
	readyQueue := []string{g.start}   // ordered queue of ready nodes
	readySet[g.start] = true
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

		// Execute node
		err := node.Execute(ctx, state)
		if err != nil {
			return nil, errors.Wrapf(err, "node %s execution failed", nodeID)
		}

		// Mark as executed
		executed[nodeID] = true

		// Check edges and add next nodes to ready queue
		for _, edge := range g.edges[nodeID] {
			// Only add nodes that are not executed and not already ready
			if !executed[edge.to] && !readySet[edge.to] {
				// Check edge condition if present
				if edge.cond == nil || edge.cond(state) {
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
