// Package planprojection provides the single projection function from
// workflow engine steps to taskfabric PlanSteps, plus a compile
// coordinator that records compile provenance for introspection.
//
// The projection lives in its own package (not in taskfabric) so the
// kernel never imports the planner package — the caller (cmd layer)
// projects engine.Step onto PlanStep, then hands the batch to
// Fabric.CompilePlan.
package planprojection

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/taskfabric"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// CompileCoordinator manages the projection → CompilePlan pipeline. It
// holds a reference to the task fabric and the event store, records
// compile provenance, and supports event-driven recompilation from
// MutableDAG GraphEvents.
type CompileCoordinator struct {
	fabric     *taskfabric.Fabric
	store      ares_events.EventStore
	generation int // tracks the current evolution generation

	// lastCompile is the most recent compile record (for introspection).
	mu          sync.RWMutex
	lastCompile CompileRecord

	// compileSeq generates unique compile IDs.
	compileSeq uint64
}

// NewCompileCoordinator creates a coordinator wired to the given fabric
// and event store. Either may be nil for testing (the methods are
// nil-safe and degrade gracefully).
func NewCompileCoordinator(fabric *taskfabric.Fabric, store ares_events.EventStore) *CompileCoordinator {
	return &CompileCoordinator{
		fabric: fabric,
		store:  store,
	}
}

// SetGeneration sets the current evolution generation. Called by the
// GA lifecycle when a new generation starts.
func (c *CompileCoordinator) SetGeneration(gen int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation = gen
}

// CompileDAG projects the DAG's steps into PlanSteps and calls
// Fabric.CompilePlan. It records a compile event with the generation,
// DAG version, compile ID, and plan IDs for introspection.
//
// This is the SINGLE entry point for DAG → task compilation — both the
// startup path (after UpdateLiveDAG) and the runtime event-driven path
// (GraphEvent subscription) call this method.
func (c *CompileCoordinator) CompileDAG(ctx context.Context, dag *engine.MutableDAG) (CompileRecord, error) {
	if dag == nil {
		return CompileRecord{}, fmt.Errorf("planprojection: compile DAG: nil dag")
	}
	if c == nil || c.fabric == nil {
		return CompileRecord{}, fmt.Errorf("planprojection: compile DAG: nil fabric")
	}

	dagVersion := dag.Version()
	steps := dag.Steps()
	planSteps := ProjectSteps(steps)

	compileID := c.nextCompileID()

	// Clean up tasks from the previous compile so recompilation (triggered
	// by a GraphEvent mutation) does not hit ErrTaskExists. We delete the
	// previous compile's plan IDs before creating the new batch. This is
	// best-effort: a task that has already been acquired by a scheduler
	// cannot be deleted (it is owned), so we skip it rather than failing
	// the whole recompile — the stale task will drain naturally.
	c.mu.RLock()
	oldIDs := c.lastCompile.PlanIDs
	c.mu.RUnlock()
	for _, id := range oldIDs {
		_ = c.fabric.Delete(id) // best-effort; acquired tasks survive
	}

	planIDs, err := c.fabric.CompilePlan(ctx, planSteps)
	record := CompileRecord{
		Generation: c.generation,
		DAGVersion: dagVersion,
		CompileID:  compileID,
		PlanIDs:    planIDs,
		StepCount:  len(planSteps),
	}

	if err != nil {
		return record, fmt.Errorf("planprojection: compile DAG: %w", err)
	}

	c.mu.Lock()
	c.lastCompile = record
	c.mu.Unlock()

	c.recordCompileEvent(ctx, record)

	return record, nil
}

// LastCompile returns the most recent compile record. Safe for concurrent
// access; returns a zero value if no compile has happened yet.
func (c *CompileCoordinator) LastCompile() CompileRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastCompile
}

// CompileCount returns the total number of CompilePlan calls since startup
// (C5.1/C5.3). This is the compileSeq counter — the same source that
// generates compile IDs — exposed for introspection and metrics. A flat
// zero means no recompile has fired, which indicates the GraphEvent
// subscription is not wired or the DAG has not been mutated.
func (c *CompileCoordinator) CompileCount() uint64 {
	return atomic.LoadUint64(&c.compileSeq)
}

// CompileID returns the most recent compile's unique identifier (C5.2).
// Empty when no compile has happened yet.
func (c *CompileCoordinator) CompileID() string {
	return c.LastCompile().CompileID
}

// DAGVersion returns the live DAG's mutation counter at the last compile
// (C5.2). Zero when no compile has happened yet.
func (c *CompileCoordinator) DAGVersion() uint64 {
	return c.LastCompile().DAGVersion
}

// SubscribeGraphEvents subscribes to GraphEvents from the MutableDAG and
// triggers recompilation on every mutation. The subscription is managed:
// it is cleaned up when ctx is cancelled. The returned function can be
// called to unsubscribe early (e.g. during shutdown).
//
// This closes the "two graphs" gap: a GraphPatchExecutor mutation on the
// live MutableDAG triggers a recompile so the next scheduler drain sees the
// updated task set.
func (c *CompileCoordinator) SubscribeGraphEvents(ctx context.Context, dag *engine.MutableDAG) func() {
	if dag == nil {
		return func() {}
	}
	subID, ch := dag.SubscribeWithID()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				dag.Unsubscribe(subID)
				return
			case evt, ok := <-ch:
				if !ok {
					return
				}
				if evt.Success {
					compileCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
					_, _ = c.CompileDAG(compileCtx, dag)
					cancel()
				}
			}
		}
	}()

	return func() {
		dag.Unsubscribe(subID)
		<-done
	}
}

// nextCompileID generates a unique compile identifier.
func (c *CompileCoordinator) nextCompileID() string {
	n := atomic.AddUint64(&c.compileSeq, 1)
	return fmt.Sprintf("compile-%d", n)
}

// recordCompileEvent writes a compile lifecycle event to the event store
// for introspection. Best-effort: errors are not surfaced to the caller.
func (c *CompileCoordinator) recordCompileEvent(ctx context.Context, record CompileRecord) {
	if c.store == nil {
		return
	}
	evt := &ares_events.Event{
		ID:       fmt.Sprintf("compile-%s", record.CompileID),
		StreamID: "evolution.compile",
		Type:     ares_events.EventType("evolution.compile"),
		Payload: map[string]any{
			"generation":  record.Generation,
			"dag_version": record.DAGVersion,
			"compile_id":  record.CompileID,
			"plan_ids":    record.PlanIDs,
			"step_count":  record.StepCount,
		},
		Timestamp: time.Now(),
	}
	_ = c.store.Append(ctx, "evolution.compile", []*ares_events.Event{evt}, -1)
}
