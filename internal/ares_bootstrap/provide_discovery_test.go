package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/discovery"
)

// TestForwardDiscoveryEvent_MapsTypesAndPayload verifies the discovery→
// EventStore bridge: every engine event type lands on the "discovery" stream
// with its mapped ares_events type and carries service identity payload.
func TestForwardDiscoveryEvent_MapsTypesAndPayload(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	ctx := context.Background()

	cases := []struct {
		in   discovery.EventType
		want ares_events.EventType
	}{
		{discovery.EventServiceAdded, ares_events.EventDiscoveryServiceAdded},
		{discovery.EventServiceRemoved, ares_events.EventDiscoveryServiceRemoved},
		{discovery.EventHealthChanged, ares_events.EventDiscoveryHealthChanged},
		{discovery.EventType("unknown.kind"), ares_events.EventDiscoveryCycleCompleted},
	}
	for _, tc := range cases {
		forwardDiscoveryEvent(ctx, store, discovery.Event{
			Type:      tc.in,
			ServiceID: "svc-1",
			Source:    "test",
			Service: &discovery.DiscoveredService{
				Identity: discovery.ServiceIdentity{ID: "svc-1", Name: "demo", Endpoint: ":5001"},
			},
		})
	}

	// Drain the stream and verify all events arrived mapped + populated.
	got := map[ares_events.EventType]bool{}
	until := time.Now().Add(2 * time.Second)
	for len(got) < len(cases) && time.Now().Before(until) {
		events, err := store.Read(ctx, "discovery", ares_events.ReadOptions{Limit: 100})
		if err != nil {
			t.Fatalf("read stream: %v", err)
		}
		for _, e := range events {
			got[e.Type] = true
			if e.Type == ares_events.EventDiscoveryServiceAdded {
				require.Equal(t, "demo", e.Payload["name"])
				require.Equal(t, ":5001", e.Payload["endpoint"])
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, tc := range cases {
		require.True(t, got[tc.want], "expected %s to be forwarded", tc.want)
	}
}

// TestProvideDiscovery_EnabledBridgesEvents verifies that an enabled config
// yields a running engine whose findings reach the EventStore (the #10
// consumer loop). Uses a short interval so one cycle completes quickly.
func TestProvideDiscovery_EnabledBridgesEvents(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	cfg := &ares_config.DiscoveryConfig{Enabled: true, Interval: 20 * time.Millisecond}
	compDisc, err := ProvideDiscovery(context.Background(), cfg, store)
	require.NoError(t, err)
	require.NotNil(t, compDisc)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, readErr := store.Read(context.Background(), "discovery", ares_events.ReadOptions{Limit: 100})
		if readErr == nil && len(events) > 0 {
			return // cycle-complete event forwarded: bridge is live.
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no discovery events reached the event store within deadline")
}

// TestProvideDiscovery_Disabled verifies the disabled contract is preserved.
func TestProvideDiscovery_Disabled(t *testing.T) {
	_, err := ProvideDiscovery(context.Background(), &ares_config.DiscoveryConfig{}, nil)
	require.ErrorIs(t, err, ErrDiscoveryDisabled)
}
