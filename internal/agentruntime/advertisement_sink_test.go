package agentruntime

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// recordStore is a minimal EventStore that keeps what was appended.
type recordStore struct {
	mu      sync.Mutex
	streams map[string][]*ares_events.Event
}

func (s *recordStore) Append(_ context.Context, streamID string, events []*ares_events.Event, _ int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams == nil {
		s.streams = map[string][]*ares_events.Event{}
	}
	s.streams[streamID] = append(s.streams[streamID], events...)
	return nil
}
func (s *recordStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *recordStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (s *recordStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (s *recordStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

// TestAdvertisementSink_RecordsWhatTheModelSaw is the P2 "into events" half: the advertisement
// used to exist only in a log line, which cannot answer "why did this run see these tools" once
// the log window has rolled over.
func TestAdvertisementSink_RecordsWhatTheModelSaw(t *testing.T) {
	store := &recordStore{}
	sink := advertisementSinkFor(store)
	require.NotNil(t, sink)

	sink.OnToolAdvertisement(context.Background(), "sess-1", "task-1", []string{"network"}, 2, 40)

	require.Len(t, store.streams["sess-1"], 1)
	ev := store.streams["sess-1"][0]
	assert.Equal(t, ares_events.EventType("tool.advertisement"), ev.Type)
	assert.Equal(t, []string{"network"}, ev.Payload["caps"])
	assert.Equal(t, 2, ev.Payload["advertised"])
	assert.Equal(t, 40, ev.Payload["total"])

	// A session-less task still lands somewhere: the task id is the fallback stream.
	sink.OnToolAdvertisement(context.Background(), "", "task-2", nil, 40, 40)
	require.Len(t, store.streams["task-2"], 1)

	// No store means no sink, which is what keeps the feature inert by default.
	assert.Nil(t, advertisementSinkFor(nil))
}
