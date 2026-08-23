package main

import (
	"context"
	"errors"
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

// ErrGraphInvalid marks SUBMISSION-TIME validation failures (malformed
// nodes/edges, unknown references, dependency cycles) that map to HTTP 4xx.
// Execution failures after task creation keep 500 — they are runtime faults,
// not caller mistakes.
var ErrGraphInvalid = errors.New("collab graph invalid")

// runCollabGraph creates every node task in the kernel fabric and waits for
// the whole graph to settle. It returns nodeID → textual output extracted
// from each task's completion checkpoint.
//
// Failure semantics: the first FAILED node aborts the wait and is returned as
// an error naming the node; sibling branches that already completed are still
// reported in the outputs map (partial results survive).
func runCollabGraph(ctx context.Context, k *kernelHandle, runID string, nodes []graphNodeSpec, edges []graphEdgeSpec) (outputs map[string]string, taskIDs map[string]string, err error) {
	if k == nil || k.fabric == nil {
		return nil, nil, fmt.Errorf("collab graph: kernel fabric not wired")
	}
	ids := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ID == "" || n.Capability == "" {
			return nil, nil, fmt.Errorf("%w: node requires id and capability", ErrGraphInvalid)
		}
		if ids[n.ID] {
			return nil, nil, fmt.Errorf("%w: duplicate node id %q", ErrGraphInvalid, n.ID)
		}
		ids[n.ID] = true
	}
	deps := make(map[string][]string, len(nodes))
	for _, e := range edges {
		if !ids[e.From] || !ids[e.To] {
			return nil, nil, fmt.Errorf("%w: edge %q→%q references an unknown node", ErrGraphInvalid, e.From, e.To)
		}
		deps[e.To] = append(deps[e.To], e.From)
	}
	if cyc := findCycle(ids, deps); cyc != "" {
		return nil, nil, fmt.Errorf("%w: dependency cycle involving %q", ErrGraphInvalid, cyc)
	}

	taskIDs = make(map[string]string, len(nodes)) // nodeID → taskID
	created := make([]string, 0, len(nodes))      // every task we Create, for cleanup
	defer func() {
		// Ephemeral lifecycle (C4 review #2): submitted graphs must not leave
		// zombie entries in the long-lived fabric. Delete is best-effort —
		// in-flight (LEASED/RUNNING/SUSPENDED) tasks are refused by the guard
		// and finish naturally; their ids are unique so nothing collides.
		for _, tid := range created {
			if derr := k.fabric.Delete(tid); derr != nil && derr != taskfabric.ErrTaskNotFound {
				log.Printf("peer mode: cleanup %s: %v", tid, derr)
			}
		}
	}()
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
			return nil, taskIDs, fmt.Errorf("collab graph %s: create node %q: %w", runID, n.ID, err)
		}
		created = append(created, tid)
	}
	log.Printf("peer mode: collaboration graph %s submitted (%d nodes)", runID, len(nodes))

	waitCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, collabTimeout)
		defer cancel()
	}

	outputs = make(map[string]string, len(nodes))
	pending := make([]string, 0, len(nodes))
	for _, n := range nodes {
		pending = append(pending, n.ID)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for len(pending) > 0 {
		progressed := false
		var failErr error
		still := pending[:0]
		for _, nid := range pending {
			tk, err := k.fabric.Task(taskIDs[nid])
			if err != nil {
				return outputs, taskIDs, fmt.Errorf("collab graph %s: read node %q: %w", runID, nid, err)
			}
			switch tk.State {
			case taskfabric.StateCompleted:
				outputs[nid] = collabNodeOutput(tk)
				progressed = true
			case taskfabric.StateFailed:
				outputs[nid] = collabNodeOutput(tk)
				failErr = fmt.Errorf("collab graph %s: node %q failed", runID, nid)
			default:
				still = append(still, nid)
			}
		}
		pending = still
		if failErr != nil {
			// Fail-fast (#4): the deferred cleanup deletes every not-yet-
			// started READY sibling so it never runs. Quanta already RUNNING
			// finish naturally (cooperative model — no hard cancel exists);
			// their results go unread and their unique ids keep the fabric
			// collision-free.
			return outputs, taskIDs, failErr
		}
		if progressed {
			continue // re-scan immediately; more may have settled this instant
		}
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.Canceled) {
				return outputs, taskIDs, fmt.Errorf("collab graph %s: canceled: %w (%d/%d settled)",
					runID, waitCtx.Err(), len(outputs), len(nodes))
			}
			return outputs, taskIDs, fmt.Errorf("collab graph %s: timed out with %d/%d nodes settled",
				runID, len(outputs), len(nodes))
		case <-ticker.C:
		}
	}
	return outputs, taskIDs, nil
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
