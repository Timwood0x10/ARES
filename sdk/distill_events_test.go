package sdk

import (
	"context"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"

	ares_bootstrap "github.com/Timwood0x10/ares/internal/ares_bootstrap"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
)

// shutdownTimeout bounds the wait for background goroutines so a deadlock in
// the subscriber lifecycle surfaces as a test failure rather than hanging the
// suite.
const shutdownTimeout = 3 * time.Second

// TestNewEventBackend exercises the event backend factory. With a nil
// distillSvc the subscriber is skipped (no goroutine registered with the
// errgroup), so cancel + eg.Wait must return immediately. The returned
// context, cancel, errgroup, and store must all be non-nil.
func TestNewEventBackend(t *testing.T) {
	t.Run("nil_distillSvc_skips_subscriber_clean_teardown", func(t *testing.T) {
		ctx, cancel, eg, store := newEventBackend(nil, nil)
		if ctx == nil {
			t.Fatal("expected non-nil ctx")
		}
		if cancel == nil {
			t.Fatal("expected non-nil cancel")
		}
		if eg == nil {
			t.Fatal("expected non-nil errgroup")
		}
		if store == nil {
			t.Fatal("expected non-nil event store")
		}

		// No subscriber was wired, so eg.Wait must complete immediately.
		cancel()
		waitOrTimeout(t, eg, shutdownTimeout)
	})

	t.Run("store_is_usable_for_append", func(t *testing.T) {
		// The returned store must be a working EventStore: appending an event
		// must succeed. This guards against newEventBackend returning a nil
		// wrapper by mistake.
		ctx, cancel, eg, store := newEventBackend(nil, nil)
		defer cancel()
		defer waitOrTimeout(t, eg, shutdownTimeout)

		err := store.Append(ctx, "stream-1", []*ares_events.Event{
			{Type: ares_events.EventTaskCompleted, Payload: map[string]any{"task": "x"}},
		}, 0)
		if err != nil {
			t.Fatalf("Append error: %v", err)
		}
	})
}

// TestWireDistillationSubscriber verifies the subscriber lifecycle: it
// subscribes to the store, runs under the errgroup, and exits cleanly on
// context cancellation.
//
// distSvc is nil in these tests because no events are emitted, so
// HandleTaskCompletedForDistillation is never invoked and distSvc is never
// dereferenced. This is a lifecycle-only test; the distSvc-dependent path is
// covered by TestHandleTaskCompletedForDistillation below and by the
// integration suite (which requires a live LLM).
func TestWireDistillationSubscriber(t *testing.T) {
	t.Run("starts_and_stops_cleanly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		eg := &errgroup.Group{}
		store := ares_events.NewMemoryEventStore()
		defer func() { _ = store.Close() }()

		// nil distSvc is safe: with no events emitted the goroutine never
		// reaches HandleTaskCompletedForDistillation.
		wireDistillationSubscriber(ctx, eg, store, nil, nil)

		// Cancel and wait. The subscriber goroutine must exit via ctx.Done()
		// and eg.Wait must return nil within the timeout.
		cancel()
		waitOrTimeout(t, eg, shutdownTimeout)
	})

	t.Run("cancel_stops_subscriber", func(t *testing.T) {
		// Independently verify the cancel-then-wait ordering produces a clean
		// shutdown without a hard deadline on the goroutine itself.
		ctx, cancel := context.WithCancel(context.Background())
		eg := &errgroup.Group{}
		store := ares_events.NewMemoryEventStore()
		defer func() { _ = store.Close() }()

		wireDistillationSubscriber(ctx, eg, store, nil, nil)

		cancel()
		waitOrTimeout(t, eg, shutdownTimeout)
	})
}

// TestHandleTaskCompletedForDistillation exercises the content-length and
// tenant guards in the exported handler. Each guard returns BEFORE calling
// svc.Distill, so a nil DistillationService is safe and does not need to be
// constructed (which would require a live LLM). This is the contract the SDK
// subscriber relies on: bad events are dropped without touching the service.
func TestHandleTaskCompletedForDistillation(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		// evType is the event type used for the test event; the handler reads
		// it to set Success but the guards fire before that matters.
		evType ares_events.EventType
	}{
		{
			// task text shorter than 10 chars → early return.
			name:   "skips_short_task",
			evType: ares_events.EventTaskCompleted,
			payload: map[string]any{
				ares_events.EventKeyTask:     "short",
				ares_events.EventKeyResult:   "a sufficiently long result text",
				ares_events.EventKeyTenantID: "tenant-1",
			},
		},
		{
			// result text shorter than 20 chars → early return.
			name:   "skips_short_result",
			evType: ares_events.EventTaskCompleted,
			payload: map[string]any{
				ares_events.EventKeyTask:     "a long enough task",
				ares_events.EventKeyResult:   "too short",
				ares_events.EventKeyTenantID: "tenant-1",
			},
		},
		{
			// empty tenantID → early return.
			name:   "skips_empty_tenant",
			evType: ares_events.EventTaskCompleted,
			payload: map[string]any{
				ares_events.EventKeyTask:     "a long enough task",
				ares_events.EventKeyResult:   "a sufficiently long result text",
				ares_events.EventKeyTenantID: "",
			},
		},
		{
			// missing tenant key entirely → stringField returns "" → early return.
			name:   "skips_missing_tenant",
			evType: ares_events.EventTaskFailed,
			payload: map[string]any{
				ares_events.EventKeyTask:   "a long enough task",
				ares_events.EventKeyResult: "a sufficiently long result text",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &ares_events.Event{
				Type:    tt.evType,
				Payload: tt.payload,
			}
			// nil svc is safe: every guard in this table returns before
			// svc.Distill is reached. If the guard regresses, this test
			// would panic on the nil dereference, surfacing the bug.
			ares_bootstrap.HandleTaskCompletedForDistillation(
				context.Background(), nil, ev,
			)
		})
	}
}

// waitOrTimeout waits for the errgroup to finish within the timeout and fails
// the test if it does not (the subscriber goroutine failed to exit on cancel).
func waitOrTimeout(t *testing.T, eg *errgroup.Group, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- eg.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("eg.Wait error = %v, want nil", err)
		}
	case <-time.After(timeout):
		t.Fatalf("errgroup did not finish within %v", timeout)
	}
}
