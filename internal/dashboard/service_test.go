package dashboard

import (
	"context"

	"goagentx/internal/events"
)

// mockEventStore implements events.EventStore for testing.
type mockEventStore struct {
	events []*events.Event
}

func (m *mockEventStore) Append(_ context.Context, _ string, _ []*events.Event, _ int64) error {
	return nil
}

func (m *mockEventStore) Read(_ context.Context, _ string, _ events.ReadOptions) ([]*events.Event, error) {
	return m.events, nil
}

func (m *mockEventStore) ReadAll(_ context.Context, _ events.ReadOptions) ([]*events.Event, error) {
	return m.events, nil
}

func (m *mockEventStore) Subscribe(_ context.Context, _ events.EventFilter) (<-chan *events.Event, error) {
	ch := make(chan *events.Event, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	return ch, nil
}

func (m *mockEventStore) StreamVersion(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

// Ensure mockEventStore satisfies the EventStore interface at compile time.
var _ events.EventStore = (*mockEventStore)(nil)
