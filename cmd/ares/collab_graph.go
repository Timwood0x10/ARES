package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// Collaboration graphs (fusion plan Phase C) execute as KERNEL fabric tasks:
// every node is a durable task whose Dependencies express the edges, and the
// kernelscheduler drives them through the standard Schedule→Acquire→RunQuantum
// path. This is deliberately NOT a second engine — it is the same engine
// sdk.Graph compiles to, used directly because these fixed collaboration
// shapes (delegate = one node, pipeline = chain, orchestrate = fan-out with
// implicit join-by-dependencies) need no conditions or routing.

// graphNodeSpec is one executable vertex of a collaboration graph.
type graphNodeSpec struct {
	// ID is the caller-chosen node identifier (unique within the graph).
	ID string `json:"id"`
	// Capability selects which peer executor runs this node.
	Capability string `json:"capability"`
	// Input becomes the node task's payload["input"].
	Input any `json:"input,omitempty"`
}

// graphEdgeSpec is a directed dependency: to runs only after from completes.
type graphEdgeSpec struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// collabTimeout bounds one graph execution when the caller's ctx has no
// deadline (LLM-backed peers make an unbounded wait dangerous).
const collabTimeout = 10 * time.Minute

// runCollabGraph creates every node task in the kernel fabric and waits for
// the whole graph to settle. It returns nodeID → textual output extracted
// from each task's completion checkpoint.
//
// Failure semantics: the first FAILED node aborts the wait and is returned as
// an error naming the node; sibling branches that already completed are still
// reported in the outputs map (partial results survive).
func runCollabGraph(ctx context.Context, k *kernelHandle, runID string, nodes []graphNodeSpec, edges []graphEdgeSpec) (map[string]string, error) {
	if k == nil || k.fabric == nil {
		return nil, fmt.Errorf("collab graph: kernel fabric not wired")
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ID == "" || n.Capability == "" {
			return nil, fmt.Errorf("collab graph %s: node requires id and capability", runID)
		}
		if ids[n.ID] {
			return nil, fmt.Errorf("collab graph %s: duplicate node id %q", runID, n.ID)
		}
		ids[n.ID] = true
	}
	deps := make(map[string][]string, len(nodes))
	for _, e := range edges {
		if !ids[e.From] || !ids[e.To] {
			return nil, fmt.Errorf("collab graph %s: edge %q→%q references an unknown node", runID, e.From, e.To)
		}
		deps[e.To] = append(deps[e.To], e.From)
	}
	if cyc := findCycle(ids, deps); cyc != "" {
		return nil, fmt.Errorf("collab graph %s: dependency cycle involving %q", runID, cyc)
	}

	taskIDs := make(map[string]string, len(nodes)) // nodeID → taskID
	for _, n := range nodes {
		tid := "collab-" + runID + "-" + n.ID
		taskIDs[n.ID] = tid
		// Dependencies are expressed in NODE ids in the submission wire
		// format but must reference real TASK ids in the fabric.
		nodeDeps := make([]string, 0, len(deps[n.ID]))
		for _, d := range deps[n.ID] {
			nodeDeps = append(nodeDeps, "collab-"+runID+"-"+d)
		}
		if err := k.fabric.Create(&taskfabric.Task{
			ID:           tid,
			Capability:   n.Capability,
			Dependencies: nodeDeps,
			RetryPolicy:  taskfabric.RetryPolicy{MaxRetries: 2},
			Checkpoint: &taskfabric.CheckpointEnvelope{
				Payload: map[string]any{"input": n.Input},
			},
		}); err != nil {
			return nil, fmt.Errorf("collab graph %s: create node %q: %w", runID, n.ID, err)
		}
	}
	log.Printf("peer mode: collaboration graph %s submitted (%d nodes)", runID, len(nodes))

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, collabTimeout)
		defer cancel()
	}

	outputs := make(map[string]string, len(nodes))
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	settled := 0
	for settled < len(nodes) {
		select {
		case <-waitCtx.Done():
			return outputs, fmt.Errorf("collab graph %s: timed out with %d/%d nodes settled",
				runID, settled, len(nodes))
		case <-ticker.C:
			for _, n := range nodes {
				if _, done := outputs[n.ID]; done {
					continue
				}
				tk, err := k.fabric.Task(taskIDs[n.ID])
				if err != nil {
					return outputs, fmt.Errorf("collab graph %s: read node %q: %w", runID, n.ID, err)
				}
				switch tk.State {
				case taskfabric.StateCompleted:
					outputs[n.ID] = collabNodeOutput(tk)
					settled++
				case taskfabric.StateFailed:
					outputs[n.ID] = collabNodeOutput(tk)
					return outputs, fmt.Errorf("collab graph %s: node %q failed", runID, n.ID)
				}
			}
		}
	}
	return outputs, nil
}

// findCycle runs Kahn's topological sort over the dependency graph; any node
// left unresolved belongs to a cycle (or depends on one). Kernel-fabric
// dependencies are purely completion-driven, so an undetected cycle would
// park every member at READY until the caller times out — rejection at
// submission turns that runtime hang into a precise 400.
func findCycle(ids map[string]bool, deps map[string][]string) string {
	indegree := make(map[string]int, len(ids))
	adj := make(map[string][]string, len(ids))
	for id := range ids {
		indegree[id] = 0
	}
	for to, froms := range deps {
		indegree[to] += len(froms)
		for _, from := range froms {
			adj[from] = append(adj[from], to)
		}
	}
	queue := make([]string, 0, len(ids))
	for id, d := range indegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}
	resolved := 0
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		resolved++
		for _, nxt := range adj[id] {
			indegree[nxt]--
			if indegree[nxt] == 0 {
				queue = append(queue, nxt)
			}
		}
	}
	if resolved == len(ids) {
		return ""
	}
	for id, d := range indegree {
		if d > 0 {
			return id
		}
	}
	return ""
}

// collabNodeOutput extracts the executor's textual result from the completion
// checkpoint (the same envelope the dispatcher reads for reflux).
//
// API contract note: outputs carry ONLY the Reason summary text. Executors
// that place structured payloads under other checkpoint keys expose them via
// the task itself (query fabric.Task(id) + DecodeCheckpoint), not through
// this map — keep callers' expectations aligned with that boundary.
func collabNodeOutput(tk *taskfabric.Task) string {
	dc, err := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if err != nil {
		return ""
	}
	step, ok := dc.StepCheckpoint.(map[string]any)
	if !ok {
		return ""
	}
	if reason, _ := step["reason"].(string); reason != "" {
		return reason
	}
	return ""
}
