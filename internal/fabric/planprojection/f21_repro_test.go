package planprojection

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// F-21 regression tests (docs/reviews/0.3.1-final-deep-review.md §0.1):
// graph-event drops must ALWAYS be compensated. The two failure modes both
// leave session tail nodes unmaterialized as fabric tasks ("missing"
// forever):
//
//  1. the one-shot tail timer armed by delivery fired with no recorded
//     drops and was never re-armed (fixed: the tick is standing);
//  2. the drop counter was consumed by a reconcile that ran while the
//     publisher was still mid-burst — the partial graph had nothing to
//     create, and the counter never moved again once the burst finished
//     (fixed: the counter is paired with the DAG version on the tick path).
//
// The tests below drive burst patterns that straddle the 64-event hub buffer
// and assert the invariant both modes violate: after drops, every graph node
// eventually has a fabric task (compensation converges) within a generous
// window.

// f21AddNode appends a chain node (prev == "" means root).
func f21AddNode(ctx context.Context, dag *engine.MutableDAG, id, prev string) bool {
	step := &engine.Step{ID: id, AgentType: "echo"}
	if prev != "" {
		step.DependsOn = []string{prev}
	}
	return dag.AddNode(ctx, step) == nil
}

// f21WaitForAll asserts the compensation invariant: every id in ids must
// exist as a fabric task within window (plus a small margin for the
// reconcile tick), or the test fails naming the stragglers.
func f21WaitForAll(t *testing.T, fabric *taskfabric.Fabric, ids []string, window time.Duration) {
	t.Helper()
	deadline := time.Now().Add(window)
	var missing []string
	for time.Now().Before(deadline) {
		missing = missing[:0]
		for _, id := range ids {
			if _, err := fabric.Task(id); err != nil {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("compensation did not converge: %d/%d nodes never materialized as fabric tasks: %v",
		len(missing), len(ids), missing[:min(10, len(missing))])
}

// TestF21TailDropCompensationConverges grows a warm-up chain, idles past the
// reconcile tick, then publishes a burst far larger than the 64-slot buffer
// with gaps straddling the tick interval — drops land at different phases of
// the subscriber loop, mirroring the CI flake's scheduler-delay-split burst.
func TestF21TailDropCompensationConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := NewCompileCoordinator(fabric, nil)
	dag, err := engine.NewMutableDAG([]*engine.Step{{ID: "root", AgentType: "echo"}})
	require.NoError(t, err)

	subID, _ := dag.SubscribeWithID()
	defer dag.Unsubscribe(subID)
	stop := coord.SubscribeGraphEvents(ctx, dag)
	defer stop()

	prev := "root"
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("warm%d", i)
		require.True(t, f21AddNode(ctx, dag, id, prev), "warm-up add")
		prev = id
	}
	require.Eventually(t, func() bool {
		_, err := fabric.Task("warm2")
		return err == nil
	}, 5*time.Second, 5*time.Millisecond, "warm-up nodes must materialize")
	// Idle past one tick so the standing timer has fired at least once with
	// nothing to do before the burst starts.
	time.Sleep(400 * time.Millisecond)

	const seg1, seg2, seg3 = 63, 64, 48
	var ids []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		p := "warm2"
		for _, seg := range [][2]int{{0, seg1}, {0, seg2}, {0, seg3}} {
			for i := seg[0]; i < seg[1]; i++ {
				id := fmt.Sprintf("s%d_%d", seg[1], i)
				if !f21AddNode(ctx, dag, id, p) {
					return
				}
				ids = append(ids, id)
				p = id
			}
			// The gap lets the subscriber drain and the tick fire between
			// segments, splitting the burst across timer boundaries.
			time.Sleep(600 * time.Millisecond)
		}
	}()
	<-done

	f21WaitForAll(t, fabric, ids, 10*time.Second)
}

// TestF21ContinuousBurstCompensationConverges publishes one continuous flood
// (no gaps) so the subscriber falls behind, the buffer fills, and the tail
// drops mid-burst — exercising the counter-consumed-by-partial-reconcile
// failure mode: the reconcile triggered mid-burst sees a partial graph, and
// only the version-paired tick can catch the completion afterwards.
func TestF21ContinuousBurstCompensationConverges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := NewCompileCoordinator(fabric, nil)
	dag, err := engine.NewMutableDAG([]*engine.Step{{ID: "root", AgentType: "echo"}})
	require.NoError(t, err)

	subID, _ := dag.SubscribeWithID()
	defer dag.Unsubscribe(subID)
	stop := coord.SubscribeGraphEvents(ctx, dag)
	defer stop()

	const flood, tail = 300, 80
	var ids []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		p := "root"
		for i := 0; i < flood+tail; i++ {
			prefix := "f"
			if i >= flood {
				prefix = "t"
			}
			id := fmt.Sprintf("%s_%d", prefix, i)
			if !f21AddNode(ctx, dag, id, p) {
				return
			}
			ids = append(ids, id)
			p = id
		}
	}()
	<-done

	f21WaitForAll(t, fabric, ids, 10*time.Second)
}
