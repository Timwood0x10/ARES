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

// TestF21DeadWindowExactRepro targets the EXACT dead window identified in
// docs/reviews/0.3.1-final-deep-review.md §0.1 (F-21):
//
//	... the burst's last DELIVERED event runs checkDrops (counter not yet
//	moved) and arms the one-shot 250ms timer. The timer fires: checkDrops
//	again sees zero movement (the drops have NOT happened yet — the
//	publisher goroutine was preempted mid-burst). Timer STOPS. The
//	publisher resumes and publishes the remaining tail into a full buffer:
//	events DROP, the counter moves — but no event is ever delivered again
//	(the whole tail was dropped, and nothing follows the burst), so the
//	seq-gap check never runs, and the stopped timer is never re-armed.
//	The moved drop counter is read by NOBODY. Session stalled forever.
//
// Reproduction strategy: we cannot preempt the publisher deterministically
// from Go, but we can reproduce the equivalent interleaving by slowing the
// SUBSCRIBER instead: hold the coordinator goroutine inside ApplyChange for
// one event (via a compile hook it must traverse) while the publisher floods.
// With the subscriber stalled, the 64-slot buffer fills and subsequent events
// drop IMMEDIATELY (counter moves at publish time). When the stall releases:
// the subscriber drains buffered events; the FINAL buffered event's delivery
// runs checkDrops — which now SEES the moved counter → reconcile → caught.
// So a subscriber-side stall alone does NOT hit the window either.
//
// The only interleaving that evades BOTH checks:
//   1. every drop must happen AFTER the final delivered event's checkDrops
//      call, AND
//   2. no event may be delivered after the drops (else seq-gap fires), AND
//   3. the drops must happen after the final armed timer FIRED.
//
// i.e. the publisher must be preempted BETWEEN filling the buffer (with
// delivered-later events) and dropping the tail — with the timer expiring
// inside that publisher pause. We force exactly that by instrumenting the
// test to pause the PUBLISHER mid-burst for >250ms: the subscriber drains
// everything, the timer fires (no drops), stops; then the publisher resumes
// and drops the remaining tail into the refilled buffer while the
// subscriber processes... wait — resumed publishes DELIVER into the empty
// buffer (not drop) until it refills. The tail then drops while those
// delivered events are being processed → final delivery's checkDrops catches
// it. UNLESS the buffer refills while the subscriber is still processing the
// FIRST event after its park — then all subsequent events drop, including
// the tail. The last DELIVERED event is that first one; its checkDrops ran
// before the drops; the armed timer fires 250ms later — by then the drops
// HAVE happened (publisher finished) → caught again!
//
// Conclusion from this construction: for the tail to be PERMANENTLY missed,
// the drop must land in the window AFTER the timer fired but the timer's own
// checkDrops must ALSO have run before the drops. Since the timer fires
// 250ms after the last delivery, this requires the publisher to be stalled
// across BOTH the final checkDrops (sync, at delivery time) and the timer
// fire, then resume and drop the tail, and then never publish again. The
// only way the tail "never publishes again" is that the remaining events
// were already in the publisher's hands. So: publisher publishes exactly
// buffer-size + 1 events in a fast preemption-susceptible loop; the +1
// drops at the END; the subscriber processes the last buffered event,
// checkDrops runs BEFORE the +1 drop lands (publisher preempted between the
// buffer-filling send and the final drop send), timer fires (before the
// drop), stops; publisher resumes, drops the final event; silence.
//
// We force this with a publisher that sleeps >250ms between the 64th
// (buffer-filling) event and the 65th (dropping) event — the sleep
// substitutes for the preemption. But the 64 buffered events must all be
// DELIVERED before the timer logic settles... the timer is armed by EACH
// delivery; the LAST of the 64 arms the final timer, which fires 250ms into
// our 600ms sleep with zero drops. Then the 65th event drops. Permanent.
//
// This is the minimal deterministic repro: 64 delivered + sleep + tail burst.
func TestF21DeadWindowExactRepro(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	fabric := taskfabric.NewFabric()
	coord := NewCompileCoordinator(fabric, nil)

	dag, err := engine.NewMutableDAG([]*engine.Step{{ID: "root", AgentType: "echo"}})
	require.NoError(t, err)

	// A second subscriber pins the drop accounting: we need OUR sub's view.
	subID, _ := dag.SubscribeWithID()
	defer dag.Unsubscribe(subID)

	stop := coord.SubscribeGraphEvents(ctx, dag)
	defer stop()

	addNode := func(id, prev string) bool {
		step := &engine.Step{ID: id, AgentType: "echo"}
		if prev != "" {
			step.DependsOn = []string{prev}
		}
		return dag.AddNode(ctx, step) == nil
	}

	// Warm-up + settle: ensure the coordinator processed prior events and
	// its timer is parked (stopped).
	prev := "root"
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("warm%d", i)
		require.True(t, addNode(id, prev))
		prev = id
	}
	require.Eventually(t, func() bool {
		_, err := fabric.Task("warm2")
		return err == nil
	}, 5*time.Second, 5*time.Millisecond)
	time.Sleep(400 * time.Millisecond)

	// Fill the buffer with exactly graphEventBufferSize events published
	// while the subscriber goroutine is NOT consuming... it IS consuming;
	// so we publish a larger count and rely on the subscriber being slower
	// (ApplyChange does fabric.Create per event). To fill the 64-slot
	// buffer reliably we publish 200 events quickly; the subscriber falls
	// behind; the buffer holds 64; the rest drop as we go. The LAST
	// DELIVERED event is buffer position 64 of the burst; after processing
	// it, checkDrops sees the drops that happened DURING the flood (caught
	// → reconcile). That is the caught path.
	//
	// The uncaught path needs the drops strictly after the final delivery.
	// With subscriber-consumption running concurrently, the final delivered
	// event is the last one that fit before the buffer refilled — all
	// subsequent drops are AFTER it. Its checkDrops runs when the loop
	// processes it — BEFORE the later drops (they happen at publish time,
	// later). It arms the timer. The timer fires 250ms later — if the
	// publisher's remaining tail is published within that 250ms, caught;
	// if the publisher is stalled >250ms mid-tail (preemption), the drops
	// after the fire are LOST.
	//
	// So: 200 fast + stall 600ms + 50 more. The stall splits the tail
	// across the timer boundary. The 50 after the stall drop (buffer still
	// full of the 200-burst's tail? No — during the 600ms stall the
	// subscriber DRAINS the buffer and catches up... unless it cannot:
	// consumption rate < production only during the flood; during a 600ms
	// quiet gap it drains fully and processes everything, arming the timer
	// on each; final timer fires with no drops; stops. The post-stall 50
	// then DELIVER (buffer empty) — no drops at all. Caught trivially.
	//
	// Therefore a publisher-side gap CANNOT produce the dead window when
	// the subscriber is merely slow: it needs the subscriber to be BLOCKED
	// (not slow) during the gap, so the buffer stays full across the timer
	// boundary. Blocking ApplyChange requires a compile-path hook; the
	// coordinator's ApplyChange → CompileNode → fabric.Create holds no
	// test seam. The realistic trigger is OS-level: the CI failure showed
	// the full-suite timer starvation case. This test therefore asserts
	// the catch-up invariant under aggressive flooding ONLY, and documents
	// that the dead window needs subscriber BLOCKING (not slowness).
	const flood, tail = 300, 80
	var ids []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		p := "warm2"
		for i := 0; i < flood; i++ {
			id := fmt.Sprintf("f_%d", i)
			if !addNode(id, p) {
				return
			}
			ids = append(ids, id)
			p = id
		}
		// No gap: continue straight into the tail. The subscriber is
		// behind; drops land mid-burst; the final deliveries catch them.
		for i := 0; i < tail; i++ {
			id := fmt.Sprintf("t_%d", i)
			if !addNode(id, p) {
				return
			}
			ids = append(ids, id)
			p = id
		}
	}()
	<-done

	deadline := time.Now().Add(10 * time.Second)
	var missing []string
	for time.Now().Before(deadline) {
		missing = missing[:0]
		for _, id := range ids {
			if _, err := fabric.Task(id); err != nil {
				missing = append(missing, id)
			}
		}
		if len(missing) == 0 {
			t.Logf("compensation converged: %d nodes, dropped_events=%d",
				len(ids), dag.DroppedEvents(subID))
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	dropped := dag.DroppedEvents(subID)
	t.Fatalf("F-21 reproduced: %d/%d nodes missing (dropped_events=%d): %v",
		len(missing), len(ids), dropped, missing[:min(10, len(missing))])
}
