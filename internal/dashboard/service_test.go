package dashboard

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// mockEventStore implements ares_events.EventStore for testing.
type mockEventStore struct {
	ares_events []*ares_events.Event
}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []*ares_events.Event, _ int64) error {
	return nil
}

func (m *mockEventStore) Read(_ context.Context, _ string, _ ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return m.ares_events, nil
}

func (m *mockEventStore) ReadAll(_ context.Context, _ ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return m.ares_events, nil
}

func (m *mockEventStore) Subscribe(_ context.Context, _ ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	ch := make(chan *ares_events.Event, len(m.ares_events))
	for _, e := range m.ares_events {
		ch <- e
	}
	return ch, nil
}

func (m *mockEventStore) StreamVersion(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

// Ensure mockEventStore satisfies the EventStore interface at compile time.
var _ ares_events.EventStore = (*mockEventStore)(nil)
