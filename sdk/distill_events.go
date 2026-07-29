package sdk

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"

	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
)

// newEventBackend creates the Runtime lifecycle context, errgroup, and in-memory
// event store, and — when distillSvc is non-nil — registers a background
// subscriber that distills TaskCompleted/TaskFailed events into long-term
// experiences. Bundling these together keeps New() under the 100-line limit:
// the context, errgroup, store, and subscriber are all facets of the same
// "event/lifecycle backend" owned by the Runtime.
//
// The returned context is cancelled by Runtime.Close to stop the subscriber
// goroutine cleanly. The errgroup lets Close wait for in-flight distillation
// work before releasing other resources.
//
// Args:
//
//	distillSvc - distillation service; nil disables the subscriber (store is still returned).
//
// Returns:
//
//	ctx    - lifecycle context for background goroutines; cancelled by the returned cancel.
//	cancel - cancels ctx.
//	eg     - errgroup tracking the subscriber goroutine for clean shutdown.
//	store  - the in-memory event store shared by emitters (Agent.Run) and the subscriber.
func newEventBackend(distillSvc *aresexp.DistillationService) (
	context.Context, context.CancelFunc, *errgroup.Group, ares_events.EventStore,
) {
	ctx, cancel := context.WithCancel(context.Background())
	eg := &errgroup.Group{}
	store := ares_events.NewMemoryEventStore()
	if distillSvc != nil {
		wireDistillationSubscriber(ctx, eg, store, distillSvc)
	}
	return ctx, cancel, eg, store
}

// wireDistillationSubscriber registers a background consumer of TaskCompleted
// and TaskFailed events that feeds each completed task into the
// DistillationService so conversations are distilled into long-term experiences
// automatically. The goroutine runs under the Runtime's errgroup and exits when
// ctx is cancelled (typically in Runtime.Close) or when the event store closes
// the subscription channel. Errors during distillation are logged by
// HandleTaskCompletedForDistillation and do not stop the subscriber, so a single
// bad event cannot starve the distillation loop.
//
// Subscribe failures are non-fatal: a warning is logged and no subscriber is
// registered, leaving the Runtime running without event-driven distillation.
//
// Args:
//
//	ctx     - lifecycle context; cancellation stops the subscriber.
//	eg      - errgroup tracking the subscriber goroutine for clean shutdown.
//	store   - the EventStore to subscribe to; must be non-nil.
//	distSvc - the distillation service that consumes each event; must be non-nil.
func wireDistillationSubscriber(
	ctx context.Context,
	eg *errgroup.Group,
	store ares_events.EventStore,
	distSvc *aresexp.DistillationService,
) {
	// EventFilter.Types restricts the subscription to the two lifecycle events
	// the distillation loop cares about. Confirmed against
	// internal/ares_events/types.go (EventFilter.Types []EventType).
	filter := ares_events.EventFilter{
		Types: []ares_events.EventType{
			ares_events.EventTaskCompleted,
			ares_events.EventTaskFailed,
		},
	}
	ch, err := store.Subscribe(ctx, filter)
	if err != nil {
		slog.Warn("sdk: distillation subscriber failed to subscribe; event-driven distillation disabled",
			"error", err)
		return
	}
	eg.Go(func() error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case ev, ok := <-ch:
				if !ok {
					// Store closed the channel (ctx cancellation also closes it
					// via MemoryEventStore.unsubscribe); exit cleanly.
					return nil
				}
				ares_bootstrap.HandleTaskCompletedForDistillation(ctx, distSvc, ev)
			}
		}
	})
	slog.Info("sdk: event-driven distillation subscriber started")
}
