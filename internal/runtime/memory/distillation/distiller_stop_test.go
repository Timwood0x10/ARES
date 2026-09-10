// distiller_stop_test.go locks REVIEW 3.3#9: SubscribeAndDistill's
// errgroup had no Wait caller, so the subscription goroutine was never
// joined at shutdown. Stop cancels the derived subscription context and
// waits for the goroutine to drain.
package distillation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

func TestSubscribeAndDistillStopJoinsLoop(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	d := NewDistiller(nil, NewMockEmbeddingService(), NewMockExperienceRepository(nil))
	d.OnMessageAdded = func(ctx context.Context, streamID, role string) {}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.SubscribeAndDistill(ctx, store)

	// Stop must cancel the subscription loop and return; the loop only
	// exits via the derived context (the memory store keeps the channel
	// open until its own subscriber ctx is cancelled, which happens through
	// the same derived context).
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not join the subscription goroutine within 3s")
	}

	// A second Stop must be a safe no-op.
	second := make(chan struct{})
	go func() {
		d.Stop()
		close(second)
	}()
	select {
	case <-second:
	case <-time.After(3 * time.Second):
		t.Fatal("second Stop did not return")
	}
}

func TestSubscribeAndDistillStopWithoutSubscription(t *testing.T) {
	d := NewDistiller(nil, NewMockEmbeddingService(), NewMockExperienceRepository(nil))
	require.NotPanics(t, func() { d.Stop() })
}
